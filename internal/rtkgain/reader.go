package rtkgain

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Summary struct {
	TotalCommands int64   `json:"total_commands"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	SavedTokens   int64   `json:"saved_tokens"`
	SavingsPct    float64 `json:"savings_pct"`
	TotalTimeMs   int64   `json:"total_time_ms"`
	Available     bool    `json:"available"`
}

type TimePoint struct {
	Date        string  `json:"date"`
	Commands    int64   `json:"commands"`
	SavedTokens int64   `json:"saved_tokens"`
	SavingsPct  float64 `json:"savings_pct"`
	TotalTimeMs int64   `json:"total_time_ms"`
}

type CommandStat struct {
	Rank        int     `json:"rank"`
	Command     string  `json:"command"`
	Count       int64   `json:"count"`
	SavedTokens int64   `json:"saved_tokens"`
	SavingsPct  float64 `json:"savings_pct"`
	TotalTimeMs int64   `json:"total_time_ms"`
	ImpactPct   float64 `json:"impact_pct"`
}

type Reader struct {
	db               *sql.DB
	mu               sync.Mutex
	lastFetch        time.Time
	cacheTTL         time.Duration
	cachedSummary    *Summary
	cachedTimeseries []TimePoint
	cachedCommands   []CommandStat
}

func New(dbPath string) (*Reader, error) {
	if dbPath == "" {
		return nil, nil
	}

	db, err := sql.Open("sqlite", "file:"+dbPath+"?_busy_timeout=5000&_journal_mode=DELETE")
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	log.Printf("rtkgain: opened RTK database at %s", dbPath)
	return &Reader{
		db:       db,
		cacheTTL: 30 * time.Second,
	}, nil
}

func (r *Reader) IsAvailable() bool {
	return r != nil && r.db != nil
}

func (r *Reader) Close() {
	if r != nil && r.db != nil {
		r.db.Close()
	}
}

func (r *Reader) Summary(ctx context.Context) (*Summary, error) {
	if !r.IsAvailable() {
		return &Summary{Available: false}, nil
	}

	r.mu.Lock()
	if r.cachedSummary != nil && time.Since(r.lastFetch) < r.cacheTTL {
		s := r.cachedSummary
		r.mu.Unlock()
		return s, nil
	}
	r.mu.Unlock()

	var s Summary
	row := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(saved_tokens), 0),
			CASE WHEN SUM(saved_tokens) + SUM(output_tokens) > 0
				THEN CAST(SUM(saved_tokens) AS REAL) / (SUM(saved_tokens) + SUM(output_tokens)) * 100
				ELSE 0.0
			END,
			COALESCE(SUM(exec_time_ms), 0)
		FROM commands
	`)
	if err := row.Scan(&s.TotalCommands, &s.InputTokens, &s.OutputTokens, &s.SavedTokens, &s.SavingsPct, &s.TotalTimeMs); err != nil {
		log.Printf("rtkgain: summary query failed: %v", err)
		return &Summary{Available: false}, nil
	}
	s.Available = true

	r.mu.Lock()
	r.cachedSummary = &s
	r.lastFetch = time.Now()
	r.mu.Unlock()

	return &s, nil
}

func (r *Reader) TimeSeries(ctx context.Context, bucket string, from, to *time.Time, command string) ([]TimePoint, error) {
	if !r.IsAvailable() {
		return []TimePoint{}, nil
	}

	// Only use cache for unfiltered queries.
	if command == "" {
		r.mu.Lock()
		if r.cachedTimeseries != nil && time.Since(r.lastFetch) < r.cacheTTL {
			ts := r.cachedTimeseries
			r.mu.Unlock()
			return ts, nil
		}
		r.mu.Unlock()
	}

	groupExpr := "DATE(timestamp)"
	if bucket == "hour" {
		groupExpr = "strftime('%Y-%m-%dT%H:00:00', timestamp)"
	}

	query := `
		SELECT
			` + groupExpr + ` AS period,
			COUNT(*) AS commands,
			COALESCE(SUM(saved_tokens), 0) AS saved_tokens,
			CASE WHEN SUM(saved_tokens) + SUM(output_tokens) > 0
				THEN CAST(SUM(saved_tokens) AS REAL) / (SUM(saved_tokens) + SUM(output_tokens)) * 100
				ELSE 0.0
			END AS savings_pct,
			COALESCE(SUM(exec_time_ms), 0) AS total_time_ms
		FROM commands
		WHERE 1=1
	`
	args := []any{}
	if command != "" {
		query += " AND original_cmd = ?"
		args = append(args, command)
	}
	if from != nil {
		query += " AND timestamp >= ?"
		args = append(args, from.Format(time.RFC3339))
	}
	if to != nil {
		query += " AND timestamp <= ?"
		args = append(args, to.Format(time.RFC3339))
	}
	query += " GROUP BY period ORDER BY period"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("rtkgain: timeseries query failed: %v", err)
		return []TimePoint{}, nil
	}
	defer rows.Close()

	var points []TimePoint
	for rows.Next() {
		var p TimePoint
		if err := rows.Scan(&p.Date, &p.Commands, &p.SavedTokens, &p.SavingsPct, &p.TotalTimeMs); err != nil {
			continue
		}
		points = append(points, p)
	}

	if command == "" {
		r.mu.Lock()
		r.cachedTimeseries = points
		r.lastFetch = time.Now()
		r.mu.Unlock()
	}

	return points, nil
}

func (r *Reader) Commands(ctx context.Context, limit int, date string) ([]CommandStat, error) {
	if !r.IsAvailable() {
		return []CommandStat{}, nil
	}

	// Only cache the all-time (unfiltered) query.
	if date == "" {
		r.mu.Lock()
		if r.cachedCommands != nil && time.Since(r.lastFetch) < r.cacheTTL {
			cmds := r.cachedCommands
			r.mu.Unlock()
			return cmds, nil
		}
		r.mu.Unlock()
	}

	if limit <= 0 {
		limit = 10
	}

	query := `
		SELECT
			original_cmd,
			COUNT(*) AS count,
			COALESCE(SUM(saved_tokens), 0) AS saved_tokens,
			CASE WHEN SUM(saved_tokens) + SUM(output_tokens) > 0
				THEN CAST(SUM(saved_tokens) AS REAL) / (SUM(saved_tokens) + SUM(output_tokens)) * 100
				ELSE 0.0
			END AS savings_pct,
			COALESCE(SUM(exec_time_ms), 0) AS total_time_ms
		FROM commands
		WHERE 1=1
	`
	args := []any{}
	if date != "" {
		query += " AND DATE(timestamp) = ?"
		args = append(args, date)
	}
	query += " GROUP BY original_cmd ORDER BY saved_tokens DESC LIMIT ?"
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("rtkgain: commands query failed: %v", err)
		return []CommandStat{}, nil
	}
	defer rows.Close()

	var totalSaved int64
	var raw []CommandStat
	for rows.Next() {
		var c CommandStat
		if err := rows.Scan(&c.Command, &c.Count, &c.SavedTokens, &c.SavingsPct, &c.TotalTimeMs); err != nil {
			continue
		}
		totalSaved += c.SavedTokens
		raw = append(raw, c)
	}

	for i := range raw {
		raw[i].Rank = i + 1
		if totalSaved > 0 {
			raw[i].ImpactPct = float64(raw[i].SavedTokens) / float64(totalSaved) * 100
		}
	}

	if date == "" {
		r.mu.Lock()
		r.cachedCommands = raw
		r.lastFetch = time.Now()
		r.mu.Unlock()
	}

	return raw, nil
}

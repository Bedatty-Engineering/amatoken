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
}

type Reader struct {
	db               *sql.DB
	mu               sync.Mutex
	lastFetch        time.Time
	cacheTTL         time.Duration
	cachedSummary    *Summary
	cachedTimeseries []TimePoint
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

func (r *Reader) TimeSeries(ctx context.Context, bucket string, from, to *time.Time) ([]TimePoint, error) {
	if !r.IsAvailable() {
		return []TimePoint{}, nil
	}

	r.mu.Lock()
	if r.cachedTimeseries != nil && time.Since(r.lastFetch) < r.cacheTTL {
		ts := r.cachedTimeseries
		r.mu.Unlock()
		return ts, nil
	}
	r.mu.Unlock()

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
			END AS savings_pct
		FROM commands
		WHERE 1=1
	`
	args := []any{}
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
		if err := rows.Scan(&p.Date, &p.Commands, &p.SavedTokens, &p.SavingsPct); err != nil {
			continue
		}
		points = append(points, p)
	}

	r.mu.Lock()
	r.cachedTimeseries = points
	r.lastFetch = time.Now()
	r.mu.Unlock()

	return points, nil
}

package storage

import (
	"context"
	"database/sql"
	"time"
)

// projectKeyExpr is the canonical identity for a "project" in queries.
// We prefer the per-record cwd (the actual working directory at the time of
// the message) over project_slug because Claude Code's slug is the directory
// where a session was first started — multiple subdirectories can share one
// slug, hiding spend on each subproject. Falling back to project_slug keeps
// records with no recorded cwd visible.
const projectKeyExpr = `COALESCE(NULLIF(cwd, ''), project_slug)`

// modernc.org/sqlite returns DATETIME columns as strings. parseTime accepts
// the formats Go's sql driver may emit when binding time.Time, plus a couple
// of SQLite-native variants.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

func parseTime(s string) time.Time {
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

type Repo struct{ DB *sql.DB }

func New(db *sql.DB) *Repo { return &Repo{DB: db} }

type UsageRecord struct {
	MessageID           string
	RequestID           string
	SessionID           string
	ProjectSlug         string
	Cwd                 string
	GitBranch           string
	Model               string
	Timestamp           time.Time
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	SourceFile          string
	SourceLine          int64
}

func (r *Repo) InsertUsage(ctx context.Context, u *UsageRecord) error {
	_, err := r.DB.ExecContext(ctx, `INSERT OR IGNORE INTO usage_records
		(message_id, request_id, session_id, project_slug, cwd, git_branch, model, ts,
		 input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, source_file, source_line)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.MessageID, nullStr(u.RequestID), u.SessionID, u.ProjectSlug, nullStr(u.Cwd), nullStr(u.GitBranch),
		u.Model, u.Timestamp.UTC().Format(time.RFC3339Nano), u.InputTokens, u.OutputTokens, u.CacheCreationTokens, u.CacheReadTokens,
		u.SourceFile, u.SourceLine)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type IngestState struct {
	ByteOffset int64
	LastLine   int64
}

func (r *Repo) GetIngestState(ctx context.Context, file string) (IngestState, error) {
	var s IngestState
	err := r.DB.QueryRowContext(ctx, `SELECT byte_offset, last_line FROM ingest_state WHERE source_file = ?`, file).
		Scan(&s.ByteOffset, &s.LastLine)
	if err == sql.ErrNoRows {
		return IngestState{}, nil
	}
	return s, err
}

func (r *Repo) SetIngestState(ctx context.Context, file string, s IngestState) error {
	_, err := r.DB.ExecContext(ctx, `INSERT INTO ingest_state(source_file, byte_offset, last_line, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(source_file) DO UPDATE SET byte_offset=excluded.byte_offset, last_line=excluded.last_line, updated_at=excluded.updated_at`,
		file, s.ByteOffset, s.LastLine, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

type Filters struct {
	From, To *time.Time
	Project  string
	Model    string
	Search   string // free-text match across project_slug, cwd, git_branch, model, session_id
}

func (f Filters) where() (string, []any) {
	// Synthetic messages are Claude Code internal events (context compaction
	// etc.) with no real cost — always excluded.
	q := " WHERE model != '<synthetic>'"
	var args []any
	if f.From != nil {
		q += " AND ts >= ?"
		args = append(args, f.From.UTC().Format(time.RFC3339Nano))
	}
	if f.To != nil {
		q += " AND ts < ?"
		args = append(args, f.To.UTC().Format(time.RFC3339Nano))
	}
	if f.Project != "" {
		q += " AND " + projectKeyExpr + " = ?"
		args = append(args, f.Project)
	}
	if f.Model != "" {
		q += " AND model = ?"
		args = append(args, f.Model)
	}
	if f.Search != "" {
		q += " AND (project_slug LIKE ? OR COALESCE(cwd,'') LIKE ? OR COALESCE(git_branch,'') LIKE ? OR model LIKE ? OR session_id LIKE ?)"
		needle := "%" + f.Search + "%"
		args = append(args, needle, needle, needle, needle, needle)
	}
	return q, args
}

type Summary struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	Sessions            int64 `json:"sessions"`
	Messages            int64 `json:"messages"`
	CostUSD             float64 `json:"cost_usd"`
}

func (r *Repo) Summary(ctx context.Context, f Filters) (Summary, error) {
	w, args := f.where()
	var s Summary
	err := r.DB.QueryRowContext(ctx, `SELECT
			COALESCE(SUM(input_tokens),0),
			COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(cache_creation_tokens),0),
			COALESCE(SUM(cache_read_tokens),0),
			COUNT(DISTINCT session_id),
			COUNT(*)
		FROM usage_records`+w, args...).
		Scan(&s.InputTokens, &s.OutputTokens, &s.CacheCreationTokens, &s.CacheReadTokens, &s.Sessions, &s.Messages)
	return s, err
}

type ModelTotals struct {
	Model               string `json:"model"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	Messages            int64  `json:"messages"`
}

func (r *Repo) TotalsByModel(ctx context.Context, f Filters) ([]ModelTotals, error) {
	w, args := f.where()
	rows, err := r.DB.QueryContext(ctx, `SELECT model,
			COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
			COUNT(*)
		FROM usage_records`+w+` GROUP BY model ORDER BY model`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelTotals
	for rows.Next() {
		var m ModelTotals
		if err := rows.Scan(&m.Model, &m.InputTokens, &m.OutputTokens, &m.CacheCreationTokens, &m.CacheReadTokens, &m.Messages); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type TimePoint struct {
	Bucket              time.Time `json:"bucket"`
	InputTokens         int64     `json:"input_tokens"`
	OutputTokens        int64     `json:"output_tokens"`
	CacheCreationTokens int64     `json:"cache_creation_tokens"`
	CacheReadTokens     int64     `json:"cache_read_tokens"`
	CostUSD             float64   `json:"cost_usd"`
}

// TimeSeriesByModel mirrors TimeSeries but breaks down by model so the caller
// can compute cost per bucket using the active rate sheet.
type TimeSeriesByModelPoint struct {
	BucketKey           string
	Model               string
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
}

func (r *Repo) TimeSeriesByModel(ctx context.Context, f Filters, bucket string) ([]TimeSeriesByModelPoint, error) {
	fmtStr := "%Y-%m-%d"
	if bucket == "hour" {
		fmtStr = "%Y-%m-%d %H:00:00"
	}
	w, args := f.where()
	q := `SELECT strftime('` + fmtStr + `', datetime(ts)) AS b, model,
			COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0)
		FROM usage_records` + w + ` GROUP BY b, model HAVING b IS NOT NULL ORDER BY b, model`
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TimeSeriesByModelPoint
	for rows.Next() {
		var p TimeSeriesByModelPoint
		var b sql.NullString
		if err := rows.Scan(&b, &p.Model, &p.InputTokens, &p.OutputTokens, &p.CacheCreationTokens, &p.CacheReadTokens); err != nil {
			return nil, err
		}
		if !b.Valid {
			continue
		}
		p.BucketKey = b.String
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) TimeSeries(ctx context.Context, f Filters, bucket string) ([]TimePoint, error) {
	fmtStr := "%Y-%m-%d"
	if bucket == "hour" {
		fmtStr = "%Y-%m-%d %H:00:00"
	}
	w, args := f.where()
	// Wrap ts with datetime() so strftime works regardless of how Go's sql
	// driver formatted the timestamp on insert (timezone-aware strings break
	// strftime and silently return NULL).
	q := `SELECT strftime('` + fmtStr + `', datetime(ts)) AS b,
			COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0)
		FROM usage_records` + w + ` GROUP BY b HAVING b IS NOT NULL ORDER BY b`
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TimePoint
	layout := "2006-01-02"
	if bucket == "hour" {
		layout = "2006-01-02 15:04:05"
	}
	for rows.Next() {
		var b sql.NullString
		var p TimePoint
		if err := rows.Scan(&b, &p.InputTokens, &p.OutputTokens, &p.CacheCreationTokens, &p.CacheReadTokens); err != nil {
			return nil, err
		}
		if !b.Valid {
			continue
		}
		t, _ := time.Parse(layout, b.String)
		p.Bucket = t
		out = append(out, p)
	}
	return out, rows.Err()
}

type SessionRow struct {
	SessionID           string    `json:"session_id"`
	ProjectSlug         string    `json:"project_slug"`
	Cwd                 string    `json:"cwd"`
	GitBranch           string    `json:"git_branch"`
	Models              string    `json:"models"`
	FirstSeen           time.Time `json:"first_seen"`
	LastSeen            time.Time `json:"last_seen"`
	InputTokens         int64     `json:"input_tokens"`
	OutputTokens        int64     `json:"output_tokens"`
	CacheCreationTokens int64     `json:"cache_creation_tokens"`
	CacheReadTokens     int64     `json:"cache_read_tokens"`
	Messages            int64     `json:"messages"`
}

func (r *Repo) CountSessions(ctx context.Context, f Filters) (int64, error) {
	w, args := f.where()
	var n int64
	err := r.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM (SELECT 1 FROM usage_records`+w+` GROUP BY session_id, project_slug)`,
		args...).Scan(&n)
	return n, err
}

func (r *Repo) ListSessions(ctx context.Context, f Filters, limit, offset int) ([]SessionRow, error) {
	w, args := f.where()
	args = append(args, limit, offset)
	rows, err := r.DB.QueryContext(ctx, `SELECT session_id, project_slug,
			COALESCE(MAX(cwd), ''), COALESCE(MAX(git_branch), ''),
			GROUP_CONCAT(DISTINCT model),
			MIN(ts), MAX(ts),
			COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
			COUNT(*)
		FROM usage_records`+w+` GROUP BY session_id, project_slug
		ORDER BY MAX(ts) DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		var s SessionRow
		var models sql.NullString
		var firstSeen, lastSeen string
		if err := rows.Scan(&s.SessionID, &s.ProjectSlug, &s.Cwd, &s.GitBranch, &models,
			&firstSeen, &lastSeen, &s.InputTokens, &s.OutputTokens,
			&s.CacheCreationTokens, &s.CacheReadTokens, &s.Messages); err != nil {
			return nil, err
		}
		s.Models = models.String
		s.FirstSeen = parseTime(firstSeen)
		s.LastSeen = parseTime(lastSeen)
		out = append(out, s)
	}
	return out, rows.Err()
}

// ProjectModelTotals returns per-(project, model) token totals across the
// filtered set, used to build the projects ranking with accurate cost.
type ProjectModelTotals struct {
	ProjectSlug         string
	Cwd                 string
	Model               string
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
	Messages            int64
	Sessions            int64
}

func (r *Repo) TotalsByProjectModel(ctx context.Context, f Filters) ([]ProjectModelTotals, error) {
	w, args := f.where()
	q := `SELECT ` + projectKeyExpr + ` AS project_key,
			COALESCE(NULLIF(cwd, ''), project_slug) AS cwd_or_slug,
			model,
			COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0),
			COUNT(*), COUNT(DISTINCT session_id)
		FROM usage_records` + w + `
		GROUP BY project_key, model`
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectModelTotals
	for rows.Next() {
		var t ProjectModelTotals
		if err := rows.Scan(&t.ProjectSlug, &t.Cwd, &t.Model,
			&t.InputTokens, &t.OutputTokens, &t.CacheCreationTokens, &t.CacheReadTokens,
			&t.Messages, &t.Sessions); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SessionModelBreakdown returns per-(session, model) token totals for the
// given session ids, respecting the same filters used to list the page.
// Used to compute accurate per-session cost (each session can mix models).
type SessionModelBreakdown struct {
	SessionID           string
	ProjectSlug         string
	Model               string
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
}

func (r *Repo) SessionModelBreakdown(ctx context.Context, f Filters, sessionIDs []string) ([]SessionModelBreakdown, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	w, args := f.where()
	placeholders := ""
	for i, id := range sessionIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id)
	}
	q := `SELECT session_id, project_slug, model,
			COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
			COALESCE(SUM(cache_creation_tokens),0), COALESCE(SUM(cache_read_tokens),0)
		FROM usage_records` + w + ` AND session_id IN (` + placeholders + `)
		GROUP BY session_id, project_slug, model`
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionModelBreakdown
	for rows.Next() {
		var b SessionModelBreakdown
		if err := rows.Scan(&b.SessionID, &b.ProjectSlug, &b.Model,
			&b.InputTokens, &b.OutputTokens, &b.CacheCreationTokens, &b.CacheReadTokens); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SessionsByProject returns COUNT(DISTINCT session_id) per project for the
// given filter — used to populate the sessions column on the project ranking
// without over-counting sessions that span multiple models.
func (r *Repo) SessionsByProject(ctx context.Context, f Filters) (map[string]int64, error) {
	w, args := f.where()
	rows, err := r.DB.QueryContext(ctx,
		`SELECT `+projectKeyExpr+` AS project_key, COUNT(DISTINCT session_id) FROM usage_records`+w+` GROUP BY project_key`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var p string
		var n int64
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, rows.Err()
}

func (r *Repo) DistinctProjects(ctx context.Context) ([]string, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT DISTINCT `+projectKeyExpr+` AS project_key FROM usage_records ORDER BY project_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) DistinctModels(ctx context.Context) ([]string, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT DISTINCT model FROM usage_records WHERE model != '<synthetic>' ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SessionRecord is a single message-level row used by the drill-down view.
type SessionRecord struct {
	ID                  int64     `json:"id"`
	Timestamp           time.Time `json:"ts"`
	Model               string    `json:"model"`
	GitBranch           string    `json:"git_branch"`
	InputTokens         int64     `json:"input_tokens"`
	OutputTokens        int64     `json:"output_tokens"`
	CacheCreationTokens int64     `json:"cache_creation_tokens"`
	CacheReadTokens     int64     `json:"cache_read_tokens"`
	SourceFile          string    `json:"source_file"`
	SourceLine          int64     `json:"source_line"`
}

func (r *Repo) ListSessionRecords(ctx context.Context, sessionID string) ([]SessionRecord, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id, ts, model, COALESCE(git_branch, ''),
		input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
		source_file, source_line
		FROM usage_records
		WHERE session_id = ? AND model != '<synthetic>'
		ORDER BY ts ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRecord
	for rows.Next() {
		var rec SessionRecord
		var ts string
		if err := rows.Scan(&rec.ID, &ts, &rec.Model, &rec.GitBranch,
			&rec.InputTokens, &rec.OutputTokens, &rec.CacheCreationTokens, &rec.CacheReadTokens,
			&rec.SourceFile, &rec.SourceLine); err != nil {
			return nil, err
		}
		rec.Timestamp = parseTime(ts)
		out = append(out, rec)
	}
	return out, rows.Err()
}

type Budget struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	AmountUSD        float64   `json:"amount_usd"`
	ShowInDashboard  bool      `json:"show_in_dashboard"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (r *Repo) ListBudgets(ctx context.Context) ([]Budget, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id, name, amount_usd, COALESCE(show_in_dashboard, 0), created_at, updated_at FROM budgets ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Budget
	for rows.Next() {
		var b Budget
		var show int
		var created, updated string
		if err := rows.Scan(&b.ID, &b.Name, &b.AmountUSD, &show, &created, &updated); err != nil {
			return nil, err
		}
		b.ShowInDashboard = show != 0
		b.CreatedAt = parseTime(created)
		b.UpdatedAt = parseTime(updated)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *Repo) CreateBudget(ctx context.Context, name string, amount float64) (*Budget, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.DB.ExecContext(ctx,
		`INSERT INTO budgets(name, amount_usd, show_in_dashboard, created_at, updated_at) VALUES (?, ?, 0, ?, ?)`,
		name, amount, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Budget{ID: id, Name: name, AmountUSD: amount, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil
}

func (r *Repo) UpdateBudget(ctx context.Context, id int64, name string, amount float64, show bool) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	showInt := 0
	if show {
		showInt = 1
	}
	_, err := r.DB.ExecContext(ctx,
		`UPDATE budgets SET name=?, amount_usd=?, show_in_dashboard=?, updated_at=? WHERE id=?`,
		name, amount, showInt, now, id)
	return err
}

func (r *Repo) DeleteBudget(ctx context.Context, id int64) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM budgets WHERE id=?`, id)
	return err
}

type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *Repo) ListSettings(ctx context.Context) (map[string]string, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (r *Repo) UpsertSetting(ctx context.Context, key, value string) error {
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO app_settings(key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (r *Repo) DeleteRecord(ctx context.Context, id int64) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM usage_records WHERE id = ?`, id)
	return err
}

type Pricing struct {
	Model                string     `json:"model"`
	InputPerMTokUSD      float64    `json:"input_per_mtok_usd"`
	OutputPerMTokUSD     float64    `json:"output_per_mtok_usd"`
	CacheWritePerMTokUSD float64    `json:"cache_write_per_mtok_usd"`
	CacheReadPerMTokUSD  float64    `json:"cache_read_per_mtok_usd"`
	Source               string     `json:"source"`
	FetchedAt            *time.Time `json:"fetched_at,omitempty"`
	UpdatedAt            *time.Time `json:"updated_at,omitempty"`
}

func (r *Repo) ListPricing(ctx context.Context) ([]Pricing, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT model, input_per_mtok_usd, output_per_mtok_usd,
		cache_write_per_mtok_usd, cache_read_per_mtok_usd,
		COALESCE(source, 'manual'), COALESCE(fetched_at, ''), COALESCE(updated_at, '')
		FROM model_pricing ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pricing
	for rows.Next() {
		var p Pricing
		var fetchedAt, updatedAt string
		if err := rows.Scan(&p.Model, &p.InputPerMTokUSD, &p.OutputPerMTokUSD,
			&p.CacheWritePerMTokUSD, &p.CacheReadPerMTokUSD,
			&p.Source, &fetchedAt, &updatedAt); err != nil {
			return nil, err
		}
		if fetchedAt != "" {
			t := parseTime(fetchedAt)
			if !t.IsZero() {
				p.FetchedAt = &t
			}
		}
		if updatedAt != "" {
			t := parseTime(updatedAt)
			if !t.IsZero() {
				p.UpdatedAt = &t
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) UpsertPricing(ctx context.Context, p Pricing) error {
	if p.Source == "" {
		p.Source = "manual"
	}
	var fetchedAt any
	if p.FetchedAt != nil {
		fetchedAt = p.FetchedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := r.DB.ExecContext(ctx, `INSERT INTO model_pricing(model, input_per_mtok_usd, output_per_mtok_usd,
		cache_write_per_mtok_usd, cache_read_per_mtok_usd, source, fetched_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(model) DO UPDATE SET
			input_per_mtok_usd=excluded.input_per_mtok_usd,
			output_per_mtok_usd=excluded.output_per_mtok_usd,
			cache_write_per_mtok_usd=excluded.cache_write_per_mtok_usd,
			cache_read_per_mtok_usd=excluded.cache_read_per_mtok_usd,
			source=excluded.source,
			fetched_at=excluded.fetched_at,
			updated_at=excluded.updated_at`,
		p.Model, p.InputPerMTokUSD, p.OutputPerMTokUSD, p.CacheWritePerMTokUSD, p.CacheReadPerMTokUSD,
		p.Source, fetchedAt, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (r *Repo) DeletePricing(ctx context.Context, model string) error {
	_, err := r.DB.ExecContext(ctx, `DELETE FROM model_pricing WHERE model = ?`, model)
	return err
}

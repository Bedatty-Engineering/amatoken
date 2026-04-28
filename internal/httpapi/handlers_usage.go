package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/bedatty/amatoken/internal/pricing"
	"github.com/bedatty/amatoken/internal/storage"
	"github.com/go-chi/chi/v5"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseFilters(r *http.Request) storage.Filters {
	q := r.URL.Query()
	f := storage.Filters{Project: q.Get("project"), Model: q.Get("model"), Search: q.Get("q")}
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.From = &t
		} else if t, err := time.Parse("2006-01-02", v); err == nil {
			f.From = &t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.To = &t
		} else if t, err := time.Parse("2006-01-02", v); err == nil {
			tt := t.Add(24 * time.Hour)
			f.To = &tt
		}
	}
	return f
}

func (s *Server) calculator(ctx context.Context) (*pricing.Calculator, error) {
	rates, err := s.Repo.ListPricing(ctx)
	if err != nil {
		return nil, err
	}
	return pricing.NewCalculator(rates), nil
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f := parseFilters(r)
	sum, err := s.Repo.Summary(ctx, f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	totals, err := s.Repo.TotalsByModel(ctx, f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	calc, err := s.calculator(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, t := range totals {
		sum.CostUSD += calc.CostUSD(t.Model, t.InputTokens, t.OutputTokens, t.CacheCreationTokens, t.CacheReadTokens)
	}
	type modelOut struct {
		storage.ModelTotals
		CostUSD float64 `json:"cost_usd"`
	}
	out := struct {
		Summary storage.Summary `json:"summary"`
		Models  []modelOut      `json:"models"`
	}{Summary: sum}
	for _, t := range totals {
		out.Models = append(out.Models, modelOut{
			ModelTotals: t,
			CostUSD:     calc.CostUSD(t.Model, t.InputTokens, t.OutputTokens, t.CacheCreationTokens, t.CacheReadTokens),
		})
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleTimeSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bucket := r.URL.Query().Get("bucket")
	if bucket != "hour" {
		bucket = "day"
	}
	filters := parseFilters(r)
	pts, err := s.Repo.TimeSeries(ctx, filters, bucket)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Layer cost per bucket on top — useful for the chart hover details.
	bdRows, err := s.Repo.TimeSeriesByModel(ctx, filters, bucket)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	calc, err := s.calculator(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	costByBucket := map[string]float64{}
	for _, b := range bdRows {
		costByBucket[b.BucketKey] += calc.CostUSD(b.Model, b.InputTokens, b.OutputTokens, b.CacheCreationTokens, b.CacheReadTokens)
	}
	layout := "2006-01-02"
	if bucket == "hour" {
		layout = "2006-01-02 15:04:05"
	}
	for i := range pts {
		key := pts[i].Bucket.Format(layout)
		pts[i].CostUSD = costByBucket[key]
	}
	writeJSON(w, 200, pts)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	limit := 50
	offset := 0
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v >= 0 {
		offset = v
	}
	filters := parseFilters(r)
	rows, err := s.Repo.ListSessions(ctx, filters, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	total, err := s.Repo.CountSessions(ctx, filters)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	calc, err := s.calculator(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Accurate per-session cost: a session can mix several models, each with
	// its own pricing. Pull the per-(session, model) breakdown for the page
	// rows and sum cost across models.
	ids := make([]string, 0, len(rows))
	for _, sr := range rows {
		ids = append(ids, sr.SessionID)
	}
	bd, err := s.Repo.SessionModelBreakdown(ctx, filters, ids)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	costBySession := map[string]float64{}
	for _, b := range bd {
		costBySession[b.SessionID] += calc.CostUSD(b.Model, b.InputTokens, b.OutputTokens, b.CacheCreationTokens, b.CacheReadTokens)
	}

	type out struct {
		storage.SessionRow
		CostUSD float64 `json:"cost_usd"`
	}
	res := make([]out, 0, len(rows))
	for _, sr := range rows {
		res = append(res, out{
			SessionRow: sr,
			CostUSD:    costBySession[sr.SessionID],
		})
	}
	writeJSON(w, 200, map[string]any{
		"rows":   res,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleFilters(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projects, err := s.Repo.DistinctProjects(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	models, err := s.Repo.DistinctModels(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"projects": projects, "models": models})
}

func (s *Server) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if err := s.Repo.DeleteRecord(r.Context(), id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) handleSessionRecords(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "session id required", 400)
		return
	}
	rows, err := s.Repo.ListSessionRecords(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	calc, err := s.calculator(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type out struct {
		storage.SessionRecord
		CostUSD float64 `json:"cost_usd"`
	}
	res := make([]out, 0, len(rows))
	for _, rec := range rows {
		res = append(res, out{
			SessionRecord: rec,
			CostUSD:       calc.CostUSD(rec.Model, rec.InputTokens, rec.OutputTokens, rec.CacheCreationTokens, rec.CacheReadTokens),
		})
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleListBudgets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	budgets, err := s.Repo.ListBudgets(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Compute current calendar-month spend once and reuse for every budget.
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	filters := storage.Filters{From: &monthStart}
	totals, err := s.Repo.TotalsByModel(ctx, filters)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	calc, err := s.calculator(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var spent float64
	for _, t := range totals {
		spent += calc.CostUSD(t.Model, t.InputTokens, t.OutputTokens, t.CacheCreationTokens, t.CacheReadTokens)
	}
	type out struct {
		storage.Budget
		SpentUSD float64 `json:"spent_usd"`
		PctUsed  float64 `json:"pct_used"`
	}
	res := make([]out, 0, len(budgets))
	for _, b := range budgets {
		pct := 0.0
		if b.AmountUSD > 0 {
			pct = spent / b.AmountUSD * 100
		}
		res = append(res, out{Budget: b, SpentUSD: spent, PctUsed: pct})
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleCreateBudget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string  `json:"name"`
		AmountUSD float64 `json:"amount_usd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.AmountUSD <= 0 {
		http.Error(w, "name and positive amount_usd required", 400)
		return
	}
	b, err := s.Repo.CreateBudget(r.Context(), body.Name, body.AmountUSD)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 201, b)
}

func (s *Server) handleUpdateBudget(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	var body struct {
		Name            string  `json:"name"`
		AmountUSD       float64 `json:"amount_usd"`
		ShowInDashboard bool    `json:"show_in_dashboard"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.AmountUSD <= 0 {
		http.Error(w, "name and positive amount_usd required", 400)
		return
	}
	if err := s.Repo.UpdateBudget(r.Context(), id, body.Name, body.AmountUSD, body.ShowInDashboard); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) handleDeleteBudget(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	if err := s.Repo.DeleteBudget(r.Context(), id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) handleListSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Repo.ListSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if settings == nil {
		settings = map[string]string{}
	}
	writeJSON(w, 200, settings)
}

func (s *Server) handleUpsertSetting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if body.Key == "" {
		http.Error(w, "key required", 400)
		return
	}
	if err := s.Repo.UpsertSetting(r.Context(), body.Key, body.Value); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, body)
}

func (s *Server) handleProjectsRanking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.Repo.TotalsByProjectModel(ctx, parseFilters(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	calc, err := s.calculator(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type proj struct {
		ProjectSlug         string  `json:"project_slug"`
		Cwd                 string  `json:"cwd"`
		Models              []string `json:"models"`
		InputTokens         int64   `json:"input_tokens"`
		OutputTokens        int64   `json:"output_tokens"`
		CacheCreationTokens int64   `json:"cache_creation_tokens"`
		CacheReadTokens     int64   `json:"cache_read_tokens"`
		Messages            int64   `json:"messages"`
		Sessions            int64   `json:"sessions"`
		CostUSD             float64 `json:"cost_usd"`
	}
	agg := map[string]*proj{}
	modelsSeen := map[string]map[string]bool{}
	for _, t := range rows {
		p, ok := agg[t.ProjectSlug]
		if !ok {
			p = &proj{ProjectSlug: t.ProjectSlug, Cwd: t.Cwd}
			agg[t.ProjectSlug] = p
			modelsSeen[t.ProjectSlug] = map[string]bool{}
		}
		if t.Cwd != "" {
			p.Cwd = t.Cwd
		}
		if !modelsSeen[t.ProjectSlug][t.Model] {
			modelsSeen[t.ProjectSlug][t.Model] = true
			p.Models = append(p.Models, t.Model)
		}
		p.InputTokens += t.InputTokens
		p.OutputTokens += t.OutputTokens
		p.CacheCreationTokens += t.CacheCreationTokens
		p.CacheReadTokens += t.CacheReadTokens
		p.Messages += t.Messages
		// Sessions per project: COUNT(DISTINCT session_id) within (project, model)
		// over-counts when the same session uses multiple models. Track unique
		// sessions per project at handler level instead.
		_ = t.Sessions
		p.CostUSD += calc.CostUSD(t.Model, t.InputTokens, t.OutputTokens, t.CacheCreationTokens, t.CacheReadTokens)
	}
	// Recompute distinct session count per project via a separate cheap query.
	sessRows, err := s.Repo.SessionsByProject(ctx, parseFilters(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for proj, n := range sessRows {
		if p, ok := agg[proj]; ok {
			p.Sessions = n
		}
	}
	out := make([]*proj, 0, len(agg))
	for _, p := range agg {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CostUSD > out[j].CostUSD })
	writeJSON(w, 200, out)
}

func (s *Server) handleModelsRanking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	totals, err := s.Repo.TotalsByModel(ctx, parseFilters(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	calc, err := s.calculator(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type modelOut struct {
		storage.ModelTotals
		CostUSD float64 `json:"cost_usd"`
	}
	out := make([]modelOut, 0, len(totals))
	for _, t := range totals {
		out = append(out, modelOut{
			ModelTotals: t,
			CostUSD:     calc.CostUSD(t.Model, t.InputTokens, t.OutputTokens, t.CacheCreationTokens, t.CacheReadTokens),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CostUSD > out[j].CostUSD })
	writeJSON(w, 200, out)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if err := s.Scanner.ScanAll(r.Context()); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

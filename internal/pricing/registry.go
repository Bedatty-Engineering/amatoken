package pricing

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/bedatty/amatoken/internal/storage"
)

const SourceManual = "manual"

// Registry coordinates the local pricing table with an external Provider.
// Manual entries (created/edited via UI) are never overwritten by sync —
// only rows with source != "manual" or missing rows are replaced.
type Registry struct {
	Repo     RegistryStore
	Provider Provider
	Interval time.Duration

	mu         sync.RWMutex
	lastSyncAt time.Time
	lastError  string
	lastCount  int
}

func NewRegistry(repo RegistryStore, p Provider, interval time.Duration) *Registry {
	return &Registry{Repo: repo, Provider: p, Interval: interval}
}

type SyncResult struct {
	Source     string    `json:"source"`
	Inserted   int       `json:"inserted"`
	Updated    int       `json:"updated"`
	Skipped    int       `json:"skipped_manual"`
	FetchedAt  time.Time `json:"fetched_at"`
	DurationMs int64     `json:"duration_ms"`
}

type ResetResult struct {
	Model  string `json:"model"`
	Action string `json:"action"`
	Source string `json:"source,omitempty"`
}

type ResetAllResult struct {
	Restored int           `json:"restored"`
	Deleted  int           `json:"deleted"`
	Results  []ResetResult `json:"results,omitempty"`
}

// Sync fetches once and upserts. Manual rows are preserved.
func (r *Registry) Sync(ctx context.Context) (*SyncResult, error) {
	start := time.Now()
	prices, err := r.Provider.Fetch(ctx)
	if err != nil {
		r.recordError(err.Error())
		return nil, err
	}
	existing, err := r.Repo.ListPricing(ctx)
	if err != nil {
		r.recordError(err.Error())
		return nil, err
	}
	// Sync only touches rows whose source is provider-managed (openrouter or
	// the legacy seed defaults). Rows born from a manual UI add keep their
	// "manual" source forever and are skipped. A user editing an
	// openrouter-sourced row keeps it openrouter, so the next sync still
	// pulls fresh values into it (this is by design — the row is still a
	// derivative of OpenRouter pricing).
	manual := map[string]bool{}
	known := map[string]bool{}
	for _, e := range existing {
		known[e.Model] = true
		if e.Source == SourceManual {
			manual[e.Model] = true
		}
	}
	res := SyncResult{Source: r.Provider.Name(), FetchedAt: time.Now().UTC()}
	for _, p := range prices {
		if manual[p.Model] {
			res.Skipped++
			continue
		}
		fetchedAt := p.FetchedAt
		row := storage.Pricing{
			Model:                p.Model,
			InputPerMTokUSD:      p.InputPerMTokUSD,
			OutputPerMTokUSD:     p.OutputPerMTokUSD,
			CacheWritePerMTokUSD: p.CacheWritePerMTokUSD,
			CacheReadPerMTokUSD:  p.CacheReadPerMTokUSD,
			ContextLength:        p.ContextLength,
			MaxOutputTokens:      p.MaxOutputTokens,
			Source:               p.Source,
			FetchedAt:            &fetchedAt,
		}
		if err := r.Repo.UpsertPricing(ctx, row); err != nil {
			log.Printf("pricing sync upsert %s: %v", p.Model, err)
			continue
		}
		if known[p.Model] {
			res.Updated++
		} else {
			res.Inserted++
		}
	}
	res.DurationMs = time.Since(start).Milliseconds()
	r.recordSuccess(res.Inserted + res.Updated)
	return &res, nil
}

// Run starts the periodic sync loop. The first attempt is non-blocking: if it
// fails the cached values keep working. Errors do not abort the loop.
func (r *Registry) Run(ctx context.Context) {
	if _, err := r.Sync(ctx); err != nil {
		log.Printf("pricing initial sync: %v (using cached values)", err)
	}
	if r.Interval <= 0 {
		return
	}
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			settings, _ := r.Repo.ListSettings(ctx)
			// "pricing_auto_sync" is a string boolean stored in app_settings.
			// Anything other than literal "false" enables auto-sync (default on).
			if settings["pricing_auto_sync"] == "false" {
				continue
			}
			if _, err := r.Sync(ctx); err != nil {
				log.Printf("pricing periodic sync: %v", err)
			}
		}
	}
}

type Status struct {
	LastSyncAt   *time.Time `json:"last_sync_at"`
	LastError    string     `json:"last_error,omitempty"`
	LastCount    int        `json:"last_count"`
	ProviderName string     `json:"provider"`
	IntervalSec  int64      `json:"interval_seconds"`
}

func (r *Registry) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := Status{
		LastError:    r.lastError,
		LastCount:    r.lastCount,
		ProviderName: r.Provider.Name(),
		IntervalSec:  int64(r.Interval / time.Second),
	}
	if !r.lastSyncAt.IsZero() {
		t := r.lastSyncAt
		st.LastSyncAt = &t
	}
	return st
}

func (r *Registry) recordSuccess(count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastSyncAt = time.Now().UTC()
	r.lastError = ""
	r.lastCount = count
}

func (r *Registry) recordError(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastError = msg
}

func (r *Registry) ResetModel(ctx context.Context, model string) (*ResetResult, error) {
	prices, err := r.Provider.Fetch(ctx)
	if err != nil {
		r.recordError(err.Error())
		return nil, err
	}
	providerMap := map[string]ModelPrice{}
	for _, p := range prices {
		providerMap[p.Model] = p
	}
	if p, ok := providerMap[model]; ok {
		fetchedAt := p.FetchedAt
		row := storage.Pricing{
			Model:                p.Model,
			InputPerMTokUSD:      p.InputPerMTokUSD,
			OutputPerMTokUSD:     p.OutputPerMTokUSD,
			CacheWritePerMTokUSD: p.CacheWritePerMTokUSD,
			CacheReadPerMTokUSD:  p.CacheReadPerMTokUSD,
			ContextLength:        p.ContextLength,
			MaxOutputTokens:      p.MaxOutputTokens,
			Source:               p.Source,
			FetchedAt:            &fetchedAt,
		}
		if err := r.Repo.UpsertPricing(ctx, row); err != nil {
			return nil, err
		}
		r.recordSuccess(1)
		return &ResetResult{Model: model, Action: "restored", Source: p.Source}, nil
	}
	if p, ok := DefaultRateForModel(model); ok {
		p.FetchedAt = nil
		if err := r.Repo.UpsertPricing(ctx, p); err != nil {
			return nil, err
		}
		r.recordSuccess(1)
		return &ResetResult{Model: model, Action: "restored", Source: p.Source}, nil
	}
	return &ResetResult{Model: model, Action: "skipped"}, nil
}

func (r *Registry) ResetAll(ctx context.Context) (*ResetAllResult, error) {
	prices, err := r.Provider.Fetch(ctx)
	if err != nil {
		r.recordError(err.Error())
		return nil, err
	}
	providerMap := map[string]ModelPrice{}
	for _, p := range prices {
		providerMap[p.Model] = p
	}
	existing, err := r.Repo.ListPricing(ctx)
	if err != nil {
		return nil, err
	}
	res := &ResetAllResult{}
	for _, current := range existing {
		if p, ok := providerMap[current.Model]; ok {
			fetchedAt := p.FetchedAt
			row := storage.Pricing{
				Model:                p.Model,
				InputPerMTokUSD:      p.InputPerMTokUSD,
				OutputPerMTokUSD:     p.OutputPerMTokUSD,
				CacheWritePerMTokUSD: p.CacheWritePerMTokUSD,
				CacheReadPerMTokUSD:  p.CacheReadPerMTokUSD,
				ContextLength:        p.ContextLength,
				MaxOutputTokens:      p.MaxOutputTokens,
				Source:               p.Source,
				FetchedAt:            &fetchedAt,
			}
			if err := r.Repo.UpsertPricing(ctx, row); err != nil {
				return nil, err
			}
			res.Restored++
			res.Results = append(res.Results, ResetResult{Model: current.Model, Action: "restored", Source: p.Source})
			continue
		}
		if p, ok := DefaultRateForModel(current.Model); ok {
			p.FetchedAt = nil
			if err := r.Repo.UpsertPricing(ctx, p); err != nil {
				return nil, err
			}
			res.Restored++
			res.Results = append(res.Results, ResetResult{Model: current.Model, Action: "restored", Source: p.Source})
			continue
		}
		res.Results = append(res.Results, ResetResult{Model: current.Model, Action: "skipped"})
	}
	r.recordSuccess(res.Restored)
	return res, nil
}

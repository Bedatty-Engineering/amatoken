package seed

import (
	"context"

	"github.com/bedatty/amatoken/internal/storage"
)

// FirstRunExamples seeds one example budget and one example manual pricing
// row on the first launch (when those tables are empty), so users see the
// shape of those features without having to manufacture demo data.
//
// Idempotent: only runs when the underlying tables are empty.
func FirstRunExamples(ctx context.Context, repo *storage.Repo) error {
	if err := seedExampleBudget(ctx, repo); err != nil {
		return err
	}
	return seedExampleManualPricing(ctx, repo)
}

func seedExampleBudget(ctx context.Context, repo *storage.Repo) error {
	existing, err := repo.ListBudgets(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	b, err := repo.CreateBudget(ctx, "Monthly cap", 1500)
	if err != nil {
		return err
	}
	// Pin to the dashboard by default so the user immediately sees the
	// budget bar feature in action; they can untick at any time.
	return repo.UpdateBudget(ctx, b.ID, b.Name, b.AmountUSD, true)
}

func seedExampleManualPricing(ctx context.Context, repo *storage.Repo) error {
	existing, err := repo.ListPricing(ctx)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Source == "manual" {
			return nil // user already has manual entries — skip
		}
	}
	return repo.UpsertPricing(ctx, storage.Pricing{
		Model:                "claude-custom",
		InputPerMTokUSD:      3,
		OutputPerMTokUSD:     15,
		CacheWritePerMTokUSD: 3.75,
		CacheReadPerMTokUSD:  0.30,
		Source:               "manual",
	})
}

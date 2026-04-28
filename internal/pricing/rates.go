package pricing

import (
	"context"

	"github.com/bedatty/amatoken/internal/storage"
)

// DefaultRates seeds model_pricing on first start. Values in USD per million tokens.
// Sourced from public Anthropic pricing for the Claude 4.x families. The user can edit via UI.
// SourceSeed marks rows produced by SeedDefaults — used as offline fallback,
// always overwritten by a successful OpenRouter sync. Distinct from "manual"
// (user-edited rows), which sync never touches.
const SourceSeed = "seed"

var DefaultRates = []storage.Pricing{
	{Model: "claude-opus-4-7", InputPerMTokUSD: 15, OutputPerMTokUSD: 75, CacheWritePerMTokUSD: 18.75, CacheReadPerMTokUSD: 1.5},
	{Model: "claude-opus-4-6", InputPerMTokUSD: 15, OutputPerMTokUSD: 75, CacheWritePerMTokUSD: 18.75, CacheReadPerMTokUSD: 1.5},
	{Model: "claude-opus-4-5", InputPerMTokUSD: 15, OutputPerMTokUSD: 75, CacheWritePerMTokUSD: 18.75, CacheReadPerMTokUSD: 1.5},
	{Model: "claude-opus-4", InputPerMTokUSD: 15, OutputPerMTokUSD: 75, CacheWritePerMTokUSD: 18.75, CacheReadPerMTokUSD: 1.5},
	{Model: "claude-sonnet-4-6", InputPerMTokUSD: 3, OutputPerMTokUSD: 15, CacheWritePerMTokUSD: 3.75, CacheReadPerMTokUSD: 0.3},
	{Model: "claude-sonnet-4-5", InputPerMTokUSD: 3, OutputPerMTokUSD: 15, CacheWritePerMTokUSD: 3.75, CacheReadPerMTokUSD: 0.3},
	{Model: "claude-sonnet-4", InputPerMTokUSD: 3, OutputPerMTokUSD: 15, CacheWritePerMTokUSD: 3.75, CacheReadPerMTokUSD: 0.3},
	{Model: "claude-haiku-4-5", InputPerMTokUSD: 1, OutputPerMTokUSD: 5, CacheWritePerMTokUSD: 1.25, CacheReadPerMTokUSD: 0.1},
}

func SeedDefaults(ctx context.Context, repo *storage.Repo) error {
	existing, err := repo.ListPricing(ctx)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Model] = true
	}
	for _, p := range DefaultRates {
		if have[p.Model] {
			continue
		}
		p.Source = SourceSeed
		if err := repo.UpsertPricing(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

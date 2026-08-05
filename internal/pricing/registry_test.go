package pricing

import (
	"context"
	"testing"
	"time"

	"github.com/bedatty/amatoken/internal/storage"
)

func TestCalculatorFallsBackToClosestModelFamily(t *testing.T) {
	calc := NewCalculator([]storage.Pricing{
		{Model: "claude-opus-4", InputPerMTokUSD: 15, OutputPerMTokUSD: 75, CacheWritePerMTokUSD: 18.75, CacheReadPerMTokUSD: 1.5},
	})

	cost := calc.CostUSD("claude-opus-4-7-20260101", 1_000_000, 1_000_000, 1_000_000, 1_000_000)
	want := 15 + 75 + 18.75 + 1.5
	if cost != want {
		t.Fatalf("CostUSD() = %v, want %v", cost, want)
	}
}

func TestRegistrySyncPreservesManualRows(t *testing.T) {
	fetchedAt := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	store := &fakeRegistryStore{
		pricing: []storage.Pricing{
			{Model: "manual-model", Source: SourceManual, InputPerMTokUSD: 1},
			{Model: "seed-model", Source: SourceSeed, InputPerMTokUSD: 2},
		},
	}
	provider := fakeProvider{prices: []ModelPrice{
		{Model: "manual-model", InputPerMTokUSD: 10, Source: "test", FetchedAt: fetchedAt},
		{Model: "seed-model", InputPerMTokUSD: 20, Source: "test", FetchedAt: fetchedAt},
		{Model: "new-model", InputPerMTokUSD: 30, Source: "test", FetchedAt: fetchedAt},
	}}

	res, err := NewRegistry(store, provider, 0).Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Updated != 1 || res.Inserted != 1 {
		t.Fatalf("sync counts = inserted %d updated %d skipped %d", res.Inserted, res.Updated, res.Skipped)
	}
	if got := store.byModel("manual-model").InputPerMTokUSD; got != 1 {
		t.Fatalf("manual model was overwritten: %v", got)
	}
	if got := store.byModel("seed-model").InputPerMTokUSD; got != 20 {
		t.Fatalf("seed model was not updated: %v", got)
	}
	if got := store.byModel("new-model").InputPerMTokUSD; got != 30 {
		t.Fatalf("new model was not inserted: %v", got)
	}
}

type fakeProvider struct {
	prices []ModelPrice
}

func (p fakeProvider) Name() string { return "test" }

func (p fakeProvider) Fetch(context.Context) ([]ModelPrice, error) {
	return p.prices, nil
}

type fakeRegistryStore struct {
	pricing []storage.Pricing
}

func (s *fakeRegistryStore) ListPricing(context.Context) ([]storage.Pricing, error) {
	return append([]storage.Pricing(nil), s.pricing...), nil
}

func (s *fakeRegistryStore) UpsertPricing(_ context.Context, p storage.Pricing) error {
	for i, existing := range s.pricing {
		if existing.Model == p.Model {
			s.pricing[i] = p
			return nil
		}
	}
	s.pricing = append(s.pricing, p)
	return nil
}

func (s *fakeRegistryStore) ListSettings(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

func (s *fakeRegistryStore) byModel(model string) storage.Pricing {
	for _, p := range s.pricing {
		if p.Model == model {
			return p
		}
	}
	return storage.Pricing{}
}

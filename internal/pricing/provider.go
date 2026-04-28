package pricing

import (
	"context"
	"time"
)

// ModelPrice is the canonical pricing record produced by a Provider, expressed
// as USD per 1M tokens for each token category. CacheWrite/CacheRead are
// optional — Anthropic publishes them via OpenRouter for cache-supporting
// models but older models leave them at zero.
type ModelPrice struct {
	Model                string
	InputPerMTokUSD      float64
	OutputPerMTokUSD     float64
	CacheWritePerMTokUSD float64
	CacheReadPerMTokUSD  float64
	Source               string
	FetchedAt            time.Time
}

// Provider is the contract for any external pricing source. Implementations
// are stateless — they fetch once on demand. The Registry handles persistence,
// caching and scheduling.
type Provider interface {
	Name() string
	Fetch(ctx context.Context) ([]ModelPrice, error)
}

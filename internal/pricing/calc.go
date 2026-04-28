package pricing

import (
	"regexp"
	"strings"

	"github.com/bedatty/amatoken/internal/storage"
)

var dateSuffix = regexp.MustCompile(`-\d{8}$`)
var trailingVersion = regexp.MustCompile(`-\d+$`)

type Calculator struct {
	rates map[string]storage.Pricing
}

func NewCalculator(rates []storage.Pricing) *Calculator {
	m := make(map[string]storage.Pricing, len(rates))
	for _, r := range rates {
		m[r.Model] = r
	}
	return &Calculator{rates: m}
}

// CostUSD computes cost given token totals and model.
// Unknown models contribute zero cost (caller can detect via Has).
func (c *Calculator) CostUSD(model string, input, output, cacheWrite, cacheRead int64) float64 {
	p, ok := c.lookup(model)
	if !ok {
		return 0
	}
	const m = 1_000_000.0
	return float64(input)*p.InputPerMTokUSD/m +
		float64(output)*p.OutputPerMTokUSD/m +
		float64(cacheWrite)*p.CacheWritePerMTokUSD/m +
		float64(cacheRead)*p.CacheReadPerMTokUSD/m
}

func (c *Calculator) Has(model string) bool {
	_, ok := c.lookup(model)
	return ok
}

// lookup matches a model id with progressive fallbacks:
//  1. exact match
//  2. strip trailing -YYYYMMDD date stamp (e.g. "claude-haiku-4-5-20251001")
//  3. walk up the version tail one segment at a time
//     (e.g. "claude-opus-4-7" → "claude-opus-4")
//
// OpenRouter typically registers families like "claude-opus-4"; our records
// carry specific point releases like "claude-opus-4-7". Walking up matches
// the closest published price.
func (c *Calculator) lookup(model string) (storage.Pricing, bool) {
	if p, ok := c.rates[model]; ok {
		return p, true
	}
	candidates := []string{}
	if s := dateSuffix.ReplaceAllString(model, ""); s != model {
		candidates = append(candidates, s)
	}
	cur := dateSuffix.ReplaceAllString(model, "")
	for {
		next := trailingVersion.ReplaceAllString(cur, "")
		if next == cur || !strings.Contains(next, "-") {
			break
		}
		cur = next
		candidates = append(candidates, cur)
	}
	for _, m := range candidates {
		if p, ok := c.rates[m]; ok {
			return p, true
		}
	}
	return storage.Pricing{}, false
}

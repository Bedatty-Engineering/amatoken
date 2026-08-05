package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	openRouterURL    = "https://openrouter.ai/api/v1/models"
	sourceOpenRouter = "openrouter"
)

// OpenRouter is a Provider that fetches models from
// https://openrouter.ai/api/v1/models. Pricing is published as USD per token
// strings; we convert to USD per 1M tokens.
type OpenRouter struct {
	HTTP *http.Client
	URL  string // override for tests
}

func NewOpenRouter() *OpenRouter {
	return &OpenRouter{
		HTTP: &http.Client{Timeout: 15 * time.Second},
		URL:  openRouterURL,
	}
}

func (o *OpenRouter) Name() string { return sourceOpenRouter }

type openRouterResponse struct {
	Data []openRouterModel `json:"data"`
}

type openRouterModel struct {
	ID            string `json:"id"`
	ContextLength int64  `json:"context_length"`
	TopProvider   struct {
		ContextLength       int64 `json:"context_length"`
		MaxCompletionTokens int64 `json:"max_completion_tokens"`
	} `json:"top_provider"`
	Pricing struct {
		Prompt          string `json:"prompt"`
		Completion      string `json:"completion"`
		InputCacheRead  string `json:"input_cache_read"`
		InputCacheWrite string `json:"input_cache_write"`
	} `json:"pricing"`
}

func (o *OpenRouter) Fetch(ctx context.Context) ([]ModelPrice, error) {
	url := o.URL
	if url == "" {
		url = openRouterURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("openrouter status %d: %s", resp.StatusCode, body)
	}
	var raw openRouterResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("openrouter decode: %w", err)
	}
	now := time.Now().UTC()
	out := make([]ModelPrice, 0, 32)
	for _, m := range raw.Data {
		modelID := m.ID
		if idx := strings.IndexByte(modelID, '/'); idx >= 0 {
			modelID = modelID[idx+1:]
		}
		// Skip variants like "claude-3.7-sonnet:thinking" - these are
		// inference modes, not separate billable models for our scope.
		if strings.Contains(modelID, ":") {
			continue
		}
		// Normalise dot-versioning ("claude-opus-4.7" / "gpt-4.1") to
		// dash-versioning so it matches what Claude Code and Codex write into JSONL.
		modelID = strings.ReplaceAll(modelID, ".", "-")
		prompt := parseDollarPerToken(m.Pricing.Prompt)
		completion := parseDollarPerToken(m.Pricing.Completion)
		// OpenRouter still lists the model even when prompt=0 (free aliases or
		// gated entries); skip those - they would silently zero out costs.
		if prompt == 0 && completion == 0 {
			continue
		}
		contextLength := m.ContextLength
		if m.TopProvider.ContextLength > 0 {
			contextLength = m.TopProvider.ContextLength
		}
		mp := ModelPrice{
			Model:                modelID,
			InputPerMTokUSD:      prompt,
			OutputPerMTokUSD:     completion,
			CacheWritePerMTokUSD: parseDollarPerToken(m.Pricing.InputCacheWrite),
			CacheReadPerMTokUSD:  parseDollarPerToken(m.Pricing.InputCacheRead),
			ContextLength:        contextLength,
			MaxOutputTokens:      m.TopProvider.MaxCompletionTokens,
			Source:               sourceOpenRouter,
			FetchedAt:            now,
		}
		// Anthropic's documented defaults when OpenRouter omits cache pricing:
		// cache write = 1.25x input, cache read = 0.10x input.
		if strings.Contains(modelID, "claude") {
			if mp.CacheWritePerMTokUSD == 0 {
				mp.CacheWritePerMTokUSD = mp.InputPerMTokUSD * 1.25
			}
			if mp.CacheReadPerMTokUSD == 0 {
				mp.CacheReadPerMTokUSD = mp.InputPerMTokUSD * 0.10
			}
		}
		out = append(out, mp)
	}
	return out, nil
}

// parseDollarPerToken converts OpenRouter's "0.000003" (USD per token) into
// USD per 1M tokens. Empty / non-numeric strings yield 0.
func parseDollarPerToken(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v * 1_000_000
}

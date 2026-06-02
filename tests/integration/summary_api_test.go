package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bedatty/amatoken/internal/httpapi"
	"github.com/bedatty/amatoken/internal/ingest"
	"github.com/bedatty/amatoken/internal/storage"
)

func TestClaudeIngestToSummaryAPI(t *testing.T) {
	tmp := t.TempDir()
	db, err := storage.Open(filepath.Join(tmp, "amatoken.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := storage.New(db)
	if err := repo.UpsertPricing(context.Background(), storage.Pricing{
		Model:                "claude-sonnet-4",
		InputPerMTokUSD:      3,
		OutputPerMTokUSD:     15,
		CacheWritePerMTokUSD: 3.75,
		CacheReadPerMTokUSD:  0.3,
		Source:               "test",
	}); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(tmp, "claude-projects")
	projectDir := filepath.Join(root, "project-one")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(projectDir, "session.jsonl")
	writeClaudeUsageFile(t, sessionFile)

	scanner := ingest.NewScanner(repo, root)
	if err := scanner.ScanAll(context.Background()); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(httpapi.New(repo, scanner, nil, nil).Router())
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/api/summary")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/summary status = %d", resp.StatusCode)
	}

	var body struct {
		Summary struct {
			InputTokens         int64   `json:"input_tokens"`
			OutputTokens        int64   `json:"output_tokens"`
			CacheCreationTokens int64   `json:"cache_creation_tokens"`
			CacheReadTokens     int64   `json:"cache_read_tokens"`
			Sessions            int64   `json:"sessions"`
			Messages            int64   `json:"messages"`
			CostUSD             float64 `json:"cost_usd"`
		} `json:"summary"`
		Models []struct {
			Model   string  `json:"model"`
			CostUSD float64 `json:"cost_usd"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if body.Summary.InputTokens != 1_000_000 || body.Summary.OutputTokens != 2_000_000 {
		t.Fatalf("unexpected token totals: %+v", body.Summary)
	}
	if body.Summary.CacheCreationTokens != 100_000 || body.Summary.CacheReadTokens != 50_000 {
		t.Fatalf("unexpected cache totals: %+v", body.Summary)
	}
	if body.Summary.Sessions != 1 || body.Summary.Messages != 1 {
		t.Fatalf("unexpected counts: %+v", body.Summary)
	}
	if body.Summary.CostUSD != 33.39 {
		t.Fatalf("cost_usd = %v, want 33.39", body.Summary.CostUSD)
	}
	if len(body.Models) != 1 || body.Models[0].Model != "claude-sonnet-4" {
		t.Fatalf("unexpected models: %+v", body.Models)
	}
}

func writeClaudeUsageFile(t *testing.T, path string) {
	t.Helper()
	line := map[string]any{
		"type":      "assistant",
		"requestId": "req-1",
		"sessionId": "session-1",
		"cwd":       "/work/project-one",
		"gitBranch": "main",
		"timestamp": time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		"message": map[string]any{
			"id":    "msg-1",
			"model": "claude-sonnet-4",
			"usage": map[string]any{
				"input_tokens":                1_000_000,
				"output_tokens":               2_000_000,
				"cache_creation_input_tokens": 100_000,
				"cache_read_input_tokens":     50_000,
			},
		},
	}
	data, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

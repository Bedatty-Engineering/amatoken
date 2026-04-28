package ingest

import (
	"encoding/json"
	"time"

	"github.com/bedatty/amatoken/internal/storage"
)

type rawLine struct {
	Type      string    `json:"type"`
	RequestID string    `json:"requestId"`
	SessionID string    `json:"sessionId"`
	Cwd       string    `json:"cwd"`
	GitBranch string    `json:"gitBranch"`
	Timestamp time.Time `json:"timestamp"`
	Message   *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseLine returns nil, nil if the line is not an assistant usage record.
func ParseLine(data []byte, projectSlug, sourceFile string, lineNo int64) (*storage.UsageRecord, error) {
	var r rawLine
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Type != "assistant" || r.Message == nil || r.Message.Usage == nil || r.Message.ID == "" {
		return nil, nil
	}
	u := r.Message.Usage
	return &storage.UsageRecord{
		MessageID:           r.Message.ID,
		RequestID:           r.RequestID,
		SessionID:           r.SessionID,
		ProjectSlug:         projectSlug,
		Cwd:                 r.Cwd,
		GitBranch:           r.GitBranch,
		Model:               r.Message.Model,
		Timestamp:           r.Timestamp,
		InputTokens:         u.InputTokens,
		OutputTokens:        u.OutputTokens,
		CacheCreationTokens: u.CacheCreationInputTokens,
		CacheReadTokens:     u.CacheReadInputTokens,
		SourceFile:          sourceFile,
		SourceLine:          lineNo,
	}, nil
}

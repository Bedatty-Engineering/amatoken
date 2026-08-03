package ingest

import (
	"encoding/json"
	"time"

	"github.com/bedatty/amatoken/internal/storage"
)

// CodexSessionMeta holds the session-level context extracted from the
// session_meta line that begins every Codex JSONL file.
type CodexSessionMeta struct {
	ID  string
	Cwd string
}

type codexRawLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMetaPayload struct {
	ID  string `json:"id"`
	Cwd string `json:"cwd"`
}

type codexEventMsgPayload struct {
	Type string `json:"type"`
	Info struct {
		LastTokenUsage struct {
			InputTokens           int64 `json:"input_tokens"`
			CachedInputTokens     int64 `json:"cached_input_tokens"`
			OutputTokens          int64 `json:"output_tokens"`
			ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
		} `json:"last_token_usage"`
	} `json:"info"`
}

// ParseCodexMeta extracts the session metadata from a session_meta line.
// Returns nil if the line is not a session_meta event.
func ParseCodexMeta(data []byte) (*CodexSessionMeta, error) {
	var r codexRawLine
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Type != "session_meta" {
		return nil, nil
	}
	var p codexSessionMetaPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return nil, err
	}
	return &CodexSessionMeta{ID: p.ID, Cwd: p.Cwd}, nil
}

// ParseCodexLine converts a Codex event_msg/token_count line into a UsageRecord.
// Returns nil, nil when the line carries no billable token data.
func ParseCodexLine(data []byte, meta *CodexSessionMeta, model, projectSlug, sourceFile string, lineNo int64) (*storage.UsageRecord, error) {
	if meta == nil {
		return nil, nil
	}
	var r codexRawLine
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Type != "event_msg" {
		return nil, nil
	}
	var p codexEventMsgPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return nil, err
	}
	if p.Type != "token_count" {
		return nil, nil
	}
	lu := p.Info.LastTokenUsage
	if lu.InputTokens == 0 && lu.OutputTokens == 0 && lu.ReasoningOutputTokens == 0 {
		return nil, nil
	}

	ts, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil {
		ts, _ = time.Parse(time.RFC3339, r.Timestamp)
	}

	return &storage.UsageRecord{
		// Synthesised dedup key: provider prefix + session + event timestamp.
		MessageID:   "cx:" + meta.ID + ":" + r.Timestamp,
		SessionID:   meta.ID,
		ProjectSlug: projectSlug,
		Cwd:         meta.Cwd,
		Model:       model,
		Provider:    "codex",
		Timestamp:   ts,
		InputTokens: lu.InputTokens,
		// Reasoning tokens are billed as output tokens by OpenAI.
		OutputTokens:    lu.OutputTokens + lu.ReasoningOutputTokens,
		CacheReadTokens: lu.CachedInputTokens,
		SourceFile:      sourceFile,
		SourceLine:      lineNo,
	}, nil
}

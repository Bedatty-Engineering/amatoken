package ingest

import "testing"

func TestParseCodexLineConvertsTokenCount(t *testing.T) {
	meta := &CodexSessionMeta{ID: "session-1", Cwd: "/work/app"}
	line := []byte(`{"timestamp":"2026-06-02T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":30,"reasoning_output_tokens":7}}}}`)

	rec, err := ParseCodexLine(line, meta, "gpt-5-5", "2026", "/tmp/session.jsonl", 42)
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil {
		t.Fatal("expected usage record")
	}
	if rec.MessageID != "cx:session-1:2026-06-02T10:00:00Z" {
		t.Fatalf("MessageID = %q", rec.MessageID)
	}
	if rec.Provider != "codex" || rec.SessionID != "session-1" || rec.Cwd != "/work/app" {
		t.Fatalf("unexpected record context: %+v", rec)
	}
	if rec.InputTokens != 100 || rec.CacheReadTokens != 20 || rec.OutputTokens != 37 {
		t.Fatalf("unexpected token totals: %+v", rec)
	}
	if rec.ProjectSlug != "2026" || rec.SourceFile != "/tmp/session.jsonl" || rec.SourceLine != 42 {
		t.Fatalf("unexpected source metadata: %+v", rec)
	}
}

func TestParseCodexMetaIgnoresNonMetaLines(t *testing.T) {
	line := []byte(`{"timestamp":"2026-06-02T10:00:00Z","type":"event_msg","payload":{"type":"token_count"}}`)
	meta, err := ParseCodexMeta(line)
	if err != nil {
		t.Fatal(err)
	}
	if meta != nil {
		t.Fatalf("ParseCodexMeta() = %+v, want nil", meta)
	}
}

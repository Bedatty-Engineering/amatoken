package ingest

import (
	"bufio"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bedatty/amatoken/internal/storage"
)

// normalizeModel converts dot-versioning ("gpt-5.5") to dash-versioning
// ("gpt-5-5") so model IDs match what OpenRouter returns after sync.
func normalizeModel(model string) string {
	return strings.ReplaceAll(model, ".", "-")
}

// CodexScanner reads ~/.codex/sessions/**/*.jsonl and ingests token_count events.
type CodexScanner struct {
	Repo  Store
	Root  string
	Model string // default model name (e.g. "gpt-5.5") used when not in JSONL
}

func NewCodexScanner(repo Store, root, model string) *CodexScanner {
	return &CodexScanner{Repo: repo, Root: root, Model: normalizeModel(model)}
}

func (s *CodexScanner) GetRoot() string { return s.Root }

func (s *CodexScanner) ScanAll(ctx context.Context) error {
	return filepath.Walk(s.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if err := s.ProcessFile(ctx, path); err != nil {
			log.Printf("codex scan %s: %v", path, err)
		}
		return nil
	})
}

// ProcessFile ingests new token_count events from a single Codex session file.
// It uses a "codex:" key prefix in ingest_state to avoid collision with Claude files.
func (s *CodexScanner) ProcessFile(ctx context.Context, path string) error {
	rel, err := filepath.Rel(s.Root, path)
	if err != nil {
		rel = path
	}
	// Use year/month as project slug fallback; cwd from session_meta takes precedence in queries.
	projectSlug := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]

	stateKey := "codex:" + path
	st, err := s.Repo.GetIngestState(ctx, stateKey)
	if err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() < st.ByteOffset {
		st = storage.IngestState{}
	}
	if fi.Size() == st.ByteOffset {
		return nil // nothing new
	}

	// Pass 1: scan from the beginning to collect session_meta (always the first line).
	var meta *CodexSessionMeta
	preScanner := bufio.NewScanner(f)
	preScanner.Buffer(make([]byte, 2<<20), 2<<20) // 2 MB – session_meta has huge base_instructions
	for preScanner.Scan() {
		line := preScanner.Bytes()
		if len(line) == 0 {
			continue
		}
		m, _ := ParseCodexMeta(line)
		if m != nil {
			meta = m
			break
		}
	}
	if meta == nil {
		return nil // session_meta not yet written; skip until next reconcile
	}

	// Pass 2: seek to the saved offset and ingest new token_count events.
	if _, err := f.Seek(st.ByteOffset, io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	offset := st.ByteOffset
	lineNo := st.LastLine

	for {
		line, err := br.ReadBytes('\n')
		n := int64(len(line))
		if n > 0 {
			lineNo++
			trimmed := line
			if trimmed[len(trimmed)-1] == '\n' {
				trimmed = trimmed[:len(trimmed)-1]
			}
			if len(trimmed) > 0 {
				rec, perr := ParseCodexLine(trimmed, meta, s.Model, projectSlug, path, lineNo)
				if perr == nil && rec != nil {
					if ierr := s.Repo.InsertUsage(ctx, rec); ierr != nil {
						log.Printf("codex insert %s:%d: %v", path, lineNo, ierr)
					}
				}
			}
			offset += n
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	return s.Repo.SetIngestState(ctx, stateKey, storage.IngestState{ByteOffset: offset, LastLine: lineNo})
}

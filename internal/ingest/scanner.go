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

type Scanner struct {
	Repo *storage.Repo
	Root string
}

func NewScanner(repo *storage.Repo, root string) *Scanner {
	return &Scanner{Repo: repo, Root: root}
}

// ScanAll walks Root and processes every *.jsonl file from its saved offset.
func (s *Scanner) ScanAll(ctx context.Context) error {
	return filepath.Walk(s.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if err := s.ProcessFile(ctx, path); err != nil {
			log.Printf("scan %s: %v", path, err)
		}
		return nil
	})
}

func (s *Scanner) ProcessFile(ctx context.Context, path string) error {
	rel, err := filepath.Rel(s.Root, path)
	if err != nil {
		rel = path
	}
	projectSlug := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]

	st, err := s.Repo.GetIngestState(ctx, path)
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
	// File rotated/truncated — restart.
	if fi.Size() < st.ByteOffset {
		st = storage.IngestState{}
	}
	if _, err := f.Seek(st.ByteOffset, io.SeekStart); err != nil {
		return err
	}

	br := bufio.NewReaderSize(f, 1<<20)
	offset := st.ByteOffset
	lineNo := st.LastLine
	for {
		line, err := br.ReadBytes('\n')
		readN := int64(len(line))
		if readN > 0 {
			lineNo++
			trimmed := line
			if trimmed[len(trimmed)-1] == '\n' {
				trimmed = trimmed[:len(trimmed)-1]
			}
			if len(trimmed) > 0 {
				rec, perr := ParseLine(trimmed, projectSlug, path, lineNo)
				if perr == nil && rec != nil {
					if ierr := s.Repo.InsertUsage(ctx, rec); ierr != nil {
						log.Printf("insert %s:%d: %v", path, lineNo, ierr)
					}
				}
			}
			offset += readN
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	return s.Repo.SetIngestState(ctx, path, storage.IngestState{ByteOffset: offset, LastLine: lineNo})
}

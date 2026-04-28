package ingest

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	Scanner            *Scanner
	ReconcileInterval  time.Duration
	debounce           time.Duration
}

func NewWatcher(s *Scanner, interval time.Duration) *Watcher {
	return &Watcher{Scanner: s, ReconcileInterval: interval, debounce: 500 * time.Millisecond}
}

func (w *Watcher) Run(ctx context.Context) error {
	if err := w.Scanner.ScanAll(ctx); err != nil {
		log.Printf("initial scan: %v", err)
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fw.Close()

	w.addDirs(fw, w.Scanner.Root)

	ticker := time.NewTicker(w.ReconcileInterval)
	defer ticker.Stop()

	pending := map[string]time.Time{}
	flush := time.NewTicker(w.debounce)
	defer flush.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.Scanner.ScanAll(ctx); err != nil {
				log.Printf("reconcile: %v", err)
			}
		case ev, ok := <-fw.Events:
			if !ok {
				return nil
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					_ = fw.Add(ev.Name)
					continue
				}
				if strings.HasSuffix(ev.Name, ".jsonl") {
					pending[ev.Name] = time.Now()
				}
			}
		case <-flush.C:
			now := time.Now()
			for path, t := range pending {
				if now.Sub(t) >= w.debounce {
					if err := w.Scanner.ProcessFile(ctx, path); err != nil {
						log.Printf("watch process %s: %v", path, err)
					}
					delete(pending, path)
				}
			}
		case err, ok := <-fw.Errors:
			if !ok {
				return nil
			}
			log.Printf("fsnotify: %v", err)
		}
	}
}

func (w *Watcher) addDirs(fw *fsnotify.Watcher, root string) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if err := fw.Add(path); err != nil {
				log.Printf("watch add %s: %v", path, err)
			}
		}
		return nil
	})
}

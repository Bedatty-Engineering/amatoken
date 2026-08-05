package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bedatty/amatoken/internal/httpapi"
	"github.com/bedatty/amatoken/internal/ingest"
	"github.com/bedatty/amatoken/internal/pricing"
	"github.com/bedatty/amatoken/internal/rtkgain"
	"github.com/bedatty/amatoken/internal/seed"
	"github.com/bedatty/amatoken/internal/storage"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	root := env("CLAUDE_PROJECTS_DIR", "/claude-projects")
	dbPath := env("DB_PATH", "/data/amatoken.db")
	addr := env("LISTEN_ADDR", ":2002")
	intervalStr := env("RECONCILE_INTERVAL", "60s")
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		log.Fatalf("RECONCILE_INTERVAL: %v", err)
	}
	pricingIntervalStr := env("PRICING_SYNC_INTERVAL", "12h")
	pricingInterval, err := time.ParseDuration(pricingIntervalStr)
	if err != nil {
		log.Fatalf("PRICING_SYNC_INTERVAL: %v", err)
	}
	codexModelsPath := env("CODEX_MODELS_CACHE_PATH", "")

	db, err := storage.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	repo := storage.New(db)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := pricing.SeedDefaults(ctx, repo); err != nil {
		log.Fatalf("seed pricing: %v", err)
	}
	if err := seed.FirstRunExamples(ctx, repo); err != nil {
		log.Printf("seed examples: %v (continuing)", err)
	}

	scanner := ingest.NewScanner(repo, root)
	watcher := ingest.NewWatcher(scanner, interval)
	go func() {
		if err := watcher.Run(ctx); err != nil && err != context.Canceled {
			log.Printf("watcher: %v", err)
		}
	}()

	codexRoot := env("CODEX_SESSIONS_DIR", "")
	if codexRoot != "" {
		codexModel := env("CODEX_MODEL", "gpt-5.5")
		codexScanner := ingest.NewCodexScanner(repo, codexRoot, codexModel)
		codexWatcher := ingest.NewWatcher(codexScanner, interval)
		go func() {
			if err := codexWatcher.Run(ctx); err != nil && err != context.Canceled {
				log.Printf("codex watcher: %v", err)
			}
		}()
		log.Printf("codex: watching %s (model=%s)", codexRoot, codexModel)
	}

	registry := pricing.NewRegistry(repo, pricing.NewOpenRouter(), pricingInterval)
	go registry.Run(ctx)

	rtkDBPath := env("RTK_DB_PATH", "")
	rtkConfigured := rtkDBPath != ""
	rtkInitError := ""
	rtkReader, err := rtkgain.New(rtkDBPath)
	if err != nil {
		rtkInitError = err.Error()
		log.Printf("rtk: init failed: %v (continuing without RTK tab)", err)
	} else if rtkReader != nil {
		log.Printf("rtk: opened RTK database at %s", rtkDBPath)
		defer rtkReader.Close()
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.New(repo, scanner, registry, rtkReader, rtkConfigured, rtkInitError, codexModelsPath).Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("amatoken listening on %s (root=%s db=%s)", addr, root, dbPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	_ = srv.Shutdown(shutdownCtx)
}

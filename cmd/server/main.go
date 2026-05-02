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

	registry := pricing.NewRegistry(repo, pricing.NewOpenRouter(), pricingInterval)
	go registry.Run(ctx)

	srv := &http.Server{
		Addr:              addr,
		Handler:           httpapi.New(repo, scanner, registry).Router(),
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

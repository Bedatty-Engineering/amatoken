package httpapi

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/bedatty/amatoken/internal/ingest"
	"github.com/bedatty/amatoken/internal/pricing"
	"github.com/bedatty/amatoken/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	Repo            *storage.Repo
	Scanner         *ingest.Scanner
	PricingRegistry *pricing.Registry
}

func New(repo *storage.Repo, scanner *ingest.Scanner, registry *pricing.Registry) *Server {
	return &Server{Repo: repo, Scanner: scanner, PricingRegistry: registry}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	r.Route("/api", func(r chi.Router) {
		r.Get("/summary", s.handleSummary)
		r.Get("/timeseries", s.handleTimeSeries)
		r.Get("/sessions", s.handleSessions)
		r.Get("/sessions/{id}/records", s.handleSessionRecords)
		r.Get("/filters", s.handleFilters)
		r.Get("/settings", s.handleListSettings)
		r.Put("/settings", s.handleUpsertSetting)
		r.Get("/budgets", s.handleListBudgets)
		r.Post("/budgets", s.handleCreateBudget)
		r.Put("/budgets/{id}", s.handleUpdateBudget)
		r.Delete("/budgets/{id}", s.handleDeleteBudget)
		r.Get("/rankings/projects", s.handleProjectsRanking)
		r.Get("/rankings/models", s.handleModelsRanking)
		r.Delete("/records/{id}", s.handleDeleteRecord)

		r.Get("/pricing", s.handleListPricing)
		r.Post("/pricing", s.handleUpsertPricing)
		r.Put("/pricing/{model}", s.handleUpsertPricing)
		r.Delete("/pricing/{model}", s.handleDeletePricing)
		r.Post("/pricing/sync", s.handlePricingSync)
		r.Get("/pricing/status", s.handlePricingStatus)

		r.Post("/ingest/refresh", s.handleRefresh)
	})

	sub, _ := fs.Sub(staticFS, "static")
	r.Handle("/*", http.FileServer(http.FS(sub)))

	return r
}

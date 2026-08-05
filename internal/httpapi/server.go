package httpapi

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"reflect"
	"time"

	"github.com/bedatty/amatoken/internal/ingest"
	"github.com/bedatty/amatoken/internal/pricing"
	"github.com/bedatty/amatoken/internal/rtkgain"
	"github.com/bedatty/amatoken/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed static
var staticFS embed.FS

type RTKReader interface {
	Summary(ctx context.Context, from, to *time.Time) (*rtkgain.Summary, error)
	Commands(ctx context.Context, limit int, date string, from, to *time.Time) ([]rtkgain.CommandStat, error)
	TimeSeries(ctx context.Context, bucket string, from, to *time.Time, command string) ([]rtkgain.TimePoint, error)
}

type Server struct {
	Repo            *storage.Repo
	Scanner         *ingest.Scanner
	PricingRegistry *pricing.Registry
	RTKReader       RTKReader
	RTKConfigured   bool
	RTKInitError    string
	CodexModelsPath string
}

func New(repo *storage.Repo, scanner *ingest.Scanner, registry *pricing.Registry, rtkReader RTKReader, extras ...any) *Server {
	if isNilRTKReader(rtkReader) {
		rtkReader = nil
	}
	s := &Server{Repo: repo, Scanner: scanner, PricingRegistry: registry, RTKReader: rtkReader}
	if len(extras) > 0 {
		if v, ok := extras[0].(bool); ok {
			s.RTKConfigured = v
		}
	}
	if len(extras) > 1 {
		if v, ok := extras[1].(string); ok {
			s.RTKInitError = v
		}
	}
	if len(extras) > 2 {
		if v, ok := extras[2].(string); ok {
			s.CodexModelsPath = v
		}
	}
	return s
}

func isNilRTKReader(v RTKReader) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
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

	r.Get("/api/resources", s.handleResources)

	r.Route("/api", func(r chi.Router) {
		r.Get("/summary", s.handleSummary)
		r.Get("/timeseries", s.handleTimeSeries)
		r.Get("/sessions", s.handleSessions)
		r.Post("/sessions/import", s.handleImportSession)
		r.Get("/sessions/export-all", s.handleExportAllSessions)
		r.Get("/sessions/{id}/records", s.handleSessionRecords)
		r.Get("/sessions/{id}/delete-preview", s.handleDeleteSessionPreview)
		r.Get("/sessions/{id}/export", s.handleExportSession)
		r.Delete("/sessions/{id}", s.handleDeleteSession)
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
		r.Post("/pricing", s.handleCreatePricing)
		r.Put("/pricing/{model}", s.handleUpdatePricing)
		r.Post("/pricing/{model}/factory-reset", s.handleFactoryResetPricing)
		r.Delete("/pricing/{model}", s.handleDeletePricing)
		r.Post("/pricing/sync", s.handlePricingSync)
		r.Post("/pricing/factory-reset", s.handleFactoryResetAllPricing)
		r.Get("/pricing/status", s.handlePricingStatus)
		r.Get("/model-catalog", s.handleModelCatalog)

		r.Get("/rtk/summary", s.handleRTKSummary)
		r.Get("/rtk/timeseries", s.handleRTKTimeSeries)
		r.Get("/rtk/commands", s.handleRTKCommands)

		r.Post("/ingest/refresh", s.handleRefresh)
	})

	sub, _ := fs.Sub(staticFS, "static")
	r.Handle("/*", http.FileServer(http.FS(sub)))

	return r
}

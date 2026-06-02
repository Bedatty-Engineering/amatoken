package httpapi

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"reflect"
	"time"

	"github.com/bedatty/amatoken/internal/pricing"
	"github.com/bedatty/amatoken/internal/rtkgain"
	"github.com/bedatty/amatoken/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	Repo            Store
	Scanner         Scanner
	PricingRegistry PricingRegistry
	RTKReader       RTKReader
}

type PricingStore interface {
	ListPricing(ctx context.Context) ([]storage.Pricing, error)
	UpsertPricing(ctx context.Context, p storage.Pricing) error
	DeletePricing(ctx context.Context, model string) error
}

type UsageStore interface {
	Summary(ctx context.Context, f storage.Filters) (storage.Summary, error)
	TotalsByModel(ctx context.Context, f storage.Filters) ([]storage.ModelTotals, error)
	TimeSeries(ctx context.Context, f storage.Filters, bucket string) ([]storage.TimePoint, error)
	TimeSeriesByModel(ctx context.Context, f storage.Filters, bucket string) ([]storage.TimeSeriesByModelPoint, error)
	DistinctProjects(ctx context.Context) ([]string, error)
	DistinctModels(ctx context.Context) ([]string, error)
	DeleteRecord(ctx context.Context, id int64) error
}

type SessionStore interface {
	CountSessions(ctx context.Context, f storage.Filters) (int64, error)
	ListSessions(ctx context.Context, f storage.Filters, limit, offset int) ([]storage.SessionRow, error)
	SessionModelBreakdown(ctx context.Context, f storage.Filters, sessionIDs []string) ([]storage.SessionModelBreakdown, error)
	ListSessionRecords(ctx context.Context, sessionID string) ([]storage.SessionRecord, error)
}

type BudgetStore interface {
	ListBudgets(ctx context.Context) ([]storage.Budget, error)
	CreateBudget(ctx context.Context, name string, amount float64) (*storage.Budget, error)
	UpdateBudget(ctx context.Context, id int64, name string, amount float64, show bool) error
	DeleteBudget(ctx context.Context, id int64) error
}

type SettingsStore interface {
	ListSettings(ctx context.Context) (map[string]string, error)
	UpsertSetting(ctx context.Context, key, value string) error
}

type RankingStore interface {
	TotalsByProjectModel(ctx context.Context, f storage.Filters) ([]storage.ProjectModelTotals, error)
	SessionsByProject(ctx context.Context, f storage.Filters) (map[string]int64, error)
}

type Store interface {
	PricingStore
	UsageStore
	SessionStore
	BudgetStore
	SettingsStore
	RankingStore
}

type Scanner interface {
	ScanAll(ctx context.Context) error
}

type PricingRegistry interface {
	Sync(ctx context.Context) (*pricing.SyncResult, error)
	Status() pricing.Status
}

type RTKReader interface {
	Summary(ctx context.Context) (*rtkgain.Summary, error)
	Commands(ctx context.Context, limit int, date string) ([]rtkgain.CommandStat, error)
	TimeSeries(ctx context.Context, bucket string, from, to *time.Time, command string) ([]rtkgain.TimePoint, error)
}

func New(repo Store, scanner Scanner, registry PricingRegistry, rtkReader RTKReader) *Server {
	if isNilInterface(rtkReader) {
		rtkReader = nil
	}
	return &Server{Repo: repo, Scanner: scanner, PricingRegistry: registry, RTKReader: rtkReader}
}

func isNilInterface(v any) bool {
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
		r.Post("/pricing", s.handleCreatePricing)
		r.Put("/pricing/{model}", s.handleUpdatePricing)
		r.Delete("/pricing/{model}", s.handleDeletePricing)
		r.Post("/pricing/sync", s.handlePricingSync)
		r.Get("/pricing/status", s.handlePricingStatus)

		r.Get("/rtk/summary", s.handleRTKSummary)
		r.Get("/rtk/timeseries", s.handleRTKTimeSeries)
		r.Get("/rtk/commands", s.handleRTKCommands)

		r.Post("/ingest/refresh", s.handleRefresh)
	})

	sub, _ := fs.Sub(staticFS, "static")
	r.Handle("/*", http.FileServer(http.FS(sub)))

	return r
}

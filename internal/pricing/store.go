package pricing

import (
	"context"

	"github.com/bedatty/amatoken/internal/storage"
)

type Store interface {
	ListPricing(ctx context.Context) ([]storage.Pricing, error)
	UpsertPricing(ctx context.Context, p storage.Pricing) error
}

type RegistryStore interface {
	Store
	ListSettings(ctx context.Context) (map[string]string, error)
}

package main

import (
	"context"
	"log/slog"
	"time"

	catalogdomain "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/domain"
	orderingdomain "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

type catalogResolver interface {
	Resolve(context.Context, []string) ([]catalogdomain.Product, error)
}

type checkoutCatalog struct{ catalog catalogResolver }

func (adapter checkoutCatalog) ResolveCheckoutProducts(ctx context.Context, productIDs []string) ([]orderingdomain.ProductSnapshot, error) {
	products, err := adapter.catalog.Resolve(ctx, productIDs)
	if err != nil {
		return nil, err
	}
	result := make([]orderingdomain.ProductSnapshot, 0, len(products))
	for _, product := range products {
		result = append(result, orderingdomain.ProductSnapshot{
			ProductID: product.ID, Name: product.Name,
			UnitPriceCentavos: product.PriceCentavos, Orderable: product.Available,
		})
	}
	return result, nil
}

type catalogSyncer interface{ Sync(context.Context) error }

func runCatalogSync(ctx context.Context, syncer catalogSyncer, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := syncer.Sync(ctx); err != nil {
				logger.WarnContext(ctx, "catalog sync failed", "error_class", "catalog_source_unavailable")
			}
		}
	}
}

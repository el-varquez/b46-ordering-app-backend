package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/ports"
)

const syncPageSize = 100

type Service struct {
	source ports.Source
	store  ports.Store
	clock  ports.Clock
}

func New(source ports.Source, store ports.Store, clock ports.Clock) (*Service, error) {
	if source == nil || store == nil || clock == nil {
		return nil, errors.New("catalog application: source, store, and clock are required")
	}
	return &Service{source: source, store: store, clock: clock}, nil
}

func (service *Service) Sync(ctx context.Context) error {
	afterID := ""
	seenCursors := make(map[string]struct{})
	seenProducts := make(map[string]struct{})
	products := make([]domain.Product, 0, syncPageSize)
	for {
		page, err := service.source.Products(ctx, syncPageSize, afterID)
		if err != nil {
			return fmt.Errorf("read catalog source: %w", err)
		}
		for _, product := range page.Products {
			if err := product.Validate(); err != nil {
				return fmt.Errorf("validate source product: %w", err)
			}
			if _, duplicate := seenProducts[product.ID]; duplicate {
				return fmt.Errorf("%w: source repeated product %s", domain.ErrUnavailable, product.ID)
			}
			seenProducts[product.ID] = struct{}{}
			products = append(products, product)
		}
		if page.NextAfterID == "" {
			break
		}
		if _, exists := seenCursors[page.NextAfterID]; exists || page.NextAfterID == afterID {
			return fmt.Errorf("%w: catalog source cursor repeated", domain.ErrUnavailable)
		}
		seenCursors[page.NextAfterID] = struct{}{}
		afterID = page.NextAfterID
	}
	if err := service.store.ReplaceSnapshot(ctx, products, service.clock.Now().UTC()); err != nil {
		return fmt.Errorf("publish catalog snapshot: %w", err)
	}
	return nil
}

func (service *Service) Products(ctx context.Context, limit int, afterID string, revision int64) (domain.Page, error) {
	if limit < 1 || limit > 100 || revision < 0 {
		return domain.Page{}, domain.ErrInvalidInput
	}
	return service.store.Products(ctx, limit, afterID, revision)
}

func (service *Service) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return domain.ErrInvalidInput
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := service.Sync(ctx); err != nil {
				return err
			}
		}
	}
}

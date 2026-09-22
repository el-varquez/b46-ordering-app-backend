package application

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/domain"
)

type sourceStub struct {
	pages map[string]domain.Page
	err   error
}

func (source sourceStub) Products(_ context.Context, _ int, afterID string) (domain.Page, error) {
	if source.err != nil {
		return domain.Page{}, source.err
	}
	return source.pages[afterID], nil
}

type storeStub struct {
	products []domain.Product
	syncedAt time.Time
	page     domain.Page
	err      error
}

func (store *storeStub) ReplaceSnapshot(_ context.Context, products []domain.Product, syncedAt time.Time) error {
	store.products = append([]domain.Product(nil), products...)
	store.syncedAt = syncedAt
	return store.err
}
func (store *storeStub) Products(context.Context, int, string, int64) (domain.Page, error) {
	return store.page, store.err
}
func (store *storeStub) Resolve(context.Context, []string) ([]domain.Product, error) {
	return nil, store.err
}

type clockStub struct{ now time.Time }

func (clock clockStub) Now() time.Time { return clock.now }

func TestSyncPublishesAllPagesAsOneSnapshot(t *testing.T) {
	first := validProduct("b4600000-0000-4000-8001-000000000001")
	second := validProduct("b4600000-0000-4000-8001-000000000002")
	store := &storeStub{}
	now := time.Date(2026, 9, 22, 1, 2, 3, 0, time.FixedZone("PHT", 8*60*60))
	service, err := New(sourceStub{pages: map[string]domain.Page{
		"":       {Products: []domain.Product{first}, NextAfterID: first.ID},
		first.ID: {Products: []domain.Product{second}},
	}}, store, clockStub{now: now})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !reflect.DeepEqual(store.products, []domain.Product{first, second}) {
		t.Fatalf("published products = %#v", store.products)
	}
	if store.syncedAt.Location() != time.UTC || !store.syncedAt.Equal(now) {
		t.Fatalf("synced at = %v", store.syncedAt)
	}
}

func TestSyncRejectsRepeatedProductAndCursor(t *testing.T) {
	product := validProduct("b4600000-0000-4000-8001-000000000001")
	for name, pages := range map[string]map[string]domain.Page{
		"product": {
			"":     {Products: []domain.Product{product}, NextAfterID: "next"},
			"next": {Products: []domain.Product{product}},
		},
		"cursor": {
			"":     {Products: []domain.Product{product}, NextAfterID: "next"},
			"next": {NextAfterID: "next"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			service, _ := New(sourceStub{pages: pages}, &storeStub{}, clockStub{now: time.Now()})
			if err := service.Sync(context.Background()); !errors.Is(err, domain.ErrUnavailable) {
				t.Fatalf("Sync() error = %v", err)
			}
		})
	}
}

func validProduct(id string) domain.Product {
	return domain.Product{ID: id, Name: "Product", PriceCentavos: 100,
		CategoryID: "b4600000-0000-4000-8002-000000000001", CategoryName: "Category",
		Available: true, SourceUpdatedAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)}
}

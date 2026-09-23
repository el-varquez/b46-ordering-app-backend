//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSnapshotsRemainStableAndMissingProductsBecomeUnavailable(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is intentionally opt-in")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()
	resetCatalog(t, pool)
	defer resetCatalog(t, pool)

	store := New(pool)
	now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	first := integrationProduct("b4600000-0000-4000-8001-000000000001", 8200)
	second := integrationProduct("b4600000-0000-4000-8001-000000000002", 6800)
	if err := store.ReplaceSnapshot(context.Background(), []domain.Product{first, second}, now); err != nil {
		t.Fatalf("ReplaceSnapshot(1) error = %v", err)
	}
	page, err := store.Products(context.Background(), 1, "", 0)
	if err != nil || page.SnapshotRevision != 1 || len(page.Products) != 1 || page.NextAfterID == "" {
		t.Fatalf("first page = (%#v, %v)", page, err)
	}

	first.PriceCentavos = 8300
	third := integrationProduct("b4600000-0000-4000-8001-000000000003", 9500)
	if err := store.ReplaceSnapshot(context.Background(), []domain.Product{first, third}, now.Add(time.Minute)); err != nil {
		t.Fatalf("ReplaceSnapshot(2) error = %v", err)
	}
	oldPage, err := store.Products(context.Background(), 10, page.NextAfterID, page.SnapshotRevision)
	if err != nil || len(oldPage.Products) != 1 || oldPage.Products[0].ID != second.ID || !oldPage.Products[0].Available {
		t.Fatalf("stable old page = (%#v, %v)", oldPage, err)
	}
	current, err := store.Products(context.Background(), 10, "", 0)
	if err != nil || current.SnapshotRevision != 2 || len(current.Products) != 3 {
		t.Fatalf("current page = (%#v, %v)", current, err)
	}
	for _, product := range current.Products {
		if product.ID == second.ID && product.Available {
			t.Fatalf("missing product remained available: %#v", product)
		}
	}
	resolved, err := store.Resolve(context.Background(), []string{first.ID, second.ID})
	if err != nil || len(resolved) != 2 || resolved[0].PriceCentavos != 8300 || resolved[1].Available {
		t.Fatalf("resolved = (%#v, %v)", resolved, err)
	}

	if err := store.ReplaceSnapshot(context.Background(), []domain.Product{first, third}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("ReplaceSnapshot(3) error = %v", err)
	}
	if _, err := store.Products(context.Background(), 10, "", 1); !errors.Is(err, domain.ErrSnapshotExpired) {
		t.Fatalf("expired snapshot error = %v", err)
	}
}

func integrationProduct(id string, price int64) domain.Product {
	return domain.Product{ID: id, Name: id, PriceCentavos: price,
		CategoryID: "b4600000-0000-4000-8002-000000000001", CategoryName: "Daily essentials",
		Available: true, SourceUpdatedAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)}
}

func resetCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		TRUNCATE catalog_products;
		UPDATE catalog_sync_state SET current_revision = 0, last_completed_at = NULL, updated_at = now();
	`); err != nil {
		t.Fatalf("reset catalog: %v", err)
	}
}

//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/adapters/system"
	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

var testActorID = uuid.MustParse("b4600000-0000-4000-8000-000000000046")

type fixedClock struct{ value time.Time }

func (clock fixedClock) Now() time.Time { return clock.value }

func TestCommitDeductsOnceAndReplaysExactResult(t *testing.T) {
	store, pool := newIntegrationStore(t)
	productID := uuid.New()
	insertItem(t, pool, productID, 5, true, true, false)
	command := testCommand(domain.Line{OrderLineID: uuid.New(), ProductID: productID, Quantity: 2})
	fingerprint, _ := command.Fingerprint()

	first, err := store.Commit(context.Background(), command, fingerprint)
	if err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	second, err := store.Commit(context.Background(), command, fingerprint)
	if err != nil {
		t.Fatalf("second Commit() error = %v", err)
	}
	if first.Status != domain.StatusCommitted || second.EventID != first.EventID || second.OccurredAt != first.OccurredAt {
		t.Fatalf("replay = %#v, first = %#v", second, first)
	}
	assertCounts(t, pool, productID, 3, 1, 1)

	command.Lines[0].Quantity = 1
	changedFingerprint, _ := command.Fingerprint()
	if _, err := store.Commit(context.Background(), command, changedFingerprint); !strings.Contains(err.Error(), domain.ErrOperationConflict.Error()) {
		t.Fatalf("conflicting Commit() error = %v", err)
	}
	assertCounts(t, pool, productID, 3, 1, 1)
}

func TestCommitIsAllOrNothingWhenOneItemIsUnavailable(t *testing.T) {
	store, pool := newIntegrationStore(t)
	availableID, unavailableID := uuid.New(), uuid.New()
	insertItem(t, pool, availableID, 5, true, true, false)
	insertItem(t, pool, unavailableID, 0, true, true, false)
	command := testCommand(
		domain.Line{OrderLineID: uuid.New(), ProductID: availableID, Quantity: 1},
		domain.Line{OrderLineID: uuid.New(), ProductID: unavailableID, Quantity: 1},
	)
	fingerprint, _ := command.Fingerprint()
	result, err := store.Commit(context.Background(), command, fingerprint)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result.Status != domain.StatusItemsUnavailable || len(result.UnavailableItems) != 1 {
		t.Fatalf("Commit() result = %#v", result)
	}
	assertStock(t, pool, availableID, 5)
	assertStock(t, pool, unavailableID, 0)
	assertScalar(t, pool, `SELECT count(*) FROM public."StockMovements"`, 0)
	assertScalar(t, pool, `SELECT count(*) FROM b46_adapter.inventory_commit_receipts`, 1)
}

func TestCommitExpandsCompositeWithPOSCeiling(t *testing.T) {
	store, pool := newIntegrationStore(t)
	parentID, firstComponent, secondComponent := uuid.New(), uuid.New(), uuid.New()
	insertItem(t, pool, parentID, 0, true, true, true)
	insertItem(t, pool, firstComponent, 10, true, true, false)
	insertItem(t, pool, secondComponent, 10, true, true, false)
	insertComponent(t, pool, parentID, firstComponent, "1.250")
	insertComponent(t, pool, parentID, secondComponent, "0.500")
	command := testCommand(domain.Line{OrderLineID: uuid.New(), ProductID: parentID, Quantity: 2})
	fingerprint, _ := command.Fingerprint()
	result, err := store.Commit(context.Background(), command, fingerprint)
	if err != nil || result.Status != domain.StatusCommitted {
		t.Fatalf("Commit() result = %#v, error = %v", result, err)
	}
	assertStock(t, pool, parentID, 0)
	assertStock(t, pool, firstComponent, 7)
	assertStock(t, pool, secondComponent, 9)
	assertScalar(t, pool, `SELECT count(*) FROM public."StockMovements"`, 2)
}

func TestCommitAggregatesSharedCompositeDemandAndIgnoresNonStockItems(t *testing.T) {
	store, pool := newIntegrationStore(t)
	firstParent, secondParent, sharedComponent, serviceItem := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	insertItem(t, pool, firstParent, 0, true, true, true)
	insertItem(t, pool, secondParent, 0, true, true, true)
	insertItem(t, pool, sharedComponent, 5, true, true, false)
	insertItem(t, pool, serviceItem, 0, true, false, false)
	insertComponent(t, pool, firstParent, sharedComponent, "1.250")
	insertComponent(t, pool, secondParent, sharedComponent, "0.500")
	command := testCommand(
		domain.Line{OrderLineID: uuid.New(), ProductID: firstParent, Quantity: 2},
		domain.Line{OrderLineID: uuid.New(), ProductID: secondParent, Quantity: 2},
		domain.Line{OrderLineID: uuid.New(), ProductID: serviceItem, Quantity: 1},
	)
	fingerprint, _ := command.Fingerprint()
	result, err := store.Commit(context.Background(), command, fingerprint)
	if err != nil || result.Status != domain.StatusCommitted {
		t.Fatalf("Commit() result = %#v, error = %v", result, err)
	}
	// ceil(1.25*2) + ceil(0.5*2) = 3 + 1.
	assertStock(t, pool, sharedComponent, 1)
	assertStock(t, pool, serviceItem, 0)
	assertScalar(t, pool, `SELECT count(*) FROM public."StockMovements"`, 1)
}

func TestConcurrentOrdersCannotConsumeTheSameLastUnit(t *testing.T) {
	store, pool := newIntegrationStore(t)
	productID := uuid.New()
	insertItem(t, pool, productID, 1, true, true, false)
	commands := []domain.CommitCommand{
		testCommand(domain.Line{OrderLineID: uuid.New(), ProductID: productID, Quantity: 1}),
		testCommand(domain.Line{OrderLineID: uuid.New(), ProductID: productID, Quantity: 1}),
	}

	results := make([]domain.CommitResult, 2)
	errorsFound := make([]error, 2)
	var wait sync.WaitGroup
	for index := range commands {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			fingerprint, _ := commands[index].Fingerprint()
			results[index], errorsFound[index] = store.Commit(context.Background(), commands[index], fingerprint)
		}(index)
	}
	wait.Wait()
	committed, unavailable := 0, 0
	for index, result := range results {
		if errorsFound[index] != nil {
			t.Fatalf("Commit(%d) error = %v", index, errorsFound[index])
		}
		switch result.Status {
		case domain.StatusCommitted:
			committed++
		case domain.StatusItemsUnavailable:
			unavailable++
		}
	}
	if committed != 1 || unavailable != 1 {
		t.Fatalf("statuses = %#v, want one committed and one unavailable", results)
	}
	assertCounts(t, pool, productID, 0, 1, 2)
}

func TestConcurrentDuplicateOperationDeductsOnceAndReplaysExactly(t *testing.T) {
	store, pool := newIntegrationStore(t)
	productID := uuid.New()
	insertItem(t, pool, productID, 20, true, true, false)
	command := testCommand(domain.Line{OrderLineID: uuid.New(), ProductID: productID, Quantity: 2})
	fingerprint, _ := command.Fingerprint()

	const callers = 20
	results := make([]domain.CommitResult, callers)
	errorsFound := make([]error, callers)
	var wait sync.WaitGroup
	for index := range callers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errorsFound[index] = store.Commit(context.Background(), command, fingerprint)
		}(index)
	}
	wait.Wait()
	for index := range callers {
		if errorsFound[index] != nil {
			t.Fatalf("Commit(%d) error = %v", index, errorsFound[index])
		}
		if !reflect.DeepEqual(results[index], results[0]) {
			t.Fatalf("Commit(%d) result = %#v, want %#v", index, results[index], results[0])
		}
	}
	assertCounts(t, pool, productID, 18, 1, 1)
}

func TestCompatiblePOSSaleAndOnlineOrderCannotBothConsumeLastUnit(t *testing.T) {
	store, pool := newIntegrationStore(t)
	productID := uuid.New()
	insertItem(t, pool, productID, 1, true, true, false)
	command := testCommand(domain.Line{OrderLineID: uuid.New(), ProductID: productID, Quantity: 1})
	fingerprint, _ := command.Fingerprint()

	start := make(chan struct{})
	var online domain.CommitResult
	var onlineErr, posErr error
	var posRows int64
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		online, onlineErr = store.Commit(context.Background(), command, fingerprint)
	}()
	go func() {
		defer wait.Done()
		<-start
		tag, err := pool.Exec(context.Background(), `
			UPDATE public."Items"
			SET "Stock" = "Stock" - 1, "UpdatedAt" = now()
			WHERE "Id" = $1 AND "Stock" >= 1
		`, productID.String())
		posErr = err
		posRows = tag.RowsAffected()
	}()
	close(start)
	wait.Wait()
	if onlineErr != nil || posErr != nil {
		t.Fatalf("race errors = online %v, POS %v", onlineErr, posErr)
	}
	if (online.Status == domain.StatusCommitted && posRows != 0) ||
		(online.Status == domain.StatusItemsUnavailable && posRows != 1) {
		t.Fatalf("online status = %s, POS rows = %d; want exactly one winner", online.Status, posRows)
	}
	assertStock(t, pool, productID, 0)
}

func TestCatalogUsesExactPricesKeysetPaginationAndPublicAvailability(t *testing.T) {
	store, pool := newIntegrationStore(t)
	page, err := store.Catalog(context.Background(), domain.CatalogQuery{Limit: 2})
	if err != nil {
		t.Fatalf("Catalog() first page error = %v", err)
	}
	if len(page.Products) != 2 || page.NextAfterID == uuid.Nil {
		t.Fatalf("first page = %#v", page)
	}
	if page.Products[0].PriceCentavos != 8200 || page.Products[0].Name != "Coke 1.5L" {
		t.Fatalf("first product = %#v", page.Products[0])
	}
	second, err := store.Catalog(context.Background(), domain.CatalogQuery{Limit: 2, AfterID: page.NextAfterID})
	if err != nil || len(second.Products) != 1 || second.NextAfterID != uuid.Nil {
		t.Fatalf("second page = (%#v, %v)", second, err)
	}

	unavailable := uuid.New()
	insertItem(t, pool, unavailable, 0, true, true, false)
	nonStock := uuid.New()
	insertItem(t, pool, nonStock, 0, true, false, false)
	all, err := store.Catalog(context.Background(), domain.CatalogQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Catalog() availability page error = %v", err)
	}
	availability := make(map[uuid.UUID]bool, len(all.Products))
	for _, product := range all.Products {
		availability[product.ProductID] = product.Available
	}
	if availability[unavailable] || !availability[nonStock] {
		t.Fatalf("availability = %#v", availability)
	}
}

func TestDatabaseReadinessFailsClosedOnIncompatiblePOSColumn(t *testing.T) {
	_, pool := newIntegrationStore(t)
	database := &Database{pool: pool, healthTimeout: time.Second, actorID: testActorID}
	if err := database.Check(context.Background()); err != nil {
		t.Fatalf("Check() compatible fixture error = %v", err)
	}
	if _, err := pool.Exec(context.Background(), `ALTER TABLE public."Items" ALTER COLUMN "Stock" TYPE bigint`); err != nil {
		t.Fatalf("alter incompatible column: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `ALTER TABLE public."Items" ALTER COLUMN "Stock" TYPE integer USING "Stock"::integer`); err != nil {
			t.Errorf("restore compatible Items.Stock type: %v", err)
		}
	})
	if err := database.Check(context.Background()); err == nil {
		t.Fatal("Check() succeeded with incompatible Items.Stock type")
	}
}

func newIntegrationStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	databaseURL := os.Getenv("STORE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("STORE_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	var databaseName string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatalf("read current database: %v", err)
	}
	if !strings.HasSuffix(databaseName, "_test") {
		t.Fatalf("refusing destructive fixture against database %q", databaseName)
	}

	applyAdapterMigration(t, databaseURL)
	_, sourceFile, _, _ := runtime.Caller(0)
	fixture, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "testdata", "pos_fixture.sql"))
	if err != nil {
		t.Fatalf("read POS fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, string(fixture)); err != nil {
		t.Fatalf("apply POS fixture: %v", err)
	}
	store, err := New(pool, system.IDs{}, fixedClock{value: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}, testActorID)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return store, pool
}

func applyAdapterMigration(t *testing.T, databaseURL string) {
	t.Helper()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	defer database.Close()
	_, sourceFile, _, _ := runtime.Caller(0)
	migrations := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..", "migrations"))
	if err := goose.Up(database, migrations); err != nil {
		t.Fatalf("apply adapter migration: %v", err)
	}
}

func testCommand(lines ...domain.Line) domain.CommitCommand {
	return domain.CommitCommand{SchemaVersion: domain.SchemaVersion, EventID: uuid.New(), OperationID: uuid.New(),
		OrderID: uuid.New(), OccurredAt: time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC), Lines: lines}
}

func insertItem(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, stock int, active, tracked, composite bool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO public."Items" ("Id", "Name", "SellingPrice", "Stock", "IsActive", "TracksStock", "IsComposite", "UpdatedAt")
		VALUES ($1, $2, 1.25, $3, $4, $5, $6, now())
	`, id.String(), id.String(), stock, active, tracked, composite)
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
}

func insertComponent(t *testing.T, pool *pgxpool.Pool, parent, component uuid.UUID, quantity string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO public."CompositeItems" ("Id", "ParentItemId", "ComponentItemId", "Quantity", "CreatedAt")
		VALUES ($1, $2, $3, $4, now())
	`, uuid.New().String(), parent.String(), component.String(), quantity)
	if err != nil {
		t.Fatalf("insert component: %v", err)
	}
}

func assertCounts(t *testing.T, pool *pgxpool.Pool, itemID uuid.UUID, stock, movements, receipts int) {
	t.Helper()
	assertStock(t, pool, itemID, stock)
	assertScalar(t, pool, `SELECT count(*) FROM public."StockMovements"`, movements)
	assertScalar(t, pool, `SELECT count(*) FROM b46_adapter.inventory_commit_receipts`, receipts)
}

func assertStock(t *testing.T, pool *pgxpool.Pool, itemID uuid.UUID, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), `SELECT "Stock" FROM public."Items" WHERE "Id" = $1`, itemID.String()).Scan(&got); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if got != want {
		t.Fatalf("stock = %d, want %d", got, want)
	}
}

func assertScalar(t *testing.T, pool *pgxpool.Pool, query string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), query).Scan(&got); err != nil {
		t.Fatalf("query scalar: %v", err)
	}
	if got != want {
		t.Fatalf("scalar = %d, want %d", got, want)
	}
}

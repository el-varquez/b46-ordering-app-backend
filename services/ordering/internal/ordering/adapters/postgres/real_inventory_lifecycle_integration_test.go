//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/adapters/inventoryhttp"
	orderpostgres "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/adapters/postgres"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/adapters/system"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

const (
	realInventoryToken = "phase-4-integration-token-at-least-32-bytes"
	realInventoryActor = "b4600000-0000-4000-8000-000000000046"
	realCokeID         = "b4600000-0000-4000-8001-000000000001"
	realBreadID        = "b4600000-0000-4000-8001-000000000002"
	realMilkID         = "b4600000-0000-4000-8001-000000000003"
)

func TestRealInventoryHTTPLifecycleAndAmbiguousTimeoutReplay(t *testing.T) {
	orderingDatabaseURL := os.Getenv("TEST_DATABASE_URL")
	storeDatabaseURL := os.Getenv("STORE_TEST_DATABASE_URL")
	if orderingDatabaseURL == "" || storeDatabaseURL == "" {
		t.Skip("TEST_DATABASE_URL and STORE_TEST_DATABASE_URL are intentionally opt-in")
	}

	adapterRoot := phase4AdapterRoot(t)
	storePool := prepareRealInventoryFixture(t, storeDatabaseURL, adapterRoot)
	defer storePool.Close()
	adapterURL := startRealInventoryAdapter(t, storeDatabaseURL, adapterRoot)

	orderingPool := orderingPool(t)
	defer orderingPool.Close()
	customerID := seedOrderingUser(t, orderingPool, "CUSTOMER")
	cashierID := seedOrderingUser(t, orderingPool, "CASHIER")
	defer cleanupOrderingUser(t, orderingPool, cashierID)
	defer cleanupOrderingUser(t, orderingPool, customerID)

	clock := &adjustableOrderingClock{now: time.Now().UTC().Add(5 * time.Second)}
	store := orderpostgres.New(orderingPool)
	service := application.New(store, store, orderingCatalog{products: []domain.ProductSnapshot{
		{ProductID: realCokeID, Name: "Coke 1.5L", UnitPriceCentavos: 8200, Orderable: true},
		{ProductID: realBreadID, Name: "Tasty Bread", UnitPriceCentavos: 6800, Orderable: true},
		{ProductID: realMilkID, Name: "Fresh Milk 1L", UnitPriceCentavos: 9500, Orderable: true},
	}}, system.IDs{}, clock)
	directCommitter, err := inventoryhttp.New(&http.Client{Timeout: 2 * time.Second}, adapterURL, realInventoryToken, 64*1024)
	if err != nil {
		t.Fatalf("create real inventory committer: %v", err)
	}
	workerConfig := application.WorkerConfig{BatchSize: 10, LeaseTimeout: time.Minute, RetryBase: time.Second, RetryMax: time.Minute}
	directWorker := application.NewWorker(store, directCommitter, clock, workerConfig)

	happy := placeRealInventoryOrder(t, service, customerID, []domain.CheckoutLine{
		{ProductID: realCokeID, Quantity: 2, ExpectedUnitPriceCentavos: 8200},
		{ProductID: realBreadID, Quantity: 1, ExpectedUnitPriceCentavos: 6800},
	})
	if state, stateErr := happy.Order.CustomerState(); stateErr != nil || state != domain.CustomerPreparing {
		t.Fatalf("placed customer state = %q, error = %v", state, stateErr)
	}
	if processed, processErr := directWorker.ProcessOnce(context.Background()); processErr != nil || processed != 1 {
		t.Fatalf("happy ProcessOnce() = (%d, %v), want (1, nil)", processed, processErr)
	}
	assertStoreStock(t, storePool, realCokeID, 23)
	assertStoreStock(t, storePool, realBreadID, 24)
	assertStoreScalar(t, storePool, `SELECT count(*) FROM public."StockMovements"`, 2)
	assertStoreScalar(t, storePool, `SELECT count(*) FROM b46_adapter.inventory_commit_receipts`, 1)

	queue, err := service.CashierOrders(context.Background(), domain.FulfillmentPreparing, 20, "")
	if err != nil || len(queue) != 1 || queue[0].ID != happy.Order.ID {
		t.Fatalf("cashier queue = %#v, error = %v", queue, err)
	}
	if _, err := service.AdvanceFulfillment(context.Background(), cashierID, happy.Order.ID, domain.FulfillmentDelivering); err != nil {
		t.Fatalf("advance delivering: %v", err)
	}
	if _, err := service.AdvanceFulfillment(context.Background(), cashierID, happy.Order.ID, domain.FulfillmentDelivered); err != nil {
		t.Fatalf("advance delivered: %v", err)
	}

	unavailable := placeRealInventoryOrder(t, service, customerID, []domain.CheckoutLine{
		{ProductID: realCokeID, Quantity: 100, ExpectedUnitPriceCentavos: 8200},
		{ProductID: realBreadID, Quantity: 1, ExpectedUnitPriceCentavos: 6800},
	})
	if processed, processErr := directWorker.ProcessOnce(context.Background()); processErr != nil || processed != 1 {
		t.Fatalf("unavailable ProcessOnce() = (%d, %v), want (1, nil)", processed, processErr)
	}
	rejected, err := service.CustomerOrder(context.Background(), customerID, unavailable.Order.ID)
	if err != nil || rejected.Status != domain.OrderRejected || rejected.RejectionCode != domain.RejectionItemsUnavailable {
		t.Fatalf("unavailable order = %#v, error = %v", rejected, err)
	}
	assertStoreStock(t, storePool, realCokeID, 23)
	assertStoreStock(t, storePool, realBreadID, 24)
	assertStoreScalar(t, storePool, `SELECT count(*) FROM public."StockMovements"`, 2)
	assertStoreScalar(t, storePool, `SELECT count(*) FROM b46_adapter.inventory_commit_receipts`, 2)

	timeoutOrder := placeRealInventoryOrder(t, service, customerID, []domain.CheckoutLine{
		{ProductID: realMilkID, Quantity: 1, ExpectedUnitPriceCentavos: 9500},
	})
	timeoutProxy := delayedInventoryProxy(t, adapterURL, 400*time.Millisecond)
	defer timeoutProxy.Close()
	timeoutCommitter, err := inventoryhttp.New(&http.Client{Timeout: 100 * time.Millisecond}, timeoutProxy.URL, realInventoryToken, 64*1024)
	if err != nil {
		t.Fatalf("create timeout committer: %v", err)
	}
	timeoutWorker := application.NewWorker(store, timeoutCommitter, clock, workerConfig)
	if processed, processErr := timeoutWorker.ProcessOnce(context.Background()); processErr != nil || processed != 1 {
		t.Fatalf("timeout ProcessOnce() = (%d, %v), want retry", processed, processErr)
	}
	time.Sleep(450 * time.Millisecond)
	assertStoreStock(t, storePool, realMilkID, 24)
	assertStoreScalar(t, storePool, `SELECT count(*) FROM b46_adapter.inventory_commit_receipts`, 3)
	pending, err := service.CustomerOrder(context.Background(), customerID, timeoutOrder.Order.ID)
	if err != nil || pending.Status != domain.OrderSubmitted {
		t.Fatalf("timeout pending order = %#v, error = %v", pending, err)
	}

	clock.Advance(2 * time.Second)
	if processed, processErr := directWorker.ProcessOnce(context.Background()); processErr != nil || processed != 1 {
		t.Fatalf("timeout replay ProcessOnce() = (%d, %v), want (1, nil)", processed, processErr)
	}
	confirmed, err := service.CustomerOrder(context.Background(), customerID, timeoutOrder.Order.ID)
	if err != nil || confirmed.Status != domain.OrderConfirmed {
		t.Fatalf("timeout replay order = %#v, error = %v", confirmed, err)
	}
	assertStoreStock(t, storePool, realMilkID, 24)
	assertStoreScalar(t, storePool, `SELECT count(*) FROM public."StockMovements"`, 3)
	assertStoreScalar(t, storePool, `SELECT count(*) FROM b46_adapter.inventory_commit_receipts`, 3)
}

func phase4AdapterRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test path")
	}
	orderingRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", ".."))
	repositoryRoot := filepath.Clean(filepath.Join(orderingRoot, "..", ".."))
	return filepath.Join(repositoryRoot, "services", "inventory-adapter")
}

func prepareRealInventoryFixture(t *testing.T, databaseURL, adapterRoot string) *pgxpool.Pool {
	t.Helper()
	migrate := exec.Command("go", "run", "./cmd/migrate", "-dir", "migrations", "up")
	migrate.Dir = adapterRoot
	migrate.Env = append(os.Environ(), "STORE_DATABASE_URL="+databaseURL)
	if output, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("migrate real inventory fixture: %v\n%s", err, output)
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open real inventory fixture: %v", err)
	}
	var databaseName string
	if err := pool.QueryRow(context.Background(), `SELECT current_database()`).Scan(&databaseName); err != nil {
		pool.Close()
		t.Fatalf("read store test database name: %v", err)
	}
	if !strings.HasSuffix(databaseName, "_test") {
		pool.Close()
		t.Fatalf("refusing destructive fixture against database %q", databaseName)
	}
	fixturePath := filepath.Join(adapterRoot, "internal", "inventory", "adapters", "postgres", "testdata", "pos_fixture.sql")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		pool.Close()
		t.Fatalf("read real inventory fixture: %v", err)
	}
	if _, err := pool.Exec(context.Background(), string(fixture)); err != nil {
		pool.Close()
		t.Fatalf("apply real inventory fixture: %v", err)
	}
	return pool
}

func startRealInventoryAdapter(t *testing.T, databaseURL, adapterRoot string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve adapter port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	binaryName := "inventory-adapter"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-o", binaryPath, "./cmd/adapter")
	build.Dir = adapterRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build inventory adapter: %v\n%s", err, output)
	}

	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binaryPath)
	command.Dir = adapterRoot
	var logs bytes.Buffer
	command.Stdout = &logs
	command.Stderr = &logs
	command.Env = append(os.Environ(),
		"APP_ENV=test",
		"HTTP_HOST=127.0.0.1",
		"HTTP_PORT="+strconv.Itoa(port),
		"STORE_DATABASE_URL="+databaseURL,
		"INVENTORY_SERVICE_TOKEN="+realInventoryToken,
		"POS_MOVEMENT_ACTOR_ID="+realInventoryActor,
		"LOG_LEVEL=error",
	)
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start inventory adapter: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = command.Wait()
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, requestErr := http.Get(baseURL + "/health/ready") //nolint:gosec -- loopback integration process
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return baseURL
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	_ = command.Wait()
	t.Fatalf("inventory adapter did not become ready:\n%s", logs.String())
	return ""
}

func placeRealInventoryOrder(t *testing.T, service *application.Service, customerID string, lines []domain.CheckoutLine) domain.PlaceOrderResult {
	t.Helper()
	result, err := service.PlaceOrder(context.Background(), domain.PlaceOrderCommand{
		CustomerID: customerID, CheckoutID: uuid.NewString(), Lines: lines, DeliveryAddress: "Bria Homes",
	})
	if err != nil {
		t.Fatalf("PlaceOrder() error = %v", err)
	}
	return result
}

func delayedInventoryProxy(t *testing.T, upstream string, delay time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read proxy request: %v", err)
			return
		}
		upstreamRequest, err := http.NewRequestWithContext(context.Background(), request.Method, upstream+request.URL.Path, bytes.NewReader(body))
		if err != nil {
			t.Errorf("create proxy request: %v", err)
			return
		}
		upstreamRequest.Header = request.Header.Clone()
		response, err := http.DefaultClient.Do(upstreamRequest)
		if err != nil {
			t.Errorf("forward proxy request: %v", err)
			return
		}
		defer response.Body.Close()
		responseBody, err := io.ReadAll(response.Body)
		if err != nil {
			t.Errorf("read proxy response: %v", err)
			return
		}
		time.Sleep(delay)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write(responseBody)
	}))
}

func assertStoreStock(t *testing.T, pool *pgxpool.Pool, itemID string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), `SELECT "Stock" FROM public."Items" WHERE "Id" = $1`, itemID).Scan(&got); err != nil {
		t.Fatalf("read store stock: %v", err)
	}
	if got != want {
		t.Fatalf("store stock for %s = %d, want %d", itemID, got, want)
	}
}

func assertStoreScalar(t *testing.T, pool *pgxpool.Pool, query string, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), query).Scan(&got); err != nil {
		t.Fatalf("read store scalar: %v", err)
	}
	if got != want {
		t.Fatalf("store scalar = %d, want %d", got, want)
	}
}

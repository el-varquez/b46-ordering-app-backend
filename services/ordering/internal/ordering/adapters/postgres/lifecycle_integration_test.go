//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/adapters/catalogfake"
	inventoryfake "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/adapters/inventoryfake"
	orderpostgres "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/adapters/postgres"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/adapters/system"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

func TestPlaceOrderIsAtomicAndIdempotent(t *testing.T) {
	pool := orderingPool(t)
	defer pool.Close()
	customerID := seedOrderingUser(t, pool, "CUSTOMER")
	defer cleanupOrderingUser(t, pool, customerID)

	store := orderpostgres.New(pool)
	service := application.New(
		store,
		store,
		orderingCatalog{products: []domain.ProductSnapshot{{
			ProductID: "COKE", Name: "Coke 1.5L", UnitPriceCentavos: 8200, Orderable: true,
		}}},
		&orderingIDs{values: []string{
			"10000000-0000-4000-8000-000000000001",
			"10000000-0000-4000-8000-000000000002",
			"10000000-0000-4000-8000-000000000003",
			"10000000-0000-4000-8000-000000000004",
			"10000000-0000-4000-8000-000000000005",
			"10000000-0000-4000-8000-000000000006",
			"10000000-0000-4000-8000-000000000007",
			"10000000-0000-4000-8000-000000000008",
			"10000000-0000-4000-8000-000000000009",
			"10000000-0000-4000-8000-000000000010",
			"10000000-0000-4000-8000-000000000011",
			"10000000-0000-4000-8000-000000000012",
		}},
		orderingClock{now: time.Now().UTC().Add(time.Second)},
	)
	command := domain.PlaceOrderCommand{
		CustomerID: customerID,
		CheckoutID: "20000000-0000-4000-8000-000000000001",
		Lines: []domain.CheckoutLine{{
			ProductID: "COKE", Quantity: 2, ExpectedUnitPriceCentavos: 8200,
		}},
		DeliveryAddress: "Block 12, Bria Homes",
	}

	first, err := service.PlaceOrder(context.Background(), command)
	if err != nil {
		t.Fatalf("first PlaceOrder() error = %v", err)
	}
	second, err := service.PlaceOrder(context.Background(), command)
	if err != nil {
		t.Fatalf("second PlaceOrder() error = %v", err)
	}
	if !second.Replayed || second.Order.ID != first.Order.ID {
		t.Fatalf("second result = %#v, want replay of %s", second, first.Order.ID)
	}

	stored, err := service.CustomerOrder(context.Background(), customerID, first.Order.ID)
	if err != nil {
		t.Fatalf("CustomerOrder() error = %v", err)
	}
	if stored.Status != domain.OrderSubmitted || len(stored.Lines) != 1 || stored.TotalCentavos != 16400 {
		t.Fatalf("stored order = %#v", stored)
	}

	command.Lines[0].Quantity = 1
	_, err = service.PlaceOrder(context.Background(), command)
	if !errors.Is(err, domain.ErrCheckoutConflict) {
		t.Fatalf("changed replay error = %v, want ErrCheckoutConflict", err)
	}
}

func TestCommittedInventoryCompletesCustomerAndCashierLifecycle(t *testing.T) {
	pool := orderingPool(t)
	defer pool.Close()
	customerID := seedOrderingUser(t, pool, "CUSTOMER")
	cashierID := seedOrderingUser(t, pool, "CASHIER")
	defer cleanupOrderingUser(t, pool, cashierID)
	defer cleanupOrderingUser(t, pool, customerID)

	now := time.Now().UTC().Add(time.Second)
	clock := orderingClock{now: now}
	store := orderpostgres.New(pool)
	service := application.New(
		store,
		store,
		orderingCatalog{products: []domain.ProductSnapshot{{
			ProductID: "BREAD", Name: "Tasty Bread", UnitPriceCentavos: 6800, Orderable: true,
		}}},
		&orderingIDs{values: []string{
			"30000000-0000-4000-8000-000000000001",
			"30000000-0000-4000-8000-000000000002",
			"30000000-0000-4000-8000-000000000003",
			"30000000-0000-4000-8000-000000000004",
		}},
		clock,
	)
	placed, err := service.PlaceOrder(context.Background(), domain.PlaceOrderCommand{
		CustomerID: customerID,
		CheckoutID: "30000000-0000-4000-8000-000000000010",
		Lines: []domain.CheckoutLine{{
			ProductID: "BREAD", Quantity: 1, ExpectedUnitPriceCentavos: 6800,
		}},
		DeliveryAddress: "Bria Homes",
	})
	if err != nil {
		t.Fatalf("PlaceOrder() error = %v", err)
	}

	inventory := inventoryfake.New(
		&orderingIDs{values: []string{"30000000-0000-4000-8000-000000000020"}},
		clock,
	)
	worker := application.NewWorker(store, inventory, clock, application.WorkerConfig{
		BatchSize: 10, LeaseTimeout: time.Minute, RetryBase: time.Second, RetryMax: time.Minute,
	})
	processed, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	if processed != 1 {
		t.Fatalf("ProcessOnce() = %d, want 1", processed)
	}

	queue, err := service.CashierOrders(context.Background(), domain.FulfillmentPreparing, 20, "")
	if err != nil {
		t.Fatalf("CashierOrders() error = %v", err)
	}
	if len(queue) != 1 || queue[0].ID != placed.Order.ID || queue[0].Fulfillment == nil || !queue[0].Fulfillment.Unread {
		t.Fatalf("cashier queue = %#v", queue)
	}

	read, err := service.MarkCashierRead(context.Background(), cashierID, placed.Order.ID)
	if err != nil {
		t.Fatalf("MarkCashierRead() error = %v", err)
	}
	if read.Fulfillment.Status != domain.FulfillmentPreparing || read.Fulfillment.Unread {
		t.Fatalf("read fulfillment = %#v", read.Fulfillment)
	}
	if _, err := service.AdvanceFulfillment(
		context.Background(), cashierID, placed.Order.ID, domain.FulfillmentDelivered,
	); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("skipped transition error = %v, want ErrInvalidTransition", err)
	}
	if _, err := service.AdvanceFulfillment(
		context.Background(), cashierID, placed.Order.ID, domain.FulfillmentDelivering,
	); err != nil {
		t.Fatalf("advance delivering: %v", err)
	}
	if _, err := service.AdvanceFulfillment(
		context.Background(), cashierID, placed.Order.ID, domain.FulfillmentDelivering,
	); err != nil {
		t.Fatalf("repeat delivering: %v", err)
	}
	delivered, err := service.AdvanceFulfillment(
		context.Background(), cashierID, placed.Order.ID, domain.FulfillmentDelivered,
	)
	if err != nil {
		t.Fatalf("advance delivered: %v", err)
	}
	if delivered.Fulfillment.Status != domain.FulfillmentDelivered {
		t.Fatalf("fulfillment = %s, want DELIVERED", delivered.Fulfillment.Status)
	}
	if _, err := service.AdvanceFulfillment(
		context.Background(), cashierID, placed.Order.ID, domain.FulfillmentDelivered,
	); err != nil {
		t.Fatalf("repeat delivered: %v", err)
	}
	customerView, err := service.CustomerOrder(context.Background(), customerID, placed.Order.ID)
	if err != nil {
		t.Fatalf("CustomerOrder() error = %v", err)
	}
	state, err := customerView.CustomerState()
	if err != nil || state != domain.CustomerDelivered {
		t.Fatalf("customer state = %q, error = %v", state, err)
	}
}

func TestUnavailableInventoryRejectsOrderAndHidesItFromCashier(t *testing.T) {
	pool := orderingPool(t)
	defer pool.Close()
	customerID := seedOrderingUser(t, pool, "CUSTOMER")
	defer cleanupOrderingUser(t, pool, customerID)
	now := time.Now().UTC().Add(time.Second)
	clock := orderingClock{now: now}
	store := orderpostgres.New(pool)
	service := application.New(
		store, store,
		orderingCatalog{products: []domain.ProductSnapshot{{
			ProductID: "MILK", Name: "Fresh Milk", UnitPriceCentavos: 9500, Orderable: true,
		}}},
		&orderingIDs{values: []string{
			"40000000-0000-4000-8000-000000000001",
			"40000000-0000-4000-8000-000000000002",
			"40000000-0000-4000-8000-000000000003",
			"40000000-0000-4000-8000-000000000004",
		}},
		clock,
	)
	placed, err := service.PlaceOrder(context.Background(), domain.PlaceOrderCommand{
		CustomerID: customerID,
		CheckoutID: "40000000-0000-4000-8000-000000000010",
		Lines: []domain.CheckoutLine{{
			ProductID: "MILK", Quantity: 2, ExpectedUnitPriceCentavos: 9500,
		}},
		DeliveryAddress: "Bria Homes",
	})
	if err != nil {
		t.Fatalf("PlaceOrder() error = %v", err)
	}
	inventory := inventoryfake.New(
		&orderingIDs{values: []string{"40000000-0000-4000-8000-000000000020"}}, clock,
	)
	inventory.SetUnavailable("MILK", 0)
	worker := application.NewWorker(store, inventory, clock, application.WorkerConfig{
		BatchSize: 10, LeaseTimeout: time.Minute, RetryBase: time.Second, RetryMax: time.Minute,
	})
	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	rejected, err := service.CustomerOrder(context.Background(), customerID, placed.Order.ID)
	if err != nil {
		t.Fatalf("CustomerOrder() error = %v", err)
	}
	if rejected.Status != domain.OrderRejected || rejected.RejectionCode != domain.RejectionItemsUnavailable ||
		len(rejected.UnavailableItems) != 1 || rejected.Fulfillment != nil {
		t.Fatalf("rejected order = %#v", rejected)
	}
	queue, err := service.CashierOrders(context.Background(), "", 20, "")
	if err != nil {
		t.Fatalf("CashierOrders() error = %v", err)
	}
	for _, order := range queue {
		if order.ID == placed.Order.ID {
			t.Fatal("rejected order appeared in cashier queue")
		}
	}
	processed, err := worker.ProcessOnce(context.Background())
	if err != nil || processed != 0 {
		t.Fatalf("repeat ProcessOnce() = (%d, %v), want (0, nil)", processed, err)
	}
}

func TestWorkerRetrySurvivesWorkerRestart(t *testing.T) {
	pool := orderingPool(t)
	defer pool.Close()
	customerID := seedOrderingUser(t, pool, "CUSTOMER")
	defer cleanupOrderingUser(t, pool, customerID)
	clock := &adjustableOrderingClock{now: time.Now().UTC().Add(time.Second)}
	store := orderpostgres.New(pool)
	service := application.New(
		store, store,
		orderingCatalog{products: []domain.ProductSnapshot{{
			ProductID: "RICE", Name: "Rice 5kg", UnitPriceCentavos: 45000, Orderable: true,
		}}},
		&orderingIDs{values: []string{
			"50000000-0000-4000-8000-000000000001",
			"50000000-0000-4000-8000-000000000002",
			"50000000-0000-4000-8000-000000000003",
			"50000000-0000-4000-8000-000000000004",
		}},
		clock,
	)
	placed, err := service.PlaceOrder(context.Background(), domain.PlaceOrderCommand{
		CustomerID: customerID,
		CheckoutID: "50000000-0000-4000-8000-000000000010",
		Lines: []domain.CheckoutLine{{
			ProductID: "RICE", Quantity: 1, ExpectedUnitPriceCentavos: 45000,
		}},
		DeliveryAddress: "Bria Homes",
	})
	if err != nil {
		t.Fatalf("PlaceOrder() error = %v", err)
	}
	inventory := inventoryfake.New(
		&orderingIDs{values: []string{"50000000-0000-4000-8000-000000000020"}}, clock,
	)
	inventory.FailNext(errors.New("temporary store outage"))
	config := application.WorkerConfig{
		BatchSize: 10, LeaseTimeout: time.Minute, RetryBase: time.Second, RetryMax: time.Minute,
	}
	firstWorker := application.NewWorker(store, inventory, clock, config)
	processed, err := firstWorker.ProcessOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("first ProcessOnce() = (%d, %v), want (1, nil)", processed, err)
	}
	pending, err := service.CustomerOrder(context.Background(), customerID, placed.Order.ID)
	if err != nil || pending.Status != domain.OrderSubmitted {
		t.Fatalf("pending order = (%#v, %v)", pending, err)
	}
	if processed, err := firstWorker.ProcessOnce(context.Background()); err != nil || processed != 0 {
		t.Fatalf("early retry = (%d, %v), want (0, nil)", processed, err)
	}

	clock.Advance(2 * time.Second)
	restartedWorker := application.NewWorker(store, inventory, clock, config)
	processed, err = restartedWorker.ProcessOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("restarted ProcessOnce() = (%d, %v), want (1, nil)", processed, err)
	}
	confirmed, err := service.CustomerOrder(context.Background(), customerID, placed.Order.ID)
	if err != nil || confirmed.Status != domain.OrderConfirmed {
		t.Fatalf("confirmed order = (%#v, %v)", confirmed, err)
	}
}

func TestConcurrentCheckoutProducesOneAtomicOrder(t *testing.T) {
	pool := orderingPool(t)
	defer pool.Close()
	customerID := seedOrderingUser(t, pool, "CUSTOMER")
	defer cleanupOrderingUser(t, pool, customerID)

	store := orderpostgres.New(pool)
	service := application.New(
		store,
		store,
		catalogfake.New([]domain.ProductSnapshot{{
			ProductID: "CHIPS", Name: "Cheese Chips", UnitPriceCentavos: 4500, Orderable: true,
		}}),
		system.IDs{},
		system.Clock{},
	)
	checkoutID := "60000000-0000-4000-8000-000000000010"
	command := domain.PlaceOrderCommand{
		CustomerID: customerID,
		CheckoutID: checkoutID,
		Lines: []domain.CheckoutLine{{
			ProductID: "CHIPS", Quantity: 2, ExpectedUnitPriceCentavos: 4500,
		}},
		DeliveryAddress: "Bria Homes",
	}

	const callers = 8
	results := make(chan domain.PlaceOrderResult, callers)
	errorsFound := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.PlaceOrder(context.Background(), command)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- result
		}()
	}
	group.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent PlaceOrder() error = %v", err)
	}
	var orderID string
	for result := range results {
		if orderID == "" {
			orderID = result.Order.ID
		}
		if result.Order.ID != orderID {
			t.Fatalf("concurrent order ID = %s, want %s", result.Order.ID, orderID)
		}
	}
	var orders, operations, events int
	err := pool.QueryRow(context.Background(), `
		SELECT count(DISTINCT o.id), count(DISTINCT io.operation_id), count(DISTINCT ob.event_id)
		FROM orders o
		LEFT JOIN inventory_operations io ON io.order_id = o.id
		LEFT JOIN ordering_outbox ob ON ob.order_id = o.id
		WHERE o.checkout_id = $1
	`, checkoutID).Scan(&orders, &operations, &events)
	if err != nil {
		t.Fatalf("count checkout records: %v", err)
	}
	if orders != 1 || operations != 1 || events != 1 {
		t.Fatalf("atomic record counts = (%d, %d, %d), want (1, 1, 1)", orders, operations, events)
	}
}

func TestOutboxLeaseAndInventoryResultReplayProtection(t *testing.T) {
	pool := orderingPool(t)
	defer pool.Close()
	customerID := seedOrderingUser(t, pool, "CUSTOMER")
	defer cleanupOrderingUser(t, pool, customerID)

	now := time.Now().UTC().Add(time.Second)
	store := orderpostgres.New(pool)
	service := application.New(
		store,
		store,
		orderingCatalog{products: []domain.ProductSnapshot{{
			ProductID: "SOAP", Name: "Bath Soap", UnitPriceCentavos: 3500, Orderable: true,
		}}},
		&orderingIDs{values: []string{
			"70000000-0000-4000-8000-000000000001",
			"70000000-0000-4000-8000-000000000002",
			"70000000-0000-4000-8000-000000000003",
			"70000000-0000-4000-8000-000000000004",
		}},
		orderingClock{now: now},
	)
	_, err := service.PlaceOrder(context.Background(), domain.PlaceOrderCommand{
		CustomerID: customerID,
		CheckoutID: "70000000-0000-4000-8000-000000000010",
		Lines: []domain.CheckoutLine{{
			ProductID: "SOAP", Quantity: 1, ExpectedUnitPriceCentavos: 3500,
		}},
		DeliveryAddress: "Bria Homes",
	})
	if err != nil {
		t.Fatalf("PlaceOrder() error = %v", err)
	}

	first, err := store.ClaimInventoryWork(context.Background(), now, time.Minute, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = (%#v, %v), want one item", first, err)
	}
	active, err := store.ClaimInventoryWork(context.Background(), now.Add(30*time.Second), time.Minute, 1)
	if err != nil || len(active) != 0 {
		t.Fatalf("active lease claim = (%#v, %v), want no items", active, err)
	}
	reclaimed, err := store.ClaimInventoryWork(context.Background(), now.Add(2*time.Minute), time.Minute, 1)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ClaimToken == first[0].ClaimToken {
		t.Fatalf("expired lease reclaim = (%#v, %v), want new claim", reclaimed, err)
	}
	result := domain.InventoryResult{
		EventID:     "70000000-0000-4000-8000-000000000020",
		OperationID: reclaimed[0].Commit.OperationID,
		OrderID:     reclaimed[0].Commit.OrderID,
		Status:      domain.InventoryCommitted,
		OccurredAt:  now.Add(2 * time.Minute),
	}
	if err := store.ApplyInventoryResult(context.Background(), first[0], result, now); !errors.Is(err, domain.ErrClaimLost) {
		t.Fatalf("stale ApplyInventoryResult() error = %v, want ErrClaimLost", err)
	}
	if err := store.ApplyInventoryResult(context.Background(), reclaimed[0], result, now); err != nil {
		t.Fatalf("ApplyInventoryResult() error = %v", err)
	}
	if err := store.ApplyInventoryResult(context.Background(), reclaimed[0], result, now); err != nil {
		t.Fatalf("identical result replay error = %v", err)
	}
	changedEvent := result
	changedEvent.EventID = "70000000-0000-4000-8000-000000000021"
	if err := store.ApplyInventoryResult(context.Background(), reclaimed[0], changedEvent, now); !errors.Is(err, domain.ErrOperationConflict) {
		t.Fatalf("changed result event error = %v, want ErrOperationConflict", err)
	}
	contradictory := result
	contradictory.Status = domain.InventoryItemsUnavailable
	if err := store.ApplyInventoryResult(context.Background(), reclaimed[0], contradictory, now); !errors.Is(err, domain.ErrOperationConflict) {
		t.Fatalf("contradictory result error = %v, want ErrOperationConflict", err)
	}
}

func orderingPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is intentionally opt-in")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	return pool
}

func seedOrderingUser(t *testing.T, pool *pgxpool.Pool, role string) string {
	t.Helper()
	var userID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO users (name, normalized_email, role)
		VALUES ('Ordering Test', 'ordering-' || gen_random_uuid() || '@example.test', $1)
		RETURNING id
	`, role).Scan(&userID)
	if err != nil {
		t.Fatalf("seed ordering user: %v", err)
	}
	return userID
}

func cleanupOrderingUser(t *testing.T, pool *pgxpool.Pool, userID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		DELETE FROM audit_records
		WHERE target_type = 'ORDER'
		  AND target_id IN (SELECT id::text FROM orders WHERE customer_id = $1)
	`, userID); err != nil {
		t.Errorf("delete ordering target audit records: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_records WHERE actor_user_id = $1`, userID); err != nil {
		t.Errorf("delete ordering audit records: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM orders WHERE customer_id = $1`, userID); err != nil {
		t.Errorf("delete ordering orders: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Errorf("delete ordering user: %v", err)
	}
}

type orderingCatalog struct{ products []domain.ProductSnapshot }

func (catalog orderingCatalog) ResolveCheckoutProducts(context.Context, []string) ([]domain.ProductSnapshot, error) {
	return catalog.products, nil
}

type orderingIDs struct {
	values []string
	next   int
}

func (ids *orderingIDs) New() (string, error) {
	if ids.next >= len(ids.values) {
		return "", errors.New("ID sequence exhausted")
	}
	value := ids.values[ids.next]
	ids.next++
	return value, nil
}

type orderingClock struct{ now time.Time }

func (clock orderingClock) Now() time.Time { return clock.now }

type adjustableOrderingClock struct{ now time.Time }

func (clock *adjustableOrderingClock) Now() time.Time { return clock.now }
func (clock *adjustableOrderingClock) Advance(duration time.Duration) {
	clock.now = clock.now.Add(duration)
}

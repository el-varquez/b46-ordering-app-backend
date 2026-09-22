package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

type notifierStub struct {
	events []domain.Notification
	err    error
}

func (notifier *notifierStub) Notify(_ context.Context, event domain.Notification) error {
	notifier.events = append(notifier.events, event)
	return notifier.err
}

type queryStub struct{ order domain.Order }

func (query queryStub) CustomerOrder(context.Context, string, string) (domain.Order, error) {
	return query.order, nil
}
func (query queryStub) CustomerOrders(context.Context, string, int, string) ([]domain.Order, error) {
	return nil, nil
}
func (query queryStub) CashierOrder(context.Context, string) (domain.Order, error) {
	return query.order, nil
}
func (query queryStub) CashierOrders(context.Context, domain.FulfillmentStatus, int, string) ([]domain.Order, error) {
	return nil, nil
}
func (query queryStub) MarkCashierRead(context.Context, string, string, time.Time) (domain.Order, error) {
	return query.order, nil
}
func (query queryStub) AdvanceFulfillment(_ context.Context, _, _ string, status domain.FulfillmentStatus, _ time.Time) (domain.Order, error) {
	order := query.order
	order.Fulfillment = &domain.Fulfillment{Status: status}
	return order, nil
}

func TestFulfillmentNotificationFailureDoesNotRollBackTransition(t *testing.T) {
	notifier := &notifierStub{err: errors.New("notification unavailable")}
	order := domain.Order{ID: "00000000-0000-4000-8000-000000000001", CustomerID: "00000000-0000-4000-8000-000000000002"}
	service := New(nil, queryStub{order: order}, nil, nil, fixedClock{now: time.Now().UTC()}, notifier)
	got, err := service.AdvanceFulfillment(context.Background(),
		"00000000-0000-4000-8000-000000000003", order.ID, domain.FulfillmentDelivering)
	if err != nil || got.Fulfillment.Status != domain.FulfillmentDelivering {
		t.Fatalf("AdvanceFulfillment() = (%#v, %v)", got, err)
	}
	if len(notifier.events) != 1 || notifier.events[0].Kind != domain.NotificationOrderOnTheWay || notifier.events[0].RecipientUserID != order.CustomerID {
		t.Fatalf("events = %#v", notifier.events)
	}
}

type outboxStub struct {
	work    []domain.ClaimedInventoryWork
	applied int
}

func (store *outboxStub) ClaimInventoryWork(context.Context, time.Time, time.Duration, int) ([]domain.ClaimedInventoryWork, error) {
	return store.work, nil
}
func (store *outboxStub) ApplyInventoryResult(context.Context, domain.ClaimedInventoryWork, domain.InventoryResult, time.Time) error {
	store.applied++
	return nil
}
func (store *outboxStub) RetryInventoryWork(context.Context, domain.ClaimedInventoryWork, time.Time, string) error {
	return nil
}

type inventoryStub struct{ result domain.InventoryResult }

func (inventory inventoryStub) Commit(context.Context, domain.InventoryCommit) (domain.InventoryResult, error) {
	return inventory.result, nil
}

func TestAcceptedNotificationRunsAfterInventoryResultIsApplied(t *testing.T) {
	result := domain.InventoryResult{EventID: "event", OrderID: "order", Status: domain.InventoryCommitted}
	store := &outboxStub{work: []domain.ClaimedInventoryWork{{Commit: domain.InventoryCommit{OrderID: "order"}}}}
	notifier := &notifierStub{err: errors.New("notification unavailable")}
	worker := NewWorker(store, inventoryStub{result: result}, fixedClock{now: time.Now().UTC()}, WorkerConfig{BatchSize: 1}, notifier)
	processed, err := worker.ProcessOnce(context.Background())
	if err != nil || processed != 1 || store.applied != 1 {
		t.Fatalf("ProcessOnce() = (%d, %v), applied = %d", processed, err, store.applied)
	}
	if len(notifier.events) != 1 || notifier.events[0].Kind != domain.NotificationOrderAccepted || notifier.events[0].Audience != "CASHIER" {
		t.Fatalf("events = %#v", notifier.events)
	}
}

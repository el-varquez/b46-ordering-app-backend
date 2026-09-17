package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

func TestCustomerCanPlaceOrderWithAuthoritativeSnapshots(t *testing.T) {
	now := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	store := &checkoutStore{}
	service := New(
		store,
		nil,
		catalogStub{products: []domain.ProductSnapshot{
			{ProductID: "BREAD", Name: "Tasty Bread", UnitPriceCentavos: 6800, Orderable: true},
			{ProductID: "COKE", Name: "Coke 1.5L", UnitPriceCentavos: 8200, Orderable: true},
		}},
		&idSequence{values: []string{
			"00000000-0000-4000-8000-000000000001",
			"00000000-0000-4000-8000-000000000002",
			"00000000-0000-4000-8000-000000000003",
			"00000000-0000-4000-8000-000000000004",
			"00000000-0000-4000-8000-000000000005",
		}},
		fixedClock{now: now},
	)

	result, err := service.PlaceOrder(context.Background(), domain.PlaceOrderCommand{
		CustomerID: "00000000-0000-4000-8000-000000000010",
		CheckoutID: "00000000-0000-4000-8000-000000000020",
		Lines: []domain.CheckoutLine{
			{ProductID: "COKE", Quantity: 2, ExpectedUnitPriceCentavos: 8200},
			{ProductID: "BREAD", Quantity: 1, ExpectedUnitPriceCentavos: 6800},
		},
		DeliveryAddress: " Block 12, Bria Homes ",
		DeliveryNotes:   " Ring the bell ",
	})
	if err != nil {
		t.Fatalf("PlaceOrder() error = %v", err)
	}
	if result.Replayed {
		t.Fatal("PlaceOrder() unexpectedly reported replay")
	}
	if got, want := result.Order.TotalCentavos, int64(23200); got != want {
		t.Fatalf("total = %d, want %d", got, want)
	}
	if got := store.draft.Lines[0].ProductName; got != "Tasty Bread" {
		t.Fatalf("first sorted product = %q, want Tasty Bread", got)
	}
	if store.draft.CheckoutFingerprint == "" || store.draft.EventID == "" || store.draft.OperationID == "" {
		t.Fatal("draft omitted checkout fingerprint, event ID, or operation ID")
	}
	if store.draft.DeliveryAddress != "Block 12, Bria Homes" || store.draft.DeliveryNotes != "Ring the bell" {
		t.Fatalf("delivery fields were not normalized: %#v", store.draft)
	}
}

func TestChangedCartCreatesNoOrder(t *testing.T) {
	store := &checkoutStore{}
	service := New(
		store,
		nil,
		catalogStub{products: []domain.ProductSnapshot{{
			ProductID: "COKE", Name: "Coke 1.5L", UnitPriceCentavos: 8300, Orderable: true,
		}}},
		&idSequence{},
		fixedClock{now: time.Now().UTC()},
	)

	_, err := service.PlaceOrder(context.Background(), domain.PlaceOrderCommand{
		CustomerID:      "00000000-0000-4000-8000-000000000010",
		CheckoutID:      "00000000-0000-4000-8000-000000000020",
		DeliveryAddress: "Bria Homes",
		Lines: []domain.CheckoutLine{{
			ProductID: "COKE", Quantity: 1, ExpectedUnitPriceCentavos: 8200,
		}},
	})
	if !errors.Is(err, domain.ErrCartChanged) {
		t.Fatalf("PlaceOrder() error = %v, want ErrCartChanged", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

type checkoutStore struct {
	draft domain.OrderDraft
	calls int
}

func (store *checkoutStore) PlaceOrder(_ context.Context, draft domain.OrderDraft) (domain.PlaceOrderResult, error) {
	store.calls++
	store.draft = draft
	return domain.PlaceOrderResult{Order: draft.Order}, nil
}

type catalogStub struct{ products []domain.ProductSnapshot }

func (catalog catalogStub) ResolveCheckoutProducts(context.Context, []string) ([]domain.ProductSnapshot, error) {
	return catalog.products, nil
}

type idSequence struct {
	values []string
	next   int
}

func (ids *idSequence) New() (string, error) {
	if ids.next >= len(ids.values) {
		return "", errors.New("ID sequence exhausted")
	}
	value := ids.values[ids.next]
	ids.next++
	return value, nil
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

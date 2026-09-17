package inventoryfake

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

func TestCommitDeduplicatesOperationResult(t *testing.T) {
	ids := &fakeIDs{values: []string{"00000000-0000-4000-8000-000000000001"}}
	committer := New(ids, fakeClock{})
	request := domain.InventoryCommit{
		OperationID: "operation", OrderID: "order",
		Items: []domain.InventoryItem{{OrderLineID: "line", ProductID: "COKE", Quantity: 1}},
	}

	first, err := committer.Commit(context.Background(), request)
	if err != nil {
		t.Fatalf("first Commit() error = %v", err)
	}
	second, err := committer.Commit(context.Background(), request)
	if err != nil {
		t.Fatalf("second Commit() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) || ids.next != 1 {
		t.Fatalf("results = (%#v, %#v), generated IDs = %d; want one stable result", first, second, ids.next)
	}
}

func TestCommitReturnsWholeOrderUnavailable(t *testing.T) {
	committer := New(
		&fakeIDs{values: []string{"00000000-0000-4000-8000-000000000002"}},
		fakeClock{},
	)
	committer.SetUnavailable("MILK", 1)

	result, err := committer.Commit(context.Background(), domain.InventoryCommit{
		OperationID: "operation", OrderID: "order",
		Items: []domain.InventoryItem{{OrderLineID: "line", ProductID: "MILK", Quantity: 2}},
	})
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if result.Status != domain.InventoryItemsUnavailable || len(result.UnavailableItems) != 1 ||
		result.UnavailableItems[0].AvailableQuantity != 1 {
		t.Fatalf("result = %#v, want one unavailable item", result)
	}
}

func TestTechnicalFailureIsRetryableAndNotCached(t *testing.T) {
	committer := New(
		&fakeIDs{values: []string{"00000000-0000-4000-8000-000000000003"}},
		fakeClock{},
	)
	temporary := errors.New("temporary")
	committer.FailNext(temporary)
	request := domain.InventoryCommit{OperationID: "operation", OrderID: "order"}

	if _, err := committer.Commit(context.Background(), request); !errors.Is(err, temporary) {
		t.Fatalf("first Commit() error = %v, want temporary error", err)
	}
	result, err := committer.Commit(context.Background(), request)
	if err != nil || result.Status != domain.InventoryCommitted {
		t.Fatalf("retry Commit() = (%#v, %v), want committed", result, err)
	}
}

type fakeIDs struct {
	values []string
	next   int
}

func (ids *fakeIDs) New() (string, error) {
	value := ids.values[ids.next]
	ids.next++
	return value, nil
}

type fakeClock struct{}

func (fakeClock) Now() time.Time { return time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC) }

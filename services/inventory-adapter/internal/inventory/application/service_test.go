package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/domain"
	"github.com/google/uuid"
)

type fakeStore struct {
	calls       int
	fingerprint string
	result      domain.CommitResult
	err         error
}

func (store *fakeStore) Commit(_ context.Context, _ domain.CommitCommand, fingerprint string) (domain.CommitResult, error) {
	store.calls++
	store.fingerprint = fingerprint
	return store.result, store.err
}

func TestServiceValidatesBeforeCallingStore(t *testing.T) {
	store := &fakeStore{}
	service, err := NewService(store)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	_, err = service.Commit(context.Background(), domain.CommitCommand{})
	if !errors.Is(err, domain.ErrInvalidInput) || store.calls != 0 {
		t.Fatalf("Commit() error = %v, calls = %d", err, store.calls)
	}
}

func TestServiceDelegatesOneValidatedCommit(t *testing.T) {
	command := domain.CommitCommand{
		SchemaVersion: domain.SchemaVersion,
		EventID:       uuid.New(),
		OperationID:   uuid.New(),
		OrderID:       uuid.New(),
		OccurredAt:    time.Now().UTC(),
		Lines:         []domain.Line{{OrderLineID: uuid.New(), ProductID: uuid.New(), Quantity: 1}},
	}
	result := domain.CommitResult{SchemaVersion: domain.SchemaVersion, EventID: uuid.New(), OperationID: command.OperationID,
		OrderID: command.OrderID, Status: domain.StatusCommitted, OccurredAt: time.Now().UTC()}
	store := &fakeStore{result: result}
	service, _ := NewService(store)
	got, err := service.Commit(context.Background(), command)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if got.Status != result.Status || store.calls != 1 || len(store.fingerprint) != 64 {
		t.Fatalf("Commit() result = %#v, calls = %d, fingerprint = %q", got, store.calls, store.fingerprint)
	}
}

func TestServiceRejectsInvalidOrMismatchedStoreResult(t *testing.T) {
	command := domain.CommitCommand{SchemaVersion: domain.SchemaVersion, EventID: uuid.New(), OperationID: uuid.New(),
		OrderID: uuid.New(), OccurredAt: time.Now().UTC(),
		Lines: []domain.Line{{OrderLineID: uuid.New(), ProductID: uuid.New(), Quantity: 1}}}
	store := &fakeStore{result: domain.CommitResult{SchemaVersion: domain.SchemaVersion, EventID: uuid.New(),
		OperationID: uuid.New(), OrderID: command.OrderID, Status: domain.StatusCommitted, OccurredAt: time.Now().UTC()}}
	service, _ := NewService(store)
	if _, err := service.Commit(context.Background(), command); !errors.Is(err, domain.ErrStoreUnavailable) {
		t.Fatalf("Commit() error = %v, want fail-closed store error", err)
	}
}

func TestServicePreservesStoreErrorIdentity(t *testing.T) {
	store := &fakeStore{err: domain.ErrStoreUnavailable}
	service, _ := NewService(store)
	command := domain.CommitCommand{
		SchemaVersion: domain.SchemaVersion,
		EventID:       uuid.New(), OperationID: uuid.New(), OrderID: uuid.New(),
		OccurredAt: time.Now().UTC(),
		Lines:      []domain.Line{{OrderLineID: uuid.New(), ProductID: uuid.New(), Quantity: 1}},
	}
	_, err := service.Commit(context.Background(), command)
	if !errors.Is(err, domain.ErrStoreUnavailable) {
		t.Fatalf("Commit() error = %v, want ErrStoreUnavailable", err)
	}
}

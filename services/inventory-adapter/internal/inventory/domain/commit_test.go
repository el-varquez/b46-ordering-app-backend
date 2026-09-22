package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCommitCommandFingerprintIsCanonical(t *testing.T) {
	command := validCommand()
	want, err := command.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint() error = %v", err)
	}
	command.Lines[0], command.Lines[1] = command.Lines[1], command.Lines[0]
	got, err := command.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint() reordered error = %v", err)
	}
	if got != want {
		t.Fatalf("Fingerprint() = %q, want %q", got, want)
	}

	command.Lines[0].Quantity++
	changed, err := command.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint() changed error = %v", err)
	}
	if changed == want {
		t.Fatal("Fingerprint() did not change with request content")
	}
}

func TestCommitCommandRejectsInvalidInput(t *testing.T) {
	tests := map[string]func(*CommitCommand){
		"schema":            func(value *CommitCommand) { value.SchemaVersion = "2.0" },
		"event ID":          func(value *CommitCommand) { value.EventID = uuid.Nil },
		"operation ID":      func(value *CommitCommand) { value.OperationID = uuid.Nil },
		"order ID":          func(value *CommitCommand) { value.OrderID = uuid.Nil },
		"occurrence":        func(value *CommitCommand) { value.OccurredAt = time.Time{} },
		"empty lines":       func(value *CommitCommand) { value.Lines = nil },
		"line ID":           func(value *CommitCommand) { value.Lines[0].OrderLineID = uuid.Nil },
		"product ID":        func(value *CommitCommand) { value.Lines[0].ProductID = uuid.Nil },
		"quantity":          func(value *CommitCommand) { value.Lines[0].Quantity = 0 },
		"duplicate line":    func(value *CommitCommand) { value.Lines[1].OrderLineID = value.Lines[0].OrderLineID },
		"duplicate product": func(value *CommitCommand) { value.Lines[1].ProductID = value.Lines[0].ProductID },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			command := validCommand()
			mutate(&command)
			if err := command.Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Validate() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCommitResultRejectsMalformedUnavailableItems(t *testing.T) {
	base := CommitResult{SchemaVersion: SchemaVersion, EventID: uuid.New(), OperationID: uuid.New(),
		OrderID: uuid.New(), Status: StatusItemsUnavailable, OccurredAt: time.Now().UTC(),
		UnavailableItems: []UnavailableItem{{OrderLineID: uuid.New(), ProductID: uuid.New(), RequestedQuantity: 2}}}
	tests := map[string]func(*CommitResult){
		"line ID":            func(value *CommitResult) { value.UnavailableItems[0].OrderLineID = uuid.Nil },
		"product ID":         func(value *CommitResult) { value.UnavailableItems[0].ProductID = uuid.Nil },
		"requested quantity": func(value *CommitResult) { value.UnavailableItems[0].RequestedQuantity = 0 },
		"available quantity": func(value *CommitResult) { value.UnavailableItems[0].AvailableQuantity = -1 },
		"duplicate line": func(value *CommitResult) {
			value.UnavailableItems = append(value.UnavailableItems, value.UnavailableItems[0])
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := base
			value.UnavailableItems = append([]UnavailableItem(nil), base.UnavailableItems...)
			mutate(&value)
			if err := value.Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Validate() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func validCommand() CommitCommand {
	return CommitCommand{
		SchemaVersion: SchemaVersion,
		EventID:       uuid.MustParse("10000000-0000-4000-8000-000000000001"),
		OperationID:   uuid.MustParse("20000000-0000-4000-8000-000000000001"),
		OrderID:       uuid.MustParse("30000000-0000-4000-8000-000000000001"),
		OccurredAt:    time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC),
		Lines: []Line{
			{OrderLineID: uuid.MustParse("40000000-0000-4000-8000-000000000001"), ProductID: uuid.MustParse("50000000-0000-4000-8000-000000000001"), Quantity: 2},
			{OrderLineID: uuid.MustParse("40000000-0000-4000-8000-000000000002"), ProductID: uuid.MustParse("50000000-0000-4000-8000-000000000002"), Quantity: 1},
		},
	}
}

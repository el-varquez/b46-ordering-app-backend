package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const SchemaVersion = "1.0"

var (
	ErrInvalidInput      = errors.New("invalid inventory commit input")
	ErrOperationConflict = errors.New("inventory operation conflict")
	ErrStoreUnavailable  = errors.New("inventory store unavailable")
)

type ResultStatus string

const (
	StatusCommitted        ResultStatus = "COMMITTED"
	StatusItemsUnavailable ResultStatus = "ITEMS_UNAVAILABLE"
)

type Line struct {
	OrderLineID uuid.UUID
	ProductID   uuid.UUID
	Quantity    int
}

type CommitCommand struct {
	SchemaVersion string
	EventID       uuid.UUID
	OperationID   uuid.UUID
	OrderID       uuid.UUID
	OccurredAt    time.Time
	Lines         []Line
}

type UnavailableItem struct {
	OrderLineID       uuid.UUID
	ProductID         uuid.UUID
	RequestedQuantity int
	AvailableQuantity int
}

type CommitResult struct {
	SchemaVersion    string
	EventID          uuid.UUID
	OperationID      uuid.UUID
	OrderID          uuid.UUID
	Status           ResultStatus
	OccurredAt       time.Time
	UnavailableItems []UnavailableItem
}

func (command CommitCommand) Validate() error {
	if command.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version", ErrInvalidInput)
	}
	if command.EventID == uuid.Nil || command.OperationID == uuid.Nil || command.OrderID == uuid.Nil {
		return fmt.Errorf("%w: event, operation, and order IDs must be non-zero UUIDs", ErrInvalidInput)
	}
	if command.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidInput)
	}
	if len(command.Lines) < 1 || len(command.Lines) > 100 {
		return fmt.Errorf("%w: lines must contain 1 to 100 items", ErrInvalidInput)
	}

	lineIDs := make(map[uuid.UUID]struct{}, len(command.Lines))
	productIDs := make(map[uuid.UUID]struct{}, len(command.Lines))
	for _, line := range command.Lines {
		if line.OrderLineID == uuid.Nil || line.ProductID == uuid.Nil {
			return fmt.Errorf("%w: line identifiers must be non-zero UUIDs", ErrInvalidInput)
		}
		if line.Quantity < 1 || line.Quantity > 100 {
			return fmt.Errorf("%w: quantity must be between 1 and 100", ErrInvalidInput)
		}
		if _, exists := lineIDs[line.OrderLineID]; exists {
			return fmt.Errorf("%w: duplicate order_line_id", ErrInvalidInput)
		}
		if _, exists := productIDs[line.ProductID]; exists {
			return fmt.Errorf("%w: duplicate product_id", ErrInvalidInput)
		}
		lineIDs[line.OrderLineID] = struct{}{}
		productIDs[line.ProductID] = struct{}{}
	}
	return nil
}

func (command CommitCommand) Fingerprint() (string, error) {
	if err := command.Validate(); err != nil {
		return "", err
	}

	lines := append([]Line(nil), command.Lines...)
	sort.Slice(lines, func(i, j int) bool {
		return lines[i].OrderLineID.String() < lines[j].OrderLineID.String()
	})

	var canonical strings.Builder
	fmt.Fprintf(
		&canonical,
		"%s|%s|%s|%s|%s",
		command.SchemaVersion,
		command.EventID,
		command.OperationID,
		command.OrderID,
		command.OccurredAt.UTC().Format(time.RFC3339Nano),
	)
	for _, line := range lines {
		fmt.Fprintf(&canonical, "|%s:%s:%d", line.OrderLineID, line.ProductID, line.Quantity)
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:]), nil
}

func (result CommitResult) Validate() error {
	if result.SchemaVersion != SchemaVersion || result.EventID == uuid.Nil ||
		result.OperationID == uuid.Nil || result.OrderID == uuid.Nil || result.OccurredAt.IsZero() {
		return fmt.Errorf("%w: invalid inventory result metadata", ErrInvalidInput)
	}
	switch result.Status {
	case StatusCommitted:
		if len(result.UnavailableItems) != 0 {
			return fmt.Errorf("%w: committed result cannot contain unavailable items", ErrInvalidInput)
		}
	case StatusItemsUnavailable:
		if len(result.UnavailableItems) == 0 {
			return fmt.Errorf("%w: unavailable result requires items", ErrInvalidInput)
		}
		lineIDs := make(map[uuid.UUID]struct{}, len(result.UnavailableItems))
		for _, item := range result.UnavailableItems {
			if item.OrderLineID == uuid.Nil || item.ProductID == uuid.Nil ||
				item.RequestedQuantity < 1 || item.AvailableQuantity < 0 {
				return fmt.Errorf("%w: invalid unavailable item", ErrInvalidInput)
			}
			if _, exists := lineIDs[item.OrderLineID]; exists {
				return fmt.Errorf("%w: duplicate unavailable order_line_id", ErrInvalidInput)
			}
			lineIDs[item.OrderLineID] = struct{}{}
		}
	default:
		return fmt.Errorf("%w: invalid result status", ErrInvalidInput)
	}
	return nil
}

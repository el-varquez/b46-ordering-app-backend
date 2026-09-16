// Package inventorycontract contains only the versioned wire types exchanged
// between Ordering and the future inventory adapter. It contains no inventory
// business decisions or adapter implementation.
package inventorycontract

import (
	"encoding/json"
	"fmt"
	"io"
)

const SchemaVersion = "1.0"

type ItemRequest struct {
	OrderLineID string `json:"order_line_id"`
	ProductID   string `json:"product_id"`
	Quantity    int    `json:"quantity"`
}

type CommitRequested struct {
	SchemaVersion string        `json:"schema_version"`
	EventID       string        `json:"event_id"`
	OperationID   string        `json:"operation_id"`
	OrderID       string        `json:"order_id"`
	OccurredAt    string        `json:"occurred_at"`
	Items         []ItemRequest `json:"items"`
}

type Committed struct {
	SchemaVersion string `json:"schema_version"`
	EventID       string `json:"event_id"`
	OperationID   string `json:"operation_id"`
	OrderID       string `json:"order_id"`
	Result        string `json:"result"`
	OccurredAt    string `json:"occurred_at"`
}

type UnavailableItem struct {
	OrderLineID       string `json:"order_line_id"`
	ProductID         string `json:"product_id"`
	RequestedQuantity int    `json:"requested_quantity"`
	AvailableQuantity int    `json:"available_quantity"`
}

type ItemsUnavailable struct {
	SchemaVersion string            `json:"schema_version"`
	EventID       string            `json:"event_id"`
	OperationID   string            `json:"operation_id"`
	OrderID       string            `json:"order_id"`
	Result        string            `json:"result"`
	OccurredAt    string            `json:"occurred_at"`
	Items         []UnavailableItem `json:"items"`
}

// DecodeStrict rejects drift such as unknown fields in versioned fixtures.
func DecodeStrict(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("contract contains multiple JSON values")
		}
		return fmt.Errorf("read trailing contract data: %w", err)
	}
	return nil
}

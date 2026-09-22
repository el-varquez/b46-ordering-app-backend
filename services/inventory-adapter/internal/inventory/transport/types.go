package transport

import (
	"encoding/json"
	"fmt"
	"io"
)

type itemRequest struct {
	OrderLineID string `json:"order_line_id"`
	ProductID   string `json:"product_id"`
	Quantity    int    `json:"quantity"`
}

type commitRequest struct {
	SchemaVersion string        `json:"schema_version"`
	EventID       string        `json:"event_id"`
	OperationID   string        `json:"operation_id"`
	OrderID       string        `json:"order_id"`
	OccurredAt    string        `json:"occurred_at"`
	Items         []itemRequest `json:"items"`
}

type unavailableItem struct {
	OrderLineID       string `json:"order_line_id"`
	ProductID         string `json:"product_id"`
	RequestedQuantity int    `json:"requested_quantity"`
	AvailableQuantity int    `json:"available_quantity"`
}

type commitResponse struct {
	SchemaVersion string            `json:"schema_version"`
	EventID       string            `json:"event_id"`
	OperationID   string            `json:"operation_id"`
	OrderID       string            `json:"order_id"`
	Result        string            `json:"result"`
	OccurredAt    string            `json:"occurred_at"`
	Items         []unavailableItem `json:"items,omitempty"`
}

type errorResponse struct {
	Code string `json:"code"`
}

func decodeStrict(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return fmt.Errorf("read trailing JSON: %w", err)
	}
	return nil
}

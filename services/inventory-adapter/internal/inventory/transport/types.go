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

type catalogProductResponse struct {
	ProductID       string `json:"product_id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	PriceCentavos   int64  `json:"price_centavos"`
	CategoryID      string `json:"category_id"`
	CategoryName    string `json:"category_name"`
	Available       bool   `json:"available"`
	SourceUpdatedAt string `json:"source_updated_at"`
}

type catalogPageResponse struct {
	Products    []catalogProductResponse `json:"products"`
	NextAfterID string                   `json:"next_after_id"`
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

package transport

import (
	"fmt"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/domain"
	"github.com/google/uuid"
)

func requestToDomain(value commitRequest) (domain.CommitCommand, error) {
	eventID, err := uuid.Parse(value.EventID)
	if err != nil {
		return domain.CommitCommand{}, fmt.Errorf("event_id: %w", err)
	}
	operationID, err := uuid.Parse(value.OperationID)
	if err != nil {
		return domain.CommitCommand{}, fmt.Errorf("operation_id: %w", err)
	}
	orderID, err := uuid.Parse(value.OrderID)
	if err != nil {
		return domain.CommitCommand{}, fmt.Errorf("order_id: %w", err)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, value.OccurredAt)
	if err != nil {
		return domain.CommitCommand{}, fmt.Errorf("occurred_at: %w", err)
	}
	command := domain.CommitCommand{SchemaVersion: value.SchemaVersion, EventID: eventID,
		OperationID: operationID, OrderID: orderID, OccurredAt: occurredAt,
		Lines: make([]domain.Line, 0, len(value.Items))}
	for _, item := range value.Items {
		lineID, lineErr := uuid.Parse(item.OrderLineID)
		if lineErr != nil {
			return domain.CommitCommand{}, fmt.Errorf("order_line_id: %w", lineErr)
		}
		productID, productErr := uuid.Parse(item.ProductID)
		if productErr != nil {
			return domain.CommitCommand{}, fmt.Errorf("product_id: %w", productErr)
		}
		command.Lines = append(command.Lines, domain.Line{OrderLineID: lineID, ProductID: productID, Quantity: item.Quantity})
	}
	if err := command.Validate(); err != nil {
		return domain.CommitCommand{}, err
	}
	return command, nil
}

func resultFromDomain(value domain.CommitResult) commitResponse {
	response := commitResponse{SchemaVersion: value.SchemaVersion, EventID: value.EventID.String(),
		OperationID: value.OperationID.String(), OrderID: value.OrderID.String(), Result: string(value.Status),
		OccurredAt: value.OccurredAt.UTC().Format(time.RFC3339Nano)}
	for _, item := range value.UnavailableItems {
		response.Items = append(response.Items, unavailableItem{OrderLineID: item.OrderLineID.String(), ProductID: item.ProductID.String(),
			RequestedQuantity: item.RequestedQuantity, AvailableQuantity: item.AvailableQuantity})
	}
	return response
}

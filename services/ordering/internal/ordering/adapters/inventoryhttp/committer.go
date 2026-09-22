package inventoryhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/adapters/inventorycontract"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
	"github.com/google/uuid"
)

type doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Committer struct {
	client           doer
	endpoint         string
	token            string
	maxResponseBytes int64
}

func New(client doer, baseURL, token string, maxResponseBytes int64) (*Committer, error) {
	if client == nil || token == "" || maxResponseBytes <= 0 {
		return nil, errors.New("inventory HTTP adapter requires client, token, and response limit")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("inventory adapter URL must be absolute HTTP(S)")
	}
	endpoint, err := url.JoinPath(parsed.String(), "inventory", "commit")
	if err != nil {
		return nil, fmt.Errorf("build inventory endpoint: %w", err)
	}
	return &Committer{client: client, endpoint: endpoint, token: token, maxResponseBytes: maxResponseBytes}, nil
}

func (committer *Committer) Commit(ctx context.Context, value domain.InventoryCommit) (domain.InventoryResult, error) {
	wire := inventorycontract.CommitRequested{SchemaVersion: inventorycontract.SchemaVersion,
		EventID: value.EventID, OperationID: value.OperationID, OrderID: value.OrderID,
		OccurredAt: value.OccurredAt.UTC().Format(time.RFC3339Nano),
		Items:      make([]inventorycontract.ItemRequest, 0, len(value.Items))}
	for _, item := range value.Items {
		wire.Items = append(wire.Items, inventorycontract.ItemRequest{OrderLineID: item.OrderLineID,
			ProductID: item.ProductID, Quantity: item.Quantity})
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return domain.InventoryResult{}, classified(CodeContractRejected, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, committer.endpoint, bytes.NewReader(body))
	if err != nil {
		return domain.InventoryResult{}, classified(CodeContractRejected, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+committer.token)
	request.Header.Set("X-Operation-ID", value.OperationID)

	response, err := committer.client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return domain.InventoryResult{}, classified(CodeTimeout, err)
		}
		return domain.InventoryResult{}, classified(CodeUnreachable, err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, committer.maxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return domain.InventoryResult{}, classified(CodeInvalidResponse, err)
	}
	if int64(len(responseBody)) > committer.maxResponseBytes {
		return domain.InventoryResult{}, classified(CodeInvalidResponse, errors.New("inventory response exceeds limit"))
	}
	if response.StatusCode != http.StatusOK {
		return domain.InventoryResult{}, statusError(response.StatusCode)
	}
	return decodeResult(responseBody, value)
}

func decodeResult(body []byte, request domain.InventoryCommit) (domain.InventoryResult, error) {
	var envelope struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return domain.InventoryResult{}, classified(CodeInvalidResponse, err)
	}
	switch envelope.Result {
	case string(domain.InventoryCommitted):
		var wire inventorycontract.Committed
		if err := inventorycontract.DecodeStrict(bytes.NewReader(body), &wire); err != nil {
			return domain.InventoryResult{}, classified(CodeInvalidResponse, err)
		}
		return validateResult(wire.SchemaVersion, wire.EventID, wire.OperationID, wire.OrderID,
			wire.Result, wire.OccurredAt, nil, request)
	case string(domain.InventoryItemsUnavailable):
		var wire inventorycontract.ItemsUnavailable
		if err := inventorycontract.DecodeStrict(bytes.NewReader(body), &wire); err != nil {
			return domain.InventoryResult{}, classified(CodeInvalidResponse, err)
		}
		items := make([]domain.UnavailableItem, 0, len(wire.Items))
		for _, item := range wire.Items {
			items = append(items, domain.UnavailableItem{OrderLineID: item.OrderLineID, ProductID: item.ProductID,
				RequestedQuantity: item.RequestedQuantity, AvailableQuantity: item.AvailableQuantity})
		}
		return validateResult(wire.SchemaVersion, wire.EventID, wire.OperationID, wire.OrderID,
			wire.Result, wire.OccurredAt, items, request)
	default:
		return domain.InventoryResult{}, classified(CodeInvalidResponse, errors.New("unknown inventory result"))
	}
}

func validateResult(schemaVersion, eventID, operationID, orderID, status, occurredAt string,
	unavailable []domain.UnavailableItem, request domain.InventoryCommit,
) (domain.InventoryResult, error) {
	if schemaVersion != inventorycontract.SchemaVersion || operationID != request.OperationID || orderID != request.OrderID {
		return domain.InventoryResult{}, classified(CodeInvalidResponse, errors.New("inventory response identity mismatch"))
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return domain.InventoryResult{}, classified(CodeInvalidResponse, errors.New("inventory response event ID is invalid"))
	}
	when, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return domain.InventoryResult{}, classified(CodeInvalidResponse, errors.New("inventory response timestamp is invalid"))
	}
	requested := make(map[string]domain.InventoryItem, len(request.Items))
	for _, item := range request.Items {
		requested[item.OrderLineID] = item
	}
	if status == string(domain.InventoryItemsUnavailable) && len(unavailable) == 0 {
		return domain.InventoryResult{}, classified(CodeInvalidResponse, errors.New("inventory unavailable result has no items"))
	}
	seen := make(map[string]struct{}, len(unavailable))
	for _, item := range unavailable {
		expected, exists := requested[item.OrderLineID]
		if !exists || expected.ProductID != item.ProductID || expected.Quantity != item.RequestedQuantity || item.AvailableQuantity < 0 {
			return domain.InventoryResult{}, classified(CodeInvalidResponse, errors.New("inventory unavailable item mismatch"))
		}
		if _, duplicate := seen[item.OrderLineID]; duplicate {
			return domain.InventoryResult{}, classified(CodeInvalidResponse, errors.New("inventory unavailable item is duplicated"))
		}
		seen[item.OrderLineID] = struct{}{}
	}
	return domain.InventoryResult{EventID: eventID, OperationID: operationID, OrderID: orderID,
		Status: domain.InventoryResultStatus(status), OccurredAt: when.UTC(), UnavailableItems: unavailable}, nil
}

func statusError(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return classified(CodeUnauthorized, fmt.Errorf("inventory HTTP status %d", status))
	case status == http.StatusConflict:
		return classified(CodeOperationConflict, fmt.Errorf("inventory HTTP status %d", status))
	case status == http.StatusTooManyRequests:
		return classified(CodeThrottled, fmt.Errorf("inventory HTTP status %d", status))
	case status >= 500:
		return classified(CodeServerError, fmt.Errorf("inventory HTTP status %d", status))
	case status >= 400:
		return classified(CodeContractRejected, fmt.Errorf("inventory HTTP status %d", status))
	default:
		return classified(CodeInvalidResponse, fmt.Errorf("inventory HTTP status %d", status))
	}
}

func isTimeout(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

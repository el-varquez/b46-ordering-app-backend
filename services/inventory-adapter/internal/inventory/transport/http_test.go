package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/domain"
	"github.com/google/uuid"
)

const testToken = "test-token-with-at-least-thirty-two-bytes"

type fakeService struct {
	result        domain.CommitResult
	err           error
	calls         int
	catalogResult domain.CatalogPage
	catalogErr    error
	catalogCalls  int
}

func (service *fakeService) Commit(_ context.Context, _ domain.CommitCommand) (domain.CommitResult, error) {
	service.calls++
	return service.result, service.err
}

func (service *fakeService) Catalog(_ context.Context, _ domain.CatalogQuery) (domain.CatalogPage, error) {
	service.catalogCalls++
	return service.catalogResult, service.catalogErr
}

func TestCatalogRequiresAuthenticationAndHidesStockQuantity(t *testing.T) {
	service := &fakeService{catalogResult: domain.CatalogPage{Products: []domain.CatalogProduct{{
		ProductID: uuid.MustParse("b4600000-0000-4000-8001-000000000001"), Name: "Coke", Description: "Cold",
		PriceCentavos: 8200, CategoryID: uuid.MustParse("b4600000-0000-4000-8002-000000000001"),
		CategoryName: "Drinks", Available: true, SourceUpdatedAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC),
	}}}}
	handler := testHandler(t, service, 4096)
	request := httptest.NewRequest(http.MethodGet, "/catalog/products?limit=20", nil)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized || service.catalogCalls != 0 {
		t.Fatalf("unauthorized response = %d, calls = %d", unauthorized.Code, service.catalogCalls)
	}
	request = httptest.NewRequest(http.MethodGet, "/catalog/products?limit=20", nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"price_centavos":8200`) || strings.Contains(response.Body.String(), "stock") {
		t.Fatalf("catalog response = %d %s", response.Code, response.Body.String())
	}
}

func TestCommitRequiresAuthenticationAndStrictContract(t *testing.T) {
	service := &fakeService{}
	handler := testHandler(t, service, 4096)

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, testRequest(validRequestBody()))
	if unauthorized.Code != http.StatusUnauthorized || service.calls != 0 {
		t.Fatalf("unauthorized status = %d, calls = %d", unauthorized.Code, service.calls)
	}

	request := testRequest(strings.Replace(validRequestBody(), `"items":`, `"unknown":true,"items":`, 1))
	request.Header.Set("Authorization", "Bearer "+testToken)
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, request)
	if invalid.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("invalid status = %d, calls = %d", invalid.Code, service.calls)
	}
}

func TestCommitReturnsCommittedAndUnavailableResults(t *testing.T) {
	requestWire := decodeRequest(t, validRequestBody())
	service := &fakeService{result: domain.CommitResult{
		SchemaVersion: domain.SchemaVersion,
		EventID:       uuid.New(), OperationID: uuid.MustParse(requestWire.OperationID), OrderID: uuid.MustParse(requestWire.OrderID),
		Status: domain.StatusCommitted, OccurredAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC),
	}}
	handler := testHandler(t, service, 4096)
	response := performAuthorized(handler, validRequestBody())
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"result":"COMMITTED"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}

	service.result.Status = domain.StatusItemsUnavailable
	service.result.UnavailableItems = []domain.UnavailableItem{{
		OrderLineID: uuid.MustParse(requestWire.Items[0].OrderLineID),
		ProductID:   uuid.MustParse(requestWire.Items[0].ProductID), RequestedQuantity: 2, AvailableQuantity: 0,
	}}
	response = performAuthorized(handler, validRequestBody())
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"result":"ITEMS_UNAVAILABLE"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestCommitMapsSafeErrorsAndBodyLimit(t *testing.T) {
	service := &fakeService{err: domain.ErrOperationConflict}
	handler := testHandler(t, service, 4096)
	if got := performAuthorized(handler, validRequestBody()).Code; got != http.StatusConflict {
		t.Fatalf("conflict status = %d", got)
	}
	service.err = domain.ErrStoreUnavailable
	if got := performAuthorized(handler, validRequestBody()).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("store status = %d", got)
	}

	tinyHandler := testHandler(t, &fakeService{}, 32)
	if got := performAuthorized(tinyHandler, validRequestBody()).Code; got != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d", got)
	}
}

func testHandler(t *testing.T, service *fakeService, maxBody int64) http.Handler {
	t.Helper()
	routes, err := New(service, sha256.Sum256([]byte(testToken)), maxBody, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	mux := http.NewServeMux()
	routes.Register(mux)
	return mux
}

func testRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/inventory/commit", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func performAuthorized(handler http.Handler, body string) *httptest.ResponseRecorder {
	request := testRequest(body)
	request.Header.Set("Authorization", "Bearer "+testToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeRequest(t *testing.T, body string) commitRequest {
	t.Helper()
	var value commitRequest
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return value
}

func validRequestBody() string {
	return `{"schema_version":"1.0","event_id":"10000000-0000-4000-8000-000000000001","operation_id":"20000000-0000-4000-8000-000000000001","order_id":"30000000-0000-4000-8000-000000000001","occurred_at":"2026-09-20T10:00:00Z","items":[{"order_line_id":"40000000-0000-4000-8000-000000000001","product_id":"50000000-0000-4000-8000-000000000001","quantity":2}]}`
}

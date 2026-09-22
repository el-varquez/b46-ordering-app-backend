package inventoryhttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

const clientTestToken = "client-test-token-with-at-least-32-bytes"

func TestCommitterMapsCommittedResultAndSendsOneRequest(t *testing.T) {
	requestValue := inventoryRequest()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/inventory/commit" || request.Header.Get("Authorization") != "Bearer "+clientTestToken {
			t.Fatalf("request path/authorization = %s/%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"schema_version":"1.0","event_id":"90000000-0000-4000-8000-000000000001","operation_id":"20000000-0000-4000-8000-000000000001","order_id":"30000000-0000-4000-8000-000000000001","result":"COMMITTED","occurred_at":"2026-09-20T12:00:00Z"}`)
	}))
	defer server.Close()
	committer, err := New(server.Client(), server.URL, clientTestToken, 4096)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := committer.Commit(context.Background(), requestValue)
	if err != nil || result.Status != domain.InventoryCommitted || calls.Load() != 1 {
		t.Fatalf("Commit() result = %#v, error = %v, calls = %d", result, err, calls.Load())
	}
}

func TestCommitterMapsUnavailableAndRejectsIdentityMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"schema_version":"1.0","event_id":"90000000-0000-4000-8000-000000000001","operation_id":"20000000-0000-4000-8000-000000000001","order_id":"30000000-0000-4000-8000-000000000001","result":"ITEMS_UNAVAILABLE","occurred_at":"2026-09-20T12:00:00Z","items":[{"order_line_id":"40000000-0000-4000-8000-000000000001","product_id":"50000000-0000-4000-8000-000000000001","requested_quantity":2,"available_quantity":0}]}`)
	}))
	defer server.Close()
	committer, _ := New(server.Client(), server.URL, clientTestToken, 4096)
	result, err := committer.Commit(context.Background(), inventoryRequest())
	if err != nil || result.Status != domain.InventoryItemsUnavailable || len(result.UnavailableItems) != 1 {
		t.Fatalf("Commit() result = %#v, error = %v", result, err)
	}

	changed := inventoryRequest()
	changed.OrderID = "30000000-0000-4000-8000-000000000099"
	_, err = committer.Commit(context.Background(), changed)
	var classifiedError *Error
	if !errors.As(err, &classifiedError) || classifiedError.Code() != CodeInvalidResponse {
		t.Fatalf("Commit() mismatch error = %v", err)
	}
}

func TestCommitterClassifiesStatusAndOversizedResponse(t *testing.T) {
	tests := map[int]string{400: CodeContractRejected, 401: CodeUnauthorized, 409: CodeOperationConflict,
		429: CodeThrottled, 500: CodeServerError}
	for status, wantCode := range tests {
		t.Run(wantCode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(status) }))
			defer server.Close()
			committer, _ := New(server.Client(), server.URL, clientTestToken, 4096)
			_, err := committer.Commit(context.Background(), inventoryRequest())
			var classifiedError *Error
			if !errors.As(err, &classifiedError) || classifiedError.Code() != wantCode {
				t.Fatalf("Commit() error = %v, want %s", err, wantCode)
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, strings.Repeat("x", 100))
	}))
	defer server.Close()
	committer, _ := New(server.Client(), server.URL, clientTestToken, 32)
	_, err := committer.Commit(context.Background(), inventoryRequest())
	var classifiedError *Error
	if !errors.As(err, &classifiedError) || classifiedError.Code() != CodeInvalidResponse {
		t.Fatalf("oversized Commit() error = %v", err)
	}
}

func TestCommitterClassifiesTimeoutAndUnreachableWithoutRetrying(t *testing.T) {
	tests := []struct {
		name     string
		doer     doer
		wantCode string
	}{
		{name: "timeout", doer: &errorDoer{err: timeoutError{}}, wantCode: CodeTimeout},
		{name: "unreachable", doer: &errorDoer{err: errors.New("connection refused")}, wantCode: CodeUnreachable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			committer, err := New(test.doer, "http://inventory.test", clientTestToken, 4096)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = committer.Commit(context.Background(), inventoryRequest())
			var classifiedError *Error
			if !errors.As(err, &classifiedError) || classifiedError.Code() != test.wantCode {
				t.Fatalf("Commit() error = %v, want %s", err, test.wantCode)
			}
			if test.doer.(*errorDoer).calls != 1 {
				t.Fatalf("Do() calls = %d, want 1", test.doer.(*errorDoer).calls)
			}
		})
	}
}

func TestCommitterRejectsMalformedUnavailableResults(t *testing.T) {
	tests := map[string]string{
		"empty items":    `{"schema_version":"1.0","event_id":"90000000-0000-4000-8000-000000000001","operation_id":"20000000-0000-4000-8000-000000000001","order_id":"30000000-0000-4000-8000-000000000001","result":"ITEMS_UNAVAILABLE","occurred_at":"2026-09-20T12:00:00Z","items":[]}`,
		"duplicate item": `{"schema_version":"1.0","event_id":"90000000-0000-4000-8000-000000000001","operation_id":"20000000-0000-4000-8000-000000000001","order_id":"30000000-0000-4000-8000-000000000001","result":"ITEMS_UNAVAILABLE","occurred_at":"2026-09-20T12:00:00Z","items":[{"order_line_id":"40000000-0000-4000-8000-000000000001","product_id":"50000000-0000-4000-8000-000000000001","requested_quantity":2,"available_quantity":0},{"order_line_id":"40000000-0000-4000-8000-000000000001","product_id":"50000000-0000-4000-8000-000000000001","requested_quantity":2,"available_quantity":0}]}`,
		"trailing JSON":  `{"result":"COMMITTED"}{}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, body)
			}))
			defer server.Close()
			committer, _ := New(server.Client(), server.URL, clientTestToken, 4096)
			_, err := committer.Commit(context.Background(), inventoryRequest())
			var classifiedError *Error
			if !errors.As(err, &classifiedError) || classifiedError.Code() != CodeInvalidResponse {
				t.Fatalf("Commit() error = %v, want %s", err, CodeInvalidResponse)
			}
		})
	}
}

type errorDoer struct {
	err   error
	calls int
}

func (doer *errorDoer) Do(*http.Request) (*http.Response, error) {
	doer.calls++
	return nil, doer.err
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}

func inventoryRequest() domain.InventoryCommit {
	return domain.InventoryCommit{EventID: "10000000-0000-4000-8000-000000000001",
		OperationID: "20000000-0000-4000-8000-000000000001", OrderID: "30000000-0000-4000-8000-000000000001",
		OccurredAt: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC),
		Items: []domain.InventoryItem{{OrderLineID: "40000000-0000-4000-8000-000000000001",
			ProductID: "50000000-0000-4000-8000-000000000001", Quantity: 2}}}
}

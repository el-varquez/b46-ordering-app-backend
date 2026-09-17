package httpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type healthCheck func(context.Context) error

func (check healthCheck) Check(ctx context.Context) error { return check(ctx) }

func testHandler(check HealthChecker) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(logger, check, 1024)
}

func TestLivenessDoesNotCallReadiness(t *testing.T) {
	called := false
	handler := testHandler(healthCheck(func(context.Context) error {
		called = true
		return errors.New("database unavailable")
	}))

	request := httptest.NewRequest(http.MethodGet, "/v1/health/live", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if called {
		t.Fatal("liveness called the readiness dependency")
	}
	if response.Header().Get(CorrelationHeader) == "" {
		t.Fatal("response does not contain a correlation ID")
	}
}

func TestReadinessReturnsSafeErrorEnvelope(t *testing.T) {
	handler := testHandler(healthCheck(func(context.Context) error {
		return errors.New("password=do-not-leak")
	}))

	request := httptest.NewRequest(http.MethodGet, "/v1/health/ready", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(response.Body.String(), "do-not-leak") {
		t.Fatalf("response leaked internal error: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "SERVICE_UNAVAILABLE") {
		t.Fatalf("response = %s, want stable error code", response.Body.String())
	}
}

func TestValidInboundCorrelationIDIsReturned(t *testing.T) {
	handler := testHandler(healthCheck(func(context.Context) error { return nil }))
	request := httptest.NewRequest(http.MethodGet, "/v1/health/live", nil)
	request.Header.Set(CorrelationHeader, "client-request-123")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got := response.Header().Get(CorrelationHeader); got != "client-request-123" {
		t.Fatalf("correlation ID = %q, want client-request-123", got)
	}
}

func TestAccessLogOmitsCredentials(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := NewHandler(logger, healthCheck(func(context.Context) error { return nil }), 1024)
	request := httptest.NewRequest(
		http.MethodPost,
		"/missing",
		strings.NewReader(`{"password":"credential-secret","refresh_token":"refresh-secret"}`),
	)
	request.Header.Set("Authorization", "Bearer access-secret")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	logged := output.String()
	for _, secret := range []string{"credential-secret", "refresh-secret", "access-secret", "Authorization"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("access log leaked %q: %s", secret, logged)
		}
	}
	if !strings.Contains(logged, "correlation_id") {
		t.Fatalf("access log omitted correlation ID: %s", logged)
	}
}

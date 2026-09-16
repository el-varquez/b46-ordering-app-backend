package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

const CorrelationHeader = "X-Correlation-ID"

type correlationKey struct{}

func CorrelationID(ctx context.Context) string {
	value, _ := ctx.Value(correlationKey{}).(string)
	return value
}

func withCorrelation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		correlationID := strings.TrimSpace(request.Header.Get(CorrelationHeader))
		if !validCorrelationID(correlationID) {
			correlationID = newCorrelationID()
		}
		writer.Header().Set(CorrelationHeader, correlationID)
		ctx := context.WithValue(request.Context(), correlationKey{}, correlationID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func validCorrelationID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func newCorrelationID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))[:32]
	}
	return hex.EncodeToString(bytes)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	if recorder.status != 0 {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(body)
}

func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: writer}
		next.ServeHTTP(recorder, request)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.InfoContext(request.Context(), "http request",
			"correlation_id", CorrelationID(request.Context()),
			"method", request.Method,
			"route", request.URL.Path,
			"status", status,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(request.Context(), "recovered request panic",
					"correlation_id", CorrelationID(request.Context()),
					"error", recovered,
					"stack", string(debug.Stack()),
				)
				writeError(writer, request, http.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred.")
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func limitBody(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, maxBytes)
		next.ServeHTTP(writer, request)
	})
}

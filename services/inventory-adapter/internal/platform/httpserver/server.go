package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

type HealthChecker interface {
	Check(context.Context) error
}

type RouteRegistrar interface {
	Register(*http.ServeMux)
}

type Options struct {
	Address      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	Logger       *slog.Logger
	Readiness    HealthChecker
	Routes       []RouteRegistrar
}

func New(options Options) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"service": "inventory-adapter", "status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(writer http.ResponseWriter, request *http.Request) {
		if err := options.Readiness.Check(request.Context()); err != nil {
			options.Logger.WarnContext(request.Context(), "inventory adapter readiness failed", "reason", "store_not_ready")
			writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"code": "SERVICE_UNAVAILABLE"})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]string{"service": "inventory-adapter", "status": "ready"})
	})
	for _, routes := range options.Routes {
		routes.Register(mux)
	}
	handler := recoverPanics(options.Logger, mux)
	return &http.Server{Addr: options.Address, Handler: handler, ReadHeaderTimeout: options.ReadTimeout,
		ReadTimeout: options.ReadTimeout, WriteTimeout: options.WriteTimeout, IdleTimeout: options.IdleTimeout}
}

func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recover() != nil {
				logger.ErrorContext(request.Context(), "inventory adapter request panic")
				writeJSON(writer, http.StatusInternalServerError, map[string]string{"code": "INTERNAL_ERROR"})
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

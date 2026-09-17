package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// HealthChecker is implemented by the PostgreSQL adapter. Keeping it at this
// transport seam makes health behavior easy to test without a database.
type HealthChecker interface {
	Check(context.Context) error
}

type RouteRegistrar interface {
	Register(*http.ServeMux)
}

type Options struct {
	Address             string
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	MaxRequestBodyBytes int64
	Logger              *slog.Logger
	Readiness           HealthChecker
	Routes              []RouteRegistrar
}

func New(options Options) *http.Server {
	handler := NewHandler(options.Logger, options.Readiness, options.MaxRequestBodyBytes, options.Routes...)
	return &http.Server{
		Addr:              options.Address,
		Handler:           handler,
		ReadHeaderTimeout: options.ReadTimeout,
		ReadTimeout:       options.ReadTimeout,
		WriteTimeout:      options.WriteTimeout,
		IdleTimeout:       options.IdleTimeout,
	}
}

func NewHandler(
	logger *slog.Logger,
	readiness HealthChecker,
	maxRequestBodyBytes int64,
	routes ...RouteRegistrar,
) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health/live", func(writer http.ResponseWriter, request *http.Request) {
		writeSuccess(writer, request, http.StatusOK, map[string]string{
			"service": "ordering",
			"status":  "ok",
		})
	})
	mux.HandleFunc("GET /v1/health/ready", func(writer http.ResponseWriter, request *http.Request) {
		if err := readiness.Check(request.Context()); err != nil {
			logger.WarnContext(request.Context(), "readiness check failed",
				"correlation_id", CorrelationID(request.Context()),
				"reason", "database_not_ready",
			)
			writeError(writer, request, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "The service is not ready.")
			return
		}
		writeSuccess(writer, request, http.StatusOK, map[string]string{
			"service": "ordering",
			"status":  "ready",
		})
	})
	for _, registrar := range routes {
		registrar.Register(mux)
	}
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		writeError(writer, request, http.StatusNotFound, "NOT_FOUND", "The requested resource was not found.")
	})

	return withCorrelation(accessLog(logger, recoverPanics(logger, limitBody(maxRequestBodyBytes, mux))))
}

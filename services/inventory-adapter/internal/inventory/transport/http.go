package transport

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/domain"
	"github.com/google/uuid"
)

type commitService interface {
	Commit(context.Context, domain.CommitCommand) (domain.CommitResult, error)
}

type catalogService interface {
	Catalog(context.Context, domain.CatalogQuery) (domain.CatalogPage, error)
}

type Routes struct {
	service      commitService
	catalog      catalogService
	tokenDigest  [32]byte
	maxBodyBytes int64
	logger       *slog.Logger
}

func New(service commitService, tokenDigest [32]byte, maxBodyBytes int64, logger *slog.Logger) (*Routes, error) {
	if service == nil || maxBodyBytes <= 0 || logger == nil {
		return nil, errors.New("inventory transport: service, body limit, and logger are required")
	}
	catalog, _ := service.(catalogService)
	return &Routes{service: service, catalog: catalog, tokenDigest: tokenDigest, maxBodyBytes: maxBodyBytes, logger: logger}, nil
}

func (routes *Routes) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /inventory/commit", routes.commit)
	mux.HandleFunc("GET /catalog/products", routes.products)
}

func (routes *Routes) products(writer http.ResponseWriter, request *http.Request) {
	if !routes.authorized(request) {
		writeError(writer, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	if routes.catalog == nil {
		writeError(writer, http.StatusServiceUnavailable, "STORE_UNAVAILABLE")
		return
	}
	limit := 100
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		limit = value
	}
	afterID := uuid.Nil
	if raw := strings.TrimSpace(request.URL.Query().Get("after_id")); raw != "" {
		value, err := uuid.Parse(raw)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		afterID = value
	}
	page, err := routes.catalog.Catalog(request.Context(), domain.CatalogQuery{Limit: limit, AfterID: afterID})
	if err != nil {
		if errors.Is(err, domain.ErrInvalidCatalogQuery) {
			writeError(writer, http.StatusBadRequest, "INVALID_REQUEST")
			return
		}
		routes.logger.ErrorContext(request.Context(), "catalog read failed", "error_class", "internal")
		writeError(writer, http.StatusServiceUnavailable, "STORE_UNAVAILABLE")
		return
	}
	writeJSON(writer, http.StatusOK, catalogFromDomain(page))
}

func (routes *Routes) commit(writer http.ResponseWriter, request *http.Request) {
	if !routes.authorized(request) {
		writeError(writer, http.StatusUnauthorized, "UNAUTHORIZED")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "CONTENT_TYPE_REQUIRED")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, routes.maxBodyBytes)
	var wire commitRequest
	if err := decodeStrict(request.Body, &wire); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(writer, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE")
			return
		}
		writeError(writer, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	command, err := requestToDomain(wire)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	result, err := routes.service.Commit(request.Context(), command)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrOperationConflict):
			writeError(writer, http.StatusConflict, "OPERATION_CONFLICT")
		case errors.Is(err, domain.ErrStoreUnavailable):
			writeError(writer, http.StatusServiceUnavailable, "STORE_UNAVAILABLE")
		default:
			routes.logger.ErrorContext(request.Context(), "inventory commit failed", "operation_id", command.OperationID, "error_class", "internal")
			writeError(writer, http.StatusInternalServerError, "INTERNAL_ERROR")
		}
		return
	}
	writeJSON(writer, http.StatusOK, resultFromDomain(result))
}

func (routes *Routes) authorized(request *http.Request) bool {
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	presented := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
	return subtle.ConstantTimeCompare(routes.tokenDigest[:], presented[:]) == 1
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, errorResponse{Code: code})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

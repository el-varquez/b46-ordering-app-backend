package transport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/domain"
	identitydomain "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
	"github.com/google/uuid"
)

type CatalogApplication interface {
	Products(context.Context, int, string, int64) (domain.Page, error)
}

type Responder interface {
	Success(http.ResponseWriter, *http.Request, int, any)
	Error(http.ResponseWriter, *http.Request, int, string, string)
}

type Authorize func(
	identitydomain.Role,
	func(http.ResponseWriter, *http.Request, identitydomain.Principal),
) http.HandlerFunc

type Handler struct {
	catalog   CatalogApplication
	responses Responder
	authorize Authorize
}

func New(catalog CatalogApplication, responses Responder, authorize Authorize) *Handler {
	return &Handler{catalog: catalog, responses: responses, authorize: authorize}
}

func (handler *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/products", handler.authorize(identitydomain.RoleCustomer, handler.products))
}

func (handler *Handler) products(writer http.ResponseWriter, request *http.Request, _ identitydomain.Principal) {
	limit, ok := positiveInteger(request.URL.Query().Get("limit"), 20, 100)
	if !ok {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	afterID := strings.TrimSpace(request.URL.Query().Get("after_id"))
	if afterID != "" {
		if _, err := uuid.Parse(afterID); err != nil {
			handler.fail(writer, request, domain.ErrInvalidInput)
			return
		}
	}
	revision, ok := nonNegativeInteger(request.URL.Query().Get("snapshot_revision"))
	if !ok {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	page, err := handler.catalog.Products(request.Context(), limit, afterID, revision)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	products := make([]map[string]any, 0, len(page.Products))
	for _, product := range page.Products {
		var imageURL any
		if product.ImageURL != "" {
			imageURL = product.ImageURL
		}
		products = append(products, map[string]any{
			"product_id": product.ID, "name": product.Name, "description": product.Description,
			"price_centavos": product.PriceCentavos, "category_id": product.CategoryID,
			"category_name": product.CategoryName, "image_url": imageURL, "available": product.Available,
		})
	}
	handler.responses.Success(writer, request, http.StatusOK, map[string]any{
		"products": products, "snapshot_revision": page.SnapshotRevision,
		"after_id": page.NextAfterID, "has_more": page.NextAfterID != "",
	})
}

func (handler *Handler) fail(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		handler.responses.Error(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
	case errors.Is(err, domain.ErrSnapshotExpired):
		handler.responses.Error(writer, request, http.StatusConflict, "CATALOG_SNAPSHOT_EXPIRED", "Reload the product catalog and try again.")
	case errors.Is(err, domain.ErrUnavailable):
		handler.responses.Error(writer, request, http.StatusServiceUnavailable, "CATALOG_UNAVAILABLE", "The product catalog is temporarily unavailable.")
	default:
		handler.responses.Error(writer, request, http.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred.")
	}
}

func positiveInteger(raw string, fallback, maximum int) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= 1 && value <= maximum
}

func nonNegativeInteger(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil && value >= 0
}

package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/domain"
	identitydomain "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type catalogStub struct {
	page domain.Page
	err  error
}

func (catalog catalogStub) Products(context.Context, int, string, int64) (domain.Page, error) {
	return catalog.page, catalog.err
}

type responderStub struct {
	status int
	data   any
	code   string
}

func (responder *responderStub) Success(_ http.ResponseWriter, _ *http.Request, status int, data any) {
	responder.status, responder.data = status, data
}
func (responder *responderStub) Error(_ http.ResponseWriter, _ *http.Request, status int, code, _ string) {
	responder.status, responder.code = status, code
}

func TestProductsReturnsMobileSafePage(t *testing.T) {
	product := domain.Product{ID: "b4600000-0000-4000-8001-000000000001", Name: "Coke", PriceCentavos: 8200,
		CategoryID: "b4600000-0000-4000-8002-000000000001", CategoryName: "Drinks", Available: true, SourceUpdatedAt: time.Now()}
	responses := &responderStub{}
	handler := New(catalogStub{page: domain.Page{Products: []domain.Product{product}, SnapshotRevision: 7, NextAfterID: product.ID}}, responses, allowCustomer)
	mux := http.NewServeMux()
	handler.Register(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/products?limit=1", nil))
	if responses.status != http.StatusOK {
		t.Fatalf("status = %d", responses.status)
	}
	data := responses.data.(map[string]any)
	if data["snapshot_revision"] != int64(7) || data["has_more"] != true {
		t.Fatalf("data = %#v", data)
	}
	products := data["products"].([]map[string]any)
	value := products[0]
	if _, leaked := value["source_updated_at"]; leaked || value["price_centavos"] != int64(8200) {
		t.Fatalf("product = %#v", value)
	}
}

func TestProductsRejectsInvalidCursorAndExpiredSnapshot(t *testing.T) {
	for target, testCase := range map[string]struct {
		catalog  catalogStub
		expected int
	}{
		"/v1/products?after_id=bad":        {expected: http.StatusBadRequest},
		"/v1/products?snapshot_revision=1": {catalog: catalogStub{err: domain.ErrSnapshotExpired}, expected: http.StatusConflict},
	} {
		responses := &responderStub{}
		handler := New(testCase.catalog, responses, allowCustomer)
		mux := http.NewServeMux()
		handler.Register(mux)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if responses.status != testCase.expected {
			t.Fatalf("%s status = %d", target, responses.status)
		}
	}
}

func allowCustomer(_ identitydomain.Role, next func(http.ResponseWriter, *http.Request, identitydomain.Principal)) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		next(writer, request, identitydomain.Principal{})
	}
}

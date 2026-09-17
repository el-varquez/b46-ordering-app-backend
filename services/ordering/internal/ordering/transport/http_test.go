package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identitydomain "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

func TestPlaceOrderRejectsClientOwnedFields(t *testing.T) {
	application := &orderApplicationStub{}
	responses := &recordedOrderResponse{}
	handler := New(application, responses, allowRole)
	mux := http.NewServeMux()
	handler.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(`{
		"checkout_id":"00000000-0000-4000-8000-000000000001",
		"customer_id":"00000000-0000-4000-8000-000000000002",
		"lines":[{"product_id":"COKE","quantity":1,"expected_unit_price_centavos":8200}],
		"delivery_address":"Bria Homes"
	}`))

	mux.ServeHTTP(httptest.NewRecorder(), request)

	if responses.status != http.StatusBadRequest || responses.code != "INVALID_REQUEST" {
		t.Fatalf("response = (%d, %s), want (400, INVALID_REQUEST)", responses.status, responses.code)
	}
	if application.placeCalls != 0 {
		t.Fatalf("PlaceOrder() calls = %d, want 0", application.placeCalls)
	}
}

func TestPlaceOrderResponseHidesInternalLifecycleState(t *testing.T) {
	application := &orderApplicationStub{placeResult: domain.PlaceOrderResult{Order: domain.Order{
		ID:              "00000000-0000-4000-8000-000000000010",
		CheckoutID:      "00000000-0000-4000-8000-000000000001",
		CustomerID:      "00000000-0000-4000-8000-000000000002",
		Status:          domain.OrderSubmitted,
		TotalCentavos:   8200,
		DeliveryAddress: "Bria Homes",
		CreatedAt:       time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC),
		Lines: []domain.OrderLine{{
			ID: "00000000-0000-4000-8000-000000000011", ProductID: "COKE",
			ProductName: "Coke 1.5L", UnitPriceCentavos: 8200, Quantity: 1,
			LineTotalCentavos: 8200,
		}},
	}}}
	responses := &recordedOrderResponse{}
	handler := New(application, responses, allowRole)
	mux := http.NewServeMux()
	handler.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/orders", strings.NewReader(`{
		"checkout_id":"00000000-0000-4000-8000-000000000001",
		"lines":[{"product_id":"COKE","quantity":1,"expected_unit_price_centavos":8200}],
		"delivery_address":"Bria Homes"
	}`))

	mux.ServeHTTP(httptest.NewRecorder(), request)

	encoded, err := json.Marshal(responses.data)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	text := string(encoded)
	if responses.status != http.StatusAccepted || !strings.Contains(text, `"status":"PREPARING"`) {
		t.Fatalf("response = (%d, %s), want accepted Preparing", responses.status, text)
	}
	for _, forbidden := range []string{"SUBMITTED", "operation_id", "event_id", "available_quantity"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, text)
		}
	}
}

func allowRole(
	role identitydomain.Role,
	next func(http.ResponseWriter, *http.Request, identitydomain.Principal),
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		next(writer, request, identitydomain.Principal{
			UserID: "00000000-0000-4000-8000-000000000002",
			Role:   role, Status: identitydomain.AccountActive,
		})
	}
}

type recordedOrderResponse struct {
	status int
	code   string
	data   any
}

func (response *recordedOrderResponse) Success(_ http.ResponseWriter, _ *http.Request, status int, data any) {
	response.status = status
	response.data = data
}

func (response *recordedOrderResponse) Error(_ http.ResponseWriter, _ *http.Request, status int, code, _ string) {
	response.status = status
	response.code = code
}

type orderApplicationStub struct {
	placeResult domain.PlaceOrderResult
	placeCalls  int
}

func (application *orderApplicationStub) PlaceOrder(context.Context, domain.PlaceOrderCommand) (domain.PlaceOrderResult, error) {
	application.placeCalls++
	return application.placeResult, nil
}
func (*orderApplicationStub) CustomerOrder(context.Context, string, string) (domain.Order, error) {
	return domain.Order{}, nil
}
func (*orderApplicationStub) CustomerOrders(context.Context, string, int, string) ([]domain.Order, error) {
	return nil, nil
}
func (*orderApplicationStub) CashierOrder(context.Context, string) (domain.Order, error) {
	return domain.Order{}, nil
}
func (*orderApplicationStub) CashierOrders(context.Context, domain.FulfillmentStatus, int, string) ([]domain.Order, error) {
	return nil, nil
}
func (*orderApplicationStub) MarkCashierRead(context.Context, string, string) (domain.Order, error) {
	return domain.Order{}, nil
}
func (*orderApplicationStub) AdvanceFulfillment(context.Context, string, string, domain.FulfillmentStatus) (domain.Order, error) {
	return domain.Order{}, nil
}

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	identitydomain "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

type Responder interface {
	Success(http.ResponseWriter, *http.Request, int, any)
	Error(http.ResponseWriter, *http.Request, int, string, string)
}

type OrderApplication interface {
	PlaceOrder(context.Context, domain.PlaceOrderCommand) (domain.PlaceOrderResult, error)
	CustomerOrder(context.Context, string, string) (domain.Order, error)
	CustomerOrders(context.Context, string, int, string) ([]domain.Order, error)
	CashierOrder(context.Context, string) (domain.Order, error)
	CashierOrders(context.Context, domain.FulfillmentStatus, int, string) ([]domain.Order, error)
	MarkCashierRead(context.Context, string, string) (domain.Order, error)
	AdvanceFulfillment(context.Context, string, string, domain.FulfillmentStatus) (domain.Order, error)
}

type Authorize func(
	identitydomain.Role,
	func(http.ResponseWriter, *http.Request, identitydomain.Principal),
) http.HandlerFunc

type Handler struct {
	orders    OrderApplication
	responses Responder
	authorize Authorize
}

func New(orders OrderApplication, responses Responder, authorize Authorize) *Handler {
	return &Handler{orders: orders, responses: responses, authorize: authorize}
}

func (handler *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/orders", handler.authorize(identitydomain.RoleCustomer, handler.placeOrder))
	mux.HandleFunc("GET /v1/orders", handler.authorize(identitydomain.RoleCustomer, handler.customerOrders))
	mux.HandleFunc("GET /v1/orders/{order_id}", handler.authorize(identitydomain.RoleCustomer, handler.customerOrder))
	mux.HandleFunc("GET /v1/staff/orders", handler.authorize(identitydomain.RoleCashier, handler.cashierOrders))
	mux.HandleFunc("GET /v1/staff/orders/{order_id}", handler.authorize(identitydomain.RoleCashier, handler.cashierOrder))
	mux.HandleFunc("POST /v1/staff/orders/{order_id}/read", handler.authorize(identitydomain.RoleCashier, handler.markCashierRead))
	mux.HandleFunc("PATCH /v1/staff/orders/{order_id}/status", handler.authorize(identitydomain.RoleCashier, handler.advanceFulfillment))
}

type placeOrderRequest struct {
	CheckoutID      string                  `json:"checkout_id"`
	Lines           []placeOrderLineRequest `json:"lines"`
	DeliveryAddress string                  `json:"delivery_address"`
	DeliveryNotes   string                  `json:"delivery_notes"`
}

type placeOrderLineRequest struct {
	ProductID                 string `json:"product_id"`
	Quantity                  int    `json:"quantity"`
	ExpectedUnitPriceCentavos int64  `json:"expected_unit_price_centavos"`
}

func (handler *Handler) placeOrder(
	writer http.ResponseWriter,
	request *http.Request,
	principal identitydomain.Principal,
) {
	var body placeOrderRequest
	if err := decodeJSON(request, &body); err != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	command := domain.PlaceOrderCommand{
		CustomerID: principal.UserID, CheckoutID: body.CheckoutID,
		DeliveryAddress: body.DeliveryAddress, DeliveryNotes: body.DeliveryNotes,
		Lines: make([]domain.CheckoutLine, 0, len(body.Lines)),
	}
	for _, line := range body.Lines {
		command.Lines = append(command.Lines, domain.CheckoutLine{
			ProductID: line.ProductID, Quantity: line.Quantity,
			ExpectedUnitPriceCentavos: line.ExpectedUnitPriceCentavos,
		})
	}
	result, err := handler.orders.PlaceOrder(request.Context(), command)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	status := http.StatusAccepted
	if result.Replayed {
		status = http.StatusOK
	}
	writer.Header().Set("Location", "/v1/orders/"+result.Order.ID)
	handler.responses.Success(writer, request, status, customerOrderResponse(result.Order))
}

func (handler *Handler) customerOrder(
	writer http.ResponseWriter,
	request *http.Request,
	principal identitydomain.Principal,
) {
	order, err := handler.orders.CustomerOrder(request.Context(), principal.UserID, request.PathValue("order_id"))
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, customerOrderResponse(order))
}

func (handler *Handler) customerOrders(
	writer http.ResponseWriter,
	request *http.Request,
	principal identitydomain.Principal,
) {
	limit, ok := listLimit(request)
	if !ok {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	orders, err := handler.orders.CustomerOrders(
		request.Context(), principal.UserID, limit, request.URL.Query().Get("after_id"),
	)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	items := make([]any, 0, len(orders))
	for _, order := range orders {
		items = append(items, customerOrderResponse(order))
	}
	handler.responses.Success(writer, request, http.StatusOK, listResponse(items, orders, limit))
}

func (handler *Handler) cashierOrder(
	writer http.ResponseWriter,
	request *http.Request,
	_ identitydomain.Principal,
) {
	order, err := handler.orders.CashierOrder(request.Context(), request.PathValue("order_id"))
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, cashierOrderResponse(order))
}

func (handler *Handler) cashierOrders(
	writer http.ResponseWriter,
	request *http.Request,
	_ identitydomain.Principal,
) {
	limit, ok := listLimit(request)
	if !ok {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	status := domain.FulfillmentStatus(strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("status"))))
	orders, err := handler.orders.CashierOrders(
		request.Context(), status, limit, request.URL.Query().Get("after_id"),
	)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	items := make([]any, 0, len(orders))
	for _, order := range orders {
		items = append(items, cashierOrderResponse(order))
	}
	handler.responses.Success(writer, request, http.StatusOK, listResponse(items, orders, limit))
}

func (handler *Handler) markCashierRead(
	writer http.ResponseWriter,
	request *http.Request,
	principal identitydomain.Principal,
) {
	order, err := handler.orders.MarkCashierRead(
		request.Context(), principal.UserID, request.PathValue("order_id"),
	)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, cashierOrderResponse(order))
}

type fulfillmentRequest struct {
	Status domain.FulfillmentStatus `json:"status"`
}

func (handler *Handler) advanceFulfillment(
	writer http.ResponseWriter,
	request *http.Request,
	principal identitydomain.Principal,
) {
	var body fulfillmentRequest
	if err := decodeJSON(request, &body); err != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	order, err := handler.orders.AdvanceFulfillment(
		request.Context(), principal.UserID, request.PathValue("order_id"), body.Status,
	)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, cashierOrderResponse(order))
}

func listLimit(request *http.Request) (int, bool) {
	raw := strings.TrimSpace(request.URL.Query().Get("limit"))
	if raw == "" {
		return 20, true
	}
	limit, err := strconv.Atoi(raw)
	return limit, err == nil && limit >= 1 && limit <= 100
}

func listResponse(items []any, orders []domain.Order, limit int) map[string]any {
	next := ""
	if len(orders) == limit && len(orders) > 0 {
		next = orders[len(orders)-1].ID
	}
	return map[string]any{"orders": items, "next_after_id": next}
}

func customerOrderResponse(order domain.Order) map[string]any {
	state, _ := order.CustomerState()
	lines := orderLinesResponse(order.Lines)
	result := map[string]any{
		"order_id": order.ID, "checkout_id": order.CheckoutID, "status": state,
		"subtotal_centavos":     order.SubtotalCentavos,
		"delivery_fee_centavos": order.DeliveryFeeCentavos,
		"total_centavos":        order.TotalCentavos,
		"delivery_address":      order.DeliveryAddress, "delivery_notes": order.DeliveryNotes,
		"lines": lines, "created_at": order.CreatedAt, "updated_at": order.UpdatedAt,
	}
	if state == domain.CustomerRejected {
		items := make([]map[string]any, 0, len(order.UnavailableItems))
		for _, item := range order.UnavailableItems {
			items = append(items, map[string]any{
				"product_id": item.ProductID, "reason": "OUT_OF_STOCK", "available": false,
			})
		}
		result["rejection"] = map[string]any{
			"reason": domain.RejectionItemsUnavailable, "unavailable_items": items,
		}
	}
	return result
}

func cashierOrderResponse(order domain.Order) map[string]any {
	result := map[string]any{
		"order_id":              order.ID,
		"customer":              map[string]any{"customer_id": order.CustomerID, "name": order.CustomerName},
		"subtotal_centavos":     order.SubtotalCentavos,
		"delivery_fee_centavos": order.DeliveryFeeCentavos,
		"total_centavos":        order.TotalCentavos,
		"delivery_address":      order.DeliveryAddress, "delivery_notes": order.DeliveryNotes,
		"lines": orderLinesResponse(order.Lines), "created_at": order.CreatedAt,
	}
	if order.Fulfillment != nil {
		next := order.Fulfillment.NextAction()
		result["status"] = order.Fulfillment.Status
		result["unread"] = order.Fulfillment.Unread
		if next == "" {
			result["next_action"] = nil
		} else {
			result["next_action"] = next
		}
	}
	return result
}

func orderLinesResponse(lines []domain.OrderLine) []map[string]any {
	result := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		result = append(result, map[string]any{
			"order_line_id": line.ID, "product_id": line.ProductID,
			"product_name": line.ProductName, "unit_price_centavos": line.UnitPriceCentavos,
			"quantity": line.Quantity, "line_total_centavos": line.LineTotalCentavos,
		})
	}
	return result
}

func (handler *Handler) fail(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		handler.responses.Error(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
	case errors.Is(err, domain.ErrCartChanged):
		handler.responses.Error(writer, request, http.StatusConflict, "CART_CHANGED", "The cart changed. Review it and try again.")
	case errors.Is(err, domain.ErrCheckoutConflict):
		handler.responses.Error(writer, request, http.StatusConflict, "CONFLICT", "The checkout identifier was already used.")
	case errors.Is(err, domain.ErrInvalidTransition):
		handler.responses.Error(writer, request, http.StatusConflict, "INVALID_TRANSITION", "The order cannot move to that status.")
	case errors.Is(err, domain.ErrNotFound):
		handler.responses.Error(writer, request, http.StatusNotFound, "NOT_FOUND", "The order was not found.")
	default:
		handler.responses.Error(writer, request, http.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred.")
	}
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

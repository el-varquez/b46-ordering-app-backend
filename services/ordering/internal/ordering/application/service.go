package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/ports"
)

const (
	maximumCheckoutLines = 100
	maximumLineQuantity  = 100
)

type Service struct {
	checkouts ports.CheckoutStore
	queries   ports.OrderQueries
	catalog   ports.CheckoutCatalog
	ids       ports.IDGenerator
	clock     ports.Clock
}

func New(
	checkouts ports.CheckoutStore,
	queries ports.OrderQueries,
	catalog ports.CheckoutCatalog,
	ids ports.IDGenerator,
	clock ports.Clock,
) *Service {
	return &Service{checkouts: checkouts, queries: queries, catalog: catalog, ids: ids, clock: clock}
}

func (service *Service) PlaceOrder(ctx context.Context, command domain.PlaceOrderCommand) (domain.PlaceOrderResult, error) {
	command.DeliveryAddress = strings.TrimSpace(command.DeliveryAddress)
	command.DeliveryNotes = strings.TrimSpace(command.DeliveryNotes)
	if !validUUID(command.CustomerID) || !validUUID(command.CheckoutID) ||
		len(command.Lines) == 0 || len(command.Lines) > maximumCheckoutLines ||
		utf8.RuneCountInString(command.DeliveryAddress) == 0 ||
		utf8.RuneCountInString(command.DeliveryAddress) > 500 ||
		utf8.RuneCountInString(command.DeliveryNotes) > 500 {
		return domain.PlaceOrderResult{}, domain.ErrInvalidInput
	}

	requested := make(map[string]domain.CheckoutLine, len(command.Lines))
	productIDs := make([]string, 0, len(command.Lines))
	for _, line := range command.Lines {
		line.ProductID = strings.TrimSpace(line.ProductID)
		if line.ProductID == "" || len(line.ProductID) > 100 || line.Quantity < 1 ||
			line.Quantity > maximumLineQuantity || line.ExpectedUnitPriceCentavos < 0 {
			return domain.PlaceOrderResult{}, domain.ErrInvalidInput
		}
		if _, exists := requested[line.ProductID]; exists {
			return domain.PlaceOrderResult{}, domain.ErrInvalidInput
		}
		requested[line.ProductID] = line
		productIDs = append(productIDs, line.ProductID)
	}
	sort.Strings(productIDs)

	products, err := service.catalog.ResolveCheckoutProducts(ctx, productIDs)
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("resolve checkout products: %w", err)
	}
	byID := make(map[string]domain.ProductSnapshot, len(products))
	for _, product := range products {
		byID[product.ProductID] = product
	}

	var subtotal int64
	for _, productID := range productIDs {
		request := requested[productID]
		product, exists := byID[productID]
		if !exists || !product.Orderable || product.UnitPriceCentavos != request.ExpectedUnitPriceCentavos ||
			strings.TrimSpace(product.Name) == "" || product.UnitPriceCentavos < 0 {
			return domain.PlaceOrderResult{}, domain.ErrCartChanged
		}
		if product.UnitPriceCentavos > math.MaxInt64/int64(request.Quantity) {
			return domain.PlaceOrderResult{}, domain.ErrInvalidInput
		}
		lineTotal := product.UnitPriceCentavos * int64(request.Quantity)
		if subtotal > math.MaxInt64-lineTotal {
			return domain.PlaceOrderResult{}, domain.ErrInvalidInput
		}
		subtotal += lineTotal
	}

	orderID, err := service.ids.New()
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("generate order ID: %w", err)
	}
	eventID, err := service.ids.New()
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("generate event ID: %w", err)
	}
	operationID, err := service.ids.New()
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("generate operation ID: %w", err)
	}

	lines := make([]domain.OrderLine, 0, len(productIDs))
	for _, productID := range productIDs {
		request := requested[productID]
		product := byID[productID]
		lineID, idErr := service.ids.New()
		if idErr != nil {
			return domain.PlaceOrderResult{}, fmt.Errorf("generate order line ID: %w", idErr)
		}
		lineTotal := product.UnitPriceCentavos * int64(request.Quantity)
		lines = append(lines, domain.OrderLine{
			ID: lineID, ProductID: productID, ProductName: strings.TrimSpace(product.Name),
			UnitPriceCentavos: product.UnitPriceCentavos, Quantity: request.Quantity,
			LineTotalCentavos: lineTotal,
		})
	}

	fingerprint, err := checkoutFingerprint(command, productIDs, requested)
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("fingerprint checkout: %w", err)
	}
	now := service.clock.Now().UTC()
	draft := domain.OrderDraft{
		Order: domain.Order{
			ID: orderID, CheckoutID: command.CheckoutID, CustomerID: command.CustomerID,
			Status: domain.OrderSubmitted, SubtotalCentavos: subtotal, TotalCentavos: subtotal,
			DeliveryAddress: command.DeliveryAddress, DeliveryNotes: command.DeliveryNotes,
			Lines: lines, CreatedAt: now, UpdatedAt: now,
		},
		CheckoutFingerprint: fingerprint,
		EventID:             eventID, OperationID: operationID, OccurredAt: now,
	}
	result, err := service.checkouts.PlaceOrder(ctx, draft)
	if err != nil {
		return domain.PlaceOrderResult{}, err
	}
	return result, nil
}

func checkoutFingerprint(
	command domain.PlaceOrderCommand,
	productIDs []string,
	requested map[string]domain.CheckoutLine,
) (string, error) {
	type canonicalLine struct {
		ProductID                 string `json:"product_id"`
		Quantity                  int    `json:"quantity"`
		ExpectedUnitPriceCentavos int64  `json:"expected_unit_price_centavos"`
	}
	payload := struct {
		CustomerID      string          `json:"customer_id"`
		DeliveryAddress string          `json:"delivery_address"`
		DeliveryNotes   string          `json:"delivery_notes"`
		Lines           []canonicalLine `json:"lines"`
	}{
		CustomerID:      command.CustomerID,
		DeliveryAddress: command.DeliveryAddress,
		DeliveryNotes:   command.DeliveryNotes,
		Lines:           make([]canonicalLine, 0, len(productIDs)),
	}
	for _, productID := range productIDs {
		line := requested[productID]
		payload.Lines = append(payload.Lines, canonicalLine{
			ProductID: productID, Quantity: line.Quantity,
			ExpectedUnitPriceCentavos: line.ExpectedUnitPriceCentavos,
		})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}

func (service *Service) CustomerOrder(ctx context.Context, customerID, orderID string) (domain.Order, error) {
	if service.queries == nil || !validUUID(customerID) || !validUUID(orderID) {
		return domain.Order{}, domain.ErrInvalidInput
	}
	return service.queries.CustomerOrder(ctx, customerID, orderID)
}

func (service *Service) CustomerOrders(ctx context.Context, customerID string, limit int, afterID string) ([]domain.Order, error) {
	if service.queries == nil || !validUUID(customerID) || limit < 1 || limit > 100 || (afterID != "" && !validUUID(afterID)) {
		return nil, domain.ErrInvalidInput
	}
	return service.queries.CustomerOrders(ctx, customerID, limit, afterID)
}

func (service *Service) CashierOrder(ctx context.Context, orderID string) (domain.Order, error) {
	if service.queries == nil || !validUUID(orderID) {
		return domain.Order{}, domain.ErrInvalidInput
	}
	return service.queries.CashierOrder(ctx, orderID)
}

func (service *Service) CashierOrders(ctx context.Context, status domain.FulfillmentStatus, limit int, afterID string) ([]domain.Order, error) {
	if service.queries == nil || limit < 1 || limit > 100 || (afterID != "" && !validUUID(afterID)) {
		return nil, domain.ErrInvalidInput
	}
	if status != "" && status != domain.FulfillmentPreparing && status != domain.FulfillmentDelivering && status != domain.FulfillmentDelivered {
		return nil, domain.ErrInvalidInput
	}
	return service.queries.CashierOrders(ctx, status, limit, afterID)
}

func (service *Service) MarkCashierRead(ctx context.Context, cashierID, orderID string) (domain.Order, error) {
	if service.queries == nil || !validUUID(cashierID) || !validUUID(orderID) {
		return domain.Order{}, domain.ErrInvalidInput
	}
	return service.queries.MarkCashierRead(ctx, cashierID, orderID, service.clock.Now().UTC())
}

func (service *Service) AdvanceFulfillment(ctx context.Context, cashierID, orderID string, target domain.FulfillmentStatus) (domain.Order, error) {
	if service.queries == nil || !validUUID(cashierID) || !validUUID(orderID) ||
		(target != domain.FulfillmentDelivering && target != domain.FulfillmentDelivered) {
		return domain.Order{}, domain.ErrInvalidInput
	}
	order, err := service.queries.AdvanceFulfillment(ctx, cashierID, orderID, target, service.clock.Now().UTC())
	if err != nil && !errors.Is(err, domain.ErrInvalidTransition) {
		return domain.Order{}, err
	}
	return order, err
}

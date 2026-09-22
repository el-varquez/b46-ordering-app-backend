package domain

import (
	"errors"
	"time"
)

type OrderStatus string

const (
	OrderSubmitted OrderStatus = "SUBMITTED"
	OrderConfirmed OrderStatus = "CONFIRMED"
	OrderRejected  OrderStatus = "REJECTED"
)

type FulfillmentStatus string

const (
	FulfillmentPreparing  FulfillmentStatus = "PREPARING"
	FulfillmentDelivering FulfillmentStatus = "DELIVERING"
	FulfillmentDelivered  FulfillmentStatus = "DELIVERED"
)

type CustomerState string

const (
	CustomerPreparing CustomerState = "PREPARING"
	CustomerOnTheWay  CustomerState = "ON_THE_WAY"
	CustomerDelivered CustomerState = "DELIVERED"
	CustomerRejected  CustomerState = "REJECTED"
)

const RejectionItemsUnavailable = "ITEMS_UNAVAILABLE"

var (
	ErrInvalidInput       = errors.New("invalid order input")
	ErrCartChanged        = errors.New("cart changed")
	ErrCheckoutConflict   = errors.New("checkout conflict")
	ErrNotFound           = errors.New("order not found")
	ErrInvalidTransition  = errors.New("invalid fulfillment transition")
	ErrOperationConflict  = errors.New("inventory operation conflict")
	ErrClaimLost          = errors.New("outbox claim lost")
	ErrInventoryTechnical = errors.New("inventory technical failure")
)

type Order struct {
	ID                  string
	CheckoutID          string
	CustomerID          string
	CustomerName        string
	Status              OrderStatus
	RejectionCode       string
	SubtotalCentavos    int64
	DeliveryFeeCentavos int64
	TotalCentavos       int64
	DeliveryAddress     string
	DeliveryNotes       string
	Lines               []OrderLine
	Fulfillment         *Fulfillment
	UnavailableItems    []UnavailableItem
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type OrderLine struct {
	ID                string
	ProductID         string
	ProductName       string
	UnitPriceCentavos int64
	Quantity          int
	LineTotalCentavos int64
}

type Fulfillment struct {
	Status        FulfillmentStatus
	Version       int
	Unread        bool
	PreparingAt   time.Time
	DeliveringAt  *time.Time
	DeliveredAt   *time.Time
	CashierReadAt *time.Time
	CashierReadBy string
	UpdatedBy     string
}

type UnavailableItem struct {
	OrderLineID       string `json:"order_line_id"`
	ProductID         string `json:"product_id"`
	RequestedQuantity int    `json:"requested_quantity"`
	AvailableQuantity int    `json:"available_quantity"`
}

type InventoryItem struct {
	OrderLineID string
	ProductID   string
	Quantity    int
}

type InventoryCommit struct {
	EventID     string
	OperationID string
	OrderID     string
	OccurredAt  time.Time
	Items       []InventoryItem
}

type InventoryResultStatus string

const (
	InventoryCommitted        InventoryResultStatus = "COMMITTED"
	InventoryItemsUnavailable InventoryResultStatus = "ITEMS_UNAVAILABLE"
)

type InventoryResult struct {
	EventID          string
	OperationID      string
	OrderID          string
	Status           InventoryResultStatus
	OccurredAt       time.Time
	UnavailableItems []UnavailableItem
}

type ClaimedInventoryWork struct {
	Commit       InventoryCommit
	ClaimToken   string
	AttemptCount int
}

type CheckoutLine struct {
	ProductID                 string
	Quantity                  int
	ExpectedUnitPriceCentavos int64
}

type PlaceOrderCommand struct {
	CustomerID      string
	CheckoutID      string
	Lines           []CheckoutLine
	DeliveryAddress string
	DeliveryNotes   string
}

type ProductSnapshot struct {
	ProductID         string
	Name              string
	UnitPriceCentavos int64
	Orderable         bool
}

type OrderDraft struct {
	Order
	CheckoutFingerprint string
	EventID             string
	OperationID         string
	OccurredAt          time.Time
}

type PlaceOrderResult struct {
	Order    Order
	Replayed bool
}

type NotificationKind string

const (
	NotificationOrderAccepted  NotificationKind = "ORDER_ACCEPTED"
	NotificationOrderOnTheWay  NotificationKind = "ORDER_ON_THE_WAY"
	NotificationOrderDelivered NotificationKind = "ORDER_DELIVERED"
)

type Notification struct {
	EventID         string
	Kind            NotificationKind
	OrderID         string
	RecipientUserID string
	Audience        string
	OccurredAt      time.Time
}

func (order Order) CustomerState() (CustomerState, error) {
	switch order.Status {
	case OrderSubmitted:
		return CustomerPreparing, nil
	case OrderRejected:
		return CustomerRejected, nil
	case OrderConfirmed:
		if order.Fulfillment == nil {
			return "", ErrInvalidTransition
		}
		switch order.Fulfillment.Status {
		case FulfillmentPreparing:
			return CustomerPreparing, nil
		case FulfillmentDelivering:
			return CustomerOnTheWay, nil
		case FulfillmentDelivered:
			return CustomerDelivered, nil
		}
	}
	return "", ErrInvalidTransition
}

func (fulfillment Fulfillment) NextAction() FulfillmentStatus {
	switch fulfillment.Status {
	case FulfillmentPreparing:
		return FulfillmentDelivering
	case FulfillmentDelivering:
		return FulfillmentDelivered
	default:
		return ""
	}
}

func ValidateFulfillmentTarget(current, target FulfillmentStatus) error {
	if current == target && (current == FulfillmentDelivering || current == FulfillmentDelivered) {
		return nil
	}
	if current == FulfillmentPreparing && target == FulfillmentDelivering {
		return nil
	}
	if current == FulfillmentDelivering && target == FulfillmentDelivered {
		return nil
	}
	return ErrInvalidTransition
}

package ports

import (
	"context"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

type CheckoutStore interface {
	PlaceOrder(context.Context, domain.OrderDraft) (domain.PlaceOrderResult, error)
}

type OrderQueries interface {
	CustomerOrder(context.Context, string, string) (domain.Order, error)
	CustomerOrders(context.Context, string, int, string) ([]domain.Order, error)
	CashierOrder(context.Context, string) (domain.Order, error)
	CashierOrders(context.Context, domain.FulfillmentStatus, int, string) ([]domain.Order, error)
	MarkCashierRead(context.Context, string, string, time.Time) (domain.Order, error)
	AdvanceFulfillment(context.Context, string, string, domain.FulfillmentStatus, time.Time) (domain.Order, error)
}

type CheckoutCatalog interface {
	ResolveCheckoutProducts(context.Context, []string) ([]domain.ProductSnapshot, error)
}

type IDGenerator interface {
	New() (string, error)
}

type Clock interface {
	Now() time.Time
}

type InventoryCommitter interface {
	Commit(context.Context, domain.InventoryCommit) (domain.InventoryResult, error)
}

type OutboxStore interface {
	ClaimInventoryWork(context.Context, time.Time, time.Duration, int) ([]domain.ClaimedInventoryWork, error)
	ApplyInventoryResult(context.Context, domain.ClaimedInventoryWork, domain.InventoryResult, time.Time) error
	RetryInventoryWork(context.Context, domain.ClaimedInventoryWork, time.Time, string) error
}

type Notifier interface {
	Notify(context.Context, domain.Notification) error
}

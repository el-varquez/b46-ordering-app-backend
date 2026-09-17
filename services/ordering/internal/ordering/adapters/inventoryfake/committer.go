package inventoryfake

import (
	"context"
	"fmt"
	"sync"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/ports"
)

type Committer struct {
	mutex       sync.Mutex
	ids         ports.IDGenerator
	clock       ports.Clock
	results     map[string]domain.InventoryResult
	unavailable map[string]int
	nextError   error
}

func New(ids ports.IDGenerator, clock ports.Clock) *Committer {
	return &Committer{
		ids: ids, clock: clock, results: make(map[string]domain.InventoryResult),
		unavailable: make(map[string]int),
	}
}

func (committer *Committer) SetUnavailable(productID string, availableQuantity int) {
	committer.mutex.Lock()
	defer committer.mutex.Unlock()
	committer.unavailable[productID] = availableQuantity
}

func (committer *Committer) FailNext(err error) {
	committer.mutex.Lock()
	defer committer.mutex.Unlock()
	committer.nextError = err
}

func (committer *Committer) Commit(
	_ context.Context,
	request domain.InventoryCommit,
) (domain.InventoryResult, error) {
	committer.mutex.Lock()
	defer committer.mutex.Unlock()
	if existing, ok := committer.results[request.OperationID]; ok {
		return existing, nil
	}
	if committer.nextError != nil {
		err := committer.nextError
		committer.nextError = nil
		return domain.InventoryResult{}, err
	}
	eventID, err := committer.ids.New()
	if err != nil {
		return domain.InventoryResult{}, fmt.Errorf("generate fake result event ID: %w", err)
	}
	result := domain.InventoryResult{
		EventID: eventID, OperationID: request.OperationID, OrderID: request.OrderID,
		Status: domain.InventoryCommitted, OccurredAt: committer.clock.Now().UTC(),
	}
	for _, item := range request.Items {
		if available, unavailable := committer.unavailable[item.ProductID]; unavailable && available < item.Quantity {
			result.Status = domain.InventoryItemsUnavailable
			result.UnavailableItems = append(result.UnavailableItems, domain.UnavailableItem{
				OrderLineID: item.OrderLineID, ProductID: item.ProductID,
				RequestedQuantity: item.Quantity, AvailableQuantity: available,
			})
		}
	}
	committer.results[request.OperationID] = result
	return result, nil
}

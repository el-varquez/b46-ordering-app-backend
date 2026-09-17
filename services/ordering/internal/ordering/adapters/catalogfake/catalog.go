package catalogfake

import (
	"context"
	"sync"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

type Catalog struct {
	mutex    sync.RWMutex
	products map[string]domain.ProductSnapshot
}

func New(products []domain.ProductSnapshot) *Catalog {
	catalog := &Catalog{products: make(map[string]domain.ProductSnapshot, len(products))}
	for _, product := range products {
		catalog.products[product.ProductID] = product
	}
	return catalog
}

func (catalog *Catalog) Set(product domain.ProductSnapshot) {
	catalog.mutex.Lock()
	defer catalog.mutex.Unlock()
	catalog.products[product.ProductID] = product
}

func (catalog *Catalog) ResolveCheckoutProducts(
	_ context.Context,
	productIDs []string,
) ([]domain.ProductSnapshot, error) {
	catalog.mutex.RLock()
	defer catalog.mutex.RUnlock()
	products := make([]domain.ProductSnapshot, 0, len(productIDs))
	for _, productID := range productIDs {
		if product, ok := catalog.products[productID]; ok {
			products = append(products, product)
		}
	}
	return products, nil
}

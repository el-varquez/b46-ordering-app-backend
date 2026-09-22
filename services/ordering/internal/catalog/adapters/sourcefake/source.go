package sourcefake

import (
	"context"
	"sort"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/domain"
)

type Source struct{ products []domain.Product }

func New(products []domain.Product) *Source {
	copyOfProducts := append([]domain.Product(nil), products...)
	sort.Slice(copyOfProducts, func(left, right int) bool { return copyOfProducts[left].ID < copyOfProducts[right].ID })
	return &Source{products: copyOfProducts}
}

func Default() *Source {
	updatedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	categoryID := "b4600000-0000-4000-8002-000000000001"
	return New([]domain.Product{
		{ID: "b4600000-0000-4000-8001-000000000001", Name: "Coke 1.5L", Description: "Chilled bottle", PriceCentavos: 8200, CategoryID: categoryID, CategoryName: "Drinks", Available: true, SourceUpdatedAt: updatedAt},
		{ID: "b4600000-0000-4000-8001-000000000002", Name: "Tasty Bread", Description: "600 g loaf", PriceCentavos: 6800, CategoryID: categoryID, CategoryName: "Pantry", Available: true, SourceUpdatedAt: updatedAt},
		{ID: "b4600000-0000-4000-8001-000000000003", Name: "Fresh Milk 1L", Description: "Fresh dairy", PriceCentavos: 9500, CategoryID: categoryID, CategoryName: "Drinks", Available: true, SourceUpdatedAt: updatedAt},
	})
}

func (source *Source) Products(_ context.Context, limit int, afterID string) (domain.Page, error) {
	start := sort.Search(len(source.products), func(index int) bool { return source.products[index].ID > afterID })
	end := start + limit
	if end > len(source.products) {
		end = len(source.products)
	}
	page := domain.Page{Products: append([]domain.Product(nil), source.products[start:end]...)}
	if end < len(source.products) && len(page.Products) > 0 {
		page.NextAfterID = page.Products[len(page.Products)-1].ID
	}
	return page, nil
}

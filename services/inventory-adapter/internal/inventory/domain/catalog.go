package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidCatalogQuery = errors.New("invalid catalog query")

type CatalogQuery struct {
	Limit   int
	AfterID uuid.UUID
}

type CatalogProduct struct {
	ProductID       uuid.UUID
	Name            string
	Description     string
	PriceCentavos   int64
	CategoryID      uuid.UUID
	CategoryName    string
	Available       bool
	SourceUpdatedAt time.Time
}

type CatalogPage struct {
	Products    []CatalogProduct
	NextAfterID uuid.UUID
}

func (query CatalogQuery) Validate() error {
	if query.Limit < 1 || query.Limit > 100 {
		return ErrInvalidCatalogQuery
	}
	return nil
}

func (page CatalogPage) Validate() error {
	if len(page.Products) > 100 {
		return ErrInvalidCatalogQuery
	}
	for _, product := range page.Products {
		if product.ProductID == uuid.Nil || product.CategoryID == uuid.Nil ||
			strings.TrimSpace(product.Name) == "" || strings.TrimSpace(product.CategoryName) == "" ||
			product.PriceCentavos < 0 || product.SourceUpdatedAt.IsZero() {
			return ErrInvalidCatalogQuery
		}
	}
	return nil
}

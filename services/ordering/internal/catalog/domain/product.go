package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidInput    = errors.New("invalid catalog input")
	ErrUnavailable     = errors.New("catalog unavailable")
	ErrSnapshotExpired = errors.New("catalog snapshot expired")
)

type Product struct {
	ID              string
	Name            string
	Description     string
	PriceCentavos   int64
	CategoryID      string
	CategoryName    string
	ImageURL        string
	Available       bool
	SourceUpdatedAt time.Time
	Revision        int64
}

type Page struct {
	Products         []Product
	SnapshotRevision int64
	NextAfterID      string
}

func (product Product) Validate() error {
	if strings.TrimSpace(product.ID) == "" || strings.TrimSpace(product.Name) == "" ||
		strings.TrimSpace(product.CategoryID) == "" || strings.TrimSpace(product.CategoryName) == "" ||
		product.PriceCentavos < 0 || product.SourceUpdatedAt.IsZero() {
		return ErrInvalidInput
	}
	if _, err := uuid.Parse(product.ID); err != nil {
		return ErrInvalidInput
	}
	if _, err := uuid.Parse(product.CategoryID); err != nil {
		return ErrInvalidInput
	}
	return nil
}

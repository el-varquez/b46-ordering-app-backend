package ports

import (
	"context"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/catalog/domain"
)

type Source interface {
	Products(context.Context, int, string) (domain.Page, error)
}

type Store interface {
	ReplaceSnapshot(context.Context, []domain.Product, time.Time) error
	Products(context.Context, int, string, int64) (domain.Page, error)
	Resolve(context.Context, []string) ([]domain.Product, error)
}

type Clock interface {
	Now() time.Time
}

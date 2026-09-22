package ports

import (
	"context"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/domain"
	"github.com/google/uuid"
)

type CommitStore interface {
	Commit(context.Context, domain.CommitCommand, string) (domain.CommitResult, error)
}

type IDGenerator interface {
	New() uuid.UUID
}

type Clock interface {
	Now() time.Time
}

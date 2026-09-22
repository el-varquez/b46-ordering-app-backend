package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/ports"
)

type Service struct {
	store ports.CommitStore
}

func NewService(store ports.CommitStore) (*Service, error) {
	if store == nil {
		return nil, errors.New("inventory application: commit store is required")
	}
	return &Service{store: store}, nil
}

func (service *Service) Commit(ctx context.Context, command domain.CommitCommand) (domain.CommitResult, error) {
	fingerprint, err := command.Fingerprint()
	if err != nil {
		return domain.CommitResult{}, err
	}
	result, err := service.store.Commit(ctx, command, fingerprint)
	if err != nil {
		return domain.CommitResult{}, fmt.Errorf("commit inventory: %w", err)
	}
	if err := result.Validate(); err != nil {
		return domain.CommitResult{}, fmt.Errorf("%w: invalid stored result", domain.ErrStoreUnavailable)
	}
	if result.OperationID != command.OperationID || result.OrderID != command.OrderID {
		return domain.CommitResult{}, fmt.Errorf("%w: stored result identity mismatch", domain.ErrStoreUnavailable)
	}
	return result, nil
}

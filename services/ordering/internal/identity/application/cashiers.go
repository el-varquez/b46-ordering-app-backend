package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

func (service *Service) ListCashiers(ctx context.Context, actor domain.Principal, status domain.AccountStatus) ([]domain.UserView, error) {
	if err := Authorize(actor, domain.RoleAdmin); err != nil {
		return nil, err
	}
	if status != "" && status != domain.AccountActive && status != domain.AccountDisabled {
		return nil, domain.ErrInvalidInput
	}
	return service.store.ListCashiers(ctx, status)
}

func (service *Service) Cashier(ctx context.Context, actor domain.Principal, id string) (domain.UserView, error) {
	if err := Authorize(actor, domain.RoleAdmin); err != nil {
		return domain.UserView{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return domain.UserView{}, domain.ErrInvalidInput
	}
	return service.store.CashierView(ctx, id)
}

func (service *Service) SetCashierStatus(
	ctx context.Context, actor domain.Principal, id string, target domain.AccountStatus,
) (domain.UserView, error) {
	if err := Authorize(actor, domain.RoleAdmin); err != nil {
		return domain.UserView{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return domain.UserView{}, domain.ErrInvalidInput
	}
	if target != domain.AccountActive && target != domain.AccountDisabled {
		return domain.UserView{}, domain.ErrInvalidInput
	}
	return service.store.SetCashierStatus(ctx, actor.UserID, id, target, service.clock.Now())
}

func (service *Service) ResetCashierPassword(
	ctx context.Context, actor domain.Principal, id, temporaryPassword string,
) (domain.UserView, error) {
	if err := Authorize(actor, domain.RoleAdmin); err != nil {
		return domain.UserView{}, err
	}
	if _, err := uuid.Parse(id); err != nil || domain.ValidatePassword(temporaryPassword) != nil {
		return domain.UserView{}, domain.ErrInvalidInput
	}
	hash, err := service.hasher.Hash(temporaryPassword)
	if err != nil {
		return domain.UserView{}, fmt.Errorf("hash temporary password: %w", err)
	}
	return service.store.ResetCashierPassword(ctx, actor.UserID, id, hash, service.clock.Now())
}

func (service *Service) ChangeOwnPassword(
	ctx context.Context, actor domain.Principal, currentPassword, newPassword string,
) error {
	if actor.Status != domain.AccountActive {
		return domain.ErrUnauthenticated
	}
	if domain.ValidatePassword(newPassword) != nil || currentPassword == newPassword {
		return domain.ErrInvalidInput
	}
	view, err := service.store.UserView(ctx, actor.UserID)
	if err != nil {
		return err
	}
	record, err := service.store.FindPassword(ctx, view.User.NormalizedEmail)
	if errors.Is(err, domain.ErrInvalidCredentials) {
		return domain.ErrInvalidCredentials
	}
	if err != nil {
		return fmt.Errorf("read current password: %w", err)
	}
	matches, _, err := service.hasher.Verify(currentPassword, record.PasswordHash)
	if err != nil {
		return fmt.Errorf("verify current password: %w", err)
	}
	if !matches {
		return domain.ErrInvalidCredentials
	}
	hash, err := service.hasher.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	return service.store.ChangeOwnPassword(ctx, actor.UserID, actor.FamilyID, record.PasswordHash, hash, service.clock.Now())
}

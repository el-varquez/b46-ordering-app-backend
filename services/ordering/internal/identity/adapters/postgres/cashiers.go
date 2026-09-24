package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

func (store *Store) ListCashiers(ctx context.Context, status domain.AccountStatus) ([]domain.UserView, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT u.id, u.name, u.normalized_email, u.role, u.account_status,
		       u.password_change_required, u.email_verified_at IS NOT NULL,
		       ARRAY(SELECT i.provider FROM login_identities i WHERE i.user_id = u.id ORDER BY i.provider)
		FROM users u
		WHERE u.role = 'CASHIER' AND ($1 = '' OR u.account_status = $1)
		ORDER BY u.name, u.id
	`, status)
	if err != nil {
		return nil, fmt.Errorf("list cashiers: %w", err)
	}
	defer rows.Close()
	views := make([]domain.UserView, 0)
	for rows.Next() {
		var view domain.UserView
		var providers []string
		if err := rows.Scan(&view.User.ID, &view.User.Name, &view.User.NormalizedEmail,
			&view.User.Role, &view.User.Status, &view.User.PasswordChangeRequired, &view.EmailVerified,
			&providers); err != nil {
			return nil, fmt.Errorf("scan cashier: %w", err)
		}
		for _, provider := range providers {
			view.Providers = append(view.Providers, domain.Provider(provider))
		}
		views = append(views, view)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cashiers: %w", err)
	}
	return views, nil
}

func (store *Store) CashierView(ctx context.Context, id string) (domain.UserView, error) {
	var view domain.UserView
	err := store.pool.QueryRow(ctx, `
		SELECT id, name, normalized_email, role, account_status, password_change_required,
		       email_verified_at IS NOT NULL
		FROM users WHERE id = $1 AND role = 'CASHIER'
	`, id).Scan(&view.User.ID, &view.User.Name, &view.User.NormalizedEmail,
		&view.User.Role, &view.User.Status, &view.User.PasswordChangeRequired, &view.EmailVerified)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UserView{}, domain.ErrCashierNotFound
	}
	if err != nil {
		return domain.UserView{}, fmt.Errorf("read cashier: %w", err)
	}
	rows, err := store.pool.Query(ctx, `SELECT provider FROM login_identities WHERE user_id = $1 ORDER BY provider`, id)
	if err != nil {
		return domain.UserView{}, fmt.Errorf("read cashier providers: %w", err)
	}
	for rows.Next() {
		var provider domain.Provider
		if err := rows.Scan(&provider); err != nil {
			rows.Close()
			return domain.UserView{}, fmt.Errorf("scan cashier provider: %w", err)
		}
		view.Providers = append(view.Providers, provider)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return domain.UserView{}, fmt.Errorf("iterate cashier providers: %w", err)
	}
	auditRows, err := store.pool.Query(ctx, `
		SELECT action, COALESCE(actor_user_id::text, ''),
		       COALESCE(details->>'result', 'success'), occurred_at
		FROM audit_records WHERE target_type = 'USER' AND target_id = $1
		ORDER BY occurred_at DESC, id DESC LIMIT 20
	`, id)
	if err != nil {
		return domain.UserView{}, fmt.Errorf("read cashier audit: %w", err)
	}
	defer auditRows.Close()
	for auditRows.Next() {
		var entry domain.AuditEntry
		if err := auditRows.Scan(&entry.Action, &entry.ActorUserID, &entry.Result, &entry.OccurredAt); err != nil {
			return domain.UserView{}, fmt.Errorf("scan cashier audit: %w", err)
		}
		view.Audit = append(view.Audit, entry)
	}
	if err := auditRows.Err(); err != nil {
		return domain.UserView{}, fmt.Errorf("iterate cashier audit: %w", err)
	}
	return view, nil
}

func (store *Store) SetCashierStatus(
	ctx context.Context, actorID, id string, target domain.AccountStatus, now time.Time,
) (domain.UserView, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.UserView{}, fmt.Errorf("begin cashier status change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, err := userByIDForUpdate(ctx, tx, id)
	if errors.Is(err, domain.ErrUnauthenticated) {
		return domain.UserView{}, domain.ErrCashierNotFound
	}
	if err != nil {
		return domain.UserView{}, err
	}
	if user.Role != domain.RoleCashier {
		return domain.UserView{}, domain.ErrCashierNotFound
	}
	if user.Status != target {
		if _, err := tx.Exec(ctx, `UPDATE users SET account_status = $2 WHERE id = $1`, id, target); err != nil {
			return domain.UserView{}, fmt.Errorf("update cashier status: %w", err)
		}
		action := "CASHIER_RESTORED"
		if target == domain.AccountDisabled {
			action = "CASHIER_DISABLED"
			if err := revokeManagedSessions(ctx, tx, id, "ACCOUNT_DISABLED", now); err != nil {
				return domain.UserView{}, err
			}
		}
		if err := insertAudit(ctx, tx, actorID, action, "USER", id, now); err != nil {
			return domain.UserView{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.UserView{}, fmt.Errorf("commit cashier status change: %w", err)
	}
	return store.CashierView(ctx, id)
}

func (store *Store) ResetCashierPassword(
	ctx context.Context, actorID, id, hash string, now time.Time,
) (domain.UserView, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.UserView{}, fmt.Errorf("begin cashier password reset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, err := userByIDForUpdate(ctx, tx, id)
	if errors.Is(err, domain.ErrUnauthenticated) {
		return domain.UserView{}, domain.ErrCashierNotFound
	}
	if err != nil {
		return domain.UserView{}, err
	}
	if user.Role != domain.RoleCashier {
		return domain.UserView{}, domain.ErrCashierNotFound
	}
	result, err := tx.Exec(ctx, `
		UPDATE login_identities SET password_hash = $2
		WHERE user_id = $1 AND provider = 'PASSWORD'
	`, id, hash)
	if err != nil {
		return domain.UserView{}, fmt.Errorf("reset cashier password: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.UserView{}, domain.ErrInvalidInput
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_change_required = true WHERE id = $1`, id); err != nil {
		return domain.UserView{}, fmt.Errorf("require cashier password change: %w", err)
	}
	if err := revokeManagedSessions(ctx, tx, id, "PASSWORD_RESET", now); err != nil {
		return domain.UserView{}, err
	}
	if err := insertAudit(ctx, tx, actorID, "CASHIER_PASSWORD_RESET", "USER", id, now); err != nil {
		return domain.UserView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.UserView{}, fmt.Errorf("commit cashier password reset: %w", err)
	}
	return store.CashierView(ctx, id)
}

func (store *Store) ChangeOwnPassword(
	ctx context.Context, userID, familyID, oldHash, newHash string, now time.Time,
) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin password change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, err := userByIDForUpdate(ctx, tx, userID)
	if err != nil {
		return err
	}
	if user.Status != domain.AccountActive {
		return domain.ErrAccountDisabled
	}
	result, err := tx.Exec(ctx, `
		UPDATE login_identities SET password_hash = $3
		WHERE user_id = $1 AND provider = 'PASSWORD' AND password_hash = $2
	`, userID, oldHash, newHash)
	if err != nil {
		return fmt.Errorf("change own password: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrInvalidCredentials
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_change_required = false WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("clear password change requirement: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = COALESCE(revoked_at, GREATEST($3, created_at)),
		       revoked_reason = COALESCE(revoked_reason, 'PASSWORD_CHANGED')
		WHERE user_id = $1 AND family_id <> $2::uuid AND revoked_at IS NULL
	`, userID, familyID, now); err != nil {
		return fmt.Errorf("revoke other password sessions: %w", err)
	}
	if err := insertAudit(ctx, tx, userID, "PASSWORD_CHANGED", "USER", userID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func revokeManagedSessions(ctx context.Context, tx pgx.Tx, id, reason string, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = GREATEST($2, created_at),
		       revoked_reason = COALESCE(revoked_reason, $3)
		WHERE user_id = $1 AND revoked_at IS NULL
	`, id, now, reason); err != nil {
		return fmt.Errorf("revoke cashier sessions: %w", err)
	}
	return nil
}

func (store *Store) RecoverAdminPassword(ctx context.Context, email, hash string, now time.Time) (domain.User, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, fmt.Errorf("begin admin recovery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var admin domain.User
	err = tx.QueryRow(ctx, `
		SELECT id, name, normalized_email, role, account_status
		FROM users WHERE normalized_email = $1 AND role = 'ADMIN' FOR UPDATE
	`, email).Scan(&admin.ID, &admin.Name, &admin.NormalizedEmail, &admin.Role, &admin.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrAdminNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("find recovery admin: %w", err)
	}
	if admin.Status != domain.AccountActive {
		return domain.User{}, domain.ErrAccountDisabled
	}
	result, err := tx.Exec(ctx, `
		UPDATE login_identities SET password_hash = $2
		WHERE user_id = $1 AND provider = 'PASSWORD'
	`, admin.ID, hash)
	if err != nil {
		return domain.User{}, fmt.Errorf("replace admin password: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.User{}, domain.ErrAdminNotFound
	}
	if err := revokeManagedSessions(ctx, tx, admin.ID, "ADMIN_RECOVERY", now); err != nil {
		return domain.User{}, err
	}
	if err := insertAudit(ctx, tx, "", "ADMIN_PASSWORD_RECOVERED", "USER", admin.ID, now); err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit admin recovery: %w", err)
	}
	return admin, nil
}

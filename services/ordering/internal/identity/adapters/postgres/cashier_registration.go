package postgres

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

func (store *Store) BeginCashierRegistration(
	ctx context.Context, value domain.PendingRegistration, actorID, ipKey string, now time.Time,
) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cashier registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := registrationLimits(ctx, tx, value.EmailRateKey, ipKey, now); err != nil {
		return err
	}
	var role domain.Role
	var verifiedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT role, email_verified_at FROM users WHERE normalized_email = $1`, value.Email).
		Scan(&role, &verifiedAt)
	if err == nil && (role != domain.RoleCashier || verifiedAt != nil) {
		return domain.ErrEmailInUse
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check cashier registration email: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO pending_cashier_registrations (
			id, actor_user_id, normalized_email, display_name, password_hash,
			code_hash, email_rate_key, created_at, expires_at, last_sent_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$8)
		ON CONFLICT (normalized_email) DO UPDATE SET
			id = EXCLUDED.id, actor_user_id = EXCLUDED.actor_user_id,
			display_name = EXCLUDED.display_name, password_hash = EXCLUDED.password_hash,
			code_hash = EXCLUDED.code_hash, email_rate_key = EXCLUDED.email_rate_key,
			attempt_count = 0, created_at = EXCLUDED.created_at,
			expires_at = EXCLUDED.expires_at, last_sent_at = EXCLUDED.last_sent_at,
			consumed_at = NULL, cashier_user_id = NULL, created = false
	`, value.ID, actorID, value.Email, value.Name, value.PasswordHash,
		value.CodeHash, value.EmailRateKey, now, value.ExpiresAt)
	if err != nil {
		return fmt.Errorf("store pending cashier registration: %w", err)
	}
	return tx.Commit(ctx)
}

func (store *Store) ResendCashierRegistration(
	ctx context.Context, actorID, id, codeHash, ipKey string, now, expires time.Time,
) (string, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin cashier code resend: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var email, emailKey string
	var previousExpiry, lastSent time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT normalized_email, email_rate_key, expires_at, last_sent_at, consumed_at
		FROM pending_cashier_registrations
		WHERE id = $1 AND actor_user_id = $2 FOR UPDATE
	`, id, actorID).Scan(&email, &emailKey, &previousExpiry, &lastSent, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrInvalidRegistration
	}
	if err != nil {
		return "", fmt.Errorf("load cashier code resend: %w", err)
	}
	if consumedAt != nil || !now.Before(previousExpiry) {
		return "", domain.ErrInvalidRegistration
	}
	if now.Sub(lastSent) < time.Minute {
		return "", domain.ErrRegistrationCooldown
	}
	if err := registrationLimits(ctx, tx, emailKey, ipKey, now); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE pending_cashier_registrations SET code_hash = $2, expires_at = $3,
			last_sent_at = $4, attempt_count = 0 WHERE id = $1
	`, id, codeHash, expires, now); err != nil {
		return "", fmt.Errorf("update cashier code: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit cashier code resend: %w", err)
	}
	return email, nil
}

func (store *Store) VerifyCashierRegistration(
	ctx context.Context, actorID, id, codeHash string, now time.Time,
) (domain.UserView, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.UserView{}, fmt.Errorf("begin cashier email verification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var email, name, passwordHash, expectedHash string
	var attempts int
	var expiry time.Time
	var consumedAt *time.Time
	var verifiedCashierID *string
	var previouslyCreated bool
	err = tx.QueryRow(ctx, `
		SELECT normalized_email, display_name, password_hash, code_hash,
			attempt_count, expires_at, consumed_at, cashier_user_id, created
		FROM pending_cashier_registrations
		WHERE id = $1 AND actor_user_id = $2 FOR UPDATE
	`, id, actorID).Scan(&email, &name, &passwordHash, &expectedHash,
		&attempts, &expiry, &consumedAt, &verifiedCashierID, &previouslyCreated)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UserView{}, domain.ErrInvalidRegistration
	}
	if err != nil {
		return domain.UserView{}, fmt.Errorf("load cashier email verification: %w", err)
	}
	if consumedAt != nil && verifiedCashierID != nil &&
		subtle.ConstantTimeCompare([]byte(expectedHash), []byte(codeHash)) == 1 {
		if err := tx.Rollback(ctx); err != nil {
			return domain.UserView{}, fmt.Errorf("release replayed cashier verification: %w", err)
		}
		view, err := store.CashierView(ctx, *verifiedCashierID)
		view.Created = previouslyCreated
		return view, err
	}
	if consumedAt != nil || !now.Before(expiry) || attempts >= 5 {
		return domain.UserView{}, domain.ErrInvalidRegistration
	}
	if subtle.ConstantTimeCompare([]byte(expectedHash), []byte(codeHash)) != 1 {
		if _, err := tx.Exec(ctx, `UPDATE pending_cashier_registrations
			SET attempt_count = attempt_count + 1 WHERE id = $1`, id); err != nil {
			return domain.UserView{}, fmt.Errorf("count cashier code attempt: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.UserView{}, fmt.Errorf("commit cashier code attempt: %w", err)
		}
		return domain.UserView{}, domain.ErrInvalidRegistration
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "cashier:"+email); err != nil {
		return domain.UserView{}, fmt.Errorf("lock cashier email: %w", err)
	}
	var cashierID string
	var role domain.Role
	var verifiedAt *time.Time
	created := false
	err = tx.QueryRow(ctx, `SELECT id, role, email_verified_at FROM users
		WHERE normalized_email = $1 FOR UPDATE`, email).Scan(&cashierID, &role, &verifiedAt)
	if err == nil && (role != domain.RoleCashier || verifiedAt != nil) {
		return domain.UserView{}, domain.ErrEmailInUse
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.UserView{}, fmt.Errorf("resolve cashier registration: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		created = true
		err = tx.QueryRow(ctx, `
			INSERT INTO users (name, normalized_email, role, password_change_required, email_verified_at)
			VALUES ($1, $2, 'CASHIER', true, $3) RETURNING id
		`, name, email, now).Scan(&cashierID)
		if isUniqueViolation(err) {
			return domain.UserView{}, domain.ErrEmailInUse
		}
		if err != nil {
			return domain.UserView{}, fmt.Errorf("create verified cashier: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO login_identities
			(user_id, provider, provider_subject, password_hash)
			VALUES ($1, 'PASSWORD', $2, $3)`, cashierID, email, passwordHash); err != nil {
			return domain.UserView{}, fmt.Errorf("create verified cashier login: %w", err)
		}
	} else {
		// An account created by the old flow stays locked until this challenge
		// succeeds. The newly chosen temporary password replaces its old one.
		if _, err := tx.Exec(ctx, `UPDATE users SET name = $2, password_change_required = true,
			email_verified_at = $3 WHERE id = $1`, cashierID, name, now); err != nil {
			return domain.UserView{}, fmt.Errorf("verify existing cashier: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE login_identities SET password_hash = $2
			WHERE user_id = $1 AND provider = 'PASSWORD'`, cashierID, passwordHash); err != nil {
			return domain.UserView{}, fmt.Errorf("replace existing cashier login: %w", err)
		}
		if err := revokeManagedSessions(ctx, tx, cashierID, "EMAIL_VERIFIED_LOGIN_RESET", now); err != nil {
			return domain.UserView{}, err
		}
	}
	if err := insertAudit(ctx, tx, actorID, "CASHIER_EMAIL_VERIFIED", "USER", cashierID, now); err != nil {
		return domain.UserView{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE pending_cashier_registrations
		SET consumed_at = $2, cashier_user_id = $3, created = $4 WHERE id = $1`, id, now, cashierID, created); err != nil {
		return domain.UserView{}, fmt.Errorf("consume cashier code: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.UserView{}, fmt.Errorf("commit cashier email verification: %w", err)
	}
	view, err := store.CashierView(ctx, cashierID)
	view.Created = created
	return view, err
}

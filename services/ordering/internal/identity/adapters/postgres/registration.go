package postgres

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

const (
	registrationEmailHourlyLimit = 5
	registrationIPHourlyLimit    = 100
)

func (store *Store) BeginRegistration(ctx context.Context, value domain.PendingRegistration, ipKey string, now time.Time) (bool, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := registrationLimits(ctx, tx, value.EmailRateKey, ipKey, now); err != nil {
		return false, err
	}
	var owned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE normalized_email = $1)`, value.Email).Scan(&owned); err != nil {
		return false, fmt.Errorf("check registration owner: %w", err)
	}
	var passwordHash *string
	if !owned {
		passwordHash = &value.PasswordHash
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO pending_registrations (
			id, normalized_email, display_name, password_hash, code_hash, email_rate_key,
			eligible, created_at, expires_at, last_sent_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$8)
		ON CONFLICT (normalized_email) DO UPDATE SET
			id = EXCLUDED.id, display_name = EXCLUDED.display_name,
			password_hash = EXCLUDED.password_hash, code_hash = EXCLUDED.code_hash,
			email_rate_key = EXCLUDED.email_rate_key, eligible = EXCLUDED.eligible,
			attempt_count = 0, created_at = EXCLUDED.created_at,
			expires_at = EXCLUDED.expires_at, last_sent_at = EXCLUDED.last_sent_at,
			consumed_at = NULL
	`, value.ID, value.Email, value.Name, passwordHash, value.CodeHash, value.EmailRateKey,
		!owned, now, value.ExpiresAt)
	if err != nil {
		return false, fmt.Errorf("store pending registration: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit registration: %w", err)
	}
	return !owned, nil
}

func (store *Store) ResendRegistration(
	ctx context.Context, id, codeHash, ipKey string, now, expires time.Time,
) (string, bool, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", false, fmt.Errorf("begin registration resend: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var email, emailKey string
	var eligible bool
	var existingExpiry, lastSent time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT normalized_email, email_rate_key, eligible, expires_at, last_sent_at, consumed_at
		FROM pending_registrations WHERE id = $1 FOR UPDATE
	`, id).Scan(&email, &emailKey, &eligible, &existingExpiry, &lastSent, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, domain.ErrInvalidRegistration
	}
	if err != nil {
		return "", false, fmt.Errorf("load registration resend: %w", err)
	}
	if consumedAt != nil || !now.Before(existingExpiry) {
		return "", false, domain.ErrInvalidRegistration
	}
	if now.Sub(lastSent) < time.Minute {
		return "", false, domain.ErrRegistrationCooldown
	}
	if err := registrationLimits(ctx, tx, emailKey, ipKey, now); err != nil {
		return "", false, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE pending_registrations SET code_hash = $2, expires_at = $3,
			last_sent_at = $4, attempt_count = 0 WHERE id = $1
	`, id, codeHash, expires, now)
	if err != nil {
		return "", false, fmt.Errorf("update registration code: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("commit registration resend: %w", err)
	}
	return email, eligible, nil
}

func (store *Store) VerifyRegistration(ctx context.Context, id, codeHash string, now time.Time) (domain.User, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.User{}, fmt.Errorf("begin registration verification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var email, name, expectedHash string
	var passwordHash *string
	var eligible bool
	var attempts int
	var expires time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT normalized_email, display_name, password_hash, code_hash, eligible,
			attempt_count, expires_at, consumed_at
		FROM pending_registrations WHERE id = $1 FOR UPDATE
	`, id).Scan(&email, &name, &passwordHash, &expectedHash, &eligible, &attempts, &expires, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrInvalidRegistration
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("load registration verification: %w", err)
	}
	if consumedAt != nil || !now.Before(expires) || attempts >= 5 {
		return domain.User{}, domain.ErrInvalidRegistration
	}
	if subtle.ConstantTimeCompare([]byte(expectedHash), []byte(codeHash)) != 1 || !eligible || passwordHash == nil {
		if _, err := tx.Exec(ctx, `UPDATE pending_registrations SET attempt_count = attempt_count + 1 WHERE id = $1`, id); err != nil {
			return domain.User{}, fmt.Errorf("count registration attempt: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.User{}, fmt.Errorf("commit registration attempt: %w", err)
		}
		return domain.User{}, domain.ErrInvalidRegistration
	}
	var user domain.User
	err = tx.QueryRow(ctx, `
		INSERT INTO users (name, normalized_email, role)
		VALUES ($1, $2, 'CUSTOMER')
		RETURNING id, name, normalized_email, role, account_status
	`, name, email).Scan(&user.ID, &user.Name, &user.NormalizedEmail, &user.Role, &user.Status)
	if isUniqueViolation(err) {
		return domain.User{}, domain.ErrInvalidRegistration
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("create registered customer: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO login_identities (user_id, provider, provider_subject, password_hash)
		VALUES ($1, 'PASSWORD', $2, $3)
	`, user.ID, email, *passwordHash)
	if err != nil {
		return domain.User{}, fmt.Errorf("create registered password identity: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE pending_registrations SET consumed_at = $2 WHERE id = $1`, id, now)
	if err != nil {
		return domain.User{}, fmt.Errorf("consume registration: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit registration verification: %w", err)
	}
	return user, nil
}

func (store *Store) DeleteExpiredRegistrations(ctx context.Context, now time.Time) error {
	if _, err := store.pool.Exec(ctx, `DELETE FROM pending_registrations WHERE expires_at <= $1 OR consumed_at < $1`, now); err != nil {
		return fmt.Errorf("delete expired registrations: %w", err)
	}
	if _, err := store.pool.Exec(ctx, `DELETE FROM registration_rate_buckets WHERE window_started_at < $1`, now.Add(-2*time.Hour)); err != nil {
		return fmt.Errorf("delete registration rate buckets: %w", err)
	}
	return nil
}

func registrationLimits(ctx context.Context, tx pgx.Tx, emailKey, ipKey string, now time.Time) error {
	window := now.Truncate(time.Hour)
	for _, limit := range []struct {
		key     string
		maximum int
	}{
		{emailKey, registrationEmailHourlyLimit}, {ipKey, registrationIPHourlyLimit},
	} {
		var count int
		err := tx.QueryRow(ctx, `
			INSERT INTO registration_rate_buckets (bucket_key, window_started_at, request_count)
			VALUES ($1, $2, 1)
			ON CONFLICT (bucket_key, window_started_at)
			DO UPDATE SET request_count = registration_rate_buckets.request_count + 1
			RETURNING request_count
		`, limit.key, window).Scan(&count)
		if err != nil {
			return fmt.Errorf("count registration rate: %w", err)
		}
		if count > limit.maximum {
			return domain.ErrRegistrationRateLimited
		}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

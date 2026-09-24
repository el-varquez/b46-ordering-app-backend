package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/ports"
)

type Store struct{ pool *pgxpool.Pool }

var _ ports.Store = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (store *Store) FindPassword(ctx context.Context, email string) (domain.PasswordRecord, error) {
	var record domain.PasswordRecord
	err := store.pool.QueryRow(ctx, `
		SELECT u.id, u.name, u.normalized_email, u.role, u.account_status, u.password_change_required, i.password_hash
		FROM users u
		JOIN login_identities i ON i.user_id = u.id
		WHERE i.provider = 'PASSWORD' AND i.provider_subject = $1
		  AND (u.role <> 'CASHIER' OR u.email_verified_at IS NOT NULL)
	`, email).Scan(
		&record.User.ID, &record.User.Name, &record.User.NormalizedEmail,
		&record.User.Role, &record.User.Status, &record.User.PasswordChangeRequired, &record.PasswordHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PasswordRecord{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return domain.PasswordRecord{}, fmt.Errorf("query password identity: %w", err)
	}
	return record, nil
}

func (store *Store) UpdatePasswordHash(ctx context.Context, userID, encoded string) error {
	result, err := store.pool.Exec(ctx, `
		UPDATE login_identities
		SET password_hash = $2
		WHERE user_id = $1 AND provider = 'PASSWORD'
	`, userID, encoded)
	if err != nil {
		return fmt.Errorf("update password identity: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrInvalidCredentials
	}
	return nil
}

func (store *Store) CreateSession(ctx context.Context, value domain.NewSession) (domain.User, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.User{}, fmt.Errorf("begin session transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	user, err := userByIDForUpdate(ctx, tx, value.UserID)
	if err != nil {
		return domain.User{}, err
	}
	if user.Status != domain.AccountActive {
		return domain.User{}, domain.ErrAccountDisabled
	}
	if _, err := insertSession(ctx, tx, value); err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit session transaction: %w", err)
	}
	return user, nil
}

func (store *Store) AuthenticateAccess(ctx context.Context, hash string, now time.Time) (domain.Principal, error) {
	var principal domain.Principal
	err := store.pool.QueryRow(ctx, `
		SELECT u.id, u.role, u.account_status, s.family_id, u.password_change_required
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.access_token_hash = $1
		  AND s.access_expires_at > $2
		  AND s.revoked_at IS NULL
		  AND (u.role <> 'CASHIER' OR u.email_verified_at IS NOT NULL)
	`, hash, now).Scan(&principal.UserID, &principal.Role, &principal.Status, &principal.FamilyID, &principal.PasswordChangeRequired)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.Principal{}, fmt.Errorf("query access session: %w", err)
	}
	if principal.Status != domain.AccountActive {
		return domain.Principal{}, domain.ErrAccountDisabled
	}
	_, _ = store.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE access_token_hash = $1`, hash, now)
	return principal, nil
}

func (store *Store) RotateRefresh(
	ctx context.Context,
	presentedHash string,
	replacement domain.NewSession,
	now time.Time,
) (domain.User, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.User{}, fmt.Errorf("begin refresh transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var user domain.User
	var sessionID, familyID string
	var refreshExpiry time.Time
	var consumedAt, revokedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT s.id, s.family_id, s.refresh_expires_at,
		       s.refresh_consumed_at, s.revoked_at,
		       u.id, u.name, u.normalized_email, u.role, u.account_status, u.password_change_required
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.refresh_token_hash = $1
		FOR UPDATE OF s, u
	`, presentedHash).Scan(
		&sessionID, &familyID, &refreshExpiry, &consumedAt, &revokedAt,
		&user.ID, &user.Name, &user.NormalizedEmail, &user.Role, &user.Status, &user.PasswordChangeRequired,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("lock refresh session: %w", err)
	}

	if consumedAt != nil {
		if err := revokeFamily(ctx, tx, familyID, "REFRESH_REUSE", now); err != nil {
			return domain.User{}, err
		}
		if err := insertAudit(ctx, tx, user.ID, "SESSION_REFRESH_REUSE_DETECTED", "SESSION_FAMILY", familyID, now); err != nil {
			return domain.User{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.User{}, fmt.Errorf("commit replay revocation: %w", err)
		}
		return domain.User{}, domain.ErrRefreshReuse
	}
	if revokedAt != nil || !refreshExpiry.After(now) {
		return domain.User{}, domain.ErrUnauthenticated
	}
	if user.Status != domain.AccountActive {
		return domain.User{}, domain.ErrAccountDisabled
	}

	replacement.UserID = user.ID
	replacement.FamilyID = familyID
	replacement.ParentSessionID = sessionID
	replacementID, err := insertSession(ctx, tx, replacement)
	if err != nil {
		return domain.User{}, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE sessions
		SET refresh_consumed_at = GREATEST($2, created_at),
		    revoked_at = GREATEST($2, created_at),
		    revoked_reason = 'ROTATED',
		    replaced_by_session_id = $3
		WHERE id = $1
	`, sessionID, now, replacementID)
	if err != nil {
		return domain.User{}, fmt.Errorf("consume refresh session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit refresh transaction: %w", err)
	}
	return user, nil
}

func (store *Store) RevokeFamily(ctx context.Context, familyID, reason string, now time.Time) error {
	_, err := store.pool.Exec(ctx, `
		WITH revoked AS (
			UPDATE sessions
			SET revoked_at = COALESCE(revoked_at, GREATEST($2, created_at)),
			    revoked_reason = COALESCE(revoked_reason, $3)
			WHERE family_id = $1
			RETURNING user_id
		)
		INSERT INTO audit_records (actor_user_id, action, target_type, target_id, occurred_at)
		SELECT user_id, 'SESSION_FAMILY_REVOKED', 'SESSION_FAMILY', $1::text, $2
		FROM revoked
		LIMIT 1
	`, familyID, now, reason)
	if err != nil {
		return fmt.Errorf("revoke session family: %w", err)
	}
	return nil
}

func insertSession(ctx context.Context, tx pgx.Tx, value domain.NewSession) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO sessions (
			user_id, family_id, parent_session_id,
			access_token_hash, refresh_token_hash,
			access_expires_at, refresh_expires_at
		) SELECT
			u.id,
			CASE WHEN $2 = '' THEN gen_random_uuid() ELSE $2::uuid END,
			NULLIF($3, '')::uuid,
			$4, $5, $6, $7
		FROM users u WHERE u.id = $1
		  AND (u.role <> 'CASHIER' OR u.email_verified_at IS NOT NULL)
		RETURNING id
	`, value.UserID, value.FamilyID, value.ParentSessionID,
		value.AccessTokenHash, value.RefreshTokenHash,
		value.AccessExpiresAt, value.RefreshExpiresAt,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrUnauthenticated
	}
	if err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	return id, nil
}

func revokeFamily(ctx context.Context, tx pgx.Tx, familyID, reason string, now time.Time) error {
	_, err := tx.Exec(ctx, `
		UPDATE sessions
		SET revoked_at = COALESCE(revoked_at, GREATEST($2, created_at)),
		    revoked_reason = COALESCE(revoked_reason, $3)
		WHERE family_id = $1
	`, familyID, now, reason)
	if err != nil {
		return fmt.Errorf("revoke session family: %w", err)
	}
	return nil
}

func (store *Store) CreateOAuthIntent(ctx context.Context, value domain.OAuthIntent) (domain.OAuthIntent, error) {
	err := store.pool.QueryRow(ctx, `
		INSERT INTO oauth_intents (provider, purpose, bound_user_id, nonce_hash, expires_at)
		VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5)
		RETURNING id
	`, value.Provider, value.Purpose, value.BoundUserID, value.NonceHash, value.ExpiresAt).
		Scan(&value.ID)
	if err != nil {
		return domain.OAuthIntent{}, fmt.Errorf("insert oauth intent: %w", err)
	}
	return value, nil
}

func (store *Store) FindOAuthIntent(ctx context.Context, id string, now time.Time) (domain.OAuthIntent, error) {
	var value domain.OAuthIntent
	err := store.pool.QueryRow(ctx, `
		SELECT id, provider, purpose, COALESCE(bound_user_id::text, ''), nonce_hash, expires_at
		FROM oauth_intents
		WHERE id = $1 AND consumed_at IS NULL AND expires_at > $2
	`, id, now).Scan(
		&value.ID, &value.Provider, &value.Purpose, &value.BoundUserID,
		&value.NonceHash, &value.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OAuthIntent{}, domain.ErrInvalidOAuthIntent
	}
	if err != nil {
		return domain.OAuthIntent{}, fmt.Errorf("query oauth intent: %w", err)
	}
	return value, nil
}

func (store *Store) CompleteOAuthLogin(
	ctx context.Context,
	intentID string,
	verified domain.VerifiedIdentity,
	session domain.NewSession,
	now time.Time,
) (domain.User, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.User{}, fmt.Errorf("begin oauth login: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	intent, err := lockIntent(ctx, tx, intentID, now)
	if err != nil || intent.Purpose != domain.OAuthLogin || intent.Provider != verified.Provider {
		return domain.User{}, domain.ErrInvalidOAuthIntent
	}

	user, err := userByProviderSubject(ctx, tx, verified.Provider, verified.Subject)
	if errors.Is(err, pgx.ErrNoRows) {
		_, emailErr := userByEmail(ctx, tx, verified.Email)
		if emailErr == nil {
			if err := consumeIntent(ctx, tx, intentID, now); err != nil {
				return domain.User{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return domain.User{}, fmt.Errorf("commit link-required intent: %w", err)
			}
			return domain.User{}, domain.ErrIdentityLinkRequired
		}
		if !errors.Is(emailErr, pgx.ErrNoRows) {
			return domain.User{}, fmt.Errorf("check oauth email owner: %w", emailErr)
		}

		err = tx.QueryRow(ctx, `
			INSERT INTO users (name, normalized_email, role)
			VALUES ($1, $2, 'CUSTOMER')
			RETURNING id, name, normalized_email, role, account_status
		`, verified.SuggestedName, verified.Email).Scan(
			&user.ID, &user.Name, &user.NormalizedEmail, &user.Role, &user.Status,
		)
		if err != nil {
			return domain.User{}, fmt.Errorf("create oauth customer: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO login_identities (user_id, provider, provider_subject)
			VALUES ($1, $2, $3)
		`, user.ID, verified.Provider, verified.Subject)
		if err != nil {
			return domain.User{}, mapIdentityConflict(err)
		}
	} else if err != nil {
		return domain.User{}, fmt.Errorf("resolve oauth identity: %w", err)
	}
	if user.Status != domain.AccountActive {
		return domain.User{}, domain.ErrAccountDisabled
	}

	session.UserID = user.ID
	if _, err := insertSession(ctx, tx, session); err != nil {
		return domain.User{}, err
	}
	if err := consumeIntent(ctx, tx, intentID, now); err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit oauth login: %w", err)
	}
	return user, nil
}

func (store *Store) CompleteOAuthLink(
	ctx context.Context,
	intentID, userID string,
	verified domain.VerifiedIdentity,
	now time.Time,
) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin oauth link: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	intent, err := lockIntent(ctx, tx, intentID, now)
	if err != nil || intent.Purpose != domain.OAuthLink ||
		intent.BoundUserID != userID || intent.Provider != verified.Provider {
		return domain.ErrInvalidOAuthIntent
	}
	user, err := userByIDForUpdate(ctx, tx, userID)
	if err != nil {
		return err
	}
	if user.Status != domain.AccountActive {
		return domain.ErrAccountDisabled
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO login_identities (user_id, provider, provider_subject)
		VALUES ($1, $2, $3)
	`, userID, verified.Provider, verified.Subject)
	if err != nil {
		return mapIdentityConflict(err)
	}
	if err := consumeIntent(ctx, tx, intentID, now); err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, userID, "OAUTH_IDENTITY_LINKED", "USER", userID, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit oauth link: %w", err)
	}
	return nil
}

func (store *Store) UserView(ctx context.Context, userID string) (domain.UserView, error) {
	var view domain.UserView
	err := store.pool.QueryRow(ctx, `
		SELECT id, name, normalized_email, role, account_status, password_change_required
		FROM users WHERE id = $1
	`, userID).Scan(
		&view.User.ID, &view.User.Name, &view.User.NormalizedEmail,
		&view.User.Role, &view.User.Status, &view.User.PasswordChangeRequired,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UserView{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.UserView{}, fmt.Errorf("query current user: %w", err)
	}
	rows, err := store.pool.Query(ctx, `
		SELECT provider FROM login_identities WHERE user_id = $1 ORDER BY provider
	`, userID)
	if err != nil {
		return domain.UserView{}, fmt.Errorf("query current providers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var provider domain.Provider
		if err := rows.Scan(&provider); err != nil {
			return domain.UserView{}, fmt.Errorf("scan current provider: %w", err)
		}
		view.Providers = append(view.Providers, provider)
	}
	if err := rows.Err(); err != nil {
		return domain.UserView{}, fmt.Errorf("iterate current providers: %w", err)
	}
	return view, nil
}

func (store *Store) BootstrapAdmin(
	ctx context.Context,
	name, email, passwordHash string,
	now time.Time,
) (domain.User, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.User{}, fmt.Errorf("begin admin bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('b46-bootstrap-admin'))`); err != nil {
		return domain.User{}, fmt.Errorf("lock admin bootstrap: %w", err)
	}

	user, err := userByRole(ctx, tx, domain.RoleAdmin)
	if err == nil {
		if user.NormalizedEmail == email {
			if err := tx.Commit(ctx); err != nil {
				return domain.User{}, fmt.Errorf("commit idempotent bootstrap: %w", err)
			}
			return user, nil
		}
		return domain.User{}, domain.ErrAdminAlreadyExists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, fmt.Errorf("check existing admin: %w", err)
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO users (name, normalized_email, role)
		VALUES ($1, $2, 'ADMIN')
		RETURNING id, name, normalized_email, role, account_status
	`, name, email).Scan(&user.ID, &user.Name, &user.NormalizedEmail, &user.Role, &user.Status)
	if err != nil {
		return domain.User{}, fmt.Errorf("insert bootstrap admin: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO login_identities (user_id, provider, provider_subject, password_hash)
		VALUES ($1, 'PASSWORD', $2, $3)
	`, user.ID, email, passwordHash)
	if err != nil {
		return domain.User{}, fmt.Errorf("insert admin password identity: %w", err)
	}
	if err := insertAudit(ctx, tx, user.ID, "ADMIN_BOOTSTRAPPED", "USER", user.ID, now); err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("commit admin bootstrap: %w", err)
	}
	return user, nil
}

func (store *Store) DisableUser(ctx context.Context, actorID, userID string, now time.Time) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin disable user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE users SET account_status = 'DISABLED' WHERE id = $1
	`, userID)
	if err != nil {
		return fmt.Errorf("disable user: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrInvalidInput
	}
	_, err = tx.Exec(ctx, `
		UPDATE sessions
		SET revoked_at = COALESCE(revoked_at, GREATEST($2, created_at)),
		    revoked_reason = COALESCE(revoked_reason, 'ACCOUNT_DISABLED')
		WHERE user_id = $1
	`, userID, now)
	if err != nil {
		return fmt.Errorf("revoke disabled user sessions: %w", err)
	}
	if err := insertAudit(ctx, tx, actorID, "ACCOUNT_DISABLED", "USER", userID, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit disable user: %w", err)
	}
	return nil
}

func lockIntent(ctx context.Context, tx pgx.Tx, id string, now time.Time) (domain.OAuthIntent, error) {
	var value domain.OAuthIntent
	err := tx.QueryRow(ctx, `
		SELECT id, provider, purpose, COALESCE(bound_user_id::text, ''), nonce_hash, expires_at
		FROM oauth_intents
		WHERE id = $1 AND consumed_at IS NULL AND expires_at > $2
		FOR UPDATE
	`, id, now).Scan(
		&value.ID, &value.Provider, &value.Purpose, &value.BoundUserID,
		&value.NonceHash, &value.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OAuthIntent{}, domain.ErrInvalidOAuthIntent
	}
	if err != nil {
		return domain.OAuthIntent{}, fmt.Errorf("lock oauth intent: %w", err)
	}
	return value, nil
}

func consumeIntent(ctx context.Context, tx pgx.Tx, id string, now time.Time) error {
	result, err := tx.Exec(ctx, `
		UPDATE oauth_intents SET consumed_at = GREATEST($2, created_at)
		WHERE id = $1 AND consumed_at IS NULL
	`, id, now)
	if err != nil {
		return fmt.Errorf("consume oauth intent: %w", err)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrInvalidOAuthIntent
	}
	return nil
}

func userByIDForUpdate(ctx context.Context, tx pgx.Tx, id string) (domain.User, error) {
	var user domain.User
	err := tx.QueryRow(ctx, `
		SELECT id, name, normalized_email, role, account_status, password_change_required
		FROM users WHERE id = $1 FOR UPDATE
	`, id).Scan(&user.ID, &user.Name, &user.NormalizedEmail, &user.Role, &user.Status, &user.PasswordChangeRequired)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("lock user: %w", err)
	}
	return user, nil
}

func userByProviderSubject(ctx context.Context, tx pgx.Tx, provider domain.Provider, subject string) (domain.User, error) {
	var user domain.User
	err := tx.QueryRow(ctx, `
		SELECT u.id, u.name, u.normalized_email, u.role, u.account_status, u.password_change_required
		FROM login_identities i
		JOIN users u ON u.id = i.user_id
		WHERE i.provider = $1 AND i.provider_subject = $2
		FOR UPDATE OF u
	`, provider, subject).Scan(&user.ID, &user.Name, &user.NormalizedEmail, &user.Role, &user.Status, &user.PasswordChangeRequired)
	return user, err
}

func userByEmail(ctx context.Context, tx pgx.Tx, email string) (domain.User, error) {
	var user domain.User
	err := tx.QueryRow(ctx, `
		SELECT id, name, normalized_email, role, account_status
		FROM users WHERE normalized_email = $1 FOR UPDATE
	`, email).Scan(&user.ID, &user.Name, &user.NormalizedEmail, &user.Role, &user.Status)
	return user, err
}

func userByRole(ctx context.Context, tx pgx.Tx, role domain.Role) (domain.User, error) {
	var user domain.User
	err := tx.QueryRow(ctx, `
		SELECT id, name, normalized_email, role, account_status
		FROM users WHERE role = $1 LIMIT 1 FOR UPDATE
	`, role).Scan(&user.ID, &user.Name, &user.NormalizedEmail, &user.Role, &user.Status)
	return user, err
}

func insertAudit(
	ctx context.Context,
	tx pgx.Tx,
	actorID, action, targetType, targetID string,
	now time.Time,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_records (actor_user_id, action, target_type, target_id, details, occurred_at)
		VALUES (NULLIF($1, '')::uuid, $2, $3, $4, jsonb_build_object('result', 'success'), $5)
	`, actorID, action, targetType, targetID, now)
	if err != nil {
		return fmt.Errorf("insert identity audit record: %w", err)
	}
	return nil
}

func mapIdentityConflict(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return domain.ErrIdentityLinked
	}
	return fmt.Errorf("insert login identity: %w", err)
}

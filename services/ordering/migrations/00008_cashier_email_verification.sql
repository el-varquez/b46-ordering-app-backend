-- +goose Up
ALTER TABLE users ADD COLUMN email_verified_at timestamptz;

-- Existing cashier accounts were created without an email challenge. They
-- require verification before any password, OAuth, or existing session works.
UPDATE sessions SET revoked_at = COALESCE(revoked_at, now()),
    revoked_reason = COALESCE(revoked_reason, 'EMAIL_UNVERIFIED')
WHERE user_id IN (SELECT id FROM users WHERE role = 'CASHIER')
  AND revoked_at IS NULL;

CREATE TABLE pending_cashier_registrations (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 32 AND 128),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    cashier_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    created boolean NOT NULL DEFAULT false,
    normalized_email text NOT NULL UNIQUE,
    display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 120),
    password_hash text NOT NULL,
    code_hash text NOT NULL CHECK (length(code_hash) = 64),
    email_rate_key text NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 5),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    last_sent_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT pending_cashier_registration_expiry CHECK (expires_at > created_at)
);

CREATE INDEX pending_cashier_registrations_expiry_idx
    ON pending_cashier_registrations (expires_at);

-- +goose Down
DROP TABLE pending_cashier_registrations;
ALTER TABLE users DROP COLUMN email_verified_at;

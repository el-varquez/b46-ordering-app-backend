-- +goose Up
ALTER TABLE sessions RENAME COLUMN expires_at TO refresh_expires_at;

ALTER TABLE sessions
    ADD COLUMN family_id uuid,
    ADD COLUMN access_token_hash text,
    ADD COLUMN access_expires_at timestamptz,
    ADD COLUMN parent_session_id uuid REFERENCES sessions(id),
    ADD COLUMN replaced_by_session_id uuid REFERENCES sessions(id),
    ADD COLUMN refresh_consumed_at timestamptz,
    ADD COLUMN revoked_reason text,
    ADD COLUMN last_seen_at timestamptz;

UPDATE sessions
SET family_id = id,
    access_token_hash = md5('access:' || id::text) || md5('access-2:' || id::text),
    access_expires_at = created_at + interval '15 minutes',
    refresh_token_hash = CASE
        WHEN length(refresh_token_hash) = 64 THEN refresh_token_hash
        ELSE md5('refresh:' || id::text) || md5('refresh-2:' || id::text)
    END;

ALTER TABLE sessions
    ALTER COLUMN family_id SET NOT NULL,
    ALTER COLUMN access_token_hash SET NOT NULL,
    ALTER COLUMN access_expires_at SET NOT NULL,
    ADD CONSTRAINT sessions_access_hash_length CHECK (length(access_token_hash) = 64),
    ADD CONSTRAINT sessions_refresh_hash_length CHECK (length(refresh_token_hash) = 64),
    ADD CONSTRAINT sessions_access_expiry_after_creation CHECK (access_expires_at > created_at),
    ADD CONSTRAINT sessions_refresh_consumed_after_creation CHECK (
        refresh_consumed_at IS NULL OR refresh_consumed_at >= created_at
    ),
    ADD CONSTRAINT sessions_revoked_reason_shape CHECK (
        (revoked_at IS NULL AND revoked_reason IS NULL) OR
        (revoked_at IS NOT NULL AND revoked_reason IS NOT NULL)
    );

CREATE UNIQUE INDEX sessions_access_token_hash_unique
    ON sessions (access_token_hash);
CREATE INDEX sessions_refresh_token_lookup
    ON sessions (refresh_token_hash);
CREATE INDEX sessions_active_family_idx
    ON sessions (family_id, refresh_expires_at)
    WHERE revoked_at IS NULL;

DROP INDEX sessions_active_user_idx;
CREATE INDEX sessions_active_user_idx
    ON sessions (user_id, refresh_expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE oauth_intents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider text NOT NULL CHECK (provider IN ('GOOGLE', 'APPLE')),
    purpose text NOT NULL CHECK (purpose IN ('LOGIN', 'LINK')),
    bound_user_id uuid REFERENCES users(id) ON DELETE CASCADE,
    nonce_hash text NOT NULL CHECK (length(nonce_hash) = 64),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT oauth_intents_purpose_user_shape CHECK (
        (purpose = 'LOGIN' AND bound_user_id IS NULL) OR
        (purpose = 'LINK' AND bound_user_id IS NOT NULL)
    ),
    CONSTRAINT oauth_intents_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT oauth_intents_consumed_after_creation CHECK (
        consumed_at IS NULL OR consumed_at >= created_at
    )
);

CREATE INDEX oauth_intents_usable_idx
    ON oauth_intents (id, expires_at)
    WHERE consumed_at IS NULL;

-- +goose Down
DROP TABLE oauth_intents;
DROP INDEX sessions_active_user_idx;
DROP INDEX sessions_active_family_idx;
DROP INDEX sessions_refresh_token_lookup;
DROP INDEX sessions_access_token_hash_unique;

ALTER TABLE sessions
    DROP CONSTRAINT sessions_revoked_reason_shape,
    DROP CONSTRAINT sessions_refresh_consumed_after_creation,
    DROP CONSTRAINT sessions_access_expiry_after_creation,
    DROP CONSTRAINT sessions_refresh_hash_length,
    DROP CONSTRAINT sessions_access_hash_length,
    DROP COLUMN last_seen_at,
    DROP COLUMN revoked_reason,
    DROP COLUMN refresh_consumed_at,
    DROP COLUMN replaced_by_session_id,
    DROP COLUMN parent_session_id,
    DROP COLUMN access_expires_at,
    DROP COLUMN access_token_hash,
    DROP COLUMN family_id;

ALTER TABLE sessions RENAME COLUMN refresh_expires_at TO expires_at;

CREATE INDEX sessions_active_user_idx
    ON sessions (user_id, expires_at)
    WHERE revoked_at IS NULL;
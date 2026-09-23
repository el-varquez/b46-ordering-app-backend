-- +goose Up
CREATE TABLE pending_registrations (
    id text PRIMARY KEY CHECK (length(id) BETWEEN 32 AND 128),
    normalized_email text NOT NULL UNIQUE,
    display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 120),
    password_hash text,
    code_hash text NOT NULL CHECK (length(code_hash) = 64),
    email_rate_key text NOT NULL,
    eligible boolean NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 5),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    last_sent_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT pending_registration_password_shape CHECK (eligible = (password_hash IS NOT NULL)),
    CONSTRAINT pending_registration_expiry CHECK (expires_at > created_at)
);

CREATE INDEX pending_registrations_expiry_idx ON pending_registrations (expires_at);

CREATE TABLE registration_rate_buckets (
    bucket_key text NOT NULL,
    window_started_at timestamptz NOT NULL,
    request_count integer NOT NULL CHECK (request_count > 0),
    PRIMARY KEY (bucket_key, window_started_at)
);

CREATE INDEX registration_rate_buckets_expiry_idx ON registration_rate_buckets (window_started_at);

-- +goose Down
DROP TABLE registration_rate_buckets;
DROP TABLE pending_registrations;

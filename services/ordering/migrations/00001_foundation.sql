-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
    normalized_email text NOT NULL UNIQUE CHECK (normalized_email = lower(btrim(normalized_email))),
    role text NOT NULL CHECK (role IN ('CUSTOMER', 'CASHIER', 'ADMIN')),
    account_status text NOT NULL DEFAULT 'ACTIVE' CHECK (account_status IN ('ACTIVE', 'DISABLED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE login_identities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('PASSWORD', 'GOOGLE', 'APPLE')),
    provider_subject text NOT NULL CHECK (length(provider_subject) BETWEEN 1 AND 255),
    password_hash text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT login_identities_provider_subject_unique UNIQUE (provider, provider_subject),
    CONSTRAINT login_identities_user_provider_unique UNIQUE (user_id, provider),
    CONSTRAINT login_identities_password_shape CHECK (
        (provider = 'PASSWORD' AND password_hash IS NOT NULL) OR
        (provider <> 'PASSWORD' AND password_hash IS NULL)
    )
);

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash text NOT NULL UNIQUE CHECK (length(refresh_token_hash) >= 32),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sessions_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT sessions_revocation_after_creation CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE TABLE orders (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    checkout_id uuid NOT NULL UNIQUE,
    customer_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'SUBMITTED' CHECK (status IN ('SUBMITTED', 'CONFIRMED', 'REJECTED')),
    rejection_code text,
    subtotal_centavos bigint NOT NULL CHECK (subtotal_centavos >= 0),
    delivery_fee_centavos bigint NOT NULL DEFAULT 0 CHECK (delivery_fee_centavos >= 0),
    total_centavos bigint NOT NULL CHECK (total_centavos >= 0),
    delivery_address text NOT NULL CHECK (length(btrim(delivery_address)) BETWEEN 1 AND 500),
    delivery_notes text NOT NULL DEFAULT '' CHECK (length(delivery_notes) <= 500),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT orders_total_matches_components CHECK (total_centavos = subtotal_centavos + delivery_fee_centavos),
    CONSTRAINT orders_rejection_shape CHECK (
        (status = 'REJECTED' AND rejection_code IS NOT NULL) OR
        (status <> 'REJECTED' AND rejection_code IS NULL)
    )
);

CREATE TABLE order_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id text NOT NULL CHECK (length(product_id) BETWEEN 1 AND 100),
    product_name text NOT NULL CHECK (length(btrim(product_name)) BETWEEN 1 AND 200),
    unit_price_centavos bigint NOT NULL CHECK (unit_price_centavos >= 0),
    quantity integer NOT NULL CHECK (quantity > 0),
    line_total_centavos bigint NOT NULL CHECK (line_total_centavos >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT order_lines_product_once_per_order UNIQUE (order_id, product_id),
    CONSTRAINT order_lines_total_matches_quantity CHECK (line_total_centavos = unit_price_centavos * quantity)
);

CREATE TABLE fulfillments (
    order_id uuid PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'PREPARING' CHECK (status IN ('PREPARING', 'DELIVERING', 'DELIVERED')),
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    preparing_at timestamptz NOT NULL DEFAULT now(),
    delivering_at timestamptz,
    delivered_at timestamptz,
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fulfillments_timeline CHECK (
        (status = 'PREPARING' AND delivering_at IS NULL AND delivered_at IS NULL) OR
        (status = 'DELIVERING' AND delivering_at IS NOT NULL AND delivered_at IS NULL) OR
        (status = 'DELIVERED' AND delivering_at IS NOT NULL AND delivered_at IS NOT NULL AND delivered_at >= delivering_at)
    )
);

CREATE TABLE ordering_outbox (
    event_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_id uuid NOT NULL UNIQUE,
    order_id uuid NOT NULL UNIQUE REFERENCES orders(id) ON DELETE CASCADE,
    event_type text NOT NULL CHECK (event_type = 'InventoryCommitRequested'),
    schema_version text NOT NULL DEFAULT '1.0' CHECK (schema_version = '1.0'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_at timestamptz,
    last_error_code text
);

CREATE INDEX ordering_outbox_claim_idx
    ON ordering_outbox (next_attempt_at, occurred_at)
    WHERE published_at IS NULL;

CREATE TABLE inventory_operations (
    operation_id uuid PRIMARY KEY,
    order_id uuid NOT NULL UNIQUE REFERENCES orders(id) ON DELETE CASCADE,
    result_status text NOT NULL DEFAULT 'PENDING' CHECK (result_status IN ('PENDING', 'COMMITTED', 'ITEMS_UNAVAILABLE')),
    result_event_id uuid UNIQUE,
    unavailable_items jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CONSTRAINT inventory_operations_result_shape CHECK (
        (result_status = 'PENDING' AND result_event_id IS NULL AND completed_at IS NULL AND unavailable_items IS NULL) OR
        (result_status = 'COMMITTED' AND result_event_id IS NOT NULL AND completed_at IS NOT NULL AND unavailable_items IS NULL) OR
        (result_status = 'ITEMS_UNAVAILABLE' AND result_event_id IS NOT NULL AND completed_at IS NOT NULL AND jsonb_typeof(unavailable_items) = 'array')
    )
);

CREATE TABLE audit_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid REFERENCES users(id),
    action text NOT NULL CHECK (length(action) BETWEEN 1 AND 100),
    target_type text NOT NULL CHECK (length(target_type) BETWEEN 1 AND 100),
    target_id text NOT NULL CHECK (length(target_id) BETWEEN 1 AND 255),
    details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_records_target_idx ON audit_records (target_type, target_id, occurred_at DESC);
CREATE INDEX sessions_active_user_idx ON sessions (user_id, expires_at) WHERE revoked_at IS NULL;
CREATE INDEX orders_customer_created_idx ON orders (customer_id, created_at DESC);
CREATE INDEX orders_status_created_idx ON orders (status, created_at);

CREATE TRIGGER users_set_updated_at
BEFORE UPDATE ON users
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER login_identities_set_updated_at
BEFORE UPDATE ON login_identities
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER orders_set_updated_at
BEFORE UPDATE ON orders
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER fulfillments_set_updated_at
BEFORE UPDATE ON fulfillments
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE audit_records;
DROP TABLE inventory_operations;
DROP TABLE ordering_outbox;
DROP TABLE fulfillments;
DROP TABLE order_lines;
DROP TABLE orders;
DROP TABLE sessions;
DROP TABLE login_identities;
DROP TABLE users;
DROP FUNCTION set_updated_at();

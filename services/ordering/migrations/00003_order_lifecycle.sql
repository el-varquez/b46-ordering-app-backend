-- +goose Up
ALTER TABLE orders
    ADD COLUMN checkout_fingerprint text;

UPDATE orders
SET checkout_fingerprint = md5('legacy-checkout:' || checkout_id::text) ||
                           md5('legacy-checkout-2:' || checkout_id::text);

ALTER TABLE orders
    ALTER COLUMN checkout_fingerprint SET NOT NULL,
    ADD CONSTRAINT orders_checkout_fingerprint_length
        CHECK (length(checkout_fingerprint) = 64);

CREATE INDEX orders_customer_page_idx
    ON orders (customer_id, created_at DESC, id DESC);

ALTER TABLE fulfillments
    ALTER COLUMN updated_by DROP NOT NULL,
    ADD COLUMN cashier_read_at timestamptz,
    ADD COLUMN cashier_read_by uuid REFERENCES users(id),
    DROP CONSTRAINT fulfillments_timeline,
    ADD CONSTRAINT fulfillments_read_shape CHECK (
        (cashier_read_at IS NULL AND cashier_read_by IS NULL) OR
        (cashier_read_at IS NOT NULL AND cashier_read_by IS NOT NULL)
    ),
    ADD CONSTRAINT fulfillments_timeline CHECK (
        (status = 'PREPARING' AND delivering_at IS NULL AND delivered_at IS NULL) OR
        (status = 'DELIVERING' AND delivering_at IS NOT NULL AND delivered_at IS NULL AND updated_by IS NOT NULL) OR
        (status = 'DELIVERED' AND delivering_at IS NOT NULL AND delivered_at IS NOT NULL AND
            delivered_at >= delivering_at AND updated_by IS NOT NULL)
    );

CREATE INDEX fulfillments_cashier_queue_idx
    ON fulfillments (status, created_at DESC, order_id DESC);
CREATE INDEX fulfillments_unread_idx
    ON fulfillments (created_at DESC, order_id DESC)
    WHERE cashier_read_at IS NULL;

ALTER TABLE ordering_outbox
    ADD COLUMN claim_token uuid;

DROP INDEX ordering_outbox_claim_idx;
CREATE INDEX ordering_outbox_claim_idx
    ON ordering_outbox (next_attempt_at, locked_at, occurred_at)
    WHERE published_at IS NULL;

-- +goose Down
DROP INDEX ordering_outbox_claim_idx;
CREATE INDEX ordering_outbox_claim_idx
    ON ordering_outbox (next_attempt_at, occurred_at)
    WHERE published_at IS NULL;

ALTER TABLE ordering_outbox
    DROP COLUMN claim_token;

DROP INDEX fulfillments_unread_idx;
DROP INDEX fulfillments_cashier_queue_idx;

ALTER TABLE fulfillments
    DROP CONSTRAINT fulfillments_timeline,
    DROP CONSTRAINT fulfillments_read_shape,
    DROP COLUMN cashier_read_by,
    DROP COLUMN cashier_read_at,
    ALTER COLUMN updated_by SET NOT NULL,
    ADD CONSTRAINT fulfillments_timeline CHECK (
        (status = 'PREPARING' AND delivering_at IS NULL AND delivered_at IS NULL) OR
        (status = 'DELIVERING' AND delivering_at IS NOT NULL AND delivered_at IS NULL) OR
        (status = 'DELIVERED' AND delivering_at IS NOT NULL AND delivered_at IS NOT NULL AND delivered_at >= delivering_at)
    );

DROP INDEX orders_customer_page_idx;

ALTER TABLE orders
    DROP CONSTRAINT orders_checkout_fingerprint_length,
    DROP COLUMN checkout_fingerprint;

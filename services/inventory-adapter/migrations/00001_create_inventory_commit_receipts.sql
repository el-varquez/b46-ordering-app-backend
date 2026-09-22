-- +goose Up
CREATE SCHEMA IF NOT EXISTS b46_adapter;

CREATE TABLE b46_adapter.inventory_commit_receipts (
    operation_id uuid PRIMARY KEY,
    request_event_id uuid NOT NULL UNIQUE,
    order_id uuid NOT NULL,
    request_fingerprint char(64) NOT NULL,
    request_payload jsonb NOT NULL,
    result_event_id uuid NOT NULL UNIQUE,
    result_status text NOT NULL CHECK (
        result_status IN ('COMMITTED', 'ITEMS_UNAVAILABLE')
    ),
    result_payload jsonb NOT NULL,
    completed_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT inventory_commit_receipts_fingerprint_format CHECK (
        request_fingerprint ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT inventory_commit_receipts_request_shape CHECK (
        jsonb_typeof(request_payload) = 'object'
        AND request_payload->>'schema_version' = '1.0'
        AND request_payload->>'event_id' = request_event_id::text
        AND request_payload->>'operation_id' = operation_id::text
        AND request_payload->>'order_id' = order_id::text
        AND jsonb_typeof(request_payload->'items') = 'array'
        AND jsonb_array_length(request_payload->'items') BETWEEN 1 AND 100
    ),
    CONSTRAINT inventory_commit_receipts_result_shape CHECK (
        jsonb_typeof(result_payload) = 'object'
        AND result_payload->>'schema_version' = '1.0'
        AND result_payload->>'event_id' = result_event_id::text
        AND result_payload->>'operation_id' = operation_id::text
        AND result_payload->>'order_id' = order_id::text
        AND result_payload->>'result' = result_status
        AND (
            (result_status = 'COMMITTED' AND NOT result_payload ? 'items')
            OR
            (result_status = 'ITEMS_UNAVAILABLE'
                AND jsonb_typeof(result_payload->'items') = 'array'
                AND jsonb_array_length(result_payload->'items') > 0)
        )
    )
);

CREATE INDEX inventory_commit_receipts_order_id_idx
    ON b46_adapter.inventory_commit_receipts (order_id);

-- +goose Down
DROP TABLE IF EXISTS b46_adapter.inventory_commit_receipts;
DROP SCHEMA IF EXISTS b46_adapter;

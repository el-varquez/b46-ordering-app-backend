-- +goose Up
ALTER TABLE users ADD COLUMN password_change_required boolean NOT NULL DEFAULT false;

CREATE INDEX users_cashiers_status_name_idx
    ON users (account_status, name, id) WHERE role = 'CASHIER';

CREATE TABLE cashier_create_requests (
    request_id uuid PRIMARY KEY,
    actor_user_id uuid NOT NULL REFERENCES users(id),
    normalized_email text NOT NULL,
    cashier_user_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE cashier_create_requests;
DROP INDEX users_cashiers_status_name_idx;
ALTER TABLE users DROP COLUMN password_change_required;

-- +goose Up
ALTER TABLE cashier_create_requests
    ADD COLUMN created boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE cashier_create_requests DROP COLUMN created;

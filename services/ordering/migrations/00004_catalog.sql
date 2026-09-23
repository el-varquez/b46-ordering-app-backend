-- +goose Up
CREATE TABLE catalog_sync_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    current_revision bigint NOT NULL DEFAULT 0 CHECK (current_revision >= 0),
    last_completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO catalog_sync_state (singleton) VALUES (true);

CREATE TABLE catalog_products (
    product_id uuid NOT NULL,
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
    price_centavos bigint NOT NULL CHECK (price_centavos >= 0),
    category_id uuid NOT NULL,
    category_name text NOT NULL CHECK (length(btrim(category_name)) BETWEEN 1 AND 200),
    image_url text,
    available boolean NOT NULL,
    source_updated_at timestamptz NOT NULL,
    catalog_revision bigint NOT NULL CHECK (catalog_revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (catalog_revision, product_id)
);

CREATE INDEX catalog_products_product_idx ON catalog_products (product_id, catalog_revision DESC);
CREATE INDEX catalog_products_available_idx ON catalog_products (catalog_revision, available, product_id);

-- +goose Down
DROP TABLE catalog_products;
DROP TABLE catalog_sync_state;

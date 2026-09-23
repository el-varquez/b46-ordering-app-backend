package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Database struct {
	pool          *pgxpool.Pool
	healthTimeout time.Duration
	actorID       uuid.UUID
}

func Open(ctx context.Context, databaseURL string, healthTimeout time.Duration, actorID uuid.UUID) (*Database, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse store database configuration: %w", err)
	}
	config.MaxConns = 10
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create store database pool: %w", err)
	}
	return &Database{pool: pool, healthTimeout: healthTimeout, actorID: actorID}, nil
}

func (database *Database) Pool() *pgxpool.Pool { return database.pool }

func (database *Database) Close() { database.pool.Close() }

func (database *Database) Check(ctx context.Context) error {
	checkContext, cancel := context.WithTimeout(ctx, database.healthTimeout)
	defer cancel()
	if err := database.pool.Ping(checkContext); err != nil {
		return fmt.Errorf("ping store database: %w", err)
	}

	var compatible bool
	err := database.pool.QueryRow(checkContext, `
		WITH required(table_schema, table_name, column_name, data_type, udt_name) AS (
			VALUES
				('b46_adapter', 'inventory_commit_receipts', 'operation_id', 'uuid', 'uuid'),
				('b46_adapter', 'inventory_commit_receipts', 'request_fingerprint', 'character', 'bpchar'),
				('b46_adapter', 'inventory_commit_receipts', 'result_payload', 'jsonb', 'jsonb'),
				('public', 'Items', 'Id', 'uuid', 'uuid'),
				('public', 'Items', 'Name', 'text', 'text'),
				('public', 'Items', 'Description', 'text', 'text'),
				('public', 'Items', 'SellingPrice', 'numeric', 'numeric'),
				('public', 'Items', 'Stock', 'integer', 'int4'),
				('public', 'Items', 'IsActive', 'boolean', 'bool'),
				('public', 'Items', 'TracksStock', 'boolean', 'bool'),
				('public', 'Items', 'IsComposite', 'boolean', 'bool'),
				('public', 'Items', 'CategoryId', 'uuid', 'uuid'),
				('public', 'Items', 'CreatedAt', 'timestamp with time zone', 'timestamptz'),
				('public', 'Items', 'UpdatedAt', 'timestamp with time zone', 'timestamptz'),
				('public', 'Categories', 'Id', 'uuid', 'uuid'),
				('public', 'Categories', 'Name', 'text', 'text'),
				('public', 'CompositeItems', 'ParentItemId', 'uuid', 'uuid'),
				('public', 'CompositeItems', 'ComponentItemId', 'uuid', 'uuid'),
				('public', 'CompositeItems', 'Quantity', 'numeric', 'numeric'),
				('public', 'StockMovements', 'Id', 'uuid', 'uuid'),
				('public', 'StockMovements', 'ItemId', 'uuid', 'uuid'),
				('public', 'StockMovements', 'Type', 'integer', 'int4'),
				('public', 'StockMovements', 'Quantity', 'integer', 'int4'),
				('public', 'StockMovements', 'CreatedBy', 'uuid', 'uuid'),
				('public', 'StockMovements', 'CreatedAt', 'timestamp with time zone', 'timestamptz'),
				('public', 'Users', 'Id', 'uuid', 'uuid')
		), actual AS (
			SELECT table_schema, table_name, column_name, data_type, udt_name
			FROM information_schema.columns
			WHERE table_schema IN ('public', 'b46_adapter')
		)
		SELECT NOT EXISTS (
			SELECT * FROM required
			EXCEPT
			SELECT * FROM actual
		)
	`).Scan(&compatible)
	if err != nil {
		return fmt.Errorf("check store schema: %w", err)
	}
	if !compatible {
		return fmt.Errorf("store schema is incompatible or adapter migration is missing")
	}

	var actorExists bool
	if err := database.pool.QueryRow(checkContext,
		`SELECT EXISTS (SELECT 1 FROM public."Users" WHERE "Id" = $1)`,
		database.actorID.String(),
	).Scan(&actorExists); err != nil {
		return fmt.Errorf("check movement actor: %w", err)
	}
	if !actorExists {
		return fmt.Errorf("configured POS movement actor does not exist")
	}
	return nil
}

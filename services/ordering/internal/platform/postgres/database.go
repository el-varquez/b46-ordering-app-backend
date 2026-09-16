package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const RequiredMigrationVersion int64 = 1

// Database owns the PostgreSQL connection pool used by the process.
type Database struct {
	pool          *pgxpool.Pool
	healthTimeout time.Duration
}

// Open parses the connection string and creates a bounded pool. Connectivity is
// checked separately so callers can report startup failures precisely.
func Open(ctx context.Context, databaseURL string, healthTimeout time.Duration) (*Database, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	poolConfig.MaxConns = 10
	poolConfig.MinConns = 1
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	return &Database{pool: pool, healthTimeout: healthTimeout}, nil
}

func (database *Database) Close() {
	database.pool.Close()
}

// Check verifies both connectivity and schema readiness. A reachable database
// with missing or stale migrations is not ready to serve application traffic.
func (database *Database) Check(ctx context.Context) error {
	checkContext, cancel := context.WithTimeout(ctx, database.healthTimeout)
	defer cancel()

	if err := database.pool.Ping(checkContext); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	var currentVersion int64
	err := database.pool.QueryRow(checkContext, `
		SELECT COALESCE(MAX(version_id) FILTER (WHERE is_applied), 0)
		FROM goose_db_version
	`).Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}
	if currentVersion != RequiredMigrationVersion {
		return fmt.Errorf("database migration version is %d, require %d", currentVersion, RequiredMigrationVersion)
	}
	return nil
}

func (database *Database) Pool() *pgxpool.Pool {
	return database.pool
}

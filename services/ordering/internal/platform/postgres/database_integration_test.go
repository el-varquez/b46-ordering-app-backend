//go:build integration

package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	value := os.Getenv("TEST_DATABASE_URL")
	if value == "" {
		t.Skip("TEST_DATABASE_URL is not set; integration database is intentionally opt-in")
	}
	return value
}

func randomUUID(t *testing.T) string {
	t.Helper()
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("generate UUID: %v", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func TestCheckRequiresConnectivityAndCurrentMigrations(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, testDatabaseURL(t), 2*time.Second)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	if err := database.Check(ctx); err != nil {
		t.Fatalf("Check() after migrations error = %v", err)
	}

	schema := "unmigrated_" + hex.EncodeToString([]byte(randomUUID(t)))[:16]
	if _, err := database.Pool().Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() { _, _ = database.Pool().Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`) }()

	parsed, err := url.Parse(testDatabaseURL(t))
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	unmigrated, err := Open(ctx, parsed.String(), 2*time.Second)
	if err != nil {
		t.Fatalf("Open() unmigrated error = %v", err)
	}
	defer unmigrated.Close()
	if err := unmigrated.Check(ctx); err == nil {
		t.Fatal("Check() accepted a database schema without migrations")
	}
}

func TestFoundationConstraints(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, testDatabaseURL(t))
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var customerID string
	err = tx.QueryRow(ctx, `
		INSERT INTO users (name, normalized_email, role)
		VALUES ('Test Customer', $1, 'CUSTOMER')
		RETURNING id
	`, "customer-"+randomUUID(t)+"@example.test").Scan(&customerID)
	if err != nil {
		t.Fatalf("insert customer: %v", err)
	}

	checkoutID := randomUUID(t)
	var orderID string
	err = tx.QueryRow(ctx, `
		INSERT INTO orders (
			checkout_id, customer_id, subtotal_centavos, delivery_fee_centavos,
			total_centavos, delivery_address
		) VALUES ($1, $2, 1000, 50, 1050, 'Bria Homes')
		RETURNING id
	`, checkoutID, customerID).Scan(&orderID)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}

	expectSQLState(t, ctx, tx, "duplicate checkout_id", "23505", `
		INSERT INTO orders (
			checkout_id, customer_id, subtotal_centavos, delivery_fee_centavos,
			total_centavos, delivery_address
		) VALUES ($1, $2, 1000, 50, 1050, 'Bria Homes')
	`, checkoutID, customerID)

	expectSQLState(t, ctx, tx, "invalid order state", "23514", `
		INSERT INTO orders (
			checkout_id, customer_id, status, subtotal_centavos,
			delivery_fee_centavos, total_centavos, delivery_address
		) VALUES ($1, $2, 'CHECKING_INVENTORY', 1000, 50, 1050, 'Bria Homes')
	`, randomUUID(t), customerID)

	expectSQLState(t, ctx, tx, "broken customer relationship", "23503", `
		INSERT INTO orders (
			checkout_id, customer_id, subtotal_centavos, delivery_fee_centavos,
			total_centavos, delivery_address
		) VALUES ($1, $2, 1000, 50, 1050, 'Bria Homes')
	`, randomUUID(t), randomUUID(t))

	operationID := randomUUID(t)
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventory_operations (operation_id, order_id)
		VALUES ($1, $2)
	`, operationID, orderID); err != nil {
		t.Fatalf("insert inventory operation: %v", err)
	}

	var secondOrderID string
	err = tx.QueryRow(ctx, `
		INSERT INTO orders (
			checkout_id, customer_id, subtotal_centavos, delivery_fee_centavos,
			total_centavos, delivery_address
		) VALUES ($1, $2, 500, 0, 500, 'Bria Homes')
		RETURNING id
	`, randomUUID(t), customerID).Scan(&secondOrderID)
	if err != nil {
		t.Fatalf("insert second order: %v", err)
	}
	expectSQLState(t, ctx, tx, "duplicate operation_id", "23505", `
		INSERT INTO inventory_operations (operation_id, order_id)
		VALUES ($1, $2)
	`, operationID, secondOrderID)
}

func expectSQLState(t *testing.T, ctx context.Context, tx pgx.Tx, name, expectedState, statement string, arguments ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, "SAVEPOINT constraint_check"); err != nil {
		t.Fatalf("%s: create savepoint: %v", name, err)
	}
	_, statementError := tx.Exec(ctx, statement, arguments...)
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT constraint_check"); err != nil {
		t.Fatalf("%s: rollback savepoint: %v", name, err)
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT constraint_check"); err != nil {
		t.Fatalf("%s: release savepoint: %v", name, err)
	}

	var postgresError *pgconn.PgError
	if !errors.As(statementError, &postgresError) || postgresError.Code != expectedState {
		t.Fatalf("%s: error = %v, want PostgreSQL state %s", name, statementError, expectedState)
	}
}

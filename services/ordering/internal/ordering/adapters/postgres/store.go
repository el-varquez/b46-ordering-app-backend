package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/adapters/inventorycontract"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (store *Store) PlaceOrder(ctx context.Context, draft domain.OrderDraft) (domain.PlaceOrderResult, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("begin place order: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	existing, fingerprint, owner, err := findCheckout(ctx, tx, draft.CheckoutID)
	if err == nil {
		if fingerprint != draft.CheckoutFingerprint || owner != draft.CustomerID {
			return domain.PlaceOrderResult{}, domain.ErrCheckoutConflict
		}
		order, loadErr := loadOrder(ctx, tx, existing, draft.CustomerID, false)
		if loadErr != nil {
			return domain.PlaceOrderResult{}, loadErr
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.PlaceOrderResult{}, fmt.Errorf("commit checkout replay: %w", err)
		}
		return domain.PlaceOrderResult{Order: order, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.PlaceOrderResult{}, fmt.Errorf("find checkout: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO orders (
			id, checkout_id, checkout_fingerprint, customer_id, status,
			subtotal_centavos, delivery_fee_centavos, total_centavos,
			delivery_address, delivery_notes, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'SUBMITTED', $5, $6, $7, $8, $9, $10, $10)
	`, draft.ID, draft.CheckoutID, draft.CheckoutFingerprint, draft.CustomerID,
		draft.SubtotalCentavos, draft.DeliveryFeeCentavos, draft.TotalCentavos,
		draft.DeliveryAddress, draft.DeliveryNotes, draft.OccurredAt)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			_ = tx.Rollback(ctx)
			return store.checkoutAfterConflict(ctx, draft)
		}
		return domain.PlaceOrderResult{}, fmt.Errorf("insert order: %w", err)
	}

	items := make([]inventorycontract.ItemRequest, 0, len(draft.Lines))
	for _, line := range draft.Lines {
		_, err = tx.Exec(ctx, `
			INSERT INTO order_lines (
				id, order_id, product_id, product_name, unit_price_centavos,
				quantity, line_total_centavos, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, line.ID, draft.ID, line.ProductID, line.ProductName, line.UnitPriceCentavos,
			line.Quantity, line.LineTotalCentavos, draft.OccurredAt)
		if err != nil {
			return domain.PlaceOrderResult{}, fmt.Errorf("insert order line: %w", err)
		}
		items = append(items, inventorycontract.ItemRequest{
			OrderLineID: line.ID, ProductID: line.ProductID, Quantity: line.Quantity,
		})
	}

	payload, err := json.Marshal(inventorycontract.CommitRequested{
		SchemaVersion: inventorycontract.SchemaVersion,
		EventID:       draft.EventID, OperationID: draft.OperationID, OrderID: draft.ID,
		OccurredAt: draft.OccurredAt.Format(time.RFC3339Nano), Items: items,
	})
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("encode inventory request: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO inventory_operations (operation_id, order_id, result_status, created_at)
		VALUES ($1, $2, 'PENDING', $3)
	`, draft.OperationID, draft.ID, draft.OccurredAt)
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("insert inventory operation: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO ordering_outbox (
			event_id, operation_id, order_id, event_type, schema_version,
			payload, occurred_at, next_attempt_at
		) VALUES ($1, $2, $3, 'InventoryCommitRequested', $4, $5, $6, $6)
	`, draft.EventID, draft.OperationID, draft.ID, inventorycontract.SchemaVersion, payload, draft.OccurredAt)
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("insert ordering outbox: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_records (actor_user_id, action, target_type, target_id, details, occurred_at)
		VALUES ($1, 'ORDER_PLACED', 'ORDER', $2, jsonb_build_object('checkout_id', $3::text), $4)
	`, draft.CustomerID, draft.ID, draft.CheckoutID, draft.OccurredAt)
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("audit order placement: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("commit place order: %w", err)
	}
	return domain.PlaceOrderResult{Order: draft.Order}, nil
}

func (store *Store) checkoutAfterConflict(ctx context.Context, draft domain.OrderDraft) (domain.PlaceOrderResult, error) {
	orderID, fingerprint, owner, err := findCheckout(ctx, store.pool, draft.CheckoutID)
	if err != nil {
		return domain.PlaceOrderResult{}, fmt.Errorf("load concurrent checkout: %w", err)
	}
	if fingerprint != draft.CheckoutFingerprint || owner != draft.CustomerID {
		return domain.PlaceOrderResult{}, domain.ErrCheckoutConflict
	}
	order, err := loadOrder(ctx, store.pool, orderID, draft.CustomerID, false)
	if err != nil {
		return domain.PlaceOrderResult{}, err
	}
	return domain.PlaceOrderResult{Order: order, Replayed: true}, nil
}

func (store *Store) CustomerOrder(ctx context.Context, customerID, orderID string) (domain.Order, error) {
	return loadOrder(ctx, store.pool, orderID, customerID, false)
}

func (store *Store) CustomerOrders(ctx context.Context, customerID string, limit int, afterID string) ([]domain.Order, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT o.id
		FROM orders o
		WHERE o.customer_id = $1
		  AND ($2 = '' OR (o.created_at, o.id) < (
			SELECT anchor.created_at, anchor.id FROM orders anchor
			WHERE anchor.id = $2::uuid AND anchor.customer_id = $1
		  ))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $3
	`, customerID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list customer order IDs: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan customer order ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate customer order IDs: %w", err)
	}
	return store.loadOrders(ctx, ids, customerID, false)
}

func (store *Store) CashierOrder(ctx context.Context, orderID string) (domain.Order, error) {
	return loadOrder(ctx, store.pool, orderID, "", true)
}

func (store *Store) CashierOrders(
	ctx context.Context,
	status domain.FulfillmentStatus,
	limit int,
	afterID string,
) ([]domain.Order, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT o.id
		FROM orders o
		JOIN fulfillments f ON f.order_id = o.id
		WHERE o.status = 'CONFIRMED'
		  AND ($1 = '' OR f.status = $1)
		  AND ($2 = '' OR (o.created_at, o.id) < (
			SELECT anchor.created_at, anchor.id FROM orders anchor WHERE anchor.id = $2::uuid
		  ))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $3
	`, status, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list cashier order IDs: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan cashier order ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cashier order IDs: %w", err)
	}
	return store.loadOrders(ctx, ids, "", true)
}

func (store *Store) loadOrders(ctx context.Context, ids []string, customerID string, cashier bool) ([]domain.Order, error) {
	orders := make([]domain.Order, 0, len(ids))
	for _, id := range ids {
		order, err := loadOrder(ctx, store.pool, id, customerID, cashier)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, nil
}

func (store *Store) MarkCashierRead(ctx context.Context, cashierID, orderID string, now time.Time) (domain.Order, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.Order{}, fmt.Errorf("begin mark cashier read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		UPDATE fulfillments f
		SET cashier_read_at = $3, cashier_read_by = $1
		FROM orders o
		WHERE f.order_id = $2 AND o.id = f.order_id AND o.status = 'CONFIRMED'
		  AND f.cashier_read_at IS NULL
	`, cashierID, orderID, now)
	if err != nil {
		return domain.Order{}, fmt.Errorf("mark cashier order read: %w", err)
	}
	if command.RowsAffected() == 1 {
		_, err = tx.Exec(ctx, `
			INSERT INTO audit_records (actor_user_id, action, target_type, target_id, occurred_at)
			VALUES ($1, 'CASHIER_ORDER_READ', 'ORDER', $2, $3)
		`, cashierID, orderID, now)
		if err != nil {
			return domain.Order{}, fmt.Errorf("audit cashier read: %w", err)
		}
	}
	order, err := loadOrder(ctx, tx, orderID, "", true)
	if err != nil {
		return domain.Order{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Order{}, fmt.Errorf("commit cashier read: %w", err)
	}
	return order, nil
}

func (store *Store) AdvanceFulfillment(
	ctx context.Context,
	cashierID, orderID string,
	target domain.FulfillmentStatus,
	now time.Time,
) (domain.Order, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.Order{}, fmt.Errorf("begin fulfillment transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current domain.FulfillmentStatus
	if err := tx.QueryRow(ctx, `
		SELECT f.status
		FROM fulfillments f JOIN orders o ON o.id = f.order_id
		WHERE f.order_id = $1 AND o.status = 'CONFIRMED'
		FOR UPDATE OF f
	`, orderID).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrNotFound
	} else if err != nil {
		return domain.Order{}, fmt.Errorf("lock fulfillment: %w", err)
	}
	if err := domain.ValidateFulfillmentTarget(current, target); err != nil {
		return domain.Order{}, err
	}
	if current != target {
		var command pgconn.CommandTag
		switch target {
		case domain.FulfillmentDelivering:
			command, err = tx.Exec(ctx, `
				UPDATE fulfillments
				SET status = 'DELIVERING', version = version + 1,
				    delivering_at = $3, updated_by = $1
				WHERE order_id = $2 AND status = 'PREPARING'
			`, cashierID, orderID, now)
		case domain.FulfillmentDelivered:
			command, err = tx.Exec(ctx, `
				UPDATE fulfillments
				SET status = 'DELIVERED', version = version + 1,
				    delivered_at = $3, updated_by = $1
				WHERE order_id = $2 AND status = 'DELIVERING'
			`, cashierID, orderID, now)
		}
		if err != nil {
			return domain.Order{}, fmt.Errorf("advance fulfillment: %w", err)
		}
		if command.RowsAffected() != 1 {
			return domain.Order{}, domain.ErrInvalidTransition
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO audit_records (actor_user_id, action, target_type, target_id, details, occurred_at)
			VALUES ($1, 'FULFILLMENT_ADVANCED', 'ORDER', $2,
			        jsonb_build_object('from', $3::text, 'to', $4::text), $5)
		`, cashierID, orderID, current, target, now)
		if err != nil {
			return domain.Order{}, fmt.Errorf("audit fulfillment transition: %w", err)
		}
	}
	order, err := loadOrder(ctx, tx, orderID, "", true)
	if err != nil {
		return domain.Order{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Order{}, fmt.Errorf("commit fulfillment transition: %w", err)
	}
	return order, nil
}

func (store *Store) ClaimInventoryWork(
	ctx context.Context,
	now time.Time,
	leaseTimeout time.Duration,
	batchSize int,
) ([]domain.ClaimedInventoryWork, error) {
	rows, err := store.pool.Query(ctx, `
		WITH candidates AS (
			SELECT event_id
			FROM ordering_outbox
			WHERE published_at IS NULL
			  AND next_attempt_at <= $1
			  AND (locked_at IS NULL OR locked_at <= $2)
			ORDER BY next_attempt_at, occurred_at
			FOR UPDATE SKIP LOCKED
			LIMIT $3
		)
		UPDATE ordering_outbox outbox
		SET locked_at = $1,
		    claim_token = gen_random_uuid(),
		    attempt_count = outbox.attempt_count + 1,
		    last_error_code = NULL
		FROM candidates
		WHERE outbox.event_id = candidates.event_id
		RETURNING outbox.payload, outbox.claim_token, outbox.attempt_count
	`, now, now.Add(-leaseTimeout), batchSize)
	if err != nil {
		return nil, fmt.Errorf("claim ordering outbox: %w", err)
	}
	defer rows.Close()
	work := make([]domain.ClaimedInventoryWork, 0, batchSize)
	for rows.Next() {
		var payload []byte
		var claimToken string
		var attemptCount int
		if err := rows.Scan(&payload, &claimToken, &attemptCount); err != nil {
			return nil, fmt.Errorf("scan claimed outbox row: %w", err)
		}
		var wire inventorycontract.CommitRequested
		if err := inventorycontract.DecodeStrict(bytes.NewReader(payload), &wire); err != nil {
			return nil, fmt.Errorf("decode claimed inventory request: %w", err)
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, wire.OccurredAt)
		if err != nil {
			return nil, fmt.Errorf("parse inventory request occurrence: %w", err)
		}
		commit := domain.InventoryCommit{
			EventID: wire.EventID, OperationID: wire.OperationID,
			OrderID: wire.OrderID, OccurredAt: occurredAt,
			Items: make([]domain.InventoryItem, 0, len(wire.Items)),
		}
		for _, item := range wire.Items {
			commit.Items = append(commit.Items, domain.InventoryItem{
				OrderLineID: item.OrderLineID, ProductID: item.ProductID, Quantity: item.Quantity,
			})
		}
		work = append(work, domain.ClaimedInventoryWork{
			Commit: commit, ClaimToken: claimToken, AttemptCount: attemptCount,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed outbox rows: %w", err)
	}
	return work, nil
}

func (store *Store) ApplyInventoryResult(
	ctx context.Context,
	claimed domain.ClaimedInventoryWork,
	result domain.InventoryResult,
	now time.Time,
) error {
	if result.EventID == "" || result.OperationID != claimed.Commit.OperationID ||
		result.OrderID != claimed.Commit.OrderID ||
		(result.Status != domain.InventoryCommitted && result.Status != domain.InventoryItemsUnavailable) {
		return domain.ErrOperationConflict
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin inventory result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current domain.InventoryResultStatus
	var currentResultEventID *string
	var currentClaim *string
	var publishedAt *time.Time
	var currentUnavailable []byte
	err = tx.QueryRow(ctx, `
		SELECT io.result_status, io.result_event_id::text,
		       COALESCE(io.unavailable_items, '[]'::jsonb),
		       outbox.claim_token::text, outbox.published_at
		FROM inventory_operations io
		JOIN ordering_outbox outbox ON outbox.operation_id = io.operation_id
		WHERE io.operation_id = $1 AND io.order_id = $2
		FOR UPDATE OF io, outbox
	`, result.OperationID, result.OrderID).Scan(
		&current, &currentResultEventID, &currentUnavailable, &currentClaim, &publishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock inventory operation: %w", err)
	}
	if current != "PENDING" {
		var storedUnavailable []domain.UnavailableItem
		if err := json.Unmarshal(currentUnavailable, &storedUnavailable); err != nil {
			return fmt.Errorf("decode stored unavailable items: %w", err)
		}
		if current == result.Status && currentResultEventID != nil &&
			*currentResultEventID == result.EventID &&
			slices.Equal(storedUnavailable, result.UnavailableItems) {
			return tx.Commit(ctx)
		}
		return domain.ErrOperationConflict
	}
	if publishedAt != nil || currentClaim == nil || *currentClaim != claimed.ClaimToken {
		return domain.ErrClaimLost
	}

	unavailableJSON, err := json.Marshal(result.UnavailableItems)
	if err != nil {
		return fmt.Errorf("encode unavailable items: %w", err)
	}
	var command pgconn.CommandTag
	if result.Status == domain.InventoryCommitted {
		command, err = tx.Exec(ctx, `
			UPDATE inventory_operations
			SET result_status = 'COMMITTED', result_event_id = $2,
			    completed_at = $3
			WHERE operation_id = $1 AND result_status = 'PENDING'
		`, result.OperationID, result.EventID, result.OccurredAt)
		if err == nil && command.RowsAffected() == 1 {
			command, err = tx.Exec(ctx, `
				UPDATE orders SET status = 'CONFIRMED'
				WHERE id = $1 AND status = 'SUBMITTED'
			`, result.OrderID)
		}
		if err == nil && command.RowsAffected() == 1 {
			_, err = tx.Exec(ctx, `
				INSERT INTO fulfillments (order_id, status, preparing_at, updated_by, created_at, updated_at)
				VALUES ($1, 'PREPARING', $2, NULL, $2, $2)
			`, result.OrderID, result.OccurredAt)
		}
	} else {
		command, err = tx.Exec(ctx, `
			UPDATE inventory_operations
			SET result_status = 'ITEMS_UNAVAILABLE', result_event_id = $2,
			    unavailable_items = $3, completed_at = $4
			WHERE operation_id = $1 AND result_status = 'PENDING'
		`, result.OperationID, result.EventID, unavailableJSON, result.OccurredAt)
		if err == nil && command.RowsAffected() == 1 {
			command, err = tx.Exec(ctx, `
				UPDATE orders
				SET status = 'REJECTED', rejection_code = 'ITEMS_UNAVAILABLE'
				WHERE id = $1 AND status = 'SUBMITTED'
			`, result.OrderID)
		}
	}
	if err != nil {
		return fmt.Errorf("apply inventory business result: %w", err)
	}
	if command.RowsAffected() != 1 {
		return domain.ErrOperationConflict
	}
	action := "INVENTORY_COMMITTED"
	if result.Status == domain.InventoryItemsUnavailable {
		action = "ORDER_REJECTED_ITEMS_UNAVAILABLE"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_records (action, target_type, target_id, details, occurred_at)
		VALUES ($1, 'ORDER', $2, jsonb_build_object('operation_id', $3::text), $4)
	`, action, result.OrderID, result.OperationID, now)
	if err != nil {
		return fmt.Errorf("audit inventory result: %w", err)
	}
	command, err = tx.Exec(ctx, `
		UPDATE ordering_outbox
		SET published_at = $3, locked_at = NULL, claim_token = NULL, last_error_code = NULL
		WHERE event_id = $1 AND claim_token = $2 AND published_at IS NULL
	`, claimed.Commit.EventID, claimed.ClaimToken, now)
	if err != nil {
		return fmt.Errorf("publish ordering outbox: %w", err)
	}
	if command.RowsAffected() != 1 {
		return domain.ErrClaimLost
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit inventory result: %w", err)
	}
	return nil
}

func (store *Store) RetryInventoryWork(
	ctx context.Context,
	claimed domain.ClaimedInventoryWork,
	nextAttemptAt time.Time,
	errorCode string,
) error {
	command, err := store.pool.Exec(ctx, `
		UPDATE ordering_outbox
		SET locked_at = NULL, claim_token = NULL,
		    next_attempt_at = $3, last_error_code = $4
		WHERE event_id = $1 AND claim_token = $2 AND published_at IS NULL
	`, claimed.Commit.EventID, claimed.ClaimToken, nextAttemptAt, errorCode)
	if err != nil {
		return fmt.Errorf("release ordering outbox retry: %w", err)
	}
	if command.RowsAffected() != 1 {
		return domain.ErrClaimLost
	}
	return nil
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func findCheckout(ctx context.Context, query rowQuerier, checkoutID string) (string, string, string, error) {
	var orderID, fingerprint, owner string
	err := query.QueryRow(ctx, `
		SELECT id, checkout_fingerprint, customer_id
		FROM orders WHERE checkout_id = $1
	`, checkoutID).Scan(&orderID, &fingerprint, &owner)
	return orderID, fingerprint, owner, err
}

func loadOrder(
	ctx context.Context,
	query rowQuerier,
	orderID, customerID string,
	cashier bool,
) (domain.Order, error) {
	var order domain.Order
	var fulfillmentStatus *string
	var fulfillmentVersion *int
	var fulfillmentUnread *bool
	var preparingAt, deliveringAt, deliveredAt, cashierReadAt *time.Time
	var cashierReadBy, updatedBy *string
	var unavailableJSON []byte
	err := query.QueryRow(ctx, `
		SELECT
			o.id, o.checkout_id, o.customer_id, customer.name, o.status,
			COALESCE(o.rejection_code, ''), o.subtotal_centavos,
			o.delivery_fee_centavos, o.total_centavos,
			o.delivery_address, o.delivery_notes, o.created_at, o.updated_at,
			f.status, f.version,
			CASE WHEN f.order_id IS NULL THEN NULL ELSE f.cashier_read_at IS NULL END,
			f.preparing_at, f.delivering_at, f.delivered_at, f.cashier_read_at,
			f.cashier_read_by::text, f.updated_by::text,
			COALESCE(io.unavailable_items, '[]'::jsonb)
		FROM orders o
		JOIN users customer ON customer.id = o.customer_id
		LEFT JOIN fulfillments f ON f.order_id = o.id
		LEFT JOIN inventory_operations io ON io.order_id = o.id
		WHERE o.id = $1
		  AND ($2 = '' OR o.customer_id = $2::uuid)
		  AND (NOT $3 OR (o.status = 'CONFIRMED' AND f.order_id IS NOT NULL))
	`, orderID, customerID, cashier).Scan(
		&order.ID, &order.CheckoutID, &order.CustomerID, &order.CustomerName, &order.Status,
		&order.RejectionCode, &order.SubtotalCentavos, &order.DeliveryFeeCentavos,
		&order.TotalCentavos, &order.DeliveryAddress, &order.DeliveryNotes,
		&order.CreatedAt, &order.UpdatedAt, &fulfillmentStatus, &fulfillmentVersion,
		&fulfillmentUnread, &preparingAt, &deliveringAt, &deliveredAt, &cashierReadAt,
		&cashierReadBy, &updatedBy, &unavailableJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("load order: %w", err)
	}
	if fulfillmentStatus != nil {
		order.Fulfillment = &domain.Fulfillment{
			Status: domain.FulfillmentStatus(*fulfillmentStatus), Version: *fulfillmentVersion,
			Unread: *fulfillmentUnread, PreparingAt: *preparingAt,
			DeliveringAt: deliveringAt, DeliveredAt: deliveredAt, CashierReadAt: cashierReadAt,
		}
		if cashierReadBy != nil {
			order.Fulfillment.CashierReadBy = *cashierReadBy
		}
		if updatedBy != nil {
			order.Fulfillment.UpdatedBy = *updatedBy
		}
	}
	if err := json.Unmarshal(unavailableJSON, &order.UnavailableItems); err != nil {
		return domain.Order{}, fmt.Errorf("decode unavailable items: %w", err)
	}
	rows, err := query.Query(ctx, `
		SELECT id, product_id, product_name, unit_price_centavos, quantity, line_total_centavos
		FROM order_lines WHERE order_id = $1 ORDER BY product_id, id
	`, orderID)
	if err != nil {
		return domain.Order{}, fmt.Errorf("load order lines: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var line domain.OrderLine
		if err := rows.Scan(
			&line.ID, &line.ProductID, &line.ProductName, &line.UnitPriceCentavos,
			&line.Quantity, &line.LineTotalCentavos,
		); err != nil {
			return domain.Order{}, fmt.Errorf("scan order line: %w", err)
		}
		order.Lines = append(order.Lines, line)
	}
	if err := rows.Err(); err != nil {
		return domain.Order{}, fmt.Errorf("iterate order lines: %w", err)
	}
	return order, nil
}

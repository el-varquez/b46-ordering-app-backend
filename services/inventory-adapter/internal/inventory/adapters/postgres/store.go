package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/inventory-adapter/internal/inventory/ports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	stockMovementTypeSale = 1
	maxSerializableTries  = 3
)

type Store struct {
	pool    *pgxpool.Pool
	ids     ports.IDGenerator
	clock   ports.Clock
	actorID uuid.UUID
}

type itemState struct {
	ID          uuid.UUID
	Stock       int
	Active      bool
	TracksStock bool
	Composite   bool
}

type stockDemand struct {
	Quantity      int
	DependentLine map[uuid.UUID]struct{}
}

type receiptRequest struct {
	SchemaVersion string        `json:"schema_version"`
	EventID       string        `json:"event_id"`
	OperationID   string        `json:"operation_id"`
	OrderID       string        `json:"order_id"`
	OccurredAt    string        `json:"occurred_at"`
	Items         []receiptItem `json:"items"`
}

type receiptItem struct {
	OrderLineID string `json:"order_line_id"`
	ProductID   string `json:"product_id"`
	Quantity    int    `json:"quantity"`
}

type receiptResult struct {
	SchemaVersion string                   `json:"schema_version"`
	EventID       string                   `json:"event_id"`
	OperationID   string                   `json:"operation_id"`
	OrderID       string                   `json:"order_id"`
	Result        domain.ResultStatus      `json:"result"`
	OccurredAt    string                   `json:"occurred_at"`
	Items         []receiptUnavailableItem `json:"items,omitempty"`
}

type receiptUnavailableItem struct {
	OrderLineID       string `json:"order_line_id"`
	ProductID         string `json:"product_id"`
	RequestedQuantity int    `json:"requested_quantity"`
	AvailableQuantity int    `json:"available_quantity"`
}

func New(pool *pgxpool.Pool, ids ports.IDGenerator, clock ports.Clock, actorID uuid.UUID) (*Store, error) {
	if pool == nil || ids == nil || clock == nil || actorID == uuid.Nil {
		return nil, errors.New("postgres inventory store: all dependencies are required")
	}
	return &Store{pool: pool, ids: ids, clock: clock, actorID: actorID}, nil
}

func (store *Store) Commit(ctx context.Context, command domain.CommitCommand, fingerprint string) (domain.CommitResult, error) {
	var lastErr error
	for attempt := 0; attempt < maxSerializableTries; attempt++ {
		result, err := store.commitOnce(ctx, command, fingerprint)
		if err == nil {
			return result, nil
		}
		if !isSerializationFailure(err) {
			return domain.CommitResult{}, classifyStoreError(err)
		}
		lastErr = err
	}
	return domain.CommitResult{}, fmt.Errorf("%w: serializable transaction retries exhausted: %v", domain.ErrStoreUnavailable, lastErr)
}

func (store *Store) commitOnce(ctx context.Context, command domain.CommitCommand, fingerprint string) (result domain.CommitResult, err error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return result, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, command.OperationID.String()); err != nil {
		return result, fmt.Errorf("lock operation: %w", err)
	}
	if replay, found, err := loadReceipt(ctx, tx, command.OperationID, fingerprint); err != nil {
		return result, err
	} else if found {
		return replay, nil
	}

	requestedIDs := make([]uuid.UUID, 0, len(command.Lines))
	for _, line := range command.Lines {
		requestedIDs = append(requestedIDs, line.ProductID)
	}
	initialItems, err := loadItems(ctx, tx, requestedIDs, false)
	if err != nil {
		return result, err
	}

	demands := make(map[uuid.UUID]*stockDemand)
	unavailable := make(map[uuid.UUID]domain.UnavailableItem)
	compositeLines := make(map[uuid.UUID]bool)
	allRelevant := make(map[uuid.UUID]struct{}, len(requestedIDs))
	for _, id := range requestedIDs {
		allRelevant[id] = struct{}{}
	}

	for _, line := range command.Lines {
		item, exists := initialItems[line.ProductID]
		if !exists || !item.Active {
			unavailable[line.OrderLineID] = unavailableFor(line, 0)
			continue
		}
		if !item.TracksStock {
			continue
		}
		if !item.Composite {
			addDemand(demands, line.ProductID, line.OrderLineID, line.Quantity)
			continue
		}

		compositeLines[line.OrderLineID] = true
		components, err := loadCompositeDemand(ctx, tx, line.ProductID, line.Quantity)
		if err != nil {
			return result, err
		}
		if len(components) == 0 {
			unavailable[line.OrderLineID] = unavailableFor(line, 0)
			continue
		}
		for componentID, quantity := range components {
			allRelevant[componentID] = struct{}{}
			addDemand(demands, componentID, line.OrderLineID, quantity)
		}
	}

	lockIDs := make([]uuid.UUID, 0, len(allRelevant))
	for id := range allRelevant {
		lockIDs = append(lockIDs, id)
	}
	lockedItems, err := loadItems(ctx, tx, lockIDs, true)
	if err != nil {
		return result, err
	}

	linesByID := make(map[uuid.UUID]domain.Line, len(command.Lines))
	for _, line := range command.Lines {
		linesByID[line.OrderLineID] = line
		item, exists := lockedItems[line.ProductID]
		if !exists || !item.Active {
			unavailable[line.OrderLineID] = unavailableFor(line, 0)
		}
	}
	for stockItemID, demand := range demands {
		item, exists := lockedItems[stockItemID]
		available := 0
		validPhysicalItem := exists && item.Active && item.TracksStock && !item.Composite
		if validPhysicalItem && item.Stock > 0 {
			available = item.Stock
		}
		if validPhysicalItem && item.Stock >= demand.Quantity {
			continue
		}
		for lineID := range demand.DependentLine {
			line := linesByID[lineID]
			lineAvailable := available
			if compositeLines[lineID] {
				lineAvailable = 0
			}
			unavailable[lineID] = unavailableFor(line, lineAvailable)
		}
	}

	now := store.clock.Now().UTC()
	result = domain.CommitResult{
		SchemaVersion: domain.SchemaVersion,
		EventID:       store.ids.New(),
		OperationID:   command.OperationID,
		OrderID:       command.OrderID,
		OccurredAt:    now,
	}
	if len(unavailable) > 0 {
		result.Status = domain.StatusItemsUnavailable
		result.UnavailableItems = sortedUnavailable(unavailable)
	} else {
		result.Status = domain.StatusCommitted
		stockIDs := sortedDemandIDs(demands)
		for _, stockItemID := range stockIDs {
			demand := demands[stockItemID]
			tag, err := tx.Exec(ctx, `
				UPDATE public."Items"
				SET "Stock" = "Stock" - $2, "UpdatedAt" = $3
				WHERE "Id" = $1 AND "IsActive" = true
				  AND "TracksStock" = true AND "IsComposite" = false
				  AND "Stock" >= $2
			`, stockItemID.String(), demand.Quantity, now)
			if err != nil {
				return domain.CommitResult{}, fmt.Errorf("deduct stock item %s: %w", stockItemID, err)
			}
			if tag.RowsAffected() != 1 {
				return domain.CommitResult{}, fmt.Errorf("stock item %s changed after availability decision", stockItemID)
			}
			notes := fmt.Sprintf("B46 online order %s; operation %s", command.OrderID, command.OperationID)
			_, err = tx.Exec(ctx, `
				INSERT INTO public."StockMovements" (
					"Id", "ItemId", "Type", "Quantity", "Notes",
					"CreatedBy", "CreatedAt", "UpdatedAt"
				) VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
			`, store.ids.New().String(), stockItemID.String(), stockMovementTypeSale,
				-demand.Quantity, notes, store.actorID.String(), now)
			if err != nil {
				return domain.CommitResult{}, fmt.Errorf("insert stock movement for %s: %w", stockItemID, err)
			}
		}
	}

	if err := insertReceipt(ctx, tx, command, fingerprint, result); err != nil {
		return domain.CommitResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.CommitResult{}, fmt.Errorf("commit transaction: %w", err)
	}
	return result, nil
}

func loadReceipt(ctx context.Context, tx pgx.Tx, operationID uuid.UUID, fingerprint string) (domain.CommitResult, bool, error) {
	var storedFingerprint string
	var payload []byte
	err := tx.QueryRow(ctx, `
		SELECT request_fingerprint, result_payload
		FROM b46_adapter.inventory_commit_receipts
		WHERE operation_id = $1
	`, operationID.String()).Scan(&storedFingerprint, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CommitResult{}, false, nil
	}
	if err != nil {
		return domain.CommitResult{}, false, fmt.Errorf("load operation receipt: %w", err)
	}
	if storedFingerprint != fingerprint {
		return domain.CommitResult{}, false, fmt.Errorf("%w: operation %s", domain.ErrOperationConflict, operationID)
	}
	result, err := decodeReceiptResult(payload)
	if err != nil {
		return domain.CommitResult{}, false, fmt.Errorf("decode stored result: %w", err)
	}
	return result, true, nil
}

func loadItems(ctx context.Context, tx pgx.Tx, ids []uuid.UUID, lock bool) (map[uuid.UUID]itemState, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]itemState{}, nil
	}
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, id.String())
	}
	query := `
		SELECT "Id", "Stock", "IsActive", "TracksStock", "IsComposite"
		FROM public."Items"
		WHERE "Id" = ANY(ARRAY(SELECT value::uuid FROM unnest($1::text[]) AS value))
		ORDER BY "Id"
	`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := tx.Query(ctx, query, values)
	if err != nil {
		return nil, fmt.Errorf("load POS items: %w", err)
	}
	defer rows.Close()
	items := make(map[uuid.UUID]itemState, len(ids))
	for rows.Next() {
		var item itemState
		if err := rows.Scan(&item.ID, &item.Stock, &item.Active, &item.TracksStock, &item.Composite); err != nil {
			return nil, fmt.Errorf("scan POS item: %w", err)
		}
		items[item.ID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate POS items: %w", err)
	}
	return items, nil
}

func loadCompositeDemand(ctx context.Context, tx pgx.Tx, parentID uuid.UUID, orderedQuantity int) (map[uuid.UUID]int, error) {
	rows, err := tx.Query(ctx, `
		SELECT "ComponentItemId", CEIL("Quantity" * $2::integer)::integer
		FROM public."CompositeItems"
		WHERE "ParentItemId" = $1
		ORDER BY "ComponentItemId"
		FOR SHARE
	`, parentID.String(), orderedQuantity)
	if err != nil {
		return nil, fmt.Errorf("load composite components: %w", err)
	}
	defer rows.Close()
	components := make(map[uuid.UUID]int)
	for rows.Next() {
		var id uuid.UUID
		var quantity int
		if err := rows.Scan(&id, &quantity); err != nil {
			return nil, fmt.Errorf("scan composite component: %w", err)
		}
		components[id] += quantity
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate composite components: %w", err)
	}
	return components, nil
}

func insertReceipt(ctx context.Context, tx pgx.Tx, command domain.CommitCommand, fingerprint string, result domain.CommitResult) error {
	requestPayload, err := json.Marshal(requestToReceipt(command))
	if err != nil {
		return fmt.Errorf("encode receipt request: %w", err)
	}
	resultPayload, err := json.Marshal(resultToReceipt(result))
	if err != nil {
		return fmt.Errorf("encode receipt result: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO b46_adapter.inventory_commit_receipts (
			operation_id, request_event_id, order_id, request_fingerprint,
			request_payload, result_event_id, result_status, result_payload, completed_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8::jsonb, $9)
	`, command.OperationID.String(), command.EventID.String(), command.OrderID.String(), fingerprint,
		requestPayload, result.EventID.String(), result.Status, resultPayload, result.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert operation receipt: %w", err)
	}
	return nil
}

func requestToReceipt(command domain.CommitCommand) receiptRequest {
	items := make([]receiptItem, 0, len(command.Lines))
	for _, line := range command.Lines {
		items = append(items, receiptItem{OrderLineID: line.OrderLineID.String(), ProductID: line.ProductID.String(), Quantity: line.Quantity})
	}
	return receiptRequest{SchemaVersion: command.SchemaVersion, EventID: command.EventID.String(),
		OperationID: command.OperationID.String(), OrderID: command.OrderID.String(),
		OccurredAt: command.OccurredAt.UTC().Format(time.RFC3339Nano), Items: items}
}

func resultToReceipt(result domain.CommitResult) receiptResult {
	items := make([]receiptUnavailableItem, 0, len(result.UnavailableItems))
	for _, item := range result.UnavailableItems {
		items = append(items, receiptUnavailableItem{OrderLineID: item.OrderLineID.String(), ProductID: item.ProductID.String(),
			RequestedQuantity: item.RequestedQuantity, AvailableQuantity: item.AvailableQuantity})
	}
	return receiptResult{SchemaVersion: result.SchemaVersion, EventID: result.EventID.String(),
		OperationID: result.OperationID.String(), OrderID: result.OrderID.String(), Result: result.Status,
		OccurredAt: result.OccurredAt.UTC().Format(time.RFC3339Nano), Items: items}
}

func decodeReceiptResult(payload []byte) (domain.CommitResult, error) {
	var wire receiptResult
	if err := json.Unmarshal(payload, &wire); err != nil {
		return domain.CommitResult{}, err
	}
	eventID, err := uuid.Parse(wire.EventID)
	if err != nil {
		return domain.CommitResult{}, err
	}
	operationID, err := uuid.Parse(wire.OperationID)
	if err != nil {
		return domain.CommitResult{}, err
	}
	orderID, err := uuid.Parse(wire.OrderID)
	if err != nil {
		return domain.CommitResult{}, err
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, wire.OccurredAt)
	if err != nil {
		return domain.CommitResult{}, err
	}
	result := domain.CommitResult{SchemaVersion: wire.SchemaVersion, EventID: eventID,
		OperationID: operationID, OrderID: orderID, Status: wire.Result, OccurredAt: occurredAt}
	for _, item := range wire.Items {
		lineID, lineErr := uuid.Parse(item.OrderLineID)
		productID, productErr := uuid.Parse(item.ProductID)
		if lineErr != nil || productErr != nil {
			return domain.CommitResult{}, errors.New("stored unavailable item contains invalid UUID")
		}
		result.UnavailableItems = append(result.UnavailableItems, domain.UnavailableItem{OrderLineID: lineID,
			ProductID: productID, RequestedQuantity: item.RequestedQuantity, AvailableQuantity: item.AvailableQuantity})
	}
	if err := result.Validate(); err != nil {
		return domain.CommitResult{}, err
	}
	return result, nil
}

func addDemand(demands map[uuid.UUID]*stockDemand, stockItemID, lineID uuid.UUID, quantity int) {
	value, exists := demands[stockItemID]
	if !exists {
		value = &stockDemand{DependentLine: make(map[uuid.UUID]struct{})}
		demands[stockItemID] = value
	}
	value.Quantity += quantity
	value.DependentLine[lineID] = struct{}{}
}

func unavailableFor(line domain.Line, available int) domain.UnavailableItem {
	if available < 0 {
		available = 0
	}
	return domain.UnavailableItem{OrderLineID: line.OrderLineID, ProductID: line.ProductID,
		RequestedQuantity: line.Quantity, AvailableQuantity: available}
}

func sortedUnavailable(values map[uuid.UUID]domain.UnavailableItem) []domain.UnavailableItem {
	result := make([]domain.UnavailableItem, 0, len(values))
	for _, item := range values {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].OrderLineID.String() < result[j].OrderLineID.String() })
	return result
}

func sortedDemandIDs(demands map[uuid.UUID]*stockDemand) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(demands))
	for id := range demands {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids
}

func isSerializationFailure(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "40001"
}

func classifyStoreError(err error) error {
	if errors.Is(err, domain.ErrOperationConflict) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("%w: receipt identity already used", domain.ErrOperationConflict)
	}
	return fmt.Errorf("%w: %v", domain.ErrStoreUnavailable, err)
}

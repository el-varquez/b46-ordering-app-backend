package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/ports"
)

const defaultInventoryErrorCode = "INVENTORY_TECHNICAL"

type codedError interface {
	Code() string
}

type WorkerConfig struct {
	PollInterval time.Duration
	BatchSize    int
	LeaseTimeout time.Duration
	RetryBase    time.Duration
	RetryMax     time.Duration
}

type Worker struct {
	store     ports.OutboxStore
	inventory ports.InventoryCommitter
	clock     ports.Clock
	config    WorkerConfig
	notifier  ports.Notifier
}

func NewWorker(
	store ports.OutboxStore,
	inventory ports.InventoryCommitter,
	clock ports.Clock,
	config WorkerConfig,
	notifiers ...ports.Notifier,
) *Worker {
	notifier := ports.Notifier(discardNotifier{})
	if len(notifiers) > 0 && notifiers[0] != nil {
		notifier = notifiers[0]
	}
	return &Worker{store: store, inventory: inventory, clock: clock, config: config, notifier: notifier}
}

func (worker *Worker) ProcessOnce(ctx context.Context) (int, error) {
	now := worker.clock.Now().UTC()
	work, err := worker.store.ClaimInventoryWork(ctx, now, worker.config.LeaseTimeout, worker.config.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("claim inventory work: %w", err)
	}
	for _, claimed := range work {
		result, commitErr := worker.inventory.Commit(ctx, claimed.Commit)
		if commitErr != nil {
			next := now.Add(worker.retryDelay(claimed.AttemptCount))
			if err := worker.store.RetryInventoryWork(ctx, claimed, next, safeInventoryErrorCode(commitErr)); err != nil {
				return 0, fmt.Errorf("schedule inventory retry: %w", err)
			}
			continue
		}
		if err := worker.store.ApplyInventoryResult(ctx, claimed, result, now); err != nil {
			return 0, fmt.Errorf("apply inventory result: %w", err)
		}
		if result.Status == domain.InventoryCommitted {
			_ = worker.notifier.Notify(ctx, domain.Notification{
				EventID: result.EventID + ":" + string(domain.NotificationOrderAccepted),
				Kind:    domain.NotificationOrderAccepted, OrderID: result.OrderID,
				Audience: "CASHIER", OccurredAt: now,
			})
		}
	}
	return len(work), nil
}

func safeInventoryErrorCode(err error) string {
	var coded codedError
	if errors.As(err, &coded) {
		switch coded.Code() {
		case "INVENTORY_UNREACHABLE", "INVENTORY_TIMEOUT", "INVENTORY_THROTTLED",
			"INVENTORY_UNAUTHORIZED", "INVENTORY_SERVER_ERROR", "INVENTORY_CONTRACT_REJECTED",
			"INVENTORY_OPERATION_CONFLICT", "INVENTORY_INVALID_RESPONSE":
			return coded.Code()
		}
	}
	return defaultInventoryErrorCode
}

func (worker *Worker) Run(ctx context.Context) error {
	if _, err := worker.ProcessOnce(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(worker.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := worker.ProcessOnce(ctx); err != nil {
				return err
			}
		}
	}
}

func (worker *Worker) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := worker.config.RetryBase
	for current := 1; current < attempt && delay < worker.config.RetryMax; current++ {
		if delay > worker.config.RetryMax/2 {
			return worker.config.RetryMax
		}
		delay *= 2
	}
	if delay > worker.config.RetryMax {
		return worker.config.RetryMax
	}
	return delay
}

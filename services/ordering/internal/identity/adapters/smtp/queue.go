package smtp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/ports"
)

type delivery struct {
	email string
	code  string
}

// Queue keeps SMTP latency and per-recipient failures out of the public
// registration response. Pending registrations can request a replacement code
// if a process stops before delivery or a mail handoff fails.
type Queue struct {
	ctx    context.Context
	sender ports.VerificationMailer
	jobs   chan delivery
}

var _ ports.VerificationMailer = (*Queue)(nil)

func NewQueue(ctx context.Context, sender ports.VerificationMailer, capacity int) (*Queue, error) {
	if ctx == nil || sender == nil || !sender.Configured() || capacity < 1 {
		return nil, errors.New("invalid registration mail queue configuration")
	}
	queue := &Queue{ctx: ctx, sender: sender, jobs: make(chan delivery, capacity)}
	go queue.run()
	return queue, nil
}

func (queue *Queue) Configured() bool { return queue.sender.Configured() }

func (queue *Queue) SendCode(ctx context.Context, email, code string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-queue.ctx.Done():
		return errors.New("registration mail queue stopped")
	case queue.jobs <- delivery{email: email, code: code}:
		return nil
	default:
		return errors.New("registration mail queue full")
	}
}

func (queue *Queue) run() {
	for {
		select {
		case <-queue.ctx.Done():
			return
		case message := <-queue.jobs:
			deliveryContext, cancel := context.WithTimeout(queue.ctx, 12*time.Second)
			err := queue.sender.SendCode(deliveryContext, message.email, message.code)
			cancel()
			if err != nil {
				slog.Error("registration email delivery failed", "operation", "queued_send", "error_type", fmt.Sprintf("%T", err))
			}
		}
	}
}

package smtp

import (
	"context"
	"testing"
	"time"
)

type blockingSender struct {
	started chan struct{}
}

func (*blockingSender) Configured() bool { return true }

func (sender *blockingSender) SendCode(ctx context.Context, _, _ string) error {
	close(sender.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestQueueAcknowledgesWithoutWaitingForSMTPAndBoundsWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sender := &blockingSender{started: make(chan struct{})}
	queue, err := NewQueue(ctx, sender, 1)
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	start := time.Now()
	if err := queue.SendCode(ctx, "customer@example.test", "123456"); err != nil {
		t.Fatalf("enqueue first message: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("registration mail enqueue waited for SMTP")
	}
	select {
	case <-sender.started:
	case <-time.After(time.Second):
		t.Fatal("queued message was not handed to sender")
	}
	if err := queue.SendCode(ctx, "other@example.test", "234567"); err != nil {
		t.Fatalf("enqueue second message: %v", err)
	}
	if err := queue.SendCode(ctx, "third@example.test", "345678"); err == nil {
		t.Fatal("full queue accepted another message")
	}
}

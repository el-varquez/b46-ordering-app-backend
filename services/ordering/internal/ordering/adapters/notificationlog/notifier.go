package notificationlog

import (
	"context"
	"log/slog"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/ordering/domain"
)

type Notifier struct{ logger *slog.Logger }

func New(logger *slog.Logger) *Notifier { return &Notifier{logger: logger} }

func (notifier *Notifier) Notify(ctx context.Context, event domain.Notification) error {
	notifier.logger.InfoContext(ctx, "development order notification",
		"event_id", event.EventID, "kind", event.Kind, "order_id", event.OrderID,
		"audience", event.Audience, "recipient_user_id", event.RecipientUserID,
	)
	return nil
}

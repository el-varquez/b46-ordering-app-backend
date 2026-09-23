package smtp

import (
	"context"
	"errors"
)

type DisabledSender struct{}

func (DisabledSender) Configured() bool { return false }

func (DisabledSender) SendCode(context.Context, string, string) error {
	return errors.New("SMTP is not configured")
}

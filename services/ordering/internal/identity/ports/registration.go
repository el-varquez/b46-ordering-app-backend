package ports

import (
	"context"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type RegistrationStore interface {
	BeginRegistration(context.Context, domain.PendingRegistration, string, time.Time) (bool, error)
	ResendRegistration(context.Context, string, string, string, time.Time, time.Time) (string, bool, error)
	VerifyRegistration(context.Context, string, string, time.Time) (domain.User, error)
	DeleteExpiredRegistrations(context.Context, time.Time) error
}

type VerificationMailer interface {
	Configured() bool
	SendCode(context.Context, string, string) error
}

type VerificationCodeGenerator interface {
	NewCode() (string, error)
}

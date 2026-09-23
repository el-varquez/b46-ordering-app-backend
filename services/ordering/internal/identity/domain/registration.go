package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidRegistration         = errors.New("invalid or expired registration")
	ErrRegistrationRateLimited     = errors.New("registration rate limited")
	ErrRegistrationCooldown        = errors.New("registration resend cooldown")
	ErrEmailUnavailable            = errors.New("verification email unavailable")
	ErrInvalidRegistrationName     = fmt.Errorf("%w: registration name", ErrInvalidInput)
	ErrInvalidRegistrationEmail    = fmt.Errorf("%w: registration email", ErrInvalidInput)
	ErrInvalidRegistrationPassword = fmt.Errorf("%w: registration password", ErrInvalidInput)
)

type PendingRegistration struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string
	CodeHash     string
	EmailRateKey string
	Eligible     bool
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

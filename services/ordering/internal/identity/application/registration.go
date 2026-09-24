package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/ports"
)

const registrationLifetime = 10 * time.Minute

type RegistrationService struct {
	identity *Service
	store    ports.RegistrationStore
	mailer   ports.VerificationMailer
	codes    ports.VerificationCodeGenerator
	key      []byte
}

type RegistrationStart struct {
	ID        string    `json:"registration_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func NewRegistrationService(
	identity *Service,
	store ports.RegistrationStore,
	mailer ports.VerificationMailer,
	codes ports.VerificationCodeGenerator,
	key []byte,
) (*RegistrationService, error) {
	if identity == nil || store == nil || mailer == nil || codes == nil || len(key) < 32 {
		return nil, errors.New("invalid registration service configuration")
	}
	return &RegistrationService{identity: identity, store: store, mailer: mailer, codes: codes, key: append([]byte(nil), key...)}, nil
}

func (service *RegistrationService) Begin(ctx context.Context, name, email, password, clientIP string) (RegistrationStart, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 120 {
		return RegistrationStart{}, domain.ErrInvalidRegistrationName
	}
	normalizedEmail, err := domain.NormalizeEmail(email)
	if err != nil {
		return RegistrationStart{}, domain.ErrInvalidRegistrationEmail
	}
	if domain.ValidatePassword(password) != nil {
		return RegistrationStart{}, domain.ErrInvalidRegistrationPassword
	}
	if !service.mailer.Configured() {
		return RegistrationStart{}, domain.ErrEmailUnavailable
	}
	passwordHash, err := service.identity.hasher.Hash(password)
	if err != nil {
		return RegistrationStart{}, fmt.Errorf("hash registration password: %w", err)
	}
	id, err := service.identity.tokens.New()
	if err != nil {
		return RegistrationStart{}, fmt.Errorf("generate registration ID: %w", err)
	}
	code, err := service.codes.NewCode()
	if err != nil {
		return RegistrationStart{}, fmt.Errorf("generate registration code: %w", err)
	}
	now := service.identity.clock.Now()
	value := domain.PendingRegistration{
		ID: id, Email: normalizedEmail, Name: name, PasswordHash: passwordHash,
		CodeHash:     service.digest(id + ":" + code),
		EmailRateKey: "email:" + service.digest(normalizedEmail),
		Eligible:     true, CreatedAt: now, ExpiresAt: now.Add(registrationLifetime),
	}
	eligible, err := service.store.BeginRegistration(ctx, value, "ip:"+service.digest(clientIP), now)
	if err != nil {
		return RegistrationStart{}, err
	}
	if eligible {
		if err := service.mailer.SendCode(ctx, normalizedEmail, code); err != nil {
			slog.Error("registration email delivery failed", "operation", "begin", "error_type", fmt.Sprintf("%T", err))
		}
	}
	return RegistrationStart{ID: id, ExpiresAt: value.ExpiresAt}, nil
}

func (service *RegistrationService) Resend(ctx context.Context, id, clientIP string) (RegistrationStart, error) {
	if !service.mailer.Configured() {
		return RegistrationStart{}, domain.ErrEmailUnavailable
	}
	if strings.TrimSpace(id) == "" {
		return RegistrationStart{}, domain.ErrInvalidRegistration
	}
	code, err := service.codes.NewCode()
	if err != nil {
		return RegistrationStart{}, fmt.Errorf("generate registration code: %w", err)
	}
	now := service.identity.clock.Now()
	expires := now.Add(registrationLifetime)
	email, eligible, err := service.store.ResendRegistration(ctx, id, service.digest(id+":"+code), "ip:"+service.digest(clientIP), now, expires)
	if err != nil {
		return RegistrationStart{}, err
	}
	if eligible {
		if err := service.mailer.SendCode(ctx, email, code); err != nil {
			slog.Error("registration email delivery failed", "operation", "resend", "error_type", fmt.Sprintf("%T", err))
		}
	}
	return RegistrationStart{ID: id, ExpiresAt: expires}, nil
}

func (service *RegistrationService) Verify(ctx context.Context, id, code string) (domain.SessionTokens, error) {
	if !validRegistrationCode(id, code) {
		return domain.SessionTokens{}, domain.ErrInvalidRegistration
	}
	user, err := service.store.VerifyRegistration(ctx, id, service.digest(id+":"+code), service.identity.clock.Now())
	if err != nil {
		return domain.SessionTokens{}, err
	}
	plain, stored, err := service.identity.makeSession(user.ID)
	if err != nil {
		return domain.SessionTokens{}, err
	}
	created, err := service.identity.store.CreateSession(ctx, stored)
	if err != nil {
		return domain.SessionTokens{}, fmt.Errorf("create registration session: %w", err)
	}
	plain.User = created
	return plain, nil
}

// BeginCashier sends the challenge to the cashier's own mailbox. The actor is
// bound to the pending request so a different admin cannot consume the code.
func (service *RegistrationService) BeginCashier(
	ctx context.Context, actor domain.Principal, name, email, temporaryPassword, clientIP string,
) (RegistrationStart, error) {
	if err := Authorize(actor, domain.RoleAdmin); err != nil {
		return RegistrationStart{}, err
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 120 {
		return RegistrationStart{}, domain.ErrInvalidRegistrationName
	}
	normalized, err := domain.NormalizeEmail(email)
	if err != nil {
		return RegistrationStart{}, domain.ErrInvalidRegistrationEmail
	}
	if domain.ValidatePassword(temporaryPassword) != nil {
		return RegistrationStart{}, domain.ErrInvalidRegistrationPassword
	}
	if !service.mailer.Configured() {
		return RegistrationStart{}, domain.ErrEmailUnavailable
	}
	hash, err := service.identity.hasher.Hash(temporaryPassword)
	if err != nil {
		return RegistrationStart{}, fmt.Errorf("hash cashier password: %w", err)
	}
	id, err := service.identity.tokens.New()
	if err != nil {
		return RegistrationStart{}, fmt.Errorf("generate cashier registration ID: %w", err)
	}
	code, err := service.codes.NewCode()
	if err != nil {
		return RegistrationStart{}, fmt.Errorf("generate cashier registration code: %w", err)
	}
	now := service.identity.clock.Now()
	value := domain.PendingRegistration{
		ID: id, Email: normalized, Name: name, PasswordHash: hash,
		CodeHash:     service.digest(id + ":" + code),
		EmailRateKey: "email:" + service.digest(normalized),
		CreatedAt:    now, ExpiresAt: now.Add(registrationLifetime),
	}
	if err := service.store.BeginCashierRegistration(ctx, value, actor.UserID, "ip:"+service.digest(clientIP), now); err != nil {
		return RegistrationStart{}, err
	}
	if err := service.mailer.SendCode(ctx, normalized, code); err != nil {
		slog.Error("cashier verification email delivery failed", "operation", "begin", "error_type", fmt.Sprintf("%T", err))
	}
	return RegistrationStart{ID: id, ExpiresAt: value.ExpiresAt}, nil
}

func (service *RegistrationService) ResendCashier(
	ctx context.Context, actor domain.Principal, id, clientIP string,
) (RegistrationStart, error) {
	if err := Authorize(actor, domain.RoleAdmin); err != nil {
		return RegistrationStart{}, err
	}
	if !service.mailer.Configured() {
		return RegistrationStart{}, domain.ErrEmailUnavailable
	}
	if strings.TrimSpace(id) == "" {
		return RegistrationStart{}, domain.ErrInvalidRegistration
	}
	code, err := service.codes.NewCode()
	if err != nil {
		return RegistrationStart{}, fmt.Errorf("generate cashier registration code: %w", err)
	}
	now := service.identity.clock.Now()
	expires := now.Add(registrationLifetime)
	email, err := service.store.ResendCashierRegistration(ctx, actor.UserID, id,
		service.digest(id+":"+code), "ip:"+service.digest(clientIP), now, expires)
	if err != nil {
		return RegistrationStart{}, err
	}
	if err := service.mailer.SendCode(ctx, email, code); err != nil {
		slog.Error("cashier verification email delivery failed", "operation", "resend", "error_type", fmt.Sprintf("%T", err))
	}
	return RegistrationStart{ID: id, ExpiresAt: expires}, nil
}

func (service *RegistrationService) VerifyCashier(
	ctx context.Context, actor domain.Principal, id, code string,
) (domain.UserView, error) {
	if err := Authorize(actor, domain.RoleAdmin); err != nil {
		return domain.UserView{}, err
	}
	if !validRegistrationCode(id, code) {
		return domain.UserView{}, domain.ErrInvalidRegistration
	}
	return service.store.VerifyCashierRegistration(ctx, actor.UserID, id,
		service.digest(id+":"+code), service.identity.clock.Now())
}

func validRegistrationCode(id, code string) bool {
	if strings.TrimSpace(id) == "" || len(code) != 6 {
		return false
	}
	for _, char := range code {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func (service *RegistrationService) Cleanup(ctx context.Context) error {
	return service.store.DeleteExpiredRegistrations(ctx, service.identity.clock.Now())
}

func (service *RegistrationService) digest(value string) string {
	mac := hmac.New(sha256.New, service.key)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/ports"
)

type Config struct {
	AccessTokenLifetime  time.Duration
	RefreshTokenLifetime time.Duration
	OAuthIntentLifetime  time.Duration
}

type Service struct {
	store     ports.Store
	hasher    ports.PasswordHasher
	tokens    ports.TokenGenerator
	clock     ports.Clock
	oauth     ports.OAuthVerifier
	config    Config
	dummyHash string
}

func New(
	store ports.Store,
	hasher ports.PasswordHasher,
	tokens ports.TokenGenerator,
	clock ports.Clock,
	oauth ports.OAuthVerifier,
	config Config,
) (*Service, error) {
	if config.AccessTokenLifetime <= 0 ||
		config.RefreshTokenLifetime <= config.AccessTokenLifetime ||
		config.OAuthIntentLifetime <= 0 {
		return nil, errors.New("invalid identity application configuration")
	}
	dummyHash, err := hasher.Hash("b46 timing equalizer password")
	if err != nil {
		return nil, fmt.Errorf("create login timing hash: %w", err)
	}
	return &Service{
		store: store, hasher: hasher, tokens: tokens, clock: clock,
		oauth: oauth, config: config, dummyHash: dummyHash,
	}, nil
}

func (service *Service) PasswordLogin(ctx context.Context, email, password string) (domain.SessionTokens, error) {
	normalizedEmail, emailError := domain.NormalizeEmail(email)
	if emailError != nil || domain.ValidatePassword(password) != nil {
		_, _, _ = service.hasher.Verify(password, service.dummyHash)
		return domain.SessionTokens{}, domain.ErrInvalidCredentials
	}

	record, err := service.store.FindPassword(ctx, normalizedEmail)
	if errors.Is(err, domain.ErrInvalidCredentials) {
		_, _, _ = service.hasher.Verify(password, service.dummyHash)
		return domain.SessionTokens{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return domain.SessionTokens{}, fmt.Errorf("find password identity: %w", err)
	}

	matches, needsRehash, err := service.hasher.Verify(password, record.PasswordHash)
	if err != nil {
		return domain.SessionTokens{}, fmt.Errorf("verify password: %w", err)
	}
	if !matches {
		return domain.SessionTokens{}, domain.ErrInvalidCredentials
	}
	if record.User.Status == domain.AccountDisabled {
		return domain.SessionTokens{}, domain.ErrAccountDisabled
	}
	if needsRehash {
		replacement, hashErr := service.hasher.Hash(password)
		if hashErr != nil {
			return domain.SessionTokens{}, fmt.Errorf("upgrade password hash: %w", hashErr)
		}
		if err := service.store.UpdatePasswordHash(ctx, record.User.ID, replacement); err != nil {
			return domain.SessionTokens{}, fmt.Errorf("store upgraded password hash: %w", err)
		}
	}

	plain, stored, err := service.makeSession(record.User.ID)
	if err != nil {
		return domain.SessionTokens{}, err
	}
	user, err := service.store.CreateSession(ctx, stored)
	if err != nil {
		return domain.SessionTokens{}, fmt.Errorf("create login session: %w", err)
	}
	plain.User = user
	return plain, nil
}

func (service *Service) Refresh(ctx context.Context, refreshToken string) (domain.SessionTokens, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return domain.SessionTokens{}, domain.ErrUnauthenticated
	}
	plain, replacement, err := service.makeSession("")
	if err != nil {
		return domain.SessionTokens{}, err
	}
	user, err := service.store.RotateRefresh(
		ctx, hashToken(refreshToken), replacement, service.clock.Now(),
	)
	if errors.Is(err, domain.ErrRefreshReuse) || errors.Is(err, domain.ErrUnauthenticated) || errors.Is(err, domain.ErrAccountDisabled) {
		return domain.SessionTokens{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.SessionTokens{}, fmt.Errorf("rotate refresh session: %w", err)
	}
	plain.User = user
	return plain, nil
}

func (service *Service) Authenticate(ctx context.Context, accessToken string) (domain.Principal, error) {
	if strings.TrimSpace(accessToken) == "" {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	principal, err := service.store.AuthenticateAccess(ctx, hashToken(accessToken), service.clock.Now())
	if errors.Is(err, domain.ErrUnauthenticated) || errors.Is(err, domain.ErrAccountDisabled) {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.Principal{}, fmt.Errorf("authenticate access token: %w", err)
	}
	return principal, nil
}

func (service *Service) Logout(ctx context.Context, accessToken string) error {
	principal, err := service.Authenticate(ctx, accessToken)
	if errors.Is(err, domain.ErrUnauthenticated) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := service.store.RevokeFamily(ctx, principal.FamilyID, "LOGOUT", service.clock.Now()); err != nil {
		return fmt.Errorf("revoke logout family: %w", err)
	}
	return nil
}

type OAuthStart struct {
	IntentID  string          `json:"intent_id"`
	Provider  domain.Provider `json:"provider"`
	Nonce     string          `json:"nonce"`
	ExpiresAt time.Time       `json:"expires_at"`
}

func (service *Service) BeginOAuthLogin(ctx context.Context, provider domain.Provider) (OAuthStart, error) {
	return service.beginOAuth(ctx, provider, domain.OAuthLogin, "")
}

func (service *Service) BeginOAuthLink(ctx context.Context, principal domain.Principal, provider domain.Provider) (OAuthStart, error) {
	return service.beginOAuth(ctx, provider, domain.OAuthLink, principal.UserID)
}

func (service *Service) beginOAuth(
	ctx context.Context,
	provider domain.Provider,
	purpose domain.OAuthPurpose,
	boundUserID string,
) (OAuthStart, error) {
	if !provider.OAuth() {
		return OAuthStart{}, domain.ErrInvalidInput
	}
	nonce, err := service.tokens.New()
	if err != nil {
		return OAuthStart{}, fmt.Errorf("generate oauth nonce: %w", err)
	}
	intent, err := service.store.CreateOAuthIntent(ctx, domain.OAuthIntent{
		Provider: provider, Purpose: purpose, BoundUserID: boundUserID,
		NonceHash: hashToken(nonce),
		ExpiresAt: service.clock.Now().Add(service.config.OAuthIntentLifetime),
	})
	if err != nil {
		return OAuthStart{}, fmt.Errorf("create oauth intent: %w", err)
	}
	return OAuthStart{IntentID: intent.ID, Provider: provider, Nonce: nonce, ExpiresAt: intent.ExpiresAt}, nil
}

func (service *Service) OAuthLogin(ctx context.Context, intentID, providerToken string) (domain.SessionTokens, error) {
	intent, err := service.store.FindOAuthIntent(ctx, intentID, service.clock.Now())
	if err != nil || intent.Purpose != domain.OAuthLogin {
		return domain.SessionTokens{}, domain.ErrInvalidOAuthIntent
	}
	verified, err := service.oauth.Verify(ctx, intent.Provider, providerToken, intent.NonceHash)
	if err != nil {
		return domain.SessionTokens{}, domain.ErrInvalidOAuthToken
	}
	verified, err = normalizeVerifiedIdentity(verified)
	if err != nil || !verified.EmailVerified {
		return domain.SessionTokens{}, domain.ErrInvalidOAuthToken
	}
	plain, stored, err := service.makeSession("")
	if err != nil {
		return domain.SessionTokens{}, err
	}
	user, err := service.store.CompleteOAuthLogin(ctx, intent.ID, verified, stored, service.clock.Now())
	if err != nil {
		return domain.SessionTokens{}, err
	}
	plain.User = user
	return plain, nil
}

func (service *Service) LinkOAuth(
	ctx context.Context,
	principal domain.Principal,
	intentID, providerToken string,
) error {
	intent, err := service.store.FindOAuthIntent(ctx, intentID, service.clock.Now())
	if err != nil || intent.Purpose != domain.OAuthLink || intent.BoundUserID != principal.UserID {
		return domain.ErrInvalidOAuthIntent
	}
	verified, err := service.oauth.Verify(ctx, intent.Provider, providerToken, intent.NonceHash)
	if err != nil {
		return domain.ErrInvalidOAuthToken
	}
	verified, err = normalizeVerifiedIdentity(verified)
	if err != nil {
		return domain.ErrInvalidOAuthToken
	}
	return service.store.CompleteOAuthLink(ctx, intent.ID, principal.UserID, verified, service.clock.Now())
}

func (service *Service) Me(ctx context.Context, principal domain.Principal) (domain.UserView, error) {
	return service.store.UserView(ctx, principal.UserID)
}

func (service *Service) BootstrapAdmin(ctx context.Context, name, email, password string) (domain.User, error) {
	normalized, err := domain.NormalizeEmail(email)
	if err != nil || domain.ValidatePassword(password) != nil {
		return domain.User{}, domain.ErrInvalidInput
	}
	hash, err := service.hasher.Hash(password)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash bootstrap password: %w", err)
	}
	return service.store.BootstrapAdmin(
		ctx, domain.DisplayName(name, normalized), normalized, hash, service.clock.Now(),
	)
}

func (service *Service) DisableUser(ctx context.Context, actor domain.Principal, userID string) error {
	if actor.Role != domain.RoleAdmin {
		return domain.ErrForbidden
	}
	return service.store.DisableUser(ctx, actor.UserID, userID, service.clock.Now())
}

func Authorize(principal domain.Principal, required domain.Role) error {
	if principal.Status != domain.AccountActive {
		return domain.ErrUnauthenticated
	}
	if principal.Role != required {
		return domain.ErrForbidden
	}
	return nil
}

func (service *Service) makeSession(userID string) (domain.SessionTokens, domain.NewSession, error) {
	access, err := service.tokens.New()
	if err != nil {
		return domain.SessionTokens{}, domain.NewSession{}, fmt.Errorf("generate access token: %w", err)
	}
	refresh, err := service.tokens.New()
	if err != nil {
		return domain.SessionTokens{}, domain.NewSession{}, fmt.Errorf("generate refresh token: %w", err)
	}
	now := service.clock.Now()
	accessExpiry := now.Add(service.config.AccessTokenLifetime)
	refreshExpiry := now.Add(service.config.RefreshTokenLifetime)
	return domain.SessionTokens{
		AccessToken: access, AccessTokenExpiresAt: accessExpiry,
		RefreshToken: refresh, RefreshTokenExpiresAt: refreshExpiry,
	}, domain.NewSession{
		UserID: userID, AccessTokenHash: hashToken(access),
		RefreshTokenHash: hashToken(refresh), AccessExpiresAt: accessExpiry,
		RefreshExpiresAt: refreshExpiry,
	}, nil
}

func normalizeVerifiedIdentity(value domain.VerifiedIdentity) (domain.VerifiedIdentity, error) {
	if !value.Provider.OAuth() || strings.TrimSpace(value.Subject) == "" {
		return domain.VerifiedIdentity{}, domain.ErrInvalidOAuthToken
	}
	normalized, err := domain.NormalizeEmail(value.Email)
	if err != nil {
		return domain.VerifiedIdentity{}, domain.ErrInvalidOAuthToken
	}
	value.Email = normalized
	value.SuggestedName = domain.DisplayName(value.SuggestedName, normalized)
	return value, nil
}

func hashToken(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

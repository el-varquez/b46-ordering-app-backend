package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type Role string

const (
	RoleCustomer Role = "CUSTOMER"
	RoleCashier  Role = "CASHIER"
	RoleAdmin    Role = "ADMIN"
)

func (role Role) Valid() bool {
	return role == RoleCustomer || role == RoleCashier || role == RoleAdmin
}

type AccountStatus string

const (
	AccountActive   AccountStatus = "ACTIVE"
	AccountDisabled AccountStatus = "DISABLED"
)

type Provider string

const (
	ProviderPassword Provider = "PASSWORD"
	ProviderGoogle   Provider = "GOOGLE"
	ProviderApple    Provider = "APPLE"
)

func (provider Provider) OAuth() bool {
	return provider == ProviderGoogle || provider == ProviderApple
}

type OAuthPurpose string

const (
	OAuthLogin OAuthPurpose = "LOGIN"
	OAuthLink  OAuthPurpose = "LINK"
)

var (
	ErrInvalidInput         = errors.New("invalid identity input")
	ErrInvalidCredentials   = errors.New("invalid credentials")
	ErrUnauthenticated      = errors.New("unauthenticated")
	ErrForbidden            = errors.New("forbidden")
	ErrAccountDisabled      = errors.New("account disabled")
	ErrRefreshReuse         = errors.New("refresh token reuse detected")
	ErrInvalidOAuthIntent   = errors.New("invalid oauth intent")
	ErrInvalidOAuthToken    = errors.New("invalid oauth credential")
	ErrIdentityLinkRequired = errors.New("identity link required")
	ErrIdentityLinked       = errors.New("identity already linked")
	ErrAdminAlreadyExists   = errors.New("admin already exists")
)

type User struct {
	ID              string
	Name            string
	NormalizedEmail string
	Role            Role
	Status          AccountStatus
}

type Principal struct {
	UserID   string
	Role     Role
	Status   AccountStatus
	FamilyID string
}

type PasswordRecord struct {
	User         User
	PasswordHash string
}

type NewSession struct {
	UserID           string
	FamilyID         string
	ParentSessionID  string
	AccessTokenHash  string
	RefreshTokenHash string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

type SessionTokens struct {
	AccessToken           string
	AccessTokenExpiresAt  time.Time
	RefreshToken          string
	RefreshTokenExpiresAt time.Time
	User                  User
}

type OAuthIntent struct {
	ID          string
	Provider    Provider
	Purpose     OAuthPurpose
	BoundUserID string
	NonceHash   string
	ExpiresAt   time.Time
}

type VerifiedIdentity struct {
	Provider      Provider
	Subject       string
	Email         string
	EmailVerified bool
	SuggestedName string
}

type UserView struct {
	User      User
	Providers []Provider
}

func NormalizeEmail(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	parts := strings.Split(normalized, "@")
	if len(parts) != 2 || parts[0] == "" || !strings.Contains(parts[1], ".") {
		return "", ErrInvalidInput
	}
	if utf8.RuneCountInString(normalized) > 254 {
		return "", ErrInvalidInput
	}
	return normalized, nil
}

func ValidatePassword(value string) error {
	length := utf8.RuneCountInString(value)
	if length < 12 || length > 128 {
		return ErrInvalidInput
	}
	return nil
}

func DisplayName(value, normalizedEmail string) string {
	name := strings.TrimSpace(value)
	if name != "" {
		if utf8.RuneCountInString(name) > 120 {
			return string([]rune(name)[:120])
		}
		return name
	}
	return strings.Split(normalizedEmail, "@")[0]
}

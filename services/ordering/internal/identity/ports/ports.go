package ports

import (
	"context"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type Store interface {
	FindPassword(context.Context, string) (domain.PasswordRecord, error)
	UpdatePasswordHash(context.Context, string, string) error
	CreateSession(context.Context, domain.NewSession) (domain.User, error)
	AuthenticateAccess(context.Context, string, time.Time) (domain.Principal, error)
	RotateRefresh(context.Context, string, domain.NewSession, time.Time) (domain.User, error)
	RevokeFamily(context.Context, string, string, time.Time) error

	CreateOAuthIntent(context.Context, domain.OAuthIntent) (domain.OAuthIntent, error)
	FindOAuthIntent(context.Context, string, time.Time) (domain.OAuthIntent, error)
	CompleteOAuthLogin(context.Context, string, domain.VerifiedIdentity, domain.NewSession, time.Time) (domain.User, error)
	CompleteOAuthLink(context.Context, string, string, domain.VerifiedIdentity, time.Time) error

	UserView(context.Context, string) (domain.UserView, error)
	BootstrapAdmin(context.Context, string, string, string, time.Time) (domain.User, error)
	RecoverAdminPassword(context.Context, string, string, time.Time) (domain.User, error)
	DisableUser(context.Context, string, string, time.Time) error
	ListCashiers(context.Context, domain.AccountStatus) ([]domain.UserView, error)
	CashierView(context.Context, string) (domain.UserView, error)
	SetCashierStatus(context.Context, string, string, domain.AccountStatus, time.Time) (domain.UserView, error)
	ResetCashierPassword(context.Context, string, string, string, time.Time) (domain.UserView, error)
	ChangeOwnPassword(context.Context, string, string, string, string, time.Time) error
}

type PasswordHasher interface {
	Hash(string) (string, error)
	Verify(string, string) (matches bool, needsRehash bool, err error)
}

type TokenGenerator interface {
	New() (string, error)
}

type Clock interface {
	Now() time.Time
}

type OAuthVerifier interface {
	Verify(context.Context, domain.Provider, string, string) (domain.VerifiedIdentity, error)
}

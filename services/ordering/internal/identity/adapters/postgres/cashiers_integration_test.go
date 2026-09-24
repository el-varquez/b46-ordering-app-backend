//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	identitypostgres "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/postgres"
	identitysecurity "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/security"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

func TestAdminCashierLifecycle(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	service := identityService(t, pool, clock, hasher, staticOAuthVerifier{})
	admin := seedPasswordUser(t, pool, hasher, domain.RoleAdmin, domain.AccountActive)
	defer cleanupUser(t, pool, admin.ID)
	customer := seedPasswordUser(t, pool, hasher, domain.RoleCustomer, domain.AccountActive)
	defer cleanupUser(t, pool, customer.ID)
	adminActor := domain.Principal{UserID: admin.ID, Role: domain.RoleAdmin, Status: domain.AccountActive}
	customerActor := domain.Principal{UserID: customer.ID, Role: domain.RoleCustomer, Status: domain.AccountActive}
	email := "cashier-" + uuid.NewString() + "@example.test"
	temporary := "temporary-cashier-password"
	mailer := &registrationMailer{}
	registration, err := application.NewRegistrationService(service, identitypostgres.New(pool), mailer,
		&registrationCodes{values: []string{"123456", "654321", "111111"}}, []byte("integration-only-registration-code-key-32-bytes"))
	if err != nil {
		t.Fatalf("create cashier registration service: %v", err)
	}

	if _, err := registration.BeginCashier(ctx, customerActor, "Cashier", email, temporary, "192.0.2.1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Customer create = %v, want forbidden", err)
	}
	if _, err := service.ListCashiers(ctx, customerActor, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Customer list = %v, want forbidden", err)
	}
	if _, err := registration.BeginCashier(ctx, adminActor, "Wrong", customer.NormalizedEmail, temporary, "192.0.2.1"); !errors.Is(err, domain.ErrEmailInUse) {
		t.Fatalf("existing Customer email = %v, want conflict", err)
	}
	started, err := registration.BeginCashier(ctx, adminActor, "Cashier", email, temporary, "192.0.2.1")
	if err != nil {
		t.Fatalf("begin Cashier verification: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM pending_cashier_registrations WHERE id = $1`, started.ID) }()
	if _, err := service.PasswordLogin(ctx, email, temporary); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("unverified Cashier login = %v, want invalid", err)
	}
	if _, err := registration.VerifyCashier(ctx, customerActor, started.ID, "123456"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Customer verification = %v, want forbidden", err)
	}
	if _, err := registration.VerifyCashier(ctx, adminActor, started.ID, "000000"); !errors.Is(err, domain.ErrInvalidRegistration) {
		t.Fatalf("wrong code = %v, want invalid", err)
	}
	created, err := registration.VerifyCashier(ctx, adminActor, started.ID, mailer.Codes()[0])
	if err != nil {
		t.Fatalf("verify Cashier: %v", err)
	}
	if !created.Created {
		t.Fatal("new Cashier was not marked created")
	}
	defer func() {
		cleanupUser(t, pool, created.User.ID)
	}()
	replayed, err := registration.VerifyCashier(ctx, adminActor, started.ID, mailer.Codes()[0])
	if err != nil || replayed.User.ID != created.User.ID || !replayed.Created {
		t.Fatalf("replayed verification = (%+v, %v), want same cashier", replayed, err)
	}
	if _, err := registration.BeginCashier(ctx, adminActor, "Ignored", email, "ignored-password", "192.0.2.1"); !errors.Is(err, domain.ErrEmailInUse) {
		t.Fatalf("already verified Cashier = %v, want conflict", err)
	}
	login, err := service.PasswordLogin(ctx, email, temporary)
	if err != nil || !login.User.PasswordChangeRequired {
		t.Fatalf("temporary login = (%+v, %v), want password change required", login.User, err)
	}
	principal, err := service.Authenticate(ctx, login.AccessToken)
	if err != nil || !errors.Is(application.Authorize(principal, domain.RoleCashier), domain.ErrPasswordChangeRequired) {
		t.Fatalf("temporary cashier authorization = (%+v, %v)", principal, err)
	}
	if err := service.ChangeOwnPassword(ctx, principal, temporary, "permanent-cashier-password"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if err := application.Authorize(domain.Principal{UserID: principal.UserID, Role: principal.Role, Status: principal.Status}, domain.RoleCashier); err != nil {
		t.Fatalf("permanent password authorization: %v", err)
	}
	permanentLogin, err := service.PasswordLogin(ctx, email, "permanent-cashier-password")
	if err != nil || permanentLogin.User.PasswordChangeRequired {
		t.Fatalf("permanent login = (%+v, %v)", permanentLogin.User, err)
	}
	cashierActor, err := service.Authenticate(ctx, permanentLogin.AccessToken)
	if err != nil {
		t.Fatalf("authenticate Cashier: %v", err)
	}
	if _, err := service.ListCashiers(ctx, cashierActor, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Cashier list = %v, want forbidden", err)
	}
	if _, err := service.SetCashierStatus(ctx, adminActor, admin.ID, domain.AccountDisabled); !errors.Is(err, domain.ErrCashierNotFound) {
		t.Fatalf("admin self-disable = %v, want cashier-not-found", err)
	}
	if _, err := service.SetCashierStatus(ctx, adminActor, created.User.ID, domain.AccountDisabled); err != nil {
		t.Fatalf("disable cashier: %v", err)
	}
	if _, err := service.Authenticate(ctx, permanentLogin.AccessToken); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("disabled session = %v, want unauthenticated", err)
	}
	if _, err := service.PasswordLogin(ctx, email, "permanent-cashier-password"); !errors.Is(err, domain.ErrAccountDisabled) {
		t.Fatalf("disabled login = %v, want disabled", err)
	}
	if _, err := service.SetCashierStatus(ctx, adminActor, created.User.ID, domain.AccountDisabled); err != nil {
		t.Fatalf("idempotent disable: %v", err)
	}
	var disableAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_records WHERE action = 'CASHIER_DISABLED' AND target_id = $1`, created.User.ID).Scan(&disableAudits); err != nil || disableAudits != 1 {
		t.Fatalf("disable audits = (%d, %v), want one", disableAudits, err)
	}
	if _, err := service.SetCashierStatus(ctx, adminActor, created.User.ID, domain.AccountActive); err != nil {
		t.Fatalf("restore cashier: %v", err)
	}
	if _, err := service.ResetCashierPassword(ctx, adminActor, created.User.ID, "reset-cashier-password"); err != nil {
		t.Fatalf("reset cashier password: %v", err)
	}
	if _, err := service.PasswordLogin(ctx, email, "permanent-cashier-password"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("old password after reset = %v, want invalid", err)
	}
	resetLogin, err := service.PasswordLogin(ctx, email, "reset-cashier-password")
	if err != nil || !resetLogin.User.PasswordChangeRequired {
		t.Fatalf("reset login = (%+v, %v), want password change required", resetLogin.User, err)
	}
	if _, err := service.RecoverAdminPassword(ctx, customer.NormalizedEmail, "new-admin-password"); !errors.Is(err, domain.ErrAdminNotFound) {
		t.Fatalf("recover Customer as Admin = %v, want not found", err)
	}
	if _, err := service.RecoverAdminPassword(ctx, admin.NormalizedEmail, "new-admin-password"); err != nil {
		t.Fatalf("recover existing Admin: %v", err)
	}
	if _, err := service.PasswordLogin(ctx, admin.NormalizedEmail, integrationPassword); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("old Admin password = %v, want invalid", err)
	}
	if _, err := service.PasswordLogin(ctx, admin.NormalizedEmail, "new-admin-password"); err != nil {
		t.Fatalf("recovered Admin login: %v", err)
	}
}

func TestLegacyCashierMustVerifyEmailBeforeAnySessionWorks(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	identity := identityService(t, pool, clock, hasher, staticOAuthVerifier{})
	admin := seedPasswordUser(t, pool, hasher, domain.RoleAdmin, domain.AccountActive)
	defer cleanupUser(t, pool, admin.ID)
	cashier := seedPasswordUser(t, pool, hasher, domain.RoleCashier, domain.AccountActive)
	defer cleanupUser(t, pool, cashier.ID)
	oldSession, err := identity.PasswordLogin(ctx, cashier.NormalizedEmail, integrationPassword)
	if err != nil {
		t.Fatalf("create old session: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email_verified_at = NULL WHERE id = $1`, cashier.ID); err != nil {
		t.Fatalf("mark legacy cashier unverified: %v", err)
	}
	if _, err := identity.PasswordLogin(ctx, cashier.NormalizedEmail, integrationPassword); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("unverified password login = %v", err)
	}
	if _, err := identity.Authenticate(ctx, oldSession.AccessToken); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("unverified access session = %v", err)
	}
	if _, err := identity.Refresh(ctx, oldSession.RefreshToken); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("unverified refresh session = %v", err)
	}
	mailer := &registrationMailer{}
	registration, err := application.NewRegistrationService(identity, identitypostgres.New(pool), mailer,
		&registrationCodes{values: []string{"123456"}}, []byte("integration-only-registration-code-key-32-bytes"))
	if err != nil {
		t.Fatalf("create registration service: %v", err)
	}
	actor := domain.Principal{UserID: admin.ID, Role: domain.RoleAdmin, Status: domain.AccountActive}
	started, err := registration.BeginCashier(ctx, actor, "Verified Cashier", cashier.NormalizedEmail, "new-temporary-password", "192.0.2.5")
	if err != nil {
		t.Fatalf("begin legacy verification: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM pending_cashier_registrations WHERE id = $1`, started.ID) }()
	verified, err := registration.VerifyCashier(ctx, actor, started.ID, mailer.Codes()[0])
	if err != nil || verified.Created || !verified.EmailVerified || verified.User.ID != cashier.ID {
		t.Fatalf("legacy verification = (%+v, %v)", verified, err)
	}
	if _, err := identity.PasswordLogin(ctx, cashier.NormalizedEmail, integrationPassword); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("old password after verification = %v", err)
	}
	login, err := identity.PasswordLogin(ctx, cashier.NormalizedEmail, "new-temporary-password")
	if err != nil || !login.User.PasswordChangeRequired {
		t.Fatalf("verified temporary login = (%+v, %v)", login.User, err)
	}
}

func TestDisableCashierRevokesSessionWhenAppClockLagsDatabase(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	ctx := context.Background()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	identity := identityService(t, pool, clock, hasher, staticOAuthVerifier{})
	admin := seedPasswordUser(t, pool, hasher, domain.RoleAdmin, domain.AccountActive)
	defer cleanupUser(t, pool, admin.ID)
	cashier := seedPasswordUser(t, pool, hasher, domain.RoleCashier, domain.AccountActive)
	defer cleanupUser(t, pool, cashier.ID)
	if _, err := identity.PasswordLogin(ctx, cashier.NormalizedEmail, integrationPassword); err != nil {
		t.Fatalf("create cashier session: %v", err)
	}
	var createdAt time.Time
	if err := pool.QueryRow(ctx, `SELECT created_at FROM sessions WHERE user_id = $1`, cashier.ID).Scan(&createdAt); err != nil {
		t.Fatalf("read session creation time: %v", err)
	}
	store := identitypostgres.New(pool)
	if _, err := store.SetCashierStatus(ctx, admin.ID, cashier.ID, domain.AccountDisabled, createdAt.Add(-time.Second)); err != nil {
		t.Fatalf("disable cashier with lagging app clock: %v", err)
	}
	var revokedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT revoked_at FROM sessions WHERE user_id = $1`, cashier.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("read session revocation time: %v", err)
	}
	if revokedAt.Before(createdAt) {
		t.Fatalf("revocation %s precedes session creation %s", revokedAt, createdAt)
	}
}

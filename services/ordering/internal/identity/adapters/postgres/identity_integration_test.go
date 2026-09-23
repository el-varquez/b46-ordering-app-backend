//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	identitypostgres "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/postgres"
	identitysecurity "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/security"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

const integrationPassword = "correct horse battery staple"

type adjustableClock struct {
	mutex sync.RWMutex
	now   time.Time
}

func (clock *adjustableClock) Now() time.Time {
	clock.mutex.RLock()
	defer clock.mutex.RUnlock()
	return clock.now
}

func (clock *adjustableClock) Advance(duration time.Duration) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.now = clock.now.Add(duration)
}

type staticOAuthVerifier struct {
	identity domain.VerifiedIdentity
	err      error
}

func (verifier staticOAuthVerifier) Verify(
	context.Context,
	domain.Provider,
	string,
	string,
) (domain.VerifiedIdentity, error) {
	return verifier.identity, verifier.err
}

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is intentionally opt-in")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	return pool
}

func identityService(
	t *testing.T,
	pool *pgxpool.Pool,
	clock *adjustableClock,
	hasher *identitysecurity.Argon2id,
	verifier staticOAuthVerifier,
) *application.Service {
	t.Helper()
	service, err := application.New(
		identitypostgres.New(pool),
		hasher,
		identitysecurity.RandomTokenGenerator{},
		clock,
		verifier,
		application.Config{
			AccessTokenLifetime:  15 * time.Minute,
			RefreshTokenLifetime: 30 * 24 * time.Hour,
			OAuthIntentLifetime:  10 * time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("create identity service: %v", err)
	}
	return service
}

func seedPasswordUser(
	t *testing.T,
	pool *pgxpool.Pool,
	hasher *identitysecurity.Argon2id,
	role domain.Role,
	status domain.AccountStatus,
) domain.User {
	t.Helper()
	passwordHash, err := hasher.Hash(integrationPassword)
	if err != nil {
		t.Fatalf("hash fixture password: %v", err)
	}
	var user domain.User
	err = pool.QueryRow(context.Background(), `
		INSERT INTO users (name, normalized_email, role, account_status)
		VALUES ($1, lower($2) || '-' || gen_random_uuid() || '@example.test', $3, $4)
		RETURNING id, name, normalized_email, role, account_status
	`, "Integration "+string(role), string(role), role, status).Scan(
		&user.ID, &user.Name, &user.NormalizedEmail, &user.Role, &user.Status,
	)
	if err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	_, err = pool.Exec(context.Background(), `
		INSERT INTO login_identities (user_id, provider, provider_subject, password_hash)
		VALUES ($1, 'PASSWORD', $2, $3)
	`, user.ID, user.NormalizedEmail, passwordHash)
	if err != nil {
		t.Fatalf("insert fixture password identity: %v", err)
	}
	return user
}

func cleanupUser(t *testing.T, pool *pgxpool.Pool, userID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		DELETE FROM audit_records
		WHERE actor_user_id = $1 OR (target_type = 'USER' AND target_id = $1::text)
	`, userID); err != nil {
		t.Errorf("delete fixture audit records: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Errorf("delete fixture user: %v", err)
	}
}

func TestPasswordLoginSupportsRolesAndStoresOnlyTokenHashes(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	service := identityService(t, pool, clock, hasher, staticOAuthVerifier{})

	for _, role := range []domain.Role{domain.RoleCustomer, domain.RoleCashier, domain.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			user := seedPasswordUser(t, pool, hasher, role, domain.AccountActive)
			defer cleanupUser(t, pool, user.ID)

			session, err := service.PasswordLogin(context.Background(), user.NormalizedEmail, integrationPassword)
			if err != nil {
				t.Fatalf("PasswordLogin() error = %v", err)
			}
			if session.User.ID != user.ID || session.User.Role != role {
				t.Fatalf("PasswordLogin() user = %#v, want canonical %s user", session.User, role)
			}
			var accessHash, refreshHash string
			if err := pool.QueryRow(context.Background(), `
				SELECT access_token_hash, refresh_token_hash
				FROM sessions WHERE user_id = $1
			`, user.ID).Scan(&accessHash, &refreshHash); err != nil {
				t.Fatalf("read stored token hashes: %v", err)
			}
			if accessHash == session.AccessToken || refreshHash == session.RefreshToken ||
				len(accessHash) != 64 || len(refreshHash) != 64 {
				t.Fatal("session persisted plaintext or malformed token hashes")
			}
		})
	}
}

func TestPasswordLoginFailureAndHashUpgrade(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	oldHasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	newHasher := identitysecurity.NewArgon2id(32*1024, 3, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	service := identityService(t, pool, clock, newHasher, staticOAuthVerifier{})
	user := seedPasswordUser(t, pool, oldHasher, domain.RoleCustomer, domain.AccountActive)
	defer cleanupUser(t, pool, user.ID)

	_, unknownErr := service.PasswordLogin(context.Background(), "unknown@example.test", integrationPassword)
	_, wrongErr := service.PasswordLogin(context.Background(), user.NormalizedEmail, "definitely wrong password")
	if !errors.Is(unknownErr, domain.ErrInvalidCredentials) || !errors.Is(wrongErr, domain.ErrInvalidCredentials) {
		t.Fatalf("unknown error = %v, wrong error = %v, want identical invalid credentials", unknownErr, wrongErr)
	}
	if _, err := service.PasswordLogin(context.Background(), user.NormalizedEmail, integrationPassword); err != nil {
		t.Fatalf("PasswordLogin() upgrade error = %v", err)
	}
	var upgraded string
	if err := pool.QueryRow(context.Background(), `
		SELECT password_hash FROM login_identities
		WHERE user_id = $1 AND provider = 'PASSWORD'
	`, user.ID).Scan(&upgraded); err != nil {
		t.Fatalf("read upgraded password hash: %v", err)
	}
	matches, needsRehash, err := newHasher.Verify(integrationPassword, upgraded)
	if err != nil || !matches || needsRehash {
		t.Fatalf("upgraded hash verification = (%v, %v, %v), want (true, false, nil)", matches, needsRehash, err)
	}

	disabled := seedPasswordUser(t, pool, oldHasher, domain.RoleCashier, domain.AccountDisabled)
	defer cleanupUser(t, pool, disabled.ID)
	if _, err := service.PasswordLogin(context.Background(), disabled.NormalizedEmail, integrationPassword); !errors.Is(err, domain.ErrAccountDisabled) {
		t.Fatalf("disabled PasswordLogin() error = %v, want ErrAccountDisabled", err)
	}
}

func TestLogoutIsFamilyScopedAndDisableRevokesEveryFamily(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	service := identityService(t, pool, clock, hasher, staticOAuthVerifier{})
	customer := seedPasswordUser(t, pool, hasher, domain.RoleCustomer, domain.AccountActive)
	admin := seedPasswordUser(t, pool, hasher, domain.RoleAdmin, domain.AccountActive)
	defer cleanupUser(t, pool, customer.ID)
	defer cleanupUser(t, pool, admin.ID)

	first, err := service.PasswordLogin(context.Background(), customer.NormalizedEmail, integrationPassword)
	if err != nil {
		t.Fatalf("first PasswordLogin() error = %v", err)
	}
	second, err := service.PasswordLogin(context.Background(), customer.NormalizedEmail, integrationPassword)
	if err != nil {
		t.Fatalf("second PasswordLogin() error = %v", err)
	}
	if err := service.Logout(context.Background(), first.AccessToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	var logoutAudits int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM audit_records
		WHERE action = 'SESSION_FAMILY_REVOKED'
		  AND actor_user_id = $1
	`, customer.ID).Scan(&logoutAudits); err != nil {
		t.Fatalf("count logout audits: %v", err)
	}
	if logoutAudits != 1 {
		t.Fatalf("logout audit count = %d, want 1", logoutAudits)
	}
	if err := service.Logout(context.Background(), first.AccessToken); err != nil {
		t.Fatalf("repeat Logout() error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), first.AccessToken); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("logged-out access error = %v, want ErrUnauthenticated", err)
	}
	if _, err := service.Authenticate(context.Background(), second.AccessToken); err != nil {
		t.Fatalf("independent family Authenticate() error = %v", err)
	}

	actor := domain.Principal{UserID: admin.ID, Role: domain.RoleAdmin, Status: domain.AccountActive}
	if err := service.DisableUser(context.Background(), actor, customer.ID); err != nil {
		t.Fatalf("DisableUser() error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), second.AccessToken); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("disabled access error = %v, want ErrUnauthenticated", err)
	}
	if _, err := service.Refresh(context.Background(), second.RefreshToken); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("disabled refresh error = %v, want ErrUnauthenticated", err)
	}
}

func TestExpiredAccessCanUseValidRefresh(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	service := identityService(t, pool, clock, hasher, staticOAuthVerifier{})
	user := seedPasswordUser(t, pool, hasher, domain.RoleCustomer, domain.AccountActive)
	defer cleanupUser(t, pool, user.ID)

	login, err := service.PasswordLogin(context.Background(), user.NormalizedEmail, integrationPassword)
	if err != nil {
		t.Fatalf("PasswordLogin() error = %v", err)
	}
	clock.Advance(16 * time.Minute)
	if _, err := service.Authenticate(context.Background(), login.AccessToken); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("expired access error = %v, want ErrUnauthenticated", err)
	}
	refreshed, err := service.Refresh(context.Background(), login.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if refreshed.AccessToken == login.AccessToken || refreshed.RefreshToken == login.RefreshToken {
		t.Fatal("Refresh() reused a plaintext token")
	}
	if _, err := service.Authenticate(context.Background(), refreshed.AccessToken); err != nil {
		t.Fatalf("refreshed access Authenticate() error = %v", err)
	}
}

func TestOAuthIntentExpiryReplayAndExistingEmailProtection(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}

	expiredEmail := fmt.Sprintf("oauth-expired-%d@example.test", time.Now().UnixNano())
	verifier := staticOAuthVerifier{identity: domain.VerifiedIdentity{
		Provider: domain.ProviderGoogle, Subject: "expired-subject-" + expiredEmail,
		Email: expiredEmail, EmailVerified: true, SuggestedName: "Expired OAuth",
	}}
	service := identityService(t, pool, clock, hasher, verifier)
	expiredIntent, err := service.BeginOAuthLogin(context.Background(), domain.ProviderGoogle)
	if err != nil {
		t.Fatalf("BeginOAuthLogin() error = %v", err)
	}
	clock.Advance(11 * time.Minute)
	if _, err := service.OAuthLogin(context.Background(), expiredIntent.IntentID, "signed-token"); !errors.Is(err, domain.ErrInvalidOAuthIntent) {
		t.Fatalf("expired OAuthLogin() error = %v, want ErrInvalidOAuthIntent", err)
	}

	newEmail := fmt.Sprintf("oauth-new-%d@example.test", time.Now().UnixNano())
	service = identityService(t, pool, clock, hasher, staticOAuthVerifier{identity: domain.VerifiedIdentity{
		Provider: domain.ProviderGoogle, Subject: "new-subject-" + newEmail,
		Email: newEmail, EmailVerified: true, SuggestedName: "New OAuth",
	}})
	intent, err := service.BeginOAuthLogin(context.Background(), domain.ProviderGoogle)
	if err != nil {
		t.Fatalf("BeginOAuthLogin() second error = %v", err)
	}
	created, err := service.OAuthLogin(context.Background(), intent.IntentID, "signed-token")
	if err != nil {
		t.Fatalf("OAuthLogin() create error = %v", err)
	}
	defer cleanupUser(t, pool, created.User.ID)
	if created.User.Role != domain.RoleCustomer {
		t.Fatalf("OAuthLogin() role = %s, want CUSTOMER", created.User.Role)
	}
	if _, err := service.OAuthLogin(context.Background(), intent.IntentID, "signed-token"); !errors.Is(err, domain.ErrInvalidOAuthIntent) {
		t.Fatalf("replayed OAuthLogin() error = %v, want ErrInvalidOAuthIntent", err)
	}

	existing := seedPasswordUser(t, pool, hasher, domain.RoleAdmin, domain.AccountActive)
	defer cleanupUser(t, pool, existing.ID)
	protected := identityService(t, pool, clock, hasher, staticOAuthVerifier{identity: domain.VerifiedIdentity{
		Provider: domain.ProviderGoogle, Subject: "protected-subject-" + existing.ID,
		Email: existing.NormalizedEmail, EmailVerified: true, SuggestedName: existing.Name,
	}})
	protectedIntent, err := protected.BeginOAuthLogin(context.Background(), domain.ProviderGoogle)
	if err != nil {
		t.Fatalf("BeginOAuthLogin() protected error = %v", err)
	}
	if _, err := protected.OAuthLogin(context.Background(), protectedIntent.IntentID, "signed-token"); !errors.Is(err, domain.ErrIdentityLinkRequired) {
		t.Fatalf("matching-email OAuthLogin() error = %v, want ErrIdentityLinkRequired", err)
	}
	var users, oauthIdentities int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE normalized_email = $1`, existing.NormalizedEmail).Scan(&users); err != nil {
		t.Fatalf("count protected users: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM login_identities WHERE provider = 'GOOGLE' AND provider_subject = $1
	`, "protected-subject-"+existing.ID).Scan(&oauthIdentities); err != nil {
		t.Fatalf("count protected identities: %v", err)
	}
	if users != 1 || oauthIdentities != 0 {
		t.Fatalf("matching email created users=%d identities=%d, want 1 and 0", users, oauthIdentities)
	}
}

func TestConcurrentOAuthLinkHasOneWinnerAndPreservesUser(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	first := seedPasswordUser(t, pool, hasher, domain.RoleCustomer, domain.AccountActive)
	second := seedPasswordUser(t, pool, hasher, domain.RoleCashier, domain.AccountActive)
	defer cleanupUser(t, pool, first.ID)
	defer cleanupUser(t, pool, second.ID)

	subject := "shared-provider-subject-" + first.ID
	verifier := staticOAuthVerifier{identity: domain.VerifiedIdentity{
		Provider: domain.ProviderGoogle, Subject: subject,
		Email: "provider@example.test", EmailVerified: true, SuggestedName: "Provider User",
	}}
	service := identityService(t, pool, clock, hasher, verifier)
	firstIntent, err := service.BeginOAuthLink(context.Background(), domain.Principal{UserID: first.ID}, domain.ProviderGoogle)
	if err != nil {
		t.Fatalf("first BeginOAuthLink() error = %v", err)
	}
	secondIntent, err := service.BeginOAuthLink(context.Background(), domain.Principal{UserID: second.ID}, domain.ProviderGoogle)
	if err != nil {
		t.Fatalf("second BeginOAuthLink() error = %v", err)
	}

	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for _, attempt := range []struct {
		principal domain.Principal
		intentID  string
	}{
		{domain.Principal{UserID: first.ID}, firstIntent.IntentID},
		{domain.Principal{UserID: second.ID}, secondIntent.IntentID},
	} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errorsFound <- service.LinkOAuth(context.Background(), attempt.principal, attempt.intentID, "signed-token")
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)

	successes := 0
	conflicts := 0
	for linkErr := range errorsFound {
		switch {
		case linkErr == nil:
			successes++
		case errors.Is(linkErr, domain.ErrIdentityLinked):
			conflicts++
		default:
			t.Fatalf("LinkOAuth() unexpected error = %v", linkErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("link successes=%d conflicts=%d, want 1 and 1", successes, conflicts)
	}
	var ownerID string
	if err := pool.QueryRow(context.Background(), `
		SELECT user_id FROM login_identities WHERE provider = 'GOOGLE' AND provider_subject = $1
	`, subject).Scan(&ownerID); err != nil {
		t.Fatalf("read linked identity owner: %v", err)
	}
	loginIntent, err := service.BeginOAuthLogin(context.Background(), domain.ProviderGoogle)
	if err != nil {
		t.Fatalf("BeginOAuthLogin() linked error = %v", err)
	}
	login, err := service.OAuthLogin(context.Background(), loginIntent.IntentID, "signed-token")
	if err != nil {
		t.Fatalf("OAuthLogin() linked error = %v", err)
	}
	if login.User.ID != ownerID || (login.User.Role != first.Role && login.User.Role != second.Role) {
		t.Fatalf("linked OAuth user = %#v, want canonical owner %s", login.User, ownerID)
	}
}

func TestBootstrapAdminIsIdempotentAuditedAndRejectsConflict(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	service := identityService(t, pool, clock, hasher, staticOAuthVerifier{})
	email := fmt.Sprintf("bootstrap-%d@example.test", time.Now().UnixNano())

	admin, err := service.BootstrapAdmin(context.Background(), "Bootstrap Admin", email, integrationPassword)
	if err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	defer cleanupUser(t, pool, admin.ID)
	again, err := service.BootstrapAdmin(context.Background(), "Ignored Name", email, integrationPassword)
	if err != nil || again.ID != admin.ID {
		t.Fatalf("idempotent BootstrapAdmin() = (%#v, %v), want same admin", again, err)
	}
	_, err = service.BootstrapAdmin(
		context.Background(), "Second Admin",
		fmt.Sprintf("other-bootstrap-%d@example.test", time.Now().UnixNano()),
		integrationPassword,
	)
	if !errors.Is(err, domain.ErrAdminAlreadyExists) {
		t.Fatalf("conflicting BootstrapAdmin() error = %v, want ErrAdminAlreadyExists", err)
	}
	var auditCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM audit_records
		WHERE action = 'ADMIN_BOOTSTRAPPED' AND target_id = $1
	`, admin.ID).Scan(&auditCount); err != nil {
		t.Fatalf("count bootstrap audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("bootstrap audit count = %d, want 1", auditCount)
	}
}

func TestUnverifiedOAuthEmailCannotCreateCustomer(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	email := fmt.Sprintf("unverified-%d@example.test", time.Now().UnixNano())
	service := identityService(t, pool, clock, hasher, staticOAuthVerifier{identity: domain.VerifiedIdentity{
		Provider: domain.ProviderGoogle, Subject: "unverified-" + email,
		Email: email, EmailVerified: false, SuggestedName: "Unverified",
	}})
	intent, err := service.BeginOAuthLogin(context.Background(), domain.ProviderGoogle)
	if err != nil {
		t.Fatalf("BeginOAuthLogin() error = %v", err)
	}
	if _, err := service.OAuthLogin(context.Background(), intent.IntentID, "signed-token"); !errors.Is(err, domain.ErrInvalidOAuthToken) {
		t.Fatalf("OAuthLogin() error = %v, want ErrInvalidOAuthToken", err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE normalized_email = $1`, email).Scan(&count); err != nil {
		t.Fatalf("count unverified users: %v", err)
	}
	if count != 0 {
		t.Fatalf("unverified OAuth created %d users", count)
	}
}

func TestMalformedPasswordHashFailsSafely(t *testing.T) {
	pool := integrationPool(t)
	defer pool.Close()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	service := identityService(t, pool, clock, hasher, staticOAuthVerifier{})
	user := seedPasswordUser(t, pool, hasher, domain.RoleCustomer, domain.AccountActive)
	defer cleanupUser(t, pool, user.ID)
	if _, err := pool.Exec(context.Background(), `
		UPDATE login_identities SET password_hash = 'malformed'
		WHERE user_id = $1 AND provider = 'PASSWORD'
	`, user.ID); err != nil {
		t.Fatalf("corrupt fixture password hash: %v", err)
	}
	_, err := service.PasswordLogin(context.Background(), user.NormalizedEmail, integrationPassword)
	if err == nil || errors.Is(err, domain.ErrInvalidCredentials) || strings.Contains(err.Error(), integrationPassword) {
		t.Fatalf("PasswordLogin() malformed hash error = %v, want safe internal failure", err)
	}
}

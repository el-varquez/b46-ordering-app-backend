//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	identitypostgres "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/postgres"
	identitysecurity "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/security"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type registrationCodes struct {
	mutex  sync.Mutex
	values []string
}

func (codes *registrationCodes) NewCode() (string, error) {
	codes.mutex.Lock()
	defer codes.mutex.Unlock()
	if len(codes.values) == 0 {
		return "", errors.New("no fixture code")
	}
	value := codes.values[0]
	codes.values = codes.values[1:]
	return value, nil
}

type registrationMailer struct {
	mutex sync.Mutex
	sent  []string
	fail  bool
}

func (*registrationMailer) Configured() bool { return true }

func (mailer *registrationMailer) SendCode(_ context.Context, _, code string) error {
	mailer.mutex.Lock()
	defer mailer.mutex.Unlock()
	if mailer.fail {
		return errors.New("fixture mail failure")
	}
	mailer.sent = append(mailer.sent, code)
	return nil
}

func (mailer *registrationMailer) Codes() []string {
	mailer.mutex.Lock()
	defer mailer.mutex.Unlock()
	return append([]string(nil), mailer.sent...)
}

func registrationFixture(t *testing.T, values ...string) (*application.RegistrationService, *registrationMailer, *adjustableClock, *application.Service) {
	t.Helper()
	pool := integrationPool(t)
	t.Cleanup(pool.Close)
	clock := &adjustableClock{now: time.Now().UTC().Add(time.Second)}
	identity := identityService(t, pool, clock, identitysecurity.NewArgon2id(19*1024, 2, 1), staticOAuthVerifier{})
	mailer := &registrationMailer{}
	registration, err := application.NewRegistrationService(identity, identitypostgres.New(pool), mailer,
		&registrationCodes{values: values}, []byte("integration-only-registration-code-key-32-bytes"))
	if err != nil {
		t.Fatalf("create registration service: %v", err)
	}
	return registration, mailer, clock, identity
}

func TestRegistrationVerifiesOnceAndUsesNormalPasswordSession(t *testing.T) {
	registration, mailer, _, identity := registrationFixture(t, "123456")
	email := fmt.Sprintf("register-%d@example.test", time.Now().UnixNano())
	ctx := context.Background()
	const password = "abcdefgh"
	start, err := registration.Begin(ctx, "New Customer", email, password, "192.0.2.1")
	if err != nil {
		t.Fatalf("begin registration: %v", err)
	}
	if start.ID == "" || len(mailer.Codes()) != 1 {
		t.Fatal("registration did not create a delivery attempt")
	}
	if _, err := registration.Verify(ctx, start.ID, "000000"); !errors.Is(err, domain.ErrInvalidRegistration) {
		t.Fatalf("wrong code error = %v", err)
	}
	session, err := registration.Verify(ctx, start.ID, mailer.Codes()[0])
	if err != nil {
		t.Fatalf("verify registration: %v", err)
	}
	if session.User.Role != domain.RoleCustomer || session.User.NormalizedEmail != email || session.AccessToken == "" || session.RefreshToken == "" {
		t.Fatalf("unexpected verified session role/email/tokens")
	}
	if _, err := registration.Verify(ctx, start.ID, mailer.Codes()[0]); !errors.Is(err, domain.ErrInvalidRegistration) {
		t.Fatalf("replayed code error = %v", err)
	}
	relogin, err := identity.PasswordLogin(ctx, email, password)
	if err != nil || relogin.User.ID != session.User.ID {
		t.Fatalf("password re-login error = %v", err)
	}
}

func TestRegistrationCannotClaimStaffEmail(t *testing.T) {
	registration, mailer, _, _ := registrationFixture(t, "123456")
	pool := integrationPool(t)
	defer pool.Close()
	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	staff := seedPasswordUser(t, pool, hasher, domain.RoleCashier, domain.AccountActive)
	defer cleanupUser(t, pool, staff.ID)
	start, err := registration.Begin(context.Background(), "Imposter", staff.NormalizedEmail, integrationPassword, "192.0.2.2")
	if err != nil || start.ID == "" {
		t.Fatalf("generic registration error = %v", err)
	}
	if len(mailer.Codes()) != 0 {
		t.Fatal("code sent for existing staff email")
	}
	if _, err := registration.Verify(context.Background(), start.ID, "123456"); !errors.Is(err, domain.ErrInvalidRegistration) {
		t.Fatalf("staff claim error = %v", err)
	}
}

func TestRegistrationResendInvalidatesOldCode(t *testing.T) {
	registration, mailer, clock, _ := registrationFixture(t, "123456", "222222", "654321")
	email := fmt.Sprintf("resend-%d@example.test", time.Now().UnixNano())
	ctx := context.Background()
	start, err := registration.Begin(ctx, "Customer", email, integrationPassword, "192.0.2.3")
	if err != nil {
		t.Fatalf("begin registration: %v", err)
	}
	if _, err := registration.Resend(ctx, start.ID, "192.0.2.3"); !errors.Is(err, domain.ErrRegistrationCooldown) {
		t.Fatalf("early resend error = %v", err)
	}
	// A cooldown response never updates the stored code.
	clock.Advance(61 * time.Second)
	_, err = registration.Resend(ctx, start.ID, "192.0.2.3")
	if err != nil {
		t.Fatalf("resend registration: %v", err)
	}
	if len(mailer.Codes()) != 2 {
		t.Fatal("resend did not deliver replacement")
	}
	if _, err := registration.Verify(ctx, start.ID, "123456"); !errors.Is(err, domain.ErrInvalidRegistration) {
		t.Fatalf("old code error = %v", err)
	}
	if _, err := registration.Verify(ctx, start.ID, "654321"); err != nil {
		t.Fatalf("replacement code: %v", err)
	}
}

func TestRegistrationExpiresAndEmailFailureNeverActivates(t *testing.T) {
	registration, mailer, clock, _ := registrationFixture(t, "123456", "654321")
	mailer.fail = true
	email := fmt.Sprintf("mail-fail-%d@example.test", time.Now().UnixNano())
	if _, err := registration.Begin(context.Background(), "Customer", email, integrationPassword, "192.0.2.4"); err != nil {
		t.Fatalf("mail failure must preserve generic acknowledgement: %v", err)
	}
	pool := integrationPool(t)
	defer pool.Close()
	var users int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE normalized_email=$1`, email).Scan(&users); err != nil || users != 0 {
		t.Fatalf("mail failure created user = %d, error = %v", users, err)
	}
	mailer.fail = false
	clock.Advance(11 * time.Minute)
	start, err := registration.Begin(context.Background(), "Customer", email, integrationPassword, "192.0.2.4")
	if err != nil {
		t.Fatalf("retry begin: %v", err)
	}
	clock.Advance(11 * time.Minute)
	if _, err := registration.Verify(context.Background(), start.ID, "654321"); !errors.Is(err, domain.ErrInvalidRegistration) {
		t.Fatalf("expired code error = %v", err)
	}
}

func TestRegistrationCodeLocksAfterFiveWrongAttempts(t *testing.T) {
	registration, mailer, _, _ := registrationFixture(t, "123456")
	email := fmt.Sprintf("attempt-limit-%d@example.test", time.Now().UnixNano())
	start, err := registration.Begin(context.Background(), "Customer", email, integrationPassword, "192.0.2.5")
	if err != nil {
		t.Fatalf("begin registration: %v", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := registration.Verify(context.Background(), start.ID, "000000"); !errors.Is(err, domain.ErrInvalidRegistration) {
			t.Fatalf("attempt %d error = %v", attempt+1, err)
		}
	}
	if _, err := registration.Verify(context.Background(), start.ID, mailer.Codes()[0]); !errors.Is(err, domain.ErrInvalidRegistration) {
		t.Fatalf("correct code after limit error = %v", err)
	}
}

func TestConcurrentRegistrationVerificationCreatesOneCustomer(t *testing.T) {
	registration, mailer, _, _ := registrationFixture(t, "123456")
	email := fmt.Sprintf("concurrent-register-%d@example.test", time.Now().UnixNano())
	start, err := registration.Begin(context.Background(), "Customer", email, integrationPassword, "192.0.2.6")
	if err != nil {
		t.Fatalf("begin registration: %v", err)
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for attempt := 0; attempt < 2; attempt++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, verifyErr := registration.Verify(context.Background(), start.ID, mailer.Codes()[0])
			results <- verifyErr
		}()
	}
	wait.Wait()
	close(results)
	successes, denied := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrInvalidRegistration):
			denied++
		default:
			t.Fatalf("unexpected verify error: %v", err)
		}
	}
	if successes != 1 || denied != 1 {
		t.Fatalf("concurrent verification successes=%d denied=%d", successes, denied)
	}
	pool := integrationPool(t)
	defer pool.Close()
	var users int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE normalized_email=$1`, email).Scan(&users); err != nil || users != 1 {
		t.Fatalf("users for email=%d, error=%v", users, err)
	}
}

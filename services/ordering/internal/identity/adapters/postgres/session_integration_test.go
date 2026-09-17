//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	identitypostgres "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/postgres"
	identitysecurity "github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/adapters/security"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestRefreshHasOneWinnerAndReplayRevokesFamily(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is intentionally opt-in")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()

	hasher := identitysecurity.NewArgon2id(19*1024, 2, 1)
	password := "correct horse battery staple"
	passwordHash, err := hasher.Hash(password)
	if err != nil {
		t.Fatalf("hash fixture password: %v", err)
	}
	var userID string
	err = pool.QueryRow(ctx, `
		WITH new_user AS (
			INSERT INTO users (name, normalized_email, role)
			VALUES ('Refresh Test', 'refresh-test-' || gen_random_uuid() || '@example.test', 'CUSTOMER')
			RETURNING id, normalized_email
		), new_identity AS (
			INSERT INTO login_identities (user_id, provider, provider_subject, password_hash)
			SELECT id, 'PASSWORD', normalized_email, $1 FROM new_user
		)
		SELECT id FROM new_user
	`, passwordHash).Scan(&userID)
	if err != nil {
		t.Fatalf("seed identity: %v", err)
	}
	defer cleanupUser(t, pool, userID)

	var email string
	if err := pool.QueryRow(ctx, `SELECT normalized_email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
		t.Fatalf("read fixture email: %v", err)
	}
	clock := fixedClock{now: time.Now().UTC().Add(time.Second)}
	identity, err := application.New(
		identitypostgres.New(pool), hasher,
		identitysecurity.RandomTokenGenerator{}, clock, nil,
		application.Config{
			AccessTokenLifetime:  15 * time.Minute,
			RefreshTokenLifetime: 30 * 24 * time.Hour,
			OAuthIntentLifetime:  10 * time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("create identity application: %v", err)
	}
	login, err := identity.PasswordLogin(ctx, email, password)
	if err != nil {
		t.Fatalf("PasswordLogin() error = %v", err)
	}

	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	results := make(chan domain.SessionTokens, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, refreshErr := identity.Refresh(ctx, login.RefreshToken)
			results <- result
			errorsFound <- refreshErr
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)

	successes := 0
	unauthenticated := 0
	var unexpected []error
	var winner domain.SessionTokens
	for result := range results {
		if result.AccessToken != "" {
			successes++
			winner = result
		}
	}
	for refreshErr := range errorsFound {
		if errors.Is(refreshErr, domain.ErrUnauthenticated) {
			unauthenticated++
		} else if refreshErr != nil {
			unexpected = append(unexpected, refreshErr)
		}
	}
	if successes != 1 || unauthenticated != 1 {
		t.Fatalf(
			"successes = %d, unauthenticated = %d, unexpected = %v, want 1 and 1",
			successes, unauthenticated, unexpected,
		)
	}
	if _, err := identity.Authenticate(ctx, winner.AccessToken); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("winner token after replay error = %v, want family revocation", err)
	}
}

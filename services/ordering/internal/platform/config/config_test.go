package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("Load() error = %v, want DATABASE_URL validation", err)
	}
}

func TestLoadUsesValidatedDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/b46?sslmode=disable")
	t.Setenv("HTTP_HOST", "")
	t.Setenv("HTTP_PORT", "")
	t.Setenv("LOG_LEVEL", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.HTTPAddress != "0.0.0.0:8080" {
		t.Errorf("HTTPAddress = %q, want %q", got.HTTPAddress, "0.0.0.0:8080")
	}
	if got.MaxRequestBodyBytes != 1<<20 {
		t.Errorf("MaxRequestBodyBytes = %d, want %d", got.MaxRequestBodyBytes, 1<<20)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/b46")
	t.Setenv("HTTP_PORT", "70000")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "HTTP_PORT") {
		t.Fatalf("Load() error = %v, want HTTP_PORT validation", err)
	}
}

func TestLoadRejectsUnknownEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "prod-ish")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/b46")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("Load() error = %v, want APP_ENV validation", err)
	}
}

func TestLoadIdentityDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/b46")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.AccessTokenLifetime != 15*time.Minute {
		t.Errorf("AccessTokenLifetime = %s, want 15m", got.AccessTokenLifetime)
	}
	if got.RefreshTokenLifetime != 30*24*time.Hour {
		t.Errorf("RefreshTokenLifetime = %s, want 720h", got.RefreshTokenLifetime)
	}
	if got.ArgonMemoryKiB < 19*1024 || got.ArgonIterations < 2 {
		t.Fatal("Argon2id defaults are below the locked minimum")
	}
}

func TestProductionRequiresOAuthAudiences(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/b46")
	t.Setenv("GOOGLE_CLIENT_IDS", "")
	t.Setenv("APPLE_CLIENT_IDS", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "CLIENT_IDS") {
		t.Fatalf("Load() error = %v, want provider audience validation", err)
	}
}

func TestLoadOutboxWorkerDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/b46")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.OutboxPollInterval != 500*time.Millisecond || got.OutboxBatchSize != 10 ||
		got.OutboxLeaseTimeout != 30*time.Second || got.OutboxRetryBase != time.Second ||
		got.OutboxRetryMax != time.Minute {
		t.Fatalf("unexpected worker defaults: %#v", got)
	}
}

func TestLoadRejectsWorkerLeaseShorterThanPoll(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/b46")
	t.Setenv("OUTBOX_POLL_INTERVAL", "2s")
	t.Setenv("OUTBOX_LEASE_TIMEOUT", "1s")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "OUTBOX_LEASE_TIMEOUT") {
		t.Fatalf("Load() error = %v, want lease validation", err)
	}
}

func TestLoadAcceptsHTTPInventoryAdapter(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/b46")
	t.Setenv("INVENTORY_ADAPTER_MODE", "HTTP")
	t.Setenv("INVENTORY_ADAPTER_URL", "http://127.0.0.1:8081")
	t.Setenv("INVENTORY_ADAPTER_TOKEN", "local-placeholder-with-at-least-32-bytes")
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.InventoryAdapterMode != "HTTP" || got.InventoryAdapterTimeout != 5*time.Second ||
		got.InventoryMaxResponseBodyBytes != 256*1024 {
		t.Fatalf("inventory config = %#v", got)
	}
}

func TestProductionRejectsFakeInventoryAdapter(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/b46")
	t.Setenv("GOOGLE_CLIENT_IDS", "google-client")
	t.Setenv("APPLE_CLIENT_IDS", "apple-client")
	t.Setenv("REGISTRATION_CODE_KEY", "example-test-key-with-at-least-32-bytes")
	t.Setenv("SMTP_HOST", "mail.example.test")
	t.Setenv("SMTP_FROM", "hello@example.test")
	t.Setenv("SMTP_USERNAME", "hello@example.test")
	t.Setenv("SMTP_PASSWORD", "test-only-password")
	t.Setenv("INVENTORY_ADAPTER_MODE", "FAKE")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "INVENTORY_ADAPTER_MODE") {
		t.Fatalf("Load() error = %v, want production inventory validation", err)
	}
}

func TestProductionRequiresRegistrationSecretsButNotAppleAudience(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/b46")
	t.Setenv("GOOGLE_CLIENT_IDS", "google-client")
	t.Setenv("APPLE_CLIENT_IDS", "")
	t.Setenv("REGISTRATION_CODE_KEY", "")
	t.Setenv("SMTP_HOST", "mail.example.test")
	t.Setenv("SMTP_FROM", "hello@example.test")
	t.Setenv("SMTP_USERNAME", "hello@example.test")
	t.Setenv("SMTP_PASSWORD", "test-only-password")
	t.Setenv("INVENTORY_ADAPTER_MODE", "HTTP")
	t.Setenv("INVENTORY_ADAPTER_URL", "https://inventory.example.test")
	t.Setenv("INVENTORY_ADAPTER_TOKEN", "example-inventory-token-at-least-32-bytes")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "REGISTRATION_CODE_KEY") {
		t.Fatalf("Load() error = %v, want registration key validation", err)
	}
	t.Setenv("REGISTRATION_CODE_KEY", "example-registration-key-at-least-32-bytes")
	if _, err := Load(); err != nil {
		t.Fatalf("production should allow Google-only audience: %v", err)
	}
	t.Setenv("SMTP_ALLOW_INSECURE_LOCAL", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "insecure SMTP") {
		t.Fatalf("Load() error = %v, want production SMTP TLS validation", err)
	}
}

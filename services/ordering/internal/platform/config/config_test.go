package config

import (
	"strings"
	"testing"
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

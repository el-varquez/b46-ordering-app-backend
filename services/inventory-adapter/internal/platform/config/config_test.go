package config

import "testing"

func TestLoadRequiresStoreConnectionSecretAndActor(t *testing.T) {
	t.Setenv("STORE_DATABASE_URL", "")
	t.Setenv("INVENTORY_SERVICE_TOKEN", "")
	t.Setenv("POS_MOVEMENT_ACTOR_ID", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted missing required values")
	}
}

func TestLoadAcceptsSafeDevelopmentConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("STORE_DATABASE_URL", "postgres://user:not-a-secret@localhost:5434/b46_store_test?sslmode=disable")
	t.Setenv("INVENTORY_SERVICE_TOKEN", "local-placeholder-with-at-least-32-bytes")
	t.Setenv("POS_MOVEMENT_ACTOR_ID", "b4600000-0000-4000-8000-000000000046")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.HTTPAddress != "0.0.0.0:8081" || got.MaxRequestBodyBytes != 262144 {
		t.Fatalf("Load() = %#v", got)
	}
}

func TestLoadRejectsUnsafeBounds(t *testing.T) {
	t.Setenv("STORE_DATABASE_URL", "postgres://user:not-a-secret@localhost:5434/b46_store_test?sslmode=disable")
	t.Setenv("INVENTORY_SERVICE_TOKEN", "local-placeholder-with-at-least-32-bytes")
	t.Setenv("POS_MOVEMENT_ACTOR_ID", "b4600000-0000-4000-8000-000000000046")
	t.Setenv("MAX_REQUEST_BODY_BYTES", "10")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted undersized request-body limit")
	}
}

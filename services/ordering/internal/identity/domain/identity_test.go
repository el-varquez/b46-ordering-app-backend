package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	got, err := NormalizeEmail("  Admin@Gmail.COM ")
	if err != nil {
		t.Fatalf("NormalizeEmail() error = %v", err)
	}
	if got != "admin@gmail.com" {
		t.Fatalf("NormalizeEmail() = %q, want admin@gmail.com", got)
	}
}

func TestNormalizeEmailRejectsMalformedValue(t *testing.T) {
	_, err := NormalizeEmail("not-an-email")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NormalizeEmail() error = %v, want ErrInvalidInput", err)
	}
}

func TestValidatePasswordCountsCharacters(t *testing.T) {
	if err := ValidatePassword("abcdefgh"); err != nil {
		t.Fatalf("ValidatePassword() error = %v", err)
	}
	if err := ValidatePassword("abcdefg"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("short password error = %v, want ErrInvalidInput", err)
	}
	if err := ValidatePassword(strings.Repeat("界", 8)); err != nil {
		t.Fatalf("eight-character Unicode password error = %v", err)
	}
	if err := ValidatePassword(strings.Repeat("界", 129)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("long password error = %v, want ErrInvalidInput", err)
	}
}

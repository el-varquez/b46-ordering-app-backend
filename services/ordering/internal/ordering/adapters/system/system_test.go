package system

import (
	"regexp"
	"testing"
)

func TestIDsGenerateDistinctVersion4UUIDs(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	first, err := (IDs{}).New()
	if err != nil {
		t.Fatalf("first New() error = %v", err)
	}
	second, err := (IDs{}).New()
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}
	if !pattern.MatchString(first) || !pattern.MatchString(second) {
		t.Fatalf("generated values are not UUIDv4: %q, %q", first, second)
	}
	if first == second {
		t.Fatalf("generated duplicate UUID %q", first)
	}
}

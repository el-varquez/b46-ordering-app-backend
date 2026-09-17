package security

import "testing"

func TestRandomTokensAreUniqueAndHashesAreFixedLength(t *testing.T) {
	generator := RandomTokenGenerator{}
	first, err := generator.New()
	if err != nil {
		t.Fatalf("New() first error = %v", err)
	}
	second, err := generator.New()
	if err != nil {
		t.Fatalf("New() second error = %v", err)
	}
	if first == second {
		t.Fatal("two generated tokens were equal")
	}
	if len(HashToken(first)) != 64 {
		t.Fatalf("hash length = %d, want 64", len(HashToken(first)))
	}
}

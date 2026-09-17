package security

import "testing"

func TestArgon2idHashAndVerify(t *testing.T) {
	hasher := NewArgon2id(19*1024, 2, 1)
	encoded, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	matches, needsRehash, err := hasher.Verify("correct horse battery staple", encoded)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !matches || needsRehash {
		t.Fatalf("matches = %v, needsRehash = %v", matches, needsRehash)
	}

	matches, _, err = hasher.Verify("definitely the wrong password", encoded)
	if err != nil {
		t.Fatalf("Verify() wrong password error = %v", err)
	}
	if matches {
		t.Fatal("wrong password matched")
	}
}

func TestArgon2idReportsParameterUpgrade(t *testing.T) {
	oldHasher := NewArgon2id(19*1024, 2, 1)
	encoded, err := oldHasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	newHasher := NewArgon2id(32*1024, 3, 1)
	matches, needsRehash, err := newHasher.Verify("correct horse battery staple", encoded)
	if err != nil || !matches || !needsRehash {
		t.Fatalf("Verify() = (%v, %v, %v), want (true, true, nil)", matches, needsRehash, err)
	}
}

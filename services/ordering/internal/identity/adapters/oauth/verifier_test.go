package oauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

func TestVerifyGoogleAndAppleTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	keys := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}
	registry := newWithKeySets(keys, keys, []string{"google-client"}, []string{"apple-client"})
	nonce := "one-time-nonce"
	nonceHash := sha256.Sum256([]byte(nonce))

	tests := []struct {
		name              string
		provider          domain.Provider
		claims            map[string]any
		wantErr           bool
		wantEmailVerified bool
	}{
		{
			name:              "valid Google",
			provider:          domain.ProviderGoogle,
			claims:            validClaims(googleIssuer, "google-client", nonce),
			wantEmailVerified: true,
		},
		{
			name:              "valid Google alternate issuer",
			provider:          domain.ProviderGoogle,
			claims:            validClaims("accounts.google.com", "google-client", nonce),
			wantEmailVerified: true,
		},
		{
			name:              "valid Apple",
			provider:          domain.ProviderApple,
			claims:            validClaims(appleIssuer, "apple-client", nonce),
			wantEmailVerified: true,
		},
		{
			name:     "wrong audience",
			provider: domain.ProviderGoogle,
			claims:   validClaims(googleIssuer, "different-client", nonce),
			wantErr:  true,
		},
		{
			name:     "wrong issuer",
			provider: domain.ProviderGoogle,
			claims:   validClaims("https://attacker.example", "google-client", nonce),
			wantErr:  true,
		},
		{
			name:     "expired",
			provider: domain.ProviderGoogle,
			claims: func() map[string]any {
				value := validClaims(googleIssuer, "google-client", nonce)
				value["exp"] = time.Now().Add(-time.Minute).Unix()
				return value
			}(),
			wantErr: true,
		},
		{
			name:     "wrong nonce",
			provider: domain.ProviderGoogle,
			claims:   validClaims(googleIssuer, "google-client", "wrong"),
			wantErr:  true,
		},
		{
			name:     "missing nonce",
			provider: domain.ProviderGoogle,
			claims: func() map[string]any {
				value := validClaims(googleIssuer, "google-client", nonce)
				delete(value, "nonce")
				return value
			}(),
			wantErr: true,
		},
		{
			name:     "missing subject",
			provider: domain.ProviderGoogle,
			claims: func() map[string]any {
				value := validClaims(googleIssuer, "google-client", nonce)
				delete(value, "sub")
				return value
			}(),
			wantErr: true,
		},
		{
			name:     "unverified email is preserved as false",
			provider: domain.ProviderGoogle,
			claims: func() map[string]any {
				value := validClaims(googleIssuer, "google-client", nonce)
				value["email_verified"] = false
				return value
			}(),
		},
		{
			name:     "malformed verified email",
			provider: domain.ProviderGoogle,
			claims: func() map[string]any {
				value := validClaims(googleIssuer, "google-client", nonce)
				value["email_verified"] = "not-a-boolean"
				return value
			}(),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := signToken(t, key, test.claims)
			got, err := registry.Verify(
				context.Background(), test.provider, raw,
				hex.EncodeToString(nonceHash[:]),
			)
			if test.wantErr && err == nil {
				t.Fatal("Verify() error = nil, want rejection")
			}
			if !test.wantErr {
				if err != nil {
					t.Fatalf("Verify() error = %v", err)
				}
				if got.Subject != "provider-user-123" || got.EmailVerified != test.wantEmailVerified {
					t.Fatalf("Verify() = %#v, want subject and email verification state", got)
				}
			}
		})
	}
}

func TestVerifyRejectsUnknownSigningKey(t *testing.T) {
	trustedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate trusted RSA key: %v", err)
	}
	untrustedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate untrusted RSA key: %v", err)
	}
	keys := &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&trustedKey.PublicKey}}
	registry := newWithKeySets(keys, keys, []string{"google-client"}, []string{"apple-client"})
	nonce := "one-time-nonce"
	nonceHash := sha256.Sum256([]byte(nonce))

	_, err = registry.Verify(
		context.Background(),
		domain.ProviderGoogle,
		signToken(t, untrustedKey, validClaims(googleIssuer, "google-client", nonce)),
		hex.EncodeToString(nonceHash[:]),
	)
	if err == nil {
		t.Fatal("Verify() error = nil, want unknown signing key rejection")
	}
}

func validClaims(issuer, audience, nonce string) map[string]any {
	now := time.Now().UTC()
	return map[string]any{
		"iss":            issuer,
		"aud":            audience,
		"sub":            "provider-user-123",
		"iat":            now.Add(-time.Minute).Unix(),
		"exp":            now.Add(5 * time.Minute).Unix(),
		"nonce":          nonce,
		"email":          "customer@example.com",
		"email_verified": true,
		"name":           "Test Customer",
	}
}

func signToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key"})
	if err != nil {
		t.Fatalf("marshal JWT header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal JWT claims: %v", err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	signingInput := encode(header) + "." + encode(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signingInput + "." + encode(signature)
}

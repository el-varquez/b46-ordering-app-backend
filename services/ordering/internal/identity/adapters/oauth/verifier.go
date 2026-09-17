package oauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/ports"
)

const (
	googleIssuer = "https://accounts.google.com"
	googleJWKS   = "https://www.googleapis.com/oauth2/v3/certs"
	appleIssuer  = "https://appleid.apple.com"
	appleJWKS    = "https://appleid.apple.com/auth/keys"
)

type Registry struct {
	providers map[domain.Provider]providerVerifier
}

type providerVerifier struct {
	provider       domain.Provider
	allowedIssuers map[string]struct{}
	verifiers      []*oidc.IDTokenVerifier
}

var _ ports.OAuthVerifier = (*Registry)(nil)

func New(ctx context.Context, client *http.Client, googleClientIDs, appleClientIDs []string) *Registry {
	providerContext := oidc.ClientContext(ctx, client)
	googleKeys := oidc.NewRemoteKeySet(providerContext, googleJWKS)
	appleKeys := oidc.NewRemoteKeySet(providerContext, appleJWKS)
	return newWithKeySets(googleKeys, appleKeys, googleClientIDs, appleClientIDs)
}

func newWithKeySets(
	googleKeys, appleKeys oidc.KeySet,
	googleClientIDs, appleClientIDs []string,
) *Registry {
	return &Registry{providers: map[domain.Provider]providerVerifier{
		domain.ProviderGoogle: newProviderVerifier(
			domain.ProviderGoogle,
			googleIssuer,
			googleKeys,
			googleClientIDs,
			[]string{googleIssuer, "accounts.google.com"},
		),
		domain.ProviderApple: newProviderVerifier(
			domain.ProviderApple,
			appleIssuer,
			appleKeys,
			appleClientIDs,
			[]string{appleIssuer},
		),
	}}
}

func newProviderVerifier(
	provider domain.Provider,
	issuer string,
	keys oidc.KeySet,
	audiences []string,
	allowedIssuers []string,
) providerVerifier {
	value := providerVerifier{
		provider:       provider,
		allowedIssuers: make(map[string]struct{}, len(allowedIssuers)),
	}
	for _, allowed := range allowedIssuers {
		value.allowedIssuers[allowed] = struct{}{}
	}
	for _, audience := range audiences {
		value.verifiers = append(value.verifiers, oidc.NewVerifier(issuer, keys, &oidc.Config{
			ClientID:             audience,
			SupportedSigningAlgs: []string{oidc.RS256},
			SkipIssuerCheck:      true,
		}))
	}
	return value
}

func (registry *Registry) Verify(
	ctx context.Context,
	provider domain.Provider,
	rawToken, expectedNonceHash string,
) (domain.VerifiedIdentity, error) {
	configured, ok := registry.providers[provider]
	if !ok || len(configured.verifiers) == 0 || strings.TrimSpace(rawToken) == "" {
		return domain.VerifiedIdentity{}, domain.ErrInvalidOAuthToken
	}

	var verifiedToken *oidc.IDToken
	for _, verifier := range configured.verifiers {
		candidate, err := verifier.Verify(ctx, rawToken)
		if err == nil {
			verifiedToken = candidate
			break
		}
	}
	if verifiedToken == nil {
		return domain.VerifiedIdentity{}, domain.ErrInvalidOAuthToken
	}
	if _, allowed := configured.allowedIssuers[verifiedToken.Issuer]; !allowed {
		return domain.VerifiedIdentity{}, domain.ErrInvalidOAuthToken
	}
	if strings.TrimSpace(verifiedToken.Subject) == "" || !nonceMatches(verifiedToken.Nonce, expectedNonceHash) {
		return domain.VerifiedIdentity{}, domain.ErrInvalidOAuthToken
	}

	var claims struct {
		Email         string       `json:"email"`
		EmailVerified verifiedBool `json:"email_verified"`
		Name          string       `json:"name"`
	}
	if err := verifiedToken.Claims(&claims); err != nil {
		return domain.VerifiedIdentity{}, domain.ErrInvalidOAuthToken
	}
	return domain.VerifiedIdentity{
		Provider:      provider,
		Subject:       verifiedToken.Subject,
		Email:         claims.Email,
		EmailVerified: bool(claims.EmailVerified),
		SuggestedName: claims.Name,
	}, nil
}

func nonceMatches(actual, expectedHash string) bool {
	digest := sha256.Sum256([]byte(actual))
	actualHash := hex.EncodeToString(digest[:])
	return subtle.ConstantTimeCompare([]byte(actualHash), []byte(expectedHash)) == 1
}

type verifiedBool bool

func (value *verifiedBool) UnmarshalJSON(raw []byte) error {
	var boolean bool
	if err := json.Unmarshal(raw, &boolean); err == nil {
		*value = verifiedBool(boolean)
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return errors.New("email_verified must be a boolean")
	}
	switch {
	case bytes.EqualFold([]byte(text), []byte("true")):
		*value = true
		return nil
	case bytes.EqualFold([]byte(text), []byte("false")):
		*value = false
		return nil
	default:
		return errors.New("email_verified has an invalid value")
	}
}

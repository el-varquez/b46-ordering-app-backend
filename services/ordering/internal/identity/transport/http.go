package transport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type Responder interface {
	Success(http.ResponseWriter, *http.Request, int, any)
	Error(http.ResponseWriter, *http.Request, int, string, string)
}

type Handler struct {
	identity  *application.Service
	responses Responder
}

func New(identity *application.Service, responses Responder) *Handler {
	return &Handler{identity: identity, responses: responses}
}

func (handler *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/password/login", handler.passwordLogin)
	mux.HandleFunc("POST /v1/auth/oauth/intents", handler.oauthLoginIntent)
	mux.HandleFunc("POST /v1/auth/oauth/login", handler.oauthLogin)
	mux.HandleFunc("POST /v1/auth/refresh", handler.refresh)
	mux.HandleFunc("POST /v1/auth/logout", handler.logout)
	mux.HandleFunc("GET /v1/me", handler.authenticated(handler.me))
	mux.HandleFunc("POST /v1/me/oauth-link-intents", handler.authenticated(handler.oauthLinkIntent))
	mux.HandleFunc("POST /v1/me/oauth-identities", handler.authenticated(handler.oauthLink))
}

type passwordLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (handler *Handler) passwordLogin(writer http.ResponseWriter, request *http.Request) {
	var body passwordLoginRequest
	if err := decodeJSON(request, &body); err != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	result, err := handler.identity.PasswordLogin(request.Context(), body.Email, body.Password)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, sessionResponse(result))
}

type providerRequest struct {
	Provider domain.Provider `json:"provider"`
}

func (handler *Handler) oauthLoginIntent(writer http.ResponseWriter, request *http.Request) {
	var body providerRequest
	if err := decodeJSON(request, &body); err != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	result, err := handler.identity.BeginOAuthLogin(request.Context(), body.Provider)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusCreated, result)
}

type oauthCompleteRequest struct {
	IntentID      string `json:"intent_id"`
	IdentityToken string `json:"identity_token"`
}

func (handler *Handler) oauthLogin(writer http.ResponseWriter, request *http.Request) {
	var body oauthCompleteRequest
	if err := decodeJSON(request, &body); err != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	result, err := handler.identity.OAuthLogin(request.Context(), body.IntentID, body.IdentityToken)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, sessionResponse(result))
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (handler *Handler) refresh(writer http.ResponseWriter, request *http.Request) {
	var body refreshRequest
	if err := decodeJSON(request, &body); err != nil {
		handler.fail(writer, request, domain.ErrUnauthenticated)
		return
	}
	result, err := handler.identity.Refresh(request.Context(), body.RefreshToken)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, sessionResponse(result))
}

type protectedHandler func(http.ResponseWriter, *http.Request, domain.Principal)

type AuthorizedHandler = func(http.ResponseWriter, *http.Request, domain.Principal)

func (handler *Handler) RequireRole(role domain.Role, next AuthorizedHandler) http.HandlerFunc {
	return handler.authenticated(func(
		writer http.ResponseWriter,
		request *http.Request,
		principal domain.Principal,
	) {
		if err := application.Authorize(principal, role); err != nil {
			handler.fail(writer, request, err)
			return
		}
		next(writer, request, principal)
	})
}

func (handler *Handler) authenticated(next protectedHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		token, ok := bearerToken(request.Header.Get("Authorization"))
		if !ok {
			handler.fail(writer, request, domain.ErrUnauthenticated)
			return
		}
		principal, err := handler.identity.Authenticate(request.Context(), token)
		if err != nil {
			handler.fail(writer, request, err)
			return
		}
		next(writer, request, principal)
	}
}

func (handler *Handler) logout(writer http.ResponseWriter, request *http.Request) {
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok {
		handler.fail(writer, request, domain.ErrUnauthenticated)
		return
	}
	if err := handler.identity.Logout(request.Context(), token); err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (handler *Handler) me(writer http.ResponseWriter, request *http.Request, principal domain.Principal) {
	view, err := handler.identity.Me(request.Context(), principal)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, userViewResponse(view))
}

func (handler *Handler) oauthLinkIntent(writer http.ResponseWriter, request *http.Request, principal domain.Principal) {
	var body providerRequest
	if err := decodeJSON(request, &body); err != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	result, err := handler.identity.BeginOAuthLink(request.Context(), principal, body.Provider)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusCreated, result)
}

func (handler *Handler) oauthLink(writer http.ResponseWriter, request *http.Request, principal domain.Principal) {
	var body oauthCompleteRequest
	if err := decodeJSON(request, &body); err != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	if err := handler.identity.LinkOAuth(request.Context(), principal, body.IntentID, body.IdentityToken); err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, map[string]string{"status": "linked"})
}

func (handler *Handler) fail(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		handler.responses.Error(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
	case errors.Is(err, domain.ErrInvalidCredentials):
		handler.responses.Error(writer, request, http.StatusUnauthorized, "INVALID_CREDENTIALS", "The email or password is incorrect.")
	case errors.Is(err, domain.ErrInvalidOAuthToken):
		handler.responses.Error(writer, request, http.StatusUnauthorized, "INVALID_OAUTH_CREDENTIAL", "The provider credential is invalid.")
	case errors.Is(err, domain.ErrInvalidOAuthIntent):
		handler.responses.Error(writer, request, http.StatusBadRequest, "INVALID_OAUTH_INTENT", "The sign-in request is invalid or expired.")
	case errors.Is(err, domain.ErrIdentityLinkRequired):
		handler.responses.Error(writer, request, http.StatusConflict, "IDENTITY_LINK_REQUIRED", "Sign in to the existing account before connecting this provider.")
	case errors.Is(err, domain.ErrIdentityLinked):
		handler.responses.Error(writer, request, http.StatusConflict, "IDENTITY_ALREADY_LINKED", "This provider identity is already connected.")
	case errors.Is(err, domain.ErrAccountDisabled):
		handler.responses.Error(writer, request, http.StatusForbidden, "ACCOUNT_DISABLED", "This account is disabled.")
	case errors.Is(err, domain.ErrUnauthenticated):
		handler.responses.Error(writer, request, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
	case errors.Is(err, domain.ErrForbidden):
		handler.responses.Error(writer, request, http.StatusForbidden, "FORBIDDEN", "This account cannot perform that action.")
	default:
		handler.responses.Error(writer, request, http.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred.")
	}
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func sessionResponse(value domain.SessionTokens) map[string]any {
	return map[string]any{
		"access_token":             value.AccessToken,
		"access_token_expires_at":  value.AccessTokenExpiresAt,
		"refresh_token":            value.RefreshToken,
		"refresh_token_expires_at": value.RefreshTokenExpiresAt,
		"user":                     userResponse(value.User),
	}
}

func userViewResponse(value domain.UserView) map[string]any {
	result := userResponse(value.User)
	result["connected_providers"] = value.Providers
	return result
}

func userResponse(value domain.User) map[string]any {
	return map[string]any{
		"user_id": value.ID,
		"name":    value.Name,
		"email":   value.NormalizedEmail,
		"role":    value.Role,
		"status":  value.Status,
	}
}

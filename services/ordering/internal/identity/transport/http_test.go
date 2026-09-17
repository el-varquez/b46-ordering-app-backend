package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type recordedResponse struct {
	status int
	code   string
}

func (response *recordedResponse) Success(
	http.ResponseWriter,
	*http.Request,
	int,
	any,
) {
	response.status = http.StatusOK
}

func (response *recordedResponse) Error(
	_ http.ResponseWriter,
	_ *http.Request,
	status int,
	code, _ string,
) {
	response.status = status
	response.code = code
}

func TestProtectedRouteRejectsMalformedBearerHeader(t *testing.T) {
	for _, header := range []string{"", "Basic credential", "Bearer", "Bearer one two"} {
		t.Run(header, func(t *testing.T) {
			responses := &recordedResponse{}
			handler := New(nil, responses)
			mux := http.NewServeMux()
			handler.Register(mux)
			request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			request.Header.Set("Authorization", header)

			mux.ServeHTTP(httptest.NewRecorder(), request)

			if responses.status != http.StatusUnauthorized || responses.code != "UNAUTHENTICATED" {
				t.Fatalf("response = (%d, %s), want (401, UNAUTHENTICATED)", responses.status, responses.code)
			}
		})
	}
}

func TestPasswordLoginRejectsUnknownJSONFields(t *testing.T) {
	responses := &recordedResponse{}
	handler := New(nil, responses)
	mux := http.NewServeMux()
	handler.Register(mux)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/auth/password/login",
		strings.NewReader(`{"email":"admin@gmail.com","password":"long-enough-password","role":"ADMIN"}`),
	)

	mux.ServeHTTP(httptest.NewRecorder(), request)

	if responses.status != http.StatusBadRequest || responses.code != "INVALID_REQUEST" {
		t.Fatalf("response = (%d, %s), want (400, INVALID_REQUEST)", responses.status, responses.code)
	}
}

func TestLogoutIsRepeatableAtHTTPBoundary(t *testing.T) {
	store := &logoutStore{}
	identity, err := application.New(
		store,
		stubHasher{},
		stubTokens{},
		stubClock{},
		nil,
		application.Config{
			AccessTokenLifetime:  time.Minute,
			RefreshTokenLifetime: time.Hour,
			OAuthIntentLifetime:  time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	responses := &recordedResponse{}
	handler := New(identity, responses)
	mux := http.NewServeMux()
	handler.Register(mux)

	for attempt := 1; attempt <= 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
		request.Header.Set("Authorization", "Bearer access-token")
		mux.ServeHTTP(httptest.NewRecorder(), request)

		if responses.status != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want 200", attempt, responses.status)
		}
	}
}

func TestRequireRoleEnforcesExactActiveRole(t *testing.T) {
	tests := []struct {
		name       string
		principal  domain.Principal
		required   domain.Role
		wantStatus int
		wantCode   string
		wantCalled bool
	}{
		{
			name: "matching customer", principal: domain.Principal{UserID: "user", Role: domain.RoleCustomer, Status: domain.AccountActive},
			required: domain.RoleCustomer, wantStatus: http.StatusOK, wantCalled: true,
		},
		{
			name: "admin is not cashier", principal: domain.Principal{UserID: "user", Role: domain.RoleAdmin, Status: domain.AccountActive},
			required: domain.RoleCashier, wantStatus: http.StatusForbidden, wantCode: "FORBIDDEN",
		},
		{
			name: "disabled cashier", principal: domain.Principal{UserID: "user", Role: domain.RoleCashier, Status: domain.AccountDisabled},
			required: domain.RoleCashier, wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHENTICATED",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &logoutStore{principal: test.principal}
			identity, err := application.New(
				store, stubHasher{}, stubTokens{}, stubClock{}, nil,
				application.Config{AccessTokenLifetime: time.Minute, RefreshTokenLifetime: time.Hour, OAuthIntentLifetime: time.Minute},
			)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			responses := &recordedResponse{}
			handler := New(identity, responses)
			called := false
			protected := handler.RequireRole(test.required, func(http.ResponseWriter, *http.Request, domain.Principal) {
				called = true
				responses.status = http.StatusOK
			})
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer access-token")

			protected(httptest.NewRecorder(), request)

			if responses.status != test.wantStatus || responses.code != test.wantCode || called != test.wantCalled {
				t.Fatalf("response = (%d, %q, called=%t), want (%d, %q, called=%t)",
					responses.status, responses.code, called, test.wantStatus, test.wantCode, test.wantCalled)
			}
		})
	}
}

type logoutStore struct {
	revoked   bool
	principal domain.Principal
}

func (*logoutStore) FindPassword(context.Context, string) (domain.PasswordRecord, error) {
	return domain.PasswordRecord{}, nil
}
func (*logoutStore) UpdatePasswordHash(context.Context, string, string) error { return nil }
func (*logoutStore) CreateSession(context.Context, domain.NewSession) (domain.User, error) {
	return domain.User{}, nil
}
func (store *logoutStore) AuthenticateAccess(context.Context, string, time.Time) (domain.Principal, error) {
	if store.revoked {
		return domain.Principal{}, domain.ErrUnauthenticated
	}
	if store.principal.UserID != "" {
		return store.principal, nil
	}
	return domain.Principal{
		UserID: "user-id", Role: domain.RoleAdmin,
		Status: domain.AccountActive, FamilyID: "family-id",
	}, nil
}
func (*logoutStore) RotateRefresh(context.Context, string, domain.NewSession, time.Time) (domain.User, error) {
	return domain.User{}, nil
}
func (store *logoutStore) RevokeFamily(context.Context, string, string, time.Time) error {
	store.revoked = true
	return nil
}
func (*logoutStore) CreateOAuthIntent(context.Context, domain.OAuthIntent) (domain.OAuthIntent, error) {
	return domain.OAuthIntent{}, nil
}
func (*logoutStore) FindOAuthIntent(context.Context, string, time.Time) (domain.OAuthIntent, error) {
	return domain.OAuthIntent{}, nil
}
func (*logoutStore) CompleteOAuthLogin(context.Context, string, domain.VerifiedIdentity, domain.NewSession, time.Time) (domain.User, error) {
	return domain.User{}, nil
}
func (*logoutStore) CompleteOAuthLink(context.Context, string, string, domain.VerifiedIdentity, time.Time) error {
	return nil
}
func (*logoutStore) UserView(context.Context, string) (domain.UserView, error) {
	return domain.UserView{}, nil
}
func (*logoutStore) BootstrapAdmin(context.Context, string, string, string, time.Time) (domain.User, error) {
	return domain.User{}, nil
}
func (*logoutStore) DisableUser(context.Context, string, string, time.Time) error { return nil }

type stubHasher struct{}

func (stubHasher) Hash(string) (string, error)               { return "hash", nil }
func (stubHasher) Verify(string, string) (bool, bool, error) { return true, false, nil }

type stubTokens struct{}

func (stubTokens) New() (string, error) { return "token", nil }

type stubClock struct{}

func (stubClock) Now() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

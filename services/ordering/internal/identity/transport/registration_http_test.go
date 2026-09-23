package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type registrationRecordedResponse struct {
	status  int
	code    string
	message string
}

func (response *registrationRecordedResponse) Success(http.ResponseWriter, *http.Request, int, any) {}
func (response *registrationRecordedResponse) Error(_ http.ResponseWriter, _ *http.Request, status int, code, message string) {
	response.status, response.code, response.message = status, code, message
}

func TestRegistrationValidationResponseNamesTheRejectedField(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		err     error
		message string
	}{
		{"name", domain.ErrInvalidRegistrationName, "Enter your name (up to 120 characters)."},
		{"email", domain.ErrInvalidRegistrationEmail, "Enter a valid email address, such as name@example.com."},
		{"password", domain.ErrInvalidRegistrationPassword, "Use a password with 8 to 128 characters."},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			response := &registrationRecordedResponse{}
			handler := NewRegistration(nil, response)
			handler.fail(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/auth/password/registrations", nil), scenario.err)
			if response.status != http.StatusBadRequest || response.code != "INVALID_REQUEST" || response.message != scenario.message {
				t.Fatalf("response = (%d, %s, %q), want (400, INVALID_REQUEST, %q)", response.status, response.code, response.message, scenario.message)
			}
		})
	}
}

func TestRegistrationMalformedJSONKeepsGenericError(t *testing.T) {
	response := &registrationRecordedResponse{}
	handler := NewRegistration(nil, response)
	mux := http.NewServeMux()
	handler.Register(mux)
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/password/registrations", strings.NewReader(`{"name":"A","email":"a@example.com","password":"abcdefgh","role":"ADMIN"}`))
	mux.ServeHTTP(httptest.NewRecorder(), request)
	if response.status != http.StatusBadRequest || response.code != "INVALID_REQUEST" || response.message != "The request is invalid." {
		t.Fatalf("response = (%d, %s, %q), want a generic 400", response.status, response.code, response.message)
	}
}

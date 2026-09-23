package transport

import (
	"errors"
	"net"
	"net/http"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/application"
	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type RegistrationHandler struct {
	service   *application.RegistrationService
	responses Responder
}

func NewRegistration(service *application.RegistrationService, responses Responder) *RegistrationHandler {
	return &RegistrationHandler{service: service, responses: responses}
}

func (handler *RegistrationHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/password/registrations", handler.begin)
	mux.HandleFunc("POST /v1/auth/password/registrations/resend", handler.resend)
	mux.HandleFunc("POST /v1/auth/password/registrations/verify", handler.verify)
}

type beginRegistrationRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registrationCodeRequest struct {
	RegistrationID string `json:"registration_id"`
	Code           string `json:"code"`
}

type registrationIDRequest struct {
	RegistrationID string `json:"registration_id"`
}

func (handler *RegistrationHandler) begin(writer http.ResponseWriter, request *http.Request) {
	var body beginRegistrationRequest
	if decodeJSON(request, &body) != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	result, err := handler.service.Begin(request.Context(), body.Name, body.Email, body.Password, clientIP(request))
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusAccepted, result)
}

func (handler *RegistrationHandler) resend(writer http.ResponseWriter, request *http.Request) {
	var body registrationIDRequest
	if decodeJSON(request, &body) != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	result, err := handler.service.Resend(request.Context(), body.RegistrationID, clientIP(request))
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusAccepted, result)
}

func (handler *RegistrationHandler) verify(writer http.ResponseWriter, request *http.Request) {
	var body registrationCodeRequest
	if decodeJSON(request, &body) != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	result, err := handler.service.Verify(request.Context(), body.RegistrationID, body.Code)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, sessionResponse(result))
}

func (handler *RegistrationHandler) fail(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidRegistrationName):
		handler.responses.Error(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "Enter your name (up to 120 characters).")
	case errors.Is(err, domain.ErrInvalidRegistrationEmail):
		handler.responses.Error(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "Enter a valid email address, such as name@example.com.")
	case errors.Is(err, domain.ErrInvalidRegistrationPassword):
		handler.responses.Error(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "Use a password with 8 to 128 characters.")
	case errors.Is(err, domain.ErrInvalidInput):
		handler.responses.Error(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.")
	case errors.Is(err, domain.ErrInvalidRegistration):
		handler.responses.Error(writer, request, http.StatusBadRequest, "INVALID_REGISTRATION", "The code is invalid or expired. Request a new code.")
	case errors.Is(err, domain.ErrRegistrationRateLimited), errors.Is(err, domain.ErrRegistrationCooldown):
		writer.Header().Set("Retry-After", "60")
		handler.responses.Error(writer, request, http.StatusTooManyRequests, "REGISTRATION_RATE_LIMITED", "Please wait before trying again.")
	case errors.Is(err, domain.ErrEmailUnavailable):
		handler.responses.Error(writer, request, http.StatusServiceUnavailable, "EMAIL_UNAVAILABLE", "We could not send the email right now. Please try again.")
	default:
		handler.responses.Error(writer, request, http.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred.")
	}
}

func clientIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}

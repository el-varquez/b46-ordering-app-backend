package transport

import (
	"errors"
	"net/http"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

// AdminCashierRegistrationHandler exposes the email challenge only behind
// admin authentication. Public customer registration cannot request a role.
type AdminCashierRegistrationHandler struct {
	identity     *Handler
	registration *RegistrationHandler
}

func NewAdminCashierRegistration(identity *Handler, registration *RegistrationHandler) *AdminCashierRegistrationHandler {
	return &AdminCashierRegistrationHandler{identity: identity, registration: registration}
}

func (handler *AdminCashierRegistrationHandler) Register(mux *http.ServeMux) {
	protect := handler.identity.RequireRole
	mux.HandleFunc("POST /v1/admin/cashiers/registrations", protect(domain.RoleAdmin, handler.begin))
	mux.HandleFunc("POST /v1/admin/cashiers/registrations/resend", protect(domain.RoleAdmin, handler.resend))
	mux.HandleFunc("POST /v1/admin/cashiers/registrations/verify", protect(domain.RoleAdmin, handler.verify))
}

func (handler *AdminCashierRegistrationHandler) begin(writer http.ResponseWriter, request *http.Request, actor domain.Principal) {
	var body beginRegistrationRequest
	if decodeJSON(request, &body) != nil {
		handler.identity.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	result, err := handler.registration.service.BeginCashier(request.Context(), actor,
		body.Name, body.Email, body.Password, clientIP(request))
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.identity.responses.Success(writer, request, http.StatusAccepted, result)
}

func (handler *AdminCashierRegistrationHandler) resend(writer http.ResponseWriter, request *http.Request, actor domain.Principal) {
	var body registrationIDRequest
	if decodeJSON(request, &body) != nil {
		handler.identity.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	result, err := handler.registration.service.ResendCashier(request.Context(), actor,
		body.RegistrationID, clientIP(request))
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.identity.responses.Success(writer, request, http.StatusAccepted, result)
}

func (handler *AdminCashierRegistrationHandler) verify(writer http.ResponseWriter, request *http.Request, actor domain.Principal) {
	var body registrationCodeRequest
	if decodeJSON(request, &body) != nil {
		handler.identity.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	view, err := handler.registration.service.VerifyCashier(request.Context(), actor,
		body.RegistrationID, body.Code)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.identity.responses.Success(writer, request, http.StatusOK, cashierResponse(view))
}

func (handler *AdminCashierRegistrationHandler) fail(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, domain.ErrEmailInUse) || errors.Is(err, domain.ErrForbidden) ||
		errors.Is(err, domain.ErrUnauthenticated) || errors.Is(err, domain.ErrPasswordChangeRequired) {
		handler.identity.fail(writer, request, err)
		return
	}
	handler.registration.fail(writer, request, err)
}

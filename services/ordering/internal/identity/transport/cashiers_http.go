package transport

import (
	"net/http"

	"github.com/el-varquez/b46-ordering-app-backend/services/ordering/internal/identity/domain"
)

type cashierStatusRequest struct {
	Status domain.AccountStatus `json:"status"`
}

type resetCashierPasswordRequest struct {
	TemporaryPassword string `json:"temporary_password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (handler *Handler) listCashiers(writer http.ResponseWriter, request *http.Request, actor domain.Principal) {
	status := domain.AccountStatus(request.URL.Query().Get("status"))
	views, err := handler.identity.ListCashiers(request.Context(), actor, status)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	cashiers := make([]any, 0, len(views))
	for _, view := range views {
		cashiers = append(cashiers, cashierResponse(view))
	}
	handler.responses.Success(writer, request, http.StatusOK, map[string]any{"cashiers": cashiers})
}

func (handler *Handler) cashier(writer http.ResponseWriter, request *http.Request, actor domain.Principal) {
	view, err := handler.identity.Cashier(request.Context(), actor, request.PathValue("cashier_id"))
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, cashierResponse(view))
}

func (handler *Handler) setCashierStatus(writer http.ResponseWriter, request *http.Request, actor domain.Principal) {
	var body cashierStatusRequest
	if decodeJSON(request, &body) != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	view, err := handler.identity.SetCashierStatus(request.Context(), actor, request.PathValue("cashier_id"), body.Status)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, cashierResponse(view))
}

func (handler *Handler) resetCashierPassword(writer http.ResponseWriter, request *http.Request, actor domain.Principal) {
	var body resetCashierPasswordRequest
	if decodeJSON(request, &body) != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	view, err := handler.identity.ResetCashierPassword(request.Context(), actor, request.PathValue("cashier_id"), body.TemporaryPassword)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, cashierResponse(view))
}

func (handler *Handler) changePassword(writer http.ResponseWriter, request *http.Request, actor domain.Principal) {
	var body changePasswordRequest
	if decodeJSON(request, &body) != nil {
		handler.fail(writer, request, domain.ErrInvalidInput)
		return
	}
	if err := handler.identity.ChangeOwnPassword(request.Context(), actor, body.CurrentPassword, body.NewPassword); err != nil {
		handler.fail(writer, request, err)
		return
	}
	view, err := handler.identity.Me(request.Context(), actor)
	if err != nil {
		handler.fail(writer, request, err)
		return
	}
	handler.responses.Success(writer, request, http.StatusOK, userViewResponse(view))
}

func cashierResponse(view domain.UserView) map[string]any {
	result := userViewResponse(view)
	result["created"] = view.Created
	result["email_verified"] = view.EmailVerified
	audit := make([]any, 0, len(view.Audit))
	for _, entry := range view.Audit {
		audit = append(audit, map[string]any{
			"action":        entry.Action,
			"actor_user_id": entry.ActorUserID,
			"result":        entry.Result,
			"occurred_at":   entry.OccurredAt,
		})
	}
	result["audit"] = audit
	return result
}

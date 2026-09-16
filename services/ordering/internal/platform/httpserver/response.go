package httpserver

import (
	"encoding/json"
	"net/http"
)

type responseMeta struct {
	RequestID string `json:"request_id"`
}

type successEnvelope struct {
	Data any          `json:"data"`
	Meta responseMeta `json:"meta"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error errorBody    `json:"error"`
	Meta  responseMeta `json:"meta"`
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}

func writeSuccess(writer http.ResponseWriter, request *http.Request, status int, data any) {
	writeJSON(writer, status, successEnvelope{
		Data: data,
		Meta: responseMeta{RequestID: CorrelationID(request.Context())},
	})
}

func writeError(writer http.ResponseWriter, request *http.Request, status int, code, message string) {
	writeJSON(writer, status, errorEnvelope{
		Error: errorBody{Code: code, Message: message},
		Meta:  responseMeta{RequestID: CorrelationID(request.Context())},
	})
}

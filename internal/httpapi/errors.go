package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/javded-itres/open-comfy/internal/workflow"
)

type apiErrorBody struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
	Param   any    `json:"param"`
}

func writeError(w http.ResponseWriter, status int, typ, code, msg, param string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var p any
	if param != "" {
		p = param
	}
	_ = json.NewEncoder(w).Encode(apiErrorBody{Error: apiError{
		Message: msg, Type: typ, Code: code, Param: p,
	}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func mapErr(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var we *workflow.Error
	if errors.As(err, &we) {
		writeError(w, 400, "invalid_request_error", we.Code, we.Message, we.Param)
		return
	}
	writeError(w, 500, "api_error", "internal_error", err.Error(), "")
}

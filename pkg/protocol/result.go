package protocol

import (
	"encoding/json"
	"net/http"
)

const (
	CodeOK                     = 0
	CodeParameterMissing       = 10000
	CodeParameterValidateError = 10001
	CodeMetadataIllegal        = 100002
	CodeImportedDataEmpty      = 100005
	CodeNoSelectedConfig       = 20001
	CodeDataEmpty              = 20002
	CodeAccessDenied           = 403
	CodeConflict               = 409
	CodeNotFound               = 404
	CodeNotImplemented         = 501
)

type Result struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

type Error struct {
	Code    int
	Message string
	Data    any
}

func WriteResult(w http.ResponseWriter, status int, data any) {
	WriteEnvelope(w, status, CodeOK, "success", data)
}

func WriteError(w http.ResponseWriter, status int, err Error) {
	if err.Message == "" {
		err.Message = http.StatusText(status)
	}
	if err.Code == 0 {
		err.Code = status
	}
	WriteEnvelope(w, status, err.Code, err.Message, err.Data)
}

func WriteEnvelope(w http.ResponseWriter, status, code int, message string, data any) {
	writeJSON(w, status, Result{
		Code:    code,
		Message: message,
		Data:    data,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

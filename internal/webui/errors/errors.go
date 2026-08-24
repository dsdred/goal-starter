package errors

import (
	"encoding/json"
	"net/http"
)

// Code represents an API error code.
type Code string

const (
	CodeBadRequest     Code = "bad_request"
	CodeUnauthorized   Code = "unauthorized"
	CodeForbidden      Code = "forbidden"
	CodeNotFound       Code = "not_found"
	CodeConflict       Code = "conflict"
	CodeRateLimited    Code = "rate_limited"
	CodeInvalidPort    Code = "invalid_port"
	CodeInvalidHost    Code = "invalid_host"
	CodeInvalidAddress Code = "invalid_address"
	CodeInvalidRuntime Code = "invalid_runtime"
	CodeInvalidModel   Code = "invalid_model"
	CodeInternalServer Code = "internal_server_error"
)

// APIError represents a structured API error.
type APIError struct {
	Code    Code     `json:"error_code"`
	Message string   `json:"error"`
	Details []string `json:"details,omitempty"`
}

func (e *APIError) Error() string {
	return e.Message
}

// NewAPIError creates a new APIError.
func NewAPIError(code Code, message string, details ...string) *APIError {
	return &APIError{
		Code:    code,
		Message: message,
		Details: details,
	}
}

// WriteJSON writes the APIError as a JSON HTTP response.
func (e *APIError) WriteJSON(w http.ResponseWriter, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(e)
}

// ErrorResponse is the JSON wrapper for API errors.
type ErrorResponse struct {
	Error *APIError `json:"error"`
}

// WriteError writes a structured error response.
func WriteError(w http.ResponseWriter, statusCode int, apiErr *APIError) {
	resp := &ErrorResponse{Error: apiErr}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(resp)
}

// Predefined errors.

var (
	ErrBadRequest     = NewAPIError(CodeBadRequest, "invalid request")
	ErrValidation     = NewAPIError(CodeBadRequest, "validation failed")
	ErrUnauthorized   = NewAPIError(CodeUnauthorized, "unauthorized")
	ErrForbidden      = NewAPIError(CodeForbidden, "forbidden")
	ErrNotFound       = NewAPIError(CodeNotFound, "resource not found")
	ErrConflict       = NewAPIError(CodeConflict, "resource conflict")
	ErrInternalServer = NewAPIError(CodeInternalServer, "internal server error")
)

// ErrInvalidPort creates a port validation error.
func ErrInvalidPortDetail(msg string) *APIError {
	return NewAPIError(CodeInvalidPort, "invalid port: "+msg)
}

// ErrInvalidHost creates a host validation error.
func ErrInvalidHostDetail(msg string) *APIError {
	return NewAPIError(CodeInvalidHost, "invalid host: "+msg)
}

// ErrInvalidAddress creates an address validation error.
func ErrInvalidAddressDetail(hostMsg, portMsg string) *APIError {
	return NewAPIError(CodeInvalidAddress, "invalid address", "host: "+hostMsg, "port: "+portMsg)
}

// ErrRuntimeNotFound creates a runtime not found error.
func ErrRuntimeNotFound(id string) *APIError {
	return NewAPIError(CodeInvalidRuntime, "runtime not found: "+id)
}

// ErrModelNotFound creates a model not found error.
func ErrModelNotFound(id string) *APIError {
	return NewAPIError(CodeInvalidModel, "model not found: "+id)
}

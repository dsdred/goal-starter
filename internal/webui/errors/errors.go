package errors

// Code represents an API error code.
type Code string

const (
	CodeBadRequest         Code = "bad_request"
	CodeUnauthorized       Code = "unauthorized"
	CodeForbidden          Code = "forbidden"
	CodeNotFound           Code = "not_found"
	CodeGone               Code = "gone"
	CodeConflict           Code = "conflict"
	CodeServiceUnavailable Code = "service_unavailable"
	CodeRateLimited        Code = "rate_limited"
	CodeInvalidPort        Code = "invalid_port"
	CodeInvalidHost        Code = "invalid_host"
	CodeInvalidAddress     Code = "invalid_address"
	CodeInvalidRuntime     Code = "invalid_runtime"
	CodeInvalidModel       Code = "invalid_model"
	CodeInternalServer     Code = "internal_server_error"
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

// Predefined errors.

var (
	ErrValidation = NewAPIError(CodeBadRequest, "validation failed")
)

// ErrRuntimeNotFound creates a runtime not found error.
func ErrRuntimeNotFound(id string) *APIError {
	return NewAPIError(CodeInvalidRuntime, "runtime not found: "+id)
}

package errors

import (
	"testing"
)

func TestNewAPIError(t *testing.T) {
	err := NewAPIError(CodeBadRequest, "test message", "detail 1", "detail 2")
	if err.Code != CodeBadRequest {
		t.Errorf("expected CodeBadRequest, got %s", err.Code)
	}
	if err.Message != "test message" {
		t.Errorf("expected 'test message', got %s", err.Message)
	}
	if len(err.Details) != 2 {
		t.Errorf("expected 2 details, got %d", len(err.Details))
	}
}

func TestAPIError_Error(t *testing.T) {
	err := NewAPIError(CodeBadRequest, "test message")
	if err.Error() != "test message" {
		t.Errorf("expected 'test message', got %s", err.Error())
	}
}

func TestPredefinedErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      *APIError
		expected Code
	}{
		{"ErrValidation", ErrValidation, CodeBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code != tt.expected {
				t.Errorf("%s: expected %s, got %s", tt.name, tt.expected, tt.err.Code)
			}
		})
	}
}

func TestSpecificErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      *APIError
		wantCode Code
	}{
		{"ErrRuntimeNotFound", ErrRuntimeNotFound("id_456"), CodeInvalidRuntime},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code != tt.wantCode {
				t.Errorf("expected %s, got %s", tt.wantCode, tt.err.Code)
			}
		})
	}
}

package errors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestAPIError_WriteJSON(t *testing.T) {
	err := NewAPIError(CodeBadRequest, "test message")
	w := httptest.NewRecorder()
	err.WriteJSON(w, http.StatusBadRequest)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
	}

	// WriteJSON encodes APIError directly (not ErrorResponse wrapper).
	var body APIError
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Code != CodeBadRequest {
		t.Errorf("expected CodeBadRequest in response, got %s", body.Code)
	}
	if body.Message != "test message" {
		t.Errorf("expected 'test message' in response, got %s", body.Message)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusNotFound, ErrNotFound)

	resp := w.Result()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, resp.StatusCode)
	}

	var body ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Error == nil {
		t.Fatal("expected error in response body, got nil")
	}
}

func TestPredefinedErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      *APIError
		expected Code
	}{
		{"ErrBadRequest", ErrBadRequest, CodeBadRequest},
		{"ErrUnauthorized", ErrUnauthorized, CodeUnauthorized},
		{"ErrForbidden", ErrForbidden, CodeForbidden},
		{"ErrNotFound", ErrNotFound, CodeNotFound},
		{"ErrConflict", ErrConflict, CodeConflict},
		{"ErrInternalServer", ErrInternalServer, CodeInternalServer},
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
		{"ErrInvalidPortDetail", ErrInvalidPortDetail("out of range"), CodeInvalidPort},
		{"ErrInvalidHostDetail", ErrInvalidHostDetail("invalid format"), CodeInvalidHost},
		{"ErrInvalidAddressDetail", ErrInvalidAddressDetail("bad host", "bad port"), CodeInvalidAddress},
		{"ErrProfileNotFound", ErrProfileNotFound("id_123"), CodeInvalidProfile},
		{"ErrRuntimeNotFound", ErrRuntimeNotFound("id_456"), CodeInvalidRuntime},
		{"ErrModelNotFound", ErrModelNotFound("id_789"), CodeInvalidModel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code != tt.wantCode {
				t.Errorf("expected %s, got %s", tt.wantCode, tt.err.Code)
			}
		})
	}
}

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusWriterPreservesFlusher(t *testing.T) {
	recorder := httptest.NewRecorder()
	wrapper := &statusWriter{ResponseWriter: recorder, statusCode: http.StatusOK}
	var writer http.ResponseWriter = wrapper
	flusher, ok := writer.(http.Flusher)
	if !ok {
		t.Fatal("logging response writer does not preserve http.Flusher")
	}
	flusher.Flush()
	if !recorder.Flushed {
		t.Fatal("Flush was not delegated to the underlying writer")
	}
}

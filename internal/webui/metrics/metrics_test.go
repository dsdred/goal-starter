package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandler_ReturnsOK(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "goal_uptime_seconds") {
		t.Error("expected response to contain goal_uptime_seconds")
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestHandler_ContentType(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	contentType := rec.Header().Get("Content-Type")
	if contentType != "text/plain; charset=utf-8" {
		t.Errorf("expected Content-Type 'text/plain; charset=utf-8', got '%s'", contentType)
	}
}

func TestHandler_AllMetricsPresent(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()

	requiredMetrics := []string{
		"goal_uptime_seconds",
		"goal_http_requests_total",
		"goal_http_errors_total",
		"goal_models_started_total",
		"goal_models_stopped_total",
		"goal_models_failed_total",
		"goal_sessions_created_total",
		"goal_sessions_expired_total",
		"goal_go_memstats_alloc_bytes",
		"goal_go_memstats_total_alloc_bytes",
		"goal_go_memstats_sys_bytes",
		"goal_go_goroutines",
		"goal_go_numcpu",
	}

	for _, metric := range requiredMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("expected response to contain %s", metric)
		}
	}
}

func TestHandler_UptimeIncreases(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	time.Sleep(100 * time.Millisecond)

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	body1 := rec1.Body.String()
	body2 := rec2.Body.String()

	if body2 == body1 {
		t.Error("expected uptime to increase between requests")
	}
}

func TestRecordRequest(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	for i := 0; i < 5; i++ {
		m.RecordRequest()
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "goal_http_requests_total 5") {
		t.Error("expected goal_http_requests_total to be 5")
	}
}

func TestRecordError(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	for i := 0; i < 3; i++ {
		m.RecordError()
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "goal_http_errors_total 3") {
		t.Error("expected goal_http_errors_total to be 3")
	}
}

func TestRecordModelMetrics(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	m.RecordModelStart()
	m.RecordModelStart()
	m.RecordModelStop()
	m.RecordModelFailed()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()

	if !strings.Contains(body, "goal_models_started_total 2") {
		t.Error("expected goal_models_started_total to be 2")
	}
	if !strings.Contains(body, "goal_models_stopped_total 1") {
		t.Error("expected goal_models_stopped_total to be 1")
	}
	if !strings.Contains(body, "goal_models_failed_total 1") {
		t.Error("expected goal_models_failed_total to be 1")
	}
}

func TestRecordSessionMetrics(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	m.RecordSessionCreated()
	m.RecordSessionCreated()
	m.RecordSessionExpired()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()

	if !strings.Contains(body, "goal_sessions_created_total 2") {
		t.Error("expected goal_sessions_created_total to be 2")
	}
	if !strings.Contains(body, "goal_sessions_expired_total 1") {
		t.Error("expected goal_sessions_expired_total to be 1")
	}
}

func TestHandler_PrometheusFormat(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	body := rec.Body.String()

	if !strings.Contains(body, "# HELP goal_uptime_seconds") {
		t.Error("expected # HELP line for goal_uptime_seconds")
	}
	if !strings.Contains(body, "# TYPE goal_uptime_seconds counter") {
		t.Error("expected # TYPE line for goal_uptime_seconds")
	}
}

func TestHandler_NoRuntimePanics(t *testing.T) {
	m := NewManager()
	handler := m.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	_, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Errorf("expected to read body, got error: %v", err)
	}
}

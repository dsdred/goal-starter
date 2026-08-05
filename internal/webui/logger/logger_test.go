package logger

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLevel_String(t *testing.T) {
	tests := []struct {
		level Level
		want  string
	}{
		{DEBUG, "DEBUG"},
		{INFO, "INFO"},
		{WARN, "WARN"},
		{ERROR, "ERROR"},
		{FATAL, "FATAL"},
		{Level(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.level.String()
			if got != tt.want {
				t.Errorf("Level(%d).String() = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}

func TestLevel_MarshalJSON(t *testing.T) {
	l := INFO
	data, err := l.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	expected := `"INFO"`
	if string(data) != expected {
		t.Errorf("MarshalJSON = %s, want %s", string(data), expected)
	}
}

func TestNewJSONLogger(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, INFO)
	if l == nil {
		t.Fatal("NewJSONLogger returned nil")
	}
	if l.level != INFO {
		t.Errorf("expected level INFO, got %d", l.level)
	}
}

func TestWithPrefix(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, DEBUG, WithPrefix("prefix: "))
	if l.prefix != "prefix: " {
		t.Errorf("expected prefix 'prefix: ', got %q", l.prefix)
	}
}

func TestWithFields(t *testing.T) {
	var buf bytes.Buffer
	fields := map[string]interface{}{"key": "value"}
	l := New(&buf, DEBUG, WithFields(fields))
	if l.fields["key"] != "value" {
		t.Errorf("expected field key=value, got %v", l.fields["key"])
	}
}

func TestJSONLogger_LogLevels(t *testing.T) {
	tests := []struct {
		name     string
		logFn    func(*JSONLogger, string, map[string]interface{})
		level    Level
		minLevel Level
		wantOK   bool
	}{
		{"Debug at Debug level", func(l *JSONLogger, msg string, f map[string]interface{}) { l.Debug(msg, f) }, DEBUG, DEBUG, true},
		{"Debug at Info level", func(l *JSONLogger, msg string, f map[string]interface{}) { l.Debug(msg, f) }, DEBUG, INFO, false},
		{"Info at Info level", func(l *JSONLogger, msg string, f map[string]interface{}) { l.Info(msg, f) }, INFO, INFO, true},
		{"Warn at Error level", func(l *JSONLogger, msg string, f map[string]interface{}) { l.Warn(msg, f) }, WARN, ERROR, false},
		{"Error at Error level", func(l *JSONLogger, msg string, f map[string]interface{}) { l.Error(msg, f) }, ERROR, ERROR, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(&buf, tt.minLevel)
			tt.logFn(l, "test message", nil)
			if tt.wantOK {
				if buf.Len() == 0 {
					t.Errorf("expected output at level %d with min level %d", tt.level, tt.minLevel)
				}
			} else {
				if buf.Len() != 0 {
					t.Errorf("expected no output at level %d with min level %d", tt.level, tt.minLevel)
				}
			}
		})
	}
}

func TestJSONLogger_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, DEBUG)
	l.Info("test message", map[string]interface{}{"request_id": "abc123"})

	output := buf.String()
	if output == "" {
		t.Fatal("expected non-empty output")
	}

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatalf("failed to parse JSON output: %v\noutput: %s", err, output)
	}

	if raw["lvl"] != "INFO" {
		t.Errorf("expected lvl 'INFO', got %v", raw["lvl"])
	}
	if raw["msg"] != "test message" {
		t.Errorf("expected message 'test message', got %v", raw["msg"])
	}
	fields := raw["fields"].(map[string]interface{})
	if fields["request_id"] != "abc123" {
		t.Errorf("expected request_id 'abc123', got %v", fields["request_id"])
	}
	if raw["ts"] == "" {
		t.Error("expected non-empty timestamp")
	}
}

func TestJSONLogger_MergeFields(t *testing.T) {
	var buf bytes.Buffer
	defaultFields := map[string]interface{}{"service": "api"}
	l := New(&buf, DEBUG, WithFields(defaultFields))
	l.Info("test", map[string]interface{}{"user": "john"})

	output := buf.String()
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Fields are nested in the "fields" object.
	fieldsRaw := raw["fields"]
	if fieldsRaw == nil {
		t.Fatal("expected fields in log output")
	}
	fields, ok := fieldsRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected fields to be a map, got %T", fieldsRaw)
	}

	if fields["service"] != "api" {
		t.Errorf("expected service 'api', got %v", fields["service"])
	}
	if fields["user"] != "john" {
		t.Errorf("expected user 'john', got %v", fields["user"])
	}
}

func TestJSONLogger_Prefix(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, DEBUG, WithPrefix("[server] "))
	l.Info("hello", nil)

	output := buf.String()
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if !strings.HasPrefix(raw["msg"].(string), "[server]") {
		t.Errorf("expected message to start with '[server]', got %v", raw["msg"])
	}
}

func TestJSONLogger_ChildLogger(t *testing.T) {
	var buf bytes.Buffer
	parent := New(&buf, DEBUG, WithPrefix("parent: "), WithFields(map[string]interface{}{"parent_field": "pval"}))
	child := parent.NewChild("child:", map[string]interface{}{"child_field": "cval"})

	child.Info("test", nil)

	output := buf.String()
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	expectedMsg := "parent: child:test"
	if raw["msg"].(string) != expectedMsg {
		t.Errorf("expected message %q, got %q", expectedMsg, raw["msg"])
	}
	fields := raw["fields"].(map[string]interface{})
	if fields["parent_field"] != "pval" {
		t.Errorf("expected parent_field 'pval', got %v", fields["parent_field"])
	}
	if fields["child_field"] != "cval" {
		t.Errorf("expected child_field 'cval', got %v", fields["child_field"])
	}
}

func TestHTTPMiddleware(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, DEBUG)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := l.HTTPMiddleware(handler)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	middleware.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	output := buf.String()
	if output == "" {
		t.Fatal("expected non-empty output from HTTP middleware")
	}

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %s", err, output)
	}

	if raw["msg"] != "http_request" {
		t.Errorf("expected message 'http_request', got %v", raw["msg"])
	}
	fields := raw["fields"].(map[string]interface{})
	if fields["method"] != "GET" {
		t.Errorf("expected method 'GET', got %v", fields["method"])
	}
	if fields["path"] != "/test" {
		t.Errorf("expected path '/test', got %v", fields["path"])
	}
	if fields["status"] != float64(200) {
		t.Errorf("expected status 200, got %v", fields["status"])
	}
}

func TestHTTPMiddleware_WithJSONLogger(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, DEBUG)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	middleware := l.HTTPMiddleware(handler)
	req := httptest.NewRequest(http.MethodPost, "/not-found", nil)
	rec := httptest.NewRecorder()

	middleware.ServeHTTP(rec, req)

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(buf.String()), &raw); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	fields := raw["fields"].(map[string]interface{})
	if fields["status"] != float64(404) {
		t.Errorf("expected status 404, got %v", fields["status"])
	}
}

func TestJSONLogger_ConcurrentSafety(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, DEBUG)

	done := make(chan bool)
	for i := 0; i < 100; i++ {
		go func(n int) {
			l.Info("concurrent", map[string]interface{}{"goroutine": n})
			done <- true
		}(i)
	}
	for i := 0; i < 100; i++ {
		<-done
	}

	lines := buf.String()
	if lines == "" {
		t.Fatal("expected output from concurrent logging")
	}

	// Verify all lines are valid JSON.
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(lines), "\n") {
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Errorf("line %q is not valid JSON: %v", line, err)
		}
		count++
	}
	if count != 100 {
		t.Errorf("expected 100 log entries, got %d", count)
	}
}

func TestHTTPMiddlewareStatusWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &HTTPMiddlewareStatusWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	w.WriteHeader(http.StatusCreated)

	if w.statusCode != http.StatusCreated {
		t.Errorf("expected status code 201, got %d", w.statusCode)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("expected underlying response code 201, got %d", rec.Code)
	}
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		realIP     string
		xff        string
		remoteAddr string
		wantIP     string
	}{
		{
			name:       "X-Real-IP",
			realIP:     "192.168.1.1",
			xff:        "10.0.0.1",
			remoteAddr: "127.0.0.1:12345",
			wantIP:     "192.168.1.1",
		},
		{
			name:       "X-Forwarded-For",
			realIP:     "",
			xff:        "10.0.0.1",
			remoteAddr: "127.0.0.1:12345",
			wantIP:     "10.0.0.1",
		},
		{
			name:       "RemoteAddr",
			realIP:     "",
			xff:        "",
			remoteAddr: "192.168.1.100:54321",
			wantIP:     "192.168.1.100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{
				Header: http.Header{
					"X-Real-Ip":       {tt.realIP},
					"X-Forwarded-For": {tt.xff},
				},
				RemoteAddr: tt.remoteAddr,
			}
			got := getClientIP(req)
			if got != tt.wantIP {
				t.Errorf("getClientIP() = %q, want %q", got, tt.wantIP)
			}
		})
	}
}

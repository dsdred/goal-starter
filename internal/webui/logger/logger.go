package logger

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Level represents a structured log level.
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
	FATAL
)

func (l Level) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// MarshalJSON implements json.Marshaler for Level.
func (l Level) MarshalJSON() ([]byte, error) {
	return []byte(`"` + l.String() + `"`), nil
}

// Entry represents a single structured log entry.
type Entry struct {
	Timestamp string                 `json:"ts"`
	Level     Level                  `json:"lvl"`
	Message   string                 `json:"msg"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

// HTTPMiddlewareStatusWriter wraps http.ResponseWriter to capture status code.
type HTTPMiddlewareStatusWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *HTTPMiddlewareStatusWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// getClientIP extracts the real client IP from the request.
func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return ip
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

// JSONLogger logs entries as JSON objects to an output writer.
type JSONLogger struct {
	mu     sync.Mutex
	out    io.Writer
	level  Level
	prefix string
	fields map[string]interface{}
}

// Option configures the JSONLogger.
type Option func(*JSONLogger)

// New creates a new JSONLogger that writes to the given output with the given minimum level.
func New(out io.Writer, minLevel Level, opts ...Option) *JSONLogger {
	l := &JSONLogger{
		out:    out,
		level:  minLevel,
		fields: make(map[string]interface{}),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// WithPrefix sets a prefix for log messages.
func WithPrefix(prefix string) Option {
	return func(l *JSONLogger) {
		l.prefix = prefix
	}
}

// WithFields sets additional default fields for all log entries.
func WithFields(fields map[string]interface{}) Option {
	return func(l *JSONLogger) {
		for k, v := range fields {
			l.fields[k] = v
		}
	}
}

// NewChild creates a child logger inheriting prefix and fields.
func (l *JSONLogger) NewChild(prefix string, fields map[string]interface{}) *JSONLogger {
	child := &JSONLogger{
		out:    l.out,
		level:  l.level,
		prefix: l.prefix + prefix,
		fields: make(map[string]interface{}),
	}
	// Copy parent fields.
	for k, v := range l.fields {
		child.fields[k] = v
	}
	// Add child fields.
	for k, v := range fields {
		child.fields[k] = v
	}
	return child
}

// log writes a structured JSON log entry if the level is sufficient.
func (l *JSONLogger) log(level Level, msg string, fields map[string]interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level,
		Message:   l.prefix + msg,
		Fields:    make(map[string]interface{}),
	}

	// Merge default fields.
	for k, v := range l.fields {
		entry.Fields[k] = v
	}
	// Merge passed fields.
	for k, v := range fields {
		entry.Fields[k] = v
	}

	jsonData, err := json.Marshal(entry)
	if err != nil {
		// Fallback to plain text if JSON marshal fails.
		_, _ = l.out.Write([]byte("json marshal error: " + err.Error() + "\n"))
		return
	}

	_, _ = l.out.Write(jsonData)
	_, _ = l.out.Write([]byte("\n"))
}

// Debug logs a debug-level message.
func (l *JSONLogger) Debug(msg string, fields map[string]interface{}) {
	l.log(DEBUG, msg, fields)
}

// Info logs an info-level message.
func (l *JSONLogger) Info(msg string, fields map[string]interface{}) {
	l.log(INFO, msg, fields)
}

// Warn logs a warning-level message.
func (l *JSONLogger) Warn(msg string, fields map[string]interface{}) {
	l.log(WARN, msg, fields)
}

// Error logs an error-level message.
func (l *JSONLogger) Error(msg string, fields map[string]interface{}) {
	l.log(ERROR, msg, fields)
}

// Fatal logs a fatal-level message and exits.
func (l *JSONLogger) Fatal(msg string, fields map[string]interface{}) {
	l.log(FATAL, msg, fields)
	os.Exit(1)
}

// HTTPMiddleware returns an http.Handler middleware that logs requests as JSON.
func (l *JSONLogger) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		wrapper := &HTTPMiddlewareStatusWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapper, r)

		duration := time.Since(start)

		l.Info("http_request", map[string]interface{}{
			"method":         r.Method,
			"path":           r.URL.Path,
			"status":         wrapper.statusCode,
			"duration":       duration.String(),
			"duration_ms":    duration.Milliseconds(),
			"client_ip":      getClientIP(r),
			"user_agent":     r.UserAgent(),
			"content_length": r.ContentLength,
		})
	})
}

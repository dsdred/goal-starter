package middleware

import (
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// LoggingMiddleware logs each HTTP request with method, path, status, duration and IP.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code.
		wrapper := &statusWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapper, r)

		duration := time.Since(start)

		// Sanitize user agent.
		userAgent := r.UserAgent()
		if userAgent != "" {
			if len(userAgent) > 200 {
				userAgent = userAgent[:200]
			}
		}

		log.Printf("http: %s %s %d %v %s %s", r.Method, r.URL.Path, wrapper.statusCode, duration, getClientIP(r), userAgent)
	})
}

type statusWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// getClientIP extracts the real client IP from the request.
func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		parts := strings.Split(ip, ",")
		return strings.TrimSpace(parts[0])
	}
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

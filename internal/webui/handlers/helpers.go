package handlers

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/dsdred/goal/internal/webui/audit"
	apierrors "github.com/dsdred/goal/internal/webui/errors"
	"github.com/dsdred/goal/internal/webui/security"
)

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// writeAPIError writes a structured API error response with details.
func writeAPIError(w http.ResponseWriter, status int, err *apierrors.APIError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{
		"error": err.Message,
		"code":  string(err.Code),
	}
	if len(err.Details) > 0 {
		resp["details"] = err.Details
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// clientIP returns the TCP peer address for audit records.
// X-Forwarded-For / X-Real-IP are intentionally not trusted (spoofable).
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// auditUser resolves the authenticated username for the request, or "" when
// the session is unknown (auth disabled, missing or invalid cookie).
func auditUser(sess *security.SessionStore, r *http.Request) string {
	if sess == nil {
		return ""
	}
	token, err := security.GetSessionToken(r)
	if err != nil || token == "" {
		return ""
	}
	session, err := sess.ValidateSession(token)
	if err != nil || session == nil {
		return ""
	}
	return session.User
}

// logAudit emits an audit event fail-open (ADR 007 §6): a persistence failure
// never affects the business operation; the logger emits the structured
// operational diagnostic itself.
func logAudit(logger *audit.AuditLogger, sess *security.SessionStore, r *http.Request, event string, detail map[string]string) {
	if logger == nil {
		return
	}
	_ = logger.Log(audit.AuditEvent{
		Event:    event,
		User:     auditUser(sess, r),
		SourceIP: clientIP(r),
		Detail:   detail,
	})
}

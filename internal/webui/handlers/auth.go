package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/dsdred/goal/internal/webui/audit"
	"github.com/dsdred/goal/internal/webui/security"
)

// AuthHandler handles authentication-related HTTP requests.
type AuthHandler struct {
	sess        *security.SessionStore
	pass        *security.PasswordStore
	csrf        *security.CSRF
	authEnabled bool
	audit       *audit.AuditLogger
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(sess *security.SessionStore, pass *security.PasswordStore, csrf *security.CSRF) *AuthHandler {
	return &AuthHandler{
		sess:        sess,
		pass:        pass,
		csrf:        csrf,
		authEnabled: true,
	}
}

// WithAuthEnabled enables or disables authentication for the handler.
func (h *AuthHandler) WithAuthEnabled(enabled bool) *AuthHandler {
	h.authEnabled = enabled
	return h
}

// WithAudit injects the durable audit logger (ADR 007). A nil logger
// disables audit emission for this handler.
func (h *AuthHandler) WithAudit(logger *audit.AuditLogger) *AuthHandler {
	h.audit = logger
	return h
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if !h.authEnabled {
		writeJSON(w, http.StatusOK, map[string]string{
			"authenticated": "true",
			"message":       "authentication is disabled",
		})
		return
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if !h.pass.ValidateCredentials(creds.Username, creds.Password) {
		// Audit records the attempted username; the password is never recorded.
		h.auditLogin(audit.EventLoginFailure, creds.Username, r)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	session, err := h.sess.CreateSession(creds.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	security.SetSessionCookie(w, session.Token)

	// The CSRF token is generated with and bound to this session.
	security.SetCSRFCookie(w, session.CSRFToken)

	h.auditLogin(audit.EventLoginSuccess, creds.Username, r)

	writeJSON(w, http.StatusOK, map[string]string{
		"csrf":          session.CSRFToken,
		"csrf_token":    session.CSRFToken,
		"authenticated": "true",
		"user":          session.User,
	})
}

// auditLogin emits a login outcome event with an explicit username: at login
// time the request carries no (valid) session yet, so the user is the
// authenticated (success) or attempted (failure) username, never from a cookie.
func (h *AuthHandler) auditLogin(event, user string, r *http.Request) {
	if h.audit == nil {
		return
	}
	_ = h.audit.Log(audit.AuditEvent{
		Event:    event,
		User:     user,
		SourceIP: clientIP(r),
	})
}

// Logout handles POST /api/v1/auth/logout.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	token, err := security.GetSessionToken(r)
	user := ""
	if err == nil && token != "" {
		if session, sessErr := h.sess.ValidateSession(token); sessErr == nil && session != nil {
			user = session.User
		}
		_ = h.sess.DestroySession(token)
	}
	security.ClearSessionCookie(w)
	if h.audit != nil {
		_ = h.audit.Log(audit.AuditEvent{
			Event:    audit.EventSessionLogout,
			User:     user,
			SourceIP: clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// CheckSession handles GET /api/v1/auth/session.
func (h *AuthHandler) CheckSession(w http.ResponseWriter, r *http.Request) {
	if !h.authEnabled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"authenticated": true,
			"user":          "public",
			"auth_disabled": true,
		})
		return
	}

	token, err := security.GetSessionToken(r)
	if err != nil || token == "" {
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
		return
	}

	session, err := h.sess.ValidateSession(token)
	if err != nil || session == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"authenticated": true,
		"user":          session.User,
	})
}

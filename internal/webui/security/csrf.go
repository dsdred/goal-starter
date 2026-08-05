package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

const (
	csrfCookieName = "goal_csrf_token"
	csrfHeaderName = "X-CSRF-Token"
	csrfTokenLen   = 32
)

// CSRF protector stores the global token for double-submit cookie validation
// on routes that do not go through requireAuthCSRF.
type CSRF struct {
	mu      sync.RWMutex
	token   string
	enabled bool
}

// NewCSRF creates a new CSRF protector with a random initial token.
func NewCSRF() *CSRF {
	token := generateCSRFToken()
	return &CSRF{token: token, enabled: true}
}

// generateCSRFToken creates a random CSRF token.
func generateCSRFToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// GetToken returns the current CSRF token.
func (c *CSRF) GetToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

// RotateToken generates a new CSRF token and stores it, returning the new value.
// The new token is sent to the client via SetCSRFCookie.
func (c *CSRF) RotateToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = generateCSRFToken()
	return c.token
}

// ValidateRequest checks the CSRF token in the request header and cookie
// against an expected token. It enforces double-submit cookie validation:
// the header token must equal the cookie token, and both must equal expectedToken.
func (c *CSRF) ValidateRequest(r *http.Request, expectedToken string) error {
	if !c.enabled {
		return nil
	}

	headerToken := r.Header.Get(csrfHeaderName)
	cookieToken, cookieErr := r.Cookie(csrfCookieName)

	// Double-submit: header must equal cookie.
	if headerToken == "" || cookieErr != nil || cookieToken.Value == "" {
		return errors.New("missing CSRF token")
	}
	if subtle.ConstantTimeCompare([]byte(headerToken), []byte(cookieToken.Value)) != 1 {
		return errors.New("csrf header/cookie mismatch")
	}

	// Compare against the session-bound expected token.
	if subtle.ConstantTimeCompare([]byte(headerToken), []byte(expectedToken)) != 1 {
		return errors.New("invalid CSRF token")
	}
	return nil
}

// ValidateSessionCSRF validates the CSRF token for a specific session.
// It compares the request header and cookie against the session's CSRFToken.
func (c *CSRF) ValidateSessionCSRF(r *http.Request, session *Session) error {
	if !c.enabled {
		return nil
	}

	headerToken := r.Header.Get(csrfHeaderName)
	cookieToken, cookieErr := r.Cookie(csrfCookieName)

	// Double-submit: header must equal cookie.
	if headerToken == "" || cookieErr != nil || cookieToken.Value == "" {
		return errors.New("missing CSRF token")
	}
	if subtle.ConstantTimeCompare([]byte(headerToken), []byte(cookieToken.Value)) != 1 {
		return errors.New("csrf header/cookie mismatch")
	}

	// Compare against the session's CSRF token.
	if subtle.ConstantTimeCompare([]byte(headerToken), []byte(session.CSRFToken)) != 1 {
		return errors.New("invalid CSRF token")
	}
	return nil
}

// SetCSRFCookie sets the CSRF token in a secure cookie.
func SetCSRFCookie(w http.ResponseWriter, token string) {
	cookie := &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(24 * time.Hour.Seconds()),
	}
	http.SetCookie(w, cookie)
}

// Middleware returns an HTTP middleware that enforces CSRF validation on unsafe methods
// using the global token (for routes that do not use requireAuthCSRF).
func (c *CSRF) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only unsafe methods need CSRF validation.
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		default:
			// Use the global token for double-submit validation.
			if err := c.ValidateRequest(r, c.token); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

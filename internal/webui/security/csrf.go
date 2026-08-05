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

// CSRF protector stores issued tokens per session (simple single-token mode).
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

// RotateToken generates a new CSRF token and returns the old one.
func (c *CSRF) RotateToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.token
	c.token = generateCSRFToken()
	return old
}

// ValidateRequest checks the CSRF token in the request header against the stored token.
// Returns nil if valid, error otherwise.
func (c *CSRF) ValidateRequest(r *http.Request) error {
	if !c.enabled {
		return nil
	}
	header := r.Header.Get(csrfHeaderName)
	if header == "" {
		return errors.New("missing CSRF token")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if subtle.ConstantTimeCompare([]byte(header), []byte(c.token)) != 1 {
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

// Middleware returns an HTTP middleware that enforces CSRF validation on unsafe methods.
func (c *CSRF) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Safe methods don't need CSRF validation.
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && r.Method != "DELETE" {
			if err := c.ValidateRequest(r); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

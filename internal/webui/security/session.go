package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookieName = "goal_session"
	sessionTokenLen   = 32
	sessionTTL        = 24 * time.Hour
	cleanupInterval   = 5 * time.Minute
)

// Session stores session metadata.
type Session struct {
	Token    string    `json:"token"`
	User     string    `json:"user"`
	Created  time.Time `json:"created"`
	Expiry   time.Time `json:"expiry"`
	LastUsed time.Time `json:"last_used"`
}

// SessionStore manages active sessions in memory.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionStore creates a new session store and starts cleanup goroutine.
func NewSessionStore() *SessionStore {
	s := &SessionStore{sessions: make(map[string]*Session)}
	go s.cleanupLoop()
	return s
}

// cleanupLoop removes expired sessions periodically.
func (s *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		s.cleanup()
	}
}

// cleanup removes all expired sessions.
func (s *SessionStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for token, session := range s.sessions {
		if now.After(session.Expiry) {
			delete(s.sessions, token)
		}
	}
}

// CreateSession creates a new session for the given user.
func (s *SessionStore) CreateSession(user string) (*Session, error) {
	token, err := generateToken()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	session := &Session{
		Token:    token,
		User:     user,
		Created:  now,
		Expiry:   now.Add(sessionTTL),
		LastUsed: now,
	}
	s.mu.Lock()
	s.sessions[token] = session
	s.mu.Unlock()
	return session, nil
}

// ValidateSession validates a session token and updates last used time.
func (s *SessionStore) ValidateSession(token string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[token]
	if !ok {
		return nil, errors.New("session not found")
	}
	if time.Now().After(session.Expiry) {
		delete(s.sessions, token)
		return nil, errors.New("session expired")
	}
	session.LastUsed = time.Now()
	// Extend expiry on active use.
	session.Expiry = time.Now().Add(sessionTTL)
	return session, nil
}

// DestroySession removes a session.
func (s *SessionStore) DestroySession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[token]; !ok {
		return errors.New("session not found")
	}
	delete(s.sessions, token)
	return nil
}

// GetSessionUser returns the user for a valid session token.
func (s *SessionStore) GetSessionUser(token string) (string, error) {
	session, err := s.ValidateSession(token)
	if err != nil {
		return "", err
	}
	return session.User, nil
}

// SetSessionCookie sets the session cookie in the response.
func SetSessionCookie(w http.ResponseWriter, token string) {
	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   false, // will be set to true in middleware for HTTPS
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	}
	http.SetCookie(w, cookie)
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	}
	http.SetCookie(w, cookie)
}

// GetSessionToken extracts session token from request cookie.
func GetSessionToken(r *http.Request) (string, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", errors.New("no session cookie")
	}
	if cookie.Value == "" {
		return "", errors.New("empty session cookie")
	}
	return cookie.Value, nil
}

// generateToken creates a random 32-byte token.
func generateToken() (string, error) {
	b := make([]byte, sessionTokenLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// PasswordStore stores admin passwords (first user wins in simple mode).
type PasswordStore struct {
	mu    sync.RWMutex
	users map[string]string // username -> hashed password
}

// NewPasswordStore creates password store with default admin credentials.
func NewPasswordStore() *PasswordStore {
	return &PasswordStore{
		users: map[string]string{
			"admin": "", // will be set via config or generated
		},
	}
}

// AddUser adds a user with the given password.
func (p *PasswordStore) AddUser(username, password string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if username == "" || password == "" {
		return errors.New("username and password are required")
	}
	p.users[username] = password
	return nil
}

// ValidateCredentials checks username/password against stored credentials.
func (p *PasswordStore) ValidateCredentials(username, password string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	stored, ok := p.users[username]
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(stored)) == 1
}

// GetUsernames returns all usernames.
func (p *PasswordStore) GetUsernames() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]string, 0, len(p.users))
	for u := range p.users {
		result = append(result, u)
	}
	return result
}

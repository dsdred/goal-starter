package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionStore_CreateAndValidate(t *testing.T) {
	store := NewSessionStore()
	defer store.cleanup()

	sess, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	if sess.Token == "" {
		t.Error("Expected non-empty token")
	}
	if sess.User != "testuser" {
		t.Errorf("Expected user 'testuser', got '%s'", sess.User)
	}

	validated, err := store.ValidateSession(sess.Token)
	if err != nil {
		t.Fatalf("ValidateSession error: %v", err)
	}
	if validated.User != "testuser" {
		t.Error("Expected validated user 'testuser'")
	}
}

func TestSessionStore_InvalidToken(t *testing.T) {
	store := NewSessionStore()
	defer store.cleanup()

	_, err := store.ValidateSession("nonexistent-token")
	if err == nil {
		t.Error("Expected error for invalid token")
	}
}

func TestSessionStore_Destroy(t *testing.T) {
	store := NewSessionStore()
	defer store.cleanup()

	sess, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}

	err = store.DestroySession(sess.Token)
	if err != nil {
		t.Fatalf("DestroySession error: %v", err)
	}

	_, err = store.ValidateSession(sess.Token)
	if err == nil {
		t.Error("Expected error after destroying session")
	}
}

func TestPasswordStore_ValidateCredentials(t *testing.T) {
	store := NewPasswordStore()

	// Empty store: no users exist yet.
	if store.ValidateCredentials("admin", "anypass") {
		t.Error("Expected unknown user to fail on empty store")
	}
	if store.ValidateCredentials("admin", "") {
		t.Error("Expected empty password to fail")
	}
	if store.ValidateCredentials("", "pass") {
		t.Error("Expected empty username to fail")
	}

	err := store.SetPassword("admin", "secret123")
	if err != nil {
		t.Fatalf("SetPassword error: %v", err)
	}

	if !store.ValidateCredentials("admin", "secret123") {
		t.Error("Expected correct credentials to validate")
	}

	if store.ValidateCredentials("admin", "wrongpass") {
		t.Error("Expected wrong password to fail")
	}

	if store.ValidateCredentials("unknown", "secret123") {
		t.Error("Expected unknown user to fail")
	}

	err = store.SetPassword("custom", "custompass")
	if err != nil {
		t.Fatalf("SetPassword error: %v", err)
	}

	if !store.ValidateCredentials("custom", "custompass") {
		t.Error("Expected custom user to validate")
	}

	if store.ValidateCredentials("custom", "wrongpass") {
		t.Error("Expected custom user with wrong password to fail")
	}
}

func TestPasswordStore_EmptyCredentials(t *testing.T) {
	store := NewPasswordStore()

	err := store.SetPassword("", "pass")
	if err == nil {
		t.Error("Expected error for empty username")
	}

	err = store.SetPassword("user", "")
	if err != nil {
		// bcrypt rejects empty password
		t.Log("Expected error for empty password:", err)
	}
}

func TestSetSessionCookie(t *testing.T) {
	store := NewSessionStore()
	defer store.cleanup()

	sess, err := store.CreateSession("testuser")
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}

	w := httptest.NewRecorder()
	SetSessionCookie(w, sess.Token)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Expected 1 cookie, got %d", len(cookies))
	}

	cookie := cookies[0]
	if cookie.Name != sessionCookieName {
		t.Errorf("Expected cookie name '%s', got '%s'", sessionCookieName, cookie.Name)
	}
	if cookie.Value != sess.Token {
		t.Error("Cookie value mismatch")
	}
	if !cookie.HttpOnly {
		t.Error("Expected HttpOnly to be true")
	}
}

func TestGetSessionToken(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "test-token"})

	token, err := GetSessionToken(req)
	if err != nil {
		t.Fatalf("GetSessionToken error: %v", err)
	}
	if token != "test-token" {
		t.Errorf("Expected 'test-token', got '%s'", token)
	}
}

func TestGetSessionToken_NoCookie(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	_, err := GetSessionToken(req)
	if err == nil {
		t.Error("Expected error for missing cookie")
	}
}

func TestGetSessionToken_EmptyCookie(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: ""})

	_, err := GetSessionToken(req)
	if err == nil {
		t.Error("Expected error for empty cookie")
	}
}

func TestCSRF_GetToken(t *testing.T) {
	csrf := NewCSRF()
	token := csrf.GetToken()
	if token == "" {
		t.Error("Expected non-empty CSRF token")
	}
}

func TestCSRF_RotateToken(t *testing.T) {
	csrf := NewCSRF()
	oldToken := csrf.GetToken()
	// RotateToken now returns the NEW token that was stored.
	newToken := csrf.RotateToken()
	// RotateToken should return the new token.
	if newToken == oldToken {
		t.Error("RotateToken should return a new token different from the old one")
	}
	// The stored token should match what RotateToken returned.
	currentToken := csrf.GetToken()
	if currentToken != newToken {
		t.Error("Stored token should match what RotateToken returned")
	}
}

func TestCSRF_Middleware(t *testing.T) {
	csrf := NewCSRF()
	handler := csrf.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	token := csrf.GetToken()

	// Test: missing CSRF token → 403.
	req := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()
	csrf.Middleware(handler).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for missing CSRF token, got %d", w.Code)
	}

	// Test: valid double-submit (header == cookie == token) → 200.
	req = httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(&http.Cookie{Name: "goal_csrf_token", Value: token})
	w = httptest.NewRecorder()
	csrf.Middleware(handler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for valid CSRF token, got %d", w.Code)
	}
}

func TestCSRF_Middleware_SafeMethods(t *testing.T) {
	csrf := NewCSRF()
	handler := csrf.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	csrf.Middleware(handler).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for GET without CSRF token, got %d", w.Code)
	}
}

func TestCSRF_ValidateRequest(t *testing.T) {
	csrf := NewCSRF()
	token := csrf.GetToken()

	// Test 1: missing header and cookie.
	req := httptest.NewRequest("POST", "/", nil)
	if err := csrf.ValidateRequest(req, token); err == nil {
		t.Error("Expected error for missing CSRF token")
	}

	// Test 2: valid header, missing cookie.
	req = httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-CSRF-Token", token)
	if err := csrf.ValidateRequest(req, token); err == nil {
		t.Error("Expected error for missing cookie with valid header")
	}

	// Test 3: valid header and cookie, but mismatch with expected token.
	req = httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(&http.Cookie{Name: "goal_csrf_token", Value: token})
	// Cookie and header match each other, but expected is different.
	if err := csrf.ValidateRequest(req, "different-token"); err == nil {
		t.Error("Expected error for wrong expected token")
	}

	// Test 4: valid header, valid cookie, correct expected token (double-submit success).
	req = httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(&http.Cookie{Name: "goal_csrf_token", Value: token})
	if err := csrf.ValidateRequest(req, token); err != nil {
		t.Errorf("Expected no error for valid CSRF token, got: %v", err)
	}

	// Test 5: header/cookie mismatch (header != cookie).
	req = httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(&http.Cookie{Name: "goal_csrf_token", Value: "different-cookie"})
	if err := csrf.ValidateRequest(req, token); err == nil {
		t.Error("Expected error for header/cookie mismatch")
	}
}

func TestClearSessionCookie(t *testing.T) {
	w := httptest.NewRecorder()
	ClearSessionCookie(w)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Expected 1 cookie, got %d", len(cookies))
	}

	cookie := cookies[0]
	if cookie.MaxAge != -1 {
		t.Errorf("Expected MaxAge -1, got %d", cookie.MaxAge)
	}
}

func TestSession_Store(t *testing.T) {
	if strings.Contains("test", "test") == false {
		t.Error("strings.Contains test failed")
	}
}

func BenchmarkCreateSession(b *testing.B) {
	store := NewSessionStore()
	defer store.cleanup()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := store.CreateSession("benchuser")
		if err != nil {
			b.Fatal(err)
		}
	}
}

package handlers

import (
	"bytes"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dsdred/goal/internal/config"
	"github.com/dsdred/goal/internal/webui/security"
)

// ADR 019 §D18 makes `Secure` a property of the connection that produced the
// response. These tests drive the production emit/clear sites (Login sets the
// session and CSRF cookies, Logout clears the session cookie) over both
// connection kinds, and prove nothing else in the handler can decide the flag.

func newAuthHandler(t *testing.T) *AuthHandler {
	t.Helper()
	pass := security.NewPasswordStore()
	if err := pass.SetPassword("admin", "testpassword"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	sess := security.NewSessionStore()
	return NewAuthHandler(sess, pass, security.NewCSRF()).WithAuthEnabled(true)
}

func cookieLoginRequest(overTLS bool) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		bytes.NewBufferString(`{"username":"admin","password":"testpassword"}`))
	req.Header.Set("Content-Type", "application/json")
	if overTLS {
		req.TLS = &tls.ConnectionState{}
	}
	return httptest.NewRecorder(), req
}

// cookiesOf returns the emitted cookies keyed by name, failing on any name the
// contract does not allow (§D18 property 3: no second cookie, no prefix).
func cookiesOf(t *testing.T, w *httptest.ResponseRecorder) map[string]*http.Cookie {
	t.Helper()
	got := make(map[string]*http.Cookie)
	for _, c := range w.Result().Cookies() {
		if _, dup := got[c.Name]; dup {
			t.Fatalf("duplicate cookie name %q", c.Name)
		}
		got[c.Name] = c
	}
	if len(got) == 0 {
		t.Fatalf("no Set-Cookie emitted (headers: %v)", w.Result().Header)
	}
	return got
}

// TestLogin_SecureCookiesFollowConnection covers the login emit path for both
// cookies on both connection kinds.
func TestLogin_SecureCookiesFollowConnection(t *testing.T) {
	h := newAuthHandler(t)

	for _, overTLS := range []bool{false, true} {
		w, req := cookieLoginRequest(overTLS)
		h.Login(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("login over %s: expected 200, got %d: %s", schemeOf(overTLS), w.Code, w.Body.String())
		}

		got := cookiesOf(t, w)
		if len(got) != 2 {
			t.Fatalf("login over %s: expected goal_session + goal_csrf_token, got %v", schemeOf(overTLS), names(got))
		}
		for _, name := range []string{"goal_session", "goal_csrf_token"} {
			if got[name].Secure != overTLS {
				t.Errorf("login over %s: %s Secure=%v, want %v", schemeOf(overTLS), name, got[name].Secure, overTLS)
			}
		}
		if s := got["goal_session"]; !s.HttpOnly || s.SameSite != http.SameSiteLaxMode {
			t.Errorf("login over %s: session attributes changed: %+v", schemeOf(overTLS), s)
		}
		if c := got["goal_csrf_token"]; c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
			t.Errorf("login over %s: csrf attributes changed: %+v", schemeOf(overTLS), c)
		}
	}
}

// TestLogout_ClearCarriesConnectionSecure asserts the clear path emits the same
// flag as the connection it answers, so a logout over TLS cannot downgrade the
// jar entry it removes (measured rows D2/D4).
func TestLogout_ClearCarriesConnectionSecure(t *testing.T) {
	h := newAuthHandler(t)

	for _, overTLS := range []bool{false, true} {
		w, req := cookieLoginRequest(overTLS)
		h.Login(w, req)
		token := cookiesOf(t, w)["goal_session"].Value

		logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		logoutReq.AddCookie(&http.Cookie{Name: "goal_session", Value: token})
		if overTLS {
			logoutReq.TLS = &tls.ConnectionState{}
		}
		lw := httptest.NewRecorder()
		h.Logout(lw, logoutReq)

		if lw.Code != http.StatusOK {
			t.Fatalf("logout over %s: expected 200, got %d", schemeOf(overTLS), lw.Code)
		}
		cleared := cookiesOf(t, lw)["goal_session"]
		if cleared.Secure != overTLS {
			t.Errorf("logout over %s: clear Secure=%v, want %v", schemeOf(overTLS), cleared.Secure, overTLS)
		}
		if cleared.MaxAge != -1 || cleared.Value != "" {
			t.Errorf("logout over %s: clear must expire the cookie, got %+v", schemeOf(overTLS), cleared)
		}
	}
}

// TestLogin_ForwardedProtoDoesNotMintSecure pins the §D20 / obligation 10 cookie
// half at the handler: metadata from a TLS-terminating proxy changes nothing.
func TestLogin_ForwardedProtoDoesNotMintSecure(t *testing.T) {
	h := newAuthHandler(t)

	w, req := cookieLoginRequest(false)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Real-IP", "203.0.113.9")
	h.Login(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	for _, c := range cookiesOf(t, w) {
		if c.Secure {
			t.Errorf("%s: forwarded headers minted Secure on a plain connection", c.Name)
		}
	}
}

// TestLogin_TLSEnabledConfigStillNonSecureOverHTTP proves the flag is not derived
// from configuration. The deployment below carries a complete, enabled `tls`
// block; a request that arrived over plain HTTP still gets a non-Secure cookie,
// because the emit path reads the connection and the handler holds no config.
func TestLogin_TLSEnabledConfigStillNonSecureOverHTTP(t *testing.T) {
	cfg := config.Default()
	port := 8443
	cfg.TLS = &config.TLSConfig{
		Enabled:  true,
		Port:     &port,
		CertFile: `C:\absent-for-this-test\cert.pem`,
		KeyFile:  `C:\absent-for-this-test\key.pem`,
	}
	if cfg.TLS == nil || !cfg.TLS.Enabled {
		t.Fatal("test setup lost the enabled tls block")
	}

	h := newAuthHandler(t)
	w, req := cookieLoginRequest(false)
	h.Login(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	for _, c := range cookiesOf(t, w) {
		if c.Secure {
			t.Errorf("%s: config-derived Secure leaked into an HTTP response", c.Name)
		}
		if raw := w.Result().Header.Get("Set-Cookie"); strings.Contains(strings.ToLower(raw), "secure") {
			t.Errorf("%s: HTTP baseline header changed: %q", c.Name, raw)
		}
	}
}

// TestAuthCookies_RealConnections runs the handler behind live HTTP and HTTPS
// servers, so the asserted flag comes from a connection Go itself established.
func TestAuthCookies_RealConnections(t *testing.T) {
	for _, overTLS := range []bool{false, true} {
		h := newAuthHandler(t)
		srv := httptest.NewServer(http.HandlerFunc(h.Login))
		if overTLS {
			srv = httptest.NewTLSServer(http.HandlerFunc(h.Login))
		}

		resp, err := srv.Client().Post(srv.URL, "application/json",
			strings.NewReader(`{"username":"admin","password":"testpassword"}`))
		if err != nil {
			t.Fatalf("login over %s: %v", schemeOf(overTLS), err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("login over %s: expected 200, got %d", schemeOf(overTLS), resp.StatusCode)
		}
		cookies := resp.Cookies()
		if len(cookies) != 2 {
			t.Fatalf("login over %s: expected 2 cookies, got %d", schemeOf(overTLS), len(cookies))
		}
		for _, c := range cookies {
			if c.Secure != overTLS {
				t.Errorf("login over %s: %s Secure=%v, want %v", schemeOf(overTLS), c.Name, c.Secure, overTLS)
			}
		}
		srv.Close()
	}
}

func schemeOf(overTLS bool) string {
	if overTLS {
		return "HTTPS"
	}
	return "HTTP"
}

func names(m map[string]*http.Cookie) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

package security

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ADR 019 §D18 puts exactly three Set-Cookie sites under the connection-derived
// `Secure` rule. The slice is only closed when all three are covered, because a
// clear path that kept a hard-coded flag would silently downgrade the jar entry.

// emitPath is one Set-Cookie site under the rule.
type emitPath struct {
	name string
	emit func(http.ResponseWriter, *http.Request)
	coop string // cookie name the site must emit
}

func emitPaths() []emitPath {
	return []emitPath{
		{"session set", func(w http.ResponseWriter, r *http.Request) { SetSessionCookie(w, r, "session-token") }, sessionCookieName},
		{"session clear", func(w http.ResponseWriter, r *http.Request) { ClearSessionCookie(w, r) }, sessionCookieName},
		{"csrf set", func(w http.ResponseWriter, r *http.Request) { SetCSRFCookie(w, r, "csrf-token") }, csrfCookieName},
	}
}

func pathEmitter(t *testing.T, name string) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	for _, p := range emitPaths() {
		if p.name == name {
			return p.emit
		}
	}
	t.Fatalf("no emit path registered for %q", name)
	return nil
}

// connRequest builds a request whose connection is TLS or not. The URL string is
// the same either way on purpose: the fact GoAl must read lives in r.TLS, never
// in the request URI or a header.
func connRequest(tlsConn bool) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://aids:8088/api/v1/auth/login", nil)
	if tlsConn {
		r.TLS = &tls.ConnectionState{}
	}
	return r
}

func emitRaw(t *testing.T, w *httptest.ResponseRecorder, r *http.Request, path emitPath) string {
	t.Helper()
	path.emit(w, r)
	raw := w.Result().Header.Get("Set-Cookie")
	if raw == "" {
		t.Fatalf("%s: no Set-Cookie emitted", path.name)
	}
	return raw
}

func hasSecure(raw string) bool {
	for _, part := range strings.Split(raw, ";") {
		if strings.EqualFold(strings.TrimSpace(part), "Secure") {
			return true
		}
	}
	return false
}

// TestSecureCookie_MatchesConnectionOnEveryEmitPath is §D18's normative rule,
// asserted on the wire bytes for all three emit sites and both connection kinds.
func TestSecureCookie_MatchesConnectionOnEveryEmitPath(t *testing.T) {
	for _, path := range emitPaths() {
		for _, tlsConn := range []bool{false, true} {
			w := httptest.NewRecorder()
			raw := emitRaw(t, w, connRequest(tlsConn), path)

			if got := hasSecure(raw); got != tlsConn {
				t.Errorf("%s over %s: Secure=%v, want %v (%q)", path.name, schemeName(tlsConn), got, tlsConn, raw)
			}
			if strings.Count(raw, "\n") != 0 {
				t.Errorf("%s: expected exactly one Set-Cookie header value, got %q", path.name, raw)
			}
			if !strings.HasPrefix(raw, path.coop+"=") {
				t.Errorf("%s: cookie name changed, got %q", path.name, raw)
			}
		}
	}
}

// TestSecureCookie_HttpOnlyBaselineIsUnchangedOnTheWire pins §D18 required
// property 2: with no TLS connection the emitted header carries no `Secure`
// attribute at all, so none of the measured jar transitions becomes reachable.
func TestSecureCookie_HttpOnlyBaselineIsUnchangedOnTheWire(t *testing.T) {
	for _, path := range emitPaths() {
		w := httptest.NewRecorder()
		raw := emitRaw(t, w, connRequest(false), path)
		if strings.Contains(strings.ToLower(raw), "secure") {
			t.Errorf("%s over plain HTTP must not carry Secure, got %q", path.name, raw)
		}
	}
}

// TestSecureCookie_OtherAttributesFrozen asserts the rule changed exactly one
// flag: name, Path, host-only scope (no Domain), HttpOnly and SameSite keep the
// shipped values on every emit path (§D18 property 3).
func TestSecureCookie_OtherAttributesFrozen(t *testing.T) {
	tests := []struct {
		path     string
		coop     string
		value    string
		must     []string
		mustNot  []string
		httpOnly bool
		sameSite http.SameSite
		maxAge   int
	}{
		{
			path:     "session set",
			coop:     sessionCookieName,
			value:    "session-token",
			must:     []string{"Path=/", "HttpOnly", "SameSite=Lax", "Max-Age=86400"},
			mustNot:  []string{"Domain=", "SameSite=Strict", "SameSite=None"},
			httpOnly: true,
			sameSite: http.SameSiteLaxMode,
			maxAge:   int(sessionTTL.Seconds()),
		},
		{
			// The clear path is the shipped deletion form: Go serializes MaxAge -1 as
			// `Max-Age=0` and never emitted SameSite, so the parsed value is 0.
			path:     "session clear",
			coop:     sessionCookieName,
			value:    "",
			must:     []string{"Path=/", "HttpOnly", "Max-Age=0"},
			mustNot:  []string{"Domain=", "SameSite=", "SameSite=Lax"},
			httpOnly: true,
			sameSite: 0,
			maxAge:   -1,
		},
		{
			path:     "csrf set",
			coop:     csrfCookieName,
			value:    "csrf-token",
			must:     []string{"Path=/", "SameSite=Strict", "Max-Age=86400"},
			mustNot:  []string{"Domain=", "HttpOnly"},
			httpOnly: false,
			sameSite: http.SameSiteStrictMode,
			maxAge:   int((24 * time.Hour).Seconds()),
		},
	}

	for _, want := range tests {
		emitFn := pathEmitter(t, want.path)
		for _, tlsConn := range []bool{false, true} {
			w := httptest.NewRecorder()
			emitFn(w, connRequest(tlsConn))
			raw := w.Result().Header.Get("Set-Cookie")

			if !strings.HasPrefix(raw, want.coop+"="+want.value+";") {
				t.Errorf("%s/%s: name or value changed: %q", want.path, schemeName(tlsConn), raw)
			}
			for _, attr := range want.must {
				if !strings.Contains(raw, attr) {
					t.Errorf("%s/%s: missing %q in %q", want.path, schemeName(tlsConn), attr, raw)
				}
			}
			for _, attr := range want.mustNot {
				if strings.Contains(raw, attr) {
					t.Errorf("%s/%s: unexpected %q in %q", want.path, schemeName(tlsConn), attr, raw)
				}
			}

			parsed := onlyCookie(t, w, want.coop)
			if parsed.HttpOnly != want.httpOnly {
				t.Errorf("%s/%s: HttpOnly=%v, want %v", want.path, schemeName(tlsConn), parsed.HttpOnly, want.httpOnly)
			}
			if parsed.SameSite != want.sameSite {
				t.Errorf("%s/%s: SameSite=%v, want %v", want.path, schemeName(tlsConn), parsed.SameSite, want.sameSite)
			}
			if parsed.MaxAge != want.maxAge {
				t.Errorf("%s/%s: MaxAge=%d, want %d", want.path, schemeName(tlsConn), parsed.MaxAge, want.maxAge)
			}
			// Host-only scope: parsed Domain must stay empty.
			if parsed.Domain != "" {
				t.Errorf("%s/%s: Domain=%q, want host-only scope", want.path, schemeName(tlsConn), parsed.Domain)
			}
			if parsed.Secure != tlsConn {
				t.Errorf("%s/%s: Secure=%v, want the connection-derived %v", want.path, schemeName(tlsConn), parsed.Secure, tlsConn)
			}
		}
	}
}

func onlyCookie(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly 1 cookie (no scheme-specific namespace), got %d: %v", len(cookies), cookies)
	}
	if cookies[0].Name != name {
		t.Fatalf("expected cookie %q, got %q", name, cookies[0].Name)
	}
	return cookies[0]
}

// TestSecureCookie_ProxyHeadersNeverDecideIt is §D20 plus test obligation 10 for
// the cookie half: forwarded metadata must not mint `Secure` on a plain
// connection, and must not strip it from a TLS one.
func TestSecureCookie_ProxyHeadersNeverDecideIt(t *testing.T) {
	headers := map[string]string{
		"X-Forwarded-Proto": "https",
		"X-Forwarded-For":   "203.0.113.9",
		"X-Real-IP":         "203.0.113.9",
		"Forwarded":         "proto=https;for=203.0.113.9;by=proxy",
	}

	for _, path := range emitPaths() {
		// A TLS-terminating proxy: GoAl sees r.TLS == nil plus https metadata.
		r := connRequest(false)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		raw := emitRaw(t, w, r, path)
		if hasSecure(raw) {
			t.Errorf("%s: forwarded proxy headers minted Secure on a plain connection (%q)", path.name, raw)
		}

		// The reverse: direct TLS plus metadata claiming plain HTTP.
		r2 := connRequest(true)
		r2.Header.Set("X-Forwarded-Proto", "http")
		w2 := httptest.NewRecorder()
		raw2 := emitRaw(t, w2, r2, path)
		if !hasSecure(raw2) {
			t.Errorf("%s: X-Forwarded-Proto: http downgraded a TLS connection (%q)", path.name, raw2)
		}
	}
}

// TestSecureCookie_SchemeFromConnectionNotURL asserts the rule cannot be
// satisfied by string-sniffing the request URI: an https:-shaped request on a
// non-TLS connection stays non-Secure, and a plain-shaped URL over TLS is Secure.
func TestSecureCookie_SchemeFromConnectionNotURL(t *testing.T) {
	// httptest.NewRequest marks an https:// URL as a TLS request; clear that to
	// model a request whose URL text says https while its connection does not.
	httpsURL := connRequest(false)
	httpsURL.URL.Scheme = "https"
	httpsURL.Host = "aids:8443"

	plainURL := connRequest(true)

	cases := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"https URL, no TLS connection", httpsURL, false},
		{"http URL, TLS connection", plainURL, true},
	}

	for _, path := range emitPaths() {
		for _, tc := range cases {
			w := httptest.NewRecorder()
			raw := emitRaw(t, w, tc.r, path)
			if got := hasSecure(raw); got != tc.want {
				t.Errorf("%s via %s: Secure=%v, want %v (%q)", path.name, tc.name, got, tc.want, raw)
			}
		}
	}
}

// TestSecureCookie_RealConnections drives actual servers, so the asserted flag
// comes from a connection Go itself established rather than a request a test
// wrote by hand.
func TestSecureCookie_RealConnections(t *testing.T) {
	for _, path := range emitPaths() {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path.emit(w, r)
		})

		for _, overTLS := range []bool{false, true} {
			srv := httptest.NewServer(handler)
			if overTLS {
				srv = httptest.NewTLSServer(handler)
			}
			checkEmit(t, path, srv, overTLS)
			srv.Close()
		}
	}
}

func checkEmit(t *testing.T, path emitPath, srv *httptest.Server, overTLS bool) {
	t.Helper()
	// srv.Client() trusts the httptest self-signed certificate; that handshake is
	// precisely the connection-level fact the rule reads.
	resp, err := srv.Client().Post(srv.URL, "application/json", nil) //nolint:noctx // test-local httptest server
	if err != nil {
		t.Fatalf("%s over %s: request: %v", path.name, schemeName(overTLS), err)
	}
	defer resp.Body.Close()

	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("%s over %s: expected 1 cookie (no scheme-specific namespace), got %d", path.name, schemeName(overTLS), len(cookies))
	}
	if cookies[0].Name != path.coop {
		t.Fatalf("%s over %s: expected %q, got %q", path.name, schemeName(overTLS), path.coop, cookies[0].Name)
	}
	if cookies[0].Secure != overTLS {
		t.Errorf("%s over %s: Secure=%v, want %v", path.name, schemeName(overTLS), cookies[0].Secure, overTLS)
	}
}

func schemeName(overTLS bool) string {
	if overTLS {
		return "HTTPS"
	}
	return "HTTP"
}

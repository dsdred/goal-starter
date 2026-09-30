package webui

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/config"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

// freeTCPPort returns a port no test owns. The listener is closed immediately,
// so the caller can bind it and observe bind-before-serve behavior.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("release free port: %v", err)
	}
	return port
}

// issuedCert is a generated test certificate with its private key.
type issuedCert struct {
	cert *x509.Certificate
	der  []byte
	key  *ecdsa.PrivateKey
}

func (c *issuedCert) certPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.der})
}

func (c *issuedCert) keyPEM(t *testing.T) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(c.key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func newTestCert(t *testing.T, cn string, isCA bool, parent *issuedCert) *issuedCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"GoAl webui tests"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  isCA,
		DNSNames:              []string{cn, "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return &issuedCert{cert: tmpl, der: der, key: key}
}

// tlsFiles is a written cert/key pair plus the exact private-key bytes, so the
// log-privacy assertions can prove key material never appears anywhere.
type tlsFiles struct {
	certPath string
	keyPath  string
	keyPEM   []byte
	leafPEM  []byte
	// leafFirst is the leaf-then-issuer certFile content, only set when the
	// caller supplied an issuer to chain (§D5: GoAl presents the file as-is).
	leafFirst []byte
}

// writeTLSFiles writes a leaf-only certFile by default; callers that need a
// chain overwrite certPath with leafFirst.
func writeTLSFiles(t *testing.T, leaf *issuedCert, ca *issuedCert) *tlsFiles {
	t.Helper()
	dir := t.TempDir()
	f := &tlsFiles{
		certPath: filepath.Join(dir, "goal.crt"),
		keyPath:  filepath.Join(dir, "goal.key"),
		keyPEM:   leaf.keyPEM(t),
		leafPEM:  leaf.certPEM(),
	}
	if ca != nil {
		f.leafFirst = append(append([]byte{}, f.leafPEM...), ca.certPEM()...)
	}
	if err := os.WriteFile(f.certPath, f.leafPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(f.keyPath, f.keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return f
}

// tlsConfig builds a config.Config with the HTTPS listener enabled on port.
func tlsConfig(webPort, httpsPort int, f *tlsFiles) *config.Config {
	cfg := baseConfig(webPort)
	port := httpsPort
	cfg.TLS = &config.TLSConfig{Enabled: true, Port: &port, CertFile: f.certPath, KeyFile: f.keyPath}
	return cfg
}

func baseConfig(webPort int) *config.Config {
	return &config.Config{ListenAddress: "127.0.0.1", WebPort: webPort}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
}

// syncBuffer is an io.Writer safe for concurrent slog handlers.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs redirects the default slog output for the duration of the test.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// logText returns the captured log with slog's TextHandler quoting undone. A
// value containing a space or '=' is quoted with strconv, which doubles every
// backslash in a Windows path, so path assertions must read the unquoted form.
func logText(b *syncBuffer) string {
	return strings.ReplaceAll(b.String(), `\\`, `\`)
}

// injectedListener is a net.Listener whose Accept fails on command, so one Serve
// loop can be made to return while a sibling is still healthy. Close reports the
// error a real already-closed listener returns, which is what would let a
// shutdown error bury the serve failure if the aggregation ever started
// reporting one for a listener that was already gone.
type injectedListener struct {
	addr     net.Addr
	release  chan struct{}
	err      error
	closeErr error
	closed   atomic.Bool
}

func newInjectedListener(t *testing.T, scheme string) *injectedListener {
	t.Helper()
	return &injectedListener{
		addr:     &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1, Zone: scheme},
		release:  make(chan struct{}),
		err:      errors.New("injected accept failure"),
		closeErr: errors.New("use of closed network connection"),
	}
}

func (l *injectedListener) Accept() (net.Conn, error) {
	<-l.release
	return nil, l.err
}

func (l *injectedListener) Close() error {
	l.closed.Store(true)
	return l.closeErr
}

func (l *injectedListener) Addr() net.Addr { return l.addr }

func newTestServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

// serveInBackground starts set.serve and returns a func that waits for it to
// finish and reports the error. Waiting is bounded by the test deadline so a
// lifecycle bug fails loudly instead of hanging the suite.
func serveInBackground(t *testing.T, set *listenerSet, ctx context.Context) func() error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- set.serve(ctx) }()
	return func() error {
		t.Helper()
		select {
		case err := <-done:
			return err
		case <-time.After(15 * time.Second):
			t.Fatal("listener set did not return within 15s (lifecycle hang)")
			return nil
		}
	}
}

func assertPortFree(t *testing.T, addr string) {
	t.Helper()
	probe, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("expected %s to be free, but bind failed: %v", addr, err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close probe: %v", err)
	}
}

func assertPortBound(t *testing.T, addr string) {
	t.Helper()
	probe, err := net.Listen("tcp", addr)
	if err == nil {
		_ = probe.Close()
		t.Fatalf("expected %s to be bound by the listener set, but it is still free", addr)
	}
}

// ---------------------------------------------------------------------------
// §D1 / §D2 / §D3 — which shapes open a listener
// ---------------------------------------------------------------------------

// TestIntendedListenerShapes pins the listener-level half of the §D3 table: no
// shape other than an explicit enabled:true with a port can add a listener.
func TestIntendedListenerShapes(t *testing.T) {
	port := 18443
	struck := func(mut func(*config.Config)) *config.Config {
		cfg := baseConfig(18080)
		mut(cfg)
		return cfg
	}
	cases := []struct {
		name        string
		cfg         *config.Config
		wantS       []string
		wantErr     bool
		errFragment string
	}{
		{"absent tls block", struck(func(*config.Config) {}), []string{"HTTP"}, false, ""},
		{"empty tls block", struck(func(c *config.Config) { c.TLS = &config.TLSConfig{} }), []string{"HTTP"}, false, ""},
		{"enabled false", struck(func(c *config.Config) { c.TLS = &config.TLSConfig{Enabled: false} }), []string{"HTTP"}, false, ""},
		{"enabled false with full values", struck(func(c *config.Config) {
			c.TLS = &config.TLSConfig{Enabled: false, Port: &port, CertFile: "C:\\certs\\a.crt", KeyFile: "C:\\certs\\a.key"}
		}), []string{"HTTP"}, false, ""},
		{"enabled true without port", struck(func(c *config.Config) {
			c.TLS = &config.TLSConfig{Enabled: true, CertFile: "C:\\certs\\a.crt", KeyFile: "C:\\certs\\a.key"}
		}), nil, true, "tls.port is required"},
		{"enabled true with port", struck(func(c *config.Config) {
			c.TLS = &config.TLSConfig{Enabled: true, Port: &port}
		}), []string{"HTTP", "HTTPS"}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := intendedListeners(tc.cfg)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), tc.errFragment) {
					t.Fatalf("err = %v, want one containing %q", err, tc.errFragment)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.wantS) {
				t.Fatalf("listeners = %d (%+v), want %d", len(got), got, len(tc.wantS))
			}
			for i, want := range tc.wantS {
				if got[i].scheme != want {
					t.Errorf("listener %d scheme = %q, want %q", i, got[i].scheme, want)
				}
			}
			if len(got) == 2 && got[0].addr != "127.0.0.1:18080" {
				t.Errorf("HTTP addr = %q, want the unchanged 127.0.0.1:18080", got[0].addr)
			}
			if len(got) == 2 && got[1].addr != "127.0.0.1:18443" {
				t.Errorf("HTTPS addr = %q, want 127.0.0.1:18443 (same bind address, §D2)", got[1].addr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// §D15.2 / §D15.3 — bind-before-serve, per-listener server, TLS constants
// ---------------------------------------------------------------------------

// TestBindListenersHTTPOnlyBaseline is the §D24 regression baseline: with no tls
// block the set is exactly today's single HTTP listener with today's timeouts and
// no TLS configuration.
func TestBindListenersHTTPOnlyBaseline(t *testing.T) {
	webPort := freeTCPPort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", webPort)
	logs := captureLogs(t)

	set, err := bindListeners(baseConfig(webPort), okHandler())
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	t.Cleanup(func() { set.closeAll() })

	if len(set.listeners) != 1 {
		t.Fatalf("listeners = %d, want 1", len(set.listeners))
	}
	l := set.listeners[0]
	if l.scheme != "HTTP" {
		t.Errorf("scheme = %q, want HTTP", l.scheme)
	}
	if l.addr != addr {
		t.Errorf("addr = %q, want %q", l.addr, addr)
	}
	if l.server.TLSConfig != nil {
		t.Error("HTTP server carries a TLSConfig; the plain listener must stay untouched")
	}
	// The documented HTTP-only posture (§D18 deployment state 1) needs a nil
	// startup-attr set: no cert, no expiry, no san keys in the log line.
	if len(l.startupAttrs) != 0 {
		t.Errorf("HTTP startupAttrs = %v, want none", l.startupAttrs)
	}
	if got := l.server.ReadHeaderTimeout; got != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", got)
	}
	if got := l.server.ReadTimeout; got != 30*time.Second {
		t.Errorf("ReadTimeout = %v, want 30s", got)
	}
	if got := l.server.IdleTimeout; got != 60*time.Second {
		t.Errorf("IdleTimeout = %v, want 60s", got)
	}
	if got := l.server.MaxHeaderBytes; got != 1<<20 {
		t.Errorf("MaxHeaderBytes = %d, want %d", got, 1<<20)
	}
	// Bound, not yet serving: bind happened without any Serve call.
	assertPortBound(t, addr)
	if strings.Contains(logs.String(), "starting HTTP server") {
		t.Error("bind logged a startup line; that belongs to serve")
	}
}

// TestBindListenersDualSetAssertsTLSConstants proves §D22 at construction time
// and that both intended listeners are bound before anything serves.
func TestBindListenersDualSetAssertsTLSConstants(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)
	httpAddr := fmt.Sprintf("127.0.0.1:%d", webPort)
	httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)

	set, err := bindListeners(tlsConfig(webPort, httpsPort, files), okHandler())
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	t.Cleanup(func() { set.closeAll() })

	if len(set.listeners) != 2 {
		t.Fatalf("listeners = %d, want 2", len(set.listeners))
	}
	if set.listeners[0].scheme != "HTTP" || set.listeners[1].scheme != "HTTPS" {
		t.Fatalf("schemes = %q, %q, want HTTP, HTTPS", set.listeners[0].scheme, set.listeners[1].scheme)
	}
	https := set.listeners[1]
	if https.server.TLSConfig == nil {
		t.Fatal("HTTPS server has no TLSConfig")
	}
	if https.server.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want tls.VersionTLS12 (%d)", https.server.TLSConfig.MinVersion, tls.VersionTLS12)
	}
	if len(https.server.TLSConfig.Certificates) != 1 {
		t.Fatalf("Certificates = %d, want 1 loaded pair", len(https.server.TLSConfig.Certificates))
	}
	if got := len(https.server.TLSConfig.Certificates[0].Certificate); got != 1 {
		t.Errorf("served chain = %d blocks, want the leaf-only file's 1 (§D5: no assembly)", got)
	}
	if https.server.TLSConfig.CipherSuites != nil {
		t.Error("CipherSuites overridden; §D22 keeps Go defaults")
	}
	if https.server.TLSConfig.ClientAuth != tls.NoClientCert {
		t.Errorf("ClientAuth = %v, want mTLS off (§D22)", https.server.TLSConfig.ClientAuth)
	}
	assertPortBound(t, httpAddr)
	assertPortBound(t, httpsAddr)
}

// TestBindListenersHTTPBindFailureIsAtomic: the first bind fails, so nothing was
// ever bound and the intended HTTPS port was never opened.
func TestBindListenersHTTPBindFailureIsAtomic(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)
	occupied, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", webPort))
	if err != nil {
		t.Fatalf("occupy webPort: %v", err)
	}
	defer occupied.Close()

	logs := captureLogs(t)
	set, err := bindListeners(tlsConfig(webPort, httpsPort, files), okHandler())
	if err == nil {
		t.Fatal("expected bind failure when webPort is occupied")
	}
	if set != nil {
		t.Fatal("listener set must be nil when startup fails")
	}
	if !strings.Contains(err.Error(), "bind HTTP") {
		t.Errorf("error = %q, want it to name the HTTP listener (§D19)", err)
	}
	assertPortFree(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))
	if !strings.Contains(logs.String(), "listener setup failed") {
		t.Errorf("startup failure was not logged: %q", logs.String())
	}
}

// TestBindListenersHTTPSBindFailureIsAtomic is the partial-success guard: the
// HTTP listener binds first, the HTTPS bind then fails, and HTTP must NOT be
// left bound or serving (§D9 row "HTTPS port already occupied", §D15.2).
func TestBindListenersHTTPSBindFailureIsAtomic(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)
	occupied, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", httpsPort))
	if err != nil {
		t.Fatalf("occupy tls.port: %v", err)
	}
	defer occupied.Close()

	set, err := bindListeners(tlsConfig(webPort, httpsPort, files), okHandler())
	if err == nil {
		t.Fatal("expected bind failure when tls.port is occupied")
	}
	if set != nil {
		t.Fatal("listener set must be nil when startup fails")
	}
	if !strings.Contains(err.Error(), "bind HTTPS") {
		t.Errorf("error = %q, want it to name the HTTPS listener", err)
	}
	// The already-bound HTTP listener was closed on the way out.
	assertPortFree(t, fmt.Sprintf("127.0.0.1:%d", webPort))
}

// TestBindListenersTLSMaterialFailureIsAtomic covers material that became
// unreadable after §D8 validated it: startup fails, the paths and reason are
// reported, and no listener survives — including the HTTP one bound earlier.
func TestBindListenersTLSMaterialFailureIsAtomic(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)
	if err := os.Remove(files.certPath); err != nil {
		t.Fatalf("remove cert: %v", err)
	}
	logs := captureLogs(t)

	set, err := bindListeners(tlsConfig(webPort, httpsPort, files), okHandler())
	if err == nil {
		t.Fatal("expected startup failure for unreadable certificate")
	}
	if set != nil {
		t.Fatal("listener set must be nil when startup fails")
	}
	if !strings.Contains(err.Error(), files.certPath) {
		t.Errorf("error = %q, want it to name the certificate path", err)
	}
	if strings.Contains(err.Error(), string(files.keyPEM)) {
		t.Errorf("error leaked private key material: %q", err)
	}
	assertPortFree(t, fmt.Sprintf("127.0.0.1:%d", webPort))
	assertPortFree(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))
	if !strings.Contains(logText(logs), files.certPath) {
		t.Errorf("startup failure log must name the path: %q", logs.String())
	}
	if strings.Contains(logText(logs), keyBody(t, files.keyPEM)) {
		t.Errorf("startup failure log leaked key material: %q", logs.String())
	}
}

// keyBody returns a contiguous run of the key's base64 secret material. PEM
// output wraps at 64 characters, so the prefix of the unwrapped body is the
// longest form that appears in both a wrapped PEM log line and a raw one.
func keyBody(t *testing.T, keyPEM []byte) string {
	t.Helper()
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		t.Fatal("test fixture key PEM did not decode")
	}
	body := base64.StdEncoding.EncodeToString(block.Bytes)
	if len(body) < 32 {
		t.Fatalf("test fixture key body too short to assert on: %d bytes", len(body))
	}
	return body[:32]
}

// ---------------------------------------------------------------------------
// §D15.4 — serve-error propagation and coordinated shutdown
// ---------------------------------------------------------------------------

// TestServeErrorCausesCoordinatedShutdown is test obligation 17: forcing one
// Serve to return shuts the sibling listener down and reports one aggregated
// error naming the listener that failed.
func TestServeErrorCausesCoordinatedShutdown(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)
	httpAddr := fmt.Sprintf("127.0.0.1:%d", webPort)

	realHTTPS, err := net.Listen("tcp", httpsAddr)
	if err != nil {
		t.Fatalf("bind sibling: %v", err)
	}
	injected := newInjectedListener(t, "HTTP")
	set := &listenerSet{listeners: []*managedListener{
		{scheme: "HTTP", addr: httpAddr, server: newTestServer(httpAddr, okHandler()), ln: injected},
		{scheme: "HTTPS", addr: httpsAddr, server: newTestServer(httpsAddr, okHandler()), ln: realHTTPS},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := captureLogs(t)
	wait := serveInBackground(t, set, ctx)

	// The sibling is genuinely serving before the failure is injected.
	resp, err := http.Get("http://" + httpsAddr + "/")
	if err != nil {
		// This listener is a plain HTTP server here; TLS is not under test.
		t.Fatalf("sibling not serving: %v", err)
	}
	_ = resp.Body.Close()

	close(injected.release)
	err = wait()
	if err == nil {
		t.Fatal("serve returned no error after a listener was lost")
	}
	if !strings.Contains(err.Error(), "serve HTTP: injected accept failure") {
		t.Errorf("error = %q, want the serve HTTP line (§D19 distinguishes the listener)", err)
	}
	// The sibling is not left serving: its port is released.
	assertPortFree(t, httpsAddr)
	if _, err := net.DialTimeout("tcp", httpsAddr, 200*time.Millisecond); err == nil {
		t.Error("sibling HTTPS listener still accepts connections")
	}
	// The failed listener is shut down through the same path as the healthy one
	// (§D15.5), and its already-closed listener never turns into a shutdown error
	// that would bury the serve failure.
	if !injected.closed.Load() {
		t.Error("failed listener was not closed")
	}
	// §D15.5 is one Shutdown path for the whole set: the lost listener goes
	// through the same logged, deadline-shared stop as the healthy sibling.
	if text := logText(logs); !strings.Contains(text, "shutting down HTTP server") ||
		!strings.Contains(text, "shutting down HTTPS server") {
		t.Errorf("coordinated shutdown did not cover both listeners: %q", text)
	}
	if strings.Contains(err.Error(), "shutdown HTTP") {
		t.Errorf("aggregated error buried the serve failure in shutdown noise: %q", err)
	}
	// Exactly one failure is reported.
	if got := strings.Count(err.Error(), "serve "); got != 1 {
		t.Errorf("aggregated error has %d serve lines, want 1: %q", got, err)
	}
}

// TestServeHTTPSOnlyErrorNamesHTTPS proves the naming works for the TLS
// listener too (§D19 "Shutdown/serve logs distinguish which listener errored").
func TestServeHTTPSOnlyErrorNamesHTTPS(t *testing.T) {
	webPort := freeTCPPort(t)
	httpAddr := fmt.Sprintf("127.0.0.1:%d", webPort)
	httpsAddr := "127.0.0.1:18443"

	realHTTP, err := net.Listen("tcp", httpAddr)
	if err != nil {
		t.Fatalf("bind HTTP: %v", err)
	}
	injected := newInjectedListener(t, "HTTPS")
	set := &listenerSet{listeners: []*managedListener{
		{scheme: "HTTP", addr: httpAddr, server: newTestServer(httpAddr, okHandler()), ln: realHTTP},
		{scheme: "HTTPS", addr: httpsAddr, server: newTestServer(httpsAddr, okHandler()), ln: injected},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := captureLogs(t)
	wait := serveInBackground(t, set, ctx)
	close(injected.release)

	err = wait()
	if err == nil || !strings.Contains(err.Error(), "serve HTTPS: injected accept failure") {
		t.Fatalf("error = %v, want the serve HTTPS line", err)
	}
	if !strings.Contains(logs.String(), "HTTPS server error") {
		t.Errorf("serve error was not logged per listener: %q", logs.String())
	}
	assertPortFree(t, httpAddr)
}

// TestAggregateFailuresOrderAndDedup pins the reported-error contract directly,
// because which listener's own Serve error survives a race is decided inside
// http.Server: the aggregate must name listeners in configured order, at most
// once each, with the shutdown outcomes after the cause that produced them.
func TestAggregateFailuresOrderAndDedup(t *testing.T) {
	set := &listenerSet{listeners: []*managedListener{{scheme: "HTTP"}, {scheme: "HTTPS"}}}

	err := set.aggregateFailures(
		[]serveFailure{
			{scheme: "HTTPS", err: errors.New("https accept loop died")},
			{scheme: "HTTPS", err: errors.New("https reported a second time")},
			{scheme: "HTTP", err: errors.New("http accept loop died")},
		},
		// Shaped exactly as shutdownAll builds them: "shutdown <scheme>: <cause>".
		[]error{nil, fmt.Errorf("shutdown HTTPS: %w", errors.New("https stop held past the deadline"))},
	)
	if err == nil {
		t.Fatal("a lost listener set must report an error")
	}
	text := err.Error()
	httpAt := strings.Index(text, "serve HTTP: http accept loop died")
	httpsAt := strings.Index(text, "serve HTTPS: https accept loop died")
	if httpAt < 0 || httpsAt < 0 {
		t.Fatalf("aggregate lost a listener: %q", text)
	}
	if httpAt > httpsAt {
		t.Errorf("aggregate order = %q, want the configured HTTP-before-HTTPS order", text)
	}
	if got := strings.Count(text, "serve HTTPS:"); got != 1 {
		t.Errorf("aggregate repeats a lost listener %d times, want one line each: %q", got, text)
	}
	if strings.Contains(text, "https reported a second time") {
		t.Errorf("aggregate reported a second failure for one listener: %q", text)
	}
	if stopAt := strings.Index(text, "shutdown HTTPS:"); stopAt < httpsAt {
		t.Errorf("the shutdown outcome must follow the serve cause that triggered it: %q", text)
	}

	if got := set.aggregateFailures(nil, []error{nil, nil}); got != nil {
		t.Errorf("a clean stop aggregated to %v, want nil", got)
	}
}

// TestBothServeFailuresReportTheCause: with both accept loops lost the caller
// still gets a non-nil error naming a listener cause. Which listener's own error
// survives is up to Go — Serve reports ErrServerClosed for a listener that dies
// after the coordinated shutdown already began — so this asserts the invariant,
// not an exact line count.
func TestBothServeFailuresReportTheCause(t *testing.T) {
	httpInjected := newInjectedListener(t, "HTTP")
	httpsInjected := newInjectedListener(t, "HTTPS")
	set := &listenerSet{listeners: []*managedListener{
		{scheme: "HTTP", addr: httpInjected.addr.String(), server: newTestServer("http", okHandler()), ln: httpInjected},
		{scheme: "HTTPS", addr: httpsInjected.addr.String(), server: newTestServer("https", okHandler()), ln: httpsInjected},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := serveInBackground(t, set, ctx)

	close(httpInjected.release)
	time.Sleep(50 * time.Millisecond)
	close(httpsInjected.release)

	err := wait()
	if err == nil {
		t.Fatal("expected an aggregated error")
	}
	text := err.Error()
	if !strings.Contains(text, "serve HTTP: injected accept failure") &&
		!strings.Contains(text, "serve HTTPS: injected accept failure") {
		t.Errorf("aggregate names no lost listener cause: %q", text)
	}
}

// TestShutdownWithAlreadyReturnedServe: a listener that already returned must
// not turn a context cancellation into a spurious shutdown error, and the
// surviving listener still shuts down cleanly.
func TestShutdownWithAlreadyReturnedServe(t *testing.T) {
	httpInjected := newInjectedListener(t, "HTTP")
	httpsPort := freeTCPPort(t)
	httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)
	realHTTPS, err := net.Listen("tcp", httpsAddr)
	if err != nil {
		t.Fatalf("bind HTTPS: %v", err)
	}
	set := &listenerSet{listeners: []*managedListener{
		{scheme: "HTTP", addr: "injected-http:1", server: newTestServer("injected", okHandler()), ln: httpInjected},
		{scheme: "HTTPS", addr: httpsAddr, server: newTestServer(httpsAddr, okHandler()), ln: realHTTPS},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	wait := serveInBackground(t, set, ctx)
	close(httpInjected.release)
	// Give the failure path time to run, then cancel as a caller would.
	time.Sleep(50 * time.Millisecond)
	cancel()

	err = wait()
	if err == nil || !strings.Contains(err.Error(), "serve HTTP: injected accept failure") {
		t.Fatalf("error = %v, want only the serve failure", err)
	}
	if strings.Contains(err.Error(), "shutdown") {
		t.Errorf("the already-returned listener reported a shutdown error: %q", err)
	}
	assertPortFree(t, httpsAddr)
}

// TestContextCancelIsNotAFailure is the §D15 rule that http.ErrServerClosed is
// the coordinated shutdown already in progress, never a reported error.
func TestContextCancelIsNotAFailure(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)
	logs := captureLogs(t)

	set, err := bindListeners(tlsConfig(webPort, httpsPort, files), okHandler())
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	wait := serveInBackground(t, set, ctx)
	waitServing(t, set)

	cancel()
	if err := wait(); err != nil {
		t.Fatalf("normal stop reported an error: %v", err)
	}
	assertPortFree(t, fmt.Sprintf("127.0.0.1:%d", webPort))
	assertPortFree(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))

	// Both listeners shut down concurrently under the single shared deadline,
	// so a normal stop never reaches the deadline at all.
	text := logs.String()
	if !strings.Contains(text, "shutting down HTTP server") || !strings.Contains(text, "shutting down HTTPS server") {
		t.Errorf("shutdown was not logged per listener: %q", text)
	}
	if strings.Contains(text, "server shutdown error") {
		t.Errorf("a clean stop logged a shutdown error: %q", text)
	}
}

// TestNoListenerGoroutineLeak: after serve returns, every Serve goroutine is
// gone. A stuck goroutine here would be the leak the atomic shutdown must not
// introduce.
func TestNoListenerGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)
	set, err := bindListeners(tlsConfig(webPort, httpsPort, files), okHandler())
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	wait := serveInBackground(t, set, ctx)
	waitServing(t, set)
	cancel()
	if err := wait(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines leaked: before=%d after=%d", before, runtime.NumGoroutine())
}

// ---------------------------------------------------------------------------
// §D15.5 — one shared shutdown deadline
// ---------------------------------------------------------------------------

// TestSharedShutdownDeadlineWithOpenStreams is test obligation 7: two listeners
// with open streaming connections stop within ONE deadline, not one each, and
// both outcomes are aggregated.
func TestSharedShutdownDeadlineWithOpenStreams(t *testing.T) {
	defer resetDeadline(t, 500*time.Millisecond)()

	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	releaseOnce := &sync.Once{}
	unblockHandlers := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblockHandlers)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = w.Write([]byte("done"))
	})

	set, err := bindListeners(tlsConfig(webPort, httpsPort, files), handler)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	wait := serveInBackground(t, set, ctx)
	waitServing(t, set)

	httpAddr := fmt.Sprintf("127.0.0.1:%d", webPort)
	httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)
	var wg sync.WaitGroup
	for _, url := range []string{"http://" + httpAddr + "/", "https://" + httpsAddr + "/"} {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			resp, err := client.Get(url)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}(url)
	}
	for range []int{0, 1} {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("streaming handler was never entered on both listeners")
		}
	}

	start := time.Now()
	cancel()
	err = wait()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Shutdown was still holding active connections, so an error is expected")
	}
	text := err.Error()
	if !strings.Contains(text, "shutdown HTTP") || !strings.Contains(text, "shutdown HTTPS") {
		t.Fatalf("aggregate lost a listener shutdown outcome: %q", text)
	}
	// One shared deadline means ~1x, not ~2x. A sequential implementation would
	// need at least 2*deadline here.
	if elapsed < 300*time.Millisecond {
		t.Errorf("stop finished in %v, below the shared deadline — the streams were not waited on", elapsed)
	}
	if elapsed >= 900*time.Millisecond {
		t.Errorf("stop took %v with a 500ms deadline — the deadlines were not shared (§D15.5)", elapsed)
	}
	t.Logf("shared-deadline stop took %v for two blocked listeners", elapsed)
	// Release the handlers only after the stop was measured: they are what keeps
	// Shutdown busy until the shared deadline expires.
	unblockHandlers()
	wg.Wait()
}

func resetDeadline(t *testing.T, d time.Duration) func() {
	t.Helper()
	prev := listenerShutdownDeadline
	listenerShutdownDeadline = d
	return func() { listenerShutdownDeadline = prev }
}

// ---------------------------------------------------------------------------
// Real serving over both listeners (§D15.3, §D22, §D5)
// ---------------------------------------------------------------------------

// TestServeBothListenersHandshakeAndRequest proves the dual set actually serves:
// plain HTTP over the existing port and a real TLS handshake on tls.port at
// TLS 1.2 or above.
func TestServeBothListenersHandshakeAndRequest(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)
	httpAddr := fmt.Sprintf("127.0.0.1:%d", webPort)
	httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)

	set, err := bindListeners(tlsConfig(webPort, httpsPort, files), okHandler())
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := serveInBackground(t, set, ctx)
	waitServing(t, set)

	body := httpGET(t, "http://"+httpAddr+"/")
	if body != "ok" {
		t.Errorf("HTTP body = %q, want ok", body)
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", httpsAddr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("TLS handshake: %v", err)
	}
	state := conn.ConnectionState()
	_ = conn.Close()
	if state.Version < tls.VersionTLS12 {
		t.Errorf("negotiated version = 0x%x, want >= TLS 1.2", state.Version)
	}
	body = httpsGET(t, "https://"+httpsAddr+"/")
	if body != "ok" {
		t.Errorf("HTTPS body = %q, want ok", body)
	}

	// tls.port must not answer plaintext: ServeTLS dispatch is the difference
	// between a secure origin and an open HTTP port wearing a certificate.
	// A plaintext request to tls.port must never reach the application. Go's TLS
	// listener answers this exact mistake with a 400 page, so what is under test
	// is whether the handler was reached, not whether the transport errored.
	plainResp, err := (&http.Client{Timeout: 3 * time.Second}).Get("http://" + httpsAddr + "/")
	if err == nil {
		plainBody, _ := io.ReadAll(plainResp.Body)
		_ = plainResp.Body.Close()
		if plainResp.StatusCode == http.StatusOK || string(plainBody) == "ok" {
			t.Errorf("tls.port served the application over plaintext: status %d body %q", plainResp.StatusCode, plainBody)
		}
	}

	cancel()
	if err := wait(); err != nil {
		t.Fatalf("dual-listener stop reported an error: %v", err)
	}
	assertPortFree(t, httpAddr)
	assertPortFree(t, httpsAddr)
}

// TestServeRefusesBelowTLS12 exercises the §D22 minimum from the client side.
func TestServeRefusesBelowTLS12(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)
	httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)

	set, err := bindListeners(tlsConfig(webPort, httpsPort, files), okHandler())
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := serveInBackground(t, set, ctx)
	waitServing(t, set)

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	_, err = tls.DialWithDialer(dialer, "tcp", httpsAddr, &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS10,
		MaxVersion:         tls.VersionTLS11,
	})
	if err == nil {
		t.Fatal("a TLS 1.0/1.1-only client completed a handshake; §D22 minimum is not enforced")
	}
	// The refusal must be a TLS-level rejection, not a transport accident.
	if !strings.Contains(strings.ToLower(err.Error()), "tls") {
		t.Errorf("handshake failed for a non-TLS reason: %v", err)
	}

	cancel()
	if err := wait(); err != nil {
		t.Fatalf("stop after a refused handshake reported an error: %v", err)
	}
}

// TestServedChainMatchesFile is test obligation 16: what GoAl presents is
// exactly what the file carries — leaf only, or leaf then intermediate, with no
// assembly or reordering.
func TestServedChainMatchesFile(t *testing.T) {
	cases := []struct {
		name       string
		chain      bool
		wantBlocks int
	}{
		{"leaf only", false, 1},
		{"leaf plus intermediate", true, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			webPort := freeTCPPort(t)
			httpsPort := freeTCPPort(t)
			ca := newTestCert(t, "goal-webui-ca", true, nil)
			leaf := newTestCert(t, "goal-webui", false, ca)
			files := writeTLSFiles(t, leaf, ca)
			if tc.chain {
				if err := os.WriteFile(files.certPath, files.leafFirst, 0o600); err != nil {
					t.Fatalf("write chain: %v", err)
				}
			}
			httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)

			set, err := bindListeners(tlsConfig(webPort, httpsPort, files), okHandler())
			if err != nil {
				t.Fatalf("bind: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wait := serveInBackground(t, set, ctx)
			waitServing(t, set)

			var presented int
			var firstIsLeaf bool
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			conn, err := tls.DialWithDialer(dialer, "tcp", httpsAddr, &tls.Config{
				InsecureSkipVerify: true,
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
					presented = len(rawCerts)
					if len(rawCerts) > 0 {
						if parsed, err := x509.ParseCertificate(rawCerts[0]); err == nil {
							firstIsLeaf = parsed.Subject.CommonName == "goal-webui"
						}
					}
					return nil
				},
			})
			if err != nil {
				t.Fatalf("handshake: %v", err)
			}
			_ = conn.Close()

			if presented != tc.wantBlocks {
				t.Errorf("server presented %d certificate(s), want %d (file order, no assembly)", presented, tc.wantBlocks)
			}
			if !firstIsLeaf {
				t.Error("first presented certificate is not the leaf (§D5 leaf-first order)")
			}
			cancel()
			if err := wait(); err != nil {
				t.Fatalf("stop reported an error: %v", err)
			}
		})
	}
}

func httpGET(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", url, resp.StatusCode)
	}
	return string(body)
}

func httpsGET(t *testing.T, url string) string {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", url, resp.StatusCode)
	}
	return string(body)
}

// waitServing confirms every bound listener is accepting before the test acts.
func waitServing(t *testing.T, set *listenerSet) {
	t.Helper()
	for _, l := range set.listeners {
		deadline := time.Now().Add(5 * time.Second)
		for {
			conn, err := net.DialTimeout("tcp", l.addr, 250*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s listener on %s never started accepting: %v", l.scheme, l.addr, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// ---------------------------------------------------------------------------
// §D19 — startup diagnostics and log privacy
// ---------------------------------------------------------------------------

// TestStartupLogNamesBothListenersSeparately checks the §D19 startup lines and
// that no key material ever reaches the log across the whole startup matrix.
func TestStartupLogNamesBothListenersSeparately(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)
	httpAddr := fmt.Sprintf("127.0.0.1:%d", webPort)
	httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)

	logs := captureLogs(t)
	set, err := bindListeners(tlsConfig(webPort, httpsPort, files), okHandler())
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	wait := serveInBackground(t, set, ctx)
	waitServing(t, set)
	cancel()
	if err := wait(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	text := logText(logs)
	if !strings.Contains(text, `msg="starting HTTP server"`) || !strings.Contains(text, `addr=`+httpAddr) {
		t.Errorf("HTTP startup line missing: %q", text)
	}
	if !strings.Contains(text, `msg="starting HTTPS server"`) {
		t.Errorf("HTTPS startup line missing: %q", text)
	}
	// The path is asserted through its file name: slog.TextHandler quotes values
	// containing spaces, which would make an exact full-path match environment
	// dependent while the name is always present.
	for _, want := range []string{"addr=" + httpsAddr, "tls=enabled", "cert=", filepath.Base(files.certPath), "expires=", "san=DNS:goal-webui,DNS:localhost,IP:127.0.0.1"} {
		if !strings.Contains(text, want) {
			t.Errorf("HTTPS startup line is missing %q\nlog: %q", want, text)
		}
	}
	if !strings.Contains(text, "cert="+files.certPath) && !strings.Contains(text, `cert="`+files.certPath+`"`) {
		t.Errorf("HTTPS startup line does not name the configured certificate path: %q", text)
	}
	if strings.Contains(text, keyBody(t, files.keyPEM)) {
		t.Fatal("private key material appeared in the log (test obligation 8)")
	}
	if strings.Contains(text, string(files.keyPEM)) {
		t.Fatal("private key PEM appeared in the log (test obligation 8)")
	}
}

// TestMismatchedPairLogsNoKeyMaterial covers the §D9 mismatch row on the serving
// path: the message names the pair, never the content.
func TestMismatchedPairLogsNoKeyMaterial(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leafA := newTestCert(t, "goal-webui-a", false, nil)
	leafB := newTestCert(t, "goal-webui-b", false, nil)
	dir := t.TempDir()
	certPath := filepath.Join(dir, "a.crt")
	keyPath := filepath.Join(dir, "b.key")
	if err := os.WriteFile(certPath, leafA.certPEM(), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, leafB.keyPEM(t), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	cfg := baseConfig(webPort)
	port := httpsPort
	cfg.TLS = &config.TLSConfig{Enabled: true, Port: &port, CertFile: certPath, KeyFile: keyPath}

	logs := captureLogs(t)
	set, err := bindListeners(cfg, okHandler())
	if err == nil {
		t.Fatal("expected startup failure for a mismatched certificate/key pair")
	}
	if set != nil {
		t.Fatal("listener set must be nil when startup fails")
	}
	text := logText(logs)
	if strings.Contains(text, keyBody(t, leafB.keyPEM(t))) || strings.Contains(text, string(leafB.keyPEM(t))) {
		t.Errorf("mismatch log leaked key material: %q", logs.String())
	}
	if !strings.Contains(text, certPath) || !strings.Contains(text, keyPath) {
		t.Errorf("mismatch log must name both paths: %q", logs.String())
	}
	assertPortFree(t, fmt.Sprintf("127.0.0.1:%d", webPort))
	assertPortFree(t, fmt.Sprintf("127.0.0.1:%d", httpsPort))
}

// TestCertificateSANsRendering keeps the san= field honest, including the
// name-less case GoAl still refuses to validate (§D17).
func TestCertificateSANsRendering(t *testing.T) {
	leaf := newTestCert(t, "goal-webui", false, nil)
	// Render from the DER, exactly as the server does, not from the template.
	parsedLeaf, err := x509.ParseCertificate(leaf.der)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	got := certificateSANs(parsedLeaf)
	if got != "DNS:goal-webui,DNS:localhost,IP:127.0.0.1" {
		t.Errorf("san = %q, want the DNS and IP entries of the leaf", got)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(42),
		Subject:               pkix.Name{CommonName: "no-san"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := certificateSANs(parsed); got != "none" {
		t.Errorf("san for a certificate without subjectAltNames = %q, want none", got)
	}
}

// ---------------------------------------------------------------------------
// Caller contract (§D15.4, §D21 shared path) — through App.Run
// ---------------------------------------------------------------------------

func TestRunRequiresRegistry(t *testing.T) {
	app := &App{cfg: baseConfig(freeTCPPort(t))}
	if err := app.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "route registry not initialized") {
		t.Fatalf("err = %v, want the registry guard to stay first", err)
	}
}

// TestRunHTTPEndToEndIsUnchanged is the §D24 compatibility baseline at the
// caller-contract level: no tls block, one listener, real registry, clean nil
// return on cancellation.
func TestRunHTTPEndToEndIsUnchanged(t *testing.T) {
	webPort := freeTCPPort(t)
	cfg := baseConfig(webPort)
	cfg.DataDir = t.TempDir()
	app := newLifecycleApp(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	waitAppServing(t, fmt.Sprintf("127.0.0.1:%d", webPort))

	body := httpGET(t, fmt.Sprintf("http://127.0.0.1:%d/api/v1/version", webPort))
	if !strings.Contains(body, "\"version\"") {
		t.Errorf("version endpoint body = %q", body)
	}

	cancel()
	if err := waitAppResult(t, done); err != nil {
		t.Fatalf("Run returned an error on a clean HTTP-only stop: %v", err)
	}
	assertPortFree(t, fmt.Sprintf("127.0.0.1:%d", webPort))
}

// TestRunWithTLSBothListenersEndToEnd is the slice's acceptance shape: one
// process, one registry, both listeners serving real endpoints, and a stop that
// still returns nil.
func TestRunWithTLSBothListenersEndToEnd(t *testing.T) {
	webPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)

	cfg := tlsConfig(webPort, httpsPort, files)
	cfg.DataDir = t.TempDir()
	app := newLifecycleApp(t, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	httpAddr := fmt.Sprintf("127.0.0.1:%d", webPort)
	httpsAddr := fmt.Sprintf("127.0.0.1:%d", httpsPort)
	waitAppServing(t, httpAddr)
	waitAppServing(t, httpsAddr)

	body := httpGET(t, "http://"+httpAddr+"/api/v1/version")
	if !strings.Contains(body, "\"version\"") {
		t.Errorf("HTTP version body = %q", body)
	}
	secureBody := httpsGET(t, "https://"+httpsAddr+"/api/v1/version")
	if secureBody != body {
		t.Errorf("HTTPS served %q while HTTP served %q; both listeners share one router", secureBody, body)
	}

	cancel()
	if err := waitAppResult(t, done); err != nil {
		t.Fatalf("Run returned an error on a clean dual-listener stop: %v", err)
	}
	assertPortFree(t, httpAddr)
	assertPortFree(t, httpsAddr)
}

// TestRunTLSToSameAddressFailsWithoutServing models the §D9 port-collision row
// at the bind layer when it is reached with a hand-built config: the second bind
// fails and nothing is left serving.
func TestRunTLSToSameAddressFailsWithoutServing(t *testing.T) {
	webPort := freeTCPPort(t)
	leaf := newTestCert(t, "goal-webui", false, nil)
	files := writeTLSFiles(t, leaf, nil)
	cfg := tlsConfig(webPort, webPort, files)
	cfg.DataDir = t.TempDir()
	app := newLifecycleApp(t, cfg)

	err := app.Run(context.Background())
	if err == nil {
		t.Fatal("expected startup failure when tls.port collides with webPort")
	}
	if !strings.Contains(err.Error(), "bind HTTPS") {
		t.Errorf("err = %q, want it to name the HTTPS listener", err)
	}
	assertPortFree(t, fmt.Sprintf("127.0.0.1:%d", webPort))
}

func newLifecycleApp(t *testing.T, cfg *config.Config) *App {
	t.Helper()
	repoPath := filepath.Join(t.TempDir(), "goal_repo.json")
	repo, err := storage.NewJSONRepository(repoPath)
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	supervisor := process.NewSupervisorWithContext(context.Background(), repo)
	app, err := NewApp(cfg, repo, supervisor)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	app.SetConfigPath(filepath.Join(t.TempDir(), "goal.json"))
	app.InitRegistry()
	t.Cleanup(func() { _ = app.CloseAudit() })
	return app
}

func waitAppServing(t *testing.T, addr string) {
	t.Helper()
	waitServing(t, &listenerSet{listeners: []*managedListener{{scheme: "app", addr: addr}}})
}

func waitAppResult(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("App.Run did not return within 15s")
		return nil
	}
}

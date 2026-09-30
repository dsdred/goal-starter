package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	tlsTestWebPort   = 18080
	tlsTestHTTPSPort = 18443
)

// issuedCert is a generated test certificate with its private key.
type issuedCert struct {
	cert *x509.Certificate
	der  []byte
	key  *ecdsa.PrivateKey
}

// tlsFixture holds generated PEM material written into a temp dir.
type tlsFixture struct {
	certPath string
	keyPath  string
	certPEM  []byte
	keyPEM   []byte
}

func newTLSCert(t *testing.T, cn string, notBefore, notAfter time.Time, isCA bool, parent *issuedCert) *issuedCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"GoAl config tests"}},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
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

func writeTLSFiles(t *testing.T, certPEM, keyPEM []byte) *tlsFixture {
	t.Helper()
	dir := t.TempDir()
	f := &tlsFixture{
		certPath: filepath.Join(dir, "goal.crt"),
		keyPath:  filepath.Join(dir, "goal.key"),
		certPEM:  certPEM,
		keyPEM:   keyPEM,
	}
	if err := os.WriteFile(f.certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(f.keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return f
}

// validTLSFixture generates a currently-valid leaf/key pair.
func validTLSFixture(t *testing.T) *tlsFixture {
	t.Helper()
	c := newTLSCert(t, "goal-test", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), false, nil)
	keyPEM := c.keyPEM(t)
	return writeTLSFiles(t, c.certPEM(), keyPEM)
}

func enabledTLS(f *tlsFixture, port int) *TLSConfig {
	return &TLSConfig{Enabled: true, Port: ptrInt(port), CertFile: f.certPath, KeyFile: f.keyPath}
}

// ptrInt builds an explicit tls.port value; a nil *int is an absent key.
func ptrInt(i int) *int { return &i }

func tlsTestConfig(block *TLSConfig) Config {
	cfg := Default()
	cfg.WebPort = tlsTestWebPort
	cfg.TLS = block
	return cfg
}

// pemBodySample returns a distinctive 24-character window of a PEM body, used to
// prove that generated key material never reaches an error string or a log line.
func pemBodySample(pemBytes []byte) string {
	var body strings.Builder
	for _, line := range strings.Split(string(pemBytes), "\n") {
		if strings.HasPrefix(line, "-----") {
			continue
		}
		body.WriteString(strings.TrimSpace(line))
	}
	s := body.String()
	if len(s) > 24 {
		s = s[:24]
	}
	return s
}

func assertNoPEMMaterial(t *testing.T, msg string, blocks ...[]byte) {
	t.Helper()
	if strings.Contains(msg, "BEGIN ") {
		t.Fatalf("diagnostic carries a PEM marker: %q", msg)
	}
	for i, b := range blocks {
		if sample := pemBodySample(b); sample != "" && strings.Contains(msg, sample) {
			t.Errorf("diagnostic %q leaks generated material block %d", msg, i)
		}
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	fn()
	// Captured writes stay far below the pipe buffer, so closing before reading
	// cannot block.
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	os.Stderr = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

// TestValidateTLS_AbsentBlockIsUnchanged is the backward-compatibility baseline:
// no tls key means today's behavior, with no new error and no new warning.
func TestValidateTLS_AbsentBlockIsUnchanged(t *testing.T) {
	cfg := tlsTestConfig(nil)
	var gotErr error
	stderr := captureStderr(t, func() { gotErr = cfg.ValidateFull() })
	if gotErr != nil {
		t.Fatalf("ValidateFull() = %v, want unchanged success without a tls block", gotErr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want no tls diagnostics when the block is absent", stderr)
	}
}

// TestValidateTLS_SemanticsMatrix covers every row of the ADR 019 §D3 zero/absent
// table plus the §D9 disabled-and-stray-values row: fail / warn / proceed.
func TestValidateTLS_SemanticsMatrix(t *testing.T) {
	f := validTLSFixture(t)
	missingCert := filepath.Join(t.TempDir(), "absent.crt")
	missingKey := filepath.Join(t.TempDir(), "absent.key")

	cases := []struct {
		name        string
		block       *TLSConfig
		wantErr     string
		wantNoErr   string
		wantWarning bool
	}{
		{name: "tls key absent", block: nil},
		{name: "empty object", block: &TLSConfig{}},
		{name: "explicit false", block: &TLSConfig{Enabled: false}},
		{name: "false with an explicit zero port stays silent", block: &TLSConfig{Enabled: false, Port: ptrInt(0)}},
		{
			name:        "false with inert values warns",
			block:       &TLSConfig{Enabled: false, Port: ptrInt(tlsTestHTTPSPort), CertFile: f.certPath, KeyFile: f.keyPath},
			wantWarning: true,
		},
		{
			name:      "enabled with absent port",
			block:     &TLSConfig{Enabled: true, CertFile: f.certPath, KeyFile: f.keyPath},
			wantErr:   "tls.port is required when tls.enabled is true",
			wantNoErr: "out of range",
		},
		{
			name:      "enabled with explicit zero port",
			block:     &TLSConfig{Enabled: true, Port: ptrInt(0), CertFile: f.certPath, KeyFile: f.keyPath},
			wantErr:   "tls.port out of range 1-65535, got 0",
			wantNoErr: "tls.port is required",
		},
		{
			name:    "enabled with negative port",
			block:   &TLSConfig{Enabled: true, Port: ptrInt(-1), CertFile: f.certPath, KeyFile: f.keyPath},
			wantErr: "tls.port out of range 1-65535",
		},
		{
			name:    "enabled with port above range",
			block:   &TLSConfig{Enabled: true, Port: ptrInt(70000), CertFile: f.certPath, KeyFile: f.keyPath},
			wantErr: "tls.port out of range 1-65535",
		},
		{
			name:    "enabled without certFile",
			block:   &TLSConfig{Enabled: true, Port: ptrInt(tlsTestHTTPSPort), KeyFile: f.keyPath},
			wantErr: "tls.certFile is required",
		},
		{
			name:    "enabled without keyFile",
			block:   &TLSConfig{Enabled: true, Port: ptrInt(tlsTestHTTPSPort), CertFile: f.certPath},
			wantErr: "tls.keyFile is required",
		},
		{
			name:    "enabled with missing cert file",
			block:   &TLSConfig{Enabled: true, Port: ptrInt(tlsTestHTTPSPort), CertFile: missingCert, KeyFile: f.keyPath},
			wantErr: "tls.certFile " + missingCert + " is not readable",
		},
		{
			name:    "enabled with missing key file",
			block:   &TLSConfig{Enabled: true, Port: ptrInt(tlsTestHTTPSPort), CertFile: f.certPath, KeyFile: missingKey},
			wantErr: "tls.keyFile " + missingKey + " is not readable",
		},
		{
			name:  "enabled with a complete valid block",
			block: enabledTLS(f, tlsTestHTTPSPort),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tlsTestConfig(tc.block)
			var gotErr error
			stderr := captureStderr(t, func() { gotErr = cfg.ValidateFull() })
			t.Logf("ValidateFull() error = %v; warning line = %q", gotErr, strings.TrimSpace(stderr))

			if tc.wantErr == "" {
				if gotErr != nil {
					t.Fatalf("ValidateFull() = %v, want no error", gotErr)
				}
			} else if gotErr == nil {
				t.Fatalf("ValidateFull() = nil, want error containing %q", tc.wantErr)
			} else if !strings.Contains(gotErr.Error(), tc.wantErr) {
				t.Errorf("ValidateFull() error = %q, want it to contain %q", gotErr, tc.wantErr)
			}
			if tc.wantNoErr != "" && gotErr != nil && strings.Contains(gotErr.Error(), tc.wantNoErr) {
				t.Errorf("ValidateFull() error = %q, must not contain %q", gotErr, tc.wantNoErr)
			}

			warned := strings.Contains(stderr, "tls configured but disabled")
			if warned != tc.wantWarning {
				t.Errorf("warning emitted = %v (stderr %q), want %v", warned, stderr, tc.wantWarning)
			}
		})
	}
}

// TestValidateTLS_PortCollisionNamesBothFields is test obligation 2.
func TestValidateTLS_PortCollisionNamesBothFields(t *testing.T) {
	f := validTLSFixture(t)
	cfg := tlsTestConfig(&TLSConfig{Enabled: true, Port: ptrInt(tlsTestWebPort), CertFile: f.certPath, KeyFile: f.keyPath})
	err := cfg.ValidateFull()
	if err == nil {
		t.Fatal("ValidateFull() = nil, want tls.port == webPort rejected")
	}
	msg := err.Error()
	for _, want := range []string{"tls.port", "webPort", strconv.Itoa(tlsTestWebPort)} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q must name both fields and their value, missing %q", msg, want)
		}
	}
}

// TestValidateTLS_RelativePathsRejected is test obligation 5 (§D5-D7).
func TestValidateTLS_RelativePathsRejected(t *testing.T) {
	f := validTLSFixture(t)
	cases := []struct {
		name     string
		certFile string
		keyFile  string
		want     string
	}{
		{"relative certFile", filepath.Join("certs", "goal.crt"), f.keyPath, "tls.certFile must be an absolute path"},
		{"relative keyFile", f.certPath, filepath.Join("certs", "goal.key"), "tls.keyFile must be an absolute path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tlsTestConfig(&TLSConfig{Enabled: true, Port: ptrInt(tlsTestHTTPSPort), CertFile: tc.certFile, KeyFile: tc.keyFile})
			err := cfg.ValidateFull()
			if err == nil {
				t.Fatal("ValidateFull() = nil, want relative path rejected")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
		})
	}

	if err := tlsTestConfig(enabledTLS(f, tlsTestHTTPSPort)).ValidateFull(); err != nil {
		t.Errorf("absolute paths rejected: %v", err)
	}
}

// TestValidateTLS_InvalidMaterialFailsClosed covers the §D9 rows for invalid PEM,
// pair mismatch, expiry and a not-yet-valid certificate (obligations 3 and 4).
func TestValidateTLS_InvalidMaterialFailsClosed(t *testing.T) {
	f := validTLSFixture(t)
	other := newTLSCert(t, "goal-other", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), false, nil)
	notYet := newTLSCert(t, "goal-future", time.Now().Add(2*time.Hour), time.Now().Add(24*time.Hour), false, nil)
	expired := newTLSCert(t, "goal-past", time.Now().Add(-48*time.Hour), time.Now().Add(-2*time.Hour), false, nil)

	cases := []struct {
		name           string
		certPEM        []byte
		keyPEM         []byte
		wantMsg        string
		namesBothPaths bool
	}{
		{name: "malformed certificate PEM", certPEM: []byte("this is not a pem block\n"), keyPEM: f.keyPEM, wantMsg: "failed to load", namesBothPaths: true},
		{name: "malformed key PEM", certPEM: f.certPEM, keyPEM: []byte("garbage key\n"), wantMsg: "failed to load", namesBothPaths: true},
		{name: "key placed in cert file", certPEM: f.keyPEM, keyPEM: f.keyPEM, wantMsg: "failed to load", namesBothPaths: true},
		{name: "certificate and key mismatch", certPEM: other.certPEM(), keyPEM: f.keyPEM, wantMsg: "does not match", namesBothPaths: true},
		{name: "expired certificate", certPEM: expired.certPEM(), keyPEM: expired.keyPEM(t), wantMsg: "has expired"},
		{name: "not yet valid certificate", certPEM: notYet.certPEM(), keyPEM: notYet.keyPEM(t), wantMsg: "is not yet valid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := writeTLSFiles(t, tc.certPEM, tc.keyPEM)
			cfg := tlsTestConfig(enabledTLS(bad, tlsTestHTTPSPort))
			err := cfg.ValidateFull()
			if err == nil {
				t.Fatal("ValidateFull() = nil, want startup to fail")
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", msg, tc.wantMsg)
			}
			// Failure messages name the path and the reason, never the contents (§D19).
			if !strings.Contains(msg, bad.certPath) {
				t.Errorf("error = %q, want it to name cert path %q", msg, bad.certPath)
			}
			if tc.namesBothPaths && !strings.Contains(msg, bad.keyPath) {
				t.Errorf("error = %q, want it to name key path %q", msg, bad.keyPath)
			}
			assertNoPEMMaterial(t, msg, bad.certPEM, bad.keyPEM)
		})
	}
}

// TestValidateTLS_DiagnosticsNeverLeakKeyMaterial is the config-half of test
// obligation 8: across the failure matrix no key material reaches an error or a
// warning line.
func TestValidateTLS_DiagnosticsNeverLeakKeyMaterial(t *testing.T) {
	f := validTLSFixture(t)
	other := newTLSCert(t, "goal-other", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), false, nil)
	mismatch := writeTLSFiles(t, other.certPEM(), f.keyPEM)
	garbage := writeTLSFiles(t, f.keyPEM, f.keyPEM)
	unreadable := tlsTestConfig(&TLSConfig{Enabled: true, Port: ptrInt(tlsTestHTTPSPort), CertFile: f.certPath, KeyFile: filepath.Join(t.TempDir(), "gone.key")})

	cases := map[string]Config{
		"pair mismatch":   tlsTestConfig(enabledTLS(mismatch, tlsTestHTTPSPort)),
		"malformed input": tlsTestConfig(enabledTLS(garbage, tlsTestHTTPSPort)),
		"missing key":     unreadable,
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			var gotErr error
			stderr := captureStderr(t, func() { gotErr = cfg.ValidateFull() })
			if gotErr == nil {
				t.Fatal("ValidateFull() = nil, want failure")
			}
			assertNoPEMMaterial(t, gotErr.Error(), f.keyPEM, f.certPEM)
			assertNoPEMMaterial(t, stderr, f.keyPEM, f.certPEM)
		})
	}
}

// TestValidateTLS_ChainLoadsInFileOrder is the load half of test obligation 16:
// a leaf-only file and a leaf+intermediate file both pass validation, and the
// presented chain is exactly the blocks in the file.
func TestValidateTLS_ChainLoadsInFileOrder(t *testing.T) {
	intCA := newTLSCert(t, "goal-test-ca", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), true, nil)
	leaf := newTLSCert(t, "goal-test-leaf", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour), false, intCA)
	leafKeyPEM := leaf.keyPEM(t)

	chain := writeTLSFiles(t, append(leaf.certPEM(), intCA.certPEM()...), leafKeyPEM)
	if err := tlsTestConfig(enabledTLS(chain, tlsTestHTTPSPort)).ValidateFull(); err != nil {
		t.Fatalf("leaf+intermediate chain rejected: %v", err)
	}
	pair, err := tls.X509KeyPair(chain.certPEM, chain.keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	if len(pair.Certificate) != 2 {
		t.Fatalf("presented chain = %d blocks, want 2 in file order", len(pair.Certificate))
	}
	if got := parsedSubject(t, pair.Certificate[0]); got != "goal-test-leaf" {
		t.Errorf("first block = %q, want the leaf", got)
	}
	if got := parsedSubject(t, pair.Certificate[1]); got != "goal-test-ca" {
		t.Errorf("second block = %q, want the intermediate", got)
	}

	leafOnly := writeTLSFiles(t, leaf.certPEM(), leafKeyPEM)
	if err := tlsTestConfig(enabledTLS(leafOnly, tlsTestHTTPSPort)).ValidateFull(); err != nil {
		t.Fatalf("leaf-only chain rejected: %v", err)
	}
}

func parsedSubject(t *testing.T, der []byte) string {
	t.Helper()
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return c.Subject.CommonName
}

// TestValidateTLS_WorldReadableKeyWarns is the §D9 POSIX warning row: loud, but
// startup still proceeds.
func TestValidateTLS_WorldReadableKeyWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only permission model")
	}
	f := validTLSFixture(t)
	if err := os.Chmod(f.keyPath, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	cfg := tlsTestConfig(enabledTLS(f, tlsTestHTTPSPort))
	var gotErr error
	stderr := captureStderr(t, func() { gotErr = cfg.ValidateFull() })
	if gotErr != nil {
		t.Fatalf("ValidateFull() = %v, want warning only", gotErr)
	}
	if !strings.Contains(stderr, "readable by group or other") {
		t.Errorf("stderr = %q, want the world-readable key warning", stderr)
	}
}

// TestUnmarshal_TLSShapes pins the §D3 claim that every non-true shape is the
// same struct, so no shape other than a complete enabled block can mean enabled.
func TestUnmarshal_TLSShapes(t *testing.T) {
	cases := []struct {
		name string
		text string
		want *TLSConfig
	}{
		{"key absent", `{}`, nil},
		{"explicit null", `{"tls":null}`, nil},
		{"empty object", `{"tls":{}}`, &TLSConfig{}},
		{"disabled", `{"tls":{"enabled":false}}`, &TLSConfig{}},
		{"disabled with inert values", `{"tls":{"enabled":false,"port":8443}}`, &TLSConfig{Port: ptrInt(8443)}},
		{"enabled without a port key", `{"tls":{"enabled":true,"certFile":"/a","keyFile":"/b"}}`, &TLSConfig{Enabled: true, CertFile: "/a", KeyFile: "/b"}},
		{"enabled with an explicit zero port", `{"tls":{"enabled":true,"port":0,"certFile":"/a","keyFile":"/b"}}`, &TLSConfig{Enabled: true, Port: ptrInt(0), CertFile: "/a", KeyFile: "/b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c Config
			if err := json.Unmarshal([]byte(tc.text), &c); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(c.TLS, tc.want) {
				t.Errorf("TLS = %+v, want %+v", c.TLS, tc.want)
			}
		})
	}

	// §D3 gives an absent port and an explicit 0 two different startup
	// diagnostics, so the decoded shapes must stay distinguishable.
	var absent, zeroPort Config
	if err := json.Unmarshal([]byte(`{"tls":{"enabled":true,"certFile":"/a","keyFile":"/b"}}`), &absent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"tls":{"enabled":true,"port":0,"certFile":"/a","keyFile":"/b"}}`), &zeroPort); err != nil {
		t.Fatal(err)
	}
	if absent.TLS.Port != nil {
		t.Errorf("absent port decoded as %d, want nil", *absent.TLS.Port)
	}
	if zeroPort.TLS.Port == nil || *zeroPort.TLS.Port != 0 {
		t.Errorf("explicit zero port decoded as %+v, want a non-nil pointer to 0", zeroPort.TLS.Port)
	}
	if reflect.DeepEqual(absent.TLS, zeroPort.TLS) {
		t.Error(`"port" absent and "port": 0 must not decode to the same struct`)
	}

	var empty, disabled Config
	if err := json.Unmarshal([]byte(`{"tls":{}}`), &empty); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"tls":{"enabled":false}}`), &disabled); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(empty.TLS, disabled.TLS) {
		t.Errorf(`"tls":{} and enabled:false must decode to the same struct: %+v vs %+v`, empty.TLS, disabled.TLS)
	}

	var enabled Config
	if err := json.Unmarshal([]byte(`{"tls":{"enabled":true,"port":8443,"certFile":"/a","keyFile":"/b"}}`), &enabled); err != nil {
		t.Fatal(err)
	}
	if enabled.TLS == nil || !enabled.TLS.Enabled || enabled.TLS.Port == nil || *enabled.TLS.Port != 8443 {
		t.Fatalf("enabled block not decoded: %+v", enabled.TLS)
	}
}

// TestSave_TLSRoundTrip is test obligation 11 together with §D24: the block
// survives a save, and an absent block keeps producing byte-identical output.
func TestSave_TLSRoundTrip(t *testing.T) {
	f := validTLSFixture(t)
	dir := t.TempDir()

	path := filepath.Join(dir, "with-tls.json")
	cfg := tlsTestConfig(enabledTLS(f, tlsTestHTTPSPort))
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tls"`) {
		t.Fatalf("saved config lost the tls block: %s", data)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(loaded.TLS, cfg.TLS) {
		t.Errorf("TLS after round-trip = %+v, want %+v", loaded.TLS, cfg.TLS)
	}

	plainPath := filepath.Join(dir, "absent-tls.json")
	plain := tlsTestConfig(nil)
	if err := Save(plainPath, plain); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first, err := os.ReadFile(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), `"tls"`) {
		t.Errorf("config without TLS gained a tls key: %s", first)
	}
	reloaded, err := Load(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(plainPath, reloaded); err != nil {
		t.Fatalf("Save: %v", err)
	}
	second, err := os.ReadFile(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("re-saving a config without TLS changed bytes:\n%s\n%s", first, second)
	}

	// §D3's two port rows must survive a rewrite: an absent key stays absent and
	// an explicit 0 stays explicit, so neither shape can drift into the other's
	// diagnostic after config.Save regenerates goal.json.
	for _, tc := range []struct {
		name    string
		port    *int
		wantErr string
	}{
		{"absent port", nil, "tls.port is required when tls.enabled is true"},
		{"explicit zero port", ptrInt(0), "tls.port out of range 1-65535, got 0"},
	} {
		t.Run(tc.name+" survives Save", func(t *testing.T) {
			path := filepath.Join(dir, "port-"+strings.ReplaceAll(tc.name, " ", "-")+".json")
			cfg := tlsTestConfig(&TLSConfig{Enabled: true, Port: tc.port, CertFile: f.certPath, KeyFile: f.keyPath})
			if err := Save(path, cfg); err != nil {
				t.Fatalf("Save: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(data), `"port"`); got != (tc.port != nil) {
				t.Errorf(`saved config has a "port" key = %v, want %v: %s`, got, tc.port != nil, data)
			}
			saved, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if saved.TLS == nil || (saved.TLS.Port != nil) != (tc.port != nil) {
				t.Fatalf("port presence after round-trip = %+v, want %v", saved.TLS, tc.port != nil)
			}
			err = saved.ValidateFull()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("ValidateFull() after round-trip = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// TestSave_UnknownKeyInTLSBlockIsDropped documents §D3's last row: an unknown key
// inside tls is parsed and ignored, so a required field reads as missing, and a
// later save drops the unknown key. It also pins the measured Go behavior behind
// the §D3 example: a case-variant key ("certfile") is matched case-insensitively
// and therefore lands in CertFile instead of surfacing as missing.
func TestSave_UnknownKeyInTLSBlockIsDropped(t *testing.T) {
	f := validTLSFixture(t)

	t.Run("unmatched key fails required check", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "goal.json")
		text := `{
  "version": 2,
  "listenAddress": "127.0.0.1",
  "webPort": 18080,
  "dataDir": "./data",
  "adminUser": "admin",
  "tls": {"enabled": true, "port": 18443, "certificate": "` + jsonQuote(f.certPath) + `", "keyFile": "` + jsonQuote(f.keyPath) + `", "httpsPort": 9443}
}`
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		err = cfg.ValidateFull()
		if err == nil {
			t.Fatal("ValidateFull() = nil, want the misspelled key to surface as a missing required field")
		}
		if !strings.Contains(err.Error(), "tls.certFile is required") {
			t.Errorf("error = %q, want %q", err, "tls.certFile is required")
		}

		if err := Save(path, cfg); err != nil {
			t.Fatalf("Save: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, dropped := range []string{`"certificate"`, `"httpsPort"`} {
			if strings.Contains(string(data), dropped) {
				t.Errorf("saved config kept unknown key %s: %s", dropped, data)
			}
		}
	})

	t.Run("case-variant key decodes into the field", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "goal.json")
		text := `{
  "version": 2,
  "listenAddress": "127.0.0.1",
  "webPort": 18080,
  "dataDir": "./data",
  "adminUser": "admin",
  "tls": {"enabled": true, "port": 18443, "certfile": "` + jsonQuote(f.certPath) + `", "keyFile": "` + jsonQuote(f.keyPath) + `"}
}`
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.TLS == nil || cfg.TLS.CertFile != f.certPath {
			t.Fatalf("TLS = %+v, want CertFile populated from the case-variant key", cfg.TLS)
		}
		if err := cfg.ValidateFull(); err != nil {
			t.Errorf("ValidateFull() = %v, want the decoded block accepted", err)
		}
		if err := Save(path, cfg); err != nil {
			t.Fatalf("Save: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"certfile"`) {
			t.Errorf("saved config kept the case-variant key: %s", data)
		}
		reloaded, err := Load(path)
		if err != nil {
			t.Fatalf("Load after save: %v", err)
		}
		if !reflect.DeepEqual(reloaded.TLS, cfg.TLS) {
			t.Errorf("TLS = %+v after rewrite, want %+v", reloaded.TLS, cfg.TLS)
		}
	})
}

// jsonQuote returns s escaped for embedding inside a JSON string literal.
func jsonQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	return string(b[1 : len(b)-1])
}

// TestDiffHot_TLSIsRestartRequired is the §D4/§D23 classification: a tls change
// is reported as restart-required and never as a hot apply.
func TestDiffHot_TLSIsRestartRequired(t *testing.T) {
	f := validTLSFixture(t)
	other := validTLSFixture(t)
	block := enabledTLS(f, tlsTestHTTPSPort)
	same := enabledTLS(f, tlsTestHTTPSPort)

	cases := []struct {
		name string
		file *TLSConfig
		live *TLSConfig
	}{
		{"block added", block, nil},
		{"block removed", nil, block},
		{"port changed", enabledTLS(f, tlsTestHTTPSPort+1), block},
		{"port absent vs explicit zero", &TLSConfig{Enabled: true, Port: ptrInt(0), CertFile: f.certPath, KeyFile: f.keyPath}, &TLSConfig{Enabled: true, CertFile: f.certPath, KeyFile: f.keyPath}},
		{"disabled to enabled", block, &TLSConfig{Enabled: false, Port: block.Port, CertFile: block.CertFile, KeyFile: block.KeyFile}},
		{"cert path changed", enabledTLS(other, tlsTestHTTPSPort), block},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := DiffHot(tlsTestConfig(tc.file), tlsTestConfig(tc.live))
			if len(d.RestartRequired) != 1 || d.RestartRequired[0] != "tls" {
				t.Errorf("RestartRequired = %v, want [tls]", d.RestartRequired)
			}
			if len(d.Applied) != 0 {
				t.Errorf("Applied = %v, want empty (tls is not a hot field)", d.Applied)
			}
		})
	}

	// Equal content behind different pointers is not a change.
	d := DiffHot(tlsTestConfig(same), tlsTestConfig(block))
	if len(d.Applied) != 0 || len(d.RestartRequired) != 0 {
		t.Errorf("equal tls blocks classified as changed: %+v", d)
	}

	// Two independent parses of the same text is the shape a reload actually
	// sees: identical values in freshly allocated port pointers.
	text := `{"tls":{"enabled":true,"port":8443,"certFile":"/a","keyFile":"/b"}}`
	var first, second Config
	if err := json.Unmarshal([]byte(text), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(text), &second); err != nil {
		t.Fatal(err)
	}
	if first.TLS.Port == second.TLS.Port {
		t.Fatal("test precondition: the two parses share a port pointer")
	}
	d = DiffHot(first, second)
	if len(d.Applied) != 0 || len(d.RestartRequired) != 0 {
		t.Errorf("identical parsed tls blocks classified as changed: %+v", d)
	}
}

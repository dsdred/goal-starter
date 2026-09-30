package config

import (
	"strconv"
	"strings"
	"testing"
)

// ADR 019 §D19 extends the non-loopback-without-auth warning in WORDING only: it
// must name every listener the configuration actually opens, and must keep
// warning — never blocking startup, never mutating configuration (Owner
// decisions D7 and P6).

func exposedConfig(t *testing.T, block *TLSConfig) Config {
	t.Helper()
	cfg := tlsTestConfig(block)
	cfg.ListenAddress = "0.0.0.0"
	cfg.AuthEnabled = false
	return cfg
}

func TestUnauthWarning_HTTPOnlyNamesHTTPListener(t *testing.T) {
	cfg := exposedConfig(t, nil)

	var err error
	stderr := captureStderr(t, func() { err = cfg.ValidateFull() })

	if err != nil {
		t.Fatalf("warning-only contract broken: startup blocked with %v", err)
	}
	if !strings.Contains(stderr, "WARNING") {
		t.Fatalf("non-loopback without auth must warn, got %q", stderr)
	}
	want := "HTTP on 0.0.0.0:" + strconv.Itoa(cfg.WebPort)
	if !strings.Contains(stderr, want) {
		t.Errorf("warning must name the HTTP listener %q, got %q", want, stderr)
	}
	if strings.Contains(stderr, "HTTPS on") {
		t.Errorf("no HTTPS listener is configured, warning must not name one: %q", stderr)
	}
}

func TestUnauthWarning_HTTPPlusHTTPSNamesBothListeners(t *testing.T) {
	f := validTLSFixture(t)
	cfg := exposedConfig(t, enabledTLS(f, 18443))

	var err error
	stderr := captureStderr(t, func() { err = cfg.ValidateFull() })

	if err != nil {
		t.Fatalf("a valid tls block must not turn the warning into a startup failure: %v", err)
	}
	for _, want := range []string{
		"HTTP on 0.0.0.0:" + strconv.Itoa(cfg.WebPort),
		"HTTPS on 0.0.0.0:18443",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("warning must name %q, got %q", want, stderr)
		}
	}
	// §D19 requires the text to say the exposure is scheme-independent.
	if !strings.Contains(stderr, "regardless of scheme") || !strings.Contains(stderr, "authenticates nothing") {
		t.Errorf("warning must state the exposure is scheme-independent, got %q", stderr)
	}
}

func TestUnauthWarning_TLSBlockDisabledStillHTTPOnly(t *testing.T) {
	f := validTLSFixture(t)
	block := enabledTLS(f, 18443)
	block.Enabled = false
	cfg := exposedConfig(t, block)

	var err error
	stderr := captureStderr(t, func() { err = cfg.ValidateFull() })

	if err != nil {
		t.Fatalf("disabled tls block must not block startup here: %v", err)
	}
	if strings.Contains(stderr, "HTTPS on") {
		t.Errorf("disabled tls block must not be advertised as a listener: %q", stderr)
	}
}

// TestUnauthWarning_SilentWhenLoopbackOrAuthEnabled keeps the existing trigger
// unchanged: the warning is about exposure, not about TLS.
func TestUnauthWarning_SilentWhenLoopbackOrAuthEnabled(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		authOn  bool
		tlsPort int
	}{
		{"loopback, auth disabled", "127.0.0.1", false, 0},
		{"localhost alias, auth disabled", "localhost", false, 0},
		{"loopback over TLS, auth disabled", "127.0.0.1", false, 18443},
		{"non-loopback with auth enabled", "0.0.0.0", true, 0},
		{"non-loopback with auth enabled and TLS", "0.0.0.0", true, 18443},
	}

	for _, tc := range tests {
		var block *TLSConfig
		if tc.tlsPort != 0 {
			block = enabledTLS(validTLSFixture(t), tc.tlsPort)
		}
		cfg := tlsTestConfig(block)
		cfg.ListenAddress = tc.host
		cfg.AuthEnabled = tc.authOn
		if tc.authOn {
			cfg.AdminUser = "admin"
			cfg.AdminPassword = "a-test-password"
		}

		var err error
		stderr := captureStderr(t, func() { err = cfg.ValidateFull() })

		if err != nil {
			t.Errorf("%s: expected clean startup, got %v", tc.name, err)
		}
		if stderr != "" {
			t.Errorf("%s: expected no warning, got %q", tc.name, stderr)
		}
	}
}

// TestUnauthWarning_DoesNotChangeFailClosedTLS proves the extended wording did
// not move the warning into the error path: an enabled but unusable tls block
// still fails for the TLS reason, and the pre-bind warning is still emitted.
func TestUnauthWarning_DoesNotChangeFailClosedTLS(t *testing.T) {
	dir := t.TempDir()
	cfg := exposedConfig(t, &TLSConfig{
		Enabled:  true,
		Port:     ptrInt(18443),
		CertFile: dir + "/absent.crt",
		KeyFile:  dir + "/absent.key",
	})

	var err error
	stderr := captureStderr(t, func() { err = cfg.ValidateFull() })

	if err == nil {
		t.Fatal("enabled tls with unreadable material must still fail startup")
	}
	if !strings.Contains(err.Error(), "tls") {
		t.Errorf("failure must be the TLS reason, got %v", err)
	}
	if strings.Contains(err.Error(), "exposed without authentication") {
		t.Errorf("the exposure warning must never become an error: %v", err)
	}
	if !strings.Contains(stderr, "WARNING") {
		t.Errorf("warning is still due before the TLS failure, got %q", stderr)
	}
}

// TestUnauthWarning_LeavesConfigUnmutated pins "no configuration is mutated":
// ValidateFull phrases exposure from the values it was given and writes none back.
func TestUnauthWarning_LeavesConfigUnmutated(t *testing.T) {
	f := validTLSFixture(t)
	cfg := exposedConfig(t, enabledTLS(f, 18443))
	before := cfg

	_ = captureStderr(t, func() { _ = cfg.ValidateFull() })

	if cfg.ListenAddress != before.ListenAddress || cfg.WebPort != before.WebPort {
		t.Errorf("listeners mutated: %+v", cfg)
	}
	if cfg.AuthEnabled != before.AuthEnabled {
		t.Errorf("authEnabled silently changed to %v", cfg.AuthEnabled)
	}
	if cfg.TLS == nil || !cfg.TLS.Enabled || *cfg.TLS.Port != 18443 ||
		cfg.TLS.CertFile != before.TLS.CertFile || cfg.TLS.KeyFile != before.TLS.KeyFile {
		t.Errorf("tls block mutated: %+v", cfg.TLS)
	}
}

// TestUnauthWarning_NeverCarriesKeyMaterial keeps §D19's log-privacy rule on the
// one startup diagnostic that names certificate paths.
func TestUnauthWarning_NeverCarriesKeyMaterial(t *testing.T) {
	f := validTLSFixture(t)
	cfg := exposedConfig(t, enabledTLS(f, 18443))

	stderr := captureStderr(t, func() { _ = cfg.ValidateFull() })

	assertNoPEMMaterial(t, stderr, f.keyPEM, f.certPEM)
	if strings.Contains(stderr, "PRIVATE KEY") {
		t.Errorf("warning names key material: %q", stderr)
	}
}

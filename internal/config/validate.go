package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"github.com/dsdred/goal/internal/webui/validation"
)

// ValidateFull performs comprehensive configuration validation at startup.
// It checks all config fields, runtime executables, model paths, working directories,
// and address validity.
func (c Config) ValidateFull() error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	// If authentication is disabled and the server is exposed to the network,
	// log a prominent warning but do NOT block startup.
	if !c.AuthEnabled {
		if err := validateLocalhostOnly(c.ListenAddress); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: GoAl Web UI is exposed without authentication. Anyone with network access to this address can control this GoAl instance.\n")
		}
	}

	// Validate listen address.
	if err := validateAddress(c.ListenAddress, c.WebPort); err != nil {
		return fmt.Errorf("listen address: %w", err)
	}

	// Validate the optional native HTTPS block before anything is served:
	// an enabled block that cannot be loaded must fail startup (ADR 019 §D8).
	if err := c.validateTLS(); err != nil {
		return err
	}

	// Validate runtimes.
	for _, rt := range c.Runtimes {
		if err := validateRuntime(&rt); err != nil {
			return fmt.Errorf("runtime %q: %w", rt.Name, err)
		}
	}

	// Validate models.
	for _, m := range c.Models {
		if err := validateModel(&m); err != nil {
			return fmt.Errorf("model %q: %w", m.Name, err)
		}
	}

	// Validate profiles.
	for _, p := range c.Profiles {
		if err := validateProfile(&p); err != nil {
			return fmt.Errorf("profile %q: %w", p.Name, err)
		}
	}

	return nil
}

// validateAddress checks if host:port is bindable.
func validateAddress(host string, port int) error {
	if err := validation.ValidateHost(host); err != nil {
		return err
	}
	if err := validation.ValidatePort(port); err != nil {
		return err
	}

	// Check if address is already in use (best-effort).
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		// Address might be in use, but don't fail if binding to 0.0.0.0.
		if host != "0.0.0.0" && host != "::" {
			return fmt.Errorf("address %s is not available: %w", addr, err)
		}
	} else {
		_ = l.Close()
	}
	return nil
}

// validateTLS checks the optional native HTTPS block (ADR 019 §D8/§D9). It runs
// in ValidateFull, i.e. before any listener exists, so an enabled block that
// cannot be served fails startup instead of claiming HTTPS availability it does
// not have. Disabled shapes never read the other fields. Failure messages name
// the field, the reason and the file path, never the file contents (§D19). An
// absent tls.port and an explicit 0 are two different §D3 rows, which is why the
// port is a pointer.
func (c Config) validateTLS() error {
	if c.TLS == nil || !c.TLS.Enabled {
		warnDisabledTLS(c.TLS)
		return nil
	}
	t := c.TLS
	if t.Port == nil {
		return errors.New("tls.port is required when tls.enabled is true")
	}
	if *t.Port < 1 || *t.Port > 65535 {
		return fmt.Errorf("tls.port out of range 1-65535, got %d", *t.Port)
	}
	if *t.Port == c.WebPort {
		return fmt.Errorf("tls.port %d must differ from webPort %d: both listeners would bind the same address", *t.Port, c.WebPort)
	}
	if t.CertFile == "" {
		return errors.New("tls.certFile is required")
	}
	if t.KeyFile == "" {
		return errors.New("tls.keyFile is required")
	}
	if !filepath.IsAbs(t.CertFile) {
		return fmt.Errorf("tls.certFile must be an absolute path: %s", t.CertFile)
	}
	if !filepath.IsAbs(t.KeyFile) {
		return fmt.Errorf("tls.keyFile must be an absolute path: %s", t.KeyFile)
	}

	certPEM, err := os.ReadFile(t.CertFile)
	if err != nil {
		return fmt.Errorf("tls.certFile %s is not readable: %w", t.CertFile, err)
	}
	keyPEM, err := os.ReadFile(t.KeyFile)
	if err != nil {
		return fmt.Errorf("tls.keyFile %s is not readable: %w", t.KeyFile, err)
	}
	// The same call the HTTPS server will use, so a pair that loads here can be
	// served; no chain assembly, reordering or issuer lookup happens here (§D5).
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("tls certificate/key pair (%s, %s) failed to load: %w", t.CertFile, t.KeyFile, err)
	}

	// Go does not check the validity window when loading a key pair, so the
	// window is checked explicitly against the local clock (§D9).
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("tls.certFile %s has no parseable leaf certificate: %w", t.CertFile, err)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) {
		return fmt.Errorf("tls.certFile %s certificate is not yet valid: notBefore=%s", t.CertFile, leaf.NotBefore.Format(time.RFC3339))
	}
	if now.After(leaf.NotAfter) {
		return fmt.Errorf("tls.certFile %s certificate has expired: notAfter=%s", t.CertFile, leaf.NotAfter.Format(time.RFC3339))
	}

	warnWorldReadableKey(t.KeyFile)
	return nil
}

// warnDisabledTLS reports inert tls values (§D3, §D9): configured but never
// applied. §D3's one distinguishable disabled shape is a non-zero field, so an
// absent block, a nil or zero port and empty paths stay silent.
func warnDisabledTLS(t *TLSConfig) {
	if t == nil {
		return
	}
	portSet := t.Port != nil && *t.Port != 0
	if !portSet && t.CertFile == "" && t.KeyFile == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "WARNING: tls configured but disabled; tls.port=%s tls.certFile=%s tls.keyFile=%s are ignored while tls.enabled is false.\n",
		portText(t.Port), t.CertFile, t.KeyFile)
}

// portText renders an optional port for the disabled-block warning, naming an
// absent key instead of printing the same 0 an explicit 0 would print.
func portText(p *int) string {
	if p == nil {
		return "unset"
	}
	return strconv.Itoa(*p)
}

// warnWorldReadableKey warns about group/other-readable key files. POSIX-only
// by decision: file modes are not reliable on Windows and a hard failure there
// would break normal ACL layouts (§D9).
func warnWorldReadableKey(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if perm := info.Mode().Perm(); perm&0o044 != 0 {
		fmt.Fprintf(os.Stderr, "WARNING: tls.keyFile %s is readable by group or other (mode %04o); restrict it to the account running GoAl.\n", path, perm)
	}
}

// validateRuntime checks runtime configuration.
func validateRuntime(rt *Runtime) error {
	if rt.Name == "" {
		return fmt.Errorf("name is required")
	}
	if rt.Executable == "" {
		return fmt.Errorf("executable is required")
	}

	// Check if executable exists.
	if abs, err := filepath.Abs(rt.Executable); err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	} else if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("executable does not exist: %s", rt.Executable)
	}

	// Validate working directory if specified.
	if rt.WorkingDirectory != "" {
		if info, err := os.Stat(rt.WorkingDirectory); err != nil || !info.IsDir() {
			return fmt.Errorf("working directory does not exist or is not a directory: %s", rt.WorkingDirectory)
		}
	}

	// Validate environment variables.
	for k := range rt.Environment {
		if err := validateEnvKey(k); err != nil {
			return fmt.Errorf("invalid env key %q: %w", k, err)
		}
	}

	return nil
}

// validateModel checks model configuration.
// Models can be configured either with a "path" (legacy GGUF-based) or with "arguments" (inline args).
func validateModel(m *Model) error {
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}

	// At least one of path or arguments is required.
	if m.Path == "" && len(m.Arguments) == 0 {
		return fmt.Errorf("either path or arguments is required")
	}

	// Validate path if specified.
	if m.Path != "" {
		if abs, err := filepath.Abs(m.Path); err != nil {
			return fmt.Errorf("cannot resolve model path: %w", err)
		} else if _, err := os.Stat(abs); err != nil {
			// Model file is optional (might be loaded later).
		}
	}

	// Validate arguments if specified.
	for _, arg := range m.Arguments {
		if arg == "" {
			return fmt.Errorf("model arguments must not contain empty entries")
		}
	}

	// Validate environment variables.
	for k := range m.Environment {
		if err := validateEnvKey(k); err != nil {
			return fmt.Errorf("invalid env key %q: %w", k, err)
		}
	}

	return nil
}

// validateProfile checks profile configuration.
func validateProfile(p *Profile) error {
	if p.Name == "" {
		return fmt.Errorf("name is required")
	}
	if p.RuntimeID == "" {
		return fmt.Errorf("runtime_id is required")
	}
	if p.ModelID == "" {
		return fmt.Errorf("model_id is required")
	}

	// Validate host and port.
	if p.Host == "" {
		return fmt.Errorf("host is required")
	}
	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	// Validate environment variables.
	for k := range p.Environment {
		if err := validateEnvKey(k); err != nil {
			return fmt.Errorf("invalid env key %q: %w", k, err)
		}
	}

	return nil
}

// validateEnvKey checks if an environment variable name is valid.
// Valid names contain only alphanumeric characters and underscores, starting with a letter or underscore.
func validateEnvKey(key string) error {
	if key == "" {
		return fmt.Errorf("environment variable name cannot be empty")
	}
	// Allow standard env var characters: letters, digits, underscores, dots, hyphens.
	matched, err := regexp.MatchString(`^[A-Za-z_][A-Za-z0-9_.\-]*$`, key)
	if err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("invalid character in environment variable name %q", key)
	}
	return nil
}

// validateLocalhostOnly ensures the address is a loopback address when auth is disabled.
func validateLocalhostOnly(host string) error {
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return nil
		}
		return fmt.Errorf("listen address %s is not a loopback address; refusing to start without authentication", host)
	}
	return fmt.Errorf("listen address %s is not a loopback address; refusing to start without authentication", host)
}

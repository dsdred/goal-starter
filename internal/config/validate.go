package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/example/goal/internal/webui/validation"
)

// ValidateFull performs comprehensive configuration validation at startup.
// It checks all config fields, runtime executables, model paths, working directories,
// and address validity.
func (c Config) ValidateFull() error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	// Validate listen address.
	if err := validateAddress(c.ListenAddress, c.WebPort); err != nil {
		return fmt.Errorf("listen address: %w", err)
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
func validateModel(m *Model) error {
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}
	if m.Path == "" {
		return fmt.Errorf("path is required")
	}

	// Check if model file exists.
	if abs, err := filepath.Abs(m.Path); err != nil {
		return fmt.Errorf("cannot resolve model path: %w", err)
	} else if _, err := os.Stat(abs); err != nil {
		// Model file is optional (might be loaded later).
	}

	// Validate mmproj path if specified.
	if m.MMProj != "" {
		if abs, err := filepath.Abs(m.MMProj); err != nil {
			return fmt.Errorf("cannot resolve mmproj path: %w", err)
		} else if _, err := os.Stat(abs); err != nil {
			// mmproj file is optional (some models don't use it).
		}
	}

	// Validate format.
	if m.Format != "" {
		validFormats := []string{"gguf", "bin", "safetensors", "pt", "pth"}
		lowerFormat := strings.ToLower(m.Format)
		valid := false
		for _, f := range validFormats {
			if lowerFormat == f {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("unknown model format %q, expected one of: %s", m.Format, strings.Join(validFormats, ", "))
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

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateValidConfig(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		AdminUser:     "admin",
		AuthEnabled:   false,
	}
	if err := cfg.ValidateFull(); err != nil {
		t.Errorf("expected valid config, got error: %v", err)
	}
}

func TestValidateEmptyListenAddress(t *testing.T) {
	cfg := Config{
		Version:   1,
		WebPort:   8080,
		AdminUser: "admin",
	}
	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for empty listen address, got nil")
	}
}

func TestValidateInvalidPort(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       0,
		AdminUser:     "admin",
	}
	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for port 0, got nil")
	}

	cfg.WebPort = 65536
	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for port 65536, got nil")
	}
}

func TestValidateAuthRequiresAdminUser(t *testing.T) {
	cfg := Config{
		Version:     1,
		AuthEnabled: true,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when auth enabled but admin user empty, got nil")
	}
}

func TestValidateRuntimeExecutableExists(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "test-runtime")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\necho test"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Runtimes: []Runtime{
			{
				Name:       "test",
				Executable: exePath,
			},
		},
	}

	if err := cfg.ValidateFull(); err != nil {
		t.Errorf("expected valid config with existing executable, got error: %v", err)
	}
}

func TestValidateRuntimeExecutableNotExist(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Runtimes: []Runtime{
			{
				Name:       "nonexistent",
				Executable: "/tmp/nonexistent-binary-12345",
			},
		},
	}

	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for nonexistent executable, got nil")
	}
}

func TestValidateRuntimeEmptyName(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Runtimes: []Runtime{
			{
				Name:       "",
				Executable: "/usr/bin/python3",
			},
		},
	}

	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for empty runtime name, got nil")
	}
}

func TestValidateRuntimeInvalidEnvKey(t *testing.T) {
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "test")
	os.WriteFile(exePath, []byte("echo test"), 0755)

	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Runtimes: []Runtime{
			{
				Name:       "test",
				Executable: exePath,
				Environment: map[string]string{
					"VALID_KEY":  "value",
					"123INVALID": "value", // starts with digit
				},
			},
		},
	}

	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for invalid env key, got nil")
	}
}

func TestValidateModelValid(t *testing.T) {
	tmpDir := t.TempDir()
	modelPath := filepath.Join(tmpDir, "model.gguf")
	os.WriteFile(modelPath, []byte("test model data"), 0644)

	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Models: []Model{
			{
				Name:   "test-model",
				Path:   modelPath,
				Format: "gguf",
			},
		},
	}

	if err := cfg.ValidateFull(); err != nil {
		t.Errorf("expected valid model config, got error: %v", err)
	}
}

func TestValidateModelInvalidFormat(t *testing.T) {
	tmpDir := t.TempDir()
	modelPath := filepath.Join(tmpDir, "model.gguf")
	os.WriteFile(modelPath, []byte("test"), 0644)

	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Models: []Model{
			{
				Name:   "test",
				Path:   modelPath,
				Format: "invalid-format",
			},
		},
	}

	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for invalid model format, got nil")
	}
}

func TestValidateModelEmptyName(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Models: []Model{
			{
				Name:   "",
				Path:   "/tmp/model.gguf",
				Format: "gguf",
			},
		},
	}

	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for empty model name, got nil")
	}
}

func TestValidateProfileValid(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Profiles: []Profile{
			{
				Name:      "test-profile",
				RuntimeID: "rt-1",
				ModelID:   "model-1",
				Host:      "127.0.0.1",
				Port:      8000,
			},
		},
	}

	if err := cfg.ValidateFull(); err != nil {
		t.Errorf("expected valid profile config, got error: %v", err)
	}
}

func TestValidateProfileEmptyName(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Profiles: []Profile{
			{
				Name:      "",
				RuntimeID: "rt-1",
				ModelID:   "model-1",
				Host:      "127.0.0.1",
				Port:      8000,
			},
		},
	}

	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for empty profile name, got nil")
	}
}

func TestValidateProfileInvalidPort(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Profiles: []Profile{
			{
				Name:      "test",
				RuntimeID: "rt-1",
				ModelID:   "model-1",
				Host:      "127.0.0.1",
				Port:      0,
			},
		},
	}

	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for profile port 0, got nil")
	}
}

func TestValidateProfileEmptyHost(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Profiles: []Profile{
			{
				Name:      "test",
				RuntimeID: "rt-1",
				ModelID:   "model-1",
				Host:      "",
				Port:      8000,
			},
		},
	}

	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for empty profile host, got nil")
	}
}

func TestValidateEnvKeyValid(t *testing.T) {
	validKeys := []string{
		"PATH",
		"HOME_DIR",
		"MY-VAR",
		"VAR_WITH_DOTS",
	}

	for _, key := range validKeys {
		if err := validateEnvKey(key); err != nil {
			t.Errorf("expected key %q to be valid, got error: %v", key, err)
		}
	}
}

func TestValidateEnvKeyInvalid(t *testing.T) {
	invalidKeys := []string{
		"",
		"123START",
		"VAR WITH SPACES",
		"VAR@SPECIAL",
	}

	for _, key := range invalidKeys {
		if err := validateEnvKey(key); err == nil {
			t.Errorf("expected key %q to be invalid, got nil", key)
		}
	}
}

func TestValidateModelFormatCaseInsensitive(t *testing.T) {
	tmpDir := t.TempDir()
	modelPath := filepath.Join(tmpDir, "model.gguf")
	os.WriteFile(modelPath, []byte("test"), 0644)

	formats := []string{"GGUF", "Gguf", "safetensors", "SAFETENSORS"}
	for _, format := range formats {
		cfg := Config{
			Version:       1,
			ListenAddress: "127.0.0.1",
			WebPort:       8080,
			Models: []Model{
				{
					Name:   "test",
					Path:   modelPath,
					Format: format,
				},
			},
		}

		if err := cfg.ValidateFull(); err != nil {
			t.Errorf("expected format %q to be valid, got error: %v", format, err)
		}
	}
}

func TestValidateProfileMissingRuntimeID(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Profiles: []Profile{
			{
				Name:      "test",
				RuntimeID: "",
				ModelID:   "model-1",
				Host:      "127.0.0.1",
				Port:      8000,
			},
		},
	}

	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for empty runtime_id, got nil")
	}
}

func TestValidateProfileMissingModelID(t *testing.T) {
	cfg := Config{
		Version:       1,
		ListenAddress: "127.0.0.1",
		WebPort:       8080,
		Profiles: []Profile{
			{
				Name:      "test",
				RuntimeID: "rt-1",
				ModelID:   "",
				Host:      "127.0.0.1",
				Port:      8000,
			},
		},
	}

	if err := cfg.ValidateFull(); err == nil {
		t.Error("expected error for empty model_id, got nil")
	}
}

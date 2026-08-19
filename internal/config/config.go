package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Version       int       `json:"version"`
	ListenAddress string    `json:"listenAddress"`
	WebPort       int       `json:"webPort"`
	DataDir       string    `json:"dataDir"`
	Runtimes      []Runtime `json:"runtimes"`
	Models        []Model   `json:"models"`
	Profiles      []Profile `json:"profiles"`
	AdminUser     string    `json:"adminUser"`
	AdminPassword string    `json:"adminPassword"`
	AuthEnabled   bool      `json:"authEnabled"`
}

type Runtime struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Executable       string            `json:"executable"`
	WorkingDirectory string            `json:"workingDirectory,omitempty"`
	DefaultArgs      []string          `json:"defaultArgs,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	// HealthCheck is optional runtime-specific health check configuration.
	HealthCheck *RuntimeHealthCheck `json:"healthCheck,omitempty"`
}

// RuntimeHealthCheck holds runtime-specific health check settings.
type RuntimeHealthCheck struct {
	Type     string `json:"type"` // "tcp", "http"
	Enabled  bool   `json:"enabled"`
	Interval int    `json:"interval"` // seconds between checks
	Timeout  int    `json:"timeout"`  // seconds per check
	Host     string `json:"host"`     // for tcp checks
	Port     int    `json:"port"`     // for tcp checks
	HTTPPath string `json:"httpPath"` // for http checks
}

type Model struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Path        string            `json:"path,omitempty"`
	RuntimeID   string            `json:"runtimeId,omitempty"`
	Arguments   []string          `json:"arguments,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

type Profile struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	RuntimeID   string            `json:"runtimeId"`
	ModelID     string            `json:"modelId"`
	Host        string            `json:"host"`
	Port        int               `json:"port"`
	Args        []string          `json:"args,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	// HealthCheck is optional profile-specific health check configuration.
	HealthCheck *ProfileHealthCheck `json:"healthCheck,omitempty"`
}

// UnmarshalJSON supports both `args` and `arguments` for Profile args
// for backward compatibility with older configuration examples.
func (p *Profile) UnmarshalJSON(data []byte) error {
	type Alias Profile
	aux := &struct {
		*Alias
		Arguments []string `json:"arguments,omitempty"`
	}{Alias: (*Alias)(p)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if len(p.Args) == 0 && len(aux.Arguments) > 0 {
		p.Args = aux.Arguments
	}
	return nil
}

// ProfileHealthCheck holds profile-specific health check settings.
type ProfileHealthCheck struct {
	Enabled    bool   `json:"enabled"`
	Interval   int    `json:"interval"`   // seconds between checks
	Timeout    int    `json:"timeout"`    // seconds per check
	HTTPPath   string `json:"httpPath"`   // HTTP path to check (e.g., /health)
	HTTPStatus int    `json:"httpStatus"` // expected HTTP status code
}

func Default() Config {
	return Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       "./data",
		AdminUser:     "admin",
		AuthEnabled:   false,
	}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		if err := Save(path, cfg); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	// Migrate from older config versions.
	if cfg.Version < 1 {
		if err := migrateV1ToV2(&cfg); err != nil {
			return Config{}, fmt.Errorf("config migration v1->v2: %w", err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// migrateV1ToV2 upgrades config from version 1 to version 2.
// Adds profile-level and runtime-level health check config.
// Also normalizes model entries: if a model has "arguments" or "runtimeId" instead of "path",
// it is left as-is since version 2 models support both legacy path-based and new argument-based configs.
func migrateV1ToV2(cfg *Config) error {
	if cfg.Version >= 2 {
		return nil // already migrated
	}
	cfg.Version = 2
	// Add default health check config to each profile.
	for i := range cfg.Profiles {
		if cfg.Profiles[i].HealthCheck == nil {
			cfg.Profiles[i].HealthCheck = &ProfileHealthCheck{
				Enabled:    true,
				Interval:   30,
				Timeout:    5,
				HTTPPath:   "/health",
				HTTPStatus: 200,
			}
		}
	}
	// Add default health check config to each runtime.
	for i := range cfg.Runtimes {
		if cfg.Runtimes[i].HealthCheck == nil {
			cfg.Runtimes[i].HealthCheck = &RuntimeHealthCheck{
				Type:     "tcp",
				Enabled:  true,
				Interval: 30,
				Timeout:  3,
			}
		}
	}
	return nil
}

func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c Config) Validate() error {
	if c.Version < 1 {
		return errors.New("config version must be >= 1")
	}
	if c.ListenAddress == "" {
		return errors.New("listenAddress is required")
	}
	if c.WebPort < 1 || c.WebPort > 65535 {
		return errors.New("webPort must be between 1 and 65535")
	}
	if c.AuthEnabled && c.AdminUser == "" {
		return errors.New("adminUser is required when auth is enabled")
	}
	if c.AuthEnabled && c.AdminPassword == "" {
		return errors.New("adminPassword is required when auth is enabled")
	}
	return nil
}

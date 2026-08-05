package config

import (
	"fmt"
	"slices"
	"strings"
)

// Migration represents a single config migration step.
type Migration struct {
	FromVersion int
	ToVersion   int
	Apply       func(*Config) error
}

// migrations defines all supported config migrations.
var migrations = []Migration{
	{
		FromVersion: 1,
		ToVersion:   2,
		Apply:       migrateV1toV2,
	},
}

// migrateV1toV2 upgrades v1 configs to v2 by applying defaults for missing fields.
func migrateV1toV2(cfg *Config) error {
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = Default().ListenAddress
	}
	if cfg.WebPort < 1 || cfg.WebPort > 65535 {
		cfg.WebPort = Default().WebPort
	}
	if cfg.DataDir == "" {
		cfg.DataDir = Default().DataDir
	}
	cfg.Version = 2
	return cfg.Validate()
}

// GetMigrations returns the list of available migrations.
func GetMigrations() []Migration {
	return slices.Clone(migrations)
}

// GetSupportedVersions returns all supported config versions.
func GetSupportedVersions() []int {
	versions := []int{1}
	for _, m := range migrations {
		if !slices.Contains(versions, m.FromVersion) {
			versions = append(versions, m.FromVersion)
		}
		if !slices.Contains(versions, m.ToVersion) {
			versions = append(versions, m.ToVersion)
		}
	}
	slices.Sort(versions)
	return versions
}

// CanMigrate checks if migration is possible from the given version.
func CanMigrate(fromVersion int) bool {
	for _, m := range migrations {
		if m.FromVersion == fromVersion {
			return true
		}
	}
	return false
}

// GetMigrationSteps returns all steps needed to migrate fromVersion to current.
func GetMigrationSteps(fromVersion int) ([]Migration, error) {
	if fromVersion < 1 {
		return nil, fmt.Errorf("unsupported config version: %d (minimum: 1)", fromVersion)
	}

	current := CurrentVersion()
	if fromVersion == current {
		return nil, nil
	}

	if fromVersion > current {
		return nil, fmt.Errorf("config version %d is newer than supported version %d", fromVersion, current)
	}

	var steps []Migration
	for _, m := range migrations {
		if m.FromVersion >= fromVersion && m.ToVersion <= current {
			steps = append(steps, m)
		}
	}

	if len(steps) == 0 {
		return nil, fmt.Errorf("no migration path from version %d to %d", fromVersion, current)
	}

	return steps, nil
}

// CurrentVersion returns the latest supported config version.
func CurrentVersion() int {
	max := 1
	for _, m := range migrations {
		if m.ToVersion > max {
			max = m.ToVersion
		}
	}
	return max
}

// MigrateConfig applies all necessary migrations to upgrade the config.
// Returns the migrated config and a list of applied migration steps.
func MigrateConfig(cfg Config) (Config, []string, error) {
	var applied []string

	for {
		step, ok := findNextMigration(cfg.Version)
		if !ok {
			break
		}

		if err := step.Apply(&cfg); err != nil {
			return Config{}, applied, fmt.Errorf("migration %d->%d failed: %w", step.FromVersion, step.ToVersion, err)
		}

		applied = append(applied, fmt.Sprintf("%d->%d (%s)", step.FromVersion, step.ToVersion, step.Description()))
	}

	if len(applied) > 0 {
		return cfg, applied, nil
	}

	return cfg, nil, nil
}

func findNextMigration(fromVersion int) (*Migration, bool) {
	for _, m := range migrations {
		if m.FromVersion == fromVersion {
			return &m, true
		}
	}
	return nil, false
}

// ValidateMigrationChain verifies all migrations form a continuous chain from version 1 to current.
func ValidateMigrationChain() error {
	current := CurrentVersion()
	if current < 1 {
		return fmt.Errorf("current version must be >= 1, got %d", current)
	}

	seen := make(map[int]bool)
	for _, m := range migrations {
		if m.FromVersion < 1 {
			return fmt.Errorf("migration from invalid version: %d", m.FromVersion)
		}
		if m.ToVersion <= m.FromVersion {
			return fmt.Errorf("migration must advance version: %d->%d", m.FromVersion, m.ToVersion)
		}
		if seen[m.FromVersion] {
			return fmt.Errorf("duplicate from version: %d", m.FromVersion)
		}
		seen[m.FromVersion] = true
	}

	for v := 1; v < current; v++ {
		if !seen[v] {
			return fmt.Errorf("missing migration starting from version: %d", v)
		}
	}

	return nil
}

// GetMigrationStatus returns the status of a config relative to the current version.
func GetMigrationStatus(configVersion int) MigrationStatus {
	current := CurrentVersion()

	if configVersion < 1 {
		return MigrationStatus{
			ConfigVersion:  configVersion,
			CurrentVersion: current,
			Status:         "unsupported",
			Message:        fmt.Sprintf("Config version %d is not supported (minimum: 1)", configVersion),
			NeedsMigration: false,
		}
	}

	if configVersion == current {
		return MigrationStatus{
			ConfigVersion:  configVersion,
			CurrentVersion: current,
			Status:         "up-to-date",
			Message:        "Config is up to date",
			NeedsMigration: false,
		}
	}

	if configVersion > current {
		return MigrationStatus{
			ConfigVersion:  configVersion,
			CurrentVersion: current,
			Status:         "newer",
			Message:        fmt.Sprintf("Config version %d is newer than supported version %d", configVersion, current),
			NeedsMigration: false,
		}
	}

	steps, err := GetMigrationSteps(configVersion)
	if err != nil {
		return MigrationStatus{
			ConfigVersion:  configVersion,
			CurrentVersion: current,
			Status:         "error",
			Message:        err.Error(),
			NeedsMigration: true,
			StepCount:      0,
		}
	}

	return MigrationStatus{
		ConfigVersion:  configVersion,
		CurrentVersion: current,
		Status:         "needs-migration",
		Message:        fmt.Sprintf("Config needs %d migration step(s) to reach version %d", len(steps), current),
		NeedsMigration: true,
		StepCount:      len(steps),
	}
}

// MigrationStatus describes the migration status of a configuration.
type MigrationStatus struct {
	ConfigVersion  int
	CurrentVersion int
	Status         string // "up-to-date", "needs-migration", "newer", "unsupported", "error"
	Message        string
	NeedsMigration bool
	StepCount      int
}

// MigrationDescription provides a human-readable description for a migration step.
func (m Migration) Description() string {
	descs := map[string]string{
		"1->2": "Add profiles section and update schema",
	}
	if d, ok := descs[fmt.Sprintf("%d->%d", m.FromVersion, m.ToVersion)]; ok {
		return d
	}
	return fmt.Sprintf("Migrate from version %d to %d", m.FromVersion, m.ToVersion)
}

// ParseConfigVersion safely parses a version string.
func ParseConfigVersion(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty version string")
	}

	var v int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid version string: %q (non-numeric character)", s)
		}
		v = v*10 + int(r-'0')
	}

	if v < 1 {
		return 0, fmt.Errorf("config version must be >= 1, got %d", v)
	}

	return v, nil
}

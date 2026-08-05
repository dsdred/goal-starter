package config

import (
	"testing"
)

func TestCurrentVersion(t *testing.T) {
	v := CurrentVersion()
	if v < 1 {
		t.Errorf("CurrentVersion() = %d, want >= 1", v)
	}
}

func TestGetMigrations(t *testing.T) {
	migrations := GetMigrations()
	if len(migrations) == 0 {
		t.Fatal("GetMigrations() returned empty slice")
	}
	for _, m := range migrations {
		if m.FromVersion < 1 {
			t.Errorf("migration from invalid version: %d", m.FromVersion)
		}
		if m.ToVersion <= m.FromVersion {
			t.Errorf("migration must advance version: %d->%d", m.FromVersion, m.ToVersion)
		}
		if m.Apply == nil {
			t.Errorf("migration %d->%d has nil Apply", m.FromVersion, m.ToVersion)
		}
	}
}

func TestGetSupportedVersions(t *testing.T) {
	versions := GetSupportedVersions()
	if len(versions) == 0 {
		t.Fatal("GetSupportedVersions() returned empty slice")
	}
	if versions[0] != 1 {
		t.Errorf("first supported version must be 1, got %d", versions[0])
	}
}

func TestCanMigrate(t *testing.T) {
	if !CanMigrate(1) {
		t.Error("CanMigrate(1) = false, want true")
	}
}

func TestGetMigrationSteps(t *testing.T) {
	steps, err := GetMigrationSteps(1)
	if err != nil {
		t.Fatalf("GetMigrationSteps(1) error: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("GetMigrationSteps(1) returned no steps")
	}
}

func TestGetMigrationSteps_NoStepsNeeded(t *testing.T) {
	current := CurrentVersion()
	steps, err := GetMigrationSteps(current)
	if err != nil {
		t.Fatalf("GetMigrationSteps(%d) error: %v", current, err)
	}
	if len(steps) != 0 {
		t.Errorf("GetMigrationSteps(%d) returned %d steps, want 0", current, len(steps))
	}
}

func TestGetMigrationSteps_UnsupportedVersion(t *testing.T) {
	_, err := GetMigrationSteps(0)
	if err == nil {
		t.Fatal("GetMigrationSteps(0) returned nil error, want error")
	}
}

func TestGetMigrationSteps_NewerVersion(t *testing.T) {
	current := CurrentVersion()
	_, err := GetMigrationSteps(current + 100)
	if err == nil {
		t.Fatal("GetMigrationSteps(current+100) returned nil error, want error")
	}
}

func TestMigrateConfig_NoMigrationNeeded(t *testing.T) {
	cfg := Config{Version: CurrentVersion()}
	result, applied, err := MigrateConfig(cfg)
	if err != nil {
		t.Fatalf("MigrateConfig() error: %v", err)
	}
	if result.Version != CurrentVersion() {
		t.Errorf("MigrateConfig() version = %d, want %d", result.Version, CurrentVersion())
	}
	if applied != nil {
		t.Errorf("MigrateConfig() applied = %v, want nil", applied)
	}
}

func TestMigrateConfig_V1toV2(t *testing.T) {
	cfg := Config{Version: 1, ListenAddress: "127.0.0.1", WebPort: 9090}
	result, applied, err := MigrateConfig(cfg)
	if err != nil {
		t.Fatalf("MigrateConfig() error: %v", err)
	}
	if result.Version != 2 {
		t.Errorf("MigrateConfig() version = %d, want 2", result.Version)
	}
	if len(applied) != 1 {
		t.Errorf("MigrateConfig() applied = %v, want 1 entry", applied)
	}
}

func TestMigrateConfig_InvalidConfig(t *testing.T) {
	// Config with empty listenAddress gets default applied
	cfg := Config{Version: 1, WebPort: 9090, ListenAddress: ""}
	cfg, _, err := MigrateConfig(cfg)
	if err != nil {
		t.Fatalf("MigrateConfig() error: %v", err)
	}
	// listenAddress should now have default value, Validate should pass
	if err := cfg.Validate(); err != nil {
		t.Errorf("MigrateConfig() should apply defaults, but Validate failed: %v", err)
	}

	// Config with empty adminUser when auth enabled should still fail (migration doesn't touch this)
	cfg2 := Config{Version: 1, ListenAddress: "127.0.0.1", WebPort: 9090, AuthEnabled: true, AdminUser: ""}
	_, _, err = MigrateConfig(cfg2)
	if err == nil {
		t.Fatal("MigrateConfig() with invalid config (auth without admin) returned nil error, want error")
	}
}

func TestValidateMigrationChain(t *testing.T) {
	if err := ValidateMigrationChain(); err != nil {
		t.Errorf("ValidateMigrationChain() error: %v", err)
	}
}

func TestGetMigrationStatus_UpToDate(t *testing.T) {
	current := CurrentVersion()
	status := GetMigrationStatus(current)
	if status.Status != "up-to-date" {
		t.Errorf("GetMigrationStatus(%d) status = %q, want \"up-to-date\"", current, status.Status)
	}
	if status.NeedsMigration {
		t.Errorf("GetMigrationStatus(%d) NeedsMigration = true, want false", current)
	}
}

func TestGetMigrationStatus_NeedsMigration(t *testing.T) {
	status := GetMigrationStatus(1)
	if status.Status != "needs-migration" {
		t.Errorf("GetMigrationStatus(1) status = %q, want \"needs-migration\"", status.Status)
	}
	if !status.NeedsMigration {
		t.Error("GetMigrationStatus(1) NeedsMigration = false, want true")
	}
}

func TestGetMigrationStatus_UnsupportedVersion(t *testing.T) {
	status := GetMigrationStatus(0)
	if status.Status != "unsupported" {
		t.Errorf("GetMigrationStatus(0) status = %q, want \"unsupported\"", status.Status)
	}
	if status.NeedsMigration {
		t.Error("GetMigrationStatus(0) NeedsMigration = true, want false")
	}
}

func TestGetMigrationStatus_NewerVersion(t *testing.T) {
	current := CurrentVersion()
	status := GetMigrationStatus(current + 100)
	if status.Status != "newer" {
		t.Errorf("GetMigrationStatus(current+100) status = %q, want \"newer\"", status.Status)
	}
	if status.NeedsMigration {
		t.Error("GetMigrationStatus(current+100) NeedsMigration = true, want false")
	}
}

func TestMigration_Description(t *testing.T) {
	for _, m := range GetMigrations() {
		desc := m.Description()
		if desc == "" {
			t.Errorf("Migration(%d->%d).Description() returned empty string", m.FromVersion, m.ToVersion)
		}
	}
}

func TestParseConfigVersion(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{"1", 1, false},
		{"2", 2, false},
		{" 1 ", 1, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
		{"", 0, true},
		{"1.0", 0, true},
	}

	for _, tt := range tests {
		got, err := ParseConfigVersion(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseConfigVersion(%q) expected error, got nil", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseConfigVersion(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseConfigVersion(%q) = %d, want %d", tt.input, got, tt.want)
			}
		}
	}
}

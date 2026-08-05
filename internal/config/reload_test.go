package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewReloadConfig_LoadsFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "goal.json")

	cfgData := `{
		"version": 1,
		"listenAddress": "127.0.0.1",
		"webPort": 8080
	}`
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0644); err != nil {
		t.Fatal(err)
	}

	rc, err := NewReloadConfig(cfgPath)
	if err != nil {
		t.Fatalf("NewReloadConfig failed: %v", err)
	}
	defer rc.Stop()

	cfg := rc.Get()
	if cfg.ListenAddress != "127.0.0.1" {
		t.Errorf("expected 127.0.0.1, got %s", cfg.ListenAddress)
	}
	if cfg.WebPort != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.WebPort)
	}
}

func TestReloadConfig_ReloadDetectsChanges(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "goal.json")

	// Initial config.
	cfgData := `{
		"version": 1,
		"listenAddress": "127.0.0.1",
		"webPort": 8080
	}`
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0644); err != nil {
		t.Fatal(err)
	}

	rc, err := NewReloadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Stop()

	// Wait a moment for mod time to settle.
	time.Sleep(100 * time.Millisecond)

	// Modify config.
	newCfgData := `{
		"version": 1,
		"listenAddress": "0.0.0.0",
		"webPort": 9090
	}`
	if err := os.WriteFile(cfgPath, []byte(newCfgData), 0644); err != nil {
		t.Fatal(err)
	}

	// Reload should detect changes.
	if err := rc.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	cfg := rc.Get()
	if cfg.ListenAddress != "0.0.0.0" {
		t.Errorf("expected 0.0.0.0 after reload, got %s", cfg.ListenAddress)
	}
	if cfg.WebPort != 9090 {
		t.Errorf("expected port 9090 after reload, got %d", cfg.WebPort)
	}
}

func TestReloadConfig_NoReloadWhenUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "goal.json")

	cfgData := `{
		"version": 1,
		"listenAddress": "127.0.0.1",
		"webPort": 8080
	}`
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0644); err != nil {
		t.Fatal(err)
	}

	rc, err := NewReloadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Stop()

	// Immediate reload should succeed without error (no changes).
	if err := rc.Reload(); err != nil {
		t.Fatalf("Reload should succeed even with no changes: %v", err)
	}

	// Config should remain unchanged.
	cfg := rc.Get()
	if cfg.ListenAddress != "127.0.0.1" {
		t.Errorf("expected 127.0.0.1, got %s", cfg.ListenAddress)
	}
}

func TestReloadConfig_WatchChannel(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "goal.json")

	cfgData := `{
		"version": 1,
		"listenAddress": "127.0.0.1",
		"webPort": 8080
	}`
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0644); err != nil {
		t.Fatal(err)
	}

	rc, err := NewReloadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	// Start watch in background.
	rc.StartWatch()

	// Give watch loop time to start.
	time.Sleep(50 * time.Millisecond)

	// Modify config.
	time.Sleep(100 * time.Millisecond) // Ensure mod time changes
	newCfgData := `{
		"version": 1,
		"listenAddress": "0.0.0.0",
		"webPort": 9090
	}`
	if err := os.WriteFile(cfgPath, []byte(newCfgData), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for watch loop to detect change and send on channel.
	select {
	case cfg := <-rc.Watch():
		if cfg.ListenAddress != "0.0.0.0" {
			t.Errorf("expected 0.0.0.0 via watch, got %s", cfg.ListenAddress)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for config update on watch channel")
	}

	rc.Stop()
}

func TestReloadConfig_InvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "goal.json")

	// Invalid: port out of range.
	cfgData := `{
		"version": 1,
		"listenAddress": "127.0.0.1",
		"webPort": 70000
	}`
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := NewReloadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid config, got nil")
	}
}

func TestReloadConfig_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "nonexistent.json")

	_, err := NewReloadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing config file, got nil")
	}
}

func TestReloadConfig_Save(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "goal.json")

	cfgData := `{
		"version": 1,
		"listenAddress": "127.0.0.1",
		"webPort": 8080,
		"adminUser": "admin",
		"adminPassword": "secret"
	}`
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0644); err != nil {
		t.Fatal(err)
	}

	rc, err := NewReloadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Stop()

	// Change config and save.
	rc.mu.Lock()
	rc.cfg.AdminUser = "newadmin"
	rc.mu.Unlock()

	if err := rc.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify saved file.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("failed to parse saved config: %v", err)
	}

	if loaded.AdminUser != "newadmin" {
		t.Errorf("expected admin user 'newadmin', got %s", loaded.AdminUser)
	}
	// Password should be cleared on save.
	if loaded.AdminPassword != "" {
		t.Error("expected password to be cleared on save")
	}
}

func TestReloadConfig_GetReturnsCopy(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "goal.json")

	cfgData := `{
		"version": 1,
		"listenAddress": "127.0.0.1",
		"webPort": 8080
	}`
	if err := os.WriteFile(cfgPath, []byte(cfgData), 0644); err != nil {
		t.Fatal(err)
	}

	rc, err := NewReloadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Stop()

	// Get config.
	cfg := rc.Get()

	// Modify returned config.
	cfg.ListenAddress = "0.0.0.0"

	// Get again - should be unchanged.
	cfg2 := rc.Get()
	if cfg2.ListenAddress != "127.0.0.1" {
		t.Errorf("expected Get to return copy, got %s", cfg2.ListenAddress)
	}
}

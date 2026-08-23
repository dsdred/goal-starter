package handlers

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/config"
	"github.com/dsdred/goal/internal/process"
	"github.com/dsdred/goal/internal/storage"
	"github.com/dsdred/goal/internal/webui/security"
)

// TestCredential_LiveAuthWiring proves the full flow:
// legacy plaintext config → MigrateCredentials → NewApp uses SetHash →
// login with the old plaintext password succeeds via the migrated hash.
func TestCredential_LiveAuthWiring(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "goal.json")

	// Step 1: Write a legacy config with plaintext password.
	legacy := config.Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       dir,
		AdminUser:     "admin",
		AdminPassword: "oldpass123",
		AuthEnabled:   true,
	}
	legacyData, _ := json.MarshalIndent(legacy, "", "  ")
	if err := os.WriteFile(configPath, legacyData, 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	// Step 2: Load and migrate (simulates main.go startup).
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg, migrated, err := config.MigrateCredentials(cfg, configPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration to occur")
	}
	if !config.IsBcryptHash(cfg.AdminPasswordHash) {
		t.Fatal("expected valid hash after migration")
	}
	if cfg.AdminPassword != "" {
		t.Fatal("plaintext should be cleared")
	}

	// Step 3: Verify on-disk state (no plaintext).
	data, _ := os.ReadFile(configPath)
	if bytes.Contains(data, []byte("oldpass123")) {
		t.Fatal("plaintext still on disk after migration")
	}

	// Step 4: Wire up the app exactly as server.go does (SetHash path).
	repo, err := storage.NewJSONRepository(filepath.Join(dir, "repo.json"))
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	_ = process.NewSupervisor(repo)
	passStore := security.NewPasswordStore()
	if err := passStore.SetHash(cfg.AdminUser, cfg.AdminPasswordHash); err != nil {
		t.Fatalf("SetHash: %v", err)
	}

	// Step 5: Login with the ORIGINAL plaintext password (now only exists as hash).
	sessionStore := security.NewSessionStore()
	csrf := security.NewCSRF()
	authHandler := NewAuthHandler(sessionStore, passStore, csrf)
	authHandler.WithAuthEnabled(true)

	body := `{"username":"admin","password":"oldpass123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	authHandler.Login(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login with old password after migration: expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	// Step 6: Login with WRONG password fails.
	body = `{"username":"admin","password":"wrongpass"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	authHandler.Login(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("login with wrong password: expected 401, got %d", w.Code)
	}
}

// TestCredential_Settings_RestartHint_Protocol verifies the hint semantics:
// - password-only change → hint "ok" (no restart needed)
// - port change → hint "restart_required"
func TestCredential_Settings_RestartHint(t *testing.T) {
	// Setup: config with existing hash.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "goal.json")
	hash, _ := config.HashPassword("existing")
	cfg := config.Config{
		Version:           2,
		ListenAddress:     "127.0.0.1",
		WebPort:           8088,
		DataDir:           dir,
		AdminUser:         "admin",
		AdminPasswordHash: hash,
		AuthEnabled:       true,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o600)

	h := NewSystemHandler(nil, nil, nil, nil)
	h.configPath = configPath
	h.listenAddr = "127.0.0.1"
	h.webPort = 8088
	h.authEnabled = true
	h.passStore = security.NewPasswordStore()

	// Case 1: Password-only change (same listen/port) → hint "ok".
	body := `{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":true,"admin_user":"admin","admin_password":"newpass"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["hint"] != "ok" {
		t.Errorf("password-only change: expected hint=ok, got %q", resp["hint"])
	}

	// Case 2: Port change → hint "restart_required".
	body = `{"listen_address":"127.0.0.1","web_port":9999,"auth_enabled":true,"admin_user":"admin"}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	h.SaveSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["hint"] != "restart_required" {
		t.Errorf("port change: expected hint=restart_required, got %q", resp["hint"])
	}
}

// TestCredential_Settings_Protocol_Protocol verifies that unrelated settings
// changes preserve the hash byte-for-byte.
func TestCredential_Settings_Protocol_Protocol(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "goal.json")
	hash, _ := config.HashPassword("stable")
	cfg := config.Config{
		Version:           2,
		ListenAddress:     "127.0.0.1",
		WebPort:           8088,
		DataDir:           dir,
		AdminUser:         "admin",
		AdminPasswordHash: hash,
		AuthEnabled:       true,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o600)

	h := NewSystemHandler(nil, nil, nil, nil)
	h.configPath = configPath
	h.listenAddr = "127.0.0.1"
	h.webPort = 8088
	h.authEnabled = true

	// Change only the port, no password.
	body := `{"listen_address":"127.0.0.1","web_port":9999,"auth_enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	saved, _ := config.Load(configPath)
	if saved.AdminPasswordHash != hash {
		t.Errorf("hash changed on unrelated save: expected %q, got %q", hash, saved.AdminPasswordHash)
	}
}

// TestCredential_72Byte_Protocol verifies the 72-byte boundary.
func TestCredential_72Byte_Protocol(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "goal.json")
	hash, _ := config.HashPassword("base")
	cfg := config.Config{
		Version:           2,
		ListenAddress:     "127.0.0.1",
		WebPort:           8088,
		DataDir:           dir,
		AdminUser:         "admin",
		AdminPasswordHash: hash,
		AuthEnabled:       true,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o600)

	h := NewSystemHandler(nil, nil, nil, nil)
	h.configPath = configPath
	h.listenAddr = "127.0.0.1"
	h.webPort = 8088
	h.authEnabled = true
	h.passStore = security.NewPasswordStore()

	// 73 bytes → rejected.
	long73 := make([]byte, 73)
	for i := range long73 {
		long73[i] = 'a'
	}
	body := `{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":true,"admin_user":"admin","admin_password":"` + string(long73) + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("73-byte password: expected 400, got %d, body: %s", w.Code, w.Body.String())
	}

	// 72 bytes → accepted.
	exact72 := make([]byte, 72)
	for i := range exact72 {
		exact72[i] = 'b'
	}
	body = `{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":true,"admin_user":"admin","admin_password":"` + string(exact72) + `"}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w = httptest.NewRecorder()
	h.SaveSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("72-byte password: expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
	saved, _ := config.Load(configPath)
	if !config.IsBcryptHash(saved.AdminPasswordHash) {
		t.Error("72-byte password: expected valid hash")
	}
}

// TestCredential_MalformedHash_StartupValidation verifies that a malformed
// persisted hash causes a clear validation error.
func TestCredential_MalformedHash_StartupValidation(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "goal.json")
	cfg := config.Config{
		Version:           2,
		ListenAddress:     "127.0.0.1",
		WebPort:           8088,
		DataDir:           dir,
		AdminUser:         "admin",
		AdminPasswordHash: "not-a-real-hash",
		AuthEnabled:       true,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o600)

	// Validate should reject it.
	loaded, err := config.Load(configPath)
	if err == nil {
		t.Fatal("expected load validation error for malformed hash")
	}
	_ = loaded
}

// TestCredential_API_NoSecretExposure proves the metrics endpoint never
// returns the hash or plaintext.
func TestCredential_API_NoSecretExposure(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "goal.json")
	hash, _ := config.HashPassword("supersecret")
	cfg := config.Config{
		Version:           2,
		ListenAddress:     "127.0.0.1",
		WebPort:           8088,
		DataDir:           dir,
		AdminUser:         "admin",
		AdminPasswordHash: hash,
		AuthEnabled:       true,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o600)

	repo, _ := storage.NewJSONRepository(filepath.Join(dir, "repo.json"))
	supervisor := process.NewSupervisor(repo)
	h := NewSystemHandler(supervisor, nil, nil, application.NewInstanceService(supervisor, repo))
	h.configPath = configPath
	h.listenAddr = "127.0.0.1"
	h.webPort = 8088
	h.authEnabled = true

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	w := httptest.NewRecorder()
	h.Metrics(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if bytes.Contains([]byte(body), []byte(hash)) {
		t.Error("metrics response leaked the hash")
	}
	if bytes.Contains([]byte(body), []byte("supersecret")) {
		t.Error("metrics response leaked the plaintext")
	}
	var resp map[string]any
	json.Unmarshal([]byte(body), &resp)
	if resp["admin_password_set"] != true {
		t.Error("admin_password_set should be true")
	}
}

// TestCredential_Settings_Rotation_OldFails_NewWorks proves that after a
// password rotation via settings, the old password fails and the new one works.
func TestCredential_Settings_Rotation_OldFails_NewWorks(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "goal.json")
	oldHash, _ := config.HashPassword("oldpass")
	cfg := config.Config{
		Version:           2,
		ListenAddress:     "127.0.0.1",
		WebPort:           8088,
		DataDir:           dir,
		AdminUser:         "admin",
		AdminPasswordHash: oldHash,
		AuthEnabled:       true,
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(configPath, data, 0o600)

	passStore := security.NewPasswordStore()
	passStore.SetHash("admin", oldHash)

	h := NewSystemHandler(nil, nil, nil, nil)
	h.configPath = configPath
	h.listenAddr = "127.0.0.1"
	h.webPort = 8088
	h.authEnabled = true
	h.passStore = passStore

	// Rotate password.
	body := `{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":true,"admin_user":"admin","admin_password":"newpass999"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rotation: expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	// Old password fails.
	if passStore.ValidateCredentials("admin", "oldpass") {
		t.Error("old password should fail after rotation")
	}
	// New password works.
	if !passStore.ValidateCredentials("admin", "newpass999") {
		t.Error("new password should work after rotation")
	}
}

var _ fs.FS = fstest.MapFS{}

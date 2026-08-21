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

func newTestConfigFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "goal.json")
	cfg := config.Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       dir,
		AdminUser:     "admin",
		AdminPassword: "secret123",
		AuthEnabled:   false,
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	return path
}

func newTestSettingsHandler(t *testing.T, configPath string) *SystemHandler {
	t.Helper()
	h := NewSystemHandler(nil, nil, nil, nil)
	h.configPath = configPath
	h.listenAddr = "127.0.0.1"
	h.webPort = 8088
	h.authEnabled = false
	return h
}

func TestSettings_Save_Success(t *testing.T) {
	path := newTestConfigFile(t)
	h := newTestSettingsHandler(t, path)

	body := `{"listen_address":"0.0.0.0","web_port":9090,"auth_enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "saved" {
		t.Errorf("expected status=saved, got %q", resp["status"])
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	if saved.ListenAddress != "0.0.0.0" {
		t.Errorf("listen: expected 0.0.0.0, got %q", saved.ListenAddress)
	}
	if saved.WebPort != 9090 {
		t.Errorf("port: expected 9090, got %d", saved.WebPort)
	}
	if !saved.AuthEnabled {
		t.Error("auth: expected true")
	}
}

func TestSettings_Save_PreservesUnrelatedFields(t *testing.T) {
	path := newTestConfigFile(t)
	h := newTestSettingsHandler(t, path)

	// Set a data dir and add a runtime to the config first.
	cfg, _ := config.Load(path)
	cfg.DataDir = "/custom/data"
	cfg.Runtimes = []config.Runtime{{ID: "rt-1", Name: "Test RT", Executable: "test.exe"}}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	body := `{"listen_address":"192.168.1.5","web_port":7777,"auth_enabled":false}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	saved, _ := config.Load(path)
	if saved.DataDir != "/custom/data" {
		t.Errorf("DataDir lost: expected /custom/data, got %q", saved.DataDir)
	}
	if len(saved.Runtimes) != 1 || saved.Runtimes[0].Name != "Test RT" {
		t.Errorf("Runtimes lost: got %+v", saved.Runtimes)
	}
	if saved.ListenAddress != "192.168.1.5" {
		t.Errorf("listen not updated: got %q", saved.ListenAddress)
	}
}

func TestSettings_Save_PreservesAdminCredentials(t *testing.T) {
	path := newTestConfigFile(t)
	h := newTestSettingsHandler(t, path)

	body := `{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":false}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	saved, _ := config.Load(path)
	if saved.AdminUser != "admin" {
		t.Errorf("AdminUser lost: expected admin, got %q", saved.AdminUser)
	}
	if saved.AdminPassword != "secret123" {
		t.Errorf("AdminPassword lost: expected secret123, got %q", saved.AdminPassword)
	}
}

func TestSettings_Save_InvalidPort(t *testing.T) {
	path := newTestConfigFile(t)
	h := newTestSettingsHandler(t, path)

	body := `{"listen_address":"127.0.0.1","web_port":99999,"auth_enabled":false}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	// Config file must be unchanged.
	saved, _ := config.Load(path)
	if saved.WebPort != 8088 {
		t.Errorf("config changed on error: port=%d", saved.WebPort)
	}
}

func TestSettings_Save_EmptyListenAddress(t *testing.T) {
	path := newTestConfigFile(t)
	h := newTestSettingsHandler(t, path)

	body := `{"listen_address":"","web_port":8088,"auth_enabled":false}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	saved, _ := config.Load(path)
	if saved.ListenAddress != "127.0.0.1" {
		t.Errorf("config changed on error: addr=%q", saved.ListenAddress)
	}
}

func TestSettings_Save_InvalidListenAddress(t *testing.T) {
	path := newTestConfigFile(t)
	h := newTestSettingsHandler(t, path)

	for _, bad := range []string{"has spaces", "http://bad", "a/b", "addr:8080"} {
		body := `{"listen_address":"` + bad + `","web_port":8088,"auth_enabled":false}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		h.SaveSettings(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("addr=%q: expected 400, got %d", bad, w.Code)
		}
	}

	saved, _ := config.Load(path)
	if saved.ListenAddress != "127.0.0.1" {
		t.Errorf("config changed: got %q", saved.ListenAddress)
	}
}

func TestSettings_Save_ValidListenAddresses(t *testing.T) {
	path := newTestConfigFile(t)
	h := newTestSettingsHandler(t, path)

	for _, good := range []string{"127.0.0.1", "0.0.0.0", "::1", "::", "*", "192.168.1.1"} {
		body := `{"listen_address":"` + good + `","web_port":8088,"auth_enabled":false}`
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		h.SaveSettings(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("addr=%q: expected 200, got %d, body: %s", good, w.Code, w.Body.String())
		}
	}
}

func TestSettings_Save_AuthEnabled_WithoutCredentials(t *testing.T) {
	path := newTestConfigFile(t)
	h := newTestSettingsHandler(t, path)

	// Create a config WITHOUT admin credentials.
	cfg := config.Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       filepath.Dir(path),
		AdminUser:     "",
		AdminPassword: "",
		AuthEnabled:   false,
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	body := `{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)

	// Should fail with 400 (client validation error).
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body: %s", w.Code, w.Body.String())
	}

	// Config unchanged.
	saved, _ := config.Load(path)
	if saved.AuthEnabled {
		t.Error("auth was enabled despite missing credentials")
	}
}

func TestSettings_Save_NoConfigPath(t *testing.T) {
	h := NewSystemHandler(nil, nil, nil, nil)
	h.configPath = ""

	body := `{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":false}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestSettings_Save_AtomicWrite(t *testing.T) {
	path := newTestConfigFile(t)
	h := newTestSettingsHandler(t, path)

	body := `{"listen_address":"127.0.0.1","web_port":9999,"auth_enabled":false}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.SaveSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify no .tmp file remains (atomic rename completed).
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file remains after save (non-atomic write)")
	}

	// Verify the file is readable and valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Errorf("config is not valid JSON after save: %v", err)
	}
	if cfg.WebPort != 9999 {
		t.Errorf("port not saved: got %d", cfg.WebPort)
	}
}

// --- Security tests for PUT /api/v1/settings ---

func newSettingsSecurityRouter(t *testing.T, configPath string) http.Handler {
	t.Helper()
	repo, err := storage.NewJSONRepository(filepath.Join(t.TempDir(), "repo.json"))
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	supervisor := process.NewSupervisor(repo)
	passwords := security.NewPasswordStore()
	if err := passwords.SetPassword("admin", "secret"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	assets := fstest.MapFS{
		"templates/index.html": &fstest.MapFile{Data: []byte("<!doctype html>")},
		"static/app.js":        &fstest.MapFile{Data: []byte("'use strict';")},
	}
	return NewRouteRegistry(
		application.NewInstanceService(supervisor, repo),
		application.NewRuntimeService(repo),
		application.NewModelService(repo),
		supervisor,
		repo,
		security.NewCSRF(),
		security.NewSessionStore(),
		passwords,
		WithAuthEnabled(true),
		WithWebAssets(fs.FS(assets), fs.FS(assets)),
		WithConfigPath(configPath),
	).Build()
}

func loginAndGetCookies(t *testing.T, router http.Handler) (string, []*http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return body["csrf_token"], w.Result().Cookies()
}

func TestSettings_Security_NoSession(t *testing.T) {
	path := newTestConfigFile(t)
	router := newSettingsSecurityRouter(t, path)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(`{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestSettings_Security_MissingCSRF(t *testing.T) {
	path := newTestConfigFile(t)
	router := newSettingsSecurityRouter(t, path)
	_, cookies := loginAndGetCookies(t, router)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(`{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	// No X-CSRF-Token header.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (missing CSRF), got %d", w.Code)
	}
	// Config unchanged.
	cfg, _ := config.Load(path)
	if cfg.WebPort != 8088 {
		t.Errorf("config changed: port=%d", cfg.WebPort)
	}
}

func TestSettings_Security_WrongCSRF(t *testing.T) {
	path := newTestConfigFile(t)
	router := newSettingsSecurityRouter(t, path)
	_, cookies := loginAndGetCookies(t, router)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(`{"listen_address":"127.0.0.1","web_port":8088,"auth_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "wrong-token")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (wrong CSRF), got %d", w.Code)
	}
}

func TestSettings_Security_ValidSessionAndCSRF(t *testing.T) {
	path := newTestConfigFile(t)
	router := newSettingsSecurityRouter(t, path)
	csrfToken, cookies := loginAndGetCookies(t, router)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(`{"listen_address":"127.0.0.1","web_port":9999,"auth_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfToken)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
	cfg, _ := config.Load(path)
	if cfg.WebPort != 9999 {
		t.Errorf("port not saved: got %d", cfg.WebPort)
	}
}

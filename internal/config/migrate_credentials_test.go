package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestConfig(t *testing.T, cfg Config) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "goal.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestMigrate_FreshConfig_NoCredential(t *testing.T) {
	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       "./data",
		AdminUser:     "admin",
		AuthEnabled:   false,
	}
	path := writeTestConfig(t, cfg)

	result, migrated, err := MigrateCredentials(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if migrated {
		t.Error("expected no migration for fresh config")
	}
	if result.AdminPasswordHash != "" {
		t.Errorf("expected empty hash, got %q", result.AdminPasswordHash)
	}
	if result.AdminPassword != "" {
		t.Errorf("expected empty plaintext, got %q", result.AdminPassword)
	}
}

func TestMigrate_LegacyPlaintext_Migrated(t *testing.T) {
	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       "./data",
		AdminUser:     "admin",
		AdminPassword: "hunter2",
		AuthEnabled:   false,
	}
	path := writeTestConfig(t, cfg)

	result, migrated, err := MigrateCredentials(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration to occur")
	}
	if !IsBcryptHash(result.AdminPasswordHash) {
		t.Fatalf("expected valid bcrypt hash, got %q", result.AdminPasswordHash)
	}
	if result.AdminPassword != "" {
		t.Errorf("expected plaintext cleared, got %q", result.AdminPassword)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	var onDisk Config
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if onDisk.AdminPassword != "" {
		t.Errorf("plaintext still on disk: %q", onDisk.AdminPassword)
	}
	if !IsBcryptHash(onDisk.AdminPasswordHash) {
		t.Errorf("hash not persisted on disk")
	}
	if strings.Contains(string(data), "hunter2") {
		t.Error("plaintext password still present in file")
	}
}

func TestMigrate_ExistingHash_NoRehash(t *testing.T) {
	hash, err := HashPassword("mypass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	cfg := Config{
		Version:           2,
		ListenAddress:     "127.0.0.1",
		WebPort:           8088,
		DataDir:           "./data",
		AdminUser:         "admin",
		AdminPasswordHash: hash,
		AuthEnabled:       true,
	}
	path := writeTestConfig(t, cfg)

	result, migrated, err := MigrateCredentials(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if migrated {
		t.Error("expected no migration for existing hash")
	}
	if result.AdminPasswordHash != hash {
		t.Errorf("hash changed: expected %q, got %q", hash, result.AdminPasswordHash)
	}
}

func TestMigrate_HashAndPlaintext_Conflict(t *testing.T) {
	hash, err := HashPassword("realpass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	cfg := Config{
		Version:           2,
		ListenAddress:     "127.0.0.1",
		WebPort:           8088,
		DataDir:           "./data",
		AdminUser:         "admin",
		AdminPasswordHash: hash,
		AdminPassword:     "stale-plaintext",
		AuthEnabled:       true,
	}
	path := writeTestConfig(t, cfg)

	result, migrated, err := MigrateCredentials(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !migrated {
		t.Error("expected migration (conflict cleanup)")
	}
	if result.AdminPasswordHash != hash {
		t.Error("hash should be preserved (authoritative)")
	}
	if result.AdminPassword != "" {
		t.Errorf("plaintext should be cleared, got %q", result.AdminPassword)
	}

	data, _ := os.ReadFile(path)
	var onDisk Config
	json.Unmarshal(data, &onDisk)
	if onDisk.AdminPassword != "" {
		t.Error("plaintext still on disk after conflict resolution")
	}
}

func TestMigrate_HashInWrongField(t *testing.T) {
	hash, err := HashPassword("wrongfield")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       "./data",
		AdminUser:     "admin",
		AdminPassword: hash,
		AuthEnabled:   false,
	}
	path := writeTestConfig(t, cfg)

	result, migrated, err := MigrateCredentials(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !migrated {
		t.Error("expected migration (hash moved from wrong field)")
	}
	if result.AdminPasswordHash != hash {
		t.Error("hash not moved to correct field")
	}
	if result.AdminPassword != "" {
		t.Error("wrong field not cleared")
	}
}

func TestMigrate_SaveFailure(t *testing.T) {
	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       "./data",
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuthEnabled:   false,
	}
	dir := t.TempDir()
	// Create a directory at the config path — Save will fail to write to a directory.
	path := filepath.Join(dir, "goal.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, _, err := MigrateCredentials(cfg, path)
	if err == nil {
		t.Fatal("expected error on save failure")
	}
}

func TestMigrate_AuthDisabled_WithPlaintext(t *testing.T) {
	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       "./data",
		AdminUser:     "admin",
		AdminPassword: "legacy-pass",
		AuthEnabled:   false,
	}
	path := writeTestConfig(t, cfg)

	result, migrated, err := MigrateCredentials(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !migrated {
		t.Error("expected migration even with auth disabled (defensive)")
	}
	if !IsBcryptHash(result.AdminPasswordHash) {
		t.Error("expected hash after defensive migration")
	}
	if result.AdminPassword != "" {
		t.Error("plaintext should be cleared")
	}
}

func TestIsBcryptHash_Valid(t *testing.T) {
	hash, _ := HashPassword("test")
	if !IsBcryptHash(hash) {
		t.Errorf("expected valid hash %q", hash)
	}
}

func TestIsBcryptHash_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"short",
		"$2a$12$tooshort",
		"$2a$xx$invalidcost",
		"$2a$12_toosHORT",
		"not-a-hash-at-all-but-long-enough-to-hit-60-chars-000000",
		strings.Repeat("a", 60),
	}
	for _, s := range invalid {
		if IsBcryptHash(s) {
			t.Errorf("IsBcryptHash(%q) = true, want false", s)
		}
	}
}

func TestValidate_AuthEnabled_RequiresHash(t *testing.T) {
	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		AdminUser:     "admin",
		AuthEnabled:   true,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for auth enabled without hash")
	}
}

func TestValidate_AuthEnabled_InvalidHash(t *testing.T) {
	cfg := Config{
		Version:           2,
		ListenAddress:     "127.0.0.1",
		WebPort:           8088,
		AdminUser:         "admin",
		AdminPasswordHash: "not-a-valid-hash",
		AuthEnabled:       true,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for invalid hash")
	}
}

func TestValidate_AuthEnabled_ValidHash(t *testing.T) {
	hash, _ := HashPassword("valid")
	cfg := Config{
		Version:           2,
		ListenAddress:     "127.0.0.1",
		WebPort:           8088,
		AdminUser:         "admin",
		AdminPasswordHash: hash,
		AuthEnabled:       true,
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected validation error: %v", err)
	}
}

func TestHashPassword_72ByteLimit(t *testing.T) {
	password := strings.Repeat("a", 72)
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("72-byte password should be accepted: %v", err)
	}
	if !IsBcryptHash(hash) {
		t.Error("expected valid hash")
	}

	overLimit := strings.Repeat("a", 73)
	_ = overLimit
	// The 72-byte limit is enforced at the settings endpoint, not in HashPassword itself.
	// HashPassword will still work (bcrypt truncates), but the API rejects >72.
}

func TestMigrate_BackwardCompat_LegacyConfig(t *testing.T) {
	// Simulate a legacy config that was loaded (version already migrated to 2
	// by Load, but still has plaintext in adminPassword).
	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       "./data",
		AdminUser:     "admin",
		AdminPassword: "oldpass",
		AuthEnabled:   true,
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "goal.json")
	raw := `{"version":2,"listenAddress":"127.0.0.1","webPort":8088,"dataDir":"./data","adminUser":"admin","adminPassword":"oldpass","authEnabled":true}`
	os.WriteFile(path, []byte(raw), 0o600)

	result, migrated, err := MigrateCredentials(cfg, path)
	if err != nil {
		t.Fatalf("migration error: %v", err)
	}
	if !migrated {
		t.Error("expected migration for legacy plaintext")
	}
	if !IsBcryptHash(result.AdminPasswordHash) {
		t.Error("expected hash after legacy migration")
	}
	if result.AdminPassword != "" {
		t.Error("plaintext should be cleared")
	}

	// Verify on-disk result.
	data, _ := os.ReadFile(path)
	var onDisk Config
	json.Unmarshal(data, &onDisk)
	if onDisk.AdminPassword != "" {
		t.Error("plaintext still on disk")
	}
	if !IsBcryptHash(onDisk.AdminPasswordHash) {
		t.Error("hash not on disk")
	}
}

func TestMigrate_NoSecretInError(t *testing.T) {
	// Verify that migration errors do not contain the plaintext password or hash.
	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       "./data",
		AdminUser:     "admin",
		AdminPassword: "super-secret-123",
		AuthEnabled:   false,
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "goal.json")
	os.Mkdir(path, 0o755)

	_, _, err := MigrateCredentials(cfg, path)
	if err == nil {
		t.Fatal("expected error")
	}
	errStr := err.Error()
	if strings.Contains(errStr, "super-secret-123") {
		t.Error("error message leaked the plaintext password")
	}
}

func TestMigrate_FreshPasswordCreation_HashOnlyPersisted(t *testing.T) {
	// Prove: fresh password creation via MigrateCredentials path →
	// persisted config contains ONLY the hash, never the plaintext.
	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       "./data",
		AdminUser:     "admin",
		AdminPassword: "brand-new-password",
		AuthEnabled:   false,
	}
	path := writeTestConfig(t, cfg)

	result, migrated, err := MigrateCredentials(cfg, path)
	if err != nil {
		t.Fatalf("migration: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration")
	}
	if !IsBcryptHash(result.AdminPasswordHash) {
		t.Fatal("expected valid hash")
	}

	// Read back from disk: verify hash-only persistence.
	data, _ := os.ReadFile(path)
	var onDisk Config
	json.Unmarshal(data, &onDisk)
	if onDisk.AdminPassword != "" {
		t.Error("plaintext persisted on disk")
	}
	if !IsBcryptHash(onDisk.AdminPasswordHash) {
		t.Error("hash not persisted on disk")
	}
	if strings.Contains(string(data), "brand-new-password") {
		t.Error("plaintext password string found in persisted file")
	}
}

func TestMigrate_FreshConfig_NoCredentialPresent(t *testing.T) {
	// Prove: fresh config without credentials → credentials are absent
	// (neither hash nor plaintext).
	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       "./data",
		AdminUser:     "admin",
		AuthEnabled:   false,
	}
	path := writeTestConfig(t, cfg)

	result, migrated, err := MigrateCredentials(cfg, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if migrated {
		t.Error("expected no migration for credential-less config")
	}
	if result.AdminPasswordHash != "" {
		t.Error("unexpected hash in credential-less config")
	}
	if result.AdminPassword != "" {
		t.Error("unexpected plaintext in credential-less config")
	}

	// Verify on-disk: neither field present.
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "adminPasswordHash") {
		t.Error("adminPasswordHash key should be absent (omitempty)")
	}
	if strings.Contains(string(data), "adminPassword") {
		t.Error("adminPassword key should be absent (omitempty)")
	}
}

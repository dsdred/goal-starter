package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSave_DurableBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goal.json")

	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       dir,
		AdminUser:     "admin",
		AuthEnabled:   false,
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}

	cfg.WebPort = 9999
	if err := Save(path, cfg); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup missing after second save: %v", err)
	}
	if string(first) == string(second) {
		t.Fatal("second save must change the file")
	}
	if string(first) != string(bak) {
		t.Fatal("backup must equal the previous config byte-for-byte")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}
}

func TestSave_NoBackupOnFirstWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goal.json")

	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       dir,
		AdminUser:     "admin",
		AuthEnabled:   false,
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatal("backup must not exist for the first write")
	}
}

func TestSave_FailureLeavesPreviousFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only permission model")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "goal.json")

	cfg := Config{
		Version:       2,
		ListenAddress: "127.0.0.1",
		WebPort:       8088,
		DataDir:       dir,
		AdminUser:     "admin",
		AuthEnabled:   false,
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	cfg.WebPort = 7777
	if err := Save(path, cfg); err == nil {
		t.Fatal("expected save failure on read-only directory")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after failure: %v", err)
	}
	if string(after) != string(first) {
		t.Fatal("config file must remain the previous state after a failed save")
	}
}

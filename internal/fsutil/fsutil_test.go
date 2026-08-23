package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileDurable_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")

	if err := WriteFileDurable(path, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatalf("WriteFileDurable: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != `{"a":1}` {
		t.Fatalf("content mismatch: %q", data)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind after successful write")
	}
}

func TestWriteFileDurable_BackupOnSecondWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")

	if err := WriteFileDurable(path, []byte("v1"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteFileDurable(path, []byte("v2"), 0o600); err != nil {
		t.Fatalf("second write: %v", err)
	}

	main, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read main: %v", err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(main) != "v2" {
		t.Errorf("main = %q, want v2", main)
	}
	if string(bak) != "v1" {
		t.Errorf("backup = %q, want v1", bak)
	}
}

func TestWriteFileDurable_NoBackupForNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.json")

	if err := WriteFileDurable(path, []byte("v1"), 0o600); err != nil {
		t.Fatalf("WriteFileDurable: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatal("backup must not exist for the first write")
	}
}

func TestWriteFileDurable_MissingDirError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "data.json")

	if err := WriteFileDurable(path, []byte("x"), 0o600); err == nil {
		t.Fatal("expected error for missing directory")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind after failure")
	}
}

func TestWriteFileDurable_ReadonlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only permission model")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := WriteFileDurable(filepath.Join(dir, "x.json"), []byte("x"), 0o600); err == nil {
		t.Fatal("expected error writing into read-only directory")
	}
}

func TestWriteFileDurable_TargetUnchangedOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only permission model")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	if err := WriteFileDurable(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := WriteFileDurable(path, []byte("replaced"), 0o600); err == nil {
		t.Fatal("expected error on read-only directory")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("target changed on failed write: %q", data)
	}
}

func TestCopyFileAtomic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.json")
	dst := filepath.Join(dir, "dst.json")
	if err := os.WriteFile(src, []byte("source-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old-content"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CopyFileAtomic(src, dst); err != nil {
		t.Fatalf("CopyFileAtomic: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "source-content" {
		t.Fatalf("dst = %q, want source-content", data)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("backup temp left behind")
	}
}

func TestSyncDir(t *testing.T) {
	if err := SyncDir(t.TempDir()); err != nil {
		t.Fatalf("SyncDir on existing dir: %v", err)
	}
}

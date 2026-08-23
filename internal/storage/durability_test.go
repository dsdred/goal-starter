package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dsdred/goal/internal/domain"
)

func TestJSONRepository_DurableWriteBackupChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goal_repo.json")

	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	if err := repo.CreateRuntime(&domain.RuntimeEntry{Name: "rt-a", Executable: "rt-a"}); err != nil {
		t.Fatalf("CreateRuntime rt-a: %v", err)
	}
	main1, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read main v1: %v", err)
	}
	if err := repo.CreateRuntime(&domain.RuntimeEntry{Name: "rt-b", Executable: "rt-b"}); err != nil {
		t.Fatalf("CreateRuntime rt-b: %v", err)
	}
	main2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read main v2: %v", err)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	if !bytes.Contains(main2, []byte("rt-b")) {
		t.Fatal("main v2 must contain rt-b")
	}
	if bytes.Contains(main1, []byte("rt-b")) {
		t.Fatal("main v1 must not contain rt-b")
	}
	if !bytes.Equal(main1, bak) {
		t.Fatal("backup must equal the previous main state byte-for-byte")
	}
	if bytes.Contains(bak, []byte("rt-b")) {
		t.Fatal("backup must be the previous generation (no rt-b)")
	}
}

func TestJSONRepository_SaveFailurePropagates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only permission model")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "goal_repo.json")

	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := repo.CreateRuntime(&domain.RuntimeEntry{Name: "rt-x", Executable: "rt-x"}); err == nil {
		t.Fatal("expected save error on read-only directory, got nil")
	}
	// The on-disk file must not contain the unsaved entity.
	if data, rerr := os.ReadFile(path); rerr == nil && bytes.Contains(data, []byte("rt-x")) {
		t.Fatal("failed save must not persist the entity")
	}
	if _, rerr := os.Stat(path + ".tmp"); !os.IsNotExist(rerr) {
		t.Fatal("temp file left behind after failed save")
	}
}

func TestJSONRepository_SaveUnifiedDurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "goal_repo.json")

	repo, err := NewJSONRepository(path)
	if err != nil {
		t.Fatalf("NewJSONRepository: %v", err)
	}
	if err := repo.CreateModel(&domain.ModelEntry{Name: "m1", RuntimeID: "rt-none"}); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	alt := filepath.Join(dir, "unified.json")
	if err := repo.SaveUnified(alt); err != nil {
		t.Fatalf("SaveUnified: %v", err)
	}
	data, err := os.ReadFile(alt)
	if err != nil {
		t.Fatalf("read unified: %v", err)
	}
	if !bytes.Contains(data, []byte("m1")) {
		t.Fatal("unified file must contain the model")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(alt)
		if err != nil {
			t.Fatalf("stat unified: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("unified file perm = %o, want 600", perm)
		}
	}

	if err := repo.SaveUnified(alt); err != nil {
		t.Fatalf("SaveUnified second: %v", err)
	}
	if _, err := os.Stat(alt + ".bak"); err != nil {
		t.Fatalf("SaveUnified must back up the previous file: %v", err)
	}
}

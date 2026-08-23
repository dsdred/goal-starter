package fsutil

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// SyncDir flushes directory metadata to durable storage.
//
// POSIX: a rename is durable only after the containing directory is fsynced;
// this is that step, and a failure here is a failed write.
//
// Windows: no supported directory-flush API exists (there is no documented
// FlushFileBuffers behavior for directory handles, and the standard library
// exposes none), so this is a no-op. Rename durability on Windows is provided
// by the file system instead: on a journaling NTFS volume the rename
// transaction is committed to the NTFS log before MoveFileExW returns, and
// the file data was already flushed with FlushFileBuffers (File.Sync) before
// the rename. The guarantee does not hold on non-journaled volumes (FAT/exFAT,
// or NTFS with journaling disabled).
func SyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync directory %s: %w", dir, err)
	}
	return nil
}

// CopyFileAtomic atomically replaces dst with a durable copy of src:
// copy to dst.tmp, fsync, rename over dst, sync the directory.
func CopyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %s: %w", src, err)
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open backup temp %s: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("copy to backup temp: %w", err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync backup temp: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close backup temp: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename backup: %w", err)
	}
	return SyncDir(filepath.Dir(dst))
}

// WriteFileDurable atomically and durably writes data to path:
//  1. write data to path.tmp in the same directory and fsync it
//  2. verify the written bytes by reading them back
//  3. if path exists, atomically preserve it as path.bak (backup before every write)
//  4. rename path.tmp to path
//  5. sync the parent directory (Windows: see SyncDir)
//
// A partially written file is never left at path: on any failure the target
// is either unchanged or durably replaced. Failures are returned, never
// swallowed.
func WriteFileDurable(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := writeSynced(tmp, data, perm); err != nil {
		return err
	}

	written, err := os.ReadFile(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("read back temp file: %w", err)
	}
	if !bytes.Equal(written, data) {
		_ = os.Remove(tmp)
		return errors.New("written data does not match source (read-back verification failed)")
	}

	if _, err := os.Stat(path); err == nil {
		if err := CopyFileAtomic(path, path+".bak"); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("create backup before write: %w", err)
		}
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return SyncDir(filepath.Dir(path))
}

func writeSynced(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("open file %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("sync file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close file: %w", err)
	}
	return nil
}

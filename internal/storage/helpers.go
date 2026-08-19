package storage

import (
	"io"
	"os"
	"runtime"
)

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	tmp := dst + ".tmp"
	tmpFile, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		out.Close()
		return err
	}

	if _, err = io.Copy(tmpFile, in); err != nil {
		tmpFile.Close()
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err = tmpFile.Sync(); err != nil {
		tmpFile.Close()
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err = tmpFile.Close(); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}

	if err = out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, dst)
}

func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

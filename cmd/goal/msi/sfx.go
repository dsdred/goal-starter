package msi

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// SFXBuilder creates a self-extracting archive installer.
// Works on both Windows and Linux without external dependencies.
type SFXBuilder struct {
	BinaryPath    string
	ExampleConfig string
	ReadmePath    string
	ReadmeRuPath  string
	OutputPath    string
	Version       string
}

// Build creates a self-extracting archive installer.
func (b *SFXBuilder) Build() error {
	if err := b.validate(); err != nil {
		return fmt.Errorf("sfx builder validation: %w", err)
	}

	// Create the archive
	if err := b.createArchive(); err != nil {
		return fmt.Errorf("create archive: %w", err)
	}

	fmt.Printf("[+] Self-extracting installer created: %s\n", b.OutputPath)
	return nil
}

func (b *SFXBuilder) validate() error {
	if b.BinaryPath == "" {
		return fmt.Errorf("binary path is required")
	}
	if _, err := os.Stat(b.BinaryPath); os.IsNotExist(err) {
		return fmt.Errorf("binary not found: %s", b.BinaryPath)
	}

	if b.Version == "" {
		return fmt.Errorf("version is required")
	}

	if b.OutputPath == "" {
		return fmt.Errorf("output path is required")
	}

	return nil
}

func (b *SFXBuilder) createArchive() error {
	// Create output file
	out, err := os.Create(b.OutputPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer out.Close()

	w := zip.NewWriter(out)

	// Add files to archive
	files := []struct {
		src, dst string
	}{
		{b.BinaryPath, "goal.exe"},
		{b.ExampleConfig, "goal.example.json"},
		{b.ReadmePath, "README.md"},
		{b.ReadmeRuPath, "README_RU.md"},
	}

	for _, f := range files {
		if err := b.addFile(w, f.src, f.dst); err != nil {
			return fmt.Errorf("add %s: %w", f.dst, err)
		}
	}

	// Add install script for self-extraction
	if err := b.addInstallScript(w); err != nil {
		return fmt.Errorf("add install script: %w", err)
	}

	return w.Close()
}

func (b *SFXBuilder) addFile(w *zip.Writer, src, dst string) error {
	// Create directory in archive
	dir := filepath.Dir(dst)
	if dir != "." {
		if err := addDirectory(w, dir); err != nil {
			return err
		}
	}

	// Open source file
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer f.Close()

	// Create zip entry
	zf, err := w.Create(dst)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", dst, err)
	}

	_, err = io.Copy(zf, f)
	return err
}

func addDirectory(w *zip.Writer, dir string) error {
	h := &zip.FileHeader{
		Name:               dir,
		Method:             zip.Store,
		UncompressedSize64: 0,
	}
	_, err := w.CreateHeader(h)
	return err
}

func (b *SFXBuilder) addInstallScript(w *zip.Writer) error {
	script := fmt.Sprintf(`@echo off
setlocal
echo Installing GoAl v%s...
set INSTALL_DIR=%%PROGRAMFILES%%\GoAl
if not exist "%%INSTALL_DIR%%" mkdir "%%INSTALL_DIR%%"

:: Extract files
for %%f in (goal.exe goal.example.json README.md README_RU.md) do (
    if exist "%%f" (
        move /y "%%f" "%%INSTALL_DIR%%\\" >nul 2>&1
    )
)

echo GoAl v%s installed to %%INSTALL_DIR%%
echo.
echo Next steps:
echo   1. Copy goal.example.json to goal.json and edit it.
echo   2. Install as Windows service (as Administrator):
echo      %%INSTALL_DIR%%\\goal.exe --service install --config "%%INSTALL_DIR%%\\goal.json"
echo      %%INSTALL_DIR%%\\goal.exe --service start
echo.
pause
`, b.Version, b.Version)

	zf, err := w.Create("install.bat")
	if err != nil {
		return err
	}
	_, err = zf.Write([]byte(script))
	return err
}

// BuildSFX creates a self-extracting installer.
func BuildSFX(binaryPath, exampleConfig, readme, readmeRu, output, version string) error {
	return (&SFXBuilder{
		BinaryPath:    binaryPath,
		ExampleConfig: exampleConfig,
		ReadmePath:    readme,
		ReadmeRuPath:  readmeRu,
		OutputPath:    output,
		Version:       version,
	}).Build()
}

// CheckWiXAvailability checks if WiX Toolset is available.
func CheckWiXAvailability() error {
	// Try to find in PATH first
	for _, tool := range []string{"candle.exe", "light.exe"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("WiX tool '%s' not found in PATH", tool)
		}
	}
	return nil
}

// GetInstallerType returns whether WiX is available.
func GetInstallerType() string {
	if runtime.GOOS == "windows" {
		if err := CheckWiXAvailability(); err == nil {
			return "msi"
		}
	}
	return "sfx"
}

package msi

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Builder creates MSI installer using WiX Toolset.
type Builder struct {
	// Paths configuration
	BinaryPath      string
	InstallScript   string
	UninstallScript string
	ExampleConfig   string
	ReadmePath      string
	ReadmeRuPath    string
	WiXDir          string // Path to wix directory (contains light.exe, candle.exe)
	OutputPath      string
	Version         string

	// Internal state
	wixObjPath string // set after candle compilation
}

// Build creates the MSI installer.
func (b *Builder) Build() error {
	// Validate inputs
	if err := b.validate(); err != nil {
		return fmt.Errorf("msi builder validation: %w", err)
	}

	// Check WiX tools
	if err := b.checkWixTools(); err != nil {
		return fmt.Errorf("wiX tools check: %w", err)
	}

	// Step 1: Compile the .wxs file with candle
	if err := b.compileWXS(); err != nil {
		return fmt.Errorf("candle compilation: %w", err)
	}

	// Step 2: Link the .wixobj to create MSI
	if err := b.linkMSI(); err != nil {
		return fmt.Errorf("light linking: %w", err)
	}

	// Step 3: Clean up intermediate files
	b.cleanup()

	fmt.Printf("[+] MSI installer created: %s\n", b.OutputPath)
	return nil
}

// validate checks that all required paths exist.
func (b *Builder) validate() error {
	if b.BinaryPath == "" {
		return fmt.Errorf("binary path is required")
	}
	if _, err := os.Stat(b.BinaryPath); os.IsNotExist(err) {
		return fmt.Errorf("binary not found: %s", b.BinaryPath)
	}

	if b.InstallScript == "" {
		return fmt.Errorf("install script path is required")
	}
	if _, err := os.Stat(b.InstallScript); os.IsNotExist(err) {
		return fmt.Errorf("install script not found: %s", b.InstallScript)
	}

	if b.UninstallScript == "" {
		return fmt.Errorf("uninstall script path is required")
	}
	if _, err := os.Stat(b.UninstallScript); os.IsNotExist(err) {
		return fmt.Errorf("uninstall script not found: %s", b.UninstallScript)
	}

	if b.ExampleConfig == "" {
		return fmt.Errorf("example config path is required")
	}
	if _, err := os.Stat(b.ExampleConfig); os.IsNotExist(err) {
		return fmt.Errorf("example config not found: %s", b.ExampleConfig)
	}

	if b.ReadmePath == "" {
		return fmt.Errorf("readme path is required")
	}
	if _, err := os.Stat(b.ReadmePath); os.IsNotExist(err) {
		return fmt.Errorf("readme not found: %s", b.ReadmePath)
	}

	if b.Version == "" {
		return fmt.Errorf("version is required")
	}

	if b.OutputPath == "" {
		return fmt.Errorf("output path is required")
	}

	// Normalize version for WiX (must be X.Y.Z format)
	wixVer := normalizeVersion(b.Version)
	if wixVer == "" {
		return fmt.Errorf("invalid version format: %s", b.Version)
	}
	b.Version = wixVer

	return nil
}

// checkWixTools verifies that WiX Toolset is available.
func (b *Builder) checkWixTools() error {
	// Try to find WiX tools
	if b.WiXDir != "" {
		// Check custom WiX directory
		for _, tool := range []string{"candle.exe", "light.exe"} {
			toolPath := filepath.Join(b.WiXDir, tool)
			if _, err := os.Stat(toolPath); os.IsNotExist(err) {
				return fmt.Errorf("WiX tool not found: %s (looked in: %s)", tool, b.WiXDir)
			}
		}
		return nil
	}

	// Try to find in PATH
	for _, tool := range []string{"candle.exe", "light.exe"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("WiX tool '%s' not found in PATH. Install WiX Toolset v3.x from https://wixtoolset.org/", tool)
		}
	}
	return nil
}

// compileWXS runs candle to compile the .wxs file.
func (b *Builder) compileWXS() error {
	fmt.Println("[+] Compiling MSI definition with candle...")

	// Get the directory containing the .wxs file
	wxsPath := filepath.Join("cmd", "goal", "msi", "wxs", "goal.wxs")

	// Create temp output directory
	tmpDir := os.TempDir()
	objPath := filepath.Join(tmpDir, "goal.wixobj")
	b.wixObjPath = objPath

	// Build candle arguments
	args := []string{
		"-out", objPath,
		"-arch", "x64",
		fmt.Sprintf("-DVersion=%s", b.Version),
		fmt.Sprintf("-DBinaryPath=%s", b.BinaryPath),
		fmt.Sprintf("-DInstallScriptPath=%s", b.InstallScript),
		fmt.Sprintf("-DUninstallScriptPath=%s", b.UninstallScript),
		fmt.Sprintf("-DExampleConfigPath=%s", b.ExampleConfig),
		fmt.Sprintf("-DReadmePath=%s", b.ReadmePath),
		fmt.Sprintf("-DReadmeRuPath=%s", b.ReadmeRuPath),
		wxsPath,
	}

	cmd := exec.Command("candle", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("    candle %s\n", strings.Join(args[1:], " "))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("candle failed: %w", err)
	}

	// Verify wixobj was created
	if _, err := os.Stat(b.wixObjPath); os.IsNotExist(err) {
		return fmt.Errorf("candle did not produce output: %s", b.wixObjPath)
	}

	return nil
}

// linkMSI runs light to create the MSI file.
func (b *Builder) linkMSI() error {
	fmt.Println("[+] Linking MSI with light...")

	// Ensure output directory exists
	outDir := filepath.Dir(b.OutputPath)
	if outDir != "" {
		if err := os.MkdirAll(outDir, 0755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	}

	// Build light arguments
	args := []string{
		"-out", b.OutputPath,
		"-sw1076", // suppress warning about no icon
		"-sw1077", // suppress warning about unrecognized publisher
		b.wixObjPath,
	}

	// Add WiXUI extension if WiXDir is set
	if b.WiXDir != "" {
		args = append([]string{
			"-loc", filepath.Join(b.WiXDir, "WixUI.en-us.wxl"),
			"-ext", filepath.Join(b.WiXDir, "WixUIExtension.dll"),
		}, args...)
	}

	cmd := exec.Command("light", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("    light %s\n", strings.Join(args[1:], " "))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("light failed: %w", err)
	}

	// Verify MSI was created
	if _, err := os.Stat(b.OutputPath); os.IsNotExist(err) {
		return fmt.Errorf("light did not produce output: %s", b.OutputPath)
	}

	return nil
}

// cleanup removes intermediate files.
func (b *Builder) cleanup() {
	if b.wixObjPath != "" {
		os.Remove(b.wixObjPath)
	}
}

// normalizeVersion converts version to WiX-compatible X.Y.Z format.
func normalizeVersion(v string) string {
	// Remove leading 'v' if present
	v = strings.TrimPrefix(v, "v")

	// Replace non-alphanumeric characters (except dot and dash) with underscore
	var cleaned strings.Builder
	for _, r := range v {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' {
			cleaned.WriteRune(r)
		} else {
			cleaned.WriteString("_")
		}
	}
	v = cleaned.String()

	// Take only first 3 components (major.minor.patch)
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 3 {
		// Pad with .0
		for len(parts) < 3 {
			parts = append(parts, "0")
		}
	}

	return parts[0] + "." + parts[1] + "." + parts[2]
}

// BuildMSI is the high-level function to build an installer.
// It tries MSI first, falls back to SFX if WiX is not available.
func BuildMSI(binaryPath, installScript, uninstallScript, exampleConfig, readme, readmeRu, output, version string) error {
	// Try WiX MSI first
	if getWixDirForBuilder() != "" || CheckWiXAvailability() == nil {
		return (&Builder{
			BinaryPath:      binaryPath,
			InstallScript:   installScript,
			UninstallScript: uninstallScript,
			ExampleConfig:   exampleConfig,
			ReadmePath:      readme,
			ReadmeRuPath:    readmeRu,
			OutputPath:      output,
			Version:         version,
			WiXDir:          getWixDirForBuilder(),
		}).Build()
	}

	// Fall back to SFX
	sfxOutput := output
	if len(output) > 4 && output[len(output)-4:] == ".msi" {
		sfxOutput = output[:len(output)-4] + ".zip"
	}
	return BuildSFX(binaryPath, installScript, uninstallScript, exampleConfig, readme, readmeRu, sfxOutput, version)
}

// getWixDir attempts to locate the WiX tools directory.
func getWixDir() string {
	if runtime.GOOS == "windows" {
		// Common WiX installation paths
		candidates := []string{
			`C:\Program Files (x86)\WiX Toolset v3.14`,
		}
		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	return ""
}

// wixDirOverride is a global override for WiX directory.
var wixDirOverride string

// SetWiXDir sets a global override for the WiX directory.
func SetWiXDir(path string) {
	wixDirOverride = path
}

// getWixDirForBuilder returns the effective WiX directory.
func getWixDirForBuilder() string {
	if wixDirOverride != "" {
		return wixDirOverride
	}
	return getWixDir()
}

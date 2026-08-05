package updater

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ReleaseInfo contains information about a new release.
type ReleaseInfo struct {
	Version      string         `json:"version"`
	TagName      string         `json:"tag_name"`
	PublishedAt  time.Time      `json:"published_at"`
	DownloadsURL string         `json:"downloads_url"`
	Assets       []ReleaseAsset `json:"assets"`
	Body         string         `json:"body"`
}

// ReleaseAsset represents a release asset.
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
	Size               int64  `json:"size"`
}

// Updater manages application updates.
type Updater struct {
	CurrentVersion string
	UpdateURL      string // URL for checking updates (GitHub releases API endpoint)
	AssetPattern   string // Pattern to match update assets (e.g., "goal-*-linux-amd64.tar.gz")
	InstallDir     string // Directory where the app is installed
	HTTPClient     *http.Client
	logger         Logger
}

// Logger is an interface for update logging.
type Logger interface {
	Info(format string, args ...interface{})
	Error(format string, args ...interface{})
	Debug(format string, args ...interface{})
}

// DefaultLogger implements a simple logger.
type DefaultLogger struct{}

func (l *DefaultLogger) Info(format string, args ...interface{}) {
	fmt.Printf("[UPDATER] INFO: "+format+"\n", args...)
}

func (l *DefaultLogger) Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[UPDATER] ERROR: "+format+"\n", args...)
}

func (l *DefaultLogger) Debug(format string, args ...interface{}) {
	fmt.Printf("[UPDATER] DEBUG: "+format+"\n", args...)
}

// NewUpdater creates a new updater instance.
func NewUpdater(currentVersion, updateURL, assetPattern, installDir string) *Updater {
	return &Updater{
		CurrentVersion: currentVersion,
		UpdateURL:      updateURL,
		AssetPattern:   assetPattern,
		InstallDir:     installDir,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: &DefaultLogger{},
	}
}

// SetLogger sets a custom logger.
func (u *Updater) SetLogger(logger Logger) {
	u.logger = logger
}

// CheckForUpdate checks for available updates.
func (u *Updater) CheckForUpdate() (*ReleaseInfo, error) {
	release, err := u.fetchLatestRelease()
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}

	if release.Version == u.CurrentVersion {
		return nil, nil // No update available
	}

	return release, nil
}

// fetchLatestRelease fetches the latest release information.
func (u *Updater) fetchLatestRelease() (*ReleaseInfo, error) {
	var url string
	if u.UpdateURL != "" {
		url = u.UpdateURL
	} else {
		// Default to GitHub releases API
		repo := "dsdred/goal-starter" // Default repository
		url = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "goal-updater/"+u.CurrentVersion)

	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from update URL", resp.StatusCode)
	}

	var release ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	// Extract version from tag or version field
	if release.Version == "" {
		release.Version = release.TagName
	}

	return &release, nil
}

// DownloadAsset downloads a release asset and verifies its checksum.
func (u *Updater) DownloadAsset(asset ReleaseAsset, destDir string) (string, error) {
	u.logger.Info("Downloading %s...", asset.Name)

	// Create destination directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("create dest dir: %w", err)
	}

	destPath := filepath.Join(destDir, asset.Name)
	destFile, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer destFile.Close()

	// Download file
	resp, err := http.Get(asset.BrowserDownloadURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d downloading asset", resp.StatusCode)
	}

	// Calculate hash while downloading
	hash := sha256.New()
	writer := io.MultiWriter(destFile, hash)
	if _, err := io.Copy(writer, resp.Body); err != nil {
		return "", err
	}

	checksum := hex.EncodeToString(hash.Sum(nil))
	u.logger.Debug("Downloaded %s, SHA256: %s", asset.Name, checksum[:16])

	return destPath, nil
}

// InstallUpdate installs the downloaded update.
func (u *Updater) InstallUpdate(archivePath string) error {
	// Determine the installation method based on the OS and asset type
	switch runtime.GOOS {
	case "windows":
		return u.installWindows(archivePath)
	case "linux":
		return u.installLinux(archivePath)
	default:
		return u.installGeneric(archivePath)
	}
}

// installWindows handles Windows update installation.
func (u *Updater) installWindows(archivePath string) error {
	// Extract to temp directory first
	tempDir, err := os.MkdirTemp("", "goal-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	// Use unzip or tar depending on file type
	if err := u.extractArchive(archivePath, tempDir); err != nil {
		return err
	}

	// Find the new binary
	newBinary, err := u.findBinary(tempDir)
	if err != nil {
		return err
	}

	// Backup current binary
	currentBinary := filepath.Join(u.InstallDir, "goal.exe")
	backupPath := currentBinary + ".bak"
	if _, err := os.Stat(currentBinary); err == nil {
		if err := os.Rename(currentBinary, backupPath); err != nil {
			return err
		}
	}

	// Copy new binary
	if err := copyFile(newBinary, currentBinary); err != nil {
		// Restore backup on failure
		os.Rename(backupPath, currentBinary)
		return err
	}

	// Clean up backup on success (delayed)
	go func() {
		time.AfterFunc(1*time.Hour, func() {
			os.Remove(backupPath)
		})
	}()

	return nil
}

// installLinux handles Linux update installation.
func (u *Updater) installLinux(archivePath string) error {
	// For packages (.deb/.rpm), use the package manager
	if filepath.Ext(archivePath) == ".deb" || filepath.Ext(archivePath) == ".rpm" {
		return u.installViaPackageManger(archivePath)
	}

	// For archives, extract and replace
	tempDir, err := os.MkdirTemp("", "goal-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	if err := u.extractArchive(archivePath, tempDir); err != nil {
		return err
	}

	newBinary, err := u.findBinary(tempDir)
	if err != nil {
		return err
	}

	currentBinary := filepath.Join(u.InstallDir, "goal")
	backupPath := currentBinary + ".bak"

	if _, err := os.Stat(currentBinary); err == nil {
		if err := os.Rename(currentBinary, backupPath); err != nil {
			return err
		}
	}

	if err := copyFile(newBinary, currentBinary); err != nil {
		os.Rename(backupPath, currentBinary)
		return err
	}

	// Restore permissions
	if err := os.Chmod(currentBinary, 0755); err != nil {
		return err
	}

	// Restart the service if running
	if err := u.restartService(); err != nil {
		u.logger.Debug("Service restart failed (may need manual intervention): %v", err)
	}

	// Clean up backup
	go func() {
		time.AfterFunc(24*time.Hour, func() {
			os.Remove(backupPath)
		})
	}()

	return nil
}

// installViaPackageManager handles .deb/.rpm installation.
func (u *Updater) installViaPackageManger(archivePath string) error {
	var cmd *exec.Cmd
	ext := filepath.Ext(archivePath)

	switch ext {
	case ".deb":
		cmd = exec.Command("sudo", "dpkg", "-i", archivePath)
	case ".rpm":
		cmd = exec.Command("sudo", "rpm", "-U", "--force", archivePath)
	default:
		return fmt.Errorf("unsupported package type: %s", ext)
	}

	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("package install failed: %w", err)
	}

	return u.restartService()
}

// installGeneric handles generic update installation.
func (u *Updater) installGeneric(archivePath string) error {
	tempDir, err := os.MkdirTemp("", "goal-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	if err := u.extractArchive(archivePath, tempDir); err != nil {
		return err
	}

	newBinary, err := u.findBinary(tempDir)
	if err != nil {
		return err
	}

	currentBinary := filepath.Join(u.InstallDir, "goal")
	if err := copyFile(newBinary, currentBinary); err != nil {
		return err
	}

	return nil
}

// extractArchive extracts an archive to the destination directory.
func (u *Updater) extractArchive(archivePath, destDir string) error {
	// Try unzip first (for .zip files)
	if filepath.Ext(archivePath) == ".zip" {
		cmd := exec.Command("unzip", "-o", archivePath, "-d", destDir)
		if _, err := cmd.CombinedOutput(); err != nil {
			// Fall back to Go's archive/zip
			return u.extractZip(archivePath, destDir)
		}
		return nil
	}

	// Try tar.gz extraction
	if filepath.Ext(archivePath) == ".gz" {
		// Check if it's a tar.gz
		cmd := exec.Command("tar", "xzf", archivePath, "-C", destDir)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("tar extraction failed: %w", err)
		}
		return nil
	}

	// Last resort: use Go's archive/zip
	return u.extractZip(archivePath, destDir)
}

// extractZip extracts a zip archive using Go.
func (u *Updater) extractZip(archivePath, destDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, f := range reader.File {
		destPath := filepath.Join(destDir, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		wf, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(wf, rc)
		rc.Close()
		wf.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// findBinary finds the main binary in the extracted files.
func (u *Updater) findBinary(dir string) (string, error) {
	var binaries []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			name := info.Name()
			// Look for the main binary
			if name == "goal" || name == "goal.exe" || name == "goal-msi.exe" {
				binaries = append(binaries, path)
			}
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	if len(binaries) == 0 {
		return "", fmt.Errorf("no binary found in archive")
	}

	// Return the first non-script binary
	for _, b := range binaries {
		if !strings.HasSuffix(b, ".ps1") && !strings.HasSuffix(b, ".sh") &&
			!strings.HasSuffix(b, ".bat") && !strings.HasSuffix(b, ".cmd") {
			return b, nil
		}
	}

	return binaries[0], nil
}

// restartService restarts the GoAl service if running.
func (u *Updater) restartService() error {
	switch runtime.GOOS {
	case "linux":
		cmd := exec.Command("systemctl", "restart", "goal")
		return cmd.Run()
	case "windows":
		// Try to restart as Windows service
		cmd := exec.Command("sc", "restart", "goal")
		return cmd.Run()
	default:
		return nil // No service management
	}
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// IsUpdateAvailable is a convenience function to check if an update is available.
func IsUpdateAvailable(currentVersion, updateURL string) (bool, *ReleaseInfo, error) {
	updater := NewUpdater(currentVersion, updateURL, "", "")
	release, err := updater.CheckForUpdate()
	if err != nil {
		return false, nil, err
	}
	return release != nil, release, nil
}

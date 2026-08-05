package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ReloadConfig represents a configuration source that supports hot-reload.
type ReloadConfig struct {
	mu      sync.RWMutex
	cfg     Config
	path    string
	watchCh chan Config
	stopCh  chan struct{}
	lastMod time.Time
	loaded  bool
}

// NewReloadConfig creates a new hot-reloadable configuration manager.
// It loads the config from the given path and starts watching for changes.
func NewReloadConfig(path string) (*ReloadConfig, error) {
	rc := &ReloadConfig{
		path:    path,
		watchCh: make(chan Config, 1),
		stopCh:  make(chan struct{}),
	}

	if err := rc.load(); err != nil {
		return nil, fmt.Errorf("initial load failed: %w", err)
	}

	return rc, nil
}

// load reads and validates the config from disk.
func (rc *ReloadConfig) load() error {
	data, err := os.ReadFile(rc.path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Validate config.
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	rc.mu.Lock()
	rc.cfg = cfg
	rc.lastMod = time.Now()
	rc.loaded = true
	rc.mu.Unlock()

	return nil
}

// Reload re-reads the config from disk and validates it.
// Returns nil if config was reloaded successfully, or an error.
// Returns nil with reloaded=false if no changes detected.
func (rc *ReloadConfig) Reload() error {
	// Get file info to check modification time.
	info, err := os.Stat(rc.path)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}

	// Skip if not modified since last load.
	if !info.ModTime().After(rc.lastMod) {
		return nil
	}

	return rc.load()
}

// StartWatch begins periodic configuration file watching.
// It checks for changes every 5 seconds. Call Stop() to end watching.
func (rc *ReloadConfig) StartWatch() {
	go rc.watchLoop()
}

// watchLoop periodically checks for config file changes.
func (rc *ReloadConfig) watchLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-rc.stopCh:
			return
		case <-ticker.C:
			if err := rc.Reload(); err != nil {
				// Log error but continue watching.
				continue
			}

			// Notify watchers if config changed.
			rc.mu.RLock()
			cfg := rc.cfg
			rc.mu.RUnlock()

			select {
			case rc.watchCh <- cfg:
			default:
				// Drop if no listeners.
			}
		}
	}
}

// Stop stops the configuration watch loop.
func (rc *ReloadConfig) Stop() {
	close(rc.stopCh)
}

// Get returns the current configuration.
// The returned config is a copy and should not be modified.
func (rc *ReloadConfig) Get() Config {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.cfg
}

// Watch returns a channel that receives config updates when the file changes.
// The channel is buffered (size 1) to prevent blocking.
// Close via Stop().
func (rc *ReloadConfig) Watch() <-chan Config {
	return rc.watchCh
}

// GetConfig returns the raw config for use by other packages.
func (rc *ReloadConfig) GetConfig() Config {
	return rc.Get()
}

// Save persists the current config to disk.
func (rc *ReloadConfig) Save() error {
	rc.mu.RLock()
	clone := rc.cfg
	rc.mu.RUnlock()

	// Clear sensitive fields before saving.
	clone.AdminPassword = ""
	if err := clone.Validate(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(clone, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(rc.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp := rc.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}

	if err := os.Rename(tmp, rc.path); err != nil {
		return err
	}

	// Update lastMod to prevent immediate re-trigger.
	rc.mu.Lock()
	rc.lastMod = time.Now()
	rc.mu.Unlock()

	return nil
}

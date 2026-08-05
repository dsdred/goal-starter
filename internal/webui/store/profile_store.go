package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dsdred/goal/internal/webui/validation"
)

// Profile represents a launch profile with all necessary configuration.
type Profile struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	RuntimeID   string            `json:"runtime_id"`
	ModelID     string            `json:"model_id"`
	Host        string            `json:"host"`
	Port        int               `json:"port"`
	Active      bool              `json:"active"`
	Args        []string          `json:"args,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// RuntimeEntry represents a runtime configuration.
type RuntimeEntry struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Executable       string            `json:"executable"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	DefaultArgs      []string          `json:"default_args,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// ModelEntry represents a model configuration.
type ModelEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	MMProj string `json:"mmproj,omitempty"`
	Format string `json:"format,omitempty"`
}

// Store manages persistent storage for profiles, runtimes, and models.
type Store struct {
	mu       sync.RWMutex
	dataDir  string
	profiles map[string]*Profile
	runtimes map[string]*RuntimeEntry
}

// NewStore creates a new Store backed by JSON files in dataDir.
func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	s := &Store{
		dataDir:  dataDir,
		profiles: make(map[string]*Profile),
		runtimes: make(map[string]*RuntimeEntry),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	profilesPath := filepath.Join(s.dataDir, "profiles.json")
	runtimesPath := filepath.Join(s.dataDir, "runtimes.json")

	if _, err := os.Stat(profilesPath); err == nil {
		data, err := os.ReadFile(profilesPath)
		if err != nil {
			return fmt.Errorf("read profiles: %w", err)
		}
		var profiles []*Profile
		if err := json.Unmarshal(data, &profiles); err != nil {
			return fmt.Errorf("decode profiles: %w", err)
		}
		for _, p := range profiles {
			s.profiles[p.ID] = p
		}
	}

	if _, err := os.Stat(runtimesPath); err == nil {
		data, err := os.ReadFile(runtimesPath)
		if err != nil {
			return fmt.Errorf("read runtimes: %w", err)
		}
		var runtimes []*RuntimeEntry
		if err := json.Unmarshal(data, &runtimes); err != nil {
			return fmt.Errorf("decode runtimes: %w", err)
		}
		for _, r := range runtimes {
			s.runtimes[r.ID] = r
		}
	}

	return nil
}

// save writes profiles and runtimes to disk.
// Must be called while s.mu.Lock() is already held (by CreateProfile, UpdateProfile,
// ActivateProfile, DeactivateProfile, DeleteProfile, CreateRuntime, UpdateRuntime, DeleteRuntime).
func (s *Store) save() error {
	profiles := make([]*Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		profiles = append(profiles, p)
	}
	runtimes := make([]*RuntimeEntry, 0, len(s.runtimes))
	for _, r := range s.runtimes {
		runtimes = append(runtimes, r)
	}

	profilesData, _ := json.MarshalIndent(profiles, "", "  ")
	runtimesData, _ := json.MarshalIndent(runtimes, "", "  ")

	if err := os.WriteFile(filepath.Join(s.dataDir, "profiles.json"), profilesData, 0o600); err != nil {
		return fmt.Errorf("write profiles: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.dataDir, "runtimes.json"), runtimesData, 0o600); err != nil {
		return fmt.Errorf("write runtimes: %w", err)
	}

	return nil
}

// --- Profile operations ---

func generateID() string {
	return fmt.Sprintf("id_%d", time.Now().UnixNano())
}

// ListProfiles returns all profiles.
func (s *Store) ListProfiles() ([]*Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		result = append(result, p)
	}
	return result, nil
}

// GetProfile returns a profile by ID.
func (s *Store) GetProfile(id string) (*Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.profiles[id]
	if !ok {
		return nil, errors.New("profile not found")
	}
	return p, nil
}

// CreateProfile creates a new profile with host and port validation.
func (s *Store) CreateProfile(name, runtimeID, modelID, host string, port int, args []string, environment map[string]string) (*Profile, error) {
	if err := validation.ValidateAddress(host, port); err != nil {
		return nil, fmt.Errorf("invalid address: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := generateID()
	now := time.Now()
	p := &Profile{
		ID:          id,
		Name:        name,
		RuntimeID:   runtimeID,
		ModelID:     modelID,
		Host:        host,
		Port:        port,
		Args:        args,
		Environment: environment,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.profiles[id] = p
	if err := s.save(); err != nil {
		delete(s.profiles, id)
		return nil, err
	}
	return p, nil
}

// UpdateProfile updates an existing profile with host and port validation.
func (s *Store) UpdateProfile(id, name, runtimeID, modelID, host string, port int, args []string, environment map[string]string) (*Profile, error) {
	if err := validation.ValidateAddress(host, port); err != nil {
		return nil, fmt.Errorf("invalid address: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.profiles[id]
	if !ok {
		return nil, errors.New("profile not found")
	}
	p.Name = name
	p.RuntimeID = runtimeID
	p.ModelID = modelID
	p.Host = host
	p.Port = port
	p.Args = args
	p.Environment = environment
	p.UpdatedAt = time.Now()
	if err := s.save(); err != nil {
		return nil, err
	}
	return p, nil
}

// DeleteProfile deletes a profile by ID.
func (s *Store) DeleteProfile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.profiles[id]; !ok {
		return errors.New("profile not found")
	}
	delete(s.profiles, id)
	return s.save()
}

// ActivateProfile activates a profile by setting Active to true.
func (s *Store) ActivateProfile(id string) (*Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.profiles[id]
	if !ok {
		return nil, errors.New("profile not found")
	}
	p.Active = true
	p.UpdatedAt = time.Now()
	if err := s.save(); err != nil {
		return nil, err
	}
	return p, nil
}

// DeactivateProfile deactivates a profile by setting Active to false.
func (s *Store) DeactivateProfile(id string) (*Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.profiles[id]
	if !ok {
		return nil, errors.New("profile not found")
	}
	p.Active = false
	p.UpdatedAt = time.Now()
	if err := s.save(); err != nil {
		return nil, err
	}
	return p, nil
}

// --- Runtime operations ---

// ListRuntimes returns all runtimes.
func (s *Store) ListRuntimes() ([]*RuntimeEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*RuntimeEntry, 0, len(s.runtimes))
	for _, r := range s.runtimes {
		result = append(result, r)
	}
	return result, nil
}

// GetRuntime returns a runtime by ID.
func (s *Store) GetRuntime(id string) (*RuntimeEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.runtimes[id]
	if !ok {
		return nil, errors.New("runtime not found")
	}
	return r, nil
}

// CreateRuntime creates a new runtime.
func (s *Store) CreateRuntime(name, executable, workingDir string, defaultArgs []string, environment map[string]string) (*RuntimeEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := generateID()
	now := time.Now()
	r := &RuntimeEntry{
		ID:               id,
		Name:             name,
		Executable:       executable,
		WorkingDirectory: workingDir,
		DefaultArgs:      defaultArgs,
		Environment:      environment,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	s.runtimes[id] = r
	if err := s.save(); err != nil {
		delete(s.runtimes, id)
		return nil, err
	}
	return r, nil
}

// UpdateRuntime updates an existing runtime.
func (s *Store) UpdateRuntime(id, name, executable, workingDir string, defaultArgs []string, environment map[string]string) (*RuntimeEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.runtimes[id]
	if !ok {
		return nil, errors.New("runtime not found")
	}
	r.Name = name
	r.Executable = executable
	r.WorkingDirectory = workingDir
	r.DefaultArgs = defaultArgs
	r.Environment = environment
	r.UpdatedAt = time.Now()
	if err := s.save(); err != nil {
		return nil, err
	}
	return r, nil
}

// DeleteRuntime deletes a runtime by ID.
func (s *Store) DeleteRuntime(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.runtimes[id]; !ok {
		return errors.New("runtime not found")
	}
	delete(s.runtimes, id)
	return s.save()
}

// --- Model operations ---

// ModelStore handles model storage with proper dataDir tracking.
type ModelStore struct {
	mu      sync.RWMutex
	dataDir string
	Models  []ModelEntry `json:"models"`
}

// NewModelStore creates a new ModelStore.
func NewModelStore(dataDir string) (*ModelStore, error) {
	ms := &ModelStore{dataDir: dataDir}
	pathsFile := filepath.Join(dataDir, "models.json")
	if _, err := os.Stat(pathsFile); err == nil {
		data, err := os.ReadFile(pathsFile)
		if err != nil {
			return nil, fmt.Errorf("read models: %w", err)
		}
		if err := json.Unmarshal(data, &ms.Models); err != nil {
			return nil, fmt.Errorf("decode models: %w", err)
		}
	}
	return ms, nil
}

// ListModels returns all models.
func (ms *ModelStore) ListModels() ([]ModelEntry, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	result := make([]ModelEntry, len(ms.Models))
	copy(result, ms.Models)
	return result, nil
}

// GetModel returns a model by ID.
func (ms *ModelStore) GetModel(id string) (*ModelEntry, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	for _, m := range ms.Models {
		if m.ID == id {
			return &m, nil
		}
	}
	return nil, errors.New("model not found")
}

// CreateModel creates a new model entry.
func (ms *ModelStore) CreateModel(name, path, mmproj, format string) (*ModelEntry, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	id := generateID()
	m := ModelEntry{
		ID:     id,
		Name:   name,
		Path:   path,
		MMProj: mmproj,
		Format: format,
	}
	ms.Models = append(ms.Models, m)
	if err := ms.save(); err != nil {
		ms.Models = ms.Models[:len(ms.Models)-1]
		return nil, err
	}
	return &m, nil
}

// UpdateModel updates an existing model.
func (ms *ModelStore) UpdateModel(id, name, path, mmproj, format string) (*ModelEntry, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	for i, m := range ms.Models {
		if m.ID == id {
			ms.Models[i].Name = name
			ms.Models[i].Path = path
			ms.Models[i].MMProj = mmproj
			ms.Models[i].Format = format
			if err := ms.save(); err != nil {
				return nil, err
			}
			return &ms.Models[i], nil
		}
	}
	return nil, errors.New("model not found")
}

func (ms *ModelStore) save() error {
	data, _ := json.MarshalIndent(ms.Models, "", "  ")
	return os.WriteFile(filepath.Join(ms.dataDir, "models.json"), data, 0o600)
}

// DeleteModel deletes a model by ID.
func (ms *ModelStore) DeleteModel(id string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	for i, m := range ms.Models {
		if m.ID == id {
			ms.Models = append(ms.Models[:i], ms.Models[i+1:]...)
			return ms.save()
		}
	}
	return errors.New("model not found")
}

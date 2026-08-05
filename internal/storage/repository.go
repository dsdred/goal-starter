package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RuntimeEntry represents a runtime definition.
type RuntimeEntry struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Executable       string            `json:"executable"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	DefaultArgs      []string          `json:"default_args"`
	Environment      map[string]string `json:"environment,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// ModelEntry represents a model definition.
type ModelEntry struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	MMProj    string    `json:"mmproj,omitempty"`
	Format    string    `json:"format,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProfileEntry represents a profile definition.
type ProfileEntry struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	RuntimeID   string            `json:"runtime_id"`
	ModelID     string            `json:"model_id,omitempty"`
	Host        string            `json:"host"`
	Port        int               `json:"port"`
	Args        []string          `json:"args,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Active      bool              `json:"active"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// LaunchInstanceEntry represents a persisted launch instance.
type LaunchInstanceEntry struct {
	ID               string            `json:"id"`
	ProfileID        string            `json:"profile_id"`
	RuntimeID        string            `json:"runtime_id"`
	ModelID          string            `json:"model_id,omitempty"`
	Executable       string            `json:"executable,omitempty"`
	Args             []string          `json:"args,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	State            string            `json:"state"`
	PID              int               `json:"pid,omitempty"`
	ExitCode         int               `json:"exit_code,omitempty"`
	ExitClass        string            `json:"exit_class,omitempty"`
	LastError        string            `json:"last_error,omitempty"`
	StartedAt        time.Time         `json:"started_at,omitempty"`
	StoppedAt        time.Time         `json:"stopped_at,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// InstanceStore is a minimal interface for instance operations needed by supervisor.
type InstanceStore interface {
	Create(e *LaunchInstanceEntry) error
	Get(id string) (*LaunchInstanceEntry, error)
	Update(e *LaunchInstanceEntry) error
	Delete(id string) error
	List() ([]*LaunchInstanceEntry, error)
	ListByProfileID(profileID string) ([]*LaunchInstanceEntry, error)
}

// Repository is the unified data store for all entities.
type Repository interface {
	// InstanceStore methods (delegated)
	CreateInstance(e *LaunchInstanceEntry) error
	GetInstance(id string) (*LaunchInstanceEntry, error)
	UpdateInstance(e *LaunchInstanceEntry) error
	DeleteInstance(id string) error
	ListInstances() ([]*LaunchInstanceEntry, error)
	// InstanceStore compatibility methods
	Create(e *LaunchInstanceEntry) error
	Get(id string) (*LaunchInstanceEntry, error)
	Update(e *LaunchInstanceEntry) error
	Delete(id string) error
	List() ([]*LaunchInstanceEntry, error)

	// Runtime operations
	CreateRuntime(e *RuntimeEntry) error
	GetRuntime(id string) (*RuntimeEntry, error)
	UpdateRuntime(e *RuntimeEntry) error
	DeleteRuntime(id string) error
	ListRuntimes() ([]*RuntimeEntry, error)

	// Model operations
	CreateModel(e *ModelEntry) error
	GetModel(id string) (*ModelEntry, error)
	UpdateModel(e *ModelEntry) error
	DeleteModel(id string) error
	ListModels() ([]*ModelEntry, error)

	// Profile operations
	CreateProfile(e *ProfileEntry) error
	GetProfile(id string) (*ProfileEntry, error)
	UpdateProfile(e *ProfileEntry) error
	DeleteProfile(id string) error
	ListProfiles() ([]*ProfileEntry, error)

	// Launch instance operations
	CreateLaunchInstance(e *LaunchInstanceEntry) error
	GetLaunchInstance(id string) (*LaunchInstanceEntry, error)
	UpdateLaunchInstance(e *LaunchInstanceEntry) error
	DeleteLaunchInstance(id string) error
	ListLaunchInstances() ([]*LaunchInstanceEntry, error)
	ListByProfileID(profileID string) ([]*LaunchInstanceEntry, error)

	// Schema management
	SchemaVersion() int
	Upgrade() error
	SaveUnified(path string) error

	// Utility
	ValidateCrossReferences(ctx context.Context) error
	CountActiveInstances() int
}

// JSONRepository implements Repository using a single atomic JSON file.
type JSONRepository struct {
	mu        sync.RWMutex
	filePath  string
	runtimes  []*RuntimeEntry
	models    []*ModelEntry
	profiles  []*ProfileEntry
	instances []*LaunchInstanceEntry
}

// NewJSONRepository creates a new JSONRepository.
func NewJSONRepository(filePath string) (Repository, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	r := &JSONRepository{
		filePath: filePath,
	}

	if err := r.load(); err != nil {
		if os.IsNotExist(err) {
			return r, r.save()
		}
		return nil, fmt.Errorf("load repository: %w", err)
	}

	return r, nil
}

// load reads the JSON file into memory.
func (r *JSONRepository) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.filePath)
	if err != nil {
		return err
	}

	var unified struct {
		SchemaVersion int                    `json:"schema_version"`
		Runtimes      []*RuntimeEntry        `json:"runtimes"`
		Models        []*ModelEntry          `json:"models"`
		Profiles      []*ProfileEntry        `json:"profiles"`
		Instances     []*LaunchInstanceEntry `json:"instances"`
	}

	if err := json.Unmarshal(data, &unified); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}

	r.runtimes = unified.Runtimes
	r.models = unified.Models
	r.profiles = unified.Profiles
	r.instances = unified.Instances

	return nil
}

// save writes the data to disk atomically.
func (r *JSONRepository) save() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	unified := map[string]interface{}{
		"schema_version": 4,
		"runtimes":       r.runtimes,
		"models":         r.models,
		"profiles":       r.profiles,
		"instances":      r.instances,
	}

	data, err := json.MarshalIndent(unified, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}

	tmp := r.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmp, r.filePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

// SchemaVersion returns the current schema version.
func (r *JSONRepository) SchemaVersion() int {
	return 4
}

// Upgrade migrates from older schema versions.
func (r *JSONRepository) Upgrade() error {
	return r.save()
}

// SaveUnified writes the data to the specified path atomically.
func (r *JSONRepository) SaveUnified(path string) error {
	data, err := json.MarshalIndent(map[string]interface{}{
		"schema_version": 4,
		"runtimes":       r.runtimes,
		"models":         r.models,
		"profiles":       r.profiles,
		"instances":      r.instances,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

// ---------- Runtime operations ----------

func (r *JSONRepository) CreateRuntime(e *RuntimeEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	e.ID = generateID()
	e.CreatedAt = time.Now()
	e.UpdatedAt = time.Now()

	r.runtimes = append(r.runtimes, e)
	return r.save()
}

func (r *JSONRepository) GetRuntime(id string) (*RuntimeEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, e := range r.runtimes {
		if e.ID == id {
			ec := *e
			ec.Environment = make(map[string]string)
			for k, v := range e.Environment {
				ec.Environment[k] = v
			}
			ec.DefaultArgs = make([]string, len(e.DefaultArgs))
			copy(ec.DefaultArgs, e.DefaultArgs)
			return &ec, nil
		}
	}
	return nil, fmt.Errorf("runtime not found: %s", id)
}

func (r *JSONRepository) UpdateRuntime(e *RuntimeEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.runtimes {
		if existing.ID == e.ID {
			r.runtimes[i] = e
			e.UpdatedAt = time.Now()
			return r.save()
		}
	}
	return fmt.Errorf("runtime not found: %s", e.ID)
}

func (r *JSONRepository) DeleteRuntime(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, e := range r.runtimes {
		if e.ID == id {
			r.runtimes = append(r.runtimes[:i], r.runtimes[i+1:]...)
			return r.save()
		}
	}
	return fmt.Errorf("runtime not found: %s", id)
}

func (r *JSONRepository) ListRuntimes() ([]*RuntimeEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*RuntimeEntry, 0, len(r.runtimes))
	for _, e := range r.runtimes {
		ec := *e
		ec.Environment = make(map[string]string)
		for k, v := range e.Environment {
			ec.Environment[k] = v
		}
		ec.DefaultArgs = make([]string, len(e.DefaultArgs))
		copy(ec.DefaultArgs, e.DefaultArgs)
		result = append(result, &ec)
	}
	return result, nil
}

// ---------- Model operations ----------

func (r *JSONRepository) CreateModel(e *ModelEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	e.ID = generateID()
	e.CreatedAt = time.Now()
	e.UpdatedAt = time.Now()

	r.models = append(r.models, e)
	return r.save()
}

func (r *JSONRepository) GetModel(id string) (*ModelEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, e := range r.models {
		if e.ID == id {
			ec := *e
			return &ec, nil
		}
	}
	return nil, fmt.Errorf("model not found: %s", id)
}

func (r *JSONRepository) UpdateModel(e *ModelEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.models {
		if existing.ID == e.ID {
			r.models[i] = e
			e.UpdatedAt = time.Now()
			return r.save()
		}
	}
	return fmt.Errorf("model not found: %s", e.ID)
}

func (r *JSONRepository) DeleteModel(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, e := range r.models {
		if e.ID == id {
			r.models = append(r.models[:i], r.models[i+1:]...)
			return r.save()
		}
	}
	return fmt.Errorf("model not found: %s", id)
}

func (r *JSONRepository) ListModels() ([]*ModelEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ModelEntry, 0, len(r.models))
	for _, e := range r.models {
		ec := *e
		result = append(result, &ec)
	}
	return result, nil
}

// ---------- Profile operations ----------

func (r *JSONRepository) CreateProfile(e *ProfileEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	e.ID = generateID()
	e.CreatedAt = time.Now()
	e.UpdatedAt = time.Now()

	r.profiles = append(r.profiles, e)
	return r.save()
}

func (r *JSONRepository) GetProfile(id string) (*ProfileEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, e := range r.profiles {
		if e.ID == id {
			ec := *e
			ec.Environment = make(map[string]string)
			for k, v := range e.Environment {
				ec.Environment[k] = v
			}
			ec.Args = make([]string, len(e.Args))
			copy(ec.Args, e.Args)
			return &ec, nil
		}
	}
	return nil, fmt.Errorf("profile not found: %s", id)
}

func (r *JSONRepository) UpdateProfile(e *ProfileEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.profiles {
		if existing.ID == e.ID {
			r.profiles[i] = e
			e.UpdatedAt = time.Now()
			return r.save()
		}
	}
	return fmt.Errorf("profile not found: %s", e.ID)
}

func (r *JSONRepository) DeleteProfile(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, e := range r.profiles {
		if e.ID == id {
			r.profiles = append(r.profiles[:i], r.profiles[i+1:]...)
			return r.save()
		}
	}
	return fmt.Errorf("profile not found: %s", id)
}

func (r *JSONRepository) ListProfiles() ([]*ProfileEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ProfileEntry, 0, len(r.profiles))
	for _, e := range r.profiles {
		ec := *e
		ec.Environment = make(map[string]string)
		for k, v := range e.Environment {
			ec.Environment[k] = v
		}
		ec.Args = make([]string, len(e.Args))
		copy(ec.Args, e.Args)
		result = append(result, &ec)
	}
	return result, nil
}

// ---------- Launch Instance operations ----------

func (r *JSONRepository) CreateLaunchInstance(e *LaunchInstanceEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	e.ID = generateID()
	e.CreatedAt = time.Now()
	e.UpdatedAt = time.Now()

	r.instances = append(r.instances, e)
	return r.save()
}

func (r *JSONRepository) GetLaunchInstance(id string) (*LaunchInstanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, e := range r.instances {
		if e.ID == id {
			ec := *e
			ec.Environment = make(map[string]string)
			for k, v := range e.Environment {
				ec.Environment[k] = v
			}
			ec.Args = make([]string, len(e.Args))
			copy(ec.Args, e.Args)
			return &ec, nil
		}
	}
	return nil, fmt.Errorf("launch instance not found: %s", id)
}

func (r *JSONRepository) UpdateLaunchInstance(e *LaunchInstanceEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.instances {
		if existing.ID == e.ID {
			r.instances[i] = e
			e.UpdatedAt = time.Now()
			return r.save()
		}
	}
	return fmt.Errorf("launch instance not found: %s", e.ID)
}

func (r *JSONRepository) DeleteLaunchInstance(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, e := range r.instances {
		if e.ID == id {
			r.instances = append(r.instances[:i], r.instances[i+1:]...)
			return r.save()
		}
	}
	return fmt.Errorf("launch instance not found: %s", id)
}

func (r *JSONRepository) ListLaunchInstances() ([]*LaunchInstanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*LaunchInstanceEntry, 0, len(r.instances))
	for _, e := range r.instances {
		ec := *e
		ec.Environment = make(map[string]string)
		for k, v := range e.Environment {
			ec.Environment[k] = v
		}
		ec.Args = make([]string, len(e.Args))
		copy(ec.Args, e.Args)
		result = append(result, &ec)
	}
	return result, nil
}

func (r *JSONRepository) ListByProfileID(profileID string) ([]*LaunchInstanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*LaunchInstanceEntry, 0)
	for _, e := range r.instances {
		if e.ProfileID == profileID {
			ec := *e
			ec.Environment = make(map[string]string)
			for k, v := range e.Environment {
				ec.Environment[k] = v
			}
			ec.Args = make([]string, len(e.Args))
			copy(ec.Args, e.Args)
			result = append(result, &ec)
		}
	}
	return result, nil
}

// generateID creates a unique ID.
func generateID() string {
	return fmt.Sprintf("inst_%d", time.Now().UnixNano())
}

// CreateInstance delegates to CreateLaunchInstance for InstanceStore compatibility.
func (r *JSONRepository) CreateInstance(e *LaunchInstanceEntry) error {
	return r.CreateLaunchInstance(e)
}

// GetInstance delegates to GetLaunchInstance for InstanceStore compatibility.
func (r *JSONRepository) GetInstance(id string) (*LaunchInstanceEntry, error) {
	return r.GetLaunchInstance(id)
}

// UpdateInstance delegates to UpdateLaunchInstance for InstanceStore compatibility.
func (r *JSONRepository) UpdateInstance(e *LaunchInstanceEntry) error {
	return r.UpdateLaunchInstance(e)
}

// DeleteInstance delegates to DeleteLaunchInstance for InstanceStore compatibility.
func (r *JSONRepository) DeleteInstance(id string) error {
	return r.DeleteLaunchInstance(id)
}

// ListInstances delegates to ListLaunchInstances for InstanceStore compatibility.
func (r *JSONRepository) ListInstances() ([]*LaunchInstanceEntry, error) {
	return r.ListLaunchInstances()
}

// ValidateCrossReferences checks that all references are valid.
func (r *JSONRepository) ValidateCrossReferences(ctx context.Context) error {
	runtimeIDs := make(map[string]bool)
	for _, rt := range r.runtimes {
		runtimeIDs[rt.ID] = true
	}

	modelIDs := make(map[string]bool)
	for _, m := range r.models {
		modelIDs[m.ID] = true
	}

	for _, p := range r.profiles {
		if !runtimeIDs[p.RuntimeID] {
			return fmt.Errorf("profile %s references non-existent runtime %s", p.ID, p.RuntimeID)
		}
		if p.ModelID != "" && !modelIDs[p.ModelID] {
			return fmt.Errorf("profile %s references non-existent model %s", p.ID, p.ModelID)
		}
	}

	return nil
}

// CountActiveInstances returns the number of active instances.
func (r *JSONRepository) CountActiveInstances() int {
	count := 0
	for _, e := range r.instances {
		if e.State == "running" || e.State == "starting" {
			count++
		}
	}
	return count
}

// InstanceStore adapter methods - convert between domain.LaunchInstance and LaunchInstanceEntry.

// Create adapts LaunchInstanceEntry to domain.LaunchInstance interface.
func (r *JSONRepository) Create(e *LaunchInstanceEntry) error {
	return r.CreateLaunchInstance(e)
}

// Get retrieves a launch instance by ID.
func (r *JSONRepository) Get(id string) (*LaunchInstanceEntry, error) {
	return r.GetLaunchInstance(id)
}

// Update updates a launch instance.
func (r *JSONRepository) Update(e *LaunchInstanceEntry) error {
	return r.UpdateLaunchInstance(e)
}

// Delete removes a launch instance by ID.
func (r *JSONRepository) Delete(id string) error {
	return r.DeleteLaunchInstance(id)
}

// List returns all launch instances.
func (r *JSONRepository) List() ([]*LaunchInstanceEntry, error) {
	return r.ListLaunchInstances()
}

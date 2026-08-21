package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

// InstanceStoreJSON persists launch instances to a JSON file.
type InstanceStoreJSON struct {
	mu        sync.RWMutex
	dir       string
	file      string
	instances map[domain.InstanceID]*domain.LaunchInstance
}

// InstanceStoreOptions configures the instance store.
type InstanceStoreOptions struct {
	Directory string
	Filename  string
}

// snapshot is the JSON serialization format for instance data.
type snapshot struct {
	Version   int                  `json:"version"`
	Revision  int64                `json:"revision"`
	Instances []domainInstanceJSON `json:"instances"`
}

// domainInstanceJSON is the JSON representation of a LaunchInstance.
type domainInstanceJSON struct {
	ID               string            `json:"id"`
	ModelID          string            `json:"model_id"`
	ModelName        string            `json:"model_name,omitempty"`
	RuntimeID        string            `json:"runtime_id"`
	PID              int               `json:"pid,omitempty"`
	State            string            `json:"state"`
	StartedAt        string            `json:"started_at,omitempty"`
	StoppedAt        string            `json:"stopped_at,omitempty"`
	ExitCode         *int              `json:"exit_code,omitempty"`
	ExitClass        string            `json:"exit_class,omitempty"`
	LastError        string            `json:"last_error,omitempty"`
	Executable       string            `json:"executable,omitempty"`
	Args             []string          `json:"args,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
}

// NewInstanceStoreJSON creates a new instance store backed by a JSON file.
func NewInstanceStoreJSON(opts InstanceStoreOptions) (*InstanceStoreJSON, error) {
	dir := opts.Directory
	if dir == "" {
		dir = "data"
	}
	filename := opts.Filename
	if filename == "" {
		filename = "instances.json"
	}

	storage := &InstanceStoreJSON{
		dir:       dir,
		file:      filename,
		instances: make(map[domain.InstanceID]*domain.LaunchInstance),
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create storage directory: %w", err)
	}

	if err := storage.load(); err != nil {
		return nil, err
	}

	return storage, nil
}

// Create persists a new instance.
func (s *InstanceStoreJSON) Create(inst *domain.LaunchInstance) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.instances[inst.ID] = inst
	return s.save()
}

// Get retrieves an instance by ID.
func (s *InstanceStoreJSON) Get(id domain.InstanceID) (*domain.LaunchInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inst, ok := s.instances[id]
	if !ok {
		return nil, fmt.Errorf("instance %s not found", id)
	}

	c := *inst
	return &c, nil
}

// Update persists changes to an existing instance.
func (s *InstanceStoreJSON) Update(inst *domain.LaunchInstance) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.instances[inst.ID]; !ok {
		return fmt.Errorf("instance %s not found", inst.ID)
	}

	s.instances[inst.ID] = inst
	return s.save()
}

// Delete removes an instance from storage.
func (s *InstanceStoreJSON) Delete(id domain.InstanceID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.instances[id]; !ok {
		return fmt.Errorf("instance %s not found", id)
	}

	delete(s.instances, id)
	return s.save()
}

// List returns all instances.
func (s *InstanceStoreJSON) List() ([]*domain.LaunchInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*domain.LaunchInstance, 0, len(s.instances))
	for _, inst := range s.instances {
		c := *inst
		result = append(result, &c)
	}
	return result, nil
}

// FindByModelID returns instances for a specific model.
func (s *InstanceStoreJSON) FindByModelID(modelID string) ([]*domain.LaunchInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*domain.LaunchInstance, 0)
	for _, inst := range s.instances {
		if inst.ModelID == modelID {
			c := *inst
			result = append(result, &c)
		}
	}
	return result, nil
}

// CountActive returns the number of active instances.
func (s *InstanceStoreJSON) CountActive() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, inst := range s.instances {
		if inst.IsActive() {
			count++
		}
	}
	return count
}

// CleanupTerminal removes instances that are in a terminal state for more than the given duration.
func (s *InstanceStoreJSON) CleanupTerminal(maxAge time.Duration) (cleaned int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, inst := range s.instances {
		if inst.IsTerminal() && !inst.StoppedAt.IsZero() && now.Sub(inst.StoppedAt) > maxAge {
			delete(s.instances, id)
			cleaned++
		}
	}
	if cleaned > 0 {
		_ = s.save()
	}
	return cleaned
}

// save writes the in-memory state to the JSON file atomically.
func (s *InstanceStoreJSON) save() error {
	var snap snapshot
	snap.Version = 1
	snap.Revision = time.Now().UnixNano()

	for _, inst := range s.instances {
		dij := domainInstanceJSON{
			ID:               string(inst.ID),
			ModelID:          inst.ModelID,
			ModelName:        inst.ModelName,
			RuntimeID:        inst.RuntimeID,
			PID:              inst.PID,
			State:            string(inst.State),
			ExitCode:         inst.ExitCode,
			ExitClass:        string(inst.ExitClass),
			LastError:        inst.LastError,
			Executable:       inst.Executable,
			Args:             inst.Args,
			WorkingDirectory: inst.WorkingDirectory,
			Environment:      inst.Environment,
			CreatedAt:        inst.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:        inst.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if !inst.StartedAt.IsZero() {
			dij.StartedAt = inst.StartedAt.UTC().Format(time.RFC3339)
		}
		if !inst.StoppedAt.IsZero() {
			dij.StoppedAt = inst.StoppedAt.UTC().Format(time.RFC3339)
		}
		snap.Instances = append(snap.Instances, dij)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	tmp := filepath.Join(s.dir, "instances.tmp.json")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmp, filepath.Join(s.dir, s.file)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temp file: %w", err)
	}

	return nil
}

// load reads the JSON file into memory.
func (s *InstanceStoreJSON) load() error {
	path := filepath.Join(s.dir, s.file)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("check file: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("unmarshal snapshot: %w", err)
	}

	for _, dij := range snap.Instances {
		inst := &domain.LaunchInstance{
			ID:               domain.InstanceID(dij.ID),
			ModelID:          dij.ModelID,
			ModelName:        dij.ModelName,
			RuntimeID:        dij.RuntimeID,
			PID:              dij.PID,
			State:            domain.InstanceState(dij.State),
			ExitCode:         dij.ExitCode,
			ExitClass:        domain.InstanceExitClass(dij.ExitClass),
			LastError:        dij.LastError,
			Executable:       dij.Executable,
			Args:             dij.Args,
			WorkingDirectory: dij.WorkingDirectory,
			Environment:      dij.Environment,
		}

		if dij.StartedAt != "" {
			t, err := time.Parse(time.RFC3339, dij.StartedAt)
			if err == nil {
				inst.StartedAt = t
			}
		}
		if dij.StoppedAt != "" {
			t, err := time.Parse(time.RFC3339, dij.StoppedAt)
			if err == nil {
				inst.StoppedAt = t
			}
		}
		if dij.CreatedAt != "" {
			t, err := time.Parse(time.RFC3339, dij.CreatedAt)
			if err == nil {
				inst.CreatedAt = t
			}
		}
		if dij.UpdatedAt != "" {
			t, err := time.Parse(time.RFC3339, dij.UpdatedAt)
			if err == nil {
				inst.UpdatedAt = t
			}
		}

		s.instances[inst.ID] = inst
	}

	return nil
}

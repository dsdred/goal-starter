package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/dsdred/goal/internal/domain"
)

type LaunchInstanceEntry = domain.LaunchInstanceEntry
type RuntimeEntry = domain.RuntimeEntry
type ModelEntry = domain.ModelEntry

// InstanceStore is a minimal interface for instance operations needed by supervisor.
type InstanceStore interface {
	Create(e *LaunchInstanceEntry) error
	Get(id string) (*LaunchInstanceEntry, error)
	Update(e *LaunchInstanceEntry) error
	Delete(id string) error
	List() ([]*LaunchInstanceEntry, error)
	ListByModelID(modelID string) ([]*LaunchInstanceEntry, error)
}

// Repository is the unified data store for all entities.
type Repository interface {
	CreateInstance(e *LaunchInstanceEntry) error
	GetInstance(id string) (*LaunchInstanceEntry, error)
	UpdateInstance(e *LaunchInstanceEntry) error
	DeleteInstance(id string) error
	ListInstances() ([]*LaunchInstanceEntry, error)
	Create(e *LaunchInstanceEntry) error
	Get(id string) (*LaunchInstanceEntry, error)
	Update(e *LaunchInstanceEntry) error
	Delete(id string) error
	List() ([]*LaunchInstanceEntry, error)

	CreateRuntime(e *RuntimeEntry) error
	GetRuntime(id string) (*RuntimeEntry, error)
	UpdateRuntime(e *RuntimeEntry) error
	DeleteRuntime(id string) error
	ListRuntimes() ([]*RuntimeEntry, error)

	CreateModel(e *ModelEntry) error
	GetModel(id string) (*ModelEntry, error)
	UpdateModel(e *ModelEntry) error
	DeleteModel(id string) error
	ListModels() ([]*ModelEntry, error)

	CreateLaunchInstance(e *LaunchInstanceEntry) error
	GetLaunchInstance(id string) (*LaunchInstanceEntry, error)
	UpdateLaunchInstance(e *LaunchInstanceEntry) error
	DeleteLaunchInstance(id string) error
	ListLaunchInstances() ([]*LaunchInstanceEntry, error)
	ListByModelID(modelID string) ([]*LaunchInstanceEntry, error)

	SchemaVersion() int
	Upgrade() error
	SaveUnified(path string) error

	ValidateCrossReferences(ctx context.Context) error
	CountActiveInstances() int
}

// JSONRepository implements Repository using a single atomic JSON file.
type JSONRepository struct {
	mu          sync.RWMutex
	filePath    string
	runtimes    []*RuntimeEntry
	models      []*ModelEntry
	instances   []*LaunchInstanceEntry
	idGenerator func() string
}

func NewJSONRepository(filePath string) (Repository, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	r := &JSONRepository{
		filePath:    filePath,
		idGenerator: generateID,
	}

	if err := r.load(); err != nil {
		if os.IsNotExist(err) {
			return r, r.save()
		}
		return nil, fmt.Errorf("load repository: %w", err)
	}

	return r, nil
}

// v5File represents the legacy v5 schema for migration.
type v5File struct {
	SchemaVersion int           `json:"schema_version"`
	Runtimes      []*v5Runtime  `json:"runtimes"`
	Models        []*v5Model    `json:"models"`
	Profiles      []*v5Profile  `json:"profiles"`
	Instances     []*v5Instance `json:"instances"`
}

type v5Runtime struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Executable       string            `json:"executable"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	DefaultArgs      []string          `json:"default_args"`
	Environment      map[string]string `json:"environment,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type v5Model struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	MMProj    string    `json:"mmproj,omitempty"`
	Format    string    `json:"format,omitempty"`
	Arguments []string  `json:"arguments,omitempty"`
	RuntimeID string    `json:"runtime_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type v5Profile struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	RuntimeID      string            `json:"runtime_id"`
	ModelID        string            `json:"model_id,omitempty"`
	Host           string            `json:"host"`
	Port           int               `json:"port"`
	Args           []string          `json:"args,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	Active         bool              `json:"active"`
	AutostartDelay int               `json:"autostart_delay,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type v5Instance struct {
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

func (r *JSONRepository) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return err
		}
		data, err = r.tryBackup()
		if err != nil {
			return fmt.Errorf("load repository (main and backup failed): %w", err)
		}
	}

	// Detect schema version.
	var versionCheck struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &versionCheck); err != nil {
		log.Printf("WARN: main file corrupted, trying backup: file=%s error=%v", r.filePath, err)
		data, err = r.tryBackup()
		if err != nil {
			return fmt.Errorf("load repository (main and backup failed): %w", err)
		}
		if err := json.Unmarshal(data, &versionCheck); err != nil {
			return fmt.Errorf("backup file also corrupted: %w", err)
		}
	}

	if versionCheck.SchemaVersion <= 5 {
		if err := r.migrateV5(data); err != nil {
			return fmt.Errorf("migrate v5 to v6: %w", err)
		}
	} else {
		var unified struct {
			SchemaVersion int                    `json:"schema_version"`
			Runtimes      []*RuntimeEntry        `json:"runtimes"`
			Models        []*ModelEntry          `json:"models"`
			Instances     []*LaunchInstanceEntry `json:"instances"`
		}
		if err := json.Unmarshal(data, &unified); err != nil {
			return fmt.Errorf("unmarshal v6: %w", err)
		}
		r.runtimes = unified.Runtimes
		r.models = unified.Models
		r.instances = unified.Instances
	}

	return nil
}

// migrateV5 converts a v5 repository file to v6 in memory.
func (r *JSONRepository) migrateV5(data []byte) error {
	var v5 v5File
	if err := json.Unmarshal(data, &v5); err != nil {
		return fmt.Errorf("unmarshal v5: %w", err)
	}

	// Migrate runtimes (unchanged).
	r.runtimes = make([]*RuntimeEntry, 0, len(v5.Runtimes))
	for _, rt := range v5.Runtimes {
		r.runtimes = append(r.runtimes, &RuntimeEntry{
			ID:               rt.ID,
			Name:             rt.Name,
			Executable:       rt.Executable,
			WorkingDirectory: rt.WorkingDirectory,
			DefaultArgs:      rt.DefaultArgs,
			Environment:      rt.Environment,
			CreatedAt:        rt.CreatedAt,
			UpdatedAt:        rt.UpdatedAt,
		})
	}

	// Build old model lookup.
	modelMap := make(map[string]*v5Model)
	for _, m := range v5.Models {
		modelMap[m.ID] = m
	}

	// Migrate profiles → new models.
	r.models = make([]*ModelEntry, 0, len(v5.Profiles))
	for _, p := range v5.Profiles {
		args := make([]string, 0, len(p.Args)+8)
		args = append(args, p.Args...)
		if p.ModelID != "" {
			if m, ok := modelMap[p.ModelID]; ok {
				if m.Path != "" {
					args = append(args, "-m", m.Path)
				}
				if m.MMProj != "" {
					args = append(args, "--mmproj", m.MMProj)
				}
				args = append(args, m.Arguments...)
			}
		}

		updatedAt := p.UpdatedAt
		if p.ModelID != "" {
			if m, ok := modelMap[p.ModelID]; ok && m.UpdatedAt.After(updatedAt) {
				updatedAt = m.UpdatedAt
			}
		}

		r.models = append(r.models, &ModelEntry{
			ID:             p.ID,
			Name:           p.Name,
			RuntimeID:      p.RuntimeID,
			Args:           args,
			Host:           p.Host,
			Port:           p.Port,
			Environment:    p.Environment,
			Active:         p.Active,
			AutostartDelay: p.AutostartDelay,
			CreatedAt:      p.CreatedAt,
			UpdatedAt:      updatedAt,
		})
	}

	// Migrate instances: profile_id → model_id.
	r.instances = make([]*LaunchInstanceEntry, 0, len(v5.Instances))
	for _, inst := range v5.Instances {
		r.instances = append(r.instances, &LaunchInstanceEntry{
			ID:               inst.ID,
			ModelID:          inst.ProfileID,
			RuntimeID:        inst.RuntimeID,
			Executable:       inst.Executable,
			Args:             inst.Args,
			WorkingDirectory: inst.WorkingDirectory,
			Environment:      inst.Environment,
			State:            inst.State,
			PID:              inst.PID,
			ExitCode:         inst.ExitCode,
			ExitClass:        inst.ExitClass,
			LastError:        inst.LastError,
			StartedAt:        inst.StartedAt,
			StoppedAt:        inst.StoppedAt,
			CreatedAt:        inst.CreatedAt,
			UpdatedAt:        inst.UpdatedAt,
		})
	}

	return nil
}

func (r *JSONRepository) tryBackup() ([]byte, error) {
	bakPath := r.filePath + ".bak"
	data, err := os.ReadFile(bakPath)
	if err != nil {
		return nil, fmt.Errorf("read backup: %w", err)
	}
	var check struct {
		SchemaVersion int
	}
	if err := json.Unmarshal(data, &check); err != nil {
		return nil, fmt.Errorf("validate backup: %w", err)
	}
	return data, nil
}

func (r *JSONRepository) saveLocked() error {
	unified := map[string]interface{}{
		"schema_version": 6,
		"runtimes":       r.runtimes,
		"models":         r.models,
		"instances":      r.instances,
	}

	data, err := json.MarshalIndent(unified, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}

	tmp := r.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	f, syncErr := os.OpenFile(tmp, os.O_WRONLY, 0)
	if syncErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("open temp file for sync: %w", syncErr)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp file: %w", err)
	}

	validateData, err := os.ReadFile(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("validate temp file: %w", err)
	}
	var validated map[string]interface{}
	if err := json.Unmarshal(validateData, &validated); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("validate temp file JSON: %w", err)
	}

	if _, err := os.Stat(r.filePath); err == nil {
		bakPath := r.filePath + ".bak"
		if err = copyFile(r.filePath, bakPath); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("create backup %s: %w", bakPath, err)
		}
	}

	if err := os.Rename(tmp, r.filePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename temp file: %w", err)
	}

	dir := filepath.Dir(r.filePath)
	_ = syncDir(dir)

	return nil
}

func (r *JSONRepository) save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked()
}

func (r *JSONRepository) SchemaVersion() int { return 6 }

func (r *JSONRepository) Upgrade() error { return r.save() }

func (r *JSONRepository) SaveUnified(path string) error {
	data, err := json.MarshalIndent(map[string]interface{}{
		"schema_version": 6,
		"runtimes":       r.runtimes,
		"models":         r.models,
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
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// ─── Runtime CRUD ───

func (r *JSONRepository) CreateRuntime(e *RuntimeEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.ID == "" {
		e.ID = r.idGenerator()
	}
	now := time.Now()
	e.CreatedAt = now
	e.UpdatedAt = now
	for _, x := range r.runtimes {
		if x.ID == e.ID {
			return fmt.Errorf("runtime already exists: %s", e.ID)
		}
	}
	cp := *e
	r.runtimes = append(r.runtimes, &cp)
	return r.saveLocked()
}

func (r *JSONRepository) GetRuntime(id string) (*RuntimeEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.runtimes {
		if e.ID == id {
			cp := *e
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("runtime not found: %s", id)
}

func (r *JSONRepository) UpdateRuntime(e *RuntimeEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.runtimes {
		if x.ID == e.ID {
			e.UpdatedAt = time.Now()
			cp := *e
			r.runtimes[i] = &cp
			return r.saveLocked()
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
			return r.saveLocked()
		}
	}
	return fmt.Errorf("runtime not found: %s", id)
}

func (r *JSONRepository) ListRuntimes() ([]*RuntimeEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RuntimeEntry, len(r.runtimes))
	for i, e := range r.runtimes {
		cp := *e
		out[i] = &cp
	}
	return out, nil
}

// ─── Model CRUD ───

func (r *JSONRepository) CreateModel(e *ModelEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.ID == "" {
		e.ID = r.idGenerator()
	}
	now := time.Now()
	e.CreatedAt = now
	e.UpdatedAt = now
	for _, x := range r.models {
		if x.ID == e.ID {
			return fmt.Errorf("model already exists: %s", e.ID)
		}
	}
	cp := *e
	r.models = append(r.models, &cp)
	return r.saveLocked()
}

func (r *JSONRepository) GetModel(id string) (*ModelEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.models {
		if e.ID == id {
			cp := *e
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("model not found: %s", id)
}

func (r *JSONRepository) UpdateModel(e *ModelEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.models {
		if x.ID == e.ID {
			e.UpdatedAt = time.Now()
			cp := *e
			r.models[i] = &cp
			return r.saveLocked()
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
			return r.saveLocked()
		}
	}
	return fmt.Errorf("model not found: %s", id)
}

func (r *JSONRepository) ListModels() ([]*ModelEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ModelEntry, len(r.models))
	for i, e := range r.models {
		cp := *e
		out[i] = &cp
	}
	return out, nil
}

// ─── Instance CRUD ───

func (r *JSONRepository) CreateInstance(e *LaunchInstanceEntry) error {
	return r.Create(e)
}

func (r *JSONRepository) GetInstance(id string) (*LaunchInstanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.instances {
		if e.ID == id {
			cp := *e
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("instance not found: %s", id)
}

func (r *JSONRepository) UpdateInstance(e *LaunchInstanceEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.instances {
		if x.ID == e.ID {
			cp := *e
			r.instances[i] = &cp
			return r.saveLocked()
		}
	}
	return fmt.Errorf("instance not found: %s", e.ID)
}

func (r *JSONRepository) DeleteInstance(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.instances {
		if e.ID == id {
			r.instances = append(r.instances[:i], r.instances[i+1:]...)
			return r.saveLocked()
		}
	}
	return fmt.Errorf("instance not found: %s", id)
}

func (r *JSONRepository) ListInstances() ([]*LaunchInstanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*LaunchInstanceEntry, len(r.instances))
	for i, e := range r.instances {
		cp := *e
		out[i] = &cp
	}
	return out, nil
}

// ─── InstanceStore compatibility ───

func (r *JSONRepository) Create(e *LaunchInstanceEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.ID == "" {
		e.ID = r.idGenerator()
	}
	for i, x := range r.instances {
		if x.ID == e.ID {
			cp := *e
			r.instances[i] = &cp
			return r.saveLocked()
		}
	}
	cp := *e
	r.instances = append(r.instances, &cp)
	return r.saveLocked()
}

func (r *JSONRepository) Get(id string) (*LaunchInstanceEntry, error) {
	return r.GetInstance(id)
}

func (r *JSONRepository) Update(e *LaunchInstanceEntry) error {
	return r.UpdateInstance(e)
}

func (r *JSONRepository) Delete(id string) error {
	return r.DeleteInstance(id)
}

func (r *JSONRepository) List() ([]*LaunchInstanceEntry, error) {
	return r.ListInstances()
}

func (r *JSONRepository) ListByModelID(modelID string) ([]*LaunchInstanceEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*LaunchInstanceEntry
	for _, e := range r.instances {
		if e.ModelID == modelID {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out, nil
}

// ─── Launch instance aliases ───

func (r *JSONRepository) CreateLaunchInstance(e *LaunchInstanceEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.ID == "" {
		e.ID = r.idGenerator()
	}
	now := time.Now()
	e.CreatedAt = now
	e.UpdatedAt = now
	for _, x := range r.instances {
		if x.ID == e.ID {
			return fmt.Errorf("instance already exists: %s", e.ID)
		}
	}
	cp := *e
	r.instances = append(r.instances, &cp)
	return r.saveLocked()
}

func (r *JSONRepository) GetLaunchInstance(id string) (*LaunchInstanceEntry, error) {
	return r.Get(id)
}

func (r *JSONRepository) UpdateLaunchInstance(e *LaunchInstanceEntry) error {
	return r.Update(e)
}

func (r *JSONRepository) DeleteLaunchInstance(id string) error {
	return r.Delete(id)
}

func (r *JSONRepository) ListLaunchInstances() ([]*LaunchInstanceEntry, error) {
	return r.List()
}

// ─── Utility ───

func (r *JSONRepository) ValidateCrossReferences(ctx context.Context) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rtSet := make(map[string]bool, len(r.runtimes))
	for _, rt := range r.runtimes {
		rtSet[rt.ID] = true
	}

	for _, m := range r.models {
		if !rtSet[m.RuntimeID] {
			return fmt.Errorf("model %s references missing runtime %s", m.ID, m.RuntimeID)
		}
	}

	return nil
}

func (r *JSONRepository) CountActiveInstances() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, e := range r.instances {
		switch e.State {
		case "running", "starting", "stopping", "pending":
			count++
		}
	}
	return count
}

// ─── ID generation ───

func generateID() string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("ent_%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("ent_%d", time.Now().UnixNano())
}

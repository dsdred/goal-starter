package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/fsutil"
)

type LaunchInstanceEntry = domain.LaunchInstanceEntry
type RuntimeEntry = domain.RuntimeEntry
type ModelEntry = domain.ModelEntry
type PipelineEntry = domain.PipelineEntry
type PipelineModel = domain.PipelineModel

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
	PatchRuntime(id string, p *RuntimePatch) error
	DeleteRuntime(id string) error
	ListRuntimes() ([]*RuntimeEntry, error)
	ReplaceRuntimeAndDelete(oldID, newID string) (int, error)
	CascadeDeleteRuntimeAndModels(id string) (int, error)
	DeleteTerminalInstances(mode string, ids []string, cutoff time.Time) (int, error)

	CreateModel(e *ModelEntry) error
	GetModel(id string) (*ModelEntry, error)
	UpdateModel(e *ModelEntry) error
	PatchModel(id string, p *ModelPatch) error
	DeleteModel(id string) error
	ListModels() ([]*ModelEntry, error)

	CreatePipeline(e *PipelineEntry) error
	GetPipeline(id string) (*PipelineEntry, error)
	UpdatePipeline(e *PipelineEntry) error
	DeletePipeline(id string) error
	ListPipelines() ([]*PipelineEntry, error)

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

	ImportGraph(runtimes []*RuntimeEntry, models []*ModelEntry, pipelines []*PipelineEntry) error
}

// ErrImportConflict is returned by ImportGraph when one or more imported
// entities collide with existing repository state.
type ErrImportConflict struct {
	Conflicts []ImportConflict
}

func (e *ErrImportConflict) Error() string {
	return fmt.Sprintf("import conflict: %d collision(s)", len(e.Conflicts))
}

// ImportConflict describes a single collision.
type ImportConflict struct {
	Type   string
	ID     string
	Reason string
	Name   string
}

// JSONRepository implements Repository using a single atomic JSON file.
type JSONRepository struct {
	mu             sync.RWMutex
	filePath       string
	runtimes       []*RuntimeEntry
	models         []*ModelEntry
	instances      []*LaunchInstanceEntry
	pipelines      []*PipelineEntry
	idGenerator    func() string
	writeFunc      func(path string, data []byte, perm os.FileMode) error
	importLockHook func()
}

func NewJSONRepository(filePath string) (Repository, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	r := &JSONRepository{
		filePath:    filePath,
		idGenerator: generateID,
		writeFunc:   fsutil.WriteFileDurable,
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
			return fmt.Errorf("migrate v5 to v7: %w", err)
		}
	} else if versionCheck.SchemaVersion == 6 {
		if err := r.migrateV6ToV7(data); err != nil {
			return fmt.Errorf("migrate v6 to v7: %w", err)
		}
	} else {
		// v7 and v8 share the same shape; v8 is purely additive (the
		// pipelines key is simply absent in v7 files and loads as an empty
		// list; ADR 010 D1.2).
		var unified struct {
			SchemaVersion int                    `json:"schema_version"`
			Runtimes      []*RuntimeEntry        `json:"runtimes"`
			Models        []*ModelEntry          `json:"models"`
			Instances     []*LaunchInstanceEntry `json:"instances"`
			Pipelines     []*PipelineEntry       `json:"pipelines"`
		}
		if err := json.Unmarshal(data, &unified); err != nil {
			return fmt.Errorf("unmarshal unified schema: %w", err)
		}
		r.runtimes = unified.Runtimes
		r.models = unified.Models
		r.instances = unified.Instances
		r.pipelines = unified.Pipelines
	}

	if r.runtimes == nil {
		r.runtimes = make([]*RuntimeEntry, 0)
	}
	if r.models == nil {
		r.models = make([]*ModelEntry, 0)
	}
	if r.instances == nil {
		r.instances = make([]*LaunchInstanceEntry, 0)
	}
	if r.pipelines == nil {
		r.pipelines = make([]*PipelineEntry, 0)
	}
	r.backfillPipelineEntryIDs()

	return nil
}

// backfillPipelineEntryIDs assigns deterministic ids to legacy pipeline
// entries (files predating ADR 013 D1, entries without an id). The id
// follows the entry's position in the original list and is stable once
// persisted: a reload of a saved file never reassigns ids, and reordering
// entries after persistence never changes their ids (ADR 013 D6 stability).
func (r *JSONRepository) backfillPipelineEntryIDs() {
	for _, p := range r.pipelines {
		for i := range p.Models {
			if p.Models[i].ID == "" {
				p.Models[i].ID = p.ID + "-e" + strconv.Itoa(i+1)
			}
		}
	}
}

// migrateV5 converts a v5 repository file to v7 in memory.
func (r *JSONRepository) migrateV5(data []byte) error {
	var v5 v5File
	if err := json.Unmarshal(data, &v5); err != nil {
		return fmt.Errorf("unmarshal v5: %w", err)
	}

	// Build runtime DefaultArgs lookup.
	runtimeArgsMap := make(map[string][]string)
	for _, rt := range v5.Runtimes {
		runtimeArgsMap[rt.ID] = rt.DefaultArgs
	}

	// Migrate runtimes (no DefaultArgs in v7).
	r.runtimes = make([]*RuntimeEntry, 0, len(v5.Runtimes))
	for _, rt := range v5.Runtimes {
		r.runtimes = append(r.runtimes, &RuntimeEntry{
			ID:               rt.ID,
			Name:             rt.Name,
			Executable:       rt.Executable,
			WorkingDirectory: rt.WorkingDirectory,
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

	// Migrate profiles → new models (folding DefaultArgs + Host/Port into args).
	r.models = make([]*ModelEntry, 0, len(v5.Profiles))
	for _, p := range v5.Profiles {
		args := make([]string, 0, len(runtimeArgsMap[p.RuntimeID])+len(p.Args)+8)
		args = append(args, runtimeArgsMap[p.RuntimeID]...)
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
		if p.Host != "" && !containsV5Flag(args, "--host", "-a") {
			args = append(args, "--host", p.Host)
		}
		if p.Port > 0 && !containsV5Flag(args, "--port") {
			args = append(args, "--port", fmt.Sprintf("%d", p.Port))
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

// v6Runtime is a temporary struct for reading v6 schema data.
type v6Runtime struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Executable       string            `json:"executable"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	DefaultArgs      []string          `json:"default_args"`
	Environment      map[string]string `json:"environment,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// v6Model is a temporary struct for reading v6 schema data.
type v6Model struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	RuntimeID      string            `json:"runtime_id"`
	Args           []string          `json:"args,omitempty"`
	Host           string            `json:"host"`
	Port           int               `json:"port"`
	Environment    map[string]string `json:"environment,omitempty"`
	Active         bool              `json:"active"`
	AutostartDelay int               `json:"autostart_delay,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// v6File represents the v6 schema for migration to v7.
type v6File struct {
	SchemaVersion int                    `json:"schema_version"`
	Runtimes      []*v6Runtime           `json:"runtimes"`
	Models        []*v6Model             `json:"models"`
	Instances     []*LaunchInstanceEntry `json:"instances"`
}

// migrateV6ToV7 converts v6 data (with DefaultArgs, Host, Port) to v7 format.
func (r *JSONRepository) migrateV6ToV7(data []byte) error {
	var v6 v6File
	if err := json.Unmarshal(data, &v6); err != nil {
		return fmt.Errorf("unmarshal v6: %w", err)
	}

	runtimeArgsMap := make(map[string][]string, len(v6.Runtimes))
	r.runtimes = make([]*RuntimeEntry, 0, len(v6.Runtimes))
	for _, rt := range v6.Runtimes {
		runtimeArgsMap[rt.ID] = rt.DefaultArgs
		r.runtimes = append(r.runtimes, &RuntimeEntry{
			ID:               rt.ID,
			Name:             rt.Name,
			Executable:       rt.Executable,
			WorkingDirectory: rt.WorkingDirectory,
			Environment:      rt.Environment,
			CreatedAt:        rt.CreatedAt,
			UpdatedAt:        rt.UpdatedAt,
		})
	}

	r.models = make([]*ModelEntry, 0, len(v6.Models))
	for _, m := range v6.Models {
		args := make([]string, 0, len(runtimeArgsMap[m.RuntimeID])+len(m.Args)+4)
		args = append(args, runtimeArgsMap[m.RuntimeID]...)
		args = append(args, m.Args...)
		if m.Host != "" && !containsV5Flag(args, "--host", "-a") {
			args = append(args, "--host", m.Host)
		}
		if m.Port > 0 && !containsV5Flag(args, "--port") {
			args = append(args, "--port", fmt.Sprintf("%d", m.Port))
		}
		r.models = append(r.models, &ModelEntry{
			ID:             m.ID,
			Name:           m.Name,
			RuntimeID:      m.RuntimeID,
			Args:           args,
			Environment:    m.Environment,
			Active:         m.Active,
			AutostartDelay: m.AutostartDelay,
			CreatedAt:      m.CreatedAt,
			UpdatedAt:      m.UpdatedAt,
		})
	}

	r.instances = v6.Instances
	return nil
}

func containsV5Flag(args []string, flags ...string) bool {
	for _, arg := range args {
		for _, f := range flags {
			if arg == f {
				return true
			}
		}
	}
	return false
}

func hasFlag(args []string, flags ...string) bool {
	return containsV5Flag(args, flags...)
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
	return r.writeUnified(r.filePath)
}

func (r *JSONRepository) writeUnified(path string) error {
	data, err := json.MarshalIndent(map[string]interface{}{
		"schema_version": 8,
		"runtimes":       r.runtimes,
		"models":         r.models,
		"instances":      r.instances,
		"pipelines":      r.pipelines,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	wf := r.writeFunc
	if wf == nil {
		wf = fsutil.WriteFileDurable
	}
	return wf(path, data, 0o600)
}

func (r *JSONRepository) save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked()
}

func (r *JSONRepository) SchemaVersion() int { return 8 }

func (r *JSONRepository) Upgrade() error { return r.save() }

func (r *JSONRepository) SaveUnified(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeUnified(path)
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
	previous := r.runtimes
	r.runtimes = append(r.runtimes, &cp)
	if err := r.saveLocked(); err != nil {
		r.runtimes = previous
		return err
	}
	return nil
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
			previous := r.runtimes[i]
			r.runtimes[i] = &cp
			if err := r.saveLocked(); err != nil {
				r.runtimes[i] = previous
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("runtime not found: %s", e.ID)
}

// PatchRuntime applies a partial update to a runtime. Fields that are nil in
// the patch are preserved from the existing entry. Environment is patched
// per-key via the envPatch argument (nil = preserve existing env entirely).
type RuntimePatch struct {
	Name             *string
	Executable       *string
	WorkingDirectory *string
	Environment      []domain.EnvPatchOp
}

func (r *JSONRepository) PatchRuntime(id string, p *RuntimePatch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.runtimes {
		if x.ID == id {
			updated := *x
			if p.Name != nil {
				updated.Name = *p.Name
			}
			if p.Executable != nil {
				updated.Executable = *p.Executable
			}
			if p.WorkingDirectory != nil {
				updated.WorkingDirectory = *p.WorkingDirectory
			}
			if p.Environment != nil {
				updated.Environment = domain.ApplyEnvPatch(x.Environment, p.Environment)
			}
			updated.UpdatedAt = time.Now()
			previous := r.runtimes[i]
			r.runtimes[i] = &updated
			if err := r.saveLocked(); err != nil {
				r.runtimes[i] = previous
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("runtime not found: %s", id)
}

func (r *JSONRepository) DeleteRuntime(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.runtimes {
		if e.ID == id {
			previous := make([]*RuntimeEntry, len(r.runtimes))
			copy(previous, r.runtimes)
			r.runtimes = append(r.runtimes[:i], r.runtimes[i+1:]...)
			if err := r.saveLocked(); err != nil {
				r.runtimes = previous
				return err
			}
			return nil
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

// ReplaceRuntimeAndDelete atomically rebinds models from oldID to newID, then deletes oldID.
func (r *JSONRepository) ReplaceRuntimeAndDelete(oldID, newID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	oldIdx := -1
	newFound := false
	for i, e := range r.runtimes {
		if e.ID == oldID {
			oldIdx = i
		}
		if e.ID == newID {
			newFound = true
		}
	}
	if oldIdx < 0 {
		return 0, fmt.Errorf("runtime not found: %s", oldID)
	}
	if !newFound {
		return 0, fmt.Errorf("runtime not found: %s", newID)
	}

	backupModels := r.models
	backupRuntimes := r.runtimes

	now := time.Now()
	models := make([]*ModelEntry, len(r.models))
	moved := 0
	for i, m := range r.models {
		cp := *m
		if cp.RuntimeID == oldID {
			cp.RuntimeID = newID
			cp.UpdatedAt = now
			moved++
		}
		models[i] = &cp
	}
	runtimes := make([]*RuntimeEntry, 0, len(r.runtimes)-1)
	for i, e := range r.runtimes {
		if i != oldIdx {
			runtimes = append(runtimes, e)
		}
	}

	r.models = models
	r.runtimes = runtimes
	if err := r.saveLocked(); err != nil {
		r.models = backupModels
		r.runtimes = backupRuntimes
		return 0, err
	}
	return moved, nil
}

// CascadeDeleteRuntimeAndModels atomically deletes a runtime and all models referencing it.
func (r *JSONRepository) CascadeDeleteRuntimeAndModels(id string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	found := false
	for _, e := range r.runtimes {
		if e.ID == id {
			found = true
			break
		}
	}
	if !found {
		return 0, fmt.Errorf("runtime not found: %s", id)
	}

	backupModels := r.models
	backupRuntimes := r.runtimes

	models := make([]*ModelEntry, 0, len(r.models))
	deleted := 0
	for _, m := range r.models {
		if m.RuntimeID == id {
			deleted++
			continue
		}
		models = append(models, m)
	}
	runtimes := make([]*RuntimeEntry, 0, len(r.runtimes)-1)
	for _, e := range r.runtimes {
		if e.ID != id {
			runtimes = append(runtimes, e)
		}
	}

	r.models = models
	r.runtimes = runtimes
	if err := r.saveLocked(); err != nil {
		r.models = backupModels
		r.runtimes = backupRuntimes
		return 0, err
	}
	return deleted, nil
}

// DeleteTerminalInstances deletes instances matching the filter. Returns count deleted.
// Only terminal instances (exited, failed, stale) are ever deleted.
func (r *JSONRepository) DeleteTerminalInstances(mode string, ids []string, cutoff time.Time) (int, error) {
	switch mode {
	case "all_terminal", "older_than_7d", "older_than_30d", "selected":
	default:
		return 0, fmt.Errorf("invalid cleanup mode: %s", mode)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	isTerminal := func(e *LaunchInstanceEntry) bool {
		switch e.State {
		case "exited", "failed", "stale":
			return true
		default:
			return false
		}
	}

	shouldDelete := func(e *LaunchInstanceEntry) bool {
		if !isTerminal(e) {
			return false
		}
		switch mode {
		case "all_terminal":
			return true
		case "older_than_7d", "older_than_30d":
			return !e.StoppedAt.IsZero() && e.StoppedAt.Before(cutoff)
		case "selected":
			return idSet[e.ID]
		}
		return false
	}

	deleted := 0
	kept := make([]*LaunchInstanceEntry, 0, len(r.instances))
	for _, e := range r.instances {
		if shouldDelete(e) {
			deleted++
			continue
		}
		kept = append(kept, e)
	}
	if deleted == 0 {
		return 0, nil
	}

	backup := r.instances
	r.instances = kept
	if err := r.saveLocked(); err != nil {
		r.instances = backup
		return 0, err
	}
	return deleted, nil
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
	previous := r.models
	r.models = append(r.models, &cp)
	if err := r.saveLocked(); err != nil {
		r.models = previous
		return err
	}
	return nil
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
			previous := r.models[i]
			r.models[i] = &cp
			if err := r.saveLocked(); err != nil {
				r.models[i] = previous
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("model not found: %s", e.ID)
}

// PatchModel applies a partial update to a model. Fields that are nil in the
// patch are preserved from the existing entry. Environment is patched per-key
// via the envPatch argument (nil = preserve existing env entirely).
type ModelPatch struct {
	Name           *string
	RuntimeID      *string
	Args           *[]string
	Active         *bool
	AutostartDelay *int
	Environment    []domain.EnvPatchOp
}

func (r *JSONRepository) PatchModel(id string, p *ModelPatch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.models {
		if x.ID == id {
			updated := *x
			if p.Name != nil {
				updated.Name = *p.Name
			}
			if p.RuntimeID != nil {
				updated.RuntimeID = *p.RuntimeID
			}
			if p.Args != nil {
				updated.Args = *p.Args
			}
			if p.Active != nil {
				updated.Active = *p.Active
			}
			if p.AutostartDelay != nil {
				updated.AutostartDelay = *p.AutostartDelay
			}
			if p.Environment != nil {
				updated.Environment = domain.ApplyEnvPatch(x.Environment, p.Environment)
			}
			updated.UpdatedAt = time.Now()
			previous := r.models[i]
			r.models[i] = &updated
			if err := r.saveLocked(); err != nil {
				r.models[i] = previous
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("model not found: %s", id)
}

func (r *JSONRepository) DeleteModel(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.models {
		if e.ID == id {
			previous := make([]*ModelEntry, len(r.models))
			copy(previous, r.models)
			r.models = append(r.models[:i], r.models[i+1:]...)
			if err := r.saveLocked(); err != nil {
				r.models = previous
				return err
			}
			return nil
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

// ─── Pipeline CRUD ───

func (r *JSONRepository) CreatePipeline(e *PipelineEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.ID == "" {
		e.ID = r.idGenerator()
	}
	now := time.Now()
	e.CreatedAt = now
	e.UpdatedAt = now
	for _, x := range r.pipelines {
		if x.ID == e.ID {
			return fmt.Errorf("pipeline already exists: %s", e.ID)
		}
	}
	cp := *e
	if e.Models == nil {
		cp.Models = make([]PipelineModel, 0)
	}
	for i := range cp.Models {
		if cp.Models[i].ID == "" {
			cp.Models[i].ID = r.idGenerator()
		}
	}
	previous := r.pipelines
	r.pipelines = append(r.pipelines, &cp)
	if err := r.saveLocked(); err != nil {
		r.pipelines = previous
		return err
	}
	return nil
}

func (r *JSONRepository) GetPipeline(id string) (*PipelineEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.pipelines {
		if e.ID == id {
			cp := *e
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("pipeline not found: %s", id)
}

func (r *JSONRepository) UpdatePipeline(e *PipelineEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.pipelines {
		if x.ID == e.ID {
			e.UpdatedAt = time.Now()
			cp := *e
			if e.Models == nil {
				cp.Models = make([]PipelineModel, 0)
			}
			for j := range cp.Models {
				if cp.Models[j].ID == "" {
					cp.Models[j].ID = r.idGenerator()
				}
			}
			previous := r.pipelines[i]
			r.pipelines[i] = &cp
			if err := r.saveLocked(); err != nil {
				r.pipelines[i] = previous
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("pipeline not found: %s", e.ID)
}

func (r *JSONRepository) DeletePipeline(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.pipelines {
		if e.ID == id {
			previous := make([]*PipelineEntry, len(r.pipelines))
			copy(previous, r.pipelines)
			r.pipelines = append(r.pipelines[:i], r.pipelines[i+1:]...)
			if err := r.saveLocked(); err != nil {
				r.pipelines = previous
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("pipeline not found: %s", id)
}

func (r *JSONRepository) ListPipelines() ([]*PipelineEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*PipelineEntry, len(r.pipelines))
	for i, e := range r.pipelines {
		cp := *e
		out[i] = &cp
	}
	return out, nil
}

// ImportGraph atomically imports runtimes, models, and pipelines into the
// repository. Collision detection, mutation, and durable write happen under
// a single exclusive lock. On save failure, in-memory state is rolled back.
func (r *JSONRepository) ImportGraph(runtimes []*RuntimeEntry, models []*ModelEntry, pipelines []*PipelineEntry) error {
	r.mu.Lock()
	if r.importLockHook != nil {
		r.importLockHook()
	}
	defer r.mu.Unlock()

	// Collision detection under lock.
	var conflicts []ImportConflict
	for _, rt := range runtimes {
		for _, existing := range r.runtimes {
			if existing.ID == rt.ID {
				conflicts = append(conflicts, ImportConflict{Type: "runtime", ID: rt.ID, Reason: "id_exists"})
				break
			}
		}
		for _, existing := range r.runtimes {
			if strings.EqualFold(existing.Name, rt.Name) {
				conflicts = append(conflicts, ImportConflict{Type: "runtime", ID: rt.ID, Reason: "name_exists", Name: rt.Name})
				break
			}
		}
	}
	for _, m := range models {
		for _, existing := range r.models {
			if existing.ID == m.ID {
				conflicts = append(conflicts, ImportConflict{Type: "model", ID: m.ID, Reason: "id_exists"})
				break
			}
		}
	}
	for _, p := range pipelines {
		for _, existing := range r.pipelines {
			if existing.ID == p.ID {
				conflicts = append(conflicts, ImportConflict{Type: "pipeline", ID: p.ID, Reason: "id_exists"})
				break
			}
		}
	}
	if len(conflicts) > 0 {
		return &ErrImportConflict{Conflicts: conflicts}
	}

	// Capture rollback state (copy slice headers, not backing arrays).
	prevRuntimes := make([]*RuntimeEntry, len(r.runtimes))
	copy(prevRuntimes, r.runtimes)
	prevModels := make([]*ModelEntry, len(r.models))
	copy(prevModels, r.models)
	prevPipelines := make([]*PipelineEntry, len(r.pipelines))
	copy(prevPipelines, r.pipelines)

	// Mutate in-memory state.
	r.runtimes = append(r.runtimes, runtimes...)
	r.models = append(r.models, models...)
	r.pipelines = append(r.pipelines, pipelines...)

	// Single durable write.
	if err := r.saveLocked(); err != nil {
		r.runtimes = prevRuntimes
		r.models = prevModels
		r.pipelines = prevPipelines
		return err
	}
	return nil
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
			previous := r.instances[i]
			r.instances[i] = &cp
			if err := r.saveLocked(); err != nil {
				r.instances[i] = previous
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("instance not found: %s", e.ID)
}

func (r *JSONRepository) DeleteInstance(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.instances {
		if e.ID == id {
			previous := make([]*LaunchInstanceEntry, len(r.instances))
			copy(previous, r.instances)
			r.instances = append(r.instances[:i], r.instances[i+1:]...)
			if err := r.saveLocked(); err != nil {
				r.instances = previous
				return err
			}
			return nil
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
			if err := r.saveLocked(); err != nil {
				r.instances[i] = x
				return err
			}
			return nil
		}
	}
	previous := r.instances
	cp := *e
	r.instances = append(r.instances, &cp)
	if err := r.saveLocked(); err != nil {
		r.instances = previous
		return err
	}
	return nil
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
	previous := r.instances
	r.instances = append(r.instances, &cp)
	if err := r.saveLocked(); err != nil {
		r.instances = previous
		return err
	}
	return nil
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
		if domain.InstanceState(e.State).IsInFlight() {
			count++
		}
	}
	return count
}

// ─── ID generation ───

// idSeq makes generated ids unique even when several are minted within the
// same time.Now() tick (ADR 013 D1 mints one entry id per pipeline entry in a
// tight loop). The ent_ prefix is preserved.
var idSeq uint64

func generateID() string {
	n := atomic.AddUint64(&idSeq, 1)
	return fmt.Sprintf("ent_%d_%d", time.Now().UnixNano(), n)
}

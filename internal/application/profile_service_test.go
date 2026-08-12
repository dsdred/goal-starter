package application

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dsdred/goal/internal/storage"
)

// mockRepo wraps JSONRepository with a call counter for CreateProfile.
type countingRepo struct {
	createCalled bool
	wrapped      storage.Repository
}

func (c *countingRepo) CreateProfile(e *storage.ProfileEntry) error {
	c.createCalled = true
	return c.wrapped.CreateProfile(e)
}

func (c *countingRepo) Create(e *storage.LaunchInstanceEntry) error { return c.wrapped.Create(e) }
func (c *countingRepo) CreateInstance(e *storage.LaunchInstanceEntry) error {
	return c.wrapped.CreateInstance(e)
}
func (c *countingRepo) CreateLaunchInstance(e *storage.LaunchInstanceEntry) error {
	return c.wrapped.CreateLaunchInstance(e)
}
func (c *countingRepo) CreateModel(e *storage.ModelEntry) error { return c.wrapped.CreateModel(e) }
func (c *countingRepo) CreateRuntime(e *storage.RuntimeEntry) error {
	return c.wrapped.CreateRuntime(e)
}
func (c *countingRepo) Delete(id string) error         { return c.wrapped.Delete(id) }
func (c *countingRepo) DeleteInstance(id string) error { return c.wrapped.DeleteInstance(id) }
func (c *countingRepo) DeleteLaunchInstance(id string) error {
	return c.wrapped.DeleteLaunchInstance(id)
}
func (c *countingRepo) DeleteModel(id string) error                         { return c.wrapped.DeleteModel(id) }
func (c *countingRepo) DeleteProfile(id string) error                       { return c.wrapped.DeleteProfile(id) }
func (c *countingRepo) DeleteRuntime(id string) error                       { return c.wrapped.DeleteRuntime(id) }
func (c *countingRepo) Get(id string) (*storage.LaunchInstanceEntry, error) { return c.wrapped.Get(id) }
func (c *countingRepo) GetInstance(id string) (*storage.LaunchInstanceEntry, error) {
	return c.wrapped.GetInstance(id)
}
func (c *countingRepo) GetLaunchInstance(id string) (*storage.LaunchInstanceEntry, error) {
	return c.wrapped.GetLaunchInstance(id)
}
func (c *countingRepo) GetModel(id string) (*storage.ModelEntry, error) {
	return c.wrapped.GetModel(id)
}
func (c *countingRepo) GetProfile(id string) (*storage.ProfileEntry, error) {
	return c.wrapped.GetProfile(id)
}
func (c *countingRepo) GetRuntime(id string) (*storage.RuntimeEntry, error) {
	return c.wrapped.GetRuntime(id)
}
func (c *countingRepo) List() ([]*storage.LaunchInstanceEntry, error) { return c.wrapped.List() }
func (c *countingRepo) ListInstances() ([]*storage.LaunchInstanceEntry, error) {
	return c.wrapped.ListInstances()
}
func (c *countingRepo) ListLaunchInstances() ([]*storage.LaunchInstanceEntry, error) {
	return c.wrapped.ListLaunchInstances()
}
func (c *countingRepo) ListModels() ([]*storage.ModelEntry, error) { return c.wrapped.ListModels() }
func (c *countingRepo) ListProfiles() ([]*storage.ProfileEntry, error) {
	return c.wrapped.ListProfiles()
}
func (c *countingRepo) ListRuntimes() ([]*storage.RuntimeEntry, error) {
	return c.wrapped.ListRuntimes()
}
func (c *countingRepo) ListByProfileID(profileID string) ([]*storage.LaunchInstanceEntry, error) {
	return c.wrapped.ListByProfileID(profileID)
}
func (c *countingRepo) SchemaVersion() int                          { return c.wrapped.SchemaVersion() }
func (c *countingRepo) Update(e *storage.LaunchInstanceEntry) error { return c.wrapped.Update(e) }
func (c *countingRepo) UpdateInstance(e *storage.LaunchInstanceEntry) error {
	return c.wrapped.UpdateInstance(e)
}
func (c *countingRepo) UpdateLaunchInstance(e *storage.LaunchInstanceEntry) error {
	return c.wrapped.UpdateLaunchInstance(e)
}
func (c *countingRepo) UpdateModel(e *storage.ModelEntry) error { return c.wrapped.UpdateModel(e) }
func (c *countingRepo) UpdateProfile(e *storage.ProfileEntry) error {
	return c.wrapped.UpdateProfile(e)
}
func (c *countingRepo) UpdateRuntime(e *storage.RuntimeEntry) error {
	return c.wrapped.UpdateRuntime(e)
}
func (c *countingRepo) Upgrade() error                { return c.wrapped.Upgrade() }
func (c *countingRepo) SaveUnified(path string) error { return c.wrapped.SaveUnified(path) }
func (c *countingRepo) ValidateCrossReferences(ctx context.Context) error {
	return c.wrapped.ValidateCrossReferences(ctx)
}
func (c *countingRepo) CountActiveInstances() int { return c.wrapped.CountActiveInstances() }

func TestProfileService_CreateProfile_EmptyName(t *testing.T) {
	dir := t.TempDir()
	repo, err := storage.NewJSONRepository(filepath.Join(dir, "repo.json"))
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	counting := &countingRepo{wrapped: repo}
	svc := NewProfileService(counting)

	err = svc.CreateProfile(context.Background(), &storage.ProfileEntry{Name: ""})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if counting.createCalled {
		t.Fatal("CreateProfile should not be called for empty name")
	}
}

func TestProfileService_CreateProfile_ValidName(t *testing.T) {
	dir := t.TempDir()
	repo, err := storage.NewJSONRepository(filepath.Join(dir, "repo.json"))
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	counting := &countingRepo{wrapped: repo}
	svc := NewProfileService(counting)

	entry := &storage.ProfileEntry{
		Name:      "test-profile",
		RuntimeID: "rt1",
		Host:      "127.0.0.1",
		Port:      11434,
	}

	err = svc.CreateProfile(context.Background(), entry)
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if entry.ID == "" {
		t.Fatal("expected ID to be auto-assigned")
	}
	if !counting.createCalled {
		t.Fatal("CreateProfile should have been called")
	}

	// Verify retrievable.
	got, err := repo.GetProfile(entry.ID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if got.Name != "test-profile" {
		t.Errorf("expected name 'test-profile', got '%s'", got.Name)
	}
}

func TestProfileService_CreateProfile_EmptyName_NoMutation(t *testing.T) {
	dir := t.TempDir()
	repo, err := storage.NewJSONRepository(filepath.Join(dir, "repo.json"))
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	counting := &countingRepo{wrapped: repo}
	svc := NewProfileService(counting)

	// Empty name should not persist.
	err = svc.CreateProfile(context.Background(), &storage.ProfileEntry{Name: ""})
	if err == nil {
		t.Fatal("expected error")
	}

	// Valid profile should still work and get a unique ID.
	valid := &storage.ProfileEntry{
		Name: "valid", RuntimeID: "rt1", Host: "127.0.0.1", Port: 11434,
	}
	err = svc.CreateProfile(context.Background(), valid)
	if err != nil {
		t.Fatalf("CreateProfile valid: %v", err)
	}
	if valid.ID == "" {
		t.Fatal("expected auto-generated ID")
	}

	// List should contain exactly one profile.
	profiles, err := repo.ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Name != "valid" {
		t.Fatalf("expected 'valid', got '%s'", profiles[0].Name)
	}
}

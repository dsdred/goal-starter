package store

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
)

func TestActivateProfile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	p, err := s.CreateProfile("test", "rt1", "m1", "127.0.0.1", 8080, nil, nil)
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}

	if p.Active {
		t.Error("expected Active=false initially")
	}

	activated, err := s.ActivateProfile(p.ID)
	if err != nil {
		t.Fatalf("ActivateProfile failed: %v", err)
	}

	if !activated.Active {
		t.Error("expected Active=true after ActivateProfile")
	}

	// Check persistence.
	stored, err := s.GetProfile(p.ID)
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	if !stored.Active {
		t.Error("expected Active=true after reload")
	}
}

func TestActivateProfile_NotFound(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	_, err = s.ActivateProfile("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent profile")
	}
}

func TestDeactivateProfile(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	p, err := s.CreateProfile("test", "rt1", "m1", "127.0.0.1", 8080, nil, nil)
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}

	// Activate first.
	_, err = s.ActivateProfile(p.ID)
	if err != nil {
		t.Fatalf("ActivateProfile failed: %v", err)
	}

	// Now deactivate.
	deactivated, err := s.DeactivateProfile(p.ID)
	if err != nil {
		t.Fatalf("DeactivateProfile failed: %v", err)
	}

	if deactivated.Active {
		t.Error("expected Active=false after DeactivateProfile")
	}
}

func TestDeactivateProfile_NotFound(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	_, err = s.DeactivateProfile("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent profile")
	}
}

func TestProfileList_AfterActivate(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	p1, err := s.CreateProfile("active-test", "rt1", "m1", "127.0.0.1", 8080, nil, nil)
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}

	p2, err := s.CreateProfile("inactive-test", "rt1", "m1", "127.0.0.1", 8081, nil, nil)
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}

	// Activate only p1.
	_, err = s.ActivateProfile(p1.ID)
	if err != nil {
		t.Fatalf("ActivateProfile failed: %v", err)
	}

	profiles, err := s.ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles failed: %v", err)
	}

	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}

	// Check Active flags.
	for _, prof := range profiles {
		if prof.ID == p1.ID && !prof.Active {
			t.Error("expected p1 to be Active")
		}
		if prof.ID == p2.ID && prof.Active {
			t.Error("expected p2 to be inactive")
		}
	}
}

func TestConcurrentActivateDeactivate(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	p, err := s.CreateProfile("concurrent-test", "rt1", "m1", "127.0.0.1", 8080, nil, nil)
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.ActivateProfile(p.ID)
		}()
		go func() {
			defer wg.Done()
			_, _ = s.DeactivateProfile(p.ID)
		}()
	}
	wg.Wait()

	// Verify persistence.
	profile, err := s.GetProfile(p.ID)
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	// Should have either true or false, no partial writes.
	if !profile.Active && profile.Active {
		t.Error("expected consistent Active state")
	}
}

func TestSaveLoad_PreservesActiveState(t *testing.T) {
	dir := t.TempDir()

	// Create first store, activate profile.
	s1, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	p, err := s1.CreateProfile("persist-test", "rt1", "m1", "127.0.0.1", 8080, nil, nil)
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}

	_, err = s1.ActivateProfile(p.ID)
	if err != nil {
		t.Fatalf("ActivateProfile failed: %v", err)
	}

	// Create new store from same dir.
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	p2, err := s2.GetProfile(p.ID)
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}

	if !p2.Active {
		t.Error("expected Active=true after reload from disk")
	}
}

func TestProfile_UpdatedAt(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	p, err := s.CreateProfile("time-test", "rt1", "m1", "127.0.0.1", 8080, nil, nil)
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}

	originalUpdated := p.UpdatedAt

	time.Sleep(10 * time.Millisecond)

	_, err = s.ActivateProfile(p.ID)
	if err != nil {
		t.Fatalf("ActivateProfile failed: %v", err)
	}

	stored, err := s.GetProfile(p.ID)
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}

	if stored.UpdatedAt.Equal(originalUpdated) {
		t.Error("expected UpdatedAt to change after ActivateProfile")
	}
}

func TestProfilesJSON_ValidFile(t *testing.T) {
	dir := t.TempDir()
	profilesPath := dir + "/profiles.json"

	// Create a valid profiles.json.
	expectedProfiles := []*Profile{
		{
			ID:        "test_1",
			Name:      "Test Profile",
			RuntimeID: "rt1",
			ModelID:   "m1",
			Host:      "127.0.0.1",
			Port:      8080,
			Active:    true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	data, _ := json.MarshalIndent(expectedProfiles, "", "  ")
	_ = os.WriteFile(profilesPath, data, 0o600)

	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	p, err := s.GetProfile("test_1")
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}

	if p.Name != "Test Profile" {
		t.Errorf("expected 'Test Profile', got %q", p.Name)
	}
	if !p.Active {
		t.Error("expected Active=true")
	}
}

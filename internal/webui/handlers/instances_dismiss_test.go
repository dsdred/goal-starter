package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/application"
	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
)

func TestInstancesHandler_Dismiss_Orphan(t *testing.T) {
	repo := newTestRepo(t)

	insStore := &mockInstanceStore{}
	orphanEntry := &domain.LaunchInstanceEntry{
		ID:         "inst-orphan-1",
		State:      "orphan",
		PID:        42,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  time.Now().Add(-time.Hour),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	insStore.GetFunc = func(id string) (*domain.LaunchInstanceEntry, error) {
		if id == "inst-orphan-1" {
			return orphanEntry, nil
		}
		return nil, errors.New("not found")
	}
	var updated *domain.LaunchInstanceEntry
	insStore.UpdateFunc = func(e *domain.LaunchInstanceEntry) error {
		updated = e
		return nil
	}

	sup := process.NewSupervisor(insStore)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/inst-orphan-1/dismiss", nil)
	w := httptest.NewRecorder()
	h.Dismiss(w, req)
	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if updated == nil {
		t.Fatal("expected Update to be called")
	}
	if updated.State != "stale" {
		t.Errorf("expected stale, got %s", updated.State)
	}
	if updated.RecoveryReason != "reconciled-by-user" {
		t.Errorf("expected reconciled-by-user, got %q", updated.RecoveryReason)
	}
}

func TestInstancesHandler_Dismiss_NotOrphan(t *testing.T) {
	repo := newTestRepo(t)

	runningEntry := &domain.LaunchInstanceEntry{
		ID:        "inst-running-1",
		State:     "running",
		PID:       42,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	insStore := &mockInstanceStore{}
	insStore.GetFunc = func(id string) (*domain.LaunchInstanceEntry, error) {
		if id == "inst-running-1" {
			return runningEntry, nil
		}
		return nil, errors.New("not found")
	}

	sup := process.NewSupervisor(insStore)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/inst-running-1/dismiss", nil)
	w := httptest.NewRecorder()
	h.Dismiss(w, req)
	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got %d", resp.StatusCode)
	}
}

func TestInstancesHandler_Dismiss_NotFound(t *testing.T) {
	repo := newTestRepo(t)

	insStore := &mockInstanceStore{}
	insStore.GetFunc = func(id string) (*domain.LaunchInstanceEntry, error) {
		return nil, errors.New("not found")
	}

	sup := process.NewSupervisor(insStore)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances/nonexistent/dismiss", nil)
	w := httptest.NewRecorder()
	h.Dismiss(w, req)
	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestInstancesHandler_Dismiss_MissingID(t *testing.T) {
	repo := newTestRepo(t)
	sup := newTestSupervisor(t)
	insSvc := application.NewInstanceService(sup, repo)
	h := NewInstancesHandler(insSvc, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/instances//dismiss", nil)
	w := httptest.NewRecorder()
	h.Dismiss(w, req)
	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

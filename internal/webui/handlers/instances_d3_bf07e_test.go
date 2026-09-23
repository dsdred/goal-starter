package handlers

import (
	"errors"
	"net/http"
	"testing"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/process"
)

// D3 / BF-07e: the restart preflight's unknown-instance result is the documented
// bounded not-found class. The error asserted here is the one
// Supervisor.PreflightRestart actually returns, classified by the shared
// lifecycle mapper both preflight call sites already use — so the correction
// routes an existing error into an existing, already-documented class instead
// of adding one.
func TestPreflightUnknownInstanceIsClassifiedNotFound(t *testing.T) {
	repo := newTestRepo(t)
	sup := process.NewSupervisor(repo)

	err := sup.PreflightRestart([]domain.InstanceID{"d3-handler-missing"})
	if err == nil {
		t.Fatal("expected a bounded error for an unknown instance")
	}
	if !errors.Is(err, process.ErrInstanceNotFound) {
		t.Fatalf("PreflightRestart err = %v, want ErrInstanceNotFound", err)
	}

	status, code, msg := writeLifecycle(t, err)
	if status != http.StatusNotFound || code != "not_found" || msg != "instance_not_found" {
		t.Fatalf("got %d code=%q error=%q, want 404 not_found instance_not_found", status, code, msg)
	}
}

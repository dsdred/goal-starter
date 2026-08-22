package domain

import (
	"testing"
	"time"
)

func TestInstanceStateOrphan_IsTerminal(t *testing.T) {
	inst := &LaunchInstance{ID: "test", State: InstanceStateOrphan}
	if inst.IsTerminal() {
		t.Error("orphan must NOT be terminal")
	}
}

func TestInstanceStateOrphan_IsActive(t *testing.T) {
	inst := &LaunchInstance{ID: "test", State: InstanceStateOrphan}
	if inst.IsActive() {
		t.Error("orphan must NOT be active")
	}
}

func TestInstanceStateStale_IsTerminal(t *testing.T) {
	inst := &LaunchInstance{ID: "test", State: InstanceStateStale}
	if !inst.IsTerminal() {
		t.Error("stale MUST be terminal")
	}
}

func TestUpdateState_Orphan_ClearsError(t *testing.T) {
	inst := &LaunchInstance{
		ID:        "test",
		State:     InstanceStateRunning,
		PID:       1234,
		ExitCode:  ptr(1),
		ExitClass: InstanceExitFailure,
		LastError: "something went wrong",
	}
	inst.UpdateState(InstanceStateOrphan)
	if inst.ExitCode != nil {
		t.Error("orphan must clear ExitCode")
	}
	if inst.ExitClass != "" {
		t.Error("orphan must clear ExitClass")
	}
	if inst.LastError != "" {
		t.Error("orphan must clear LastError")
	}
	if !inst.StoppedAt.IsZero() {
		t.Error("orphan must clear StoppedAt")
	}
}

func TestUpdateState_Stale_SetsStoppedAt(t *testing.T) {
	inst := &LaunchInstance{ID: "test", State: InstanceStateOrphan}
	inst.UpdateState(InstanceStateStale)
	if inst.StoppedAt.IsZero() {
		t.Error("stale transition should set StoppedAt")
	}
}

func TestToStorageEntry_RecoveryReason(t *testing.T) {
	inst := &LaunchInstance{
		ID:             "test",
		State:          InstanceStateStale,
		RecoveryReason: "pid-not-found",
	}
	entry := ToStorageEntry(inst)
	if entry.RecoveryReason != "pid-not-found" {
		t.Errorf("expected recovery_reason=pid-not-found, got %q", entry.RecoveryReason)
	}
}

func TestToDomain_RecoveryReason(t *testing.T) {
	entry := &LaunchInstanceEntry{
		ID:             "test",
		State:          "stale",
		RecoveryReason: "identity-unconfirmed",
	}
	inst := ToDomain(entry)
	if inst.RecoveryReason != "identity-unconfirmed" {
		t.Errorf("expected recovery_reason=identity-unconfirmed, got %q", inst.RecoveryReason)
	}
}

func TestOrphanToStale_Transition(t *testing.T) {
	inst := &LaunchInstance{ID: "test", State: InstanceStateOrphan}
	inst.UpdateState(InstanceStateStale)
	if inst.State != InstanceStateStale {
		t.Errorf("expected stale, got %s", inst.State)
	}
	if !inst.IsTerminal() {
		t.Error("after dismiss, instance must be terminal")
	}
}

func ptr(i int) *int {
	return &i
}

var _ = time.Now

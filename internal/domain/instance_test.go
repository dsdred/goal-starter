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

func TestInstanceStateOrphan_IsLive(t *testing.T) {
	inst := &LaunchInstance{ID: "test", State: InstanceStateOrphan}
	if inst.IsLive() {
		t.Error("orphan must NOT be live")
	}
}

func TestInstanceStateSemantics_Matrix(t *testing.T) {
	cases := []struct {
		state    InstanceState
		live     bool
		inFlight bool
		ros      bool
		terminal bool
	}{
		{InstanceStatePending, false, true, false, false},
		{InstanceStateStarting, true, true, true, false},
		{InstanceStateRunning, true, true, true, false},
		{InstanceStateStopping, true, true, false, false},
		{InstanceStateExited, false, false, false, true},
		{InstanceStateFailed, false, false, false, true},
		{InstanceStateStale, false, false, false, true},
		{InstanceStateOrphan, false, false, false, false},
		{InstanceStateUnknown, false, false, false, false},
	}
	for _, c := range cases {
		inst := &LaunchInstance{ID: "test", State: c.state}
		if got := inst.IsLive(); got != c.live {
			t.Errorf("%s: IsLive() = %v, want %v", c.state, got, c.live)
		}
		if got := inst.IsInFlight(); got != c.inFlight {
			t.Errorf("%s: IsInFlight() = %v, want %v", c.state, got, c.inFlight)
		}
		if got := inst.IsRunningOrStarting(); got != c.ros {
			t.Errorf("%s: IsRunningOrStarting() = %v, want %v", c.state, got, c.ros)
		}
		if got := inst.IsTerminal(); got != c.terminal {
			t.Errorf("%s: IsTerminal() = %v, want %v", c.state, got, c.terminal)
		}
		if got := c.state.IsLive(); got != c.live {
			t.Errorf("%s: InstanceState.IsLive() = %v, want %v", c.state, got, c.live)
		}
		if got := c.state.IsInFlight(); got != c.inFlight {
			t.Errorf("%s: InstanceState.IsInFlight() = %v, want %v", c.state, got, c.inFlight)
		}
		if got := c.state.IsRunningOrStarting(); got != c.ros {
			t.Errorf("%s: InstanceState.IsRunningOrStarting() = %v, want %v", c.state, got, c.ros)
		}
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

package process

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
)

// fakeKiller implements platform.ProcessKiller with scripted errors and
// signal-count tracking. onGraceful/onForce run synchronously before the
// scripted error is returned, letting tests flip prober state at signal time.
type fakeKiller struct {
	gracefulErr   error
	forceErr      error
	gracefulCount int
	forceCount    int
	onGraceful    func()
	onForce       func()
}

func (f *fakeKiller) SignalGraceful(pid int) error {
	f.gracefulCount++
	if f.onGraceful != nil {
		f.onGraceful()
	}
	return f.gracefulErr
}

func (f *fakeKiller) SignalForce(pid int) error {
	f.forceCount++
	if f.onForce != nil {
		f.onForce()
	}
	return f.forceErr
}

// shortenKillWindows tightens the grace/poll windows for tests and restores
// them afterwards.
func shortenKillWindows(t *testing.T) {
	t.Helper()
	oldGrace, oldPoll := orphanKillGrace, orphanKillPoll
	orphanKillGrace = 300 * time.Millisecond
	orphanKillPoll = 25 * time.Millisecond
	t.Cleanup(func() {
		orphanKillGrace = oldGrace
		orphanKillPoll = oldPoll
	})
}

func newKillFixture(t *testing.T, pid int, alive bool, ident platform.ProcessIdentity) (*mockStore, *mockProber, *fakeKiller, *Supervisor) {
	t.Helper()
	store := newMockStore()
	inst := &domain.LaunchInstance{
		ID:         "inst-kill",
		State:      domain.InstanceStateOrphan,
		PID:        pid,
		Executable: "/usr/bin/fake-runtime",
		StartedAt:  ident.StartTime,
		CreatedAt:  time.Now(),
	}
	if inst.StartedAt.IsZero() {
		inst.StartedAt = time.Now().Add(-time.Minute)
	}
	store.instances["inst-kill"] = domain.ToStorageEntry(inst)

	prober := newMockProber()
	prober.alive[pid] = alive
	prober.identities[pid] = ident
	killer := &fakeKiller{}

	sup := NewSupervisor(store)
	sup.prober = prober
	sup.killer = killer
	return store, prober, killer, sup
}

func matchingIdentity() platform.ProcessIdentity {
	start := time.Now().Add(-time.Minute)
	return platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/fake-runtime",
		StartTime:      start,
		HasStartTime:   true,
	}
}

// ADR 008 acceptance 1: kill on an identity-verified orphan terminates the
// process and reconciles to stale(killed-by-user, exit_class=killed).
func TestKillOrphan_Terminated(t *testing.T) {
	shortenKillWindows(t)
	store, prober, killer, sup := newKillFixture(t, 2000, true, matchingIdentity())
	if runtime.GOOS == "windows" {
		killer.onForce = func() { prober.alive[2000] = false }
	} else {
		killer.onGraceful = func() { prober.alive[2000] = false }
	}

	res, err := sup.KillOrphan(context.Background(), "inst-kill")
	if err != nil {
		t.Fatalf("KillOrphan error: %v", err)
	}
	if res.Outcome != KillOutcomeTerminated {
		t.Errorf("expected terminated, got %s", res.Outcome)
	}
	if runtime.GOOS == "windows" {
		if res.Reason != KillReasonTerminateProcess {
			t.Errorf("expected terminateprocess, got %q", res.Reason)
		}
		if killer.forceCount != 1 || killer.gracefulCount != 0 {
			t.Errorf("expected exactly one force signal, got force=%d graceful=%d", killer.forceCount, killer.gracefulCount)
		}
	} else {
		if res.Reason != KillReasonSIGTERM {
			t.Errorf("expected sigterm, got %q", res.Reason)
		}
		if killer.gracefulCount != 1 || killer.forceCount != 0 {
			t.Errorf("expected graceful only, got graceful=%d force=%d", killer.gracefulCount, killer.forceCount)
		}
	}

	entry := store.instances["inst-kill"]
	if entry.State != "stale" {
		t.Errorf("expected stale, got %s", entry.State)
	}
	if entry.RecoveryReason != "killed-by-user" {
		t.Errorf("expected killed-by-user, got %q", entry.RecoveryReason)
	}
	if entry.ExitClass != "killed" {
		t.Errorf("expected exit_class killed, got %q", entry.ExitClass)
	}
	if entry.StoppedAt.IsZero() {
		t.Error("expected stopped_at set")
	}
}

// ADR 008 acceptance 5 (Unix): SIGKILL escalation only after the grace
// period, with a fresh re-verification, then confirmed exit (Case B).
func TestKillOrphan_ForceAfterGrace_Unix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only sequence")
	}
	shortenKillWindows(t)
	store, prober, killer, sup := newKillFixture(t, 2100, true, matchingIdentity())
	killer.onForce = func() { prober.alive[2100] = false }

	res, err := sup.KillOrphan(context.Background(), "inst-kill")
	if err != nil {
		t.Fatalf("KillOrphan error: %v", err)
	}
	if res.Outcome != KillOutcomeTerminated || res.Reason != KillReasonSIGKILL {
		t.Errorf("expected terminated/sigkill, got %s/%s", res.Outcome, res.Reason)
	}
	if killer.gracefulCount != 1 || killer.forceCount != 1 {
		t.Errorf("expected graceful then force, got graceful=%d force=%d", killer.gracefulCount, killer.forceCount)
	}
	if store.instances["inst-kill"].State != "stale" {
		t.Errorf("expected stale, got %s", store.instances["inst-kill"].State)
	}
}

// ADR 008 acceptance 5 (Unix): escalation re-verification failure stops the
// sequence; no SIGKILL is sent (Case F at escalation).
func TestKillOrphan_EscalationReverifyMismatch_Unix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only sequence")
	}
	shortenKillWindows(t)
	store, prober, killer, sup := newKillFixture(t, 2200, true, matchingIdentity())
	killer.onGraceful = func() {
		// The process "re-identifies" as a different executable at grace end.
		prober.identities[2200] = platform.ProcessIdentity{
			ExecutablePath: "/usr/bin/some-other-app",
			StartTime:      prober.identities[2200].StartTime,
			HasStartTime:   true,
		}
	}

	res, err := sup.KillOrphan(context.Background(), "inst-kill")
	if !errors.Is(err, ErrKillIdentityUnconfirmed) {
		t.Fatalf("expected identity-unconfirmed, got %v", err)
	}
	if res.Reason != KillReasonIdentityUnconfirmed {
		t.Errorf("expected identity-unconfirmed reason, got %q", res.Reason)
	}
	if killer.forceCount != 0 {
		t.Errorf("no SIGKILL may be sent after re-verification failure, got force=%d", killer.forceCount)
	}
	entry := store.instances["inst-kill"]
	if entry.State != "orphan" {
		t.Errorf("orphan must be preserved, got %s", entry.State)
	}
	if entry.LastError != "identity-unconfirmed" {
		t.Errorf("expected last_error identity-unconfirmed, got %q", entry.LastError)
	}
}

// ADR 008 acceptance 8: unconfirmable termination (still visible after the
// force stage) preserves the orphan; no success is reported (Case C).
func TestKillOrphan_Unconfirmed(t *testing.T) {
	shortenKillWindows(t)
	store, _, _, sup := newKillFixture(t, 2300, true, matchingIdentity())
	// The process never dies.

	res, err := sup.KillOrphan(context.Background(), "inst-kill")
	if !errors.Is(err, ErrKillOutcomeUnconfirmed) {
		t.Fatalf("expected unconfirmed, got %v", err)
	}
	if res.Outcome != KillOutcomeRefused || res.Reason != KillReasonUnconfirmed {
		t.Errorf("expected refused/unconfirmed, got %s/%s", res.Outcome, res.Reason)
	}
	entry := store.instances["inst-kill"]
	if entry.State != "orphan" {
		t.Errorf("orphan must be preserved on unconfirmed termination, got %s", entry.State)
	}
	if entry.LastError != "unconfirmed" {
		t.Errorf("expected last_error unconfirmed, got %q", entry.LastError)
	}
}

// ADR 008 acceptance 7: privilege denial is an explicit refusal with the
// orphan preserved (Case D).
func TestKillOrphan_PrivilegeDenied(t *testing.T) {
	store, _, killer, sup := newKillFixture(t, 2400, true, matchingIdentity())
	killer.gracefulErr = platform.ErrKillAccessDenied
	killer.forceErr = platform.ErrKillAccessDenied

	res, err := sup.KillOrphan(context.Background(), "inst-kill")
	if !errors.Is(err, ErrKillInsufficientPrivilege) {
		t.Fatalf("expected insufficient-privilege, got %v", err)
	}
	if res.Outcome != KillOutcomeRefused || res.Reason != KillReasonInsufficientPrivilege {
		t.Errorf("expected refused/insufficient-privilege, got %s/%s", res.Outcome, res.Reason)
	}
	entry := store.instances["inst-kill"]
	if entry.State != "orphan" {
		t.Errorf("orphan must be preserved, got %s", entry.State)
	}
	if entry.LastError != "insufficient-privilege" {
		t.Errorf("expected last_error insufficient-privilege, got %q", entry.LastError)
	}
}

// ADR 008 acceptance 3: PID gone at kill time sends no signal and reconciles
// to stale(pid-gone) with unset exit_class (Case E).
func TestKillOrphan_PIDGone(t *testing.T) {
	store, _, killer, sup := newKillFixture(t, 2500, false, matchingIdentity())

	res, err := sup.KillOrphan(context.Background(), "inst-kill")
	if err != nil {
		t.Fatalf("KillOrphan error: %v", err)
	}
	if res.Outcome != KillOutcomeReconciled || res.Reason != KillReasonPIDGone {
		t.Errorf("expected reconciled/pid-gone, got %s/%s", res.Outcome, res.Reason)
	}
	if killer.gracefulCount != 0 || killer.forceCount != 0 {
		t.Errorf("no signal may be sent, got graceful=%d force=%d", killer.gracefulCount, killer.forceCount)
	}
	entry := store.instances["inst-kill"]
	if entry.State != "stale" {
		t.Errorf("expected stale, got %s", entry.State)
	}
	if entry.RecoveryReason != "pid-gone" {
		t.Errorf("expected pid-gone, got %q", entry.RecoveryReason)
	}
	if entry.ExitClass != "" {
		t.Errorf("exit_class must stay unset for pid-gone, got %q", entry.ExitClass)
	}
}

// The process exits between verification and the signal syscall: nothing was
// killed; the attempt is reconciled as pid-gone (Case E, signal race).
func TestKillOrphan_SignalRaceAlreadyGone(t *testing.T) {
	store, _, killer, sup := newKillFixture(t, 2600, true, matchingIdentity())
	killer.gracefulErr = platform.ErrKillAlreadyGone
	killer.forceErr = platform.ErrKillAlreadyGone

	res, err := sup.KillOrphan(context.Background(), "inst-kill")
	if err != nil {
		t.Fatalf("KillOrphan error: %v", err)
	}
	if res.Outcome != KillOutcomeReconciled || res.Reason != KillReasonPIDGone {
		t.Errorf("expected reconciled/pid-gone, got %s/%s", res.Outcome, res.Reason)
	}
	entry := store.instances["inst-kill"]
	if entry.State != "stale" || entry.RecoveryReason != "pid-gone" {
		t.Errorf("expected stale/pid-gone, got %s/%q", entry.State, entry.RecoveryReason)
	}
	if entry.ExitClass != "" {
		t.Errorf("exit_class must stay unset, got %q", entry.ExitClass)
	}
}

// ADR 008 acceptance 2: identity unconfirmed at kill time refuses with no
// signal (Case F) — path mismatch.
func TestKillOrphan_IdentityPathMismatch(t *testing.T) {
	store, _, killer, sup := newKillFixture(t, 2700, true, platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/some-other-app",
		StartTime:      time.Now().Add(-time.Minute),
		HasStartTime:   true,
	})

	_, err := sup.KillOrphan(context.Background(), "inst-kill")
	if !errors.Is(err, ErrKillIdentityUnconfirmed) {
		t.Fatalf("expected identity-unconfirmed, got %v", err)
	}
	if killer.gracefulCount != 0 || killer.forceCount != 0 {
		t.Errorf("no signal may be sent, got graceful=%d force=%d", killer.gracefulCount, killer.forceCount)
	}
	entry := store.instances["inst-kill"]
	if entry.State != "orphan" {
		t.Errorf("orphan must be preserved, got %s", entry.State)
	}
	if entry.LastError != "identity-unconfirmed" {
		t.Errorf("expected last_error identity-unconfirmed, got %q", entry.LastError)
	}
}

// ADR 008 acceptance 2/4: start-time mismatch refuses (Case F).
func TestKillOrphan_IdentityStartTimeMismatch(t *testing.T) {
	ident := matchingIdentity()
	ident.StartTime = time.Now().Add(-24 * time.Hour)
	store, _, killer, sup := newKillFixture(t, 2800, true, ident)
	// Recorded instance start time differs from the live probe by 24h.
	store.instances["inst-kill"].StartedAt = time.Now().Add(-time.Minute)

	_, err := sup.KillOrphan(context.Background(), "inst-kill")
	if !errors.Is(err, ErrKillIdentityUnconfirmed) {
		t.Fatalf("expected identity-unconfirmed, got %v", err)
	}
	if killer.gracefulCount != 0 || killer.forceCount != 0 {
		t.Errorf("no signal may be sent, got graceful=%d force=%d", killer.gracefulCount, killer.forceCount)
	}
	if store.instances["inst-kill"].State != "orphan" {
		t.Errorf("orphan must be preserved, got %s", store.instances["inst-kill"].State)
	}
}

// ADR 008 acceptance 4: no PID-only kill exists. A live probe without a
// start time (HasStartTime=false) MUST refuse, unlike the lenient
// detection-time verifier.
func TestKillOrphan_StartTimeUnavailableRefused(t *testing.T) {
	store, _, killer, sup := newKillFixture(t, 2900, true, platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/fake-runtime",
		HasStartTime:   false,
	})

	res, err := sup.KillOrphan(context.Background(), "inst-kill")
	if !errors.Is(err, ErrKillIdentityUnconfirmed) {
		t.Fatalf("expected identity-unconfirmed (strict verifier), got %v", err)
	}
	if res.Reason != KillReasonIdentityUnconfirmed {
		t.Errorf("expected identity-unconfirmed reason, got %q", res.Reason)
	}
	if killer.gracefulCount != 0 || killer.forceCount != 0 {
		t.Errorf("no signal may be sent without a start-time anchor, got graceful=%d force=%d", killer.gracefulCount, killer.forceCount)
	}
	if store.instances["inst-kill"].State != "orphan" {
		t.Errorf("orphan must be preserved, got %s", store.instances["inst-kill"].State)
	}
}

// ADR 008 acceptance 10: kill never touches non-orphan instances (Case G).
func TestKillOrphan_NotOrphan(t *testing.T) {
	store := newMockStore()
	for _, state := range []string{"stale", "exited", "failed", "running"} {
		id := "inst-" + state
		inst := &domain.LaunchInstance{
			ID:        domain.InstanceID(id),
			State:     domain.InstanceState(state),
			PID:       3000,
			CreatedAt: time.Now(),
		}
		store.instances[id] = domain.ToStorageEntry(inst)
	}

	sup := NewSupervisor(store)
	sup.prober = newMockProber()
	killer := &fakeKiller{}
	sup.killer = killer

	for _, state := range []string{"stale", "exited", "failed", "running"} {
		id := "inst-" + state
		_, err := sup.KillOrphan(context.Background(), domain.InstanceID(id))
		if err == nil {
			t.Errorf("%s: expected error for non-orphan", state)
		}
		if store.instances[id].State != state {
			t.Errorf("%s: state must be unchanged, got %s", state, store.instances[id].State)
		}
	}
	if killer.gracefulCount != 0 || killer.forceCount != 0 {
		t.Errorf("no signal may be sent for non-orphan instances")
	}
}

// ADR 008 acceptance 14: after a successful kill the instance is stale; a
// repeat kill is a conflict (no longer orphan).
func TestKillOrphan_Idempotency(t *testing.T) {
	shortenKillWindows(t)
	store, prober, killer, sup := newKillFixture(t, 3100, true, matchingIdentity())
	if runtime.GOOS == "windows" {
		killer.onForce = func() { prober.alive[3100] = false }
	} else {
		killer.onGraceful = func() { prober.alive[3100] = false }
	}

	if _, err := sup.KillOrphan(context.Background(), "inst-kill"); err != nil {
		t.Fatalf("first KillOrphan: %v", err)
	}
	if store.instances["inst-kill"].State != "stale" {
		t.Fatalf("expected stale after kill, got %s", store.instances["inst-kill"].State)
	}
	prober.alive[3100] = true // even if the PID reappears, the record is stale
	_, err := sup.KillOrphan(context.Background(), "inst-kill")
	if err == nil {
		t.Fatal("expected error on repeat kill of a stale instance")
	}
}

// ADR 008 acceptance 17: Dismiss remains the always-available safe fallback
// after a refused kill (the orphan state is preserved).
func TestKillOrphan_RefusedThenDismiss(t *testing.T) {
	store, _, _, sup := newKillFixture(t, 3200, true, platform.ProcessIdentity{
		ExecutablePath: "/usr/bin/some-other-app",
		StartTime:      time.Now().Add(-time.Minute),
		HasStartTime:   true,
	})

	if _, err := sup.KillOrphan(context.Background(), "inst-kill"); err == nil {
		t.Fatal("expected refusal")
	}
	if err := sup.DismissOrphan(context.Background(), "inst-kill"); err != nil {
		t.Fatalf("Dismiss after refusal must work: %v", err)
	}
	entry := store.instances["inst-kill"]
	if entry.State != "stale" || entry.RecoveryReason != "reconciled-by-user" {
		t.Errorf("expected stale/reconciled-by-user, got %s/%q", entry.State, entry.RecoveryReason)
	}
}

// verifyIdentityForKill strict matrix (ADR 008 acceptance 4).
func TestVerifyIdentityForKill_StrictMatrix(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	base := func() *domain.LaunchInstance {
		return &domain.LaunchInstance{
			ID:         "x",
			Executable: "/usr/bin/fake-runtime",
			StartedAt:  start,
		}
	}
	ident := func() platform.ProcessIdentity {
		return platform.ProcessIdentity{
			ExecutablePath: "/usr/bin/fake-runtime",
			StartTime:      start,
			HasStartTime:   true,
		}
	}

	cases := []struct {
		name   string
		inst   *domain.LaunchInstance
		pid    platform.ProcessIdentity
		expect bool
	}{
		{"full match", base(), ident(), true},
		{"path mismatch", base(), func() platform.ProcessIdentity { i := ident(); i.ExecutablePath = "/usr/bin/other"; return i }(), false},
		{"live start time missing", base(), func() platform.ProcessIdentity { i := ident(); i.HasStartTime = false; return i }(), false},
		{"recorded start time missing", func() *domain.LaunchInstance { i := base(); i.StartedAt = time.Time{}; return i }(), ident(), false},
		{"start time mismatch", base(), func() platform.ProcessIdentity { i := ident(); i.StartTime = time.Now().Add(-24 * time.Hour); return i }(), false},
		{"empty live path", base(), platform.ProcessIdentity{StartTime: start, HasStartTime: true}, false},
		{"empty recorded path", func() *domain.LaunchInstance { i := base(); i.Executable = ""; return i }(), ident(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := verifyIdentityForKill(tc.inst, tc.pid); got != tc.expect {
				t.Errorf("verifyIdentityForKill = %v, want %v", got, tc.expect)
			}
		})
	}
}

package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"time"

	"github.com/dsdred/goal/internal/domain"
	"github.com/dsdred/goal/internal/platform"
)

var (
	// orphanKillGrace is the bounded SIGTERM grace period (ADR 008 D2).
	orphanKillGrace = 5 * time.Second
	// orphanKillPoll is the liveness poll interval during grace and
	// post-force confirmation windows.
	orphanKillPoll = 250 * time.Millisecond
)

// SetKillWindows overrides the kill grace/poll windows (test hook).
func SetKillWindows(grace, poll time.Duration) {
	orphanKillGrace = grace
	orphanKillPoll = poll
}

// KillOutcome is the bounded ADR 008 outcome vocabulary (audit + API).
type KillOutcome string

const (
	KillOutcomeTerminated KillOutcome = "terminated"
	KillOutcomeReconciled KillOutcome = "reconciled"
	KillOutcomeRefused    KillOutcome = "refused"
)

// Bounded ADR 008 reason vocabulary.
const (
	KillReasonSIGTERM               = "sigterm"
	KillReasonSIGKILL               = "sigkill"
	KillReasonTerminateProcess      = "terminateprocess"
	KillReasonPIDGone               = "pid-gone"
	KillReasonIdentityUnconfirmed   = "identity-unconfirmed"
	KillReasonInsufficientPrivilege = "insufficient-privilege"
	KillReasonUnconfirmed           = "unconfirmed"
)

// KillResult is the classified outcome of a KillOrphan attempt (ADR 008).
// Success outcomes (terminated/reconciled) carry a nil error; refused
// outcomes carry the matching sentinel error.
type KillResult struct {
	Outcome KillOutcome
	Reason  string
}

// Sentinel errors for refused kills (ADR 008 Cases C/D/F). Case G (not
// orphan / not found) uses the ordinary lookup errors.
var (
	ErrKillIdentityUnconfirmed   = errors.New("kill refused: identity unconfirmed")
	ErrKillInsufficientPrivilege = errors.New("kill refused: insufficient privilege")
	ErrKillOutcomeUnconfirmed    = errors.New("kill outcome unconfirmed")
)

// verifyIdentityForKill applies the ADR 008 strict identity contract:
// executable path equal AND start time present and equal on both sides.
// Unlike verifyIdentity (detection-time, deliberately lenient), a missing
// start-time anchor refuses the kill (owner decision D1).
func verifyIdentityForKill(inst *domain.LaunchInstance, id platform.ProcessIdentity) bool {
	if id.ExecutablePath == "" {
		return false
	}
	if inst.Executable == "" {
		return false
	}
	if !pathsEqual(inst.Executable, id.ExecutablePath) {
		return false
	}
	if !id.HasStartTime {
		return false
	}
	if inst.StartedAt.IsZero() {
		return false
	}
	return timesApproximatelyEqual(inst.StartedAt, id.StartTime)
}

// KillOrphan terminates an orphan process with strict identity re-verification
// before every destructive syscall and reconciles the instance per the ADR 008
// post-kill lifecycle contract (Cases A-G). Kill is only ever invoked as an
// explicit user action (D6); no code path calls it automatically.
func (s *Supervisor) KillOrphan(ctx context.Context, instanceID domain.InstanceID) (KillResult, error) {
	if s.store == nil {
		return KillResult{}, fmt.Errorf("no store configured")
	}
	if s.prober == nil {
		return KillResult{}, fmt.Errorf("no prober configured")
	}
	killer := s.killer
	if killer == nil {
		killer = platform.NewProcessKiller()
	}

	entry, err := s.store.Get(string(instanceID))
	if err != nil {
		return KillResult{}, fmt.Errorf("get instance %s: %w", string(instanceID), err)
	}
	inst := domain.ToDomain(entry)
	if inst.State != domain.InstanceStateOrphan {
		return KillResult{}, fmt.Errorf("instance %s is not in orphan state (current: %s)", string(instanceID), string(inst.State))
	}

	// No addressable PID: the record cannot refer to a running process.
	// Reconcile honestly as pid-gone (no signal sent, Case E).
	if inst.PID <= 0 {
		return s.finishKill(inst, "pid-gone", "", KillResult{Outcome: KillOutcomeReconciled, Reason: KillReasonPIDGone})
	}

	// Strict re-verification immediately before the first destructive call.
	alive, err := s.prober.IsProcessAlive(inst.PID)
	if err != nil || !alive {
		// The PID is gone (or liveness cannot be established): nothing to
		// kill. Reconcile as pid-gone, no signal sent (Case E).
		return s.finishKill(inst, "pid-gone", "", KillResult{Outcome: KillOutcomeReconciled, Reason: KillReasonPIDGone})
	}
	identity, err := s.prober.GetProcessIdentity(inst.PID)
	if err != nil || !verifyIdentityForKill(inst, identity) {
		return s.refuseKill(inst, KillReasonIdentityUnconfirmed, ErrKillIdentityUnconfirmed)
	}

	if runtime.GOOS == "windows" {
		return s.killOrphanWindows(ctx, inst, killer)
	}
	return s.killOrphanUnix(ctx, inst, killer)
}

// killOrphanUnix implements the ADR 008 D2 sequence:
// SIGTERM -> bounded grace -> re-verify -> SIGKILL only if still alive and
// still identity-matching. Exit within the grace is success (Case A); the
// escalation decision is re-based on a fresh probe, never the earlier check.
func (s *Supervisor) killOrphanUnix(ctx context.Context, inst *domain.LaunchInstance, killer platform.ProcessKiller) (KillResult, error) {
	sigErr := killer.SignalGraceful(inst.PID)
	switch {
	case sigErr == nil:
		gone, confirmErr := s.pollGone(ctx, inst.PID, orphanKillGrace)
		if confirmErr != nil {
			// Liveness cannot be established during the grace window: no
			// exit claim, no escalation (Case C).
			return s.refuseKill(inst, KillReasonUnconfirmed, ErrKillOutcomeUnconfirmed)
		}
		if gone {
			// Case A: graceful signal delivered, exit confirmed.
			return s.finishKill(inst, "killed-by-user", "killed", KillResult{Outcome: KillOutcomeTerminated, Reason: KillReasonSIGTERM})
		}
		// Still alive at grace end: re-check before escalating (D2).
		alive, err := s.prober.IsProcessAlive(inst.PID)
		if err != nil {
			return s.refuseKill(inst, KillReasonUnconfirmed, ErrKillOutcomeUnconfirmed)
		}
		if !alive {
			// Exited after SIGTERM, before escalation: attribute to the
			// delivered graceful signal (Case A).
			return s.finishKill(inst, "killed-by-user", "killed", KillResult{Outcome: KillOutcomeTerminated, Reason: KillReasonSIGTERM})
		}
		identity, err := s.prober.GetProcessIdentity(inst.PID)
		if err != nil || !verifyIdentityForKill(inst, identity) {
			return s.refuseKill(inst, KillReasonIdentityUnconfirmed, ErrKillIdentityUnconfirmed)
		}
		return s.forceAndConfirm(ctx, inst, killer)
	case errors.Is(sigErr, platform.ErrKillAlreadyGone):
		// The process exited before the signal was delivered: nothing was
		// killed. Reconcile honestly as pid-gone (Case E).
		return s.finishKill(inst, "pid-gone", "", KillResult{Outcome: KillOutcomeReconciled, Reason: KillReasonPIDGone})
	case errors.Is(sigErr, platform.ErrKillAccessDenied):
		// Case D: privilege denied.
		return s.refuseKill(inst, KillReasonInsufficientPrivilege, ErrKillInsufficientPrivilege)
	default:
		// Unexpected signal error: establish process state before deciding.
		alive, perr := s.prober.IsProcessAlive(inst.PID)
		if perr != nil || alive {
			// Case C: exit not confirmable (probe error or still visible).
			return s.refuseKill(inst, KillReasonUnconfirmed, ErrKillOutcomeUnconfirmed)
		}
		return s.finishKill(inst, "pid-gone", "", KillResult{Outcome: KillOutcomeReconciled, Reason: KillReasonPIDGone})
	}
}

// forceAndConfirm sends SIGKILL (the caller already re-verified identity)
// and confirms exit within a bounded window. Case B on confirmed exit,
// Case C when the process remains visible (no false success).
func (s *Supervisor) forceAndConfirm(ctx context.Context, inst *domain.LaunchInstance, killer platform.ProcessKiller) (KillResult, error) {
	forceErr := killer.SignalForce(inst.PID)
	switch {
	case forceErr == nil:
		gone, confirmErr := s.pollGone(ctx, inst.PID, orphanKillGrace)
		if confirmErr == nil && gone {
			// Case B: forced signal delivered, exit confirmed.
			return s.finishKill(inst, "killed-by-user", "killed", KillResult{Outcome: KillOutcomeTerminated, Reason: KillReasonSIGKILL})
		}
		return s.refuseKill(inst, KillReasonUnconfirmed, ErrKillOutcomeUnconfirmed)
	case errors.Is(forceErr, platform.ErrKillAlreadyGone):
		// The process exited after SIGTERM (escalation was a no-op):
		// attribute to the delivered graceful signal (Case A).
		return s.finishKill(inst, "killed-by-user", "killed", KillResult{Outcome: KillOutcomeTerminated, Reason: KillReasonSIGTERM})
	case errors.Is(forceErr, platform.ErrKillAccessDenied):
		return s.refuseKill(inst, KillReasonInsufficientPrivilege, ErrKillInsufficientPrivilege)
	default:
		alive, perr := s.prober.IsProcessAlive(inst.PID)
		if perr != nil || alive {
			return s.refuseKill(inst, KillReasonUnconfirmed, ErrKillOutcomeUnconfirmed)
		}
		// Confirmed gone after a failed force syscall: the delivered SIGTERM
		// preceded the exit (Case A).
		return s.finishKill(inst, "killed-by-user", "killed", KillResult{Outcome: KillOutcomeTerminated, Reason: KillReasonSIGTERM})
	}
}

// killOrphanWindows implements the ADR 008 D3 sequence: one strict
// re-verification (done by the caller) then immediate TerminateProcess
// (no graceful phase). Query and terminate rights are independent: a
// verification success does not imply the termination will succeed.
func (s *Supervisor) killOrphanWindows(ctx context.Context, inst *domain.LaunchInstance, killer platform.ProcessKiller) (KillResult, error) {
	termErr := killer.SignalForce(inst.PID)
	switch {
	case termErr == nil:
		gone, confirmErr := s.pollGone(ctx, inst.PID, orphanKillGrace)
		if confirmErr == nil && gone {
			// Case B: termination confirmed.
			return s.finishKill(inst, "killed-by-user", "killed", KillResult{Outcome: KillOutcomeTerminated, Reason: KillReasonTerminateProcess})
		}
		return s.refuseKill(inst, KillReasonUnconfirmed, ErrKillOutcomeUnconfirmed)
	case errors.Is(termErr, platform.ErrKillAlreadyGone):
		// The process exited between verification and the syscall: nothing
		// was killed (Case E).
		return s.finishKill(inst, "pid-gone", "", KillResult{Outcome: KillOutcomeReconciled, Reason: KillReasonPIDGone})
	case errors.Is(termErr, platform.ErrKillAccessDenied):
		return s.refuseKill(inst, KillReasonInsufficientPrivilege, ErrKillInsufficientPrivilege)
	default:
		alive, perr := s.prober.IsProcessAlive(inst.PID)
		if perr != nil || alive {
			return s.refuseKill(inst, KillReasonUnconfirmed, ErrKillOutcomeUnconfirmed)
		}
		return s.finishKill(inst, "pid-gone", "", KillResult{Outcome: KillOutcomeReconciled, Reason: KillReasonPIDGone})
	}
}

// pollGone polls liveness until the PID is gone, the window elapses, or ctx
// is cancelled. It returns (gone, err); a probe error is returned as err
// with gone=false (the caller must not claim exit on an unconfirmable state).
func (s *Supervisor) pollGone(ctx context.Context, pid int, window time.Duration) (bool, error) {
	deadline := time.Now().Add(window)
	for {
		alive, err := s.prober.IsProcessAlive(pid)
		if err != nil {
			return false, err
		}
		if !alive {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(orphanKillPoll):
		}
	}
}

// finishKill persists the terminal reconciliation (Cases A/B/E) and logs it.
func (s *Supervisor) finishKill(inst *domain.LaunchInstance, recoveryReason, exitClass string, result KillResult) (KillResult, error) {
	inst.UpdateState(domain.InstanceStateStale)
	inst.RecoveryReason = recoveryReason
	if exitClass != "" {
		inst.ExitClass = domain.InstanceExitClass(exitClass)
	}
	if err := s.store.Update(domain.ToStorageEntry(inst)); err != nil {
		return KillResult{}, fmt.Errorf("persist kill reconciliation %s: %w", string(inst.ID), err)
	}
	slog.Info("orphan kill reconciled",
		"instance_id", string(inst.ID),
		"outcome", string(result.Outcome),
		"reason", result.Reason,
	)
	return result, nil
}

// refuseKill persists the bounded refusal diagnostic on the orphan record
// (state preserved, retriable) and returns the refusal (Cases C/D/F).
func (s *Supervisor) refuseKill(inst *domain.LaunchInstance, reason string, sentinel error) (KillResult, error) {
	inst.UpdateError(reason, "")
	if err := s.store.Update(domain.ToStorageEntry(inst)); err != nil {
		return KillResult{}, fmt.Errorf("persist kill refusal %s: %w", string(inst.ID), err)
	}
	slog.Warn("orphan kill refused",
		"instance_id", string(inst.ID),
		"reason", reason,
	)
	return KillResult{Outcome: KillOutcomeRefused, Reason: reason}, sentinel
}

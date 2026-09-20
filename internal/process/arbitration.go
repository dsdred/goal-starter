package process

import (
	"fmt"
	"sync/atomic"

	"github.com/dsdred/goal/internal/domain"
)

// === ADR 017 corrective slice D1: unified launch/restart arbitration =========
//
// BF-01: before D1 a restart could reach Manager.Start without ever passing
// the model-level ADR 017 arbitration boundary, so a restart of a historical
// terminal instance could produce a second live generation of a model whose
// ownership relationship is incompatible with the already-live one.
//
// D1 closes that by giving the boundary a single sealed shape: arbitration
// consumes a claimant (one of a closed set of modes), performs the conflict /
// orphan / shutdown checks itself, publishes the winning operation, and mints a
// single-use, generation-bound spawn claim. Spawn authorization is consumed at
// the RB-015b commit-C boundary, so no production path can omit, manufacture,
// replay or reuse it.
//
// Lock contract for this file (actual nesting edges, not a total order):
//   - arbLock is acquired only by Supervisor.arbitrate and is released before
//     it returns, so nothing below it can reach ic.lifecycleMu (R3) and no
//     caller-supplied code runs while it is held (R1): the arbitrator performs
//     the publication itself (R2).
//   - Under arbLock the boundary may acquire launchMu, s.mu and ic.mu — the
//     same edges the pre-D1 admission path already had.
//   - ic.lifecycleMu is never acquired here; the restart lifecycle work starts
//     only after arbLock has been released (R4).

// claimantMode is the closed set of publication actions the arbitration
// boundary knows how to perform. Its values are only produced by
// newAdmissionClaimant and newRestartClaimant, so the boundary dispatches the
// publication action without executing any caller-supplied code.
type claimantMode uint8

const (
	// claimNewAdmission publishes a brand-new pending instance in the registry
	// (the ADR 017 linearization point) and registers its RB-015b pre-spawn
	// token.
	claimNewAdmission claimantMode = iota
	// claimRestartExisting publishes a controller-local restart reservation on
	// the existing controller of the exact target instance.
	claimRestartExisting
)

// claimant is the sealed input of the arbitration boundary. All fields are
// unexported and the struct is only built by the two constructors below.
type claimant struct {
	mode       claimantMode
	modelID    string
	instanceID domain.InstanceID
	owner      domain.LaunchOwner
	ctrl       *InstanceController
	// inst is the pending instance materialized by a new admission; nil for a
	// restart, which never creates an instance record.
	inst *domain.LaunchInstance
}

// newAdmissionClaimant builds the claimant for a launch that will create a new
// instance record (manual start, pipeline start, autostart).
func newAdmissionClaimant(modelID string, inst *domain.LaunchInstance, ctrl *InstanceController, owner domain.LaunchOwner) claimant {
	return claimant{
		mode:       claimNewAdmission,
		modelID:    modelID,
		instanceID: inst.ID,
		owner:      owner,
		ctrl:       ctrl,
		inst:       inst,
	}
}

// newRestartClaimant builds the claimant for a restart of an existing
// instance. The owner comes from the TARGET instance (never from the caller),
// and self-exemption is bound to the exact InstanceID held by ctrl.
func newRestartClaimant(ctrl *InstanceController, modelID string, owner domain.LaunchOwner) claimant {
	return claimant{
		mode:       claimRestartExisting,
		modelID:    modelID,
		instanceID: ctrl.instanceID,
		owner:      owner,
		ctrl:       ctrl,
	}
}

// launchOperation is the controller-local record of the one operation that
// currently owns this instance's next spawn authorization. For a restart it IS
// the restart reservation: it stays published across the old generation's stop,
// the terminal interval, and the new generation's spawn, so arbitration cannot
// admit a conflicting launch into that gap.
//
// The struct's fields are written once at publication; only the owning
// controller's active pointer (guarded by ic.mu) changes afterwards.
type launchOperation struct {
	id    uint64
	owner domain.LaunchOwner
	claim *spawnClaim
}

// spawnClaim is the opaque, pointer-only spawn authorization produced by the
// arbitration boundary. It is passed through the launch path and consumed
// exactly once at commit C, immediately before the spawn.
//
// It is deliberately NOT a value token: noCopy makes copying it a vet
// (copylocks) error, so a copy can never duplicate the authority, and every
// binding it carries (supervisor, model, owner, instance, operation) is
// validated against the controller that presents it. A claim cannot be
// reconstructed from the ordinary startCore arguments.
type spawnClaim struct {
	noCopy
	consumed   atomic.Bool
	sup        *Supervisor
	modelID    string
	owner      domain.LaunchOwner
	instanceID domain.InstanceID
	opID       uint64
	restart    bool
}

// noCopy is a zero-size marker that makes `go vet` reject any copy of the
// struct that embeds it as a field.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

// nextOperationID mints the monotonic operation/generation identity. Restart
// reuses the InstanceID, so the operation ID — not the instance ID — is what
// binds a claim to exactly one generation. It is a deterministic per-Supervisor
// counter, never random.
func (s *Supervisor) nextOperationID() uint64 {
	return s.opSeq.Add(1)
}

// arbitrate is the single ADR 017 model-level arbitration boundary used by BOTH
// new admission and restart (D1). While the per-ModelID arbLock is held it
// performs the atomic sequence: shutdown/lifecycle check, target precondition
// check (restart), same-ModelID conflict scan with exact-InstanceID
// self-exemption, the repository orphan fence, and publication of the winning
// operation with its spawn claim. arbLock is released before it returns —
// never across stop, wait, persistence, slot acquisition or spawn.
//
// Linearization point: the publication in publishClaimLocked, performed under
// launchMu while arbLock is still held —
//   - new admission: s.instances[inst.ID] = ctrl (plus the pre-spawn token),
//     unchanged from pre-D1 ADR 017;
//   - restart: ctrl.active = op, the controller-local restart reservation.
//
// After it returns, no other arbitration for the same model can admit an
// incompatible operation until the published operation releases its record.
func (s *Supervisor) arbitrate(c claimant) (*spawnClaim, error) {
	arbLock := s.arbitrationLock(c.modelID)
	arbLock.Lock()
	defer arbLock.Unlock()

	if err := s.lifecycleContext().Err(); err != nil {
		return nil, &AdmissionRejection{Reason: RejShuttingDown, ModelID: c.modelID}
	}

	if c.mode == claimRestartExisting {
		if err := checkRestartTarget(c.ctrl); err != nil {
			return nil, err
		}
	}

	if err := s.scanConflicts(c); err != nil {
		return nil, err
	}

	if err := s.orphanFence(c); err != nil {
		return nil, err
	}

	return s.publishClaimLocked(c)
}

// scanConflicts examines every registered instance of the claimant's model.
// Self-exemption is exact InstanceID equality only — never ModelID, PipelineID,
// EntryID, owner equality or pointer coincidence — so a sibling pipeline entry
// on the same model is still evaluated through compatible().
//
// A candidate is conflict-visible when its state is in flight OR when it
// publishes an active operation: the latter is what closes the BF-01 restart
// gap, because a restarting instance is terminal for most of its
// stop→spawn interval.
func (s *Supervisor) scanConflicts(c claimant) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for id, ctrl := range s.instances {
		if id == c.instanceID {
			continue
		}
		snap := ctrl.Snapshot()
		if snap.ModelID != c.modelID {
			continue
		}
		if !snap.IsInFlight() && ctrl.activeOperation() == nil {
			continue
		}
		if !compatible(c.owner, &snap) {
			return &AdmissionRejection{
				Reason:     RejInFlight,
				ModelID:    c.modelID,
				ConflictID: id,
				PipelineID: snap.PipelineID,
				EntryID:    snap.PipelineEntryID,
			}
		}
	}
	return nil
}

// orphanFence is the repository-side ADR 017 fence: an unresolved orphan record
// for the model denies the spawn, for both new admission and restart.
func (s *Supervisor) orphanFence(c claimant) error {
	if s.store == nil {
		return nil
	}
	entries, err := s.store.ListByModelID(c.modelID)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.State == string(domain.InstanceStateOrphan) {
			return &AdmissionRejection{
				Reason:     RejOrphan,
				ModelID:    c.modelID,
				ConflictID: domain.InstanceID(e.ID),
				PipelineID: e.PipelineID,
				EntryID:    e.PipelineEntryID,
			}
		}
	}
	return nil
}

// publishClaimLocked performs the mode-selected publication while arbLock is
// held. It is serialized against shutdown drain-start (D) on launchMu, so a
// claim never exists across a drain that already began.
func (s *Supervisor) publishClaimLocked(c claimant) (*spawnClaim, error) {
	opID := s.nextOperationID()
	claim := &spawnClaim{
		sup:        s,
		modelID:    c.modelID,
		owner:      c.owner,
		instanceID: c.instanceID,
		opID:       opID,
		restart:    c.mode == claimRestartExisting,
	}
	op := &launchOperation{id: opID, owner: c.owner, claim: claim}

	s.launchMu.Lock()
	if s.draining {
		s.launchMu.Unlock()
		return nil, &AdmissionRejection{Reason: RejShuttingDown, ModelID: c.modelID}
	}
	// The reservation is published before the controller becomes reachable
	// through the registry, so a scan can never observe an operation that is
	// visible but unauthorized, nor an authorized operation that is invisible.
	c.ctrl.mu.Lock()
	c.ctrl.active = op
	c.ctrl.mu.Unlock()
	if c.mode == claimNewAdmission {
		s.mu.Lock()
		s.instances[c.inst.ID] = c.ctrl
		s.preSpawnInFlight++
		c.ctrl.preSpawnToken = true
		s.mu.Unlock()
	}
	s.launchMu.Unlock()
	return claim, nil
}

// instanceOwner derives the launch owner recorded on an instance. A restart's
// ownership therefore comes from the target, not from the HTTP caller. A
// pipeline instance without entry attribution cannot be re-attributed and is
// refused (the conservative rule the application layer already applies).
func instanceOwner(snap *domain.LaunchInstance) (domain.LaunchOwner, error) {
	if snap.PipelineID == "" {
		return domain.ManualOwner, nil
	}
	if snap.PipelineEntryID == "" {
		return domain.LaunchOwner{}, fmt.Errorf("instance %s: %w: pipeline entry ownership cannot be reconstructed (legacy attribution)", string(snap.ID), ErrNotRestartable)
	}
	return domain.PipelineOwner(snap.PipelineID, snap.PipelineEntryID), nil
}

// checkRestartTarget is the D1 restart state contract, shared by arbitration and
// by the model-level preflight so the two cannot drift:
//
//	pending  refuse — the launch is still in flight
//	starting refuse — the generation is inside the ADR 016 ownership-
//	         establishment window and may still become a C/D residual
//	running  allow
//	stopping allow — without becoming a second stop owner
//	exited   allow
//	failed   allow
//	stale    refuse — terminal-but-unrecoverable attribution
//	orphan   refuse — the repository orphan fence is authoritative
func checkRestartTarget(ctrl *InstanceController) error {
	snap := ctrl.Snapshot()
	switch snap.State {
	case domain.InstanceStatePending:
		return fmt.Errorf("restart instance %s: %w: launch in flight", string(ctrl.instanceID), ErrLaunchInFlight)
	case domain.InstanceStateStarting:
		return fmt.Errorf("restart instance %s: %w: generation is establishing ADR 016 ownership", string(ctrl.instanceID), ErrLaunchInFlight)
	case domain.InstanceStateRunning, domain.InstanceStateStopping,
		domain.InstanceStateExited, domain.InstanceStateFailed:
	default:
		return fmt.Errorf("restart instance %s: %w (state %s)", string(ctrl.instanceID), ErrNotRestartable, string(snap.State))
	}
	if ctrl.activeOperation() != nil {
		return fmt.Errorf("restart instance %s: %w: restart already in flight", string(ctrl.instanceID), ErrLaunchInFlight)
	}
	return nil
}

// PreflightRestart validates a whole selected restart target set against the
// immediately-known D1 preconditions before any target is mutated. It is
// pre-flight atomicity only: it performs no reservation, no arbitration and no
// rollback, and the authoritative decision is still made per target by the
// arbitration boundary when each restart runs.
func (s *Supervisor) PreflightRestart(ids []domain.InstanceID) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, id := range ids {
		ctrl, ok := s.instances[id]
		if !ok {
			return fmt.Errorf("instance %s not found", string(id))
		}
		snap := ctrl.Snapshot()
		if _, err := instanceOwner(&snap); err != nil {
			return err
		}
		if err := checkRestartTarget(ctrl); err != nil {
			return err
		}
	}
	return nil
}

// activeOperation returns the controller's currently published operation, or
// nil. The returned record's fields are immutable after publication. The caller
// MUST NOT hold ic.mu.
func (ic *InstanceController) activeOperation() *launchOperation {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	return ic.active
}

// releaseLaunchOperation ends the operation identified by opID. Release is
// exact-operation and idempotent: a release from an operation that already lost
// or never gained ownership is a no-op, so no failure path can clear another
// operation's reservation. The caller MUST NOT hold ic.mu.
func (ic *InstanceController) releaseLaunchOperation(opID uint64) {
	ic.mu.Lock()
	if ic.active != nil && ic.active.id == opID {
		ic.active = nil
	}
	ic.mu.Unlock()
}

// revalidateOperation re-checks, after lifecycleMu has been acquired, that the
// caller's claim is still the operation that owns this controller.
func (ic *InstanceController) revalidateOperation(claim *spawnClaim) error {
	ic.mu.RLock()
	op := ic.active
	ic.mu.RUnlock()
	if op == nil || claim == nil || op.claim != claim || op.id != claim.opID {
		return fmt.Errorf("instance %s: %w: operation identity no longer held", string(ic.instanceID), ErrLaunchInFlight)
	}
	return nil
}

// consumeSpawnClaimLocked validates the spawn authorization against this
// controller and consumes it exactly once. It is the commit-C claim boundary:
// the caller MUST hold launchMu (the linearization domain of the spawn commit)
// and ic.mu.
//
// A consumed claim is never restored. If shutdown drain wins after this point,
// the claim stays spent and a retry requires fresh arbitration, a fresh
// operation identity and a fresh claim.
func (ic *InstanceController) consumeSpawnClaimLocked(claim *spawnClaim) error {
	reject := func(reason string) error {
		return fmt.Errorf("start instance %s: %w: %s", string(ic.instanceID), errSpawnClaimRejected, reason)
	}

	switch {
	case claim == nil:
		return reject("no spawn authorization presented")
	case claim.sup != ic.supervisorRef:
		return reject("claim was issued by a different supervisor")
	case claim.modelID != ic.modelID:
		return reject("claim model does not match the instance")
	case claim.instanceID != ic.instanceID:
		return reject("claim instance does not match the controller")
	}

	op := ic.active
	switch {
	case op == nil:
		return reject("no operation currently owns this controller")
	case op.claim != claim:
		return reject("claim belongs to a different generation of this instance")
	case op.id != claim.opID:
		return reject("claim operation identity mismatch")
	case claim.owner != op.owner:
		return reject("claim owner does not match the owning operation")
	}

	if !claim.consumed.CompareAndSwap(false, true) {
		return reject("claim already consumed")
	}
	return nil
}

// errSpawnClaimRejected marks an internal authorization violation: a spawn was
// requested without a valid, unconsumed, generation-bound claim bound to this
// exact controller. Reaching it in production means a caller bypassed the
// arbitration boundary, which is the defect class BF-01 describes.
var errSpawnClaimRejected = fmt.Errorf("spawn authorization rejected")

// mintTestSpawnClaim publishes a spawn claim WITHOUT arbitration for the
// package-internal transitional launch helpers (Supervisor.start and the ADR
// 016 lifecycle tests), which exercise the start lifecycle itself rather than
// ADR 017 admission.
//
// TEST SUPPORT ONLY. It is the single place that can create a valid claim
// outside Supervisor.arbitrate, it is unexported, and production code MUST NOT
// call it: every production spawn must carry a claim produced by the
// arbitration boundary.
func (s *Supervisor) mintTestSpawnClaim(ctrl *InstanceController, owner domain.LaunchOwner) *spawnClaim {
	claim := &spawnClaim{
		sup:        s,
		modelID:    ctrl.modelID,
		owner:      owner,
		instanceID: ctrl.instanceID,
		opID:       s.nextOperationID(),
	}
	op := &launchOperation{id: claim.opID, owner: owner, claim: claim}
	ctrl.mu.Lock()
	ctrl.active = op
	ctrl.mu.Unlock()
	return claim
}

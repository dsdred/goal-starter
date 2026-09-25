# ADR 017: Unified Owner-Aware Launch Arbitration Boundary

**Status:** Accepted — implemented and published 2026-09-19 (Slices A+B+C); RB-002 was declared resolved on that basis. **The original closure was later found incomplete** by the focused remediation review: terminal-instance restart could reach the spawn path without passing this ADR's arbitration boundary. Corrective slice **D1** closed that gap (see [Closure Chronology](#closure-chronology-corrective-slices-d0-d62)). Accepted / implemented / published — with the restart contract now resting on D1 rather than on Slice C alone.
**Date:** 2026-09-18
**Remediates:** RB-002 (process/state divergence and false admission across launch initiators)
**Depends on:** ADR 016 (Durable Lifecycle Ownership — defines "what a successful Start means"), ADR 013 (Pipeline repeatable model entries — defines the compatibility matrix), ADR 005 (Recovery — defines orphan semantics)
**Related:** RB-004 (legacy runtime start endpoint), RB-015b (shutdown admission for already-admitted work)

## Context

Every model/rime launch in GoAl funnels through `Supervisor.Start` (`internal/process/supervisor.go:292`), which performs **no model-level admission**. It mints a new `InstanceID`, registers in `s.instances`, acquires a slot, persists pending, spawns. All duplicate-prevention is caller responsibility.

Three mutually independent lock keyings exist at the application layer:
- Per-`ModelID` mutex in `InstanceService.StartModel` (the "C3 guard", `instance_service.go:19-41`)
- Per-`PipelineID` mutex in `PipelineService` (`pipeline_service.go:87-88`)
- Per-`InstanceID` `lifecycleMu` in `InstanceController` (supervisor-level, per-instance)

These do not compose. Confirmed manifestations (re-verified against HEAD `2f92281` after ADR 016):

| Scenario | Defect |
|----------|--------|
| Recovered orphan × manual Start | **CONFIRMED** — C3 guard reads in-memory `s.instances` only; orphan is absent from `s.instances` and is not `IsInFlight()`. No server-side guard. UI hides Start button (client-only). API has no guard. Double-launch possible. |
| Manual Start × pipeline Start (same ModelID) | **CONFIRMED** — different mutex keyings (per-ModelID vs per-PipelineID). TOCTOU window between each path's check and `supervisor.Start`. |
| Pipeline A × Pipeline B (same ModelID) | **CONFIRMED** — different per-pipeline locks. TOCTOU. The model-owner rule (ADR 013 D3) is the intended guard but is non-atomic across pipelines. |
| Manual × Manual (same ModelID) | **PROTECTED** by C3 per-model mutex. |
| ADR 016 C/D residual × new Start | **PROTECTED** — C/D instance stays in `s.instances` as `starting` (in-flight); C3 guard and pipeline gate both see it. |
| Model autostart × anything | **PROTECTED in practice** by temporal ordering (single-goroutine startup before HTTP server). Not an architectural guarantee. |

ADR 016 explicitly defers this to RB-002 (lines 452-468): "Any arbitration that relies solely on `supervisor.List()` will be blind to orphans. The RB-002 design MUST account for this."

## Scope

**In scope:**
- One authoritative, non-bypassable launch admission boundary used by every production launch initiator.
- Eliminate the confirmed manifestations above.
- Preserve ADR 013 compatibility semantics (within-pipeline repeated ModelID entries).
- Preserve ADR 016 durable lifecycle ownership invariants.
- Define the orphan admission contract.
- Define the shutdown admission rejection capability.
- Migration plan (Slices A/B/C) with transitional invariants.

**Non-goals (explicitly out of scope):**
- RB-015b (shutdown admission for already-admitted, not-yet-spawned work) — separate task.
- RB-004 (legacy `/api/v1/runtimes/{id}/action/start` disposition) — separate task.
- ADR 015 (Readiness) — FROZEN.
- Multi-instance/replica semantics — future, not designed here.
- Supervisor decomposition — not a rewrite.

## Design

### Arbitration Partition vs Full Claim Identity

**Arbitration partition key:** `ModelID`

This is the bucket that partitions the concurrency domain. It answers "which models are in the same conflict domain?" All in-flight instances of the same ModelID are in the same partition, with one formally defined exception (within-pipeline distinct entries).

**Full claim identity (admission):**

| Field | Type | Source |
|-------|------|--------|
| `ModelID` | string | Request (partition key) |
| `OwnerKind` | `manual \| pipeline` | Request |
| `PipelineID` | string (empty for manual) | Request |
| `PipelineEntryID` | string (empty for manual) | Request |

**After claim materialization**, the claim is identified by `InstanceID` (globally unique, `modelID-unixnano-atomicSeq`) with the owner fields attached. The claim is NOT a separate token — it IS the pending `LaunchInstance` in `s.instances`.

### Compatibility Matrix (from ADR 013 D3, owner-agreed 2026-09-06)

An incoming claim `C_new` is **admissible** against an existing in-flight claim `C_existing` (same ModelID) if and only if:

| C_existing | C_new | Result | ADR 013 evidence |
|------------|-------|--------|-----------------|
| manual(M) | manual(M) | CONFLICT | One in-flight per model |
| manual(M) | pipeline(P,E,M) | CONFLICT | D3: "If ANY active instance of M is owned by another pipeline or is manual... yields already-running" |
| pipeline(P1,E1,M) | pipeline(P2,E2,M), P1≠P2 | CONFLICT | D3: "owned by another pipeline"; D4: "the earlier pipeline owns the model; the later yields already-running" |
| pipeline(P,E1,M) | pipeline(P,E2,M), E1≠E2 | **ALLOW** | D3: "Within-pipeline independence"; "first start launches two independent instances" |
| pipeline(P,E,M) | pipeline(P,E,M) same entry | CONFLICT | D3: "Per-entry idempotency: no active instance of M is attributed to E" |
| pipeline(P,E,M) | manual(M) | CONFLICT | D3: "Manual instances always take priority over pipeline entries" |
| any(M) | any(M'), M≠M' | ALLOW | Different partition |
| orphan(M) in repo | any launch(M) | CONFLICT | ADR 010: "a model whose instance is orphan gets no Start action"; D3 orphan gate |
| ADR 016 residual C/D (M) | any incompatible launch(M) | CONFLICT | State `starting` = `IsInFlight()`; visible in `s.instances` |

### Claim == Pending Instance

There is no separate admission token. The pending `*domain.LaunchInstance` inserted into `s.instances` IS the materialized claim.

**Claim lifecycle state machine:**

```
UNMATERIALIZED → CLAIMED → SLOT_OWNED → SPAWNED → LIFECYCLE_OWNED → TERMINAL/RELEASED
```

| Phase | Transition | Release authority |
|-------|-----------|-------------------|
| UNMATERIALIZED → CLAIMED | `s.instances[inst.ID] = ctrl` under `s.arbLocks[modelID]` + `s.mu` | — (claim materialized) |
| CLAIMED → SLOT_OWNED | `acquireSlot` succeeds + `store.Create(pending)` | — |
| CLAIMED → RELEASED | `acquireSlot` fails: `delete(s.instances)` (no slot to release) | Calling goroutine |
| SLOT_OWNED → RELEASED | `store.Create` fails or `startCore` pre-spawn failure: `releaseSlot` + `delete(s.instances)` | Calling goroutine |
| SLOT_OWNED → SPAWNED | `manager.Start` returns success (process EXISTS) | **IRREVERSIBLE** — ADR 016 boundary |
| SPAWNED → LIFECYCLE_OWNED | ADR 016 running+PID persist succeeds; `wait()` goroutine started | Ownership transferred to `wait()` |
| SPAWNED → RELEASED (Outcome A) | Persist fails; kill confirmed dead; `confirmExit` succeeds; `releaseSlot` + `delete(s.instances)` | `startCore` (after confirmed termination) |
| SPAWNED → RELEASED (Outcome B) | Persist fails; kill confirmed dead; failed-persist also fails; same as A for slot/instances | `startCore` (after confirmed termination) |
| SPAWNED → LIFECYCLE_OWNED (Outcome C/D) | Persist fails; kill unconfirmed/refused; state reverted to `starting`; `wait()` started | `wait()` (on eventual exit) |
| LIFECYCLE_OWNED → TERMINAL (slot RELEASED) | Process exits; `wait()` persists the terminal state, then `releaseSlot` + `complete` | `wait()` |

**Terminal is not deregistered.** Production `wait()` performs terminal finalization and releases the
slot; the terminal controller then **remains registered** in `s.instances`, which is what keeps the
process-scoped restart capability of its `InstanceID` alive. The only production path that unregisters a
terminal controller is explicit cleanup's registry reconciliation (`POST /api/v1/instances/cleanup` →
`Supervisor.ForgetCleanedControllers`) — see [Cleanup and Registry Reconciliation Invariant](#cleanup-and-registry-reconciliation-invariant).
`Supervisor.RemoveTerminal` exists but has **zero production callers**; it is not the eviction mechanism,
and its removal-or-adoption decision is tracked as debt in [BACKLOG.md](../../BACKLOG.md). The retained
terminal metadata is hygiene-with-cost, **not** an established process/goroutine/slot leak.

**Hard invariant:** Once `manager.Start` can have created an OS process (SPAWNED phase), the claim is NEVER deleted, the slot is NEVER released, and the ModelID is NEVER made admissible again — until termination is confirmed (A/B) or `wait()` confirms exit (C/D/normal).

### Authoritative Operation: `Supervisor.AdmitAndStart`

**One inseparable operation. No `TryAdmit` + `Start` split.**

```go
func (s *Supervisor) AdmitAndStart(
    ctx context.Context,
    model *domain.Model,
    runtime *domain.Runtime,
    owner domain.LaunchOwner,
    customArgs []string,
    customEnv map[string]string,
) (*domain.LaunchInstance, error)
```

Where:
```go
type LaunchOwner struct {
    Kind            OwnerKind // OwnerManual | OwnerPipeline
    PipelineID      string    // empty for manual
    PipelineEntryID string    // empty for manual
}
```

**Contract:**
1. Acquires the per-ModelID arbitration lock.
2. Checks in-memory `s.instances` for conflicting in-flight instances (using the compatibility matrix above).
3. Checks repository for orphans of the ModelID.
4. If no conflict: creates pending Instance, inserts into `s.instances` (linearization point), releases arbitration lock.
5. Proceeds with the existing `Start` body: slot acquire → persist pending → `startCore` → spawn → ADR 016.
6. Returns the started instance (success) or a structured error.

**On admission rejection**, the error wraps `*AdmissionRejection`:
```go
type AdmissionRejection struct {
    Reason     RejectionReason // RejInFlight | RejOrphan | RejShuttingDown
    ModelID    string
    ConflictID domain.InstanceID
    PipelineID string // owner of the conflicting instance
    EntryID    string
}
```

Pipeline callers use `errors.As(err, &rejection)` to map to per-entry outcomes (`already-running`, `orphan-skipped`).

### Linearization Point

The linearization point of a successful admission is: **the write `s.instances[inst.ID] = ctrl` under `s.mu.Lock()`, while holding `s.arbLocks[modelID]`.**

Precise sequence:
```
s.arbLocks[modelID].Lock()
  s.mu.RLock()
    conflict scan of s.instances (compatibility matrix)
  s.mu.RUnlock()
  store.ListByModelID(modelID)  // orphan check (takes repo r.mu internally)
  if conflict → reject, return
  s.mu.Lock()
    s.instances[inst.ID] = ctrl   // ← LINEARIZATION POINT
  s.mu.Unlock()
s.arbLocks[modelID].Unlock()
// proceed: acquireSlot → persist → startCore → ADR 016
```

After the linearization point, any concurrent `AdmitAndStart` for the same ModelID will observe the new instance via the in-memory scan and apply the compatibility matrix.

### Lock Ownership and Ordering

**New lock on `Supervisor`:**
```go
arbMu    sync.Mutex            // guards arbLocks map (brief)
arbLocks map[string]*sync.Mutex // per-ModelID arbitration lock
```

- **Creation:** Lazy on first `AdmitAndStart` for a ModelID. Guarded by `arbMu`.
- **Removal:** Never. Bounded by the number of distinct ModelIDs (user-created, typically <100). Same tradeoff as the existing C3 `startLocks` pattern.
- **`arbMu`** is held only for the map lookup/insert (nanoseconds). Never held while the per-model lock is held.

**Complete lock order (outermost to innermost):**
```
s.arbMu (brief, map access only)
→ s.arbLocks[modelID] (admission critical section)
  → r.mu (repository RLock, for orphan ListByModelID)
  → s.mu (supervisor map, for conflict scan + insert)
→ [arbLocks released]
→ s.semaphore (slot acquire, blocking; no other lock held)
→ ic.lifecycleMu (per-instance, in startCore)
  → ic.mu (per-instance, in startCore)
    → r.mu (repository, for persist)
```

**No existing code acquires `s.mu` then `s.arbLocks[modelID]`.** Verified: all `s.mu` usages in `Supervisor` are for map operations only; none are followed by an arbitration lock. `DismissOrphan`/`KillOrphan` acquire `r.mu` but never `s.mu` or `arbLocks`. No lock inversion is possible.

**`s.mu` is NOT replaced by `arbLocks`.** `s.mu` continues to protect the map for all other accessors (`List`, `Status`, `Stop`, `RemoveTerminal`, `Recover`). The arbitration lock is an ADDITIONAL higher-level lock that serializes the check+claim sequence for the same ModelID without blocking cross-model operations.

### Orphan Resolution Contract

An unresolved orphan in the repository blocks all launches of that ModelID.

- **Admission sees orphan** → reject with `RejOrphan`.
- **Dismiss** (`DismissOrphan`, supervisor.go:1142) transitions orphan → stale. After the durable `store.Update` completes, admission sees stale (not orphan) and allows.
- **Kill** (supervisor_kill.go:92) transitions orphan → stale only after confirmed termination (`finishKill`). Until the durable `store.Update` completes, the repository still says `orphan` and admission rejects. If kill is refused, state stays `orphan` and admission continues to reject.
- **Concurrency:** The repository's `RWMutex` serializes the orphan state read (admission) and write (Dismiss/Kill). The one-way state transition (orphan → stale, never reversed) guarantees:
  - If admission sees `orphan` → reject (safe, conservative).
  - If admission sees `stale` → allow (safe, operator resolved it).
  - No ordering produces "live orphan process + newly admitted process" due to cross-surface TOCTOU.
- **No additional synchronization mechanism is required.**

### ADR 016 Interaction

- **S1/F1 preserved:** `AdmitAndStart` delegates to the same `startCore`. The ADR 016 persist gate (running+PID must be durable for success) is untouched.
- **A/B:** Release occurs only after `confirmExit` (confirmed termination). `AdmitAndStart` returns `(nil, error)`.
- **C/D:** Instance stays in `s.instances` (state `starting`, in-flight). Slot held. `wait()` owns release. `AdmitAndStart` returns `(instance, error)`.
- **Normal success:** `wait()` owns the lifecycle. `AdmitAndStart` returns `(instance, nil)`.
- **Admission rejection** never reaches `startCore`. ADR 016 code is not involved.

### Restart Interaction

**Published current behavior (corrected by corrective slice D1).** The earlier version of this section
asserted that restart operates on an already materialized in-flight instance and therefore needs no
second admission path. That premise was **false**: the restart target is frequently a **terminal**
instance, which is not `IsInFlight()` at all — so an in-flight-only argument left the terminal restart
path outside the arbitration boundary.

What holds now:

- **A terminal historical restart is not necessarily `IsInFlight()`.** `exited` and `failed`
  instances are restartable while their controller is still registered in the current GoAl process
  **and** its history record has not been explicitly cleaned (see `docs/API.md`, "Historical terminal
  restart is process-scoped"); neither is visible to an in-flight-only conflict scan.
- **`stale` is deliberately NOT restartable, and `pending`/`starting` are refused as in-flight.**
  `checkRestartTarget` (`arbitration.go`) allows `running`, `stopping`, `exited` and `failed`; it
  refuses `pending`/`starting` with `ErrLaunchInFlight` (`409` `launch_in_flight`) and everything else
  — `stale` and `orphan` — with `ErrNotRestartable` (`409` `not_restartable`). The distinction is
  attribution, not terminality: `exited`/`failed` record a **confirmed** termination, while `stale`
  means process termination or process identity could **not** be reliably confirmed (recovery
  `pid-not-found`/`identity-unconfirmed`, or an orphan reconciliation), so relaunching that
  generation's identity could stack a second process next to a possibly-live, unclaimed one.
  In ordinary operation a `stale` `InstanceID` never even reaches that check: the `stale`-producing
  paths (`Recover`, `DismissOrphan`, `finishKill`) write repository records only and do not
  register or reconstruct a controller, so the lookup fails earlier as `404` `instance_not_found`.
  `409` `not_restartable` for `stale` is reachable only if a `stale` controller exists in this
  process; the code makes no attempt to manufacture that answer.
- **`RestartWithLaunch` uses the same arbitration machinery as a new launch**, through the restart
  claimant path: `Supervisor.RestartWithLaunch` → `restart` → `Supervisor.arbitrate` with a restart-mode
  claimant, sharing `scanConflicts`, the orphan fence and the compatibility matrix with
  `AdmitAndStart`. There is one arbitration boundary, entered from two claim modes (`new admission` and
  `restart existing`) — not two admission systems.
- **The restart reservation stays visible across the relevant interval.** Restart publishes an active
  operation on the controller before the stop begins, so it remains conflict-visible through
  stop → terminal → spawn. An in-flight check alone does not close that interval; the published
  operation does, which is why the scan tests `IsInFlight()` **or** an active operation.
- **Same-model conflicts remain subject to the ADR 013 compatibility matrix** above. In particular,
  within one pipeline, distinct `PipelineEntryID` entries for the same ModelID stay mutually compatible
  (ADR 013 D3) — a restart of one entry does not conflict with its sibling entries.
- The per-instance `lifecycleMu` and the pending gate still protect **same-instance** concurrency; they
  are complements to arbitration, never a substitute for it.

Consequence for this ADR's non-bypassability claim: it is established by Slice C **plus** D1, not by
Slice C alone — see [Migration Slices](#migration-slices) and the chronology below.

### Shutdown / RB-015b Boundary

- **RB-002 (this ADR):** `AdmitAndStart` checks `s.lifecycleCtx.Err()` inside the arbitration critical section. If cancelled → reject with `RejShuttingDown`. No new admission after shutdown begins.
- **RB-015b (separate task, subsequently resolved):** Already-admitted work blocked on `acquireSlot` when shutdown begins. Originally deferred by this ADR; resolved by a dedicated shutdown drain correction (lifecycle-aware pre-spawn abort + `launchMu`-linearized admission/commit/drain) ensuring no admitted PRE-SPAWN launch spawns after a successful `Shutdown`. Implementation: `f13bae8`; CI 35467099833 (7/7 PASS).

### RB-004 Boundary

The legacy `/api/v1/runtimes/{id}/action/start` was retired (RB-004): it now returns `410 Gone` before any instance lookup or launch, directing callers to the canonical `POST /api/v1/models/{id}/start`. It no longer routes through `InstanceService.StartModel`/`AdmitAndStart` (a runtime is a launch template with no unambiguous ModelID). The `stop`/`restart` actions are unchanged.

### Cleanup and Registry Reconciliation Invariant

Explicit cleanup is ordered repository-first, and only the second half arrived with corrective slice D3:

- `InstanceService.DeleteTerminalInstances` removes the matching terminal records from the repository;
- **only after that succeeds**, `Supervisor.ForgetCleanedControllers` reconciles the registry — a safe
  terminal controller whose `InstanceID` no longer has a durable record is unregistered (identity-checked
  delete behind the `launchMu` ownership fence; registry-only, never a repository write).

**Why whole-set reconciliation is sound — a CURRENT call-graph invariant, not an eternal architectural
law.** `DeleteTerminalInstances` is currently the only production path that removes a `LaunchInstance`
record while the Supervisor is alive (the per-ID deleters `DeleteInstance` / `Delete` /
`DeleteLaunchInstance` have zero production callers), and production admission-failure paths never leave
a safely-terminal registered controller whose durable record was never created. So at the reconciliation
point, "a safe terminal controller without a durable record" is currently attributable to exactly one
cause: explicit cleanup. **If another production `LaunchInstance`-record deletion path is introduced,
this assumption must be re-reviewed** — the future-change trigger is tracked in
[BACKLOG.md](../../BACKLOG.md).

Observable consequences: a cleaned `InstanceID` is no longer Supervisor-addressable (status, stop and the
restart preflight return the documented unknown-instance `404`), and that `InstanceID`'s historical
process-scoped restart capability is removed with it. Nothing is evicted automatically — terminal
controllers of instances that were not cleaned stay registered until a cleanup selects them or the GoAl
process ends.

### Source-of-Truth Table

| Fact | Authoritative Source |
|------|---------------------|
| Currently controlled generation (pending/starting/running/stopping) | `s.instances` (in-memory) |
| Recovered orphan (process alive, no controller) | Repository |
| Reservation/pending (between admit and spawn) | `s.instances` (pending state, in-memory) |
| ADR 016 persistence failure | In-memory for operational; repository for recovery |
| Terminal persist failure | In-memory terminal for operational; repository (stale `running`) for recovery |
| ADR 016 C/D residual | `s.instances` (`starting` = in-flight) |

Admission checks: (1) `s.instances` for `IsInFlight()` instances of the ModelID, (2) repository for `orphan` state. Since D1 the in-memory check is **not** limited to `IsInFlight()`: a controller with a published active operation (a restart in progress against a terminal instance) is also conflict-visible, because a terminal restart target is by definition not in flight — see [Restart Interaction](#restart-interaction).

### Migration Slices

| Slice | Scope | Invariant established |
|-------|-------|----------------------|
| **A** | `AdmitAndStart` on Supervisor + `arbLocks` + `LaunchOwner` + manual Start migration (remove C3 mutex) + orphan rejection + structured rejection + concurrency tests | Manual Start paths are non-bypassable. Orphan blocks manual Start. C3 mutex removed. |
| **B** | Pipeline `startEntry` migrated to `AdmitAndStart` with owner; ADR 013 compatibility matrix enforced; per-pipeline mutex retained for ordering; structured outcomes preserved | Pipeline paths are non-bypassable. Cross-pipeline TOCTOU eliminated. Within-pipeline independence preserved. |
| **C** | Model autostart migrated to `AdmitAndStart`; old exported `Supervisor.Start` removed or made unexported; final production caller audit | Claimed at the time: **Global non-bypassability achieved.** Zero production callers of the old `Supervisor.Start`. RB-002 globally implemented. **Later falsified in part** — see the annotation below. |

**Transitional invariant:** After Slice A, manual paths are protected but pipeline/autostart still use the old path. After Slice B, pipeline is protected but autostart still uses the old path. Only after Slice C + final caller audit is RB-002 globally closed.

> **Annotation on Slice C's closure claim (kept, not deleted).** The Slice C caller audit covered the
> launch initiators identified at the time — the callers of the old exported `Supervisor.Start`. The
> terminal-restart path was **not** among them: it reaches the spawn path through
> `Supervisor.RestartWithLaunch` → `restart` → `startCore`, never through `Supervisor.Start`, so a
> "zero callers of `Supervisor.Start`" audit could not see it. The statement "Global non-bypassability
> achieved / RB-002 globally implemented" was therefore **premature as written**, and the corrective
> slice **D1** — which routed the restart claimant through `Supervisor.arbitrate` — is what established
> the current non-bypassable restart contract. RB-002 is resolved by A+B+C **plus D1**.

### Final Non-Bypassability Acceptance Criterion

As originally written this criterion was evaluated after Slice C and passed while the restart bypass was
still open — which is the drift D1 corrected. Restated against published behavior:

1. No production path reaches the OS spawn except through the arbitration boundary. The production routes
   into `InstanceController.startCore` are exactly two, and both call `Supervisor.arbitrate` first:
   `AdmitAndStart` → `startPostAdmit` → `startWithReservation` → `startCore`, and
   `restart` → `restartWithRefresh` → `startCore`. The only remaining caller of `startPostAdmit` is the
   package-private test seam `Supervisor.start`, which mints a test-only spawn claim
   (`mintTestSpawnClaim`) and has **zero production callers** — every caller of it is a `_test.go` file.
2. The old exported method is unexported: exported `Supervisor.Start` does not exist in the repository.
   The surviving package-private seam is named exactly `start`; this criterion previously named a
   different identifier that the implementation never used.
3. The production OS-spawn initiators are enumerable, and each is arbitration-backed:
   - manual model start — `POST /api/v1/models/{id}/start` and `POST /api/v1/instances/start` →
     `InstanceService.StartModel` → `AdmitAndStart` (manual owner);
   - startup model autostart — `cmd/goal` → `AdmitAndStart` (manual owner);
   - pipeline entry start — `PipelineService.startEntry`, used by pipeline start/restart → `AdmitAndStart`
     (pipeline owner, per-entry identity);
   - instance restart — `POST /api/v1/instances/{id}/restart` and the model restart action →
     `InstanceService.RestartInstance` → `Supervisor.RestartWithLaunch` → `restart` → `arbitrate`
     (restart claimant). **This is the initiator Slice C missed and D1 added.**
4. Race detector passes (Linux CI job).

### Concurrency / Regression Test Inventory

Reconciled by D6.1: the earlier version of this table listed identifiers that were never shipped
(`TestAdmitAndStart_SameModel_Concurrent`, `…_Orphan_AfterKill`, `…_ShardDown_Rejects`,
`TestStartModel_Orphan_BackendRejection`, `TestAutostart_Orphan_Skipped` and others). Every row now
names a function that exists in the repository, or is explicitly labelled **by composition** — two or
more shipped tests whose propositions together establish the invariant, with no single end-to-end
test. Location abbreviations: `adr017` = `internal/process/supervisor_adr017_test.go`,
`bf01` = `internal/process/supervisor_bf01_test.go`, `adr016` = `internal/process/supervisor_adr016_test.go`,
`pending` = `internal/process/supervisor_pending_test.go`, `recovery` = `internal/process/supervisor_recovery_test.go`,
`kill` = `internal/process/supervisor_kill_test.go`, `shutdown` = `internal/process/supervisor_shutdown_test.go`,
`handlers` = `internal/webui/handlers/lifecycle_errors_d2_test.go`, `autostart` = `cmd/goal/autostart_test.go`,
`pipeline` = `internal/application/pipeline_service_test.go`.

| Invariant | Shipped proof | Kind |
|-----------|---------------|------|
| Two concurrent admits for one ModelID: exactly one succeeds | `TestAdmitAndStart_SameModelID_Concurrent` (adr017:53), serialized case `TestAdmitAndStart_SameModelID_SerialAdmission` (adr017:16) | direct |
| Different models start concurrently (no cross-model blocking) | `TestAdmitAndStart_DifferentModelIDs_Concurrent` (adr017:128) | direct |
| Admission rejects when the repository holds an orphan for the ModelID | `TestAdmitAndStart_OrphanConflict` (adr017:196), `TestAdmitAndStart_Pipeline_Orphan` (adr017:626), `TestRestart_OrphanFence` (bf01:551) | direct |
| After Dismiss (orphan→stale) admission succeeds | Fence side: `TestAdmitAndStart_DismissedOrphanNoLongerBlocks` (adr017:230) seeds a `stale` record and asserts a successful `AdmitAndStart`; transition side: `TestDismissOrphan_Success` (recovery:333) asserts the durable `stale` write | **by composition** (no single chain test — debt) |
| After Kill (orphan→stale) admission succeeds | Fence side: same `TestAdmitAndStart_DismissedOrphanNoLongerBlocks` (the fence keys only on `orphan`); transition side: `TestKillOrphan_Terminated` (kill:93) asserts the persisted record becomes `stale` | **by composition** (no single chain test — debt) |
| Two pipelines sharing a ModelID, concurrent: exactly one launches | `TestAdmitAndStart_Pipeline_CrossPipeline_SameModel` (adr017:525) | direct |
| Pipeline + manual start of the same ModelID, concurrent: exactly one launches | `TestAdmitAndStart_Manual_Pipeline_SameModel` (adr017:576) | direct |
| Same pipeline, two entries of one ModelID: both launch (ADR 013) | `TestAdmitAndStart_Pipeline_SamePipeline_DifferentEntries_SameModel` (adr017:463); restart-side sibling `TestRestart_WithinPipeline_DuplicateModelID_Allowed` (bf01:511) | direct |
| Same pipeline+entry, second start rejected | `TestAdmitAndStart_Pipeline_SameEntry_Rejected` (adr017:492) | direct |
| After ADR 016 C (termination unconfirmed) new admission is rejected | Rejection: `TestAdmitAndStart_ResidualStartingRejected` (adr017:257) — C leaves the generation `starting`, which is the state that predicate covers; cause: `TestStartCore_RunningPersistFail_KillUnconfirmed` (adr016:254) | direct |
| After ADR 016 D (rollback failed) new admission is rejected | Rejection: `TestAdmitAndStart_ResidualStartingRejected` (adr017:257); cause: `TestStartCore_RunningPersistFail_KillFailed` (adr016:310), registry survival `TestD3Cleanup_ADR016ResidualSurvives` | direct |
| After the lifecycle context is cancelled, admission returns `RejShuttingDown` | `TestAdmitAndStart_Shutdown` (adr017:368) plus the RB-015b drain series `TestRB015b_T3`…`T6` (shutdown) | direct |
| Pending window is conflict-visible | `TestAdmitAndStart_PendingVisibility` (adr017:296) plus `TestSupervisor_PendingWindow_*` (pending:64, :114, :167, :205) | direct |
| `POST /models/{id}/start` returns `409 orphan` when an orphan exists | Fence: `TestAdmitAndStart_OrphanConflict` (adr017:196); mapping: the `orphan rejection` case of `TestLifecycleErrorMappingMatrix` (handlers:68), which drives the same `writeLifecycleError` the handler calls | **by composition** (no single HTTP chain test — debt) |
| Autostart does not launch a model it cannot own | `TestAutostart_DuplicateGuard_ActiveInstanceExists` (autostart:543), `TestAutostart_DuplicateGuard_StaleInstanceDoesNotBlock` (autostart:584), `TestAutostart_FailureDoesNotBlockNext` (autostart:187); the orphan deny itself is `TestAdmitAndStart_OrphanConflict` | **by composition, partial** (no orphan-specific autostart test — debt) |
| Pipeline `startEntry` maps an `AdmissionRejection` to the right per-entry outcome | `TestPipelineStart_OrphanSkipped` (pipeline:323) plus the `OutcomeAlreadyRunning`/`ReasonShuttingDown` assertions in `internal/application/pipeline_d2_test.go` and `internal/application/pipeline_adr013_test.go` | direct |
| Restart of an in-flight instance blocks concurrent `AdmitAndStart` | `TestRestart_DoesNotBypassAdmit` (bf01:259) | direct |

The corrective slices added their own regressions, which are part of the same acceptance surface:
D1 terminal-target restart conflict (`TestRestart_TerminalTarget_ConflictRejected`, bf01:332), plus the
D1 reservation/claim series (`TestRestart_AbortAfterClaim_ReleasesReservation`,
`TestSpawnClaim_*`, `TestRestart_Stopping_AwaitsExistingGeneration`, `TestRestart_StartingRejected`,
`TestPreflightRestart_CompleteSet`), D0 unspawned-failure cleanup synchronization (`TestBF04_*`),
D3 cleanup ↔ registry coherence (`TestD3Cleanup_*` in `internal/process/supervisor_d3_cleanup_test.go`
and `internal/application/instance_cleanup_d3_test.go`, including `TestD3Cleanup_NoAutomaticEviction`
and `TestD3Cleanup_HistoricalRestartBeforeAndAfterCleanup`), and the canonical-precedence pins for the
lifecycle error contract (`TestLifecycleErrorOrdering`,
`TestLifecyclePrecedenceAgreesAcrossMappers`, `TestGroupFailureClassPrecedence`).

**Missing chain coverage is recorded as technical debt, not as an acceptance blocker** — see
[BACKLOG.md](../../BACKLOG.md): the `KillOrphan → stale → AdmitAndStart` end-to-end test, the
`POST /models/{id}/start → 409 orphan` end-to-end test, and the orphan-specific autostart test.

## Closure Chronology (corrective slices D0-D6.2)

Preserving chronology — the original closure happened, was published, and was later found incomplete.
Nothing below rewrites that sequence.

| When | What | Status |
|------|------|--------|
| 2026-09-18 | This ADR accepted. | Historical |
| 2026-09-19 | Slices A (`b377c46`), B (`a55e673`), C (`1d37918`) published, each CI 7/7. RB-002 **declared** resolved on Slice C's caller audit. | Historical (the declaration was premature — see below) |
| 2026-09-20 | **Focused remediation review** ran over the published batch (RB-002 Slices A+B+C, RB-004, RB-015b; baseline `08aef89`) and returned findings BF-01…BF-09. | Historical |
| 2026-09-20 | **D0** `d3d2c6d` + CI 35526734598 — post-admission failure cleanup synchronized (BF-04). | Published |
| 2026-09-21 | **D1** `d89c58a` + CI 35536576751 — terminal restart routed through `Supervisor.arbitrate` with a launch reservation visible across stop → spawn (BF-01); relaunched generation owned by the supervisor lifecycle context (BF-09). **This is the slice RB-002's current closure depends on.** | Published |
| 2026-09-22 | **D2** `6b89a01` + CI 35657065639 (green on **attempt 2**) — pipeline group stop/restart success attribution (BF-02) and sentinel-based lifecycle error mapping (BF-03a) with the bounded class-A API/documentation subset (BF-03b). Adjacent to this ADR, not an ADR 017 slice. | Published |
| 2026-09-23 | **D3** `bc7d06d` + CI 35857525442 (attempt 1) — explicit cleanup reconciles the Supervisor registry, and the unknown-instance restart preflight is classified `404` instead of `500`. Adjacent to this ADR, not an ADR 017 slice. | Published |
| 2026-09-24 | **D4** `68fdd63` + CI 36004107121 (attempt 1, 7/7 jobs, Linux `-race` step executed) — documentation/tracking reconciliation: the ADR's status, restart contract, Slice C claim, acceptance-criteria identifiers, terminalization wording and orphan error code were aligned with published behavior. | Published |
| 2026-09-24 | **D5** — focused remediation **re-review** of the published D0–D4 chain (baseline `68fdd63`). Ran and was **NOT clean**: the lifecycle, arbitration, shutdown, cleanup and attribution contracts re-proved from current bytes, but the review returned findings D5-01…D5-13, of which it labeled four as documentation/tracking or contract-consistency blockers to acceptance (D6 re-adjudicated that split in the next row). | Complete — remediation blockers found |
| 2026-09-24 | **D6** — bounded adjudication of the D5 blocker set, from current bytes. Acceptance blocker: **D5-01** only. Non-blocking but required before program closure: **D5-02, D5-03, D5-04, D5-10**. **F-3** was not established from repository evidence and no finding was invented. Canonical lifecycle error precedence fixed as **500 > 503 > 409**. | Design complete — blockers adjudicated |
| 2026-09-24 | **D6.1** — this correction slice: D5-01 restart contract, D5-02 test inventory, D5-03 chronology, D5-04 precedence alignment (`lifecycleAPIError`) with regression pins, D5-10 F-2 wording, plus the stale `DismissOrphan` pointer. | **Published** — committed `3ba719d9cf0573420b5665ab6c949154e4d435b7` and pushed; exact-SHA CI run `36020633891` (CI #158, event `push`) conclusion success with all 7 jobs green. *As authored (2026-09-24) this row said "commit and publication are separate Owner gates, so nothing beyond this is asserted about its own SHA" — that was true at the time; the separate gates have since run, and `git merge-base --is-ancestor 3ba719d main` proves the content is on published `main`.* |
| 2026-09-24 → recorded 2026-09-26 | **D6.2** — focused re-review of exactly D5-01, D5-02, D5-03, D5-04, D5-10 and the F-3 disposition, plus non-regression of the corrected error mappings. Ran over the published `3ba719d` bytes **after** D6.1's publication. | **Complete — PASS** (historical review). *Provenance: the verdict is persisted here on 2026-09-26 from the review's own gate record; the repository holds no separate D6.2 report artifact, so the execution is documented, not re-performed. Its subject matter is independently checkable in current bytes — see the corroboration row below. This record is NOT a new D6.2 execution.* |
| 2026-09-26 | **Repository corroboration of D6.2's subject matter** (documentation-side, not a re-review): `internal/webui/handlers/helpers.go` `lifecycleAPIError` evaluates the three 500 classes ahead of the two 503 classes under the comment "Canonical precedence inside a multi-cause error is 500 > 503 > 409"; this ADR states `stale` is deliberately not restartable; its required-test inventory is the D6.1-reconciled real-identifier table; `BACKLOG.md` F-2 records that **no `deleted == 0` short-circuit exists**. | Verified against `main` |
| 2026-09-24 / 2026-09-26 | **Manual Owner Acceptance** — RAN on 2026-09-24 over `3ba719d` and produced findings OWNER-UX-01 / OWNER-UX-02 / OWNER-IMPORT-01; the Owner recheck of those findings ran 2026-09-26 on a fresh `485cc6c` build. It is **no longer** gated on "D6.1 publication / D6.2 pass" — both already happened. **ADR 015** — FROZEN until remediation and Manual Owner Acceptance close. | Open — acceptance not closed; see ROADMAP P0/P1 for the current blocker set |

**What was actually wrong.** Terminal-instance `RestartWithLaunch` could reach the spawn path without
passing the arbitration boundary this ADR defines: the scan inside `arbitrate` looked at in-flight
instances, while a restart target in a terminal state is not in flight, so a concurrent same-model
launch could be admitted next to a restart about to spawn. Slice C's audit could not detect this because
it enumerated callers of the old exported `Supervisor.Start`, and the restart path never went through
that method. D1 closed it by introducing the restart claimant mode on the same boundary.

**What is NOT claimed here.** D2 and D3 are post-review remediation adjacent to this ADR and are not
ADR 017 migration slices; the deferred BF-07 aspects (terminal-controller metadata retention,
process-scoped historical restart) remain open debt, and this ADR's remediation program is **not**
complete. *Reconciled 2026-09-26:* the remaining open condition is **Manual Owner Acceptance closure**
— the earlier formulation of this sentence ("until D6.1 is published, D6.2 passes and Manual Owner
Acceptance closes") listed two events that have since occurred (D6.1 published as `3ba719d`, CI
`36020633891` 7/7; D6.2 complete — PASS), so it no longer describes the gate. Current blockers and
findings are tracked in `ROADMAP.md` (P0 ADR 017 item and P1 Manual Owner Acceptance), not here.

## Rejected Alternatives

| Alternative | Reason for Rejection |
|-------------|---------------------|
| Per-entry lock (PipelineEntryID-keyed) for all initiators | Manual starts have no entry identity. Would require synthetic IDs. |
| Admission inside `Supervisor.Start` (internal gate) | Requires repository access (orphan check) + owner knowledge. `AdmitAndStart` is the natural place; `Start` becomes the internal post-admission path. |
| Global lock (single mutex for all starts) | Kills cross-model parallelism. Unacceptable. |
| Repository-only admission (check repo, not s.instances) | Misses the pending window. Would allow double-launch. |
| Keep C3 mutex AND add `AdmitAndStart` | Redundant. Two mechanisms for the same purpose. |
| Optimistic admission (Start first, roll back if conflict) | Violates ADR 016. Cannot roll back a spawned process for an admission failure. |
| Separate `TryAdmit` + `Start` two-phase | Allows a caller to obtain admission and fail to consume it. Non-atomic. |
| Event-based admission | Non-deterministic, adds latency, complicates single-binary. |

## Affected Documentation (on implementation acceptance)

- API.md: the orphan rejection for a manual start is `409` with `code=conflict` and `error=orphan` — the
  published contract, documented in API.md §"Lifecycle error mapping". This bullet previously recorded a
  different error token that the implementation never emitted. No transport behavior changed.
- ADR 010: cross-reference (orphan gate now enforced server-side for all paths, not just pipeline).
- ADR 016: cross-reference (admission boundary is a pre-Start gate; does not modify ADR 016 invariants).
- ADR 013: cross-reference (compatibility matrix is codified in the admission boundary).
- ARCHITECTURE(_RU): ADR table row.
- ROADMAP: RB-002 status.

# ADR 017: Unified Owner-Aware Launch Arbitration Boundary

**Status:** Accepted — fully implemented and published 2026-09-19 (Slices A+B+C; RB-002 RESOLVED)
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
| LIFECYCLE_OWNED → TERMINAL/RELEASED | Process exits; `wait()` persists terminal; `releaseSlot` + `complete`; `RemoveTerminal` | `wait()` |

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
- **Dismiss** (supervisor.go:748) transitions orphan → stale. After the durable `store.Update` completes, admission sees stale (not orphan) and allows.
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

Restart paths (`RestartInstance` → `Supervisor.RestartWithLaunch`) operate on an **already materialized in-flight instance** (it is in `s.instances` and `IsInFlight()`). A concurrent `AdmitAndStart` for the same ModelID sees this in-flight instance and rejects per the compatibility matrix. No second admission path is needed. The per-instance `lifecycleMu` + pending gate protects same-instance concurrency.

### Shutdown / RB-015b Boundary

- **RB-002 (this ADR):** `AdmitAndStart` checks `s.lifecycleCtx.Err()` inside the arbitration critical section. If cancelled → reject with `RejShuttingDown`. No new admission after shutdown begins.
- **RB-015b (separate task):** Already-admitted work blocked on `acquireSlot` when shutdown begins. Requires `acquireSlot` to use the lifecycle context. Not implemented by this ADR.

### RB-004 Boundary

The legacy `/api/v1/runtimes/{id}/action/start` was retired (RB-004): it now returns `410 Gone` before any instance lookup or launch, directing callers to the canonical `POST /api/v1/models/{id}/start`. It no longer routes through `InstanceService.StartModel`/`AdmitAndStart` (a runtime is a launch template with no unambiguous ModelID). The `stop`/`restart` actions are unchanged.

### Source-of-Truth Table

| Fact | Authoritative Source |
|------|---------------------|
| Currently controlled generation (pending/starting/running/stopping) | `s.instances` (in-memory) |
| Recovered orphan (process alive, no controller) | Repository |
| Reservation/pending (between admit and spawn) | `s.instances` (pending state, in-memory) |
| ADR 016 persistence failure | In-memory for operational; repository for recovery |
| Terminal persist failure | In-memory terminal for operational; repository (stale `running`) for recovery |
| ADR 016 C/D residual | `s.instances` (`starting` = in-flight) |

Admission checks: (1) `s.instances` for `IsInFlight()` instances of the ModelID, (2) repository for `orphan` state. These two checks are complete.

### Migration Slices

| Slice | Scope | Invariant established |
|-------|-------|----------------------|
| **A** | `AdmitAndStart` on Supervisor + `arbLocks` + `LaunchOwner` + manual Start migration (remove C3 mutex) + orphan rejection + structured rejection + concurrency tests | Manual Start paths are non-bypassable. Orphan blocks manual Start. C3 mutex removed. |
| **B** | Pipeline `startEntry` migrated to `AdmitAndStart` with owner; ADR 013 compatibility matrix enforced; per-pipeline mutex retained for ordering; structured outcomes preserved | Pipeline paths are non-bypassable. Cross-pipeline TOCTOU eliminated. Within-pipeline independence preserved. |
| **C** | Model autostart migrated to `AdmitAndStart`; old exported `Supervisor.Start` removed or made unexported; final production caller audit | **Global non-bypassability achieved.** Zero production callers of the old `Supervisor.Start`. RB-002 globally implemented. |

**Transitional invariant:** After Slice A, manual paths are protected but pipeline/autostart still use the old path. After Slice B, pipeline is protected but autostart still uses the old path. Only after Slice C + final caller audit is RB-002 globally closed.

### Final Non-Bypassability Acceptance Criterion

After Slice C:
1. `grep` for `supervisor.Start` (or the old public method name) in production code shows zero callers outside `AdmitAndStart` itself.
2. The old method is either removed or unexported (package-private `startUnfenced`).
3. Every production OS-spawn path (initiators 1-10 from the forensic) routes through `AdmitAndStart`.
4. Race detector passes.

### Required Concurrency / Regression Tests

| Test | What it proves |
|------|---------------|
| `TestAdmitAndStart_SameModel_Concurrent` | Two concurrent admits for same ModelID: exactly one succeeds |
| `TestAdmitAndStart_DifferentModels_Concurrent` | Two different models start concurrently (no cross-model blocking) |
| `TestAdmitAndStart_Orphan_Present` | Rejects when repo has orphan for ModelID |
| `TestAdmitAndStart_Orphan_AfterDismiss` | After Dismiss (orphan→stale), admission succeeds |
| `TestAdmitAndStart_Orphan_AfterKill` | After Kill (orphan→stale), admission succeeds |
| `TestAdmitAndStart_Pipeline_CrossPipeline_Race` | Two pipelines sharing ModelID, concurrent: exactly one launches |
| `TestAdmitAndStart_Pipeline_Manual_Race` | Pipeline + manual Start of same ModelID, concurrent: exactly one launches |
| `TestAdmitAndStart_Pipeline_WithinPipeline_Repeat` | Same pipeline, two entries of same ModelID: both launch (ADR 013) |
| `TestAdmitAndStart_SameEntry_Idempotency` | Same pipeline+entry, second Start: rejected (already-running) |
| `TestAdmitAndStart_ADR016_C_Residual` | After C (termination unconfirmed), new admission rejected until wait() confirms |
| `TestAdmitAndStart_ADR016_D_Residual` | After D (kill failed), new admission rejected until wait() confirms |
| `TestAdmitAndStart_ShardDown_Rejects` | After lifecycle ctx cancelled, returns RejShuttingDown |
| `TestAdmitAndStart_PendingWindow` | During slot acquire (pending in s.instances, not in repo), second admit sees pending and rejects |
| `TestStartModel_Orphan_BackendRejection` | HTTP: POST /models/{id}/start returns 409 when orphan exists |
| `TestAutostart_Orphan_Skipped` | Model autostart skips model with orphan |
| `TestPipeline_StartEntry_AdmitOutcome` | Pipeline startEntry maps AdmissionRejection to correct per-entry outcome |
| `TestRestart_DoesNotBypassAdmit` | Restart of an in-flight instance blocks concurrent AdmitAndStart |

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

- API.md: 409 `recovery_conflict` code (orphan rejection for manual starts).
- ADR 010: cross-reference (orphan gate now enforced server-side for all paths, not just pipeline).
- ADR 016: cross-reference (admission boundary is a pre-Start gate; does not modify ADR 016 invariants).
- ADR 013: cross-reference (compatibility matrix is codified in the admission boundary).
- ARCHITECTURE(_RU): ADR table row.
- ROADMAP: RB-002 status.

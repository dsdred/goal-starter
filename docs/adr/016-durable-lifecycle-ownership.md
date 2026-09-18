# ADR 016: Durable Lifecycle Ownership Contract

**Status:** Proposed (revision 3 — final gap resolution)
**Date:** 2026-09-17
**Related:** ADR 001 (Process Ownership), ADR 002 (Supervisor & Instance Model), ADR 005 (Recovery — Identity-Verified Orphan Detection), ADR 008 (Recovery Kill Orphan)
**Remediates:** RB-001 (CONFIRMED HIGH), RB-003 (CONFIRMED MEDIUM)
**Blocks:** RB-002 Unified Launch Arbitration (depends on "what a successful Start means")

## Context

The process lifecycle has two critical transitions where durable state and in-memory state can diverge:

1. **Start:** `starting` → process spawned → `running` (RB-001)
2. **Terminal:** `running`/`stopping` → process exited → `exited`/`failed` (RB-003)

The current implementation at `internal/process/supervisor.go` permits both transitions to complete in memory while the durable repository retains a stale state. This creates crash windows where recovery cannot correctly identify the process generation.

## Current-State Forensic

### RB-001: `startCore` (supervisor.go:795-862)

```
Line 799: ic.instance.UpdateState(InstanceStateStarting)
Line 801: persistStateLocked() → persists {state=starting, no PID}  [MUST succeed]
Line 817: manager.Start() → OS process spawned                      [if fails → failed, persisted]
Line 833: status := ic.manager.Status() → PID, StartedAt
Line 834: ic.instance.PID = status.PID
Line 836: ic.instance.UpdateState(InstanceStateRunning)
Line 839: persistStateLocked() → persists {state=running, PID, StartedAt}
          if FAILS: LastError set, logged, but function CONTINUES
Line 861: return ic.instance, nil  ← SUCCESS (degraded)
```

**Consequence:** Repository has `starting` (no PID). Memory has `running` (with PID). If GoAl crashes before `wait()` persists terminal, `Recover` sees `starting` + PID=0 → classifies `stale` (pid-not-found). The alive process becomes an unidentifiable orphan.

### RB-003: `wait` (supervisor.go:1079-1183)

```
Line 1089: <-done  (process has exited)
Line 1100: ic.mu.Lock()
Line 1145: ic.instance.State = targetState  (terminal: exited/failed)
Line 1149-1154: store.Update(terminal entry)
          if FAILS: LastError set, logged, memory state remains terminal
Line 1151: ic.mu.Unlock()
Line 1171: run.releaseSlot()
Line 1182: run.complete()
```

**Consequence:** Repository retains `running` (or `stopping`). Memory is terminal. Slot is released. A new instance can be started. If crash occurs before any retry, `Recover` sees old instance as `running` + dead PID → `stale`. The exit code/class is permanently lost.

### Existing test that encodes the current (defective) contract

`TestRunningPersistenceFailureRollsBackOrDegrades` (supervisor_test.go:823) asserts:
- `Start()` returns `nil` error (degraded success)
- `LastError` is set
- Process continues running (no rollback)

This test codifies the RB-001 defect. It MUST be replaced.

## Design Questions and Answers

### 1. What exactly must be durably persisted before Start() may return success?

The **full running identity**: `{state=running, PID, StartedAt, InstanceID, ModelID, Executable, Args, WorkingDirectory, Environment}`.

This is the minimum durable evidence that allows `Recover` to:
- Identify the process (PID + Executable + StartedAt per ADR 005)
- Determine ownership (GoAl launched this specific process generation)
- Classify correctly (orphan vs stale)

If only `starting` (without PID) is durable, recovery cannot distinguish "GoAl crashed between persisting starting and spawning the process" (safe, no process exists) from "GoAl crashed after spawning but before persisting PID" (unsafe, process exists but is unidentifiable).

### 2. After OS process spawn succeeds but persistence of PID/running state fails, what is the authoritative outcome?

**Fail-closed with explicit rollback-failure contract.** The process is terminated and Start() returns an error. However, the invariant "no live process without durable running record" is **only conditionally guaranteed** — it holds if and only if the rollback kill succeeds. If the kill fails, an explicit ownership/recovery contract applies (see §2.1).

### 2.1 RB-001 Complete Outcome Matrix

After spawn succeeds and `persist(running+PID)` fails (retry exhausted), the rollback sequence produces the following outcomes:

| # | Kill | Wait (confirm exit) | Failed-state persist | OS Process | Controller Memory | Repository | Start() Result | Ownership | Recovery after crash | Slot Release |
|---|------|---------------------|---------------------|------------|-------------------|------------|----------------|-----------|---------------------|--------------|
| A | Success | Success (confirmed dead) | Success | **Dead** | `failed`, LastError | `failed` (durable) | Error: `ErrPersistenceFailure` | GoAl (recorded) | Terminal (`failed`) — no action | After persist (by `startCore`) |
| B | Success | Success (confirmed dead) | Failure | **Dead** | `failed`, LastError | `starting` (no PID) | Error: `ErrPersistenceFailure` | GoAl (memory only) | `stale` (pid-not-found) — process dead, correct | After persist attempt (by `startCore`) |
| C | Success | **Timeout** (cannot confirm) | N/A | **Unknown** (almost certainly dead) | `starting`, LastError: "termination unconfirmed" | `starting` (no PID) | Error: `ErrPersistenceFailure` + `ErrTerminationUnconfirmed` | **Held by `wait()`** | `stale` if dead; `orphan` if alive (PID not in repo) | **By `wait()` on eventual exit** |
| D | **Failure** (genuine OS refusal) | N/A (no kill delivered) | N/A | **ALIVE** | `starting`, LastError: "rollback failed: <kill error>" | `starting` (no PID) | Error: `ErrPersistenceFailure` + `ErrRollbackFailed` | **Held by `wait()`** | `stale` (pid-not-found) — **unresolvable orphan** | **By `wait()` on eventual exit** |

**Note on "Kill returns process-not-found":** If `manager.Kill()` returns an error indicating the process no longer exists (e.g., "no such process", "invalid PID"), this is NOT Outcome D. The process already exited between the persist failure and the kill attempt. This is reclassified as **Outcome A or B** (confirmed dead). Only a genuine OS refusal (access denied, permission error) constitutes Outcome D.

**Unified C/D residual-ownership contract:**

Outcomes C and D share a single invariant: **slot and run ownership are held until `wait()` confirms process exit.** They differ only diagnostically:
- C: kill accepted by OS, termination unconfirmed (process almost certainly dead)
- D: kill refused by OS (process almost certainly alive)

In both cases:
- The instance remains in `s.instances` (visible to `List()`, `Status()`)
- The `wait()` goroutine is started (it monitors `managerDone`)
- The slot is NOT released by `startCore`
- The run is NOT completed by `startCore`
- The ERROR log contains the full process identity (PID, Executable, InstanceID, ModelID) for operator awareness
- When the process eventually exits, `wait()` releases the slot and completes the run (exactly-once via `sync.Once`)
- If the process never exits (infinite mode), the slot is held until `Shutdown` kills it

**`Supervisor.Start` return for C/D:** `startCore` returns `(instance, error)` — the instance is kept in `s.instances` and the caller receives both the instance reference and the error. The caller (HTTP handler) returns 500 to the user, but the instance remains manageable (visible, stoppable) until the process exits.

**`Supervisor.Start` return for A/B:** `startCore` returns `(nil, error)` — the instance is cleaned up (removed from `s.instances`, slot released, run completed). The caller receives only the error.

**Why C and D do NOT release the slot:**

The concurrency slot represents "a process generation is consuming a managed slot." Releasing it implies the process is no longer running (or no longer needs to be accounted for). If we cannot confirm the process is dead:
- Releasing the slot allows a new instance to start while the old one may still be running
- This violates resource safety (two processes where one slot is accounted)
- The `wait()` goroutine will release the slot when the process actually exits

The cost of holding the slot in Outcomes C/D is:
- One slot is occupied by a (likely dead or dying) process
- In Outcome C: the slot is held for a very short time (the process is almost certainly dead; `wait()` will detect exit within milliseconds)
- In Outcome D: the slot is held until the operator kills the process or GoAl shuts down
- This is acceptable: Outcomes C/D are extreme edge cases (kill timeout / kill refusal)

### 3. Is degraded-success still permitted?

**No.** The current degraded-success contract is rejected.

The existing test `TestRunningPersistenceFailureRollsBackOrDegrades` encodes the defective behavior and must be replaced.

### 4. If rollback/termination is required after persistence failure, who owns that cleanup and how is its failure represented?

**Ownership:** `startCore` owns the rollback. It is in the same call stack as the spawn and holds the `InstanceController` reference.

**Rollback sequence (after retry exhaustion):**
1. `ic.manager.Kill()` — terminate the process (SIGKILL / TerminateProcess)
   - If Kill returns "process not found" / "no such process" → reclassify as confirmed dead (Outcome A/B)
   - If Kill returns a genuine OS error (access denied, permission) → Outcome D
2. Wait for process exit (bounded timeout via lifecycle context, see §8)
   - If confirmed dead → Outcome A/B
   - If timeout (kill was accepted) → Outcome C
3. **Outcome A/B (confirmed dead):**
   - `ic.instance.UpdateState(InstanceStateFailed)`
   - `ic.instance.LastError = "persist running state failed: <err>; rollback: terminated"`
   - `persistStateLocked()` — best-effort persist of `failed` state
   - Start `wait()` goroutine (will detect the already-dead process immediately)
   - Release slot, complete run
   - Return `(nil, error)` to `Supervisor.Start`
4. **Outcome C/D (termination unconfirmed or kill failed):**
   - `ic.instance.LastError = "persist running state failed: <err>; rollback: <unconfirmed|failed>"`
   - Do NOT set state to `failed` (process may be alive)
   - Do NOT persist (no confirmed terminal state)
   - Start `wait()` goroutine (will detect eventual exit, release slot, complete run)
   - Do NOT release slot
   - Do NOT complete run
   - Log at ERROR level with full identity (PID, Executable, InstanceID, ModelID)
   - Return `(instance, error)` to `Supervisor.Start` (instance stays in `s.instances`)

**Error representation:**
- Outcomes A/B: `errors.Join(ErrPersistenceFailure, <original persist error>)`
- Outcome C: `errors.Join(ErrPersistenceFailure, <original persist error>, ErrTerminationUnconfirmed)`
- Outcome D: `errors.Join(ErrPersistenceFailure, <original persist error>, ErrRollbackFailed, <kill error>)`

### 5. What durable evidence is required so recovery can identify the exact spawned generation after a GoAl crash?

Per ADR 005 identity contract, the durable record must contain:
- `PID` — the OS process identifier
- `Executable` — the resolved executable path
- `StartedAt` — the wall-clock time of process spawn (for `timesApproximatelyEqual` ±5s check)
- `Args` — the full argument vector (for identity verification if supported by the prober)
- `WorkingDirectory` — the working directory (additional identity anchor)

All five must be in the same atomic persistence operation. A partial record (e.g., PID without StartedAt) is insufficient for identity verification.

**During rollback (Outcomes B, C, D):** The durable record remains `starting` (no PID). Recovery cannot identify the process. This is the accepted residual risk of the fail-closed contract. The ERROR log is the only record of the PID.

### 6. What happens when terminal-state persistence fails after the process has already exited?

**Bounded retry, then the run is finalized with a bounded failure outcome.**

Unlike RB-001, there is no live process to kill. The process is already dead.

**Three distinct truth levels after terminal persistence exhaustion:**

| Level | Content | Authority |
|-------|---------|-----------|
| **In-memory terminal truth** | `ic.instance.State = exited/failed`, ExitCode, ExitClass, StoppedAt | Operational: `List()`, `Status()`, slot release, run completion all proceed |
| **Last durable repository state** | Whatever was last successfully persisted (typically `running` with PID) | Recovery: if GoAl crashes, `Recover` sees this state |
| **Future recovery reconciliation** | `Recover` probes the PID → dead → classifies `stale` | Reconciliation: this is NOT a "persisted fallback" — it is a future runtime action by a new GoAl instance |

**The run IS fully finalized after retry exhaustion.** Specifically:
- The in-memory state is terminal (authoritative for all in-process operations)
- The slot IS released
- The run IS completed
- The instance remains in `s.instances` (visible to `List()`)
- `ShutdownWithPersistence` will retry on graceful shutdown (best-effort)

**The persistence failure is a bounded failure outcome, not a blocker to run finalization.** The run cannot be "un-finalized" — the process is dead, the slot must be freed, the run must complete. The only consequence of the persistence failure is that the durable record is stale until recovery reconciles it.

**No fail-closed needed:** The process is dead. There is nothing to roll back.

### 7. Which source is authoritative during persistence failure?

**The repository is the durable source of truth. Controller memory is the operational source of truth.**

During a persistence failure:
- **Operational decisions** (can I start another instance? can I stop this one?) use controller memory. The process IS running (or dead) regardless of what the repository says.
- **Recovery decisions** (what state was this instance in?) use the repository. After a crash, only the repository survives.
- **The contract:** After Start() returns success (nil error), the repository MUST contain sufficient evidence for recovery to identify the process. If it doesn't, Start() must not have returned success.

There is no "degraded state" in the durable record. The repository either has the full running identity or it doesn't. If it doesn't, Start() returned an error and the rollback contract applies.

### 8. Retry policy

**Principles (not arbitrary constants):**

| Principle | Rationale |
|-----------|-----------|
| Retries are **synchronous** | The caller (startCore / wait goroutine) owns the retry. No background daemons. Ownership is clear. |
| Retries are **bounded** (finite attempts) | Must not block indefinitely. Slot release and run completion must proceed. |
| Retries occur while **lifecycle/slot ownership is held** | The instance is still "in flight" during retry. The slot is not released until the persist outcome is known. |
| Retries are **cancellable** via context | If the supervisor's lifecycle context is cancelled (shutdown), the retry loop exits immediately. No retry after shutdown begins. |

**Which errors are retryable:**
- **Retryable:** Transient I/O errors — `EAGAIN`, `EIO`, file-lock contention, "resource temporarily unavailable". The JSON store's `store.Update` wraps these.
- **NOT retryable:** Permission errors (`EACCES`, `EPERM`), disk full (`ENOSPC`), invalid path. These will not succeed on retry.
- **Implementation:** The retry loop checks the error. If the error is a known permanent failure (permission, ENOSPC), it skips remaining retries and proceeds to the failure path immediately. If the error is transient (or unknown), it retries.

**Attempt count and backoff:**
- The exact numbers (e.g., 3 attempts, 100ms/200ms backoff) are **implementation parameters**, not architectural invariants. They MUST be:
  - Small enough to not delay the user-visible response by more than ~1s total
  - Large enough to handle a single transient I/O stall
  - Configurable via a constant (not hardcoded in the retry loop) for testability
- **Architectural requirement:** total retry time MUST be bounded to < 2 seconds. The exact value is an implementation detail.

**Cancellation semantics:**
- The retry loop checks `ctx.Done()` before each attempt.
- If the context is cancelled (supervisor shutdown), the retry exits immediately with `context.Canceled`.
- For `startCore`: if the retry is cancelled, the fail-closed path proceeds (kill the process).
- For `wait()`: if the retry is cancelled, the run finalizes (process is already dead, slot releases).

### 8.1 Context Ownership: Post-Spawn Lifecycle Operations

**Critical distinction:** The caller's request context (e.g., HTTP request `r.Context()`) is NOT the same as the supervisor's lifecycle context. After the OS process is spawned, the process belongs to the supervisor's lifecycle, not to the caller's request.

**The problem:** In the current code, `Supervisor.Start(ctx, ...)` receives the caller's context (HTTP request context). This context is used for `acquireSlot(ctx)` (pre-spawn) and passed to `startWithReservation(s.lifecycleContext(), reservation)` where `s.lifecycleContext()` is used for the actual process spawn. However, the persist and rollback operations inside `startCore` currently use no explicit context — they are bare calls.

**The contract:** After successful OS spawn, ALL subsequent lifecycle operations (persist running, rollback kill, wait/confirm termination, failure persistence) MUST use the **supervisor lifecycle context** (`s.lifecycleContext()`), NOT the caller's request context.

**Rationale:**
- A disconnected/cancelled HTTP request MUST NOT abandon lifecycle ownership of an already-spawned process.
- If the HTTP client disconnects after the process is spawned, the request context is cancelled. If we used the request context for persist/rollback, the cancellation would abort the rollback, leaving an unmanaged process.
- The lifecycle context is tied to the GoAl application's lifetime (cancelled only on application shutdown). It outlives any individual HTTP request.

**Implementation contract:**

| Operation | Context Used | Cancellation Meaning |
|-----------|-------------|---------------------|
| `acquireSlot` (pre-spawn) | Caller's request context (`ctx` parameter) | Request cancelled → don't start (no process exists yet) |
| `manager.Start()` (spawn) | `s.lifecycleContext()` | Application shutting down → don't spawn |
| `persist(running+PID)` + retry | `s.lifecycleContext()` | Application shutting down → abort retry, proceed to fail-closed |
| `manager.Kill()` (rollback) | `s.lifecycleContext()` | Application shutting down → still attempt kill (process must not be orphaned) |
| Wait for exit (rollback) | `s.lifecycleContext()` with bounded timeout | Application shutting down → abort wait, Outcome C |
| `persist(failed)` (rollback) | `s.lifecycleContext()` | Application shutting down → skip (best-effort) |
| `wait()` goroutine (normal operation) | `s.lifecycleContext()` | Application shutting down → `Shutdown` will kill the process |

**Shutdown interaction (relevant to RB-015b):**
- When `Shutdown` begins, it cancels the lifecycle context.
- In-flight `startCore` operations (persist retry, rollback) detect the cancellation and proceed to their failure paths.
- The `wait()` goroutines detect the cancellation via `Shutdown`'s explicit `Stop()` calls (which kill the processes).
- A `Start` call that is blocked on `acquireSlot` when the lifecycle context is cancelled will fail (slot acquisition returns error). No new process is spawned.
- A `Start` call that has already spawned the process but is in the persist/rollback phase will complete its rollback (the lifecycle context is the same one being cancelled, but the kill and wait use bounded timeouts that complete before the 30s shutdown budget expires).

**RB-015b dependency:** The Unified Launch Arbitration task will define whether `Start` is rejected entirely once Shutdown begins (admission control). This ADR defines what happens to in-flight `Start` calls that have already passed admission. The two are complementary: RB-015b prevents new admissions; this ADR ensures in-flight operations complete safely.

### 9. How are concurrency-slot release and run completion ordered relative to terminal persistence?

**For normal terminal transition (RB-003):**
```
1. Set terminal state in memory (under ic.mu)
2. Persist terminal state (outside ic.mu, with bounded retry)
3. Release slot (regardless of persist outcome)
4. Complete run
```

The slot IS released after retry exhaustion. This is safe because:
- The process is dead (no resource contention)
- The old instance will be classified `stale` on recovery (excluded from active)
- A new instance can safely start (the old one is dead)

**For RB-001 fail-closed (running persist failure):**

| Outcome | Slot Release | Run Completion | `wait()` goroutine |
|---------|-------------|----------------|-------------------|
| A (kill confirmed, persist OK) | By `startCore` after persist | Yes | Started (detects already-dead process) |
| B (kill confirmed, persist fail) | By `startCore` after persist attempt | Yes | Started (detects already-dead process) |
| C (kill accepted, unconfirmed) | **By `wait()` on eventual exit** | **By `wait()`** | Started (monitors for exit) |
| D (kill failed, process alive) | **By `wait()` on eventual exit** | **By `wait()`** | Started (monitors for exit) |

**Unified invariant for C/D:** Slot and run ownership are held by the `wait()` goroutine until it confirms process exit. The `wait()` goroutine calls `run.releaseSlot()` and `run.complete()` (exactly-once via `sync.Once`). If the process never exits, the slot is held until `Shutdown` terminates it.

### 10. What crash windows exist at every transition and what must Recover() observe for each?

| # | Window | Durable State | Process State | Recover Classification | Safe? |
|---|--------|--------------|---------------|----------------------|-------|
| 1 | After `persist(starting)`, before `manager.Start()` | `starting`, no PID | Not spawned | `stale` (pid-not-found) | YES |
| 2a | After `manager.Start()`, during `persist(running+PID)` retry | `starting`, no PID | ALIVE | If crash: `stale` (pid-not-found) → **ORPHAN** | **MITIGATED**: crash during retry is extremely narrow (~1s window); process is alive but unidentifiable. Operator must check ERROR log. |
| 2b | After fail-closed Outcome C/D, crash before `wait()` detects exit | `starting`, no PID | Unknown/ALIVE | `stale` (pid-not-found) → **UNRESOLVABLE ORPHAN** (C) or confirmed alive but unidentifiable (D) | **RESIDUAL RISK** (documented, operator intervention) |
| 3 | After `persist(running+PID)` succeeds | `running`, PID, StartedAt | ALIVE | `orphan` (identity confirmed) or `stale` (identity failed) | YES |
| 4 | Process exited, before `persist(terminal)` | `running`, PID | Dead | `stale` (pid-not-found) | YES (info loss) |
| 5 | After `persist(terminal)` succeeds | `exited`/`failed`, exit code | Dead | Terminal (no recovery needed) | YES |

**Window 2a** (crash during the ~1s retry window): The process is alive, the repository has `starting` without PID. Recovery classifies `stale`. The process is an unidentifiable orphan. This window is extremely narrow (bounded retry is < 1s) and requires a crash at exactly the wrong moment. The ERROR log (written before the retry) contains the PID for operator intervention.

**Window 2b** (Outcome C/D): The fail-closed path completed (kill attempted), but `wait()` has not yet detected the process exit. If GoAl crashes in this window, the repository has `starting` without PID. The process may or may not be alive. This is the documented residual risk requiring operator intervention.

## Lifecycle Persistence State Machine

```
                    persist fails (no retry)
                    ┌─────────────────────────────────┐
                    │                                 ▼
 [create] ──► PENDING ──► STARTING ──► SPAWN ──┬──► RUNNING ──► ... ──► TERMINAL
                (slot held)   (persist OK)    │       (persist OK)         (persist w/ retry → finalize)
                                               │
                                               ├── spawn fails ──► FAILED (persist)
                                               │
                                               └── persist(running) fails after retry
                                                     ├── kill OK + confirmed ──► FAILED (best-effort persist) → slot released
                                                     ├── kill OK + unconfirmed ──► FAILED (unconfirmed) → slot released
                                                     └── kill FAILED ──► STARTING (stuck) → slot HELD → operator intervention
```

**States with durable persistence:**
- `pending`: persisted at `Supervisor.Start:263` (after slot acquisition)
- `starting`: persisted at `startCore:801` (MUST succeed)
- `running+PID`: persisted at `startCore:839` (MUST succeed after retry, else fail-closed)
- `stopping`: persisted at `stopCore` (existing behavior)
- `exited`/`failed`: persisted at `wait()` or `stopCore` (retry, run finalizes regardless)

## Proposed Invariant Set

### Start Success Invariant

| # | Invariant | Enforcement Point |
|---|-----------|-------------------|
| S1 | Start() returns `nil` error ONLY IF the repository contains `{state=running, PID, StartedAt, Executable, Args, WorkingDirectory}` for the instance | `startCore` after successful persist |
| S2 | PID is persisted atomically with `state=running` (single `store.Update` call) | `persistStateLocked` |

### Start Failure Ownership Invariant

| # | Invariant | Enforcement Point |
|---|-----------|-------------------|
| F1 | Start() returns non-nil error ONLY IF the full running identity is NOT durable in the repository | `startCore` — see proof below |
| F2 | If Start() returns non-nil error AND termination is confirmed (Outcome A/B), no live process exists that this Start spawned | `startCore` rollback |
| F3 | If Start() returns non-nil error AND termination is NOT confirmed (Outcome C/D), the `wait()` goroutine holds slot/run ownership and will release it on process exit; the ERROR log contains full process identity | `startCore` + `wait()` |
| F4 | Outcome C/D: the instance remains in `s.instances`, the slot is held, the run is not completed — until `wait()` confirms exit | `wait()` goroutine |
| F5 | Start() non-nil error does NOT guarantee "process is absent" — it guarantees "ownership is either terminated (F2) or actively held by `wait()` with operator visibility (F3)" | `startCore` return |

**F1 Proof:** In `startCore`, the only code path that reaches `return ic.instance, nil` (success) is AFTER `persistStateLocked()` at line 839 succeeds. All other return paths are:
- Line 805: `persist(starting)` failed → `return nil, err` (no running identity)
- Line 827: `manager.Start()` failed → `return nil, err` (no running identity)
- Proposed fail-closed path: `persist(running)` failed → `return nil, err` or `return instance, err` (no running identity)

There is NO code path where `persist(running+PID)` succeeds and `startCore` subsequently returns an error. The operations between successful persist and `return nil` are: `publishBrokerEvent` (non-failing, in-memory), `ic.mu.Unlock()` (non-failing), `go forwardLogs` (non-failing), `go wait` (non-failing). Therefore F1 holds: if Start() returns an error, the running identity was never persisted.

**Note on `Supervisor.Start` wrapper:** After `startCore` returns, `Supervisor.Start` can return an error in two cases:
- `startCore` returned `(nil, err)`: no running identity was persisted (F1 holds)
- `startCore` returned `(instance, err)`: this is Outcome C/D — no running identity was persisted (F1 holds)
- `startCore` returned `(instance, nil)`: `Supervisor.Start` returns `(snapshot, nil)` — success (F1 not applicable)

No path exists where `Supervisor.Start` returns an error after running identity persistence succeeded.

### Terminal Transition Invariants (RB-003)

| # | Invariant | Enforcement Point |
|---|-----------|-------------------|
| T1 | Terminal state persistence failure does NOT prevent slot release or run completion | `wait()` after retry exhaustion |
| T2 | Terminal state persistence failure does NOT prevent the instance from remaining in `s.instances` | `wait()` — instance is not removed |
| T3 | The in-memory terminal state is authoritative for all in-process operations regardless of persist outcome | `wait()` |
| T4 | `ShutdownWithPersistence` retries terminal persistence for all in-memory terminal instances | `ShutdownWithPersistence` (existing) |
| T5 | If GoAl crashes after terminal persist failure, `Recover` classifies the instance as `stale` (dead PID) — this is recovery reconciliation, not a persisted fallback | `Recover` + `classifyForRecovery` |

### Preserved Invariants

| # | Invariant | Source |
|---|-----------|--------|
| P1 | Exactly one `cmd.Wait()` owner per process generation | ADR 002 |
| P2 | Exactly-once slot release per run | `run.releaseSlot()` via `sync.Once` |
| P3 | Start() is lifecycle/spawn semantics, NOT readiness semantics | This ADR |
| P4 | No new state is introduced. The existing state set is unchanged. | This ADR |
| P5 | Lifecycle and readiness are orthogonal | This ADR (ADR 015 FROZEN) |

## Rejected Alternatives

| Alternative | Reason for Rejection |
|-------------|---------------------|
| **Degraded success (current)** | Creates permanent crash window (Window 2a). Process alive but unidentifiable after crash. Violates S1/F1. |
| **Async retry daemon** | Introduces unbounded background goroutines, complicates shutdown, makes ownership unclear. Violates P1/P2. |
| **Two-phase persist (PID first, then state)** | "starting with PID" is a semantically invalid state. Recovery would need to handle it. Increases state space without benefit. |
| **WAL (write-ahead log) for state transitions** | Over-engineered for a single-binary local tool. The JSON store is sufficient if we enforce the atomic persist contract. |
| **Do not persist `starting` at all** | Loses diagnostic value. Cannot distinguish "crashed before spawn" from "crashed after spawn". |
| **Infinite retry on terminal persist** | Blocks the `wait()` goroutine indefinitely. Prevents slot release. Prevents shutdown. Violates T1. |
| **Unconditionally release slot in Outcome D** | The process is alive and consuming resources. Releasing the slot allows a new instance to start while the old one is still running. Violates resource safety. |
| **Declare "no live process without durable record" as absolute invariant** | Cannot be guaranteed in Outcome D (kill failed). Must be a conditional invariant with explicit residual risk. |

## Compatibility / API Consequences

1. **Start() error semantics change:** Previously, Start() could return `nil` error with `LastError` set (degraded success). After this ADR, Start() returns `nil` error ONLY on full success (running+PID persisted). All other outcomes return non-nil error.
   - HTTP `POST /api/v1/models/{id}/start`: previously could return 200 with a degraded instance. After: returns 500 if persistence fails after retry. The 200 response guarantees durable running identity (S1).
   - This is a **breaking change** for any client that relies on 200 + LastError as a "started but degraded" signal. No such client exists.

2. **New error sentinels:**
   - `process.ErrPersistenceFailure` — persist failed after retry
   - `process.ErrTerminationUnconfirmed` — kill sent but exit not confirmed (Outcome C)
   - `process.ErrRollbackFailed` — kill failed, process may be alive (Outcome D)
   - Callers can use `errors.Is` to differentiate failure modes.

3. **No new API endpoints.** No new states. No new configuration.

4. **Recovery contract unchanged.** `Recover` still classifies based on PID + identity. The only new case is Outcome D (unresolvable orphan) — but this requires the kill to genuinely fail, which is an extreme edge case.

## Required Test Changes

| Test | Action | New Assertion |
|------|--------|---------------|
| `TestRunningPersistenceFailureRollsBackOrDegrades` | **REPLACE** | Outcome A: persist fails → kill → confirmed dead → Start() returns `ErrPersistenceFailure`, instance `failed`, slot released |
| New: `TestStartCore_RunningPersistFail_KillConfirmed` | **ADD** | Outcome A/B: full rollback sequence, assert process dead, error returned, slot free |
| New: `TestStartCore_RunningPersistFail_KillUnconfirmed` | **ADD** | Outcome C: kill sent but wait times out. Assert: error includes `ErrTerminationUnconfirmed`, instance `failed`, slot released |
| New: `TestStartCore_RunningPersistFail_KillFailed` | **ADD** | Outcome D: kill returns error (process alive). Assert: error includes `ErrRollbackFailed`, instance stays `starting`, slot NOT released, ERROR log contains PID |
| New: `TestStartCore_RunningPersistRetry_SucceedsOnSecondAttempt` | **ADD** | Mock store fails first, succeeds second. Assert: Start() returns nil, PID in repository, process running |
| New: `TestStartCore_RunningPersistFail_KillProcessAlreadyGone` | **ADD** | Kill returns "no such process". Assert: treated as confirmed dead (Outcome A/B), NOT Outcome D |
| `TestNaturalExitPersistenceFailureObservable` | **ADAPT** | Terminal persist failure: LastError set, instance in List() as terminal, slot released, run completed. Repository still has `running`. (T1-T5) |
| New: `TestWait_TerminalPersistFail_SlotReleased_RunCompleted` | **ADD** | Explicitly assert T1 (slot released) and run completion after terminal persist exhaustion |

## Minimal Implementation Slices

**Slice 1: Fail-closed on running persist failure (RB-001)**
- File: `internal/process/supervisor.go` — `startCore`
- Change: Replace the "log and continue" at line 839-846 with: bounded retry → on exhaustion: `manager.Kill()` → handle outcomes A-D → return appropriate error
- New sentinels: `internal/process/errors.go` — `ErrPersistenceFailure`, `ErrTerminationUnconfirmed`, `ErrRollbackFailed`
- Test: Replace + add tests as listed
- Lines changed: ~50 in supervisor.go, ~10 in errors.go

**Slice 2: Bounded retry on terminal persist (RB-003)**
- File: `internal/process/supervisor.go` — `wait`
- Change: Wrap the `store.Update` at line 1154 in a bounded retry loop with context cancellation. On exhaustion: log ERROR, proceed to slot release and run completion.
- Test: Adapt + add tests as listed
- Lines changed: ~20 in supervisor.go

**Slice 3: Test updates**
- Replace/adapt/add tests as listed above
- No production behavior change beyond Slices 1-2

## Interaction Constraints for Unified Launch Arbitration (RB-002)

This ADR establishes the **durable lifecycle ownership contract**. It does NOT define the arbitration source-of-truth for launch admission. That is the scope of the RB-002 design task.

**What this ADR guarantees for RB-002:**
- S1: A successful Start (nil error) means the repository has the full running identity.
- F1-F5: A failed Start has explicit ownership semantics (process terminated or operator intervention required).
- P4: No new states. The state set is unchanged.

**What this ADR does NOT decide (RB-002 scope):**
- Whether the arbitration boundary uses `supervisor.List()`, the repository, or both.
- How recovered orphans (repository-only, not in `s.instances`) interact with the in-flight guard.
- How pipeline starts are arbitrated against manual starts.
- The fate of the legacy `/runtimes/{id}/action/start` endpoint.
- Shutdown admission (RB-015b).

**Known constraint for RB-002:** Recovered orphans are in the repository but NOT in `s.instances`. Any arbitration that relies solely on `supervisor.List()` will be blind to orphans. The RB-002 design MUST account for this.

## ADR 015 Readiness

**FROZEN.** This ADR explicitly does NOT introduce readiness probing, readiness states, or readiness timeouts. The `starting` → `running` transition is purely lifecycle/spawn. Readiness is orthogonal and will be designed separately under ADR 015 when unfrozen.

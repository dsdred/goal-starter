# ADR 005: Recovery — Identity-Verified Orphan Detection and Restart Reconciliation

**Status:** Proposed
**Date:** 2026-08-22
**Agreed:** 2026-08-22 (owner contract agreement)
**Related:** ADR 001 (Process Ownership, Windows Job Object / Linux process group), ADR 002 (Supervisor & Instance Model — current conservative recovery), ROADMAP P0 "Recovery: identity-verified orphan detection and restart reconciliation"

## Context

ADR 002 defines the current recovery contract: on startup, `Supervisor.Recover` (`internal/process/supervisor.go`) loads all instances and, for each instance in a transitional state (`pending`/`starting`/`running`/`stopping`), marks it `stale` and persists it. Stale instances are excluded from the active list. There is **no** PID reattachment and **no** liveness verification. `stale` is a terminal state (`domain.InstanceState`, `LaunchInstance.IsTerminal`).

v2.0.1 manual acceptance observed History rows with the state «УСТАРЕВШИЙ» (STALE). That is the current contract, not a defect, but the semantics are opaque to users: a single `stale` state conflates "the process is gone" with "the process may still be running but GoAl no longer owns it."

ROADMAP P0 requires this design before any implementation. This ADR defines the recovery contract and answers the six design questions recorded under the P0 item:

1. When is an instance considered stale?
2. When is an instance considered orphaned?
3. When is PID reattachment possible?
4. When is process ownership considered confirmed?
5. What happens to an orphan process?
6. What state is persisted in history, and how is it explained to the user?

## Current behavior (baseline)

- `Supervisor.Recover` maps `pending|starting|running|stopping` → `stale`; persists; stale is terminal and excluded from active/concurrency accounting.
- The instance entry already stores the identity anchors needed for detection: `PID`, `Executable`, `Args`, `WorkingDirectory`, `StartedAt` (`domain.LaunchInstance`, `internal/storage` `LaunchInstanceEntry`).
- Ownership primitives (ADR 001): Windows Job Object owns the process tree; Linux uses a dedicated process group (SIGTERM → SIGKILL). Both are bound to the GoAl process that launched the child.

## Key constraint (drives the decision)

OS process ownership is bound to the **launching** process. After GoAl exits and restarts, the new GoAl process does **not** hold the prior session's Job Object handle (Windows) or process-group parentage (Linux), so it **cannot re-claim lifecycle control** (stop, capture exit code, job/group cleanup) over a child it did not launch in this session:

- **Windows:** the Job Object handle closes on process exit; a new process cannot attach to an arbitrary PID. If the Job was created with kill-on-close, the child may already be dead.
- **Linux:** process-group signaling requires parentage / group membership / privilege; a fresh process cannot signal another group.

**Consequence:** true lifecycle reattachment after a restart is infeasible/unsafe under the current ownership model. The recovery contract is therefore framed as **orphan detection + restart reconciliation**, not reattachment.

Additionally, the OS **reuses PIDs**. A bare "is PID N alive?" check is unsafe: it can match an unrelated process. Identity must be verified with the strongest available anchors, never PID alone.

## Decision

**GoAl does NOT perform lifecycle reattachment after restart.** On startup it performs **identity-verified liveness detection** to classify each previously-transitional instance as `orphan` or `stale`, and it provides a **safe Dismiss/reconciliation** flow. Destructive termination ("kill") of an orphan is **out of scope** for the first implementation (see Future work).

### State model (final)

- `pending` / `starting` / `running` / `stopping` — live transitional states (unchanged).
- `exited` / `failed` — terminal, set within a live session (unchanged).
- `unknown` — unchanged.
- **`orphan`** — new **first-class, non-terminal** runtime state: the process may still be running, but the current GoAl does **not** own its lifecycle. User-visible and actionable via Dismiss/reconciliation. It is **not** a UI-only sub-status of `stale`.
- **`stale`** — **terminal** historical state: the prior process is gone, or its identity could not be confirmed. "Start fresh."

Transitions:
- `IsTerminal`: `exited`, `failed`, `stale` (orphan is **not** terminal).
- `IsActive` / concurrency accounting: neither `orphan` nor `stale` occupies a running slot.
- `orphan` → `stale` on user **Dismiss** (reconciliation); no process is touched.

### Identity contract (for liveness detection)

Detection uses the **strongest available** identity combination: **PID + executable path + process start time**.

- **Bare PID is forbidden** as the basis for any ownership claim or action (PID reuse).
- **PID + executable path alone is NOT sufficient** to claim `orphan` when start-time verification is available; if a start time is available and does not match, identity is not confirmed.
- **Conservative fallback** — when identity cannot be reliably confirmed (any required anchor missing or mismatched, or probing is permission-limited/unavailable), GoAl:
  - does **not** claim ownership (does **not** mark `orphan`);
  - performs **no** destructive action;
  - records the instance as `stale` with an explicit **uncertainty diagnostic** (`recovery_reason: pid-not-found | identity-unconfirmed`), so the state/diagnostics reflect the uncertainty.

### Ownership confirmation

Ownership is considered **confirmed** only for processes GoAl launched in the **current** session (it holds the Job Object / process-group handle). For prior-session PIDs, ownership is **never** re-confirmed; at best identity-verified liveness is *detected*.

### Orphan handling — first implementation scope

In scope:
- Orphan **detection** + **identity verification** (per the identity contract).
- **User-facing `orphan` state** (plain label, no STALE jargon).
- **Safe Dismiss / reconciliation flow:** the user acknowledges an orphan and resolves it to a terminal state (`orphan` → `stale` with a `reconciled-by-user` diagnostic). **No process is touched.**

Out of scope (future):
- **Kill of an orphan** — a destructive lifecycle operation requiring its own contract/security review of identity-verification sufficiency (see Future work).

### User-facing explanation

- `stale` → "Stopped (not tracked)" / «Остановлен (не отслеживается)» — hint: start a new instance. The diagnostic may note the reason: process not found vs. identity unconfirmed.
- `orphan` → "May still be running outside GoAl" / «Может выполняться вне GoAl» — action: "Dismiss" / «Отклонить» (safe reconciliation). No kill in the first scope.
- Internal states map to plain labels (no STALE jargon). This also feeds the Instance-History "Human-readable exit reason" item (state + exit class + user label).

### Recovery behavior on restart (sequence)

1. Load all instance entries.
2. For each instance in a transitional state (`pending|starting|running|stopping`):
   1. Probe the recorded PID for liveness (platform code in `internal/platform`).
   2. Not alive → `stale` (`recovery_reason: pid-not-found`).
   3. Alive → verify identity with the strongest available anchors (executable path + start time):
      - Confirmed → `orphan`.
      - Not confirmable → `stale` (`recovery_reason: identity-unconfirmed`).
   4. Persist the new state + diagnostic.
3. `orphan` and `stale` are excluded from the active list and from auto-start / concurrency accounting.
4. No process is started, stopped, or signaled during recovery.

## Alternatives considered

- **A. Keep current behavior (all → `stale`).** Rejected as the final contract (opaque). Per the owner decision, we also do **not** ship a "stale-label-only" interim — the agreed `orphan`/`stale` semantics are implemented together.
- **B. Full lifecycle reattachment (re-claim stop / exit code / ownership).** Rejected — infeasible/unsafe under the current ownership model (this ADR establishes that).
- **C. Identity-verified orphan detection + restart reconciliation + safe Dismiss (chosen).** Platform-honest, safe, and gives users actionable clarity without destructive risk.

## Consequences

### Positive
- Users distinguish "gone" (`stale`) from "possibly still running, unowned" (`orphan`); uncertainty is explicit.
- No unsafe auto-kill; no reattachment the OS cannot provide; the identity contract prevents PID-reuse misattribution.
- First scope is safe: detection + state + Dismiss only.

### Negative
- New state + platform detection + Dismiss action + a diagnostic field = non-trivial scope.
- Start-time probing is platform-specific and may be unavailable (notably Windows); the fallback must stay conservative.
- Detection is isolated in `internal/platform` and needs Windows + Linux integration coverage.

## Acceptance contract (must hold before this is considered done)

1. PID gone → `stale` (`pid-not-found`); excluded from the active list; no process touched.
2. PID alive + identity confirmed (strongest available) → `orphan`; user-visible; **no** automatic action.
3. PID alive + identity not confirmable → `stale` (`identity-unconfirmed`); no ownership claimed; no action.
4. A bare PID (no executable path / start time) never yields `orphan`.
5. Auto-start and concurrency accounting never treat `orphan` or `stale` as a running slot.
6. **Dismiss** is an explicit, auth + CSRF-protected action that transitions `orphan` → `stale` (reconciled) without touching the process; audited once P0 audit logging lands.
7. No kill of any orphan exists in the first implementation (verified by API surface + tests).
8. Windows and Linux builds pass; detection is isolated in `internal/platform`.
9. Race-detector clean (recovery runs at startup, concurrent with handlers).

## Decisions recorded (owner contract agreement, 2026-08-22)

1. `orphan` is a **separate first-class runtime state** (not a UI-only sub-status of `stale`); `stale` remains terminal/historical.
2. Identity verification uses the **strongest available** combination — **PID + executable path + process start time**. Bare PID is forbidden; PID + path alone is insufficient when start-time is available. When identity cannot be reliably confirmed, apply the **conservative fallback** (no ownership claim, no destructive action, explicit uncertainty in state/diagnostics).
3. **Kill orphan is out of the first implementation scope.** First scope = orphan detection, identity verification, user-facing `orphan` state, and safe Dismiss/reconciliation. Kill is a separate future security/lifecycle item.
4. **No intermediate "stale-label-only" implementation.** The agreed `orphan`/`stale` semantics are implemented together in the future implementation task.
5. ROADMAP direction renamed from "full PID reattachment + orphan handling" to reflect the actual contract, since lifecycle reattachment after restart is infeasible/unsafe under the current ownership model.

## Future work (tracked separately)

- **Kill of an orphan (destructive lifecycle operation).** Requires a separate contract/security review establishing that identity verification is sufficient to make termination safe (PID-reuse risk, start-time availability, privilege). Not part of ADR 005's first implementation scope.

## Implementation status

**Not started.** Per the ADR process the status is **Proposed** (decision made, implementation pending); the owner contract agreement is recorded (2026-08-22). Implementation is the next step and, when it begins, transitions this ADR to **Accepted** (being implemented). The full agreed `orphan`/`stale` semantics are implemented together (no partial "stale-only" step). No production code is changed by this ADR.

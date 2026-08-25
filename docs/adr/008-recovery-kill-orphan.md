# ADR 008: Recovery — Kill of an Orphan (Destructive Termination)

**Status:** Proposed (owner contract agreed 2026-08-26 — all six decisions recorded in §Owner contract decisions)
**Date:** 2026-08-25
**Related:** ADR 001 (Process Ownership — Windows Job Object / Linux process group), ADR 002 (Supervisor & Instance Model), ADR 005 (Recovery — Identity-Verified Orphan Detection and Restart Reconciliation; kill explicitly out of first scope), ADR 007 (Audit Logging — event taxonomy), ROADMAP P0 "Recovery: kill of an orphan (destructive)"

## Context

ADR 005 (Accepted, implemented 2026-08-23) shipped identity-verified orphan detection, the first-class `orphan` state, and a **safe Dismiss** flow (`orphan` → `stale`, reconciled-by-user, **no process touched**). ADR 005 §"Future work" defers the destructive operation:

> **Kill of an orphan (destructive lifecycle operation).** Requires a separate contract/security review establishing that identity verification is sufficient to make termination safe (PID-reuse risk, start-time availability, privilege). Not part of ADR 005's first implementation scope.

ROADMAP P0 records this as a separate item that "requires its own contract/security review (ADR) of identity-verification sufficiency before implementation." This ADR is that review. It defines the contract under which terminating an orphan process is safe, records the owner decisions, and fixes the post-kill lifecycle contract.

### Why kill is needed at all (product motivation)

An `orphan` is a process that "may still be running outside GoAl." Dismiss reconciles the **record** (→ `stale`) but leaves the **process** running: it keeps holding its port, memory, GPU/VRAM, and file handles. For a manager whose job is to free resources when a user is done with a model, "Dismiss" alone is insufficient — the user must be able to actually stop a runaway/orphaned inference server. Kill closes that gap.

## The core security problem: PID is not an identity

Termination on both supported platforms is **PID-addressed**. There is no OS API to "terminate the process I verified a moment ago" — the OS only knows PIDs, and **PIDs are reused** (recycled) by the kernel. Therefore any kill has a **TOCTOU (time-of-check-to-time-of-use) window**:

1. We verify identity of PID `N` (executable path + start time match the recorded orphan).
2. Between step 1 and the kill, PID `N` exits.
3. The kernel reuses PID `N` for an **unrelated** process.
4. A kill addressed to PID `N` now hits the wrong process.

A check-then-kill with no re-verification at kill time is therefore **unsafe**. This is the central constraint this ADR resolves.

### What the platform layer actually provides (baseline, verified in `internal/platform`)

`RecoveryProber` (recovery.go) exposes, for a given PID:
- `IsProcessAlive(pid)` — liveness (Windows `OpenProcess`; Unix `kill(0)`).
- `GetProcessIdentity(pid)` → `{ExecutablePath, StartTime, HasStartTime}`:
  - **Executable path:** Windows `QueryFullProcessImageNameW` (needs `PROCESS_QUERY_INFORMATION`); Unix `/proc/PID/exe` readlink.
  - **Start time:** Windows `GetProcessTimes` (creation time); Unix `/proc/PID/stat` field 22 (clock ticks since boot, converted with `/proc/stat` btime). `HasStartTime=false` when the anchor is unavailable.

Key platform facts that shape the contract:

- **No handle-based kill exists for prior-session processes** (ADR 005 key constraint). After a restart GoAl does not hold the orphan's Job Object (Windows) or process-group parentage (Unix), so termination can only be PID-addressed.
- **Windows query vs terminate are separate rights.** Identity verification opens with `PROCESS_QUERY_INFORMATION`; termination needs `PROCESS_TERMINATE` via `OpenProcess` + `TerminateProcess`. These are independent access rights and can succeed/fail independently (different user, session, protected process, or insufficient privilege). `TerminateProcess` is **immediate** (no graceful signal concept for arbitrary processes).
- **Unix signaling is privilege-scoped by UID.** Same-user processes can be signaled without privilege; a different-UID orphan cannot be signaled (EPERM). Unix has a graceful path: `SIGTERM`, then escalate to `SIGKILL` (mirrors ADR 001's owned-process model).
- **Start-time availability is not guaranteed.** `HasStartTime` can be `false` (permission-limited `GetProcessTimes`, unparseable `/proc/stat`). When it is `false`, identity reduces to PID + executable path, which is **insufficient** for a destructive kill (see §Identity re-verification).

## Owner contract decisions (agreed 2026-08-26)

The six decisions below were put to the owner and are the contract for implementation. They replace the draft "Decisions required" list.

1. **Identity re-verification before EVERY destructive OS action (D1).** Re-verification is mandatory immediately before each of: the first signal (`SIGTERM`), and the escalation signal (`SIGKILL` / `TerminateProcess`). A kill proceeding on PID alone (without a current identity match) is forbidden. If the start-time anchor is unavailable (live probe `HasStartTime=false`) or mismatched at any re-verification point, the kill **MUST be refused** — never downgraded to path-only.
2. **Unix termination policy (D2).** `SIGTERM` → bounded grace (**5s**, fixed in first scope) → re-verify identity → `SIGKILL` only if the process is still alive **and** the identity still matches. If the process is already gone after `SIGTERM` (grace-period exit), the kill **succeeds** and no `SIGKILL` is sent. The `SIGKILL` decision is re-based on a fresh probe, never on the earlier verification.
3. **Windows termination policy (D3).** `TerminateProcess` after a single identity re-verification. There is no graceful phase (no portable graceful-termination primitive for an arbitrary non-owned process; faking one, e.g. `WM_CLOSE`, is unreliable and out of scope). Query (`PROCESS_QUERY_INFORMATION`) and terminate (`PROCESS_TERMINATE`) rights are treated as independent: a verification success does not imply termination will succeed, and vice versa.
4. **Audit (D4).** One event, `instance.kill` — no split taxonomy (no separate `instance.kill.refused` / `instance.kill.terminated`). The event carries bounded, secret-safe detail: `instance_id`, `outcome` (`terminated | reconciled | refused`), `reason` (bounded vocabulary, §Audit). Fail-open per ADR 007: an audit-write failure is reported operationally (slog) and never fails or alters the kill outcome. The event is additive to the ADR 007 taxonomy (one new constant, analogous to the existing `instance.dismiss`).
5. **Residual TOCTOU (D5).** The PID-recycling window between the final re-verification and the syscall is **irreducible** for PID-addressed termination. The contract does NOT claim "sub-millisecond" as a guarantee; it states that re-verification **minimizes** the window and that a mis-kill requires PID **and** executable path **and** start time to all collide on an unrelated process in that window. The residual is an accepted, documented risk (§Security consequences).
6. **Scope guard (D6).** Kill is an **explicit user action only** (auth + CSRF-protected endpoint, destructive-confirmed UI). It acts **only** on instances in the `orphan` state. It is **never automatic** — no auto-kill at startup, during recovery/reconciliation, via Dismiss, or via any cleanup path. Kill does not re-attach ownership (§Post-kill lifecycle).

## Decision

**Killing an orphan is permitted only when full identity is re-verified at kill time, immediately before each destructive syscall, using strict anchors (path + start time, both mandatory). Otherwise the kill is refused, the `orphan` state is preserved, and a bounded diagnostic is returned. No best-effort or PID-only kill exists.**

### Identity re-verification at kill time (the safety core)

Immediately before sending each termination signal (in the same code section, shortest possible gap to the syscall), the prober re-probes the PID:

- **Strict anchor requirement (kill-specific):** executable path **equal**, AND start time **equal** (within the existing 5s comparison window), AND start time **available** on both sides (recorded `StartedAt` non-zero and live probe `HasStartTime=true`).
- **Kill verification is stricter than detection verification.** The existing detection-time `verifyIdentity` (supervisor.go) is deliberately lenient: it accepts path-only when a start time is unavailable, because detection only needs to *classify* a record. Kill must not reuse that leniency. Implementation adds a kill-specific strict check (e.g. `verifyIdentityForKill`); a kill request **cannot** proceed on PID + executable path when a start time would be required.
- **Missing or mismatched start time → refuse** (conservative, per D1). The orphan is left in place with diagnostic `identity-unconfirmed`.
- **Liveness re-check:** if the PID is no longer alive at kill time, there is nothing to kill; the instance is reconciled to `stale` (`pid-gone`), no signal sent (Case E, §Post-kill lifecycle).
- The re-verification and the signal are **atomic with respect to GoAl's own view**: no other GoAl code path may start/stop an instance with the same PID in between (single-writer supervisor, already the case).

Rationale: re-verification at each destructive moment shrinks the TOCTOU window to the gap between the last identity read and the `kill`/`TerminateProcess` syscall. It does **not** eliminate the window (D5); it makes a mis-kill a vanishingly improbable compound event (PID + path + start time collision), and strictly safer than detection-time-only verification.

### Platform termination mechanics

- **Unix (D2):** `SIGTERM` to the verified PID; wait up to a bounded grace period (**5s**, fixed in first scope) polling liveness; if exited within the grace → success (`method: sigterm`), no `SIGKILL`. If still alive at grace end → **re-verify identity** → `SIGKILL` only if identity still matches → confirm exit (Case B). If identity no longer matches at escalation → stop, no `SIGKILL` (Case C). Rationale: mirrors ADR 001's owned-process graceful-then-force model; gives inference servers a chance to flush/release cleanly.
- **Windows (D3):** `OpenProcess(PROCESS_TERMINATE)` + `TerminateProcess` (immediate) after one re-verification. A verification success does not guarantee termination success (independent rights); `OpenProcess`/`TerminateProcess` failure is an explicit `insufficient-privilege` refusal, never a success.
- **Privilege failure is not success.** If the OS denies the terminate right (EPERM / `OpenProcess(PROCESS_TERMINATE)` failure / access denied), the kill **fails** with diagnostic `insufficient-privilege` (Case F); the `orphan` state is preserved; no success is reported.

### Scope boundary

- Kill acts **only** on instances in the `orphan` state (D6). It never touches `stale`, `exited`, `failed`, or owned live instances (they keep ADR 001/002 stop semantics). A request against any non-orphan state is a **conflict** (409).
- Kill is a **user-initiated, explicit** action — never automatic (D6).
- Kill does **not** re-attach ownership: after a successful kill the instance is reconciled to a terminal `stale` with `recovery_reason: killed-by-user`; it is not re-adopted into the active set.

### Post-kill lifecycle contract

**Governing principle (owner):** the orphan may not be declared eliminated merely because a destructive OS API returned success. A successful transition to a terminal state must rest on a **factually confirmable process state** within the capabilities of the existing recovery model (liveness + identity probes). Where confirmation is impossible, the contract says what is said — and it is never "killed."

**No new state is introduced.** `stale` remains the terminal reconciliation state for records whose process lived outside GoAl (ADR 005 model; same target as Dismiss). The existing exit-class vocabulary already carries the semantics: `InstanceExitClass` includes `killed`. Persistence uses the existing durable store contract (ADR 004/007).

**Record shape on reconciliation:**
- Confirmed kill (Cases A/B): `state=stale`, `recovery_reason=killed-by-user`, `exit_class=killed`, `stopped_at=now`, `exit_code` unset (GoAl never `Wait`s the orphan — no ownership, consistent with "each `exec.Cmd` has exactly one owner"), `last_error` empty.
- Case E (pid-gone): `state=stale`, `recovery_reason=pid-gone`, `exit_class` unset (we did not kill it; the process exited on its own), `stopped_at=now`.
- Refusals (Cases C/F): **no state transition** — the instance stays `orphan` with the same `recovery_reason` (`""`), `last_error` updated to the bounded refusal diagnostic (non-terminal, retriable; a later Dismiss still works).

**Case table** (each row: trigger → API outcome → resulting state → persistence → audit → idempotency):

| Case | Trigger | API outcome | Resulting state | Persistence | Audit `instance.kill` detail | Idempotency |
|------|---------|-------------|-----------------|-------------|------------------------------|-------------|
| **A** | Unix: identity re-verified; `SIGTERM` sent; liveness probe confirms PID gone within the 5s grace | `200 {"status":"killed","method":"sigterm"}` | `stale` | `killed-by-user`, `exit_class=killed`, `stopped_at=now` (durable write) | `outcome=terminated`, `reason=sigterm` | Repeat request → `409` (no longer orphan) |
| **B** | Unix: alive at grace end; identity re-verified **again**; `SIGKILL` sent; liveness probe confirms PID gone. Windows: identity re-verified; `TerminateProcess` succeeds; liveness probe confirms PID gone | `200 {"status":"killed","method":"sigkill"}` / `"terminateprocess"` | `stale` | as Case A | `reason=sigkill` / `reason=terminateprocess` | Repeat → `409` |
| **C** | After `SIGKILL` (Unix) or `TerminateProcess` (Windows), the bounded confirmation probe cannot establish that the PID is gone (still visible), **or** the escalation re-verification (D2) failed / the probe errored | `500 {"error":"kill outcome unconfirmed: <bounded diagnostic>","code":"internal"}` — **no success reported** | `orphan` **preserved** (retriable) | `state=orphan`; `last_error="kill-outcome-unconfirmed"` (durable) | `outcome=refused`, `reason=unconfirmed` | Retry allowed (still orphan). **Never** reconciled to `stale` on unconfirmed termination |
| **D** | OS terminate call itself failed before any confirmation (EPERM / `OpenProcess(PROCESS_TERMINATE)` access denied) | `403 {"error":"kill refused: insufficient privilege","code":"forbidden","reason":"insufficient-privilege"}` | `orphan` **preserved** | `state=orphan`; `last_error="insufficient-privilege"` (durable) | `outcome=refused`, `reason=insufficient-privilege` | Retry allowed (state unchanged; may still fail) |
| **E** | At kill time (initial re-verification), the PID is **not alive** — the process exited on its own | `200 {"status":"reconciled","reason":"pid-gone"}` — nothing was killed; the record is reconciled honestly | `stale` | `pid-gone`, `exit_class` unset, `stopped_at=now` (durable write) | `outcome=reconciled`, `reason=pid-gone` | Repeat → `409` |
| **F** | At any mandatory re-verification point, identity is unconfirmed: path mismatch, start-time mismatch, or start-time unavailable (D1) | `409 {"error":"kill refused: identity unconfirmed","code":"conflict","reason":"identity-unconfirmed"}` | `orphan` **preserved** | `state=orphan`; `last_error="identity-unconfirmed"` (durable) | `outcome=refused`, `reason=identity-unconfirmed` | Retry allowed (may succeed if the process state was transient) |
| **G** | Instance is no longer `orphan` at request time (dismissed, already killed, or reconciled in the meantime) / instance does not exist | `409 {"error":"instance not in orphan state (current: <state>)"}` / `404 {"error":"instance not found"}` | unchanged / — | unchanged / — | **no audit event** (precondition failure, not a kill attempt; same pattern as the existing `dismiss` handler) | N/A |

Notes:
- **`pid-gone` before the first signal is not a kill.** Reporting it as `terminated` would violate the governing principle; `reconciled` keeps the record honest (the resource is free because the process exited itself, not because GoAl terminated it).
- **Case C is the anti-false-success row.** It is deliberately a *preserved orphan*, not a terminal state: declaring elimination without confirmation is exactly what the principle forbids. A subsequent kill attempt re-verifies from scratch; a subsequent Dismiss remains available.
- **Refusals are persisted diagnostics, not transient log lines:** `last_error` on the `orphan` record tells the user *why* the kill was refused and keeps the record self-describing across restarts (the record is the source of truth, not chat/logs).
- **Bounded vocabulary.** `reason` values: `sigterm`, `sigkill`, `terminateprocess`, `pid-gone`, `identity-unconfirmed`, `insufficient-privilege`, `unconfirmed`. `outcome` values: `terminated`, `reconciled`, `refused`. All detail values are bounded strings or the instance ID — no command lines, environment, tokens, or other secret-bearing data (ADR 007 §secret-safety).

### Audit (ADR 007 taxonomy extension)

One event, `instance.kill` (D4), emitted for every kill request that passes the state precondition (Cases A–F). Detail keys: `instance_id`, `outcome`, `reason` (bounded, table above). Fail-open per ADR 007: a failed audit write is reported via slog (event name + raw I/O error only) and does not change the kill outcome. This is a small additive extension to the ADR 007 taxonomy: one new constant (`EventInstanceKill = "instance.kill"`), listed alongside `instance.dismiss`.

### API & UI

- `POST /api/v1/instances/{id}/kill` — auth + CSRF protected (same middleware as `dismiss`).
- Outcome mapping (existing `writeError`/`writeAPIError` model; codes from the existing vocabulary): `200 killed|reconciled`, `409 conflict` (identity-unconfirmed / not-in-orphan), `403 forbidden` (insufficient-privilege), `500 internal` (outcome-unconfirmed), `404 not_found`, `400 bad_request` (missing ID).
- The UI presents kill as a **destructive, explicitly confirmed** action (distinct from the safe Dismiss), displaying the identity anchors that will be re-verified and the platform consequence (immediate on Windows / graceful-then-force on Unix), and rendering the `last_error` refusal diagnostic on the instance card.

## Alternatives considered

- **A. Keep Dismiss-only; no kill.** Safe, but leaves orphan processes running (port/memory/VRAM the user cannot clear). Rejected as the final state — it defeats the product goal of freeing resources. (It remains the always-available safe fallback and the path on every refusal.)
- **B. PID-only kill (no re-verification).** Rejected — unsafe (PID reuse), directly contradicts ADR 005's identity contract and owner decision D1.
- **C. Check-then-kill with detection-time identity only.** Rejected — the TOCTOU window is unbounded and exploitable by PID recycling; detection-time verification also uses the lenient (path-acceptable) rule, which D1 forbids for destructive actions.
- **D. Reuse the lenient detection-time `verifyIdentity` for kill (path-only when start time missing).** Rejected per D1 — a destructive action must not accept a weaker anchor than a classification decision; kill gets a strict verifier.
- **E. Introduce a new terminal state `killed`.** Rejected — `stale` already serves as the terminal reconciliation state for out-of-GoAl processes (ADR 005), and the existing `exit_class=killed` vocabulary already distinguishes the cause. A new state would ripple through `IsTerminal`, the active list, concurrency accounting, UI, and docs for zero behavioral gain.
- **F. Identity re-verification at each destructive moment + confirmable-exit transitions (chosen).** Safest PID-addressed approach available under the no-reattachment constraint; residual risk explicitly bounded and documented (D5).

## Consequences

### Positive
- Users can actually terminate runaway/orphaned inference servers, closing the resource-leak gap Dismiss leaves.
- The kill is strictly safer than detection-time-only verification; the identity contract is enforced at the destructive moment, not just at detection.
- No false success: every terminal transition is backed by a confirmable process state (Case C preserves the orphan on unconfirmed termination; Case E says `reconciled`, not `killed`).
- Privilege/availability failures are explicit, persisted on the record, and never silently reported as success.
- Additive and auditable: one new endpoint, one new audit event, no change to owned-process (ADR 001/002) stop semantics, no new state.

### Negative / accepted risk
- **Residual TOCTOU (D5):** a PID can still be recycled in the (unbounded, implementation-dependent) gap between final re-verification and the syscall. Accepted as vanishingly improbable (requires PID + executable path + start time to all coincide on an unrelated process in that window), and strictly better than any PID-addressed alternative. Documented, not eliminated. The contract makes no "sub-millisecond" guarantee.
- **Start-time dependence (D1):** on hosts where the start-time anchor is unavailable (`HasStartTime=false`, e.g. permission-limited probing), kill is **refused** (conservative). Kill may therefore be unavailable in some permission-limited environments; Dismiss remains the always-available safe path.
- **Windows immediacy (D3):** `TerminateProcess` is forceful (no graceful flush). Inference servers that need a clean shutdown get none on Windows. Documented.
- **Case C (unconfirmed termination) leaves the record `orphan`:** a force-killed process that lingers in the OS view (e.g. uninterruptible state) keeps the record actionable rather than falsely closed; the user can retry kill or Dismiss.
- New destructive endpoint + privilege surface + audit extension = meaningful review/test scope (Windows + Linux integration coverage of kill, re-verification, privilege denial, and PID-reuse simulation where feasible).

## Security consequences

- **Threat: PID-reuse mis-kill.** Mitigation: strict re-verification immediately before **each** destructive syscall (D1/D2); the residual window is acknowledged irreducible (D5) and bounded in impact (requires a triple anchor collision). Accepted risk.
- **Threat: weak-anchor escalation (path-only kill).** Mitigation: kill-specific strict verifier (D1); code path + tests prove no PID-only kill exists (acceptance 4).
- **Threat: privilege escalation / cross-user kill.** Out of scope by design: the process is only signalable when the OS already permits it (same-UID on Unix; rights on Windows); denial is an explicit `insufficient-privilege` refusal (Case D), never a retry-loop or escalation attempt.
- **Threat: audit tampering/loss.** Governed by ADR 007 (per-event fsync, fail-open, secret-safe payload). `instance.kill` carries no command line, environment, or secret-bearing data.
- **Threat: automated misuse of the destructive path.** Mitigated by D6: explicit user action only, auth + CSRF, destructive-confirmed UI; no code path invokes kill automatically.

## Acceptance contract (must hold before this is considered done)

1. Kill on an identity-verified orphan (full strict anchors re-verified at kill time) terminates the process and reconciles the instance to terminal `stale` with `recovery_reason=killed-by-user`, `exit_class=killed`, durable persistence; excluded from the active list; audited `instance.kill {outcome:terminated}`.
2. Kill is **refused** (no signal sent) and the orphan is **preserved** when, at a re-verification point: identity is unconfirmed (path mismatch, start-time mismatch, or start-time unavailable) → Case F (`409 identity-unconfirmed`, `last_error` persisted, audited `refused/identity-unconfirmed`).
3. Kill on a PID that is no longer alive at kill time sends **no signal**; instance reconciled to `stale` with `recovery_reason=pid-gone` and **unset** `exit_class`; API `200 {"status":"reconciled","reason":"pid-gone"}`; audited `reconciled/pid-gone` (Case E).
4. **No PID-only kill exists:** a kill request cannot proceed on PID + executable path when a start time is required (kill-specific strict verifier; verified by code-path review + tests, including a `HasStartTime=false` fixture that must refuse).
5. Unix: `SIGTERM` first, bounded 5s grace, exit within grace → success with **no** `SIGKILL` (Case A); `SIGKILL` escalation **only after** a fresh re-verification that still matches (Case B); escalation re-verification failure stops the sequence with no `SIGKILL` (Case C/F).
6. Windows: single re-verification then immediate `TerminateProcess` (Case B); no graceful phase; query and terminate rights handled independently — verification success does not imply termination success.
7. Privilege denial (EPERM / `OpenProcess(PROCESS_TERMINATE)` failure) → Case D (`403 insufficient-privilege`), orphan preserved with persisted `last_error`, **no** success reported, audited `refused/insufficient-privilege`.
8. **No false success:** an unconfirmable termination (process still visible after the force stage, or confirmation probe error) → Case C (`500 unconfirmed`), orphan **preserved** (retriable), never reconciled to `stale`; audited `refused/unconfirmed`.
9. Kill is never automatic; it is an explicit, auth + CSRF-protected user action; destructive-confirmed in the UI (distinct from Dismiss).
10. Kill never touches `stale`/`exited`/`failed`/owned-live instances (they keep ADR 001/002 semantics); non-orphan request → `409`; missing instance → `404` (Case G); **no audit event** for Case G.
11. ADR 005 first-scope invariants still hold (no reattachment; Dismiss still safe/no-touch; orphan/stale excluded from concurrency accounting; recovery classification unchanged).
12. Audit: exactly one `instance.kill` event per kill attempt that passes the state precondition; bounded detail keys `instance_id`/`outcome`/`reason` with the fixed vocabulary; no secret-bearing data; fail-open (audit write failure does not change the kill outcome); ADR 007 taxonomy note updated.
13. Persistence: every reconciliation and every persisted `last_error` refusal goes through the durable store contract (survives restart; a restart recovery does not resurrect a reconciled record — recovery only classifies transitional states).
14. Idempotency: after Cases A/B/E the instance is `stale`; a repeat kill → `409 not in orphan state`; after Cases C/F/D the instance is still `orphan` and a repeat kill is a fresh attempt (re-verify from scratch).
15. Layering: kill primitives in `internal/platform` (prober/terminate behind an interface, fakeable in tests); state machine in `internal/process` (supervisor); endpoint in `internal/webui` (handler → `application.InstanceService` → supervisor — mirrors `DismissOrphan`); HTTP handlers never manage `exec.Cmd`.
16. Windows and Linux builds pass; kill + re-verification isolated in `internal/platform`; race-detector clean; `gofmt`/`go vet` clean.
17. Dismiss remains the always-available safe fallback and works on every refused kill (orphan state preserved).

## Future work (tracked separately)

- Cross-platform **PID-reuse simulation** test harness (deterministically force a PID recycle between re-verification and kill) to exercise the residual path where the OS allows it.
- Optional: per-instance kill grace/timeout configuration (currently fixed 5s).
- Optional: `kill` detail enrichment (e.g. method duration) — only if bounded and secret-safe.

## Implementation status

**Not started.** The design gate is complete (owner contract agreed 2026-08-26). Implementation follows the standard flow: contract agreement (done) → implementation (kill-specific strict verification + platform terminate primitives in `internal/platform`, supervisor kill path with the Case A–G transitions, `POST /api/v1/instances/{id}/kill` handler, `instance.kill` audit event constant) → tests (unit + Windows/Linux integration, fake prober fixtures per case) → acceptance (this contract) → documentation reconciliation (API.md new endpoint; SECURITY.md kill + TOCTOU residual; USER_GUIDE EN/RU destructive action; ADR 007 taxonomy note; ADR 005 "Future work" pointer; ROADMAP L28 → `[x]`).

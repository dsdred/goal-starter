# ADR 024: Commands — unified control-command dispatch (Stage 1, `model.start`)

**Status:** **Accepted** (Owner ratification 2026-11-17). Decisions **D-1 … D-6** ratified as proposed; **D-7 amended** (no new `command.execute` audit in Stage 1 — unified control-action audit moved to the separate non-blocking follow-up [BACKLOG.md](../../BACKLOG.md) **G3-AUDIT-01**; no existing audit disabled or bypassed). Per [../DEVELOPMENT.md](../DEVELOPMENT.md) §ADR process (`:177-199`), **Accepted** authorizes the bounded CMD-1 implementation slice (the Owner separately authorized implementation, 2026-11-17); commit / publication / release remain separate gates.

**Date:** 2026-11-17

**Depends on:** [ADR 017 — Unified Launch Arbitration](017-unified-launch-arbitration.md) (the **single** `Supervisor.AdmitAndStart` boundary this dispatch delegates to and must **not** duplicate or bypass); [ADR 016 — Durable Lifecycle Ownership](016-durable-lifecycle-ownership.md) and [ADR 021 — Lifecycle State Observability View](021-lifecycle-state-observability-view.md) (the lifecycle semantics the command leaves unchanged); [ADR 007 — Audit Logging](007-audit-logging.md) (the audit-policy decision §D-7).

**Related:** [ROADMAP.md](../../ROADMAP.md) §GoAl 3 → `#### 1 — Commands (new)`; [BACKLOG.md](../../BACKLOG.md) **G3-COMMANDS-01** (this slice), **G3-COMMANDS-02 / -03** (Stages 2/3 — out of scope here), **G3-TOKENS-01**, **G3-MCP-01** (Stage 3 prerequisites).

**Numbering:** `docs/adr/` holds `0001`, `002`–`011`, `013`, `014`, `016`–`023` at baseline (`012` never materialized; `015` is a roadmap-referenced gate with no file). **024 is the next free number** and the only ADR file added by this gate.

**Baseline for every file:line reference below:** `8fb80be7d32018778438ebdee1ea6bb2b5302722` (`HEAD == origin/main` at the time of writing). The cited routes, service methods and error tokens — not the line numbers — are the contract.

## Context

### What is being contracted

GoAl 3 priority **#1 — Commands** ([ROADMAP.md](../../ROADMAP.md) §GoAl 3, 2026-11-16) introduces a **unified command-execution surface** over the existing services: a command *describes* and *initiates* an action and *delegates* execution to the service that already performs it. The first slice (**CMD-1**, [BACKLOG.md](../../BACKLOG.md) **G3-COMMANDS-01**) is the thinnest possible vertical: **one command (`model.start`), one dispatch point, delegation to the existing `InstanceService.StartModel`, a minimal API contract, and tests.** It exists to prove the "describe + delegate, do not duplicate" pattern — no second supervisor, no duplicated ADR 017 arbitration, no duplicated ADR 016 / 021 lifecycle.

### Why this is ADR-first (the significance test)

[AGENTS.md](../../AGENTS.md) gates **significant work** behind `ROADMAP → ADR → contract → implementation → tests → acceptance → docs reconciliation`, and lists **"public API"** and **cross-cutting architecture** among the triggers. CMD-1 is a **new public HTTP endpoint** (`POST /api/v1/commands/execute`) and the **seed of a cross-cutting command surface** on which Stage 2 (a persisted command entity) and Stage 3 (agent commands over MCP + scoped tokens) will build. The command *model, vocabulary, error normalization and audit semantics* are therefore a durable contract, not a one-off. The [BACKLOG.md](../../BACKLOG.md) **G3-COMMANDS-01** row already records this: the full Commands contract is "**class E**, scoped minimally, and **opened only when this first slice is authorized for implementation**."

### Present bytes (measured, not recalled)

| Surface | Coordinate | Present value |
|---|---|---|
| Existing `model.start` route | `internal/webui/handlers/routes.go:193` | `POST /api/v1/models/{id}/start` → `requireAuthCSRF(r.modelHandler.Start)` |
| `model.start` handler | `internal/webui/handlers/models.go:236` | `Start`: pre-check for an in-flight launch of the model (409 `launch_in_flight`), then `instanceSvc.StartModel`, then `writeJSON(200, domain.ToStorageEntry(inst))` with `inst.Environment = nil` |
| Service delegation | `internal/application/instance_service.go:29` | `StartModel` resolves model+runtime and calls `supervisor.AdmitAndStart(ctx, model, runtime, domain.ManualOwner, nil, nil)` |
| The single arbitration boundary | `internal/process/arbitration.go:154` | `Supervisor.arbitrate` — the ADR 017 linearization point every launch/restart passes; refuses `pending`/`starting`/`stale`/`orphan`, admits `running`/`stopping`/`terminal` |
| Lifecycle error → HTTP | `internal/webui/handlers/helpers.go:89` | `writeLifecycleError`: `ErrLaunchInFlight`/`RejInFlight` → 409 `launch_in_flight`; `ErrNotRestartable` → 409 `not_restartable`; `RejOrphan` → 409 `orphan`; `ErrInstanceNotFound` → 404 `instance_not_found`; `RejShuttingDown`/`ErrLaunchAbortedByShutdown` → 503; persistence/termination/rollback → 500 |
| **Non-existent model today** | `instance_service.go:31` → `helpers.go:92` | `StartModel` returns a plain `fmt.Errorf("model not found: …")`; `lifecycleAPIError` matches no sentinel ⇒ `writeLifecycleError` falls through to **500** with the raw message |
| **`model.start` audit today** | `models.go:236-256` | **none** — the model route's Start emits **no** `logAudit` call (audit constants exist for `instance.*` / `pipeline.*`, `audit.go:55-64`, but model-route lifecycle is unaudited) |
| Internal (not user) command type | `internal/domain/command.go` | `CommandSpec` / `LaunchResolver` are **internal launch-resolution** (executable/args/env/working-dir) — **not** a user command entity; must not be confused with this ADR's command |

## Decision

The command is a **thin dispatch primitive at the HTTP edge**. It names an action and routes it to the service that already performs it. The sealed invariants and the CMD-1 contract are **D-1 … D-7**.

- **D-1 — Thin dispatch, not a controller.** A new `CommandsHandler` (HTTP edge) maps a command name to an existing application-service method. It holds **no** `*process.Supervisor` reference, **no** lifecycle logic and **no** arbitration code of its own — the entire lifecycle is reached only by delegating to `InstanceService.StartModel` (→ `Supervisor.AdmitAndStart`). This is the single non-bypassable boundary: **there is no second supervisor, no second arbitration path, no lifecycle duplication.**

- **D-2 — `POST /api/v1/commands/execute`, `requireAuthCSRF`.** One mutating route for all Stage 1 commands, wrapped by the same `requireAuthCSRF` boundary every other mutating route uses ([routes.go](../../internal/webui/handlers/routes.go) `:283`). A command executes with the caller's own session + CSRF token; there is no weaker or separate auth path, and no LAN exposure.

- **D-3 — Closed Stage 1 vocabulary, exact-match.** The Stage 1 command set is exactly **{ `model.start` }**. Matching is exact, case-sensitive string equality; any other `command` value is an **unknown command** (D-6). No `model.stop` / `model.restart` / `pipeline.*` in this slice — they are added by a later, separately-authorized Stage 1 completion and each re-uses the same dispatch point.

- **D-4 — Body & identifiers.** Request body is `{ "command": string, "targetId": string }`. `targetId` is the **model ID** for `model.start` — the same identifier the existing route carries in its path. An empty `command` or empty `targetId` is a **malformed request** (400). The command carries **no** other parameters in Stage 1.

- **D-5 — Success & lifecycle errors are identical to `model.start`.** On success the endpoint returns the **same** `200` body the existing `model.start` returns — the started instance entry (`domain.ToStorageEntry(inst)`, environment nulled). Every lifecycle failure is mapped through the **existing** `writeLifecycleError` classifier, so `model.start` via `/commands/execute` yields the **same** status + `code` as `model.start` via `/models/{id}/start`: 409 `launch_in_flight`, 409 `orphan`, 503 `shutting_down` / `launch_aborted`, 500 `launch_persist_failed` / `termination_unconfirmed` / `rollback_failed`. **No lifecycle code path is re-implemented; the command adds zero lifecycle semantics.**

- **D-6 — Command-surface errors are new bounded codes (the one place the surface differs).** Two conditions are inherent to the command surface and have no `model.start` analogue, so they get their own bounded tokens rather than leaking a 500:
  - **unknown command** → `400` `{ "code": "unknown_command", "error": … }`. A new `CodeUnknownCommand` bounded code (the closed set of Stage 1 commands is the reference; the message names the rejected value, never a stack trace).
  - **unresolvable target** (the model does not exist) → `404` `{ "code": "not_found", … }`. This is a **deliberate improvement** over the current `model.start` behavior for a non-existent model, which today falls through to **500** (see "Present bytes"). It changes **only the new endpoint's** error classification: the existing `POST /api/v1/models/{id}/start` route and `InstanceService.StartModel` are **not** modified, so `model.start` semantics on the existing route are unchanged (constraint respected). The command layer re-runs the same resolution and reports the not-found as a bounded 404 before delegating.
  - A new bounded code for unknown command, and the 404 reclassification, are **additive**: they touch only the new handler's error path and the shared `errors` code set (one new constant). No existing route's mapping changes.

- **D-7 — No new audit event in Stage 1; unified control-action audit is a separate non-blocking follow-up (amended 2026-11-17).** CMD-1 introduces **no** `command.execute` audit of its own. `model.start` via `/commands/execute` carries the **same** audit behavior as `model.start` via `/models/{id}/start` today — which is **none** (see "Present bytes": the model route emits no `logAudit`). Rationale (Owner decision 2026-11-17): the **same** control action must have **one** audit semantic regardless of entry point; auditing only the new endpoint would fork that semantic, so the unified control-action audit is **not** introduced piecemeal in this slice. It is registered as a **separate non-blocking follow-up** ([BACKLOG.md](../../BACKLOG.md) **G3-AUDIT-01**), which will give every control action one entry-point-independent ADR 007 record. CMD-1 **does not disable or bypass any existing audit**: the `instance.*` / `pipeline.*` ADR 007 events (`audit.go:55-64`) are untouched. This is the strict-parity form of the original D-7 proposal; the dispatch (D-1…D-6) is unchanged.

## Out of scope (Stage 2 / Stage 3 — each its own ADR, NOT this slice)

- **Stage 2 (User Commands)** — persisted command entity, parameters, sequences, reuse ⇒ new domain entity + persistence ⇒ separate class-E ADR ([BACKLOG.md](../../BACKLOG.md) **G3-COMMANDS-02**).
- **Stage 3 (Agent Commands)** — external AI agents over API/MCP authorized by scoped tokens ⇒ new auth/security model ⇒ separate ADR, depends on **G3-TOKENS-01** + **G3-MCP-01** (**G3-COMMANDS-03**).
- **Not added here:** API tokens, MCP, `model.stop` / `model.restart` / `pipeline.*` commands, idempotency keys, batch/multi-target commands, any change to the existing per-object routes.

## Security analysis (no bypass)

- **Auth:** identical `requireAuthCSRF` boundary as every other mutating route; no new trust relationship, no unauthenticated or LAN-exposed path.
- **CSRF:** command execution is a state-changing POST behind the session CSRF token — the same protection as `model.start`.
- **Arbitration / lifecycle:** reached **only** through `InstanceService.StartModel` → `Supervisor.AdmitAndStart` (ADR 017). The command layer never calls the supervisor, never mints a spawn claim, and never short-circuits the conflict/orphan/shutdown checks.
- **Error surface:** bounded tokens only (D-5/D-6); no raw process text, no internal detail, no new information channel.

## Acceptance (obligations for the implementation slice, NOT run here)

- **C-1** `POST /api/v1/commands/execute` with `model.start` + a valid, startable `targetId` returns the same 200 instance-entry body as `POST /api/v1/models/{id}/start`.
- **C-2** unknown `command` ⇒ 400 `unknown_command`; empty `command`/`targetId` ⇒ 400.
- **C-3** non-existent `targetId` ⇒ 404 `not_found`.
- **C-4** an in-flight launch of the target ⇒ 409 `launch_in_flight` (same token as `model.start`).
- **C-5** unauthenticated ⇒ 401; authenticated without a valid CSRF token ⇒ 403.
- **C-6** the existing `POST /api/v1/models/{id}/start` route's behavior and its (current) unaudited/no-500-vs-404 distinction are **byte-for-byte unchanged**; `InstanceService.StartModel` and the ADR 017 boundary are **untouched**.
- **C-7** (per amended D-7) a command execution emits **no** `command.execute` audit record in Stage 1 (matching the unaudited model route), and **no** existing `instance.*` / `pipeline.*` ADR 007 audit is disabled or bypassed.

## Ratification

**Owner decision — ACCEPTED (2026-11-17).** **D-1 … D-6: ПРИНЯТЫ** (ratified as proposed), including **D-6** (the new `unknown_command` code + the 404 reclassification on the new surface). **D-7: ИЗМЕНИТЬ** — amended per the Owner's decision: CMD-1 introduces **no** separate `command.execute` audit, because the same control action must have one entry-point-independent audit semantic; the unified control-action audit is registered as a separate **non-blocking** follow-up ([BACKLOG.md](../../BACKLOG.md) **G3-AUDIT-01**), and no existing audit is disabled or bypassed. With this ADR **Accepted** and implementation separately authorized by the Owner (2026-11-17), **CMD-1 is authorized** for the bounded Stage 1 slice described here. Commit / publication / release remain separate, separately-authorized gates.

## Provenance

This ADR, [ROADMAP.md](../../ROADMAP.md) §GoAl 3 → Commands, and [BACKLOG.md](../../BACKLOG.md) **G3-COMMANDS-01** together register the GoAl 3 Commands track. Supersedes: nothing.

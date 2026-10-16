# Architecture

GoAl is a single-binary application composed of distinct ownership layers. This document describes the current GoAl 2.0 architecture.

## Domain Model (v8)

GoAl 2.0 uses a simplified 4-entity domain:

- **Runtime** — execution engine configuration (executable, working directory, environment)
- **Model** — configured launch definition (runtime reference, launch args, environment, autostart)
- **Instance** — concrete launch history (immutable record of a process launch)
- **Pipeline** — ordered group of existing Models with group lifecycle and a per-model all-or-nothing Args override (ADR 010). Persisted under the `pipelines` key (schema v8); instances it launches carry `pipeline_id`.

All launch parameters (`--host`, `--port`, `-m`, `--mmproj`, etc.) are expressed
through Model Args. There are no separate host/port fields on Model. A Pipeline never
stores launch parameters of its own — each entry either overrides the referenced Model's
Args entirely (non-empty) or defers to `Model.Args` (empty).

Physical model files (GGUF, MMProj) are NOT separate domain entities. They are ordinary
launch arguments (e.g., `-m <path>`, `--mmproj <path>`).

Relationship: Runtime ← Model → Instance; Pipeline → (ordered) Model references

## Composition root

`cmd/goal/main.go` wires all components:

```
main
├── config.Load()        → configuration
├── JSONRepository       → persistence
├── Application services → domain logic
├── Supervisor           → process lifecycle
├── HTTP handlers        → REST API
├── Embedded Web UI      → dashboard
└── Signal handler       → graceful shutdown
```

## Layer ownership

| Layer | Package | Responsibility |
|-------|---------|----------------|
| Configuration | `internal/config/` | Parse, validate, migrate, save `goal.json`. |
| Persistence | `internal/storage/` | `JSONRepository` — single-file storage with atomic writes. |
| Domain | `internal/domain/` | Type definitions, DTO conversion between storage and application layers. |
| Application | `internal/application/` | Business logic: ModelService, RuntimeService, InstanceService. |
| Process lifecycle | `internal/process/` | Supervisor, Manager, LogBroker, LogStore, SlotReservation. |
| OS behavior | `internal/platform/` | Windows Job Object, Linux process groups. |
| HTTP / UI | `internal/webui/` | Routes, handlers, embedded templates, static assets, security. |
| Security | `internal/webui/security/` | Sessions, CSRF, password store (bcrypt). |
| Errors | `internal/webui/errors/` | Structured API error codes and classification. |
| Validation | `internal/webui/validation/` | Port, host, address validation. |
| Logging | `internal/webui/logger/` | Structured JSON HTTP logger. |
| Health | `internal/webui/health/` | TCP/HTTP health checker for runtimes. |

## Data flow

```
User (Web UI / API)
    │
    ▼
HTTP handlers (internal/webui/handlers/)
    │
    ▼
Application services (internal/application/)
    │
    ├──► JSONRepository (internal/storage/) — persist entities
    │
    └──► Supervisor (internal/process/) — manage running processes
           │
           ├──► Manager (per-instance)
           │      └──► platform.Prepare() — OS-specific process setup
           │
           ├──► LogBroker — multi-instance log subscription
           └──► LogStore — per-instance ring buffer (10000 entries)
```

## Authoritative persistence

`goal_repo.json` is the single source of truth for all entities after the first startup.

**Durable write sequence** — `fsutil.WriteFileDurable` (`internal/fsutil/`), used for `goal_repo.json` (`JSONRepository.saveLocked` and `SaveUnified`) and `goal.json` (`config.Save`):
1. Serialize to `.tmp` file in the same directory
2. `fsync` the temp file (`File.Sync()`: `fsync(2)` on POSIX, `FlushFileBuffers` on Windows)
3. Verify the written bytes by reading the temp file back
4. Save the previous file as `.bak` (same durable sequence; before every write)
5. `os.Rename()` (atomic on both platforms: `rename(2)` on POSIX, `MoveFileExW` with `MOVEFILE_REPLACE_EXISTING` on Windows)
6. POSIX: `fsync` the parent directory (mandatory; a failed sync is a failed write)

**Platform guarantees:**
- **POSIX (Linux):** after step 6, file data and the rename (directory metadata) are on stable storage. Power loss at any point leaves either the complete previous state or the complete new state.
- **Windows:** file data is durable after step 2; the rename is durable when `MoveFileExW` returns, provided the volume is journaling NTFS (the rename transaction is committed to the NTFS log before the API returns). The supported Windows API model has no directory flush; on non-journaled volumes (FAT/exFAT, or NTFS with journaling disabled) the rename-durability guarantee does not hold.

Every step propagates errors: a write is reported as successful only if it is durable, and a failed write never leaves a partially written file at the target path.

**In-memory consistency:** mutating `JSONRepository` operations change in-memory state, then persist. If the persist fails, the operation restores the pre-save state (rollback) before returning the error, so memory and disk never diverge because of a failed save.

The unified JSON repository in `internal/storage/` (`JSONRepository`) is the sole persistence layer for all entities.

## Security audit trail

`internal/webui/audit/` implements the durable security audit log (ADR 007): an append-only JSON Lines file at `<dataDir>/goal_audit.jsonl` (mode `0600`). Each event is a single `O_APPEND` write of a complete line followed by `fsync`; the event is considered recorded only after `fsync` returns. Rotation to `goal_audit.jsonl.1` happens before the append that would cross 10 MiB; at most 3 generations are kept. A single mutex-protected `AuditLogger` keeps file order equal to occurrence order.

Secret safety is enforced by construction: the logger accepts only the typed `AuditEvent` built by named handler call sites (login outcomes, logout, settings saves, instance lifecycle actions); there is no generic request-logging path. Failure semantics are fail-open: a write failure never fails or rolls back the business operation, it emits a structured `slog.Error` (event name + raw I/O error only, no event payload), and the logger never latches — each subsequent event independently attempts a new write. The file is the source of truth and is read on every `GET /api/v1/admin/audit` request (no in-memory cache); a missing file (fresh install) yields an empty list, and a torn trailing line is skipped.

The `webui.App` owns the logger (created from `dataDir` in `NewApp`, closed on shutdown in `main`); the `RouteRegistry` injects it into the auth, system, and instance handlers via `WithAuditLogger`.

See [ADR 007](adr/007-audit-logging.md) and [SECURITY.md — Audit trail](SECURITY.md#audit-trail).

## Process lifecycle

See [ARCHITECTURE.md - Process Lifecycle](#process-lifecycle) section below and [LIMITATIONS.md](LIMITATIONS.md).

## Configuration seed-once

`goal.json` is imported into `goal_repo.json` at first startup via `storage.SeedFromConfig()`. Subsequent startups only add new entities (by ID); existing entities in the repository are not overwritten.

See [ADR 004](adr/004-config-vs-repository-ownership.md).

## Configuration hot-reload

Hot-reload is explicit (ADR 009): `POST /api/v1/admin/reload` (auth + CSRF) re-reads the config file via `config.LoadReadOnly()` (no side effects — a reload never writes the file), validates it, applies hot fields (`logLevel` via a hot-swappable `slog` level), and reports the restart-pending diff (`config.DiffHot`: file vs the startup-effective config). There is no file watching, SIGHUP, or polling, and the former unused `ReloadConfig` type (whose `Save()` violated the durable-write contract) was removed. Field classification (hot / restart / seed-only) is defined in the ADR and [CONFIGURATION.md](CONFIGURATION.md#hot-reload-adr-009). Every attempt emits a `config.reload` audit event (field names only).

See [ADR 009](adr/009-hot-reload-wiring.md), [ADR 004](adr/004-config-vs-repository-ownership.md), and [CONFIGURATION.md](CONFIGURATION.md#hot-reload-adr-009).

## Web UI serving

The Web UI is served from embedded filesystems (`templateFS`, `staticFS` declared in `internal/webui/server.go`). The dashboard renders `templates/index.html` from `templateFS`. Static assets are served from `staticFS` at `/static/`.

See [ADR 003](adr/003-webui-embedded-fs.md).

## Dependency direction

```
handlers → application services → domain adapters → storage interfaces
                                                        ↓
                                                 JSONRepository
handlers → Supervisor → Manager → platform.Prepare()
handlers → LogBroker ← LogStore (per-instance)
config ← main (loaded once at startup)
```

## Process ownership rules

- Each `exec.Cmd` has exactly one goroutine owner calling `cmd.Wait()`.
- HTTP handlers do not manage `exec.Cmd` directly; they delegate to `Supervisor`.
- Process arguments are passed as `[]string` — no shell invocation.
- Model environment variables are merged with the parent process environment (model variables override system variables).

## ADR summary

| ADR | Topic | Status |
|-----|-------|--------|
| 0001 | Product and architecture (Go, single binary, SSE logs) | Accepted |
| 0002 | Multi-instance Supervisor and Profile → Instance model | Accepted |
| 0003 | Web UI serving via embedded FS | Accepted (decision 1 implemented; decision 2 withdrawn) |
| 0004 | Config file vs Repository ownership (seed-once) | Proposed |
| 0005 | Recovery: identity-verified orphan detection and restart reconciliation | Accepted |
| 0006 | Secure credential storage (bcrypt `adminPasswordHash`) | Accepted |
| 0007 | Full audit logging (structured security events, `goal_audit.jsonl`) | Accepted |
| 0008 | Recovery: kill of an orphan (identity re-verified destructive termination) | Accepted |
| 0009 | Hot-reload wiring (field classification, explicit `POST /api/v1/admin/reload`) | Accepted |
| 0010 | Pipeline: group launch of existing Models with per-model Args override (Accepted 2026-08-29, implemented 2026-08-31 — schema v8, group lifecycle, 8-endpoint API, lifecycle audit, Pipelines UI, `tests/browser/pipeline.cjs`) | Accepted |
| 0011 | Windows Service / Background Mode: true SCM integration (Accepted 2026-08-31; implemented 2026-08-31 — in-binary `--service` verbs, `--service run` internal SCM entrypoint, absolute registered command, absolute-path install pre-flight, LocalSystem, `auto` start without implicit start, StopPending/30 s wait hint + 45 s outer budget, SCM-level restart, Event Log operational diagnostics (slog handler), ps1 scripts + MSI-tooling references removed in the same change) | Implemented |
| 0013 | Pipeline repeatable model entries + UX/product polish (Accepted 2026-09-06, owner contract agreed 2026-09-06; **owner-UI reconciliation 2026-09-09** — entry-level identity `PipelineModel.id` supersedes ADR 010 D1.3, per-entry launch gate + model-owner rule supersedes ADR 010 D3 and extends D4, per-entry stop/restart with legacy fallback, additive API `entry_id`/`index`, no schema bump (v8), strict Args semantics unchanged, **`AutoStart` reconciled: Active → all entries launch (per-entry field written-but-ignored; UI control + ⚡ removed)**, UI contract: explicit Args mode, ↑/↓/× reorder, inline icon strip (no overflow "…"), form-head bounded grid, mobile dense card, instances geometry, sidebar icon set, History rename, empty state, responsive polish) | Accepted |
| 0014 | Portable Configuration — Variable Resolution and Secret-Safe Export/Import (Accepted 2026-09-13; `${NAME}` syntax + `$$` escape, built-in `GOAL_DATA`, process-env source, resolve-at-consumption, undefined-var + malformed-ref errors, secret-safe export (keys only), portable bundle v1, atomic graph-import, 3-slice phasing; **MVP complete — Slices 1–3 published 2026-09-15**, final SHA `4624d81`, CI 7/7 PASS; import conflict policy **amended 2026-09-25: SKIP EXISTING**, replacing the original REJECT-on-collision — **that amendment is superseded as the current conflict / restore policy by ADR 018 (2026-09-26) and is no longer the shipped behavior; its text stays in the ADR as history**) | Accepted |
| 0018 | Portable Configuration Backup / Restore Semantics — **NEW / UPDATE / UNCHANGED / BLOCKED** (Owner decisions D1–D15 with the dash-labelled ratifications D-1…D-6 of 2026-10-04, D-7…D-9 of 2026-10-05 and D-10…D-17 of 2026-10-06; supersedes the 2026-09-25 SKIP EXISTING amendment of ADR 014 as current policy). Contract: a same-ID entity is matched by identity and classified new / update / unchanged / blocked; a restore writes only the D11 projection onto the stored record, so local Environment values, `CreatedAt` and pipeline-entry identity are preserved; `can_import` is the single predicate `WillMutate() && !HasBlocked()` shared by apply, wire and UI enablement; the runtime-name contest is decided once over the **intended final** state and is bundle-order independent — every claimant that would write a contested name blocks, self-ownership is not a conflict, an unchanged runtime reserves its name without blocking, and `related_id` is the lexicographically smallest other claimant presented as `(related: <id>)`; pipeline entries match by **entry ID**, never by position; apply runs under one lock with one durable write plus a pre-write graph invariant as defense-in-depth (an escaped collision is an ordinary blocked `409` with zero writes and zero in-memory residue); `existing` / `skipped` and the legacy flat top-level mirrors are retired with **no compatibility shim**; vocabulary EN **New / Will restore / Up to date / Blocked**, RU **Новые / Будут восстановлены / Без изменений / Заблокировано**. **Slices 1 and 2′ implemented and published** — Slice 1 `85c7e9c` (CI `37185521486`, attempt 1, 7/7 PASS); Slice 2′ `eec3007` (**CI `38303986246`, attempt 1 FAILED on Vulnerability Check, 6/7 jobs passed, never rerun — that SHA is permanently not full-CI green**), with the corrective toolchain commit `4b62f02` (`.go-version` 1.26.9, no vulnerability suppression) as the first full-CI-green carrier of those bytes (**CI `38310903770`, attempt 1 FAILED on the Linux race job, exactly one authorized same-SHA failed-job rerun → attempt 2 = 7/7; never first-attempt green**). **Slice 5 — this documentation reconciliation — is IMPLEMENTED, COMMITTED and PUBLISHED (2026-10-13: `d64cbe9`, docs-only, 11 Markdown paths / 58 insertions / 47 deletions; CI run `38385691793` FAILED at attempt 1 on Test (Windows) — `fatal error: runtime.semawakeup`, 6 of 7 — and reached 7/7 at attempt 2 through exactly one Owner-authorized same-SHA failed-job rerun: RECOVERED BY AUTHORIZED SAME-SHA RERUN, never first-attempt green; not tagged, not released), and it is the last slice of the 1 → 2′ → 5 ordering.** Bundle format stays v1 and repository schema stays v8; no lifecycle semantics change. Lifecycle label stays **Proposed** (the move to Accepted is an Owner decision), and **OWNER-IMPORT-01 stays OPEN**. *(Reconciled 2026-10-15: the Owner's manual browser acceptance of the shipped restore behavior has been performed and is recorded — gates 11–14, **25 of 25 UI criteria Owner-confirmed** — in [acceptance/ADR018-PORTABLE-IMPORT-BROWSER-ACCEPTANCE.md](acceptance/ADR018-PORTABLE-IMPORT-BROWSER-ACCEPTANCE.md); this wording previously read "OWNER-IMPORT-01 manual acceptance stays OPEN", which was accurate until that date, and the finding now stays OPEN on the Owner's separate closure decision rather than on an outstanding acceptance run.)* *(Reconciled 2026-10-17: **OWNER-IMPORT-01 is CLOSED by Owner decision of that date**, resting on the acceptance record cited above — 25 of 25 matrix-v3 UI criteria. The `OWNER-IMPORT-01 stays OPEN` clause earlier in this cell stands as this table's 2026-10-13 then-state. The lifecycle label stays **Proposed**, and every published-slice CI caveat above is unchanged.)* Provenance of each published slice: `ROADMAP.md` | Proposed |
| 0019 | Native HTTPS / Secure Origin — the single binary terminates TLS itself; no reverse proxy required (Accepted 2026-09-29, Owner decisions D1–D9 of the secure-origin design gate + P1–P8 of the reconciliation gate, whose cookie semantics were resolved from real-browser measurement). Contract: opt-in `tls` block in `goal.json` only (config file, never the API, the Settings UI or a portable bundle), fail-closed startup validation before any listener, HTTP kept and never redirected, HTTPS on the same `listenAddress` + `tls.port`, TLS 1.2 minimum as a constant, `Secure` derived per response from the connection (`r.TLS != nil`) on every cookie emit including the session clear, no forwarded-header trust for security identity. **Slices 1–5 implemented and published**: schema + validation, dual-listener bind-before-serve lifecycle, connection-derived `Secure` + extended non-loopback warning, documentation reconciliation, and the maintained HTTPS browser-acceptance suite (`tests/browser/https.cjs`; evidence record `docs/acceptance/ADR019-HTTPS-BROWSER-ACCEPTANCE.md`). *(The "Slices 1–3 implemented … slice 4 (documentation) reconciled with them in this change; slice 5 (HTTPS browser acceptance) NOT started" wording this row carried between slice 4's and slice 5's publication was accurate when written and is superseded here as tracking truth only.)* **OWNER-EXPORT-01 manual acceptance is OWNER PASS (2026-10-03)**: the automated suite proves the secure origin and that the browser-native picker is installed and invoked, while the human-completed Save As dialog is proved separately by the Owner's own run, recorded as result C of `docs/acceptance/ADR019-HTTPS-BROWSER-ACCEPTANCE.md`. *(The "remains open" wording this row carried before that run was accurate when written and is superseded here as tracking truth only.)* Provenance of each published slice: `ROADMAP.md` | Accepted |

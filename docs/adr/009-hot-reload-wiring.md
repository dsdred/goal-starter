# ADR 009: Hot-Reload Wiring — Field Classification and Explicit Reload Endpoint

**Status:** Accepted — owner contract agreed 2026-08-26 (decisions D1–D6 as drafted)
**Date:** 2026-08-26
**Related:** ADR 004 (Config vs Repository ownership), ADR 006 (Secure Credential Storage), ADR 007 (Audit Logging — event taxonomy), ROADMAP P0 "Hot-reload wired into main startup"

## Context

ROADMAP P0 item: "Hot-reload wired into main startup". Pre-implementation forensic (recorded in ROADMAP L44, commit `48d8f17`) plus a full code inventory establish the current state:

1. **`config.ReloadConfig` exists but is dead production code.** `internal/config/reload.go` has no production callers (only `reload_test.go` and docs). Its defects: `Save()` (`reload.go:157-192`) uses plain `os.WriteFile` + `os.Rename` — **no fsync, no read-back, no `.bak`** — violating the P0 durable-write contract that `config.Save` (`config.go:250-265` → `fsutil.WriteFileDurable`) satisfies; `watchLoop` is a 5-second mtime poll (`reload.go:99-130`) with a "log error" comment that never logs; `load()`'s doc claims a held lock that callers do not hold; `Stop()` can double-close.
2. **Docs overclaim.** `docs/CONFIGURATION.md:152-155` lists `logLevel` and `healthCheck.interval` as hot-reloadable fields — **neither field exists** in `Config` (`config.go:16-28`; the health-check interval is hardcoded 30s in `server.go:136` and health-check data is repository-side). `USER_GUIDE.md:255` / `USER_GUIDE_RU.md:255` repeat the `logLevel` claim.
3. **The settings endpoint already does the durable part.** `PUT /api/v1/settings` (`system.go:185-281`) persists via the durable path, refreshes the live bcrypt store on password change, and implements the `hint` contract: password-only → `ok`, anything touching listen/port/auth → `restart_required` (`system.go:276-280`; documented in `docs/API.md:65`).
4. **Structural facts.** The listener address is baked at `Run()` start (`server.go:212-231`, `http.ListenAndServe`, no rebind path); `authEnabled` is baked into route-registry closures at build time (`routes.go:227-266`, `auth.go:45,127`); `dataDir` is baked at startup (`main.go:59-64`, `server.go:61-77`); the config's `runtimes`/`models`/`profiles` are **seed-once** into `goal_repo.json` (`main.go:86`) and live editing happens via API against the repository.
5. **No watchers, no SIGHUP.** No fsnotify dependency; the only signal handling is shutdown (`main.go:91`).

Consequence: "hot-reload" for this product is (a) an authoritative **field classification**, (b) an **explicit, auditable reload trigger** for file edits, (c) **doc reconciliation** — not a general-purpose config engine.

## Decisions

### D1 — Field classification (authoritative)

| Field | Class | Rationale |
|---|---|---|
| `listenAddress`, `webPort` | **restart** | Listener ownership is structural (`http.Server` owns the socket). Rebind is rejected: it opens a port-free window, breaks active SSE/log streams and in-flight requests, and requires careful hand-off sequencing for zero user-visible gain in a local single-binary manager. |
| `dataDir` | **restart** | Repository and audit-log paths are baked at startup. |
| `authEnabled`, `adminUser` | **restart** | Baked into the route registry. Hot toggle is technically possible (per-request atomic flag read in `requireAuth`/`requireAuthCSRF`) but is **rejected for first scope**: it creates a live path to an unauthenticated admin surface, and the existing hint contract already classifies these as `restart_required` (no contract change). |
| `adminPasswordHash` | **hot via settings endpoint only** | `PUT /api/v1/settings` already updates the live bcrypt store (`system.go:254-256`). A hand-edited hash in `goal.json` is applied **only at next restart** — reload never applies credential material (keeps credential changes on the audited, validated endpoint path). |
| `logLevel` | **hot (new field)** | New `Config` field, values `debug\|info\|warn\|error`, default `info` (absent value = `info`, backward-compatible load). Drives the application `slog` level via a hot-swappable handler level; no restart needed. |
| `runtimes`, `models`, `profiles` (config sections) | **seed-only, never re-applied** | Seed-once into the repository at startup; live data lives in `goal_repo.json` and is edited via API. Re-seeding on reload would **overwrite user data** — forbidden. |

### D2 — Trigger: explicit `POST /api/v1/admin/reload`

Auth + CSRF protected (same middleware class as `GET /api/v1/admin/audit`). User-initiated, deterministic, cross-platform, auditable, no new dependency.

Rejected alternatives:
- **fsnotify / file watching:** implicit and "magical" (an editor autosave reloads config mid-action); a new third-party dependency; the app's own durable writes (settings endpoint) would self-trigger reloads and need self-echo suppression; cross-platform watcher semantics differ.
- **SIGHUP:** not a reliable cross-platform signal on Windows; unreachable from the Web UI.
- **Polling (existing `watchLoop`):** mtime-only, 5s latency, known defects (Context 1), and implicit.

### D3 — Reload semantics (endpoint contract)

1. Re-read the configured file (`config.Load`).
2. Validate with `Config.Validate()` (log-level value check added there — see D6). **Validation scope = application scope**: the seed sections are not re-applied, so startup-only full validation (executable existence, bind probe — `validate.go:59-79`) is *not* a reload gate.
3. **Rejected reload:** any load/validation failure → `400 {"status":"rejected","error":"<bounded>","code":"bad_request"}`. The file on disk is **never modified** by reload; live values are **unchanged** (all-or-nothing: hot fields are applied only after validation passes).
4. **Apply hot fields** (`logLevel`).
5. **Compute the restart-pending diff**: file value vs currently-effective live value for every restart-class field (`listenAddress`, `webPort`, `dataDir`, `authEnabled`, `adminUser`) → reported in the response.
6. **Audit:** one `config.reload` event (ADR 007 additive extension, analogous to `instance.kill`): detail keys `applied` + `restart_required` (field **names** only, bounded; no values, never credential material; fail-open per ADR 007). Rejected reloads emit `config.reload` with `status=rejected` and the bounded error class (no file content).
7. **Response:** `200 {"status":"reloaded","applied":["logLevel"],"restart_required":["listenAddress"]}` — field names only. Empty arrays are included, not omitted.

### D4 — Settings-endpoint hint contract: unchanged

`ok` (password-only) vs `restart_required` (anything else) remains correct under D1 — an `authEnabled`-only toggle stays `restart_required`. No API.md change to `PUT /api/v1/settings`; no test changes to `TestCredential_Settings_RestartHint_Protocol`.

### D5 — `ReloadConfig` disposition: removed

The type and its `reload_test.go` are removed. Every production-facing capability is dead (Context 1); `Save()` violates the durable-write contract; the polling watcher is rejected by D2. The new minimal reload path (load → validate → apply hot → diff) lives in `internal/config` (pure, testable) with the endpoint in `internal/webui/handlers` and the audit event in `internal/webui/audit`. No shell, no new dependencies.

### D6 — Documentation reconciliation (part of the task)

- `docs/CONFIGURATION.md`: the Hot-reload table becomes the D1 classification (authoritative); the phantom `healthCheck.interval` row is removed; `logLevel` gains a real field entry (implementation now matches the docs' existing claim).
- `docs/API.md`: `POST /api/v1/admin/reload` row + response shape; audit taxonomy line gains `config.reload`.
- `docs/SECURITY.md`: reload endpoint auth/CSRF + audit line.
- `docs/USER_GUIDE.md` / `USER_GUIDE_RU.md`: "Hot Configuration Reload" sections made accurate (what is hot, what requires restart, how to trigger a reload).
- `docs/ARCHITECTURE.md` / `ARCHITECTURE_RU.md`: "Configuration hot-reload" section updated (wired, endpoint-based, D1 classification).
- `BACKLOG.md:205`: "[x] Hot-reload configuration (`ReloadConfig`)" corrected — the type is removed by this ADR; the item is annotated, not silently rewritten.
- `ROADMAP.md`: P0 item → `[x]` with shipped evidence.

## Acceptance contract

1. Reload with only `logLevel` changed → `200 {"status":"reloaded","applied":["logLevel"],"restart_required":[]}`; application log level changes immediately; no restart.
2. Reload with `listenAddress` (or `webPort`/`dataDir`/`authEnabled`/`adminUser`) changed → `200`, the changed field(s) appear in `restart_required`, the live listener/registry are untouched.
3. Reload with broken JSON in the file → `400 status=rejected`; live values unchanged; the file on disk is byte-identical.
4. Reload with an unknown `logLevel` value → `400` (validation), live values unchanged. The same value at **startup** also fails `ValidateFull`.
5. Seed sections (`runtimes`/`models`/`profiles`) changed in the file → repository data is **unchanged** after reload (no re-seed).
6. Unauthenticated reload → `401`; authenticated without CSRF token → `403`.
7. `config.reload` audit event: exactly one per request; detail carries field names only (never values, never credential material); a rejected request is audited with `status=rejected` and a bounded error class; audit-write failure does not change the reload outcome (fail-open).
8. Password-only change via `PUT /api/v1/settings` still yields `hint=ok` and updates the live hash (regression guard).
9. A hand-edited `adminPasswordHash` in the file is **not** applied by reload (live store unchanged until restart).
10. Backward-compatible load: an existing `goal.json` without `logLevel` behaves as `info`.
11. `gofmt`/`go vet` clean; Windows + Linux builds pass; race-detector clean (CI).

## Security consequences

- No new unauthenticated surface: reload is auth + CSRF protected.
- Reload never writes the file and never applies credential material — the only live credential path remains the audited settings endpoint (ADR 006 contract intact).
- Rejected reloads leave the live configuration untouched (all-or-nothing), so a corrupt or maliciously edited file cannot partially reconfigure a running instance.
- The `restart_required` report makes the pending-restart state visible instead of implicit (supports the Settings UX "pending-restart indication" direction).

## Future work (not in first scope)

- Hot toggle of `authEnabled` via per-request atomic flag (requires its own security review of the live unauth-surface path).
- Listener rebind without downtime (if ever demanded).
- `healthCheck.interval` as a real, hot-applied setting (repository-side; separate item).
- UI: reload button + pending-restart banner consuming the D3 response.

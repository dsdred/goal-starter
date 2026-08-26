# GoAl Roadmap

## Current state

- **v2.0.1 — released** (latest published release, tag `v2.0.1`). Shipped the v2.0.1 manual-acceptance corrections (editable auth settings, responsive tables, mobile layout fixes).
- **v2.0.0 — released** (superseded). v2.0 simplified the domain model and completed manual-acceptance stabilization.

### v1.0.0 (historical release)

- Multi-instance `Supervisor` with InstanceController
- LogBroker — multi-instance log streaming (SSE)
- QueryLogs — aggregated logs with deterministic ordering
- CAS reservation for maxConcurrent
- Snapshot model for concurrent-safe access
- Conservative recovery: stale instance detection
- JSON repository with atomic write + backup recovery
- Session authentication + CSRF protection
- Embedded Web UI from embedded FS
- Cross-platform: Windows amd64 + Linux amd64
- CI: gofmt, vet, test -race, build (Windows+Linux), govulncheck

## Next: production hardening + product

### P0 — Production hardening
- [x] Recovery: identity-verified orphan detection and restart reconciliation
  - Design gate: [ADR 005](docs/adr/005-recovery-pid-reattach-orphan.md) (**Accepted** — implemented 2026-08-23) defines the `orphan`/`stale` state model, the identity contract (PID + executable path + start time; bare PID forbidden; conservative fallback), safe Dismiss/reconciliation, and the user-facing explanation. First implementation scope **excludes kill**.
  - Shipped: `d2df293` + CI fixes `aae6e95`, `979e952` (CI run 32599996165, 6/6 PASS including Linux race + e2e). Real Chrome acceptance: 21/21 PASS.
- [x] Recovery: kill of an orphan (destructive) — separate item; requires its own contract/security review (ADR) of identity-verification sufficiency before implementation
  - Design gate: [ADR 008](docs/adr/008-recovery-kill-orphan.md) (**Accepted** — owner contract agreed 2026-08-26, implemented 2026-08-26, CI reconciled 2026-08-26) defines the safety core (strict identity re-verification before **every** destructive syscall, never PID-only, start-time unavailable → refuse), Unix termination (SIGTERM → 5s grace → re-verify → SIGKILL only if still alive and identity still matches; exit within grace = success, no SIGKILL), Windows termination (immediate `TerminateProcess` after re-verification, no graceful phase, query/terminate rights independent), the post-kill lifecycle contract (Cases A–G: no false success — terminal `stale` only on confirmable process state; unconfirmed termination preserves `orphan`; pid-gone → `reconciled`, not `killed`), refusal semantics with persisted `last_error` diagnostics (identity-unconfirmed 409, insufficient-privilege 403, unconfirmed 500), an additive single `instance.kill` ADR 007 audit event (outcome/reason bounded, secret-safe, fail-open), the irreducible TOCTOU residual as accepted documented risk, and the scope guard (explicit user action only, `orphan`-only, never automatic).
  - Shipped: `a3a945b09721fe33d356558ffc6747444f168be3` + CI run 32934761306 (6/6 PASS including Linux race): `internal/platform/kill{,_unix,_windows}.go` (`ProcessKiller`: SIGTERM/SIGKILL, `TerminateProcess`, `ErrKillAccessDenied`/`ErrKillAlreadyGone`), `internal/process/supervisor_kill.go` (`KillOrphan` + strict `verifyIdentityForKill` + Cases A–G + `pollGone`), `InstanceService.KillOrphan`, `POST /api/v1/instances/{id}/kill` (auth+CSRF; 200 `killed`/`reconciled`, 409/403/500 refusals with bounded `reason`), `instance.kill` audit event, UI destructive-confirmed Kill button (EN/RU). Tests: `supervisor_kill_test.go` (13 cases, cross-platform) + `instances_kill_test.go` (8, incl. auth+CSRF route + audit). `gofmt`/`go vet`/`go test ./...` PASS; Windows + Linux builds PASS; race via CI. Docs reconciled: API.md, SECURITY.md, USER_GUIDE EN/RU, ADR 007, ADR 005.
- [x] Login rate limiting: per-client-address fixed window on `POST /api/v1/auth/login` (100 requests/min, HTTP 429 `rate_limited`); `X-Forwarded-For`/`X-Real-IP` intentionally not trusted (spoofable)
  - Shipped: `6f6bb5f` + CI run 32687269358 (6/6 PASS including Linux race). `internal/webui/security.RateLimiter` (fixed window, per-TCP-peer key, background cleanup) wired to the login route; 9 tests (`internal/webui/security/ratelimit_test.go`, `internal/webui/handlers/routes_rate_limit_test.go`).
- [x] Full audit logging: structured security audit events (login, session, settings, instance actions), persistence and retention, query API. Replaces the metrics-only state; satisfies the audit commitment in ADR 005 (Dismiss audit). Significant work per DEVELOPMENT.md (new entity + persistence + public API)
  - Design gate: [ADR 007](docs/adr/007-audit-logging.md) (**Accepted** — owner contract agreed 2026-08-24, implemented 2026-08-25, CI reconciled 2026-08-25) defines the event taxonomy (login/session/settings/instance actions), JSONL append-only storage with per-event fsync and 3×10 MiB rotation, `GET /api/v1/admin/audit` query API, hard secret-safety rules, fail-open failure semantics (structured `slog.Error` per failed write, no secret-bearing diagnostic data, no logger latching, independent retry per event), and a 17-scenario acceptance contract
  - Shipped: `d46514a9e343222e31565fbc11999670eb87d82a` + CI run 32776891778 (6/6 PASS including Linux race; `internal/webui/audit` and `internal/webui/handlers` PASS in both Linux runs). `internal/webui/audit/` (AuditLogger: single `O_APPEND` write + per-event fsync, 10 MiB rotation × 3 generations, mode 0600, fail-open without latch), handler call sites (login success/failure/rate_limited, session.logout, settings.saved with changed field names only, instance start/stop/restart/dismiss/cleanup), `GET /api/v1/admin/audit` (auth, limit/offset/exact-event filter, newest first, missing file = empty list). All 17 acceptance scenarios covered (1–13, 16–17 in `internal/webui/handlers/audit_integration_test.go`; 14–15 in `internal/webui/audit/audit_test.go`). **ADR 005 Dismiss-audit commitment fulfilled**: `instance.dismiss` is audited (`d46514a`). Docs updated: API.md, SECURITY.md, CONFIGURATION.md, USER_GUIDE EN/RU, ARCHITECTURE EN/RU.
- [x] Secure credential storage: migrate `adminPassword` from plaintext in config JSON to stored hash (bcrypt/Argon2id) with backward-compatible migration (detect plaintext → hash on first load); no hash in API responses; UI password-set semantics preserved
  - Design gate: [ADR 006](docs/adr/006-secure-credential-storage.md) (**Accepted** — agreed 2026-08-23, implemented 2026-08-23) defines bcrypt cost 12, `adminPasswordHash` field, explicit startup migration, 72-byte validation, failure semantics, and 18-scenario acceptance contract
  - Shipped: `9d2d0fb` + CI run 32657837425 (PASS). All 18 acceptance scenarios covered by `internal/config/migrate_credentials_test.go` and `internal/webui/handlers/credential_integration_test.go`.
- [x] JSON durability: durable writes on all platforms (file-data fsync before rename; rename durability: POSIX — directory fsync after rename, Windows — NTFS log commit, no directory-flush API in the supported model); transactional `.bak` backup before every write
  - Shipped: `7db7d61` + CI run 32667643972 (6/6 PASS including Linux race). `internal/fsutil.WriteFileDurable` — fsynced temp file, read-back verification, atomic `.bak` before every write, atomic rename, mandatory directory fsync (POSIX) — applied to `goal_repo.json` (`saveLocked` + `SaveUnified`, permissions fixed 0644→0600) and `goal.json` (`config.Save`). Tests: `internal/fsutil/fsutil_test.go`, `internal/storage/durability_test.go`, `internal/config/durable_save_test.go`.
- [x] P0 consistency (technical debt): rollback on save failure at all `JSONRepository` CRUD paths (previously non-rolling CRUD sites restore the pre-save in-memory state; memory and disk never diverge due to a failed save) — approach chosen: rollback, not validate-before-mutate
  - Shipped: `f8e73b3` + CI run 32757535814 (6/6 PASS including Linux race). All 11 mutating CRUD save sites now roll back (delete paths use full slice copy — slice-header restore is unsafe after in-place `append(s[:i], s[i+1:]...)` shifts); tests: `internal/storage/consistency_test.go` (4 cross-platform) + extended `TestJSONRepository_SaveFailurePropagates`.
- [x] Hot-reload wired into main startup
  - Design gate: [ADR 009](docs/adr/009-hot-reload-wiring.md) (**Accepted** — owner contract agreed 2026-08-26, implemented 2026-08-26, CI reconciled 2026-08-26) defines the authoritative field classification (hot: `logLevel` — new field, `debug|info|warn|error`, default `info`; `adminPasswordHash` — hot via the settings endpoint only, a hand-edited hash applies only at restart; restart: `listenAddress`/`webPort`/`dataDir`/`authEnabled`/`adminUser`; seed-only, never re-applied: config `runtimes`/`models`/`profiles`), the explicit trigger `POST /api/v1/admin/reload` (auth + CSRF; fsnotify / SIGHUP / polling rejected), all-or-nothing semantics (a reload never writes the file, never applies credential material, never re-seeds; rejected reload → `400 status=rejected` with live values and the file untouched), the response contract `{"status":"reloaded","applied":[...],"restart_required":[...]}` (field names only, deterministic order), the additive single `config.reload` ADR 007 audit event (field names only, fail-open, secret-safe), the unchanged `ok | restart_required` settings hint contract, and the removal of the dead `ReloadConfig` type (unused in production; its `Save()` violated the durable-write contract — no fsync / read-back / `.bak`)
  - Shipped: `633cec6` + CI run 32953491491 (6/6 PASS including Linux race): `config.LogLevel` field + `config.LogLevel()` parse/validation (added to `Config.Validate()`), `config.LoadReadOnly` (side-effect-free read — never creates the default file), `config.DiffHot`, `POST /api/v1/admin/reload` (auth + CSRF, `WithLiveConfig` wiring), hot `logLevel` application via `slog` level swap, `config.reload` audit event (`EventConfigReload`), removed `internal/config/reload.go` + `reload_test.go` (replaced by `reload_hot_test.go` and `system_reload_test.go`: 11 config tests + 9 endpoint tests incl. 401/403, rejected-reload immutability, seed-not-reseeded, credential-not-applied, audit field-names-only). Docs reconciled: CONFIGURATION.md, API.md, SECURITY.md, USER_GUIDE EN/RU, ARCHITECTURE EN/RU, BACKLOG
- [x] Maintained real-Chrome acceptance as a release gate (build-out under Product/UX → Browser Acceptance Suite)
  - Shipped: `tests/browser/` maintained Playwright + Chromium suite — `core.cjs` (wizard, resolve, lifecycle, logs/history/instances, edit/delete, 409, autostart, polling, auth OFF/ON, env-secret safety), `responsive.cjs` (monotonic 768px contract), `orphan.cjs` (recovery, RU/EN, Kill/Dismiss), `migration.cjs` (v5 → v7), `stress.cjs` (long live-log stream, ~100 lines/s past the 2000-line client window — regression for the tab-freeze defect below); 250 checks, all PASS on Windows local Chromium. Replaced the one-off `*_acceptance*.cjs` scratch scripts (removed). Deterministic fixtures: platform-built `goal` + `fake-runtime`, seeded config/repo per suite. Headless-Chromium CI job `browser-acceptance` (ubuntu-latest) added to `.github/workflows/ci.yml`; suite manifest tracked (`tests/browser/package.json` + lock, `node_modules/` ignored). Stale scratch assertions corrected to the current contract (schema v7, host/port folded into args, current wizard markup, `#model-list` rows, sidebar logout). Local run: `cd tests/browser && npm test`.
- [x] WebUI long-session tab freeze (production defect, class A): a tab whose live log view accumulates 2001 rendered log lines wedges the tab's main thread (fully unresponsive / black tab). Root cause: the trim loop in `appendLogLine` iterates a **static** `querySelectorAll('.log-line')` NodeList — `while (lines.length > 2000) lines[0].remove();` never terminates because `lines.length` is frozen and `remove()` on an already-detached node is a no-op. Server state is unaffected (health 200 during the freeze); a new tab after re-login recovers; F5 on the frozen tab does not. Forensic proof in real Chromium: tabs that visited Logs (directly or after navigating away) freeze at exactly line 2001 with 100%-pinned renderer CPU and flat JS heap (not a memory leak); a Models-only tab survives 11 minutes; the server stays healthy throughout.
   - Shipped: `5e00628` (bounded trim in `internal/webui/static/app.js` — the pre-existing 2000-line client display window is now actually enforced; `flood` mode added to `testdata/fake-runtime`; `tests/browser/stress.cjs` maintained regression — 21 checks, verified to FAIL deterministically on the pre-fix code and PASS on the fix) + CI fixture fix `4406f8e` (deterministic long-path fixture in `responsive.cjs`; prior CI run 33004652901 failed on clean Linux because the fixture relied on a pre-existing `os.tmpdir()` file). Final CI: run 33011027572, 7/7 PASS including Linux race + headless Browser Acceptance. Documented behavior: LIMITATIONS.md (bounded 2000-line client display window).

### P1 — Product & reliability
- [x] Forensic: untracked `cmd/goal/linux/packager.go` — BACKLOG lists it as completed Linux packaging, but the file was never committed and is not imported by `cmd/goal`; per AGENTS.md a separate forensic decides include-vs-reject, then the BACKLOG record is corrected
  - Forensic verdict (**REJECT** — 2026-08-27): single untracked file (583 lines), no git history on any branch, zero imports, no build-script/CI reference, no tests. Proven never-executed defects: RPM-spec `fmt.Sprintf` arg/template mismatch (`Name: <version>`, `Version: <arch>`, `BuildArch: <name>`, `systemctl disable <version>.service` — reproduced), deb `Architecture: x86_64` on cross-build hosts, fpm mode references postinst/prerm scripts that are never written. The false `[x]` BACKLOG record was introduced by unrelated commit `48ccbfe`; BACKLOG record corrected (marked rejected with evidence). Installer direction stays open under "Later" (MSI/.deb/.rpm as demand matures) and requires its own tested + CI-wired implementation task.
- [ ] Stale root-level `webui/` duplicate: the embedded UI is `internal/webui/` (see `server.go` go:embed); the root `webui/` copy is tracked, diverged, and referenced nowhere — remove or merge (technical debt)
- [ ] Pipeline MVP (see Pipeline contract below)
- [ ] Windows Service / Background Mode: true SCM integration (service registration, graceful stop, diagnostics without console, service-mode paths); compatible with Recovery (ADR 005); uninstall safety
  - Design gate: lifecycle ADR (SCM contract, interaction with Supervisor/Recovery, service vs foreground mode)
  - Pre-implementation: forensic of existing `deploy/windows/install-service.ps1`, `uninstall-service.ps1`, and `internal/updater` service integration to avoid a parallel mechanism
- [ ] Native HTTPS / TLS: binary serves HTTPS directly (cert/key config, HTTP/HTTPS mode toggle, Secure cookie, TLS version defaults, diagnostics); no reverse proxy required; independent security-hardening direction (not blocked by Secure Credential Storage)
  - Design gate: security/config ADR (TLS config model, certificate loading, Secure-cookie activation)
- [ ] Portable Configuration & Path Variables: config export/import, environment-variable expansion (`${VAR}`), built-in GoAl variables, resolve-at-consumption, undefined-variable diagnostic, backward-compatible load, secret-safe export
  - Design gate: architecture ADR (config model, variable resolution semantics, security policy for exported secrets)
  - Constraint (not a hard dependency): secret-safe export must account for Secure Credential Storage (P0) and future TLS private keys; the portable-config ADR defines the export security contract, but implementation is not blocked until both directions land
- [ ] Persistent logs
- [ ] Configurable log storage location
- [ ] Prometheus-compatible monitoring
- [ ] Bruno API collections
- [ ] Supervisor decomposition
- [ ] Migration framework (schema migration + tests)
- [ ] Chaos / concurrency / recovery tests
- [ ] Comprehensive integration tests
- [ ] Windows/Linux-specific lifecycle tests
- [ ] WebUI: close the live-log SSE stream when leaving the Logs page — the consumer currently keeps running and appending to the hidden `#log-view` on every other page (bounded after the 2000-line trim fix, but it holds a server subscription + network + main-thread work for the tab's lifetime; forensics tab-C evidence: a Models page that had visited Logs also froze pre-fix)
- [ ] WebUI: per-line O(n) trim cost in `appendLogLine` (a full `querySelectorAll('.log-line')` scan per appended line; amortized O(n) with n ≤ 2000 — fine at typical rates, a hot path for very chatty models; consider keeping a first-line reference or batched appends)
- [ ] WebUI: SSE reconnect replays the last 500 lines without client-side dedup — the server replays history on every (re)connect and the client re-appends them, so network drops can duplicate rendered lines

### Pipeline contract (design note — requires an architecture/ADR before implementation)
- A Pipeline references existing Models; multiple Models per Pipeline; group lifecycle; per-model optional Args.
- Args resolution: if `PipelineModel.Args` is non-empty, use `PipelineModel.Args` **entirely**; if empty, use `Model.Args` **entirely**. **No** merge / patch / append semantics.

### P2 — Distribution
- [ ] ARM64 builds and tests
- [ ] Reproducible Windows/Linux artifacts (deterministic build inputs)
- [ ] PE version metadata (Windows) + ELF version fields (Linux) in release binaries
- [ ] Authenticode (Windows) + GPG (Linux) signatures for all artifacts
- [ ] SHA-256 checksums for all artifacts + verification workflow; distinguish verification (pre-release) binaries from signed release artifacts
- [ ] Release automation via GitHub Actions
- [ ] CONTRIBUTING / docs hygiene
- [ ] Reverse-proxy / TLS deployment guidance

### Later
- [ ] MSI / .deb / .rpm installers as demand matures
- [ ] SQLite storage — only through an explicit architecture decision (ADR)
- [ ] Auto-update
- [ ] Advanced Pipeline: DAG / dependencies / readiness / resource scheduling
- [ ] ACME / automatic certificate management (depends on Native HTTPS / TLS)

## Product/UX evolution (multi-release)

### Authentication & User Management
- [ ] Multi-user support (beyond the single admin)
- [ ] User CRUD + lifecycle (create / disable / delete, password reset)
- [ ] Password lifecycle (rotation, expiry policy, forced change)
- [ ] Session management (list / revoke active sessions)
- [ ] Roles & permissions — separate architecture / design task (ADR)

### Responsive UI Contract
- [x] Canonical **monotonic viewport** breakpoint strategy (decided 2026-08-25, Option A). A single explicit viewport breakpoint (768px, aligned with the existing mobile/drawer boundary): table above it, compact cards at/below it, no revert. The list mode depends on the **viewport width only, never on measured content width** — measured `#main-content` width is non-monotonic because the sidebar media queries change it at 1024/768 (margin-left 240→200→0), which was the root cause of the snap-back. Supersedes the earlier "content-width (not viewport)" direction, which could not be monotonic.
- [x] Desktop table → compact row transition per entity (monotonic; Runtimes / Instances / History share the primitive)
- [x] Sidebar state no longer affects list mode: hiding the sidebar only adds space and never triggers the table→cards switch (verified @1280 sidebar 240 offset; @768 drawer, contentLeft=0)
- [ ] Zoom (80 / 100 / 125 / 150%) correctness
- [x] No horizontal body scroll guarantee (`overflow-x:auto` on table wraps + `overflow-wrap:anywhere` on cells; browser acceptance verifies 0 page-level overflow at 1920/1440/1280/1024/768/600/430/375)
- [x] Reusable compact-row pattern across entities
- [x] Fix non-monotonic breakpoint in the Runtime view (table → cards → table snap-back on width change); once the compact view is reached it must stay compact (v2.0.1 manual-acceptance finding). Shipped: `d8a9e97786547ea27cecde614bb4d991eeb80bcd` + CI run 32882640063 (6/6 PASS including Linux race); browser acceptance 86/86 PASS incl. monotonic `table×4 → cards×4` per entity

### Maintained Browser Acceptance Suite
- [x] Replace one-off scratch acceptance scripts with a maintained suite — `tests/browser/` (Playwright); scratch `*_acceptance*.cjs` removed
- [x] Deterministic fixtures (fake-runtime, seeded config / data) — per-suite temp workspace; `goal` + `fake-runtime` built for the current platform; v5 migration fixture generated in-suite
- [x] Real Chrome / Chromium coverage — Playwright-bundled Chromium, headless, cross-platform (no fixed browser channel)
- [x] Responsive, auth, and wizard scenarios — `responsive.cjs` (8 viewports × 3 entity lists), `core.cjs` (auth OFF/ON phases, 3-step wizard existing/new runtime), `orphan.cjs` (RU/EN + mobile), `migration.cjs`
- [x] CI integration (headless) — `browser-acceptance` job (ubuntu-latest) in `.github/workflows/ci.yml` runs `npm test`

### Instance History
- [ ] Human-readable exit reason (normal stop / crash / forced termination)
- [ ] Immutable history snapshots
- [ ] Retention / cleanup policy
- [ ] Optional history details view
- [ ] History: move the clear-history action into the filter toolbar (natural wrap on narrow widths) and label it «Очистить» (currently a separate «Очистка» control beside the heading)

### Settings UX
- [ ] Settings grouping
- [ ] Field-level validation + save status
- [ ] Pending-restart indication
- [ ] Configured vs effective value display
- [ ] Secret-safe expansion (never expose stored secrets)

## Future

Production-ready single-binary manager with guaranteed data durability, comprehensive security model, and cross-platform support.

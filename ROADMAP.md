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
- [ ] Recovery: kill of an orphan (destructive) — separate item; requires its own contract/security review (ADR) of identity-verification sufficiency before implementation
- [x] Login rate limiting: per-client-address fixed window on `POST /api/v1/auth/login` (100 requests/min, HTTP 429 `rate_limited`); `X-Forwarded-For`/`X-Real-IP` intentionally not trusted (spoofable)
  - Shipped: `6f6bb5f` + CI run 32687269358 (6/6 PASS including Linux race). `internal/webui/security.RateLimiter` (fixed window, per-TCP-peer key, background cleanup) wired to the login route; 9 tests (`internal/webui/security/ratelimit_test.go`, `internal/webui/handlers/routes_rate_limit_test.go`).
- [ ] Full audit logging — **design task (ADR required before implementation)**: structured security audit events (login, session, settings, instance actions), persistence and retention, query API. Replaces the metrics-only state; satisfies the audit commitment in ADR 005 (Dismiss audit). Significant work per DEVELOPMENT.md (new entity + persistence + public API)
- [x] Secure credential storage: migrate `adminPassword` from plaintext in config JSON to stored hash (bcrypt/Argon2id) with backward-compatible migration (detect plaintext → hash on first load); no hash in API responses; UI password-set semantics preserved
  - Design gate: [ADR 006](docs/adr/006-secure-credential-storage.md) (**Accepted** — agreed 2026-08-23, implemented 2026-08-23) defines bcrypt cost 12, `adminPasswordHash` field, explicit startup migration, 72-byte validation, failure semantics, and 18-scenario acceptance contract
  - Shipped: `9d2d0fb` + CI run 32657837425 (PASS). All 18 acceptance scenarios covered by `internal/config/migrate_credentials_test.go` and `internal/webui/handlers/credential_integration_test.go`.
- [x] JSON durability: durable writes on all platforms (file-data fsync before rename; rename durability: POSIX — directory fsync after rename, Windows — NTFS log commit, no directory-flush API in the supported model); transactional `.bak` backup before every write
  - Shipped: `7db7d61` + CI run 32667643972 (6/6 PASS including Linux race). `internal/fsutil.WriteFileDurable` — fsynced temp file, read-back verification, atomic `.bak` before every write, atomic rename, mandatory directory fsync (POSIX) — applied to `goal_repo.json` (`saveLocked` + `SaveUnified`, permissions fixed 0644→0600) and `goal.json` (`config.Save`). Tests: `internal/fsutil/fsutil_test.go`, `internal/storage/durability_test.go`, `internal/config/durable_save_test.go`.
- [ ] P0 consistency (technical debt): rollback on save failure at all `JSONRepository` CRUD paths (12 previously non-rolling call sites restore the pre-save in-memory state; memory and disk never diverge due to a failed save) — approach chosen: rollback, not validate-before-mutate; tests: `internal/storage/consistency_test.go`
- [ ] Hot-reload wired into main startup
- [ ] Maintained real-Chrome acceptance as a release gate (build-out under Product/UX → Browser Acceptance Suite)

### P1 — Product & reliability
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
- [ ] Canonical content-width (not viewport) breakpoint strategy
- [ ] Desktop table → compact row transition per entity
- [ ] Sidebar-aware width accounting
- [ ] Zoom (80 / 100 / 125 / 150%) correctness
- [ ] No horizontal body scroll guarantee
- [ ] Reusable compact-row pattern across entities
- [ ] Fix non-monotonic breakpoint in the Runtime view (table → cards → table snap-back on width change); once the compact view is reached it must stay compact (v2.0.1 manual-acceptance finding)

### Maintained Browser Acceptance Suite
- [ ] Replace one-off scratch acceptance scripts with a maintained suite
- [ ] Deterministic fixtures (fake-runtime, seeded config / data)
- [ ] Real Chrome / Chromium coverage
- [ ] Responsive, auth, and wizard scenarios
- [ ] CI integration (headless)

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

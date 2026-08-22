# GoAl Roadmap

## Current state

- **v2.0.0 — released** (latest published release). v2.0 simplified the domain model and completed manual-acceptance stabilization.
- **v2.0.1 — in stabilization**: manual-acceptance corrections on `main`, not yet tagged. Release blockers take priority; see the release/stabilization boundary in [AGENTS.md](AGENTS.md).

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

## Next: v2.0.1 hardening + production

### P0 — Production hardening
- [ ] Recovery: full PID reattachment + orphan handling on restart
- [ ] Login rate limiting (complete) + full audit logging
- [ ] JSON durability: fsync after rename on all platforms; transactional backup before every write
- [ ] Hot-reload wired into main startup
- [ ] Maintained real-Chrome acceptance as a release gate (build-out under Product/UX → Browser Acceptance Suite)

### P1 — Product & reliability
- [ ] Pipeline MVP (see Pipeline contract below)
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

### Settings UX
- [ ] Settings grouping
- [ ] Field-level validation + save status
- [ ] Pending-restart indication
- [ ] Configured vs effective value display
- [ ] Secret-safe expansion (never expose stored secrets)

## Future

Production-ready single-binary manager with guaranteed data durability, comprehensive security model, and cross-platform support.

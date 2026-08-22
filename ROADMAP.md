# GoAl Roadmap

## Current: v1.0.0 (Released)

Production-ready single-binary manager for local AI runtimes and models.

### Release highlights (v1.0.0)

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

## Next: v1.1

### P0 — Production improvements
- [ ] SQLite storage (single-binary)
- [ ] Full PID reattachment on restart
- [ ] Hot-reload wired into main startup
- [ ] Login rate limit fully implemented
- [ ] fsync after rename on all platforms
- [ ] Transactional backup before every write

### P1 — Reliability
- [ ] Comprehensive integration tests
- [ ] Chaos testing for Supervisor recovery
- [ ] Schema migration tests
- [ ] Concurrent write protection tests
- [ ] Windows/Linux-specific lifecycle tests

### P2 — Packaging
- [ ] .deb/.rpm packages via CI
- [ ] GPG signatures for all artifacts
- [ ] ARM64 builds and tests
- [ ] Windows MSI installer (via WiX)
- [ ] Release automation via GitHub Actions
- [ ] Reproducible Windows/Linux artifacts (deterministic build inputs)
- [ ] PE version metadata (Windows) + ELF version fields (Linux) in release binaries
- [ ] SHA-256 checksums for all artifacts + verification workflow
- [ ] Distinguish verification binaries (pre-release) from signed release artifacts

## Product & UX Evolution (multi-release)

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

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

## Future

Production-ready single-binary manager with guaranteed data durability, comprehensive security model, and cross-platform support.

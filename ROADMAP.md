# GoAl Roadmap

## Current: v0.9 Stabilization (Текущая итерация)

Multi-instance supervisor architecture, LogBroker, QueryLogs aggregation, maxConcurrent CAS reservation, Snapshot model, recovery policy, JSON atomicity, hot-reload WIP, security audit.

### Deliverables этой итерации:
- ✅ Multi-instance `Supervisor` с InstanceController
- ✅ LogBroker — подписка на логи всех instances
- ✅ QueryLogs — агрегированные логи с глобальной пагинацией
- ✅ CAS reservation для maxConcurrent
- ✅ Snapshot model для Safe concurrent access
- ✅ Recovery policy: restorable/stale/orphaned
- ✅ JSON repository atomic write + backup
- ✅ CSRF protection, session security, rate limit
- ✅ Hot-reload config (WIP: not wired into main)
- ✅ CI: gofmt, vet, test -race, build (Windows+Linux), govulncheck
- 📝 Обновлённая документация

## Next: v0.10 Release Hardening

### P0 — Production readiness
- [ ] SQLite storage (с сохранением single-binary)
- [ ] Full reattach к произвольному PID
- [ ] Hot-reload wired into main startup
- [ ] Audit logging (полноценный, не только metrics)
- [ ] Login rate limit fully implemented
- [ ] fsync after rename на всех платформах
- [ ] Transactional backup перед каждой записью

### P1 — Reliability
- [ ] Comprehensive integration tests
- [ ] Chaos testing for Supervisor recovery
- [ ] Schema migration tests
- [ ] Concurrent write protection tests
- [ ] Windows/Linux-specific lifecycle tests

### P2 — Packaging
- [ ] .deb/.rpm packages через CI pipeline
- [ ] GPG signatures для всех артефактов
- [ ] ARM64 builds и tests
- [ ] Windows MSI installer (через WiX)
- [ ] Release automation через GitHub Actions

## Future: v1.0

Production-ready single-binary manager for local AI runtimes with guaranteed data durability, comprehensive security model, and cross-platform support.

### Requirements:
- [ ] Все P0 features из v0.10
- [ ] Performance benchmarks
- [ ] Security audit (external)
- [ ] Documentation complete and reviewed
- [ ] All tests pass on Windows + Linux + ARM64
- [ ] CI pipeline with release gating
- [ ] Upgrade migration tooling
- [ ] Changelog и release notes

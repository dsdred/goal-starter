# Architecture

GoAl is a single-binary application composed of distinct ownership layers. This document describes the production V1 architecture.

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
| Application | `internal/application/` | Business logic: ProfileService, InstanceService, RuntimeService, ModelService. |
| Process lifecycle | `internal/process/` | Supervisor, Manager, LogBroker, LogStore, SlotReservation. |
| OS behavior | `internal/platform/` | Windows Job Object, Linux process groups. |
| HTTP / UI | `internal/webui/` | Routes, handlers, embedded templates, static assets, security. |
| Security | `internal/webui/security/` | Sessions, CSRF, password store (bcrypt). |
| Errors | `internal/webui/errors/` | Structured API error codes and classification. |
| Validation | `internal/webui/validation/` | Port, host, address validation. |
| Metrics | `internal/webui/metrics/` | Prometheus-format application metrics. |
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

**Atomic write sequence:**
1. Serialize to `.tmp` file in the same directory
2. `fsync` via `File.Sync()`
3. Save previous correct file as `.bak`
4. `os.Rename()` (atomic on Windows and Linux)
5. Sync parent directory

**Legacy stores:** `internal/webui/store/` contains file-based stores (profiles, runtimes, models) but is not used for production persistence. The authoritative store is `internal/storage/JSONRepository`.

## Process lifecycle

See [ARCHITECTURE.md - Process Lifecycle](#process-lifecycle) section below and [LIMITATIONS.md](LIMITATIONS.md).

## Configuration seed-once

`goal.json` is imported into `goal_repo.json` at first startup via `storage.SeedFromConfig()`. Subsequent startups only add new entities (by ID); existing entities in the repository are not overwritten.

See [ADR 004](adr/004-config-vs-repository-ownership.md).

## Configuration hot-reload

Hot-reload is implemented in `internal/config/reload.go` (`ReloadConfig`, `Watch`) but is not yet wired into main application startup. The config is read once at startup via `config.Load()`.

See [ADR 004](adr/004-config-vs-repository-ownership.md) and [CONFIGURATION.md](CONFIGURATION.md#hot-reload).

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
- Profile environment variables are merged with the parent process environment (profile variables override system variables).

## ADR summary

| ADR | Topic | Status |
|-----|-------|--------|
| 0001 | Product and architecture (Go, single binary, SSE logs) | Accepted |
| 0002 | Multi-instance Supervisor and Profile → Instance model | Accepted |
| 0003 | Web UI serving via embedded FS | Proposed |
| 0004 | Config file vs Repository ownership (seed-once) | Proposed |

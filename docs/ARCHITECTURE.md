# Architecture

GoAl is a single-binary application composed of distinct ownership layers. This document describes the current GoAl 2.0 architecture.

## Domain Model (v7)

GoAl 2.0 uses a simplified 3-entity domain:

- **Runtime** — execution engine configuration (executable, working directory, environment)
- **Model** — configured launch definition (runtime reference, launch args, environment, autostart)
- **Instance** — concrete launch history (immutable record of a process launch)

All launch parameters (`--host`, `--port`, `-m`, `--mmproj`, etc.) are expressed
through Model Args. There are no separate host/port fields on Model.

Physical model files (GGUF, MMProj) are NOT separate domain entities. They are ordinary
launch arguments (e.g., `-m <path>`, `--mmproj <path>`).

Relationship: Runtime ← Model → Instance

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

The unified JSON repository in `internal/storage/` (`JSONRepository`) is the sole persistence layer for all entities.

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
- Model environment variables are merged with the parent process environment (model variables override system variables).

## ADR summary

| ADR | Topic | Status |
|-----|-------|--------|
| 0001 | Product and architecture (Go, single binary, SSE logs) | Accepted |
| 0002 | Multi-instance Supervisor and Profile → Instance model | Accepted |
| 0003 | Web UI serving via embedded FS | Proposed |
| 0004 | Config file vs Repository ownership (seed-once) | Proposed |

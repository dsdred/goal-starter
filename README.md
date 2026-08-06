# GoAl

GoAl is a lightweight, cross-platform web service for managing local AI runtimes, models, launch profiles, processes, and logs.

## Status

**v0.9 — Architecture Consolidation (supervisor, instance model, application services).**

> This repository is an architectural starter. Security and reliability hardening in progress.

## Supported targets

- Windows amd64
- Linux amd64
- Planned: arm64

## Quick start on Windows

```powershell
.\scripts\bootstrap-windows.ps1
Copy-Item goal.example.json goal.json
$env:GOAL_CONFIG = (Resolve-Path .\goal.json)
go run .\cmd\goal
```

## Building

### Full cross-compile (Windows + Linux)

```powershell
.\scripts\build-all.ps1
```

This produces:
- `bin/goal-windows-amd64.exe` — Windows binary
- `bin/goal-linux-amd64` — Linux binary
- `bin/checksums.txt` — SHA256 checksums

### Manual cross-compilation

```powershell
# Windows
$env:GOOS='windows'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; go build -o bin/goal-windows-amd64.exe ./cmd/goal

# Linux
$env:GOOS='linux'; $env:GOARCH='amd64'; go build -o bin/goal-linux-amd64 ./cmd/goal

Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

## Required checks

```powershell
gofmt -w .
go test ./...
go test -race ./...  # requires CGO_ENABLED=1 and gcc
go vet ./...
go build ./cmd/goal
```

## Configuration

Copy and edit the example config:

```powershell
Copy-Item goal.example.json goal.json
```

The `goal.json` file is gitignored (contains secrets and user-specific paths).

### Configuration migration

GoAl automatically migrates configuration at startup. Supported steps:
- `1 -> 2`: Apply default healthCheck values for missing profile/runtime fields

Migration status endpoint: `GET /api/v1/migration/status`

### Configuration hot-reload

Configuration hot-reload is implemented in `internal/config` (`ReloadConfig`, `Watch` methods) but **not yet integrated** into main application startup. The config file is read once at startup via `config.Load()`.

Hot-reload supports safe live updates for:
- `logLevel` — changes logging verbosity without restart
- `healthCheck.interval` — changes health check frequency

Fields requiring restart:
- `listenAddress`, `webPort`, `dataDir`

Status: **WIP** — reload coordinator and restart-required reporting planned.

## API endpoints

### Authentication

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/auth/login` | Login (HTTP-only cookies) |
| POST | `/api/v1/auth/logout` | Logout |
| GET | `/api/v1/auth/session` | Check session |

### Process management

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Web dashboard |
| GET | `/api/v1/status` | Process status (legacy) |
| GET | `/api/v1/health` | Health check (stub) |
| GET | `/api/v1/version` | Version and metadata |
| GET | `/api/v1/metrics` | Application metrics |
| GET | `/api/v1/logs/stream` | SSE log stream |
| GET | `/api/v1/logs/query` | Log filtering and pagination |
| GET | `/api/v1/migration/status` | Migration status |

### Instance Management

Profile is a launch template. Instance is a running process.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/instances` | List all instances (from Supervisor) |
| GET | `/api/v1/instances/{id}` | Instance status |
| POST | `/api/v1/instances/{id}/stop` | Stop instance (auth + CSRF) |
| POST | `/api/v1/instances/{id}/restart` | Restart instance (auth + CSRF) |

### Profiles (CRUD)

Profile is a launch template, not a process.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/profiles` | List profiles |
| GET | `/api/v1/profiles/{id}` | Get profile |
| POST | `/api/v1/profiles` | Create profile |
| PUT | `/api/v1/profiles/` | Update profile |
| DELETE | `/api/v1/profiles/` | Delete profile |
| POST | `/api/v1/profiles/{id}/resolve` | Preview launch command |
| POST | `/api/v1/profiles/{id}/start` | Start process by profile |
| POST | `/api/v1/profiles/{id}/stop` | Stop all profile processes |
| POST | `/api/v1/profiles/{id}/restart` | Restart profile processes |
| GET | `/api/v1/profiles/{id}/status` | Profile process status |
| POST | `/api/v1/profiles/{id}/activate` | Activate profile |
| POST | `/api/v1/profiles/{id}/deactivate` | Deactivate profile |

### Runtimes

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/runtimes` | List runtimes |
| GET | `/api/v1/runtimes/{id}` | Get runtime |
| POST | `/api/v1/runtimes` | Create runtime |
| PUT | `/api/v1/runtimes/` | Update runtime |
| DELETE | `/api/v1/runtimes/` | Delete runtime |
| POST | `/api/v1/runtimes/{id}/start` | Start runtime process |
| POST | `/api/v1/runtimes/{id}/stop` | Stop runtime process |
| POST | `/api/v1/runtimes/{id}/restart` | Restart runtime process |
| POST | `/api/v1/runtimes/{id}/action/{action}` | Start/stop/restart (legacy) |
| GET | `/api/v1/runtimes/health` | Check all runtimes health |
| GET | `/api/v1/runtimes/health/{id}` | Check specific runtime health |

### Models

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/models` | List models |
| GET | `/api/v1/models/{id}` | Get model |
| POST | `/api/v1/models` | Create model |
| PUT | `/api/v1/models/` | Update model |
| DELETE | `/api/v1/models/` | Delete model |

### SSE / WebSocket

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/logs/stream` | SSE log stream |
| GET | `/api/v1/logs/query` | Log filtering and pagination |
| GET | `/ws` | WebSocket log stream (WIP) |

### Structured API errors

All errors return structured JSON:

```json
{"error":{"error_code":"invalid_port","error":"invalid port: out of range","details":[]}}
```

Codes: `bad_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `invalid_port`, `invalid_host`, `invalid_address`, `invalid_profile`, `invalid_runtime`, `invalid_model`, `internal_server_error`

## Security

- **Authentication** — HTTP-only cookies, session-based
- **CSRF protection** — CSRF token for all unsafe methods (GET, HEAD, OPTIONS, DELETE protected)
- **Rate limiting** — 100 requests per minute per IP
- **Login rate limit** — 5 attempts / 5 minutes
- **Request body size limit** — http.MaxBytesReader
- **Default bind** — 127.0.0.1 (all interfaces requires explicit config)
- **Secret env vars** — `AdminPassword` cleared on save
- **External bind** — requires `listenAddress` config change
- **Runtime path validation** — executable and working directory validated against allowed roots

## Architecture

### Domain Model: Profile → Instance

**Profile** is a launch template (configuration).
**Instance** is a running process (runtime entity).

```
Profile (static)
  ├─ runtime_id → Runtime
  ├─ model_id → Model (optional)
  ├─ args, environment, active
  └─ ...

Instance (dynamic, created on start)
  ├─ profile_id → Profile
  ├─ pid, state, exit_code
  ├─ started_at, stopped_at
  └─ ...
```

Separation means:
- Profiles are independent of process lifecycle
- Multiple instances can share one profile
- Stopping an instance does not delete the profile
- Restart creates a new instance with a new ID

### Process Management

GoAl uses a multi-instance `Supervisor` that manages multiple `process.Manager` instances — one per launch instance. Each `exec.Cmd` has exactly one owner calling `Wait()`. Process lifecycle is managed through the `platform.ProcessControl` interface:

- **Windows**: Job Object with kill-on-close
- **Linux**: Process group (SIGTERM/SIGKILL)

Process environments are merged with the parent process environment (profile variables override system variables).

SysProcAttr is removed from `CommandSpec` — platform-specific setup is performed via `platform.Prepare`.

### Recovery on Startup

On startup Supervisor:
1. Loads all `LaunchInstanceEntry` from repository
2. Checks which instances were running
3. Verifies PID liveness
4. If alive: updates state to `running`, subscribes to logs
5. If dead: marks as `exited` with `recovered` exit class

### Recovery Policy

- **Restorable**: PID exists, identity matches, pipes owned → restore state to `running`
- **Stale**: PID exists but identity mismatch or pipes lost → state becomes `stale`
- **Orphaned**: PID reused by another process → state becomes `orphaned`
- **Terminal states**: persist reliably via `RemoveTerminal()`

See `docs/adr/` for detailed recovery semantics.

### Data Storage

**Unified JSON Repository** (`goal_repo.json`) — single-file storage for runtimes, models, profiles, and instances.

Schema version: `4`. Atomic writes via `tmp + rename` pattern with backup recovery.

```
goal_repo.json           — active repository
goal_repo.json.tmp       — temporary write file
goal_repo.json.bak       — backup of last known good state
```

**Atomic write sequence:**
1. Serialize to temp file in same directory
2. `fsync` temp file (via `File.Sync()`)
3. Save previous correct file as `.bak`
4. `os.Rename` temp file (atomic on Windows and Linux)
5. Sync parent directory

**Limitations (current):**
- No fsync guarantee on all platforms (OS handles flushing)
- Corrupted file recovers from `.bak` automatically
- Concurrent write protection beyond mutex — WIP
- Schema migration tests — in progress

**Planned improvements:**
- fsync after rename on all platforms
- Consider SQLite for v1.0 (still single-binary)

### Logging

Process logs are stored per-instance via `process.LogStore` ring buffer (up to 10000 entries per instance). Access via:
- Real-time SSE streaming (`GET /api/v1/logs/stream`) — multi-instance LogBroker
- Filtering by stream, search, time range, instance_id
- Pagination (page/page_size) — aggregated after merging logs from all instances
- `GET /api/v1/logs` — QueryLogs with instance_id filter

LogBroker (`process.LogBroker`) provides:
- Subscription to logs from all running instances
- Filtering by instance_id
- Safe idempotent subscription cancellation
- Drop oldest policy for slow subscribers
- Correct shutdown behavior

Legacy `/api/v1/status` still reads from the first process manager. Per-instance endpoints: `GET /api/v1/instances` and `GET /api/v1/instances/{id}`.

### Health Checks

Periodic runtime health checking (every 30 seconds). Supports TCP and HTTP health checks. Health check definitions are built from Profile host/port fields.

### Configuration Hot-Reload

Configuration hot-reload is implemented (`internal/config` → `ReloadConfig`) but **not wired** into main startup. Status: **WIP**.

See "Configuration hot-reload" section above for details.

## Repository layout

| Path | Purpose |
|------|---------|
| `cmd/goal/` | Main entry point |
| `cmd/goal-msi/` | MSI installer builder |
| `internal/config/` | Config parsing, validation, hot-reload, migrations |
| `internal/process/` | Process lifecycle management, log store |
| `internal/platform/` | OS-specific process handling |
| `internal/version/` | Version and metadata |
| `internal/webui/` | HTTP server and templates |
| `internal/webui/errors/` | Structured API errors |
| `internal/webui/security/` | Authentication, CSRF, sessions |
| `internal/webui/validation/` | Port, host, address validation |
| `internal/webui/middleware/` | Logging middleware |
| `internal/webui/store/` | File-based store (profiles, runtimes, models) |
| `internal/webui/health/` | Runtime health check |
| `internal/webui/metrics/` | Application metrics |
| `internal/webui/websocket/` | WebSocket log stream |
| `internal/webui/logger/` | HTTP logger |
| `testdata/fake-runtime/` | Fake runtime for integration tests |
| `deploy/` | Systemd services, Windows service support |
| `scripts/` | Build and bootstrap scripts |
| `web/`, `webui/` | Static files and templates (duplicates for compatibility) |

## .gitignore

- `bin/` — build artifacts
- `data/` — runtime data
- `goal.json` — user configuration
- `*.log`, `*.tmp`, `*.bak` — temp files
- `*.exe` — compiled binaries
- `.env*` — environment secrets
- `goal` — dev binary (not tracked in git)

## Known limitations v0.9

- Hot-reload: not wired into main startup, WIP
- SQLite storage: not implemented (JSON only)
- ARM64: not tested
- Auto-update: GitHub-based only
- Full reattach to arbitrary PID: not implemented (stale detection only)
- Audit logging: WIP (metrics available only)
- Login rate limit: placeholder (not fully implemented)

## Before development

Review `AGENTS.md`, `BACKLOG.md`, `ROADMAP.md`, and `SUBAGENT_MASTER_PROMPT.md`.
# Limitations

This document lists the verified limitations of GoAl 2.0. Each item is either a deliberate design decision or a known gap.

## Process lifecycle

### No PID reattachment after restart

When GoAl restarts, running instances are marked as `stale`. GoAl does not verify whether the PID is still alive, does not reuse PIDs, and does not reattach to pipes.

**Impact:** After a restart, the user must manually start a new instance if the previous process is still running externally.

**Code:** `internal/process/supervisor.go` — `Recover()` marks `running|starting|stopping|pending` instances as `stale`.

### No pipe reattachment

stdout/stderr from a previously running process are not restored after restart. A new instance gets new pipes.

### Degraded persistence

If a process starts successfully but persistence fails, the process continues running and `LastError` is set on the snapshot. The instance is not rolled back.

## Logging

### SSE is the authoritative live-log transport

`GET /api/v1/logs/stream` uses Server-Sent Events with the `LogBroker` for multi-instance streaming. This is the production live-log mechanism.

### WebSocket is not wired

`/ws` is implemented in `internal/webui/websocket/` but not registered in `routes.go`. WebSocket is **not** part of the public API contract.

### Historical logs are in-memory

`LogStore` is a ring buffer (10000 entries per instance) held in memory. Logs are lost when the instance exits and is not recovered on restart (except via the process's own output logs).

### Aggregation scope

`GET /api/v1/logs` aggregates logs from all running instances. Filtering by `instance_id` is applied after aggregation. Pagination is global (applied once to the aggregated result).

## Health checks

### TCP HealthChecker results internal only

`GET /api/v1/runtimes/health` returns instance-based health (count and running instance details). The periodic TCP health checker (`HealthChecker` in `internal/webui/health/`) stores results internally but does not expose them via a separate API endpoint.

### HTTP health check is model/runtime only

Health check configuration is available in the goal.json config format for both models and runtimes. The checker targets `host:port` with optional `httpPath` and `httpStatus`.

## Configuration

### Seed-once policy

`goal.json` is imported into `goal_repo.json` only on first startup (or when ID doesn't exist). Subsequent edits to `goal.json` do not update existing repository entries.

**Workaround:** Delete the entity from `goal_repo.json` or use the API/UI to update it.

### Hot-reload not wired

Hot-reload is implemented (`internal/config/reload.go`) but not connected to main startup. The config file is read once at startup.

### Schema migration

Config schema: `1 -> 2` (add default health check config). Storage schema: `5 -> 6` (profiles become models, old physical models folded into args). Both run automatically at startup.

## Security

### Login rate limit is placeholder

`rateLimiter` is a placeholder in `RouteRegistry`. The actual rate limiting on login (5 attempts / 5 minutes) is not yet enforced.

### No external security audit

No third-party security audit has been performed.

### No GPG or Authenticode signatures

Release binaries are not GPG-signed, and Windows binaries are not Authenticode-signed. Verification is via SHA256 checksums in `checksums.txt`.

## Storage

### JSON only

No SQLite, PostgreSQL, or other database backend. Single JSON file with atomic write semantics.

### No concurrent write protection beyond mutex

`JSONRepository` uses a sync.Mutex for write protection. Concurrent writes from different processes (e.g., multiple GoAl instances pointing at the same `goal_repo.json`) may race.

### No schema versioning framework

Schema version is tracked (`version` field) with automatic migrations for config (v1→v2) and storage (v5→v6). No general-purpose migration framework exists; future schema changes require code-level migration functions.

## Packaging

### Windows only via PowerShell service scripts

`deploy/windows/install-service.ps1` and `uninstall-service.ps1` provide Windows service installation. No MSI installer or Chocolatey package.

### Linux only via systemd unit file

`deploy/systemd/goal.service` provides systemd integration. No .deb/.rpm packages.

## ARM64

ARM64 is not tested. Cross-compilation works (`GOOS=linux GOARCH=arm64`) but runtime behavior is unverified.

## Web UI

### Embedded FS serving

The Web UI is served from embedded filesystems. The binary must be self-contained — no external `web/` directory is required at runtime. See [ADR 003](adr/003-webui-embedded-fs.md).

## Auto-update

Auto-update via GitHub Releases API exists (`internal/updater/updater.go`) but is not wired into the Web UI.

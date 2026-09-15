# Limitations

This document lists the verified limitations of GoAl 2.0. Each item is either a deliberate design decision or a known gap.

## Process lifecycle

### No PID reattachment after restart

When GoAl restarts, transitional instances (`running`, `starting`, `stopping`, `pending`) are classified by `Recover()`:

- **`orphan`** — the process is still alive and its identity is confirmed (executable path match; start time where available). GoAl does not adopt or reattach to it.
- **`stale`** — the process is gone, has no recorded PID, or its identity cannot be confirmed.

In both cases GoAl does not reuse the PID, does not reattach to stdout/stderr pipes, and does not guarantee runtime readiness (a live process does not mean the model has finished loading or that its HTTP endpoint is accepting connections).

**Impact:** After a restart, the user must manually start a new instance or use the Dismiss/Kill actions on an `orphan` before starting again.

**Code:** `internal/process/supervisor.go` — `Recover()` + `classifyForRecovery()` (ADR 005).

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

### Hot-reload is explicit, not automatic

Config file changes are never applied automatically. The explicit endpoint `POST /api/v1/admin/reload` (auth + CSRF, [ADR 009](adr/009-hot-reload-wiring.md)) re-reads the config file and applies only hot-classified fields (`logLevel`); fields classified as restart-required (`listenAddress`, `webPort`, `dataDir`, `authEnabled`, `adminUser`) are reported in the response and take effect only at the next process restart. A rejected reload never writes the file and never applies credential material.

### Schema migration

Config schema: `1 -> 2` (apply defaults for ListenAddress, WebPort, DataDir). Storage schema: `≤5/6 -> 7 -> 8` (profiles become models, old physical models folded into args, pipelines added). Both run automatically at startup; current storage schema is v8.

## Security

### Login rate limit: request-rate based, not failure-based

Login rate limiting is enforced: per client address (TCP peer), at most 100 `POST /api/v1/auth/login` requests per fixed minute window, then HTTP 429 (`rate_limited`). Remaining gaps:

- The limit bounds **request rate**, not **failed attempts** (the originally sketched 5-attempts/5-minutes lockout is not implemented), so up to 100 password guesses per minute per address remain possible.
- `X-Forwarded-For`/`X-Real-IP` are not trusted (spoofable); behind a reverse proxy all clients share the proxy's 100/min bucket.

### No external security audit

No third-party security audit has been performed.

### No GPG or Authenticode signatures

Release binaries are not GPG-signed, and Windows binaries are not Authenticode-signed. Verification is via SHA256 checksums in `checksums.txt`.

## Storage

### JSON only

No SQLite, PostgreSQL, or other database backend. Single JSON file with atomic, durable write semantics (fsync, read-back verification, `.bak` backup before every write). Only one generation of backup is kept.

### Windows: no directory fsync

The supported Windows API model has no directory flush; rename durability relies on the NTFS log commit (see [ARCHITECTURE.md](ARCHITECTURE.md#authoritative-persistence)). On non-journaled volumes (FAT/exFAT, or NTFS with journaling disabled), a crash immediately after a rename may lose the most recent write.

### No concurrent write protection beyond mutex

`JSONRepository` uses a sync.Mutex for write protection. Concurrent writes from different processes (e.g., multiple GoAl instances pointing at the same `goal_repo.json`) may race.

### No schema versioning framework

Schema version is tracked (`version` field) with automatic migrations for config (v1→v2) and storage (≤5/6→7→8, current v8). No general-purpose migration framework exists; future schema changes require code-level migration functions.

## Packaging

### Windows service: in-binary registration only

Windows service installation is provided by the binary itself: `goal --service install` (Administrator required; [ADR 011](adr/011-windows-service.md)). Install refuses registration unless the resolved config passes validation, the effective `dataDir` exists (install never creates it), all runtime working directories and model paths (config-seeded and current repository) are absolute paths, and every runtime executable is either absolute or a relative path whose absolute working directory contains the executable (the service then runs the joined absolute path, exactly as a normal launch does — ADR 011 D3.2 addendum). A relative executable without an absolute working-directory anchor, or whose joined path does not exist, is never a supported service configuration and paths are never reinterpreted at runtime or rewritten by install. The service account is LocalSystem: everything the service touches must be accessible to LocalSystem, and `C:\Users\<user>\…` locations are not a supported service configuration. No MSI installer or Chocolatey package.

### Linux only via systemd unit file

`deploy/systemd/goal.service` provides systemd integration. No .deb/.rpm packages.

## ARM64

ARM64 is not tested. Cross-compilation works (`GOOS=linux GOARCH=arm64`) but runtime behavior is unverified.

## Web UI

### Embedded FS serving

The Web UI is served from embedded filesystems. The binary must be self-contained — no external `web/` directory is required at runtime. See [ADR 003](adr/003-webui-embedded-fs.md).

### Live log view: bounded client display window

The Web UI live log view renders at most the last 2000 log lines; older rendered lines are removed as new lines arrive. This is a client-side display window, **not** retention: the full in-memory history (10000 entries per instance) remains available through `GET /api/v1/logs` / `GET /api/v1/instances/{id}/logs`, and the SSE stream is unaffected.

## Auto-update

Auto-update via GitHub Releases API exists (`internal/updater/updater.go`) but is not wired into the Web UI.

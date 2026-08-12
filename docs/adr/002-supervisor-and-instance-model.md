# ADR 002: Multi-Instance Supervisor and Profile → Instance Model

**Status:** Accepted
**Date:** 2026-08-06
**Related:** ADR 001 (Process Ownership, Windows, Linux)

## Context

GoAl v0.8 had a single-process architecture: one `process.Manager` per application, one process at a time.
The API endpoints (`/api/v1/status`, `/api/v1/logs/stream`) were tied to this single manager.

As the project evolved, the need to run multiple AI runtimes concurrently became clear.
The old architecture could not support this because:
- One manager cannot own multiple `exec.Cmd` trees safely (Wait() has one owner)
- Process lifecycle was coupled to HTTP request context
- Logs from different runtimes were mixed in a single buffer

## Decision

### Multi-Instance Supervisor

GoAl now uses a `Supervisor` that manages multiple `process.Manager` instances — one per launch instance.

```
Supervisor
  ├─ Manager (instance-001)
  │   ├─ exec.Cmd
  │   └─ LogBroker
  ├─ Manager (instance-002)
  │   └─ exec.Cmd
  └─ ...
```

Each instance has:
- Its own `process.Manager` with exclusive `Wait()` ownership
- Its own `LogBroker` for log streaming
- Its own `LaunchInstanceEntry` in the repository

### Profile → Instance Model

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

This separation means:
- Profiles are independent of process lifecycle
- Multiple instances can share one profile
- Stopping an instance does not delete its profile
- Restarting creates a new instance with a new ID

### Lifecycle

```
1. User calls POST /profiles/{id}/start
2. ProfileService loads Profile + Runtime + Model
3. LaunchResolver builds immutable CommandSpec
4. Supervisor.StartProfile creates LaunchInstanceEntry
5. Instance Manager starts exec.Cmd
6. Platform (Windows Job Object / Linux process group) owns process tree
7. Instance state persisted to Repository after each state change
```

### Application Context vs Request Context

The Supervisor receives a long-lived `application.Context`, not an HTTP request context.
This ensures:
- Process continues running after HTTP request completes
- Shutdown is controlled by application signal handler (SIGINT/SIGTERM)
- 30-second forced shutdown context is separate from request context

### Recovery on Startup

On startup, the Supervisor:
1. Loads all `LaunchInstanceEntry` from repository
2. Checks if each instance was in a transitional state (running/starting/stopping/pending)
3. Marks them as `stale` and persists the updated state
4. Stale instances are NOT added to the active instance list (no PID reattachment, no liveness verification)

## Consequences

### Positive
- Multiple AI runtimes can run concurrently
- Processes are isolated: stopping one does not affect others
- Logs are per-instance
- Clean separation between configuration (Profile) and runtime (Instance)
- Recovery after crash is supported

### Negative
- More complex state management (need to track multiple instances)
- Repository must handle concurrent reads/writes
- Legacy `/api/v1/status` removed in v1.0.0 (was already deprecated)

### Trade-offs

The multi-instance model adds complexity to the repository (multiple entries per profile) and to the API (instance ID routing). This is acceptable because:
- Most users will run 1-3 runtimes simultaneously
- The Profile → Instance separation is architecturally cleaner
- Recovery and per-instance logs justify the complexity

## Notes

- Legacy `process.Manager` removed in v1.0.0
- All endpoints use Supervisor via `InstanceService`
- `/api/v1/status` removed in v1.0.0

## Related

- ADR 001: Process Ownership
- `internal/process/supervisor.go`
- `internal/application/instance_service.go`
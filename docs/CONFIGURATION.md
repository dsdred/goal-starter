# Configuration Reference

Configuration is loaded from a JSON file (default: `goal.json`) at application startup. The path can be overridden with `GOAL_CONFIG` environment variable.

**Schema version:** `2`

## Root fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `version` | int | No | `2` | Configuration schema version. Auto-migrated on load. |
| `listenAddress` | string | Yes | `127.0.0.1` | HTTP server bind address. |
| `webPort` | int | No | `8088` | HTTP server port (1–65535). |
| `dataDir` | string | No | `./data` | Directory for `goal_repo.json` and runtime data. |
| `adminUser` | string | No | `admin` | Administrator username. Required when `authEnabled` is true. |
| `adminPassword` | string | Conditional | `""` | Administrator password; required when `authEnabled=true`. Configuration files use mode `0600` on POSIX; restrict the containing directory with an ACL on Windows. |
| `authEnabled` | bool | No | `false` | Enable session-based authentication and CSRF. |
| `runtimes` | array | No | `[]` | Initial runtime definitions (seeded once). |
| `models` | array | No | `[]` | Initial model definitions (seeded once). |
| `profiles` | array | No | `[]` | Initial profile definitions (seeded once). |

### Configuration migration

GoAl automatically migrates configuration at startup:

- `1 -> 2`: Adds default `HealthCheck` configuration to all profiles and runtimes.

Migration runs via `config.MigrateConfig()` during `config.Load()`. No separate status endpoint.

### Config vs Repository

| Source | Role | Lifecycle |
|--------|------|-----------|
| `goal.json` | Startup seed | Read once at startup; subsequent edits do not update existing entities. New entities (by ID) are added. |
| `goal_repo.json` | Authoritative store | Written by API/UI; survives restarts; contains runtimes, models, and instances. |

`goal_repo.json` can contain runtime and model environment values and must be
protected as sensitive local data. Runtime and model API responses omit those
values and expose only their keys; the values remain available internally when
GoAl launches a runtime. This storage is not an encrypted secret vault.

After the first startup, `goal_repo.json` is the source of truth. To modify existing entities after the first run, use the API or Web UI.

## Runtime configuration

Each runtime entry defines an external inference server or service.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `id` | string | Yes | — | Unique identifier (lowercase, digits, hyphens). |
| `name` | string | Yes | — | Display name. |
| `executable` | string | Yes | — | Path to the server executable. |
| `workingDirectory` | string | No | `""` | Working directory for the process. |
| `defaultArgs` | array of string | No | `[]` | Legacy; migrated to model args. Retained for backward compatibility only. |
| `environment` | map[string]string | No | `{}` | Process environment variables. |
| `healthCheck` | object | No | — | Health check configuration (see below). |
| `active` | bool | No | `true` | Whether the runtime is enabled. |

Runtime environment values may contain sensitive process configuration. The
HTTP API accepts them as write-only input and returns sorted
`environment_keys`, never the values. For API updates, omitting `environment`
preserves the stored map, `{}` clears it, and an explicit map replaces it.

### Runtime healthCheck

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `type` | string | No | `"tcp"` | `"tcp"` or `"http"`. |
| `enabled` | bool | No | `true` | Whether periodic health checking is active. |
| `interval` | int | No | `30` | Seconds between health checks. |
| `timeout` | int | No | `3` | Seconds per health check attempt. |
| `host` | string | Conditional | — | Target host for TCP checks. |
| `port` | int | Conditional | — | Target port for TCP checks. |
| `httpPath` | string | Conditional | — | HTTP path for HTTP health checks. |

## Model configuration

Each model entry in `goal.json` uses the legacy v5 format. At startup, `SeedFromConfig`
folds old model data (`path`, `arguments`) into the launch args of the corresponding
GoAl 2.0 Model (derived from `profiles`). New models created via the API/UI use the
simplified format: `id`, `name`, `runtimeId`, `args`, `environment`.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `id` | string | Yes | — | Unique identifier. |
| `name` | string | Yes | — | Display name. |
| `runtimeId` | string | No | — | ID of the runtime this model targets. |
| `path` | string | No | `""` | Direct path to a GGUF file (legacy; folded into args as `-m <path>`). |
| `arguments` | array of string | No | `[]` | Command-line arguments (legacy; folded into model args). |
| `environment` | map[string]string | No | `{}` | Process environment variables. |

## Profile configuration (legacy `goal.json` format)

Profiles in `goal.json` are the legacy launch template format. At startup, each profile
becomes a GoAl 2.0 **Model**: the profile's `args` are preserved, and if `modelId`
references a legacy model entry, that model's `path` and `arguments` are folded into
the resulting model's args (e.g., `-m <path>`). There is no separate "profile" entity
in the GoAl 2.0 domain.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `id` | string | Yes | — | Unique identifier. |
| `name` | string | Yes | — | Display name. |
| `runtimeId` | string | Yes | — | ID of the target runtime. |
| `modelId` | string | No | `""` | ID of a legacy model entry to fold into args (optional). |
| `host` | string | No | `""` | Override target host. |
| `port` | int | No | `0` | Override target port. |
| `args` | array of string | No | `[]` | Additional command-line arguments (accepts legacy `arguments` key). |
| `environment` | map[string]string | No | `{}` | Process environment variables. |
| `healthCheck` | object | No | — | Profile-specific health check (see below). |
| `active` | bool | No | `true` | Whether the profile is enabled. |

### Profile healthCheck

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | bool | No | `true` | Whether health checking is active. |
| `interval` | int | No | `30` | Seconds between checks. |
| `timeout` | int | No | `5` | Seconds per check. |
| `httpPath` | string | No | `"/health"` | HTTP path to check. |
| `httpStatus` | int | No | `200` | Expected HTTP status code. |

### Profile args key compatibility

The profile JSON accepts both `args` and `arguments` as the key for additional command-line arguments. The parser treats them as equivalent for backward compatibility.

## Storage migration (v5 → v6)

On first startup with a v5 `goal_repo.json`, GoAl automatically migrates to v6:

- `profiles` entries become `models` (launch definitions).
- Old physical `models` entries (with `path`/`arguments`) are folded into the corresponding model's launch args (e.g., `-m <path>`).
- `profile_id` in instances becomes `model_id`.
- Instance history is preserved.
- Resolved-command semantics are maintained: the final launch command is identical before and after migration.

After migration, `goal_repo.json` contains only `runtimes`, `models`, and `instances`.

## Hot-reload

The following fields can be changed in `goal.json` without restarting GoAl:

| Field | Requires restart |
|-------|-----------------|
| `logLevel` | No |
| `healthCheck.interval` | No |

The following fields require a restart:

| Field |
|-------|
| `listenAddress` |
| `webPort` |
| `dataDir` |

Hot-reload is implemented in `internal/config` but not yet wired into main startup.

## Security implications

| Field | Security note |
|-------|--------------|
| `adminPassword` | Persisted in `goal.json` so authentication survives restart; stored as a bcrypt hash in memory. Protect the file with POSIX permissions or a Windows ACL. |
| `authEnabled` | Must be `true` when `listenAddress` is non-loopback. |
| `dataDir` | Contains `goal_repo.json` (secrets, paths). Default `./data` is gitignored. |

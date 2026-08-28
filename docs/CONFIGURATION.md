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
| `adminPasswordHash` | string | Conditional | `""` | Bcrypt hash of the admin password (cost 12, 60 chars). Required when `authEnabled=true`. Never plaintext. |
| `authEnabled` | bool | No | `false` | Enable session-based authentication and CSRF. |
| `logLevel` | string | No | `info` | Application log level: `debug`, `info`, `warn`, `error`. Absent means `info`. Hot field (applied by reload, no restart). |
| `runtimes` | array | No | `[]` | Initial runtime definitions (seeded once). |
| `models` | array | No | `[]` | Initial model definitions (seeded once). |
| `profiles` | array | No | `[]` | Initial profile definitions (seeded once). |

### Configuration migration

GoAl automatically migrates configuration at startup:

- `1 -> 2`: Adds default `HealthCheck` configuration to all profiles and runtimes.
- **Credential migration** (content-based): If a legacy `adminPassword` plaintext is detected in the config, it is hashed with bcrypt (cost 12) and stored in `adminPasswordHash`. The `adminPassword` field is then cleared. This runs as an explicit step after `config.Load()` via `config.MigrateCredentials()`. If the hash already exists, no re-hashing occurs.

Migration runs at startup. No separate status endpoint.

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

### Audit log file

The security audit log (ADR 007) lives in the data directory as `goal_audit.jsonl` (JSON Lines, mode `0600`). There are **no config fields** for it in the first scope: file location, the 10 MiB rotation threshold, and the 3-generation retention (3 × 10 MiB max: `goal_audit.jsonl`, `.1`, `.2`) are fixed constants in the audit package (`internal/webui/audit`). Every event line is fsynced on write; a write failure is fail-open for the business operation (structured operational log entry, no event payload). Query it through `GET /api/v1/admin/audit` or read the file directly (`tail`/`grep`); include `goal_audit.jsonl*` in `dataDir` backups. See [SECURITY.md](SECURITY.md#audit-trail) for the event taxonomy and secret-safety rules.

### Write durability

Both `goal.json` and `goal_repo.json` are written with the same durable-write contract (`fsutil.WriteFileDurable`): an fsynced temp file in the same directory, read-back verification, an atomic backup of the previous file as `<file>.bak` before every write, an atomic `rename`, and a parent-directory `fsync` on POSIX (Windows: rename durability is provided by the NTFS log commit — see [ARCHITECTURE.md](ARCHITECTURE.md#authoritative-persistence) for the exact per-platform sequence and guarantees). A failed write is returned as an error and never leaves a partially written file at the target path. One generation of backup is kept (`goal.json.bak` / `goal_repo.json.bak`) and must be protected with the same permissions as the main files.

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
simplified format: `id`, `name`, `runtime_id`, `args`, `environment`.

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

## Hot-reload (ADR 009)

Field classification is authoritative per [ADR 009](adr/009-hot-reload-wiring.md):

| Field | Class | Notes |
|-------|-------|-------|
| `logLevel` | **hot** | Applied immediately by reload. |
| `adminPasswordHash` | **hot via settings endpoint only** | `PUT /api/v1/settings` updates the live credential store immediately; a hand-edited hash in `goal.json` is applied only at the next restart. Reload never applies credential material. |
| `listenAddress` | **restart** | The HTTP listener is bound at startup; rebind is not supported. |
| `webPort` | **restart** | Same as `listenAddress`. |
| `dataDir` | **restart** | Repository and audit-log paths are fixed at startup. |
| `authEnabled` | **restart** | Baked into the route registry at startup. |
| `adminUser` | **restart** | Baked into the route registry at startup. |
| `runtimes`, `models`, `profiles` | **seed-only, never re-applied** | Seed once into `goal_repo.json` at startup; live editing happens via API. Reload never re-seeds (it would overwrite user data). |

Trigger: `POST /api/v1/admin/reload` (auth + CSRF protected). There is no file watching, no SIGHUP, and no polling — a reload happens only when explicitly requested. The endpoint re-reads and validates `goal.json`, applies hot fields, and responds with `{"status":"reloaded","applied":[...],"restart_required":[...]}` (field names only). A rejected reload (unreadable or invalid file) returns `400 {"status":"rejected",...}` and changes nothing: the file on disk is never modified by reload and live values are untouched. Every reload attempt emits a `config.reload` audit event (field names only).

The `PUT /api/v1/settings` hint contract is unchanged: password-only change → `hint=ok`; anything else → `hint=restart_required`.

## Runtime Name uniqueness

`Runtime.Name` must be unique across all runtimes (case-insensitive). The API returns `409 Conflict` when creating or renaming a runtime to an already-existing name. A runtime can be edited without changing its own name.

## Active Instances vs Instance History

| Page | States shown | Source | Actions |
|------|-------------|--------|---------|
| **Instances** (Экземпляры) | Active only: `starting`, `running`, `stopping` | In-memory supervisor | Logs, Stop, Restart |
| **Instance History** (История) | Terminal only: `exited`, `failed`, `stale` | Persistent repository (`goal_repo.json`) | Logs, Cleanup |

Terminal instances move from Instances to History automatically when they reach a terminal state. History is repository-backed: records persist across GoAl restarts. The `GET /api/v1/history` endpoint reads terminal instances directly from the persistent store, ensuring they remain visible after process restart. History cleanup deletes terminal instances only; active instances are never affected.

## Web UI Sidebar

The sidebar footer contains compact Theme and Language selectors, a server status indicator with version, and (when authentication is enabled) the authenticated username with a Logout button. When authentication is disabled, no username or Logout is displayed.

The server status dot is live: the client polls `GET /api/v1/health` every 5 seconds (plus immediate checks on browser offline/online events and on live-log stream errors) and switches the dot between green (`online`) and red (`offline`). While the server is unreachable, a red connection banner is shown at the top of the page; on recovery the banner is hidden and a localized "connection restored" toast appears. Any HTTP response counts as reachable — only a fetch-level network failure marks the connection offline.

## Server settings in Web UI

Server parameters (`listenAddress`, `webPort`, `authEnabled`, `adminUser`, and the admin password) can be edited from **Settings → Server → Edit**. The edit modal is where the "Enable authentication" toggle and the admin username / password fields live.

Behavior:
- **Restart hint.** Server settings are written to `goal.json`. Password changes take effect immediately (the live credential store is updated); other changes (port, address, auth toggle) take effect after a GoAl restart (hot-reconfiguration of the HTTP listener is not supported). The response `hint` (`ok` / `restart_required`) and the UI message reflect this.
- **Credentials.** Enabling authentication requires a non-empty `adminUser` and a password (up to 72 bytes). The entered value is hashed with bcrypt (cost 12) and stored as `adminPasswordHash`; it is never persisted in plaintext. When a password is already configured, the password field may be left empty to **keep the current password** (an empty value never erases the stored one); entering a value replaces it. The stored hash is never returned by the API — `GET /api/v1/metrics` only reports `admin_user` and a boolean `admin_password_set`.
- **Validation.** The server rejects enabling auth without both credentials (`400 cannot enable auth: ...`), invalid bind addresses, and out-of-range ports.

To change server configuration without the UI:
1. Stop GoAl.
2. Edit `goal.json` (path via `GOAL_CONFIG` or default location).
3. Start GoAl again.

## Security implications

| Field | Security note |
|-------|--------------|
| `adminPasswordHash` | Persisted in `goal.json` as a bcrypt hash (cost 12) so authentication survives restart; plaintext is never persisted (legacy `adminPassword` auto-migrates on first startup). Protect the file with POSIX permissions or a Windows ACL. |
| `authEnabled` | Recommended `true` when `listenAddress` is non-loopback. If `false` on non-loopback, a prominent security warning is emitted but startup is not blocked. |
| `dataDir` | Contains `goal_repo.json` (secrets, paths). Default `./data` is gitignored. |

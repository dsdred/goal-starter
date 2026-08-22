# API Reference

All endpoints are under the `/api/v1` prefix. Base URL is `http://127.0.0.1:8088` (configurable via `webPort`).

## Authentication model

| Mode | `authEnabled` | Effect |
|------|--------------|--------|
| Private | `true` | Management endpoints require a valid session cookie. Login, session discovery, `/health`, `/version`, the UI shell, and static assets remain public. Unsafe authenticated methods additionally require a CSRF token. |
| Public | `false` | No authentication required. Non-loopback bind (`0.0.0.0`) emits a prominent security warning (startup is not blocked). |

Session cookie: `goal_session` (HTTP-only, SameSite=Lax).
CSRF cookie: `goal_csrf_token` (double-submit pattern). Send the same value in `X-CSRF-Token` for unsafe authenticated requests.

## Structured errors

All errors return JSON:

```json
{
  "error": {
    "error_code": "invalid_port",
    "error": "invalid port: out of range",
    "details": []
  }
}
```

Error codes: `bad_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `invalid_port`, `invalid_host`, `invalid_address`, `invalid_runtime`, `invalid_model`, `internal_server_error`.

## Health & version

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/health` | No | Basic health check. |
| `GET` | `/api/v1/version` | No | Returns version, git commit, build time. |

## Authentication

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| `POST` | `/api/v1/auth/login` | No | No | Login with credentials. Sets session cookie. |
| `POST` | `/api/v1/auth/logout` | Yes | Yes | Clears session cookie. |
| `GET` | `/api/v1/auth/session` | No | — | Reports whether the current browser session is authenticated. |

## Sessions & admin

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/session` | Yes | — | Current session info. |
| `GET` | `/api/v1/admin/users` | Yes | — | List configured users. |
| `GET` | `/api/v1/admin/sessions` | Yes | — | List active sessions. |
| `GET` | `/api/v1/metrics` | Yes | — | Instance counts plus server settings: `listen_address`, `web_port`, `auth_enabled`, `admin_user`, `admin_password_set` (boolean; the password itself is never returned). |
| `PUT` | `/api/v1/settings` | Yes | Yes | Save server settings (`listen_address`, `web_port`, `auth_enabled`, optional `admin_user`, `admin_password`). Empty `admin_password` preserves the stored one. Enabling auth without credentials → `400`. Requires restart. |

## Instances (processes)

Instances are running processes created from models.

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| `GET` | `/api/v1/instances` | Yes | — | List all instances. |
| `GET` | `/api/v1/instances/{id}` | Yes | — | Instance detail. |
| `GET` | `/api/v1/history` | Yes | — | List terminal instances (repository-backed, persists across restart). |
| `POST` | `/api/v1/instances/start` | Yes | Yes | Start a new instance from a model. |
| `POST` | `/api/v1/instances/{id}/stop` | Yes | Yes | Stop an instance. |
| `POST` | `/api/v1/instances/{id}/restart` | Yes | Yes | Restart an instance. |
| `POST` | `/api/v1/instances/{id}/dismiss` | Yes | Yes | Dismiss an orphan instance (transitions `orphan` → `stale`). No process is touched. |

### Instance logs

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/instances/{id}/logs` | Yes | — | Historical logs for instance (query with filters). |
| `GET` | `/api/v1/instances/{id}/logs/stream` | Yes | — | SSE log stream for instance. |

### POST /api/v1/instances/cleanup

Mass-delete terminal (non-active) instances. Requires auth + CSRF.

Request body:
```json
{ "mode": "all_terminal" }
```

Modes: `all_terminal`, `older_than_7d`, `older_than_30d`, `selected` (requires `ids` array).

Response 200:
```json
{ "status": "cleaned", "deleted": 5 }
```

### POST /api/v1/runtimes/{id}/replace

Rebind all models from the given runtime to a new runtime, then delete the old one. Requires auth + CSRF.

Request body:
```json
{ "new_runtime_id": "rt-new-id" }
```

Response 200:
```json
{ "status": "replaced", "models_moved": 3 }
```

### POST /api/v1/runtimes/{id}/cascade-delete

Delete the runtime and all models referencing it. Instance history is preserved. Requires auth + CSRF.

No request body.

Response 200:
```json
{ "status": "deleted", "models_deleted": 2 }
```

## Runtimes

Runtime `environment` values are write-only. Runtime read and mutation
responses never return the values; they return sorted `environment_keys`
instead. On `PUT`, omitting `environment` preserves the stored map, an explicit
empty object clears it, and a non-empty object replaces it. Runtime environment
values remain available internally for process launch.

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| `GET` | `/api/v1/runtimes` | Yes | — | List runtimes. |
| `GET` | `/api/v1/runtimes/{id}` | Yes | — | Get runtime. |
| `POST` | `/api/v1/runtimes` | Yes | Yes | Create runtime. |
| `PUT` | `/api/v1/runtimes/{id}` | Yes | Yes | Update runtime. |
| `DELETE` | `/api/v1/runtimes/{id}` | Yes | Yes | Delete runtime. |
| `POST` | `/api/v1/runtimes/{id}/action/{action}` | Yes | Yes | Legacy action endpoint. |
| `GET` | `/api/v1/runtimes/health` | Yes | — | Health of all runtimes (instance-based). |
| `GET` | `/api/v1/runtimes/health/{id}` | Yes | — | Health of specific runtime. |

## Models

Models are configured launch definitions combining a runtime with launch arguments and environment.

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/models | List all models |
| GET | /api/v1/models/{id} | Get a model |
| POST | /api/v1/models | Create a model |
| PUT | /api/v1/models/{id} | Update a model |
| DELETE | /api/v1/models/{id} | Delete a model |
| POST | /api/v1/models/{id}/start | Start an instance |
| POST | /api/v1/models/{id}/stop | Stop active instances |
| POST | /api/v1/models/{id}/restart | Restart |
| GET | /api/v1/models/{id}/status | Get instance status |
| POST | /api/v1/models/{id}/activate | Enable autostart |
| POST | /api/v1/models/{id}/deactivate | Disable autostart |
| POST | /api/v1/models/{id}/resolve | Preview resolved command |

Model environment values are write-only: they are accepted on create/update but never
returned in API responses. Only `environment_keys` (the list of variable names) is exposed.

## Logs (aggregated)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/logs` | Yes | — | Query aggregated logs (filters: `stream`, `search`, `instance_id`, `page`, `page_size`). |
| `GET` | `/api/v1/logs/stream` | Yes | — | SSE log stream (multi-instance LogBroker). |

## Not part of the public contract

| Path | Status |
|------|--------|
| `/ws` | WebSocket implemented in `internal/webui/websocket/` but not wired to routes. |
| `/api/v1/migration/status` | Migration runs automatically; no status endpoint. |

## Web dashboard

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/` | Conditional | Embedded Web UI dashboard. |
| `GET` | `/static/*` | No | Static assets from embedded FS. |

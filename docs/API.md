# API Reference

All endpoints are under the `/api/v1` prefix. Base URL is `http://127.0.0.1:9090` (configurable via `webPort`).

## Authentication model

| Mode | `authEnabled` | Effect |
|------|--------------|--------|
| Private | `true` | All endpoints (except `/health` and `/version`) require valid session cookie. Unsafe methods additionally require CSRF token. |
| Public | `false` | No authentication required. Non-loopback bind (`0.0.0.0`) rejects `authEnabled=false`. |

Session cookie: `session` (HTTP-only, SameSite=Lax).
CSRF cookie: `csrf` (double-submit pattern).

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

Error codes: `bad_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `invalid_port`, `invalid_host`, `invalid_address`, `invalid_profile`, `invalid_runtime`, `invalid_model`, `internal_server_error`.

## Health & version

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/health` | No | Basic health check. |
| `GET` | `/api/v1/version` | No | Returns version, git commit, build time. |

## Authentication

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| `POST` | `/api/v1/auth/login` | No | No | Login with credentials. Sets session cookie. |
| `POST` | `/api/v1/auth/logout` | Yes | No | Clears session cookie. |

## Sessions & admin

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/session` | Yes | — | Current session info. |
| `GET` | `/api/v1/admin/users` | Yes | — | List configured users. |
| `GET` | `/api/v1/admin/sessions` | Yes | — | List active sessions. |
| `GET` | `/api/v1/metrics` | Yes | — | Application metrics. |

## Instances (processes)

Instances are running processes created from profiles.

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| `GET` | `/api/v1/instances` | Yes | — | List all instances. |
| `GET` | `/api/v1/instances/{id}` | Yes | — | Instance detail. |
| `POST` | `/api/v1/instances/start` | Yes | Yes | Start a new instance from a profile. |
| `POST` | `/api/v1/instances/{id}/stop` | Yes | Yes | Stop an instance. |
| `POST` | `/api/v1/instances/{id}/restart` | Yes | Yes | Restart an instance. |

### Instance logs

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/instances/{id}/logs` | Yes | — | Historical logs for instance (query with filters). |
| `GET` | `/api/v1/instances/{id}/logs/stream` | Yes | — | SSE log stream for instance. |

## Profiles (launch templates)

Profiles are static configuration. Instances are created from profiles.

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| `GET` | `/api/v1/profiles` | Yes | — | List profiles. |
| `GET` | `/api/v1/profiles/{id}` | Yes | — | Get profile. |
| `POST` | `/api/v1/profiles` | Yes | Yes | Create profile. |
| `PUT` | `/api/v1/profiles/{id}` | Yes | Yes | Update profile. |
| `DELETE` | `/api/v1/profiles/{id}` | Yes | Yes | Delete profile. |
| `POST` | `/api/v1/profiles/{id}/start` | Yes | Yes | Start instance from profile. |
| `POST` | `/api/v1/profiles/{id}/stop` | Yes | Yes | Stop all instances from profile. |
| `POST` | `/api/v1/profiles/{id}/restart` | Yes | Yes | Restart all instances from profile. |
| `POST` | `/api/v1/profiles/{id}/action/{action}` | Yes | Yes | Legacy action endpoint (start/stop/restart). |
| `GET` | `/api/v1/profiles/{id}/status` | Yes | — | Process status for profile. |
| `POST` | `/api/v1/profiles/{id}/activate` | Yes | Yes | Activate profile. |
| `POST` | `/api/v1/profiles/{id}/deactivate` | Yes | Yes | Deactivate profile. |
| `POST` | `/api/v1/profiles/{id}/resolve` | Yes | Yes | Preview resolved launch command. |

## Runtimes

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| `GET` | `/api/v1/runtimes` | Yes | — | List runtimes. |
| `GET` | `/api/v1/runtimes/{id}` | Yes | — | Get runtime. |
| `POST` | `/api/v1/runtimes` | Yes | Yes | Create runtime. |
| `PUT` | `/api/v1/runtimes/{id}` | Yes | Yes | Update runtime. |
| `DELETE` | `/api/v1/runtimes/{id}` | Yes | Yes | Delete runtime. |
| `POST` | `/api/v1/runtimes/{id}/start` | Yes | Yes | Start runtime process. |
| `POST` | `/api/v1/runtimes/{id}/stop` | Yes | Yes | Stop runtime process. |
| `POST` | `/api/v1/runtimes/{id}/restart` | Yes | Yes | Restart runtime process. |
| `POST` | `/api/v1/runtimes/{id}/action/{action}` | Yes | Yes | Legacy action endpoint. |
| `GET` | `/api/v1/runtimes/health` | Yes | — | Health of all runtimes (instance-based). |
| `GET` | `/api/v1/runtimes/health/{id}` | Yes | — | Health of specific runtime. |

## Models

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| `GET` | `/api/v1/models` | Yes | — | List models. |
| `GET` | `/api/v1/models/{id}` | Yes | — | Get model. |
| `POST` | `/api/v1/models` | Yes | Yes | Create model. |
| `PUT` | `/api/v1/models/{id}` | Yes | Yes | Update model. |
| `DELETE` | `/api/v1/models/{id}` | Yes | Yes | Delete model. |

## Logs (aggregated)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/logs` | Yes | — | Query aggregated logs (filters: `stream`, `search`, `instance_id`, `page`, `page_size`). |
| `GET` | `/api/v1/logs/stream` | Yes | — | SSE log stream (multi-instance LogBroker). |

## Not part of V1 public contract

| Path | Status |
|------|--------|
| `/ws` | WebSocket implemented in `internal/webui/websocket/` but not wired to routes. |
| `/api/v1/migration/status` | Migration runs automatically; no status endpoint. |

## Web dashboard

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/` | Conditional | Embedded Web UI dashboard. |
| `GET` | `/static/*` | No | Static assets from embedded FS. |

# API Reference

All endpoints are under the `/api/v1` prefix. Base URL is `http://127.0.0.1:8088` (configurable via `webPort`).

## Authentication model

| Mode | `authEnabled` | Effect |
|------|--------------|--------|
| Private | `true` | Management endpoints require a valid session cookie. Login, session discovery, `/health`, `/version`, the UI shell, and static assets remain public. Unsafe authenticated methods additionally require a CSRF token. |
| Public | `false` | No authentication required. Non-loopback bind (`0.0.0.0`) emits a prominent security warning (startup is not blocked). |

Session cookie: `goal_session` (HTTP-only, SameSite=Lax).
CSRF cookie: `goal_csrf_token` (double-submit pattern). Send the same value in `X-CSRF-Token` for unsafe authenticated requests.

## Error responses

All errors return JSON with an `error` string. Two shapes occur:

```json
{ "error": "runtime not found: rt-1" }
```

```json
{ "error": "too many login attempts, please try again later", "code": "rate_limited" }
```

The second shape adds a stable `code` (and optional `details`, an array of strings) on the following endpoints: login rate limiting (`rate_limited`), runtime delete/replace/cascade-delete (404/409 mapping: `not_found`, `invalid_runtime`, `conflict`, `bad_request`), model 404 (`invalid_model`), the audit query endpoint (`bad_request`, `internal_server_error`), every process-lifecycle endpoint (see [Lifecycle error mapping](#lifecycle-error-mapping)), and the pipeline group lifecycle bodies, which carry the same two keys *inside* an otherwise complete per-entry payload.

Known error codes: `bad_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `rate_limited`, `gone`, `service_unavailable`, `invalid_port`, `invalid_host`, `invalid_address`, `invalid_runtime`, `invalid_model`, `internal_server_error`.

The `error` strings are English by design; the Web UI maps them to localized messages on the client side.

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

### Login rate limiting

`POST /api/v1/auth/login` is rate-limited per client address (TCP peer): at most **100 requests per minute**. Exceeding the limit returns `429 Too Many Requests`:

```json
{ "error": "too many login attempts, please try again later", "code": "rate_limited" }
```

`X-Forwarded-For` / `X-Real-IP` headers are **not** used for rate limiting (client-supplied, spoofable). Behind a reverse proxy, all clients share the proxy's 100/min bucket.

## Sessions & admin

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/session` | Yes | — | Current session info. |
| `GET` | `/api/v1/admin/users` | Yes | — | List configured users. |
| `GET` | `/api/v1/admin/sessions` | Yes | — | List active sessions. |
| `GET` | `/api/v1/admin/audit` | Yes | — | Query the security audit log ([details below](#get-apiv1adminaudit)). |
| `GET` | `/api/v1/metrics` | Yes | — | Instance counts plus server settings: `listen_address`, `web_port`, `auth_enabled`, `admin_user`, `admin_password_set` (boolean; the password itself is never returned). |
| `PUT` | `/api/v1/settings` | Yes | Yes | Save server settings (`listen_address`, `web_port`, `auth_enabled`, optional `admin_user`, `admin_password`). Empty `admin_password` preserves the stored hash. Password changes take effect immediately (no restart); other changes require restart (indicated by `hint` in response). Password >72 bytes → `400`. Enabling auth without credentials → `400`. |
| `POST` | `/api/v1/admin/reload` | Yes | Yes | Explicit hot-reload (ADR 009): re-reads and validates `goal.json`, applies hot fields (`logLevel`), responds `{"status":"reloaded","applied":[...],"restart_required":[...]}` (field names only). Never writes the file, never applies credential material, never re-seeds runtimes/models/profiles. Unreadable or invalid file → `400 {"status":"rejected","error":"...","code":"bad_request"}` with live values and the file untouched. Every attempt is audited as `config.reload`. |

### GET /api/v1/admin/audit

Query the durable security audit log (ADR 007). Requires auth; no CSRF (GET). The file is the source of truth and is read on every request; a missing file (fresh install) returns `200` with an empty list.

Query parameters:

| Parameter | Default | Notes |
|-----------|---------|-------|
| `limit` | `100` | Page size, max `1000`. |
| `offset` | `0` | Number of matching events to skip. |
| `event` | — | Exact event-name filter (e.g. `login.failure`). |

Response 200 (events **newest first**):

```json
{
  "events": [
    {
      "ts": "2026-08-24T12:00:00Z",
      "event": "instance.start",
      "user": "admin",
      "src_ip": "127.0.0.1",
      "detail": { "model_id": "model_1", "instance_id": "inst_abc" }
    }
  ],
  "total": 137
}
```

`total` is the count of **all** matching events, not just this page. `src_ip` is the TCP peer address only (`X-Forwarded-For`/`X-Real-IP` are not trusted). `detail` carries identifiers and booleans only — never secrets.

First-scope event taxonomy: `login.success`, `login.failure` (attempted user), `login.rate_limited`, `session.logout`, `settings.saved` (changed field *names*; `password_changed`), `instance.start` (success and failure), `instance.stop`, `instance.restart`, `instance.dismiss`, `instance.kill` (every kill attempt that passes the state precondition; detail `instance_id` + bounded `outcome` `terminated|reconciled|refused` + `reason`), `instance.cleanup` (`mode` + `deleted` count), `config.reload` (ADR 009; `status` `reloaded|rejected` + bounded field-name lists `applied` / `restart_required`; rejected events carry `error=invalid_config`, never file content), `pipeline.start` / `pipeline.stop` / `pipeline.restart` (ADR 010; bounded per-outcome counters).

Entity-CRUD extension (ADR 007 §2a): `model.create|update|delete|activate|deactivate`, `runtime.create|update|delete|replace|cascade_delete`, `pipeline.create|update|delete` — success-only, after the durable mutation, no event on 400/404/409. Detail: identifiers, bounded counts/booleans, and changed field *names* only (sentinel `"changed"`, never values) — `model.update`/`runtime.update`/`pipeline.update` record one `"<field>":"changed"` key per changed field; `runtime.replace` adds `new_runtime_id` + `models_moved`; `runtime.cascade_delete` adds `models_deleted`; `pipeline.create` adds `entries`; `model.activate`/`model.deactivate` add `active` `true`/`false`. Model-page and runtime-page process actions are not audited here (the `instance.*` trail is the sole process-lifecycle record).

The audit log never contains passwords or hashes, session/CSRF tokens, environment values, entity names, launch args, executable/working-directory paths, request bodies, or raw headers.

## Instances (processes)

Instances are running processes created from models.

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| `GET` | `/api/v1/instances` | Yes | — | List all instances. |
| `GET` | `/api/v1/instances/{id}` | Yes | — | Instance detail. |
| `GET` | `/api/v1/history` | Yes | — | List terminal instances (repository-backed, persists across restart). |
| `POST` | `/api/v1/instances/start` | Yes | Yes | Start a new instance from a model. |
| `POST` | `/api/v1/instances/{id}/stop` | Yes | Yes | Stop an instance. Returns `409` with `code=conflict`, `error=launch_in_flight` if the instance is in `pending` state (launch not yet complete), `404` with `error=instance_not_found` if this process has no controller for the id. See [Lifecycle error mapping](#lifecycle-error-mapping). |
| `POST` | `/api/v1/instances/{id}/restart` | Yes | Yes | Restart an instance: the old process is stopped and a new one is started under the **same InstanceID** (pipeline attribution preserved). Restart re-resolves the **current** launch configuration: `Model.Args` + `Model.Environment`, the runtime (executable/working directory/environment) of the current `Model.RuntimeID`, and, for pipeline-owned instances, the owning entry's `Args` override (all-or-nothing). If the model, runtime, or owning pipeline/entry can no longer be resolved (including legacy instances without entry attribution), the request fails with `409` `code=conflict`, `error=not_restartable` and the frozen launch snapshot is never relaunched. Returns `409` with `code=conflict`, `error=launch_in_flight` if the instance is in `pending` state. |
| `POST` | `/api/v1/instances/{id}/dismiss` | Yes | Yes | Dismiss an orphan instance (transitions `orphan` → `stale`). No process is touched. |
| `POST` | `/api/v1/instances/{id}/kill` | Yes | Yes | Terminate an orphan process (destructive, ADR 008). Strict identity re-verification before every signal; `orphan`-only. |

### POST /api/v1/instances/{id}/kill

Terminate an orphaned process. Requires auth + CSRF. Per [ADR 008](adr/008-recovery-kill-orphan.md), the process identity (executable path + start time) is **strictly re-verified immediately before every destructive syscall** — a missing or mismatched start-time anchor refuses the kill (no PID-only kill exists). Unix: `SIGTERM` → 5 s grace → re-verify → `SIGKILL` only if still alive and still identity-matching. Windows: immediate `TerminateProcess` (no graceful phase).

A successful transition to `stale` requires a **confirmed** process state; an unconfirmable termination never reports success (the `orphan` state is preserved and the attempt is retriable).

Response 200 (process terminated, exit confirmed):
```json
{ "status": "killed", "method": "sigterm" }
```
`method` is `sigterm`, `sigkill` (Unix) or `terminateprocess` (Windows). The instance transitions to `stale` with `recovery_reason=killed-by-user`, `exit_class=killed`.

Response 200 (PID already gone before any signal; nothing was killed):
```json
{ "status": "reconciled", "reason": "pid-gone" }
```
The instance transitions to `stale` with `recovery_reason=pid-gone` and unset `exit_class`.

Refusals (the `orphan` state is preserved with a persisted `last_error` diagnostic; audited `instance.kill` with `outcome=refused`):

| Status | `code` | `reason` | Meaning |
|--------|--------|----------|---------|
| `409` | `conflict` | `identity-unconfirmed` | Identity re-verification failed (path/start-time mismatch or start time unavailable). |
| `403` | `forbidden` | `insufficient-privilege` | The OS denied the terminate right (EPERM / access denied). |
| `500` | `internal_server_error` | `unconfirmed` | The termination outcome could not be confirmed (process still visible). |

Case G (no audit event): `409` if the instance is not in `orphan` state, `404` if not found, `400` if the ID is missing.

### Launch-in-flight guard

Two related rules use the `launch_in_flight` error:

1. **Pending-state protection:** When an instance is in `pending` state (slot acquisition or spawn not yet complete), lifecycle operations that would race the in-flight launch are refused.
2. **Single-in-flight-per-model contract:** A model may have at most one instance in an in-flight state (`pending`, `starting`, `running`, or `stopping`) at any time. Attempting to start a model that already has any in-flight instance is refused.

Both return:

```json
{ "error": "launch_in_flight", "code": "conflict" }
```

Affected endpoints:
- `POST /api/v1/instances/{id}/stop` — refuses to stop a pending instance (rule 1).
- `POST /api/v1/instances/{id}/restart` — refuses to restart a pending instance (rule 1).
- `POST /api/v1/models/{id}/start` — refuses if the model already has any instance in `pending|starting|running|stopping` state (rule 2).
- `POST /api/v1/models/{id}/stop` — refuses if a pending instance of the model exists (rule 1).
- `POST /api/v1/models/{id}/restart` — refuses if a pending instance of the model exists (rule 1).

The `pending` state is never terminal. A pending instance transitions to `starting` once the concurrency slot is acquired and the process spawn begins. Multi-instance/replica-per-model semantics are a future product decision and are not supported by the current contract.

### Lifecycle error mapping

Every single-target lifecycle endpoint (`instances/start`, `instances/{id}/stop|restart`,
`models/{id}/start|stop|restart`, `runtimes/{id}/action/stop|restart`) classifies the process layer's
failure by **sentinel identity** — never by message text — into one bounded `error` token. The tokens
are the client-visible vocabulary; raw Go error text never appears in a classified response.

| Status | `code` | `error` | Meaning |
|--------|--------|---------|---------|
| `503` | `service_unavailable` | `launch_aborted` | Shutdown won after the launch was admitted (RB-015b); nothing was spawned and no record persists. Retry after restart. |
| `503` | `service_unavailable` | `shutting_down` | Shutdown won before admission; the launch was rejected. |
| `500` | `internal_server_error` | `launch_persist_failed` | The process started but its record could not be persisted (ADR 016). |
| `500` | `internal_server_error` | `termination_unconfirmed` | Termination could not be confirmed; no success is reported. |
| `500` | `internal_server_error` | `rollback_failed` | A failed launch could not be rolled back completely. |
| `404` | `not_found` | `instance_not_found` | This process has no controller registered for the requested instance id. |
| `409` | `conflict` | `not_restartable` | The current launch configuration cannot be re-resolved (missing model/runtime/owning entry, or legacy attribution). |
| `409` | `conflict` | `orphan` | An orphan record holds this model's identity. |
| `409` | `conflict` | `launch_in_flight` | See the launch-in-flight guard above. |

Precedence inside a multi-cause error (a restart can join several causes): the `500` server class wins
over `503` and `409`; `503` wins over `409`; inside the `500` class `launch_persist_failed` is reported
first, because persisting the launch is the cause the operator acts on. An error of a class outside this
table keeps the endpoint's previous plain `500` with the error text.

`503` responses carry no `Retry-After`; the client learns completion from `GET /api/v1/instances`.

### Pipeline group lifecycle results

A pipeline start/stop/restart is best-effort and sequential, so a request that could not complete every
entry is **not** a bare success:

- The body keeps its full per-entry payload and gains the flat error keys: `code` plus `error`, where
  `error` is the aggregate class token — `shutting_down` when a shutdown class caused the aggregate,
  otherwise `pipeline_stop_incomplete` / `pipeline_restart_incomplete` for the phase that failed.
- Per-entry `error` (and, for stop, a new `failures:[{instance_id, reason}]` array) carries a frozen
  reason class: `launch-in-flight`, `instance-gone`, `persistence-failed`, `termination-unconfirmed`,
  `shutting-down`, `resolve-failed`, `start-failed`, `stop-failed`.
- HTTP status follows the aggregate classification: `500` for the persistence/termination/rollback
  class, `503` when a shutdown class is the cause, `409` for other failures, `200` when nothing failed.
- `start` promotes the request only for the shutdown class: an entry that simply failed to launch
  (`no-runtime`, `model-missing`, `start-failed`) or was skipped as `already-running` /
  `orphan-skipped` stays a `200` with the outcome in `results`, per ADR 010 acceptance. Every `stop`
  entry failure is promoted, because a pipeline that is not fully stopped is not the state the caller
  asked for; a `restart` is promoted by a stop-phase failure, while its start-phase outcomes follow the
  `start` rule above — which is why a promoted group `start` always carries
  `code=service_unavailable`, `error=shutting_down`.
- Nothing is rewound: instances that did stop (or start) stay reported as stopped/started, and
  `stopped_instance_ids` lists only genuinely stopped instances.
- `restart` runs its forward start phase even when the stop phase failed, so `start_results` always
  covers every entry while the status and flat keys describe the stop-phase failure.

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
instead. On `PUT`, the legacy `environment` field is rejected (400); environment
changes use `environment_patch` — an array of `{key, action, value?}` operations
(`action` ∈ `"set"|"delete"`). Omitting `environment_patch` preserves all existing
keys (KEEP). `set` on an existing key replaces the value; `set` on a new key adds
it; `set` with `value:""` stores an empty string; `delete` removes the key.
Runtime environment values remain available internally for process launch.

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| `GET` | `/api/v1/runtimes` | Yes | — | List runtimes. |
| `GET` | `/api/v1/runtimes/{id}` | Yes | — | Get runtime. |
| `POST` | `/api/v1/runtimes` | Yes | Yes | Create runtime. |
| `PUT` | `/api/v1/runtimes/{id}` | Yes | Yes | Update runtime. |
| `DELETE` | `/api/v1/runtimes/{id}` | Yes | Yes | Delete runtime. |
| `POST` | `/api/v1/runtimes/{id}/action/{action}` | Yes | Yes | Legacy action endpoint. `start` is **retired** — it returns `410 Gone` (callers must use `POST /api/v1/models/{id}/start`). `stop` and `restart` remain supported. |
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
| POST | /api/v1/models/{id}/start | Start an instance. Returns `409` with `code=conflict`, `error=launch_in_flight` if an in-flight instance of this model already exists; other lifecycle failures follow [Lifecycle error mapping](#lifecycle-error-mapping). |
| POST | /api/v1/models/{id}/stop | Stop active instances. Returns `409` with `code=conflict`, `error=launch_in_flight` if a pending instance of this model exists; other lifecycle failures follow [Lifecycle error mapping](#lifecycle-error-mapping). |
| POST | /api/v1/models/{id}/restart | Restart. Returns `409` with `code=conflict`, `error=launch_in_flight` if a pending instance of this model exists; other lifecycle failures follow [Lifecycle error mapping](#lifecycle-error-mapping). |
| GET | /api/v1/models/{id}/status | Get instance status |
| POST | /api/v1/models/{id}/activate | Enable autostart |
| POST | /api/v1/models/{id}/deactivate | Disable autostart |
| POST | /api/v1/models/{id}/resolve | Preview resolved command |

Model environment values are write-only: they are accepted on create (full map)
and update (`environment_patch` per-key ops) but never returned in API responses.
Only `environment_keys` (the list of variable names) is exposed. Omitting
`environment_patch` on update preserves existing keys.

**Resolve endpoint and variable resolution (ADR 014).** `POST /api/v1/models/{id}/resolve`
resolves `${VAR}` references in the model's launch-consumed fields before returning
the command. On success (200), `executable`, `args`, and `workingDirectory` are fully
resolved. On variable-resolution failure, the endpoint returns **400** with a bounded
diagnostic:

- `{"error":"model.args[1]: undefined variable MY_PATH"}` — valid reference, no value
- `{"error":"runtime.executable: invalid variable reference \"${1BAD}\""}` — malformed syntax

These are client/configuration errors (400), not server errors (500).

## Pipelines

A **Pipeline** (ADR 010) is an ordered group of existing Models with a group lifecycle.
Instances it launches carry `pipeline_id`; stop/restart act only on those owned instances.
Per-model Args are all-or-nothing: a non-empty `args` array replaces the referenced model's
args entirely at launch; an empty/absent `args` uses the model's own args.

| Method | Path | Auth | CSRF | Description |
|--------|------|------|------|-------------|
| GET | /api/v1/pipelines | Yes | — | List pipelines: `id`, `name`, `active`, `created_at`, `updated_at`, ordered `models:[{id, model_id, model_name, args?, auto_start}]` (each entry has its own `id`; a Model may repeat across entries). |
| GET | /api/v1/pipelines/{id} | Yes | — | Pipeline detail + per-entry live status: `models:[{id, model_id, model_name, args?, auto_start}]` and `statuses:[{model_id, entry_id?, index, state, instance_id?, pid?, started_at?, auto_start, has_args_override}]`, resolved by `(pipeline_id, pipeline_entry_id)` with a legacy per-model fallback (ADR 013 D4). |
| POST | /api/v1/pipelines | Yes | Yes | Create `{name, active?, models:[{model_id, args?, auto_start?}]}` → `201`. A `model_id` **may repeat** (each becomes a distinct entry with a server-generated `id`); the client sends no entry `id`s. `400` on empty name, empty model list, empty or unknown `model_id`. `active`/`auto_start` default `false`. Note: `auto_start` is a legacy field — it round-trips for backward compatibility but is **ignored** for launch decisions (an `active` pipeline launches all entries). |
| PUT | /api/v1/pipelines/{id} | Yes | Yes | Update. `name`/`args`/`active`/`auto_start` always allowed; structural change = a different **entry-`id` sequence** (add/remove/reorder) → `409` while the pipeline has active owned instances. New entries use `id: ""` (server-generated). `auto_start` round-trips but is not a launch gate. |
| DELETE | /api/v1/pipelines/{id} | Yes | Yes | Delete. `409` while it has active owned instances; `404` on unknown id. Terminal instances keep their historical `pipeline_id`. |
| POST | /api/v1/pipelines/{id}/start | Yes | Yes | Start all entries sequentially in order (best-effort, per-entry launch gate + model-owner rule — ADR 013 D3). `200 {pipeline_id, results:[{model_id, entry_id?, index, status, instance_id?, error?}]}`. `status` ∈ `started|already-running|orphan-skipped|no-runtime|model-missing|failed`. Non-`200` only for the shutdown class; see [Pipeline group lifecycle results](#pipeline-group-lifecycle-results). |
| POST | /api/v1/pipelines/{id}/stop | Yes | Yes | Stop the entry's own active instances in REVERSE order (best-effort; per-entry attribution with legacy per-model fallback — ADR 013 D4). `{pipeline_id, results:[{model_id, entry_id?, index, instance_id?, stopped_instance_ids?, failures?:[{instance_id, reason}], status, error?}]}`. `status` ∈ `stopped|failed`; `failed` never claims an instance in `stopped_instance_ids`. A partial stop returns the same body with `code`/`error` at a non-`200` status. |
| POST | /api/v1/pipelines/{id}/restart | Yes | Yes | Reverse stop then ALWAYS forward start. `{pipeline_id, stop_results:[…], start_results:[…]}`; a stop-phase failure is reported with the same additive `code`/`error` keys and the start phase still runs. |

Lifecycle requests emit one `pipeline.start` / `pipeline.stop` / `pipeline.restart` audit
event each (bounded counters only; an incomplete group request additionally records the bounded
`error` token of its aggregate class, never an instance id or raw error text). Pipeline CRUD
emits `pipeline.create|update|delete` (success-only; detail: `id`, `entries` count on create,
changed field *names* on update — ADR 007 §2a).

## Logs (aggregated)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/logs` | Yes | — | Query aggregated logs (filters: `stream`, `search`, `instance_id`, `page`, `page_size`). |
| `GET` | `/api/v1/logs/stream` | Yes | — | SSE log stream (multi-instance LogBroker). |

## Portable Configuration (ADR 014)

Export and import a self-contained portable configuration bundle (runtimes, models, pipelines).
Environment **values** are never exported; only `environment_keys` (key names) are included.
Model `args` are exported unchanged and may contain sensitive user-supplied values.

### GET /api/v1/export

| Auth | CSRF | Description |
|------|------|-------------|
| Yes | — | Export a portable configuration bundle as JSON. |

Query parameters (at most one; none = export all):

| Parameter | Effect |
|-----------|--------|
| (none) | Export all runtimes, models, and pipelines. |
| `runtime_id` | Export that runtime only. |
| `model_id` | Export that model and its runtime (closure). |
| `pipeline_id` | Export that pipeline, its models, and their runtimes (closure). |

Response `200`:

- `Content-Type: application/json`
- `Content-Disposition: attachment; filename="goal-portable-config.json"`
- Body: raw Bundle v1 JSON (`{"format":"goal-portable-config","version":1,"runtimes":[…],"models":[…],"pipelines":[…]}`).

Errors:

| Status | Meaning |
|--------|---------|
| `400` | More than one root selector, repeated selector, or empty selector value. |
| `404` | Root entity not found. |
| `500` | Internal error. |

### POST /api/v1/import

| Auth | CSRF | Description |
|------|------|-------------|
| Yes | Yes | Import a portable configuration bundle. Atomic all-or-nothing. |

Request body: raw Bundle v1 JSON (the exact file downloaded from export). No wrapper object.

Query parameters:

| Parameter | Default | Accepted values |
|-----------|---------|-----------------|
| `dry_run` | `false` | `true`, `false` |

`dry_run=true` performs full validation and collision detection but performs zero mutation.
A dry-run success does **not** guarantee a subsequent real import will succeed (TOCTOU).

Maximum body size: **10 MiB** (10,485,760 bytes). Exceeding the limit returns `413`.

Import behavior:
- Does **not** launch models or pipelines. `Active`/`AutoStart` flags are preserved and apply on next normal server startup.
- Does **not** restore `environment_keys` as Environment entries. They are advisory metadata only.
- Variable references (`${VAR}`) are validated for grammar only. Undefined variables are accepted; malformed references are rejected.
- Collision policy: **reject** (no overwrite, no merge, no remap).

Response `200`:

```json
{ "dry_run": false, "runtimes": 2, "models": 3, "pipelines": 1 }
```

Errors:

| Status | Meaning |
|--------|---------|
| `400` | Malformed JSON, wrong format, unsupported version, structural validation failure, malformed variable reference, empty body, malformed `dry_run` value. |
| `409` | Import collision (runtime ID, runtime name case-insensitive, model ID, or pipeline ID already exists). Response includes bounded conflict details. |
| `413` | Request body exceeds 10 MiB. |
| `500` | Persistence or internal failure. |

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

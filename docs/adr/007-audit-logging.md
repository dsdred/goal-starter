# ADR 007: Full Audit Logging — Structured Security Event Records

**Status:** Proposed
**Date:** 2026-08-24
**Related:** ROADMAP P0 "Full audit logging"; ADR 005 (Dismiss audit commitment); ADR 006 (credential storage)

## Context

GoAl ships security mechanisms (session auth, CSRF, bcrypt credentials, login rate limiting), but it has **no structured security audit trail**:

- `LoggingMiddleware` (`internal/webui/middleware/logging.go`) writes one stdout line per HTTP request (method, path, status, duration, IP, user agent). It is an operational log: it has no identity (who), no action semantics (what was done to which entity), no outcome detail, and it is not durable — it disappears with the console/pipe.
- The metrics endpoint (`GET /api/v1/metrics`) reports instance counts and server settings, not events.
- ADR 005 committed: orphan `Dismiss` "is … audited once P0 audit logging lands".

The ROADMAP P0 item: structured security audit events (login, session, settings, instance actions), persistence and retention, query API — replacing the metrics-only state.

Threat model (unchanged from SECURITY.md): single admin user; loopback default; network exposure only behind operator-configured proxy with `authEnabled=true`. The audit log's job is forensic answerability for that operator: *who did what, when, from where, and did it succeed* — after the fact, across restarts.

## Decision

**A durable, append-only, structured audit log of security-relevant actions, queryable through an authenticated admin API. Secrets are never recorded.**

### 1. Event model

```go
type AuditEvent struct {
    Timestamp time.Time         `json:"ts"`
    Event     string            `json:"event"` // dotted taxonomy, see §2
    User      string            `json:"user,omitempty"`
    SourceIP  string            `json:"src_ip"`
    Detail    map[string]string `json:"detail,omitempty"`
}
```

- `User` — authenticated user for authenticated actions; the *attempted* username for login outcomes; empty when unknown (e.g. rate-limited before credential parsing).
- `SourceIP` — TCP peer address only. **`X-Forwarded-For`/`X-Real-IP` are not trusted**, same principle as login rate limiting (spoofable headers would allow an attacker to attribute actions to fake addresses).
- `Detail` — small flat `map[string]string` of outcome metadata (e.g. `{"id": "ent_123", "state": "exited"}`, `{"password_changed": "true"}`). Values are identifiers and booleans only, never secrets.

### 2. First-scope event taxonomy

| Event | Emitted on | Detail |
|-------|-----------|--------|
| `login.success` | `POST /api/v1/auth/login` 200 | `user` |
| `login.failure` | `POST /api/v1/auth/login` 401 | `user` (attempted) |
| `login.rate_limited` | `POST /api/v1/auth/login` 429 | — |
| `session.logout` | `POST /api/v1/auth/logout` 200 | — |
| `settings.saved` | `PUT /api/v1/settings` 200 | changed field *names* only; `password_changed: "true"` when a password was set (value never) |
| `instance.start` | `POST /api/v1/instances/start` (success and failure) | `model_id`, `instance_id` (on success), `error` (on failure, sanitized) |
| `instance.stop` | `POST /api/v1/instances/{id}/stop` | `instance_id` |
| `instance.restart` | `POST /api/v1/instances/{id}/restart` | `instance_id` |
| `instance.dismiss` | `POST /api/v1/instances/{id}/dismiss` (ADR 005 commitment) | `instance_id` |
| `instance.cleanup` | `POST /api/v1/instances/cleanup` | `mode`, `deleted` count |

Out of first scope (future expansion, same logger): model CRUD, runtime CRUD/replace/cascade, session expiry cleanup, health-check failures. Rationale: the ROADMAP item names login, session, settings, instance actions; model/runtime events are a bounded extension once the contract is proven.

Events are emitted **only for the actions above** — not per-GET, not per-request (the request-level stdout log remains the operational record).

### 3. Storage

- **Format:** JSON Lines (one JSON object per line), file `<dataDir>/goal_audit.jsonl`. Chosen over a single JSON document: append is O(1) per event, a torn last line cannot corrupt earlier lines, and the file is readable with standard tools (`tail`, `grep`).
- **Write contract:** each event = one `O_APPEND` write of a complete line, then `fsync`. An audit event is reported recorded only after `fsync` returns (same "durable means durable" stance as `fsutil.WriteFileDurable`, adapted to append-only). File mode `0o600`.
- **Rotation:** when the file exceeds **10 MiB**, it is rotated to `goal_audit.jsonl.1`; at most **3 generations** are kept (oldest dropped). Rotation happens before the append that would cross the threshold. (Constants, not config, in first scope — see §7.)
- **Concurrency:** a single `AuditLogger` instance, mutex-protected; all writes go through it. No per-event goroutines (events are rare user actions; synchronous writes keep ordering deterministic: file order = occurrence order).

### 4. Query API

`GET /api/v1/admin/audit` — auth required, no CSRF (GET), alongside the existing admin endpoints:

- Parameters: `limit` (default 100, max 1000), `offset` (default 0), `event` (optional exact event-name filter).
- Response: events **newest first** (reversed file order), `{"events": [...], "total": N}`.
- The API reads the file on request (no in-memory cache of log contents — the file is the source of truth; the log is small by design).

No export/download endpoint in first scope (the file is a local file the operator can read directly; an HTTP export would need its own review).

### 5. Secret safety (hard rules)

The audit file must **never** contain:

- passwords or password hashes (plaintext, hash, or fragment) — `settings.saved` records `password_changed` only;
- session tokens, CSRF tokens, or cookie values;
- model/runtime `environment` values (write-only contract per SECURITY.md);
- request bodies or raw headers.

Enforced by construction: the logger accepts only the typed `AuditEvent` built by named call sites (no generic "log this request" path), and acceptance tests assert the audit file contains none of the injected secret substrings.

### 6. Failure semantics

**Audit persistence is fail-open for the business operation.** On every audit write failure:

1. The business/user operation is **not rolled back and not failed** solely because of the audit persistence failure — the action's outcome is unaffected and returned to the caller as if the audit layer did not exist.
2. The failure MUST produce an explicit **structured `slog.Error`** (e.g. `slog.Error("audit", "event", e.Event, "error", err)`) on the operational log.
3. The diagnostic message MUST NOT contain secret-bearing event data: passwords, password hashes, session tokens, CSRF tokens, environment values, request bodies, or any other value prohibited by the §5 secret-safety contract. Only the event name and the raw I/O error are eligible for the diagnostic.
4. A failed write does **not** disable the `AuditLogger` and does not move it into a permanently failed/disabled state — no latch, no error sticky bit; the logger keeps accepting events.
5. Each subsequent audit event **independently attempts a new write** (no suppression, no coalescing of failures).

Rationale: GoAl is a local single-user tool; an audit-log I/O fault (disk full) must not take the management plane offline. The trade-off (audit gap on I/O failure) is documented in SECURITY.md as a known limitation.

- **API read failure** (file missing/rotating) → `500` with `internal_server_error` code; missing file → empty list, `200` (fresh install, no events yet).

### 7. Configuration

No new config fields in first scope: file location (`<dataDir>/goal_audit.jsonl`), size cap (10 MiB), and generations (3) are fixed constants in the audit package. Configurable retention is a follow-up item, not part of this contract.

### 8. Implementation surface (for the implementation task)

- New `internal/webui/audit/` package: `AuditLogger` (`Log(AuditEvent) error`, `Query(limit, offset, event) ([]AuditEvent, int, error)`, `Close()`), rotation, tests.
- `cmd/goal/main.go` + `webui.NewApp`: create the logger with `dataDir`, stop it on shutdown.
- Call sites: `handlers/auth.go` (login outcomes incl. 429 — the 429 is emitted by the `rateLimited` wrapper in `routes.go`, so it has no parsed username), `handlers/system.go` (logout, settings.saved), `handlers/instances.go` (start/stop/restart/dismiss/cleanup).
- New route `GET /api/v1/admin/audit` in `routes.go` + handler in `system.go` (or a dedicated `audit.go` handler).
- Docs: API.md (new endpoint), SECURITY.md (audit trail section, replaces the "no audit" state), CONFIGURATION.md (file location + constants), USER_GUIDE EN/RU (how to read the audit log), ARCHITECTURE EN/RU (audit trail paragraph), ROADMAP (item → `[x]` at CI reconciliation).

## Consequences

- **Positive:** durable forensic answerability across restarts; ADR 005 Dismiss-audit commitment satisfied; secrets stay out of durable storage by construction; log is grep-friendly for the operator; bounded disk footprint (3 × 10 MiB max).
- **Negative / cost:** one `fsync` per audited action (negligible at user-action frequency); a new file to back up (operator must include `goal_audit.jsonl*` in `dataDir` backups — same as `goal_repo.json`); audit gaps are possible on I/O failure (fail-open by decision).
- **Unchanged:** stdout request logging (operational), metrics endpoint, instance log streaming (process output — different domain), all existing endpoints and their contracts.

## Acceptance contract (for the implementation task)

| # | Scenario | Expected |
|---|----------|----------|
| 1 | Successful login | `login.success` line with user + src_ip |
| 2 | Wrong password | `login.failure` with attempted user |
| 3 | Rate limit exhausted | `login.rate_limited` (no user field) |
| 4 | Logout | `session.logout` |
| 5 | `PUT /api/v1/settings` with new password | `settings.saved` with `password_changed: "true"`; audit file contains neither the old nor the new password |
| 6 | `PUT /api/v1/settings` changing only port | `settings.saved` with `web_port` name, no values |
| 7 | Instance start (success / failure) | `instance.start` with `instance_id` / sanitized `error` |
| 8 | Instance stop / restart / dismiss | corresponding events with `instance_id` |
| 9 | Cleanup with matches | `instance.cleanup` with `mode` + `deleted` |
| 10 | Secret scan | audit file contains none of: test password, session token, CSRF token, injected env value |
| 11 | API without session | `401` |
| 12 | API with session | newest-first, `limit`/`offset` honored, `event` filter exact-match |
| 13 | Fresh install (no file) | API returns `200` + empty list |
| 14 | Rotation | file >10 MiB rotates to `.1`; >3 generations → oldest dropped |
| 15 | Concurrent `Log` calls | all lines present, no torn lines (race test) |
| 16 | Audit write failure (read-only dir, POSIX) | action still succeeds; structured `slog.Error` emitted; diagnostic contains none of the secret substrings from scenario 10 |
| 17 | Recovery after a failed write | logger is not disabled (no permanent failed state); the next `Log` independently attempts a new write and, once the I/O fault is cleared, its event is persisted (line present in the file) |

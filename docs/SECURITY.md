# Security

This document describes the security model of GoAl 2.0 as implemented in production code.

## Authentication

### Mode: Session-based

| Setting | Value |
|---------|-------|
| Cookie name | `goal_session` |
| HttpOnly | `true` |
| SameSite | `Lax` |
| Secure | `false` (set to `true` when HTTPS middleware is added) |
| Password store | bcrypt hash (cost 12); persisted in `goal.json` as `adminPasswordHash`, loaded into an in-memory store at startup |
| Default credentials | None (store starts empty; `adminUser` + `adminPasswordHash` required in config when `authEnabled=true`) |

Login endpoint: `POST /api/v1/auth/login`
Logout endpoint: `POST /api/v1/auth/logout`
Session check: `GET /api/v1/auth/session`

### Credential validation

When `authEnabled=true`:

- `adminUser` and a valid `adminPasswordHash` are **required** in `goal.json` (startup rejects missing or malformed values).
- At startup, the stored hash is loaded into memory (no re-hashing). A legacy `adminPassword` plaintext is migrated to a hash on first startup (see Password storage).
- Login validates the submitted username against the stored username and the password against the bcrypt hash.
- Unknown username, wrong password, or empty credentials → `401`, no session created.
- Successful login creates a session and returns the **server-verified username** in the response.
- After logout, the session is destroyed; subsequent requests are unauthenticated.

When `authEnabled=false`:

- All routes are accessible without authentication.
- The login endpoint returns `200` immediately (no credentials parsed).
- The session endpoint reports `user: "public"`.
- A prominent warning is emitted if the bind address is non-loopback.

### Password storage

The `adminPasswordHash` in `goal.json` holds the **bcrypt hash** (cost 12, 60 chars) — the authoritative credential. Plaintext is never persisted: the settings endpoint hashes before saving, and `Config.Save()` retains the hash so authentication continues to work after restart. A legacy `adminPassword` plaintext field is migrated on first startup (`config.MigrateCredentials`): hashed, cleared, and the file re-saved atomically; if the save fails, startup aborts (fail-closed). New configs never carry the `adminPassword` key. The file uses mode `0600` on POSIX; on Windows, restrict its directory with an ACL.

### Session store

Sessions are stored in memory with automatic cleanup of expired sessions. There is no persistence layer for sessions.

## CSRF protection

### Double-submit cookie pattern

| Cookie | Name | SameSite |
|--------|------|----------|
| CSRF token | `goal_csrf_token` | `Strict` |
| Token in header | `X-CSRF-Token` | — |

The middleware validates that the cookie and header values match for unsafe methods (POST, PUT, DELETE). GET, HEAD, and OPTIONS are not CSRF-protected.

### Scope

CSRF protection applies to all routes when `authEnabled=true`. When `authEnabled=false`, CSRF middleware is not applied.

## Authorization model

GoAl has a single admin user. There are no roles or permissions — if the user is authenticated, they have full access to all endpoints.

## Bind behavior

| `listenAddress` | `authEnabled` | Effect |
|-----------------|---------------|--------|
| `127.0.0.1` (default) | `false` | Local-only access, no auth required. |
| `0.0.0.0` | `false` | **Allowed with prominent WARN** — all endpoints accessible without credentials. |
| `0.0.0.0` | `true` | Public access, session required. |
| Custom IP | `true` | Network access, session required. |

## Secrets management

| Secret | Location | Cleared on save |
|--------|----------|-----------------|
| `adminPasswordHash` | `goal.json` → `AdminPasswordHash` field (bcrypt hash; plaintext never persisted); previous generation also present in `goal.json.bak` | No; protect both files with POSIX permissions or a Windows ACL |
| Session tokens | In-memory store | Yes (expiry-based) |
| CSRF tokens | Cookie + header | Rotated on login |

Runtime and model process environment values can contain sensitive
configuration. They are stored in the local `goal_repo.json` without encryption
and must be protected through filesystem permissions. They are write-only over
the HTTP API: responses expose sorted environment variable names, never values.
GoAl is not a secret vault.

For runtime and model updates, omitting `environment` preserves stored values,
an explicit empty object clears them, and an explicit map replaces them. The
Admin credentials remain configured separately through `goal.json` (`adminPasswordHash`) or the Web UI.

Model environment values are treated as write-only API data. They remain in
the authoritative local repository so the runtime can receive them, but model
responses and browser previews expose only environment variable names. An
unrelated model update preserves existing values when `environment` is
omitted; callers must send an explicit map to replace or clear them.

Runtime environment values follow the same write-only response contract and
remain available internally for process launch.

## Network security

| Feature | Status |
|---------|--------|
| Default bind loopback | `127.0.0.1` |
| External bind warning | `authEnabled=false` + non-loopback → prominent WARN (not blocked) |
| Request body size limit | `http.MaxBytesReader` |
| Login rate limiting | **Implemented**: per-client-address fixed window on `POST /api/v1/auth/login` (100 req/min → HTTP 429 `rate_limited`) |
| Runtime path validation | Executable and working directory validated against allowed roots |

## Audit trail

GoAl keeps a durable, append-only, structured security audit log (ADR 007): `<dataDir>/goal_audit.jsonl`, one JSON line per event, each line fsynced on write. It answers *who did what, when, from where, and did it succeed* — across restarts.

- **Events (first scope):** `login.success`, `login.failure` (attempted user), `login.rate_limited`, `session.logout`, `settings.saved` (changed field names only; `password_changed` flag), `instance.start` (success and failure), `instance.stop`, `instance.restart`, `instance.dismiss`, `instance.kill` (every kill attempt that passes the state precondition; detail: `instance_id`, bounded `outcome` `terminated|reconciled|refused`, bounded `reason`), `instance.cleanup`, `config.reload` (ADR 009; `status` `reloaded|rejected` + bounded field-name lists `applied` / `restart_required`; rejected events carry `error=invalid_config`, never file content).
- **Identity:** `user` is the authenticated user, or the attempted username for login outcomes; `src_ip` is the TCP peer address only (`X-Forwarded-For`/`X-Real-IP` are not trusted, same principle as login rate limiting).
- **Secret safety (hard rules):** the file never contains passwords or hashes, session/CSRF tokens, model/runtime environment values, request bodies, or raw headers. The logger accepts only typed events built by named call sites (there is no generic "log this request" path).
- **Retention:** rotation to `goal_audit.jsonl.1` at 10 MiB, at most 3 generations (3 × 10 MiB max). Constants, not config, in the first scope.
- **Query:** `GET /api/v1/admin/audit` (auth required; see API.md).
- **Fail-open:** an audit write failure never fails or rolls back the business operation; it produces a structured `slog.Error` (event name + raw I/O error only, no event payload) and the logger keeps accepting events. **Known limitation: audit gaps are possible on I/O failure (e.g. disk full).**
- **Backup:** include `goal_audit.jsonl*` in `dataDir` backups, the same as `goal_repo.json`.

## Orphan kill (destructive termination)

`POST /api/v1/instances/{id}/kill` (auth + CSRF, destructive-confirmed in the UI) terminates an `orphan` process — a process that may still be running outside GoAl. It is an explicit user action only; no code path kills automatically (ADR 008).

- **Identity re-verification at kill time.** Termination is PID-addressed and PIDs are reused, so the kill strictly re-verifies the recorded identity (executable path **and** start time) immediately before **every** destructive syscall (the first signal and any escalation). PID-only kill does not exist; a missing or mismatched start-time anchor **refuses** the kill (conservative). Dismiss remains the always-available safe path.
- **No false success.** A transition to terminal `stale` requires a confirmable process state (liveness probe). If the termination outcome cannot be confirmed, the `orphan` state is preserved (retriable) and a 500 `unconfirmed` is returned — the process is never declared killed without confirmation.
- **Privilege denial is explicit.** EPERM / access-denied → 403 `insufficient-privilege`; the orphan is preserved and the attempt is audited.
- **Accepted residual risk (TOCTOU).** The kernel can still recycle a PID in the irreducible gap between the final re-verification and the signal syscall. Re-verification minimizes the window; a mis-kill would require PID **and** executable path **and** start time to all collide on an unrelated process in that gap. This residual is documented and accepted (ADR 008); the contract makes no sub-millisecond guarantee.
- **Audit.** Every kill attempt that passes the state precondition emits one `instance.kill` event with bounded, secret-safe detail (outcome + reason vocabulary). Precondition failures (not orphan / not found) emit no event.

## Hot-reload (configuration reload)

`POST /api/v1/admin/reload` (auth + CSRF, ADR 009) re-reads and validates `goal.json` and applies hot fields (`logLevel`). Security properties:

- **No unauthenticated surface.** The endpoint is auth + CSRF protected like the rest of the admin API.
- **Reload never writes the file and never applies credential material.** The only live credential path remains the audited `PUT /api/v1/settings` (ADR 006 contract intact); a hand-edited `adminPasswordHash` takes effect only at the next restart.
- **All-or-nothing.** A rejected reload (unreadable or invalid file) changes nothing: live values are untouched and the file on disk is byte-identical, so a corrupt or maliciously edited file cannot partially reconfigure a running instance.
- **Audit.** Every attempt emits one `config.reload` event with field *names* only (never values, never credential material); audit-write failure is fail-open (ADR 007).

## Recommended deployment

For network access:

```json
{
  "listenAddress": "0.0.0.0",
  "webPort": 8088,
  "authEnabled": true,
  "adminUser": "admin",
  "adminPasswordHash": "$2a$12$..."
}
```

The password is normally set once via **Web UI → Settings → Server** (it is stored as `adminPasswordHash`); a pre-generated bcrypt hash may also be written directly.

For maximum security:

1. Set `authEnabled: true`
2. Set a strong admin password (stored as `adminPasswordHash`)
3. Bind to a non-loopback address
4. Run behind a reverse proxy with TLS
5. Use `deploy/systemd/goal.service` (Linux) or `goal --service install` (Windows, in-binary SCM registration per ADR 011 — LocalSystem account, absolute paths required) for managed process lifecycle

## Windows code signing

Windows release binaries are currently **not Authenticode-signed**. No signing certificate is configured in the release pipeline.

### What this means for users

- The publisher may appear as "Unknown Publisher" in Windows security dialogs.
- Windows SmartScreen or Microsoft Defender may show a warning when first running a downloaded release.
- This is expected for the current distribution method, not a GoAl bug.

### User verification

Users should verify the SHA-256 hash of downloaded binaries against the `checksums.txt` published in the official [GitHub Release](https://github.com/dsdred/goal-starter/releases):

```powershell
Get-FileHash .\goal-windows-amd64.exe -Algorithm SHA256
```

### Future

Authenticode code signing is a possible future improvement. It is not currently planned or implemented.

## Security notes

- **Public mode warning:** If `authEnabled=false` and GoAl is accessible from the network, all API endpoints are accessible without authentication. A prominent WARN is emitted at startup.
- **No HTTPS in binary:** TLS is not terminated inside GoAl. Use a reverse proxy for HTTPS.
- **No token-based auth:** Only session cookies are supported. No API keys or bearer tokens.
- **No multi-user:** Single admin user only. No roles or permissions.
- **Login rate limiting:** Enforced on `POST /api/v1/auth/login` — at most 100 requests per minute per client address (TCP peer), then HTTP 429 with code `rate_limited`. `X-Forwarded-For`/`X-Real-IP` are intentionally not trusted (client-supplied headers would allow bypass). Behind a reverse proxy all clients share the proxy's bucket; for exposed deployments add proxy-level limiting as well. The limit bounds request rate; a failure-count lockout (e.g. 5 failures / 5 min) is not implemented.
- **Password stored as hash:** `goal.json` holds the bcrypt hash (`adminPasswordHash`); plaintext is never persisted (a legacy plaintext `adminPassword` auto-migrates on first startup). Protect the file with filesystem permissions.

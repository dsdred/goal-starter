# Security

This document describes the security model of GoAl 2.0 as implemented in production code.

## Authentication

### Mode: Session-based

| Setting | Value |
|---------|-------|
| Cookie name | `goal_session` |
| HttpOnly | `true` |
| SameSite | `Lax` |
| Secure | derived per response: `true` if and only if the connection that produced that response was TLS (`r.TLS != nil`), `false` over plain HTTP. Never from the `tls` config block, the port, the bind address, or a forwarded header ([ADR 019 §D18/§D20](adr/019-native-https-secure-origin.md)) |
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
- A prominent warning is emitted if the bind address is non-loopback, naming every listener the configuration opens (HTTP, and HTTPS when `tls.enabled`).

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

`goal_csrf_token` carries the same connection-derived `Secure` flag as the session cookie, from the same rule: it is `Secure` on a response produced over TLS and non-`Secure` on a plain-HTTP one. Everything else about it is unchanged — `HttpOnly: false` (the double-submit copy must stay readable by same-origin JavaScript), `SameSite: Strict`, `Path: /`, host-only. Logout clears the session cookie only; the CSRF cookie stays until its `MaxAge` expires or a new login replaces it.

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

The table describes the Web UI on **every listener the configuration opens**. With `tls.enabled` true the same rows apply unchanged to the HTTPS port, because HTTPS reuses `listenAddress` ([HTTPS and secure origin](#https-and-secure-origin-adr-019)); `authEnabled` is global, never per listener. With `authEnabled=false` on a non-loopback address, the startup warning names each listener that is actually exposed (HTTP, and HTTPS when TLS is enabled) and states that HTTPS encrypts the transport while authenticating nothing. Startup is never blocked by this condition, and no configuration is mutated.

## HTTPS and secure origin (ADR 019)

GoAl terminates TLS in its own process when the optional `tls` block is present and enabled in `goal.json` ([CONFIGURATION.md](CONFIGURATION.md#tls-configuration-adr-019)); no reverse proxy is required. An absent or disabled block leaves the shipped behavior byte-identical: one plain HTTP listener and non-`Secure` cookies.

**Listeners.** HTTP (`listenAddress` + `webPort`) is always present. Enabling TLS adds a second, independent HTTPS listener on the same `listenAddress` with `tls.port`. GoAl never redirects HTTP → HTTPS and never disables HTTP when TLS is on, so both origins are live at the same time. Every intended listener is bound before any request is served: if a bind fails or the certificate pair cannot be loaded at that point, startup fails and no port is left listening. The minimum protocol version is TLS 1.2 — a constant of this contract, not configuration — with TLS 1.3 served wherever the client supports it and Go's default cipher suites.

**What HTTPS provides and what it does not.**

| Concern | With `tls` enabled |
|---------|--------------------|
| Transport encryption browser → GoAl | Yes |
| Browser trust (no certificate warning) | **Not provided by GoAl** — only if the operator-supplied chain validates in that browser |
| Secure-context browser APIs (e.g. the native Save As used by portable export) | Yes, as a consequence of an `https:` origin the browser treats as trustworthy |
| Authentication | **No** — `authEnabled` is an independent switch; HTTPS authenticates nothing |
| Authorization, CSRF, session model | Unchanged |

**Certificate duties (operator-owned).** GoAl loads and serves exactly the PEM blocks `tls.certFile` carries: no chain assembly, no issuer lookup, no fetching, no ACME, no automatic trust-store installation. The certificate's SAN must cover every name or address clients actually type — `DNS:aids` for `https://aids:8443`, and separately `IP:192.168.3.245` for address access. GoAl performs no hostname or SAN validation of its own certificate and cannot know the names clients will use. Renewal is replace-files-then-restart; startup validation confirms the new chain and the HTTPS startup line logs its new expiry and SAN list. Expiry monitoring is an operator duty; GoAl's contribution is failing loudly at startup rather than serving a dead or expired certificate.

**One cookie jar: the session ratchet under coexistence.** HTTP and HTTPS on the same host name share **one** browser cookie jar — a cookie's key is (name, domain, path); neither the scheme nor the port participates. Because `Secure` is derived per response, enabling TLS produces a one-way ratchet that operators must understand:

| Sequence | Observed consequence |
|----------|----------------------|
| Login over `http://host:8088`, then open `https://host:8443` | The session cookie **is** sent to HTTPS: the HTTP session carries up, no re-login |
| Login over `https://host:8443`, then open `http://host:8088` | The `Secure` cookie is **not** sent to HTTP: the user appears logged out there |
| HTTP login while that `Secure` cookie exists | The browser **refuses the write**; HTTP login is a no-op until the cookie expires or is cleared over HTTPS (downgrade/session-fixation protection, not a defect) |
| HTTP → HTTPS → HTTP **without** an HTTPS re-login | If HTTPS never re-issued the cookie (for example the visit was not authenticated), the same non-`Secure` cookie travels back down and the HTTP session **continues** — the ratchet is set by the emit, not by visiting the port |
| HTTP page after an HTTPS `Secure` replacement | `goal_csrf_token` is absent from that page too, including from `document.cookie`, so a request issued from the HTTP page cannot produce the double-submit header |
| Logout over HTTPS | Clears the entry on the scheme that set it; plain HTTP can neither read nor clear a `Secure` cookie |

The supported rule is therefore **work in one scheme per browser**, not one session everywhere. Different host strings are different jars: `https://aids:8443` and `https://192.168.3.245:8443` never share a session. Cookie names, `Path: "/"`, host-only scope (no `Domain`), `HttpOnly` and `SameSite` values are unchanged, and no `__Host-`/`__Secure-` prefix is used — a prefix would force `Secure` and break plain-HTTP compatibility.

**Key material.** The private key lives only at `tls.keyFile` on the host filesystem. It is never in `goal.json`, never in `goal.json.bak`, never in a Portable Configuration bundle or import plan, and never in a log line: startup validation and serve failures name the field, the reason and the file **path**, never file contents. Restrict the key to the account that runs GoAl — POSIX mode (a group- or other-readable key is a startup warning, and startup proceeds; modes are not reliable on Windows) or a Windows ACL on its directory.

**Rollback hazard.** `config.Save` rewrites `goal.json` from the parsed struct, so a binary without the `tls` field drops the block on the next settings save. Enabling TLS therefore requires a binary that parses it, and downgrading past that binary loses the block — keep the binary and the configuration contract in step.

**Reverse proxy remains an alternative, not the primary path.** A TLS-terminating proxy is supported, and GoAl adds **no trusted-proxy semantics**: it does not honor `X-Forwarded-Proto`, `Forwarded` or `X-Real-IP` for any security decision. Consequences: behind such a proxy GoAl's own connection is plaintext, so the session cookie stays non-`Secure`; audit `src_ip` and login rate limiting keep using the TCP peer address, which means all proxied clients share one rate-limit bucket (add proxy-level limiting as well).

## Secrets management

| Secret | Location | Cleared on save |
|--------|----------|-----------------|
| `adminPasswordHash` | `goal.json` → `AdminPasswordHash` field (bcrypt hash; plaintext never persisted); previous generation also present in `goal.json.bak` | No; protect both files with POSIX permissions or a Windows ACL |
| Session tokens | In-memory store | Yes (expiry-based) |
| CSRF tokens | Cookie + header | Rotated on login |
| TLS private key | The file named by `tls.keyFile` — only that **path** is stored in `goal.json`; the key material is never in `goal.json`, `goal.json.bak`, a portable bundle or any log line | No; restrict it to the account running GoAl (POSIX mode or a Windows ACL) |

Runtime and model process environment values can contain sensitive
configuration. They are stored in the local `goal_repo.json` without encryption
and must be protected through filesystem permissions. They are write-only over
the HTTP API: responses expose sorted environment variable names, never values.
GoAl is not a secret vault.

For runtime and model updates, environment changes use `environment_patch`
(per-key `set`/`delete` operations); omitting it preserves all stored values.
The legacy whole-map `environment` field is rejected on Runtime updates (400)
and ignored on Model updates. The Admin credentials remain configured
separately through `goal.json` (`adminPasswordHash`) or the Web UI.

Model and Runtime environment values are treated as write-only API data. They
remain in the authoritative local repository so the runtime can receive them,
but responses expose only environment variable names (`environment_keys`).
An unrelated update preserves existing values when `environment_patch` is
omitted.

## Network security

| Feature | Status |
|---------|--------|
| Default bind loopback | `127.0.0.1` |
| External bind warning | `authEnabled=false` + non-loopback → prominent WARN naming every listener the config opens (HTTP, and HTTPS when `tls.enabled`) — not blocked |
| Transport encryption | Plain HTTP by default; native HTTPS on `listenAddress` + `tls.port` when the `tls` block is enabled, minimum TLS 1.2 (constant), Go default cipher suites |
| Secure cookie flag | `Secure` on every cookie emitted over a TLS connection; forwarded headers never influence it |
| Request body size limit | `http.MaxBytesReader` |
| Login rate limiting | **Implemented**: per-client-address fixed window on `POST /api/v1/auth/login` (100 req/min → HTTP 429 `rate_limited`) |
| Runtime path validation | Executable and working directory validated against allowed roots |

## Audit trail

GoAl keeps a durable, append-only, structured security audit log (ADR 007): `<dataDir>/goal_audit.jsonl`, one JSON line per event, each line fsynced on write. It answers *who did what, when, from where, and did it succeed* — across restarts.

- **Events (first scope):** `login.success`, `login.failure` (attempted user), `login.rate_limited`, `session.logout`, `settings.saved` (changed field names only; `password_changed` flag), `instance.start` (success and failure), `instance.stop`, `instance.restart`, `instance.dismiss`, `instance.kill` (every kill attempt that passes the state precondition; detail: `instance_id`, bounded `outcome` `terminated|reconciled|refused`, bounded `reason`), `instance.cleanup`, `config.reload` (ADR 009; `status` `reloaded|rejected` + bounded field-name lists `applied` / `restart_required`; rejected events carry `error=invalid_config`, never file content).
- **Events (entity-CRUD extension, ADR 007 §2a):** `model.create|update|delete|activate|deactivate`, `runtime.create|update|delete|replace|cascade_delete`, `pipeline.create|update|delete` — success-only, after the durable mutation. Update events record changed field *names* only (sentinel `"changed"`, never values); `runtime.replace`/`runtime.cascade_delete` carry bounded counts (`models_moved`/`models_deleted`) instead of per-model events. Model-page/runtime-page process actions are covered solely by the `instance.*` trail (no duplicate events).
- **Identity:** `user` is the authenticated user, or the attempted username for login outcomes; `src_ip` is the TCP peer address only (`X-Forwarded-For`/`X-Real-IP` are not trusted, same principle as login rate limiting).
- **Secret safety (hard rules):** the file never contains passwords or hashes, session/CSRF tokens, model/runtime environment values, entity names, launch args, executable/working-directory paths, request bodies, or raw headers. The logger accepts only typed events built by named call sites (there is no generic "log this request" path); entity events carry identifiers, bounded counts/booleans, and changed field *names* only.
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

For remote/LAN use where the browser needs a **secure origin** (native Save As, and generally any `[SecureContext]` API), serve HTTPS from the binary instead of adding a proxy. Paths must be absolute, and `tls.port` must differ from `webPort`:

```json
{
  "listenAddress": "0.0.0.0",
  "webPort": 8088,
  "authEnabled": true,
  "adminUser": "admin",
  "adminPasswordHash": "$2a$12$...",
  "tls": {
    "enabled": true,
    "port": 8443,
    "certFile": "C:\\certs\\aids.crt",
    "keyFile": "C:\\certs\\aids.key"
  }
}
```

Then open `https://<that host>:8443`. HTTP stays reachable on `8088`; the browser's secure-context decision comes from the URL you type, not from this file. The certificate's SAN must cover the name you use, and its chain must be trusted by the client browser if you want no warning — see [HTTPS and secure origin](#https-and-secure-origin-adr-019).

For maximum security:

1. Set `authEnabled: true`
2. Set a strong admin password (stored as `adminPasswordHash`)
3. Bind to a non-loopback address
4. Serve HTTPS from the binary (`tls` block, absolute cert/key paths, key readable only by the GoAl account) — or keep plain HTTP behind a TLS-terminating reverse proxy where that is preferred, accepting that GoAl then sees a plaintext connection and issues non-`Secure` cookies
5. Use `deploy/systemd/goal.service` (Linux) or `goal --service install` (Windows, in-binary SCM registration per ADR 011 — LocalSystem account; install pre-flight requires every path the service depends on to be absolute or deterministically anchored to an absolute working directory, so no runtime path resolves against the SCM working directory) for managed process lifecycle

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

- **Public mode warning:** If `authEnabled=false` and GoAl is accessible from the network, all API endpoints are accessible without authentication. A prominent WARN is emitted at startup, naming each exposed listener (HTTP, and HTTPS when `tls.enabled`) and stating that HTTPS encrypts the transport and authenticates nothing. Startup is not blocked.
- **HTTPS:** TLS is terminated inside the GoAl binary when the optional `tls` block is enabled — a second listener on the same `listenAddress` at `tls.port`, minimum TLS 1.2, no reverse proxy required ([ADR 019](adr/019-native-https-secure-origin.md)). Plain HTTP remains available and is never disabled or redirected automatically. A TLS-terminating reverse proxy stays a supported alternative; behind one, GoAl's own connection is plaintext and cookies stay non-`Secure`.
- **No token-based auth:** Only session cookies are supported. No API keys or bearer tokens.
- **No multi-user:** Single admin user only. No roles or permissions.
- **Login rate limiting:** Enforced on `POST /api/v1/auth/login` — at most 100 requests per minute per client address (TCP peer), then HTTP 429 with code `rate_limited`. `X-Forwarded-For`/`X-Real-IP` are intentionally not trusted (client-supplied headers would allow bypass). Behind a reverse proxy all clients share the proxy's bucket; for exposed deployments add proxy-level limiting as well. The limit bounds request rate; a failure-count lockout (e.g. 5 failures / 5 min) is not implemented.
- **Password stored as hash:** `goal.json` holds the bcrypt hash (`adminPasswordHash`); plaintext is never persisted (a legacy plaintext `adminPassword` auto-migrates on first startup). Protect the file with filesystem permissions.

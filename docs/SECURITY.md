# Security

This document describes the security model of GoAl v1.0.0 as implemented in production code.

## Authentication

### Mode: Session-based

| Setting | Value |
|---------|-------|
| Cookie name | `session` |
| HttpOnly | `true` |
| SameSite | `Lax` |
| Secure | `false` (set to `true` when HTTPS middleware is added) |
| Password store | bcrypt hash in session store |
| Default credentials | `admin` / (empty, legacy fallback) |

Login endpoint: `POST /api/v1/auth/login`
Logout endpoint: `POST /api/v1/auth/logout`

### Password storage

At startup, the configured password is hashed with bcrypt in memory. Authentication-enabled startup rejects an empty password. `Config.Save()` retains the password so authentication continues to work after restart. The file uses mode `0600` on POSIX; on Windows, restrict its directory with an ACL.

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
| `0.0.0.0` | `false` | **Rejected** — public bind requires auth. |
| `0.0.0.0` | `true` | Public access, session required. |
| Custom IP | `true` | Network access, session required. |

## Secrets management

| Secret | Location | Cleared on save |
|--------|----------|-----------------|
| `adminPassword` | `goal.json` → `AdminPassword` field | No; protect it with POSIX permissions or a Windows ACL |
| Session tokens | In-memory store | Yes (expiry-based) |
| CSRF tokens | Cookie + header | Rotated on login |

Runtime and model process environment values can contain sensitive
configuration. They are stored in the local `goal_repo.json` without encryption
and must be protected through filesystem permissions. They are write-only over
the HTTP API: responses expose sorted environment variable names, never values.
GoAl is not a secret vault.

For runtime and model updates, omitting `environment` preserves stored values,
an explicit empty object clears them, and an explicit map replaces them. The
`AdminPassword` remains configured separately through `goal.json` or the Web UI.

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
| External bind rejection | `authEnabled=false` + non-loopback → error |
| Request body size limit | `http.MaxBytesReader` |
| Rate limiting | Placeholder (wired but no-op) |
| Login rate limit | Placeholder (5 attempts / 5 minutes, not enforced) |
| Runtime path validation | Executable and working directory validated against allowed roots |

## Recommended deployment

For network access:

```json
{
  "listenAddress": "0.0.0.0",
  "webPort": 9090,
  "authEnabled": true,
  "adminPassword": "secure_hash_or_plaintext"
}
```

For maximum security:

1. Set `authEnabled: true`
2. Set a strong `adminPassword`
3. Bind to a non-loopback address
4. Run behind a reverse proxy with TLS
5. Use `deploy/systemd/goal.service` (Linux) or `deploy/windows/install-service.ps1` (Windows) for managed process lifecycle

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

- **Public mode warning:** If `authEnabled=false` and GoAl is accessible from the network, all API endpoints (except `/health` and `/version`) are accessible without authentication.
- **No HTTPS in binary:** TLS is not terminated inside GoAl. Use a reverse proxy for HTTPS.
- **No token-based auth:** Only session cookies are supported. No API keys or bearer tokens.
- **No multi-user:** Single admin user only. No roles or permissions.

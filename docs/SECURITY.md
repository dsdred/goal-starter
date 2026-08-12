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

Passwords are hashed with bcrypt when saved. On login, if the stored hash is empty, plaintext comparison is used (legacy fallback). The `adminPassword` field in `goal.json` is cleared after the first save.

### Session store

Sessions are stored in memory with automatic cleanup of expired sessions. There is no persistence layer for sessions.

## CSRF protection

### Double-submit cookie pattern

| Cookie | Name | SameSite |
|--------|------|----------|
| CSRF token | `csrf` | `Strict` |
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
| `adminPassword` | `goal.json` → `AdminPassword` field | Yes |
| Session tokens | In-memory store | Yes (expiry-based) |
| CSRF tokens | Cookie + header | Rotated on login |

Environment variables are not used for secret injection. The `AdminPassword` can be set via `goal.json` or via the Web UI.

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

## Security notes

- **Public mode warning:** If `authEnabled=false` and GoAl is accessible from the network, all API endpoints (except `/health` and `/version`) are accessible without authentication.
- **No HTTPS in binary:** TLS is not terminated inside GoAl. Use a reverse proxy for HTTPS.
- **No token-based auth:** Only session cookies are supported. No API keys or bearer tokens.
- **No multi-user:** Single admin user only. No roles or permissions.

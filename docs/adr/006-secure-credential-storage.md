# ADR 006: Secure Credential Storage — Password Hash Persistence

**Status:** Accepted
**Date:** 2026-08-23
**Agreed:** 2026-08-23 (owner contract agreement)
**Implemented:** 2026-08-23 (`9d2d0fb`; CI run 32657837425 PASS; all 18 acceptance scenarios covered by `internal/config/migrate_credentials_test.go` and `internal/webui/handlers/credential_integration_test.go`)
**Related:** ROADMAP P0 "Secure credential storage"

## Context

GoAl currently stores the admin password as **plaintext** in the `adminPassword` field of the config JSON (`goal.json` on disk, mode `0o600`). At startup, `NewApp` (`internal/webui/server.go:71-76`) reads the plaintext and calls `PasswordStore.SetPassword()`, which hashes it with bcrypt (cost 12) into an in-memory map. Login verification (`internal/webui/handlers/auth.go:54`) performs a bcrypt comparison against the in-memory hash.

The security weakness: the **durable** store on disk is plaintext. A config file backup, log capture, or disk image exposes the password directly. The bcrypt hash exists only in RAM and is re-derived from plaintext on every restart.

When the user changes the password via `PUT /api/v1/settings` (`internal/webui/handlers/system.go:215-225`), the new plaintext is written directly back to the config JSON.

bcrypt is already a dependency (`golang.org/x/crypto` v0.54.0, `go.mod`). The `PasswordStore` and `HashPassword`/`CheckPasswordHash` helpers are already in `internal/webui/security/`.

## Decision

**The config JSON stores only a bcrypt hash in a dedicated `adminPasswordHash` field. The legacy `adminPassword` plaintext field is emptied after migration and is never written in new configs.**

### Hash algorithm and work factor

- **Algorithm:** bcrypt (`golang.org/x/crypto/bcrypt`, existing dependency)
- **Cost:** 12 (fixed; not a user-configurable setting in the first scope)
- **Rationale:** ~250ms per verification on commodity hardware; appropriate for a local single-user tool. No Argon2id — would require a new dependency and parameter tuning without proportional security gain for this threat model.

### Persistent schema (target state)

```json
{
  "version": 2,
  "adminUser": "admin",
  "adminPasswordHash": "$2a$12$XQv6S1zG...",
  "authEnabled": true
}
```

| Field | JSON key | Type | Meaning |
|-------|----------|------|---------|
| `AdminUser` | `adminUser` | string | Username (unchanged) |
| `AdminPasswordHash` | `adminPasswordHash` | string | Bcrypt hash (60 chars) or empty. **Authoritative credential.** |
| `AdminPassword` | `adminPassword` | string | **Legacy only.** Plaintext pre-migration. Must be empty in all post-migration configs. |
| `AuthEnabled` | `authEnabled` | bool | Unchanged |

- New configs (first-time setup, `Default()`): `adminPasswordHash` empty, `adminPassword` absent.
- After any password set/rotation: `adminPasswordHash` contains the hash; `adminPassword` is empty.
- The `adminPassword` field is **omitted from JSON output** (`omitempty`) once empty, so post-migration configs do not even carry the key.

### Bcrypt detection helper

A value is a valid bcrypt hash if and only if:
- It starts with `$2a$`, `$2b$`, or `$2y$`
- Followed by exactly 2 digits (cost)
- Followed by `$`
- Total string length is exactly 60

This is the sole detection mechanism. No config version bump is required.

### Bcrypt 72-byte input limit

bcrypt operates on at most the first 72 bytes of input; bytes beyond 72 are silently truncated. Two distinct passwords that share the first 72 bytes produce identical hashes.

**Contract:** Password length is validated **at credential creation and change** (settings endpoint, initial setup) with a maximum of **72 bytes** (UTF-8 encoded). Exceeding the limit returns a controlled validation error (HTTP 400) with a clear message. The auth layer (bcrypt compare) never receives an over-length password.

UI validation (RU/EN) must mirror this limit at implementation time.

### Explicit startup migration flow

`config.Load()` is **read-only**: it parses the file and returns the `Config` struct. It does **not** perform file writes.

Migration is a separate, explicit step in the **startup sequence** (`main.go`, between `config.Load` and `NewApp`):

```
1. cfg := config.Load(path)
2. migrated, err := config.MigrateCredentials(cfg, path)
   // If err != nil → abort startup with explicit error (see Failure semantics)
   // If migrated == true → log "credential migrated to hash" at INFO
3. app := webui.NewApp(cfg, ...)
   // NewApp receives cfg with adminPasswordHash populated, adminPassword empty
```

#### Migration algorithm (`config.MigrateCredentials`)

```
Input: cfg (loaded), path (config file path)
Output: cfg (migrated), error

1. hasHash := IsValidBcryptHash(cfg.AdminPasswordHash)
2. hasPlaintext := cfg.AdminPassword != "" && !IsValidBcryptHash(cfg.AdminPassword)
   // Note: if adminPassword contains a valid hash (user error), treat as hash.

3. If hasHash:
   a. If hasPlaintext (conflict): hash is authoritative.
      - Set cfg.AdminPassword = ""
      - Persist (atomic Save)
      - If save fails → return error (see Failure semantics)
   b. Else (no plaintext): no action needed.
   c. Return cfg, nil (no migration occurred or only conflict cleanup)

4. If !hasHash && hasPlaintext:
   a. hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), 12)
   b. If err → return error
   c. cfg.AdminPasswordHash = string(hash)
   d. cfg.AdminPassword = ""
   e. Persist (atomic Save via existing config.Save contract)
   f. If save fails → return error (see Failure semantics)
   g. Return cfg, nil (migration occurred)

5. If !hasHash && !hasPlaintext:
   a. No credential configured. No action.
   b. Return cfg, nil

6. If cfg.AdminPassword looks like a valid hash (edge case — user pasted hash
   into wrong field):
   a. Move to AdminPasswordHash
   b. Clear AdminPassword
   c. Persist
   d. Return cfg, nil
```

#### Idempotency

Once `adminPasswordHash` contains a valid hash and `adminPassword` is empty, every subsequent startup hits step 3b (no action). **No re-hashing occurs on every startup.** The hash is authoritative and stable.

#### Auth-disabled configs

If `authEnabled` is `false`, the migration still runs (defensive): if a legacy plaintext exists, it is hashed and cleared. This ensures that enabling auth later does not expose a stale plaintext. If no credential exists (both fields empty), migration is a no-op.

### Failure semantics

| Failure | Behavior |
|---------|----------|
| `bcrypt.GenerateFromPassword` fails (should not happen with valid input) | Startup **aborts** with explicit error. No false sense of security. |
| `config.Save` fails during migration (disk full, permissions, I/O error) | Startup **aborts** with explicit error: "credential migration failed: <err>". The plaintext remains on disk; the operator is informed. GoAl does NOT continue with the belief that the credential is protected. |
| Config file is corrupt/unreadable | Existing `config.Load` error handling (startup abort). Unchanged. |

The principle: **if the hash cannot be persisted, the system must not silently proceed as if the credential is secure.**

### Authentication wiring

`NewApp` (`internal/webui/server.go:71-76`) after migration:

```go
if cfg.AdminPasswordHash != "" {
    a.passwordStore.SetHash(cfg.AdminUser, cfg.AdminPasswordHash)
}
```

- `PasswordStore.SetHash(username, hash string) error` — new method. Stores a pre-computed bcrypt hash directly (no re-hashing). Validates: `username` non-empty, `hash` is a valid bcrypt hash (prefix + length). Returns error otherwise.
- The existing `PasswordStore.SetPassword(username, password)` is **retained** (future multi-user, testing) but is **not used in the production startup path**.
- After startup migration, plaintext is **never** passed to the auth subsystem as a persisted credential.
- Login (`ValidateCredentials`) compares user input against the stored bcrypt hash via `bcrypt.CompareHashAndPassword`. Unchanged.

### Settings password change (`PUT /api/v1/settings`)

`SystemHandler.SaveSettings` (`internal/webui/handlers/system.go:176-231`):

1. **Request input:** `admin_password` in the JSON body is a **transient plaintext** accepted only for the duration of the request. It is never persisted.
2. **If `admin_password` is non-empty** (password rotation):
   a. Validate length ≤ 72 bytes. If exceeded → HTTP 400 with controlled error.
   b. `hash, err := security.HashPassword(plaintext)`
   c. `cfg.AdminPasswordHash = hash`
   d. `cfg.AdminPassword = ""` (ensure legacy field is empty)
   e. Atomic `config.Save`
   f. Update live `PasswordStore.SetHash(username, hash)` — takes effect immediately, no restart needed.
   g. If save fails → HTTP 500, in-memory store NOT updated, hash NOT changed.
3. **If `admin_password` is empty/omitted:** preserve existing `adminPasswordHash` byte-for-byte. No re-hash. No write to the hash field.
4. **Unrelated settings changes** (port, host, dataDir, etc.): the `adminPasswordHash` field is preserved byte-for-byte in the saved config. The handler must not zero, re-serialize, or alter the hash value.

The `restart_required` hint is **removed** for password-only changes (they take effect immediately). It remains for other config changes that require restart (port, listen address).

### API and UI contract

| Surface | Contract |
|---------|----------|
| `GET /api/v1/metrics` (admin config fields) | Returns `admin_user` (string) + `admin_password_set` (boolean). **Never** returns hash or plaintext. Unchanged. |
| `POST /api/v1/auth/login` | Accepts plaintext password as transient input. Compares against hash. Response: session cookie or 401. Unchanged. |
| `PUT /api/v1/settings` | Accepts `admin_password` as transient plaintext input. Response: `{"status":"saved"}`. Never echoes the password or hash back. Unchanged request shape. |
| WebUI Settings form | `admin_password_set: true` → show "Password is set" + "Change password" button. `admin_password_set: false` → show "Set password" form. The hash value is never displayed. |
| Logs | Neither plaintext nor hash appears in any log output. Config is not logged. |

### Security properties

| Property | Guarantee |
|----------|-----------|
| Plaintext never persisted post-migration | Migration clears `adminPassword`; settings endpoint hashes before save; new configs have no `adminPassword` |
| No plaintext/hash in API responses | Only `admin_password_set: bool` exposed; verified by test |
| No plaintext/hash in logs | Config values are never logged; verified by test |
| Tamper resistance (best-effort) | If `adminPasswordHash` is corrupted to an invalid string, `SetHash` validation fails → startup aborts with clear error |
| Hash stability | Existing valid hash is never re-hashed on startup; preserved byte-for-byte across unrelated saves |
| 72-byte boundary | Enforced at input; bcrypt never receives over-length data |

### What this ADR does NOT cover

- Multi-user support (future; `PasswordStore` already supports multiple users)
- Password rotation/expiry policy (future, Product/UX)
- TLS for transport (separate P1 item: Native HTTPS/TLS)
- Encrypted config file at rest (out of scope; hash eliminates the primary risk)
- Kill orphan (separate P0 item, own ADR)
- Cost parameter upgrade path (future: detect cost < 12 on successful login, re-hash transparently)

## Acceptance contract (required tests)

The implementation is not complete until all of the following are tested:

| # | Scenario | Expected |
|---|----------|----------|
| 1 | Fresh config (no credential) | Only empty/absent hash field persisted; no `adminPassword` key |
| 2 | Legacy plaintext in `adminPassword` → startup | Migrated: `adminPasswordHash` populated, `adminPassword` empty in persisted file |
| 3 | After successful migration | Plaintext absent from config file on disk |
| 4 | Existing valid hash, no plaintext → startup | Hash unchanged (byte-for-byte); no re-hash occurred |
| 5 | Both `adminPasswordHash` (valid) + `adminPassword` (plaintext) present | Hash wins; plaintext cleared after successful save |
| 6 | `adminPassword` contains a valid hash (wrong field) | Moved to `adminPasswordHash`; `adminPassword` cleared |
| 7 | Migration save fails (simulated I/O error) | Startup aborts with explicit error; no silent continuation |
| 8 | Unrelated settings save (change port only) | `adminPasswordHash` preserved byte-for-byte |
| 9 | Password rotation via settings | New hash persisted; old hash replaced |
| 10 | Login with old password after rotation | Rejected (401) |
| 11 | Login with new password after rotation | Accepted (200 + session) |
| 12 | `GET /api/v1/metrics` response | Contains `admin_password_set: true`; does NOT contain hash or plaintext |
| 13 | Log output during migration/login/settings | No password or hash value appears |
| 14 | Auth-disabled config with legacy plaintext | Migration still runs (defensive); plaintext cleared |
| 15 | Password > 72 bytes via settings | HTTP 400, controlled validation error; hash unchanged |
| 16 | Password exactly 72 bytes | Accepted; hash generated correctly |
| 17 | Backward compatibility: v1-era config with plaintext | Migrated on first load; subsequent loads see only hash |
| 18 | Empty/omitted `admin_password` in settings | Existing hash preserved byte-for-byte |

## Consequences

### Positive

- Eliminates the primary credential-exposure vector (plaintext on disk)
- No new dependencies (bcrypt already in `go.mod`)
- Backward-compatible: existing plaintext configs migrate explicitly on first startup
- Password changes take effect without restart (improved UX)
- `config.Load()` remains a pure read function (no side effects)
- Explicit, testable migration with clear failure semantics
- Minimal code surface: one new field, one migration function, one `SetHash` method, settings endpoint adjustment

### Negative / trade-offs

- Two credential fields exist temporarily during migration (`adminPasswordHash` + legacy `adminPassword`). After migration, `adminPassword` is omitted via `omitempty`.
- Startup sequence gains one step (migration between Load and NewApp). Negligible latency (~250ms bcrypt only when migration occurs).
- If the operator has a config with a plaintext password and the disk is read-only, startup will fail. This is intentional (fail-closed).
- The `adminPassword` struct field remains in Go code for backward-compatible JSON unmarshaling of legacy files, but is zeroed after migration.

### Implementation checklist (for the follow-up implementation task)

1. Add `AdminPasswordHash string` field to `Config` struct (`json:"adminPasswordHash,omitempty"`).
2. Add `IsBcryptHash(s string) bool` helper in `internal/config/` (prefix + length check).
3. Add `config.MigrateCredentials(cfg Config, path string) (Config, bool, error)` — explicit migration function.
4. Wire migration in `cmd/goal/main.go` between `config.Load` and `webui.NewApp`.
5. Add `PasswordStore.SetHash(username, hash string) error` with validation.
6. Change `server.go` startup: `SetHash` instead of `SetPassword`.
7. Change `SaveSettings`: validate ≤72 bytes → hash → persist → update live store. Remove `restart_required` for password changes.
8. Ensure `SaveSettings` preserves `adminPasswordHash` byte-for-byte for unrelated changes.
9. Add `omitempty` to `AdminPassword` JSON tag so it disappears from post-migration files.
10. Update `Validate()`: when `authEnabled`, require `adminPasswordHash` to be a valid hash (not plaintext).
11. Tests: all 18 scenarios from the acceptance contract above.
12. Update `CONFIGURATION.md` (new field, migration behavior, 72-byte limit).
13. Update `goal.example.json` (show `adminPasswordHash` field).

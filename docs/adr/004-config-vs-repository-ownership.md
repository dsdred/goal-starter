# ADR 004: Config File vs Repository Ownership

**Status:** Proposed
**Date:** 2026-08-10
**Related:** ADR 001 (single binary), ADR 002 (supervisor)

## Context

GoAl has two sources of runtime/model/profile definitions:

1. **`goal.json`** — user-edited configuration file (config-driven startup).
2. **`goal_repo.json`** — unified JSON repository (API-driven persistence).

Current behavior (baseline `09371a0`):

- At startup, `storage.SeedFromConfig` imports `goal.json` entries into `goal_repo.json` **only if the ID does not already exist**.
- If the user edits an entity via the API, changes land in `goal_repo.json`.
- If the user subsequently edits `goal.json`, the API-managed version silently wins — no conflict detection, no update propagation.
- If an entity is removed from `goal.json`, it remains in `goal_repo.json` forever.

This creates a silent configuration drift: the user cannot tell which source is authoritative after the first run.

## Problem Statement

GoAl needs an explicit, predictable ownership policy that answers:

- Who is the Source of Truth after the first startup?
- What happens when `goal.json` changes?
- What happens when an entity is deleted from `goal.json`?
- How does the user recover from a conflict?

## Alternatives

### Option A: Seed-once (current behavior)

- First startup imports config into repo.
- Subsequent startups ignore config changes.
- Config is only an initial template.

**Downside:** Config is misleading after the first run. Users edit `goal.json` and expect changes to apply.

### Option B: Repository is always authoritative

- Config is imported once; after that, config is ignored.
- All changes happen via API/Web UI.

**Downside:** No declarative configuration for automated deployments.

### Option C: Config is always authoritative

- Config always wins; API changes are lost on restart.

**Downside:** API is useless for persistent changes.

### Option D: Explicit sync with ownership markers

- Config has `strategy: seed|mirror|ignore`.
- Repo entries have `source: config|api`.
- On startup:
  - `seed` → current SeedFromConfig behavior.
  - `mirror` → config entries override repo entries with same ID.
  - `ignore` → repo is authoritative.
- Entities deleted from config are marked `orphaned` (not deleted) for visibility.

## Decision

**Option D** is the recommended long-term policy. For the current iteration, we formalize **Option A** with explicit documentation:

1. **Current state:** `goal.json` is a startup seed. After the first run, `goal_repo.json` is the source of truth.
2. **Documentation requirement:** README must explicitly state that edits to `goal.json` after the first startup only affect **new** entities (by ID).
3. **Future work:** Implement Option D with `strategy` field in config.

## Consequences

### Immediate (Option A + documentation)

- Users know that `goal.json` is only a seed.
- No silent surprise when config edits don't take effect after the first run.

### Long-term (Option D)

- Predictable behavior for both manual config edits and API-driven changes.
- Deletion semantics are explicit (orphan vs remove).
- No silent drift.

## Implementation Notes

- `SeedFromConfig` already skips existing IDs (seed semantics).
- For Option A documentation:
  - Update README.md and README_RU.md with a "Config vs Repository" section.
  - Emphasize that `goal_repo.json` is the runtime store.
- For Option D:
  - Add `strategy` field to `goal.json` schema.
  - Add `source` field to repository entries.
  - Implement sync on startup when `strategy == "mirror"`.

## Risks

| Risk | Mitigation |
|------|------------|
| User confusion after first run | Explicit README section |
| Data loss on accidental config edit | Backup recovery in JSONRepository (.bak) |
| Complexity of Option D | Implement only after user feedback |

## Verification

- Startup with existing `goal_repo.json` does not duplicate entities.
- Startup with empty `goal_repo.json` imports config.
- README explicitly documents the seed-once behavior.

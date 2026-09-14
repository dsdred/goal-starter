# ADR 014: Portable Configuration — Variable Resolution and Secret-Safe Export/Import

**Status:** Accepted — owner contract agreed 2026-09-13; **Slice 1 implemented 2026-09-14** (commit pending Owner gate); Slice 2/3 NOT STARTED
**Date:** 2026-09-13
**Related:** ADR 004 (Config vs Repository ownership), ADR 010 (Pipeline), ADR 011 (Windows Service — owner decision 3: no new path resolution without Owner contract), ADR 013 (Pipeline repeatable entries), ADR 009 (Hot-reload — restart-class fields), ADR 006 (Secure Credential Storage), ADR 007 (Audit Logging), ROADMAP P1 "Portable Configuration & Path Variables"

## Context

GoAl stores its product graph (Runtime → Model → Pipeline) in `goal_repo.json`. Machine-specific values (executable paths, working directories, model file paths in Args, environment values) make a configuration non-portable across machines. The user must manually edit every path when moving to a new machine.

The repository's existing launch path (`LaunchResolver.Resolve` / `Preview`) builds a `CommandSpec` from raw stored values: `resolveExecutablePath` handles relative paths against `WorkingDirectory`, environment is merged parent→runtime→model→custom, and args are concatenated. **No variable expansion exists** anywhere in the production code (no `os.ExpandEnv`, no `${VAR}` handling).

ADR 011 owner decision 3 explicitly forbids inventing new path resolution semantics without an Owner contract. This ADR provides that contract for variable resolution and defines the portable export/import boundary.

## Problem

1. **Non-portable configuration:** A `goal_repo.json` with absolute paths is unusable on another machine without manual editing.
2. **No declarative export/import:** There is no way to export the Runtime → Model → Pipeline graph to a portable document and import it into another GoAl instance.
3. **No variable indirection:** Users cannot write `${GOAL_DATA}/models/my-model.gguf` and have GoAl resolve it at launch time.

## Constraints / Prior Decisions

- **ADR 004:** `goal_repo.json` is the source of truth after first startup. `goal.json` is seed-only. Portable Configuration operates on the repository's domain entities, not on `goal.json`.
- **ADR 011 (owner decision 3):** No new path resolution semantics without an explicit Owner contract. This ADR is that contract.
- **ADR 009 (restart-class fields):** `dataDir` is a restart-class field (baked at startup). The built-in `${GOAL_DATA}` variable resolves to the currently-effective `dataDir`.
- **ADR 010 / 013:** Pipeline entries carry per-entry `Args` overrides (all-or-nothing, strict). Pipeline `PipelineModel.ID` provides entry identity.
- **Write-only environment contract:** Environment values are never returned to the API/UI (keys only). Any export must preserve this.
- **Restart re-resolution (shipped `971fa58`):** `RestartInstance` re-resolves current Model/Runtime/Pipeline configuration. Variable resolution must compose with this: a restart resolves variables from the *current* process environment.
- **No shell:** Process arguments are always `[]string`. Variable substitution is purely textual and produces strings that are then passed as-is to `exec.Cmd`.

## Decision

### D1 — Portable Configuration Boundary

Portable Configuration is the **declarative product graph**:

| Included | Excluded |
|----------|----------|
| Runtime definitions | `goal.json` as a file |
| Model definitions | Server settings (`ListenAddress`, `WebPort`, `DataDir`) |
| Pipeline definitions | Admin credentials / `adminPasswordHash` |
| PipelineEntry IDs and structure | Instances / process/PID state |
| Repeated Model entries (ADR 013) | History / audit / logs |
| Per-entry Args overrides | Windows Service registration / state |
| Active / Autostart declarative fields | |

This is **not** a full GoAl backup. It is a portable description of *what to launch*, not *how GoAl runs*.

### D2 — Variable Syntax

A variable reference has the exact form:

```
${NAME}
```

where `NAME` matches the regular expression `[A-Za-z_][A-Za-z0-9_]*`.

- Resolution is **explicit and deterministic**.
- `$NAME` (no braces) has no special meaning; it is literal text.
- `%NAME%` has no special meaning; it is literal text.
- An unescaped `${` begins an **attempted variable reference**. The text between `${` and the next `}` must match the `NAME` grammar; otherwise the result is an `InvalidVariableReference` error (see D9).

### D3 — Escape Syntax

A literal `$` is produced by the escape sequence `$$`.

Tokenization rules (scanned left to right):

| Sequence | Meaning |
|----------|---------|
| `$$` | Literal `$` in the output |
| `${NAME}` | Variable reference (resolved or error) |
| `$` (followed by anything other than `$` or `{`) | Literal `$` |
| Any other character | Literal |

**Justification for `$$` over `\${`:**

1. Backslash has meaning in Windows paths (`C:\Users\...`) and in the ADR 013 D7 `parseArgs` tokenizer (backslash-escape inside quoted sections). A backslash-based escape would create cross-context ambiguity in Args tokens.
2. `$$` is a single-character doubling with no cross-context ambiguity; it is the same convention used by Make, CMake, Go templates (in the `{{` idiom), and shell `$$` (PID).
3. The only interaction with the tokenizer: `$$` inside a double-quoted Args token is two characters in the stored string and becomes one `$` after resolution — the tokenizer sees the resolved value, never the escape form.

**Examples:**

| Stored value | Resolved output |
|---|---|
| `${GOAL_DATA}/models` | `<dataDir>/models` |
| `$$HOME` | `$HOME` (literal dollar + text) |
| `${HOME}/$$literal` | `<env HOME value>/$literal` |
| `$NOTAVAR` | `$NOTAVAR` (literal; `$` not followed by `{`) |
| `${1BAD}` | **Error**: `InvalidVariableReference` (name must start with letter/underscore) |
| `${}` | **Error**: `InvalidVariableReference` (empty name) |
| `${NAME` (no closing `}`) | **Error**: `InvalidVariableReference` (unterminated) |

### D4 — Variable Sources (MVP)

Two sources, in precedence order:

1. **Built-in GoAl variables** (fixed, known at compile time)
2. **Process environment variables** of the GoAl server process

**Deferred to a future extension** (NOT in MVP):

- Persisted user-defined GoAl variable map
- Variable CRUD endpoints
- Variable storage in the repository or `goal.json`
- Schema version bump solely for variables
- Variable-editor UI

### D5 — Built-in Variables

The initial built-in set is:

| Variable | Value | Rationale |
|----------|-------|-----------|
| `GOAL_DATA` | The currently-effective data directory (`Config.DataDir`, absolute, cleaned) | Model files, GGUF, MMProj, and other user data conventionally live under the data directory. This is the single most useful variable for portability. |

This is the **smallest useful set**. A second built-in (e.g., `GOAL_REPO` for the repository file path, `GOAL_HOME` for the binary directory) should be added only when a demonstrated need arises.

**Reserved namespace:** All GoAl built-in variables use the `GOAL_` prefix and are resolved from the built-in table before consulting the process environment. An OS environment variable with the same name as a known built-in **cannot override** the built-in (the built-in table is checked first and is authoritative for known names).

Non-built-in `GOAL_*` names (e.g., `GOAL_CUSTOM`) fall through to the process environment normally.

### D6 — Precedence (Authoritative)

Resolution of a variable name `N`:

1. If `N` is a **known built-in** (`GOAL_DATA` in MVP): resolve to the built-in value. The process environment is **not consulted**.
2. Otherwise: resolve from the **GoAl process environment** (same visibility as `os.LookupEnv`).
3. If not found in either source: **undefined-variable error** (see D9).

This precedence is deterministic and cannot be overridden by configuration. An OS environment variable named `GOAL_DATA` has no effect on `${GOAL_DATA}` resolution.

### D7 — Resolution Timing

Stored Runtime/Model/Pipeline values remain **RAW and unresolved** in the repository.

Variable resolution occurs **only at consumption**:

- **Launch:** `LaunchResolver.Resolve` / `ResolveToInstance`
- **Restart:** `RestartInstance` re-resolves the current launch configuration; variable resolution reads the *current* process environment at the moment of restart
- **Resolve/Preview API:** `POST /api/v1/models/{id}/resolve` (existing endpoint, extended — see D16)

**Never** resolve at:

- Save (entity create/update via API or UI)
- Load (repository deserialization)
- Import (portable bundle import)

This guarantees:
- The stored form is machine-independent (portable).
- A change in the process environment is picked up on the next launch/restart without editing stored values.
- The recently shipped Restart re-resolution behavior (`971fa58`) composes correctly: restart reads current env, resolves variables, builds a fresh `CommandSpec`.

### D8 — Resolution Algorithm

Single-pass, non-recursive substitution:

```
resolveString(raw string, source VariableSource) (string, error):
    result = ""
    i = 0
    while i < len(raw):
        if raw[i] == '$' and i+1 < len(raw):
            if raw[i+1] == '$':
                result += '$'
                i += 2
                continue
            if raw[i+1] == '{':
                // find closing '}'
                j = index of next '}' after i+2
                if j == -1:
                    return error(InvalidVariableReference, "unterminated variable reference at position " + i)
                name = raw[i+2 : j]
                if !validName(name):
                    return error(InvalidVariableReference, "invalid variable name '" + name + "' at position " + i)
                value, found = source.Lookup(name)
                if !found:
                    return error(UndefinedVariable, "undefined variable: " + name)
                result += value
                i = j + 1
                continue
            // bare '$' not followed by '$' or '{'
            result += '$'
            i++
        else:
            result += raw[i]
            i++
    return result
```

Key properties:

- **Single pass:** the scanner advances left to right; the output buffer is never re-scanned.
- **No recursion:** a resolved value that itself contains `${...}` is inserted as-is and NOT further expanded.
- **No cycles:** non-recursive expansion makes cycles impossible.
- **Environment keys are never expanded:** only values are resolved. A key containing `${X}` is a literal key.
- **Bounded error (fail-fast):** the first resolution error (either `InvalidVariableReference` or `UndefinedVariable`) stops processing. No partial result is returned. The error identifies the field being resolved and the offending reference.

### D9 — Resolution Errors

Two distinct error classes exist. Both are **bounded resolution failures** — they identify the exact field and the offending reference.

**`UndefinedVariable`:**

The reference is syntactically valid (`${NAME}` where `NAME` matches the grammar), but no built-in or process-environment value exists for that name.

**`InvalidVariableReference`:**

The reference has malformed syntax. This includes:

- `${123}` — name starts with a digit
- `${}` — empty name
- `${NAME` — unterminated (no closing `}`)
- `${NA ME}` — name contains a space (does not match the grammar)

**Behavior for both error classes:**

| Context | Behavior |
|---------|----------|
| **Preview / Resolve API** | Return a bounded diagnostic identifying the error class, the variable name (or raw text for malformed), and the affected field (e.g., `"model.args[2]: undefined variable MY_MODEL_PATH"` or `"runtime.executable: invalid variable reference '${1BAD}'"`). No launch occurs. |
| **Launch / Restart** | Refuse the launch. Return/log a bounded diagnostic. **Never launch with partially substituted values.** The instance is not started; the error is surfaced to the caller. |

Silent preservation of a malformed or undefined `${...}` sequence as literal text is **forbidden**. If the user intends a literal `${...}` sequence, they must use the `$$` escape (D3): `$${NAME}` produces the literal text `${NAME}`.

### D10 — Empty Variables

| State | Behavior |
|-------|----------|
| **Defined, empty value** (e.g., `MY_VAR=` in the environment) | The variable IS defined. `${MY_VAR}` resolves to the empty string `""`. |
| **Undefined** (not in environment, not a built-in) | Error per D9. |

A defined-empty variable is a valid, distinct state. Its resolution produces an empty string, which may result in an empty path component or an empty arg token — the user's responsibility. GoAl does not reject empty resolved values (the existing `Runtime.Executable == ""` validation still applies to the final resolved executable path).

**Implications:**
- `${GOAL_DATA}` is always defined (it resolves to the non-empty data directory).
- A user-defined environment variable set to empty (e.g., `EXTRA_ARGS=`) resolves to `""`; if used as `${EXTRA_ARGS}/path`, the result is `/path`.
- An empty resolved `Executable` fails the existing `runtime executable is empty` validation — this is a deterministic error, not a variable-resolution error.

### D11 — Supported Resolution Fields

Variable resolution applies to the following **launch-consumed string fields**:

| Entity | Field | Notes |
|--------|-------|-------|
| Runtime | `Executable` | Resolved before `resolveExecutablePath` (relative-path logic applies to the resolved value) |
| Runtime | `WorkingDirectory` | Resolved; if empty, remains empty (relative-path logic: no join) |
| Runtime | `Environment` values | Each value resolved; keys never resolved |
| Model | `Args` (each token) | Each token in the `[]string` resolved independently |
| Model | `Environment` values | Each value resolved; keys never resolved |
| Pipeline entry | `Args` (each token, when non-empty) | Custom override tokens resolved; when empty (use-model-args), Model.Args is resolved |

**NOT resolved (verified):**

| Field | Reason |
|-------|--------|
| `Model.RuntimeID` | Entity reference (ID), not a path |
| `PipelineModel.ModelID` | Entity reference (ID) |
| `PipelineModel.ID` / `Pipeline.ID` | Entity identity |
| `Model.Name` / `Runtime.Name` / `Pipeline.Name` | Display labels, not launch-consumed |
| `Model.Active` / `Pipeline.Active` / `PipelineModel.AutoStart` | Boolean fields |
| `Model.AutostartDelay` | Numeric field |
| Environment **keys** | Keys are never expanded (D8) |

The existing relative-path semantics (`resolveExecutablePath`: if not absolute and `WorkingDirectory` is non-empty, join) are **unchanged**. Variable substitution is textual; after substitution, the existing path logic applies to the resolved string.

### D12 — Backward Compatibility

- **Existing configurations without `${...}`** behave exactly as before. The resolver sees no variable references and returns the string unchanged (zero overhead: a fast path checks for the presence of `$` before invoking the full tokenizer).
- **Existing absolute paths** remain valid and are unaffected.
- **Existing relative-path behavior** (`resolveExecutablePath`) is unchanged.
- **Semantic compatibility edge — valid references:** a pre-existing literal `${NAME}` sequence in a stored field where `NAME` matches the grammar (e.g., a model arg containing the literal text `${HOME}`) will now be interpreted as a variable reference. If `HOME` is defined in the process environment, it will resolve (likely the intended behavior); if not defined, it produces an `UndefinedVariable` error at launch. The ADR-defined escape (`$$`) is the migration mechanism: the user edits the field to `$${HOME}` to preserve the literal meaning.
- **Semantic compatibility edge — malformed references:** a pre-existing literal `${...}` sequence with a non-conforming name (e.g., `${1BAD}`, `${}`) will now produce an `InvalidVariableReference` error at launch (previously it was inert text). The escape (`$$`) is the migration mechanism: the user edits the field to `$${1BAD}` to preserve the literal meaning. This case is expected to be exceedingly rare (a literal `${1BAD}` in a launch-consumed field has no plausible prior use).
- **No automatic mutation/migration** of stored values on upgrade. The stored bytes are unchanged; only the resolution semantics at consumption time change.

### D13 — Secret / Export Policy

**Default export is secret-safe.**

| Data | Exported? | Notes |
|------|-----------|-------|
| Runtime definitions (ID, Name, Executable, WorkingDirectory) | Yes | Executable/WorkingDirectory are configuration, not secrets |
| Runtime Environment **keys** | Yes | Keys are structural |
| Runtime Environment **values** | **No** (by default) | Write-only contract; values may be credentials |
| Model definitions (ID, Name, RuntimeID, Args, Active, AutostartDelay) | Yes | Args are exported (see limitation below) |
| Model Environment **keys** | Yes | |
| Model Environment **values** | **No** (by default) | |
| Pipeline definitions (ID, Name, Active) | Yes | |
| Pipeline entries (ID, ModelID, Args, AutoStart) | Yes | Entry Args are exported |
| `adminPasswordHash` | **Never** | Not part of Portable Configuration |
| Instances | **Never** | Runtime state, not configuration |
| History / audit / logs | **Never** | |

**Args limitation (explicit, non-negotiable):**

GoAl cannot reliably determine whether an arbitrary Args token contains a secret. A token like `--api-key sk-abc123` is indistinguishable from `--model /path/to/model.gguf` without semantic parsing of every possible CLI tool.

The ADR **does not pretend** Args can be automatically redacted. The export format includes Args as configuration data. The export API and UI documentation **must warn** that Args are exported as-is and may contain user-supplied sensitive values.

**Deferred (NOT in MVP):**
- An `includeSecrets: true` export flag that includes environment values
- Per-field redaction heuristics
- Encrypted export

### D14 — Portable Bundle Format

A versioned JSON document:

```json
{
  "format": "goal-portable-config",
  "version": 1,
  "runtimes": [
    {
      "id": "rt-001",
      "name": "Llama.cpp",
      "executable": "${GOAL_DATA}/bin/llama-server",
      "working_directory": "${GOAL_DATA}/bin",
      "environment_keys": ["CUDA_VISIBLE_DEVICES"]
    }
  ],
  "models": [
    {
      "id": "m-001",
      "name": "My Model",
      "runtime_id": "rt-001",
      "args": ["-m", "${GOAL_DATA}/models/my-model.gguf", "--port", "8080"],
      "environment_keys": ["MODEL_EXTRA"],
      "active": true,
      "autostart_delay": 0
    }
  ],
  "pipelines": [
    {
      "id": "p-001",
      "name": "Production",
      "active": true,
      "models": [
        {
          "id": "pe-001",
          "model_id": "m-001",
          "args": [],
          "auto_start": true
        }
      ]
    }
  ]
}
```

**Format identity:** `"format": "goal-portable-config"` — a fixed string that identifies the document type.

**Version:** integer, starting at 1. The importer must understand the exact version; forward-compatibility is not guaranteed.

**Field naming:** reconciled against existing DTO/domain naming:
- `runtimes[].executable` matches `RuntimeEntry.Executable` / `Runtime.Executable` (JSON tag `"executable"`)
- `runtimes[].working_directory` matches `RuntimeEntry.WorkingDirectory` (JSON tag `"working_directory"`)
- `models[].runtime_id` matches `ModelEntry.RuntimeID` (JSON tag `"runtime_id"`)
- `pipelines[].models[].model_id` matches `PipelineModel.ModelID` (JSON tag `"model_id"`)
- `pipelines[].models[].auto_start` matches `PipelineModel.AutoStart` (JSON tag `"auto_start"`)

**Dependency graph preservation:**
- `models[].runtime_id` references a `runtimes[].id`
- `pipelines[].models[].model_id` references a `models[].id`
- `pipelines[].models[].id` is the entry identity (ADR 013)
- Repeated model entries (same `model_id`, different entry `id`) round-trip losslessly

**Environment:** only **keys** are present (D13). Representation (settled by Owner SC-2): `"environment_keys": ["KEY1", "KEY2"]` — a sorted array of key names. Values are never exported. `environment_keys` is advisory redaction metadata describing keys whose values existed in the source but were removed. Import does NOT create Environment entries from `environment_keys`; imported entities have empty Environment. `environment_keys` is not guaranteed to survive export → import → export.

**NOT in the bundle:** `created_at`, `updated_at` timestamps (the importing instance generates its own).

### D15 — Import Validation and Collision Policy

**Trust boundary:** The portable bundle is **untrusted input**. Import performs no process launch.

**Validation (before any mutation):**

1. Bundle `format` field == `"goal-portable-config"`
2. Bundle `version` is understood by the importer (v1 in MVP)
3. All required fields present and correctly typed
4. All IDs are non-empty strings
5. Every `models[].runtime_id` references an existing `runtimes[].id` in the bundle
6. Every `pipelines[].models[].model_id` references an existing `models[].id` in the bundle
7. Pipeline entry structure is valid (entry IDs unique within the bundle)
8. Variable syntax in all resolved fields is well-formed (balanced `${}`, valid names)
9. Existing domain constraints (e.g., `Runtime.Executable` non-empty, `Model.Name` non-empty)

**Collision policy — v1: REJECT.**

If ANY entity ID in the bundle conflicts with an existing entity in the repository:

- **No overwrite.** No merge. No automatic ID remapping. No rename.
- Detect **all** conflicts before mutation.
- Return a bounded conflict report listing every conflicting ID (entity type + ID).
- **Zero writes** on conflict. The repository is unchanged.

**Future** (deferred): merge policies, ID remapping, rename strategies.

**Importing Active entities:** A model or pipeline with `active: true` in the bundle is imported with that flag set. **Importing does NOT start them.** Normal GoAl startup/autostart semantics apply on the next server start (or are already active if the server is running). Import is a data operation, not a lifecycle operation.

### D16 — Import Atomicity

Import of the graph is **all-or-nothing**.

The implementation must introduce a **repository-level atomic graph-import primitive**:

```
validate(bundle) → error
    ↓ (valid)
stage in memory (build all entities in memory, verify references)
    ↓ (staged)
check for collisions against current repository state
    ↓ (no collision)
one durable repository write (single `saveLocked` / `WriteFileDurable` call)
    ↓
on success: update in-memory state
on failure: rollback in-memory state to pre-import; repository file unchanged
```

The existing per-entity CRUD loops (one `saveLocked` per entity) are **NOT sufficient** — a failure mid-loop would leave a partial graph. The atomic primitive reuses the existing durable-write guarantees (`fsutil.WriteFileDurable`: fsync temp, read-back, `.bak`, atomic rename, directory fsync).

No partial imported graph may survive a failure.

### D17 — API / Preview Implications

The existing `POST /api/v1/models/{id}/resolve` endpoint returns a `ModelResolveResult` (Executable, Args, WorkingDirectory, EnvironmentKeys). With variable resolution, this endpoint **must resolve variables** as part of its contract:

- The returned `Executable`, `Args`, and `WorkingDirectory` are **resolved** (variables substituted).
- If an `UndefinedVariable` or `InvalidVariableReference` error is encountered, the endpoint returns a bounded diagnostic (HTTP 422 or 400) identifying the error class, the variable name (or raw text), and the field, **without** launching anything.
- `EnvironmentKeys` remain keys-only (write-only contract preserved).

**Semantic contract (implementation forensics deferred):**

The ADR defines that the resolve/preview endpoint returns resolved values and resolution-error diagnostics (both `UndefinedVariable` and `InvalidVariableReference`). The exact HTTP status code, error response shape, and whether a new `POST /api/v1/models/{id}/resolve?includeVariables=true` variant is needed (vs. always resolving) are implementation details to be determined during Slice 1 implementation forensics. The existing endpoint's contract is **extended**, not replaced.

**Future (Slice 2):** The export/import API endpoints (`GET /api/v1/export`, `POST /api/v1/import`) are defined in Slice 2 with their own API contract.

### D18 — UI Implications

**Slice 1 (variable resolution):** No new UI. The user enters `${VAR}` references in the existing editors (wizard Args field, Runtime executable field, environment value fields). The resolve/preview endpoint (already accessible from the UI's "Resolve" action) will show resolved values and surface resolution-error diagnostics (undefined or malformed references).

**Slice 2/3 (export/import UI):** A future slice adds:
- Export button (downloads the JSON bundle)
- Import flow (upload → validation → conflict report → confirm → done)
- Unresolved-variable diagnostics display
- EN/RU localization
- Maintained browser acceptance coverage

Slice 1 does **not** introduce unrelated UI changes.

### D19 — Migration / Schema Implications

**Slice 1 (variable resolution):**
- **No schema version bump.** The repository schema stays at v8. Variable references are stored as raw strings in existing fields (`Executable`, `WorkingDirectory`, `Args`, `Environment` values). The storage format is unchanged; only the resolution semantics at consumption time change.
- **No migration required.** Existing data loads and behaves identically (no `${...}` → no resolution → same output).
- **No `goal.json` changes.** The seed format is unchanged.

**Slice 2 (export/import):**
- The portable bundle is a separate document (not the repository file). It has its own `format` + `version` identity.
- The repository may need the new atomic graph-import primitive (a method on `JSONRepository`), but this does not change the schema version (it is a write-path addition, not a data-format change).

### D20 — Phased Implementation

**SLICE 1 — Variable Resolution Foundation**

Scope:
- Pure resolver function (the D8 algorithm) in `internal/domain/` or a new `internal/domain/resolve/` subpackage
- Built-in variable table (`GOAL_DATA`)
- Process-environment source
- Integration into `LaunchResolver.Resolve`, `LaunchResolver.Preview`, and `ResolveToInstance`
- Integration into `RestartInstance` (already re-resolves; the resolver now handles variables)
- Escape syntax (`$$`)
- Undefined-variable error with bounded diagnostic
- Extension of the existing `POST /api/v1/models/{id}/resolve` endpoint to return resolved values + diagnostics
- Unit tests: resolver (all tokenization cases, precedence, empty, undefined, no-recursion, single-pass), integration tests (launch with variables, restart with changed env, preview diagnostics)
- Documentation: CONFIGURATION.md (variable syntax, built-ins, resolution timing, escape, undefined behavior), API.md (resolve endpoint extension), USER_GUIDE EN/RU (how to use variables), LIMITATIONS.md (Args secret limitation)

**NOT in Slice 1:** export/import, bundle format, atomic import primitive, variable editor UI, user-defined variable store.

**SLICE 2 — Portable Bundle + Atomic Import/Export**

Scope:
- Bundle format v1 (D14)
- Secret-safe export (keys only, no env values, no credentials, no instances)
- Dependency closure (collect all referenced runtimes → models → pipelines)
- Bundle validation (D15)
- Conflict detection (all collisions, bounded report)
- Atomic graph-import repository primitive (D16)
- API endpoints: `GET /api/v1/export` (optional `?runtime_id=|model_id=|pipeline_id=`), `POST /api/v1/import` (raw Bundle v1 body, `?dry_run=true` for dry-run)
- Auth + CSRF per convention
- ADR 007 audit events for export/import
- Tests: round-trip (export → import → identical graph), collision rejection, validation failures, atomicity (failure mid-import → zero writes)

**SLICE 3 — Product UI / Acceptance**

Scope:
- Export UI (button → download)
- Import UI (upload → validation progress → conflict report → confirm)
- Dry-run / conflict presentation
- Unresolved-variable diagnostics in the import context
- EN/RU localization
- Maintained browser acceptance suite additions (`tests/browser/portable.cjs`)

**Future (NOT authorized, separate Owner decision required):**

- User-defined persisted variable store (variable CRUD, repository storage, UI editor)
- Secret-value export (opt-in `includeSecrets`)
- Merge / ID-remap import policies
- Relative-root path system (beyond the existing `resolveExecutablePath`)
- ADR 004 Option D ownership strategy/source markers

### D21 — Slice 1 Acceptance Boundary

After Slice 1 is complete and shipped:

1. A user can store `${GOAL_DATA}/models/my-model.gguf` in a Model's Args and GoAl resolves it at launch.
2. A user can store `${SOME_PROCESS_ENV}` in a Runtime's Executable and GoAl resolves it from the server's environment at launch.
3. A user can store `${GOAL_DATA}` in a Runtime's WorkingDirectory and it resolves correctly.
4. A user can store `${MY_VAR}` in an environment value and it resolves at launch.
5. Pipeline entry Args containing `${VAR}` resolve at pipeline launch and at per-entry restart.
6. **Restart** re-resolves variables from the **current** process environment (if an env var changes between launch and restart, the new value is used).
7. The resolve/preview endpoint returns resolved values; an undefined variable or malformed reference produces a bounded diagnostic (no launch, no partial result).
8. **Undefined variables and malformed references refuse the launch** with a clear error identifying the error class, the variable name (or raw text), and the affected field.
9. `$$` produces a literal `$` in the resolved output.
10. Existing configurations without `${...}` behave byte-for-byte identically to pre-Slice-1 behavior.
11. **No export/import feature exists after Slice 1.** There is no `/api/v1/export` or `/api/v1/import` endpoint. The portable bundle is not yet implemented.

Slice 1 is independently shippable and testable. It changes launch-time behavior (resolution) without changing storage format, API surface (beyond the resolve endpoint extension), or UI.

### D22 — Security / Trust Boundary

- The portable bundle is **untrusted input**. It is validated before any mutation (D15).
- Import does **not** launch processes.
- Variable resolution reads the **GoAl process environment** — the same environment the process already has. No new environment source is introduced.
- The built-in `GOAL_DATA` resolves to the configured data directory (already an authenticated, server-side value).
- Export is secret-safe by default (D13). The user is warned about Args.
- No new unauthenticated API surface: export/import endpoints require auth + CSRF (same as all other mutating/authenticated endpoints).
- The resolver is a pure function (string in, string out, source table). It does not perform I/O, access the filesystem, or invoke processes.

### D23 — Rejected / Deferred Alternatives

| Alternative | Status | Reason |
|-------------|--------|--------|
| `$VAR` (no braces) syntax | **Rejected** | Ambiguous with shell-like text in Args; no way to distinguish `${VAR}` (variable) from `$VAR` (literal) without additional rules; braces make the boundary explicit |
| `%VAR%` syntax | **Rejected** | Windows-registry-like; collides with potential `%` usage in paths or format strings; less conventional for developer tools |
| `\${VAR}` escape | **Rejected** | Backslash conflicts with Windows paths and the ADR 013 `parseArgs` tokenizer; cross-context ambiguity |
| Recursive / nested variable expansion | **Rejected** | Non-recursive (single-pass) is simpler, safer (no cycles), deterministic, and matches user expectation ("I put a path in, I get a path out") |
| Silent fallback (keep literal `${UNDEFINED}` or pass through malformed `${1BAD}` as text) | **Rejected** | Hides misconfiguration; a user who typo'd a variable name or wrote invalid syntax would get a silent broken path; explicit errors (`UndefinedVariable` / `InvalidVariableReference`) are safer |
| Persisted user-variable store (MVP) | **Deferred** | Not needed for the core portability use case (machine paths via process env + `GOAL_DATA`); adds schema, CRUD, UI, and security surface without proportional value in v1 |
| `includeSecrets` export flag (MVP) | **Deferred** | Requires a separate Owner security decision; the default is safe (keys only); adding opt-in secrets expands the attack surface of the bundle format |
| Merge / remap import policies | **Deferred** | REJECT is the safe, simple v1; merge policies require detailed conflict-resolution semantics that need their own design |
| Relative-root path system (e.g., `~`, `$HOME` expansion beyond variable syntax) | **Deferred** | The existing `resolveExecutablePath` (relative to WorkingDirectory) + variable resolution covers the use case; a separate root system is redundant complexity |
| Export `goal.json` / server settings | **Rejected** | Out of scope (D1); Portable Configuration is the product graph, not the server configuration |
| ADR 012 for this topic | **Rejected** | ADR 012 is historically associated with the paused TLS direction; ADR 014 is the correct next number |

### D24 — Test Obligations

**Slice 1 tests:**

- **Resolver unit tests** (pure function, no I/O):
  - Simple substitution: `${GOAL_DATA}` → data dir
  - Multiple variables in one string
  - `$$` escape → literal `$`
  - `$` not followed by `{` or `$` → literal `$`
  - Invalid name (starts with digit, `${123}`) → `InvalidVariableReference` error
  - Unterminated `${NAME` (no closing `}`) → `InvalidVariableReference` error
  - Empty name `${}` → `InvalidVariableReference` error
  - Name with space `${NA ME}` → `InvalidVariableReference` error
  - Non-recursive: env var value containing `${OTHER}` is NOT expanded
  - Single-pass: overlapping/nested-looking patterns
  - Precedence: `GOAL_DATA` built-in wins over env var with same name
  - Case sensitivity: `${GOAL_DATA}` vs `${goal_data}` (only the exact built-in name matches; env vars are case-sensitive on POSIX, case-insensitive on Windows — matching the existing `LaunchResolver.normalizeKey` behavior)
  - Empty defined variable → resolves to `""`
  - Undefined variable (`${UNKNOWN}`) → `UndefinedVariable` error with name + field
  - No `$` in input → fast path, returns input unchanged

- **Integration tests** (with real repository + fake-runtime):
  - Launch with `${GOAL_DATA}` in executable → process starts with resolved path
  - Launch with `${PROCESS_ENV}` in Args → child receives resolved arg (fake-runtime argv-file proof)
  - Restart with changed environment → new value resolved
  - Preview with undefined variable → `UndefinedVariable` diagnostic, no launch
  - Preview with malformed reference (`${1BAD}`) → `InvalidVariableReference` diagnostic, no launch
  - Pipeline launch with variables in entry Args → resolved
  - Pipeline per-entry restart → re-resolved
  - Existing config without variables → identical behavior (regression)

- **Browser acceptance** (if the resolve endpoint UI surface changes observably):
  - Resolve shows resolved values
  - Undefined variable shows diagnostic

**Slice 2 tests:**
- Export → import → identical graph (round-trip)
- Export excludes env values (secret-safe)
- Export excludes instances, credentials
- Import with collision → zero writes, bounded report
- Import with invalid bundle → rejected, zero writes
- Import atomicity: simulate write failure → zero partial writes
- Import does not start active entities

### D25 — Consequences

**Positive:**
- Configurations are portable: `${GOAL_DATA}/models/x.gguf` works on any machine with the same data-dir layout.
- Machine-specific paths are externalized to the environment, not hard-coded in the repository.
- Export/import enables declarative configuration management (version-control the bundle, deploy to new machines).
- The resolve/preview endpoint becomes a true "what will launch" tool (shows resolved paths).
- Composes with the shipped Restart re-resolution: environment changes are picked up on restart.

**Negative:**
- Launch-time error surface increases: undefined or malformed variable references now produce launch failures (previously a bad path would just fail at exec time; now it fails earlier with a clearer diagnostic).
- Users who had literal `${...}` text in their configuration (extremely rare) must escape it with `$$`.
- The resolver adds a small runtime cost at launch (negligible: single-pass string scan, fast path for no-`$` strings).

**Neutral:**
- Storage format unchanged (v8).
- No new dependencies.
- The resolver is a pure function — no I/O, no concurrency concerns beyond reading `os.Environ()` (which is immutable after process start on both platforms).

## Acceptance Contract (Slice 1)

1. `${GOAL_DATA}` in Runtime.Executable resolves to the absolute data directory at launch.
2. `${SOME_ENV}` in Model.Args resolves to the process environment value at launch (proven via fake-runtime argv-file).
3. `${GOAL_DATA}` in Runtime.WorkingDirectory resolves correctly; relative-path logic applies to the resolved value.
4. `${VAR}` in Runtime/Model environment values resolves at launch; keys are never expanded.
5. Pipeline entry Args with `${VAR}` resolve at pipeline launch and per-entry restart.
6. Restart re-resolves from the current environment (env var changed between launch and restart → new value used).
7. Undefined `${MISSING}` → launch refused, bounded `UndefinedVariable` diagnostic names the variable and field.
8. Malformed `${1BAD}` / `${}` / `${NAME` (no close) → launch refused, bounded `InvalidVariableReference` diagnostic names the raw text and field.
9. Preview/resolve endpoint returns resolved values; undefined or malformed reference → diagnostic (no launch).
10. `$$` in a stored field → literal `$` in the resolved output.
11. `$NOTAVAR` (no braces) → literal `$NOTAVAR` (unchanged).
12. Existing configurations without `${...}` produce byte-identical `CommandSpec` output (regression).
13. Built-in `GOAL_DATA` wins over a process environment variable of the same name.
14. Empty defined variable (`MY_VAR=`) → resolves to `""` (not an error).
15. No recursive expansion: env var value `${OTHER}` is inserted literally.
16. `gofmt` / `go vet` clean; Windows + Linux builds pass; `go test ./...` green; race via CI.

## Explicit Non-Goals

- This ADR does **not** implement Portable Configuration. It defines the architecture.
- It does not change `goal.json` format or seed semantics.
- It does not introduce a new domain entity or schema version.
- It does not add a variable editor UI (MVP has no persisted user variables).
- It does not enable secret-value export.
- It does not add merge/remap import policies.
- It does not modify the Windows Service path contract (ADR 011).
- It does not affect the TLS / Native HTTPS direction (ADR 012, PAUSED).
- It does not change the process-ownership rules (ADR 001/002).
- It does not affect recovery / orphan semantics (ADR 005/008).

## Slice 1 Implementation Evidence (2026-09-14)

Slice 1 (Variable Resolution Foundation) is implemented. Implementation commit pending Owner commit gate.

**Delivered:**

- `internal/domain/varresolve.go` — pure resolver: `ResolveString`, `ResolveStringField`, `ResolveArgs`, `ResolveEnvValues`; `UndefinedVariableError`, `InvalidVariableReferenceError`; `VariableSource` interface; `builtinSource` / `envSource` / `combinedSource`; `NewVariableSource(dataDir)`.
- `internal/domain/command.go` — `LaunchResolver` integrates variable resolution into `Resolve` and `Preview`; `SetDataDir` / `source()` wire the combined source.
- `internal/process/supervisor.go` — `Supervisor.SetDataDir(dir)` delegates to the resolver.
- `cmd/goal/main.go` — `filepath.Abs(dataDir)` passed to `supervisor.SetDataDir` at startup.
- `internal/webui/handlers/models.go` — `Resolve` endpoint maps `UndefinedVariableError` / `InvalidVariableReferenceError` to HTTP 400 (consistent with repository validation-error convention).

**Invariants verified by tests (52 permanent tests):**

- Single-pass, non-recursive substitution.
- No partial substitution (fail-fast).
- `GOAL_DATA` built-in cannot be overridden by process env.
- Defined-empty (`MY_VAR=`) resolves to `""`.
- Environment keys never resolved.
- Backward-compatible: fields without `$` pass through unchanged.
- Restart uses current process environment; same `InstanceID` preserved.
- Pipeline FROM MODEL and CUSTOM args resolve correctly.
- No schema bump, no persisted variable store, no export/import.

**NOT STARTED:** Slice 2 (Export/Import), Slice 3 (UI).

## Slice 2 Owner Contract (agreed 2026-09-14)

The following implementation decisions are settled by explicit Owner agreement. They refine (not contradict) the architecture above.

### SC-1 — Export API

`GET /api/v1/export` with optional query parameters:

- No parameter → export all portable configuration
- `?runtime_id={id}` → export that Runtime
- `?model_id={id}` → export that Model + its Runtime closure
- `?pipeline_id={id}` → export that Pipeline + full Model/Runtime closure

At most one root parameter. Auth: `requireAuth` (read). No POST export.

### SC-2 — Environment Keys Representation

Bundle v1 uses `"environment_keys": ["KEY1", "KEY2"]` (sorted array of strings).

- Environment **values** are never exported.
- `environment_keys` is **redaction/advisory metadata**: it describes keys whose values existed in the source configuration but were intentionally removed.
- **Import MUST NOT** create `{"KEY": ""}` or any `Runtime`/`Model` Environment entry merely because a key appears in `environment_keys`.
- Imported entities have `Environment: {}` (empty map). The user must explicitly reconfigure values after import.
- `environment_keys` is **NOT guaranteed to survive** export → import → export (it is not persisted repository state).

### SC-3 — Dry-Run

`POST /api/v1/import?dry_run=true` executes the full validation pipeline (parse, structural, referential, variable-syntax, collision detection) with **zero mutation and zero durable writes**. Default is `dry_run=false`.

### SC-4 — Import Request Body

`POST /api/v1/import` accepts the **raw** `goal-portable-config` Bundle v1 JSON document as the request body. No wrapper object. A downloaded `goal-portable-config.json` is directly usable as the import request body.

### SC-5 — Export Filename

`Content-Disposition: attachment; filename="goal-portable-config.json"`. No timestamp.

### SC-6 — Import Body Size Limit

Maximum import request body: **10 MiB**. The request MUST be bounded before full JSON decoding and before any repository mutation.

**Oversize response:** HTTP **413 Payload Too Large** with the existing bounded JSON error envelope. Zero repository mutation. Zero durable writes. This is a transport/request-size failure, distinct from bundle semantic validation failures (400), collisions (409), and persistence failures (500).

### SC-7 — Args Warning Contract

The ADR D13 Args warning is satisfied through **explicit documentation only** (API.md, CONFIGURATION.md, USER_GUIDE EN/RU). No custom HTTP response header, no acknowledgement parameter, no heuristic secret scanner, no warning field in the bundle.

Warning text: "Args are exported as-is and may contain user-supplied sensitive values. GoAl does not attempt heuristic secret detection or redaction inside Args."

### SC-8 — Implementation Slicing

Slice 2 is implemented as two commits:

- **2A:** Portable DTOs + Parse/Validate + Export closure + `ImportGraph` repository primitive + application service (export + import) + unit/integration tests. No HTTP surface.
- **2B:** HTTP handlers (`GET /api/v1/export`, `POST /api/v1/import`) + route registration + handler tests + documentation.

Each commit leaves main green. No dangerous half-contract is exposed.

### Slice 2A Implementation Evidence (2026-09-14)

Slice 2A (Portable Bundle + Atomic ImportGraph + Application Service) is implemented.

**Delivered:**

- `internal/application/portable/portable.go` — Bundle v1 DTOs (`Bundle`, `PortableRuntime`, `PortableModel`, `PortablePipeline`, `PortableEntry`), strict `ParseBundle` (rejects malformed JSON, unknown fields, trailing data, wrong format, unsupported version), `validateBundle` (duplicate IDs, referential integrity, variable syntax, domain constraints, runtime name uniqueness), typed errors (`ErrMalformedBundle`, `ErrWrongFormat`, `ErrUnsupportedVersion`, `ErrValidation`).
- `internal/application/portable/export.go` — `ExportBundle(repo, root)` with `ExportRoot` (RuntimeID/ModelID/PipelineID or all), dependency closure (Pipeline→Models→Runtimes, deduped by ID), deterministic `MarshalBundle` (sorted environment_keys, struct field order, no timestamps).
- `internal/application/portable/import.go` — `ImportOrchestrator.Import(bundle, dryRun)`: full validation + collision detection + `ImportGraph` call; dry-run performs zero mutation; `convertBundle` creates entities with `Environment: nil` (environment_keys advisory only).
- `internal/storage/repository.go` — `ImportGraph(runtimes, models, pipelines)`: exclusive lock, collision detection under lock, single `saveLocked()`, rollback on save failure. `ErrImportConflict` type.
- 39 permanent tests (22 unit + 8 export integration + 9 import integration).

**Invariants verified:**
- Deterministic export (byte-identical for unchanged state).
- Environment values never in bundle; keys sorted.
- Args preserved exactly (secret round-trip proven).
- Import creates empty Environment (no `{"KEY": ""}`).
- Collision = REJECT, zero writes.
- Dry-run = zero mutation.
- Active entities preserved, no process launch.
- Self-contained bundle (all refs resolve within bundle).
- Variable grammar validated, undefined variables accepted.
- No schema bump (v8 unchanged).

**NOT STARTED:** Slice 3 (UI).

### Slice 2B Implementation Evidence

**Scope:** HTTP API layer — `GET /api/v1/export`, `POST /api/v1/import`.

**Files:**
- `internal/webui/handlers/portable.go` — PortableHandler with Export and Import methods.
- `internal/webui/handlers/portable_test.go` — 28 permanent HTTP-level tests.
- `internal/webui/handlers/routes.go` — route registration (`requireAuth` for GET, `requireAuthCSRF` for POST).
- `docs/API.md` — public API documentation.
- `docs/USER_GUIDE.md`, `docs/USER_GUIDE_RU.md` — user documentation.
- `docs/CONFIGURATION.md` — variable/portable reconciliation.

**Contract implemented:**
- SC-1: `GET /api/v1/export` with `runtime_id`/`model_id`/`pipeline_id` query params.
- SC-2: `environment_keys` in bundle; never restored as Environment on import.
- SC-3: `POST /api/v1/import?dry_run=true` — same validation, zero mutation.
- SC-4: Raw Bundle v1 as request body.
- SC-5: `Content-Disposition: attachment; filename="goal-portable-config.json"`.
- SC-6: 10 MiB limit via `http.MaxBytesReader`; HTTP 413 for oversize.
- SC-7: Args exported unchanged (documentation warning only).
- SC-8: Delegates to Slice 2A `portable.ExportBundle` / `portable.ImportOrchestrator.Import`.

**Security:**
- Export: `requireAuth` (session check). Import: `requireAuthCSRF` (session + CSRF).
- Environment values never in HTTP response.
- No Bundle contents logged.
- 413 fires before any JSON parsing or repository mutation.

**Tests (28):**
- Export: all, runtime root, model root, pipeline root, unknown 404, two roots 400, empty selector, repeated selector, env-value non-leak, args-as-is.
- Import: valid real, dry-run zero-mutation, malformed JSON, wrong format, unsupported version, missing dependency, collision 409, empty body, trailing garbage, oversize 413, exact boundary, dry_run malformed/duplicate/empty.
- Atomicity: persistence failure → 500, original state intact.
- Auth/CSRF: 401 without session, 403 without CSRF token, 200 with valid CSRF.

### Slice 3 Implementation Evidence (2026-09-15)

Slice 3 (Product UI / Acceptance) is implemented.

**Delivered:**

- `internal/webui/templates/index.html` — "Portable Configuration" section in Settings (export scope selector + entity selector + download button; security warning box; import file picker + validate/confirm buttons + result area).
- `internal/webui/static/app.js` — `portableSelectScope`, `portableUpdateEntitySelector`, `portableExport` (fetch + Blob download), `portableOnFileChange` (FileReader, 10 MiB client pre-check), `portableValidate` (dry-run POST), `portableImport` (confirm dialog + real import POST), `portableResetImport`, `portableShowResult`, `portableShowConflicts`. Window exports for inline `onclick`.
- `internal/webui/static/i18n/en.json` + `ru.json` — 22 `portable.*` keys (EN/RU).
- `internal/webui/static/style.css` — `.portable-subsection`, `.portable-export-row`, `.portable-warning-box`, `.portable-import-actions`, `#portable-import-result` styles.
- `tests/browser/portable.cjs` — 51 permanent browser checks (export all/root, import happy path, collision, invalid, undefined variable, malformed variable, >10 MiB client limit, exact-boundary, Active/AutoStart safety, responsive, i18n, security warning, no 5xx).
- `tests/browser/package.json` — registered as 12th suite.
- `tests/browser/i18n.cjs` — `portable` added to `I18N_KEY_RE`.

**Contract implemented:**
- Export UI: scope selector (All/Runtime/Model/Pipeline) + entity selector (conditional) + download as `goal-portable-config.json` (fetch + Blob, no re-serialization).
- Import UI: file picker → dry-run (mandatory) → conflict/success presentation → explicit confirm dialog → real import. Same raw content used for both dry-run and real import.
- Conflict presentation: 409 `details` array rendered as a list; Import button disabled.
- Security warning: static hint box explaining env-values exclusion and Args risk.
- Active/AutoStart: confirmation dialog text explains preserved state + next-start behavior.
- Environment keys: confirmation explains values not restored.
- Variable references: confirmation explains syntax-only validation.
- 10 MiB: client-side pre-check (file.size > limit → local error, no request sent).
- Double-submit: buttons disabled during in-flight requests.
- EN/RU: all visible strings via `t()` / `data-i18n`.
- Responsive: no overflow at 430px; file input fits; buttons wrap.

**NOT modified (Slice 2A/2B freeze respected):**
- `internal/application/portable/*`
- `internal/storage/repository.go`
- `internal/webui/handlers/portable.go`

**Tests (51 browser checks):**
- Export: all (format/version/entities/env-absent), model root (closure).
- Import: happy path (dry-run → confirm → success → entities exist → no instances started), collision (409, conflicts shown, import disabled), invalid format (error, import disabled), undefined variable (dry-run passes, import succeeds, raw string preserved), malformed variable (error, import disabled).
- >10 MiB: client-side rejection (localized error, no request sent, validate disabled), exact 10 MiB boundary (not rejected by client, reaches server).
- Active/AutoStart safety: model with `active: true` imported, state preserved, zero instances created, instance count unchanged.
- UI: RU/EN labels, scope selector visibility, warning box, responsive 430px, i18n completeness (no missing keys), no unexpected console errors, no 5xx.

# ADR 014: Portable Configuration — Variable Resolution and Secret-Safe Export/Import

**Status:** Accepted — owner contract agreed 2026-09-13; implementation NOT STARTED (Slice 1 requires separate implementation gate)
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
      "environment": {
        "CUDA_VISIBLE_DEVICES": "0"
      }
    }
  ],
  "models": [
    {
      "id": "m-001",
      "name": "My Model",
      "runtime_id": "rt-001",
      "args": ["-m", "${GOAL_DATA}/models/my-model.gguf", "--port", "8080"],
      "environment": {
        "MODEL_EXTRA": ""
      },
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

**Environment:** only **keys** are present (D13). The `environment` object contains key→value pairs, but for the default export the values are omitted or empty. The exact representation of "keys without values" is an implementation detail (e.g., `{"KEY": ""}` or a separate `"environment_keys": [...]` array) — the ADR mandates that **values are not exported** but does not freeze the exact JSON shape of the key-only representation. The implementation must choose one and document it in API.md.

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
- API endpoints: `GET /api/v1/export` (or `POST` with options), `POST /api/v1/import`
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

# ADR 013: Pipeline repeatable model entries + UX/product polish

**Status:** Accepted (design gate 2026-09-06; owner contract agreed 2026-09-06 — D1, D3, D5, D6, D7 + release scope). **Implemented and shipped `3347b2b` (2026-09-12)** — all D10 acceptance items pass (Go: application/storage/handlers/cmd-goal autostart matrix + recovery of two instances of one model + InstanceID uniqueness + lossless environment editing; maintained browser suite `npm test`: 11 suites all green, `pipeline.cjs` 105/105). Gates: `gofmt`/`go vet` clean, `go test ./...` green, Windows + Linux builds pass, race via CI. **Owner-UI refinement (2026-09-09, shipped in the same commit):** linear builder (vertical blocks, not canvas/DAG/DnD), explicit ↑/↓/× reorder, **Models-style inline icon strip** (replacing the initially proposed "…" overflow menu), form-head bounded grid, mobile dense card, instances responsive geometry, AutoStart reconciliation (Active → all entries launch; per-entry field written-but-ignored, UI control removed), D7.1 hardened tokenization.
**Supersedes (partially):** ADR 010 D1.3 (model uniqueness per pipeline), ADR 010 D3 per-model skip rule, ADR 010 D4 interaction matrix (extended)

## Context

ADR 010 (Pipeline MVP, implemented at `9f0ecf5`) shipped a technically working Pipeline: an ordered group of existing Models with per-model all-or-nothing Args override, ordered best-effort group lifecycle, startup autostart, 8-endpoint API, and a Pipelines UI. Owner decision 2026-09-06: **Pipeline UX/Product Polish is a RELEASE BLOCKER for the next release with Pipeline (expected v2.1.0)**. Two problem classes are in scope:

1. **Domain limitation (A):** "a model can enter a pipeline only once" does not match the required scenario — the same Model must be able to enter one Pipeline **multiple times** as independent launch entries (e.g. `Qwen3.8-27B` on `--port 8081` and again on `--port 8082`).
2. **UX/semantic/responsive defects (C–L):** confusing create/edit modal (unnamed toggle next to Args with a misleading autostart tooltip), single-line args field, weak list self-documentation (6 equal icons incl. an ambiguous "logs" icon), inconsistent sidebar icons, mid-word name wrapping on narrow desktop, unlabeled mobile controls, terse empty state.

This is **not** a redesign of GoAl. The existing visual language, dark/light themes, and compactness are preserved.

## Forensic (actual code at `593021c`)

**Where unique ModelID is enforced (exhaustive):**

- Application layer ONLY: `ValidatePipelineEntry` (`internal/application/pipeline_service.go:117-138`) — duplicate check at create **and** update → `400 "duplicate model in pipeline: <id>"`.
- Client mirror: `handlePipelineSubmit` (`internal/webui/static/app.js:718-721`) — `pipelines.error.duplicate_model`.
- Documented contract: ADR 010 D1.3 ("a pipeline cannot contain the same model twice (400 at create/update) — order is the launch order and a duplicate would make start/stop ambiguous"), API.md pipelines table.

**Hidden per-model (single-active-instance) assumptions found:**

| # | Location | Assumption | Effect on duplicates |
|---|----------|-----------|----------------------|
| H1 | `PipelineService.startEntry` (`pipeline_service.go:330-348`) | any active instance of the Model (any owner) → `already-running`, "never a second copy" | the second duplicate entry **never launches** — the primary blocker |
| H2 | `PipelineService.stopEntry` (`pipeline_service.go:379-399`) | stops **all** active instances owned by `(pipeline_id, model_id)` in one call, per entry call; `InstanceID` field overwritten in loop; early return on first stop failure (known P3 debt) | two duplicate entries: first stop call stops **both** instances; per-entry stop contract breaks |
| H3 | `PipelineEntryStart/Stop` DTOs (`pipeline_service.go:35-55`) keyed by `ModelID` only | one result row per model | results ambiguous for duplicate entries |
| H4 | detail endpoint `Get` (`handlers/pipelines.go:141-176`) | per-model live status via `byModel[modelID]`, first active wins | both duplicate entries show the same single instance |
| H5 | `sameModelSequence` (`pipeline_service.go:198-208`) | structural = ModelID **sequence** | works with duplicates, but must move to entry-ID sequence (D8) |
| H6 | `domain.LaunchInstance.PipelineID` (`domain/instance.go:44-46`) | attribution to pipeline only, no entry | instances of duplicate entries are indistinguishable for stop/status |
| H7 | `cmd/goal/main.go:351` model-autostart `hasActiveInstance(repo, modelID)` | skip if the model has ANY active instance | correct to keep (pipeline-first ownership, ADR 010 D4) |
| H8 | UI `statusForModel` / chips (`app.js:540-560`) | per-model state for chips | duplicate chips show identical state (cosmetic until H3/H4 fixed) |

**What is NOT a blocker (proven):**

- **Storage** (`internal/storage/repository.go:842-931`): no uniqueness constraint at the storage layer; `CreatePipeline`/`UpdatePipeline` never inspect models; decode (`load()`, repository.go:220-238) unmarshals `pipelines` raw with **no validation** — a hand-edited v8 file with duplicate model ids already loads.
- **Supervisor** (`internal/process/supervisor.go:230-295`): `Start` has **no per-model gate** — the only concurrency gate is the global `maxConcurrent` semaphore (0 = unlimited; production `NewSupervisorWithContext` sets none). Instance identity `InstanceID = "<modelID>-<UnixNano>"` (`domain/command.go:208`) is unique per launch. Two instances of one model are two independent managed instances end-to-end (start/stop/restart/exit-classification/logs).
- **Recovery** (`supervisor.go:533-600`): per-instance ADR 005 identity re-verification (PID + executable/creation-time anchors); no model-level assumptions — duplicate instances classify independently.
- **Manual API** (`handlers/instances.go:77-106`, `application/instance_service.go:27-45`): `POST /api/v1/instances/start` already permits launching a model with an active instance; the single-instance behavior is UI-side only (Models page hides Start while active).
- **Failed-start persistence**: standard Supervisor terminal `failed` record per launch — per entry, already correct.
- **Logs/history**: per-instance (SSE + history) — per entry, already correct.

**Conclusion:** there is **no Architecture Blocker**. The identity model (per-launch instance ID + pipeline attribution) already supports two independent instances of one model. The change is a **contract change**: replace the ADR 010 D1.3 uniqueness rule and the H1–H6 per-model assumptions with **entry-level identity and per-entry lifecycle semantics**, plus the documented UX polish. That is exactly the kind of lifecycle-semantics/public-API change that per AGENTS.md requires a design gate and owner contract agreement — this ADR is that gate.

## Decisions

### D1 — Entry identity (relaxation: unique *entry*, not unique *model*)

- `PipelineModel` gains an additive field `ID string` (json `id`, omitempty-compatible: empty = legacy, see D6). A Pipeline may contain the **same Model multiple times**; the **entry is unique** — `ValidatePipelineEntry` drops the duplicate-model check and keeps: non-empty name, non-empty model list, every entry has a non-empty known `model_id`.
- Entry ID is generated by the repository on create (`CreatePipeline`, same `idGenerator` as pipeline IDs) and is immutable for the entry's lifetime; update bodies without an entry `id` for a known entry are rejected `400` (prevents silent re-identification).
- **Consequences:** `sameModelSequence` (H5) becomes `sameEntrySequence` — structural change = different **entry-ID sequence** (add/remove/reorder); args/`auto_start`/name/`active` edits remain non-structural (409 rule of ADR 010 D1.5 unchanged, now keyed on entry sequence).
- Model delete integrity (`ListPipelinesReferencingModel`) unchanged — duplicate references report the pipeline once.

### D2 — Args semantics: UNCHANGED, strictly preserved (owner requirement B)

- Empty `PipelineModel.Args` → `Model.Args` used. Non-empty → **all** `PipelineModel.Args` used, `Model.Args` fully ignored. No merge/patch/append/implicit inheritance. Base `Model.Args` are never modified by a pipeline (the pre-substitution effective-copy mechanism of ADR 010 D2 stays byte-identical in persistence).
- Each entry resolves args **independently**: two entries of one model with different overrides launch with different resolved commands (the "Qwen x2" scenario: `--port 8081` / `--port 8082`).
- The override must be a **complete** command (e.g. includes the model path and its own `--port`). The UI makes this explicit (D7): the override editor is always enabled together with the explicit note "model arguments are fully replaced".

### D3 — Launch gate: per-entry idempotency + model-owner rule (supersedes ADR 010 D3 "never a second copy")

An entry `E` of pipeline `P` referencing model `M` **launches a new instance iff**:

1. **Per-entry idempotency:** no active instance of `M` is attributed to `E` (`pipeline_id == P` AND `pipeline_entry_id == E.ID`), and
2. **Model-owner rule:** every active instance of `M` (if any) is owned by pipeline `P` — i.e. an entry may launch when the model's active set is empty **or** already fully owned by the same pipeline. If ANY active instance of `M` is owned by another pipeline or is manual (empty `pipeline_id`), the entry yields `already-running` (no adoption, no second copy across owners).

- Orphan gate (ADR 010 D3) preserved per entry: an `orphan` instance of `M` owned by `P` (or unattributable legacy of `M` in `P`) → `orphan-skipped`; an orphan of another owner → `already-running` (an out-of-GoAl process may hold the model's ports).
- `no-runtime`, `model-missing`, `failed` outcomes unchanged (bounded reasons).
- **Within-pipeline independence (the new capability):** `P = [M(port 8081), M(port 8082)]` — first start launches **two** independent instances (entry 1 sees empty model set; entry 2 sees all-active-owned-by-P). Each carries `pipeline_id = P` and its own `pipeline_entry_id`.
- **Cross-pipeline ownership (ADR 010 D4 preserved):** two `Active` pipelines referencing `M`: the earlier pipeline (repository order) owns the model; the later pipeline's entries yield `already-running`. Manual instances always take priority over pipeline entries.
- **Extended interaction matrix** (supersedes ADR 010 D4 matrix; `n` = number of `AutoStart=true` entries of the owning pipeline):

| model `Active` | pipelines referencing `M` (Active + entry AutoStart) | Result at startup |
|---|---|---|
| false | one pipeline, n entries | **n** pipeline-owned instances (one per entry, distinct `pipeline_entry_id`) |
| false | two pipelines (earlier P1: n1, later P2: n2) | **n1** instances owned by P1; P2 entries → `already-running` |
| true | none | **one** manual instance (empty `pipeline_id`), per today |
| true | one pipeline, n entries | **n** pipeline-owned instances; model-level autostart skips (model has active instances) |
| true | two pipelines | as row 2 (n1 pipeline-owned) + model-level skip |

### D4 — Stop / Restart: per-entry, reverse order, fixed early-return debt

- `stopEntry(E)` stops **exactly the active instances attributed to `E`** (`pipeline_id == P` AND `pipeline_entry_id == E.ID`). **Legacy fallback** (pre-upgrade instances, `pipeline_entry_id == ""`): active instances with `pipeline_id == P` AND `model_id == E.ModelID` and no entry attribution are assigned to the **first entry of that model in list order** (deterministic; no instance is left unassigned); attributed instances always stop with their own entry.
- Stop failure on one instance no longer aborts the remaining stops of that entry and does not block following entries (fixes the known P3 early-return debt); per-instance results are reported in the entry row.
- `Restart` contract unchanged in shape: `stop_results` (reverse order) + `start_results` (forward order, always all entries); D3 launch gate applies to the start phase.
- Per-pipeline in-service mutex unchanged (no double-launch race).

### D5 — API contract (additive; documented in API.md)

- `PipelineModel` JSON: `+ id` (string, generated; empty on legacy loads, backfilled per D6). List/detail responses include entry `id`.
- Create `POST /api/v1/pipelines`: body `models:[{model_id, args?, auto_start?}]` — **duplicate `model_id` now accepted**; `400` only on empty name, empty list, empty/unknown `model_id`. (Client sends no entry ids; server generates.)
- Update `PUT`: full entry list with `id`s; unknown/removed entry ids or reordered sequence = structural → ADR 010 D1.5 rules now on entry-ID sequence; new entries may be added with `id: ""` (server-generated) — structural, 409 with active owned instances.
- `PipelineEntryStart` / `PipelineEntryStop` / detail per-model status: `+ entry_id` (string) and `+ index` (int, position in the pipeline) — `model_id` stays for compatibility. Results are per **entry** in pipeline order; duplicate entries produce distinct rows with distinct `instance_id`s.
- Detail `GET /api/v1/pipelines/{id}`: per-entry live status resolved by `(pipeline_id, pipeline_entry_id)` with the D4 legacy fallback.
- Error codes unchanged (`bad_request` / `not_found` / `conflict`), flat error contract unchanged.

### D6 — Storage / backward compatibility (no migration, proven)

- **No schema version bump** (stays v8). Proof from forensic: the `load()` path for version > 6 is a single `else` branch (`repository.go:220-238`) with no per-version logic; `json.Unmarshal` ignores unknown fields (a v8-era binary reading a file with `pipeline_entry_id`/entry `id`s is unaffected) and missing fields decode to zero values (a new binary reading an old v8 file gets `id == ""` / `pipeline_entry_id == ""` — the legacy paths of D4/D5). Old pipeline JSON files remain byte-compatible and load unchanged.
- **Entry-ID backfill:** on `load()`, entries with `id == ""` get deterministic backfilled ids (`<pipelineID>-e<index+1>`); persisted on the next save (no eager rewrite on read-only load). **Stability (owner condition, 2026-09-06):** backfilled ids are stable across persist/reload/reorder — the id follows the *entry* (an immutable per-entry identifier), never its position: reordering entries changes the list order but not their ids; a reload of a persisted file yields the same ids. Test-mandated (D10 item 11).
- **Instance attribution backfill:** none at load (instances keep their historical `pipeline_id`); the D4 legacy fallback handles stop/status for pre-upgrade instances.
- Relaxation-only: removing a validation that exists only in the application layer (`ValidatePipelineEntry`) — storage and decode never enforced or assumed uniqueness (proven above), so no migration, no version bump, no `.bak`-triggering rewrite of existing data is required.

### D7 — UI contract (create/edit modal, list, mobile, icons, labels)

**Create/edit (linear builder, one UX model for both):** the pipeline is a **vertical sequence of connected blocks** (Scratch-inspired, top-to-bottom; explicitly NOT a canvas/DAG/branching/conditions/loops/drag-and-drop and no heavy frontend dependency). Each block is a visually distinct, labeled unit showing:

1. **Order number** ("1.", "2.", …) — launch order, always visible.
2. **Model select** (existing).
3. **Launch arguments — compact segmented control** `[From model | Custom]` (RU `Из модели | Свои`), replacing the unnamed toggle + raw args input:
   - `From model` (default) — args editor hidden (entry args empty, i.e. use the model's own args);
   - `Custom` — a **textarea** (min 3 rows, existing form styling) with the note "Model arguments are fully replaced."
   - Existing strict semantics (D2) made visible; create and edit render the same control.
4. **Per-entry autostart** — kept, now a **labeled** control ("Model autostart" / RU "Автостарт модели"; never "entry autostart" / "Автостарт записи", and never a bare `⚡` with no label in the chip/list), visually separated from the args mode; the misleading placement/tooltip pairing is removed. The pipeline-level `Active` toggle keeps its own label/hint and stays visually distinct (accent toggle, as today). The two controls must not read as one concept — see D11.
5. **Reorder**: up/down icon buttons (existing `icon-btn` infrastructure, RU/EN tooltips "Move up"/"Move down"); no drag-and-drop. Order is editable (list order = launch order, a mutable part of the contract; structural 409 applies while active).
6. **Remove entry** — accessible button (tooltip "Remove model entry" RU/EN, red style kept) — not a bare glyph.
7. `+ Model` button relabeled `+ Add model` (RU `+ Добавить модель`).
8. Modal name input: `required` attribute removed (T1 consistency — localized app error, no native HTML5 bubbles).

**Args editor (E):** textarea + **quote-aware arg parser** replacing the naive whitespace split in `parseArgs` (`app.js:387`); the parser is shared with the model wizard (`app.js:1136`) — double-quoted tokens keep inner spaces (e.g. `-m "E:\models\my model.gguf"` stays one argument); unquoted tokens split on whitespace as today. Unit-testable pure function; wizard regression covered by the existing maintained suites.

**D7.1 — Tokenization semantics (hardened 2026-09-09, owner acceptance blocker).** The original D7 parser treated every `"` as a bare on/off toggle with **no backslash-escape handling**, so a JSON argument such as `--chat-template-kwargs "{\"reasoning_effort\":\"medium\"}"` was mangled to the single token `{\reasoning_effort\:\medium\}` (quotes stripped, backslashes kept) and the child runtime (llama.cpp) rejected it as invalid JSON. The parser now defines the textarea as a **command-line representation** whose tokens are the exact `[]string` argv passed to `exec.Command` (Go re-escapes each token for the OS, so a token holds the real value, no quoting). Rules:

- tokens are separated by **unquoted** whitespace (space/tab/newline);
- a double-quoted section `"…"` keeps its inner whitespace in one token and the quotes are removed;
- **inside** double quotes a backslash escapes the next character: `\"`→`"` and `\\`→`\`; any other backslash is kept literally (so Windows paths like `E:\models\m.gguf` and JSON survive);
- **outside** quotes a backslash is a literal character (Windows paths);
- an empty quoted section `""` yields an empty token;
- all other characters (including Unicode) pass through verbatim.

This is a general tokenizer — there is **no** special case for `--chat-template-kwargs` or any specific runtime flag. The model wizard and the Pipeline Custom-args editor share the exact same `parseArgs`, so both agree; the Pipeline strict all-or-nothing Args override (a non-empty `PipelineModel.Args` fully **replaces** `Model.Args`, never merges) is unchanged. Regression: `tests/browser/pipeline.cjs` §12b (JSON kwargs, unquoted Windows path, empty `""`, escaped backslash, nested JSON) + §12d end-to-end round-trip proving the exact argv token the child process receives (via a `fake-runtime argv-file` mode that dumps its parsed argv).

**List (desktop, F/G — table kept, no big cards):**

- Columns: **Name | Models | State | Autostart | Primary + overflow** (State before Autostart).
- Name cell: `white-space: nowrap`, `min-width`, `overflow: hidden; text-overflow: ellipsis`, `title` = full name (K: no mid-word wrap; short name = one line; long name = controlled ellipsis + full name on hover).
- Models: chips as today; repeated model rendered **once per entry with an occurrence badge** (`Qwen3.8-27B` `Qwen3.8-27B` with a shared `×2` badge style) — each chip keeps its own state color (per-entry state, D5); chips beyond a display budget collapse to `+N` with a tooltip listing the remaining entries (RU/EN).
- State column: derived **only** from the existing per-entry lifecycle contract (D5 detail status): `Running` / `Starting` / `Stopping` / `Failed` / `Orphan` / `Stopped` aggregated as: any active → the dominant active state; else any failed/orphan → that; else `Stopped`. **No new backend state is introduced** — it is a pure projection of existing per-entry states.
- Primary + secondary actions: a **Models-style inline icon strip** (shipped implementation, 2026-09-09 refinement): stopped → Start + Edit + Delete (3 icons); running → Restart + Stop + Edit + Delete (4 icons). Each icon has a RU/EN `data-tooltip` + `aria-label`; `Delete` is destructive (red). The ambiguous "logs" icon is **removed** (logs remain reachable from the Logs page's instance selector and per-instance log actions).

**Mobile (L):** the existing table→cards contract at 768px is kept (single monotonic breakpoint, no new breakpoints). The compact card gains semantic hierarchy: **name (ellipsis+title) + state badge → model chips → inline icon strip** so controls keep their meaning after the table headers disappear.

**Sidebar (H):** one consistent inline-SVG icon set (existing infrastructure, no dependency): Models = box, **Pipelines = connected nodes / workflow** (moved **directly below Models**), Logs = list lines, History = clock; the advanced group (Runtimes = server, Instances = activity pulse, Settings = gear) gains the same icon style. All `nav-icon`s share one stroke/fill convention.

**History rename (I):** the section content is the terminal-instance launch history (`ListHistory` = exited/failed/stale instances of models) — it **is** the user's launch history. Rename RU `История экземпляров` → **`История запусков`**, EN `Instance History` → **`Launch History`** (nav + page title, RU/EN parity).

**Empty state (J):** one short description line added above the CTA (RU: «Объединяйте несколько запусков моделей в один сценарий и управляйте ими вместе.» / EN: "Group several model launches into one scenario and manage them together."); CTA unchanged; nothing else added.

**Localization/accessibility (N):** every new label/tooltip/hint gets RU+EN parity in the existing i18n architecture (`data-i18n`/`t()`); icon-only controls keep accessible names/tooltips per current convention; no dark/light theme changes beyond existing variables.

### D8 — Ordering

List order = launch order (ADR 010) is retained as a **mutable** contract part: reorder is user-editable (D7 up/down) and is a **structural** change (409 while the pipeline has active owned instances, per ADR 010 D1.5 on the entry-ID sequence). Startup and manual start iterate in list order; stop/restart in reverse — unchanged.

### D9 — Audit

No new audit events. `pipeline.start|stop|restart` keep the bounded-counter detail (counters are already per-entry results, so duplicate entries count naturally); identity fields (`pipeline_id`, bounded reasons) unchanged. ADR 007 surface untouched.

### D10 — Test / acceptance matrix (objective, mapped to existing suites)

Go (extend `internal/application/pipeline_service_test.go`, `internal/webui/handlers/pipelines_test.go`, `internal/storage/pipeline_test.go`):

1. Pipeline with two different models — start/stop/restart regression (existing behavior).
2. Pipeline with one model twice — create/update **accepted** (no 400); entries get distinct ids.
3. Same model twice with different `PipelineModel.Args` — start produces **two** independent instances with **different** resolved Args (per-entry strict semantics, D2); `Model.Args` byte-identical after.
4. Order: start sequence = list order; stop sequence = reverse; reorder (up/down) changes the order and is structural (409 with active owned instances).
5. D3 matrix: per-entry idempotency (second start → `already-running` per entry); model-owner rule (another pipeline's active instance → `already-running`, no adoption); manual active instance → `already-running`; within-pipeline independence (both entries start).
6. Stop: per-entry attribution stops exactly the entry's instances; legacy fallback (pre-upgrade instance, `pipeline_entry_id == ""`) stops via model fallback; stop failure no longer early-returns (per-instance results).
7. Restart contract: reverse stop + always-forward start; stop-failure → entry still started.
8. Recovery: two instances of one model survive restart reclassification independently (ADR 005 per instance).
9. Autostart: extended D3 matrix (one pipeline n entries → n instances; two pipelines → earlier owns, later `already-running`; model-level skip preserved) in `cmd/goal` autostart tests.
10. Failed start of one duplicate entry → terminal `failed` record for that entry only; other entry unaffected.
11. Persistence/reload: v8 file with duplicate entries + entry ids round-trips; legacy v8 file (no ids) loads, backfills on save; old instance file (no `pipeline_entry_id`) loads.
12. API validation: duplicate model id **201/200**; empty name / empty list / empty model_id / unknown model_id → `400`; structural edit with active owned instances → `409`.
13. `parseArgs` quote-aware unit tests (quoted path with spaces, mixed, empty) + wizard regression. **Extended 2026-09-09 (D7.1):** JSON-kwargs escaped quotes (`--chat-template-kwargs "{\"reasoning_effort\":\"medium\"}"` → exact `{"reasoning_effort":"medium"}` token), unquoted Windows-path backslashes, empty `""` token, escaped-backslash `\\`→`\`, nested JSON; plus an end-to-end round-trip (§12d) proving the exact argv token the child process receives.

Browser (extend maintained `tests/browser/pipeline.cjs` + `responsive.cjs`; three viewport classes — wide desktop / narrow desktop ~1024 / mobile, existing breakpoints only, no new ones):

14. RU/EN UI: segmented args control, labeled autostart, tooltips, empty-state text, History rename, `+ Add model`, name ellipsis `title`.
15. Responsive/semantic: name not wrapped mid-word at narrow desktop; chips readable; mobile card labeled autostart + primary action + overflow menu; modal usable at mobile; args textarea usable with a long full-override line; repeated model shows `×2` and per-entry states; no horizontal overflow.
16. The old unnamed per-row toggle and its misleading "Включать в автостарт GoAl" pairing with Args no longer exist (absence assertion).
17. Existing Pipeline behavior non-regression: the current `pipeline.cjs` suite (CRUD via modal, chips, all-or-nothing override, Start/Stop/Restart E2E with real fake-runtime, Active distinct from Start, duplicate validation → now "accepted" case, responsive @768px, EN/RU) passes with only the intentional contract deltas.
18. D11: the pipeline `Active` label/hint and the per-entry `AutoStart` label/hint are both present, use **distinct** wording (RU/EN), and are visually separated (pipeline-level in the form header, per-entry inside its row); no UI text implies the two toggles are the same setting.

## D11 — `Pipeline.Active` vs `PipelineModel.AutoStart`: explicit distinction (owner requirement, 2026-09-06)

The per-entry autostart semantics are **preserved unchanged** (ADR 010 D1/D4: a pipeline starts on GoAl startup only if `Active = true`; within that, only entries with `AutoStart = true` launch; `AutoStart = false` means "no automatic launch" only — manual Start processes **all** entries). This ADR removes **no** autostart semantics and changes **none** of them.

Verified distinction (documented contract):

| Control | Scope | Semantics |
|---|---|---|
| `Pipeline.Active` | the whole pipeline | "Starts this pipeline on GoAl startup." A **persistent setting**, never a manual action; does not affect the manual Start button. |
| `PipelineModel.AutoStart` | one entry | "Include this entry in GoAl autostart." Effective **only** when the pipeline's `Active = true`; `Active = false` → the pipeline never autostarts regardless of entry flags; manual `POST /pipelines/{id}/start` always processes all entries regardless of `AutoStart`. |

UI consequence (part of D7): the two controls carry distinct RU/EN wording, the per-entry control sits inside its entry row (scope visible) and the pipeline control sits in the form header (scope visible), and each hint names what the control is **not** ("Does not affect the Start button." / "Applies only when the pipeline autostart is on."). Browser acceptance: D10 item 18.

### D11 reconciliation (owner-UI acceptance 2026-09-09)

The owner's browser-acceptance pass identified that the per-entry `AutoStart` control was confusing (a second autostart toggle nested inside the builder, visually similar to the pipeline `Active` toggle, and the ⚡ chip in the list added a third signal). The agreed refinement **simplifies** the semantics: an Active pipeline launches **all** entries unconditionally at GoAl startup. The per-entry `PipelineModel.AutoStart` field is now **"written but ignored"**:

- It round-trips in the API/storage for backward compatibility (on create defaults to `false`; on edit preserves the stored value).
- The backend `Autostart` method no longer checks it — `Active = true` → launch every entry.
- The per-entry "Model autostart" UI toggle and the ⚡ chip indicator are **removed** from the builder and the list.
- The single pipeline-level `Active` toggle remains the **only** autostart control.
- No schema migration (v8 unchanged); the field stays in the `PipelineModel` struct and JSON for compatibility.

This supersedes the D11 table row for `PipelineModel.AutoStart` (the "Include this entry in GoAl autostart" semantics) and the D11 statement "per-entry autostart semantics are preserved unchanged". The original ADR 010 D4 interaction matrix is further simplified: `Active = true` → all entries launch; `Active = false` → none launch; the `n` (count of `AutoStart=true` entries) variable in the extended matrix is now always equal to the total entry count.

## Owner decisions (contract — agreed 2026-09-06)

**Status: AGREED.** The owner agreed 2026-09-06 to: (1) the D3 model-owner rule (within-pipeline independence + cross-pipeline/manual owner priority, no adoption) — extends ADR 010 D4, supersedes the "never a second copy" global gate; (2) the D1 entry identity (immutable additive `PipelineModel.id` with legacy backfill; structural = entry-ID sequence); (3) duplicate Model entries within one Pipeline and per-entry lifecycle identity; (4) D5 API additions (`entry_id`/`index`; duplicate model ids accepted); (5) D6 **no schema bump** (v8 stays current), conditional on test-proven backward compatibility and backfilled-entry-ID stability across persist/reload/reload/reorder (D10 items 11/12 — mandated); (6) D7 the proposed Pipeline UX/responsive scope, including the primary single Start/Stop action, removal of the ambiguous "logs" icon, History → `История запусков` / `Launch History`, the sidebar icon set with Pipelines below Models, and quote-aware Args parsing (shared with the model wizard); (7) the D11 `Active` vs `AutoStart` distinction requirement (per-entry autostart semantics preserved unchanged); (8) scope confirmation: Pipeline UX/Product Polish = RELEASE BLOCKER for v2.1.0; no tag/release/version change until the separate release authorization; ADR 012 remains PAUSED and out of scope.

## Affected documentation (on implementation acceptance)

- ADR 010: supersession note on D1.3/D3/D4 (this ADR).
- API.md: pipelines table (create validation, entry ids, result rows).
- USER_GUIDE EN/RU: Pipelines section (repeatable entries, args mode UI, reorder, states).
- DEVELOPMENT.md: browser-suite table rows.
- ROADMAP/BACKLOG: release-blocker item status, follow-up debt (e.g. existing P3 debts fixed here are closed).

# ADR 027: Entity duplication — copy of models, runtimes and pipelines

**Status:** **Accepted** — drafted 2026-11-26 at the design gate of BACKLOG `ENTITY-DUPLICATE-01` (position 2 of the Owner-approved polish order); the Owner **accepted the design on 2026-11-26** — decisions Q-1…Q-4 as recorded in §Open questions for the Owner plus the additional env-values notice requirement (**D-9**). The accepted design is preserved unchanged in full: existing creation forms, existing `POST` APIs, existing validation and storage, new entity IDs, new pipeline entry IDs, preserved existing dependency references, original configuration unchanged, independent mutable configuration, no history or runtime state copied, no automatic start, cancel = zero persistent writes, portable compatibility preserved, EN/RU localization. Implementation scope: **all three entity types within one subsequent implementation task**; no new API endpoints, storage schemas, server-side cloning framework or dependencies unless an actual blocker is discovered and separately accepted by the Owner. At the time of this acceptance no production code has been written for this ADR.
**Date:** 2026-11-26
**Related:** [ADR 013](013-pipeline-repeat-models-ux-polish.md) (row action icon-strip convention; server-side pipeline entry identity D1), [ADR 018](018-portable-config-export-import.md) (portable export/import identity and write-only environment keys), BACKLOG `ENTITY-DUPLICATE-01` (the 12 Owner requirements)

## Context

The Owner registered **ENTITY-DUPLICATE-01**: copying of models, runtimes and pipelines by reusing the existing creation forms pre-filled from the source entity, editable before save, with new unique IDs, dependency references preserved as references to existing entities, and no copy of history, PIDs or runtime state.

The inspected code shows that every mechanism the feature needs already exists:

- **Forms.** The model creation wizard (`#wizard-form`, `internal/webui/templates/index.html:140-219`), the runtime creation modal (`#create-runtime-modal`, `:536-544`), and the pipeline form (`#pipeline-form`, `:567-584`). All three already have a **create** mode (`openWizard(null)` `internal/webui/static/app.js:1364`, `showCreateRuntimeModal()` `:1736`, `openPipelineModal(null)` `:1102`), and the edit pre-fill logic for each form already copies entity fields into the same form fields (`openWizard(modelId)` `:1383-1407`, `editRuntime(id)` `:1759-1799`, `openPipelineModal(p)` `:1102-1126`).
- **Create APIs.** `POST /api/v1/models` (`routes.go:237`), `POST /api/v1/runtimes` (`routes.go:256`), `POST /api/v1/pipelines` (`routes.go:266`). **The client never sends an entity ID on create** (model body `app.js:1590-1599`, runtime `:1746-1751`, pipeline `:1161`); the server generates IDs: `generateID()` (`internal/storage/repository.go:1532-1535`), applied at `repository.go:573-575` (runtime), `:862-864` (model), `:997-999` (pipeline), and **pipeline entry IDs are generated server-side only** (`repository.go:1012-1016`; the handler strips client entry IDs at `internal/webui/handlers/pipelines.go:217`). So "pre-filled create form → existing POST" yields fresh entity and entry IDs by construction.
- **Validation.** Create paths enforce: model name non-empty (`internal/application/model_service.go:41-46`); runtime name non-empty plus **case-insensitive name uniqueness** → 409 (`internal/application/runtime_service.go:31-40`); pipeline name non-empty, non-empty components list, every component's model must exist → 400 (`internal/application/pipeline_service.go:127-150`). A copy saved through these endpoints is validated exactly like a manual creation.
- **Write-only environment values.** Environment **values are never serialized to clients**: `runtimes.go:46-56` returns sorted `EnvironmentKeys` only; `models.go:19-29` likewise. The edit forms therefore pre-fill **key rows with empty value inputs** and a write-only placeholder (`app.js:1392-1407`, `:1768-1796`). A UI-driven copy **cannot** read source values — and must not try.
- **Autostart gates.** `ModelEntry.Active` and `PipelineEntry.Active` are the only autostart flags; for pipelines "the Active flag (checked by the caller in cmd/goal) is the only autostart gate" (`pipeline_service.go:296`).
- **Portable bundles** copy entities verbatim with IDs and carry environment **keys only** (`internal/application/portable/export.go:168-214`, `portable.go:31,40`). A copy is an ordinary persisted entity, so it is exportable/importable with no bundle-format change; import identity is ID-based, so a same-name copy is a distinct entity (`import_plan.go:179-185`).

No architectural blocker was found: the feature is a presentation-layer convenience over existing, already-validated write paths.

## Decision

Copy = **open the existing creation form pre-filled from the source entity** + **save through the existing create endpoint**. No new endpoint, no new modal, no new storage, no server-side copy operation, no generic cloning framework.

### D-1 — UX flow

One `Copy / Копировать` icon button per entity row, following the ADR 013 icon-strip convention (`iconBtn` with `data-tooltip` + `aria-label`, `app.js:421-423`), added to:

| Surface | Current strip | Copy button position |
|---|---|---|
| Model row (`app.js:587-592`) | Start/ring · Logs · Edit · Delete | between Edit and Delete |
| Runtime row, table + compact (`:1712-1713`, `:1727-1728`) | Edit · Delete | between Edit and Delete |
| Pipeline row (`:849-855`) | Start · Edit · Delete | between Edit and Delete |

Pressing it opens the **creation** form pre-filled (D-3). The user edits anything — name, settings, components — and presses the **existing** Create button. Cancel / close = zero writes (no form auto-submits). The button is shown in every lifecycle state: copy is a pure form operation and never touches the process lifecycle, so the `starting`/`stopping` model-row restriction (ADR 022 D-5) does not apply. A new `copy` glyph is added to `ICONS` (`app.js:425-435`).

### D-2 — Identity: new by construction

The client never sends IDs on create, so the copy automatically receives: a new entity ID (`repository.go:1532-1535`), fresh `created_at`/`updated_at` (`:576-578`, `:865-867`, `:1000-1002`), and **new pipeline entry IDs** for a copied pipeline (entries carry `id: ''` → `repository.go:1012-1016`; ADR 013 D1). No ID-mapping code exists or is needed. Instance history, PIDs and runtime state are keyed by instance/entry identity and are outside the create path — nothing to exclude explicitly.

### D-3 — Per-entity field semantics

| Field | Model | Runtime | Pipeline |
|---|---|---|---|
| `id` | **new** (server) | **new** (server) | **new** (server) |
| `name` | suggested copy name, editable (D-5) | suggested copy name, editable | suggested copy name, editable |
| `runtime_id` | **same value** — copy references the source's existing runtime; user may switch in the wizard step 2 | — | — |
| `args` | copied | — | copied **per component**, with its `From model / Custom` mode |
| `executable`, `working_directory` | — | copied | — |
| `environment` | **keys pre-filled as rows with empty values** (write-only, D-6); user fills what the copy needs | same | — |
| `active` (autostart flag) | **default off** (Q-2) | — | **default off** (Q-2) — the flag is the only autostart gate (`pipeline_service.go:296`) |
| `autostart_delay` | copied, inert while `active` is off | — | — |
| components | — | — | same `model_id` references (existing models), same order, same count; **entry IDs new** (D-2) |
| `created_at` / `updated_at` | **new** | **new** | **new** |

Component order is preserved because the pre-filled builder array is appended in source order; the existing move up/down and drag-and-drop remain available for edits.

### D-4 — What is explicitly NOT introduced

No new API endpoint or request DTO; no new storage schema or migration; no generic "clone" service or framework; no server-side duplicate operation (the server stays the identity validator it is today); no copy of instance history, PIDs, instance or lifecycle state; no auto-start of the copy (D-3); no changes to delete/replace/cascade semantics (`runtime_service.go:101-156`, `model_service.go:79-94`); no portable-bundle format change.

### D-5 — Suggested name and validation

The pre-filled name is a **localized template of the source name**, editable before save:

- EN: `Copy of {name}` · RU: `{name} (копия)` — new i18n key `entity.copy.name` (Q-1);
- if the suggestion is left unchanged and a copy with that name already exists, the **existing** rules apply unchanged: runtime → 409 `a runtime with name "…" already exists` (`runtime_service.go:36-38`) surfaced by the existing toast path; model/pipeline → allowed (no name uniqueness exists today). No auto-numbering is invented (exact-data rule: the stored name is exactly what the user saved).

All other validation is delegated to the existing create endpoints (D-2 of the Context); no client-side validation is added or duplicated.

### D-6 — Sensitive fields: environment values

Environment values are write-only at the API boundary (Context); the copy therefore **pre-fills key rows with empty value inputs** using the existing write-only placeholder, exactly like the edit forms (`app.js:1392-1407`, `:1768-1796`). The user re-enters values the copy needs; secret values are never exposed, transmitted or duplicated by this feature. A pre-filled key row left empty is submitted as `KEY=""` — identical to today's behavior for a manually added empty row (`collectEnvRows`, `app.js:1647-1654`); changing that shared behavior is **out of scope** (Q-3).

### D-7 — i18n

Additive keys in **both** dictionaries (RU/EN parity stays proven by `TestI18nDictionariesParity`, `internal/webui/server_test.go:33`): `models.actions.copy`, `runtimes.actions.copy`, `pipelines.actions.copy`, `entity.copy.name`, `entity.copy.env_notice` (D-9).

### D-9 — Contextual environment-values notice (Owner addition, accepted 2026-11-26)

When the copied configuration **contains environment variable keys**, the creation form shows a **contextual, localized, informational notice** next to the pre-filled env rows, reusing the existing informational UI pattern — **no new notification subsystem, no scope expansion into UI-FEEDBACK-01**:

- RU: «Значения переменных окружения не копируются. При необходимости заполните их заново.»
- EN: `Environment variable values are not copied. Re-enter them if required.`

The notice is shown only when the copy pre-fill actually added at least one env key row (source with no env keys → no notice), lives inside the form (closing the form hides it), and has no persistent state. Key: `entity.copy.env_notice` (D-7).

### D-8 — Minimal implementation scope (estimate from inspected code)

| File | Change | Estimate |
|---|---|---|
| `internal/webui/static/app.js` | `ICONS.copy` glyph; 4 `iconBtn` insertions (model row, runtime table + compact rows, pipeline row); 3 pre-fill openers `copyModel(id)` / `copyRuntime(id)` / `copyPipeline(id)` reusing the edit pre-fill bodies with `wizEditId = null` / empty form `id` / suggested names / `active = false`; the D-9 contextual notice (existing informational pattern, shown only when pre-filled env keys exist) | +100–140 lines |
| `internal/webui/static/i18n/en.json`, `ru.json` | 5 keys each (D-7) | +5 / +5 |
| `internal/webui/templates/index.html` | **none** — all three forms already exist | 0 |
| `internal/webui/static/style.css` | **none** — `iconBtn` ghost/danger styles exist | 0 |
| Go production code | **none** | 0 |
| `tests/browser/entity-copy.cjs` (new maintained suite) | per entity, RU + EN: pre-fill values; save → new unique ID; original byte-unchanged (GET before/after); reference preserved (model→runtime id; pipeline→model ids + order); copy inactive; cancel → zero writes; runtime duplicate suggestion → 409 toast; pipeline entry IDs differ from source; D-9 notice present with the exact localized sentence when the source has env keys and **absent** when it has none | ~45–60 checks |
| Docs (on implementation acceptance) | `docs/DEVELOPMENT.md` suite-table row for the new suite; `docs/USER_GUIDE*.md` if the creation workflow is described there; `docs/ARCHITECTURE.md` ADR index row; **no** `API.md`/`CONFIGURATION.md` change (no API/config contract changes) | small |

Total production delta ≈ **110–150 lines** in 3 files; the feature adds **zero** Go bytes. Effort: one Implementation Gate (code + suite + AGENTS.md checks), one browser-acceptance run, then the normal Commit / Publication gates.

## Acceptance test plan (preview)

- **[A] browser (new `entity-copy.cjs`, RU + EN legs):** the rows above; D-9 notice leg — visible with the exact RU/EN sentence when the copied configuration has env keys, absent when it has none; plus the full-session `window.i18nMissing` set stays empty after each copy flow (i18n hard rule).
- **[A] Go:** `TestI18nDictionariesParity` covers the new keys; no other Go test is affected (no Go production change) — the full `go test ./...` + race (Linux CI) chain stays green as regression.
- **[O] Owner:** visual pass of the three copy flows in RU and EN, including the write-only placeholder visibility and the editability of the pre-filled name.

## Owner questions — decisions recorded (accepted 2026-11-26)

| # | Question | Decision |
|---|---|---|
| Q-1 | Copy-name template | **Accepted** — EN `Copy of {name}` / RU `{name} (копия)` |
| Q-2 | Copied `active` flag | **Accepted** — copies have `active = false` by default, regardless of source configuration; the user can enable it in the form |
| Q-3 | Pre-filled env key row left empty → submitted as `KEY=""` (current manual-create behavior) | **Accepted** — preserve existing manual-creation behavior: `KEY=""`; environment keys are **not** silently discarded; write-only values are **never** copied or exposed |
| Q-4 | Copy button position | **Accepted** — Copy action between Edit and Delete in all three strips (danger stays last) |

**Additional accepted requirement (→ D-9):** when the copied configuration contains environment variable keys, the creation form shows a contextual localized informational notice (RU/EN wording in D-9), reusing existing informational UI patterns — no new notification subsystem, no scope expansion into UI-FEEDBACK-01.

## Risks

| Risk | Mitigation |
|---|---|
| Copy pre-fill drifts from edit pre-fill (two call sites of the same field mapping) | The openers reuse the edit pre-fill bodies rather than re-implementing them; the browser suite asserts the pre-filled values per field |
| Second copy of the same runtime name collides | 409 with the existing message; the suggestion stays editable; auto-numbering explicitly rejected (D-5) |
| A copied pipeline references a model the user later deletes | Existing behavior: launch yields `model-missing` per entry (`pipeline_service.go:444-448`); model deletion while referenced is already blocked with 409 (`models.go:217-224`) |
| Environment values silently absent on the copy | Write-only placeholder rows are visible in the form (same as edit); the [O] leg checks placeholder visibility |
| New suite grows the browser chain time | One fixture cycle; ~45–60 assertions, no timed lifecycle legs |

## Status record

- 2026-11-26 (draft): drafted at the design gate of `ENTITY-DUPLICATE-01`; **Proposed**.
- 2026-11-26 (acceptance): the Owner accepted the design — Q-1…Q-4 as recorded above plus the additional **D-9** notice requirement; status **Accepted**. The accepted design (D-1…D-8) is preserved unchanged.
- Implementation: **all three entity types within one subsequent implementation task**, then the normal `Implementation Gate → Commit Gate → Publication Gate` cycle. No production code, API endpoints, storage schemas, server-side cloning framework or new dependencies exist or are introduced by this acceptance, except for an actual blocker discovered during implementation and separately accepted by the Owner.

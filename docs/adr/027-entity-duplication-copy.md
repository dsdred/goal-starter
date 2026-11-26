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

*(2026-11-27, Commit Gate — D-3 `autostart_delay` wording reconciled with the implemented and Owner-verified behavior: the implemented create path does NOT copy `autostart_delay` — `copyModel` turns autostart off (Q-2) and the wizard's delay field stays hidden on create, so a copy is stored with delay `0` until the user enables autostart and sets one (the edit path pre-fills both, unchanged). The accepted design's "copied, inert while `active` is off" row is superseded by this sentence; this is a documentation correction only — the implemented behavior was the one manually accepted by the Owner (6/6 recheck) and no product byte is altered by it.)*

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
- 2026-11-26 (implementation gate, working tree, awaiting the Commit Gate): D-1…D-9 implemented exactly as accepted — `internal/webui/static/app.js` (`ICONS.copy`; Copy `iconBtn` between Edit and Delete in the model row, runtime table + compact rows, and pipeline row; `copyModel` / `copyRuntime` / `copyPipeline` reusing the existing creation forms with the localized suggested name, `active = false` default, write-only env-key pre-fill, new entity and pipeline entry IDs by construction; the D-9 `hint-text` notice shown only when copied env keys exist) and **+5 keys per dictionary** (`models.actions.copy`, `runtimes.actions.copy`, `pipelines.actions.copy`, `entity.copy.name`, `entity.copy.env_notice`); zero Go, HTML and CSS bytes changed. The D-1 strip insertion shifts the closed icon counts the regression suites pin: `responsive.cjs` (runtime mobile card 2→3 actions; pipeline strip 3→4; model rows five/six-button and the transitional strip four-button) and `pipeline.cjs` (stopped strip 3→4, running strip 4→5, mobile card 3→4) were synchronized to the ADR 027 strip — assertion targets unchanged, only the accepted set widened. Maintained suite `tests/browser/entity-copy.cjs` (**50 checks** — 46 at this gate plus 4 round-trip-import legs added at the Final Owner Acceptance preparation — the exported bundle is imported into a fresh second instance and the copies keep their new IDs, preserved references and active flags, with the environment left empty on the imported copies per ADR 018 D7/D11; PASS locally, EN + RU) registered in the `package.json` chain and tabled in `docs/DEVELOPMENT.md`. No new API endpoint, storage schema, server-side cloning framework or dependency was required — no blocker occurred.
- 2026-11-27 (Manual Owner Acceptance defect #1 — Launch History stale rows; corrected, awaiting repeated acceptance): during manual acceptance of the copy feature the Owner observed that switching the History model filter from a model with launch history to a model without history (e.g. a fresh copy) showed the empty state and the previous model's table rows simultaneously; the stale rows disappeared after the 3000 ms refresh cycle. **Root cause — pre-existing at the published baseline `82e2e12`, NOT introduced by this ADR** (the ADR 027 diff never touches `renderHistory`): `renderHistory()`'s empty branch returned without clearing `#history-body` or resetting the previous render, and the filter selects' `onchange="renderHistory()"` path never calls `applyFit`, so the stale table stayed visible until the periodic refresh re-fit it. **Correction:** the empty branch now clears `#history-body` and removes `visible` from `#history-compact` before returning — the same clear contract as `renderAdvRuntimes` / `renderAdvInstances`; populated renders already rewrite both contents unconditionally, so stale content cannot survive in either direction. Filters, the 3000 ms refresh, history cleanup and EN/RU behavior are unchanged; zero Go, HTML, CSS and i18n bytes moved. **Storage/API isolation confirmed:** the history-free model owns zero records in `GET /api/v1/history` (Owner instance and regression check 16.10) — a copied entity never inherits the original's history. **Regression:** `core.cjs` section 16 (16.1–16.10; 59 → 69 checks) with a deterministic filter setter that verifies the select value stuck; red/green proven on these bytes with the final test code — pre-fix `app.js` fails the section with stale rows coexisting with the empty state (the pure filter-switch signature is 16.3/16.5/16.7/16.8/16.9; 16.2 additionally failed once in the pre-fix run with the same stale-rows-plus-empty-state shape and did not reproduce on the fixed bytes), fixed bytes pass the suite 69/69 twice and the full eighteen-suite chain (1226 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |`, local Windows). Independent bounded review: PASS on all five areas (fix correctness, behavior preservation, test quality, working-tree scope, ADR semantics). D-1…D-9 copy semantics are unchanged; row status still flips at the Commit Gate.
- 2026-11-27 (Manual Owner Acceptance PASS + Commit Gate): the Owner completed Manual Owner Acceptance of the copy feature and the repeated acceptance of the defect #1 correction — **6/6 PASS** on the rebuilt isolated instance (`goal.exe` SHA-256 `feb00c9e…c2c3f1f`, data preserved); the entity duplication implementation is **accepted**. Independent bounded reviewer: **PASS on all five areas**. Local test evidence for these bytes (Owner-accepted local deviations are disclosed, not waived at the Publication Gate): `entity-copy.cjs` **50/50**, `core.cjs` **69/69** (twice), aggregate browser evidence **1226 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |` across the eighteen suites** — that aggregate is a **re-assembled chain, not a single uninterrupted `npm test` run**: during the chain `instance-id.cjs` aborted on a one-off harness-level `FATAL: fetch failed` in its post-check cleanup (all 23 of its UI legs had passed) and the chain was completed from the isolated `instance-id.cjs` re-run (23/23) plus the remaining suites, each exit 0; local `go test ./...` reported one FAIL — `TestGpuProviderNoCache`, the known fakegpu cold-start flake, isolated re-run green — with `go vet` clean; the Owner accepted both deviations exclusively for this Commit Gate, and the exact-SHA CI requirements at the Publication Gate stand unchanged. At this gate the D-3 `autostart_delay` wording was reconciled with the implemented, Owner-verified behavior (see the D-3 dated note), and `BACKLOG.md` marks `ENTITY-DUPLICATE-01` implemented and Owner-accepted. The working-tree scope (implementation + defect correction + suites + docs) is **committed at this gate; publication and CI remain pending** (the exact SHA is reported at the gate and recorded at the publication gate).
- 2026-11-27 (publication gate + closure): the implementation commit **`ca1594c2026a3c430ab73c512ed42c31ac508f6d`** (parent `82e2e12d3ed7762cdf765e187c717edbd5df66c0`, 12 files, +776/−33) was **published** by a normal fast-forward push; exact-SHA CI run **`45890580544` attempt 1 = 7/7 jobs success, no rerun**: Test (Windows) 21 `ok` + 4 no-test-files; Test (Linux, race) step **21 `ok` + 4 no-test-files, 0 `DATA RACE`, 0 `(cached)`**; Browser Acceptance all eighteen suites — **core.cjs 69/69, entity-copy.cjs 50/50, aggregate 1226 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |`**. With the Manual Owner Acceptance (including the 6/6 defect #1 recheck), the independent bounded review (PASS on all five areas), and the portable export/import round-trip verification (`entity-copy.cjs` 6.2–6.5: all-new import plan, preserved IDs/references/flags, environment left empty per ADR 018 D7/D11), the task is **CLOSED** in `BACKLOG.md`. Final ADR 027 semantics as accepted, with the single D-3 `autostart_delay` reconciliation recorded above (delay not copied — the copy is stored with delay 0 until autostart is enabled and set; presentation of the accepted wording, not a behavior change). The disclosed local test deviations (the `instance-id.cjs` one-off harness FATAL with the isolated 23/23 re-run, and the `TestGpuProviderNoCache` cold-start flake with the isolated green re-run) were Owner-accepted for the Commit Gate only and have no bearing on this exact-SHA CI result; no tag or release is implied by this publication.

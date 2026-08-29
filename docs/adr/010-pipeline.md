# ADR 010: Pipeline — Group Launch of Existing Models with Per-Model Args Override

**Status:** Accepted — owner contract agreed 2026-08-29 (D1–D7 incl. the agreed changes: `Pipeline.Active` / per-entry `AutoStart`, reverse-order stop, always-start restart, `failed`-start terminal record, startup-autostart semantics; pipeline-level concurrency cap and pipeline CRUD audit confirmed out of MVP). Implementation NOT STARTED.
**Date:** 2026-08-29
**Related:** ROADMAP P1 "Pipeline MVP" and "Pipeline contract (design note)", ADR 002 (Supervisor and Instance Model), ADR 004 (Config vs Repository ownership), ADR 005 (Recovery — Orphan Detection), ADR 007 (Audit Logging — event taxonomy), ADR 008 (Kill of an Orphan — post-kill lifecycle)

## Context

ROADMAP P1 carries the item "Pipeline MVP (see Pipeline contract below)" plus a design note that fixes the Args semantics in advance ("requires an architecture/ADR before implementation"). Pre-implementation forensic (2026-08-29) establishes the current state:

1. **All launch parameters live in `Model.Args`.** `domain.Model` (`internal/domain/model.go:20-31`, persisted as `ModelEntry`, `internal/domain/model_entry.go:5-17`, schema v7) carries `Args []string`; `Runtime` deliberately has no launch arguments (`internal/domain/runtime.go:9` — "Launch arguments belong to Model.Args, not here").
2. **The launch path is single and already parameterized.** `InstanceService.StartModel` (`internal/application/instance_service.go:27-45`) calls `Supervisor.Start(ctx, model, runtime, nil, nil)` (`internal/process/supervisor.go:230-295`); `LaunchResolver.Resolve` appends `customArgs` after `Model.Args` (`internal/domain/command.go:56-58`) — an **additive** contract, currently nil at every production call site. Environment merges parent → runtime → model → custom (`command.go:60-77`).
3. **An instance is a self-contained denormalized snapshot** (`internal/domain/instance.go:38-65`, resolved fields written at `command.go:219-222`), keyed by `model_id` (`Repository.ListByModelID`, `internal/storage/repository.go:930-941`). There is **no group/pipeline concept anywhere** in code or UI (repo-wide case-insensitive search matches only CI/release "pipeline" in docs).
4. **One model can already have multiple instances.** `POST /api/v1/models/{id}/start` has no active-instance guard and always creates a new instance (`internal/webui/handlers/models.go:158-171`, ID = `modelID-unixnano` at `command.go:211`); model `stop`/`restart` loop over *all* active instances of the model (`models.go:173-207`).
5. **The only existing batch primitive is startup autostart** (`cmd/goal/main.go:151-194`): sequential in repository order after `Supervisor.Recover` (`main.go:99` → `main.go:105`), driven by the per-model `Active` flag, honoring the per-model `AutostartDelay`, no user trigger, no group stop. Model `activate`/`deactivate` only flip that flag (`models.go:223-259`).
6. **Recovery and orphan semantics are settled (ADR 005/008).** Transitional instances reclassify to `stale`/`orphan` on startup (`supervisor.go:533-600`); the UI contract established 2026-08-28 (shipped `cb178ec`) is that a model whose instance is `orphan` gets **no Start action** — a second copy of a process running outside GoAl must not be launched by mistake.
7. **Storage is `goal_repo.json` at schema v7** (`internal/storage/repository.go:447-461`, version branch in `load()` at `repository.go:203-224`), with durable writes (`internal/fsutil/fsutil.go:86-114`) and the P0 rollback-on-save-failure contract (shipped `f8e73b3`) at every mutating CRUD site.
8. **Audit taxonomy is additive by precedent.** ADR 007 defines `goal_audit.jsonl` with fail-open writes; ADR 008 added `instance.kill` and ADR 009 added `config.reload`, both as one new event constant each (`internal/webui/audit/audit.go:49-62`).
9. **The Args override semantics are already an owner contract.** ROADMAP "Pipeline contract (design note)": a Pipeline references existing Models; multiple Models per Pipeline; group lifecycle; per-model optional Args — *if `PipelineModel.Args` is non-empty, use it entirely; if empty, use `Model.Args` entirely; no merge / patch / append*.

Consequence: the MVP is (a) a **new first-class repository entity** (`Pipeline`, schema v8) with an `Active` autostart flag and per-entry `AutoStart`, (b) a **group lifecycle** that reuses the single `Supervisor.Start`/`Stop` path unchanged, (c) an **attribution marker** on instances so group stop never touches instances the pipeline did not start, (d) the **all-or-nothing Args override** applied by pre-substitution, and (e) **deterministic startup autostart** that composes with the existing model-level autostart without duplicate launches.

## Decisions

### D1 — Domain and persistence model (schema v8, additive)

1. New entity `Pipeline`: `ID`, `Name`, `Active bool`, `Models []PipelineModel` (ordered; `PipelineModel{ModelID string, Args []string, AutoStart bool}`), `CreatedAt`, `UpdatedAt`. Persisted as `PipelineEntry` in `goal_repo.json` under a new top-level key `pipelines`.
   Minimal persisted example (one pipeline, two entries):

   ```json
   {
     "schema_version": 8,
     "runtimes": ["…"],
     "models":   ["…"],
     "instances": ["…"],
     "pipelines": [
       {
         "id": "ent_1785400000000000000",
         "name": "Local cluster",
         "active": true,
         "models": [
           { "model_id": "ent_1785300000000000001", "auto_start": true },
           { "model_id": "ent_1785300000000000002",
             "args": ["-m", "C:\\models\\llama-3-8b.gguf", "--port", "8081"],
             "auto_start": false }
         ],
         "created_at": "2026-08-29T12:00:00Z",
         "updated_at": "2026-08-29T12:00:00Z"
       }
     ]
   }
   ```

   (Entry 1: empty `args` → `Model.Args` at launch, autostarted. Entry 2: non-empty `args` → full replacement, not autostarted. `instances` entries launched via this pipeline carry `"pipeline_id": "ent_1785400000000000000"`, all others omit it.)
2. **Schema v7 → v8 is purely additive**: a v7 file simply lacks the `pipelines` key and loads as an empty list; no data transformation of runtimes/models/instances. `saveLocked`/`SaveUnified` write `schema_version: 8` (mirrors the v6→v7 branch at `repository.go:203-224`). `LaunchInstance`/`LaunchInstanceEntry` gain `pipeline_id` (`json:"pipeline_id,omitempty"`) — likewise additive (absent = not pipeline-launched). Absent `active`/`auto_start` values load as `false` (a pipeline never autostarts by accident).
3. **Reference semantics.** A pipeline references models by ID only; a model may belong to **multiple** pipelines. A pipeline **cannot** contain the same model twice (400 at create/update) — order is the launch order and a duplicate would make start/stop ambiguous.
4. **Instance attribution.** Instances launched through pipeline endpoints or pipeline autostart carry `pipeline_id = <pipeline id>`; instances launched manually, via model endpoints, or by model-level autostart have an empty `pipeline_id`. Pipeline stop/restart act **only** on instances with a matching `pipeline_id` in an active state — a model shared between a pipeline and manual starts is never double-stopped.
5. **Integrity rules (explicit, no implicit cascade):**
   - `DELETE /api/v1/models/{id}` while one or more pipelines reference the model → `409 conflict` (consistent with the explicit `cascade-delete` philosophy for runtimes; the user edits/deletes the pipeline first).
   - `PUT /api/v1/pipelines/{id}` while the pipeline has **active owned instances**: `name`, per-model `args`, `Active`, and per-entry `AutoStart` may change (they affect only the *next* start / next startup — a live pipeline is never perturbed); structural changes (add / remove / reorder of the model list) → `409 conflict`.
   - `DELETE /api/v1/pipelines/{id}` while it has active owned instances → `409 conflict`. After a successful delete, terminal instances retain the historical `pipeline_id` (display falls back to the model; no dangling-reference errors anywhere).
6. **Repository CRUD** (`CreatePipeline`/`GetPipeline`/`UpdatePipeline`/`DeletePipeline`/`ListPipelines`) follows the P0 contract: mutex-protected, durable write via `fsutil.WriteFileDurable`, in-memory rollback on save failure.

### D2 — Args resolution: all-or-nothing override (owner contract, unchanged)

- For each pipeline entry at launch time: `PipelineModel.Args` non-empty → **use it entirely** as the model's launch args; empty → **use `Model.Args` entirely**. No merge, no patch, no append.
- **Implementation: pre-substitution.** The pipeline service builds an effective `domain.Model` copy with `Args` replaced and calls the existing `Supervisor.Start(ctx, effectiveModel, runtime, nil, nil)`. The resolver contract is **unchanged** — `customArgs` in `command.go:56-58` stays additive for any future consumer; no new resolver path, no signature changes in `internal/process` or `internal/domain`.
- **The persisted `Model.Args` is never modified** by a pipeline launch (the copy is in-memory only); instance snapshots (D2 below) and repository state both prove it.
- **Environment is not overridden** by a pipeline in first scope: the merge order parent → runtime → model (`command.go:60-77`) applies to the effective model as today. Per-model pipeline env override is future work.
- The resolved command (with the override applied) is denormalized into the instance snapshot exactly as today, so instance history shows **exactly** what ran, including pipeline overrides.
- The existing `POST /api/v1/models/{id}/resolve` preview endpoint keeps model-level semantics (it previews `Model.Args`; pipeline overrides are visible in the per-entry editor and in instance history, not in the model resolve preview).

### D3 — Group lifecycle (ordered, sequential, best-effort, per-model outcomes)

`POST /api/v1/pipelines/{id}/start` processes entries **sequentially in pipeline order** (deterministic; never parallel; respects the per-instance `maxConcurrent` CAS in `Supervisor.Start` at `supervisor.go:247` unchanged). A per-pipeline in-service mutex serializes concurrent lifecycle requests for the same pipeline (start idempotency; no double-launch race). **Best-effort is explicit: an error in one entry neither cancels nor blocks the following entries, and does not roll back already-started entries.** Per-entry outcome vocabulary (bounded strings in the response):

| Outcome | Condition |
|---|---|
| `started` | launched; new instance id returned; `pipeline_id` set |
| `already-running` | the model has **any** active instance (owned or manual) — skipped, never a second copy, and **never adopted** (the existing instance keeps its own `pipeline_id`, i.e. stays empty when manual) |
| `orphan-skipped` | the model's latest instance is `orphan` — consistent with the Models-page contract (shipped `cb178ec`): no Start while an out-of-GoAl process may be running |
| `no-runtime` | the referenced runtime is missing (defensive; integrity normally prevents it) |
| `model-missing` | the referenced model is missing (defensive; D1 integrity prevents it) |
| `failed` | launch failed; bounded reason; the launch went through the standard `Supervisor.Start` path, so **standard Supervisor failure semantics apply: a terminal `failed` instance record is persisted** (`supervisor.go:795-804`, same as a manual model start); **processing continues** with the next entry |

- **Skip outcomes create no instance record**; only `started` and `failed` produce instance records (the latter terminal `failed`, as today for any manual start).
- **Response:** `200 {"pipeline_id":"…","results":[{"model_id":"…","status":"started|…","instance_id":"…","error":"…"}]}` (absent fields omitted; order = pipeline order).

**Stop** (`POST /api/v1/pipelines/{id}/stop`) is **ordered sequential best-effort in REVERSE order of `Pipeline.Models`** (last launched, first stopped). It stops **only** active instances with `pipeline_id = <this pipeline>`; manual instances (empty `pipeline_id`), `orphan` and `stale` instances are **not** touched (Dismiss/Kill remain the ADR 005/008 paths). A stop failure on one entry does not block the remaining entries. Response: `200 {"pipeline_id":"…","results":[{"model_id":"…","instance_id":"…","status":"stopped|failed","error":"…"}]}` (reverse order).

**Restart** = Stop phase then Start phase in one request:

1. **Stop phase:** owned active instances in reverse order (D3 Stop contract; per-entry failures recorded, do not block).
2. **Start phase:** **always** executed for **all** entries in forward order, **regardless of individual stop failures** — the normal D3 start contract applies. An entry whose instance was still active because its stop failed naturally yields `already-running` in `start_results` (no second launch).
3. **Response contract (fixed):** `200 {"pipeline_id":"…","stop_results":[…],"start_results":[…]}` where `stop_results` follow the Stop shape (reverse order) and `start_results` follow the Start shape (forward order).

- **Concurrency:** no pipeline-level cap and no parallel group start in first scope (owner-confirmed: future work); the existing per-instance semaphore is the only gate, exactly as for parallel manual starts.
- **Recovery:** pipeline-owned instances reclassify to `orphan`/`stale` on restart exactly like any instance (ADR 005); pipeline status surfaces the `orphan` per-model state so the user can Dismiss/Kill as today.

### D4 — Startup / autostart semantics (deterministic; no duplicate launch)

Owner question 11 (interaction of model-level `Active` autostart and pipeline autostart) is fixed here. The startup sequence in `cmd/goal` is **fixed and deterministic**:

1. `Supervisor.Recover` (`main.go:99`, unchanged) — reclassify transitional instances per ADR 005.
2. **Pipeline autostart (new):** for each pipeline with `Active = true`, in repository order (`ListPipelines`), entries are processed **in list order, sequentially**; **only entries with `AutoStart = true` are considered** — `AutoStart = false` means "no automatic launch" only (manual `POST /pipelines/{id}/start` still processes **all** entries). Each considered entry uses the **same Args semantics (D2) and the same skip/outcome rules as manual pipeline start** (D3), and launched instances carry the `pipeline_id`. Launch order is immediate and sequential: the per-model `AutostartDelay` (model-level field) is **not** applied on the pipeline path in first scope. A per-entry failure is logged operationally (`slog`) and does not abort the pipeline, the remaining pipelines, or startup.
3. **Model-level autostart (`autostartModels`, `main.go:105`, behavior unchanged):** models with `Active = true` and **no active instance** launch with an empty `pipeline_id`, honoring `AutostartDelay` as today. A model already launched by pipeline autostart has an active instance and is therefore **skipped**.

**No duplicate launch — the ownership rule (fixed):**

- Pipeline autostart runs **before** model-level autostart, so a model covered by both mechanisms gets exactly **one** instance — the **pipeline-owned** one (ownership wins). Model-level autostart then skips it (`already-running`), and the pipeline **never adopts** a pre-existing manual instance (D3 rule).
- Two `Active` pipelines referencing the same model with `AutoStart = true`: the **earlier pipeline (repository order)** launches and owns the instance; the later pipeline's entry yields `already-running`.
- A model with model-level `Active = true` that no `Active` pipeline autostarts launches exactly once, manually (empty `pipeline_id`) — today's behavior preserved.

Interaction matrix (exhaustive, first scope):

| model `Active` | pipeline `Active` + entry `AutoStart` | Result at startup |
|---|---|---|
| false | false / (any) | no launch |
| false | true + true | **one** pipeline-owned instance |
| true | false / (any) | **one** manual instance (empty `pipeline_id`), per today |
| true | true + true | **one** pipeline-owned instance (pipeline runs first); model-level autostart skips |
| true | true + false | **one** manual instance (pipeline entry is not autostarted) |

- **Pipeline autostart is not audited** as `pipeline.*` events: startup has no user/session context (model-level autostart equally emits no `instance.start` events today — audit is handler-layer per ADR 007); outcomes are operational `slog` records.
- `Pipeline.Active` / `AutoStart` are **independent** of the model-level `Active` flag: setting either does not read, write, or imply the other.

### D5 — API surface (all reads `requireAuth`, all mutations `requireAuthCSRF`, per `routes.go` convention)

| Method | Path | Auth | CSRF | Notes |
|---|---|---|---|---|
| `GET` | `/api/v1/pipelines` | yes | no | list: id, name, `active`, ordered model ids/names with `auto_start`, created/updated |
| `GET` | `/api/v1/pipelines/{id}` | yes | no | pipeline (incl. `active`, per-entry `auto_start`) + per-model live status (state, instance id, pid, uptime) |
| `POST` | `/api/v1/pipelines` | yes | yes | create `{name, active?, models:[{model_id, args?, auto_start?}]}` (`active`/`auto_start` default `false`); 400 on empty name, empty model list, duplicate model id, unknown model id |
| `PUT` | `/api/v1/pipelines/{id}` | yes | yes | update per D1.5 (name/args/`active`/`auto_start` always; structural → 409 with active owned instances) |
| `DELETE` | `/api/v1/pipelines/{id}` | yes | yes | 409 on active owned instances; 404 on unknown id |
| `POST` | `/api/v1/pipelines/{id}/start` | yes | yes | D3 contract |
| `POST` | `/api/v1/pipelines/{id}/stop` | yes | yes | D3 contract (reverse order) |
| `POST` | `/api/v1/pipelines/{id}/restart` | yes | yes | D3 contract (`stop_results` + `start_results`) |

Error shapes use the flat `error` / `code` / `details` contract (API.md); codes: `bad_request`, `not_found`, `conflict`.

### D6 — Audit (ADR 007 additive extension)

- Three new events: `pipeline.start`, `pipeline.stop`, `pipeline.restart`. Detail (bounded, secret-safe per ADR 007): `pipeline_id` + **counters only** (`started`, `already_running`, `orphan_skipped`, `failed` for start; `stopped`, `failed` for stop; restart carries the combined set). No model names, no args, no error text beyond a bounded class — counters keep the line bounded regardless of pipeline size.
- Fail-open exactly as ADR 007: an audit-write failure is reported operationally and never changes the lifecycle outcome.
- **Pipeline CRUD (create/update/delete) is not audited in first scope** (owner-confirmed 2026-08-29): this stays in the existing ROADMAP P1 debt item "Audit logging: ADR 007 first-scope extension", whose design note will cover pipeline CRUD events together with model/runtime CRUD, keeping the taxonomy decision in one place. Pipeline **lifecycle** audit (`pipeline.start|stop|restart`) **is** in the MVP.

### D7 — UI (first scope)

- New **Pipelines** nav item (i18n EN/RU) and page following the responsive contract (table → compact cards ≤ 768px, monotonic, per the Responsive UI Contract): name, per-model status chips, Start / Stop / Restart / Logs / Edit / Delete.
- **`Pipeline.Active` toggle** (autostart switch) visible on the pipeline row and in the editor, with its own label/state so it is **clearly distinct from the manual Start action** (manual Start is an action button; `Active` is a persistent setting).
- **Per-entry `AutoStart` checkbox** in the create/edit modal, next to the optional **args override** editor (which states the all-or-nothing semantics: "when non-empty, replaces the model's args entirely"). The editor makes visible that `AutoStart`/`Active` only affect **GoAl startup**, never the manual Start button.
- Per-model status on the pipeline row reuses `modelStatus()` semantics (running/starting/stopping/orphan/stopped) with the owned-instance resolution of the D5 detail endpoint.
- **Logs:** no new endpoint — the existing Logs page and per-instance stream cover it (the user selects the instance); a pipeline-aggregated log view is future work.

## Alternatives considered

- **A. Pipeline as a config-file section (like seeded runtimes/models).** Rejected — live user data belongs in the repository (ADR 004); config is seed-only (ADR 009) and reload never re-applies seed sections.
- **B. Change `LaunchResolver` to a replacement-mode `customArgs`.** Rejected — it would alter the shared resolver contract (`command.go:56-58`) and every existing caller for a feature that can be satisfied by pre-substitution; the instance snapshot would be identical either way.
- **C. Pipeline-owned instance records (a `PipelineInstance` entity, one row per pipeline run).** Rejected — duplicates the entire instance state machine (states, exit classes, recovery, logs, history) that ADR 002/005 already govern; a pipeline is a *view + launcher* over existing instances, not a new process container.
- **D. All-or-nothing start with rollback on first failure.** Rejected (D3) — killing healthy servers because of one bad entry is a worse failure mode than a reported, stoppable partial start.
- **E. Implicit cascade (deleting a model/pipeline kills or rewrites references).** Rejected — contradicts the explicit `cascade-delete` precedent for runtimes and the "no false success" philosophy of ADR 005/008; 409 conflicts are the established, user-visible mechanism.
- **F. Model-level autostart runs before pipeline autostart.** Rejected — a model covered by both mechanisms would get a manual (non-owned) instance and the pipeline entry would degrade to `already-running`, silently disowning the very instance the user asked the pipeline to autostart; pipeline-first makes ownership deterministic (D4).
- **G. Pipeline adopts a pre-existing manual instance (`already-running` → re-tag `pipeline_id`).** Rejected (owner decision) — re-tagging a running instance's attribution mutates ownership semantics of a live process, breaks the "pipeline stop only what it started" invariant mid-flight, and is surprising; the pipeline simply reports `already-running`.

## Consequences

### Positive

- The MVP delivers the ROADMAP contract (group of existing models, group lifecycle, all-or-nothing per-model Args, deterministic group autostart) with **zero changes** to `internal/process`, `internal/domain` resolvers, the launch path, or recovery semantics — new surface is confined to `internal/domain` (entity), `internal/storage` (v8 + CRUD), `internal/application` (PipelineService), `internal/webui/handlers` (routes), `internal/webui/audit` (3 constants), `cmd/goal` (startup autostart wiring), and the UI.
- Instance attribution (`pipeline_id`) makes group stop/restart precise on shared models — the double-stop/double-start hazard of D5's current loop-over-all-instances semantics is closed for pipeline operations.
- The orphan no-start contract (cb178ec) is extended coherently to group starts (`orphan-skipped`).
- Startup behavior is fully deterministic (fixed sequence, ownership matrix, single instance per model per startup) and testable end-to-end.

### Negative / accepted risk

- Schema v8 and five new repository methods widen the storage surface; mitigated by the additive-only migration and the existing rollback/durable-write contract.
- Best-effort start/stop leaves partially started or partially stopped pipelines in a state the user must finish; mitigated by the explicit per-model responses, the reverse-order stop, the always-start restart, and the pipeline row's live per-model status.
- A pipeline referencing a model later renamed is unaffected (ID reference); a model's args edited out-of-band change what an *empty-override* entry launches on next start — expected semantics (the override is empty by user choice), surfaced in instance history.
- Two autostart mechanisms now exist (model-level `Active`, pipeline `Active`+`AutoStart`); mitigated by the D4 ownership matrix and its acceptance tests — they remain independent settings (neither implies the other) by design.

## Acceptance contract (must hold before this is considered done)

1. `goal_repo.json` v7 loads unchanged (all existing runtimes/models/instances intact) and saves as v8 with an empty `pipelines` list; v8 round-trips, including a pipeline persisted with `active` and per-entry `auto_start` values (absent fields load as `false`).
2. Create pipeline with: empty name, empty model list, duplicate model id, unknown model id → `400 bad_request`; valid create → `201` (per the runtime-create / instance-start convention, not the model-create 200), persisted durably (`.bak` present after write), defaults `active=false`, `auto_start=false`.
3. Start of a 2-model pipeline launches both in order; both instances carry `pipeline_id`; instance snapshots show the overridden args where set and `Model.Args` where the override is empty; the persisted `Model.Args` in the repository is **byte-identical** before/after.
4. Args override is all-or-nothing: a non-empty override replaces `Model.Args` entirely (no append/merge observable in the instance snapshot or the launched process).
5. Entry 2 of 4 fails (missing executable) → entries 3 and 4 are still processed; entry 1 stays running; the response lists all four in pipeline order with entry 2 `failed` (bounded reason).
6. A `failed` start entry leaves a **terminal `failed` instance record** (standard Supervisor semantics, `supervisor.go:795-804`); skip outcomes (`already-running`, `orphan-skipped`, `no-runtime`, `model-missing`) create **no** instance record.
7. Start while a model has a manual active instance → `already-running`, no new instance, the manual instance keeps an empty `pipeline_id` (no adoption). Start while the model's latest instance is `orphan` → `orphan-skipped`, no new instance.
8. Stop stops exactly the active owned instances, **in reverse `Pipeline.Models` order**; a manually started second instance of the same model is untouched; `orphan`/`stale` instances are untouched; a stop failure on one entry does not block the remaining entries.
9. Restart response has exactly the shape `{"pipeline_id","stop_results":[…],"start_results":[…]}`; `stop_results` in reverse order; `start_results` in forward order and present for **all** entries even when individual stops failed; an entry still active after a stop failure yields `already-running` in `start_results` (no second launch).
10. `DELETE /api/v1/models/{id}` with a pipeline reference → `409 conflict`, model survives. `DELETE /api/v1/pipelines/{id}` with active owned instances → `409`; after stopping, delete succeeds; terminal instances keep the historical `pipeline_id` and list/history render without error.
11. `PUT` with active owned instances: name/args/`active`/`auto_start` change succeeds (live pipeline unperturbed); add/remove/reorder → `409 conflict`.
12. Unauthenticated pipeline request → `401`; authenticated mutation without CSRF → `403`.
13. Exactly one `pipeline.start` / `pipeline.stop` / `pipeline.restart` audit event per lifecycle request; detail carries `pipeline_id` + bounded counters only; audit-write failure does not change the outcome (fail-open). **Pipeline autostart emits no `pipeline.*` audit events** (startup context).
14. Autostart determinism (D4 matrix, end-to-end across a real server restart):
    - model `Active=true` + pipeline `Active` + entry `AutoStart=true` → exactly **one** instance, pipeline-owned;
    - model `Active=true` only → exactly one manual instance (empty `pipeline_id`);
    - pipeline `Active` + `AutoStart=true` only → exactly one pipeline-owned instance;
    - pipeline `Active=true` with all `AutoStart=false` → no launches from it;
    - pipeline `Active=false` → not processed;
    - two `Active` pipelines sharing a model (both `AutoStart=true`) → exactly one instance, owned by the earlier pipeline (repository order).
15. UI: Pipelines page lists/creates/edits/deletes pipelines (EN/RU), the `Active` toggle and per-entry `AutoStart` are visible, settable, and clearly distinct from the manual Start action; per-model status renders; Start/Stop/Restart work end-to-end in real Chromium; responsive table→cards contract holds at the canonical 768px boundary; a maintained `tests/browser/pipeline.cjs` regression exists and fails on pre-feature code.
16. `gofmt` / `go vet` clean; `go test ./...` and `go test -race ./...` pass; Windows + Linux builds pass; the v7→v8 migration is covered by a test in the maintained browser suite's migration fixture lineage (`migration.cjs` updated).

## Future work (not in first scope; owner-confirmed out of MVP)

- Pipeline-level concurrency cap and parallel (in-order-ready) group start.
- Per-model pipeline environment override (extending D2's all-or-nothing to `Environment`).
- Pipeline-aggregated log view in the UI.
- Pipeline CRUD audit events (together with the model/runtime CRUD audit extension, ROADMAP P1 debt item).
- Per-entry `AutostartDelay` on the pipeline startup path (first scope applies the model-level delay only on the model-level path).
- DAG / dependencies / readiness / resource scheduling (ROADMAP "Later — Advanced Pipeline").

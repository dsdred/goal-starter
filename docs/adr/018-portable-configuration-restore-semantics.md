# ADR 018: Portable Configuration Backup / Restore Semantics — NEW / UPDATE / UNCHANGED / BLOCKED

**Status:** Owner decisions **agreed 2026-09-26**; **Slice 1 is IMPLEMENTED, COMMITTED and PUBLISHED** — `85c7e9ca699f61668f5f441926ed09cda37bd1bd` on `origin/main`, exact-SHA CI run `37185521486`, attempt 1, all 7 required jobs PASS (see §Implementation slicing) — while **Slice 2′ and Slice 5 (final documentation reconciliation) are NOT STARTED** — Slice 2′ is the single publishable vertical the Owner fixed on 2026-10-05 (see §Slice 2′ contract decisions, D-7), which absorbs the implementation scope Slice 4 carried in the 2026-09-26 recommendation, and whose contract the Owner closed further on 2026-10-06 with the second adjudication round D-10…D-17 (see §Slice 2′ contract decisions, second adjudication round) — so the restore semantics this ADR specifies are still not shipped behavior: an UPDATE is now *classified*, but it is neither applied nor made visible on the wire (D-2/D-3), and OWNER-IMPORT-01 remains **OPEN**. One behavior specified here is now shipped: the D5/D6 zero-entry Pipeline block under the new reason `pipeline_entries_empty` (Owner decision D-6), which turns such a plan into a `409` with zero mutation where the baseline wrote the pipeline. Lifecycle status per [DEVELOPMENT.md](../DEVELOPMENT.md) §ADR process stays **Proposed** — the label move to **Accepted** ("implemented or being implemented") is an Owner decision and is not made by a tracking edit. *(Reconciled 2026-10-04 at the post-publication tracking-truth pass: this line then read "implementation **NOT STARTED at HEAD** — as of 2026-10-04 **Slice 1 is implemented locally in the working tree only, NOT committed and NOT published**", and closed with "the decision is made, and no behavior specified here is shipped by the repository yet. This ADR is a design/documentation record only." Each sentence was accurate at its own moment — the first between this slice's authoring and its Commit Gate, the second until its Publication Gate — and is kept here as then-state; only the "no behavior … shipped" claim is now partly superseded, by D-6 as named above. This ADR remains the design record; the code is its partial implementation.)*
**Date:** 2026-09-26
**Supersedes:** the **2026-09-25 SKIP EXISTING amendment** of [ADR 014](014-portable-config-variables.md) as the *current* Portable Configuration conflict / restore policy (its D15 collision policy, D16 planning step, D17 API/preview contract and D18 UI contract). ADR 014's original 2026-09-13 text and its 2026-09-25 amendment section are **kept as history** and are not rewritten here.
**Depends on:** ADR 014 (bundle format v1, secret-safe export policy D13, validation list D15, atomicity D16), ADR 013 D1 (pipeline entry identity), ADR 010 (Pipeline entity), ADR 002 / ADR 016 (instance launch snapshot — this ADR changes no lifecycle semantics)
**Related:** OWNER-IMPORT-01 (Manual Owner Acceptance finding that opened this design), ADR 015 (Readiness) — **FROZEN**, untouched by this ADR; GoAl Variables — explicitly **out of scope**

## Context

Portable Configuration exists for two first-class product purposes, agreed by the Owner on 2026-09-26:

1. **BACKUP / RESTORE** — export the current configuration, later change or delete entities, then import the previously exported file and get the exported configuration back.
2. **MIGRATION** — export from one GoAl installation, import into another, and recreate the Runtime / Model / Pipeline configuration with its relationships preserved.

The policy shipped on 2026-09-25 cannot serve purpose (1). **SKIP EXISTING is a presence test, not a comparison test.** That planner received full repository records (`internal/storage/import_plan.go:38-42`) but read only `ID` and `Name` from them; a same-ID entity whose configuration *differs* was classified `existing`, reported as "skipped", and its stored record was never touched. Re-importing a backup after editing therefore restores nothing, which is why **OWNER-IMPORT-01 was adjudicated FAIL** for the product requirement (not as an implementation defect of the 2026-09-25 slice, whose stated scope it met). *(Swept 2026-10-05: this paragraph describes the 2026-09-25 policy. From Slice 1 (`85c7e9ca699f61668f5f441926ed09cda37bd1bd`) such an entity **classifies `update`**, while the shipped wire still reports it as skipped and the apply still writes nothing for it (D-3) — the restore itself, the counting and the vocabulary are Slice 2′ under D-7/D-9.)*

This ADR defines the replacement semantics: **NEW / UPDATE / UNCHANGED / BLOCKED**, with restore applied only to the fields a portable bundle v1 can actually carry, and only non-destructively.

### Shipped behavior at baseline `485cc6c` (for contrast)

| Aspect | Today | Evidence |
|--------|-------|----------|
| Plan statuses | `new` \| `existing` \| `blocked` | `internal/storage/import_plan.go:13-17` |
| Same ID, different content | `existing` → skipped, record untouched | `internal/storage/import_plan.go:144-218` (only `ID`/`Name` read) |
| Same name, different ID (Runtime) | `blocked` (`runtime_name_taken_other_id`) | `internal/storage/import_plan.go:150-153` |
| Same name, different ID (Model / Pipeline) | `new` — name is not identity | `internal/storage/import_plan.go:164-218`, no name-uniqueness rule for these types anywhere in `internal/application` / `internal/storage` / `internal/webui` |
| Blocked anywhere | zero writes | `internal/storage/repository.go:1111-1123` |
| Nothing to create | no write; durable file untouched | `internal/storage/repository.go:1125-1129` |
| Apply | append creates + one `saveLocked()` + in-memory rollback | `internal/storage/repository.go:1131-1150` |
| Dry run vs real import | advisory plan; real import re-plans under the write lock | `internal/application/portable/import.go:62-99`, `internal/storage/repository.go:1098-1109` |
| Import actionable iff | `new > 0 && blocked == 0` | `internal/application/portable/import.go:121-139` |

*Every row above is the behavior at the 2026-09-25 baseline `485cc6c`, and its `internal/storage/import_plan.go` line ranges are baseline bytes; they are historical contrast, not current-file citations. Slice 1 changed the classification row only (`new` / `update` / `unchanged` / `blocked` replace `existing`); the counting, the create-only apply, the plan/actionability rule and the HTTP body stayed exactly as described here until Slice 2′ (D-3, D-7, D-9).*

## Scope

**In scope:** the import conflict/restore policy and its plan vocabulary; the restorable projection per entity type and its equality normalization; pipeline entry-identity handling under restore; dependency and blocking rules including transitive propagation; the atomicity requirements the new plan must keep; the API plan contract and UI vocabulary; the test and documentation obligations of the implementation slices.

**Non-goals (explicitly out of scope):**
- **GoAl Variables** (path portability) — a separate deferred Owner decision; nothing here may pre-empt it. See D8.
- **Secret / environment-value export** — bundle v1 carries keys only (ADR 014 D13); an `includeSecrets` format remains deferred. See D7 and U1.
- **Mirror / full-replacement restore** (delete local entities absent from the bundle) — a separate future design. See D6 and U2.
- **Lifecycle semantics** — no change to launch, restart, arbitration, instance snapshots or readiness. ADR 015 stays **FROZEN**. See D9.
- Anything under `internal/process/` and the lifecycle contracts.
- **OWNER-UX-02** (the rejected Pipeline AutoStart layout) — a separate, styling-only correction slice, deliberately not part of this ADR.

## Repository evidence used for these decisions

| Fact | Evidence |
|------|----------|
| Runtime `ID` is primary identity; storage `CreateRuntime` checks ID only | `internal/storage/repository.go:564-586` |
| Runtime name uniqueness (case-insensitive) is enforced in the **application service**, not in storage | `internal/application/runtime_service.go:36-38`, `:48-51`, `:72-75`, helper `:88-95` |
| Import already mirrors that service rule in its own planner | `internal/storage/import_plan.go:150-153` |
| Model / Pipeline names carry **no** uniqueness constraint | absence in `internal/application/*`, `internal/storage/*`, `internal/webui/*`; planner treats ID as sole identity `internal/storage/import_plan.go:164-218` |
| Entity IDs are time/sequence based and carry no machine binding | `internal/storage/repository.go:1358-1361` (`ent_<unixnano>_<seq>`) |
| Bundle v1 exports environment **keys** only; import materializes `Environment: nil` | `internal/application/portable/export.go:168-214` (keys at `:180`, `:190`), `internal/application/portable/import.go:156-208` |
| ADR 014 D13 declares env values never exported by default | `docs/adr/014-portable-config-variables.md:277-305` |
| Per-entity update primitives exist and each takes its own lock + its own durable write | `internal/storage/repository.go:600-617` (UpdateRuntime), `:629-658` (PatchRuntime), `:889-906` (UpdateModel), `:920-955` (PatchModel), `:1032-1057` (UpdatePipeline) |
| Interactive pipeline update preserves `CreatedAt` and refuses structural change while the pipeline has active owned instances | `internal/application/pipeline_service.go:195-200` |
| Pipeline entry identity is the entry ID, unique within the pipeline, and a passed non-empty entry ID must already exist in that pipeline | `internal/domain/pipeline.go:6-18` (ADR 013 D1), `internal/application/pipeline_service.go:183-192` |
| Legacy entry IDs are backfilled deterministically as `<pipelineID>-e<index>` | `internal/storage/repository.go:296-307` |
| Instances snapshot `Executable`/`Args`/`Environment` at launch and reference `pipeline_entry_id` | `internal/domain/launch_instance_entry.go:14-18` |
| Referential integrity is guarded in the service layer, not in storage deletes | `internal/application/runtime_service.go:100-116`, `internal/application/model_service.go:79-91`, `internal/storage/repository.go:660-676`, `:957-973`, `:1059-1075`, `ValidateCrossReferences` `:1321-1337` |
| Pipeline validation requires a non-empty entry list with known model IDs and unique entry IDs | `internal/application/pipeline_service.go:127-147` |
| Bundle validation already enforces intra-bundle closure and bundle-wide case-insensitive runtime-name uniqueness | `internal/application/portable/portable.go:197-223`, `:273-281` |

## Decision

Normative Owner decisions D1–D15, agreed 2026-09-26. `MUST` / `MUST NOT` are contract statements for the future implementation, not claims about current code.

### D1 — Identity

Canonical identity remains the **entity ID**. Import `MUST NOT` remap identity, `MUST NOT` merge two entities by name, and `MUST NOT` generate a replacement ID for an imported entity that already exists locally.

### D2 — NEW

An exported entity whose ID does not exist locally, whose constraints and dependencies are satisfiable, is **NEW** and is created **using the exported ID**.

### D3 — UPDATE

An exported entity whose ID exists locally **and** whose restorable projection (D11) differs from the local projection is **UPDATE**: import restores the exported configuration **onto that same identity**.

**Same ID + different content is NOT a conflict.** It is the normal, expected shape of a backup/restore and MUST NOT be reported as an error or as "skipped".

### D4 — UNCHANGED

An exported entity whose ID exists locally and whose restorable projection is equivalent is **UNCHANGED**. No write is required for it. An all-UNCHANGED bundle is a **valid configuration**, is **not an error**, and produces no durable write.

### D5 — BLOCKED

**BLOCKED** is reserved for plans that cannot be applied safely or unambiguously. At minimum:

| Reason | Condition | Reachable over HTTP? |
|--------|-----------|----------------------|
| `runtime_name_taken_other_id` | the exported Runtime name is owned by a **different** local Runtime ID; and, from Slice 2′ (D-8), the case-insensitive final name a NEW Runtime claims is the name a pending **UPDATE** of a different Runtime intends to write — **both** entities BLOCKED; and, from **D-12** (Owner, 2026-10-06), the same holds when **two different NEW** Runtimes claim one case-insensitive final name — **both** BLOCKED, no first-wins | the repository-ownership shape (D-1): **yes**. The **contested intended-name** shapes — D-8's NEW ↔ pending-UPDATE and D-12's NEW ↔ NEW — are **planner/storage-level**: no client can submit them, because a bundle whose two Runtime entries carry one case-insensitive name is rejected with `400` before planning (`internal/application/portable/portable.go:273-281`, and exactly as D-1 already records), and **that boundary stays** (D-12) |
| `dependency_blocked` | a dependency of this entity is itself BLOCKED (transitive) | yes |
| `runtime_ref_unresolved` / `model_ref_unresolved` | the referenced entity exists neither locally nor among this plan's NEW/UPDATE entities | planner-only (shadowed by bundle-closure `400`) — unchanged from the 2026-09-25 contract |
| `pipeline_entries_empty` | **new**: applying/creating the pipeline would leave it with zero model entries, violating `ValidatePipelineEntry` | yes |
| *(invariant class)* | any other applied-graph violation of a repository/domain invariant | — |

An entity that merely **already exists** MUST NOT be BLOCKED. Same ID + changed configuration MUST classify as UPDATE, never as a conflict.

**Current gap this ADR closes.** A bundle pipeline with `"models": []` passes bundle validation today (`internal/application/portable/portable.go:206-223` iterates entries only when present), is classified NEW by the planner (which never checks length), and is appended verbatim by `ImportGraph` — writing a pipeline the interactive API refuses to create (`internal/application/pipeline_service.go:131`). The planned import path MUST reject it as BLOCKED with `pipeline_entries_empty`; bundle format v1 is not changed by this ADR.

### D6 — NON-DESTRUCTIVE RESTORE

Import restores **only** entities represented by the bundle. Local entities absent from the imported bundle remain **untouched**. No deletion-by-absence, no mirror/replace mode in v1. A destructive/full-replacement mode is a separate future design requiring its own ADR (U2).

### D7 — Environment / secrets

Bundle v1 contains **no** environment values (`export.go:180`, `:190`; ADR 014 D13). Therefore:

- **UPDATE MUST preserve the existing local `Environment` map** — restored fields are written field-by-field onto the local record, never by replacing the whole record with the bundle projection.
- Import MUST NOT clear local secret values, MUST NOT invent values, and MUST NOT create/overwrite entries from `environment_keys`.
- `environment_keys` are **informational/advisory metadata**: they are excluded from the equality projection (D11) — a local map that differs from the exported key list is NOT a difference and MUST NOT produce UPDATE.

**Documented limitation:** backup/restore v1 restores portable *configuration*; it is **not** a backup of secret values. This MUST appear in the ADR, `docs/API.md`, `docs/USER_GUIDE*.md` and the UI warning at the time of implementation.

### D8 — Cross-installation paths

`Executable`, `WorkingDirectory` and `Args` are restored exactly as represented by the portable configuration. Import MUST NOT reject a bundle because an absolute path inside it may not exist on the target installation; such portability problems surface through normal launch validation/failure, which is the existing resolve-at-consumption contract (ADR 014 D7). GoAl Variables MAY improve path portability in the future and are **out of scope** here; nothing in this ADR may constrain that deferred decision.

### D9 — Live definitions

Updating a Runtime / Model / Pipeline definition MUST NOT mutate an already-running `LaunchInstance`: the instance keeps its launch snapshot (`internal/domain/launch_instance_entry.go:15-18`) and restored configuration applies to subsequent launches. **Live use alone is NOT a BLOCKED condition.**

The interactive pipeline API keeps its stricter rule — a structural change while the pipeline owns active instances is refused (`internal/application/pipeline_service.go:195-198`). That rule lives in the application service, is **not** a repository/storage or domain invariant (storage updates and deletes are unconditional: `internal/storage/repository.go:1032-1057`, `:1059-1075`), and therefore does not gate import. **This divergence is deliberate:** the import surface is a data operation (ADR 014 D15 trust boundary), and an Owner restoring a backup of a running pipeline must not be blocked by it. The plan `MAY` expose an advisory "currently in use" marker (U4); no lifecycle semantics change, and no restart/stop behavior is added.

### D10 — Pipeline entry identity

Pipeline entry IDs are **operational identity** referenced by instances and by stop/restart aggregation (ADR 013 D1/D4/D5), so restore MUST use the following conservative rule. Given local entries `L` (ordered, IDs unique) and bundle entries `B` (ordered):

1. Index the local entry IDs of **this** pipeline.
2. Walk `B` in bundle order. For each bundle entry `b`:
   - `b.ID` empty → content-new entry; allocate a fresh ID at write time with the existing generator, exactly as `CreatePipeline`/`UpdatePipeline` already do (`internal/storage/repository.go:1006-1010`, `:1042-1046`). Defensive: bundle validation already rejects an empty entry ID with `400` (`internal/application/portable/portable.go:208-210`), so no HTTP import reaches this branch today.
   - `b.ID` matches an **unconsumed** local entry → matched: **keep the existing local entry ID** and restore the bundle's content onto it (`model_id`, `args`, `auto_start`).
   - `b.ID` matches no local entry → new entry, **preserving the exported ID** (D1).
3. Local entries left unconsumed are dropped — that is the structural part of restoring membership.
4. The resulting order is the bundle order (launch order is list order, `internal/domain/pipeline.go:20-22`).
5. The applied pipeline MUST satisfy `ValidatePipelineEntry` (`internal/application/pipeline_service.go:127-147`): non-empty entry list, non-empty and known `model_id`, unique entry IDs.

**Determinism proof.** Entry IDs in one pipeline are unique by construction (`ValidatePipelineEntry`, plus ID-preserving create/update), so an ID match is an identity match — no positional guessing is needed anywhere. A collision between two *different* logical entities is not possible: IDs are either globally unique (`ent_<unixnano>_<seq>`, `internal/storage/repository.go:1358-1361`) or legacy backfill `<pipelineID>-e<index>` (`:296-307`), and an equal legacy ID implies an equal pipeline ID and an equal original position, i.e. the same identity under D1. Intra-bundle duplicate entry IDs are already rejected by bundle validation (`internal/application/portable/portable.go:207-215`). **No unresolved architecture decision remains for D10.**

Entry IDs are consequently **excluded from the equality projection** (D11): identical content under different entry IDs is UNCHANGED, and import never rewrites an existing entry ID merely because the bundle carries a different one.

### D11 — Restorable projections

Projection equality decides UNCHANGED vs UPDATE. Fields outside a projection are never compared and never written by restore.

| Entity | Restorable projection (compared, and written on UPDATE) | Preserved locally (never written by restore) |
|--------|--------------------------------------------------------|----------------------------------------------|
| Runtime | `Name`, `Executable`, `WorkingDirectory` | `Environment` (values), `CreatedAt` |
| Model | `Name`, `RuntimeID`, `Args` (ordered), `Active`, `AutostartDelay` | `Environment` (values), `CreatedAt`; in-memory-only fields (`PipelineID`, `PipelineEntryID`, `internal/domain/model.go:29-37`) |
| Pipeline | `Name`, `Active`, ordered entries `⟨model_id, Args, AutoStart⟩` | `CreatedAt`; entry IDs per D10 |

`UpdatedAt` follows the normal repository update semantics (stamped by the write path, `internal/storage/repository.go:605`, `:894`, `:1037`); timestamps are **excluded** from the equality projection, because every legitimate restore changes them.

**Deterministic normalization (required for equality, and only this):**

| Value | Normalizes to |
|-------|---------------|
| `Args` / entry `Args` `nil` | empty sequence; compared element-wise, order-significant, exact string equality |
| `WorkingDirectory` `""` | absent — equivalent |
| `AutostartDelay` omitted / `0` | equivalent (`json:",omitempty"` in the bundle, `internal/application/portable/portable.go:42`) |
| `Environment`, `environment_keys` | not compared at all (D7) |
| `CreatedAt`, `UpdatedAt` | not compared |
| Names | exact, case-sensitive string equality (display case is restorable; only *uniqueness* of runtime names is case-insensitive) |

No other trimming, case folding, sorting or coercion is permitted — an unspecified difference is a difference, and restore is deliberately conservative about writing.

### D12 — Dependency order

Planning and application remain dependency-aware: **Runtime → Model → Pipeline**, resolving each reference against the repository state **plus** the entities this same plan creates (`internal/storage/import_plan.go:111-124`, `:154-173`, `:197-209`). BLOCKED propagates **transitively** to dependents, and the final applied plan MUST NOT leave an unresolved reference. An UPDATE of a dependency never re-resolves a reference by name and never rewrites a reference to point at a similar-looking entity.

**Bundle closure is retained.** Because `validateBundle` requires every reference to resolve *inside* the bundle (`internal/application/portable/portable.go:197-223`), a NEW entity that depends on an existing one still carries that dependency's entry, which now classifies UNCHANGED or UPDATE rather than "existing/skipped". Documentation and UI MUST NOT imply that a bundle can reference a repository entity it does not carry.

### D13 — Atomicity

- Dry-run validation remains **advisory** (a snapshot taken without the write lock).
- A real import **MUST** recompute the complete authoritative plan — including the NEW / UPDATE / UNCHANGED / BLOCKED classification and the entry-ID matching of D10 — **under the repository write lock**, as `ImportGraph` already does for the presence test (`internal/storage/repository.go:1098-1109`).
- NEW and UPDATE are applied as **one graph operation** with exactly **one** durable write and coherent in-memory rollback on failure. BLOCKED anywhere ⇒ **zero writes**.
- **No per-entity save sequence and no nested repository locking.** The per-entity `Update*` methods acquire the same non-reentrant mutex and each performs its own `saveLocked()` (`internal/storage/repository.go:600-617`, `:889-906`, `:1032-1057`) — calling them from inside the apply step would deadlock and would write partially. The apply step therefore replaces **pointer entries** (never mutating a stored record in place) inside `ImportGraph`, and the existing rollback (`:1131-1150`) extends to cover replaced indices as well as appended slices.
- The `nothing to do` short-circuit (`internal/storage/repository.go:1125-1129`) generalizes from "nothing to create" to "nothing to **create or update**": an all-UNCHANGED plan leaves the durable file byte-identical. From Slice 2′ this predicate is `WillMutate()` (D-9); `WillCreate()` is not retained and has no alias.
- **Pre-write collision invariant (D-8, defense-in-depth).** Before the single durable write, and still under the same exclusive lock, `ImportGraph` MUST verify that the resulting graph holds no two different runtimes with the same case-insensitive name. This is **not** a second classification mechanism — the planner owns classification — but a last guard: on violation apply MUST fail before `saveLocked()`, with **zero** durable mutation and no in-memory residue (the same rollback discipline as a persistence failure). Its surfacing is fixed by **D-15** (Owner, 2026-10-06): the failure surfaces as an import conflict — HTTP `409` whose plan/result body lists the collided Runtimes as BLOCKED with `can_import == false` — and it MUST NOT re-classify any other entity or become a second normal classification path.

### D14 — Import enablement

Import is actionable **iff**:

```
(new + update) > 0  AND  blocked == 0
```

- **All UNCHANGED** → valid configuration, nothing to restore, Import disabled, **not an error**.
- **BLOCKED > 0** → the bundle may be structurally valid while application is blocked; Import disabled, with a human-readable reason per blocked entity.

`can_import` (server) and the client-side enable rule MUST remain the same single predicate enforced in one place each — the plan is not a suggestion the UI may reinterpret.

Implemented as one predicate, **`WillMutate() && !HasBlocked()`** (Owner decision D-9): `WillMutate()` is `NEW + UPDATE > 0` and replaces `WillCreate()` as both the apply predicate and the enablement predicate in Slice 2′, with no compatibility alias.

### D15 — API / UX vocabulary

Primary product vocabulary becomes:

| Class | EN (primary) | RU (primary) |
|-------|--------------|--------------|
| NEW | Will create / New | Будет создано / Новые |
| UPDATE | Will restore / Restore | Будет восстановлено / Восстановление |
| UNCHANGED | Up to date / Unchanged | Без изменений |
| BLOCKED | Blocked | Заблокировано |

RU wording MUST convey the same semantics (restore ≠ create) and MUST keep the existing RU word order conventions of `portable.import.plan.*`. **Ratified 2026-10-06 by D-17:** the per-type dry-run sentence is `Runtime: новые {new}, будут восстановлены {update}, без изменений {unchanged}` with the EN mirror `Runtimes: {new} new, {update} to restore, {unchanged} up to date`, keeping the current `, заблокировано {blocked}` suffix where applicable. For a **pending** dry-run UPDATE the RU verb is the future plural **«будут восстановлены»**; the past tense «восстановлены» describes a completed result and MUST NOT describe a plan.

- Raw `id_exists` / `name_exists` tokens MUST NOT be primary UX (they are already absent from the shipped contract); machine reason codes remain collapsed technical detail only. The counterpart an entity collides with is presented **neutrally** as `(related: <id>)`, not `(existing: <id>)` — **D-14** (Owner, 2026-10-06) — in both the server-generated `details[]` strings and the Web UI technical block, while the JSON field name `related_id` itself is unchanged (an "existing" label is false for a blocked pending UPDATE).
- Validation **MUST** make an impending UPDATE/restore visible **before** the Owner presses Import: the dry-run plan already returns the same body as a real import (`internal/webui/handlers/portable.go:146-182`), so the plan adds an `update` count and per-class totals without changing the envelope shape. (Read with D-10/D-16: the parity being asserted is dry-run ↔ real-import; the retirement of `existing`/`skipped` and of the flat mirrors changes only which fields the envelope carries, not that parity.)
- Status-code discipline is unchanged: `400` file/structure invalidity, `200` plan (advisory dry run) or actual result, `409` **blocked plan** with the same plan body and **zero** durable mutation, `413` oversize, `500` persistence failure. Counts inside a `409` describe the blocked plan and MUST NOT be readable as written results.

## Plan contract (design target)

Per entity type the plan reports `{total, new, update, unchanged, blocked}`; the result reports `created{}`, **`updated{}`**, **`unchanged{}`** (the 2026-09-25 `skipped` field is retired in favor of `unchanged`, since after this change "skipped" would no longer describe what actually happened to a same-ID entity). `blocked[]` keeps `{type, id, name, reason, related_id}` (`internal/application/portable/portable.go:99-109`). The exact wire field names are agreed at the **Slice 2′** implementation gate (Slice 3 is merged into Slice 2′ by D-2, and the vocabulary vertical is whole there per D-7); the classes, the enablement rule (D14/D-9) and the 200/409 distinction (D15) are not open. **Ratified 2026-10-06 by D-16 (U5 closed):** the names are exactly `summary.{runtimes,models,pipelines} = {total, new, update, unchanged, blocked}`, result counts `created` / `updated` / `unchanged`, `blocked[]{type, id, name, reason, related_id}`, plus `dry_run` and `can_import`; `existing`, `skipped` **and the legacy top-level `runtimes` / `models` / `pipelines` mirrors** are retired from the current wire with no compatibility shim (D-10).

The embedded UI is the only documented consumer of this body besides the maintained browser suite, so the vocabulary change lands as one clean replacement inside a single implementation slice rather than as a compatibility shim. **That single slice is Slice 2′** (Owner decision D-7, 2026-10-05): the storage planner/apply change, the application result, the HTTP plan/result body, `internal/webui/static/app.js`, the EN/RU i18n keys, the maintained browser portable acceptance corpus, and the **minimal `docs/API.md` current-wire-contract delta** all move together. `existing` / `skipped` MUST NOT be kept as a compatibility shim, and no publishable intermediate state may carry the new wire while the UI, the browser suite or `docs/API.md` still present `existing`/`skipped` as current.

## Implementation slicing (recommendation — recorded 2026-09-26 as NOT started; as of 2026-10-04 **Slice 1 is IMPLEMENTED, COMMITTED and PUBLISHED at `85c7e9ca699f61668f5f441926ed09cda37bd1bd`**, exact-SHA CI `37185521486` attempt 1 = 7/7 PASS; as of 2026-10-05 **Slice 2′ is the single remaining implementation slice and Slice 5 the final documentation reconciliation** — **Slice 2′ and Slice 5 NOT STARTED**)

| Slice | Content | Production surface |
|-------|---------|--------------------|
| 1 | Statuses + restorable projections + equality normalization + `pipeline_entries_empty` (pure planner; no wire change) — **implemented, COMMITTED and PUBLISHED 2026-10-04: `85c7e9ca699f61668f5f441926ed09cda37bd1bd`, exact-SHA CI `37185521486` attempt 1 = 7/7 PASS; not tagged, not released** | `internal/storage/import_plan.go` (+ equivalence helper in `internal/storage`) |
| 2 | Apply creates **and** updates under the single lock, one write, extended rollback, `WillMutate`, plus the deterministic runtime-name conflict rule D-1 defers (test obligation 8, resolved by D-8) — **merged with Slice 3 into Slice 2′ (Owner decision D-2, 2026-10-04), and absorbed into the full vocabulary vertical by D-7 (2026-10-05)** | `internal/storage/repository.go` |
| 3 | Orchestrator result + plan body (`update`/`unchanged`, `can_import`) — **merged with Slice 2 into Slice 2′ (D-2), whole-vertical scope fixed by D-7** | `internal/application/portable/{portable.go,import.go}`, `internal/webui/handlers/portable.go` |
| 4 | UI plan presentation + EN/RU i18n + maintained browser contracts — **the Owner's 2026-10-05 adjudication (D-7) folded this implementation scope into Slice 2′ so the vocabulary change is one atomic publishable slice; no separate Slice 4 implementation remains. The number is kept for provenance and historical slice numbers are not renumbered.** | `internal/webui/static/app.js`, `internal/webui/static/i18n/{en,ru}.json` — now Slice 2′ surfaces |
| 5 | Final documentation reconciliation (this ADR status, ADR 014 pointer, `docs/USER_GUIDE*.md`, `docs/ARCHITECTURE*.md`, `docs/DEVELOPMENT.md`) — **NOT STARTED. The `docs/API.md` current-wire-contract rows move to Slice 2′ under D-7; Slice 5 keeps the remaining prose reconciliation.** | docs only |

Slice ordering is dependency-bound: **1 → 2′ → 5** (the 2026-09-26 recommendation read 1 → 2 → 3 → 4 → 5; D-2 merged 2 and 3, D-7 absorbed 4, and no historical number is renumbered). Each slice passes the normal governance gates; the `existing`/`skipped` test corpus is **re-targeted**, not deleted.

### Slice 1 contract decisions (Owner-ratified 2026-10-04)

The Owner ratified the following contract decisions for Slice 1, agreed on top of D1–D15. They are labelled **D-1…D-6** to keep them distinct from this ADR's D1–D15.

| ID | Decision |
|----|----------|
| D-1 | Restoring a rename onto a runtime name owned by a **different local Runtime ID** is BLOCKED (`runtime_name_taken_other_id`), independently of bundle order: the conflict keys on repository ownership, and a pending UPDATE never releases the name it vacates, so a name swap inside one bundle blocks **both** runtimes of the swap. The reverse direction is deliberately **not** part of this decision: the name a pending UPDATE only intends to write is not held against a NEW entry, because Slice 1 applies no update and so cannot put two colliding names into the repository. A bundle carrying both a rename to X and a NEW Runtime named X is invalid on its own and is a bundle-validation `400` before planning (`internal/application/portable/portable.go:273-281`), so no plan a client can submit has that shape; deterministic UPDATE/NEW name precedence is a **Slice 2'** obligation (test obligation 8). |
| D-2 | Slices 2 and 3 merge into **Slice 2′** (apply + server plan contract in one slice). Publishing an intermediate state that classifies UPDATE while its wire still reports `skipped` is **forbidden** — it would be able to report a false success. |
| D-3 | Slice 1 changes classification only: UPDATE **and** UNCHANGED are counted transitionally through the existing `skipped`/`existing` fields, `WillCreate()` stays the apply and enablement predicate, and apply, orchestration, the server wire contract and the existing UI plan vocabulary are untouched. Import semantics are therefore unchanged for every entity the shipped plan can already act on. The single new observable outcome is D-6: a zero-entry Pipeline is now BLOCKED instead of being created — that write is exactly the `BACKLOG.md` debt this slice closes, and it is not an UPDATE-related change. **Superseded from Slice 2′ onward by D-7 and D-9 (Owner, 2026-10-05):** the transitional `skipped`/`existing` counting ends, `WillCreate()` stops being the apply and enablement predicate, and the UI vocabulary, browser corpus and `docs/API.md` wire rows move with the change instead of following it. |
| D-4 | The localization of the new blocking reason lands **inside** Slice 1; otherwise `pipeline_entries_empty` would surface through the raw-reason fallback. |
| D-5 | Minimal blocking-reason documentation lands inside Slice 1; the full vocabulary reconciliation stays with the documentation slice. |
| D-6 | A Pipeline with zero model entries: NEW and UPDATE are BLOCKED (a write would leave it empty), UNCHANGED is **not** blocked (nothing is written for it), so a repository that already holds such a pipeline stays importable. |

Slice 1 was authored on 2026-10-04 in the working tree at baseline `c400074d4365a36c42f2d7ea2f0997edbb72159b`, committed as `85c7e9ca699f61668f5f441926ed09cda37bd1bd` and published the same day (`c400074..85c7e9c -> main`; exact-SHA CI run `37185521486`, attempt 1, 7/7 required jobs PASS — the Linux job's literal `CGO_ENABLED=1 go test -race -timeout 20m ./...` step green with 20 `ok` + 4 `[no test files]` and zero `DATA RACE`, and Browser Acceptance at 749 raw `PASS |` / 0 raw `FAIL |`): plan statuses `new` / `update` / `unchanged` / `blocked` replace `existing`, the D11 projections and their normalization live in `internal/storage/import_projection.go`, `pipeline_entries_empty` is the new reason code, EN/RU messages and the UI reason mapping are wired, and the `existing`/`skipped` corpus is re-targeted rather than deleted. A pre-Commit-Gate forensic pass on the same date corrected D-1 to the boundary the Owner ratified: the planner reserves no name for a pending UPDATE, the rename conflict keys on repository ownership only (`internal/storage/import_plan.go` `localRtByName`), which keeps the verdict order-independent in both directions, and the unreachability of a clashing bundle is pinned at the orchestration level (`TestImport_ClashingUpdateAndNewRuntimeNames_RejectedBeforePlanning`). **Committed and PUBLISHED; NOT acceptance-tested** — no manual Owner acceptance has been performed for this slice, publication is not acceptance, `OWNER-IMPORT-01` stays OPEN, and no tag or release exists past `v2.1.0`. *(Reconciled 2026-10-04 at the post-publication tracking-truth pass: this sentence read "NOT committed, NOT published, NOT acceptance-tested — `ROADMAP.md` and `BACKLOG.md` carry the same local/published distinction". That was accurate between the working tree and this slice's Commit Gate, and its first two clauses stayed accurate until the Publication Gate; they are superseded here as current status, while "NOT acceptance-tested" stands.)*

### Slice 2′ contract decisions (Owner-ratified 2026-10-05)

The Owner adjudicated the three ambiguities the Slice 2′ readiness pass surfaced (AMB-1/2/3), on top of D1–D15 and D-1…D-6. They are labelled **D-7…D-9** to keep them in the same dash-labelled family as the Slice 1 ratifications. They are contract decisions recorded before implementation; Slice 2′ itself is **NOT STARTED**.

| ID | Adjudicates | Decision |
|----|-------------|----------|
| D-7 | AMB-1 — ownership of the vocabulary change | Slice 2′ is extended to the **complete vocabulary vertical** and stays one slice (no new "Slice 2″" number is created): storage classification + apply, application result, HTTP plan/result body, `internal/webui/static/app.js`, EN/RU i18n, the maintained browser portable acceptance corpus, and the **minimal `docs/API.md` delta required so that the published HTTP contract is not knowingly false**. **Reason:** no publishable state may carry the new `update`/`unchanged` wire while the UI, the browser suite or the API documentation still treat `existing`/`skipped` as current. **No compatibility shim** for `existing`/`skipped`: one clean replacement of the live contract. Slice 5 remains the separate final documentation reconciliation, with only the current-wire `docs/API.md` rows pulled forward into Slice 2′. Consequence for the historical table: Slice 4's implementation scope is absorbed by Slice 2′ (provenance kept in the table, numbers not renumbered). |
| D-8 | AMB-2 — obligation 8 UPDATE / NEW runtime-name precedence | When a **NEW** Runtime and a pending **UPDATE** of a *different* Runtime claim the same case-insensitive final runtime name, **BOTH are BLOCKED** — neither class has precedence. The verdict MUST be deterministic and **bundle-order independent**: the classification of every entity MUST be identical for every permutation of the same bundle inputs. The **planner is the authoritative enforcement site for classification**, and a conflict MUST be detected before apply. On such a conflict: both entities BLOCKED, `can_import == false`, and `ImportGraph` performs **zero** writes. As **defense-in-depth** (explicitly *not* a second classification mechanism), `ImportGraph` MUST additionally verify before the durable write that the resulting graph holds no two **different** runtimes with the same case-insensitive name; if that invariant is violated despite the planner, apply MUST fail **before `saveLocked()`** with **no durable mutation** and no in-memory residue. **Self-ownership rule:** a Runtime never conflicts with itself — an UPDATE that keeps its own case-insensitive name is not a conflict merely because its own local record holds that name; a conflict exists only when **two different** final runtimes claim one case-insensitive name. UPDATE↔UPDATE rename swaps stay **BLOCKED** under D-1. |
| D-9 | AMB-3 — `WillCreate()` disposition | `WillCreate()` does **not** survive as a second production predicate. Slice 2′ introduces **`WillMutate()`** (`NEW + UPDATE > 0`), uses it as the repository apply predicate, and expresses D14 enablement as **`WillMutate() && !HasBlocked()`**. After every production and test call site is migrated, `WillCreate()` is **removed**, provided grep confirms no remaining required consumer. **No compatibility alias** `WillCreate() → WillMutate()`. |

**Obligation-8 required cases (Owner-fixed 2026-10-05, minimum set):**

1. `UPDATE A: x → y` + `NEW B: y`, in **both** bundle orders ⇒ **A BLOCKED and B BLOCKED**.
2. The same inputs in the reverse order ⇒ structurally/byte-equivalent classification.
3. `UPDATE A: x → x` + `NEW B: x` ⇒ the two **different** final runtimes collide; both BLOCKED.
4. `UPDATE A` keeps `x` and no other final runtime claims `x` ⇒ self-ownership does **not** block the UPDATE.
5. `UPDATE ↔ UPDATE` rename swap ⇒ remains BLOCKED under D-1.
6. After **any** successful apply ⇒ case-insensitive runtime-name collisions == 0.

**Extended by D-12/D-13 (Owner-fixed 2026-10-06):**

7. `NEW A: y` + `NEW B: y` — two **different** NEW Runtimes claiming one case-insensitive final name ⇒ **A BLOCKED and B BLOCKED**, with **no first-wins and no precedence**, and identical classification in both bundle orders. This shape is reachable only at the planner and repository level (a client-submitted bundle of that shape is the `400` named in the D-12 boundary statement below), which is where it MUST be proven; the `400` boundary stays tested as the boundary it is.
8. An entity **already BLOCKED** for another authoritative reason contributes **no** intended-final-name claim (**D-13**), so blocking one claimant never manufactures a second collision for entities whose verdict does not depend on it — while repository-owned **current** names remain reserved exactly as D-1 states.

**Tracking owed, not part of this ADR-only delta:** `ROADMAP.md` and `BACKLOG.md` still describe the pre-D-7 slicing (Slice 4 as a separate future implementation slice, and `ROADMAP.md` carrying the Slice 1-era "slices 2′ / UI / documentation NOT STARTED" wording). The repository wins, so that tracking wording is superseded by this adjudication and is reconciled by a separate docs-only tracking pass, not by editing the tracking files inside this ADR step.

### Slice 2′ contract decisions, second adjudication round (Owner-ratified 2026-10-06)

The Slice 2′ pre-change forensic pass (2026-10-06, baseline `70c949d546e053a1d09774d7449680ceeec2fc3f`) left eight contract points open rather than inventing policy for them. The Owner adjudicated all eight on 2026-10-06. They are labelled **D-10 … D-17** to continue the dash-labelled family (D-1…D-6 Slice 1, D-7…D-9 Slice 2′ round one). **No historical decision is renumbered and none is withdrawn**; where a row below narrows an earlier statement it names that statement and supersedes it in place. Slice 2′ is still **NOT STARTED** and nothing recorded here is shipped behavior.

| ID | Adjudicates | Decision |
|----|-------------|----------|
| D-10 | legacy flat top-level mirror fields | **RETIRE.** The `runtimes` / `models` / `pipelines` top-level mirrors of the created counts (`internal/webui/handlers/portable.go:172-182`, `:219-221`, documented at `docs/API.md:470`, `:476`) are **not part of the Slice 2′ wire** and are removed, with **no compatibility shim**. `docs/API.md` loses those rows inside Slice 2′ (obligation 9, D-7). The authoritative body after Slice 2′ is exactly: `dry_run`, `can_import`, `summary.{runtimes,models,pipelines} = {total, new, update, unchanged, blocked}`, result counts `created` / `updated` / `unchanged`, and `blocked[]`. |
| D-11 | U4 advisory "currently in use" marker | **OMIT from Slice 2′.** No new marker, field or UI text. Live use alone stays non-blocking (D9). If an advisory marker is ever wanted it is a separate additive design decision with its own contract, not Slice 2′ scope. U4 is closed for this slice by this decision, not deferred again. |
| D-12 | name contest between **two different NEW** Runtimes | Where two different NEW Runtimes claim the same case-insensitive final runtime name, **BOTH are BLOCKED**. There is **no first-wins semantics and no precedence for either** (this supersedes the Slice 1 ordering-dependent planner behavior in which the earlier bundle entry was created and the later one blocked, `internal/storage/import_plan.go:240-249`). Together with D-8 the rule is uniform: **a case-insensitive final runtime name claimed by two different runtimes that would write it blocks every claimant.** Classification MUST be deterministic and **bundle-order independent** — identical for every permutation of the same bundle inputs. This verdict is a **planner/storage-level** contract: a client-submitted bundle of that shape is already rejected with `400` by bundle validation, and **that 400 boundary is not weakened or removed** (see the boundary statements below). |
| D-13 | name claims contributed by an entity already BLOCKED | An entity already BLOCKED for another authoritative reason contributes **no** intended-final-name claim: only entities otherwise eligible to mutate participate in the contest. A name collision is therefore never manufactured from an operation that cannot execute anyway, and blocking one claimant does not cascade a collision onto entities whose verdict does not depend on it. **Repository-owned current names remain reserved exactly as D-1 states**: an UPDATE never releases the name it vacates, and a NEW entity colliding with a locally-owned name is still blocked by that ownership. The exclusion is single-pass and non-circular — repository-ownership blocking depends only on repository state, so claim eligibility is settled before the intended-name contest, not by iterating verdicts. |
| D-14 | reporting the counterpart in a contested set larger than two | For a BLOCKED Runtime whose contested final-name set contains more than two different runtime IDs, `related_id` is the **lexicographically smallest OTHER claimant ID** — deterministic and permutation-independent: neither the classification nor the reported ID may depend on bundle order. The JSON field name `related_id` is **unchanged**; the neutral technical presentation token changes from `(existing: <id>)` to **`(related: <id>)`** in **both** the server-generated `details[]` strings (`internal/webui/handlers/portable.go:246`) and the Web UI collapsed technical block (`internal/webui/static/app.js:2708`). |
| D-15 | surfacing the D13 pre-write collision | If the repository-level pre-write final-runtime-name invariant (D13) detects a collision that escaped the authoritative planner, it surfaces as an **import conflict**: HTTP **`409`**, whose plan/result body lists the collided Runtime entities as **BLOCKED** with **`can_import == false`**, **zero durable writes** and **zero in-memory residue**. This is **exceptional invariant protection only**: it MUST NOT become a second normal classification path, MUST NOT re-classify any other entity, and the **planner remains authoritative** (D-8). Because the body is a blocked `409` plan, D14's single predicate is untouched. |
| D-16 | exact Slice 2′ wire vocabulary (U5) | **Adopted.** Per-type plan summary `{total, new, update, unchanged, blocked}`; result counts `created`, `updated`, `unchanged`; `blocked[]` keeps `{type, id, name, reason, related_id}`; `dry_run` and `can_import` keep their names. **`existing` and `skipped` are retired completely from the portable-import current wire, with no compatibility shim**, and the flat mirrors of D-10 go with them. `docs/API.md` records exactly this body inside Slice 2′. |
| D-17 | EN/RU primary plan vocabulary | **Ratified.** EN: **New**, **Will restore**, **Up to date**, **Blocked**; RU: **Новые**, **Будут восстановлены**, **Без изменений**, **Заблокировано**. Per-type dry-run sentences keep the existing RU entity-label ordering (`Runtime: новые {new}, будут восстановлены {update}, без изменений {unchanged}` + the current `, заблокировано {blocked}` suffix where applicable, EN mirrored). For a **pending** dry-run UPDATE the RU verb is the future plural **«будут восстановлены»**; the past tense describes a completed result only. The UI MUST make the pending overwrite/restore visible **before** Import (D15). |

**Boundary statements these decisions require to be explicit:**

- **NEW↔NEW both-blocked is planner/storage-level (D-12).** Bundle validation still rejects a client-submitted bundle whose two Runtimes share a case-insensitive name with `400` (`internal/application/portable/portable.go:273-281`; pinned by `TestImport_ClashingUpdateAndNewRuntimeNames_RejectedBeforePlanning`, `internal/application/portable/import_test.go:134-167`), and that 400 boundary is **not** weakened or removed. The BOTH-BLOCKED verdict must therefore be proven directly at the planner and repository level — the only level at which D-8's NEW↔UPDATE shape is reachable either.
- **An already-BLOCKED entity manufactures no collision (D-13), and D-1 stands unchanged.**
- **Defense-in-depth does not compete with the planner (D-15).**
- **Retirement is complete (D-10/D-16):** no `existing`, no `skipped`, no flat mirrors, no shim, in code, UI, i18n, browser corpus and `docs/API.md` together.

## Test obligations (for the implementation slices)

1. Planner unit matrix: NEW / UPDATE / UNCHANGED / BLOCKED for each of Runtime / Model / Pipeline; the D11 normalization table row by row; `environment_keys` difference ⇒ UNCHANGED; timestamp difference ⇒ UNCHANGED; runtime rename onto an ID-owned name ⇒ BLOCKED.
2. Purity: planner neither mutates state nor the incoming entries (extends `internal/storage/import_plan_test.go`).
3. Repository: mixed create+update applied in **one** write; BLOCKED anywhere ⇒ zero writes *including* updates; persistence failure ⇒ both appended and replaced entries roll back coherently; all-UNCHANGED ⇒ file bytes unchanged; **and the D-8 defense-in-depth invariant — a collided applied graph fails before `saveLocked()` with zero durable mutation and no in-memory residue.**
4. D10 rule: entry-ID preservation, drop-of-unconsumed-local-entries, new-entry ID preservation, empty-ID allocation, empty pipeline ⇒ `pipeline_entries_empty`.
5. TOCTOU: dry run valid → concurrent CRUD → real import re-plans (extends the existing `importLockHook`-based tests in `internal/application/portable/import_test.go`).
6. HTTP: `200` real result counts are actual results; `409` blocked-plan counts describe a plan with zero mutation; `can_import` matches D14 as the single D-9 predicate `WillMutate() && !HasBlocked()`; status classes stay distinct in the UI (invalid file / blocked plan / server failure). **Lands in Slice 2′ (D-7).** From 2026-10-06 (D-10, D-16): response bodies MUST NOT carry `existing`, `skipped` or the legacy flat `runtimes` / `models` / `pipelines` mirrors; per D-14 the technical detail string presents the counterpart as `(related: <id>)`, never `(existing: <id>)`.
7. Maintained browser suite (`tests/browser/portable.cjs`): export → modify → re-import shows **Will restore**, then Import produces the restored configuration; restore-then-re-import shows **Up to date** with Import disabled and no error; blocked plan presentation with readable reasons and no raw codes; RU/EN parity (`tests/browser/i18n.cjs`). **Lands in Slice 2′ (D-7) — the corpus is re-targeted together with the wire it reads, never left asserting `existing`/`skipped` against the new body.** From 2026-10-06 (D-17): the RU plan line asserts the future tense «будут восстановлены» for a pending dry-run UPDATE and MUST NOT assert the past tense there.
8. **Slice 2′ only** (moved out of Slice 1 by the 2026-10-04 pre-Commit-Gate forensic correction of D-1, and **resolved by Owner decision D-8 on 2026-10-05**): a runtime name claimed by a pending UPDATE and by a NEW entry of the same bundle ⇒ **both entities BLOCKED**, with **no precedence** for either class, **identical classification in every bundle order**, and the applied graph **proven free of case-insensitive runtime-name collisions**. A runtime never conflicts with itself, so an UPDATE that keeps its own name is not blocked by its own local record. **Enforcement sites are fixed:** the planner is authoritative for classification and detects the conflict before apply; `internal/storage` still validates no names on save (`JSONRepository.saveLocked`; uniqueness elsewhere comes from `internal/application/runtime_service.go:90-92` on the CRUD path and `validateBundle` on the import path), so `ImportGraph` carries the pre-write collision check as **defense-in-depth**, not as a second classification mechanism. Cases: the eight-item set in §Slice 2′ contract decisions — the six-item minimum fixed by D-8 on 2026-10-05 plus the two items added by D-12/D-13 on 2026-10-06. Because the NEW↔NEW shape and D-8's NEW↔UPDATE shape are unreachable over HTTP (bundle validation `400`), both MUST be proven **directly at the planner and repository level**, and the `400` boundary remains tested as the boundary it is. Per D-14, `related_id` for a contested set larger than two is the lexicographically smallest OTHER claimant ID — proven deterministic and permutation-independent.
9. **Current HTTP contract documentation stays true at every published commit** (Owner decision D-7): the `docs/API.md` import plan/result rows that describe `existing`/`skipped` and the `can_import` rule are updated **inside Slice 2′**, in the same commit as the wire they document. Slice 5 keeps the remaining documentation reconciliation (this ADR's status, the ADR 014 pointer, `docs/USER_GUIDE*.md`, `docs/ARCHITECTURE*.md`, `docs/DEVELOPMENT.md`). From 2026-10-06 (D-10, D-16) that delta also **removes** the rows for the retired `existing` / `skipped` fields and the retired legacy flat `runtimes` / `models` / `pipelines` mirrors, and records the exact body of D-16.

## Consequences

- Portable Configuration becomes a genuine backup/restore mechanism for the configuration it can express, and keeps its migration behavior (empty target ⇒ all NEW with exported IDs, D1/IDs above).
- Same-ID-different-content becomes a normal, visible outcome instead of a silent skip — which also means the UI must be explicit about *pending overwrite*, and D14/D15 make that its central message.
- Restore is bounded by what bundle v1 carries: **environment/secret values are not restored and local values are preserved** (D7). This is a product limitation, not an implementation shortcut.
- Atomicity gets stricter in substance (the plan now decides writes, not just skips) while reusing the existing single-lock/single-write/rollback discipline (D13).
- The 2026-09-25 SKIP EXISTING amendment stays in the repository as the historical record of a policy that was implemented, shipped and later superseded — its tests and docs are the baseline the implementation slices must re-target.
- No schema change: bundle format stays v1 and repository schema stays v8; nothing in `internal/process/`, the lifecycle contracts or ADR 015 is touched.
- From the 2026-10-05 Owner adjudication (D-7…D-9): the restore semantics and its vocabulary now have **exactly one** remaining implementation slice — Slice 2′ is the whole vertical (planner/apply → result → HTTP body → UI → RU/EN i18n → maintained browser corpus → minimal `docs/API.md` wire rows), with no compatibility shim and no publishable new-wire/old-UI intermediate. The name-collision contract is stricter than the pre-adjudication reading (contested NEW vs pending UPDATE ⇒ **both BLOCKED**, order-independent, plus the `ImportGraph` pre-write invariant as defense-in-depth), and `WillCreate()` disappears rather than coexisting with `WillMutate()`.
- From the 2026-10-06 second adjudication round (D-10…D-17): the wire vocabulary, the collision rule and the reporting details are closed rather than left to the implementation gate. The runtime-name contest is order-independent in **every** shape, including NEW↔NEW (D-12) — which supersedes Slice 1's ordering-dependent first-wins path and is provable only at planner/repository level, since a client-submitted bundle of that shape is a `400`. Entities that cannot execute manufacture no collisions (D-13). The retired vocabulary is retired completely: no `existing`, no `skipped`, no legacy flat mirror fields, no shim (D-10/D-16). The advisory "currently in use" marker is explicitly out of this slice (D-11, closing U4 for it), the escaped-invariant surfacing is a blocked `409` with zero mutation (D-15), and the RU plan line is future-tense for a pending restore (D-17). **Slice 2′ remains NOT STARTED**; all of the above is contract, not shipped behavior.

## Unresolved / deferred decisions

| ID | Item | Status |
|----|------|--------|
| U1 | Backup of **secret values** (bundle format v2 / `includeSecrets`) | Deferred by ADR 014 D13; still open. Until it is designed, backup/restore is explicitly not a credential backup (D7). |
| U2 | Mirror / full-replacement restore (deletion-by-absence) | Separate future design; not permitted implicitly (D6). |
| U3 | Path portability via GoAl Variables | Out of scope (D8); deferred Owner decision. |
| U4 | Whether the plan body surfaces an advisory "in use" marker per entity | **Closed for Slice 2′ by D-11 (Owner, 2026-10-06): omitted** — no marker, field or UI text in this slice; an advisory marker would be a separate additive decision. Live use alone never blocks (D9). *(Former status, kept as history: "Optional; live use alone never blocks (D9). Decision at **Slice 2′** (the former Slice 3 is merged into it, D-2).")* |
| U5 | Exact wire field names of the new plan body | **RESOLVED by D-16 (Owner, 2026-10-06)** — the field names are recorded in §Plan contract and §Slice 2′ contract decisions, second round; `existing`/`skipped` and the legacy flat mirrors are retired (D-10). *(Former status, kept as history: "Agreed at the **Slice 2′** implementation gate (D-7 makes the vocabulary vertical whole there); semantics fixed by D14/D-9/D15.")* |

D10 required a provable matching rule before this ADR could accept it; the rule and its determinism proof are recorded above, so **no architecture decision had to be left open**.

## Acceptance contract (this design gate)

| # | Item | State |
|---|------|-------|
| 1 | ADR 018 exists, states D1–D15 and supersedes only the 2026-09-25 SKIP EXISTING policy point — **completed by this document**; the dash-labelled Owner ratifications D-1…D-6 (2026-10-04), D-7…D-9 (2026-10-05) and D-10…D-17 (2026-10-06) *complete* D1–D15 for the implementation slices and never renumber or withdraw them | this document |
| 2 | ADR 014 history (original contract + 2026-09-25 amendment section) preserved and readable | unchanged text + dated pointer |
| 3 | No production, test, template, asset or i18n byte changed | verified at the gate's VERIFY step |
| 4 | Tracking records this as design accepted / implementation NOT STARTED — **satisfied at the 2026-09-26 design gate; superseded as tracking truth on 2026-10-04, when `ROADMAP.md` records Slice 1 as IMPLEMENTED, COMMITTED and PUBLISHED (`85c7e9ca699f61668f5f441926ed09cda37bd1bd`, exact-SHA CI `37185521486` attempt 1 = 7/7 PASS) with Slices 2′ / UI / documentation NOT STARTED; superseded again as slicing truth on 2026-10-05 by D-7, which folds the UI/i18n/browser implementation scope into Slice 2′ (the quoted ROADMAP wording is then-state; its reconciliation is a separate tracking pass)** | `ROADMAP.md` |
| 5 | OWNER-UX-02 stays a separate correction slice, unimplemented here | `ROADMAP.md` |
| 6 | ADR 015 remains reserved and FROZEN | no ADR 015 document exists in `docs/adr/` and none was created here |
| 7 | OWNER-IMPORT-01 / OWNER-UX-01 / OWNER-UX-02 states and Manual Owner Acceptance BLOCKED recorded without any acceptance closure | `ROADMAP.md` |

# ADR 018: Portable Configuration Backup / Restore Semantics — NEW / UPDATE / UNCHANGED / BLOCKED

**Status:** Owner decisions **agreed 2026-09-26**; implementation **NOT STARTED**. Lifecycle status per [DEVELOPMENT.md](../DEVELOPMENT.md) §ADR process is **Proposed** — the decision is made, no code of this ADR exists yet. This ADR is a design/documentation record only.
**Date:** 2026-09-26
**Supersedes:** the **2026-09-25 SKIP EXISTING amendment** of [ADR 014](014-portable-config-variables.md) as the *current* Portable Configuration conflict / restore policy (its D15 collision policy, D16 planning step, D17 API/preview contract and D18 UI contract). ADR 014's original 2026-09-13 text and its 2026-09-25 amendment section are **kept as history** and are not rewritten here.
**Depends on:** ADR 014 (bundle format v1, secret-safe export policy D13, validation list D15, atomicity D16), ADR 013 D1 (pipeline entry identity), ADR 010 (Pipeline entity), ADR 002 / ADR 016 (instance launch snapshot — this ADR changes no lifecycle semantics)
**Related:** OWNER-IMPORT-01 (Manual Owner Acceptance finding that opened this design), ADR 015 (Readiness) — **FROZEN**, untouched by this ADR; GoAl Variables — explicitly **out of scope**

## Context

Portable Configuration exists for two first-class product purposes, agreed by the Owner on 2026-09-26:

1. **BACKUP / RESTORE** — export the current configuration, later change or delete entities, then import the previously exported file and get the exported configuration back.
2. **MIGRATION** — export from one GoAl installation, import into another, and recreate the Runtime / Model / Pipeline configuration with its relationships preserved.

The policy shipped on 2026-09-25 cannot serve purpose (1). **SKIP EXISTING is a presence test, not a comparison test.** The planner receives full repository records (`internal/storage/import_plan.go:38-42`) but reads only `ID` and `Name` from them (`internal/storage/import_plan.go:144-218`); a same-ID entity whose configuration *differs* is classified `existing`, reported as "skipped", and its stored record is never touched. Re-importing a backup after editing therefore restores nothing, which is why **OWNER-IMPORT-01 was adjudicated FAIL** for the product requirement (not as an implementation defect of the 2026-09-25 slice, whose stated scope it met).

This ADR defines the replacement semantics: **NEW / UPDATE / UNCHANGED / BLOCKED**, with restore applied only to the fields a portable bundle v1 can actually carry, and only non-destructively.

### Current shipped behavior (baseline `485cc6c`, for contrast)

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
| `runtime_name_taken_other_id` | the exported Runtime name is owned by a **different** local Runtime ID | yes |
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
- The `nothing to do` short-circuit (`internal/storage/repository.go:1125-1129`) generalizes from "nothing to create" to "nothing to **create or update**": an all-UNCHANGED plan leaves the durable file byte-identical.

### D14 — Import enablement

Import is actionable **iff**:

```
(new + update) > 0  AND  blocked == 0
```

- **All UNCHANGED** → valid configuration, nothing to restore, Import disabled, **not an error**.
- **BLOCKED > 0** → the bundle may be structurally valid while application is blocked; Import disabled, with a human-readable reason per blocked entity.

`can_import` (server) and the client-side enable rule MUST remain the same single predicate enforced in one place each — the plan is not a suggestion the UI may reinterpret.

### D15 — API / UX vocabulary

Primary product vocabulary becomes:

| Class | EN (primary) | RU (primary) |
|-------|--------------|--------------|
| NEW | Will create / New | Будет создано / Новые |
| UPDATE | Will restore / Restore | Будет восстановлено / Восстановление |
| UNCHANGED | Up to date / Unchanged | Без изменений |
| BLOCKED | Blocked | Заблокировано |

RU wording MUST convey the same semantics (restore ≠ create) and MUST keep the existing RU word order conventions of `portable.import.plan.*`.

- Raw `id_exists` / `name_exists` tokens MUST NOT be primary UX (they are already absent from the shipped contract); machine reason codes remain collapsed technical detail only.
- Validation **MUST** make an impending UPDATE/restore visible **before** the Owner presses Import: the dry-run plan already returns the same body as a real import (`internal/webui/handlers/portable.go:146-182`), so the plan adds an `update` count and per-class totals without changing the envelope shape.
- Status-code discipline is unchanged: `400` file/structure invalidity, `200` plan (advisory dry run) or actual result, `409` **blocked plan** with the same plan body and **zero** durable mutation, `413` oversize, `500` persistence failure. Counts inside a `409` describe the blocked plan and MUST NOT be readable as written results.

## Plan contract (design target)

Per entity type the plan reports `{total, new, update, unchanged, blocked}`; the result reports `created{}`, **`updated{}`**, **`unchanged{}`** (the 2026-09-25 `skipped` field is retired in favor of `unchanged`, since after this change "skipped" would no longer describe what actually happened to a same-ID entity). `blocked[]` keeps `{type, id, name, reason, related_id}` (`internal/application/portable/portable.go:99-109`). The exact wire field names are agreed at the implementation gate for Slice 3; the classes, the enablement rule (D14) and the 200/409 distinction (D15) are not open.

The embedded UI is the only documented consumer of this body besides the maintained browser suite, so the vocabulary change lands as one clean replacement inside a single implementation slice rather than as a compatibility shim.

## Implementation slicing (recommendation — NOT started)

| Slice | Content | Production surface |
|-------|---------|--------------------|
| 1 | Statuses + restorable projections + equality normalization + `pipeline_entries_empty` (pure planner; no wire change) | `internal/storage/import_plan.go` (+ equivalence helper in `internal/storage`) |
| 2 | Apply creates **and** updates under the single lock, one write, extended rollback, `WillMutate` | `internal/storage/repository.go` |
| 3 | Orchestrator result + plan body (`update`/`unchanged`, `can_import`) | `internal/application/portable/{portable.go,import.go}`, `internal/webui/handlers/portable.go` |
| 4 | UI plan presentation + EN/RU i18n + maintained browser contracts | `internal/webui/static/app.js`, `internal/webui/static/i18n/{en,ru}.json` |
| 5 | Documentation reconciliation (this ADR status, ADR 014 pointer, `docs/API.md`, `docs/USER_GUIDE*.md`, `docs/ARCHITECTURE*.md`, `docs/DEVELOPMENT.md`) | docs only |

Slice ordering is dependency-bound (1 → 2 → 3 → 4 → 5) and each slice passes the normal governance gates; the `existing`/`skipped` test corpus is **re-targeted**, not deleted.

## Test obligations (for the implementation slices)

1. Planner unit matrix: NEW / UPDATE / UNCHANGED / BLOCKED for each of Runtime / Model / Pipeline; the D11 normalization table row by row; `environment_keys` difference ⇒ UNCHANGED; timestamp difference ⇒ UNCHANGED; runtime rename onto an ID-owned name ⇒ BLOCKED.
2. Purity: planner neither mutates state nor the incoming entries (extends `internal/storage/import_plan_test.go`).
3. Repository: mixed create+update applied in **one** write; BLOCKED anywhere ⇒ zero writes *including* updates; persistence failure ⇒ both appended and replaced entries roll back coherently; all-UNCHANGED ⇒ file bytes unchanged.
4. D10 rule: entry-ID preservation, drop-of-unconsumed-local-entries, new-entry ID preservation, empty-ID allocation, empty pipeline ⇒ `pipeline_entries_empty`.
5. TOCTOU: dry run valid → concurrent CRUD → real import re-plans (extends the existing `importLockHook`-based tests in `internal/application/portable/import_test.go`).
6. HTTP: `200` real result counts are actual results; `409` blocked-plan counts describe a plan with zero mutation; `can_import` matches D14; status classes stay distinct in the UI (invalid file / blocked plan / server failure).
7. Maintained browser suite (`tests/browser/portable.cjs`): export → modify → re-import shows **Will restore**, then Import produces the restored configuration; restore-then-re-import shows **Up to date** with Import disabled and no error; blocked plan presentation with readable reasons and no raw codes; RU/EN parity (`tests/browser/i18n.cjs`).

## Consequences

- Portable Configuration becomes a genuine backup/restore mechanism for the configuration it can express, and keeps its migration behavior (empty target ⇒ all NEW with exported IDs, D1/IDs above).
- Same-ID-different-content becomes a normal, visible outcome instead of a silent skip — which also means the UI must be explicit about *pending overwrite*, and D14/D15 make that its central message.
- Restore is bounded by what bundle v1 carries: **environment/secret values are not restored and local values are preserved** (D7). This is a product limitation, not an implementation shortcut.
- Atomicity gets stricter in substance (the plan now decides writes, not just skips) while reusing the existing single-lock/single-write/rollback discipline (D13).
- The 2026-09-25 SKIP EXISTING amendment stays in the repository as the historical record of a policy that was implemented, shipped and later superseded — its tests and docs are the baseline the implementation slices must re-target.
- No schema change: bundle format stays v1 and repository schema stays v8; nothing in `internal/process/`, the lifecycle contracts or ADR 015 is touched.

## Unresolved / deferred decisions

| ID | Item | Status |
|----|------|--------|
| U1 | Backup of **secret values** (bundle format v2 / `includeSecrets`) | Deferred by ADR 014 D13; still open. Until it is designed, backup/restore is explicitly not a credential backup (D7). |
| U2 | Mirror / full-replacement restore (deletion-by-absence) | Separate future design; not permitted implicitly (D6). |
| U3 | Path portability via GoAl Variables | Out of scope (D8); deferred Owner decision. |
| U4 | Whether the plan body surfaces an advisory "in use" marker per entity | Optional; live use alone never blocks (D9). Decision at Slice 3. |
| U5 | Exact wire field names of the new plan body | Agreed at Slice 3; semantics fixed by D14/D15. |

D10 required a provable matching rule before this ADR could accept it; the rule and its determinism proof are recorded above, so **no architecture decision had to be left open**.

## Acceptance contract (this design gate)

| # | Item | State |
|---|------|-------|
| 1 | ADR 018 exists, states D1–D15 and supersedes only the 2026-09-25 SKIP EXISTING policy point | this document |
| 2 | ADR 014 history (original contract + 2026-09-25 amendment section) preserved and readable | unchanged text + dated pointer |
| 3 | No production, test, template, asset or i18n byte changed | verified at the gate's VERIFY step |
| 4 | Tracking records this as design accepted / implementation NOT STARTED | `ROADMAP.md` |
| 5 | OWNER-UX-02 stays a separate correction slice, unimplemented here | `ROADMAP.md` |
| 6 | ADR 015 remains reserved and FROZEN | no ADR 015 document exists in `docs/adr/` and none was created here |
| 7 | OWNER-IMPORT-01 / OWNER-UX-01 / OWNER-UX-02 states and Manual Owner Acceptance BLOCKED recorded without any acceptance closure | `ROADMAP.md` |

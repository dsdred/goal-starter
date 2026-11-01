# MA-06 F-7.5 — Owner Evidence Custody Record (external report, Revision 2)

**Status: recorded 2026-11-01 by the acceptance-preparation gate. Custody and verification record only — it is not the evidence, not an acceptance result, and not a closure of any criterion.** The artifact described here lives **outside the repository**; this file neither reproduces nor extends it, and does not turn it into a contract PASS. Every criterion row of the [acceptance contract](ADR021-LIFECYCLE-VISIBILITY-MA-06-BROWSER-ACCEPTANCE.md) stays `not run`, F-7 and MA-06 stay **OPEN**, Manual Owner Acceptance stays **OPEN / BLOCKED**, ADR 015 stays **FROZEN**, and ADR 021 stays **Accepted** with no status change.

## 1 The artifact

| Item | Value |
|---|---|
| Path | `C:\Users\dsdred\adr021-acc\evidence\agent\ADR021-F75-ACCEPTANCE-REPORT.md` |
| Size | **23 744 B** |
| SHA-256 | `223b918a416478df889afabf7271714139ca5dff80d64e0517df16acd511ba25` |
| Lines | 236 |
| mtime | `2026-10-25 21:10:15.153564500 +0500` |
| Revision | **2** (2026-10-25) — the report states revision 1 is retained in the gate record as history, not as the operative verdict |
| Subject | **F-7.5 only** — the `[O]` and `[A]` halves for a **model-route** stop, RU and EN |

The file was not written, copied or renamed by this gate. The values above are the measurements taken on 2026-11-01 and are identical to the report's own provenance sections.

## 2 Verification performed (read-only, 2026-11-01)

1. SHA-256 recomputed → `223b918a…` — **matches**.
2. Size and mtime re-read → 23 744 B, `2026-10-25 21:10:15.153564500 +0500` — **unchanged**.
3. The ten evidence artifacts listed in the report's §8 register were re-hashed and re-sized; **10/10 match both the hash and the byte count** recorded there (RU: agent report 3 427 B, `devtools.json` 34 217 B, `net.jsonl` 423 525 B, `observer.jsonl` 1 038 712 B, `watch.log` 742 B; EN: 3 432 B, 70 284 B, 2 470 894 B, 3 360 970 B, 2 037 B).
4. The code binding was checked against the environment it names: the kit's `src` worktree is at `b5c7c15858df3de84bab1b09043a0326088b51a3` with an empty `git status --porcelain`, and `bin\goal.exe` is 15 670 784 B with SHA-256 `23cc221cc241d273cb6011da701c31f56700ec8f8831c394f9e5a617ce534188`.
5. Nothing was re-executed, re-derived or re-adjudicated here. §3.2 below quotes the report's **own** verdict; §4 states what that verdict covers.

## 3 What the report records

### 3.1 Binding (report §1)

| Element | Value |
|---|---|
| Code under test | `b5c7c15858df3de84bab1b09043a0326088b51a3` |
| Documentation base | `02f58a602c3026103975736e4e2d5cf3374200dc` |
| Binary | `goal.exe`, 15 670 784 B, SHA-256 `23cc221c…`, built 2026-10-24 02:59:39+05:00 (after both commits) |
| Served frontend | `/static/app.js` SHA-256 `ab4db4b3…` after CR strip — equal to the `HEAD:internal/webui/static/app.js` blob of that tree |
| Declared limitation | `go version -m` carries **no** `vcs.revision`; the binding rests on the tree, the byte-equal served asset and the build time, not on an embedded SHA |

### 3.2 The report's verdict (report §0)

| Criterion | Language | `[O]` | `[A]` | Disposition |
|---|---|---|---|---|
| F-7.5 | RU (r3) | present, **retrospective** (§5a) | 18/18 required legs PASS | **PASS** |
| F-7.5 | EN (r3) | present, synchronous (§5) | 18/18 required legs PASS | **PASS** |
| F-7.5 as a whole (RU **and** EN) | — | both halves | both halves | **PASS** |

Ordering per operation, as recorded: RU `t0 10:31:31.133Z → t5 10:31:35.205Z`, badge dwell **2 656 ms**, instance `prof-stop-1792923383742456500-3`, PID 17 984; EN `t0 15:29:32.624Z → t5 15:29:36.725Z`, badge dwell **3 979.1 ms**, instance `prof-stop-1792931654824683300-4`, PID 27 836. Both satisfy `t0 < t1 ≤ t2 < t5` and `t0 < t3 ≤ t4 < t5`.

### 3.3 Owner statements, verbatim

EN (report §5, synchronous, 2026-10-25):

> «Подтверждаю лично: Видел статус STOPPING в интерфейсе GoAl. После завершения остановки статус самостоятельно сменился на STOPPED. F5 не нажимал, страницу не обновлял. Разрешаю зафиксировать это как Owner Observation `[O]` для EN r3.»

RU (report §5a, **retrospective**, 2026-10-25):

> «Ретроспективно подтверждаю личное наблюдение во время RU r3 (PID 17984): Видел промежуточный статус «Останавливается». После завершения остановки статус самостоятельно сменился на «Остановлен». F5 не нажимал. Это свидетельство относится именно к RU r3, а не к предыдущим прогонам.»

**Preservation rules.** The RU marker **retrospective** must never be removed, softened or restated as synchronous. No observation date or timestamp may be attributed to either statement — the `t0…t5` ordering is `[A]`, and `[O]` neither confirms nor replaces it. The PID named in the RU statement is the Owner's; the EN statement names no PID, so its PID binding is `[A]` only. Neither statement may be extended to another run, another instance or another language. The earlier program-wide RU remark ("Останавливается видел", Leg A) is **not** part of this evidence and stays adjudicated NOT OBSERVED. *(2026-11-01, Pre-Commit Correction Gate: that "Leg A" is the **historical Leg A (2026-10-25)** — the earlier model-route observation run bound to `b5c7c15`. It is not the Owner-procedure steps A–G of the contract, and not execution A1 of the new acceptance pass; see the contract §18.7.)*

## 4 What the evidence does and does not establish

| Contract item | Covered by this artifact? | Note |
|---|---|---|
| **F-7.5** (RU + EN badge, one operation each) | **Yes — at `b5c7c15`** | `[O]` + `[A]` + all five correlation facts per language |
| F-7.1, F-7.2, F-7.3, F-7.4, F-7.6 | **Supporting `[A]` lines only** | `t1`/`t3`/`t5` and the identity probes exist in the run records, but the report explicitly does not adjudicate these criteria (report §7, item 1) |
| **F-7.7** (store convergence) | **No** | the durable store was never read (report §7, item 2) |
| F-7.11 (manual stop, whole F-7.1…F-7.7 sequence) | **Partially** | model route only, and without the store leg |
| **F-7.12** (pipeline stop) | **No** | no pipeline leg was run; the instance view was not covered either |
| F-7.8, F-7.9, F-7.10 | **No** | outside the report's subject |
| F-7.13, F-7.14 | **No** | CI and deterministic tests are separate evidence |
| MA-06 / `LIFECYCLE-VIS-01` closure | **No** | the report states closure is an Owner decision and changes no repository status |

## 5 Binding and non-transferability

The artifact is bound to `b5c7c15858df3de84bab1b09043a0326088b51a3`. The repository baseline of this preparation gate is `3b22b8fbe50e9a8df8052ad8c704a4bd6cac4680`; `git diff --numstat b5c7c15..3b22b8f` covers **24 paths**, and of those:

| Layer | Delta since `b5c7c15` | Consequence for F-7.5 |
|---|---|---|
| Go / toolchain / fixtures | **zero** — no `*.go`, `go.mod`, `go.sum`, `.go-version` or `testdata/**` path appears in the range | the backend behavior the evidence observed is byte-identical |
| UI assets | `internal/webui/static/app.js` 45/6, `style.css` 116/0, `i18n/en.json` 2/2, `i18n/ru.json` 2/2, `templates/index.html` 2/2 | the **page build** observed no longer exists in the current tree |
| Browser suites | `stopping.cjs` 106/4, plus `core.cjs`, `orphan.cjs`, `pipeline.cjs`, `responsive.cjs` | the maintained leg was extended (ADR 022 legs) |
| Documentation | ADR 022/023 records, acceptance records, `ROADMAP.md`, `BACKLOG.md`, READMEs | no behavioral effect |

The stopping badge word itself is **unchanged**: `models.status.stopping` is `"STOPPING"` in `en.json` and `"Останавливается"` in `ru.json` at both revisions, and the badge element (`<span class="status-badge <state>">` with the i18n label) still renders the state as its only word. What changed is around it: ADR 022 replaced a duplicated state word in the model row's action strip with a decorative, `aria-hidden` ring; ADR 023 renamed `pending`/`orphan` labels and changed row density; ADR 023 V-2.1 added the info control.

**Consequence, stated as a fact and not resolved here.** The *server-side* facts the evidence rests on are unaffected by the delta. The *rendered artifact* was captured from a page build that current `main` no longer serves. The contract's binding rule ("a criterion is met only when it is bound to a specific commit SHA and a recorded artifact") and §16's PASS definition both attach a PASS to a SHA **and** an artifact; this artifact's SHA is `b5c7c15`. Whether that is sufficient is exactly the acceptance-SHA decision the Owner owes — see [the execution plan](ADR021-F7-EXECUTION-PLAN.md) §"Acceptance-SHA policy" and §"Owner decisions". This record does not substitute one binding for another.

*(2026-11-01, Pre-Commit Correction Gate — the question is decided. **D-1** fixes the acceptance SHA at **`3b22b8fbe50e9a8df8052ad8c704a4bd6cac4680`**; **D-2** chooses re-observation at that SHA, so the B1 (RU) and B2 (EN) executions produce fresh artifacts and this `b5c7c15` report remains **historical evidence bound to its own SHA**. It is preserved byte-for-byte: not edited, not re-dated, not overwritten, and not carried forward as the acceptance observation. Options B and C are **not permitted**. The consequential shift on the *rendered artifact* is therefore not repaired by byte comparison — it is answered by the new Owner observation, which does not exist yet.)*

## 6 Reconciliation with the contract's `not run` row

The contract's F-7.5 row, its §17 table and its "zero `[O]` evidence" wording predate this record. They remain **correct about contract-bound execution**: no criterion has been executed and recorded against an acceptance SHA selected for release. This record adds one fact — an Owner adjudication for F-7.5 **exists** at `b5c7c15`, outside the repository, with `[O]` and `[A]` halves and a complete artifact register. It is recorded as such and **not** as a PASS at any other SHA. Flipping the row requires the Owner's acceptance-SHA decision; until then the row stays `not run`, and the Owner's adjudication is not silently promoted.

*(2026-11-01, Pre-Commit Correction Gate: the acceptance-SHA decision now exists (**D-1**/**D-2**, §5 above). The last sentence is therefore superseded as a condition — but the row still stays **`not run`**, because D-2 requires a **fresh** B1/B2 observation at `3b22b8f` and none has been made. The `b5c7c15` adjudication is not promoted by this record, not at its own SHA and not at the acceptance SHA; it is historical evidence only. The contract's "zero `[O]` evidence" wording remains true of the acceptance SHA.)*

## 7 What is owed next

1. The Owner's acceptance-SHA decision (execution plan, decisions **D-1** and **D-2**).
2. If **re-observation** is chosen: B1 (RU) and B2 (EN) produce fresh artifacts at the acceptance SHA; this record then stands as the historical evidence at `b5c7c15` and is not reused for the new SHA.
3. If **per-criterion binding to `b5c7c15`** is chosen: the contract gains a dated note naming that SHA for F-7.5 and nothing else, and every other criterion still runs at the acceptance SHA.
4. In either case: the report is never edited, the ten artifacts are never rewritten, and the RU retrospective marker stays.

*(2026-11-01, Pre-Commit Correction Gate — decided. Item 1 is discharged by **D-1**/**D-2** (re-observe at `3b22b8f`); item 2 is the operative branch, so B1 (RU) and B2 (EN) will produce fresh artifacts at the acceptance SHA and this record stands as the **historical `b5c7c15` evidence**, never reused for the new SHA. Item 3 is the branch **not** taken. Item 4 holds unconditionally.)*

**Status of this record: recorded at `3b22b8f` preparation; no criterion closed; no ADR status changed.** *(2026-11-01, Pre-Commit Correction Gate: unchanged — D-1/D-2 name the SHA and the re-observation, and this record still closes nothing. F-7.5 remains `not run`.)*

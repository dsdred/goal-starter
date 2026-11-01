# MA-06 F-7 — Acceptance Execution Plan (prepared 2026-11-01, NOT EXECUTED)

**Status: PREPARED, NOT EXECUTED.** This plan prepares the execution of `F-7.1 … F-7.14`; it executes nothing, closes no criterion and authorizes no code change. The authoritative document remains the [acceptance contract](ADR021-LIFECYCLE-VISIBILITY-MA-06-BROWSER-ACCEPTANCE.md) (`OPEN`, every criterion `not run`); this plan only fixes *how* the contract would be executed, *where* the evidence goes and *what* the Owner must decide first. Where this plan and the contract differ, the contract wins.

**Owner decision this plan serves (2026-11-01).** MA-06 / F-7.1–F-7.14 must be **fully accepted before the next GoAl release** — no waiver, no post-release deferral, no reduction of the acceptance contract. That decision is binding on release readiness only. It does not authorize a release, tag, commit, push, product-code change or the acceptance execution itself.

**Pre-Commit Correction Gate (2026-11-01, second pass — documentation only, still NOT EXECUTED).** The Owner's execution decisions **D-1 … D-7** are now **decided and recorded** (§8, contract §18.6), the F-7.9 actor decision is recorded as **D-8**, and the reconciliation findings **C-1 … C-9** are applied below. Nothing here executes a leg or fills a criterion: F-7.1 … F-7.14 remain **not run**, and the corrections only make the prepared documents consistent with the decisions. Where a corrected clause supersedes an earlier line of this plan, the earlier line is left in place and the correction is a dated note next to it.

## 1 Authority, scope and prohibitions

**Authorized by the preparing gate:** documentation only — this plan, the [F-7.5 custody record](ADR021-F75-OWNER-EVIDENCE-RECORD.md), one dated preamble paragraph and one new section in the contract, one dated tracking layer in `ROADMAP.md`, one dated note in `BACKLOG.md`.

**Not authorized:** product code, tests, fixtures, workflow changes, acceptance execution, interaction with any running GoAl instance, commit, push, tag, release, staging.

**Standing rules carried into every execution below:**

- **[O] / [A] / [C] stay separate** (contract §15). `[O]` is what the Owner saw and stated; an agent-driven browser action never produces a UI PASS (**RV-9**).
- **`GET /api/v1/pipelines/{id}` is never a source of a live `stopping`** (**RV-7**): its per-entry projection promotes only under `IsRunningOrStarting()`, which excludes `stopping`. It is used only as a negative control.
- **A SKIP is never a PASS.** `NOT OBSERVED` and `BLOCKED` leave a criterion open exactly as it was.
- **Nothing is invented**: no Owner statement, no timestamp, no baseline SHA, no tolerance.
- **Isolation**: the acceptance environment is `C:\Users\dsdred\adr021-acc` (loopback `127.0.0.1:18099`, `dataDir = C:\Users\dsdred\adr021-acc\data`, `authEnabled=false`). Ports `8088` and `8097`, the Owner's production instance and the `E:\accept\adr018\` tree are **out of scope**, as are secrets of any kind.

## 2 Baseline measured at preparation (2026-11-01)

| Item | Value |
|---|---|
| `HEAD` | `3b22b8fbe50e9a8df8052ad8c704a4bd6cac4680` |
| `origin/main` | the same SHA; divergence `0 0` |
| Worktree | one entry, ` M cmd/goal/main.go`, **mtime-only** (`git diff --quiet` exits 0) — it is not a content change and must never be staged |
| Tag at `HEAD` | none; newest release remains `v2.1.0` |
| CI for this SHA | run `41244385432`, **attempt 1**, **7/7 jobs PASS**, no rerun |
| Linux race step (literal) | `CGO_ENABLED=1 go test -race -timeout 20m ./...` → **20 `ok` + 4 `[no test files]`, 0 `DATA RACE`, 0 `FAIL`** (counted inside the step's own region; the whole-log figure would double them) |
| ADR 021 Slice 3 verbose step | `matched by -list: 24 (24 expected)`; **24 `--- PASS` / 0 `--- SKIP` / 0 `--- FAIL`** on Windows **and** on Linux |
| Browser Acceptance | **1011 raw `PASS |` / 0 raw `FAIL |` / 0 raw `SKIP |`** across the maintained chain; the stopping suite reports `STOPPING (observable transitional state, browser leg): TOTAL 112 | PASS 112 | FAIL 0 | SKIP 0` |

These figures are `[A]`/`[C]` evidence for **this** SHA. They discharge nothing until the Owner names the acceptance SHA (§5).

## 3 Contract clarifications for execution

Three items were ambiguous. Two resolve from authoritative bytes; one does **not** and is left to the Owner. None of the three changes a criterion's text or state. *(2026-11-01, Pre-Commit Correction Gate: the third — F-7.10 — is no longer left open. The Owner decided it as **D-3**, option (c); the resolution and the frozen expected-decision table are in §3.3 below.)*

### 3.1 F-7.7 — the durable record, its fields, and the verification procedure

**Where it lives.** The store is a single JSON file: `filepath.Join(dataDir, "goal_repo.json")` (`cmd/goal/main.go:238-246`; default `dataDir` is `./data`). For the acceptance kit that is `C:\Users\dsdred\adr021-acc\data\goal_repo.json`. The repository is `JSONRepository` (`internal/storage/repository.go:120-153`), whose `SchemaVersion()` is **8** (`:558`). The current kit store is schema 8 with 1 runtime, 1 model, 4 `exited` instances and **0 pipelines**.

**Record and fields.** Instances are a JSON array `instances[]` of `domain.LaunchInstanceEntry` (`internal/domain/launch_instance_entry.go:8-29`). The lifecycle-relevant wire fields are:

| Field (JSON) | Use in F-7.7 |
|---|---|
| `id` | the instance identity the API and the UI row carry |
| `state` | must be terminal (`exited` or `failed`) after the stop returns |
| `pid` | must equal the PID the live observation recorded |
| `exit_code`, `exit_class` | reported by the exit path; expected to agree with the terminal mapping |
| `started_at`, `stopped_at` | `stopped_at` must be non-zero and later than `started_at` |
| `updated_at` | must be at or after the stop was issued |
| `recovery_reason` | classification evidence for F-7.8 only |
| `environment`, `args`, `working_directory` | **never read into evidence and never printed** — `environment` can carry secrets |

**Agreement definition.** F-7.7 is met when, for the same instance, the terminal state read from `GET /api/v1/instances` (or `/instances/{id}`), the state rendered in the UI row, and `instances[].state` in the store are the **same** terminal value, `pid` agrees across API and store, and `stopped_at` is present. `exit_code`/`exit_class` are recorded and compared where the API exposes them; a difference there is reported, not smoothed over.

**Timing rule.** F-7.7 asserts terminal convergence, so the authoritative store read is taken **after** the synchronous stop response returns (`docs/API.md:129`). The durable record does **not** carry `stopping` while the process is alive: `stopCore` blocks in `ic.manager.Stop(ctx)` (`internal/process/supervisor.go:1747`), and `Manager.Stop` waits for exit or the 5 s force-kill deadline (`internal/process/manager.go:221-262`) before `persistStateLocked()` writes `stopping` (`:1751`, `:2130`); the terminal state follows at `:1794`. In the window the store therefore still reads the prior state — normally `running` — while the API reports the live projection `stopping` (`docs/API.md:124`), and the repository pins this in `TestT5DurableRecordStaysRunningWhileLiveViewIsStopping`. An in-window copy is admissible only as labelled context (`running` there is expected) and neither proves nor breaks F-7.7. A read taken *before* the stop returns proves nothing about convergence; a post-return record that contradicts the terminal state is a **FAIL**.

**Admissible read-only procedure** (performed by the Coordinator, against the isolated kit only):

1. Before the window, freeze `sha256`, size and mtime of `goal_repo.json`.
2. After the stop response returns, copy the file to a scratch directory **outside** the repository, and re-freeze the original.
3. Project only the allowed fields (table above) — never `environment`, never the whole file if it would be printed.
4. Compare the projection with the API response and with the UI observation; record the three-state table.
5. Record the post-read hash/size/mtime of the original, so the read is provably read-only relative to the application's own write.

`goal_repo.json.bak` is **not** lifecycle evidence: it is the ADR 018 portable-import pre-import snapshot and carries no lifecycle meaning.

### 3.2 F-7.9(c) — the documented contract of a repeated `Stop`

Derived from the authoritative bytes; **the API contract is not redefined here** and `docs/API.md` is unchanged.

| Aspect | Documented / implemented contract |
|---|---|
| Endpoint | `POST /api/v1/instances/{id}/stop` (`docs/API.md:119`) |
| Success | `200 {"status":"stopped"}` — and only after the process is gone; no `202`, no new fields (`docs/API.md:129`) |
| `404` | `code=not_found`, `error=instance_not_found` — this process has **no controller** for the id (`docs/API.md:197`) |
| `409` | `code=conflict`, `error=launch_in_flight` — **only** while the instance is `pending` (`docs/API.md:119`, `:171`, `:200`) |
| Serialization | `InstanceController.Stop` takes `ic.lifecycleMu` for the whole call (`internal/process/supervisor.go:1704-1708`), so Stop ∥ Stop cannot become two concurrent stop owners |
| Second call while the first is in flight | it blocks on `lifecycleMu` and, once the first completes, finds a terminal instance and returns `nil` → the handler answers `200 {"status":"stopped"}` |
| Second call after termination | same as above — `200` |
| Not asserted | at-most-once `Manager.Stop`: `stop()` has no `stopping` guard (`supervisor.go:1710-1727`), so after the 15 s stop-context expiry a later `Stop` may re-invoke `manager.Stop` (`:1747`). That ordering is **OS-5**, out of scope for F-7.9 |

**Accepted outcomes for a repeated Stop in F-7.9(c):** `200` with body `{"status":"stopped"}`, or `404 instance_not_found` if the controller is genuinely absent (for example after an application restart). Any other status (`409` when not `pending`, `409 not_restartable`, `400`, `500`), or a second concurrent stop owner, is a **FAIL**. A request that never reached the server is `NOT OBSERVED`.

### 3.3 F-7.10 — the arbitration baseline: **UNRESOLVED, Owner decision required** — **RESOLVED 2026-11-01 by D-3 (the pre-decision text is preserved below)**

*(2026-11-01, Pre-Commit Correction Gate — **RESOLVED by D-3: option (c), with `3ba719d9cf0573420b5665ab6c949154e4d435b7` as the historical anchor.** The adopted reading: the **anchor** supplies the recorded acceptance point (D6.1 published with exact-SHA CI `36020633891`, 7/7, and the D6.2 PASS over those bytes); the **expected decisions** are derived from ADR 017's D1 contract, `docs/API.md` and the ADR 016/017 regression suites, frozen before execution; the observed-versus-expected comparison is recorded probe by probe. **No expected status code is invented**, and no code byte-identity at the anchor is claimed. F-7.10 is therefore no longer BLOCKED on a baseline choice — only on execution.)*

*(2026-11-01, **C-3** correction to the "What is provable today" bullet below. The bullet's claim is about the **suite blobs** and is correct as far as it goes, but it must not be read as code byte-identity at the anchor. Measured at this gate: `internal/process/arbitration.go` differs between `3ba719d` (`ca3f896102c87cc89815e416a7b8cf6afe126734`) and `3b22b8f` (`28d014843db50a659fbaccd3dde75d243103f8d0`), and `internal/process/supervisor.go` differs too (`3ba719d` `5623f552a59f77021366ee9161e35f1c17545d6b` vs `3b22b8f` `6537ffb38355412558412160b545345a5609e57e`). The web-layer mapping files are identical (`handlers/helpers.go`, `handlers/instances.go`, `handlers/models.go`). What is provable is narrower and sufficient for D-3: the two regression-suite files are byte-identical at `a5026b5`, `3ba719d` and `3b22b8f`; the anchor's CI ran those exact suite bytes green; the acceptance SHA's CI runs the same suite bytes green; and ADR 017 records that the ADR 021 read-side amendment changed no arbitration decision. The table below is derived from the contract and those suites — not from an unproven assumption that the implementation bytes are identical.)*

**Frozen expected-decision table (D-3, derived 2026-11-01 — to be frozen by A3 before A1 runs; `[C]`).** Each row names the expected *decision*, its derivation, and the evidence that the decision is the documented one.

| Probe (while the target instance is `stopping` and its process is alive) | Expected decision | Derivation |
|---|---|---|
| **Restart** of the stopping instance | **admitted** — not refused; the call completes after the in-flight stop returns and publishes a new generation; no `409 not_restartable`, and no `409 launch_in_flight` caused by the stop itself | `checkRestartTarget` allows `stopping` without becoming a second stop owner (`internal/process/arbitration.go:307-325`); `docs/API.md:120`; T-4 `TestT4RestartDuringHeldStopDoesNotPublishThroughLiveRun` |
| **Manual Start of the same model** while an instance of it is `stopping` | **refused** with `409`, `code=conflict`, `error=launch_in_flight` | `LaunchInstance.IsInFlight()` includes `stopping` (`internal/domain/instance.go`); `compatible(ManualOwner, …)` is `false` (`internal/process/supervisor.go`); `scanConflicts` → `RejInFlight` (`arbitration.go`); `docs/API.md:343`; `TestAdmitAndStart_SameModelID_SerialAdmission` |
| **Start of a different model** while an instance is `stopping` | **admitted; completes and does not stall** | per-model `arbLock` and the model filter in `scanConflicts` (`arbitration.go`); `TestAdmitAndStart_DifferentModelIDs_Concurrent` |
| **Stop** of the `stopping` instance | `200 {"status":"stopped"}`, or `404 instance_not_found`; a `409 launch_in_flight` only if the instance was genuinely `pending` at that instant | §3.2 above; `docs/API.md:119`, `:129`, `:197`, `:200` |
| **Cross-model request issued while the stop holds the controller** | completes within the observed response time; not serialized behind the stop | T-4 and the per-model lock ordering; the raw duration is recorded, not a predicted bound |

Table rules: the table is `[C]` evidence frozen by A3 before A1 runs; A1 records every probe's observed status/body beside its expected decision; a deviation is a **FAIL** for F-7.10 unless an authoritative source discovered **before** execution contradicts a row — in which case the table is amended, re-frozen, dated and the amendment recorded, and no row is added after execution begins.

The criterion requires "same decisions as at the baseline", and the repository designates **no** single arbitration baseline SHA:

- **Reading 1 — the contract's own §2 baseline**, `a5026b58c23866e40b7f0c190bff609f690267e5`. It is the snapshot this contract's defect analysis was measured at. **No observed lifecycle-decision set exists at that SHA** in the repository, so "same decisions as at the baseline" cannot be evaluated against it without running the decisions there.
- **Reading 2 — the ADR 017 acceptance chain.** ADR 017 records D6.1 as `3ba719d9cf0573420b5665ab6c949154e4d435b7` with exact-SHA CI `36020633891` (7/7) and D6.2 as a PASS over those bytes; ADR 016 is "Accepted — implemented and published 2026-09-18" and names no SHA. This is the closest thing to a recorded acceptance point, but it is a review outcome, not a stored decision matrix.
- **What is provable today:** the ADR 016 and ADR 017 regression suites are **blob-identical** at `a5026b5`, at `3ba719d` and at `HEAD` — `supervisor_adr017_test.go` = `c90839911968f4996e8e5e772f0f25a0828adb19`, `supervisor_adr016_test.go` = `4e992dcdbe221d84aa8c787010b2ef88ed8667f6`. So the `[C]` half of F-7.10 ("the ADR 017 suite unmodified and green") holds under either reading; only the "same decisions" half needs a baseline.

**Disposition:** this plan does **not** invent a baseline. The Owner chooses one of:

- **(a)** designate `3ba719d` (the D6.2 acceptance point) as the arbitration baseline, and accept that its "recorded decisions" are the ADR 017 suite plus the acceptance report's findings, not a stored matrix;
- **(b)** designate `a5026b5` and authorize a short read-only decision-capture run there (extra execution, same environment) so the comparison is literal;
- **(c)** replace the comparison with an **enumerated expected-decision table** derived from ADR 017 and `docs/API.md`, and record observed-versus-expected for each probe.

Whichever is chosen must be written into the closing acceptance record. Until then F-7.10 is **BLOCKED** (precondition unset), not FAIL. *(2026-11-01: the choice is made — D-3, option (c), table above. This sentence is the pre-decision rule and is preserved as history; F-7.10 is no longer blocked on the baseline.)*

## 4 Acceptance-SHA policy

Three things are separate and must not be substituted for one another:

1. **Exact-SHA binding** — which commit the evidence names.
2. **Code equivalence** — which bytes are provably identical across revisions.
3. **Owner acceptance** — what the Owner observed and adjudicated.

Two facts bound the decision:

- **Go code is byte-identical** between `b5c7c15` and `3b22b8f`: `git diff --numstat b5c7c15..3b22b8f -- '*.go' go.mod go.sum .go-version testdata cmd` is empty.
- **The UI surface is not.** `app.js` 45/6, `style.css` 116/0, both i18n files 2/2, `templates/index.html` 2/2, `stopping.cjs` 106/4, plus other browser suites.

Options, with consequences:

| Option | What is bound | Consequence |
|---|---|---|
| **A (recommended)** — name `3b22b8f` (or the SHA current at execution time) and **re-observe** F-7.5 RU+EN there | every criterion at one SHA | the shipped UI is what the Owner observes; the 2026-10-25 evidence stays historical; costs two extra Owner stops |
| **B** — per-criterion binding: F-7.5 to `b5c7c15`, everything else to the acceptance SHA | two SHAs, textually admitted by the contract's binding rule | cheaper, but the Owner never observes the UI that ships, and two revisions must be tracked in the closing record |
| **C** — freeze the acceptance SHA at execution and, afterwards, require any delta to be proven `.md`-only before the final verdict | one SHA + a post-hoc proof | only works **forward**: it cannot retroactively bind the 2026-10-25 artifact to `3b22b8f`, because that delta is not documentation-only |

**Recommendation:** A. C remains a useful *closing rule* on top of A (no product bytes after the acceptance SHA without re-observation), but it cannot repair the existing binding.

*(2026-11-01, Pre-Commit Correction Gate — decisions taken. **D-1:** the acceptance SHA is **fixed** at `3b22b8fbe50e9a8df8052ad8c704a4bd6cac4680`; the option-A wording above — "or the SHA current at execution time" — is superseded and must not be read as licence to rebind later. **D-2:** F-7.5 is **re-observed at that SHA**; the 2026-10-25 evidence remains historical at `b5c7c15`. **Option A is adopted; options B and C are not permitted.**)*

*(2026-11-01, **C-9** — HEAD movement and re-binding. This gate's own documentation commit will move `HEAD` past the acceptance SHA, and any later documentation commit before execution will move it further. That is expected and does not by itself invalidate the binding. At final acceptance the closing record must prove that the delta `3b22b8f..<final verification HEAD>` contains **no** product, UI, test or fixture change — a `.md`-only delta, proven with `git diff --numstat` over that range. If any relevant byte did change, the evidence binding must be **reassessed** for the affected criteria (re-observation, or a new acceptance SHA); a PASS is never carried forward across changed product/UI/test/fixture bytes by assumption. This rule is forward-only and does not repair the historical `b5c7c15` binding — D-2 handles that by re-observation.)*

## 5 The six executions

Each execution is independent, has its own evidence directory and its own disposition. Execution order: A3 (evidence availability) → A1/A2 (coordinator legs) → B1/B2/B3 (Owner legs). A1 and B1 may share one stop operation; that sharing must be recorded explicitly, not assumed.

**Execution IDs vs the historical "Leg A" (C-8, 2026-11-01).** `A1`, `A2` and `A3` are **coordinator execution identifiers** of this plan. They are not the contract's Owner-procedure steps `A`–`G`, and they are not the "Leg A" of the 2026-10-25 F-7.5 report (a model-route RU+EN run at `b5c7c15`; see the [custody record](ADR021-F75-OWNER-EVIDENCE-RECORD.md)). A record cites an execution ID (`A1`/`A2`/`A3`/`B1`/`B2`/`B3`), the Owner-procedure step letter, or "historical Leg A (2026-10-25)" — never a bare "Leg A".

### A1 — Instrumented model-route stop (Coordinator)

| Field | Content |
|---|---|
| Criteria | F-7.1, F-7.2, F-7.3, F-7.6, F-7.9, F-7.10 — **F-7.4 is not an A1 criterion** (C-1, 2026-11-01: A1 is coordinator-driven, so its browser material cannot satisfy the browser-receipt criterion; see the F-7.4 caveat below) |
| Preconditions | acceptance SHA **fixed** at `3b22b8fbe50e9a8df8052ad8c704a4bd6cac4680` (**D-1**); `goal.exe` built from a **clean checkout or worktree at that SHA** (**D-5**, C-6: `git status --porcelain` empty before the build; source SHA, Go version, full `go version -m` output and binary SHA-256 recorded in `provenance.txt`); kit on `127.0.0.1:18099`, no other instance running; fake runtime in `signal-delay` mode with `exit_delay_ms=4000`; interactive console (AH-4 closed configuration) |
| Actions | 1) freeze provenance (binary hash, served `app.js` blob equality). 2) Start one model instance through the model route; record instance id and PID. 3) Arm instruments **before** the stop: item-route poller, list-route poller, `GetProcessIdentity` prober, network recorder, DOM sampler at 25 ms. 4) Issue the stop. 5) Capture `t0…t5` for item route and list route; confirm both report `stopping` while the identity probe still matches the frozen `Executable`/`StartedAt`. 6) After the stop returns, record the terminal state and the store projection (§3.1). 7) Repeat stop twice during the flight and once after termination (§3.2) — **performed by the Coordinator** as an API-level `[A]` probe per the F-7.9 actor decision **D-8** (the contract's Owner-procedure step D is preserved as history; no `[O]` line is produced by these legs). 8) Arbitration probes while an instance is stopping: Restart, Start of the same model, Start of a **different** model — the cross-model request must complete and must not stall; record every probe against the frozen expected-decision table of §3.3, decision by decision, with the observed status/body beside it. |
| Expected | item route `stopping` inside the live window; list route the same; identity alive at `t1`; exit after `t5`; terminal `exited`; repeated stop inside §3.2; every arbitration probe equal to the **frozen expected-decision table of §3.3** — no status code is predicted here (C-5, 2026-11-01: the earlier `Start-same-model 409 launch_in_flight` expectation was pre-assumed and is withdrawn; the derived table is the source, and observed-versus-expected is recorded probe by probe) |
| Artifacts | raw response bodies with timestamps, poller JSONL, identity-probe JSONL, arbitration response table, terminal state, store projection, provenance file |
| PASS | all four server-side criteria (F-7.1, F-7.2, F-7.3, F-7.6) satisfied with `t0<t1≤t2<t5`; repeated-stop outcomes inside §3.2 (F-7.9); every arbitration probe matches the frozen §3.3 table (F-7.10). F-7.4 is **not** decided here (C-1) |
| FAIL | product behaved wrongly inside a window long enough to observe (e.g. first readable `stopping` arrives after death, list route omits it, repeated stop returns an undocumented status) |
| NOT OBSERVED | the fixture produced no dwell (signal delivery failed → immediate force kill), the page/poller missed the window, or a capture was lost — never written as FAIL |
| BLOCKED | no binary with bound provenance, wrong console mode, kit occupied |
| Cleanup | stop every remaining instance, record store state, leave the kit idle; no repository writes |

**F-7.4 caveat.** Its admissible evidence is a **browser** network record of the response consumed by a rendered row. Under RV-9 that record is collected from the **Owner's** session (B1/B2). If A1 runs alone with an agent-issued API stop, its browser-side material is at most `[C]` supplemental and F-7.4 stays `NOT OBSERVED`.

**Correction C-1 (2026-11-01).** A1 therefore records **no admissible F-7.4 result at all** — it is not among A1's criteria, and F-7.4 cannot be marked PASS, FAIL or NOT OBSERVED from A1. F-7.4 is satisfied only by the browser network receipt collected during **B1 (RU)** or **B2 (EN)**; any A1 browser material kept is labelled `[C]` context and is not adjudicated as the criterion.

### A2 — Lifecycle classification sweep (Coordinator)

| Field | Content |
|---|---|
| Criterion | F-7.8 |
| Preconditions | same environment; a quiescent store; every scenario starts from a recorded store snapshot |
| Scenarios | **S1** normal stop → terminal `exited`/`failed`, no `stale`, no `orphan`. **S2** restart of a running instance → new generation, no `stale`/`orphan`. **S3** external kill of the runtime while running, then application restart → the instance is classified per `classifyForRecovery` (`internal/process/supervisor.go:1063-1088`); with the Windows job-object execution a natural restart yields `stale` (identity unconfirmed), **never** `orphan`. **S4** application shutdown while an instance is stopping → after restart, classification per the same rules, and the stopping instance is not reported as running. **S5** application shutdown while an instance is running → same rule set. |
| Expected | every observed class equals the rule for that scenario; no class appears that ADR 005/ADR 016 do not prescribe |
| Artifacts | per-scenario store projection (`state`, `pid`, `recovery_reason`, `stopped_at`), the classification code path cited, the shutdown/restart log lines |
| PASS / FAIL / NOT OBSERVED / BLOCKED | PASS = all scenarios agree; FAIL = a classification contradicts the rule inside a valid observation; NOT OBSERVED = the scenario could not be produced; BLOCKED = environment unavailable |
| Cleanup | restore the store to its pre-scenario state via the application, never by editing bytes |

### A3 — CI and deterministic evidence for the acceptance SHA (Coordinator)

| Field | Content |
|---|---|
| Criteria | F-7.13, F-7.14, and the **`[C]` half of F-7.10** (C-3, 2026-11-01) |
| Preconditions | the acceptance SHA is **published** and its CI run exists for that exact `headSha` (a local build is not F-7.13 evidence); the F-7.10 expected-decision table (§3.3) is **frozen and dated here, before A1 runs** |
| Actions | 1) Record `headSha`, run id, attempt number and all seven job conclusions. 2) Quote the Linux job's literal `CGO_ENABLED=1 go test -race -timeout 20m ./...` line, then count **inside that step's region**: `ok`, `[no test files]`, `DATA RACE`, `FAIL`. 3) Quote the Slice 3 verbose step's `matched by -list: 24 (24 expected)` and its per-OS `--- PASS` / `--- SKIP` / `--- FAIL` counts. 4) Record the hashes of `internal/process/supervisor_stopping_view_test.go` and `internal/process/supervisor_d4_publication_scan_test.go` at that SHA. 5) Map the deterministic tests to F-7.14: `TestT1StoppingViewReadableWhileProcessAlive` and the T2 family (`TestT2SupervisorStatusReportsStoppingWhileHeld`, `TestT2SupervisorListReportsStoppingWhileHeld`, plus the route/service T2 legs) hold `Manager.Stop` at the test gate and assert the readable `stopping` view while the process is alive — the F-7.2/F-7.3 assertions, made timing-independently. 6) **F-7.10 `[C]` production:** record the blob hashes of `internal/process/supervisor_adr016_test.go` and `internal/process/supervisor_adr017_test.go` **at both** the acceptance SHA and the historical anchor `3ba719d9cf0573420b5665ab6c949154e4d435b7`; record that those two suite blobs are identical at `a5026b5`, `3ba719d` and the acceptance SHA; record the anchor's exact-SHA CI run `36020633891` (7/7) and the acceptance SHA's run, each quoted from its own job log; record that `internal/process/arbitration.go` and `internal/process/supervisor.go` are **not** byte-identical at the anchor, so no code byte-identity is claimed; attach the frozen expected-decision table of §3.3 with its derivation sources. 7) The F-7.10 `[C]` half is complete only when the table, the suite hashes at both SHAs, the anchor provenance and the acceptance-SHA CI evidence are all present. |
| Baseline numbers if the acceptance SHA is `3b22b8f` | run `41244385432` attempt 1, 7/7; race step 20 `ok` / 4 `[no test files]` / 0 `DATA RACE`; 24/24 legs PASS, 0 SKIP on both OS; browser 1011 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |` |
| Historical anchor for F-7.10 (`3ba719d`) | D6.1 published (`fix(api): reconcile lifecycle error precedence`); exact-SHA CI run `36020633891` (CI #158), **7/7 jobs**; D6.2 focused re-review returned **COMPLETE — PASS** over those bytes (a review outcome, not a stored matrix — which is why D-3 uses the enumerated table, not the anchor alone) |
| PASS | all of the above quoted from the job log of the exact SHA |
| FAIL / NOT OBSERVED / BLOCKED | FAIL = a step failed for that SHA; NOT OBSERVED = a step is absent or the log is truncated; BLOCKED = no published SHA exists yet |
| Cleanup | none (read-only) |

### B1 — Owner manual stop, RU, model route (Owner)

| Field | Content |
|---|---|
| Criteria | F-7.4, F-7.5 (RU), F-7.7, F-7.11 — **F-7.4's only admissible producer is this Owner session (C-1, 2026-11-01)** |
| Preconditions | binary built from the acceptance SHA fixed by **D-1** (`3b22b8f`) from a clean checkout/worktree (**D-5**/**C-6**); **RU** UI; `signal-delay` fixture with a ~4000 ms dwell; **interactive console** (Windows Service, detached, session-0 and Linux dwell remain unproven and would make the leg BLOCKED); recorder armed and proven armed (`armedAt`) before the click; store hash frozen |
| Owner actions | start the model, then press **Stop once** and watch the model row until it settles. No F5, no navigation, no language switch |
| Owner states (`[O]`) | the exact badge word seen, in which row, whether it appeared **before** the process exited, and that the terminal word followed by itself |
| Coordinator measures (`[A]`) | `t1` item-route body, `t2` identity probe of the PID, `t3` the browser's own `GET /instances` record, `t4` DOM/screenshot with the row identifier, `t5` termination; then the store projection (§3.1) and the API terminal state |
| PASS | five facts recorded as five separate lines for the same operation, `t0<t1≤t2<t5` and `t0<t3≤t4<t5`, plus three-way terminal agreement (API / UI / store) |
| FAIL | badge first appears after `t5`; API/UI/store disagree on the terminal state inside a valid window |
| NOT OBSERVED | the poll missed a too-short window, the capture was lost, or the run's dwell is unproven |
| BLOCKED | non-interactive console, unbound binary, recorder never armed |
| Cleanup | stop any remaining instance; record final store state |

### B2 — Owner manual stop, EN, instance view (Owner)

Same structure as B1 with the **EN** UI and the instances view; criteria **F-7.4**, F-7.5 (EN half) and F-7.11. The EN and RU halves together satisfy the contract's "RU **and** EN" requirement; either half alone leaves F-7.5 open. F-7.4's admissible record is the browser network receipt of either session (C-1: B1/B2, never A1).

### B3 — Owner pipeline stop (Owner)

| Field | Content |
|---|---|
| Criterion | F-7.12 |
| Preconditions | a pipeline with **at least one entry** that can be started and stopped. The current kit has **0 pipelines**, so one must be seeded first and its id recorded; if the single model is already in use, register a second model entry pointing at the same fake-runtime executable with distinct arguments, so the one-in-flight-per-model rule is not triggered |
| Owner actions | start the pipeline, wait for the entry to be running, then stop it through the **pipeline** action and watch the instance/model row and the pipeline chip |
| Coordinator measures | the same `t0…t5` set, read **only** from `GET /api/v1/instances` / `/instances/{id}` and the rendered row; `GET /api/v1/pipelines/{id}` recorded **as a negative control** — its entry must stay `stopped` throughout (**RV-7**) |
| PASS | the five facts hold on the instance surfaces, the pipeline endpoint never reports `stopping`, and the synchronous per-entry result matches the contract |
| FAIL | pipeline endpoint claims `stopping`, or the instance surfaces do not show it inside the live window |
| NOT OBSERVED / BLOCKED | as B1 |
| Cleanup | stop the pipeline, remove the seeded pipeline only through the application, record store state |

## 6 Evidence index and artifact discipline

Root: `C:\Users\dsdred\adr021-acc\evidence\f7-<yyyy-mm-dd>\` (outside the repository), one subdirectory per execution (`A1`, `A2`, `A3`, `B1`, `B2`, `B3`).

Per execution, the mandatory artifacts are: a `provenance.txt` (SHA, binary hash, config fields, console mode, fixture mode), the raw instrument files, the projected store table, the Owner statement file (`[O]`, verbatim, with the retrospective marker where applicable, in its own file — never merged with `[A]`), and a `report.md` listing the five facts as separate lines.

Rules: raw frames are kept **including** out-of-window material; nothing is deleted for looking wrong; each frozen artifact is recorded with SHA-256, size and mtime in a `MANIFEST.md`; the manifest does not hash itself; no artifact is edited after freezing; the store projection never contains `environment`.

| Criterion | Produced by | Minimum artifacts |
|---|---|---|
| F-7.1 | A1 (+B1) | API bodies `stopping`, code path citation |
| F-7.2 | A1 | item-route body + identity probe at the same instant |
| F-7.3 | A1 | list-route body |
| F-7.4 | B1/B2 | browser network record with request/response timestamps inside the window |
| F-7.5 | B1 (RU) + B2 (EN) | `[O]` statement + DOM/screenshot, plus the four preceding facts |
| F-7.6 | A1 | post-stop identity probe + terminal state |
| F-7.7 | B1 (+A1) | store projection + API body + `[O]` UI line |
| F-7.8 | A2 | per-scenario store projection + classification path |
| F-7.9 | A1 (Coordinator, **D-8**) | repeated-stop responses + `lifecycleMu` path |
| F-7.10 | A1 (+A3) | arbitration responses against the frozen §3.3 table + suite blobs, anchor CI and expected-decision table |
| F-7.11 | B1 (+B2) | the F-7.1…F-7.7 set on the manual path |
| F-7.12 | B3 | instance-route evidence + RV-7 negative control |
| F-7.13 | A3 | job log for the exact `headSha` |
| F-7.14 | A3 | test source hashes + CI result |

*(2026-11-01, Pre-Commit Correction Gate — what changed in this index: **C-1** removed F-7.4 from A1's admissible results, so its only producer is B1/B2; **C-2** corrected F-7.11's execution index from `B1` to `B1 (+B2)`; **C-3** gave F-7.10 its `[C]` producer in A3 and its frozen expected-decision table; **C-4**/**D-8** records the F-7.9 actor decision and labels A1's producer accordingly. No row gained a PASS.)*

## 7 Disposition rules

- **PASS** per criterion requires: the required observation, every admissible class, the `t0…t5` ordering where applicable, a named SHA and a retained artifact.
- **FAIL** asserts the product behaved wrongly **inside a window long enough to observe**.
- **NOT OBSERVED** names the measurement gap and states the observed window next to the 3000 ms poll period (or that no dwell was produced at all).
- **BLOCKED** names the unmet precondition by number.
- The **leg** is PASS only when all fourteen are PASS; FAIL if any is FAIL; otherwise INCONCLUSIVE with each non-PASS criterion listed under its own disposition.
- Closure of `LIFECYCLE-VIS-01` and of MA-06 is a separate **Owner decision**; evidence never closes a task by itself.
- The closing record — `docs/acceptance/ADR021-F7-ACCEPTANCE-RESULT.md`, created only after execution — is a separate documentation gate.

## 8 Owner decisions required before any execution

**Decided 2026-11-01 (Pre-Commit Correction Gate, C-7).** The Owner made every decision this section asked for; the proposal table below is preserved as history and is superseded by the decided table beneath it. No decision is an acceptance result and none fills a criterion.

| # | Decision | Decided value (2026-11-01) |
|---|---|---|
| **D-1** | **Acceptance SHA** | **`3b22b8fbe50e9a8df8052ad8c704a4bd6cac4680`, fixed.** Not "current HEAD"; never replaced at execution time. §4, contract §18.4. |
| **D-2** | **F-7.5 binding** | **Re-observe at the acceptance SHA.** The 2026-10-25 RU run bound to `b5c7c15` is **historical evidence, preserved, not re-dated and not overwritten**; it is not carried forward as the acceptance observation. See the record file. |
| **D-3** | **F-7.10 baseline** | **Option (c)** — the ADR 016/017 behavioural contract, with `3ba719d` as the historical anchor. Expected decisions derived from authoritative source and tests; the frozen table is §3.3. |
| **D-4** | **Pipeline fixture** | **An isolated pipeline fixture is authorized for a future acceptance execution.** No fixture is executed in this correction gate. |
| **D-5** | **Rebuild authorization** | A future acceptance binary **may** be built from a **clean checkout/worktree at `3b22b8f`**; provenance to record: exact source SHA, clean source tree, Go version, `go version -m` output, binary SHA-256. **No rebuild in this gate.** |
| **D-6** | **Session and console** | The Owner performs **exactly three** real-browser executions: **B1** RU manual Stop, **B2** EN manual Stop, **B3** Pipeline Stop. |
| **D-7** | **A1's actor** | The **Coordinator** performs the technical/API checks and collects `[A]`/`[C]`; `[O]` evidence remains **exclusively** with the Owner. |
| **D-8** | **F-7.9 actor** (added 2026-11-01) | The Coordinator **may** execute the repeated Stop in isolated A1 as `[A]`+`[C]`. Reconciled with Owner-procedure step D, which stays the Owner's manual repeated-Stop step and is **preserved unchanged**; see the contract's step-D note and §18.6. |

*(The pre-decision table, kept as history:)*

| # | Decision | Alternatives | Blocked if undecided |
|---|---|---|---|
| **D-1** | **Acceptance SHA** | A: `3b22b8f` or current HEAD at execution (recommended); B: mixed binding; C: forward-only freeze | A3, and the binding of everything else |
| **D-2** | **F-7.5 binding** | re-observe at the acceptance SHA (recommended) vs bind to `b5c7c15` | B1/B2 scope |
| **D-3** | **F-7.10 baseline** | §3.3 (a), (b) or (c) | F-7.10 only |
| **D-4** | **Pipeline fixture** | seed a pipeline in the kit; second model entry if needed | B3 |
| **D-5** | **Rebuild authorization** | build `goal.exe` from the acceptance SHA and replace the stale `b5c7c15` binary | every Owner leg |
| **D-6** | **Session and console** | Windows interactive console; Owner availability for three stops | B1–B3 |
| **D-7** | **A1's actor** | API-issued stop by the Coordinator is acceptable for `[A]`-only criteria; F-7.4 still needs the Owner's session | A1's F-7.4 line |

The current kit binary is **stale**: `bin\goal.exe` is 15 670 784 B, SHA-256 `23cc221c…`, built 2026-10-24 from `b5c7c15`. It must not be used for an execution whose acceptance SHA differs.

## 9 What this plan does not do

It does not execute a leg, does not mark any criterion, does not change the contract's rows, does not alter `docs/API.md`, does not touch product code, tests, fixtures or workflows, does not operate any GoAl instance, and does not authorize commit, publication, tag or release. D-1 … D-8 are the Owner's decisions (§8); this plan records them and derives the executions from them, it does not make them.

**Status: PREPARED at baseline `3b22b8f`; NOT EXECUTED; no criterion closed.** *(2026-11-01, Pre-Commit Correction Gate: D-1 … D-8 are decided and recorded, and corrections C-1 … C-9 are applied; the plan remains NOT EXECUTED, F-7.1 … F-7.14 remain **not run**, MA-06 stays **OPEN**, and the leg is unchanged. Next gate is the Owner's Commit Gate.)*

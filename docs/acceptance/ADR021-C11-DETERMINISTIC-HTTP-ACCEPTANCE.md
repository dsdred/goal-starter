# MA-06 F-7 — C-11 Deterministic HTTP Acceptance Record

**Finding:** MA-06 F-7, correction **C-11** — deterministic proof that **both** instance routes report `stopping` while the stop is held and the managed process is alive.
**Verdict recorded here: F-7.14 = PASS, class `[A]`; C-11 = CLOSED.**
**Evidence binding:** repository `dsdred/goal-starter`, published SHA `f886b5238ad4bc4a18cd56569fb03fb9835a6f03` (commit `test(adr021): add deterministic stopping HTTP proof`, parent `152a4e47a3659723bcfd57baf407b20d65eb725c`), CI run `41501723581`, attempt 1.
**Recorded:** 2026-11-03, documentation only, at baseline `f886b5238ad4bc4a18cd56569fb03fb9835a6f03` (`HEAD == origin/main`, divergence `0 0`; worktree carries only the known mtime-only `M cmd/goal/main.go`). The acceptance execution is the one whose CI timestamps fall on 2026-11-02T18:33–18:43Z; this record documents it and modifies no product, test or fixture byte.

**Authority.** The Owner accepted the evidence as `F-7.14: PASS [A]`, SHA `f886b5238ad4bc4a18cd56569fb03fb9835a6f03`, CI `41501723581`, attempt 1, and directed that **C-11 be closed** in the repository's tracking bytes. The closure condition registered in [the acceptance contract](ADR021-LIFECYCLE-VISIBILITY-MA-06-BROWSER-ACCEPTANCE.md) §18.9 was: *a compliant deterministic test is implemented, executed against published bytes, and accepted; only then may F-7.14 be filled again, bound to that SHA and not to `3b22b8f`*. This record states that each part is met and binds the row to `f886b52`.

**Scope.** One criterion row is filled. This is `[A]` evidence produced by a Go test and an exact-SHA CI run. It is **not** an Owner `[O]` observation, it does not substitute for one, and it does not make F-7 or MA-06 pass.

---

## 1 The requirement this record settles

ADR 021 **T-1** ([`docs/adr/021-lifecycle-state-observability-view.md`](../adr/021-lifecycle-state-observability-view.md), `:436`) obliges deterministic proof that, **with the stop gate held**, `GET /api/v1/instances/{id}` and `GET /api/v1/instances` each return `state: "stopping"` while the fake-runtime process is still alive, and that after release the terminal state is observed. Liveness must be proven by **process identity, not PID alone** (`IsProcessAlive` at `internal/platform/recovery.go:16`; `GetProcessIdentity` at `:20`; `ProcessIdentity{ExecutablePath, StartTime, HasStartTime}` at `:6-10`, compared against the instance's frozen `Executable`).

ADR 021 **T-2** (`:437`) allocates one test per layer, so a regression in one layer cannot hide in another. C-11's defect was that the two route layers had **no** timing-independent test: the pre-existing route legs took their window from the `signal-delay` fixture dwell and called `t.Skipf` when it was missed ([contract §18.9](ADR021-LIFECYCLE-VISIBILITY-MA-06-BROWSER-ACCEPTANCE.md) table and the C-11 register entry). The compliant tests are the handler-layer tests for the two route constructs; the live server/browser receipt is a different criterion (F-7.4), and the visible badge is F-7.5.

## 2 What was implemented (published at `f886b52`)

| Component | Published location | Blob |
|---|---|---|
| `signal-hold` fake-runtime mode | `testdata/fake-runtime/fake.go` — `case "signal-hold"` `:97`, `doSignalHold` `:275` | `1895cb3f504841abcbf1bf9092126eb538d99459` |
| Two handler-layer route legs and their harness | `internal/webui/handlers/instances_stopping_test.go` — `TestT2CollectionRouteReportsStoppingWhileProcessIsAlive` `:632`, `TestT2ItemRouteReportsStoppingWhileProcessIsAlive` `:661` | `3d057c1522c776d27fb814576f6ce29de986a35a` |
| Mechanism clarification | ADR 021 `:459-463` (the `signal-hold` note; unchanged semantics, no obligation relaxed) | `35ecc2e9…` |

**The barrier, not a clock.** In production, `InstanceController.stop` publishes the transitional state (`ic.setStateLocked(domain.InstanceStateStopping)`, `internal/process/supervisor.go:1723`) **before** it calls `stopCore` (`:1726`), and `stopCore` is what invokes `ic.manager.Stop(ctx)` (`:1747`) — the only place the graceful OS stop signal is sent (`internal/process/manager.go:223`). In the fixture, the child registers real `SIGTERM`/`SIGINT` handlers (`fake.go:127`), writes `ready.json` after registration, and writes `signal.json` **only after it has received a real OS signal** (`:324`). Therefore `signal.json` cannot exist unless the transitional state was already published. `awaitSignalHeld` (`instances_stopping_test.go:206`) waits for that marker, fails if the stop returns before it, and validates the marker's event, signal name, PID and executable. Once the marker is read, each route is read **exactly once** (`readRouteOnce` `:251`); there is no polling of product state and no wall-clock interval whose expiry could be scored as success.

**What each leg asserts.** `assertStoppingView` (`:289`) checks the returned instance's ID, `InstanceStateStopping`, `IsInFlight`, and the full generation identity (PID, `StartedAt`, `RuntimeID`, `Executable`, `Args`). `assertProcessLive` (`:339`) uses `platform.NewRecoveryProber()` and requires both `IsProcessAlive` and a `GetProcessIdentity` result whose executable path equals the instance's frozen `Executable`, with the start-time anchor compared when the platform supplies it.

| Leg | Window and route order |
|---|---|
| `TestT2CollectionRouteReportsStoppingWhileProcessIsAlive` (`:632`) | One held stop: item route (`InstancesHandler.Get` via `httptest`) then collection route (`InstancesHandler.List` via `httptest`), both inside the same `signal.json` → `release.json` interval, then release |
| `TestT2ItemRouteReportsStoppingWhileProcessIsAlive` (`:661`) | An independent held stop for the item route alone |

**Fail-only watchdogs.** Every deadline in these legs calls `t.Fatalf`; there is no `t.Skip`, no `testing.Short` branch and no `stopGate` seam in the HTTP legs (RV-8). The guards are: signal deadline 20 s, per-read stall guard 1500 ms, release wait 10 s, stop-call wait 30 s, process-gone wait 10 s. `awaitReleased` (`:391`) fails if the stop returns before the release is observed; `awaitStopReturn` (`:427`) requires a nil error; `assertTerminal` (`:443`) requires `exited`/`signaled`/exit 0; `assertNoLeaks` (`:476`) requires `ListActive` to be empty and the PID to be gone. The hold is bounded by production's own 5 s graceful-stop timer (`manager.go:261`), and the read budget (2 × 1.5 s) sits inside it, so a hang converts to a FAIL rather than to a pass or a skip.

## 3 Accepted observations at `f886b52`

| Claim | Evidence |
|---|---|
| A real OS stop signal was delivered and observed | the child writes `signal.json` only after `signal.Notify` receives `SIGTERM`/`SIGINT` (`fake.go:127`, `:324`); `awaitSignalHeld` validates that marker or fails |
| The state was published by the server before the signal | production ordering `supervisor.go:1723` → `:1726` → `manager.Stop` `:1747` → `GracefulStop`; no client-side state exists in the test path |
| The process was alive at the observation | identity-confirmed inside the held window by `assertProcessLive` (`instances_stopping_test.go:339`), not a bare PID check |
| Both routes returned `stopping` inside one held stop | item then collection in the collection leg; the item leg repeats it independently |
| The process exited after release, with no residue | `release.json` created with `O_EXCL`, `released.json` observed, terminal `exited`/`signaled`/exit 0, `ListActive` empty and PID gone |
| No SKIP or wall-clock success path | 0 `t.Skip`, 0 `testing.Short`; CI reports 24/24 deterministic legs PASS and 0 SKIP on each OS |

## 4 Exact-SHA CI

Run `41501723581`, attempt 1 (no rerun), `headSha = f886b5238ad4bc4a18cd56569fb03fb9835a6f03`, conclusion success, event `push`, 7/7 jobs:

| Job | Job id | Result |
|---|---|---|
| Test (Windows) | `125470617916` | success |
| Test (Linux, race) | `125470617248` | success |
| Browser Acceptance (Chromium, headless) | `125470617593` | success |
| Lint | `125470617877` | success |
| Build (windows-latest) | `125470617701` | success |
| Build (ubuntu-latest) | `125470617775` | success |
| Vulnerability Check | `125470617771` | success |

- **The two legs executed on both platforms.** In the per-OS verbose step (`ADR021_SLICE3_TESTS` enumerating 24 names), `TestT2CollectionRouteReportsStoppingWhileProcessIsAlive` reports `--- PASS` at 1.46 s (Windows) and 0.25 s (Linux, race), and `TestT2ItemRouteReportsStoppingWhileProcessIsAlive` reports `--- PASS` at 0.26 s (Windows) and 0.02 s (Linux, race). Neither line is a SKIP.
- **Literal race step.** `CGO_ENABLED=1 go test -race -timeout 20m ./...` at `2026-11-02T18:34:53.7497925Z`; quoted region: **20 `ok` + 4 `[no test files]`, 0 `DATA RACE`, 0 `FAIL`**. The Windows full-suite step reports the same 20/4/0 shape.
- **Background, not part of this criterion.** Browser Acceptance reported **1011 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |`**, with the stopping suite `TOTAL 112 | PASS 112 | FAIL 0 | SKIP 0`. Those figures are `[A]` background for the F-7 leg; they do not fill F-7.4 or F-7.5 and are not used to establish F-7.14.

## 5 Windows and Linux verification

Both CI test jobs executed the same published test bytes on their native platforms. On Windows the stop path is `CTRL_BREAK_EVENT` through `GenerateConsoleCtrlEvent`, escalating to Job Object termination when no console exists (`internal/platform/platform_windows.go`); on Linux it is `SIGTERM` to the process group (`internal/platform/platform_unix.go`). The parent commit `152a4e4` closes the job-assignment process handle on both success and failure paths, so GoAl no longer keeps a terminated process object alive and `IsProcessAlive` (which on Windows means a successful `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)`, not an exit-code check) no longer masks termination. The test's held-window liveness claim rests on the identity comparison at the observation instant; the post-release assertions (release marker observed, stop returns nil, exit 0, PID gone) close the window. Local `-race` remains unavailable on this workstation, so concurrency evidence for the published SHA is CI-only, as the contract's platform leg requires.

## 6 Known limitations

1. **Handler layer, not the live server.** The two legs drive `InstancesHandler.Get` and `InstancesHandler.List` through `httptest` with a real Supervisor/InstanceService and a mock instance store. The server mux, auth middleware and browser transport are outside this criterion; F-7.4 (browser receipt) and F-7.5 (Owner-observed badge) remain unrun.
2. **`[A]` only.** No `[O]` evidence exists for any F-7 criterion. F-7.14 is a deterministic-test obligation, not an Owner observation, and this record creates none.
3. **Windows liveness semantics.** `GetProcessIdentity` can return data for a terminated process whose object is still referenced. The evidence closes that gap by the fixture's held window and the post-release exit chain, not by asserting that the probe alone proves liveness after the fact.
4. **Bounded hold.** The window is bounded by the production 5 s graceful-stop timer. If a read exceeded its 1.5 s stall guard the leg would FAIL — by design — rather than skip, so a severely loaded runner produces a red leg, not a false pass.
5. **Mock durable store.** The handler environment uses `newMockInstanceStoreForTest`; durable-record convergence is F-7.7's subject and is not claimed here.
6. **Platform coverage scope.** The evidence covers the two CI platforms in the default (non-Service, non-session-0) execution mode. Windows Service mode, detached execution and non-interactive contexts remain outside this evidence, as they were outside the AH-4 dwell measurement.
7. **Local logs are scratch.** Working copies of the CI logs were kept at `E:\tmp\c11_*` during the acceptance gate. They are outside the repository, untracked and not evidence of record; the evidence of record is the CI run and job ids above.

## 7 Mixed-SHA evidence situation (recorded explicitly)

F-7's evidence is deliberately bound per criterion, and the F-7 baseline has **not** moved to a single new SHA:

| Criterion | Bound evidence | Run | Status |
|---|---|---|---|
| F-7.13 | `3b22b8fbe50e9a8df8052ad8c704a4bd6cac4680` (**D-1 fixed acceptance SHA**) | `41244385432` attempt 1 | **PASS `[A]`** — unchanged by this record |
| F-7.14 | `f886b5238ad4bc4a18cd56569fb03fb9835a6f03` | `41501723581` attempt 1 | **PASS `[A]`** — first bound at this SHA, because this is the first published SHA that carries the compliant test |
| F-7.10 | `[C]`: expected-decision table frozen at `3b22b8f`, historical anchor `3ba719d`, suite blobs identical at `a5026b5` / `3ba719d` / `3b22b8f` / published HEAD | anchor `36020633891` | `[C]` complete, `[A]` pending (A1) — unchanged |

**No single F-7 baseline SHA is implied.** The Owner-procedure SHA fixed by D-1 remains `3b22b8f` for A1/A2 and the browser legs; F-7.13 stays bound there. F-7.14 is bound to the later published SHA that first contains and executed the test. The acceptance contract's OPEN state, the F-7 leg's INCONCLUSIVE disposition and MA-06's OPEN state are unaffected by having two bound `[A]` rows at two SHAs.

## 8 What this record does not change

- **F-7.13** keeps its PASS at `3b22b8f` / `41244385432` attempt 1, exactly as written; it is not rebound.
- **F-7.10** keeps only its `[C]` half; the criterion stays `not run`.
- **F-7.1 … F-7.12 and F-7.14**: twelve criteria remain `not run` (F-7.10 among them), with **zero `[O]` evidence**.
- **The F-7 leg** remains **INCONCLUSIVE**; **MA-06** remains **OPEN**; the acceptance contract remains **OPEN**; Manual Owner Acceptance remains **OPEN / BLOCKED**; ADR 015 remains **FROZEN**.
- **ADR 021** keeps its status (**Accepted**) and its obligations (T-1 … T-9); this record closes a correction, not a design decision.
- **No product, test or fixture byte changes**, no acceptance execution, no commit, publication, tag or release is performed by the documentation gate that writes this file.

## 9 Provenance

- Closure record in the acceptance contract: [§18.10](ADR021-LIFECYCLE-VISIBILITY-MA-06-BROWSER-ACCEPTANCE.md).
- Design-side status note: ADR 021, paragraph following the 2026-11-01 C-11 clarification.
- Execution-plan status notes: [`ADR021-F7-EXECUTION-PLAN.md`](ADR021-F7-EXECUTION-PLAN.md).
- Tracking: [`ROADMAP.md`](../../ROADMAP.md) (LIFECYCLE-VIS-01) and [`BACKLOG.md`](../../BACKLOG.md) (lifecycle observability).
- Implementation commit: `f886b52` (`docs/adr/021-…` +6 lines, `instances_stopping_test.go` +478/−188, `fake.go` +111/−0); prerequisite Windows fix: `152a4e4` (`platform_windows.go`, `job_object_test.go`).

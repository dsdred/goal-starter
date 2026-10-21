# AH-4 — Windows `stopping`-dwell capability: measurement and Owner closure record

**Status:** **AH-4 capability prerequisite CLOSED for the measured configuration** (Owner decision, 2026-10-21). This record documents the measurement that decision relies on. It is **not** an F-7 acceptance result, does **not** close `LIFECYCLE-VIS-01` or MA-06, and does **not** approve ADR 021 implementation.

**Owner decision:** AH-4 is **ACCEPTED** for the measured Windows interactive-console configuration, and the Stage B delayed graceful-exit fixture design is approved as the basis for future F-7 acceptance. The acceptance does **not** extend to Windows Service mode or detached execution, and it is **not** an approval of ADR 021 implementation.

**Finding being closed:** Gate 24ah **AH-4** — the T-9 browser fixture's `stopping` dwell was asserted by fixture choice, but nothing in the repository measured it, and on Windows a failed `GenerateConsoleCtrlEvent` escalates immediately to `ForceKill` (`internal/platform/platform_windows.go:118-122`). ADR 021 revision 3 recorded the capability as **NOT YET PROVEN** and required a separately authorized preparation gate before any Owner UI leg could be scheduled.

**Design record:** [ADR 021 — Observable Transitional Lifecycle States](../adr/021-lifecycle-state-observability-view.md) (**Proposed**; implementation **NOT STARTED**). **Acceptance contract:** [ADR021-LIFECYCLE-VISIBILITY-MA-06-BROWSER-ACCEPTANCE.md](ADR021-LIFECYCLE-VISIBILITY-MA-06-BROWSER-ACCEPTANCE.md) (**OPEN / BLOCKED BY DESIGN**; all criteria **not run**).

---

## 1 Measured scope

| Dimension | Measured value |
|---|---|
| Platform | Windows 10 Pro, DisplayVersion 26H2, build 26300.9711 (UBR 0x25EF), AMD64 |
| Execution mode | Interactive console, Win32 session 1; per-run console with a nonzero window handle, `CONOUT$` open, `GetConsoleMode` = `0x7`, console process count 4 |
| Launch isolation | Fresh console per run (`start "AH4 <stage> <n>" /wait cmd.exe /c …`); no Windows Service, no detached execution, no `CREATE_NO_WINDOW` |
| Go toolchain | `go1.26.10 windows/amd64`, `CGO_ENABLED=0` |
| Stop path | The production `process.Manager` / `platform.ProcessControl` path at the exact baseline; no production code was modified |
| Repository baseline | `c7920c47b6b909587b5075d24cbf0e266b9dd827` (`HEAD == origin/main`, clean index and worktree) |

**Explicitly outside the measured scope:** Windows Service mode, detached execution, a non-console or session-0 service context, and Linux. Nothing in this record proves the dwell on those configurations.

## 2 What was measured

Two stages exercised the real OS lifecycle. Neither fabricated or injected a `stopping` API state; the GoAl HTTP server was never started and no API request was made.

- **Stage A — force-kill-bounded dwell.** Existing fake-runtime mode `infinite`, which ignores the stop signal and stays alive until `Manager.Stop` exhausts its 5 s grace and invokes `ForceKill`. This proves a live interval of at least 3500 ms exists, but it does **not** prove graceful termination.
- **Stage B — graceful dwell.** A scratch test-only fixture outside the repository that:
  1. registers `os.Interrupt` and `SIGTERM`;
  2. writes `ready.json` only **after** registration;
  3. on the actual OS stop signal writes `signal.json` atomically (same-directory temp file + rename);
  4. sleeps 4000 ms;
  5. exits **0**.

  The 4000 ms sleep is below the existing 5000 ms `ForceKill` grace; no timeout in production code was changed.
- **Calibration.** `Manager.Kill` on the probe process, to establish the force-kill discriminator.
- **Negative control.** The existing `graceful` mode, which must exit in **< 1000 ms** after the stop signal; a longer result would have invalidated the harness.

The probe captured `t0` immediately before `Manager.Stop`, sampled instance state, process identity and liveness every 20 ms, recorded `ProcessControl.WasSignaled()` (set **only** by `ForceKill`), and recorded the `Stop` return time. Marker files were read with a bounded retry; that retry affects only evidence-file reading, never signal delivery or process lifetime.

## 3 Results

### 3.1 Calibration and negative control

| Run | Purpose | Stop-return dwell | `ControlWasSignaled` (ForceKill) | Exit | Outcome |
|---|---|---:|---|---|---|
| CALIBRATION-1 | force-kill discriminator check (`mgr.Kill`) | 1.785 ms | true | 0 | PASS |
| NEGATIVE-1 | existing `graceful` fixture; required < 1000 ms | 5.2553 ms | false | 0 | PASS |

**Calibration finding:** Job-object `ForceKill` produced exit code 0 and Manager `ExitClass = signaled`; exit code and exit class therefore **cannot** identify a force kill. `ProcessControl.WasSignaled()` is the discriminator used throughout this record.

**Negative-control finding:** signal delivery works and the fixture exits normally in 5.2553 ms without `ForceKill`; the harness is valid.

### 3.2 Stage A — existing `infinite` mode, 3 independent runs

| Run | PID | `t0` (UTC) | Dwell to `Stop` return | State at 3500 ms checkpoint | Identity at checkpoint | `ControlWasSignaled` | Exit | Outcome |
|---|---|---:|---:|---|---|---|---|---|
| STAGEA-1 | 40924 | 2026-10-21T18:03:33.8774066Z | 5003.9640 ms | `stopping`, alive | same start + executable | true | 0 | PASS |
| STAGEA-2 | 23732 | 2026-10-21T18:03:42.4167240Z | 5004.5456 ms | `stopping`, alive | same start + executable | true | 0 | PASS |
| STAGEA-3 | 29148 | 2026-10-21T18:03:50.4859772Z | 5004.3833 ms | `stopping`, alive | same start + executable | true | 0 | PASS |

**Stage A classification: PROVEN PRELIMINARY CAPABILITY (3/3).** All three runs show a real live interval ≥ 3500 ms with an observable `stopping` state and verified process identity; termination was **force-kill-bounded**, not graceful.

### 3.3 Stage B — delayed graceful exit, 3 independent runs

| Run | PID | Ready marker (UTC) | `t0` (UTC) | Signal marker (UTC) | Signal delay from `t0` | Signal → exit | `Stop`-return dwell | Checkpoint at 3500 ms | `WasSignaled` | Exit | Outcome |
|---|---|---:|---:|---|---:|---:|---:|---|---|---|---|
| STAGEB-1 | 28668 | 2026-10-21T18:04:02.9036721Z | 2026-10-21T18:04:03.0179516Z | 2026-10-21T18:04:03.0192298Z | 1.2782 ms | 4005.7906 ms | **4007.1518 ms** | `stopping`, alive, identity OK | false | 0 | PASS |
| STAGEB-2 | 30712 | 2026-10-21T18:04:10.6869267Z | 2026-10-21T18:04:10.8048576Z | 2026-10-21T18:04:10.8059838Z | 1.1262 ms | 4005.2855 ms | **4006.4946 ms** | `stopping`, alive, identity OK | false | 0 | PASS |
| STAGEB-3 | 42728 | 2026-10-21T18:04:17.2666643Z | 2026-10-21T18:04:17.3828084Z | 2026-10-21T18:04:17.3841161Z | 1.3077 ms | 4004.6370 ms | **4006.0276 ms** | `stopping`, alive, identity OK | false | 0 | PASS |

**Stage B classification: PROVEN GRACEFUL DWELL CAPABILITY (3/3).** The measured graceful interval is **~4006–4007 ms**, which exceeds the 3000 ms UI poll (`internal/webui/static/app.js:2360-2364`).

### 3.4 Evidence linkage

- **Real OS signal receipt, independently evidenced.** The child process wrote `signal.json` itself on receiving the OS stop signal. That marker is independent of the probe's measurement: it did not exist before `t0` and appeared 1.1262–1.3077 ms after it. Marker contents in all runs: `event = signal`, `signal = interrupt`, `sleep_ms = 4000`, marker PID = Manager start PID = ready-marker PID, marker executable = fixture path.
- **Process identity verified.** Every pre-stop and 3500 ms checkpoint used `GetProcessIdentity` (executable path **and** start time), not a bare PID check. All runs reported the same executable and start time at both points.
- **No premature `ForceKill`.** `ControlWasSignaled = false` in all three Stage B runs, while calibration proved the same flag becomes `true` on `ForceKill`. `Stop` returned only after the fixture's normal exit, with exit code 0.
- **No fabricated lifecycle state.** The fixture wrote only its own marker files. No API response, instance state or `stopping` field was injected; the probe read the state produced by the production Manager.
- **Console delivery path accounted for.** `GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid)` delivered the signal because the child shared a real console with the probe. The fixture's marker proves delivery, not merely a signal attempt.

## 4 Owner decision (2026-10-21)

The Owner decided:

1. **AH-4 is ACCEPTED** for the measured Windows interactive-console configuration.
2. The **Stage B delayed graceful-exit fixture design** is approved as the basis for future F-7 acceptance.
3. This acceptance **does not extend** to Windows Service mode or detached execution.
4. This decision is **not** an approval of ADR 021 implementation.

Closure wording used by the repository: **“AH-4 capability prerequisite CLOSED for the measured configuration.”**

## 5 Evidence location and provenance

All measurement evidence is retained **outside the repository** at `E:\PRJ\ah4-measurement-20261021` on the Owner's workstation. It is not committed and is not part of the repository's tracked bytes. `sha256sum -c EVIDENCE-MANIFEST.sha256` verified **85 of 85** entries.

| Artifact | SHA-256 |
|---|---|
| `EVIDENCE-MANIFEST.sha256` | `b887096746338c50012fb8ccd92224c3decf484eb76f13f35797c05c6c18c33c` |
| `AH4-SUMMARY.md` | `747f8a6dc18db618d29208d138a338ac7f10f896fc8c63a1c8f260e1024cab1b` |
| `environment.txt` | `ba1ea3241a1e1aa239d21aa585b6d1511c10f5b21abc4a87d632b07be2773e55` |
| `repo-c7920c4.tar` (exact-HEAD archive) | `46f0ae7596e638610f7bafbd9dbd6e39e605f5c0590280cde6a91ff01044fab0` |
| `src/ah4probe/ah4_probe_test.go` (probe source) | `c15488365e096e2e336c2be94c9dbe86c0bd043967416b277a5f7fa909268d62` |
| `src/stageb-fixture/main.go` (Stage B fixture source) | `1aaa9d2afacad069315f312c841c60fb812814c1da0b58655cae814a0e44fbfd` |
| `bin/ah4-probe.test.exe` (executed test binary) | `de95417ea275a042bd46a1031f3124de57cfe4a038d56afbebfed7e3083fa9f4` |
| `bin/fake-runtime.exe` (Stage A/negative control) | `1e25ecc1d1fc65d191fdfae0dff87d695d93365b8a77e175f039ceb30061faec` |
| `bin/stageb-fixture.exe` (Stage B) | `d09f227d5980e461d13d82c8d0d7f52e59e1df4a7869eddf17ee6c0965dd9f58` |

The probe test binary (4,930,048 bytes) reports `go1.26.10`, `windows/amd64`, `CGO_ENABLED=0`, and the same SHA-256 in all eight official `result.json` files. The extracted source tree was proven equal to `HEAD` content modulo CRLF (260 tracked files; 257 normalized + 3 raw blob matches; 0 mismatches), and the key production blobs — including `internal/process/manager.go`, `internal/platform/platform_windows.go`, `internal/platform/recovery_windows.go` and `testdata/fake-runtime/fake.go` — matched their `HEAD` blobs.

**Superseded series:** an earlier official series under probe binary `240d582d…` hit a harness-only `ready.json` sharing violation in Stage B run 1 before `t0` (no signal sent, failure preserved). The final series was rerun in full on the corrected harness; the superseded and preliminary runs are retained under `runs/_superseded-retry-edit/` and `runs/_prelim/` and are **not counted** in the results above.

## 6 Limitations — what this closure does not cover

- **Windows Service mode is NOT proven.** The measurement used an interactive console; a service context has no console for `GenerateConsoleCtrlEvent`.
- **Detached execution is NOT proven** (no `CREATE_NO_WINDOW`, no detached process tree).
- **Non-interactive/session-0 console configurations are NOT proven.**
- **Linux dwell is NOT proven.** No Linux measurement was performed in this gate.
- **F-7 criteria are NOT executed.** F-7.1 … F-7.14 all remain **not run**; no PASS is introduced.
- **MA-06 remains OPEN**, and Manual Owner Acceptance remains **OPEN / BLOCKED** (ADR 015 stays **FROZEN**).
- **ADR 021 remains Proposed** and its implementation remains **NOT STARTED**. No lifecycle view, reader migration, API change or production timing change is authorized by this record.
- **The scratch Stage B fixture is not production code and was not committed.** The maintained test-only fixture mode derived from it is described in §7; it does not implement F-7.
- Exit code and exit class are not force-kill discriminators in this environment (calibration proved both can indicate a normal-looking exit after `ForceKill`); `WasSignaled` is.

## 7 Maintained fixture prepared from the proven design

The Stage B behavior is now available as the test-only fake-runtime mode **`signal-delay <marker-dir> [exit-delay-ms]`** in `testdata/fake-runtime/fake.go`:

- registers `SIGTERM` and `SIGINT` (on Windows, `CTRL_BREAK_EVENT` is delivered as `os.Interrupt`);
- writes `ready.json` atomically only after signal registration;
- writes `signal.json` atomically on actual OS signal receipt;
- sleeps the configured delay (**default 4000 ms**) and exits **0**;
- writes no API state and does not alter production timing or `Stop` semantics.

Focused tests in `internal/platform/graceful_stop_dwell_test.go` verify the marker contents, a delayed graceful exit, process identity during the dwell (including the 3500 ms checkpoint), normal exit code 0, and the absence of the `ForceKill` path. On a Windows environment with no console, where `GenerateConsoleCtrlEvent` cannot be delivered, the test records the environment limitation and is **skipped** after the ForceKill escalation; it does not pass and never treats that condition as a product failure. Existing fake-runtime modes and their contracts are unchanged.

## 8 Repository state at recording time

- Baseline `c7920c47b6b909587b5075d24cbf0e266b9dd827` was clean before this documentation/fixture gate.
- This record asserts nothing about later HEAD, remote, worktree, tag or release state; re-derive them with `git rev-parse HEAD`, `git ls-remote origin refs/heads/main` and `git tag --points-at HEAD`.
- No commit, staging, push, tag, release or CI dispatch is authorized by this record.

# ADR 011: Windows Service / Background Mode — True SCM Integration

**Status:** Accepted — owner contract agreed 2026-08-31 (owner decisions 1–10 incorporated: in-binary support, `--service run` as the internal SCM entrypoint, absolute registered command, absolute-path install pre-flight, LocalSystem account, `auto` start type without implicit start, SCM StopPending/wait-hint/45 s outer budget, SCM-level restart, Event Log = operational diagnostics only, ps1 removal on acceptance, `internal/updater` untouched). **Implemented 2026-08-31** — `goal --service {install|uninstall|start|stop|restart|status|run}` behind the `ServiceManager` interface in `internal/platform` (svc + svc/mgr + raw event-log syscalls, no new dependency), the one shared application lifecycle (`cmd/goal` `runApplication`), the D3 install pre-flight (refuse-before-register), the D2 exact registered image, LocalSystem + `auto`/`manual` + 45 s registered stop timeout (registry `StopTimeout`), SCM-level restart, Event Log `slog` handler (audit never mirrored), bounded `not supported` stub on non-Windows. `deploy/windows/*-service.ps1` removed and all doc/build references reconciled in the same change (including the live MSI/SFX tooling and `scripts/build-all.ps1`, whose release pipeline bundled the dead scripts). Automated gates pass (gofmt/vet/test, Win+Linux builds, handler state-sequence + pre-flight refusal tests). **Real-SCM manual acceptance completed 2026-09-05 — PASS for ADR 011 items 1–8 on a real Windows SCM; the combined Pipeline real-world acceptance also PASS. D8.1 targeted rerun confirmed `ProviderName="GoAl"`, operational EventData payload, no ADR 007 audit mirroring, controlled stop/uninstall, and cleanup.** Note: `x/sys` v0.47.0 no longer ships the high-level `windows/eventlog` subpackage, so the Event Log is driven by the raw `RegisterEventSource`/`ReportEvent` syscalls of the same module (no new dependency, per D1.5).
**Date:** 2026-08-31 (draft 2026-08-31; owner contract 2026-08-31)
**Related:** ROADMAP P1 "Windows Service / Background Mode", ADR 001 (Job Object ownership; "attempt a console control event for graceful shutdown where compatible, then terminate the Job Object"), ADR 005 (Recovery — orphan/stale reclassification), ADR 008 (kill of an orphan), ADR 009 (hot-reload — `config.LoadReadOnly`), ADR 010 (startup autostart), `deploy/systemd/goal.service` (Linux precedent)

## Context

ROADMAP P1 carries the item "Windows Service / Background Mode: true SCM integration (service registration, graceful stop, diagnostics without console, service-mode paths); compatible with Recovery (ADR 005); uninstall safety", with a design gate ("lifecycle ADR: SCM contract, interaction with Supervisor/Recovery, service vs foreground mode") and a pre-implementation forensic requirement (existing `deploy/windows/install-service.ps1`, `uninstall-service.ps1`, and `internal/updater` service integration — "to avoid a parallel mechanism"). Forensic (2026-08-31):

1. **The binary has no SCM awareness at all.** `cmd/goal/main.go` is a plain foreground process: the lifecycle context comes from `os.Interrupt`/`SIGTERM` (`main.go:92`), shutdown is `supervisor.ShutdownWithPersistence` with a 30 s budget + audit close (`main.go:132-140`). No `golang.org/x/sys/windows/svc` import exists anywhere in the repository. `golang.org/x/sys v0.47.0` is already a direct dependency (`go.mod:7`), so `windows/svc`, `windows/svc/mgr`, and `windows/eventlog` are available with **no new dependency**.
2. **The tracked installer registers a service that can never start.** `deploy/windows/install-service.ps1` (v0.8-era):
   - a) line 35 `sc.exe … binPath=$BINARY_PATH` is **unquoted** while `$BINARY_PATH = "C:\Program Files\GoAl\goal.exe"` contains a space — sc.exe parses the binPath at the space (`C:\Program` + arguments) and registers a broken service image (sc.exe quoting rules);
   - b) **no arguments** are passed to the binary — the service working directory is `System32`, so `config.Load("goal.json")` (`main.go:37`) reads `C:\Windows\System32\goal.json` and the process exits 1 even with a correct binPath;
   - c) deprecated `sc.exe binname=` verb;
   - d) existence of `goal.json` is only a *warning* and the existence of `goal.exe` is never checked;
   - e) structurally decisive: the binary never calls `StartServiceCtrlDispatcher`, so even a perfectly registered service image would be killed by the SCM (event 1053/1060). The gap is in the binary, not the script.
3. **`uninstall-service.ps1`** (`Stop-Service -Force` + `sc delete`) is mechanically sound but targets a service that can never run; it deletes no data (correct: the repo/config are user data, not the service's).
4. **`internal/updater` is dead code.** No file in the repository imports `internal/updater` (there is no `cmd/goal-updater` or other caller). Its `restartService()` (`updater.go:448-460`) assumes a running "goal" service and its `installWindows()` overwrites a live `goal.exe` with no stop — the exact "parallel mechanism" the ROADMAP warns about. **Owner decision 10: `internal/updater` is NOT modified, removed, or wired as part of this ADR's MVP; it stays tracked as separate technical debt / future decision.**
5. **Headless foreground already works.** `d23fe67` (console-less `GracefulStop` → immediate Job Object termination on GCE failure) closed the last defect that made *headless foreground* operation fail; this ADR adds true SCM control on top, not a workaround for it.
6. **Linux precedent is set and unchanged:** `deploy/systemd/goal.service` (`Type=simple`, journal logging, `Restart=on-failure`) is the Linux background mechanism; systemd owns the lifecycle there, so no in-binary service mode exists on Linux.
7. **Recovery contract is settled (ADR 005/008):** on every startup `Recover()` reclassifies transitional instances to `stale`/`orphan`; the Job Object (ADR 001) kills the whole instance tree when the GoAl process dies, including on an SCM hard kill. Whatever the SCM does to the GoAl process, the next startup reconciles — the service mode must not fork this path.
8. **Path-resolution semantics are CWD-relative throughout (owner decision 3 reconciliation).** Every filesystem path in the existing contract resolves against the **process working directory** (plain Go semantics, no config-file-anchored resolution anywhere): the effective `dataDir` is the config field or the default `"./data"` (default at `config.go:184`; consumer rule `main.go:60-63` and `webui/server.go:63-66` — a *relative* default); runtime `executable`/`workingDirectory` and model `path` are existence-checked CWD-relative (`validate.go:82-150`, `filepath.Abs`); repository runtime/model entries created via the UI resolve the same way at launch. Consequence: under the SCM default working directory (`C:\Windows\System32`), any relative path — including the *default* `dataDir` — would be reinterpreted against `System32`. **The existing configuration architecture cannot guarantee service-safety of relative paths by itself.** The contract below resolves this **without changing any runtime path semantics**: the registered command is fully absolute (D2), and install pre-flight **refuses registration** unless every effective path the service depends on is absolute (D3). A *runtime* guarantee for arbitrary future relative paths would require new resolution semantics — the owner explicitly forbids inventing those (decision 3); the residual is bounded and documented (D3.4) instead.

Consequence: the task is (a) an in-binary, Windows-only **SCM contract** (`run` internal entrypoint + install/uninstall/start/stop/restart/status subcommands) reusing the *exact* foreground startup and shutdown sequence, (b) an **absolute registered command** and an **absolute-path install pre-flight**, (c) **LocalSystem** service account with documented consequences, (d) **SCM StopPending/wait-hint** stop semantics with the 45 s SCM timeout as the *outer* adapter budget, (e) **Event Log** operational diagnostics with ADR 007 audit untouched, and (f) **stop-before-delete uninstall** that never touches user data.

## Decisions

### D1 — One binary; service mode is a Windows-only subcommand surface (owner decision 1)

1. New verbs on the existing `flag` parser: `goal --service <verb>`, `verb ∈ {install, uninstall, start, stop, restart, status, run}`; `--service-name` (default `GoAl`); `--config` is reused (for `install`: the config path to embed in the service image). `goal` with no `--service` is byte-for-byte the current foreground behavior.
2. **`--service run` is an internal SCM entrypoint, not an alternative interactive foreground mode.** It is valid **only** when the process was launched by the SCM (`svc.IsService()` true); executed outside an SCM session it prints a bounded error ("not running under the Service Control Manager") and exits non-zero. It never starts a UI, never reads stdin, never installs itself. The installed service image (D2) is the only production path into `run`.
3. **One application lifecycle, both modes.** Service mode reuses the existing foreground application startup/shutdown path exactly — **no separate Supervisor, no separate Recovery, no separate Pipeline autostart, no separate persistence semantics** for service mode (owner decision 1). The only differences between the modes are: the lifecycle-context source (SCM stop request vs OS signals), the operational log destination (Event Log vs stdout, D8), and the registration tooling (D5/D6/D9).
4. Platform code lives in `internal/platform` per the file-ownership rule: a `ServiceManager` interface with the SCM implementation in `internal/platform/service_windows.go` (svc + svc/mgr) and a stub in `service_unix.go` whose every verb returns a bounded `not supported on this platform` error (Linux: systemd owns the lifecycle; the binary stays single). No shell, no `cmd /c`, no PowerShell — all SCM calls are direct syscalls via `x/sys`; any process argument is passed as `[]string`.
5. **No new dependency:** `golang.org/x/sys` is already required (`go.mod:7`); only subpackages are added.

### D2 — Registered command: exact semantics (owner decision 2)

The installed service persists **exactly** this service image (SCM `binPath` string):

```
"<EXE>" --service run --config "<CONFIG>"
```

- `<EXE>`: the **absolute, cleaned path** (`filepath.Abs` + `filepath.Clean`) of the `goal.exe` binary that executed `install` (`os.Executable`). The executable path is always absolute.
- `<CONFIG>`: the **absolute, cleaned path** of the config passed via `--config` at install time (a relative argument is resolved against the installing process's working directory **at install time** and the resolved absolute form is what gets registered). An explicit absolute config path is always what the service persists.
- **Quoting:** `<EXE>` and `<CONFIG>` are wrapped in double quotes if and only if the path contains a space or a double quote (embedded quotes doubled), per SCM binPath command-line parsing; `--service run` and `--config` carry no spaces and are unquoted. No other arguments are ever appended.
- **No dependence on the SCM default working directory** (e.g. `C:\Windows\System32`): the executable and the config are absolute by construction, and every other path the service consumes is absolute by the D3 pre-flight — the SCM working directory is referenced by **no** documented service behavior. The service never `chdir`s (no new path behavior is invented).
- Also persisted at install: service name (default `GoAl`, `--service-name`), display name `GoAl - Local AI Runtime Manager`, description, start type (D5), stop timeout 45 s (D6), service account **LocalSystem** (D4).
- Example registered image: `"C:\Program Files\GoAl\goal.exe" --service run --config "C:\Program Files\GoAl\goal.json"`.

### D3 — Paths and configuration contract (owner decision 3)

1. **Runtime path semantics are UNCHANGED in both modes:** relative filesystem paths resolve against the process working directory (existing Go semantics; forensic item 8). Service mode adds no resolution mode, no `chdir`, no config-file anchoring.
2. **Deterministic service-install contract — all effective service paths must be absolute.** `install` pre-flight **refuses registration** (bounded diagnostic naming every offending entry; nothing is written) unless all of the following hold:
   - the resolved registered `--config` exists and passes `config.LoadReadOnly` (side-effect-free, ADR 009) + `ValidateFull`;
   - the **effective `dataDir`** — the config field, or `"./data"` when empty (the exact consumer rule of `main.go:60-63` / `server.go:63-66`) — is an **absolute** path; a missing/relative `dataDir` is refused (it would place the repository and the audit file in `System32`);
   - every config-seeded runtime `executable` / `workingDirectory` and model `path` (when set) is absolute;
   - if `<effective dataDir>\goal_repo.json` exists, install reads it **side-effect-free** (raw JSON decode only — no repository construction, no file creation, no backup; `storage.NewJSONRepository` has creation semantics and is never used by install) and applies the same absolute-path rule to its runtime/model entries.
3. **Install never writes user-visible state:** no default config creation (hence `LoadReadOnly`, not `Load`), no credential migration, no repository seeding, no `.bak`. Install output is either the registration or the refusal.
4. **Accepted residual (documented, bounded):** repository entries created or edited **after** install via the Web UI may reintroduce relative paths; under the service they fail at startup/launch with bounded, visible errors (Web UI instance state + Event Log + audit where applicable) — they are never silently reinterpreted against a different directory than the process CWD dictates. Service deployments are documented as requiring absolute paths for runtimes/models (USER_GUIDE + LIMITATIONS, updated at implementation acceptance per owner decision 4/9).

**Reconciliation result (owner decision 3):** NOT a design blocker — the guarantee is achieved by install-time refusal + documentation with zero runtime semantic change. The one case the existing architecture cannot guarantee (future UI-edited relative paths) is explicitly bounded (D3.4) rather than papered over with new path behavior.

### D4 — Service account: LocalSystem (owner decision 4)

1. **MVP service account is `LocalSystem`** — set explicitly at registration (not left to SCM default inference) and stated in the install output. Any other account (interactive user account, `NetworkService`, managed service accounts) is **out of MVP**.
2. **Documented consequences** (must appear in USER_GUIDE EN/RU + LIMITATIONS at implementation acceptance):
   - **User-profile assumptions are unsafe:** the service has no interactive user profile; `%USERPROFILE%`-style contexts are the LocalSystem context (`C:\Windows\System32`), and `C:\Users\<user>\…` locations are **not** a supported service configuration.
   - **Everything the service touches must be accessible to LocalSystem:** configured files/directories, runtimes, models, `dataDir` (repository + audit), and log locations must grant LocalSystem the required ACLs; access problems surface as bounded startup/launch errors (Web UI + Event Log), not as silent failures.
   - **Network/user-specific resources** (mapped drives, per-user tokens or stored credentials, UAC-virtualized paths) may require different permissions and are **outside MVP** unless already supported by the existing configuration contract.
   - LocalSystem is not subject to UAC and has no desktop session; the Web UI on the configured port is the primary interface, exactly as in headless foreground mode.

### D5 — Start policy (owner decision 5)

- Default service start type: **`auto`** (start on boot; runs the full startup including ADR 010 autostart, exactly as a foreground boot). `--start manual` is offered at install for opt-out.
- **Installation does NOT implicitly start the service.** Install and Start are separate explicit operations: `goal --service install` leaves the service registered and **stopped**; `goal --service start` (or SCM/`services.msc`) starts it. This is stated in the install output and in the docs.

### D6 — SCM lifecycle: start, stop (StopPending / wait hint), stop timeout (owner decisions 1, 6)

1. **Start:** report `StartPending` (checkpoint "starting: application initialization"), then run the **exact existing main sequence** — config load → credential migration → `ValidateFull` → repository init/seed → `Recover()` → ADR 010 autostart → `webui.Run` — under an `appCtx` cancelled by the SCM stop request (replacing the `signal.NotifyContext` source, `main.go:92`). **`Running` is reported to the SCM only after successful application startup and the HTTP server bind** (owner decision 1). A failure at any pre-bind step (bad config, recovery error, port in use) reports `Stopped` with the bounded error and the process exits — the SCM records a start failure; no partial "Running" claim, no false success.
2. **Stop (SCM contract, explicit):** on the SCM stop request the handler returns **`SERVICE_STOP_PENDING`** with **`dwWaitHint` = 30 000 ms** (the existing application shutdown budget) and checkpoint "stopping: instance shutdown and persistence". A dedicated goroutine then executes the **existing, unchanged** shutdown — `appCtx` cancel → `supervisor.ShutdownWithPersistence` (30 s budget, `main.go:132-135`) → audit close (`main.go:138-140`) — and reports **`SERVICE_STOPPED`** only when that path has completed. There is no second stop path: Ctrl+C/foreground and SCM stop converge on the same code. The **existing 30-second application shutdown behavior is unchanged**.
3. **45 s is the SCM adapter's outer stop/wait budget, not a replacement application timeout** (owner decision 6): install registers the SCM **service stop timeout = 45 s** (`SCM StopTimeout` via `ChangeServiceConfig`), strictly greater than the 30 s wait hint + margin, so the application always finishes first — including the Job Object force-kill last resort (ADR 001) — before the SCM can hard-kill. The SCM hard kill remains only for a hung application that misses its own budget (accepted residual, unchanged from foreground).
4. **Interrogate:** returns real state derived from the Supervisor snapshot (instance count per state), not a constant.
5. **`--service start | stop`** are thin SCM operations (mgr open → start / stop + wait) with bounded diagnostics; `status` prints SCM state + PID + uptime (no secrets, no instance detail — instance state is the Web UI's job).

### D7 — Restart: SCM-level, no self-reexec (owner decision 7)

`goal --service restart` is an **SCM-level operation** executed strictly as: **Stop → wait until the SCM reports `Stopped` → Start → wait until the SCM reports `Running`** (bounded waits: stop ≤ the registered 45 s stop timeout; start ≤ the SCM start timeout; a missed bound is a bounded error reporting the observed SCM state). **No self-reexec, no parallel restart mechanism, no Web-UI involvement.** Instance lifecycle across the restart is exactly the normal stop (D6.2) + normal start (D6.1, including ADR 010 autostart) — nothing restart-specific exists.

### D8 — Diagnostics (owner decision 8)

1. The **Windows Event Log** (Application log, source `GoAl`, via `golang.org/x/sys/windows/eventlog` — same module, no new dependency) carries **service/SCM operational diagnostics**: in service mode operational `slog` output goes there; in foreground mode stdout, unchanged.
2. **ADR 007 audit semantics remain unchanged and are not replaced by the Event Log:** `goal_audit.jsonl` (mode 0600, per-event fsync, fail-open, rotation) stays the **only** audit source of truth; audit events are never mirrored into the Event Log (keeps the ADR 007 secret-safety surface exactly as agreed — the Event Log is readable by more principals).
3. `goal --service status` (D6.5) is the console-less health probe; the Web UI remains the primary interface (instance state, logs, ADR 007 audit) in both modes.

### D9 — Existing scripts: single supported mechanism (owner decision 9)

On **implementation acceptance**, `deploy/windows/install-service.ps1` and `deploy/windows/uninstall-service.ps1` are **removed** (they register a service that can never start — forensic item 2 — and every function they had is replaced by the tested binary subcommands). **All documentation references are reconciled in the same change** (USER_GUIDE EN/RU §Windows service, SECURITY.md recommendation, LIMITATIONS.md). The project supports **exactly one** service-management mechanism: `goal --service …`.

### D10 — Updater: untouched (owner decision 10)

`internal/updater` is **not modified, removed, or wired** as part of this ADR's MVP. It remains **explicitly tracked as separate technical debt / future decision** (dedicated ROADMAP P1 debt line), including its `restartService()`/live-binary-overwrite assumptions, which are superseded in direction by D6/D7 but out of scope here.

## Alternatives considered

- **A. Keep/fix the PowerShell scripts (quoted binPath + `--service run --config` args).** Rejected — the structural defect is the binary (no `StartServiceCtrlDispatcher`, forensic item 2e); a fixed script still yields a dead service, and the repair would live outside the tested Go surface (no CI, quoting bugs by construction); owner decision 9 forbids two mechanisms.
- **B. A separate management binary (`goal-service`).** Rejected — violates the single-binary distribution constraint (AGENTS.md); `svc/mgr` is a few imports on a Windows-only file.
- **C. Third-party service libraries or external managers (NSSM-style).** Rejected — external process managers fork the lifecycle away from the binary (the "parallel mechanism" the ROADMAP forbids); `golang.org/x/sys/windows/svc` is already a dependency and is the stdlib-adjacent contract.
- **D. In-service self-update / SCM failure actions wired now.** Rejected — `internal/updater` is dead code and owner decision 10 defers it; update semantics need their own ADR (ROADMAP "Later — Auto-update"); SCM failure-action defaults are left untouched and documented.
- **E. Mirror the audit log into the Event Log for "full" diagnostics.** Rejected (owner decision 8) — widens the ADR 007 secret-safety surface (the Event Log is readable by more principals than the mode-0600 JSONL) for no lifecycle benefit.
- **F. A `--daemonize`/background-attach mode on Linux too.** Rejected — systemd is the Linux contract (`deploy/systemd/goal.service`); a parallel in-binary daemonization would create the second lifecycle owner the project has explicitly avoided.
- **G. Make the service process `chdir` to the config/data directory (or anchor relative paths to the config file).** Rejected (owner decision 3) — that is new path behavior; the existing CWD-relative contract is preserved and made service-safe by the D3 install pre-flight instead.
- **H. Non-LocalSystem service account in MVP (user account / NetworkService / gMSA).** Rejected (owner decision 4) — LocalSystem only; other accounts are a future decision with their own permission-contract review.

## Consequences

### Positive

- The published Windows service workflow finally works: install → auto-start on boot → full GoAl (Web UI, instances, autostart, audit) → graceful stop on shutdown, all from the tested single binary.
- Foreground and service modes share one startup/shutdown sequence — the `d23fe67` headless fix, ADR 005 recovery, ADR 010 autostart, and ADR 007 audit all apply in both modes with zero divergence.
- The registered command and the install pre-flight make the service deterministic: absolute exe, absolute config, absolute effective dataDir, absolute seeded runtime/model paths; the SCM working directory is referenced by no documented behavior.
- Install and uninstall are safe by contract: refuse-before-register with validation; stop-before-delete; user data is never the service's to remove.

### Negative / accepted risk

- Windows-only surface widens the platform test matrix: SCM behavior must be integration-tested on Windows (cross-compilation cannot validate it — ADR 001 precedent); CI on Linux covers compile + stub errors only.
- The Event Log source registration and SCM registration require admin at install time (inherent to SCM registration, same as the old scripts); service mode is unusable below the installed account's rights — documented.
- A service deployment whose config or repository ever contains a relative path (post-install UI edits) fails visibly at startup/launch instead of working — the documented trade for not inventing path semantics (D3.4).
- An SCM hard kill after the 45 s stop timeout leaves the accepted ADR 001/005 residual (orphans/stale, reconciled on next start) — unchanged from foreground Ctrl+C-kill today.
- Removing the two tracked ps1 scripts is a behavior change for anyone who bookmarked them; mitigated by doc updates in the same change and the idempotent re-install path.

## Acceptance contract (must hold before this is considered done)

1. **Registration (objective via `QueryServiceConfig`/`QueryServiceStatus`):** after `goal --service install --config <cfg>` (admin, Windows), the registration contains: binPath **exactly** `"<absEXE>" --service run --config "<absCONFIG>"` per D2 (quotes iff spaces; verified byte-for-byte); account **LocalSystem**; start type **auto** by default (and `manual` when `--start manual`); stop timeout **45 s**; and the service is **Stopped** afterwards — install did not start it (owner decisions 2, 4, 5).
2. **Install refusal (each case: bounded diagnostic, registration absent, zero files created/modified):** missing exe; missing `--config` file; config failing `ValidateFull`; effective `dataDir` missing or relative (e.g. absent field → `./data`); a seeded runtime `executable`/`workingDirectory` or model `path` relative; an existing repository with a relative runtime path. Re-install with an identical image = idempotent no-op success; an existing registration with a different image (exe, config, or arguments) = refused with the bounded diff (owner decisions 2, 3).
3. **Start semantics:** SCM start reaches **`Running` only after the HTTP bind** — `/api/v1/health` answers and `QueryServiceStatus` = Running; the full startup (recovery, ADR 010 autostart) is observable and identical to foreground; a failing config (e.g. port in use) ends in `Stopped` + process exit + SCM start failure, never `Running`.
4. **Stop contract (owner decision 6):** SCM stop on a service with running instances: state sequence `Running → StopPending → Stopped`; while `StopPending`, the reported **wait hint is 30 000 ms**; **all owned instance processes are terminated** (Job Object), repository states persisted terminal, audit file closed; total stop duration ≤ 30 s (the unchanged application budget) and well inside the 45 s SCM timeout; the application shutdown code path is the same one foreground uses (no service-only stop branch).
5. **Unclean-kill residual:** `taskkill /F` of the service process (no graceful stop) → all instance children dead (Job Object kill-on-close) and the **next start reconciles exactly per ADR 005** (transitional instances → `stale`/`orphan`; no "running" zombies; no reattach).
6. **Restart (owner decision 7):** `goal --service restart` performs Stop → wait `Stopped` → Start → wait `Running` (observable via `QueryServiceStatus` polling); on completion autostart semantics re-established the normal start set; a simulated stop that never reaches `Stopped` (e.g. hung instance beyond budget) yields a bounded error with the observed SCM state — **no second GoAl process is ever spawned by restart** (process-count check).
7. **LocalSystem (owner decision 4):** the service runs as LocalSystem (`QueryServiceConfig` `ServiceStartName` = `LocalSystem`); a runtime configured under `C:\Users\<user>\…` (access-denied for LocalSystem) produces a **bounded, visible** startup/launch error in the Web UI + Event Log (no silent failure); USER_GUIDE EN/RU + LIMITATIONS document the D4 consequences (no user-profile assumptions; LocalSystem ACLs required; network/user-specific resources out of MVP).
8. **Diagnostics (owner decision 8):** in service mode, operational slog lines appear in the Event Log (Application, source `GoAl`); `goal_audit.jsonl` is written/rotated/closed **exactly** as in foreground (ADR 007 unchanged); **no audit event appears in the Event Log** (mirror check on a full UI-driven session).
9. **Uninstall (owner decision 9):** `goal --service uninstall` on a running service performs the D6.2 graceful stop **before** deleting the registration; afterwards the SCM registration is gone and **no file under the config/data directory was created, modified, or deleted** by uninstall (repo, config, audit, and log files verified untouched); uninstall of an unknown service prints bounded "not found"; the two `deploy/windows/*-service.ps1` scripts are **removed** and no documentation file references them (repo-wide grep clean; BACKLOG historical mentions excepted).
10. **Portability & gates:** on Linux (and every non-Windows build) every `--service <verb>` returns a bounded `not supported on this platform` error and the binary otherwise behaves exactly as today; `deploy/systemd/goal.service` is unchanged. `gofmt`/`go vet` clean; `go test ./...` and `go test -race ./...` pass; Windows + Linux builds pass; the new platform code is confined to `internal/platform` (+ `cmd/goal` flag wiring) with the SCM behind the `ServiceManager` interface (fake on CI/Linux); items 1–8 are verified by **real-SCM manual acceptance on Windows** recorded per DEVELOPMENT.md.
11. **Updater untouched (owner decision 10):** the diff contains no change to `internal/updater`; the ROADMAP carries a dedicated debt line for it.

## Future work (not in first scope)

- SCM failure actions (auto-restart-on-failure policy, reset period) — needs owner decision alongside the Auto-update ADR.
- `internal/updater`: wiring or removal — separate tracked technical debt / future decision (owner decision 10).
- Non-LocalSystem service accounts (user account / gMSA) with their own permission-contract review (owner decision 4 deferral).
- Service-mode visibility in the Web UI (a "running as service" indicator, `--service status` output in Settings).
- Per-service diagnostics export (event-log tail in the UI) — depends on the persistent-logs roadmap item.

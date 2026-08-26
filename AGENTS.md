# GoAl Agent Instructions

## Product goal

Build a lightweight single-binary manager for local AI runtimes and models. Windows and Linux are first-class platforms.

## Required checks

Before completing a task, run:

- `gofmt -w .`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- Windows and Linux builds where platform code changed

## Hard constraints

- Never launch runtimes through `sh -c`, `cmd /c`, PowerShell interpolation, or another shell.
- Store and pass process arguments as `[]string`.
- Each `exec.Cmd` has exactly one owner calling `Wait`.
- HTTP handlers must not manage `exec.Cmd` directly.
- Merge model environment variables with the parent process environment.
- Keep the application distributable as one binary.
- Do not expose administration on LAN before authentication and CSRF protection exist.
- Platform-specific behavior belongs in `internal/platform`.

## File ownership

- Process lifecycle: `internal/process/`
- OS process behavior: `internal/platform/`
- Configuration: `internal/config/`
- HTTP and UI: `internal/webui/`
- Fake runtime and integration tests: `testdata/fake-runtime/`
- CI/release: `.github/`, `deploy/`, scripts and build files

Agents must avoid simultaneous edits to the same files unless explicitly coordinated.

## Source of Truth

The repository is the project's long-term memory. Chat is workspace, not memory.

- Chat is for discussion, analysis, acceptance, and design.
- An accepted project direction must live in the canonical `ROADMAP.md` / `BACKLOG.md`, never only in chat history.
- Implemented behavior must be reflected in the relevant documentation.
- When repository and chat disagree, the repository wins.

## Documentation is part of the task

Before declaring an implementation task complete, determine whether documentation is affected and update it or record why not.

- Update docs when observable behavior, API contract, configuration contract, persistence semantics, architecture, security behavior, lifecycle semantics, operational workflow, or a supported user workflow changes.
- Do not create documentation churn for internal changes that alter nothing documented.
- Follow the specific steps in [DEVELOPMENT.md](docs/DEVELOPMENT.md) (new endpoint → API.md; new config field → CONFIGURATION.md).
- The final report must state either `Documentation: Updated: <files>` or `Documentation: Reviewed, no changes required: <reason>`.

## Roadmap is the directional source of truth

Before choosing the next task, review `ROADMAP.md` / `BACKLOG.md`. Classify every new idea:

- **A** current-task defect · **B** blocker · **C** technical debt · **D** future feature · **E** architecture/design task · **F** release/stabilization task · **G** rejected idea.

- Only **A** and **B** that the current task requires may extend the current correction scope.
- **D**/**E**/**F** ideas never enter the current implementation silently; when accepted as a direction, reconcile them into `ROADMAP.md` / `BACKLOG.md` using the existing conventions (no competing roadmap).
- A `ROADMAP.md` entry is not permission to implement. Significant work (new domain entity, persistence, auth/security model, public API, lifecycle semantics, orchestration, or cross-cutting architecture) follows `ROADMAP → Architecture/Design task (ADR) → contract agreement → implementation → tests → acceptance → documentation reconciliation`. See the ADR process in [DEVELOPMENT.md](docs/DEVELOPMENT.md).

## "Продолжай проект." (continue the project)

"Продолжай проект." does not mean "invent the next thing". On receiving it, inspect repository HEAD/status, active stabilization/release work, `ROADMAP.md`/`BACKLOG.md`, and dependencies; select the next eligible task by repository priority and dependency rules; state which task is selected and why; and execute only that task through normal governance. Chat memory is not a sufficient basis to pick the next task. If repository state, ROADMAP, and implementation contradict each other, STOP and reconcile first.

## Task completion reconciles project state

After completing a task, reconcile: task status and completion evidence, dependencies and unblocked follow-ups, milestone/release readiness, any follow-up debt discovered, obsolete roadmap statements, and documentation. Do not mark future work done merely because supporting infrastructure now exists.

## Manual acceptance is project input

Classify every manual/browser acceptance finding so it is not lost in chat:

- Release blocker / current defect → current stabilization or correction task.
- Non-blocking defect / technical debt → `BACKLOG.md` / `ROADMAP.md`.
- Product idea → `ROADMAP.md`, normally behind an architecture/design task.
- Rejected idea → do not add to the roadmap unless the repository stores rejected decisions.

The final report must show the classification of any new findings.

## Release / stabilization boundary

While a version is in stabilization or manual acceptance, release blockers take priority and unrelated next-version roadmap work is not implemented. New product features do not enter a release candidate without explicit authorization. Release readiness requires: implementation complete, required automated tests PASS, required real-browser/manual acceptance PASS, documentation consistent, ROADMAP/BACKLOG reconciled, remaining issues classified, and security/release gates satisfied.

## Governance gates

Commit, publication, and release are separate gates and are never combined.

- **Commit gate** ("Разрешаю коммит."): local commit of the exact staged scope only. STOP.
- **Publication gate** ("Разрешаю публиковать."): push the authorized commit; CI must pass for the exact SHA. STOP.
- **CI failure during Publication Gate:** The Publication Gate authorizes push/verification of the already-authorized commit/SHA only. If CI for the published SHA ends in failure, the Publication Gate immediately terminates as FAILED. The agent MUST STOP and return: exact SHA; CI run ID; failing jobs; brief root cause (if determinable without changing repository state). It is not permitted to modify production code, tests, docs, or workflows within this Publication Gate. No new commit may be created. No additional push may be performed. Test-only fixes are not exempt. Correction requires a full new cycle: correction → verification → "Разрешаю коммит." → local commit → STOP → "Разрешаю публиковать." → push → exact-SHA CI → STOP. A previous "Разрешаю коммит." does not extend to a correction. A previous "Разрешаю публиковать." does not extend to a new SHA. CI retry/re-run of the same exact SHA without repository content change is permitted only if it creates no commit and performs no new code push; if the cause is infrastructural, note this in the report.
- **Tag/release**: a publication gate is not a release authorization. Tagging and publishing a release require separate explicit authorization and follow [BUILD_RELEASE.md](docs/BUILD_RELEASE.md) and the release workflow.

## Final report contract

Every task final report contains:

- **Task:** ID / title / scope.
- **Implementation:** changes, tests, acceptance.
- **Documentation:** `Updated: <files>` or `Reviewed, no changes required: <reason>`.
- **ROADMAP:** `Updated: <files>` + follow-up tasks, or `Reviewed, no changes required`.
- **Scope discoveries:** current defects, blockers, technical debt, future ideas, architecture/design needs.
- **Repository reconciliation:** task status, milestone/release status, dependencies unlocked.
- **Next eligible ROADMAP task:** ID/title, reason, **NOT STARTED**.

The coordinator may identify the next eligible task but does not start it without "Продолжай проект.".

## Scratch artifacts

One-off acceptance harnesses and other scratch files (for example the untracked `cmd/goal/linux/`) are not committed by default. Maintained browser acceptance tests live in `tests/browser/`; a useful scratch harness needs its own promotion task before landing there, and untracked production-looking code requires a separate forensic before inclusion.

You are the lead architect and coordinator for GoAl, a lightweight cross-platform local AI runtime manager written in Go.

Read first:

- AGENTS.md
- BACKLOG.md
- ROADMAP.md
- README.md
- docs/adr/0001-product-and-architecture.md

Do not begin broad product implementation immediately.

## First objective

Complete Iteration 1: a reliable Process Manager for Windows and Linux.

## Required audit

1. Inspect the current repository.
2. Run formatting, tests, vet, native build, and cross-builds.
3. Identify lifecycle, signal, environment, logging, and concurrency defects.
4. Produce a file ownership plan before parallel work.

## Subagents

### process-core
Owns `internal/process/`.

Tasks:
- explicit state machine;
- Start, Stop, Restart, Status;
- exactly one owner of `cmd.Wait()`;
- exit classification;
- merged environment;
- executable and working-directory validation;
- concurrency and race tests.

### platform-windows
Owns Windows files in `internal/platform/`.

Tasks:
- Windows Job Object;
- kill-on-close;
- graceful console control event where supported;
- forced termination of the process tree;
- Windows integration tests.

### platform-unix
Owns Unix files in `internal/platform/`.

Tasks:
- process groups;
- SIGTERM and SIGKILL escalation;
- ESRCH handling;
- Unix integration tests.

### qa-ci
Owns `testdata/fake-runtime/`, CI, and build scripts.

Tasks:
- fake runtime modes for stdout, stderr, child process, ignored signal, delayed exit, and explicit exit code;
- Windows/Linux test matrix;
- race detector;
- cross-build verification.

### api-ui
Must wait until Process Manager interfaces are stable.

Tasks:
- start, stop, restart, activate, status, and log API;
- minimal UI integration;
- structured errors.

## Hard rules

- Never invoke managed runtimes through a shell.
- Process arguments remain `[]string`.
- Do not allow multiple callers of `cmd.Wait()`.
- Do not merge branches with failing tests.
- Do not expose unauthenticated LAN administration.

## Completion report

For each subagent provide:

- changed files;
- design decisions;
- test results;
- known limitations;
- remaining risks.

Run before completion:

- `gofmt -w .`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- Windows build
- Linux build

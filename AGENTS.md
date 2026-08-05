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
- Merge profile environment variables with the parent process environment.
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

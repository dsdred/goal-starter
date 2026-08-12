# Development

This document describes the development workflow for GoAl contributors.

## Prerequisites

- Go 1.22+ (recommended)
- GCC (for race detector: `CGO_ENABLED=1`)
- PowerShell 5.1+ (Windows) or bash (Linux)

## Clone and setup

```bash
git clone https://github.com/dsdred/goal-starter.git
cd goal-starter
go mod download
```

## Required checks

Run these before every commit:

```bash
gofmt -w .
go test ./...
go vet ./...
go build ./cmd/goal
```

## Race detector

```bash
CGO_ENABLED=1 go test -race ./...
```

Requires GCC on Windows. On Linux, the race detector is part of the standard toolchain.

**CI:** Race detector runs on Linux in GitHub Actions. Windows race detector is also in CI matrix.

## File ownership

| Area | Package |
|------|---------|
| Process lifecycle | `internal/process/` |
| OS process behavior | `internal/platform/` |
| Configuration | `internal/config/` |
| HTTP and UI | `internal/webui/` |
| Fake runtime / integration tests | `testdata/fake-runtime/` |
| CI / release | `.github/`, `deploy/`, scripts |

Avoid simultaneous edits to the same files unless coordinated.

## Testing

### Unit tests

```bash
go test ./...
```

### Handler tests

```bash
go test ./internal/webui/handlers/...
```

### Process tests

```bash
go test ./internal/process/...
```

### Storage tests

```bash
go test ./internal/storage/...
```

## Build

### Local build

```bash
go build -o goal ./cmd/goal
```

### Cross-compile

```powershell
# Windows
$env:GOOS='windows'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; go build -o bin/goal-windows-amd64.exe ./cmd/goal

# Linux
$env:GOOS='linux'; $env:GOARCH='amd64'; go build -o bin/goal-linux-amd64 ./cmd/goal
```

## Project structure

| Path | Purpose |
|------|---------|
| `cmd/goal/` | Main entry point |
| `cmd/goal-msi/` | MSI installer builder |
| `internal/config/` | Config parsing, validation, hot-reload, migrations |
| `internal/process/` | Process lifecycle, log store, broker |
| `internal/platform/` | OS-specific process handling |
| `internal/storage/` | JSON repository (authoritative persistence) |
| `internal/domain/` | Domain types, DTO converters |
| `internal/application/` | Business logic services |
| `internal/webui/` | HTTP server, handlers, embedded UI, security |
| `testdata/fake-runtime/` | Fake runtime for integration tests |
| `deploy/` | Systemd services, Windows service scripts |
| `scripts/` | Build and bootstrap scripts |

## Code conventions

- Never launch runtimes through `sh -c`, `cmd /c`, PowerShell interpolation, or another shell.
- Process arguments are `[]string`.
- Each `exec.Cmd` has exactly one owner calling `Wait()`.
- HTTP handlers must not manage `exec.Cmd` directly.
- Merge profile environment variables with parent process environment.
- Platform-specific behavior belongs in `internal/platform/`.

## Adding a new endpoint

1. Define handler method in appropriate `*Handler` in `internal/webui/handlers/`.
2. Register route in `routes.go:Build()`.
3. Add `requireAuth` or `requireAuthCSRF` wrapper as needed.
4. Write unit test in `_test.go` file in same package.
5. Add to [API.md](API.md).

## Adding a new config field

1. Add field to `Config` struct in `internal/config/config.go`.
2. Update `Default()` if it has a default value.
3. Update `Validate()` if validation is needed.
4. Add migration step in `migrateV1ToV2()` (or new migration function) if migrating from existing version.
5. Update [CONFIGURATION.md](CONFIGURATION.md).

## ADR process

New architectural decisions should follow the existing ADR format in `docs/adr/`:

```markdown
# ADR XXX: Title

**Status:** Draft | Proposed | Accepted | Superseded
**Date:** YYYY-MM-DD
**Related:** ADR XX

## Context

## Decision

## Consequences
```

Statuses:
- **Draft** — under discussion
- **Proposed** — decision made, implementation pending
- **Accepted** — implemented or being implemented
- **Superseded** — replaced by a later ADR

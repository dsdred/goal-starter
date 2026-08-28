# ADR 003: Web UI Serving via Embedded FS

**Status:** Accepted (decision 1 implemented; decision 2 withdrawn — see implementation note)
**Date:** 2026-08-10
**Related:** ADR 001 (Single binary), ADR 002 (Supervisor)

## Context

GoAl aims to be a single-binary distribution (ADR 001). The current implementation declares embedded FS resources:

```go
//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS
```

However, the actual HTTP handlers do **not** use these embedded assets:

- `SystemHandler.ServeIndex` writes status 200 with an empty body (no template parse/execution).
- Static files are served from disk via `http.FileServer(http.Dir("./web/static"))`.
- The `webui/static/` and `web/static/` directories are checked into the repository, both as duplicates and as source for the dev file server.
- As a result, the compiled binary is **not** self-contained: moving the binary to a different directory breaks the web UI.

This is a documented but broken behavior, not a merely theoretical mismatch.

## Decision

1. **Use embedded FS as the primary source** for the web UI.
   - `ServeIndex` must parse and render `templates/index.html` from `templateFS`.
   - Static assets must be served from `staticFS` at `/static/`.
   - Binary must work independently of the working directory.

2. **Optional on-disk override for development**.
   - `web/` disk assets are consulted only when an explicit `--dev-assets` flag or `GOAL_DEV_ASSETS=1` env var is set.
   - Production path defaults to embedded only.

3. **Handler contract**.
   - `GET /` returns the rendered dashboard HTML.
   - `GET /static/...` returns static content with appropriate `Content-Type`.
   - Missing assets return 404; index render failure returns 500 with structured error.

## Consequences

### Positive
- Single binary promise becomes true.
- No working directory dependency.
- Dev mode remains possible for hot-reload of UI files.

### Negative
- Template parse errors surface at runtime; mitigated by unit test that asserts the embedded template parses and renders.

## Out of scope

- Template caching / hot reload inside the binary.
- Advanced asset pipelines (minification, compression).

## Implementation note (2026-08-28)

- Decision 1 (embedded FS as the primary source, working-directory independence) shipped in v1.0.0 via TASK-WEBUI-001: `templateFS`/`staticFS` in `internal/webui/server.go`, real `ServeIndex` render, `/static/` served from `staticFS`.
- Decision 2 (optional on-disk override via `--dev-assets` / `GOAL_DEV_ASSETS`) was **never implemented** and is now **withdrawn**: no code path consults disk assets. The duplicate UI copies — root `webui/` (tracked, stale v0.8 snapshot, referenced nowhere) and root `web/` (untracked, empty) — were removed on 2026-08-28 (ROADMAP P1 technical-debt item). UI changes are made directly in `internal/webui/` and verified by the maintained browser-acceptance suite (`tests/browser/`); a dev-override mechanism can be re-proposed later if hot-reload of UI files becomes a real need.
- The Context section above is a point-in-time problem statement from 2026-08-10.

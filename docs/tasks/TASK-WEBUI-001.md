# TASK: Fix Web UI Serving via Embedded FS

**ID:** TASK-WEBUI-001
**Priority:** P0 (broken documented behavior)
**Size:** Bounded (single architectural issue, one user workflow)

## Goal

Make the GoAl web dashboard actually usable from the compiled binary, without requiring the working directory to contain `web/` or `webui/` assets.

## Problem

`ServeIndex` currently returns HTTP 200 with an **empty body**, and `/static/*` files are served from disk (`./web/static`). The advertised "single binary with embedded web assets" behavior (ADR 001, README) is broken:

- `templateFS` and `staticFS` are declared but unused.
- Starting the binary from `$TEMP` or after install results in a blank dashboard and 404 for `/static/app.js`.
- Verified locally: content length `0` for `/`, 404 for `/static/app.js` when the working directory lacks `web/static`.

## Current behavior

- `GET /` → HTTP 200, empty body.
- `GET /static/app.js` → 404 unless `./web/static/app.js` happens to exist on disk.

## Desired behavior

- `GET /` → rendered dashboard HTML from the embedded template.
- `GET /static/app.js` and `GET /static/style.css` → served from embedded FS.
- All responses work regardless of working directory.
- Tests cover the embedded-asset contract.

## In scope

- `internal/webui/server.go`: pass template/static FS into `SystemHandler` (or implement serving in server.go).
- `internal/webui/handlers/system.go`: implement real `ServeIndex` using embedded template, real static serving.
- Remove or gate disk fallback for static assets.
- Template parse and handler unit tests.
- E2E smoke verification on Windows (binary from different working directory).

## Out of scope

- SSE logs streaming (`/api/v1/logs/stream`) — separate task.
- WebSocket `/ws` endpoint — already separate package.
- Auth UI flow changes.
- Template redesign.

## Architecture constraints

- Single binary (ADR 001): embedded FS is production source of truth.
- Platform-specific behavior stays in `internal/platform`.
- No shell invocation for runtime management.
- Handler changes confined to `internal/webui/handlers/` and `internal/webui/server.go`.

## Acceptance criteria

1. `go build ./cmd/goal` succeeds.
2. `go test ./internal/webui/...` passes, including new template/static handler tests.
3. `curl http://127.0.0.1:<port>/` returns non-empty HTML containing dashboard markup.
4. `curl http://127.0.0.1:<port>/static/app.js` returns JavaScript content, not 404.
5. Verified from a clean working directory without `web/` or `webui/` assets.
6. `gofmt -l .`, `go vet ./...` clean.
7. Existing tests continue to pass (including process supervisor, config seed, storage).

## Verification matrix

| Verification | Method |
|--------------|--------|
| Template parses | Go unit test with `template.ParseFS` |
| Static serving | `httptest.NewRecorder` against `SystemHandler` |
| Binary independence | Build → run from temp dir → HTTP assertions |
| Regression | `go test ./...` full suite |
| Cross-compile | `GOOS=linux go build ./cmd/goal` |

## Documentation impact

- `README.md` / `README_RU.md` already describe the intended behavior — no change needed once the behavior matches docs.
- `docs/adr/003-webui-embedded-fs.md` is created as the design record.

## Risks

- `template.ParseFS` naming conflict: `index.html` defines a template named `index.html`; the handler must execute the correct template name.
- Content-Type detection for static files must remain standard Go behavior.
- Existing tests in `internal/webui/handlers/` may rely on the stubbed `ServeIndex`; tests will be updated to expect real rendering.

## Rollback/compatibility

- No breaking API changes.
- `web/` and `webui/` directories remain in the repository for development but are no longer required at runtime.
- If problems arise, reverting to disk-based static serving is a one-line code change (but would re-break the single-binary contract).

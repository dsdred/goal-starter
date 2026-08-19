# GoAl

GoAl is a lightweight, single-binary manager for local AI runtimes and models. One executable for Windows amd64 and Linux amd64.

**Latest stable: v1.0.3** — [GitHub Releases](https://github.com/dsdred/goal-starter/releases/tag/v1.0.3)

## Key features

- **Runtime CRUD** — configure Ollama, llama.cpp, vLLM, or custom inference servers
- **Model management** — GGUF files, inline arguments, environment variables
- **Model-based launches** — combine Runtime + launch args
- **Multi-instance supervisor** — run several processes concurrently with configurable concurrency limits
- **Live logs** — SSE streaming with instance filtering and pagination
- **Historical logs** — paginated, searchable query with time-range and stream filters
- **Preview / Resolve** — see the resolved command before starting
- **Embedded Web UI** — single-file dashboard with authentication and CSRF protection
- **Atomic JSON persistence** — tmp + rename + backup recovery, no external database
- **Conservative recovery** — stale instance detection on restart

## Quick start

```powershell
.\scripts\bootstrap-windows.ps1
Copy-Item goal.example.json goal.json
$env:GOAL_CONFIG = (Resolve-Path .\goal.json)
go run .\cmd\goal
```

Then open **http://127.0.0.1:9090** in your browser.

## Minimal configuration

```json
{
  "version": 2,
  "listenAddress": "127.0.0.1",
  "webPort": 9090,
  "dataDir": "./data"
}
```

No runtimes or models are required to start. The Web UI lets you configure everything.

## Platforms

| Platform | Architecture | Status |
|----------|-------------|--------|
| Windows  | amd64       | Production |
| Linux    | amd64       | Production |
| Linux    | arm64       | Planned |

## Windows SmartScreen note

GoAl Windows releases are currently **not code-signed**. The publisher may appear as "Unknown Publisher", and Windows SmartScreen or Microsoft Defender may show a warning when you first run a downloaded release. This is expected for the current distribution method, not a GoAl bug. If you downloaded a release from the official [GitHub Releases](https://github.com/dsdred/goal-starter/releases) page and verify its SHA-256 against the `checksums.txt` in that release, you can choose to run the app (for example via "More info" → "Run anyway"). Code signing is a possible future improvement.

## Security

- HTTP-only session cookies, bcrypt password hashing
- CSRF protection for all unsafe methods
- Default bind: `127.0.0.1`
- `authEnabled=false` rejected for non-loopback addresses
- **Public mode warning:** if authentication is disabled and GoAl is bound to `0.0.0.0`, all API endpoints are accessible without credentials.
> Full reference: [SECURITY.md](docs/SECURITY.md)

## Known limitations

- No PID reattachment after GoAl restart (instances marked as `stale`)
- SSE is the authoritative live-log transport; WebSocket is implemented but not wired
- TCP HealthChecker results stored internally, not exposed as a separate public API
> Full reference: [LIMITATIONS.md](docs/LIMITATIONS.md)

## Build from source

```powershell
.\scripts\build-all.ps1
# Produces: bin/goal-windows-amd64.exe, bin/goal-linux-amd64, bin/checksums.txt
```

```powershell
go test ./...
go test -race ./...   # requires CGO_ENABLED=1 and gcc
go vet ./...
```

## Documentation

- [User Guide](docs/USER_GUIDE.md) — download, configure, run, Web UI walk-through
- [Configuration Reference](docs/CONFIGURATION.md) — all goal.json options
- [API Reference](docs/API.md) — every production endpoint
- [Architecture](docs/ARCHITECTURE.md) — process lifecycle, storage, design decisions
- [Development](docs/DEVELOPMENT.md) — build, test, release workflow

# Build & Release

This document describes the release process for GoAl.

## Release artifacts

| Artifact | Platform | Architecture |
|----------|----------|--------------|
| `goal-windows-amd64.exe` | Windows | amd64 |
| `goal-linux-amd64` | Linux | amd64 |
| `checksums.txt` | — | SHA256 checksums for all binaries |

**Tag:** `v1.0.0`
**Release:** [GitHub Releases](https://github.com/dsdred/goal-starter/releases/tag/v1.0.0)

## Build script

`scripts/build-all.ps1` produces all artifacts:

```powershell
.\scripts\build-all.ps1
```

Output:
- `bin/goal-windows-amd64.exe`
- `bin/goal-linux-amd64`
- `bin/checksums.txt`

The script also creates release archives:
- `releases/goal-<version>-windows-amd64.zip`
- `releases/goal-<version>-linux-amd64.tar.gz`

## Build verification

```powershell
gofmt -w .
go test ./...
go test -race ./...   # CGO_ENABLED=1 + gcc
go vet ./...
go build ./cmd/goal
```

**CI:** GitHub Actions runs lint, build (Windows+Linux), test with race detector, and govulncheck. See `.github/workflows/ci.yml`.

## Tag convention

Semantic versioning: `vMAJOR.MINOR.PATCH`

- `v1.0.0` — first stable release
- Future: `v1.x.y` for patch releases, `v2.0.0` for breaking changes

## Release process

1. Ensure all tests pass: `go test ./... && go test -race ./...`
2. Ensure all required checks pass: `gofmt -w . && go vet ./...`
3. Build for all targets: `.\scripts\build-all.ps1`
4. Verify checksums: `Get-FileHash` (Windows) or `sha256sum` (Linux)
5. Create tag: `git tag vX.Y.Z`
6. Push tag: `git push origin vX.Y.Z`
7. Create GitHub Release with:
   - Title: `GoAl vX.Y.Z`
   - Notes: release changelog
   - Attachments: `goal-windows-amd64.exe`, `goal-linux-amd64`, `checksums.txt`
8. Upload release archives (ZIP/TAR.GZ)

## Release verification

```powershell
# Download and verify
Get-FileHash bin/goal-windows-amd64.exe -Algorithm SHA256
# Compare with checksums.txt from release
```

```bash
sha256sum -c checksums.txt
```

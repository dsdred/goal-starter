# Build & Release

This document describes the release process for GoAl.

## Release artifacts

| Artifact | Platform | Architecture |
|----------|----------|--------------|
| `goal-windows-amd64.exe` | Windows | amd64 |
| `goal-linux-amd64` | Linux | amd64 |
| `checksums.txt` | — | SHA256 checksums for all binaries |

**Latest published tag:** `v1.0.0`
**Release:** [GitHub Releases](https://github.com/dsdred/goal-starter/releases/tag/v1.0.0)

## Windows Authenticode signing

Windows release binaries are signed only when a signing certificate is configured. Unsigned local release builds are supported and are explicitly identified as unsigned in the generated Windows archive metadata.

- **Signing certificate** stored in GitHub Secrets (`GOAL_SIGN_CERT`, `GOAL_SIGN_CERT_PASSWORD`)
- **Timestamp server** configured via `GOAL_SIGN_TIMESTAMP_SERVER` (default: DigiCert)
- **Checksums** generated AFTER signing (signing modifies the PE binary)
- **Signature verification** always inspects the final binary; a requested signing operation fails unless the final status is `Valid`
- **Archive metadata** records the actual Authenticode, publisher, and timestamp state of the included binary

### How signing works in the pipeline

1. Windows binary is built with embedded PE metadata (ProductName, FileDescription, etc.)
2. If a certificate is configured, the binary is signed with `signtool.exe`
3. A trusted timestamp is requested for signed builds
4. The final binary is inspected with `Get-AuthenticodeSignature`
5. Requested signing must produce `Status = Valid`; otherwise packaging fails
6. Without a certificate, the expected state is `NotSigned`
7. SHA256 checksums are generated from the final binary bytes
8. `RELEASE.txt` is generated from the actual signature result and validated against the archived binary

### SmartScreen note

Valid Authenticode signature ≠ guaranteed immediate SmartScreen reputation.

After a new certificate is issued:
- Windows will show the publisher name (no longer "Unknown Publisher")
- SmartScreen may still show "Unknown app" or "Downloaded from Internet" for the first few thousand downloads
- Reputation builds over time (downloads, opens, network signals)
- An EV certificate accelerates reputation but does not guarantee instant SmartScreen clearance

## Windows resource metadata

GoAl embeds Windows PE version resources at build time using `go-winres`.

Resource configuration: `winres/winres.json`

Generated `.syso` files are embedded into the Windows binary during `go build`.

## Local build script

`scripts/build-all.ps1` produces all artifacts:

```powershell
.\scripts\build-all.ps1
```

For a signed build:

```powershell
$env:SIGN_CERT = "C:\certs\goal.pfx"
$env:SIGN_PASSWORD = "your_password"
$env:SIGN_TIMESTAMP = "http://timestamp.digicert.com"
.\scripts\build-all.ps1 -ReleaseVersion vX.Y.Z
```

Output:
- `bin/goal-windows-amd64.exe` (signed if SIGN_CERT provided)
- `bin/goal-linux-amd64`
- `bin/checksums.txt`

The script also creates release archives:
- `releases/goal-<version>-windows-amd64.zip`
- `releases/goal-<version>-linux-amd64.tar.gz`

## Developer build

Normal `go build` does not require a signing certificate or `go-winres`:

```powershell
go build ./cmd/goal
```

Developer builds:
- Unsigned
- Version = `dev`
- No PE metadata (unless winres is configured)

## Build verification

```powershell
gofmt -w .
go test ./...
go test -race ./...   # CGO_ENABLED=1 + gcc
go vet ./...
go build ./cmd/goal
```

**CI:** GitHub Actions runs lint, build (Windows+Linux), test with race detector, and govulncheck. See `.github/workflows/ci.yml`.

**Release CI:** `.github/workflows/release.yml` triggers on tag push. Runs tests, builds, signs Windows binary (if certificate available), verifies signature, generates checksums, and publishes release.

## Tag convention

Semantic versioning: `vMAJOR.MINOR.PATCH`

- `v1.0.0` — first stable release
- Future: `v1.x.y` for patch releases, `v2.0.0` for breaking changes

## Release process

1. Ensure all tests pass: `go test ./... && go test -race ./...`
2. Ensure all required checks pass: `gofmt -w . && go vet ./...`
3. Create tag: `git tag vX.Y.Z`
4. Push tag: `git push origin vX.Y.Z`
5. GitHub Actions release workflow runs automatically:
   - Tests, build, sign Windows, verify signature
   - Generate checksums (post-signing)
   - Publish release with all assets
6. Verify the published release:
   - Check signature: `Get-AuthenticodeSignature .\goal-windows-amd64.exe`
   - Verify checksums: `Get-FileHash` vs `checksums.txt`

## Release verification

```powershell
# Download from GitHub Release

# Verify SHA256
Get-FileHash bin/goal-windows-amd64.exe -Algorithm SHA256
# Compare with checksums.txt from release

# Verify Authenticode signature (Windows release only)
Get-AuthenticodeSignature .\goal-windows-amd64.exe
```

Expected output for a signed build:

```
SignerCertificate : ...
Status            : Valid
StatusMessage     : Signature verified successfully.
Path              : goal-windows-amd64.exe
```

Expected status for an unsigned build:

```text
Status            : NotSigned
```

```bash
sha256sum -c checksums.txt
```

## GitHub Secrets

Required secrets for release (configured in repository Settings → Secrets):

| Secret | Description |
|--------|-------------|
| `GOAL_SIGN_CERT` | Base64-encoded PFX certificate or path to cert in CI |
| `GOAL_SIGN_CERT_PASSWORD` | PFX password |
| `GOAL_SIGN_TIMESTAMP_SERVER` | Timestamp server URL (optional, defaults to DigiCert) |

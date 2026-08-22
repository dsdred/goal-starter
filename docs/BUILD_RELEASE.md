# Build & Release

This document describes the release process for GoAl.

## Release artifacts

| Artifact | Platform | Architecture |
|----------|----------|--------------|
| `goal-windows-amd64.exe` | Windows | amd64 |
| `goal-linux-amd64` | Linux | amd64 |
| `goal-vX.Y.Z-windows-amd64.zip` | Windows | amd64 archive |
| `goal-vX.Y.Z-linux-amd64.tar.gz` | Linux | amd64 archive |
| `checksums.txt` | — | SHA256 checksums for the two release archives |

The GitHub Release asset set is exactly these five files. Empty-version names such as `goal--windows-amd64.zip` are invalid and must never be published.

**Latest published tag:** `v2.0.0`
**Release:** [GitHub Releases](https://github.com/dsdred/goal-starter/releases/tag/v2.0.0)

## Windows Authenticode signing (capability, not currently active)

Windows release binaries are currently **unsigned**. No signing certificate is configured in the release pipeline. All published releases (v1.0.0–v2.0.0) shipped with `Status: NotSigned`.

The build script (`scripts/build-all.ps1`) includes a signing code path that activates when `SIGN_CERT` and `SIGN_PASSWORD` environment variables are set. This capability exists for when a certificate becomes available, but is not currently in use.

When a certificate IS configured:
- **Signing certificate** loaded from `SIGN_CERT` (base64 PFX) and `SIGN_PASSWORD`
- **Timestamp server** configured via `SIGN_TIMESTAMP` (default: DigiCert)
- **Checksums** generated AFTER signing (signing modifies the PE binary)
- **Signature verification** always inspects the final binary; a requested signing operation fails unless the final status is `Valid`
- **Archive metadata** records the actual Authenticode, publisher, and timestamp state of the included binary

### How the signing code path works

1. Windows binary is built with embedded PE metadata (ProductName, FileDescription, etc.)
2. If `SIGN_CERT` is set, the binary is signed with `signtool.exe`
3. A trusted timestamp is requested for signed builds
4. The final binary is inspected with `Get-AuthenticodeSignature`
5. Requested signing must produce `Status = Valid`; otherwise packaging fails
6. Without a certificate (current state), the expected state is `NotSigned`
7. SHA256 checksums are generated from the final binary bytes
8. `RELEASE.txt` is generated from the actual signature result and validated against the archived binary

### SmartScreen note

Because releases are currently unsigned, Windows SmartScreen may show a warning ("Unknown Publisher") when users first run a downloaded binary. This is expected. Users should verify SHA-256 against `checksums.txt` from the official GitHub Release before running. Authenticode signing is a possible future improvement.

## Windows resource metadata

GoAl embeds Windows PE version resources at build time using `go-winres`.

Resource configuration: `winres/winres.json`

Generated `.syso` files are embedded into the Windows binary during `go build`.

## Local build script

`scripts/build-all.ps1` produces all artifacts:

```powershell
.\scripts\build-all.ps1 -ReleaseVersion vX.Y.Z
```

`ReleaseVersion` is required and must use the `vMAJOR.MINOR.PATCH` format (an optional prerelease suffix is supported). Empty, whitespace-only, or malformed versions fail before build or packaging.

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
- `releases/goal-vX.Y.Z-windows-amd64.zip`
- `releases/goal-vX.Y.Z-linux-amd64.tar.gz`
- `releases/checksums.txt`

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

# Check Authenticode status (Windows release only)
Get-AuthenticodeSignature .\goal-windows-amd64.exe
```

Expected status for the current unsigned release:

```text
Status            : NotSigned
```

If a future release is signed, the expected output would be:

```
SignerCertificate : ...
Status            : Valid
StatusMessage     : Signature verified successfully.
Path              : goal-windows-amd64.exe
```

```bash
sha256sum -c checksums.txt
```

## GitHub Secrets

Optional secrets for the (currently unused) signing code path. None are currently configured:

| Secret | Description | Status |
|--------|-------------|--------|
| `GOAL_SIGN_CERT` | Base64-encoded PFX certificate | Not set |
| `GOAL_SIGN_CERT_PASSWORD` | PFX password | Not set |
| `GOAL_SIGN_TIMESTAMP_SERVER` | Timestamp server URL | Not set (defaults to DigiCert if used) |

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

**Latest published tag:** `v2.3.0` (tag → commit `0811bb71128fea36f1d99720db9fb66271135f00`, release workflow run `45456730986` attempt 1, published 2026-11-24T18:15:24Z; release body is the tracked RU notes `docs/releases/v2.3.0-ru.md` fed via `body_path` at creation; previous release `v2.2.0` → `1e55064`, run `43930538485`, 2026-11-16)

**Bilingual release notes contract (Owner decision 2026-11-25, from v2.3.0; MANDATORY standard for every GoAl release — an agent preparing the next release MUST follow this contract):** GoAl v2.3.0 is the reference release for this format: [https://github.com/dsdred/goal-starter/releases/tag/v2.3.0](https://github.com/dsdred/goal-starter/releases/tag/v2.3.0). Every release tag carries two tracked note files — `docs/releases/<tag>-en.md` (English, primary) and `docs/releases/<tag>-ru.md` (Russian, secondary) with semantic parity (release overview; features; improvements; fixes; commands; resource monitoring; supported platforms; known limitations; installation and upgrade with the release artifact list and SHA256 checksums; acceptance and quality check results). The section structure adapts to the actual content of each release: changes, test results, and limitations are never invented to fill a section, and planned features are never advertised as implemented. The notes' factual accuracy (version, commits, CI runs, asset digests, acceptance statements) MUST be verified against tracked evidence — git history, CI runs, acceptance records — before publication. The release workflow validates **both** files before any GitHub Release creation (missing, empty, or placeholder-only notes fail the release) and generates a single combined body (English section first, Russian section second) — `English | Русский` navigation with working Markdown anchors, then a `## English` section, a `---` separator, and a `## Русский` section (UTF-8, no BOM) — fed to the Release via softprops `body_path` **at creation, never post-hoc**. The already-published v2.3.0 body (RU-only) and all previous releases remain unchanged; updating the v2.3.0 remote body is a separate Owner decision. **2026-11-25 correction (supersedes the preceding sentence):** the v2.3.0 remote body was updated by an authorized body-only PATCH to the sealed bilingual artifact. Owner manual acceptance then FAILED on the language navigation: the release-body renderer emits **no heading anchor ids**, so the `#english`/`#русский` Markdown fragments could not resolve. The workflow's generated body therefore uses explicit HTML anchors — `<a id="english"></a>` / `<a id="russkiy"></a>` (ASCII ids, stable across releases) placed immediately before each language section, with navigation hrefs `#user-content-english` / `#user-content-russkiy`: GitHub's release-body renderer preserves explicit HTML anchor elements but prefixes their rendered ids with `user-content-` (verified in the rendered DOM on 2026-11-25). A compact `[↑ English]` return link is emitted at the top of the Russian section. The corrected combined body is to be applied to the published v2.3.0 by a separate Owner-authorized body-only PATCH. All previous releases remain unchanged. **2026-11-25 (closure):** the corrected body was applied by the authorized second body-only PATCH — the published v2.3.0 body is now the nav-fixed combined body (sha256 `b33d1d0171ce830e5827f0b82f913b77df1df7bc49ce043093dd76eaa9b4ed19`, 30 943 B; release id, tag, publication date and all five assets unchanged) and the Owner manual acceptance of the published page PASSED (3/3 navigation links, both sections correct). The first production execution of the tag-triggered bilingual workflow remains the next release tag; the `user-content-` id prefix is an undocumented GitHub convention and must be re-verified then.
**Release:** [GitHub Releases](https://github.com/dsdred/goal-starter/releases/tag/v2.3.0)

## Windows Authenticode signing (capability, not currently active)

Windows release binaries are currently **unsigned**. No signing certificate is configured in the release pipeline. All published releases (v1.0.0–v2.3.0) shipped with `Status: NotSigned`.

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
3. Author and commit the bilingual release notes **before** tagging: `docs/releases/vX.Y.Z-en.md` (English, primary) and `docs/releases/vX.Y.Z-ru.md` (Russian, secondary), with the semantic parity required by the Bilingual Release Notes contract above (release overview; features; improvements; fixes; commands; resource monitoring; supported platforms; known limitations; installation and upgrade with the release artifact list and SHA256 checksums; acceptance and quality check results); the structure adapts to the release's actual content — never invent changes, test results, or limitations to fill a section, and never advertise planned features as implemented; verify the notes' factual accuracy against tracked evidence
4. Create tag: `git tag vX.Y.Z`
5. Push tag: `git push origin vX.Y.Z`
6. GitHub Actions release workflow runs automatically:
   - Validates both note files (missing, empty, or placeholder-only notes fail the release)
   - Generates the combined bilingual body (`## English` / `## Русский`) used as the Release body at creation; language navigation is explicit HTML anchors (`<a id="english"></a>` / `<a id="russkiy"></a>`) with `#user-content-…` hrefs — the release-body renderer emits no heading anchor ids (see the bilingual contract above)
   - Tests, build, sign Windows, verify signature
   - Generate checksums (post-signing)
   - Publish release with all assets
7. Verify the published release:
   - Check signature: `Get-AuthenticodeSignature .\goal-windows-amd64.exe`
   - Verify checksums: `Get-FileHash` vs `checksums.txt`
   - In a real browser, click both language navigation links (top nav and the `[↑ English]` return link) and confirm the page scrolls to the matching section — static Markdown inspection is not sufficient

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

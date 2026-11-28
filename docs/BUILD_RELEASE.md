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

Output (per-build ownership layout, BUILD-ARTIFACTS-01, 2026-11-28): every invocation creates its own unique, collision-safe build root under `build/` and writes only there:

- `build/vX.Y.Z-<yyyyMMdd-HHmmss>-<unique-id>/bin/goal-windows-amd64.exe` (signed if SIGN_CERT provided)
- `build/vX.Y.Z-<yyyyMMdd-HHmmss>-<unique-id>/bin/goal-linux-amd64`
- `build/vX.Y.Z-<yyyyMMdd-HHmmss>-<unique-id>/bin/checksums.txt`
- `build/vX.Y.Z-<yyyyMMdd-HHmmss>-<unique-id>/releases/goal-vX.Y.Z-windows-amd64.zip`
- `build/vX.Y.Z-<yyyyMMdd-HHmmss>-<unique-id>/releases/goal-vX.Y.Z-linux-amd64.tar.gz`
- `build/vX.Y.Z-<yyyyMMdd-HHmmss>-<unique-id>/releases/checksums.txt`

The script emits `OUTDIR=<absolute build root>` exactly once to the **success stream** (`Write-Output`), so CI passes the exact root of THIS invocation to its verification and upload steps (no newest-directory discovery). The emission must stay on the success stream: the release workflow captures it in-pipeline (`@(& .\scripts\build-all.ps1 … 4>&1 | …)`), and console emission (`[Console]::Out` / `Write-Host`) bypasses that pipeline under Windows PowerShell 5.1 (Q2 root cause, 2026-11-28).

**Build ownership rules:**
- A build only ever creates, modifies or deletes paths inside its own root (plus the `cmd/goal/rsrc.syso` it generated; a pre-existing `rsrc.syso` is preserved byte-for-byte and restored after the build). Legacy `bin/` and `releases/`, previous build roots, and foreign files are never modified, moved, overwritten or deleted.
- Fail-closed resource embedding: if a run generates no `rsrc.syso` (for example `winres/winres.json` is missing from the checkout) while a pre-existing `cmd/goal/rsrc.syso` is present, the build refuses with a clear message instead of silently embedding unidentified (possibly stale) version resources into a release binary. The file is left in place: restore `winres/winres.json` (`git checkout -- winres`) and rebuild, or inspect and remove `cmd/goal/rsrc.syso` manually if it is intentionally yours.
- Same-version rebuilds produce independent roots; there is no automatic pruning. Manual cleanup: delete the `build/vX.Y.Z-*` roots you are done with (e.g. `Remove-Item build\v2.3.1-* -Recurse -Force` — deliberately, only that version's roots).
- Concurrent invocations from the same worktree are serialized by an exclusive lock (`%TEMP%\goal-build-<worktree-key>.lock`): the second build fails fast with a clear message instead of interleaving the shared source-tree resource file.
- The script no longer runs `go clean -cache`: the Go build cache is content-addressed over package files (including the embedded `.syso`) and the per-invocation ldflags, so version metadata and resource changes invalidate it automatically.
- Baseline builds of another revision: build inside a disposable worktree — `git worktree add <tmp>\goal-baseline-<revision> <revision>`, run the script from that worktree, then `git worktree remove`. Never swap files in a modified worktree to build another revision.
- Regression coverage: `tests/scripts/build-root.test.ps1` — cases A–J, 35 checks (layout uniqueness, non-destructive same-version rebuilds, foreign/legacy/previous-root byte identity, failure isolation, lock exclusivity, artifact-set detection, simulated and **real pipeline** OUTDIR hand-off, fail-closed resource embedding). Case J runs the production script in-process with the release workflow's exact capture expression (one real unsigned build: exactly one captured OUTDIR, validated root, exact five upload assets, cleanup through the validated hand-off) plus a locked-out failure-propagation leg. Runs in regular CI as the `Run build-ownership regression (BUILD-ARTIFACTS-01)` step of the existing `test-windows` job (Windows PowerShell 5.1; a FAIL exits non-zero and fails the job); run locally from the repository root with `powershell -NoProfile -File tests/scripts/build-root.test.ps1`.
*(2026-11-28, Q2 bounded correction: the independent review of the final acceptance found the `OUTDIR` hand-off was emitted via `[Console]::Out.WriteLine`, which under Windows PowerShell 5.1 goes to the console host and bypasses the workflow's in-pipeline capture — the release build step would have thrown "exactly one OUTDIR line (found 0)" after a successful build. Correction: `scripts/build-all.ps1` now emits `OUTDIR` with `Write-Output` (success stream); no ownership, layout or locking change. RED on the pre-fix bytes with the new real-capture case J (in-process production invocation, no simulated array): J.1–J.4 FAIL with OUTDIR match count 0 while the real build itself succeeded. GREEN on the corrected bytes: 35/35. One real build (v1.2.9-e2e7) through the workflow's exact capture mechanism: exit 0, exactly one captured OUTDIR (the only pipeline object), validated root, exact five-asset sets, both checksums files cross-verified against live bytes, PE ProductVersion 1.2.9-e2e7, `rsrc.syso`/transients cleaned, lock released, legacy `bin/`+`releases/` byte-identical to the pre-build freeze. Bounded independent re-review: PASS. Disclosed limitation (preserved from acceptance): the legacy `releases/` historical SHA256 baseline is incomplete — the pre-e2e freeze exists only for `bin/`.)*

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

**Release CI:** `.github/workflows/release.yml` triggers on tag push. Runs tests, builds into a unique per-build root (capturing the script's `OUTDIR` line and validating it before use), signs the Windows binary (if certificate available), verifies signature, generates checksums, and publishes the release. The verification and upload steps consume exactly the captured build root — never a shared `bin/`/`releases/` directory and never a newest-directory search — and the upload keeps `overwrite_files: false` + `fail_on_unmatched_files: true` over the explicit five-asset allowlist.

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

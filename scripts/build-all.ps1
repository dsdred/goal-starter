# Build script for GoAl - cross-compile for Windows and Linux
# Usage: .\scripts\build-all.ps1 -ReleaseVersion vX.Y.Z
#
# Optional environment variables:
#   SIGN_CERT       - Path to PFX signing certificate (e.g., C:\certs\goal.pfx)
#   SIGN_PASSWORD   - PFX certificate password
#   SIGN_TIMESTAMP  - Timestamp server URL (default: http://timestamp.digicert.com)
#
# If SIGN_CERT is set, Windows binary will be Authenticode-signed with trusted timestamp.

param(
    [AllowEmptyString()]
    [string]$ReleaseVersion
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($ReleaseVersion)) {
    throw "ReleaseVersion is required and must use the vMAJOR.MINOR.PATCH format"
}
if ($ReleaseVersion -notmatch '^v(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$') {
    throw "Invalid ReleaseVersion '$ReleaseVersion'; expected vMAJOR.MINOR.PATCH with an optional prerelease suffix"
}

$OUTPUT_DIR = "bin"
$SOURCE = "./cmd/goal"
$UTF8_NO_BOM = New-Object System.Text.UTF8Encoding($false)
$UTF8_STRICT = New-Object System.Text.UTF8Encoding($false, $true)

# Get version info and build time
$VERSION = $ReleaseVersion
$GIT_HASH = "none"
$BUILD_TIME = "unknown"
$LDFLAGS = "-ldflags=-s -w"

try {
    $GIT_HASH = git rev-parse --short HEAD 2>$null
    $BUILD_TIME = (Get-Date -UFormat "%Y-%m-%dT%H:%M:%SZ")
    $LDFLAGS = "-ldflags=-s -w -X github.com/dsdred/goal/internal/version.Version=$VERSION -X github.com/dsdred/goal/internal/version.GitCommit=$GIT_HASH -X github.com/dsdred/goal/internal/version.BuildTime=$BUILD_TIME"
} catch {
    Write-Host "! Git info not available, using defaults"
}

# --- Windows resource generation ---
function Invoke-GenResources {
    Write-Host "+ Generating Windows resource metadata..." -ForegroundColor Yellow
    if (-not (Get-Command go-winres -ErrorAction SilentlyContinue)) {
        Write-Host "    go-winres not found, installing..." -ForegroundColor Gray
        go install github.com/tc-hib/go-winres@latest 2>&1 | Out-Null
    }
    # Clean old syso files to force regeneration
    Remove-Item "$SOURCE\rsrc.syso" -ErrorAction SilentlyContinue
    Remove-Item "rsrc_*.syso" -ErrorAction SilentlyContinue

    # Check if winres.json exists
    if (Test-Path "winres\winres.json") {
        # Generate a temporary resource definition without modifying tracked files.
        $versionForPE = $VERSION -replace '^v', ''
        $numericVersion = if ($versionForPE -match '^\d+\.\d+\.\d+$') { $versionForPE } else { '0.0.0' }
        $fileVersion = $numericVersion + '.0'
        $productVersion = $versionForPE
        $winresPath = "winres\winres.json"
        $generatedWinresPath = "winres\winres.generated.json"
        $winresContent = Get-Content $winresPath -Raw
        $winresContent = $winresContent -replace '"file_version":\s*"[^"]*"', "`"file_version`": `"$fileVersion`""
        $winresContent = $winresContent -replace '"product_version":\s*"[^"]*"', "`"product_version`": `"$productVersion`""
        $winresContent = $winresContent -replace '"FileVersion":\s*"[^"]*"', "`"FileVersion`": `"$fileVersion`""
        $winresContent = $winresContent -replace '"ProductVersion":\s*"[^"]*"', "`"ProductVersion`": `"$productVersion`""
        [System.IO.File]::WriteAllText($generatedWinresPath, $winresContent, (New-Object System.Text.UTF8Encoding $false))
        Write-Host "    Injected PE version: $productVersion (file: $fileVersion)" -ForegroundColor Gray

        try {
            go-winres make --in $generatedWinresPath --arch amd64 2>&1 | Out-Null
        } finally {
            Remove-Item $generatedWinresPath -ErrorAction SilentlyContinue
        }
        $sysoFile = Get-ChildItem rsrc_windows_amd64.syso -ErrorAction SilentlyContinue
        if ($sysoFile) {
            Copy-Item $sysoFile "$SOURCE\rsrc.syso"
            Write-Host "    OK Windows resource metadata embedded" -ForegroundColor Green
        } else {
            Write-Host "    ! No syso file generated, continuing without resources" -ForegroundColor Yellow
        }
    } else {
        Write-Host "    ! winres/winres.json not found, continuing without resource metadata" -ForegroundColor Yellow
    }
}

# --- Authenticode signing ---
function Invoke-SignBinary {
    param(
        [string]$FilePath,
        [string]$CertPath,
        [string]$CertPassword,
        [string]$TimestampServer
    )
    if (-not (Test-Path $CertPath)) {
        throw "Signing was requested, but the certificate was not found at $CertPath"
    }
    Write-Host "+ Signing $FilePath with Authenticode..." -ForegroundColor Yellow
    $signtoolPaths = @(
        "${env:ProgramFiles(x86)}\Windows Kits\10\bin\10.0.22621.0\x64\signtool.exe",
        "${env:ProgramFiles(x86)}\Windows Kits\10\bin\x64\signtool.exe",
        "${env:ProgramFiles(x86)}\Windows Kits\8.1\bin\x64\signtool.exe"
    )
    $signtool = $null
    foreach ($p in $signtoolPaths) {
        if ($p -and (Test-Path $p)) {
            $signtool = $p
            break
        }
    }
    if (-not $signtool) {
        throw "Signing was requested, but signtool.exe was not found"
    }
    $timestampUrl = $TimestampServer
    if (-not $timestampUrl) {
        $timestampUrl = "http://timestamp.digicert.com"
    }
    $signArgs = @(
        "sign",
        "/f", $CertPath,
        "/p", $CertPassword,
        "/tr", $timestampUrl,
        "/td", "sha256",
        "/fd", "sha256",
        "/as",
        $FilePath
    )
    & $signtool $signArgs 2>&1 | Out-String
    if ($LASTEXITCODE -eq 0) {
        Write-Host "    OK Signed with Authenticode + trusted timestamp" -ForegroundColor Green
    } else {
        throw "Signing failed with exit code $LASTEXITCODE"
    }
}

# --- Signature inspection and release metadata ---
function Get-BinarySignatureMetadata {
    param(
        [string]$FilePath,
        [bool]$SigningRequested
    )

    Write-Host "+ Verifying signature of $FilePath..." -ForegroundColor Yellow
    $sig = Get-AuthenticodeSignature -FilePath $FilePath -ErrorAction SilentlyContinue

    if ($SigningRequested -and $sig.Status -ne 'Valid') {
        throw "Signing was requested, but signature verification returned $($sig.Status): $($sig.StatusMessage)"
    }

    if ($sig.Status -eq 'Valid') {
        $publisher = if ($sig.SignerCertificate) { $sig.SignerCertificate.Subject } else { "unknown" }
        $timestamp = if ($sig.TimeStamperCertificate) { "present" } else { "none" }
        Write-Host "    OK Signature valid - Publisher: $publisher" -ForegroundColor Green
        return [pscustomobject]@{
            Status    = "Valid"
            Publisher = $publisher
            Timestamp = $timestamp
            Signed    = $true
        }
    }

    if ($sig.Status -eq 'NotSigned') {
        Write-Host "    Signature status: NotSigned" -ForegroundColor Gray
        return [pscustomobject]@{
            Status    = "NotSigned"
            Publisher = "none"
            Timestamp = "none"
            Signed    = $false
        }
    }

    throw "Windows binary has an unacceptable signature state: $($sig.Status): $($sig.StatusMessage)"
}

function New-WindowsReleaseText {
    param([pscustomobject]$Signature)

    $trustStatement = if ($Signature.Signed) {
        "Windows binary is Authenticode-signed and signature validation passed."
    } else {
        "Windows binary is unsigned."
    }

    return @"
# GoAl Windows Release

## Installation

1. Extract this archive to a desired directory
2. Copy or rename ``goal.example.json`` to ``goal.json``
3. Edit ``goal.json`` with your runtime and model configuration

## Quick Start

``````powershell
.\goal.exe
``````

## Install as Windows Service

``````powershell
.\install-service.ps1
``````

## Windows Trust

Authenticode: $($Signature.Status)
Publisher: $($Signature.Publisher)
Timestamp: $($Signature.Timestamp)

$trustStatement

Verify the artifact state with:

``````powershell
Get-AuthenticodeSignature .\goal.exe
``````

## Files

- ``goal.exe`` - GoAl binary
- ``goal.example.json`` - Example configuration; copy or rename it to ``goal.json``
- ``install-service.ps1`` - Install as Windows Service
- ``uninstall-service.ps1`` - Uninstall Windows Service
- ``README.md`` - English documentation
- ``README_RU.md`` - Russian documentation
"@
}

function Test-WindowsReleaseArchive {
    param([string]$ArchivePath)

    $validationDir = Join-Path ([System.IO.Path]::GetTempPath()) ("goal-release-validate-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $validationDir | Out-Null

    try {
        [System.IO.Compression.ZipFile]::ExtractToDirectory((Resolve-Path $ArchivePath), $validationDir)
        $releasePath = Join-Path $validationDir "goal\RELEASE.txt"
        $binaryPath = Join-Path $validationDir "goal\goal.exe"
        if (-not (Test-Path $releasePath) -or -not (Test-Path $binaryPath)) {
            throw "Windows archive is missing RELEASE.txt or goal.exe"
        }

        $releaseBytes = [System.IO.File]::ReadAllBytes($releasePath)
        if ($releaseBytes.Length -ge 3 -and $releaseBytes[0] -eq 0xEF -and $releaseBytes[1] -eq 0xBB -and $releaseBytes[2] -eq 0xBF) {
            throw "Windows archive RELEASE.txt must be UTF-8 without BOM"
        }
        try {
            $releaseText = $UTF8_STRICT.GetString($releaseBytes)
        } catch {
            throw "Windows archive RELEASE.txt is not valid UTF-8"
        }
        if ($releaseText -match '[^\x00-\x7F]') {
            throw "Windows archive RELEASE.txt contains unexpected non-ASCII text"
        }

        $actual = Get-BinarySignatureMetadata -FilePath $binaryPath -SigningRequested $false
        foreach ($expectedLine in @(
            "Authenticode: $($actual.Status)",
            "Publisher: $($actual.Publisher)",
            "Timestamp: $($actual.Timestamp)"
        )) {
            if (-not $releaseText.Contains($expectedLine)) {
                throw "Windows archive RELEASE.txt does not match the included binary: missing '$expectedLine'"
            }
        }

        if ($actual.Signed) {
            if (-not $releaseText.Contains("Windows binary is Authenticode-signed and signature validation passed.")) {
                throw "Signed Windows archive does not state that signature validation passed"
            }
            if ($releaseText.Contains("Windows binary is unsigned.")) {
                throw "Signed Windows archive is incorrectly identified as unsigned"
            }
        } else {
            foreach ($falseClaim in @("Authenticode: Valid", "Authenticode-signed", "Expected signature: Valid")) {
                if ($releaseText.Contains($falseClaim)) {
                    throw "Unsigned Windows archive contains a false signing claim: '$falseClaim'"
                }
            }
            if (-not $releaseText.Contains("Windows binary is unsigned.")) {
                throw "Unsigned Windows archive does not explicitly identify the binary as unsigned"
            }
        }

        Write-Host "+ Windows archive trust metadata and UTF-8 validation: PASS" -ForegroundColor Green
    } finally {
        Remove-Item $validationDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# Create output directory
if (-not (Test-Path $OUTPUT_DIR)) {
    New-Item -ItemType Directory -Path $OUTPUT_DIR | Out-Null
    Write-Host "+ Created '$OUTPUT_DIR' directory"
}

function Invoke-GoBuild {
    param(
        [string]$GOOS,
        [string]$GOARCH,
        [string]$OutputPath
    )
    $env:GOOS = $GOOS
    $env:GOARCH = $GOARCH
    if ($GOOS -eq "windows") {
        $env:CGO_ENABLED = '0'
    }
    $buildArgs = @("build", $LDFLAGS)
    $buildArgs += @("-o", $OutputPath, $SOURCE)
    $exitCode = 0
    & go $buildArgs 2>&1 | Out-String
    $exitCode = $LASTEXITCODE
    if ($exitCode -eq 0) {
        return $true
    } else {
        Write-Host "    Check build-error.log for details" -ForegroundColor Red
        return $false
    }
}

Write-Host ""
Write-Host "=== GoAl Build Script ===" -ForegroundColor Cyan
Write-Host "Version: $VERSION (commit: $GIT_HASH, time: $BUILD_TIME)"
Write-Host "Output directory: $OUTPUT_DIR\"
Write-Host ""

# Build for Windows amd64
Write-Host "+ Building for Windows amd64..." -ForegroundColor Yellow
$WIN_OUTPUT = "$OUTPUT_DIR\goal-windows-amd64.exe"

# Generate Windows resource metadata before build
Invoke-GenResources

# Clean Go build cache to ensure syso is picked up
go clean -cache 2>&1 | Out-Null

if (Invoke-GoBuild "windows" "amd64" $WIN_OUTPUT) {
    $size = [math]::Round((Get-Item $WIN_OUTPUT).Length / 1MB, 2)
    Write-Host "+ Windows amd64: $WIN_OUTPUT ($size MB)" -ForegroundColor Green

    # Version consistency check: verify binary --version matches expected
    $binaryVersion = & $WIN_OUTPUT --version 2>&1
    if ($binaryVersion -notlike "*$VERSION*") {
        Write-Host "- VERSION MISMATCH: binary reports '$binaryVersion', expected '$VERSION'" -ForegroundColor Red
        Write-Host "  This indicates ldflags or PE version injection failed." -ForegroundColor Red
        exit 1
    }
    Write-Host "    Version check PASS: $binaryVersion" -ForegroundColor Green
} else {
    Write-Host "- Windows amd64 build failed" -ForegroundColor Red
    exit 1
}

# Authenticode signing (conditional)
$SIGNING_REQUESTED = -not [string]::IsNullOrWhiteSpace($env:SIGN_CERT)
if ($SIGNING_REQUESTED) {
    Write-Host ""
    Invoke-SignBinary -FilePath $WIN_OUTPUT -CertPath $env:SIGN_CERT -CertPassword $env:SIGN_PASSWORD -TimestampServer $env:SIGN_TIMESTAMP
} else {
    Write-Host ""
    Write-Host "i No SIGN_CERT set - Windows binary will be unsigned" -ForegroundColor Gray
}

$WINDOWS_SIGNATURE = Get-BinarySignatureMetadata -FilePath $WIN_OUTPUT -SigningRequested $SIGNING_REQUESTED

# Build for Linux amd64
Write-Host ""
Write-Host "+ Building for Linux amd64..." -ForegroundColor Yellow
$LINUX_OUTPUT = "$OUTPUT_DIR\goal-linux-amd64"
if (Invoke-GoBuild "linux" "amd64" $LINUX_OUTPUT) {
    $size = [math]::Round((Get-Item $LINUX_OUTPUT).Length / 1MB, 2)
    Write-Host "+ Linux amd64: $LINUX_OUTPUT ($size MB)" -ForegroundColor Green
} else {
    Write-Host "- Linux amd64 build failed" -ForegroundColor Red
    exit 1
}

# Version for archive naming. Validation above makes this a lossless operation.
$ARCHIVE_VERSION = $VERSION

# Build MSI installer (Windows only)
if ($env:GOOS -eq "windows" -or $IsWindows) {
    Write-Host ""
    Write-Host "+ Building MSI installer..." -ForegroundColor Yellow
    $RELEASE_DIR = "releases"
    $MSI_OUTPUT = "$RELEASE_DIR\goal-${ARCHIVE_VERSION}-windows-amd64.msi"

    if (Test-Path "./cmd/goal-msi") {
        $msiBuildArgs = @("build", "-o", "bin/goal-msi.exe", "./cmd/goal-msi")
        & go $msiBuildArgs
        if ($LASTEXITCODE -ne 0) {
            Write-Host "- MSI builder build failed (continuing...)" -ForegroundColor Yellow
        }
    }

    $wixFound = $false
    $wixCandidates = @(
        "C:\Program Files (x86)\WiX Toolset v3.14",
        "C:\Program Files (x86)\WiX Toolset v3.11"
    )
    foreach ($wixPath in $wixCandidates) {
        if (Test-Path "$wixPath\bin\light.exe") {
            $wixFound = $true
            $env:PATH = "$wixPath\bin;$env:PATH"
            Write-Host "    Found WiX tools: $wixPath" -ForegroundColor Green
            break
        }
    }

    if (-not $wixFound) {
        try {
            $null = Get-Command candle.exe -ErrorAction Stop
            $wixFound = $true
            Write-Host "    Found WiX tools in PATH" -ForegroundColor Green
        } catch {
            Write-Host "    WiX tools not found in PATH" -ForegroundColor Yellow
        }
    }

    if ($wixFound) {
        Write-Host "    Building MSI with WiX..." -ForegroundColor Gray
        $msiArgs = @(".\bin\goal-msi.exe", "-binary", $WIN_OUTPUT, "-o", $MSI_OUTPUT, "-version", $ARCHIVE_VERSION)
        & $msiArgs[0] $($msiArgs[1..($msiArgs.Count - 1)]) 2>&1 | Out-String
        if ($LASTEXITCODE -eq 0 -or (Test-Path $MSI_OUTPUT)) {
            if (Test-Path $MSI_OUTPUT) {
                $msiSize = [math]::Round((Get-Item $MSI_OUTPUT).Length / 1MB, 2)
                Write-Host "+ MSI installer: $MSI_OUTPUT ($msiSize MB)" -ForegroundColor Green
            } else {
                Write-Host "+ MSI installer: $MSI_OUTPUT" -ForegroundColor Green
            }
        } else {
            Write-Host "- MSI build failed (exit code: $LASTEXITCODE)" -ForegroundColor Yellow
            Write-Host "    Falling back to SFX installer..." -ForegroundColor Yellow
            $SFX_OUTPUT = "$RELEASE_DIR\goal-${ARCHIVE_VERSION}-windows-amd64.zip"
            $sfxArgs = @(".\bin\goal-msi.exe", "-binary", $WIN_OUTPUT, "-o", $SFX_OUTPUT, "-version", $ARCHIVE_VERSION, "-sfx")
            & $sfxArgs[0] $($sfxArgs[1..($sfxArgs.Count - 1)]) 2>&1 | Out-String
            if (Test-Path $SFX_OUTPUT) {
                $sfxSize = [math]::Round((Get-Item $SFX_OUTPUT).Length / 1MB, 2)
                Write-Host "+ SFX installer: $SFX_OUTPUT ($sfxSize MB)" -ForegroundColor Green
                Write-Host "    (Self-extracting archive with install.bat)" -ForegroundColor Gray
            }
        }
    } else {
        Write-Host "    WiX tools not found, building SFX installer..." -ForegroundColor Yellow
        $SFX_OUTPUT = "$RELEASE_DIR\goal-${ARCHIVE_VERSION}-windows-amd64.zip"
        $sfxArgs = @(".\bin\goal-msi.exe", "-binary", $WIN_OUTPUT, "-o", $SFX_OUTPUT, "-version", $ARCHIVE_VERSION, "-sfx")
        & $sfxArgs[0] $($sfxArgs[1..($sfxArgs.Count - 1)]) 2>&1 | Out-String
        if (Test-Path $SFX_OUTPUT) {
            $sfxSize = [math]::Round((Get-Item $SFX_OUTPUT).Length / 1MB, 2)
            Write-Host "+ SFX installer: $SFX_OUTPUT ($sfxSize MB)" -ForegroundColor Green
            Write-Host "    (Self-extracting archive with install.bat)" -ForegroundColor Gray
        } else {
            Write-Host "! SFX build failed" -ForegroundColor Yellow
        }
    }
    Remove-Item "bin\goal-msi.exe" -ErrorAction SilentlyContinue
}

# Generate checksums FOR BINARIES (after signing)
Write-Host ""
Write-Host "+ Generating SHA256 checksums..." -ForegroundColor Yellow

$CHECKSUM_FILE = "$OUTPUT_DIR\checksums.txt"
$windowsHash = (Get-FileHash "$OUTPUT_DIR\goal-windows-amd64.exe" -Algorithm SHA256).Hash.ToLower()
$linuxHash = (Get-FileHash "$OUTPUT_DIR\goal-linux-amd64" -Algorithm SHA256).Hash.ToLower()
$binaryChecksums = "$windowsHash  goal-windows-amd64.exe`n$linuxHash  goal-linux-amd64`n"
[System.IO.File]::WriteAllText($CHECKSUM_FILE, $binaryChecksums, $UTF8_NO_BOM)
Write-Host "    Windows (post-sign): $windowsHash" -ForegroundColor Gray
Write-Host "    Linux: $linuxHash" -ForegroundColor Gray
Write-Host "+ Checksums: $CHECKSUM_FILE" -ForegroundColor Green

# Create release archives
Write-Host ""
Write-Host "+ Creating release archives..." -ForegroundColor Yellow

$RELEASE_DIR = "releases"
if (Test-Path $RELEASE_DIR) {
    Remove-Item $RELEASE_DIR -Recurse -Force
}
New-Item -ItemType Directory -Path $RELEASE_DIR | Out-Null

# --- Windows archive ---
$WIN_ARCHIVE_NAME = "goal-${ARCHIVE_VERSION}-windows-amd64.zip"
$WIN_ARCHIVE_PATH = "$RELEASE_DIR\$WIN_ARCHIVE_NAME"
$WIN_STAGING = "$RELEASE_DIR\staging-windows"
if (Test-Path $WIN_STAGING) { Remove-Item $WIN_STAGING -Recurse -Force }
New-Item -ItemType Directory -Path "$WIN_STAGING\goal" | Out-Null

# Copy binary (signed if available)
Copy-Item $WIN_OUTPUT "$WIN_STAGING\goal\goal.exe"
Copy-Item "goal.example.json" "$WIN_STAGING\goal\goal.example.json"
Copy-Item "README.md" "$WIN_STAGING\goal\README.md"
Copy-Item "README_RU.md" "$WIN_STAGING\goal\README_RU.md"
Copy-Item "deploy\windows\install-service.ps1" "$WIN_STAGING\goal\install-service.ps1"
Copy-Item "deploy\windows\uninstall-service.ps1" "$WIN_STAGING\goal\uninstall-service.ps1"

# Create Windows release metadata from the verified artifact state.
$WIN_README = New-WindowsReleaseText -Signature $WINDOWS_SIGNATURE
[System.IO.File]::WriteAllText("$WIN_STAGING\goal\RELEASE.txt", $WIN_README, $UTF8_NO_BOM)

# Create zip archive
if (Test-Path $WIN_ARCHIVE_PATH) { Remove-Item $WIN_ARCHIVE_PATH }
Add-Type -AssemblyName "System.IO.Compression.FileSystem"
[System.IO.Compression.ZipFile]::CreateFromDirectory($WIN_STAGING, $WIN_ARCHIVE_PATH)
Write-Host "+ Windows archive: $WIN_ARCHIVE_PATH" -ForegroundColor Green

Test-WindowsReleaseArchive -ArchivePath $WIN_ARCHIVE_PATH

# --- Linux archive ---
$LINUX_ARCHIVE_NAME = "goal-${ARCHIVE_VERSION}-linux-amd64.tar.gz"
$LINUX_ARCHIVE_PATH = "$RELEASE_DIR\$LINUX_ARCHIVE_NAME"
$LINUX_STAGING = "$RELEASE_DIR\staging-linux"
if (Test-Path $LINUX_STAGING) { Remove-Item $LINUX_STAGING -Recurse -Force }
New-Item -ItemType Directory -Path "$LINUX_STAGING\goal" | Out-Null

# Copy binary
Copy-Item $LINUX_OUTPUT "$LINUX_STAGING\goal\goal"
Copy-Item "goal.example.json" "$LINUX_STAGING\goal\goal.example.json"
Copy-Item "README.md" "$LINUX_STAGING\goal\README.md"
Copy-Item "README_RU.md" "$LINUX_STAGING\goal\README_RU.md"
New-Item -ItemType Directory -Path "$LINUX_STAGING\goal\etc\goal" | Out-Null
New-Item -ItemType Directory -Path "$LINUX_STAGING\goal\deploy" | Out-Null
Copy-Item "goal.example.json" "$LINUX_STAGING\goal\etc\goal\goal.example.json"
Copy-Item "deploy\systemd\goal.service" "$LINUX_STAGING\goal\deploy\goal.service"

# Create Linux README for archive
$LINUX_README = @'
# GoAl Linux Release

## Installation

1. Extract this archive to `/opt/goal/` or your preferred directory
2. Copy or rename `etc/goal/goal.example.json` to `/etc/goal/goal.json`
3. Edit configuration file

## Quick Start

```bash
sudo ./goal
```

## Install as systemd Service

```bash
sudo cp deploy/goal.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable goal
sudo systemctl start goal
```

## Files

- `goal` - GoAl binary
- `etc/goal/goal.example.json` - Example configuration
- `deploy/goal.service` - systemd service file
- `README.md` - English documentation
- `README_RU.md` - Russian documentation
'@
[System.IO.File]::WriteAllText("$LINUX_STAGING\goal\RELEASE.txt", $LINUX_README, $UTF8_NO_BOM)

# Create tar.gz archive
$tarArgs = @('czf', $LINUX_ARCHIVE_PATH, '-C', $LINUX_STAGING, 'goal')
& tar $tarArgs
Write-Host "+ Linux archive: $LINUX_ARCHIVE_PATH" -ForegroundColor Green

# Cleanup staging
Remove-Item $WIN_STAGING -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item $LINUX_STAGING -Recurse -Force -ErrorAction SilentlyContinue

# Generate checksums for release archives
Write-Host ""
Write-Host "+ Generating release archive checksums..." -ForegroundColor Yellow
$RELEASE_CHECKSUM_FILE = "$RELEASE_DIR\checksums.txt"
$winReleaseHash = (Get-FileHash $WIN_ARCHIVE_PATH -Algorithm SHA256).Hash.ToLower()
$LINUXReleaseHash = (Get-FileHash $LINUX_ARCHIVE_PATH -Algorithm SHA256).Hash.ToLower()
$releaseChecksums = "$winReleaseHash  $WIN_ARCHIVE_NAME`n$LINUXReleaseHash  $LINUX_ARCHIVE_NAME`n"
[System.IO.File]::WriteAllText($RELEASE_CHECKSUM_FILE, $releaseChecksums, $UTF8_NO_BOM)
Write-Host "+ Release checksums: $RELEASE_CHECKSUM_FILE" -ForegroundColor Green

function Assert-ExactFileSet {
    param(
        [string]$Directory,
        [string[]]$ExpectedNames
    )

    $actualNames = @(Get-ChildItem -LiteralPath $Directory -File | ForEach-Object { $_.Name } | Sort-Object)
    $expected = @($ExpectedNames | Sort-Object)
    $difference = @(Compare-Object -ReferenceObject $expected -DifferenceObject $actualNames)
    if ($difference.Count -ne 0) {
        $details = $difference | ForEach-Object { "$($_.SideIndicator) $($_.InputObject)" }
        throw "Unexpected artifact set in '$Directory': $($details -join ', ')"
    }
}

# Guard the canonical release contract against empty-version and stale artifacts.
Assert-ExactFileSet -Directory $OUTPUT_DIR -ExpectedNames @(
    "goal-windows-amd64.exe",
    "goal-linux-amd64",
    "checksums.txt"
)
Assert-ExactFileSet -Directory $RELEASE_DIR -ExpectedNames @(
    $WIN_ARCHIVE_NAME,
    $LINUX_ARCHIVE_NAME,
    "checksums.txt"
)
Write-Host "+ Exact release asset set: PASS" -ForegroundColor Green

# Cleanup temp files
Remove-Item "$OUTPUT_DIR\build.log" -ErrorAction SilentlyContinue
Remove-Item "$OUTPUT_DIR\build-error.log" -ErrorAction SilentlyContinue

# Clean up generated syso files (not needed in repo)
Remove-Item "$SOURCE\rsrc.syso" -ErrorAction SilentlyContinue
Remove-Item "rsrc_*.syso" -ErrorAction SilentlyContinue

# Restore environment
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "=== Build Complete ===" -ForegroundColor Cyan
Write-Host ""
Write-Host "Artifacts:" -ForegroundColor Cyan
Write-Host "  Binaries:   $OUTPUT_DIR\" -ForegroundColor White
Write-Host "  Archives:   releases\" -ForegroundColor White
Write-Host "  SHA256:     $RELEASE_CHECKSUM_FILE" -ForegroundColor White
Write-Host ""
Write-Host "Release files:" -ForegroundColor Cyan
Write-Host "  Windows: $WIN_ARCHIVE_PATH" -ForegroundColor White
Write-Host "  Linux:   $LINUX_ARCHIVE_PATH" -ForegroundColor White
Write-Host ""

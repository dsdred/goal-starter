# Build script for GoAl - cross-compile for Windows and Linux
# Usage: .\scripts\build-all.ps1

$ErrorActionPreference = "Stop"

$OUTPUT_DIR = "bin"
$SOURCE = "./cmd/goal"
$LDFLAGS = @("-ldflags", "-s -w")

# Create output directory
if (-not (Test-Path $OUTPUT_DIR)) {
    New-Item -ItemType Directory -Path $OUTPUT_DIR | Out-Null
    Write-Host "[+] Created '$OUTPUT_DIR' directory"
}

# Get version info and build time
$VERSION = "dev"
$GIT_HASH = "none"
$BUILD_TIME = "unknown"
try {
    $VERSION = git describe --tags --abbrev=0 2>$null
    $GIT_HASH = git rev-parse --short HEAD 2>$null
    $BUILD_TIME = (Get-Date -UFormat "%Y-%m-%dT%H:%M:%SZ")
    $LDFLAGS = @("-ldflags", "-s", "-w", "-X", "github.com/example/goal/internal/version.Version=$VERSION", "-X", "github.com/example/goal/internal/version.GitCommit=$GIT_HASH", "-X", "github.com/example/goal/internal/version.BuildTime=$BUILD_TIME")
} catch {
    Write-Host "[!] Git info not available, using defaults"
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
    
    # Build argument array: go build -ldflags -s -w -o output path
    $buildArgs = @("build")
    foreach ($flag in $LDFLAGS) {
        $buildArgs += $flag
    }
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
Write-Host "[+] Building for Windows amd64..." -ForegroundColor Yellow
$WIN_OUTPUT = "$OUTPUT_DIR\goal-windows-amd64.exe"
if (Invoke-GoBuild "windows" "amd64" $WIN_OUTPUT) {
    $size = [math]::Round((Get-Item $WIN_OUTPUT).Length / 1MB, 2)
    Write-Host "[+] Windows amd64: $WIN_OUTPUT ($size MB)" -ForegroundColor Green
} else {
    Write-Host "[-] Windows amd64 build failed" -ForegroundColor Red
    exit 1
}

# Build for Linux amd64
Write-Host "[+] Building for Linux amd64..." -ForegroundColor Yellow
$LINUX_OUTPUT = "$OUTPUT_DIR\goal-linux-amd64"
if (Invoke-GoBuild "linux" "amd64" $LINUX_OUTPUT) {
    $size = [math]::Round((Get-Item $LINUX_OUTPUT).Length / 1MB, 2)
    Write-Host "[+] Linux amd64: $LINUX_OUTPUT ($size MB)" -ForegroundColor Green
} else {
    Write-Host "[-] Linux amd64 build failed" -ForegroundColor Red
    exit 1
}

# Version for archive naming (needed for MSI build)
$ARCHIVE_VERSION = $VERSION -replace "[^0-9a-zA-Z\.\-]", "_"
if ($ARCHIVE_VERSION -eq "" -or $ARCHIVE_VERSION -eq "dev") {
    $ARCHIVE_VERSION = "dev-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
}

# Build MSI installer (Windows only)
if ($env:GOOS -eq "windows" -or $IsWindows) {
    Write-Host ""
    Write-Host "[+] Building MSI installer..." -ForegroundColor Yellow
    $RELEASE_DIR = "releases"
    $MSI_OUTPUT = "$RELEASE_DIR\goal-${ARCHIVE_VERSION}-windows-amd64.msi"

    # Build the MSI builder tool first
    Write-Host "    Building MSI builder..." -ForegroundColor Gray
    $msiBuildArgs = @("build", "-o", "bin/goal-msi.exe", "./cmd/goal-msi")
    & go $msiBuildArgs
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[-] MSI builder build failed (continuing...)" -ForegroundColor Yellow
    }

    # Check if WiX tools are available
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

    # Also check PATH
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

        $msiArgs = @(
            ".\bin\goal-msi.exe",
            "-binary", $WIN_OUTPUT,
            "-o", $MSI_OUTPUT,
            "-version", $ARCHIVE_VERSION
        )

        & $msiArgs[0] $($msiArgs[1..($msiArgs.Count - 1)]) 2>&1 | Out-String
        if ($LASTEXITCODE -eq 0 -or (Test-Path $MSI_OUTPUT)) {
            if (Test-Path $MSI_OUTPUT) {
                $msiSize = [math]::Round((Get-Item $MSI_OUTPUT).Length / 1MB, 2)
                Write-Host "[+] MSI installer: $MSI_OUTPUT ($msiSize MB)" -ForegroundColor Green
            } else {
                Write-Host "[+] MSI installer: $MSI_OUTPUT" -ForegroundColor Green
            }
        } else {
            Write-Host "[-] MSI build failed (exit code: $LASTEXITCODE)" -ForegroundColor Yellow
            Write-Host "    Falling back to SFX installer..." -ForegroundColor Yellow

            # Fallback: SFX installer (zip archive with install script)
            $SFX_OUTPUT = "$RELEASE_DIR\goal-${ARCHIVE_VERSION}-windows-amd64.zip"
            $sfxArgs = @(
                ".\bin\goal-msi.exe",
                "-binary", $WIN_OUTPUT,
                "-o", $SFX_OUTPUT,
                "-version", $ARCHIVE_VERSION,
                "-sfx"
            )
            & $sfxArgs[0] $($sfxArgs[1..($sfxArgs.Count - 1)]) 2>&1 | Out-String
            if (Test-Path $SFX_OUTPUT) {
                $sfxSize = [math]::Round((Get-Item $SFX_OUTPUT).Length / 1MB, 2)
                Write-Host "[+] SFX installer: $SFX_OUTPUT ($sfxSize MB)" -ForegroundColor Green
                Write-Host "    (Self-extracting archive with install.bat)" -ForegroundColor Gray
            }
        }
    } else {
        Write-Host "    WiX tools not found, building SFX installer..." -ForegroundColor Yellow

        # SFX installer (zip archive with install script) - no external dependencies
        $SFX_OUTPUT = "$RELEASE_DIR\goal-${ARCHIVE_VERSION}-windows-amd64.zip"
        $sfxArgs = @(
            ".\bin\goal-msi.exe",
            "-binary", $WIN_OUTPUT,
            "-o", $SFX_OUTPUT,
            "-version", $ARCHIVE_VERSION,
            "-sfx"
        )
        & $sfxArgs[0] $($sfxArgs[1..($sfxArgs.Count - 1)]) 2>&1 | Out-String
        if (Test-Path $SFX_OUTPUT) {
            $sfxSize = [math]::Round((Get-Item $SFX_OUTPUT).Length / 1MB, 2)
            Write-Host "[+] SFX installer: $SFX_OUTPUT ($sfxSize MB)" -ForegroundColor Green
            Write-Host "    (Self-extracting archive with install.bat)" -ForegroundColor Gray
        } else {
            Write-Host "[!] SFX build failed" -ForegroundColor Yellow
        }
    }

    # Cleanup MSI builder
    Remove-Item "bin\goal-msi.exe" -ErrorAction SilentlyContinue
}

# Generate checksums
Write-Host "[+] Generating SHA256 checksums..." -ForegroundColor Yellow

$CHECKSUM_FILE = "$OUTPUT_DIR\checksums.txt"
$windowsHash = (Get-FileHash "$OUTPUT_DIR\goal-windows-amd64.exe" -Algorithm SHA256).Hash.ToLower()
$linuxHash = (Get-FileHash "$OUTPUT_DIR\goal-linux-amd64" -Algorithm SHA256).Hash.ToLower()
"$windowsHash  goal-windows-amd64.exe`n$linuxHash  goal-linux-amd64" | Out-File -FilePath $CHECKSUM_FILE -Encoding utf8
Write-Host "[+] Checksums: $CHECKSUM_FILE" -ForegroundColor Green

# Create release archives
Write-Host ""
Write-Host "[+] Creating release archives..." -ForegroundColor Yellow

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

# Copy binary
Copy-Item $WIN_OUTPUT "$WIN_STAGING\goal\goal.exe"
Copy-Item "goal.example.json" "$WIN_STAGING\goal\goal.example.json"
Copy-Item "README.md" "$WIN_STAGING\goal\README.md"
Copy-Item "README_RU.md" "$WIN_STAGING\goal\README_RU.md"
Copy-Item "deploy\windows\install-service.ps1" "$WIN_STAGING\goal\install-service.ps1"
Copy-Item "deploy\windows\uninstall-service.ps1" "$WIN_STAGING\goal\uninstall-service.ps1"

# Create Windows README for archive
$WIN_README = @'
# GoAl Windows Release

## Installation

1. Extract this archive to a desired directory
2. Copy or rename `goal.example.json` to `goal.json`
3. Edit `goal.json` with your runtime and model configuration

## Quick Start

```powershell
.\goal.exe
```

## Install as Windows Service

```powershell
.\install-service.ps1
```

## Files

- `goal.exe` — GoAl binary
- `goal.json` — Configuration file (rename from example)
- `goal.example.json` — Example configuration
- `install-service.ps1` — Install as Windows Service
- `uninstall-service.ps1` — Uninstall Windows Service
- `README.md` — Full documentation
'@
Set-Content -Path "$WIN_STAGING\goal\RELEASE.txt" -Value $WIN_README -Encoding UTF8

# Create zip archive
if (Test-Path $WIN_ARCHIVE_PATH) { Remove-Item $WIN_ARCHIVE_PATH }
Add-Type -AssemblyName "System.IO.Compression.FileSystem"
[System.IO.Compression.ZipFile]::CreateFromDirectory($WIN_STAGING, $WIN_ARCHIVE_PATH)
Write-Host "[+] Windows archive: $WIN_ARCHIVE_PATH" -ForegroundColor Green

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

- `goal` — GoAl binary
- `etc/goal/goal.example.json` — Example configuration
- `deploy/goal.service` — systemd service file
- `README.md` — Full documentation
'@
Set-Content -Path "$LINUX_STAGING\goal\RELEASE.txt" -Value $LINUX_README -Encoding UTF8

# Create tar.gz archive
$tarArgs = @('czf', $LINUX_ARCHIVE_PATH, '-C', $LINUX_STAGING, 'goal')
& tar $tarArgs
Write-Host "[+] Linux archive: $LINUX_ARCHIVE_PATH" -ForegroundColor Green

# Cleanup staging
Remove-Item $WIN_STAGING -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item $LINUX_STAGING -Recurse -Force -ErrorAction SilentlyContinue

# Generate checksums for releases
Write-Host "[+] Generating release checksums..." -ForegroundColor Yellow
$RELEASE_CHECKSUM_FILE = "$RELEASE_DIR\checksums.txt"
$winReleaseHash = (Get-FileHash $WIN_ARCHIVE_PATH -Algorithm SHA256).Hash.ToLower()
$LINUXReleaseHash = (Get-FileHash $LINUX_ARCHIVE_PATH -Algorithm SHA256).Hash.ToLower()
"$winReleaseHash  $WIN_ARCHIVE_NAME`n$LINUXReleaseHash  $LINUX_ARCHIVE_NAME" | Out-File -FilePath $RELEASE_CHECKSUM_FILE -Encoding utf8
Write-Host "[+] Release checksums: $RELEASE_CHECKSUM_FILE" -ForegroundColor Green

# Also create GPG signature placeholder (requires gpg to be installed)
$GPG_SIG_PATH = "$RELEASE_DIR\checksums.txt.sig"
if (Get-Command gpg -ErrorAction SilentlyContinue) {
    gpg --detach-sign --armor "$RELEASE_CHECKSUM_FILE" -o "$GPG_SIG_PATH" 2>$null
    if ($LASTEXITCODE -eq 0) {
        Write-Host "[+] GPG signature: $GPG_SIG_PATH" -ForegroundColor Green
    } else {
        Write-Host "[!] GPG signing failed (no key?)" -ForegroundColor Yellow
    }
} else {
    Write-Host "[!] GPG not found, skipping signature (install gnupg for signatures)" -ForegroundColor Yellow
}

# Cleanup temp files
Remove-Item "$OUTPUT_DIR\build.log" -ErrorAction SilentlyContinue
Remove-Item "$OUTPUT_DIR\build-error.log" -ErrorAction SilentlyContinue

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

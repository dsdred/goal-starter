# BUILD-ARTIFACTS-01 bounded regression tests for the build ownership model.
#
# Run from the repository root:  powershell -File tests/scripts/build-root.test.ps1
#
# The tests exercise the layout/lock helpers exactly as the build script uses
# them (dot-sourcing scripts/goal-build-lib.ps1). Case E additionally runs a
# REAL scripts/build-all.ps1 invocation in a child process that already holds
# the worktree build lock, so the child must fail fast with the lock message
# without touching anything. Every case cleans up after itself; only gitignored
# build/ state and (in case E) a marker cmd/goal/rsrc.syso are created and all
# of it is removed again.

$ErrorActionPreference = "Stop"

# This file lives in tests/scripts/, so the repository root is two levels up.
$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
if (-not (Test-Path -LiteralPath (Join-Path $repoRoot "scripts/build-all.ps1"))) {
    throw "Could not locate the repository root from $PSScriptRoot"
}
# All fixtures are created relative to the repo root; be explicit about it.
Set-Location $repoRoot
. (Join-Path $repoRoot "scripts/goal-build-lib.ps1")

$script:passCount = 0
$script:failCount = 0

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if ($Condition) {
        $script:passCount++
        Write-Host "  PASS | $Message"
    } else {
        $script:failCount++
        Write-Host "  FAIL | $Message" -ForegroundColor Red
    }
}

function Get-DirectoryManifest {
    <# "relative-name`tsize`thexsha256" lines, sorted; empty for absent dirs. #>
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return @() }
    $base = (Resolve-Path -LiteralPath $Path).Path
    return @(Get-ChildItem -LiteralPath $Path -Recurse -File | Sort-Object FullName | ForEach-Object {
        $rel = $_.FullName.Substring($base.Length).TrimStart("\", "/")
        $rel + "`t" + $_.Length + "`t" + (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    })
}

function Get-FileSha {
    param([string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

Write-Host "BUILD-ROOT (build ownership model, BUILD-ARTIFACTS-01):"

# ---------------------------------------------------------------------------
# A. Consecutive build roots are unique and collision-safe.
# ---------------------------------------------------------------------------
$rootA1 = Resolve-GoalBuildRoot -Version "v9.9.9" -BaseDir "."
$rootA2 = Resolve-GoalBuildRoot -Version "v9.9.9" -BaseDir "."
Assert-True ($rootA1 -and $rootA2 -and ($rootA1 -ne $rootA2)) "A.1 consecutive build roots are distinct"
$leaf = [System.IO.Path]::GetFileName($rootA1)
Assert-True ([regex]::IsMatch($leaf, '^v9\.9\.9-\d{8}-\d{6}-[0-9a-f]{8}$')) "A.2 build root name is <version>-<yyyyMMdd-HHmmss>-<8-hex>"
Assert-True (Test-GoalBuildRoot -BuildRoot $rootA1 -Version "v9.9.9" -BaseDir ".") "A.3 Test-GoalBuildRoot accepts its own root"
Assert-True (-not (Test-GoalBuildRoot -BuildRoot $rootA1 -Version "v9.9.8" -BaseDir ".")) "A.4 Test-GoalBuildRoot rejects a version mismatch"
# A version-correct leaf that is NOT under build/ must be rejected by the
# containment check itself, not merely by the version-name prefix.
$rootA5 = Join-Path "." "v9.9.9-20260101-000000-00000000"
New-Item -ItemType Directory -Path $rootA5 -Force | Out-Null
Assert-True (-not (Test-GoalBuildRoot -BuildRoot $rootA5 -Version "v9.9.9" -BaseDir ".")) "A.5 Test-GoalBuildRoot rejects a version-correct path outside build/ (containment)"
Remove-Item -LiteralPath $rootA5 -Recurse -Force
Remove-Item -LiteralPath $rootA1 -Recurse -Force
Remove-Item -LiteralPath $rootA2 -Recurse -Force

# ---------------------------------------------------------------------------
# B. Same-version rebuild is non-destructive: a second allocation for the same
#    version leaves the first root and its contents untouched.
# ---------------------------------------------------------------------------
$rootB1 = Resolve-GoalBuildRoot -Version "v9.9.9" -BaseDir "."
Set-Content -LiteralPath (Join-Path $rootB1 "sentinel.txt") -Value "first-root" -NoNewline
$hashB1 = Get-FileSha -Path (Join-Path $rootB1 "sentinel.txt")
$rootB2 = Resolve-GoalBuildRoot -Version "v9.9.9" -BaseDir "."
Assert-True ($rootB2 -ne $rootB1) "B.1 same-version rebuild allocates a new root"
Assert-True ((Get-FileSha -Path (Join-Path $rootB1 "sentinel.txt")) -eq $hashB1) "B.2 the first root's bytes are untouched by the second allocation"
Remove-Item -LiteralPath $rootB1 -Recurse -Force
Remove-Item -LiteralPath $rootB2 -Recurse -Force

# ---------------------------------------------------------------------------
# C. Foreign files in build/ and previous build roots stay byte-identical.
# ---------------------------------------------------------------------------
New-Item -ItemType Directory -Path "build" -Force | Out-Null
Set-Content -LiteralPath "build/foreign-artifact.zip" -Value "foreign-bytes" -NoNewline
$rootCPrev = Resolve-GoalBuildRoot -Version "v9.9.8" -BaseDir "."
Set-Content -LiteralPath (Join-Path $rootCPrev "goal-v9.9.8-windows-amd64.zip") -Value "old-archive" -NoNewline
$foreignCHash = Get-FileSha -Path "build/foreign-artifact.zip"
$prevCManifest = (Get-DirectoryManifest -Path $rootCPrev) -join "`n"
$rootC = Resolve-GoalBuildRoot -Version "v9.9.9" -BaseDir "."
Assert-True ((Get-FileSha -Path "build/foreign-artifact.zip") -eq $foreignCHash) "C.1 foreign file in build/ is untouched by allocation"
Assert-True (((Get-DirectoryManifest -Path $rootCPrev) -join "`n") -eq $prevCManifest) "C.2 a previous build root is byte-identical after a new allocation"
Remove-Item -LiteralPath (Join-Path $rootCPrev "goal-v9.9.8-windows-amd64.zip") -Force
Remove-Item -LiteralPath $rootCPrev -Recurse -Force
Remove-Item -LiteralPath "build/foreign-artifact.zip" -Force
Remove-Item -LiteralPath $rootC -Recurse -Force

# ---------------------------------------------------------------------------
# D. Legacy bin/ and releases/ contents remain untouched.
# ---------------------------------------------------------------------------
New-Item -ItemType Directory -Path "bin" -Force | Out-Null
New-Item -ItemType Directory -Path "releases" -Force | Out-Null
Set-Content -LiteralPath "bin/goal.exe~" -Value "legacy-stray" -NoNewline
Set-Content -LiteralPath "releases/goal-v2.1.0-windows-amd64.zip" -Value "legacy-archive" -NoNewline
$binDBefore = (Get-DirectoryManifest -Path "bin") -join "`n"
$relDBefore = (Get-DirectoryManifest -Path "releases") -join "`n"
$rootD = Resolve-GoalBuildRoot -Version "v9.9.9" -BaseDir "."
Assert-True (((Get-DirectoryManifest -Path "bin") -join "`n") -eq $binDBefore) "D.1 legacy bin/ is byte-identical after a build-root allocation"
Assert-True (((Get-DirectoryManifest -Path "releases") -join "`n") -eq $relDBefore) "D.2 legacy releases/ is byte-identical after a build-root allocation"
Remove-Item -LiteralPath $rootD -Recurse -Force
Remove-Item -LiteralPath "bin/goal.exe~" -Force
Remove-Item -LiteralPath "releases/goal-v2.1.0-windows-amd64.zip" -Force

# ---------------------------------------------------------------------------
# E. A failing build cannot clean unrelated files: a real build-all.ps1
#    invocation that fails on the held lock leaves a pre-existing rsrc.syso
#    byte-identical, leaves foreign build/ files untouched, does not steal the
#    other holder's lock, and reports the clear failure message.
# ---------------------------------------------------------------------------
$preSyso = Join-Path "cmd/goal" "rsrc.syso"
$preSysoExisted = Test-Path -LiteralPath $preSyso
if (-not $preSysoExisted) {
    Set-Content -LiteralPath $preSyso -Value "PRE-EXISTING-SYSO-MARKER" -NoNewline
}
$preSysoHash = Get-FileSha -Path $preSyso
Set-Content -LiteralPath "build/foreign-during-failure.txt" -Value "keep-me" -NoNewline
$foreignEHash = Get-FileSha -Path "build/foreign-during-failure.txt"

$child = @"
`$ErrorActionPreference = "Stop"
. (Join-Path "$repoRoot" "scripts/goal-build-lib.ps1")
`$lock = Acquire-GoalBuildLock -WorktreeRoot "."
if (-not `$lock) { exit 99 }
`$childCode = 1
try { & (Join-Path "$repoRoot" "scripts/build-all.ps1") -ReleaseVersion "v9.9.9-e2e" } catch { Write-Host `$_.Exception.Message; `$childCode = 1 }
`$lock.Dispose()
exit `$childCode
"@
$childFile = Join-Path ([System.IO.Path]::GetTempPath()) ("goal-build-root-test-child-" + [guid]::NewGuid().ToString("N") + ".ps1")
[System.IO.File]::WriteAllText($childFile, $child, (New-Object System.Text.UTF8Encoding($false)))
# The child's stderr is intentionally merged and read as data; under the
# test's $ErrorActionPreference = "Stop" that would be terminating, so scope
# it to Continue for the invocation and capture the exit code immediately.
$previousEap = $ErrorActionPreference
$ErrorActionPreference = "Continue"
$buildEntriesBefore = @(Get-ChildItem -LiteralPath "build" -Directory -ErrorAction SilentlyContinue | ForEach-Object { $_.Name } | Sort-Object)
try {
    $eLines = @( & powershell -NoProfile -File $childFile 2>&1 | ForEach-Object { "$_" } )
    $eExit = $LASTEXITCODE
} finally {
    Remove-Item -LiteralPath $childFile -Force -ErrorAction SilentlyContinue
    $ErrorActionPreference = $previousEap
}
Assert-True (($eExit -ne 0) -and (@($eLines | Where-Object { $_ -match "build lock is held" }).Count -ge 1)) "E.1 the locked-out real build fails (exit $eExit) with the lock message"
Assert-True ((Get-FileSha -Path $preSyso) -eq $preSysoHash) "E.2 the pre-existing rsrc.syso is byte-identical after the failed build"
Assert-True ((Get-FileSha -Path "build/foreign-during-failure.txt") -eq $foreignEHash) "E.3 the foreign build/ file is untouched after the failed build"
$buildEntriesAfter = @(Get-ChildItem -LiteralPath "build" -Directory -ErrorAction SilentlyContinue | ForEach-Object { $_.Name } | Sort-Object)
Assert-True (@(Compare-Object -ReferenceObject $buildEntriesBefore -DifferenceObject $buildEntriesAfter).Count -eq 0) "E.4 the locked-out build allocated no build root (the lock precedes root allocation)"
Remove-Item -LiteralPath "build/foreign-during-failure.txt" -Force
if (-not $preSysoExisted) {
    Remove-Item -LiteralPath $preSyso -Force
}

# ---------------------------------------------------------------------------
# F. Resource-file ownership and concurrent invocation safety: the lock is
#    exclusive per worktree and is released when the holder releases it.
# ---------------------------------------------------------------------------
$lockF1 = Acquire-GoalBuildLock -WorktreeRoot "."
Assert-True ($null -ne $lockF1) "F.1 first lock acquisition from this worktree succeeds"
$lockF2 = Acquire-GoalBuildLock -WorktreeRoot "."
Assert-True ($null -eq $lockF2) "F.2 a concurrent acquisition from the same worktree is refused"
Release-GoalBuildLock -LockStream $lockF1
$lockF3 = Acquire-GoalBuildLock -WorktreeRoot "."
Assert-True ($null -ne $lockF3) "F.3 the lock is available again after release"
Release-GoalBuildLock -LockStream $lockF3

# ---------------------------------------------------------------------------
# G. The current build output contains the expected artifact set (the
#    workflow's Assert-ExactFileSet contract over a simulated current root).
# ---------------------------------------------------------------------------
$rootG = Resolve-GoalBuildRoot -Version "v9.9.9" -BaseDir "."
foreach ($f in @("bin/goal-windows-amd64.exe", "bin/goal-linux-amd64", "bin/checksums.txt", "releases/goal-v9.9.9-windows-amd64.zip", "releases/goal-v9.9.9-linux-amd64.tar.gz", "releases/checksums.txt")) {
    $p = Join-Path $rootG $f
    New-Item -ItemType Directory -Path (Split-Path -Parent $p) -Force | Out-Null
    Set-Content -LiteralPath $p -Value $f -NoNewline
}
function Assert-ExactFileSet {
    param([string]$Directory, [string[]]$ExpectedNames)
    $actualNames = @(Get-ChildItem -LiteralPath $Directory -File | ForEach-Object { $_.Name } | Sort-Object)
    $expected = @($ExpectedNames | Sort-Object)
    $difference = @(Compare-Object -ReferenceObject $expected -DifferenceObject $actualNames)
    if ($difference.Count -ne 0) {
        $details = $difference | ForEach-Object { "$($_.SideIndicator) $($_.InputObject)" }
        throw "Unexpected artifact set in '$Directory': $($details -join ', ')"
    }
}
$gOk = $true
try {
    Assert-ExactFileSet -Directory (Join-Path $rootG "bin") -ExpectedNames @("goal-windows-amd64.exe", "goal-linux-amd64", "checksums.txt")
    Assert-ExactFileSet -Directory (Join-Path $rootG "releases") -ExpectedNames @("goal-v9.9.9-windows-amd64.zip", "goal-v9.9.9-linux-amd64.tar.gz", "checksums.txt")
} catch { $gOk = $false }
Assert-True $gOk "G.1 the current build root carries exactly the contracted artifact set"
# The detection branch must actually detect: a stray file in bin/ or releases/
# makes the assertion throw.
$gNegative = $false
try {
    Set-Content -LiteralPath (Join-Path $rootG "bin/stray-artifact.exe") -Value "stray" -NoNewline
    Assert-ExactFileSet -Directory (Join-Path $rootG "bin") -ExpectedNames @("goal-windows-amd64.exe", "goal-linux-amd64", "checksums.txt")
} catch { $gNegative = $true }
Assert-True $gNegative "G.2 a stray file in the current build root is detected by the exact-set assertion"
Remove-Item -LiteralPath $rootG -Recurse -Force

# ---------------------------------------------------------------------------
# H. The release workflow resolves exactly the current invocation's assets:
#    the OUTDIR hand-off line is unique and parseable, Test-GoalBuildRoot
#    validates it before consumption, and the five upload paths under it
#    resolve; directories outside build/ are rejected (no newest-directory
#    discovery).
# ---------------------------------------------------------------------------
$rootH = Resolve-GoalBuildRoot -Version "v9.9.9" -BaseDir "."
$absH = (Resolve-Path -LiteralPath $rootH).Path
$simulatedOutput = @("=== GoAl Build Script ===", "OUTDIR=$absH", "+ Building for Windows amd64...")
$outDirLines = @($simulatedOutput | Where-Object { $_ -match '^OUTDIR=' })
Assert-True ($outDirLines.Count -eq 1) "H.1 exactly one OUTDIR line is reported"
$rootH2 = $outDirLines[0].Substring(7)
Assert-True (Test-GoalBuildRoot -BuildRoot $rootH2 -Version "v9.9.9" -BaseDir ".") "H.2 the captured OUTDIR validates as the current invocation root"
$expectedUpload = @("bin/goal-windows-amd64.exe", "bin/goal-linux-amd64", "releases/goal-v9.9.9-windows-amd64.zip", "releases/goal-v9.9.9-linux-amd64.tar.gz", "releases/checksums.txt")
foreach ($f in $expectedUpload) {
    $p = Join-Path $rootH2 $f
    New-Item -ItemType Directory -Path (Split-Path -Parent $p) -Force | Out-Null
    Set-Content -LiteralPath $p -Value $f -NoNewline
}
$allResolve = $true
foreach ($f in $expectedUpload) {
    if (-not (Test-Path -LiteralPath (Join-Path $rootH2 $f))) { $allResolve = $false }
}
Assert-True $allResolve "H.3 the five upload paths under the captured OUTDIR all resolve"
# A version-correct directory OUTSIDE build/ must fail the OUTDIR validation
# by the build/ containment check, not by the version-name prefix.
$outside = Join-Path (Get-Location).Path "v9.9.9-20260101-999999-00000000"
New-Item -ItemType Directory -Path $outside -Force | Out-Null
Set-Content -LiteralPath (Join-Path $outside "goal-windows-amd64.exe") -Value "stale" -NoNewline
Assert-True (-not (Test-GoalBuildRoot -BuildRoot $outside -Version "v9.9.9" -BaseDir ".")) "H.4 a version-correct directory outside build/ is rejected by the OUTDIR validation (containment, no newest-directory discovery)"
Remove-Item -LiteralPath $rootH -Recurse -Force
Remove-Item -LiteralPath $outside -Recurse -Force

# ---------------------------------------------------------------------------
# I. Fail-closed resource embedding: with winres/winres.json temporarily
#    absent, a pre-existing cmd/goal/rsrc.syso must NOT be silently embedded
#    — the real build-all.ps1 must refuse (non-zero exit, clear message),
#    leaving the file byte-identical for manual inspection, and winres.json
#    is restored byte-for-byte by this test afterwards.
# ---------------------------------------------------------------------------
$winresJson = Join-Path "winres" "winres.json"
$winresBak = Join-Path "winres" ("winres.json.i-backup-" + [guid]::NewGuid().ToString("N"))
$winresOriginalHash = Get-FileSha -Path $winresJson
Move-Item -LiteralPath $winresJson -Destination $winresBak
try {
    $preSysoI = Join-Path "cmd/goal" "rsrc.syso"
    $preSysoIExisted = Test-Path -LiteralPath $preSysoI
    if (-not $preSysoIExisted) {
        Set-Content -LiteralPath $preSysoI -Value "PRE-EXISTING-MARKER-FAILCLOSED" -NoNewline
    }
    $preSysoIHash = Get-FileSha -Path $preSysoI

    $childI = @"
`$ErrorActionPreference = "Stop"
& (Join-Path "$repoRoot" "scripts/build-all.ps1") -ReleaseVersion "v9.9.9-fc1"
exit `$LASTEXITCODE
"@
    $childIFile = Join-Path ([System.IO.Path]::GetTempPath()) ("goal-build-root-test-childI-" + [guid]::NewGuid().ToString("N") + ".ps1")
    [System.IO.File]::WriteAllText($childIFile, $childI, (New-Object System.Text.UTF8Encoding($false)))
    $previousEapI = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $iLines = @( & powershell -NoProfile -File $childIFile 2>&1 | ForEach-Object { "$_" } )
        $iExit = $LASTEXITCODE
    } finally {
        Remove-Item -LiteralPath $childIFile -Force -ErrorAction SilentlyContinue
        $ErrorActionPreference = $previousEapI
    }
    Assert-True (($iExit -ne 0) -and (@($iLines | Where-Object { $_ -match "Refusing to build" }).Count -ge 1)) "I.1 the build refuses (exit $iExit) when winres.json is absent and rsrc.syso pre-exists (fail-closed)"
    Assert-True ((Get-FileSha -Path $preSysoI) -eq $preSysoIHash) "I.2 the pre-existing rsrc.syso is left byte-identical for manual inspection"

    # The refused build still allocated its (empty) root before failing. The
    # OUTDIR hand-off MUST be pipeline-capturable in this scenario as well:
    # if it is missing or invalid here, the real-capture regression (case J)
    # is blind, so this test refuses to pass silently — the root must be
    # removed through the same validated hand-off path, never skipped.
    $iOutDirs = @($iLines | Where-Object { $_ -match '^OUTDIR=' })
    $iCleanupValid = $false
    if ($iOutDirs.Count -eq 1) {
        $iRoot = $iOutDirs[0].Substring(7)
        if ((Test-GoalBuildRoot -BuildRoot $iRoot -Version "v9.9.9-fc1" -BaseDir ".")) {
            Remove-Item -LiteralPath $iRoot -Recurse -Force
            $iCleanupValid = $true
        }
    }
    Assert-True $iCleanupValid "I.5 the refused build's root is removed through a valid, capturable OUTDIR hand-off (no silent skip)"
    Assert-True (-not (Test-Path -LiteralPath (Join-Path "winres" "winres.generated.json"))) "I.3 no transient winres state survives the refused build"
} finally {
    if ((Test-Path -LiteralPath $winresBak) -and (-not (Test-Path -LiteralPath $winresJson))) {
        Move-Item -LiteralPath $winresBak -Destination $winresJson
    }
}
Assert-True ((Get-FileSha -Path $winresJson) -eq $winresOriginalHash) "I.4 winres/winres.json is restored byte-for-byte by this test"
if (-not $preSysoIExisted) {
    Remove-Item -LiteralPath $preSysoI -Force
}

# ---------------------------------------------------------------------------
# J. Real OUTDIR capture through the release workflow's exact mechanism
#    (no simulated output array). J.1-J.4 invoke the production
#    scripts/build-all.ps1 IN-PROCESS — the same call form the workflow's
#    build step uses (release.yml: $outDirLines = @(& .\scripts\build-all.ps1
#    -ReleaseVersion $env:RELEASE_VERSION 4>&1 | ForEach-Object { "$_" })) —
#    and run one real (unsigned) build. The child-process cases E and I
#    capture a different channel (the child's process stdout) and cannot
#    substitute: in-process host-UI emission does not enter that pipeline.
#    J.5-J.6 prove failure propagation for a failed (locked-out) build.
# ---------------------------------------------------------------------------
$jPreviousEap = $ErrorActionPreference
$ErrorActionPreference = "Continue"
$jLines = @(& (Join-Path $repoRoot "scripts/build-all.ps1") -ReleaseVersion "v9.9.9-rcap" 4>&1 | ForEach-Object { "$_" })
$ErrorActionPreference = $jPreviousEap
$jOutDirs = @($jLines | Where-Object { $_ -match '^OUTDIR=' })
Assert-True ($jOutDirs.Count -eq 1) "J.1 exactly one OUTDIR record is pipeline-captured from the real in-process build (found $($jOutDirs.Count))"
$jRoot = $null
if ($jOutDirs.Count -eq 1) { $jRoot = $jOutDirs[0].Substring(7) }
Assert-True ($jRoot -and (Test-GoalBuildRoot -BuildRoot $jRoot -Version "v9.9.9-rcap" -BaseDir ".")) "J.2 the captured OUTDIR validates as a valid absolute root of the real build"
$jAssetsOk = $false
if ($jRoot) {
    $jExpected = @("bin/goal-windows-amd64.exe", "bin/goal-linux-amd64", "releases/goal-v9.9.9-rcap-windows-amd64.zip", "releases/goal-v9.9.9-rcap-linux-amd64.tar.gz", "releases/checksums.txt")
    $jAllResolve = $true
    foreach ($f in $jExpected) {
        if (-not (Test-Path -LiteralPath (Join-Path $jRoot $f))) { $jAllResolve = $false }
    }
    $jAssetsOk = $jAllResolve
}
Assert-True $jAssetsOk "J.3 the exact five upload assets resolve under the captured root"
$jCleaned = $false
if ($jRoot -and (Test-GoalBuildRoot -BuildRoot $jRoot -Version "v9.9.9-rcap" -BaseDir ".")) {
    Remove-Item -LiteralPath $jRoot -Recurse -Force
    $jCleaned = $true
}
Assert-True $jCleaned "J.4 the real build root is removed through the validated hand-off path"

# Failure propagation: a locked-out (failing) build must never be accepted as
# successful. It still emits its pre-lock OUTDIR line; the workflow accepts a
# build only on a successful (non-exiting) in-process invocation, which a
# failed build cannot produce — here proven via the child process exit code
# (the in-process trap path calls exit 1, which would terminate this session).
$lockJ = Acquire-GoalBuildLock -WorktreeRoot "."
$childJ = @"
& (Join-Path "$repoRoot" "scripts/build-all.ps1") -ReleaseVersion "v9.9.9-rcap2"
exit `$LASTEXITCODE
"@
$childJFile = Join-Path ([System.IO.Path]::GetTempPath()) ("goal-build-root-test-childJ-" + [guid]::NewGuid().ToString("N") + ".ps1")
[System.IO.File]::WriteAllText($childJFile, $childJ, (New-Object System.Text.UTF8Encoding($false)))
$j2PreviousEap = $ErrorActionPreference
$ErrorActionPreference = "Continue"
try {
    $j2Lines = @( & powershell -NoProfile -File $childJFile 2>&1 | ForEach-Object { "$_" })
    $j2Exit = $LASTEXITCODE
} finally {
    Remove-Item -LiteralPath $childJFile -Force -ErrorAction SilentlyContinue
    $ErrorActionPreference = $j2PreviousEap
    Release-GoalBuildLock -LockStream $lockJ
}
$j2OutDirs = @($j2Lines | Where-Object { $_ -match '^OUTDIR=' })
Assert-True (($j2Exit -ne 0) -and (@($j2Lines | Where-Object { $_ -match "build lock is held" }).Count -ge 1)) "J.5 the locked-out build fails (exit $j2Exit) with the lock message"
Assert-True (($j2Exit -ne 0) -and ($j2OutDirs.Count -le 1)) "J.6 a failed build cannot be accepted as successful: non-zero exit despite $($j2OutDirs.Count) pre-lock OUTDIR line(s)"

# ---------------------------------------------------------------------------
$script:total = $script:passCount + $script:failCount
Write-Host "BUILD-ROOT (build ownership model): TOTAL $script:total | PASS $($script:passCount) | FAIL $($script:failCount) | SKIP 0"
if ($script:failCount -gt 0) { exit 1 }
exit 0

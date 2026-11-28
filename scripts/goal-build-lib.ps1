# Goal build layout and ownership helpers.
# Dot-sourced by scripts/build-all.ps1 and tests/scripts/build-root.test.ps1.
#
# Ownership model: every build invocation allocates its own unique root under
# build/ (build/<version>-<timestamp>-<unique-id>/) and may only create,
# modify or delete paths inside its own root, plus the cmd/goal/rsrc.syso it
# generated itself (a pre-existing rsrc.syso is preserved byte-for-byte and
# restored after the build). A build never touches legacy bin/ or releases/,
# previous build roots, or foreign files. Concurrent invocations from the same
# worktree are serialized by an exclusive OS-level lock; the second
# invocation fails fast with a clear message instead of interleaving the
# shared source-tree resource file.

function Resolve-GoalBuildRoot {
    <#
    Allocates and creates a NEW, collision-safe build root under
    <BaseDir>/build/. The name is <version>-<yyyyMMdd-HHmmss>-<8-hex-guid>;
    creation uses New-Item without -Force, which fails on an existing path,
    so an existing root is never reused. Returns the created path, or $null
    if no fresh root could be allocated after $MaxAttempts tries.
    #>
    param(
        [Parameter(Mandatory = $true)][string]$Version,
        [string]$BaseDir = ".",
        [int]$MaxAttempts = 5
    )
    for ($attempt = 1; $attempt -le $MaxAttempts; $attempt++) {
        $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
        $candidate = Join-Path $BaseDir ("build/" + $Version + "-" + $stamp + "-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
        try {
            New-Item -ItemType Directory -Path $candidate -ErrorAction Stop | Out-Null
            return $candidate
        } catch {
            continue
        }
    }
    return $null
}

function Test-GoalBuildRoot {
    <#
    Validates a resolved build root before any workflow step consumes it:
    the path is absolute, lives under <BaseDir>/build/, its name starts with
    the expected version, and it exists.
    #>
    param(
        [Parameter(Mandatory = $true)][string]$BuildRoot,
        [Parameter(Mandatory = $true)][string]$Version,
        [string]$BaseDir = "."
    )
    if (-not $BuildRoot) { return $false }
    $abs = $null
    try { $abs = (Resolve-Path -LiteralPath $BuildRoot -ErrorAction Stop).Path } catch { return $false }
    if (-not [System.IO.Path]::IsPathRooted($abs)) { return $false }
    $base = (Resolve-Path -LiteralPath (Join-Path $BaseDir "build") -ErrorAction Stop).Path
    if (-not ($abs.StartsWith($base + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase))) { return $false }
    $leaf = [System.IO.Path]::GetFileName($abs)
    if (-not $leaf.StartsWith($Version + "-", [System.StringComparison]::Ordinal)) { return $false }
    return $true
}

function Acquire-GoalBuildLock {
    <#
    Takes an exclusive OS-level lock keyed by the worktree path, so two
    invocations from the same worktree can never interleave their
    cmd/goal/rsrc.syso generation and cleanup. Returns the open FileStream
    while the lock is held, or $null if another build already holds it.
    The lock file itself lives in the OS temp directory, never in the repo.
    #>
    param([Parameter(Mandatory = $true)][string]$WorktreeRoot)
    $sha = [System.Security.Cryptography.SHA1]::Create()
    $key = [System.BitConverter]::ToString(
        $sha.ComputeHash([System.Text.Encoding]::UTF8.GetBytes((Resolve-Path -LiteralPath $WorktreeRoot).Path))
    ).Replace("-", "").Substring(0, 12).ToLowerInvariant()
    $lockPath = Join-Path ([System.IO.Path]::GetTempPath()) ("goal-build-" + $key + ".lock")
    try {
        return [System.IO.File]::Open($lockPath, [System.IO.FileMode]::OpenOrCreate, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::None)
    } catch [System.IO.IOException] {
        return $null
    }
}

function Release-GoalBuildLock {
    param([System.IO.FileStream]$LockStream)
    if ($null -ne $LockStream) {
        try { $LockStream.Dispose() } catch { }
    }
}

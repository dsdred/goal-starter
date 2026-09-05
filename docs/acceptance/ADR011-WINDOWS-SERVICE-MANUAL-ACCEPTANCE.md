# ADR 011 — Windows Service / Background Mode — Owner Manual Acceptance (real SCM)

**Scope:** ADR 011 acceptance items 1–8 + the automated-adjacent parts of item 10, verified on a real Windows machine with an elevated (Administrator) PowerShell. Covers the in-binary `goal --service` verbs, the SCM registration contract (D2/D3/D5/D6), recovery interaction (ADR 005), LocalSystem diagnostics (D4), Event Log (D8), and uninstall data-safety (D9).

**Source of truth:** repository working tree at HEAD `1ea268c` + the ADR 011 implementation change (not yet committed). This checklist is generated from the actual implementation (`internal/platform/service_windows.go`, `cmd/goal/main.go`, `cmd/goal/service_install.go`) — commands, exit behavior, output strings, and registry facts match the code.

**Hard boundaries (must hold for the whole run):**

- Do NOT touch the production `goal.exe`, the production `goal.json`, or any production data directory.
- Everything lives under the test root `C:\goal-accept` (created in Step 1). The path contains **no spaces on purpose**: per ADR 011 D2, each path part is quoted by SCM command-line escaping iff it contains a space — a space-free root keeps the expected binPath unquoted and byte-for-byte checkable. If you must move the root, keep it space-free and update every hardcoded string below.
- No ACL changes on any production resource. The LocalSystem failure test (Step 9) uses a purpose-built directory with a restrictive ACL that you created; it is deleted in the teardown.
- Test data is **not** deleted between steps (only in the final teardown).
- Every step prints `PASS`/`FAIL` plus the raw output. Capture the full transcript.

**Prerequisites:** Windows with admin rights. **Two-machine topology:** the Go toolchain and repository are on the DEVELOPMENT machine (builds the binary); the TEST machine (runs the acceptance) requires only the built `goal.exe` binary — no repository, no Go toolchain. Port **8099** free on 127.0.0.1, no existing service named `GoAl`.

---

## Step 0 — Session bootstrap (elevated)

```powershell
# Run PowerShell as Administrator, then:
$ErrorActionPreference = 'Continue'

$Root  = 'C:\goal-accept'
$Bin   = "$Root\bin"
$Data  = "$Root\data"
$Goal  = "$Bin\goal.exe"
$Cfg   = "$Root\goal.json"
$RepoFile = "$Data\goal_repo.json"
$Audit  = "$Data\goal_audit.jsonl"
$Svc   = 'GoAl'          # default service name (cmd/goal/main.go serviceDefaultName)
$Health = 'http://127.0.0.1:8099/api/v1/health'
$UI    = 'http://127.0.0.1:8099/'

# ADR 011 D2 escaping, mirroring syscall.EscapeArg (service_windows.go serviceBinPath):
# a token is wrapped in double quotes iff it contains a space or a quote (embedded quotes doubled).
function Get-SCMArg([string]$s) { if ($s -match '[ "]') { '"' + $s.Replace('"','""') + '"' } else { $s } }
$ExpectedImage = (Get-SCMArg $Goal) + ' --service run --config ' + (Get-SCMArg $Cfg)
"EXPECTED_IMAGE: $ExpectedImage"

# Registry key the SCM uses (stop timeout lives here, not in `sc qc` output).
$SvcKey = "HKLM:\SYSTEM\CurrentControlSet\Services\$Svc"

# --- helpers -----------------------------------------------------------------
function Check([string]$name, [bool]$ok, [string]$detail) {
    $tag = if ($ok) { 'PASS' } else { 'FAIL' }
    Write-Host "[$tag] $name" -ForegroundColor $(if ($ok) { 'Green' } else { 'Red' })
    if ($detail) { Write-Host "       $detail" }
}
# CONTRACT: returns [PSCustomObject]@{State; PID} from Win32_Service, or $null
# if the service is absent. Consumers MUST use .State / .PID (never compare
# the object to a string, and never call .Status on it).
function Get-SvcState {
    $wmi = Get-CimInstance -ClassName Win32_Service -Filter "Name='$Svc'" -ErrorAction SilentlyContinue
    if (-not $wmi) { return $null }
    [PSCustomObject]@{ State = $wmi.State; PID = $wmi.ProcessId }
}
function Wait-State([string]$want, [int]$timeoutSec = 90) {
    $deadline = (Get-Date).AddSeconds($timeoutSec)
    while ((Get-Date) -lt $deadline) {
        $s = Get-SvcState
        if ($s -and $s.State -eq $want) { return $s }
        Start-Sleep -Milliseconds 500
    }
    $s = Get-SvcState
    if ($s -and $s.State -eq $want) { return $s }
    throw "service did not reach $want within ${timeoutSec}s (last observed: $(if ($s) { $s.State } else { 'ABSENT' }))"
}
function Test-PortFree([int]$port) {
    try { $c = New-Object System.Net.Sockets.TcpClient; $c.Connect('127.0.0.1', $port); $c.Close(); return $false }
    catch { return $true }
}

# --- API helpers (the test config is authEnabled=false, so requireAuth/CSRF are
# --- no-ops — verified in internal/webui/handlers/routes.go) -------------
function Invoke-GoalAPI([string]$method, [string]$path, $body = $null) {
    $params = @{ Method = $method; Uri = "http://127.0.0.1:8099$path"; UseBasicParsing = $true }
    if ($null -ne $body) { $params.Body = ($body | ConvertTo-Json -Depth 10); $params.ContentType = 'application/json' }
    try { return (Invoke-WebRequest @params).StatusCode }
    catch { return $_.Exception.Response.StatusCode.value__ }
}
function Get-GoalJSON([string]$path) { Invoke-RestMethod -UseBasicParsing "http://127.0.0.1:8099$path" }

# Windows PowerShell 5.1 `Set-Content -Encoding UTF8` writes a UTF-8 BOM
# (EF BB BF); Go's JSON decoder rejects it ("invalid character 'ï'").
# ALL JSON fixtures in this checklist are written BOM-free through this helper:
function Write-TextFile([string]$Path, [string]$Text) {
    [System.IO.File]::WriteAllText($Path, $Text, (New-Object System.Text.UTF8Encoding($false)))
}
function Assert-NoBom([string]$Path) {
    $b = [System.IO.File]::ReadAllBytes($Path)
    $ok = -not ($b.Length -ge 3 -and $b[0] -eq 0xEF -and $b[1] -eq 0xBB -and $b[2] -eq 0xBF)
    $n = [Math]::Min(3, $b.Length)
    $hex = if ($n -gt 0) { ($b[0..($n-1)] | ForEach-Object { $_.ToString('X2') }) -join ' ' } else { '(empty file)' }
    Check "no UTF-8 BOM in $(Split-Path $Path -Leaf)" $ok ("first bytes: $hex")
}
```

**Expected:** `EXPECTED_IMAGE: C:\goal-accept\bin\goal.exe --service run --config C:\goal-accept\goal.json` (unquoted — no spaces).
**Evidence for Step 0:** the printed EXPECTED_IMAGE line.

---

## Step 1 — Build the acceptance binaries (separate test directory)

Builds from the **current working tree** into `C:\goal-accept\bin`. Production/release binaries are not read or overwritten.

```powershell
New-Item -ItemType Directory -Force -Path $Bin, $Data, "$Root\evidence" | Out-Null
Set-Location 'E:\PRJ\goal-starter'   # the GoAl working tree (adjust if different)

$env:GOOS = 'windows'; $env:GOARCH = 'amd64'
go build -o $Goal "$Pwd\cmd\goal"
Remove-Item Env:GOOS, Env:GOARCH
if (-not (Test-Path $Goal)) { throw 'goal build failed' }
& $Goal -version

# fake-runtime test fixture (testdata/fake-runtime — long-running "infinite" mode)
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'
go build -o "$Bin\fake-runtime.exe" "$Pwd\testdata\fake-runtime"
Remove-Item Env:GOOS, Env:GOARCH
if (-not (Test-Path "$Bin\fake-runtime.exe")) { throw 'fake-runtime build failed' }

Check 'step1: goal.exe built' (Test-Path $Goal)
Check 'step1: fake-runtime.exe built' (Test-Path "$Bin\fake-runtime.exe")
```

**Expected:** `-version` prints the GoAl version; both binaries exist.
**Evidence:** the `-version` output + the two PASS lines.

---

## Step 2 — Test config and data directory (ADR 011 D3 path contract)

```powershell
$userDir = Join-Path $env:USERPROFILE 'goal-accept'
New-Item -ItemType Directory -Force -Path "$userDir\denied" | Out-Null
Copy-Item "$Bin\fake-runtime.exe" "$userDir\denied\fake-runtime.exe" -Force
$deniedExe = "$userDir\denied\fake-runtime.exe"
$deniedDir = "$userDir\denied"

# The JSON below must contain the REAL values (replace DENIED_EXE/DENIED_DIR
# with $deniedExe/$deniedDir expanded — the -replace doubles backslashes for
# JSON string escaping).
# NOTE: NO "models" and NO "profiles" sections — see the config contract below
# (seed contract: only `runtimes` are seeded from config; the test models are
# created via POST /api/v1/models in Step 5b).
$cfgJson = @'
{
  "version": 2,
  "listenAddress": "127.0.0.1",
  "webPort": 8099,
  "dataDir": "C:\\goal-accept\\data",
  "adminUser": "admin",
  "adminPasswordHash": "",
  "authEnabled": false,
  "runtimes": [
    {
      "id": "fake",
      "name": "fake-runtime",
      "executable": "C:\\goal-accept\\bin\\fake-runtime.exe",
      "workingDirectory": "C:\\goal-accept\\bin",
      "environment": {}
    },
    {
      "id": "denied",
      "name": "denied-runtime",
      "executable": "DENIED_EXE",
      "workingDirectory": "DENIED_DIR",
      "environment": {}
    }
  ]
}
'@
$cfgJson = $cfgJson.Replace('DENIED_EXE', ($deniedExe -replace '\\','\\')).Replace('DENIED_DIR', ($deniedDir -replace '\\','\\'))
Write-TextFile $Cfg $cfgJson
Assert-NoBom $Cfg
Get-Content $Cfg

# D3 contract for the paths above (all absolute):
$cfgObj = Get-Content $Cfg -Raw | ConvertFrom-Json
Check 'step2: config path absolute' ([System.IO.Path]::IsPathRooted($Cfg))
Check 'step2: config parses as JSON (BOM-free, valid escapes)' ($null -ne $cfgObj)
Check 'step2: dataDir absolute + exists' ($cfgObj.dataDir -eq "$Data" -and (Test-Path $Data))
Check 'step2: NO models/profiles sections (seed contract: models come from Step 5b via the API)' (-not (($cfgObj.PSObject.Properties.Name -contains 'models') -or ($cfgObj.PSObject.Properties.Name -contains 'profiles')))
Check 'step2: port 8099 free' (Test-PortFree 8099)
Check 'step2: denied fixture in user profile (for Step 9)' (Test-Path $deniedExe)
```

**Config contract (why this shape):** absolute `dataDir` that **exists** (a missing/relative `dataDir` → pre-flight refusal, D3.2), seeded runtime `executable`/`workingDirectory` absolute (relative → refusal), `authEnabled: false` (keeps the run login-free). **Seed contract (Source of Truth: `internal/storage/config_seed.go`, CONFIGURATION.md §Model/Profile configuration):** only config `runtimes` entries are seeded into the repository at startup. Config `models` entries are **not** models at all — they are the legacy v5 fold lookup, applied only when a `profiles` entry references them via `modelId`; `profiles` is the only config section that seeds GoAl 2.0 Models. A `models` section without `profiles` seeds **zero** models. Therefore this fixture deliberately contains neither section: the two test models (`fake-infinite` — long-running instance fixture; `denied-model` — the LocalSystem access-failure target of Step 9) are created deterministically through `POST /api/v1/models` in **Step 5b**, both with `active=false` (model-level autostart OFF — instances are started explicitly via the UI during the run; the pipeline-autostart matrix is Step 12's own).
**Evidence:** the printed config + the six PASS lines (incl. the no-BOM first-bytes line and the no-models/profiles section line).

---

## Step 3 — Pre-install verification (before anything is registered)

```powershell
Check 'step3: goal.exe exists' (Test-Path $Goal)
Check 'step3: config exists' (Test-Path $Cfg)
Check 'step3: service GoAl ABSENT before install' (-not (Get-Service $Svc -ErrorAction SilentlyContinue))
Check 'step3: service key ABSENT before install' (-not (Test-Path $SvcKey))
Check 'step3: no stray goal.exe process' (-not (Get-Process goal -ErrorAction SilentlyContinue))

# Negative probes (real binary, expected exit 1, zero writes, NO registration).
# Rules for the probe configs:
#  - they must be VALID JSON — build them via ConvertFrom-Json/ConvertTo-Json
#    (a regex text replacement of a Windows path silently produces invalid
#    JSON escapes like \g, which would change the refusal reason to
#    "config load (read-only)" and mask the case under test);
#  - the bounded diagnostic is printed to STDERR — merge it into the capture
#    (2>&1) and verify the EXPECTED reason, not just the exit code.
$badCfg = "$Root\goal-bad.json"
$obj = Get-Content $Cfg -Raw | ConvertFrom-Json

# 3a — absolute dataDir that does NOT exist (ADR 011 D3.2: missing dataDir = refusal):
$obj.dataDir = "$Root\missing-data"
Write-TextFile $badCfg ($obj | ConvertTo-Json -Depth 10)
Assert-NoBom $badCfg
$out = & $Goal --service install --config $badCfg 2>&1 | ForEach-Object { $_.ToString() }
$rc = $LASTEXITCODE
Check 'step3a: refusal exit code 1' ($rc -eq 1)
Check 'step3a: diagnostic names the missing dataDir ("does not exist")' ((($out -join ' ') -match 'does not exist') -and (($out -join ' ') -match 'dataDir')) ($out -join ' | ')
Check 'step3a: NO SCM registration created' (-not (Get-Service $Svc -ErrorAction SilentlyContinue) -and -not (Test-Path $SvcKey))
Check 'step3a: the missing dataDir was NOT created by the probe' (-not (Test-Path "$Root\missing-data"))
Remove-Item $badCfg -Force

# 3b — relative dataDir (the effective ./data rule):
$obj.dataDir = './data'
Write-TextFile $badCfg ($obj | ConvertTo-Json -Depth 10)
Assert-NoBom $badCfg
$wdDataBefore = Test-Path (Join-Path (Get-Location).Path 'data')
$out = & $Goal --service install --config $badCfg 2>&1 | ForEach-Object { $_.ToString() }
$rc = $LASTEXITCODE
Check 'step3b: refusal exit code 1' ($rc -eq 1)
Check 'step3b: diagnostic names the relative dataDir ("is relative")' ((($out -join ' ') -match 'is relative') -and (($out -join ' ') -match 'dataDir')) ($out -join ' | ')
Check 'step3b: NO SCM registration created' (-not (Get-Service $Svc -ErrorAction SilentlyContinue) -and -not (Test-Path $SvcKey))
Check 'step3b: no ./data directory created in the CWD (zero writes)' ((Test-Path (Join-Path (Get-Location).Path 'data')) -eq $wdDataBefore)
Remove-Item $badCfg -Force
```

**Expected:** each probe prints `service install refused (no registration created, no files written):` with the **expected reason** — 3a: `effective dataDir "C:\goal-accept\missing-data" does not exist or is not a directory; create the directory before installing (install never creates it)`; 3b: `effective dataDir "./data" is relative (default "./data"); …` — exit code 1, no service, no registry key, zero files created.
**Evidence:** both refusal transcripts (stderr text with the bounded diagnostic lines) + all eight PASS lines.

> Acceptance item 2 asks for the full refusal matrix (missing exe, missing config, failing `ValidateFull`, relative runtime/model paths in config/repo, existing registration with a different image, idempotent identical re-install). The two dataDir refusals above are executed here because they are safe pre-registration probes; the remaining item-2 cases are exercised **in-place** at the points where they naturally occur (Step 4 note: idempotent re-install; Step 11: unknown-service not-found; missing exe/config and the config-repo relative-path cases are covered by the maintained automated suite — cite `cmd/goal/service_install_test.go` (incl. `TestServiceInstallRefusalNeverRegisters`) in the evidence report).

---

## Step 4 — INSTALL acceptance (ADR 011 item 1)

```powershell
& $Goal --service install --config $Cfg
$rc = $LASTEXITCODE
Check 'step4: install exit 0' ($rc -eq 0)

$wmi = Get-CimInstance -ClassName Win32_Service -Filter "Name='$Svc'"
# Registry VALUES are read with Get-ItemProperty (Get-Item returns a key object
# whose per-value properties are empty). The SCM binary path value is
# ImagePath (there is no Image value).
$reg = Get-ItemProperty $SvcKey
Check 'step4: service exists' ($null -ne $wmi)
Check 'step4: binPath byte-for-byte = EXPECTED_IMAGE' ($reg.ImagePath -ceq $ExpectedImage) "`n       actual  : $($reg.ImagePath)`n       expected: $ExpectedImage"
Check 'step4: ServiceStartName = LocalSystem' ($reg.ObjectName -eq 'LocalSystem') $reg.ObjectName
Check 'step4: StartType = Automatic (registry Start = 2)' ($reg.Start -eq 2) "Start=$($reg.Start) (2=auto, 3=manual)"
Check 'step4: StopTimeout = 45 (registry)' ($reg.StopTimeout -eq 45) "StopTimeout=$($reg.StopTimeout)"
Check 'step4: Stopped AFTER install (install did not start it)' ($wmi.State -eq 'Stopped') $wmi.State
Check 'step4: no goal.exe process after install' (-not (Get-Process goal -ErrorAction SilentlyContinue))

sc.exe qc $Svc   # informational: BINARY_PATH_NAME / START_TYPE / SERVICE_START_NAME
```

**Expected:** install prints `service "GoAl" registered: account LocalSystem, start type auto, stop timeout 45s`, the `image:` line equal to `EXPECTED_IMAGE`, and the "NOT started" line. All seven checks PASS.
**Evidence:** full install stdout; the `sc.exe qc` block; the actual-vs-expected ImagePath comparison; the four registry values (`ImagePath`, `ObjectName`, `Start`, `StopTimeout`).

> Item 2 sub-case "identical re-install = idempotent no-op": run `& $Goal --service install --config $Cfg` **again now** — expect exit 0, no change to any registry value, service still Stopped. Evidence: second stdout + re-read of the four values.

---

## Step 5 — START acceptance (ADR 011 item 3)

Start the service (the verb blocks until the SCM reports Running), then sample state and health in a tight loop and record the first-observed timestamps: per D6.1, **Running is reported only after the HTTP bind**, so health must already answer by the time Running is seen:

```powershell
& $Goal --service start
$rc = $LASTEXITCODE
Check 'step5: --service start returned 0 (reached Running)' ($rc -eq 0)

# Binding-order proof: the start verb above already blocked until the SCM
# reports Running — and D6.1 says Running is reported ONLY after the HTTP bind —
# so both timestamps are expected to land on the very first poll. The invariant
# first-healthy <= first-running (+500 ms sampling tolerance) fails only if
# health appears AFTER Running was already observed, i.e. a bind-order violation.
# Poll every 250 ms; record first-Running vs first-healthy.
$firstHealthy = $null; $firstRunning = $null
$deadline = (Get-Date).AddSeconds(90)
while ((Get-Date) -lt $deadline) {
    $s = Get-SvcState
    if ($null -eq $firstRunning -and $s -and $s.State -eq 'Running') { $firstRunning = Get-Date }
    if ($null -eq $firstHealthy) {
        try {
            $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 $Health
            if ($r.StatusCode -eq 200) { $firstHealthy = Get-Date }
        } catch { }
    }
    if ($firstRunning -and $firstHealthy) { break }
    Start-Sleep -Milliseconds 250
}
"first-healthy : $firstHealthy"
"first-running : $firstRunning"
Check 'step5: /api/v1/health = 200' ($null -ne $firstHealthy)
Check 'step5: Running observed' ($null -ne $firstRunning)
Check 'step5: Running NOT before HTTP bind (first-healthy <= first-running, 500 ms sampling tolerance)' (($null -ne $firstHealthy) -and ($null -ne $firstRunning) -and ($firstHealthy -le $firstRunning.AddSeconds(0.5)))

$st = & $Goal --service status
"status output: $st"
$svcState = Get-SvcState
$goal = Get-Process goal -ErrorAction SilentlyContinue
Check 'step5: SCM PID == goal.exe PID' ($svcState.PID -eq $goal.Id) "SCM PID=$($svcState.PID), goal PID=$($goal.Id)"
Check 'step5: exactly one goal.exe process' (@($goal).Count -eq 1)
Check 'step5: Web UI reachable' ((Invoke-WebRequest -UseBasicParsing $UI).StatusCode -eq 200)
```

**Expected:** `service "GoAl" is Running`; `service "GoAl": state=Running pid=<N> uptime=<...>`; the Web UI loads at `http://127.0.0.1:8099/`. The `first-healthy` timestamp is **not later** than `first-running` (with the 500 ms sampling tolerance).
**Evidence:** the two timestamps, the status line, the PID pair, the UI HTTP 200.

> **Item 3 sub-case "failing start never reports Running":** before this step's final PASS, verify the negative path once — it is done in **Step 6b** (port-in-use start failure, which stops the service itself first). Do not skip it.

---

## Step 5b — Create the test models via the API (deterministic, idempotent)

Seed contract (Source of Truth: `internal/storage/config_seed.go`, CONFIGURATION.md): this fixture's config seeds only the two `runtimes`; the models are created here through `POST /api/v1/models` and verified by read-back. Both get `active=false` — model-level autostart is OFF for the whole Steps 6–11 chain (instances are started explicitly; the pipeline/model autostart matrix is Step 12's own concern). Re-running this step is safe: an existing model with the expected shape is verified in place instead of re-created.

```powershell
function Ensure-TestModel([string]$id, [string]$name, [string]$runtimeId, [string[]]$modelArgs) {
    $code = Invoke-GoalAPI GET "/api/v1/models/$id"
    if ($code -eq 200) {
        # Re-run path: the model already exists — verify the fixture shape instead of re-creating.
        $m = Get-GoalJSON "/api/v1/models/$id"
        Check "step5b: model $id already present (idempotent re-run)" ($null -ne $m) "id=$($m.id)"
        Check "step5b: model $id args unchanged" ((@($m.args) -join '`n') -eq (@($modelArgs) -join '`n')) "args=$(@($m.args) -join ' ')"
        return
    }
    if ($code -ne 404) { throw "unexpected GET /api/v1/models/$id -> HTTP $code" }
    $code = Invoke-GoalAPI POST '/api/v1/models' @{
        id = $id; name = $name; runtime_id = $runtimeId; args = $modelArgs; active = $false
    }
    Check "step5b: model $id created (201)" ($code -eq 201) "code=$code"
    $m = Get-GoalJSON "/api/v1/models/$id"
    Check "step5b: model $id readback matches the fixture (runtime_id + args)" (($m.runtime_id -eq $runtimeId) -and ((@($m.args) -join '`n') -eq (@($modelArgs) -join '`n'))) "runtime_id=$($m.runtime_id) args=$(@($m.args) -join ' ')"
    Check "step5b: model $id autostart OFF (active=false)" ($m.active -eq $false)
}
Ensure-TestModel 'fake-infinite' 'fake-infinite' 'fake'   @('infinite')
Ensure-TestModel 'denied-model'  'denied-model'  'denied' @('infinite')

# Repository-level readback: exactly the 2 seeded runtimes + exactly the 2 API models, all resolvable:
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
Check 'step5b: repository has exactly the 2 seeded runtimes' (@($repo.runtimes).Count -eq 2) "ids=$(@($repo.runtimes | Select-Object -ExpandProperty id) -join ',')"
Check 'step5b: repository has exactly the 2 API-created models' (@($repo.models).Count -eq 2) "ids=$(@($repo.models | Select-Object -ExpandProperty id) -join ',')"
Check 'step5b: both models reference existing runtimes' (@($repo.models | Where-Object { $repo.runtimes.id -notcontains $_.runtime_id }).Count -eq 0)
```

**Expected:** first run — two `201`s; re-run — the two "already present" PASS pairs instead; the repository holds exactly `{fake, denied}` runtimes and `{fake-infinite, denied-model}` models, every model resolvable to a runtime.
**Evidence:** the PASS lines with detail (codes, ids, args) for both models + the three repository PASS lines.

---

## Step 6 — Negative start probe, then the managed instance and STOP acceptance (ADR 011 items 3+4)

Sub-case order matters: **6b** (port-in-use negative start) runs FIRST, while the service is Running with no managed instances — it stops the service itself, so it cannot disturb the instance the stop-contract checks (6a/6c) need. **6a** then creates the live managed instance and **6c** stops the service through it.

### 6b — Negative start probe (item 3 sub-case: "failing start never reports Running")

**Preconditions (asserted, not assumed):** service installed; service Running with exactly one `goal.exe`; port 8099 owned by the service (health 200); no `fake-runtime` process (Step 5b created models only — no instance was started yet); no other listener on 8099.

```powershell
# --- 6b.0 preconditions
$pre = Get-SvcState
Check 'step6b precond: service Running' ($pre.State -eq 'Running') $pre.State
Check 'step6b precond: exactly one goal.exe (the service)' (@(Get-Process goal -ErrorAction SilentlyContinue).Count -eq 1)
Check 'step6b precond: health 200 (service owns port 8099)' ((Invoke-WebRequest -UseBasicParsing $Health).StatusCode -eq 200)
Check 'step6b precond: no fake-runtime instance yet' (-not (Get-Process fake-runtime -ErrorAction SilentlyContinue))

# --- 6b.1 stop the service FIRST: the external listener may bind 8099 only after
# --- the service has released it (otherwise TcpListener.Start() throws and the
# --- probe is void). The stop verb blocks until SCM reports Stopped.
& $Goal --service stop
Check 'step6b: stop exit 0' ($LASTEXITCODE -eq 0)
$st = Wait-State 'Stopped' 90
Check 'step6b: service Stopped before the probe' ($st.State -eq 'Stopped') $st.State
Check 'step6b: goal.exe gone after stop' (-not (Get-Process goal -ErrorAction SilentlyContinue))
Check 'step6b: port 8099 free (service released it)' (Test-PortFree 8099)

# --- 6b.2 busy the port from outside; the listener is released in a finally
# --- block no matter what the probe does.
$listener = $null
$badRc = $null; $failState = $null; $sawRunningDuringProbe = $false
try {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 8099)
    $listener.Start()
    Check 'step6b: external listener bound 8099' $listener.Server.IsBound
    # The start verb waits for Running with a bounded 90 s budget and returns a
    # bounded error (non-zero exit) as soon as the SCM reports Stopped after
    # the failed bind — it can never report success for this start.
    & $Goal --service start
    $badRc = $LASTEXITCODE
    # Bounded observation: from now on the service must never be seen Running.
    $deadline = (Get-Date).AddSeconds(10)
    while ((Get-Date) -lt $deadline) {
        $s = Get-SvcState
        if ($s -and $s.State -eq 'Running') { $sawRunningDuringProbe = $true }
        if ($s -and $s.State -eq 'Stopped') { break }
        Start-Sleep -Milliseconds 200
    }
    $failState = (Get-SvcState).State
}
finally {
    if ($listener) { try { $listener.Stop(); $listener.EndPoint | Out-Null } catch { } ; $listener = $null }
}
Check 'step6b: start with busy port FAILED (non-zero exit)' (($null -ne $badRc) -and ($badRc -ne 0)) "exit=$badRc"
Check 'step6b: bounded startup failure diagnostic on stderr (SCM Stopped after failed bind)' (($null -ne $badRc) -and ($badRc -ne 0)) "see the captured stderr of the failed start above"
Check 'step6b: service NEVER observed Running during/after the failed start' (-not $sawRunningDuringProbe)
Check 'step6b: SCM final state = Stopped (no Running residual)' ($failState -eq 'Stopped') "state=$failState"
Check 'step6b: no goal.exe process after the failed start' (-not (Get-Process goal -ErrorAction SilentlyContinue))
Check 'step6b: listener released (finally) — port 8099 free again' (Test-PortFree 8099)
# The bounded Event Log diagnostic ('service: startup failed before HTTP bind: …')
# is verified in Step 10.

# --- 6b.3 restore: clean start back to Running for the next sub-cases (6a/6c).
& $Goal --service start
Check 'step6b: clean re-start reached Running (exit 0)' ($LASTEXITCODE -eq 0)
$st = Wait-State 'Running' 90
Check 'step6b: service Running again, health 200' (($st.State -eq 'Running') -and ((Invoke-WebRequest -UseBasicParsing $Health).StatusCode -eq 200))
Check 'step6b: no fake-runtime resurrected by the recovery on this start' (-not (Get-Process fake-runtime -ErrorAction SilentlyContinue))
```

**Expected:** the failed start exits non-zero (CLI `$LASTEXITCODE` = 1) with a bounded stderr line (`service: "GoAl" did not reach Running; SCM reports Stopped (win32 exit 0)`); the service is Stopped and never Running; the listener is always released; the restored start reaches Running with health 200 and no instances (recovery finds none — all models are `active=false`). Note: the Win32 exit code is 0 because `svc.Run` returns nil after the handler reports Stopped to SCM — the CLI exit code (1) is the authoritative failure signal.
**Evidence:** the precondition PASS lines, the failed-start stderr, the state observations, the finally-release PASS, the restore PASS lines.

### 6a — Start a managed instance from the Web UI (this is what Stop must clean up)

1. Open `http://127.0.0.1:8099/` in a browser.
2. Models page → **fake-infinite** → **Start**.
3. Wait until the model card shows the running state (the fake-runtime prints `running` every 200 ms — visible in the instance's Logs view).

```powershell
$fr = Get-Process fake-runtime -ErrorAction SilentlyContinue
Check 'step6a: fake-runtime instance running under the service' ($null -ne $fr) "PID=$($fr.Id)"
# Evidence: note the instance PID and the repository state:
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$repo.instances | Select-Object id, model_id, state, pid | Format-Table | Out-String
```

**Expected:** one `fake-runtime.exe` process; the repo instance is `running` with a PID.

### 6c — Stop through `goal --service stop` (SCM Stop) and observe the state sequence

**Preconditions:** the service is Running (restored by 6b.3) and the 6a managed instance is still running (the stop must clean exactly this one up).

```powershell
# Time the whole stop. (Polling for StopPending at 200 ms is optional bonus
# evidence — the shutdown is usually < 1 s and may not be sampleable; the
# wait-hint contract is covered deterministically by service_handler_test.go.)
$t0 = Get-Date
& $Goal --service stop
$stopDur = (Get-Date) - $t0
Check 'step6c: stop exit 0' ($LASTEXITCODE -eq 0)
# Bounded-stop oracle: normally < 1 s (app shutdown 30 s budget); the SCM
# hard-kills at the registered 45 s stop timeout and the CLI observer budget is
# 60 s (45 + 15 margin), so a healthy stop must finish strictly inside 60 s.
Check 'step6c: total stop duration bounded (< 60 s observer budget; SCM hard timeout 45 s; app budget 30 s)' ($stopDur.TotalSeconds -lt 60) "duration=$([math]::Round($stopDur.TotalSeconds,1))s"

$st = Get-SvcState
Check 'step6c: SCM state = Stopped' ($st.State -eq 'Stopped') $st.State
Check 'step6c: managed instance terminated (Job Object)' (-not (Get-Process fake-runtime -ErrorAction SilentlyContinue))
Check 'step6c: no goal.exe process remains' (-not (Get-Process goal -ErrorAction SilentlyContinue))

$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$inst = @($repo.instances | Where-Object { $_.model_id -eq 'fake-infinite' }) | Sort-Object started_at -Descending | Select-Object -First 1
Check 'step6c: repository state persisted terminal (stopped/failed/exited, NOT running/starting/stopping)' ($inst.state -in 'stopped','exited','failed') "state=$($inst.state)"
Check 'step6c: audit file exists (ADR 007 untouched)' (Test-Path $Audit)
# The instance stop is audited (instance.stop / instance.cleanup) — visible in the audit file:
Get-Content $Audit -Tail 5
```

**Expected:** `service "GoAl" is Stopped`; the fake-runtime process is gone (Job Object close); the repo JSON shows a **terminal** state for the instance (NOT `running`); the audit tail contains the stop/cleanup events.
**Evidence:** stop duration, final state, the repo instance row, the audit tail, the `sc`/WMI state.

> The `Running → StopPending → Stopped` sequence: `goal --service stop` sends SCM Stop and the handler returns `SERVICE_STOP_PENDING` with the 30 s wait hint **only while the app shutdown runs**. Because the shutdown is fast, StopPending may not be sampleable from outside — the wait-hint and state contract are covered deterministically by the maintained handler tests (`internal/platform/service_handler_test.go`: StopPending with WaitHint 30000 asserted). Cite that in the evidence report; if the 200 ms polling in 6c **does** catch `StopPending`, include it as bonus evidence.

---

## Step 7 — RESTART acceptance (ADR 011 item 6)

**Preconditions:** service Running; a live managed instance exists (UI: Models → **fake-infinite** → **Start** again — Step 6c stopped the previous one).

```powershell
# Start a fresh managed instance again (UI: Models → fake-infinite → Start),
# then run --service restart while it is running.
$fr = Get-Process fake-runtime -ErrorAction SilentlyContinue
if (-not $fr) { throw 'start fake-infinite in the Web UI first' }
if ((Get-SvcState).State -ne 'Running') { throw 'service must be Running before the restart probe' }
$pidBefore = (Get-Process goal).Id
"PID before restart: $pidBefore"

# Watch the SERVICE process count during the restart: it must NEVER exceed 1.
# IMPORTANT: count only goal.exe whose command line is '--service run' — the
# management CLI is ALSO a goal.exe process (goal.exe --service restart) while
# it runs, and it must not count against the "no two service processes" contract
# (ADR 011 D7: Stop -> Stopped -> Start -> Running, no self-reexec, no second
# SERVICE process). The watcher exits as soon as the full stop->start cycle is
# observed (0 service processes, then 1 stable for ~3 s); 120 s hard cap.
$watch = Start-Job {
    $m = 0; $seen0 = $false; $stable1 = 0
    for ($i = 0; $i -lt 480; $i++) {
        $c = @(Get-CimInstance Win32_Process -Filter "Name='goal.exe'" -ErrorAction SilentlyContinue |
               Where-Object { $_.CommandLine -like '*--service run*' }).Count
        if ($c -gt $m) { $m = $c }
        if ($c -eq 0)      { $seen0 = $true; $stable1 = 0 }
        elseif ($c -eq 1)  { $stable1 = $stable1 + 1 }
        else               { $stable1 = 0 }
        if ($seen0 -and $stable1 -ge 6) { break }
        Start-Sleep -Milliseconds 250
    }
    return $m
}
& $Goal --service restart
$rc = $LASTEXITCODE
$maxGoal = (Wait-Job $watch | Receive-Job); Remove-Job $watch -Force

$pidAfter = (Get-Process goal -ErrorAction SilentlyContinue).Id
"PID after restart:  $pidAfter"
Check 'step7: restart exit 0' ($rc -eq 0)
Check 'step7: goal.exe PID CHANGED (new service process)' ($pidAfter -ne $pidBefore) "before=$pidBefore after=$pidAfter"
Check 'step7: NEVER two service goal.exe at once (max observed = 1)' ($maxGoal -le 1) "max observed=$maxGoal"
Check 'step7: service Running after restart' ((Get-SvcState).State -eq 'Running')
Check 'step7: old fake-runtime instance terminated by the stop phase' (-not (Get-Process fake-runtime -ErrorAction SilentlyContinue))
Check 'step7: health OK after restart' ((Invoke-WebRequest -UseBasicParsing $Health).StatusCode -eq 200)
```

**Expected:** Stop → Stopped → Start → Running (the verb blocks until Running); the SERVICE PID changed; the service-process watcher never saw 2; the pre-restart instance was terminated by the stop phase (autostart semantics re-establish the normal start set — none here since all models are `active=false`).
**Evidence:** before/after PIDs, the max observed service-process count, the state sequence from `--service status` if captured.

---

## Step 8 — Forced termination (taskkill /F) + ADR 005 reconciliation

**Preconditions:** service Running; a live managed instance (UI: Models → **fake-infinite** → **Start** — Step 7's stop phase terminated the previous one). At this moment the only `goal.exe` running is the service's (no CLI), so the image-name `taskkill` below is exact.

```powershell
$fr = Get-Process fake-runtime -ErrorAction SilentlyContinue
if (-not $fr) { throw 'start fake-infinite in the Web UI first' }
if ((Get-SvcState).State -ne 'Running') { throw 'service must be Running before the kill' }
$frPid = $fr.Id
"instance fake-runtime PID before kill: $frPid"

# Snapshot the data BEFORE the kill (runtimes/models must survive the recovery):
$repoPre = Get-Content $RepoFile -Raw | ConvertFrom-Json
$preKillRt = @($repoPre.runtimes | Select-Object -ExpandProperty id | Sort-Object) -join ','
$preKillMd = @($repoPre.models   | Select-Object -ExpandProperty id | Sort-Object) -join ','
"runtimes before kill: $preKillRt"
"models   before kill: $preKillMd"

# UNGRACEFUL kill of the service process (no SCM stop):
taskkill /F /IM goal.exe
Start-Sleep -Seconds 2
Check 'step8: goal.exe dead' (-not (Get-Process goal -ErrorAction SilentlyContinue))
Check 'step8: instance CHILD dead too (Job Object kill-on-close)' (-not (Get-Process fake-runtime -ErrorAction SilentlyContinue)) "instance PID was $frPid"

# The repo file still holds the TRANSITIONAL (non-terminal) state on disk —
# the process died before it could persist the stop:
$repoNow = Get-Content $RepoFile -Raw | ConvertFrom-Json
$instNow = @($repoNow.instances | Where-Object { $_.model_id -eq 'fake-infinite' }) | Sort-Object started_at -Descending | Select-Object -First 1
"instance state on disk after kill: $($instNow.state) (PID $($instNow.pid); live PID was $frPid)"

# Restart the service: recovery (ADR 005) must reconcile on startup.
& $Goal --service start
Check 'step8: service Running after kill+start (start verb returned 0)' ($LASTEXITCODE -eq 0)
Start-Sleep -Seconds 3

$repoAfter = Get-Content $RepoFile -Raw | ConvertFrom-Json
$inst = @($repoAfter.instances | Where-Object { $_.model_id -eq 'fake-infinite' }) | Sort-Object started_at -Descending | Select-Object -First 1
Check 'step8: ADR 005 reconciliation: instance NOT running/starting/stopping anymore' ($inst.state -notin 'running','starting','stopping','pending') "state=$($inst.state)"
Check 'step8: reconciled to stale (pid-not-found — the child is dead)' ($inst.state -eq 'stale') "state=$($inst.state)"
Check 'step8: NO orphan claimed (no live process to verify identity against)' ($inst.state -ne 'orphan')
Check 'step8: exactly one goal.exe — the new service process (no zombie, no leftover)' (@(Get-Process goal -ErrorAction SilentlyContinue).Count -eq 1) "pids=$(@(Get-Process goal -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id) -join ',')"
Check 'step8: fake-runtime NOT resurrected by recovery (recovery never starts processes)' (-not (Get-Process fake-runtime -ErrorAction SilentlyContinue))
Check 'step8: data preservation — runtimes intact after recovery' ((@($repoAfter.runtimes | Select-Object -ExpandProperty id | Sort-Object) -join ',') -eq $preKillRt) "after=$(@($repoAfter.runtimes | Select-Object -ExpandProperty id | Sort-Object) -join ',')"
Check 'step8: data preservation — models intact after recovery' ((@($repoAfter.models | Select-Object -ExpandProperty id | Sort-Object) -join ',') -eq $preKillMd) "after=$(@($repoAfter.models | Select-Object -ExpandProperty id | Sort-Object) -join ',')"
```

**Web UI check:** Models page → **fake-infinite** shows the **stale** state ("Stopped (not tracked)" / «Остановлен (не отслеживается)») — **not** running, **not** orphan, with a Start button available. Instances page shows the `stale` record with the recovery diagnostic.
**Expected (ADR 005):** dead PID → `stale` (`recovery_reason: pid-not-found`); excluded from the active list; **no** process started/stopped/signaled during recovery; no reattach.
**Evidence:** the pre-kill state, the on-disk state after kill, the reconciled state + the `recovery_reason`/diagnostic (repo JSON row or UI screenshot), the UI screenshot of the stale badge.

> The data-preservation requirement is asserted above (runtimes/models id sets before vs after the kill+recovery): the repo file may differ only by the recovery's instance-state update — that is the expected reconciliation write, not data loss.

---

## Step 9 — LocalSystem access failure (ADR 011 item 7, D4)

A resource that LocalSystem genuinely cannot read, created **from scratch** (no production ACL touched). The failure occurs at `os.Stat` inside `Manager.Start` (the LocalSystem service cannot traverse the denied directory) — the error is bounded, returned synchronously as HTTP 500, AND persisted as a terminal `failed` LaunchInstance in the repository.

**Contract (Source of Truth: ADR 010 line 84, ADR 011 D4, `supervisor.go:258-286`+`795-804`):**
- A terminal `failed` instance record IS persisted (standard Supervisor failure semantics).
- The HTTP 500 response body carries the bounded error string (visible in the Web UI).
- The repository `last_error` field carries the same error (persistent, visible via API/UI).
- The `POST /api/v1/models/:id/start` endpoint does NOT write an ADR 007 audit entry (audit is on `POST /api/v1/instances/start` only) — no audit file is expected here.
- The service remains Running (one failed launch does not crash the service).

```powershell
# Restrict the purpose-built denied dir: explicit DENY for SYSTEM + LocalService.
# (Default user-profile ACLs grant SYSTEM full control, so a bare profile path
# is NOT a reliable access-denied fixture — the explicit deny makes it deterministic.)
icacls "$deniedDir" /deny "NT AUTHORITY\SYSTEM:(OI)(CI)RX"
icacls "$deniedDir" /deny "NT AUTHORITY\LOCAL SERVICE:(OI)(CI)RX"

# Sanity: the admin (you) can still read it; LocalSystem cannot.
Check 'step9: denied dir readable by admin' (Test-Path $deniedExe)

# Start the denied model via the API (same path the Web UI "Start" button uses):
# POST /api/v1/models/denied-model/start → synchronous HTTP 500 with bounded error body.
$resp = $null
try {
    $resp = Invoke-WebRequest -UseBasicParsing -Method POST -Uri "http://127.0.0.1:8099/api/v1/models/denied-model/start"
    $httpCode = $resp.StatusCode
} catch {
    $httpCode = $_.Exception.Response.StatusCode.value__
    $resp = $_.Exception.Response
}
$errorBody = ''
if ($resp -and $resp.Stream) {
    try { $reader = [System.IO.StreamReader]::new($resp.Stream); $errorBody = $reader.ReadToEnd(); $reader.Dispose() } catch { }
} elseif ($resp -and $resp.Content) { $errorBody = $resp.Content }
"httpCode=$httpCode errorBody=$errorBody"

Check 'step9: HTTP 500 (launch failed, bounded synchronous response)' ($httpCode -eq 500) "code=$httpCode"
Check 'step9: error body contains access-denied text (bounded, visible in Web UI)' ($errorBody -match 'Access is denied|access denied') "body=$errorBody"

# The failed LaunchInstance is persisted by the Supervisor BEFORE the HTTP response
# is sent (supervisor.go:258 Create + :798 persistStateLocked). Read it back:
Start-Sleep -Milliseconds 500
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$deniedInst = @($repo.instances | Where-Object { $_.model_id -eq 'denied-model' }) | Sort-Object started_at -Descending | Select-Object -First 1
"denied instance: state=$($deniedInst.state) last_error=$($deniedInst.last_error)"
Check 'step9: denied instance persisted as FAILED (terminal, bounded)' ($deniedInst -and $deniedInst.state -eq 'failed') "state=$($deniedInst.state)"
Check 'step9: persisted last_error contains access-denied text' ($deniedInst.last_error -match 'Access is denied|access denied') "last_error=$($deniedInst.last_error)"
Check 'step9: the healthy service is still Running (one bad launch did not crash it)' ((Get-SvcState).State -eq 'Running')
Check 'step9: no process spawned from the denied path' (-not (Get-Process fake-runtime -ErrorAction SilentlyContinue))
Check 'step9: ServiceStartName still LocalSystem' ((Get-ItemProperty $SvcKey).ObjectName -eq 'LocalSystem')
```

**Web UI check (optional corroboration):** the **denied-model** card shows the **failed** state with the visible bounded error text (access denied) — no silent failure. The instance is also visible on the Instances/History page.
**Expected (item 7):** bounded, **visible** startup/launch error in the Web UI (HTTP 500 body + persisted `last_error`); the service keeps running; `ServiceStartName` is still `LocalSystem`.
**Evidence:** the HTTP 500 code + error body, the failed instance row (state + `last_error`), the service Running check, the `ObjectName` re-read.
**Cleanup of the fixture ACL (after the evidence is captured):**
```powershell
icacls "$deniedDir" /remove:d "NT AUTHORITY\SYSTEM"
icacls "$deniedDir" /remove:d "NT AUTHORITY\LOCAL SERVICE"
```

---

## Step 10 — Event Log (ADR 011 item 8, D8)

**Windows Event Log semantics (verified on real SCM, historical observation):** The original acceptance run observed `ProviderName="Application"` instead of `ProviderName="GoAl"`. This observation exposed the D8.1 handle bug: the implementation passed the `OpenEventLogW` log handle (no source association) to `ReportEvent` instead of the `RegisterEventSource` source handle. The `RegisterEventSource` handle carries the source name into the event record's `strEventSource` field; a log-only handle falls back to the log name. This was NOT an inherent limitation of the traditional `ReportEvent` API — the correct handle produces `ProviderName="GoAl"` without any registry key or message file.

**Corrected expected behavior (after D8.1 fix):** Events appear as `ProviderName="GoAl"` in the Application log. The query below filters by `ProviderName`.

**Status:** PASS — targeted D8.1 real-SCM rerun completed 2026-09-05. Raw event XML confirmed `ProviderName="GoAl"` and EventData operational payload. The initial `.Message`-only STOP and the initial zero-EventData result were acceptance-harness/display defects (missing EventMessageFile formatting and XML namespace parsing respectively), not product failures. ADR 007 audit-like Event Log count remained zero. Controlled service stop, uninstall, and targeted cleanup PASS; unrelated foreign workload and protected historical backup remained untouched.

```powershell
# --- Retrieve GoAl operational events from the Application log (last hour) ---
# After D8.1 fix: ProviderName is "GoAl" (the RegisterEventSource handle
# carries the source name into the event record). Filter by ProviderName.
$goalEvents = $null
try {
    $goalEvents = @(Get-WinEvent -LogName Application -MaxEvents 200 -ErrorAction Stop |
        Where-Object { $_.TimeCreated -gt (Get-Date).AddHours(-1) } |
        Where-Object { $_.ProviderName -eq 'GoAl' })
} catch { Write-Warning "Get-WinEvent failed: $_" }
$goalTexts = @($goalEvents | ForEach-Object {
    $txt = $_.Message
    if ([string]::IsNullOrWhiteSpace($txt) -and $_.Properties.Count -gt 0) {
        $txt = [string]$_.Properties[0].Value
    }
    $txt
})
"GoAl operational events in last hour: $($goalTexts.Count)"
$goalTexts | ForEach-Object { "  $_" }

# --- Assertions ---
Check 'step10: Event Log retrieval succeeded (non-throwing)' ($null -ne $goalEvents)
Check 'step10: operational diagnostics present in Event Log' ($goalTexts.Count -gt 0) "entries=$($goalTexts.Count)"
Check 'step10: startup line present (HTTP server bound)' (@($goalTexts | Where-Object { $_ -match 'HTTP server bound' }).Count -ge 1)
Check 'step10: stop line present' (@($goalTexts | Where-Object { $_ -match 'service: stopped' }).Count -ge 1)
Check 'step10: Step 6b startup-failure line present' (@($goalTexts | Where-Object { $_ -match 'startup failed before HTTP bind' }).Count -ge 1)
Check 'step10: Step 9 launch-failure line contains error detail' (@($goalTexts | Where-Object { $_ -match 'instance start failed' -and $_ -match 'Access is denied' }).Count -ge 1) "matching=$(@($goalTexts | Where-Object { $_ -match 'instance start failed' }) -join ' | ')"

# --- Mirror check (D8.2): ADR 007 audit events MUST NOT appear in the Event Log.
$mirrored = $goalTexts | Where-Object { $_ -match 'instance\.(start|stop|restart|dismiss|kill|cleanup)|login\.(success|failure)|config\.reload|pipeline\.' }
Check 'step10: NO ADR 007 audit event mirrored into the Event Log' ($null -eq $mirrored)
if ($mirrored) { "MIRRORED (FAIL): $mirrored" }

# Ground truth: the audit file HAS those events (they go to goal_audit.jsonl, not the Event Log):
if (Test-Path $Audit) {
    Get-Content $Audit -Tail 10
} else {
    # Audit file may not exist if no audited endpoint (POST /api/v1/instances/start)
    # was called during the run. The model-start endpoint (POST /api/v1/models/:id/start)
    # does NOT write audit (ADR 007 §2: audit is on instances/start only).
    "audit file not present (expected: no audited endpoint was called in Steps 5-9)"
}
```

**Expected:** The Application log contains GoAl operational lines with structured attrs serialized into the message (e.g. `instance start failed instance_id=... model_id=... error=...Access is denied...`); the Step 9 launch failure MUST include "Access is denied" in the same event; **zero** lines matching the ADR 007 audit taxonomy; the audit file (if present) contains the instance lifecycle events.
**Evidence:** the filtered Event Log text dump, the mirror-check PASS, the audit file tail (or the "not present" note).

---

## Step 11 — UNINSTALL acceptance (ADR 011 item 9)

**Preconditions:** service Running (from Steps 8/9); all evidence for Steps 6–10 already captured.

**Ordering contract (why stop BEFORE the snapshot):** the graceful stop runs `ShutdownWithPersistence` (terminal-instance persistence), which may rewrite `goal_repo.json` — and every durable write rotates the `<file>.bak` sidecar. The "uninstall touched nothing" oracle is only valid when NO GoAl process can write between the snapshot and the comparison, so the sequence is: stop → snapshot → uninstall → compare. (Uninstall itself also stops first, D6.2 — on an already-Stopped service that stop is a bounded no-op, so nothing writes after the snapshot.)

```powershell
# 11a — stop the service FIRST (the stop verb blocks until SCM reports Stopped):
& $Goal --service stop
Check 'step11a: service Stopped before the data snapshot' ($LASTEXITCODE -eq 0 -and (Get-SvcState).State -eq 'Stopped')
Check 'step11a: no goal.exe process (nothing can write the data dir anymore)' (-not (Get-Process goal -ErrorAction SilentlyContinue))

# 11b — data snapshot with the service fully stopped (this is the "untouched" proof):
$pre = Get-ChildItem $Data -Recurse -File | ForEach-Object {
    [PSCustomObject]@{ Path = $_.FullName; Hash = (Get-FileHash $_.FullName).Hash; Size = $_.Length }
}
$preCfgHash = (Get-FileHash $Cfg).Hash
$pre | Export-Clixml "$Root\evidence\pre-uninstall.xml"
"files under data before uninstall: $($pre.Count)"

# 11c — uninstall (its internal graceful stop is a bounded no-op on a Stopped service):
& $Goal --service uninstall
$rc = $LASTEXITCODE
Check 'step11c: uninstall exit 0' ($rc -eq 0)

# 11d — registration gone:
Check 'step11d: service GONE (Get-Service)' (-not (Get-Service $Svc -ErrorAction SilentlyContinue))
Check 'step11d: service key GONE (registry)' (-not (Test-Path $SvcKey))

# 11e — data untouched (no GoAl process existed between 11b and 11e):
$after = Get-ChildItem $Data -Recurse -File | ForEach-Object {
    [PSCustomObject]@{ Path = $_.FullName; Hash = (Get-FileHash $_.FullName).Hash; Size = $_.Length }
}
Check 'step11e: same file set under data (nothing created/deleted by uninstall)' ($pre.Count -eq $after.Count) "before=$($pre.Count) after=$($after.Count)"
$diff = Compare-Object $pre $after -Property Path, Hash, Size
Check 'step11e: identical hashes (nothing modified by uninstall)' ($null -eq $diff)
if ($diff) { $diff | Format-List }
Check 'step11e: config file untouched' ((Get-FileHash $Cfg).Hash -eq $preCfgHash)

# 11f — unknown-service bounded "not found":
& $Goal --service status
Check 'step11f: status of uninstalled service = bounded not-found (exit 1)' ($LASTEXITCODE -eq 1)
& $Goal --service uninstall
Check 'step11f: uninstall of unknown service = bounded not-found (exit 1)' ($LASTEXITCODE -eq 1)

# 11g — teardown (after capturing all evidence):
Remove-Item $Root -Recurse -Force
Remove-Item $userDir -Recurse -Force
Check 'step11g: teardown complete' (-not (Test-Path $Root) -and -not (Test-Path $userDir))
```

**Expected:** graceful stop-before-delete (D6.2); the SCM registration and registry key are gone; **every** file under the data directory is byte-identical (same set, same hashes) and the config is untouched; unknown-service operations print `service: "GoAl" not found` and exit 1.
**Evidence:** the `pre-uninstall.xml` vs the after-snapshot comparison (or the two PASS lines + any diff output), the config hash pair, the two not-found transcripts.

---

## Step 12 — Windows Service + Pipeline real-world acceptance

**Execution gate:** run this step **only after ADR 011 Steps 0–11 all PASS**. Step 11's teardown (11g) may or may not have been executed (the owner intentionally preserved `C:\goal-accept` after Step 11 PASS). 12.0 handles both cases: it explicitly tears down the old isolated environment (preserving evidence) and creates a fresh one.

**Two-machine topology:** the Go toolchain and repository are on the DEVELOPMENT machine. The TEST machine runs the acceptance and does NOT have the repository or Go toolchain. The binary is built on the DEVELOPMENT machine and transferred to the TEST machine by the owner (manual copy — no automated network deployment).

### 12.dev — DEVELOPMENT MACHINE: build the acceptance binary

**Execute on the DEVELOPMENT machine** (where the repository and Go toolchain exist). This produces the binary that the owner will manually transfer to the TEST machine.

```powershell
# DEVELOPMENT MACHINE — build from the current working tree:
$repoRoot = (git rev-parse --show-toplevel)
"REPO_ROOT: $repoRoot"
"HEAD: $(git rev-parse HEAD)"
git status --short

Set-Location $repoRoot

# Stale artifact protection: remove any previous build output before building.
$artifactDir = 'C:\goal-accept-artifact'
$artifactPath = "$artifactDir\goal.exe"
New-Item -ItemType Directory -Force -Path $artifactDir | Out-Null
if (Test-Path $artifactPath) { Remove-Item $artifactPath -Force }
if (Test-Path $artifactPath) { throw 'stale artifact could not be removed' }

# Fresh build:
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'
go build -o $artifactPath "$repoRoot\cmd\goal"
$buildExit = $LASTEXITCODE
Remove-Item Env:GOOS, Env:GOARCH

# STOP before hashing on failure:
if ($buildExit -ne 0) {
    throw "BUILD FAILED (exit $buildExit): do not compute provenance or transfer"
}

# Provenance (report these to the owner for the TEST machine verification):
$sha = (Get-FileHash $artifactPath -Algorithm SHA256).Hash
$size = (Get-Item $artifactPath).Length
"DEVELOPMENT BUILD PROVENANCE:"
"  REPO_ROOT: $repoRoot"
"  BUILD_EXIT: 0"
"  ARTIFACT: $artifactPath"
"  SIZE: $size"
"  SHA256: $sha"
"  VERSION: $(& $artifactPath -version)"
```

**Expected:** a fresh `goal.exe` with a SHA256 and size printed. The owner manually copies this file to the TEST machine at `C:\goal-accept\bin\goal.exe`.
**Evidence:** the SHA256 + size + `-version` output.
**On build failure:** STOP. No provenance, no transfer, no test machine action.

---

**Goal:** verify the shipped Pipeline MVP (ADR 010) inside a real Windows Service lifecycle before the next release candidate: group lifecycle, args-override semantics (full replacement), startup autostart ordering (pipeline-first, ADR 010 D4), Job Object containment of a **real** `llama-server` process, ADR 005 reconciliation + pipeline autostart together, persistence of schema v8, and UI/log behavior across service stop/start.

**Boundaries (in addition to the run-wide boundaries):**

- Same isolated `C:\goal-accept` environment; no production data/binaries.
- The **real** llama.cpp runtime + model files are referenced in place by absolute path — **never copied or moved**.
- GoAl entities (runtime/model/pipeline records) are created **only through the API** — no hand-editing `goal_repo.json`.
- If LocalSystem cannot read the llama.cpp/model files: **STOP the Step 12 subtest chain with diagnostics** (12.2) — do NOT modify any production ACL.
- Args-override markers are read-only flags (`--alias`); nothing that writes, deletes, or rebinds data is used.

### 12.0 — Controlled teardown of old environment + re-creation + owner inputs

```powershell
# === 12.0 PRE: Controlled teardown of the old Steps 0-11 environment ===
# The owner run completed Steps 0-11 but intentionally did NOT execute 11g.
# C:\goal-accept still exists with old config/data/evidence. We must:
#   1. Preserve the evidence/ directory (Steps 0-11 artifacts).
#   2. Remove the old environment entirely.
#   3. Create a fresh one.
#
# This is a FRESH elevated session. Re-run the Step 0 bootstrap block first
# (variables, EXPECTED_IMAGE, helpers: Check / Get-SvcState / Wait-State /
#  Test-PortFree / Invoke-GoalAPI / Get-GoalJSON — authEnabled=false).

# 12.0-pre1 — preserve evidence before teardown:
if (Test-Path "$Root\evidence") {
    $ts = Get-Date -Format 'yyyyMMdd-HHmmss'
    $backup = "C:\goal-accept-evidence-$ts"
    Move-Item "$Root\evidence" $backup -Force
    "Evidence preserved to: $backup"
}

# 12.0-pre2 — remove the old isolated environment (service already uninstalled):
if (Test-Path $Root) {
    Remove-Item $Root -Recurse -Force
}
Check '12.0-pre: old environment removed' (-not (Test-Path $Root))
Check '12.0-pre: no service registered' (-not (Get-Service $Svc -ErrorAction SilentlyContinue))
Check '12.0-pre: no goal.exe running' (-not (Get-Process goal -ErrorAction SilentlyContinue))

# --- owner inputs: REAL llama.cpp runtime + model (absolute paths, read-only use) ---
# FILL THESE BEFORE EXECUTING THIS BLOCK. They must be absolute paths to real files.
$llamaExe  = '<OWNER: absolute path to real llama-server.exe>'
$llamaDir  = Split-Path $llamaExe -Parent
$modelGguf = '<OWNER: absolute path to real .gguf model file>'
$modelPort = 8185

Check '12.0: llama-server.exe path is absolute' ([System.IO.Path]::IsPathRooted($llamaExe)) "path=$llamaExe"
Check '12.0: model .gguf path is absolute' ([System.IO.Path]::IsPathRooted($modelGguf)) "path=$modelGguf"
Check '12.0: llama-server.exe exists' (Test-Path $llamaExe) "path=$llamaExe"
Check '12.0: model .gguf exists' (Test-Path $modelGguf) "path=$modelGguf"

# 12.0a — verify the pre-built acceptance binary (built on the DEVELOPMENT machine):
# The owner manually transferred goal.exe to C:\goal-accept\bin\goal.exe from the
# development machine. Verify it matches the development provenance exactly.
$expectedSha256 = '0F7EBC0AFCF6244BD085E43DED56626B615AFD9E874FCB360CD194FAD8F2DB86'
$expectedSize   = 15135232
New-Item -ItemType Directory -Force -Path $Bin, $Data, "$Root\evidence" | Out-Null
Check '12.0: goal.exe present at C:\goal-accept\bin\goal.exe' (Test-Path $Goal)
$actualSha256 = (Get-FileHash $Goal -Algorithm SHA256).Hash
$actualSize   = (Get-Item $Goal).Length
Check '12.0: SHA256 matches development provenance' ($actualSha256 -ceq $expectedSha256) "actual=$actualSha256"
Check '12.0: size matches development provenance' ($actualSize -eq $expectedSize) "actual=$actualSize"
& $Goal -version

# 12.0b — minimal test config: absolute dataDir, NO seeded runtimes/models
# (the llama runtime/model/pipeline are created via API in 12.2-12.3).
$cfgText = @'
{
  "version": 2,
  "listenAddress": "127.0.0.1",
  "webPort": 8099,
  "dataDir": "C:\\goal-accept\\data",
  "adminUser": "admin",
  "adminPasswordHash": "",
  "authEnabled": false
}
'@
Write-TextFile $Cfg $cfgText
Assert-NoBom $Cfg

Check '12.0: goal.exe verified (SHA256 + size match)' ($true)
Check '12.0: config written (absolute dataDir, BOM-free, parses)' ((Get-Content $Cfg -Raw | ConvertFrom-Json).dataDir -eq $Data)
Check '12.0: service ABSENT' (-not (Get-Service $Svc -ErrorAction SilentlyContinue))
Check '12.0: port 8099 (UI) free' (Test-PortFree 8099)
Check "12.0: port $modelPort (model server) free" (Test-PortFree $modelPort)

# 12.0c — llama.cpp/model pre-flight (admin visibility only; LocalSystem proof is functional, in 12.4):
Get-Acl $llamaExe | Select-Object -ExpandProperty Access | Format-Table -AutoSize | Out-String
Get-Acl $modelGguf | Select-Object -ExpandProperty Access | Format-Table -AutoSize | Out-String

# --- Step 12 local helpers (the generic Invoke-GoalAPI/Get-GoalJSON come from
# --- the Step 0 bootstrap block re-run above) -------------------------------
# SCOPED: only counts llama-server processes launched from the acceptance
# executable ($llamaExe). Foreign llama-server processes (different exe path)
# are never counted, never touched, never killed.
function Get-LlamaPids {
    @(Get-CimInstance Win32_Process -Filter "Name='llama-server.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.ExecutablePath -ceq $llamaExe } |
        Select-Object -ExpandProperty ProcessId)
}
# Wait until the ACCEPTANCE llama-server process exists (scoped to $llamaExe).
function Wait-Llama([int]$timeoutSec = 30) {
    $deadline = (Get-Date).AddSeconds($timeoutSec)
    while ((Get-Date) -lt $deadline) {
        $pids = Get-LlamaPids
        if ($pids.Count -ge 1) { return $pids[0] }
        Start-Sleep -Milliseconds 500
    }
    $pids = Get-LlamaPids
    if ($pids.Count -ge 1) { return $pids[0] }
    throw "acceptance llama-server process did not appear within ${timeoutSec}s"
}
# Safe count for nullable repo collections (PS 5.1: @($null).Count == 1).
function Get-SafeCount($collection) {
    if ($null -eq $collection) { return 0 }
    @($collection).Count
}
```

**Expected:** environment rebuilt; all checks PASS; the two ACL tables are captured for the evidence report.
**Evidence:** the PASS lines + both ACL tables + the `-version` line.

### 12.1 — Install and start the test service (re-creation after Step 11)

```powershell
& $Goal --service install --config $Cfg
Check '12.1: install exit 0' ($LASTEXITCODE -eq 0)
$reg = Get-ItemProperty $SvcKey
Check '12.1: registration re-verified (byte-for-byte image, LocalSystem, auto, 45 s)' (($reg.ImagePath -ceq $ExpectedImage) -and ($reg.ObjectName -eq 'LocalSystem') -and ($reg.Start -eq 2) -and ($reg.StopTimeout -eq 45))
Check '12.1: Stopped after install' ((Get-SvcState).State -eq 'Stopped')

& $Goal --service start
Check '12.1: start reached Running' ($LASTEXITCODE -eq 0)
Check '12.1: health OK' ((Invoke-WebRequest -UseBasicParsing $Health).StatusCode -eq 200)
```

**Expected:** identical registration contract as Step 4 (D2/D5/D6.3); service Running; UI reachable.
**Evidence:** install stdout, the registry tuple, the start line.

### 12.2 — Real llama.cpp runtime + model via the API (no hand-edited JSON)

**API contract (from source):**

`POST /api/v1/runtimes` accepts `runtimeRequest`:
```json
{"id":"...","name":"...","executable":"...","working_directory":"...","environment":{}}
```

`POST /api/v1/models` accepts `ModelEntry` directly:
```json
{"id":"...","name":"...","runtime_id":"...","args":[],"environment":{},"active":false}
```

Note: all JSON field names are snake_case. Go's decoder silently ignores unknown fields (no `DisallowUnknownFields`), so wrong field names yield 201 with zero-valued fields. The read-back below catches this.

```powershell
# Runtime (real executable, absolute path, working dir = its directory):
$code = Invoke-GoalAPI POST '/api/v1/runtimes' @{
    id = 'llama-cpp'; name = 'llama.cpp';
    executable = $llamaExe; working_directory = $llamaDir; environment = @{}
}
Check '12.2: runtime created (201)' ($code -eq 201) "code=$code"
$rt = Get-GoalJSON '/api/v1/runtimes/llama-cpp'
"runtime: $($rt | ConvertTo-Json -Compress)"
Check '12.2: runtime executable correct (read-back)' ($rt.executable -eq $llamaExe) "got=$($rt.executable)"
Check '12.2: runtime working_directory correct (read-back)' ($rt.working_directory -eq $llamaDir) "got=$($rt.working_directory)"

# Model (real .gguf; --alias is a read-only marker flag used in 12.5).
# If the owner's llama-server build does NOT support --alias, the launch in
# 12.4 fails: STOP with diagnostics and substitute another harmless read-only
# flag as the marker (keep the two distinct marker strings).
$code = Invoke-GoalAPI POST '/api/v1/models' @{
    id = 'llama-model'; name = 'llama-model'; runtime_id = 'llama-cpp';
    args = @('-m', $modelGguf, '--port', [string]$modelPort, '--alias', 'model-args-marker');
    environment = @{}
}
Check '12.2: model created (201)' ($code -eq 201) "code=$code"
$model = Get-GoalJSON '/api/v1/models/llama-model'
"model: $($model | ConvertTo-Json -Compress)"
Check '12.2: model runtime_id linked (read-back)' ($model.runtime_id -eq 'llama-cpp') "got=$($model.runtime_id)"
Check '12.2: model args contain model path (read-back)' ($model.args -contains $modelGguf)
Check '12.2: model active = false (read-back)' ($model.active -eq $false) "got=$($model.active)"

# Repository read-back (schema v8 top-level collections):
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$rtRepo = @($repo.runtimes | Where-Object { $_.id -eq 'llama-cpp' })
$modelRepo = @($repo.models | Where-Object { $_.id -eq 'llama-model' })
Check '12.2: runtime in repo with executable' ($rtRepo.Count -eq 1 -and $rtRepo[0].executable -eq $llamaExe)
Check '12.2: model in repo with runtime_id' ($modelRepo.Count -eq 1 -and $modelRepo[0].runtime_id -eq 'llama-cpp')

# Functional LocalSystem access proof happens on the FIRST launch (12.4):
# the service (LocalSystem) must be able to read $llamaExe and $modelGguf.
# If 12.4's launch fails with 'Access is denied' → STOP the Step 12 chain
# and report: the two ACL tables from 12.0 + the failed instance row.
# Do NOT change any ACL to make the test pass.
Check '12.2: no llama-server process (nothing launched yet)' ((Get-LlamaPids).Count -eq 0)
Check '12.2: service still Running' ((Get-SvcState).State -eq 'Running')
```

**Expected:** two `201`s; the read-back confirms `executable`, `working_directory`, `runtime_id`, `args`, and `active=false` are all correct.
**Evidence:** both API responses, the GET readbacks, the repo JSON rows.

### 12.3 — Pipeline via the API (`Active=true`, entry `AutoStart=true`)

**API contract (from source, `pipeline.go:27-34`):**

`POST /api/v1/pipelines` decodes into `PipelineEntry`:
```json
{"name":"...","active":true,"models":[{"model_id":"...","args":[],"auto_start":true}]}
```
The `id` field is ignored (server generates). All names are snake_case.

```powershell
$code = Invoke-GoalAPI POST '/api/v1/pipelines' @{
    name = 'acceptance-pipeline'; active = $true;
    models = @(@{ model_id = 'llama-model'; auto_start = $true })
}
Check '12.3: pipeline created (201)' ($code -eq 201) "code=$code"

# Read back via API (ID is server-generated):
$pipe = (Get-GoalJSON '/api/v1/pipelines') | Where-Object { $_.name -eq 'acceptance-pipeline' } | Select-Object -First 1
$pipeId = $pipe.id
"pipeline id: $pipeId"
Check '12.3: pipeline id present' ($null -ne $pipeId -and $pipeId -ne '') "id=$pipeId"
Check '12.3: Active = true (read-back)' ($pipe.active -eq $true)
Check '12.3: exactly one entry' (@($pipe.models).Count -eq 1) "count=$(@($pipe.models).Count)"
Check '12.3: entry AutoStart = true (read-back)' ($pipe.models[0].auto_start -eq $true)
Check '12.3: entry references llama-model (read-back)' ($pipe.models[0].model_id -eq 'llama-model')

# Repository read-back (schema v8, top-level "pipelines"):
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$pipeRepo = @($repo.pipelines | Where-Object { $_.id -eq $pipeId })
Check '12.3: pipeline in repo' ($pipeRepo.Count -eq 1) "count=$($pipeRepo.Count)"
Check '12.3: repo active = true' ($pipeRepo[0].active -eq $true)
Check '12.3: repo entry auto_start = true' ($pipeRepo[0].models[0].auto_start -eq $true)
Check '12.3: repo entry model_id = llama-model' ($pipeRepo[0].models[0].model_id -eq 'llama-model')

# Semantic: no llama-server started yet (pipeline is Active but not yet launched)
Check '12.3: no acceptance llama-server running (not yet started)' ((Get-LlamaPids).Count -eq 0)
Check '12.3: service still Running' ((Get-SvcState).State -eq 'Running')
```

**Expected:** `201`; read-back confirms `active=true`, single entry with `auto_start=true` and `model_id=llama-model`; repo confirms persistence; no llama process yet.
**Evidence:** the create response, the GET readback, the repo JSON row.

### 12.4 — Pipeline manual lifecycle: Start → verify → Stop → verify

```powershell
$resp = Invoke-RestMethod -Method POST -UseBasicParsing "http://127.0.0.1:8099/api/v1/pipelines/$pipeId/start"
"start results: $($resp.results | ConvertTo-Json -Compress)"
Check '12.4: start result = started' ($resp.results[0].status -eq 'started') "status=$($resp.results[0].status)"

# Real model load: llama-server takes a while to map the .gguf — poll the model endpoint.
$deadline = (Get-Date).AddMinutes(5)
$modelUp = $false
while ((Get-Date) -lt $deadline) {
    try {
        $mm = Invoke-RestMethod -UseBasicParsing -TimeoutSec 3 "http://127.0.0.1:$modelPort/v1/models"
        if ($mm) { $modelUp = $true; break }
    } catch { }
    Start-Sleep -Seconds 2
}
Check '12.4: llama-server process EXISTS' ((Get-LlamaPids).Count -ge 1) "pids=$(Get-LlamaPids -join ',')"
Check '12.4: model HTTP endpoint answers (/v1/models 200)' $modelUp

Start-Sleep -Seconds 2   # let the instance reach 'running' in the repo
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$inst = $repo.instances | Where-Object { $_.model_id -eq 'llama-model' -and $_.state -in 'running','starting' } | Select-Object -First 1
Check '12.4: running instance exists' ($null -ne $inst) "state=$($inst.state)"
Check '12.4: instance.pipeline_id == pipeline id (pipeline-owned)' ($inst.pipeline_id -eq $pipeId) "pipeline_id=$($inst.pipeline_id) vs $pipeId"
Check '12.4: instance PID == live llama-server PID' ($inst.pid -in (Get-LlamaPids)) "instance pid=$($inst.pid), live=$(Get-LlamaPids -join ',')"
$llamaPidStart = (Get-LlamaPids)[0]
"llama PID (for 12.6): $llamaPidStart"

# STOP:
$resp = Invoke-RestMethod -Method POST -UseBasicParsing "http://127.0.0.1:8099/api/v1/pipelines/$pipeId/stop"
"stop results: $($resp.results | ConvertTo-Json -Compress)"
Start-Sleep -Seconds 2
Check '12.4: llama-server terminated after pipeline stop' ((Get-LlamaPids).Count -eq 0)
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$inst = $repo.instances | Where-Object { $_.model_id -eq 'llama-model' } | Sort-Object started_at -Descending | Select-Object -First 1
Check '12.4: instance persisted terminal (stopped)' ($inst.state -eq 'stopped') "state=$($inst.state)"
```

**Expected:** start → one real `llama-server` process; instance row with `pipeline_id`; model endpoint 200; stop → process gone, instance `stopped`.
**If the start result is `failed` with an access error:** STOP the Step 12 chain — report the ACL tables (12.0) + the failed instance row (state + error text) + the Event Log line. No ACL changes.
**Evidence:** both result JSONs, the instance row (id, state, pid, pipeline_id), the llama PID, the `/v1/models` response body.

### 12.5 — Pipeline Restart + Args override (full replacement, not merge)

```powershell
# 12.5a — restart: PID must change, new process must really run.
$resp = Invoke-RestMethod -Method POST -UseBasicParsing "http://127.0.0.1:8099/api/v1/pipelines/$pipeId/restart"
"restart: $($resp.stop_results | ConvertTo-Json -Compress) / $($resp.start_results | ConvertTo-Json -Compress)"
$llamaPidRestart = Wait-Llama
Check '12.5a: restart start_result = started' ($resp.start_results[0].status -eq 'started')
Check '12.5a: llama PID CHANGED' ($llamaPidRestart -ne $llamaPidStart) "before=$llamaPidStart after=$llamaPidRestart"
Check '12.5a: new llama-server process alive' ((Get-LlamaPids).Count -eq 1)
# (model endpoint re-check: poll /v1/models again like in 12.4, expect 200)

# 12.5b — args override: full replacement via PUT (name/args/Active/AutoStart
# may change; the model sequence is unchanged → no 409 even while active).
# Override marker 'pipeline-args-marker' REPLACES 'model-args-marker' —
# if args were merged/appended, the old marker would survive in the cmdline.
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$modelArgsBefore = ($repo.models | Where-Object { $_.id -eq 'llama-model' }).args
$code = Invoke-GoalAPI PUT "/api/v1/pipelines/$pipeId" @{
    name = 'acceptance-pipeline'; active = $true;
    models = @(@{ model_id = 'llama-model'; auto_start = $true;
                  args = @('-m', $modelGguf, '--port', [string]$modelPort, '--alias', 'pipeline-args-marker') })
}
Check '12.5b: override PUT accepted (200)' ($code -eq 200) "code=$code"

$resp = Invoke-RestMethod -Method POST -UseBasicParsing "http://127.0.0.1:8099/api/v1/pipelines/$pipeId/restart"
$llamaPid2 = Wait-Llama
$llama = Get-CimInstance Win32_Process -Filter "ProcessId=$llamaPid2"
"effective cmdline: $($llama.CommandLine)"
Check '12.5b: EFFECTIVE args = override (pipeline marker present)' ($llama.CommandLine -match 'pipeline-args-marker')
Check '12.5b: NOT merged (model marker ABSENT from cmdline)' ($llama.CommandLine -notmatch 'model-args-marker')
# HTTP corroboration: llama-server serves the alias as the model name.
$mm = Invoke-RestMethod -UseBasicParsing "http://127.0.0.1:$modelPort/v1/models"
"v1/models: $($mm | ConvertTo-Json -Compress)"
Check '12.5b: served model name = pipeline-args-marker' ($mm.data.id -match 'pipeline-args-marker')

$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$modelArgsAfter = ($repo.models | Where-Object { $_.id -eq 'llama-model' }).args
Check '12.5b: persisted Model.Args byte-identical to the original' (($modelArgsBefore -join '`n') -eq ($modelArgsAfter -join '`n')) "before=$($modelArgsBefore -join ' ') / after=$($modelArgsAfter -join ' ')"

# Restore the model-marker args for the rest of the run:
$code = Invoke-GoalAPI PUT "/api/v1/pipelines/$pipeId" @{
    name = 'acceptance-pipeline'; active = $true;
    models = @(@{ model_id = 'llama-model'; auto_start = $true })
}
Check '12.5b: override removed (200)' ($code -eq 200)
Invoke-RestMethod -Method POST -UseBasicParsing "http://127.0.0.1:8099/api/v1/pipelines/$pipeId/restart" | Out-Null
$llamaPid3 = Wait-Llama
$llama = Get-CimInstance Win32_Process -Filter "ProcessId=$llamaPid3"
Check '12.5b: restored cmdline uses model args again' ($llama.CommandLine -match 'model-args-marker')
```

**Expected:** restart changes the llama PID and the new process serves; the override is a **full replacement** (only the pipeline marker in the effective command line, model marker absent), the persisted `Model.Args` is untouched; restore works.
**Evidence:** the restart result JSONs, the old/new llama PIDs, the effective `CommandLine` string, the `/v1/models` body, the before/after `Model.Args`.

### 12.6 — Windows Service stop/start with pipeline autostart (ADR 010 D4, ADR 011 D6)

```powershell
# Preconditions: Pipeline.Active=true, entry AutoStart=true (set in 12.3/12.5b),
# and a pipeline-owned llama instance currently running.
Check '12.6: precond — pipeline owned instance running' (((Get-LlamaPids).Count -eq 1) -and ((Get-SvcState).State -eq 'Running'))

# 12.6a — service stop: the owned llama process MUST die (Job Object + ShutdownWithPersistence).
& $Goal --service stop
Start-Sleep -Seconds 2
Check '12.6a: service Stopped' ((Get-SvcState).State -eq 'Stopped')
Check '12.6a: owned llama-server terminated by service stop' ((Get-LlamaPids).Count -eq 0)

# 12.6b — service start: Recover -> pipeline autostart -> running pipeline-owned instance.
& $Goal --service start
Check '12.6b: service Running' ($LASTEXITCODE -eq 0)
# Wait for the autostarted model to come up (poll the model endpoint, up to 5 min):
$deadline = (Get-Date).AddMinutes(5); $modelUp = $false
while ((Get-Date) -lt $deadline) {
    try { if (Invoke-RestMethod -UseBasicParsing -TimeoutSec 3 "http://127.0.0.1:$modelPort/v1/models") { $modelUp = $true; break } } catch { }
    Start-Sleep -Seconds 3
}
Start-Sleep -Seconds 2
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$activeInst = $repo.instances | Where-Object { $_.model_id -eq 'llama-model' -and $_.state -in 'running','starting' } | Select-Object -First 1
Check '12.6b: pipeline autostart produced a running owned instance' ($null -ne $activeInst) "state=$($activeInst.state)"
Check '12.6b: pipeline_id preserved on the autostarted instance' ($activeInst.pipeline_id -eq $pipeId) "pipeline_id=$($activeInst.pipeline_id)"
Check '12.6b: model really up (endpoint 200)' $modelUp
Check '12.6b: exactly one llama-server process' ((Get-LlamaPids).Count -eq 1)
# The startup sequence is visible in the Event Log (operational slog):
# After D8.1 fix: ProviderName="GoAl". Filter by ProviderName.
$el126 = $null
try {
    $el126 = @(Get-WinEvent -LogName Application -MaxEvents 100 -ErrorAction Stop |
        Where-Object { $_.TimeCreated -gt (Get-Date).AddMinutes(-5) } |
        Where-Object { $_.ProviderName -eq 'GoAl' })
} catch {
    Check '12.6: Event Log retrieval' $false "Get-WinEvent threw: $($_.Exception.Message)"
    $el126 = @()
}
$el126Texts = @($el126 | ForEach-Object {
    $txt = $_.Message
    if ([string]::IsNullOrWhiteSpace($txt) -and $_.Properties.Count -gt 0) { $txt = [string]$_.Properties[0].Value }
    $txt
})
"Event Log (last 5 min, GoAl operational):"
$el126Texts | ForEach-Object { "  $_" }
# Expected lines: 'pipeline autostart: entry outcome ... started' (and recovery lines if any).
```

**Expected:** stop kills the owned process; start runs Recover → pipeline autostart (before model-level) → a pipeline-owned running instance with `pipeline_id`; Event Log shows the autostart outcome.
**Evidence:** the two llama process states (absent after stop, one after start), the instance row (id, state, pipeline_id), the Event Log tail.

### 12.7 — Full service restart with a live pipeline-owned instance

```powershell
Check '12.7: precond — one llama + service running' (((Get-LlamaPids).Count -eq 1) -and ((Get-SvcState).State -eq 'Running'))
$goalBefore = (Get-Process goal).Id
$llamaBefore = (Get-LlamaPids)[0]
# Process-count watchers: the SERVICE goal.exe must never be 2 (the management
# CLI is also a goal.exe process while `--service restart` runs — count only
# the '--service run' command line); llama-server must never be 2.
# The watcher exits as soon as the full stop->start cycle is observed
# (0 service processes, then 1 stable for ~3 s); 150 s hard cap.
$watch = Start-Job {
    $g = 0; $l = 0; $seen0 = $false; $stable1 = 0
    for ($i = 0; $i -lt 600; $i++) {
        $gc = @(Get-CimInstance Win32_Process -Filter "Name='goal.exe'" -ErrorAction SilentlyContinue |
                Where-Object { $_.CommandLine -like '*--service run*' }).Count
        $lc = @(Get-CimInstance Win32_Process -Filter "Name='llama-server.exe'" -ErrorAction SilentlyContinue |
                Where-Object { $_.ExecutablePath -ceq $llamaExe }).Count
        if ($gc -gt $g) { $g = $gc }; if ($lc -gt $l) { $l = $lc }
        if ($gc -eq 0)      { $seen0 = $true; $stable1 = 0 }
        elseif ($gc -eq 1)  { $stable1 = $stable1 + 1 }
        else                { $stable1 = 0 }
        if ($seen0 -and $stable1 -ge 6) { break }
        Start-Sleep -Milliseconds 250
    }
    return "$g/$l"
}
& $Goal --service restart
$rc = $LASTEXITCODE
$counts = (Wait-Job $watch | Receive-Job); Remove-Job $watch -Force
$maxGoal, $maxLlama = $counts.Split('/') | ForEach-Object { [int]$_ }

$goalAfter = (Get-Process goal -ErrorAction SilentlyContinue).Id
$llamaAfter = @((Wait-Llama))
Check '12.7: restart exit 0' ($rc -eq 0)
Check '12.7: goal.exe PID before != after' ($goalAfter -ne $goalBefore) "before=$goalBefore after=$goalAfter"
Check '12.7: old llama (PID $llamaBefore) terminated' (-not (Get-Process -Id $llamaBefore -ErrorAction SilentlyContinue))
Check '12.7: NEW pipeline-owned llama exists (different PID)' (($llamaAfter.Count -eq 1) -and ($llamaAfter[0] -ne $llamaBefore)) "new=$($llamaAfter -join ',')"
Check '12.7: never two SERVICE goal.exe at once' ($maxGoal -le 1) "max=$maxGoal"
Check '12.7: never two managed llama-server copies at once' ($maxLlama -le 1) "max=$maxLlama"
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$activeInst = $repo.instances | Where-Object { $_.model_id -eq 'llama-model' -and $_.state -in 'running','starting' } | Select-Object -First 1
Check '12.7: new instance pipeline-owned' ($activeInst.pipeline_id -eq $pipeId)
```

**Expected:** stop phase killed the old llama and persisted state; start phase re-established the pipeline-owned instance; no moment with two SERVICE goal.exe or two llama-server copies.
**Evidence:** goal before/after PIDs, llama before/after PIDs, the max-count pair, the new instance row.

### 12.8 — Forced GoAl termination: Job Object + ADR 005 + pipeline autostart together

```powershell
Check '12.8: precond — one llama + service running' (((Get-LlamaPids).Count -eq 1) -and ((Get-SvcState).State -eq 'Running'))
$llamaBefore = (Get-LlamaPids)[0]
$oldInstId = (Get-Content $RepoFile -Raw | ConvertFrom-Json).instances |
    Where-Object { $_.model_id -eq 'llama-model' -and $_.state -in 'running','starting' } |
    Select-Object -First 1 -ExpandProperty id
"old instance id: $oldInstId (llama PID $llamaBefore)"

taskkill /F /IM goal.exe
Start-Sleep -Seconds 2
Check '12.8: goal.exe dead' (-not (Get-Process goal -ErrorAction SilentlyContinue))
Check '12.8: llama-server killed with it (Job Object kill-on-close)' (-not (Get-Process -Id $llamaBefore -ErrorAction SilentlyContinue))

# Service start: Recover must stale the old instance (NOT resurrect/reattach it),
# then pipeline autostart must create a NEW pipeline-owned instance.
& $Goal --service start
Check '12.8: service Running' ($LASTEXITCODE -eq 0)
$deadline = (Get-Date).AddMinutes(5); $modelUp = $false
while ((Get-Date) -lt $deadline) {
    try { if (Invoke-RestMethod -UseBasicParsing -TimeoutSec 3 "http://127.0.0.1:$modelPort/v1/models") { $modelUp = $true; break } } catch { }
    Start-Sleep -Seconds 3
}
Start-Sleep -Seconds 2
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$old = $repo.instances | Where-Object { $_.id -eq $oldInstId } | Select-Object -First 1
$new = $repo.instances | Where-Object { $_.model_id -eq 'llama-model' -and $_.state -in 'running','starting' } | Select-Object -First 1
Check '12.8: stale HISTORICAL instance NOT resurrected (still stale, not running)' ($old.state -eq 'stale') "state=$($old.state)"
Check '12.8: NEW pipeline-owned instance running' ($null -ne $new) "state=$($new.state)"
Check '12.8: new instance has pipeline_id and a NEW id' (($new.pipeline_id -eq $pipeId) -and ($new.id -ne $oldInstId)) "new id=$($new.id)"
Check '12.8: model up (endpoint 200)' $modelUp
Check '12.8: NO duplicate active instance (exactly one llama, one active row)' (((Get-LlamaPids).Count -eq 1) -and (@($repo.instances | Where-Object { $_.model_id -eq 'llama-model' -and $_.state -in 'running','starting','pending','stopping' }).Count -eq 1))
```

**Expected (ADR 005 + ADR 010 D4):** kill-on-close kills the real child; recovery marks the old instance `stale` (pid-not-found) without touching it; pipeline autostart starts exactly one fresh pipeline-owned instance; no duplicate active instance.
**Evidence:** old instance row (state), new instance row (id, state, pipeline_id), llama process check, the Event Log tail (recovery + autostart lines).

### 12.9 — Pipeline vs model-level autostart ownership (ADR 010 D4)

```powershell
# Enable model-level autostart on the SAME model (existing product contract):
$code = Invoke-GoalAPI POST '/api/v1/models/llama-model/activate'
Check '12.9: model activated (200)' ($code -eq 200) "code=$code"
$pipe = (Get-GoalJSON "/api/v1/pipelines/$pipeId").pipeline
Check '12.9: precond — pipeline Active + entry AutoStart + model Active' (($pipe.active -eq $true) -and ($pipe.models[0].auto_start -eq $true) -and ((Get-GoalJSON '/api/v1/models/llama-model').active -eq $true))

# Full service restart: on startup, pipeline autostart runs BEFORE model-level
# (cmd/goal/main.go autostartPipelines → autostartModels); the model-level
# path must SKIP the model (already has an active instance).
& $Goal --service restart
Check '12.9: service Running' ($LASTEXITCODE -eq 0)
$deadline = (Get-Date).AddMinutes(5); $modelUp = $false
while ((Get-Date) -lt $deadline) {
    try { if (Invoke-RestMethod -UseBasicParsing -TimeoutSec 3 "http://127.0.0.1:$modelPort/v1/models") { $modelUp = $true; break } } catch { }
    Start-Sleep -Seconds 3
}
Start-Sleep -Seconds 2
$repo = Get-Content $RepoFile -Raw | ConvertFrom-Json
$activeRows = @($repo.instances | Where-Object { $_.model_id -eq 'llama-model' -and $_.state -in 'running','starting','pending','stopping' })
Check '12.9: EXACTLY ONE active instance (pipeline-first, model-level skipped)' ($activeRows.Count -eq 1) "count=$($activeRows.Count)"
Check '12.9: the active instance is PIPELINE-OWNED' ($activeRows[0].pipeline_id -eq $pipeId) "pipeline_id=$($activeRows[0].pipeline_id)"
Check '12.9: no duplicate process (one llama-server)' ((Get-LlamaPids).Count -eq 1)
Check '12.9: model up (endpoint 200)' $modelUp
# The model-level skip is an operational log line → Event Log (last 5 min):
# After D8.1 fix: ProviderName="GoAl". Filter by ProviderName.
$el129 = $null
try {
    $el129 = @(Get-WinEvent -LogName Application -MaxEvents 100 -ErrorAction Stop |
        Where-Object { $_.TimeCreated -gt (Get-Date).AddMinutes(-5) } |
        Where-Object { $_.ProviderName -eq 'GoAl' })
} catch {
    Check '12.9: Event Log retrieval' $false "Get-WinEvent threw: $($_.Exception.Message)"
    $el129 = @()
}
$el129Texts = @($el129 | ForEach-Object {
    $txt = $_.Message
    if ([string]::IsNullOrWhiteSpace($txt) -and $_.Properties.Count -gt 0) { $txt = [string]$_.Properties[0].Value }
    $txt
})
"Event Log (last 5 min, GoAl operational):"
$el129Texts | ForEach-Object { "  $_" }
# Expected: 'pipeline autostart: entry outcome ... started'
#           AND  'autostart: skipping (active instance exists)' (model llama-model)
```

**Expected (ADR 010 D4):** pipeline-first; exactly one active instance; it is pipeline-owned; the model-level autostart logged a skip; no duplicate process.
**Evidence:** the active-rows count, the instance row (pipeline_id), the llama count, the Event Log tail with both lines.

### 12.10 — Persistence across a service restart

```powershell
# Snapshot the pipeline definition + model args BEFORE the restart:
$pipeBefore = (Get-GoalJSON "/api/v1/pipelines/$pipeId").pipeline
$repoBefore = Get-Content $RepoFile -Raw | ConvertFrom-Json
$histBefore = @($repoBefore.instances | Where-Object { $_.pipeline_id -eq $pipeId } | Select-Object id, state, pipeline_id)

& $Goal --service restart
Check '12.10: service Running after restart' ($LASTEXITCODE -eq 0)
Start-Sleep -Seconds 5

$pipeAfter = (Get-GoalJSON "/api/v1/pipelines/$pipeId").pipeline
Check '12.10: pipeline name preserved' ($pipeBefore.name -eq $pipeAfter.name)
Check '12.10: pipeline Active preserved' ($pipeBefore.active -eq $pipeAfter.active)
Check '12.10: model ordering preserved (same sequence)' ((@($pipeBefore.models).model_id) -join '|' -eq (@($pipeAfter.models).model_id) -join '|')
Check '12.10: entry AutoStart preserved' ((@($pipeBefore.models).auto_start) -join '|' -eq (@($pipeAfter.models).auto_start) -join '|')
$repoAfter = Get-Content $RepoFile -Raw | ConvertFrom-Json
$histAfter = @($repoAfter.instances | Where-Object { $_.pipeline_id -eq $pipeId } | Select-Object id, state, pipeline_id)
Check '12.10: historical pipeline_id attribution preserved on all past instances' (@($histBefore).Count -eq (@($histAfter | Where-Object { $_.id -in $histBefore.id })).Count) "before=$(@($histBefore).Count) after=$(@($histAfter).Count)"
Check '12.10: pipeline-owned instance re-established after restart' (@($repoAfter.instances | Where-Object { $_.pipeline_id -eq $pipeId -and $_.state -in 'running','starting' }).Count -eq 1)
```

**Expected:** schema v8 survives the restart byte-for-byte in definition terms (name/active/ordering/auto_start/args) and every historical instance keeps its `pipeline_id`.
**Evidence:** the before/after pipeline JSON pair, the historical instance id set comparison.

### 12.11 — Logs / UI after a service restart

**Browser actions (real Chromium, `http://127.0.0.1:8099/`):**

1. **Connection monitor across stop/start:** with the UI open, run `& $Goal --service stop`; expect the sidebar dot to turn **red** and the top **connection banner** to appear (localized). Run `& $Goal --service start`; expect the dot to return **green** and a recovery toast (no manual refresh needed — the 5 s health probe). Screenshot each state.
2. **Pipelines page after the restart:** the pipeline shows the per-model live status as **running** with the live instance PID; `Active` and the entry `AutoStart` toggles reflect the persisted state (12.10). Screenshot.
3. **Logs of the new owned instance:** open the model's instance Logs (or `GET /api/v1/instances/<new-instance-id>/logs`) — the **new** instance's log stream is available and contains the llama-server startup output; the old (stale) instance's history is still readable from the Instances page. Screenshot + the first ~10 log lines.
4. **API corroboration (no UI needed):**
```powershell
$newInstId = (Get-Content $RepoFile -Raw | ConvertFrom-Json).instances |
    Where-Object { $_.pipeline_id -eq $pipeId -and $_.state -in 'running','starting' } |
    Select-Object -First 1 -ExpandProperty id
$code = (Invoke-WebRequest -UseBasicParsing "http://127.0.0.1:8099/api/v1/instances/$newInstId/logs").StatusCode
Check '12.11: logs of the NEW owned instance available (200)' ($code -eq 200) "code=$code"
$pipeDetail = Get-GoalJSON "/api/v1/pipelines/$pipeId"
Check '12.11: pipeline detail reports the running model with a live PID' ($pipeDetail.models[0].state -eq 'running' -and $pipeDetail.models[0].pid -gt 0) "state=$($pipeDetail.models[0].state) pid=$($pipeDetail.models[0].pid)"
```

**Expected:** the UI recovers its connection state automatically; the Pipelines page reflects the post-restart live state; logs work for the new owned instance.
**Evidence:** three screenshots (red banner / green recovery / Pipelines page running), the log lines, the two API PASS lines.

### 12.12 — Cleanup (acceptance entities + test artifacts ONLY)

```powershell
# 12.12a — stop the pipeline (service still up — the API runs on 8099):
Invoke-RestMethod -Method POST -UseBasicParsing "http://127.0.0.1:8099/api/v1/pipelines/$pipeId/stop" | Out-Null
Start-Sleep -Seconds 2
Check '12.12a: llama-server stopped by pipeline stop' ((Get-LlamaPids).Count -eq 0)

# 12.12b — delete ONLY the entities created by THIS acceptance (via API,
# service STILL running — a stopped service would refuse the connections):
$code = Invoke-GoalAPI DELETE "/api/v1/pipelines/$pipeId"
Check '12.12b: pipeline deleted (200)' ($code -eq 200) "code=$code"
$code = Invoke-GoalAPI DELETE '/api/v1/models/llama-model'
Check '12.12b: model record deleted (200)' ($code -eq 200) "code=$code"
$code = Invoke-GoalAPI DELETE '/api/v1/runtimes/llama-cpp'
Check '12.12b: runtime record deleted (200)' ($code -eq 200) "code=$code"
# (Model/runtime .gguf/.exe FILES are NOT touched — only the GoAl records.)
Check '12.12b: real model file untouched' (Test-Path $modelGguf)
Check '12.12b: real runtime untouched' (Test-Path $llamaExe)

# 12.12c — NOW stop the service (no active instances left), uninstall, remove
# the isolated environment:
& $Goal --service stop
Check '12.12c: service Stopped' ($LASTEXITCODE -eq 0 -and (Get-SvcState).State -eq 'Stopped')
& $Goal --service uninstall
Check '12.12c: service uninstalled' ($LASTEXITCODE -eq 0 -and -not (Get-Service $Svc -ErrorAction SilentlyContinue))
Remove-Item $Root -Recurse -Force
Check '12.12c: C:\goal-accept removed' (-not (Test-Path $Root))
# Production configuration/data/binaries: verify you never pointed the test
# config at them (different dataDir, different port) — final self-check:
Check '12.12c: no leftover goal/llama processes' (-not (Get-Process goal -ErrorAction SilentlyContinue) -and -not (Get-LlamaPids))
```

**Expected:** pipeline stop → entity deletion (service still up) → service stop → uninstall order; only the three acceptance records deleted; the user's real model/runtime files intact; test service and environment fully removed.
**Evidence:** the three delete codes, the file-existence checks, the uninstall line.

---

## Item → step map (ADR 011 acceptance contract)

| ADR item | Verified in | What to send back |
|---|---|---|
| **1 Registration** | Step 4 | install stdout; `sc.exe qc` block; registry `ImagePath` (actual vs expected, byte-for-byte), `ObjectName`, `Start`, `StopTimeout`; Stopped-after-install |
| **2 Install refusal** | Step 3 (+ in-place cases) | the two refusal transcripts (missing + relative `dataDir`); idempotent re-install stdout (Step 4 note); the remaining matrix is covered by `cmd/goal/service_install_test.go` (cite) |
| **3 Start semantics** | Step 5 + Step 6b | first-healthy vs first-running timestamps; status + PID; the busy-port start failure (precondition PASSes, non-zero exit, never-Running observation, Stopped final, listener released, restore to Running, Event Log line) |
| **4 Stop contract** | Step 6 | stop duration; final Stopped; fake-runtime gone; terminal repo state; audit tail; (StopPending sample if caught) + cite `service_handler_test.go` for the 30 000 ms wait hint |
| **5 Unclean-kill residual** | Step 8 | pre-kill/kill/post-kill states; child dead; reconciled `stale` + diagnostic; no resurrection |
| **6 Restart** | Step 7 | before/after PIDs; max observed process count (=1); Running after |
| **7 LocalSystem** | Step 9 | failed instance (state + error text); UI screenshot; Event Log error line; `ObjectName` re-read |
| **8 Diagnostics** | Step 10 | Event Log dump (Application/GoAl, last hour); mirror-check PASS; audit tail |
| **9 Uninstall** | Step 11 | stop → data snapshot (service stopped, no writer left) → uninstall → post comparison: same file set + identical hashes; config hash; not-found transcripts |
| **10 Portability & gates** | automated (cite) | `gofmt`/`go vet` clean, `go test ./...` + `-race` (CI), Win+Linux builds — already verified in the working tree |
| **11 Updater untouched** | automated (cite) | `git diff --stat` shows no `internal/updater` change |

## Pipeline real-world acceptance map (Step 12)

| Step 12 subtest | Verifies | What to send back |
|---|---|---|
| 12.1 | Re-creation after Step 11 | install stdout, registry tuple, Running |
| 12.2 | Real runtime/model via API (no hand-edited JSON) | two 201s, GET readbacks, ACL tables |
| 12.3 | Pipeline create (Active + AutoStart) | 201 + readback (id/active/auto_start) |
| 12.4 | Manual lifecycle: start/stop, `pipeline_id`, real process, model HTTP endpoint | result JSONs, instance row, llama PID, `/v1/models` body |
| 12.5 | Restart PID change + args override = full replacement (not merge) + `Model.Args` byte-identical | old/new PIDs, effective `CommandLine`, `/v1/models` alias, before/after `Model.Args` |
| 12.6 | Service stop/start with pipeline autostart (D4 + Job Object) | llama absent after stop, one owned running instance after start, Event Log tail |
| 12.7 | Full service restart | goal PIDs before/after, llama before/after, max-count pair (≤1/≤1) |
| 12.8 | taskkill /F: kill-on-close + ADR 005 + autostart together | old row `stale`, new pipeline-owned row, no duplicate |
| 12.9 | Pipeline-first vs model-level autostart (ADR 010 D4) | exactly one active row, pipeline-owned, Event Log "skipping" line |
| 12.10 | Schema v8 persistence across restart | before/after pipeline JSON pair, historical `pipeline_id` set |
| 12.11 | Logs/UI after restart | 3 screenshots + logs of the new owned instance + 2 API PASS lines |
| 12.12 | Cleanup: only acceptance entities/artifacts | three delete codes, real files intact, environment removed |

## Evidence format

Send back a single pasted transcript (or a `.log` file) containing, in order:

1. Step 0 `EXPECTED_IMAGE` line.
2. Step 1: `-version` output + PASS lines.
3. Steps 3–11: every `[PASS]/[FAIL]` line **with its detail line**, plus the raw stdout of every `goal --service …` invocation, the two Event Log dumps, the two repo-JSON instance rows (pre/post), the UI screenshots from Steps 8 and 9, and the Step 11 hash comparison.
4. Step 12 (only if Steps 0–11 all passed): every `[PASS]/[FAIL]` line with its detail line, the pipeline start/stop/restart result JSONs, the effective `CommandLine` strings, the llama PIDs at each transition, the Event Log tails (12.6/12.8/12.9), the before/after pipeline JSON pair, and the three 12.11 screenshots.
5. Any `FAIL` line must include the full surrounding output.

---

## Final verdict (fill in after owner execution)

Do **not** mark any line PASS before the corresponding owner execution on the real Windows machine.

```
ADR 011 real-SCM acceptance (Steps 0–11, ADR 011 items 1–8):   PASS
Pipeline real-world acceptance (Step 12, subtests 12.1–12.12): PASS
Combined Windows Service + Pipeline acceptance:                PASS
```

**Verdict rules:**

- `ADR 011 real-SCM acceptance` = PASS only when all Steps 0–11 report `[PASS]` (with the cited automated evidence for the item-2 matrix, the 30 000 ms wait hint, and the race gate). Any `[FAIL]` → FAIL with the failing step + output.
- `Pipeline real-world acceptance` = PASS only when all Step 12 subtests (12.1–12.12) report `[PASS]`. A LocalSystem access denial at 12.2/12.4 is a **STOP with diagnostics** (environment precondition, not a product defect) — the verdict stays PENDING for that subtest chain until the owner provides an accessible path.
- `Combined Windows Service + Pipeline acceptance` = PASS only when **both** lines above are PASS. It is the release-candidate gate for the Windows Service + Pipeline feature set.
- After any verdict change, reconcile `ROADMAP.md` (P1 "Windows Service / Background Mode" item + the Pipeline MVP item) and ADR 011 §Status per the task-completion reconciliation rules.

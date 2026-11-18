# ADR 025: GPU telemetry source — read-only nvidia-smi sampling (RES-02A)

**Status:** **Proposed** (Owner decision 2026-11-19 selects the source; implementation pending separate authorization). Per [../DEVELOPMENT.md](../DEVELOPMENT.md) §ADR process (`:177-199`), **Proposed** = "decision made, implementation pending". This gate creates **only this ADR file**; it authorizes **no** production code, **no** test, **no** dependency, **no** commit / push / tag. The Owner decision below fixes the source and the minimal contract; the bounded **RES-02A** implementation slice is authorized separately, and commit / publication / release remain separately-authorized gates.

**Date:** 2026-11-19

**Depends on:** [ADR 007 — Audit Logging](007-audit-logging.md) (the identifier-only audit policy this telemetry must **not** feed measurements into); [ADR 016 — Durable Lifecycle Ownership](016-durable-lifecycle-ownership.md) and [ADR 021 — Lifecycle State Observability View](021-lifecycle-state-observability-view.md) (the lifecycle semantics this ADR leaves **unchanged** — `Stop` stays synchronous, no API shape change, ADR 021 I-10); [ADR 024 — Commands](024-commands-control-command-dispatch.md) (the Commands API this ADR does **not** modify, and whose D-11/D-12 explicitly defer measured VRAM accounting to **G3-RES-02**).

**Related:** [ROADMAP.md](../../ROADMAP.md) §GoAl 3 → `#### 2 — Resource Management` (`:348-354`, "VRAM accounting has no current source and would need its own design"); [BACKLOG.md](../../BACKLOG.md) **G3-RES-02** (`:403`); [AGENTS.md](../../AGENTS.md) hard constraints (`:19-26`) and file ownership (`:31`). **RES-02B** (Stop / VRAM-delta acceptance) is a **separate future task**, not this ADR.

**Numbering:** `docs/adr/` holds `0001`, `002`–`011`, `013`, `014`, `016`–`024` at baseline (`012` never materialized; `015` is a roadmap-referenced **FROZEN** gate with no file). **025 is the next free number** and the only ADR file added by this gate.

**Baseline for every file:line reference below:** `2e4b8d49355ae4f7beb1a2f76305a58ba5909033` (`HEAD == origin/main` at the time of writing). The cited routes, handlers and constraints — not the line numbers — are the contract.

## Context

### What is being contracted

GoAl 3 priority **#2 — Resource Management** ([ROADMAP.md](../../ROADMAP.md) `:348-354`) needs a way to **observe** GPU/VRAM state so that resource release after a component `Stop` can eventually be measured. Today **no measurement source exists**: `GET /api/v1/metrics` reports instance counts + server settings only ([system.go](../../internal/webui/handlers/system.go) `:108-136`, [API.md](../API.md) `:68`); there is **no** GPU/VRAM/NVML code or dependency anywhere in the tree (`go.mod` has exactly two direct dependencies — `golang.org/x/crypto`, `golang.org/x/sys`; a repo-wide grep for GPU/NVML/gopsutil/nvidia-smi in `.go` files returns zero). ADR 024 repeatedly disclaims measured-VRAM claims and defers them to **G3-RES-02** ([024](024-commands-control-command-dispatch.md) `:74`, `:92`, `:117`, `:118`, `:122`).

This ADR contracts the **smallest read-only telemetry source** (RES-02A): one authenticated endpoint that returns a fresh, GPU-wide VRAM snapshot. It deliberately does **not** measure per-component consumption, does **not** change any lifecycle, and does **not** start the Stop/VRAM-delta acceptance (RES-02B).

### Why this is ADR-first (the significance test)

[AGENTS.md](../../AGENTS.md) `:65` gates **significant work** behind `ROADMAP → ADR → contract → implementation → tests → acceptance → docs reconciliation` and lists **"public API"** among the triggers. RES-02A adds a **new public HTTP endpoint** (`GET /api/v1/system/gpu`) and a **new platform-specific subprocess seam** (`internal/platform`). The data model, state vocabulary, freshness rule and execution bounds are a durable contract, not a one-off — hence ADR-first.

### Present bytes (measured, not recalled)

| Surface | Coordinate | Present value |
|---|---|---|
| Read-only system endpoint | [routes.go](../../internal/webui/handlers/routes.go) `:168` | `GET /api/v1/metrics` → `r.requireAuth(r.systemHandler.Metrics)` — **`requireAuth`, no CSRF** (the read-only precedent this ADR follows) |
| Metrics handler | [system.go](../../internal/webui/handlers/system.go) `:108-136` | inline `map[string]any`: instance counts + `listen_address`/`web_port`/`auth_enabled`/`admin_user`/`admin_password_set`. **No** GPU/VRAM/memory/PID fields |
| Direct dependencies | `go.mod` | `golang.org/x/crypto v0.54.0`, `golang.org/x/sys v0.47.0` only — **no** GPU/system-stats library, **no** cgo |
| Platform layer | [internal/platform/](../../internal/platform/) | `ProcessControl` (Job Object / process group), recovery liveness+identity only — **no** resource introspection |
| Hard constraints | [AGENTS.md](../../AGENTS.md) `:19-26` | no shell for runtimes (`:19`); `[]string` args (`:20`); one `exec.Cmd` `Wait` owner (`:21`); **HTTP handlers must not manage `exec.Cmd` directly** (`:22`); single binary (`:24`); platform-specific behavior in `internal/platform` (`:26`) |
| No GPU/VRAM source | repo-wide grep | zero GPU/NVML/VRAM identifiers in any `.go` file; the only GPU string is a test arg `--main-gpu 0` ([tests/browser/pipeline.cjs](../../tests/browser/pipeline.cjs) `:1023`) |

### Source selection (Owner decision 2026-11-19)

Three sources were compared in the G3-RES-02A discovery and design-correction rounds:

- **A — direct NVML bindings.** Windows: `syscall.NewLazyDLL("nvml.dll")` + `SyscallN` (stdlib, no cgo) is proven. **Linux: a zero-dependency, no-cgo `dlopen` is not available** — `golang.org/x/sys/unix` exposes no `Dlopen`/`Dlsym` (verified against `x/sys@v0.47.0`; Go issue #18296 "dlopen/dlsym without CGo" remains open), so calling `libnvidia-ml.so` without cgo needs assembly trampolines or a new dependency (`github.com/ebitengine/purego`). An in-process NVML call also **cannot be interrupted by a Go context** — a driver hang leaks the calling goroutine.
- **B — `nvidia-smi` via `exec.CommandContext` (no shell).** Present with the driver on both platforms; `[]string` args; **killable** bounded execution (context cancel ⇒ `SIGKILL` / `TerminateProcess`); CSV output includes GPU UUID. Cost: a short-lived child process per sample and defensive CSV parsing.
- **C — hybrid (NVML on Windows, nvidia-smi on Linux).** Technically sound but two code paths.

**Owner decision: a single unified `nvidia-smi` provider on Windows AND Linux (option B)** for the first implementation slice — one code path, zero new dependencies, zero cgo, and genuinely bounded (killable) execution on both platforms. The NVML/hybrid alternatives are recorded here as considered-and-not-chosen for this slice; they are not forbidden for a later slice.

## Decision

The telemetry source is a **read-only `nvidia-smi` sampler behind a provider seam in `internal/platform`**, exposed through **one** authenticated read-only endpoint. The sealed invariants are **D-1 … D-12**.

- **D-1 — Source: `nvidia-smi` via `exec.CommandContext`, no shell, no cgo.** A single provider invokes `nvidia-smi` directly with `exec.CommandContext(ctx, binaryPath, args...)` where `args` is a `[]string` (AGENTS.md `:20`) — **never** `sh -c`, `cmd /c`, PowerShell interpolation, or any shell (AGENTS.md `:19`). `nvidia-smi` is a **read-only measurement utility, not a managed runtime**: it is **not** launched through `Supervisor.AdmitAndStart`, creates **no** instance record, and touches **no** lifecycle. The `exec.Cmd` has **exactly one owner calling `Wait`** (AGENTS.md `:21`) — the provider (via `CommandContext` + `Output`/`Run`, which `Wait` internally). The provider lives in **`internal/platform`** (AGENTS.md `:26`, `:31`); the **HTTP handler never manages `exec.Cmd` directly** (AGENTS.md `:22`) — it delegates to the provider through an interface (D-10). **No new dependency, no cgo** (`os/exec`, `context`, `strconv`, `strings`, `time` are stdlib).

- **D-2 — Binary discovery; absence of driver / NVIDIA is a state, not a failure.** The provider resolves the binary once per sample attempt:
  - **Linux:** `exec.LookPath("nvidia-smi")` (the driver package puts it on `PATH`).
  - **Windows:** the canonical NVSMI path `%ProgramFiles%\NVIDIA Corporation\NVSMI\nvidia-smi.exe` (i.e. `C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe`), then `exec.LookPath("nvidia-smi")` as a fallback. (DriverStore enumeration is an implementation detail, not part of this contract.)
  - **Binary not found**, or `nvidia-smi` exits non-zero reporting it cannot communicate with the driver / no devices were found ⇒ **`gpu_unavailable`** (D-6). This covers **no NVIDIA hardware**, **no driver installed**, and **driver present but no usable GPU**. The exit code is **not** relied upon as a stable numeric value (it is not stably documented across versions); the signal is **non-zero exit + stderr classification**, never a specific code. `gpu_unavailable` is a **normal, healthy** outcome (the headless-CI case), **not** an error that fails the request or the process.

- **D-3 — Bounded execution; no uncontrolled repeated calls.**
  - **Timeout:** each sample runs under a fixed `context.WithTimeout` (proposed **5 s**; `nvidia-smi` normally completes well under 1 s). On expiry `exec.CommandContext` **terminates the child** (`SIGKILL` on Unix, `TerminateProcess` on Windows) and `WaitDelay` bounds the wait for I/O closure — the child is **killed, not leaked**. A timed-out sample ⇒ **`measurement_failed`** (D-6).
  - **Concurrency limit:** **single-flight** — at most **one** in-flight `nvidia-smi` child at any time. Concurrent callers share the **one** in-flight sample (same fresh result, same deadline); they do **not** each spawn a process. There is no unbounded queue of waiters.
  - **No uncontrolled repetition:** sampling is **caller-driven** (one HTTP GET ⇒ at most one shared sample). There is **no** background polling loop, **no** timer, and **no** automatic retry-on-failure. A failed measurement is returned as-is; the caller decides whether to ask again.

- **D-4 — API: `GET /api/v1/system/gpu`, authenticated, read-only.** One new route wrapped by **`requireAuth`** (the read-only precedent of `GET /api/v1/metrics`, [routes.go](../../internal/webui/handlers/routes.go) `:168`) — **no CSRF** (the request changes no state). **HTTP status policy: always `200`**, with the measurement state carried in the body (D-6); the endpoint never returns 5xx for an unavailable/failed measurement (those are normal telemetry states, not server errors). Unauthenticated ⇒ `401` (the existing `requireAuth` behavior). The endpoint is **additive**: no existing route's behavior or mapping changes.

- **D-5 — Data model (GPU-wide totals).** The query is `nvidia-smi --query-gpu=index,uuid,name,driver_version,memory.total,memory.used,memory.free --format=csv,noheader,nounits`. Each device row maps to:

  | Field | Type | Source / note |
  |---|---|---|
  | `uuid` | string | `uuid` column — **stable identity** (D-8) |
  | `index` | int | `index` column — **current position** (D-8) |
  | `name` | string | `name` column |
  | `driver` | string | `driver_version` column |
  | `total_bytes` | int64 | `memory.total` (MiB) **× 1024 × 1024** |
  | `used_bytes` | int64 | `memory.used` (MiB) × 1024 × 1024 — **whole-GPU** (WDDM caveat, D-9) |
  | `free_bytes` | int64 | `memory.free` (MiB) × 1024 × 1024 |
  | `sampled_at` | string (RFC3339Nano) | timestamp taken **after** the query completes, present **only** with `state=ok` (D-7) |

  `nvidia-smi` reports memory in **MiB** with `nounits`; the contract is **bytes**, so the provider converts. **Defensive parsing:** a required numeric field that is `N/A` or unparseable for a device ⇒ the report is **`measurement_failed`** (D-6) — partial/zero totals are **never** emitted as `ok`.

- **D-6 — States: `ok` / `gpu_unavailable` / `measurement_failed`.** The response body carries a `state` enum:
  - **`ok`** — `nvidia-smi` ran and every device row parsed; `gpus` is populated; every sample carries a fresh `sampled_at`.
  - **`gpu_unavailable`** — binary not found, or no driver / no NVIDIA / no usable device (D-2). `gpus` is empty; `error` carries a bounded human-readable reason. **Normal healthy state** (headless CI).
  - **`measurement_failed`** — the binary/driver was reachable but the measurement did not succeed: non-zero exit not classified as unavailable, timeout (child killed), or an unparseable required field (D-5). `gpus` is empty; `error` carries a bounded reason. **Distinct from `gpu_unavailable`** — it means "was reachable, measurement broke" (e.g. device removed mid-query, driver hang).
  - `error` is a **bounded token/message**, never raw stack traces or unbounded process text.

- **D-7 — Freshness: no stale-success, no cached measurements.** There is **no measurement cache**. Every `ok` value exists **only** together with its own fresh `sampled_at`; a failed/timed-out read **never** returns a previously-successful value. Single-flight (D-3) shares **one fresh in-flight sample**, not a stored prior result — concurrent callers get the same fresh measurement, never a stale one. A consumer comparing two snapshots must check both `sampled_at` values; the API provides no implicit "last known good".

- **D-8 — Multi-GPU: all visible GPUs; UUID = identity, index = position.** The provider returns **every** device `nvidia-smi` enumerates (one CSV row per GPU). **`uuid` is the stable identity** (survives reboot / re-indexing); **`index` is the current enumeration position** (may change on hotplug). Both are present. **No GPU→component attribution exists** in this slice (there is no typed GPU field on Model/Runtime/instance — only opaque `Args`/`Environment`); attributing a GPU to a specific component is an open question for a later slice, **not** contracted here.

- **D-9 — Semantics: GPU-wide measurements only; no exact per-component attribution.** Every value is a **whole-GPU total**. Under Windows **WDDM**, `used` includes desktop/compositor/display allocations (the Windows kernel memory manager, not the NVIDIA driver, allocates frame memory), so it is **not** a per-context figure. **Nothing in this API or its documentation may state that a VRAM delta equals a specific component's exact consumption.** Any before/after delta is an **estimate / bound** with named caveats (asynchronous CUDA caching-allocator release after process exit; other GPU clients in the window; WDDM desktop memory). The Stop/VRAM-delta measurement that uses these snapshots is **RES-02B** (D-11), a separate task.

- **D-10 — Testability: provider seam + fake provider + fake executable.** The provider is reached through an interface (e.g. `GpuProvider.Sample(ctx) (GpuReport, error)`) so the HTTP handler and state logic are testable **without a GPU and without `nvidia-smi`**:
  - **Fake provider** (test-only): returns a fixed `GpuReport` / error / delay ⇒ deterministic unit tests of every state branch, single-flight, timeout, and the no-cache rule.
  - **Fake executable** (test-only): a small test binary that emits canned CSV (valid rows, `N/A` cells, malformed rows, slow/hanging behavior) so the **real** discovery + `exec.CommandContext` + parse + kill path is exercised deterministically on both platforms without NVIDIA hardware.
  - The handler **never** manages `exec.Cmd` (AGENTS.md `:22`); the seam keeps the subprocess inside `internal/platform`.

- **D-11 — Scope split: RES-02A (this ADR) vs RES-02B (separate, future).**
  - **RES-02A = GPU telemetry** — the source, endpoint, data model, states, freshness, execution bounds and tests contracted here.
  - **RES-02B = Stop / VRAM-delta acceptance** — the A/B scenario (before X → `instance.stop(A)` → PID-death of A + B unchanged → after Y; Δ published as an **estimate**; `instance.start(A)` → InstanceID preserved). **NOT STARTED**, not authorized by this ADR, and requiring its own contract/acceptance. RES-02A makes **no** measured VRAM-release claim.

- **D-12 — No-change guarantees.** This ADR and its slice **do not change**: the lifecycle (ADR 016 ownership / single `Wait`; ADR 021 `Stop` stays synchronous, no API shape change, I-10); the **Commands API** (ADR 024 — no new command, no dispatch change); **no Resource Manager** is created; **ADR 015 (Readiness) stays FROZEN** and is not touched (the idle/auto-unload half of G3-RES-02 remains readiness-gated and out of scope); **no VRAM scheduling, no resource limits**. The new endpoint is purely additive and read-only.

## Out of scope (each its own future decision, NOT this slice)

- **Per-process / per-component VRAM** (Windows WDDM per-process is compute-context-only and unreliable; no typed GPU field exists).
- **RES-02B** — Stop / VRAM-delta acceptance (D-11).
- **VRAM scheduling, resource limits, idle detection / auto-unload** (the last is gated on **ADR 015 FROZEN**, a separate Owner decision).
- **A typed GPU/device field** on Model/Runtime/instance; **GPU→component attribution**.
- **Prometheus / metrics collector** integration ([ROADMAP.md](../../ROADMAP.md) `:330` — "VRAM/system metrics remain a separate collector concern").
- **NVML / hybrid source** (considered, not chosen for this slice; D-1 source selection).

## Security analysis (no bypass)

- **Auth:** `requireAuth`, identical to every other read-only GET (e.g. `/api/v1/metrics`); no unauthenticated or LAN-exposed path (AGENTS.md `:25`). No CSRF needed (read-only, no state change).
- **Subprocess:** `nvidia-smi` launched directly, `[]string` args, no shell (AGENTS.md `:19-20`), one `Wait` owner (AGENTS.md `:21`), outside the HTTP handler (AGENTS.md `:22`), in `internal/platform` (AGENTS.md `:26`). It is a measurement utility, **not** a managed runtime — no `AdmitAndStart`, no instance record, no lifecycle interaction.
- **Bounded:** fixed timeout + child kill + single-flight + no auto-retry (D-3) — no unbounded subprocess fan-out, no handler block indefinitely.
- **Error surface:** bounded state + reason only (D-6); no raw process text, no internal detail, no new information channel. Measurements are **never** written to the ADR 007 audit log (identifier-only policy, [007](007-audit-logging.md) `:12`, [audit.go](../../internal/webui/audit/audit.go) `:86`).

## Acceptance (obligations for the RES-02A implementation slice, NOT run here)

- **C-1 (unit, no GPU):** with a fake provider, `ok` / `gpu_unavailable` / `measurement_failed` each produce the correct body shape and `state`; `sampled_at` present only on `ok`; single-flight shares one sample; a failed read never returns a prior `ok` (no cache).
- **C-2 (fake executable):** the real discovery + `exec.CommandContext` + CSV-parse path is exercised with a fake binary emitting valid rows, `N/A` cells (⇒ `measurement_failed`), malformed rows, and a hanging process (⇒ timeout ⇒ child killed ⇒ `measurement_failed`). Deterministic on Windows and Linux without NVIDIA.
- **C-3 (headless CI negative control):** on Windows **and** Linux CI runners with **no GPU/driver**, both builds are green and `GET /api/v1/system/gpu` returns `200` `gpu_unavailable` with the process healthy. **No new required CI dependency.**
- **C-4 (auth):** unauthenticated ⇒ `401`; authenticated ⇒ `200` with a state body; no CSRF required (read-only).
- **C-5 (real-hardware, Owner):** on a machine with an NVIDIA GPU, `state=ok` with `uuid`/`index`/`name`/`driver`/`total_bytes`/`used_bytes`/`free_bytes` populated and a fresh `sampled_at`; values are sane (free ≤ total, used + free ≈ total).
- **C-6 (multi-GPU, Owner, ≥2 GPUs):** `len(gpus)` equals the visible device count; UUIDs are distinct and stable; per-card totals are correct.
- **C-7 (error path, Owner, optional):** device removal / driver stop mid-query ⇒ `measurement_failed`, no crash, no stale `ok`.
- **C-8 (bounded execution):** a hanging `nvidia-smi` is killed at the timeout; the handler returns `measurement_failed` without blocking indefinitely; concurrent requests never spawn more than one child.
- **C-9 (no lifecycle / API change):** the existing routes, the Commands API (ADR 024), and the lifecycle (ADR 016 / 021 `Stop` synchronous contract) are **byte-for-byte unchanged**; regression suite green. No measured VRAM-release claim is made (RES-02B).

## Consequences

- **Positive:** one code path on both platforms; zero new dependencies / zero cgo (single-binary goal preserved, AGENTS.md `:24`); genuinely bounded, killable execution; deterministic tests without GPU; a fresh, honest GPU-wide snapshot that RES-02B can later sample around a `Stop`.
- **Negative / accepted trade-offs:** a short-lived child process per sample (heavier than in-process NVML); defensive CSV parsing (`N/A`, locale, version drift); Windows binary not on `PATH` (canonical-path discovery); WDDM `used` is whole-GPU (not per-context) — explicitly **not** a per-component figure (D-9). These are the reasons the source is contracted as **estimate-grade GPU-wide telemetry**, not exact accounting.
- **No stale-success risk:** no cache + per-sample timestamp + failed-never-returns-prior-ok (D-7).

## Owner decision (ratification of the source selection)

**Owner decision — 2026-11-19:** for the first implementation slice, a **single unified `nvidia-smi` provider on Windows and Linux** is selected (option B). This ADR fixes the minimal GPU-telemetry contract (D-1 … D-12). **Status remains Proposed**: implementation is **not** authorized by this gate; the bounded **RES-02A** slice requires separate Owner authorization, and commit / publication / release remain separately-authorized gates. **RES-02B is NOT started.** ADR 015 stays **FROZEN**; lifecycle, Commands API, Resource Manager (none created), VRAM scheduling/limits are unchanged.

## Provenance

This ADR registers the **G3-RES-02A** GPU-telemetry source decision under [ROADMAP.md](../../ROADMAP.md) §GoAl 3 → Resource Management and [BACKLOG.md](../../BACKLOG.md) **G3-RES-02**. It is the design prerequisite the ADR 024 D-11/D-12 carve-outs defer to ("Measured VRAM accounting … belongs to G3-RES-02"). Supersedes: nothing. The discovery and design-correction rounds (G3-RES-02 / G3-RES-02A) are chat workspace; this file is the durable contract. ROADMAP/BACKLOG reconciliation (registering RES-02A / RES-02B and this ADR) is a **separate** documentation step, not performed by this gate.
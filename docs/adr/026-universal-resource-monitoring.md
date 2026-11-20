# ADR 026: Universal Resource Monitoring — vendor-neutral observation model (G3-RES-03)

**Status:** **Accepted** — the Owner **ratified** the ADR 026 architecture and contract on **2026-11-19** (see the Owner decision section below). Ratification accepts the **contract**; it does **not** start implementation: the bounded first slice (**RES-03A**) requires **separate Owner authorization**, and commit / publication / release remain separately-authorized gates. This ratification gate is **documentation-only** — it authorizes **no** production code, **no** test, **no** dependency, **no** UI, **no** commit / push / tag. *(Per [../DEVELOPMENT.md](../DEVELOPMENT.md) §ADR process (`:177-199`), **Accepted** normally denotes "implemented or being implemented"; here it records Owner ratification of the contract ahead of the separately-gated RES-03A implementation, which is **NOT started**.)*

**Date:** 2026-11-19

**Depends on:** [ADR 025 — GPU telemetry source (RES-02A)](025-gpu-telemetry-source.md) (the published NVIDIA `nvidia-smi` provider this architecture wraps as its **first** provider; ADR 025 is **not modified**); [ADR 007 — Audit Logging](007-audit-logging.md) (identifier-only policy: measurements are **never** audited); [ADR 016 — Durable Lifecycle Ownership](016-durable-lifecycle-ownership.md) and [ADR 021 — Lifecycle State Observability View](021-lifecycle-state-observability-view.md) (lifecycle semantics left **unchanged**); [ADR 024 — Commands](024-commands-control-command-dispatch.md) (the Commands API left **unchanged**).

**Related:** [ROADMAP.md](../../ROADMAP.md) §GoAl 3 → `#### 2 — Resource Management`; [BACKLOG.md](../../BACKLOG.md) **G3-RES-02** (scheduling / limits / idle — the *separate* scheduling track) and the new **G3-RES-03** (this monitoring track); [AGENTS.md](../../AGENTS.md) hard constraints (`:19-26`) and file ownership (`:31`). **ADR 015 (Readiness) stays FROZEN** — idle / auto-unload / scheduling are out of scope. **RES-02B** (Stop / VRAM-delta acceptance) is a **separate future task**, not started here.

**Numbering:** `docs/adr/` holds `0001`, `002`–`011`, `013`, `014`, `016`–`025` at baseline (`012` never materialized; `015` is a roadmap-referenced **FROZEN** gate with no file). **026 is the next free number** and the only ADR file added by this gate.

**Baseline for every file:line reference below:** `21ed40194845e0a16761a166747148d319b749ef` (`HEAD == origin/main` at the time of writing). The cited routes, handlers, types and constraints — not the line numbers — are the contract.

## Context

### What is being contracted

GoAl is a **universal orchestrator of local AI models**, not a tool for one hardware vendor. Resource Management ([ROADMAP.md](../../ROADMAP.md) §GoAl 3 #2) needs a **vendor-neutral, extensible observation layer** for GPU and system resources across **Windows, Linux and macOS**, covering NVIDIA, AMD, Intel, Apple Silicon, **CPU-only** systems and **system RAM**. Today the only resource telemetry is **RES-02A** (ADR 025): a single NVIDIA-specific `nvidia-smi` provider behind `GpuProvider` ([internal/platform/gpu.go](../../internal/platform/gpu.go)) exposed at `GET /api/v1/system/gpu` ([routes.go](../../internal/webui/handlers/routes.go), [system.go:146](../../internal/webui/handlers/system.go#L146)). There is **no universal resource model, no system RAM/CPU observation, no AMD/Intel/Apple provider, and no per-process attribution**. `GET /api/v1/metrics` reports instance counts + settings only ([system.go:111](../../internal/webui/handlers/system.go#L111)).

This ADR contracts the **architecture** that generalizes RES-02A into a provider-pluggable monitoring layer, and **limits the first implementation slice** to the model + capabilities + aggregator + NVIDIA adapter + one additive universal endpoint. It deliberately does **not** implement other vendors, does **not** attribute resources to processes, does **not** change any lifecycle, and does **not** start scheduling or RES-02B.

### Why this is ADR-first (the significance test)

[AGENTS.md](../../AGENTS.md) `:65` gates significant work behind `ROADMAP → ADR → contract → implementation → tests → acceptance → docs reconciliation` and lists **"public API"** and **"cross-cutting architecture"** among the triggers. This adds a **new public HTTP endpoint** (`GET /api/v1/system/resources`), a **new domain package** (`internal/resource`), and a durable cross-platform resource model / state vocabulary / provenance contract — hence ADR-first.

### Present bytes (measured, not recalled)

| Surface | Coordinate | Present value |
|---|---|---|
| GPU provider seam | [internal/platform/gpu.go](../../internal/platform/gpu.go) | `GpuProvider.Sample(ctx) GpuReport`; `GpuReport{state, gpus[], error, sampled_at}`; states `ok` / `gpu_unavailable` / `measurement_failed`; single-flight, 5 s timeout + child kill + `WaitDelay`, **no cache** (D-7); whole-GPU totals, **no per-process attribution** (D-9) |
| GPU endpoint | [system.go:146](../../internal/webui/handlers/system.go#L146) | `GET /api/v1/system/gpu` → `requireAuth`, **no CSRF**, always **200**, **no audit**; delegates to provider, never manages `exec.Cmd` |
| Runner model | [internal/domain/instance.go:79](../../internal/domain/instance.go#L79) | `LaunchInstance{ID, ModelID, RuntimeID, PipelineID, PipelineEntryID, PID, State, …}`; **PID** links to the OS process; aliveness owned by the process Manager, **never inferred from state** |
| Lifecycle seam | [internal/platform/platform.go:6](../../internal/platform/platform.go#L6) | `ProcessControl{AfterStart, GracefulStop, ForceKill, WasSignaled, Close}` — **control**, not observation |
| Direct dependencies | `go.mod` | `golang.org/x/crypto v0.54.0`, `golang.org/x/sys v0.47.0` only — **no** GPU/system-stats library, **no** cgo |
| Hard constraints | [AGENTS.md](../../AGENTS.md) `:19-26` | no shell for runtimes; `[]string` args; one `exec.Cmd` `Wait` owner; **HTTP handlers never manage `exec.Cmd`**; single binary; platform-specific behavior in `internal/platform` |

### Idea source (not a dependency)

[`shanselman/gputaskman`](https://github.com/shanselman/gputaskman) (Python, Windows/Linux/WSL/macOS, NVIDIA/AMD/Intel/Apple) is used **only as a source of architectural ideas**: provider-per-vendor-platform behind a collector; **measured counters vs estimated classifications**; **`null` for unavailable counters, never a measured zero**; a capability matrix that varies by platform; resource types kept distinct (VRAM / shared GPU RAM / system RAM / commit); PID-reuse guarding by process start time; WDDM `Local`/`Non-Local` preferred over double-counting `Dedicated`; Apple unified memory. **No code is vendored and no dependency is introduced** — GoAl stays a Go single binary with zero new dependencies and no cgo.

## Decision

The architecture is a **read-only, vendor-neutral resource observation layer**: concrete providers in `internal/platform`, a universal model + aggregator in `internal/resource`, exposed through **one additive** authenticated endpoint, with the published NVIDIA endpoint preserved byte-for-byte. The sealed invariants are **D-1 … D-14**.

- **D-1 — Three separate layers: Monitoring ≠ Lifecycle ≠ Scheduling.** This ADR contracts **Resource Monitoring only** — read-only observation. It **never** calls `ProcessControl` (`AfterStart`/`GracefulStop`/`ForceKill`), creates no instance record, and touches no lifecycle. **Runner Lifecycle** (ADR 016 ownership / single `Wait`; ADR 017 arbitration; ADR 021 `Stop` synchronous; ADR 024 Commands) is **unchanged**. **Resource Scheduling** (limits, idle / auto-unload, VRAM scheduling) is a **separate future design**; idle / auto-unload stays gated on **ADR 015 FROZEN**. Monitoring supplies data a future scheduler may consume; it makes **no** scheduling decision.

- **D-2 — Package placement.** The universal model, `Capabilities`, the `ResourceProvider` interface and the **Aggregator** live in a **new package `internal/resource`** (pure Go, cross-platform, no OS calls). Concrete **vendor × OS providers** live in **`internal/platform`** (consistent with `GpuProvider`; AGENTS.md `:26`). The HTTP handler delegates to the aggregator through the interface and **never manages `exec.Cmd`** (AGENTS.md `:22`). `internal/resource` is a new domain surface; it imports `internal/platform` for the provider seam, never the reverse (no import cycle: `platform` imports no internal packages).

- **D-3 — Provider interface; three-level capabilities (`supported` / `available` / `sampled`).** Each provider implements:

  ```go
  type ResourceProvider interface {
      ID() string                  // stable provider id, e.g. "nvidia-smi"
      Vendor() Vendor              // nvidia | amd | intel | apple | system
      Capabilities() Capabilities  // supported / available / sampled (D-3)
      Sample(ctx context.Context) ProviderReport
  }
  ```

  `Capabilities()` reports **three distinct levels** per metric, and they are never conflated:

  | Level | Question answered | How determined |
  |---|---|---|
  | **`supported`** | Can this provider type measure this metric **in principle**? | **Static** provider capability — a design fact; changes only when the provider implementation changes |
  | **`available`** | Is the measurement **source present and addressable** on this system? | The provider's **existing source-discovery mechanism** (the same discovery RES-02A already performs inside its single bounded sample), evaluated **within the current request** — **no additional expensive launch** |
  | **`sampled`** | Did **this sample** actually obtain a value? | The outcome of this request's measurement |

  **All three levels are determined within the current request** (Owner decision 2026-11-19). `supported` is static; `available` reuses the existing source discovery; `sampled` is this sample's outcome. Capabilities are derived from the **same single bounded sample** the request already takes — `available` from its source-discovery outcome, `sampled` from its measurement outcome — so determining capabilities adds **no extra child process**. **No TTL, no background polling, no last-good cache, and no separate periodic probe are introduced**: capabilities are never cached across requests and never refreshed out-of-band.

  **Source discovery is not a driver-health guarantee.** Finding the binary (the source being addressable) does **not** prove the driver returns data; whether a value is actually obtained is `sampled`, not `available`. A source that discovery reports as **absent** (no GPU / no driver — the RES-02A `gpu_unavailable` case) is not `available` ⇒ provider `unavailable`; a source that is **present but whose measurement transiently breaks** is still `available`, not `sampled` ⇒ provider `measurement_failed` (D-7). **A temporary measurement error never changes `supported` or `available`** — a timeout, a transient non-zero exit, or an unparseable row only makes the metric not `sampled` and drives the report state. Conflating the levels would make capabilities flap on every transient failure and would let one bad sample erase a real platform capability. A provider whose core measurement succeeded is `ok` even when optional metrics are not `sampled` (D-7).

- **D-4 — Memory taxonomy and semantics (four distinct kinds, never conflated).**

  | Kind | Semantic | Source examples |
  |---|---|---|
  | **Dedicated VRAM** (`dedicated_vram`) | Physical memory on a discrete GPU, exclusively device-local. `total` / `used` / `free`. | NVIDIA `memory.*`; AMD `mem_info_vram_*`; Intel discrete dedicated |
  | **Shared GPU memory** (`shared_gpu`) | **System RAM the GPU can access** — not dedicated VRAM. WDDM `Non Local` / `Shared`; an **integrated GPU's** local memory IS shared system RAM and is labelled `shared_gpu`, never `dedicated_vram`. | Windows WDDM shared counters; Intel iGPU |
  | **Unified memory** (`unified`) | Apple Silicon: **one pool shared by CPU and GPU**; no dedicated/shared split. Reported as a single physical pool with a `gpu_allocated` subset. **Never** double-counted as VRAM + shared. | macOS IOAccelerator `Alloc system memory` |
  | **System RAM** (`system_ram`) | Machine physical memory `total` / `used` / `free` (+ swap/pagefile); per-process RSS/working set. **Never GPU bytes**; never added to any GPU or unified total. | Linux `/proc/meminfo`; Windows `GlobalMemoryStatusEx`; macOS `vm_stat` |

  A device declares a `memory_class` ∈ {`dedicated`, `shared`, `unified`}. A device reports **either** dedicated (+ optional shared) **or** unified — **never both**. The aggregator **never sums across kinds**; an integrated GPU's memory is `shared`, an Apple pool is `unified`, and system RAM is always separate.

- **D-5 — Resource types and units.** `ResourceType` is an **extensible enum** (`vram_total`, `vram_used`, `vram_free`, `shared_gpu_used`, `unified_total`, `unified_gpu_allocated`, `gpu_util`, `system_ram_total`, `system_ram_used`, `system_ram_free`, `process_rss`, `process_vram`, `cpu_util`, …). Memory is **bytes** (`int64`; MiB × 1024 × 1024, as RES-02A). Utilization is **percent as a rate between two samples**. Every reading carries an explicit `Unit` (`bytes` / `percent` / …). Binary prefixes (MiB/GiB); a percentage "memory activity" is **not** bandwidth GB/s. New types are additive and never break the slice-1 contract.

- **D-6 — Reading provenance and the `unavailable` contract (absence ≠ zero).** Every metric is a `Reading`:

  ```go
  type Reading struct {
      Value      *int64     // nil when unavailable; a real number when measured/estimated
      Unit       Unit       // bytes | percent | …
      Provenance Provenance // measured | estimated | unavailable
      Source     string     // counter/source, e.g. "nvidia-smi memory.used", "wddm_local"
      Reason     string     // bounded; why unavailable / how estimated
  }
  ```

  - **`measured`** — a driver/OS counter; `Value` is a real number (a **measured zero**, e.g. `free=0` on a full GPU or a successful empty process list, IS a value).
  - **`estimated`** — a computed number with a named formula/caveat in `Source`/`Reason` (e.g. Intel PCI-window VRAM total; WDDM `Paged = max(commit − local − nonlocal, 0)`); labelled, distinct from measured.
  - **`unavailable`** — **`Value` is `nil` (JSON `null`)**, `Provenance="unavailable"`, a bounded `Reason`. **Never `0`, never `-1`, never a placeholder.** The aggregator **never fabricates** a value to fill a gap and **never treats unavailable as zero** in any sum.

- **D-7 — Report state rules (precise; optional-metric absence never implies `partial`).** Each provider yields an outcome `ok` / `unavailable` / `measurement_failed` relative to its **core** measurement (optional readings do not change it). The report `state` is derived **only** from provider outcomes, in this order:

  | Condition | Report `state` |
  |---|---|
  | Every provider `unavailable` (or zero providers) — no supported source present | **`unavailable`** (healthy; CPU-only / headless) |
  | ≥1 provider `measurement_failed` **and** ≥1 provider `ok` | **`partial`** (some fresh data, some reachable measurement broke) |
  | ≥1 provider `measurement_failed` **and** no provider `ok` | **`measurement_failed`** |
  | ≥1 provider `ok` **and** no provider `measurement_failed` | **`ok`** |

  **`unavailable` and `measurement_failed` are different facts, never interchangeable:** `unavailable` means **no measurement source is present** (no GPU, no supported provider, binary/driver absent) — a healthy, permanent-until-changed condition; `measurement_failed` means **a source was reachable and the measurement broke** (timeout, non-zero exit, unparseable output) — a transient condition. Absence of hardware is never reported as a failure, and a failure is never reported as absence.

  **An absent optional metric is a `Reading` with `Provenance=unavailable`; it never downgrades the report state.** `partial` is therefore reachable only with **≥2 providers** (a future slice); in slice 1 (NVIDIA only) the report state ∈ {`ok`, `unavailable`, `measurement_failed`}. `sampled_at` (RFC3339Nano, the sweep completion time) is present **only** when ≥1 provider is `ok` (state `ok`/`partial`); absent otherwise. `error`/`errors` carry **bounded tokens**, never raw process text.

- **D-8 — CPU-only is a first-class, fully supported scenario.** The model **never requires a GPU**. Four rules:
  1. **Absence of a GPU is not an error.** A CPU-only system (no GPU, or no supported GPU provider) is **healthy, not degraded**: the endpoint answers **200** and `devices` is **empty (never `null`)**.
  2. **Absence of a measurement source is not a measurement failure.** No source present ⇒ `unavailable`; a source that was reachable and then broke ⇒ `measurement_failed` (D-7). The two are never conflated, and neither is an HTTP error.
  3. **System resources are independent of the GPU.** System RAM/CPU observation never depends on GPU presence. In slice 1 the only provider is NVIDIA, so a CPU-only box reports `unavailable` with `system=null`; once the **system RAM/CPU provider** exists (a future slice) the same box reports **`ok`** with machine data and still-empty `devices`.
  4. **No fabricated GPU devices.** The aggregator never synthesizes a placeholder device, a "virtual GPU", or zero-filled GPU readings to make a CPU-only system look instrumented. Empty means empty.

  `unavailable` ≠ error ≠ `partial` ≠ `measurement_failed`.

- **D-9 — Device identification.** Each device carries a **stable, vendor-namespaced `DeviceID`** (NVIDIA **UUID**; AMD/Intel **PCI BDF / sysfs card path / DXGI LUID**; Apple **Metal registry id**), an `Index` (current enumeration position, may change on hotplug), `Vendor`, `Name`, `Driver`. This generalizes RES-02A's `uuid` (identity) / `index` (position). Identity survives reboot/re-indexing; position does not.

- **D-10 — Per-process attribution (FUTURE slice; structure reserved, not populated in slice 1).** Per-process readings join a provider's **PID + process identity** (OS process creation/start time, the same identity notion the recovery layer uses) to `LaunchInstance.PID`, yielding the known `ModelID` / `RuntimeID` / `PipelineID`. **PID-reuse guard:** a reading is attributed to an instance **only** when the process identity matches; a reused PID with a different identity is **not** attributed (never the previous process's numbers). Attribution is **accounting, not an ownership graph**: a per-process VRAM counter is the OS/driver's charge to that PID and does **not** prove exclusive model ownership (shared surfaces, WDDM double-count, caching allocators). GoAl knows the model identity (an advantage over tools that guess it), but per-process VRAM provenance is still platform-bound (`measured` on Linux NVML/TCC; `unavailable` on WDDM GeForce; `estimated` where derived). **No exact per-component consumption claim** is made (ADR 025 D-9 preserved). Slice 1 reserves `processes` as an **empty array** and populates nothing.

- **D-11 — Universal API: `GET /api/v1/system/resources`, additive, read-only.** One new route wrapped by **`requireAuth`** (the read-only precedent of `/api/v1/metrics` and `/api/v1/system/gpu`) — **no CSRF** (no state change), **always HTTP 200** with the state in the body (D-7), **no audit record** (ADR 007 identifier-only). Unauthenticated ⇒ `401`. The body is a `ResourceReport`:

  ```jsonc
  {
    "state": "ok | partial | unavailable | measurement_failed",
    "devices": [ /* Device: device_id, index, vendor, name, driver, memory_class, resources{type: Reading} */ ],
    "system": null,            // slice 1: no system provider yet; future: { ram, cpu }
    "processes": [],           // slice 1: empty; future per-process (D-10)
    "capabilities": { /* per provider, per metric: supported / available / sampled (D-3) */ },
    "sampled_at": "…",         // present only when state ok/partial (D-7)
    "errors": [ /* {provider, state, reason} bounded */ ]
  }
  ```

  `devices` and `processes` are **empty arrays, never `null`**; an unavailable `Reading.Value` is **`null`** (D-6). The endpoint is **additive**: no existing route's behavior or mapping changes. Exact wire field names are ratified in the slice-1 contract; the structure and semantics above are the durable contract.

- **D-12 — RES-02A integration without breaking compatibility.** `GET /api/v1/system/gpu` remains **byte-for-byte identical** (`GpuReport{state, gpus[], error, sampled_at}`, always-200, `requireAuth`/no-CSRF/no-audit, ADR 025 D-1…D-12), and **its execution path is unchanged in RES-03A**: the route still calls the existing `SystemHandler` GPU method, which still calls the existing provider's `Sample` directly. The legacy handler is **not** rerouted through the aggregator, the adapter, or the universal model. The new endpoint is served by a **separate handler** that obtains NVIDIA data through an **adapter over the existing provider** (the first `ResourceProvider`); the provider's bounded / single-flight / no-cache behavior is **unchanged**. **No shared snapshot is required between independent HTTP requests.** A legacy request and a universal request are independent samples; the contract does not promise they observe the same sweep, and no cross-request or cross-endpoint snapshot cache is introduced. Single-flight inside the provider may still collapse genuinely concurrent calls into one child — that is an existing concurrency bound, not a snapshot guarantee. **ADR 025 is not modified.** The legacy **no-cache / no-stale-success rule (ADR 025 D-7) is preserved**; the universal endpoint makes freshness explicit per provider and does **not** adopt gputaskman's last-good per-process caching for GPU data.

- **D-13 — First implementation slice (RES-03A) is bounded (Owner-confirmed minimal scope).** Slice 1 = **(1) universal resource model + (2) capabilities (`supported`/`available`/`sampled`, determined within the current request per D-3) + (3) aggregator + (4) NVIDIA adapter + (5) the additive `GET /api/v1/system/resources`**, with **(6) legacy `GET /api/v1/system/gpu` unchanged** (byte-for-byte and execution-path, D-12). **NOT in slice 1** (each a separate future slice / Owner decision): **AMD, Intel, Apple Silicon, CPU/RAM providers**, and **per-process attribution**. The model must be extensible to host those without breaking the slice-1 contract (additive types, capabilities-driven). Slice 1 returns NVIDIA device data (or `unavailable` on CPU-only/headless), `system=null`, `processes=[]`. Ratification of this ADR does **not** authorize starting RES-03A; implementation requires separate Owner authorization.

- **D-14 — No-change guarantees.** This ADR and its slices **do not change**: the lifecycle (ADR 016 / 021), the Commands API (ADR 024), **ADR 015 (FROZEN)**, the published `GET /api/v1/system/gpu` contract (ADR 025), or `go.mod` (**no new dependency, no cgo**, single binary preserved). Measurement utilities are **bounded read-only children** (timeout + child kill + single-flight, as RES-02A), launched directly with `[]string` args and **no shell** — they are **not managed runtimes** (no `AdmitAndStart`, no instance record). Measurements are **never** written to the ADR 007 audit log. **No Resource Manager / scheduler is created.** **RES-02B is NOT started.**

## Out of scope (each its own future decision, NOT slice 1)

- **AMD / Intel / Apple Silicon / system RAM / CPU providers** (future slices; platform feasibility in §Consequences).
- **Per-process attribution** and the resource↔instance join (D-10; future slice).
- **RES-02B** — Stop / VRAM-delta acceptance (ADR 025 D-11).
- **Resource scheduling, limits, idle detection / auto-unload** (ADR 015 **FROZEN**; separate design).
- **UI** for resource monitoring (separate UX track); **Prometheus / metrics collector** ([ROADMAP.md](../../ROADMAP.md) Observability — separate collector concern).
- **A typed GPU/device field** on Model/Runtime/instance; **NVML / cgo / hybrid** sources; **any new dependency**.

## Security analysis (no bypass)

- **Auth:** `requireAuth`, identical to every other read-only GET; no unauthenticated or LAN-exposed path (AGENTS.md `:25`). No CSRF (read-only).
- **Subprocess:** measurement utilities launched directly, `[]string` args, no shell (AGENTS.md `:19-20`), one `Wait` owner (`:21`), outside the HTTP handler (`:22`), in `internal/platform` (`:26`). Not managed runtimes — no `AdmitAndStart`, no instance record, no lifecycle interaction.
- **Bounded:** fixed timeout + child kill + single-flight + no auto-retry (as RES-02A D-3) — no unbounded subprocess fan-out, no indefinite handler block.
- **Error surface:** bounded state + reason tokens only (D-6/D-7); no raw process text, no new information channel. Measurements **never** audited (ADR 007 identifier-only).

## Acceptance (obligations for the RES-03A slice, NOT run here)

- **C-1 (unit, no hardware):** with fake providers, the model / aggregator / capabilities / provenance / state rules are exercised deterministically: `null` ≠ measured zero; an absent optional metric leaves state `ok`; all-`unavailable` ⇒ `unavailable`; `ok`+`measurement_failed` (≥2 providers) ⇒ `partial`; all-failed ⇒ `measurement_failed`; no supported source ⇒ `unavailable`, never `measurement_failed`; capabilities are determined within the request from the existing source discovery (`supported` static, `available` source-addressable, `sampled` value-obtained), not from paper and not from a separate probe.
- **C-2 (NVIDIA adapter, fake executable):** the real discovery + `exec.CommandContext` + CSV-parse path is exercised with a fake binary (valid rows, `N/A`, malformed, hanging); the adapter maps the existing provider's report to universal `Device` readings without altering it; the legacy `GpuReport` bytes are unchanged.
- **C-3 (headless CI negative control):** on Windows **and** Linux CI with no GPU/driver, both builds green; `GET /api/v1/system/resources` ⇒ `200` `unavailable`, `devices=[]` (never null), process healthy; `GET /api/v1/system/gpu` ⇒ `200` `gpu_unavailable` unchanged. **No new required CI dependency.**
- **C-4 (auth):** unauthenticated ⇒ `401`; authenticated ⇒ `200` state body; no CSRF required.
- **C-5 (legacy compatibility regression):** `GET /api/v1/system/gpu` JSON is **byte-identical** to the `21ed401` baseline for `ok` / `gpu_unavailable` / `measurement_failed`; ADR 025 D-1…D-12 intact; the **legacy execution path is unchanged** (handler → existing provider `Sample`, not routed through the aggregator/adapter); no cross-request snapshot cache is introduced, and the two endpoints are shown to sample independently.
- **C-6 (three-level capabilities, determined within the request):** a metric the platform cannot supply (e.g. per-process VRAM on WDDM GeForce) is `supported` but not `available`, with a bounded reason — never presented as measurable. A source discovery reports as absent ⇒ not `available`; a source present but failing this sample ⇒ still `available`, not `sampled`. **A transient measurement failure leaves `supported` and `available` unchanged**, asserted with a fake provider that fails one sample then succeeds. **`available` is derived from the existing source-discovery outcome of the request's own sample — no extra child is spawned to determine capabilities**; a fake provider counts launches and proves one sample per request. **No TTL, background polling, last-good cache or periodic probe exists**: two sequential requests with the source removed in between show `available` changing only because discovery changed, and a failing-then-succeeding source never flips `supported`/`available`. **Binary discovery is not asserted as driver health**: a fake binary that is found but returns a driver error yields `available=true`, `sampled=false`, state `measurement_failed`.
- **C-7 (real-hardware, Owner):** on an NVIDIA GPU, `GET /api/v1/system/resources` ⇒ `ok` with `device_id`/`index`/`vendor`/`name`/`driver` and byte values matching `nvidia-smi` (MiB × 1048576), fresh `sampled_at`; the legacy endpoint is unchanged.
- **C-8 (no lifecycle / API / deps change):** existing routes, Commands (ADR 024) and lifecycle (ADR 016 / 021) byte-unchanged; `go.mod` unchanged; no cgo; regression suite green.
- **C-9 (bounded execution):** a hanging provider is killed at the timeout; single-flight shares one child; genuinely concurrent calls never spawn more than one child per provider; no goroutine/subprocess leak. This is a concurrency bound only — it is **not** asserted as a shared snapshot across independent requests (D-12).

## Consequences

- **Positive:** a vendor-neutral, extensible observation model; honest `measured`/`estimated`/`unavailable` provenance with `null` ≠ zero; CPU-only as a first-class healthy scenario; capabilities separated into `supported` / `available` / `sampled` so transient failures cannot erase a real capability; RES-02A preserved byte-for-byte as the first provider with its execution path untouched; zero new dependencies / no cgo (single binary preserved); deterministic tests without hardware; a clean separation of Monitoring from Lifecycle and Scheduling.
- **Negative / accepted trade-offs:** per-reading provenance makes the JSON more verbose than flat counters; an aggregator + capabilities layer adds complexity over a single vendor endpoint; **platform feasibility is uneven under no-dep/no-cgo** — Linux sysfs/`/proc` are pure file reads (easiest); Windows WDDM (PDH/DXGI) and AMD ADL are reachable via stdlib `syscall.NewLazyDLL` (no cgo) but limited; **macOS IOKit/Metal/mach per-process is not available in pure Go without cgo/trampolines**, so Apple starts device-wide (subprocess) with per-process `unavailable`, and system RAM/CPU use subprocess fallbacks. These are recorded as **future-slice** constraints, not slice-1 obligations.
- **No stale-success risk:** the legacy endpoint keeps ADR 025 D-7 (no cache, per-sample timestamp, failed-never-returns-prior-ok); the universal endpoint timestamps each sweep and never substitutes a prior value for a failed read.

## Owner decision (ratification)

**Owner decision — 2026-11-19 (direction APPROVED):** the universal resource-monitoring direction is **APPROVED**: `internal/resource` for the universal model; `internal/platform` for concrete providers; `GET /api/v1/system/resources`; preservation of legacy `/api/v1/system/gpu`; Monitoring separate from Lifecycle and Scheduling; no mandatory external dependencies; no cgo; vendor-neutral architecture. This ADR fixes the refined contract (D-1 … D-14): memory-kind semantics, CPU-only as a first-class scenario, three-level capabilities (`supported` / `available` / `sampled`, stable under transient measurement errors), precise state rules (optional-metric absence ≠ `partial`; no source ≠ measurement failure), the `unavailable` ≠ zero contract, PID + identity attribution without ownership guarantees, RES-02A compatibility with an unchanged legacy execution path and no shared snapshot between independent requests, and the bounded first slice.

**Owner decision — 2026-11-19 (RATIFICATION; additional capabilities decision):** the ADR 026 architecture is **APPROVED / RATIFIED** and the status moves **Proposed → Accepted**. **Capabilities in RES-03A are determined within the current request:**
- **`supported`** — the provider's **static** capability;
- **`available`** — the **source is available to address** (present per the existing source-discovery mechanism);
- **`sampled`** — the **value was successfully obtained** in this sample.

**Not introduced:** **TTL**, **background polling**, **last-good cache**, or a **separate periodic probe**. A **transient measurement failure must not automatically change `supported`/`available`**. The `available` check **reuses the existing source-discovery mechanism, with no additional expensive launch**. **Binary discovery is not claimed to guarantee driver functionality** — a found-but-failing source is `available` but not `sampled` (D-3, D-7). The **minimal RES-03A scope is confirmed**: universal resource model; capabilities; aggregator; NVIDIA adapter; additive `GET /api/v1/system/resources`; legacy `GET /api/v1/system/gpu` unchanged (D-13).

**Status: Accepted (ratified 2026-11-19).** Ratification accepts the **contract only**; it does **not** authorize implementation. The bounded **RES-03A** slice requires **separate Owner authorization**, and commit / publication / release remain separately-authorized gates. **RES-03A is NOT started. RES-02B is NOT started.** **ADR 015 stays FROZEN**; lifecycle, Commands API, the published GPU endpoint, scheduling/limits and `go.mod` are unchanged. *(DEVELOPMENT.md §ADR process defines Accepted as "implemented or being implemented"; this ratification records Owner acceptance of the contract ahead of the separately-gated implementation — see the Status header.)*

## Implementation record (additive, dated)

This section records implementation state **without amending the ratified contract D-1…D-14**. It **supersedes, as to current state only**, the "RES-03A is NOT started" clauses above and in §Provenance.

**2026-11-20 — RES-03A published (baseline reconciliation).** The Owner-authorized RES-03A slice (authorized 2026-11-19) was committed and **published** at `bf68a3741eb0c57bcffee6207053375ee0aaa530`; CI run `44595797028`, **attempt 1 = 7/7 success** (verified 2026-11-20: exact SHA, Linux race step without DATA RACE, Windows tests, browser acceptance, `origin/main == bf68a37`). Real-NVIDIA acceptance **C-7 remains OPEN** (environment: no NVIDIA hardware).

**2026-11-20 — RES-03B authorized + implemented (worktree, NOT committed).** The Owner authorized the bounded **RES-03B Universal CPU/RAM Monitoring** slice ("Продолжай проект. Task: G3-RES-03B", 2026-11-20): extend `GET /api/v1/system/resources` with system RAM/CPU metrics on Windows / Linux / macOS, working without NVIDIA and without any GPU. **No public-contract amendment was required**: D-11 already reserves the `system` field with the `{ram, cpu}` sketch, D-8.3 already contracts CPU-only ⇒ `ok` once a system provider exists, and D-5 allows additive resource types. The ratified D-1…D-14 are **unchanged**.

Implemented semantics (all within the existing contract):

- **Provider** `system` (`internal/platform` OS bindings + pure measure logic; `internal/resource.SystemAdapter`): one sample per request, **single-flight**, fixed **5 s** per-sample deadline, **2 s** WaitDelay on children, **no cache / no polling** (D-3). The core measurement is **RAM total**; the provider is `ok` only when it was obtained. Optional metrics never downgrade the state (D-7).
- **Metrics** (additive D-5 types): `system_ram_total`, `system_ram_available`, `system_ram_used`, `cpu_logical_cores` (new unit `count`), `cpu_util`. Wire shape per D-11: `system.ram.{total,available,used}`, `system.cpu.{logical_cores,utilization}`; `system=null` unless the system provider succeeded.
- **Provenance** (D-6): RAM total measured on all three OSes (`GlobalMemoryStatusEx.ullTotalPhys` / `/proc/meminfo MemTotal` / `sysctl hw.memsize`); RAM available measured on Windows/Linux (`MemAvailable` absent on kernels < 3.14 ⇒ `unavailable`, never fabricated), **estimated** on macOS (`vm_stat` `(free+inactive+speculative)*page_size`); RAM used **always estimated** = `total − available` (named formula; `unavailable` when a dependency was not sampled); cores measured; utilization a measured integer percent between **two counter reads inside the request** — fixed **250 ms** window on Windows (`GetSystemTimes`, kernel includes idle) and Linux (`/proc/stat`, idle = idle+iowait), macOS `top -l 2 -n 0` (second-sample rate, bounded read-only child, `[]string` args, no shell). Zero/regressing deltas ⇒ `unavailable` (`counter_delta_invalid`). Unsupported OS ⇒ `unavailable` (`platform_not_supported`). All reasons are bounded tokens.
- **State rules now reachable with two providers** (D-7): GPU `ok` + system `ok` ⇒ `ok`; CPU-only (GPU `unavailable` + system `ok`) ⇒ **`ok` with `system` data, `devices=[]`** (D-8.3); one `ok` + one `measurement_failed` ⇒ `partial`; both sources absent ⇒ `unavailable`.
- **Legacy preserved** (D-12): `GET /api/v1/system/gpu` byte-for-byte and execution-path unchanged; it never samples the system provider (asserted by call-count tests). `go.mod` unchanged; no cgo; no new dependency; no UI; no lifecycle/scheduling interaction (D-1).

Verification at implementation time: `gofmt` / `go build` (Windows + Linux + macOS cross-builds) / `go vet` green; full `go test ./...` green on Windows including a **live real-OS measurement test** (actual Windows counters, two fresh samples); all three OSes' measure logic tested cross-platform via injected primitives; macOS child path exercised with real exec over fake `vm_stat`/`top` binaries (`testdata/fakesys`); single-flight / deadline / waiter-cancellation / no-cache proven via the test seam. Local `-race` unavailable (no cgo) ⇒ race evidence deferred to Linux CI. **Real macOS acceptance OPEN** (compile + fake-logic/child tests only); **C-7 real-NVIDIA still OPEN**. Awaiting **Commit Gate + Publication Gate**; the G3-RES-03 checkbox stays unchecked. AMD / Intel / Apple GPU providers, per-process attribution and RES-02B remain **NOT started**.

**2026-11-21 — RES-03B published; G3-RES-04 Resource Monitoring UI (Slice 1) implemented (worktree, NOT committed).** This **supersedes, as to current state only**, the RES-03B "worktree, NOT committed" / "Awaiting Commit Gate + Publication Gate" clauses above: **RES-03B is committed + published** at `b823b60b53d5b9c4c583153a4e8b099515f7c54b` (verified 2026-11-21: `git ls-remote origin refs/heads/main == b823b60 == HEAD`). The Owner authorized the separate **UX track** named in §Out of scope ("UI for resource monitoring") as **G3-RES-04 — Resource Monitoring UI, Slice 1** ("Продолжай проект. Task: G3-RES-04", 2026-11-21): a read-only **Resources** page in the Web UI over the **existing published** `GET /api/v1/system/resources`. **No public-contract amendment** — the ratified D-1…D-14 are unchanged; the UI only renders the contracted wire shape (CPU utilization % + logical cores; RAM total / used / available + usage %, used `estimated`; one card per GPU with vendor / model / driver / memory class / VRAM total / used / free + usage %). It honors D-6 (measured zero is a value; `unavailable` ⇒ `—`, never a fake zero), D-7 (`ok` / `partial` / `unavailable` / `measurement_failed` rendered distinctly; absent optional metrics never hide the rest), D-8 (CPU-only is a first-class healthy state) and D-1 (observation only — no resource control, no scheduling, no model-lifecycle change). Multiple vendors (NVIDIA / AMD / Intel / Apple) render side by side with **no NVIDIA-specific assumption**. Refresh is **page-scoped** (fetch on open + manual Refresh + a 5 s interval only while the page is visible; leaving stops sampling — no constant backend / GPU-provider load), RU/EN parity preserved, no new dependency, no backend change. Verification: `gofmt` / `go vet` / full `go test ./...` green on Windows (21 ok + 4 no-test-files, 0 FAIL / 0 DATA RACE); Windows + Linux cross-builds green; browser `resources` 57/57 (incl. a real-endpoint leg), `i18n` 46/46, `responsive` 165/165, `core` 50/50 + 59/59; local `-race` unavailable (no cgo) ⇒ race evidence deferred to Linux CI. G3-RES-04 awaits **Commit Gate + Publication Gate + Owner acceptance**; its BACKLOG checkbox stays unchecked. **Real macOS acceptance and C-7 real-NVIDIA remain OPEN**; AMD / Intel / Apple GPU providers, per-process attribution and RES-02B remain **NOT started**.

## Provenance

This ADR registers the **G3-RES-03 Universal Resource Monitoring** architecture under [ROADMAP.md](../../ROADMAP.md) §GoAl 3 → Resource Management and [BACKLOG.md](../../BACKLOG.md). It builds on **RES-02A** (ADR 025, implemented + published at `21ed401`, CI `44467105981` 7/7) as the **first provider**, and is **distinct from G3-RES-02** (the scheduling / limits / idle track, ADR 015 FROZEN). **Supersedes: nothing** — ADR 025 is intact. The architecture-discovery round was chat workspace; this file is the durable contract. **Chronology (all 2026-11-19, baseline `21ed401`):** architecture proposal → ADR 026 drafted **Proposed** with plan-level ROADMAP/BACKLOG registration → final refinement (CPU-only semantics, unchanged legacy execution path / no shared snapshot, three-level capabilities) → **Owner ratification: Proposed → Accepted**, with the additional decision that capabilities are determined within the current request (no TTL / polling / last-good cache / periodic probe; `available` reuses existing source discovery; binary discovery ≠ driver health). Every stage was **documentation-only**; **RES-03A implementation is NOT started** and remains separately gated.
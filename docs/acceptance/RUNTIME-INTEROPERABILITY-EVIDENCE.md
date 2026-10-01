# Runtime Interoperability Evidence — external runtimes verified through the generic launch path

**Scope:** a register of third-party runtimes that a human has **manually verified** to launch, serve, stop, restart and re-start through GoAl's existing, generic **Runtime → Model → Instance** capability. It records observed interoperability of shipped behavior only.

This document is **not**:

- an official support commitment or a support matrix;
- evidence of a ComfyUI-specific, audio.cpp-specific or any other product-specific integration (none exists — see "What the generic path is");
- a feature, a `ROADMAP.md` task, or an ADR decision;
- an acceptance checklist to execute. The cases below were already run by the Owner; nothing here asks anyone to re-run them.

**Source of truth:** repository bytes at HEAD `ee16f50a8cb0449e90323115af7a46a81f3c30f5`. The recorded observations come from the Owner's manual verification reports; the code citations below are verified against these committed bytes. No claim here describes behavior that is not already implemented, and no claim here is stronger than the evidence recorded in its own row.

---

## Claim vocabulary

Three distinct statements are often conflated. This register keeps them apart:

| Level | Meaning | Who can assert it here |
|-------|---------|------------------------|
| Compatible by design | The launch contract is vendor-neutral, so nothing in GoAl prevents a given runtime class from working. | Provable from repository code (see below). |
| Manually verified interoperability | A named runtime build was actually launched and its lifecycle transitions were observed to work by a human on a real machine. | **This is the only level claimed for the cases below.** |
| Officially supported | The project commits to testing, fixing, and documenting that runtime going forward. | **Claimed for nothing in this document.** |

An entry reaching "manually verified interoperability" says: on one machine, with one build of one external runtime, the generic GoAl path started it, its own UI/endpoint answered, and Stop / repeated Start / Restart behaved as expected. It says nothing about other versions, other machines, other GPUs, continuous integration, or future compatibility.

---

## What the generic path is

GoAl launches every runtime through one vendor-neutral contract — there is no runtime-kind switch on the launch path:

- `internal/domain/runtime.go:10-19` — `Runtime` carries `Executable`, `WorkingDirectory`, `Environment`. There is **no** vendor/product/type discriminator field on the runtime entity (the `type` field in `internal/config/config.go:125` belongs to the health-check config, not to runtime identity).
- `internal/domain/command.go:14-19` — `CommandSpec{Executable, Args []string, WorkingDirectory, Environment}`; arguments are a `[]string`, never a shell command line.
- `internal/process/manager.go:152-153` — the single production spawn: `exec.Command(spec.Executable, spec.Args...)` with `cmd.Dir = spec.WorkingDirectory`. No shell interpreter is involved (AGENTS.md hard constraint).
- Launch arguments come from `Model.Args` (per `internal/domain/runtime.go:9`), so *which* flags a given external runtime needs is user-supplied configuration, not GoAl behavior.

A search of `internal/`, `cmd/`, `testdata/`, `tests/`, `deploy/`, `scripts/` and `.github/` for `comfy`, `audiocpp`, `audio.cpp`, `yue2` returns **zero** matches at this HEAD (this register is the only place in the repository where those product names appear) — confirming these cases exercised the generic path, not an integration.

---

## Register

| # | Runtime (as verified) | Version | Platform | Level reached | Verified transitions |
|---|-----------------------|---------|----------|---------------|----------------------|
| 1 | ComfyUI Windows Portable (NVIDIA) | not recorded | Windows | Manually verified interoperability | Start, UI availability, Stop, repeated Start, Restart — all PASS |
| 2 | audio.cpp | v0.8.1 | Windows | Manually verified interoperability | Start, Web UI availability, CUDA runtime, real YuE2 inference, Stop, repeated Start, Restart — all PASS |

---

## Case 1 — ComfyUI Windows Portable (NVIDIA)

Launched through the ordinary GoAl flow: a Runtime record pointing at the portable Python interpreter, a Model carrying its arguments, and an Instance started from that Model. No GoAl code, configuration schema, or API was involved beyond that generic path.

**Verified configuration (as reported by the Owner):**

| Field | Value |
|-------|-------|
| Working directory | `C:\tools\ComfyUI_windows_portable` |
| Executable | `C:\tools\ComfyUI_windows_portable\python_embeded\python.exe` |
| Arguments | **Not recorded by the Owner for this verification** — deliberately left blank rather than reconstructed (see Provenance limitations) |
| GoAl entities used | Runtime → Model → Instance |

**Observed results:**

| Check | Result |
|-------|--------|
| Start | PASS |
| UI availability | PASS |
| Stop | PASS |
| Repeated Start (after Stop) | PASS |
| Restart | PASS |

**Notes.** The runtime here is an embedded Python interpreter rather than a purpose-built server binary, which is exactly the shape the generic contract already supports: GoAl does not care what the executable is, only that the process starts, is owned by one supervisor, and can be stopped. The NVIDIA element is part of the portable distribution the Owner verified; nothing in GoAl selects or detects it.

---

## Case 2 — audio.cpp v0.8.1 with a YuE2 model

Launched through the same generic GoAl flow: a Runtime record pointing at the native server executable, a Model carrying the verified CLI flags, and an Instance started from that Model.

**Verified configuration (as reported by the Owner):**

| Field | Value |
|-------|-------|
| Executable | `C:\tools\audiocpp\v081\audiocpp_server.exe` |
| Working directory | **Not recorded by the Owner for this verification** |
| Arguments (the CLI actually used) | included `--config E:\models\music\Yue2\yue2-server.json`, `--host 0.0.0.0`, `--port 8086`, `--ui`, `--log` |
| External config file | `E:\models\music\Yue2\yue2-server.json` — this is **audio.cpp's own** configuration file, referenced by the runtime's `--config` flag. It is not a GoAl configuration file and GoAl does not read or validate it |
| GoAl entities used | Runtime → Model → Instance |

**Observed results:**

| Check | Result |
|-------|--------|
| Start | PASS |
| Web UI available on the verified endpoint (`--port 8086`) | PASS |
| CUDA runtime available to the process | PASS |
| Real YuE2 inference | PASS |
| Real audio result produced | PASS — approximately 4:06 of generated audio |
| Stop | PASS |
| Repeated Start (after Stop) | PASS |
| Restart | PASS |

**Port discrepancy — recorded as a fact, not resolved into a claim.** The external config `E:\models\music\Yue2\yue2-server.json` contains `port: 8084`, while the manually verified launch passed `--port 8086` on the command line, and the Web UI was observed on **8086**. Only these three observations are recorded. **No precedence rule between the config-file value and the CLI flag is asserted**: which value wins is audio.cpp's own behavior, and neither repository evidence (GoAl never reads that file) nor manual evidence in this report establishes it. Anyone needing a single deterministic port should not infer the resolution rule from this document — it is unproven here.

---

## Relationship to existing manual-acceptance precedent

This register is the second, additive instance of manual evidence about real external runtimes. The earlier precedent stays where it is and is **not** rewritten by this document:

- `ROADMAP.md:114` — Owner manual acceptance on a real Windows server with **real llama.cpp** (2026-08-28): PASS for autostart, Start, Stop, Restart, with real-hardware VRAM release/re-allocation evidence and child-process termination under both graceful GoAl shutdown and forced `taskkill /F` (ADR 001 Job Object).
- `ROADMAP.md:126` and [ADR011-WINDOWS-SERVICE-MANUAL-ACCEPTANCE.md](ADR011-WINDOWS-SERVICE-MANUAL-ACCEPTANCE.md) Step 12 — real-SCM Windows Service acceptance with a real `llama-server` process under a Pipeline.

Read together, those llama.cpp observations and the two cases above are the manual evidence that the generic launch contract has held for three different runtime shapes (a C++ inference server, an embedded-Python web app server, and a native audio-generation server). That is an observation about past verifications, not a compatibility promise.

---

## Provenance limitations

Recorded explicitly so no reader over-reads the evidence. None of the following is provable from the data available when this register was written, and nothing here substitutes a guessed value:

1. **GoAl version / commit under test is not established.** The Owner's reports give runtime paths and outcomes, not the GoAl build identity. The repository's newest tag at the time of writing is `v2.1.0` (`2026-09-12`) and HEAD is `ee16f50`, but neither is confirmed to be the binary that was used.
2. **SHA-256 of the `goal` binary used for either case is not recorded.** No hash comparison exists for these manual runs.
3. **Verification dates are not stated** for either case, so recency relative to a given GoAl release cannot be inferred.
4. **Case 1 arguments are not recorded.** ComfyUI portable needs launch arguments, but the Owner did not report the exact argv for this verification, so the Arguments row is intentionally blank rather than reconstructed from general knowledge of the product.
5. **Case 2 working directory is not recorded**; only the executable path and the CLI flags listed above are confirmed.
6. **Case 2's argument list is reported as "included", not as an exhaustive argv.** Additional flags may have been present.
7. **Machine and environment details are not recorded** (GPU model, driver, CUDA version, OS build) beyond "Windows" and the NVIDIA tag of the ComfyUI distribution.
8. **No automated coverage exists for either runtime.** These are human observations on real hardware; the maintained test suite uses `testdata/fake-runtime/`, and CI never installs ComfyUI or audio.cpp.
9. **The `port: 8084` vs `--port 8086` resolution rule is unproven** (see Case 2).

---

## Conventions for extending this register

- Add a row to **Register** plus one case section; do not create one file per product.
- Record only what was actually observed by the human verifier. If a field is unknown, write "not recorded" — never a plausible value.
- State the level reached using the three-level vocabulary above. The default, and so far the ceiling, is **manually verified interoperability**.
- Keep lifecycle checks in the same shape: Start, endpoint/UI availability, Stop, repeated Start, Restart, plus any case-specific real-work check (inference, media generation) with its observed outcome.
- Do not turn this register, `docs/USER_GUIDE.md`, or `README.md` into a support matrix. Compatibility promises require their own design gate, not a documentation edit.

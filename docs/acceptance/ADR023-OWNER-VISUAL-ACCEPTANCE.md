# ADR 023 — Owner Visual Acceptance: Record of Owner Evidence

**Purpose:** record the Owner's `[O]` verdicts on ADR 023 / UX V-2 exactly as supplied, and nothing else. **Every verdict in this file is the Owner's**, supplied in the gate instruction named below; the agent transcribes the evidence and derives no verdict of its own. A leg the Owner has not judged is recorded as *no verdict*, never as PASS and never as OPEN-by-inference. This record authorizes no code, no test, no CSS, HTML, JavaScript or i18n byte, no commit, no tag and no release.

**Source artifact the verdict was given against:** the Windows binary built at the Owner Visual Acceptance Preparation gate from `95829e5e8aef94e7b0bffb3218afdf71f216c4ee` (parent `648a0a0`; the published fast-forward HEAD — `git ls-remote origin refs/heads/main` returned the same SHA when this record was written), run from its own kit directory on loopback (its own config and data directory, never the Owner's long-running instance). Design contract: [ADR 023 — Model-card density and status vocabulary](../adr/023-model-card-density-and-status-vocabulary.md), status **Proposed**.

**Date of this record:** 2026-10-27. **Source of the verdicts:** the Owner's instruction at the ADR 023 / UX V-2.1 Design Amendment Gate (2026-10-27), quoted below. No Owner acceptance report and no screenshots accompany them — see §Evidence gaps.

---

## Verdicts, as supplied

| Leg | Subject (per ADR 023 §Owner Visual Acceptance) | Verdict | Class | Where the verdict comes from |
|---|---|---|---|---|
| **OV-1** | RU Models vocabulary and its rendered spelling | **no verdict recorded in this gate** | — | not judged in the 2026-10-27 instruction |
| **OV-2** | EN vocabulary | **no verdict recorded in this gate** | — | not judged in the 2026-10-27 instruction |
| **OV-3** | Perceived compactness without loss of readability (name, PID, uptime) | **FAIL** | `[O]` | Owner decision, 2026-10-27, quoted in §OV-3 |
| **OV-4** | Reachability of Start/Stop/Restart/Logs/Edit/Delete by mouse and touch, plus hover/focus visibility | **no verdict recorded in this gate** | — | not judged in the 2026-10-27 instruction |
| **OV-5** | Pipelines visibly unchanged | **no verdict recorded in this gate** | — | not judged in the 2026-10-27 instruction |
| **OV-6** | Mobile layout (no overflow, correct action wrap) | **OPEN** | `[O]` | Owner's instruction: the observations are not yet sufficient for a final verdict |
| **OV-7** | Reduced motion (no spin, no row transition) | **no verdict recorded in this gate** | — | not judged in the 2026-10-27 instruction |
| **OV-8** | Instances/History coherence | **OPEN** | `[O]` | Owner's instruction: the observations are not yet sufficient for a final verdict |

**No row above is an agent verdict, and no automated run is presented in place of any `[O]` observation.** The `[A]` legs of ADR 023 remain what they were — they are recorded where they were produced, not here, and they neither create nor overturn any verdict in this table.

**"No verdict recorded in this gate" is not a soft PASS and not a FAIL.** Those five legs remain unjudged obligations; OV-6 and OV-8 are additionally **OPEN by the Owner's explicit instruction** because what has been observed so far does not support a final verdict.

---

## OV-3 — FAIL: the Owner's decision and observations, verbatim

> Owner decision: OV-3 = FAIL.

The five observations supplied with that decision, verbatim:

> 1. On narrow screens, Models cards remain unnecessarily tall.
> 2. Card content and action buttons are separated by excessive empty space.
> 3. Long model names are truncated despite inefficient use of available space.
> 4. The orphan hint "Может выполняться вне GoAl" appears as a detached italic line.
> 5. Status, description and actions need a more coherent compact layout.

These are the Owner's own words and are the whole of the `[O]` evidence for OV-3. No agent measurement, screenshot, computed style or DOM inspection appears in this section, because none of those can originate a verdict; the separate geometry measurements taken in the Design Amendment Gate are design input, not acceptance evidence, and they do not amend, explain away or substitute for the Owner's observations.

---

## What this FAIL does to the contract

- **OV-3 is the `[O]` half of AC-5 and AC-7.** AC-5 ("Models rows are measurably more compact, with every action reachable and ≥ 44×44") and AC-7 ("ADR 022's contract holds…") each carry an `[A]` and an `[O]` component. The `[A]` components are untouched by this verdict — they tested what they tested, and they are not re-graded here. The `[O]` components are **not met**: the Owner perceived the cards as unnecessarily tall and the layout as incoherent, which is exactly what AC-5's Owner leg asks about and what no automated height assertion can decide.
- **A FAIL is not a closure.** The leg is recorded as FAIL on this artifact. It becomes closed only by a later Owner verdict on a corrected build, under a new instruction; this record does not foresee or pre-authorize that run.
- **The UX V-2.1 Design Amendment Gate (2026-10-27) is open because of this verdict.** That gate inspects the layout, measures alternatives and prepares a proposed ADR 023 amendment. **Nothing it proposes is approved or implemented here**, and no CSS, HTML, JavaScript or i18n byte has changed in it.
- **ADR 023 stays Proposed.** A `[O]` FAIL on the implementation cannot promote the design record, and no acceptance verdict of any kind promotes it.

---

## Evidence gaps in the acceptance kit (agent-verified file facts, not verdicts)

The kit directory (`E:\tmp\adr023-ov-acceptance`, outside the repository) was inspected when this record was written. What it contains:

| Item | State |
|---|---|
| `OWNER-ACCEPTANCE-REPORT.md` | **absent** — only the unfilled `OWNER-ACCEPTANCE-REPORT-TEMPLATE.md` exists |
| `shots/` | **empty** — no frames archived, therefore no hash manifest for frames |
| `data/goal_repo.json` | 2 887 B, modified 2026-10-27 15:02 (filesystem mtime, an upper bound on the write), with `goal_repo.json.bak` 3 044 B |
| `helper.log` / `helper.err.log` | 47 304 B / 0 B — the supervisor's log exists; it records process state, not what the Owner saw |

**Consequence, stated as a limitation rather than a defect:** this record carries one verdict and five observations. It carries **no per-run timestamps, no frames, no ordering and no window**. Per ADR 023 §Owner Visual Acceptance, a single Owner verdict supplied without per-run timestamps is recorded as one verdict and asserts no ordering or window — that is what happens here. If a later gate requires archived frames with a hash manifest for this leg, that requirement can only be met by a new Owner run; it cannot be reconstructed after the fact from the kit's files.

---

## What this record does NOT prove

- It does not prove OV-1, OV-2, OV-4, OV-5 or OV-7 passed, failed, or were even exercised.
- It does not prove OV-6 or OV-8 either way; both stay **OPEN** on the Owner's instruction.
- It does not assert which pages, widths, locales or reduced-motion settings the Owner used, or in what order — no run report enumerates them.
- It does not re-grade, discharge or weaken any `[A]` obligation of ADR 023, including the automated density, touch-target, reduced-motion and pipeline-isolation legs.
- It does not touch ADR 022's accepted contract, ADR 021's acceptance evidence, MA-06 / F-7, `ADR022-SKIP-01`, or any roadmap status.

---

## Related state at the time of writing

- **ADR 023:** **Proposed**. Implementation `95829e5e8aef94e7b0bffb3218afdf71f216c4ee` is published; acceptance is **incomplete** (one FAIL, two OPEN, five unjudged).
- **Design amendment:** a UX V-2.1 amendment is being prepared under the Design Amendment Gate; it is **not approved**, **not implemented**, and has not altered this contract (D-1 … D-11, AC-1 … AC-10) as committed.
- **Tracking:** [`ROADMAP.md`](../../ROADMAP.md) and [`BACKLOG.md`](../../BACKLOG.md) rows are **unchanged by this record**. Registering the amendment is deferred to the amendment's own gate, after Design Approval.
- **MA-06 / F-7:** OPEN, unchanged. **ADR 021** acceptance evidence: untouched. **ADR 022:** Accepted, unchanged.
- **This file:** an uncommitted working-tree record. It is evidence for the next gate, not a publication; no commit, push, tag or release has been made, and none is authorized by it.

---

## Dated pointer — UX V-2.1 design approval (2026-10-27, later the same day)

**This section adds a pointer only. It changes no verdict, closes no leg and re-grades nothing.**

- **The Owner approved the corrected design direction at the Design Approval gate:** **Layout 2 — progressive merge** plus **H-B — an accessible info control in the action strip**, with the orphan hint no longer rendered as a detached italic sentence. [ADR 023](../adr/023-model-card-density-and-status-vocabulary.md) now carries a dated **UX V-2.1** layer as its last section (amending D-5 / D-6 / D-7 / D-9 and the T-4′ / T-6 / T-7 / T-14 obligations plus AC-5 / AC-7), including the measured breakpoint decision **`min-width: 631px`** (620 px rejected; 640 pre-measured as the alternative).
- **Why the pointer is needed:** the bullet above, in §*Related state at the time of writing*, says the V-2.1 amendment was "being prepared … **not approved**". That sentence describes the state **before** the Design Approval instruction and is **superseded** by this section. It is left as written rather than rewritten, per the project's dated-layer convention.
- **Verdicts are untouched:** **OV-3 stays FAIL** (§OV-3 above, byte-for-byte), **OV-6 and OV-8 stay OPEN**, and **OV-1 / OV-2 / OV-4 / OV-5 / OV-7 keep no verdict**. Approving a design direction is not an acceptance verdict and does not convert any of these into PASS.
- **No new evidence is created here.** The geometry measurements behind the amendment are **design input** taken on a static fixture harness, not Owner observations; they are recorded in ADR 023 §UX V-2.1 (A-4, A-5, A-11) and are not acceptance evidence for any leg in this file.
- **ADR 023 stays Proposed**, MA-06 / F-7 stay OPEN, ADR 021's acceptance evidence is untouched, and ADR 022 remains Accepted and unchanged.
- **Re-judgement requires a new run:** AC-5 and AC-7 can only be re-judged by a new Owner observation on a binary carrying the V-2.1 bytes. No binary has been built for them, and none is authorized by this record.

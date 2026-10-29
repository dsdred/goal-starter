# ADR 023 — Owner Visual Acceptance: Record of Owner Evidence

**Purpose:** record the Owner's `[O]` verdicts on ADR 023 / UX V-2 exactly as supplied, and nothing else. **Every verdict in this file is the Owner's**, supplied in the gate instruction named below; the agent transcribes the evidence and derives no verdict of its own. A leg the Owner has not judged is recorded as *no verdict*, never as PASS and never as OPEN-by-inference. This record authorizes no code, no test, no CSS, HTML, JavaScript or i18n byte, no commit, no tag and no release.

**Source artifact the verdict was given against:** the Windows binary built at the Owner Visual Acceptance Preparation gate from `95829e5e8aef94e7b0bffb3218afdf71f216c4ee` (parent `648a0a0`; the published fast-forward HEAD — `git ls-remote origin refs/heads/main` returned the same SHA when this record was written), run from its own kit directory on loopback (its own config and data directory, never the Owner's long-running instance). Design contract: [ADR 023 — Model-card density and status vocabulary](../adr/023-model-card-density-and-status-vocabulary.md), status **Proposed**.

**Date of this record:** 2026-10-27. **Source of the verdicts:** the Owner's instruction at the ADR 023 / UX V-2.1 Design Amendment Gate (2026-10-27), quoted below. No Owner acceptance report and no screenshots accompany them — see §Evidence gaps. *(2026-10-29: the "no screenshots" clause is a statement about that 2026-10-27 gate only and is superseded as a description of Owner evidence — the Owner supplied screenshots in the ChatGPT conversation on 2026-10-29, recorded in §Correction (2026-10-29, Gate K) below.)*

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

---

## Dated verdict — OV-3 accepted (2026-10-28, Owner UX Decision Reconciliation gate)

**This section records an Owner verdict; it rewrites nothing above it.** The verdict table at the top of this file and §*OV-3 — FAIL* stay byte-for-byte as the 2026-10-27 state, and are **superseded for the current state** by this section, per the dated-layer convention this file already uses. As everywhere in this file, every verdict below is the Owner's, supplied in the gate instruction named here; the agent transcribes it and derives no verdict of its own.

**Source of the verdict:** the Owner's instruction at the ADR 023 / UX V-2.1 / Owner UX Decision Reconciliation gate (2026-10-28), quoted verbatim:

> 1. Card compactness is acceptable. OV-3: Owner accepts the visual layout, subject to evidence registration.
> 2. The orphan info icon's hover behavior is preferred over mouse click-toggle.
> 3. Mouse hover: show tooltip on pointer enter, hide on pointer leave.
> 4. Keyboard: show tooltip on focus, hide on blur.
> 5. Touch: preserve access to the tooltip through tap interaction.
> 6. Preserve the current visual appearance.

Items 2–5 are **interaction requirements**, not observations; they are recorded as a dated layer in [ADR 023](../adr/023-model-card-density-and-status-vocabulary.md) §*Tooltip-interaction reconciliation (2026-10-28)* and are **not** an acceptance verdict for any leg in this file. Item 6 is a constraint on that future implementation.

| Leg | Subject (per ADR 023 §Owner Visual Acceptance) | Verdict | Class |
|---|---|---|---|
| **OV-3** | Perceived compactness without loss of readability (name, PID, uptime) | **PASS (accepted)** — "Card compactness is acceptable. OV-3: Owner accepts the visual layout" | `[O]` |

### Evidence registration, as instructed

Decision 1 makes the verdict "subject to evidence registration"; this subsection is that registration, and it records what exists rather than what would be desirable.

- **Supplied with the verdict: nothing.** No Owner acceptance report, no screenshots, no per-run timestamps, no stated browser, no stated viewport list, no ordering and no observation window. Per ADR 023 §Owner Visual Acceptance, a single Owner verdict supplied without per-run timestamps is recorded as **one verdict** and asserts no ordering or window — that is what this section does. *(2026-10-29: the screenshots clause describes this 2026-10-28 verdict and is superseded as a description of Owner evidence — the Owner supplied screenshots in the ChatGPT conversation on 2026-10-29; see §Correction (2026-10-29, Gate K).)*
- **Artifact: not named by the Owner.** The instruction says the Owner "reviewed the new Models layout" but does not identify the binary, the URL, the browser or the date of the observation. The only V-2.1 build prepared for Owner review under this contract is the isolated acceptance kit described next, but **this record does not assert that the Owner used it** — that step is not evidenced by anything the Owner supplied.
- **Kit file facts** (`E:\tmp\adr023-v21-ov-acceptance`, outside the repository; inspected read-only on 2026-10-28 00:58 +05:00). These are file facts, not observations, and they prove a run happened — not what was seen, by whom, or in what order:

| Item | State at inspection |
|---|---|
| `bin/goal.exe` | 11 049 472 B, sha256 `b2efb63137b38337315d5eb684b40c8add67c0b24662b1d5efb845200be05eff` (built from published `26523d19…` plus the uncommitted V-2.1 bytes; provenance in the kit's `PROVENANCE.md`) |
| `data/goal_repo.json` | 4 105 B, mtime 2026-10-28 00:34:27 (the start script's seed write) |
| `helper.log` | 38 960 B, last write 2026-10-28 00:50:49 (`helper.err.log` 0 B) |
| `shots/owner/` | **empty** — no frames, therefore no hash manifest for frames |
| `OWNER-ACCEPTANCE-REPORT.md` | **absent** — only the unfilled `OWNER-ACCEPTANCE-REPORT-TEMPLATE.md` |
| processes / port | no `goal.exe` under the kit path and nothing listening on `18024` at inspection |

- **Consequence, stated as a limitation rather than a defect:** the acceptance is recorded against *the layout the Owner reviewed*, which the record can describe but cannot identify by bytes or by run. If a later gate needs frames with a hash manifest for this leg, only a new Owner run can supply them; this record cannot manufacture them after the fact.

### What this verdict covers, and what it does not

- **Covers:** perceived compactness and the visual layout of the Models cards, in the sense OV-3 defines — no loss of readability of name, PID or uptime.
- **Does not cover, and is not evidence for:** any interaction behaviour. **No mouse, keyboard or touch check is claimed to have been performed**, and items 2–5 create obligations for a future implementation rather than observations of the current one. The existing interaction legs in `tests/browser/orphan.cjs` are `[A]` evidence on the current bytes and are not Owner evidence for anything here.
- **Other legs untouched:** OV-6 and OV-8 stay **OPEN**; OV-1, OV-2, OV-4, OV-5 and OV-7 keep **no verdict**. OV-4 in particular remains unjudged: the Owner's hover/focus decisions describe what the UI must do, not what was observed.
- **Effect on the acceptance criteria, using the ADR's own mapping and deriving nothing further:** OV-3 is the `[O]` component of both **AC-5** and **AC-7**. AC-7's `[O]` component is therefore **accepted to the extent of this verdict**. AC-5's `[O]` component is shared with **OV-4**, which still has no verdict, so **AC-5's `[O]` half is not fully discharged**. No `[A]` leg is re-graded and no `[A]` evidence is offered here.
- **Scope of the acceptance:** it holds for the layout the Owner observed. It does not transfer to bytes the Owner did not see, and it does not accept the not-yet-implemented interaction change of ADR 023's 2026-10-28 layer.

### Supersessions this section makes explicit

- The 2026-10-27 pointer's last bullet — "No binary has been built for them, and none is authorized by this record" — is **superseded**: a V-2.1 binary was afterwards prepared for Owner review as the isolated kit above, and the Owner has returned an OV-3 verdict. The sentence is kept as written.
- §*What this FAIL does to the contract* remains the description of the 2026-10-27 state; its "not met" reading of the `[O]` halves is superseded only to the extent stated above.
- **ADR 023 stays Proposed.** An OV-3 acceptance neither ratifies the ADR nor closes the V-2.1 implementation, whose interaction change is not implemented and not authorized. *(2026-10-28, Pre-Commit Reconciliation Gate: the trailing clause describes the state at the moment of this verdict and is **superseded** for the current state — the Owner afterwards authorized the C-4 interaction change («Разрешаю реализацию изменения поведения подсказки.») and it is implemented in the working tree at the C-4 Tooltip Implementation Gate. The sentence is kept as written. Nothing in that authorization changes this record's verdicts: OV-3 stays **accepted [O]**, OV-6 and OV-8 stay **OPEN**, the other five legs keep no verdict, OV-4 remains unjudged, and **ADR 023 stays Proposed**.)*
- Nothing here authorizes code, a commit, a push, a tag or a release. MA-06 / F-7 and `ADR022-SKIP-01` stay OPEN; ADR 021's acceptance evidence is untouched; ADR 022 remains Accepted and unchanged; the Owner's running instance was not contacted.

---

## Dated reconciliation — Owner verdicts and the Pipelines scope ruling (2026-10-29, ADR 023 / Owner Acceptance Reconciliation gate)

**This section records Owner verdicts, a scope ruling and the acceptance consequences of both; it rewrites nothing above it.** The verdict table at the top, §*OV-3 — FAIL*, the 2026-10-27 design pointer and the 2026-10-28 OV-3 acceptance stay byte-for-byte and are **superseded for the current state** only where this section says so, per the dated-layer convention this file uses. Every verdict below is the Owner's, supplied in the gate instruction quoted here; the agent transcribes it and derives no verdict of its own. The blockers and criterion effects further down are the agent's mapping of those Owner-supplied verdicts onto the ADR's own register — they are analysis, never verdicts.

**Source of the verdicts:** the Owner's instruction at the ADR 023 / Owner Acceptance Reconciliation gate (2026-10-29), quoted verbatim:

> 1. ADR 023 / Models:
>    - Compact layout accepted (OV-3 PASS).
>    - Mouse hover/leave PASS.
>    - Mouse click inert PASS.
>    - Keyboard focus/blur PASS.
>    - Mobile layout checks PASS.
>    - Real touch NOT OBSERVED.
>
> 2. Pipelines list:
>    Owner reports excessive card height
>    and large unused vertical space.
>
>    Screenshot: narrow viewport,
>    Acceptance Pipeline card.
>
>    The title and status badges are
>    near the top; action buttons are
>    separated by excessive whitespace.
>
>    Owner does NOT accept this layout
>    as the desired final UX.
>
> 3. Pipeline editor:
>    Owner previously reported
>    excessively tall component blocks
>    and unnecessary whitespace.

The rest of that instruction is task text (compare the evidence against OV-1 … OV-8, record only supported verdicts, register two pipeline UX requirements, assess Commit Gate eligibility). It is executed in this section and in the gate's two reports; it is not itself acceptance evidence and is not restated as such.

### Verdicts, as supplied

| Leg | Subject (per ADR 023 §Owner Visual Acceptance) | Verdict | Class | Where the verdict comes from |
|---|---|---|---|---|
| **OV-1** | RU Models vocabulary and its rendered spelling | **no verdict in this gate** | — | not addressed by the 2026-10-29 instruction |
| **OV-2** | EN vocabulary | **no verdict in this gate** | — | not addressed by the 2026-10-29 instruction |
| **OV-3** | Perceived compactness without loss of readability (name, PID, uptime) | **PASS (accepted) — carried forward** | `[O]` | 2026-10-28 dated verdict above; restated by the Owner at this gate as "Compact layout accepted (OV-3 PASS)". Not re-judged here. |
| **OV-4** | Reachability of Start/Stop/Restart/Logs/Edit/Delete by mouse and touch, plus hover/focus visibility | **no verdict on the leg as a whole; real touch NOT OBSERVED** — see §OV-4 | `[O]` | part of the leg observed, quoted in §OV-4; the rest not reported |
| **OV-5** | Pipelines visibly unchanged | **no verdict in this gate** — see §OV-5 | — | the Pipelines report is a design objection, not a verdict on this leg |
| **OV-6** | Mobile layout (no overflow, correct action wrap) | **PASS** | `[O]` | Owner: "Mobile layout checks PASS." |
| **OV-7** | Reduced motion (no spin, no row transition) | **no verdict in this gate** | — | not addressed by the 2026-10-29 instruction |
| **OV-8** | Instances/History coherence | **OPEN — unchanged** | `[O]` | 2026-10-27 instruction; not addressed by the 2026-10-29 instruction |

**No row above is an agent verdict.** "No verdict in this gate" is not a soft PASS and not a FAIL; OV-3 is a carried-forward Owner acceptance, not a re-judgement.

### OV-4 — what the Owner observed, and what stayed unobserved

| Observation the Owner supplied | Result | Class |
|---|---|---|
| Mouse hover / leave — pointer enter shows the hint, pointer leave hides it | **PASS** | `[O]` |
| Mouse click — does not toggle the hint | **PASS** ("inert") | `[O]` |
| Keyboard focus / blur — focus shows the hint, blur hides it | **PASS** | `[O]` |
| Real touch — first tap shows, second tap on the same control hides | **NOT OBSERVED** | — |

- **What these four rows are.** They are recorded as the Owner's observations of the orphan info control's tooltip interaction; the instruction does not name the control, but it lists exactly the interactions the 2026-10-28 decisions define (items 2–5), and the mapping follows that sequence. They are recorded because the Owner reported them; they are not re-derived from any agent run, and the maintained legs in `tests/browser/orphan.cjs` remain `[A]` evidence that this record does not re-grade or substitute.
- **Why OV-4 carries no leg verdict.** OV-4's definition couples the hover/focus observations with **reachability of Start/Stop/Restart/Logs/Edit/Delete by mouse and touch**. The instruction does not report those six actions being exercised, and real touch is **NOT OBSERVED**. The compound leg is therefore **not discharged**; recording a PASS for it would convert four observations into a verdict the Owner did not give, and recording a FAIL would invent a defect the Owner did not report.
- **Consequence for AC-5.** AC-5's `[O]` half is shared by OV-3 (accepted) and OV-4 (not discharged), so it remains **not fully discharged**.

### OV-5 and the Pipelines report — no verdict, and the scope ruling

- **What OV-5 asks.** Whether Pipelines are *visibly unchanged* by the work ADR 023 contracts. It is a regression question about this contract, not a judgement about whether the Pipelines layout is good.
- **What the Owner reported.** Excessive card height, large unused vertical space, title and status badges near the top, action buttons separated by excessive whitespace, and that this is **not accepted as the desired final UX** — shown in the screenshots the Owner supplied in the conversation (narrow viewport, GoAl Pipelines page at `127.0.0.1:18025`; see §*Evidence registration for this section*). That is a design objection to the Pipelines layout itself. The instruction neither says that UX V-2/V-2.1 changed Pipelines nor that it did not, so **no verdict is recorded on OV-5** — neither PASS nor FAIL — and AC-4's `[O]` half stays open.
- **Scope ruling: Pipeline List Card Density is outside ADR 023.** The ADR's own bytes decide this, not an interpretation of this gate: its scope sentence says "**Pipelines not affected**"; **D-4** freezes pipeline chips and card shape byte-for-byte; §*Out of scope* lists "Pipelines density, pipeline semantics … no change"; and §A-6 measured Pipelines **shape-identical** at all thirteen swept widths. A Pipelines-density requirement therefore cannot be discharged, failed or remedied inside this contract, and it is not recorded here as an ADR 023 acceptance failure.
- **"Pipelines unchanged" is not "Pipelines design accepted".** Even a PASS on OV-5 would say only that UX V-2/V-2.1 did not alter Pipelines; it would say nothing about whether that layout is the desired UX. The Owner's report is consistent with both readings of history: the objection is to the layout as it was and is, not to a change this contract made.
- **Pipeline editor density is outside ADR 023 for the same reason.** ADR 023 contracts no editor rule, names no editor selector and measures no editor geometry. The Owner's statement that this was "previously reported" is recorded as the Owner's statement: the earlier report itself is not in this file, not in the acceptance kit, and not otherwise archived in the repository.
- **Where the two requirements live.** They are registered as **two separate class D rows** in [`BACKLOG.md`](../../BACKLOG.md): **UX-PIPELINE-LIST-DENSITY** and **UX-PIPELINE-EDITOR-DENSITY**. Registration is neither implementation nor authorization; neither row amends ADR 023's scope, and neither may be discharged by editing the shared `.model-row` / `.model-list` rules under ADR 023's authority (Pipelines use the same card pattern — `internal/webui/static/style.css:572-574` — and both lists carry `class="model-list"` at `internal/webui/templates/index.html:317` and `:334`).

### OV-6 — what the PASS covers

"Mobile layout checks PASS" is recorded as a verdict on OV-6 **as the ADR defines it**: no horizontal overflow, no clipped title/PID/uptime text, and correct action wrap at 768/430/375. The Owner supplied it under the heading "ADR 023 / Models", which is the surface these legs contract; ADR 023 does not contract Pipelines. It is **not** a verdict on Pipelines density: the Owner reported neither overflow nor a wrap failure there, and OV-6 does not ask about whitespace.

### What this does to the acceptance criteria

Using the ADR's own mapping and deriving nothing further:

| Criterion | `[O]` component | State after this gate |
|---|---|---|
| **AC-1** | OV-1 | not discharged — no verdict |
| **AC-2** | OV-2 | not discharged — no verdict |
| **AC-4** | OV-5 | not discharged — no verdict; the Pipelines-density objection is **not** an AC-4 failure |
| **AC-5** | OV-3 + OV-4 | OV-3 accepted; OV-4 not discharged ⇒ **not fully discharged** |
| **AC-6** | OV-6 | **accepted to the extent of the OV-6 PASS** recorded here |
| **AC-7** | OV-3 | accepted to the extent of the 2026-10-28 verdict (unchanged) |
| **AC-8** | OV-7 | not discharged — no verdict |
| **AC-9** | OV-8 | not discharged — OV-8 remains OPEN |

No `[A]` leg is re-graded and no `[A]` evidence is offered here.

### Acceptance blockers, stated as blockers

1. **OV-4 remains partially unobserved.** Real touch is NOT OBSERVED and the six lifecycle/edit actions were not reported as exercised, so AC-5's `[O]` half cannot close on the four interaction observations above.
2. **OV-1 and OV-2 have no verdict.** AC-1 and AC-2 stay open.
3. **OV-5 has no verdict.** AC-4 stays open. The Pipelines-density objection is **not** one of these blockers: it is outside this contract.
4. **OV-7 has no verdict.** AC-8 stays open.
5. **OV-8 remains OPEN** on the 2026-10-27 instruction. AC-9 stays open.
6. **ADR 023 stays Proposed.** No verdict in this file promotes it, and the acceptance set is incomplete.

Recorded so they are not counted twice: OV-3 is accepted and is not a blocker; OV-6 is a PASS and does not waive the unjudged legs; the two pipeline UX requirements are class D product work registered in `BACKLOG.md`, not defects in this contract's delta.

### Evidence registration for this section

- **Supplied with the instruction: the Owner's screenshots, in the ChatGPT conversation.** The Owner states that they visibly show the Chrome browser, the address `127.0.0.1:18025`, the GoAl Pipelines page, a narrow viewport and the pipeline card with excessive vertical whitespace. They therefore establish that the Owner opened the GoAl interface at `127.0.0.1:18025`, which is consistent with the Gate I acceptance kit's configuration — that kit is the instance bound to port 18025 (`E:\tmp\adr023-v21-c4-ov-acceptance`). The address is recorded as established and is not to be asked for again. No filename, timestamp, viewport dimension or interaction evidence is inferred from the images.
- **What the screenshots do not establish.** They do not independently establish the binary's SHA256 or the exact source bytes the session served, so this record describes the artifact only through the kit prepared for that port and claims no byte-level provenance from the images.
- **No screenshot files were copied into the kit.** `E:\tmp\adr023-v21-c4-ov-acceptance\shots\owner` contains none, and there is no hash manifest for them; that is a statement about the kit's files, not about what the Owner supplied.
- **No `OWNER-ACCEPTANCE-REPORT.md` exists** in that kit — only the unfilled template — so nothing here comes from a written Owner report: the verdicts are the Owner's instruction and the Pipelines images are conversation evidence.
- **No per-run timestamps, viewport widths, ordering or observation window are asserted.** Per ADR 023 §Owner Visual Acceptance this is recorded as **one verdict set**, asserting no ordering and no window; the browser is named only as the Owner reports it.
- **Artifact described, not proven.** The only V-2.1 + C-4 kit prepared under this contract is `E:\tmp\adr023-v21-c4-ov-acceptance` (binary `bin\goal.exe`, 11 045 888 B, sha256 `dfdb4db7b0843a390c7ff4059291a4d74ce1b4e14f02b78f1261bfebe49f79fa`, built 2026-10-28T18:51:00Z from published `26523d19c005bacdeab269c9b5a5419e4f727e9d` plus the uncommitted Gate H bytes). The screenshots are consistent with this kit's configuration; **this record does not claim that they prove these bytes were served**.
- **Scope of these verdicts:** they hold for what the Owner observed. They do not transfer to bytes the Owner did not see, and they authorize no code, commit, push, tag or release.

### Supersessions this section makes explicit

- The 2026-10-28 section's bullet "**Other legs untouched:** OV-6 and OV-8 stay **OPEN**" — and the same clause in the 2026-10-27 pointer and in the 2026-10-28 supersession note — is **superseded for OV-6 only**: the Owner's 2026-10-29 instruction records "Mobile layout checks PASS", so OV-6 is **PASS**. **OV-8 stays OPEN**, unchanged.
- The 2026-10-28 section's bullet "**Does not cover, and is not evidence for:** any interaction behaviour. **No mouse, keyboard or touch check is claimed to have been performed**" describes the state at that verdict and is **superseded for three of its four clauses**: mouse hover/leave, mouse-click-inert and keyboard focus/blur are now Owner-reported **PASS** (quoted in §OV-4). Real touch remains **NOT OBSERVED**, so the touch clause is not superseded.
- The 2026-10-28 statement that "**OV-4 remains unjudged**" is **not superseded as a leg verdict**: no verdict on OV-4 as a whole has been given; what is new is the four-row partial observation record in §OV-4.
- Everything else above — including §*OV-3 — FAIL*, the 2026-10-28 evidence registration and the 2026-10-28 "ADR 023 stays Proposed" clause — is unchanged. **ADR 023 stays Proposed.**

### What this section does not do

- It does not close a leg for which no verdict is quoted, does not convert an unjudged leg into a soft PASS, and does not re-open OV-3.
- It does not accept the Pipelines layout, does not record it as an ADR 023 defect, and does not alter ADR 023's scope (D-1 … D-11, AC-1 … AC-10, T-1 … T-14, the §Owner Visual Acceptance definitions) by a single word.
- It does not implement either registered requirement: no CSS, HTML, JavaScript, i18n, Go, test or data byte is written by this section.
- It does not touch ADR 021's acceptance evidence, ADR 022's accepted contract, MA-06 / F-7, `ADR022-SKIP-01`, or any ROADMAP/BACKLOG status beyond the two registrations named above.
- It does not build, run, measure or contact any instance, including the Owner's running instances.

### Correction (2026-10-29, Gate K) — evidence availability

- **What was corrected.** The §*Evidence registration for this section* bullets originally read as though the Owner had supplied no screenshots, an inference drawn from the empty `shots\owner` folder in the kit. That inference was wrong, and the bullets have been corrected in place rather than left standing: the Owner supplied screenshots in the ChatGPT conversation on 2026-10-29.
- **What the record now states, and nothing more.** The screenshots show the Chrome browser at `127.0.0.1:18025`, the GoAl Pipelines page, a narrow viewport and the pipeline card with excessive vertical whitespace; they establish that the Owner opened the interface at `127.0.0.1:18025`, which is consistent with the Gate I kit's configuration; no screenshot files were copied into `shots/owner`; the images do not independently establish the binary's SHA256 or the exact source bytes; and no filename, timestamp, viewport dimension or interaction evidence is derived from them.
- **Nothing else changes.** OV-3 **PASS `[O]`**, OV-6 **PASS `[O]`**, the three tooltip-interaction PASSes and **real touch NOT OBSERVED** stand exactly as recorded; OV-1 / OV-2 / OV-5 / OV-7 keep no verdict; OV-8 stays **OPEN**; the scope ruling and the two `BACKLOG.md` registrations are unchanged; **ADR 023 stays Proposed**.
- **Why the kit folder is still empty.** The record requires no file from the Owner: the conversation screenshots are the evidence, and the kit folder's emptiness is a file fact about the kit only. The Owner is not asked to prove the address again.

## Touch-acceptance deferral (2026-10-29 — Owner decision, ratified with two clarifications)

- **Owner decision (verbatim).** "Real touchscreen testing is only possible AFTER the release. This is an environment constraint, not a PASS or FAIL verdict." The Owner approved the deferral with two clarifications — mandatory scope of the post-release obligation, and evidence sequencing — contracted in ADR 023 §Touch-acceptance deferral (X-1 … X-8).

- **OV-4 real touch: NOT OBSERVED.** No PASS, no FAIL and no SKIP is recorded, inferred or implied. The leg's other clauses stand exactly as recorded: mouse hover/leave **PASS `[O]`**, mouse click inert **PASS `[O]`**, keyboard focus/blur **PASS `[O]`**; the six-action **mouse** reachability was not reported and remains outstanding.

- **AC-5's `[O]` half:** partially discharged (OV-3 accepted; OV-4 real touch deferred), **not closed**. OV-3 **PASS** and OV-6 **PASS** are unchanged and are not re-requested.

- **Evidence sequencing (Owner clarification 2).** Before the Commit Gate, the maintained touch legs must PASS on the **final working-tree bytes**; a committed SHA is not required, because none exists until the Owner authorizes the commit. After publication, the **exact-SHA CI** run must PASS including **Browser Acceptance** and the **Linux `-race`** job. The legs are `orphan.cjs` `T-4′ RU touch` and `T-4′ RU keyboard` (S-3), plus `responsive.cjs` `T-6` and `T-14`.

- **Automated touch evidence is `[A]` and is not the verdict.** On the Gate H bytes the recorded local Windows run shows 987 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |` with `orphan.cjs` 64 and `responsive.cjs` 156 (`docs/DEVELOPMENT.md:112`). That is automated evidence on those bytes, not CI evidence for a future commit, and it substitutes for no `[O]` leg. Exactly one leg exercises touch as a *modality* (RU); there is no EN touch-modality leg, and the geometry legs measure target size, not touch behaviour.

- **`OWNER-TOUCH-01` (tracked in `BACKLOG.md`) — mandatory scope (Owner clarification 1):** real-device first-tap reveal / second-tap hide on the Models orphan info control; tooltip readability and viewport containment; Start/Stop/Restart/Logs/Edit/Delete reachability and functionality by touch; RU and EN coverage of those clauses. **Diagnostic only, not closure blockers:** the 3000 ms list rebuild (ADR 022 F-3), the transition indicator and reduced motion may be recorded if observed, but they require a separate Owner authorization to become closure obligations.

- **Supersession, narrowly stated.** The 2026-10-29 reconciliation section's blocker 1 ("OV-4 remains partially unobserved … so AC-5's `[O]` half cannot close") is **not** superseded as a *verdict* — nothing here observes real touch. It is superseded only in its **gate-eligibility** reading, and only once ADR 023 X-3's pre-commit legs are green and X-4 is satisfied.

- **What this section does not do.** It originates no verdict, closes no leg, promotes no status (**ADR 023 stays Proposed**), does not alter OV-3, OV-6, the 2026-10-27 / 2026-10-28 verdicts or the 2026-10-29 reconciliation layer, and authorizes no commit, publication, tag or release.

## Reduced-motion acceptance simplification (2026-10-29 — Owner decision)

- **Owner decision.** Manual OV-7 acceptance is not considered necessary for this release (amendment Y-1 … Y-5, ADR 023 §Reduced-motion acceptance simplification).

- **OV-7 manual: NOT OBSERVED.** No PASS, no FAIL and no SKIP is recorded, inferred or implied. The leg is removed from the mandatory Owner set and no longer blocks the Commit Gate, the Publication Gate or a release.

- **AC-8 is verified by its automated component only.** `T-10` in `tests/browser/responsive.cjs` asserts, under a `reducedMotion: 'reduce'` context, the Models row `transition-duration: 0s` and the ring `animation-name: none`, with a non-reduced negative control proving the probe is sensitive. D-8 and the `prefers-reduced-motion: reduce` CSS block remain product requirements; weakening `T-10` requires its own Owner-approved contract change.

- **Supersession, narrowly stated.** The "no verdict" wording for OV-7 in the 2026-10-27 register, the 2026-10-29 verdict register and the AC-8 row (`not discharged — no verdict`) is superseded only as to **OV-7's mandatory status**: the leg carries NOT OBSERVED and no longer gates. Nothing else in those sections is re-judged.

- **What this section does not do.** It originates no other verdict, closes no leg, promotes no status (**ADR 023 stays Proposed**), does not alter OV-3 **PASS**, OV-6 **PASS**, OV-4 real touch **NOT OBSERVED**, the three tooltip-interaction PASSes or `OWNER-TOUCH-01`, and authorizes no commit, publication, tag or release.

## Final Owner Acceptance Reconciliation (2026-10-29 — existing Owner evidence, no new verdict)

- **Owner evidence supplied.** The Owner supplied screenshots of three surfaces — Pipelines, Instances and Models — and reported: Alpha Running → **STOPPED**; Beta Running → **RUNNING**; Gamma Stopped → **RUNNING**; Delta Stopped → **STOPPED**; Orphan Test Model → **UNMANAGED**; the Pipelines page holds a pipeline with Alpha Running and Orphan Test Model, and its aggregate badge reads «Вне GoAl», which the Owner states is consistent with the observed model states. The pipeline title truncation the Owner observed remains a UX finding: it is registered under `UX-PIPELINE-LIST-DENSITY` in `BACKLOG.md` and is **not** treated as resolved here.

- **Provenance of the frames — recorded, not challenged.** The screenshots were supplied in the ChatGPT conversation, **not** in `E:\tmp\adr023-v21-c4-ov-acceptance\shots\owner` (that directory is empty), and no hash manifest accompanies them. They are **not** absent and this record does not dispute their existence; what the repository can state is only that they are not archived in the repository or in the kit, so no byte-level, timestamp or ordering claim about them can be made here. The observations are recorded from the Owner’s narration in this gate.

- **What the evidence establishes (`[O]`).** The Models surface rendered five rows carrying the five state semantics the Kit fixture seeds; the Pipelines surface rendered a pipeline holding Alpha Running and Orphan Test Model with the aggregate «Вне GoAl»; an Instances frame was supplied. Those exact row names are the Kit fixture’s own (`start-acceptance.ps1`, models `model_c4_alpha` / `model_c4_long`) and no other repository file defines them, so the frames show the Gate I kit. No PASS is inferred for any leg from these facts alone.

- **What the evidence does not establish.** (1) The rendered RU spelling of the state words and of the action labels — the Owner’s message transcribes states in English, and the only verbatim rendered string is the RU aggregate «Вне GoAl» on Pipelines, which is not the Models surface. (2) The language of the Models and Instances frames. (3) Which actions, if any, were used. (4) The contents of the Instances frame and of History. (5) Any comparison «Pipelines visibly unchanged». (6) Session ordering or timings. (7) The identity of the binary and of the frame bytes, since no manifest exists.

- **`[C]` corroboration of the aggregate.** `pipelineAggregateStatus` (`internal/webui/static/app.js:726-737`) projects running → starting → stopping → pending → failed → **orphan** → stopped. A pipeline whose entries are one `stopped` model and one `orphan` model therefore renders the `orphan` aggregate, i.e. «Вне GoAl». The Owner’s consistency statement is corroborated by the shipped rule; this is code-derived evidence, not an `[O]` verdict and not a substitute for one.

- **Discrepancy recorded, not resolved.** The Kit fixture seeds `alpha=running`, `long=running`, `gamma=stopped`, `delta=stopped`, `orphan=orphan` (`start-acceptance.ps1`). The reported frames show Alpha **STOPPED** and Gamma **RUNNING** — the reverse of the seeded states. That is compatible with the Owner having pressed Stop on Alpha and Start on Gamma by mouse, with a second `start-acceptance.ps1` run in the same session (which re-seeds and restarts the fixture), or with another cause. The frames are not available to this gate, so **no causation, ordering or action usage is asserted**, and the divergence is **not** evidence for OV-4.

- **Fixture coverage — OV-8’s `pending` component is NOT covered by the Kit.** The fixture contains no model or instance in `pending`, `starting` or `stopping`: five models whose runtime arguments are all `infinite`, and a single seeded instance (`inst_c4_orphan`, `running`). The maintained harness states why — the `pending` state is sub-frame and “the app cannot render on demand” (`tests/browser/orphan.cjs:73`) — so T-4/D-3 covers it by injecting a badge into the real containers (`#adv-instances-body` `orphan.cjs:516`; `#instances-compact` `:691`, `:706`; Models row `:530`; pipeline aggregate `:551`) and History’s non-exempt uppercase by `:745`. Consequence: the `[O]` half of OV-8 can cover only the states the Kit renders (running, stopped, orphan); the `pending` word on Instances and History is **`[A]`-only** for this release. No fixture state is invented, no test result is claimed, and changing the fixture would be implementation work requiring its own gate.

- **Per-leg status after this gate.** **OV-1** — evidence recorded for five rendered states; the rendered RU spelling and the action labels are still missing. **OV-2** — no EN evidence; missing entirely. **OV-4** — no recorded mouse exercise of the six actions; missing. **OV-5** — a Pipelines frame and its aggregate are recorded; the “visibly unchanged” statement is missing, and the truncation finding is registered separately. **OV-8** — an Instances frame was supplied but its content is not described; the coherence statement and the History check are missing, and the `pending` component is out of the Kit’s reach. **No leg is converted to PASS, FAIL or SKIP by this section.**

- **What this section does not do.** It originates no verdict, closes no leg, promotes no status (**ADR 023 stays Proposed**), does not reopen OV-7’s retirement or the touch deferral, does not alter OV-3 **PASS**, OV-6 **PASS**, OV-4 real touch **NOT OBSERVED** or `OWNER-TOUCH-01`, and authorizes no commit, publication, tag or release.

## Final Owner acceptance verdicts (2026-10-29 — Owner decision)

- **Owner verdicts (`[O]`).** The Owner confirms **PASS** for **OV-1** (RU interface), **OV-2** (EN interface), **OV-4** (six mouse actions Start / Stop / Restart / Logs / Edit / Delete), **OV-5** (Pipelines layout and behavior) and **OV-8** (Instances and History). With **OV-3 PASS** (2026-10-28) and **OV-6 PASS** (2026-10-27) recorded earlier, every mandatory Owner leg now carries a verdict. Each leg is recorded as one Owner verdict; this gate carries no per-run timestamps, so no ordering and no `[L,U]` interval is asserted for any leg (§Owner Visual Acceptance rule).

- **Evidence channel.** These five verdicts were supplied as the Owner's statements in this gate's conversation. No new frames were attached to them and none are claimed here; the 2026-10-29 screenshots remain in the ChatGPT conversation rather than in the kit's `shots\owner` (no hash manifest), as the reconciliation section above records. Each PASS is therefore attributed to the Owner's statement, not to archived imagery, and no byte, timestamp or ordering claim is made about any frame.

- **Scope of each verdict.** **OV-1 / OV-2** accept the state vocabulary and action labels as the Owner observed them on the Models surface; the rendered strings are confirmed by the verdict and are not transcribed in this gate. **OV-4** accepts mouse reachability of the six actions, on top of the three tooltip clauses accepted 2026-10-28; real touch is **not** part of this PASS. **OV-5** accepts that Pipelines layout and behavior are unchanged — it is not an acceptance of the Pipelines design. **OV-8** accepts Instances/History coherence under the Owner-approved scope in which the `pending` word rests on `[A]` coverage alone (the Kit cannot render the state; `tests/browser/orphan.cjs:73`).

- **Preserved dispositions.** **OV-7** stays **NOT OBSERVED** with the manual leg retired (Y-1 … Y-5, AC-8 `[A]`-only). **OV-4 real touch** stays **NOT OBSERVED** and **`OWNER-TOUCH-01`** stays OPEN (X-1 … X-8); no PASS is inferred for either from this gate. **OV-8's `pending`** component stays `[A]`-only.

- **Criteria discharged to the extent observed.** AC-1 (OV-1), AC-2 (OV-2), AC-4 (OV-5), AC-5 (`[O]` half: OV-3 accepted + OV-4 non-touch PASS, real touch deferred) and AC-9 (OV-8, with the `pending` scope); AC-6 and AC-7 were already discharged by OV-6 and OV-3; AC-8 is `[A]`-only by Y-1.

- **Acceptance is not ratification.** The Owner has not ratified ADR 023 in this gate. It stays **Proposed**; promotion is a separate explicit Owner decision (X-6 closure criteria), and commit, publication, exact-SHA CI and release remain separate authorizations. Nothing here claims a commit, a SHA, CI or a tag.

- **The `BACKLOG.md` rows are not closed by these PASSes.** `UX-PIPELINE-LIST-DENSITY` (including the registered title-truncation symptom), `UX-PIPELINE-EDITOR-DENSITY` and `OWNER-TOUCH-01` all stay open with their checkboxes unchecked. "Pipelines unchanged by ADR 023" must never be read as "Pipelines design accepted".

- **Supersession, narrowly stated.** The "Per-leg status after this gate" bullet of the 2026-10-29 reconciliation section above is superseded **as to the five legs' status only**; its evidence limits, provenance record and fixture-coverage finding stand unchanged.

- **What this section does not do.** It originates no automated verdict, re-grades no `[A]` leg, changes no criterion or CSS/JS/HTML/i18n/test byte, contacts no Owner instance, and authorizes no commit, publication, tag or release.

## Ratification (2026-10-29 — Owner decision)

- **Owner decision.** ADR 023 is ratified **Accepted**, and Owner Acceptance is complete. This section records that decision against the same evidence chain the section above records, and originates no observation of its own.

- **Evidence reuse.** The Gate H corpus — **987 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |`**, the final-bytes run recorded at `docs/DEVELOPMENT.md:112` — is accepted as the evidence for the final bytes on the Owner's two conditions, and both hold. Identical: the four UI/test files' current SHA-256 equals the value the kit recorded for the bytes that run executed — `app.js` `a36379b2…`, `style.css` `43591595…`, `orphan.cjs` `82b6ea28…`, `responsive.cjs` `268aad27…` (full values in ADR 023 §Ratification R-2). Permitted: X-3 requires those legs on the final working-tree bytes before the Commit Gate and defers the CI evidence to the exact-SHA run after publication. No browser corpus re-run is owed for documentation-only changes, so none was performed here and none is missing; the corpus result stands as recorded, and no run is inherited by a later one.

- **Environmental limitations.** The `go test` failure on port 8088 — `TestServiceInstallPreflightAllAbsolute`, because the Owner's GoAl holds the port — and the unavailable local `-race` run (`-race requires cgo`, `CGO_ENABLED=0`, no C compiler) are recorded as environmental limitations, not as PASS or SKIP. Owner instances are not interfered with and the port-bound test is not retried. The Linux CI `-race` and CI test jobs, and the exact-SHA CI run after publication, carry that evidence; exact-SHA CI stays mandatory.

- **Supersession, narrowly stated.** Two status claims in the sections above are superseded as to the ADR's status only. First, the 2026-10-29 reconciliation section's `promotes no status (**ADR 023 stays Proposed**)` phrase. Second, the final-acceptance bullet titled "**Acceptance is not ratification**" together with its `It stays **Proposed**` sentence. The Owner has since ratified ADR 023, which is **Accepted**. Those bullets' statements that commit, publication, exact-SHA CI and release remain separate authorizations stand unchanged, and every other bullet of both sections stands as written.

- **What this section does not do.** It changes no criterion and no CSS, HTML, JavaScript, Go, i18n or test byte, records no new `[O]` observation, closes none of `UX-PIPELINE-LIST-DENSITY`, `UX-PIPELINE-EDITOR-DENSITY` or `OWNER-TOUCH-01`, leaves OV-7 retired and OV-4's real touch **NOT OBSERVED**, and authorizes no commit, push, tag or release.

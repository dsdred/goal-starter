# ADR 023: Model-card density and user-facing status vocabulary (UX V-2)

**Status:** **Proposed** — this is the design record the Owner ordered instead of a waiver (decision **Q1a**, 2026-10-26). The Owner ratified decisions **Q1a, Q2, Q3, Q4, Q5, Q6** plus five additional constraints at the UX V-2 Design Decisions gate; they are recorded verbatim in §Owner decision record and contracted as **D-1 … D-11** below. Under [../DEVELOPMENT.md](../DEVELOPMENT.md) §ADR process (`:195-199`) **Proposed** means "decision made, implementation pending": **implementation is NOT STARTED and NOT authorized**, and this document changes no CSS, JavaScript, HTML, i18n or test byte. Nothing in this ADR is a claim of shipped behavior. **The Owner ratified §I-1 and §I-2 at the 2026-10-26 ratification gate; both are resolved below. §I-1 (the casing exemption also covers `pending`) is accepted. §I-2 is accepted as a REJECTION of the earlier "global badge-class exemption" mechanism: the casing exemption must not be a global rule and must not reach the History/Logs hint badges, so D-3 is rewritten to apply per render surface. One residual consequence of that rewrite — a `pending` badge on the frozen History/Logs surfaces keeps its current uppercased rendering — was raised as §I-3 and is now **RATIFIED** by the Owner (2026-10-26 Final Documentation Consistency gate): the History/Logs uppercase presentation for `pending` is preserved — RU «Ожидание» on Models / Instances / Pipelines, «ОЖИДАНИЕ» on History / Logs — as a presentation-only distinction that leaves the `models.status.pending` key and the internal `pending` state unchanged; no further typography redesign is authorized. **UX V-2.1 (2026-10-27, Owner Design Approval gate):** the V-2 implementation landed (`95829e5e8aef94e7b0bffb3218afdf71f216c4ee`) and the Owner's `[O]` run returned **OV-3 = FAIL**; this file now carries a dated **UX V-2.1** layer (§UX V-2.1 amendment, the last section of this file) that amends D-5/D-6/D-7/D-9, the test obligations and AC-5/AC-7 for the Owner-approved **Layout 2 (progressive merge)** + **H-B (accessible info button)** direction. That layer is **design-only**: V-2.1 is **NOT implemented and NOT authorized**, no CSS/HTML/JS/i18n/test byte changes here, and the status above stays **Proposed**.**

**Date:** 2026-10-26
**Depends on:** [ADR 022 — Model Card Transition Indicator](022-model-card-transition-indicator.md) (**Accepted**, published as `3342719c7e055bbe3ff3b805c91aa8a2ba925f64`, tracking reconciled in `c3002ec6cb566ff7059a139426783d9ea05abc48`) — its card contract is a hard boundary of this design (§D-7); [ADR 005](005-recovery-pid-reattach-orphan.md) / [ADR 008](008-recovery-kill-orphan.md) — the recovery concept whose **technical** name this ADR deliberately does not rename; [ADR 013](013-pipeline-repeat-models-ux-polish.md) — the icon-strip convention and the pipeline chip/badge shape this design must leave intact.
**Related:** [ADR 021](021-lifecycle-state-observability-view.md) (the status badge is the F-7.5 evidence surface — its class list and single-word property stay unchanged), [ADR 010](010-pipeline.md) (Pipelines share the card pattern), LIFECYCLE-VIS-01 / **MA-06** (**stays OPEN**; nothing in this ADR touches it).
**Tracking rows registered by this gate:** the `ADR 022 V-2` row of [BACKLOG.md](../../BACKLOG.md) (`:338`) gets a dated supersession note naming this ADR as the contract source; the new **ADR022-SKIP-01** row is added under the same ADR 022 register and is deliberately kept as a separate finding, **not** part of this design (§Out of scope); [ROADMAP.md](../../ROADMAP.md) gains a `### Model Cards & Status Vocabulary (multi-release UX)` subsection in §Product/UX evolution with one unchecked design-contracted row; [docs/ARCHITECTURE.md](../ARCHITECTURE.md) gains the `0023` ADR-table row with status **Proposed**.
**Numbering:** `docs/adr/` holds `0001`, `002`–`011`, `013`, `014`, `016`–`020`, `021`, `022` at the baseline — `012` never materialized (recorded in ADR 020 §Numbering) and `015` is a roadmap-referenced gate with no file. **023 is therefore the next free number** and the only file added by this gate.
**Baseline for every file:line reference below:** `c3002ec6cb566ff7059a139426783d9ea05abc48` (`HEAD == origin/main` at the time of writing). Line numbers are exact at that commit and will shift when the implementation lands; the cited selectors, functions and dictionary keys — not the numbers — are the contract.

## Context

### What is being contracted

The Owner's Model Cards & Status Filters UX direction, tracked since 2026-10-25 as "V-2" in [BACKLOG.md](../../BACKLOG.md) (`:338`), has three parts: (1) **compact model cards** — less vertical space per row, actions still reachable, responsiveness preserved, **Pipelines not affected**; (2) the `orphan` **user-facing label** renamed to RU «Вне GoAl» / EN `UNMANAGED`; (3) the `pending` label renamed to RU «Ожидание» / EN `PENDING`. The tracking row recorded the direction and its two hard boundaries — the `orphan` lifecycle **state** is not removed and process/API semantics do not change — and stated that contracting it "requires its own scope agreement". **This ADR is that scope agreement.**

### Present bytes (measured, not recalled)

| Surface | Coordinate | Present value |
|---|---|---|
| RU state word, orphan | `internal/webui/static/i18n/ru.json:59` | `"models.status.orphan": "Сирота"` |
| EN state word, orphan | `internal/webui/static/i18n/en.json:59` | `"models.status.orphan": "ORPHAN"` |
| RU state word, pending | `internal/webui/static/i18n/ru.json:60` | `"models.status.pending": "Ожидание..."` |
| EN state word, pending | `internal/webui/static/i18n/en.json:60` | `"models.status.pending": "Waiting..."` |
| RU/EN siblings | `ru.json:53-58`, `en.json:53-58` | RU mixed-case words; EN already all-caps literals (`RUNNING` … `FAILED`) — `Waiting...` is the only EN outlier, so the `PENDING` value the V-2 direction specifies (`BACKLOG.md:338`, restated as requirement 3 of the UX V-2 planning gate) restores that convention; **Q3 as ratified names only `orphan`** |
| State-filter fallback text | `internal/webui/templates/index.html:313-314` | `<option value="pending" …>Waiting...</option>`, `<option value="orphan" …>Orphan</option>` — pre-i18n placeholder text; `applyI18n()` overwrites `[data-i18n]` `textContent` at `app.js:53-56` |
| Density anchors | `style.css:231` `.model-list{gap:6px}`, `:232-236` `.model-row{gap:12px; padding:9px 14px; transition:all 0.15s}`, `:239` `.model-row-name{gap:6px; font-size:0.95rem}`, `:242` `.model-row-sub{font-size:0.78rem; margin-top:2px}`, `:250` `.model-row-actions{gap:4px}` | the only levers a CSS-only density change has |
| Touch target | `style.css:251-257` `.icon-btn{width:44px; height:44px}`, `:258` `.icon-btn svg{23×23}` | a floor **inside the Models rows**, not slack (§D-6). The compact-card variant used by Instances/History is already smaller — `style.css:441` `.compact-actions .icon-btn{width:40px; height:40px}` — which is pre-existing, outside `#model-list`, and untouched by this design |
| Badge casing rule | `style.css:281-285` `.status-badge{… letter-spacing:0.03em; text-transform:uppercase}` | the reason the renamed RU word would not display as ratified (§Rendered-language analysis) |
| Per-state badge colors | `style.css:291` `.status-badge.orphan{… color:#9b59b6}`, `:293` `.status-badge.pending{… opacity:0.8}` | the two state classes D-3 targets — by adding surface-scoped `text-transform` overrides, never by editing these rules in place |
| Reduced motion | `style.css:276-277` — the only `@media (prefers-reduced-motion: reduce)` block in the project, covering `.model-progress-ring{animation:none}` | D-8 extends it |
| Mobile card rules | `style.css:711-712` inside `@media (max-width:768px)` (`:700`): `.model-row{flex-wrap:wrap}`, `.model-row-actions{width:100%}` | shared with Pipelines |

### Every consumer of the two renamed keys

`t('models.status.' + <state>)` (and the `historyStateLabel` fallback to it) reaches the DOM from **nine production render sites** — five read the state key directly and four go through `historyStateLabel` — plus two filter options. All of them read the same dictionary, so the **value** change needs no per-site edit. What differs per site is what the badge **displays**, and that is what decides the casing exemption in §D-3:

**State-word surfaces** — the badge's visible text is the renamed word `models.status.<state>`:

1. `app.js:574` — the My Models row badge, inside `#model-list` (the ADR 022 surface);
2. `app.js:736` — `pipelineStateBadge(st)`, the Pipeline card's **aggregate** badge;
3. `app.js:797` — the inline aggregate badge inside `renderPipelines()`;
4. `app.js:1819` — the Instances desktop-table badge in `#adv-instances-body` (visible text `models.status.<state>`; its `title` uses `instances.orphan.hint` only when orphan);
5. `app.js:1843` — the Instances mobile compact card badge in `#instances-compact` (same shape);

**Hint-sentence surfaces** — the badge's visible text comes from `historyStateLabel(s)`, which prefers `history.state.<s>` and falls back to `models.status.<s>` (`app.js:1908-1914`). Because `history.state.orphan` exists (`en.json:208` / `ru.json:208`), an `orphan` badge here shows the **sentence** «Может выполняться вне GoAl», not the state word; because `history.state.pending` does **not** exist, a `pending` badge here falls back to the state word «Ожидание»:

6. `app.js:1090-1091` — the Logs instance-selector option label (`name | id | stateLabel`, a native `<option>`);
7. `app.js:1114` — the Logs bar badge in `#log-instance-bar`;
8. `app.js:1997` — the History desktop-table state-cell badge in `#history-body`;
9. `app.js:2020` — the History compact-card badge in `#history-compact`;

**Filter options:**

10. `index.html:313-314` — the Models state filter options (`value` attributes are the internal tokens and do not change; the label text comes from `models.status.*` via `applyI18n()` at `app.js:53-56`).

Two further surfaces reuse the badge **class** but are not consumers of the renamed **keys**: the Pipeline **chip** badges (`pipelineChips`, `app.js:710-712`) render a model **name** (`esc(name)`), and the Models action strip's orphan branch (`app.js:561-562`) renders the `instances.orphan.hint` text inside `.hint-text`, not a `.status-badge`. The casing exemption in D-3 is scoped to the **state-word surfaces** (sites 1–5) only; the hint-sentence surfaces (6–9) and the chips are deliberately left at today's presentation (§D-3, §D-4, §I-2).

### Constraints that shaped this decision

- **Pipelines reuse the Models card pattern verbatim.** `style.css:543` states it in a comment ("Pipeline list: uses the canonical .model-row card pattern (same as Models)") and `renderPipelines()` emits `<div class="model-row">`; `pipeline.cjs` asserts against `#pipeline-list .model-row .icon-btn` (`:348`, `:371`), `#pipeline-list .model-row .model-row-name .status-badge` (`:693`) and an in-viewport bounding-box leg (`:735`). Q2 therefore cannot be satisfied by editing shared rules.
- **Both containers carry the same class.** `index.html:317` `<div class="model-list" id="model-list">` and `index.html:334` `<div class="model-list" id="pipeline-list">` — so `.model-list` is an **unsafe** scoping hook and `#model-list` is the only safe one.
- **ADR 022's DOM contract is load-bearing for the maintained suite.** `stopping.cjs` reads the transition marker facts off the first Models row (`modelStripInPage`, `orphan.cjs:13` documents the same reader), and `orphan.cjs:156` asserts the orphan strip's text equals `I18N.ru['instances.orphan.hint']` byte-for-byte.
- **No automated reduced-motion coverage exists today.** `grep -rn reducedMotion tests/browser` matches only Playwright's own bundled type declarations, and no maintained suite creates a reduced-motion context. ADR 022's motion analysis (`022-…md:154`) reasons about the fallback from the CSS, design-derived `[C]` evidence with no leg exercising it. Playwright supports `reducedMotion: 'reduce'` as a context option, so an automated leg is feasible (Q6).
- **Label expectations are derived, not hardcoded.** `orphan.cjs:11` and `stopping.cjs:81` load the two dictionaries and `stopping.cjs:82-83` build their expected strings from the keys; no maintained leg asserts the literals «Сирота», `ORPHAN`, «Ожидание...» or `Waiting...` (the only two matches in `tests/browser/` are a suite title at `orphan.cjs:109` and a comment at `:143`, not assertions). The rename is therefore test-transparent by construction — which must be re-proved after the change, not assumed.
- **The list is rebuilt wholesale.** `renderModels()` assigns `list.innerHTML` on every render (`app.js:545`) and the page re-renders on the shipped 3000 ms timer (`startRefresh`); ADR 022 registered that as debt **F-3**. Any design that stores per-row UI state in the DOM would be erased by the rebuild — which is why expand/collapse is rejected here (Q5).

## Decision

### D-1 — The rename is presentation-only; every internal token is frozen

`orphan` and `pending` remain the state values everywhere they are internal: the Go domain and storage, the API request/response payloads, the audit records, the CSS class names (`.status-badge.orphan`, `.status-badge.pending`), the i18n **key** names (`models.status.orphan`, `models.status.pending`), the `<option value>` attributes, and every test selector. **Only four dictionary values and two hardcoded fallback strings change.** A leg may not be rewritten to match a renamed selector, because no selector is renamed.

### D-2 — The ratified vocabulary

| Internal state | Stored value (RU) | Stored value (EN) | Rendered (RU) | Rendered (EN) |
|---|---|---|---|---|
| `orphan` | Вне GoAl | UNMANAGED | **Вне GoAl** (not «ВНЕ GOAL») | UNMANAGED |
| `pending` | Ожидание | PENDING | **Ожидание** (no ellipsis) | PENDING |

The stored string **is** the displayed string for both renamed labels (D-3). No new key is introduced and no key is deleted, so `i18n.cjs`'s raw-key detector (`:166-183`) keeps its current meaning.

### D-3 — Displayed casing: the two renamed states are exempt from the uppercase transform

`style.css:284` applies `text-transform: uppercase` to `.status-badge`, which would render the ratified «Вне GoAl» as «ВНЕ GOAL» — a normalization of user-visible copy, and a mangled product name. The decision: **`text-transform: none` is applied to the renamed states on the state-word surfaces only, scoped by container — never as a global `.status-badge` rule and never as a blanket `.status-badge.orphan` / `.status-badge.pending` class rule.** Q2's id-scoping requirement governs the density (layout); the casing exemption is scoped by surface for a different reason: the same badge class is reused on surfaces that must stay frozen (§I-2).

The implementation adds overrides anchored on the containers of the five state-word surfaces:

```css
#model-list .status-badge.orphan,            #model-list .status-badge.pending,
#adv-instances-body .status-badge.orphan,    #adv-instances-body .status-badge.pending,
#instances-compact .status-badge.orphan,     #instances-compact .status-badge.pending,
#pipeline-list .model-row-name .status-badge.orphan,
#pipeline-list .model-row-name .status-badge.pending {
    text-transform: none;
}
```

These selectors are additive and edit no existing rule in place: they leave `.status-badge` (`:281-285`), `.status-badge.orphan` (`:291`) and `.status-badge.pending` (`:293`) untouched, so every unrelated status badge (`running` / `starting` / `stopping` / `stopped` / `exited` / `failed` / `stale`) keeps `text-transform: uppercase` on every page — the Owner's I-1 guard that no global typography change touches unrelated badges. The `#pipeline-list .model-row-name` form is deliberately two-part: it reaches the Pipeline card's **aggregate** state badge (`app.js:736`/`:797`, whose text is the state word) but not the Pipeline **chips**, which live in `.pl-chips-wrap` (`:800`) and carry model names (§D-4).

`pending` is included because the Owner's I-1 ratifies it: the ratified RU value «Ожидание» would otherwise only ever be seen as «ОЖИДАНИЕ» on the state-word surfaces, which contradicts D-2's "stored == displayed". EN `PENDING` is already uppercase, so the transform is a no-op there (§Rendered-language analysis).

**What this scoping deliberately does NOT reach, and why.** Because sites 6–9 are excluded by container, the History/Logs badges keep today's uppercased presentation unchanged: the `orphan` badges there render the `history.state.orphan` **sentence** («Может выполняться вне GoAl»), which is exactly the "technical hint badge" the Owner's I-2 forbids changing, and the `pending` badges there fall back to `models.status.pending` = «Ожидание» but stay rendered uppercase on those surfaces. **That last point is a recorded consequence: I-1's «Ожидание» is enforced on the actionable state-word surfaces (Models, Instances, Pipeline aggregate); on the frozen History/Logs surfaces I-2's "do not change these badges" wins, so a History/Logs `pending` badge renders «ОЖИДАНИЕ» as it does today.** This is the Owner-ratified §I-3 (judged by OV-8). No surface that displays the **label** «Вне GoAl» renders it uppercased, because every such surface is a state-word surface (1–5) and is covered.

### D-4 — Pipeline chips and card shape stay byte-for-byte; only the aggregate state badge changes

Pipeline chips carry a **model name**, not a state word, but reuse the badge class for colour (`app.js:710-712`, styled by `.pl-chips-wrap .status-badge` at `style.css:462`). Under the earlier global-class design this required an explicit three-class restore rule to keep those names uppercased; that design is **superseded** by the Owner's §I-2 ratification. Because D-3 is now scoped by container and its selector list never names `.pl-chips-wrap`, a chip badge is simply outside the exemption, keeps `text-transform: uppercase`, and no restore rule or declaration-order dependency is needed — which is exactly what resolves review finding **R-5** (the equal-specificity order dependence of the removed restore). The Pipeline card's layout, chips, actions and chip-name casing therefore stay byte-for-byte as today (Q2), and T-5 asserts it.

The Pipeline card's **aggregate** badge (`app.js:736`, rendered inline at `:797`) is a state word from the same dictionary, so it picks up the new vocabulary **and** — because D-3 explicitly lists `#pipeline-list .model-row-name` — the casing exemption. It is the one element of a Pipeline card that changes, and it changes for a reason the Owner forced rather than a loophole the design opened: Q3 forbids «ВНЕ GOAL» on any surface that shows the «Вне GoAl» label, and I-2 says preserve «Вне GoAl» wherever it is a user-facing state label. The aggregate badge's RU text goes from «СИРОТА» to «Вне GoAl» (displayed, not uppercased) and its EN from `ORPHAN` to `UNMANAGED`; for `pending` from «ОЖИДАНИЕ...» to «Ожидание» and `WAITING...` to `PENDING`. OV-5 is the Owner leg that confirms this visibly — the aggregate RU badge reads «Вне GoAl» while the chips still read model names in uppercase.

### D-5 — Density is CSS-only and scoped by the `#model-list` id

Every density rule the implementation adds is anchored on `#model-list` (the container at `index.html:317`) and must not modify a shared selector's own declaration. The permitted levers, all inside `#model-list`: row `padding` / `gap` (`style.css:232-236`), list `gap` (`:231`), name/sub typography (`:239`, `:242`), action-strip `gap` (`:250`), badge `padding` (`:281-285`) and the mobile rules (`:711-712`) **as `#model-list`-scoped overrides** — noting that those two lines carry more than wrap: `:711` is `.model-row { flex-wrap: wrap; gap: 8px; }` and `:712` is `.model-row-actions { width: 100%; justify-content: flex-end; margin-top: 4px; }`, so a scoped override must restate every declaration it wants to keep (`gap`, `justify-content`, `margin-top` included) rather than assume `flex-wrap` is the only property in play. Forbidden as hooks: `.model-list`, `.model-row`, `.model-row-*`, `.icon-btn`, `.status-badge`, `.compact-*` edited in place, because Pipelines (`index.html:334`), Instances (`app.js:1819/1843`) and History share them.

**UX V-2.1 (2026-10-27) adds levers and one boundary to this list — superseding the lever set above, not the safety rule.** Still `#model-list`-scoped, still no shared selector edited in place: (i) the mobile rules `:740-741` (`gap`, `width`, `justify-content`, `margin-top`) may be overridden as `#model-list`-scoped declarations, and `margin-top` and **`row-gap`**/column-gap become levers of their own; (ii) at **631–768 px** the wrapper `#model-list .model-row-main` may be set to `display: contents` and the three grandchildren re-sized as flex items of `.model-row` (`#model-list .model-row-name`, `#model-list .model-row-sub`, `#model-list .model-row-actions`). `order` is **forbidden** (DOM and reading order must stay name → metadata → actions), the DOM shape is unchanged (no element added, removed or re-parented in markup), and the new block's declarations are source-order dependent on the `≤768px` block, so it must be appended after it. Exact bytes and the measured effect: **A-2**, **A-3**.

### D-6 — What density may not cost

- **Touch targets:** no rule may reduce `.icon-btn` below 44×44 px inside `#model-list` (`style.css:251-257`), at any viewport of the maintained 1920/1440/1280/1024/768/600/430/375 sweep (`responsive.cjs:138-142` in these bytes; `:126-128` is the `c3002ec` coordinate named in §Baseline). Density comes from padding, gaps and typography — not from shrinking controls. **UX V-2.1 (2026-10-27):** the orphan row's info control is an `.icon-btn` and therefore inside this floor, and the sweep gains the two breakpoint-boundary widths 630/631 because the merged geometry is new there (A-1, A-8).
- **Action availability:** every action keeps its icon button and its `data-tooltip`/`aria-label` (ADR 013 convention, `iconBtn` at `app.js:421-423`); no action may be hidden behind a hover, a breakpoint or an affordance that does not exist today.
- **DOM and JavaScript:** zero bytes in `app.js`, `index.html` (beyond the two `:313-314` fallback strings) and any Go file (Q5). **UX V-2.1 (2026-10-27) amends this bullet by a single Owner-authorized exception** — the Models `orphan` branch and nothing else: the hint leaves the strip for an accessible info control, which needs `ICONS.info`, one tooltip-toggle helper, the rewritten orphan branch and the removal of that row's badge `title` (A-1). No other `app.js` byte, no `index.html` byte beyond `:313-314`, no Go byte, and **no CSS byte at all for the control** (it reuses `style.css:251-265`).
- **No expand/collapse, no hover-reveal, no second row state** — the list is rebuilt on every 3000 ms render (`app.js:545`), so DOM-held UI state would be erased mid-interaction (F-3).
- **No new animation** (Q6), and therefore no new reduced-motion obligation than D-8 already states.

### D-7 — ADR 022's contract survives unchanged

The badge stays the only state word per row (`app.js:574`, class list `status-badge <state>` intact); the action strip stays text-free in `starting`/`stopping` (`app.js:556-560`, `aria-hidden` marker + `.model-progress-ring`); the orphan branch keeps its hint text (`app.js:561-562`); the `.model-transitional` (`style.css:266` at §Baseline, `:275` in the V-2 bytes) and `.model-progress-ring` (`:269` at §Baseline, `:277` in the V-2 bytes) hooks keep existing and matching the maintained selectors. A density change may not hide, displace or clip the ring, and may not re-introduce text into the strip.

**UX V-2.1 (2026-10-27) supersedes the orphan clause of this decision, and strengthens the text-free rule.** The orphan branch no longer renders the hint as text: `app.js:562`'s `.hint-text model-transitional` span is removed and the hint is delivered by the info control of **A-1** instead. What D-7 keeps is the **preservation**, not the channel: the hint string is unchanged (D-9), it remains reachable inside the same row, and it is announced by the same canonical tooltip system. The strip therefore becomes **text-free in every state** — `exited`/`failed`/`stopped` (icon buttons only), `starting`/`stopping` (ring only, `aria-hidden`) and `orphan` (icon buttons only) — which is what ADR 022 §AC-4 intended and what AC-7 now demands. `.model-transitional` keeps its only remaining production use (the transitional marker host at `app.js:558-559`), and no `.model-row-actions .hint-text` element is rendered on any Models row in any state.

### D-8 — Reduced motion: the Models row transition is disabled, and the rule is machine-checked

`@media (prefers-reduced-motion: reduce)` (`style.css:276-277`) gains `#model-list .model-row { transition: none; }` — `.model-row` currently animates `transition: all 0.15s` (`:236`), which reduced motion does not suppress today. No new animation is added anywhere. Q6 requires this to be **automatically** verified: a leg creates a Playwright context with `reducedMotion: 'reduce'` and asserts the computed `transition-duration` of a `#model-list .model-row` is `0s` and the computed `animation-name` of `.model-progress-ring` is `none`. The leg is the project's first reduced-motion check, so ADR 022's "no automated leg asserts reduced motion" note is superseded **for the Models row only** once it lands.

### D-9 — Hints and technical explanations of orphan processes are not renamed

`instances.orphan.hint` (`ru.json:110` / `en.json:110`), `history.state.orphan` (`:208`) and `instances.kill.confirm` (`:113` — RU «Завершить процесс-сироту (PID {pid})?», EN "Kill the orphan process (PID {pid})?") keep their current bytes. The hint already reads «Может выполняться вне GoAl» / "May still be running outside GoAl", which is the same vocabulary the new label uses, so the badge and its tooltip agree without touching either string. `orphan.cjs:156` pins the hint byte-for-byte; D-9 guarantees that leg cannot break. Technical naming of the *concept* (ADR 005/008 recovery, `docs/API.md`, the internal `orphan` state) is not a UI label and is not renamed mechanically (Owner Q4 rider).

**UX V-2.1 (2026-10-27): the bytes stay frozen; the delivery channel changes, and the pins move with it.** `instances.orphan.hint` remains byte-identical in both dictionaries and remains the single string that reaches the user for an orphan model. What changes is where it is rendered: from the strip's visible text (removed, A-1) to the info control's `data-tooltip` + `aria-label` — the same pair every canonical `.icon-btn` already carries — so D-9's guarantee ("the hint is not renamed and keeps its bytes") is untouched while its *observable form* changes. Consequences for the cited pins, re-derived at `95829e5e`: `orphan.cjs:225-227` (RU strip text) and `:353-355` (EN strip text) must be replaced by the A-1 legs, because the assertion `strip.textContent === hint` describes the superseded channel; `orphan.cjs:251-252` (Instances badge `title` byte-identical to the hint) and `:203-204` / `:333-335` (Instances `title` contains «вне GoAl» / 'outside GoAl') stay **unchanged and mandatory** — those are the Instances surfaces, which this layer does not touch. The `orphan.cjs:156` coordinate quoted above is a **design-era** figure from the 28-leg corpus named in §Baseline; in the current 53-leg file the Instances `title` pin is `:251-252`, and any implementation gate must re-derive rather than copy it. `history.state.orphan` and `instances.kill.confirm` are untouched everywhere.

### D-10 — User-guide reconciliation happens with the implementation, not before

The guides describe shipped behavior. Renaming their label references now would make the documentation contradict the running app — the failure mode AGENTS.md §Source of Truth forbids in the other direction. So the guide edits are part of the implementation/reconciliation delta, and this ADR is what makes them non-discretionary. Sites to reconcile (measured at the baseline; re-derive before editing):

| Site | Present text | Disposition |
|---|---|---|
| `docs/USER_GUIDE.md:442` | heading "Orphaned processes (ORPHAN)" | rename the **label** reference to `UNMANAGED`; keep the concept name |
| `docs/USER_GUIDE.md:444` | "shown as **ORPHAN** (\"May still be running outside GoAl\")" | label reference → `UNMANAGED`; quote unchanged (D-9) |
| `docs/USER_GUIDE_RU.md:418` | heading «Процессы-сироты (ORPHAN)» | heading keeps the concept («Процессы-сироты»), parenthetical label → «Вне GoAl» |
| `docs/USER_GUIDE_RU.md:420` | «отображается как **ORPHAN** («Может выполняться вне GoAl») … показывает бейдж Сирота» | label references → «Вне GoAl»; hint unchanged |
| `docs/adr/022-model-card-transition-indicator.md:134` | table row `ORPHAN / «Сирота»` | **dated supersession note**, not a rewrite (ADR 022 is a published record) |

Any `Waiting` / `Ожидание` reference in the guides is checked at the same time; the baseline sweep found none outside the dictionaries and the template fallback.

### D-11 — RU and EN move together

The two dictionaries are edited in the same commit, with the identical key set and identical key names; a label change in one locale without the other is a defect, not a partial delivery. RU/EN coverage of both renamed states is required on the Models page, the Instances table, the Instances compact card and the Models state filter (T-8, T-9, OV-1/OV-2).

## Rendered-language analysis (why D-3 is a decision, not a detail)

| Stored / surface | Without D-3 | With D-3 (surface-scoped) |
|---|---|---|
| `Вне GoAl` on a state-word surface (Models / Instances / Pipeline aggregate) | `ВНЕ GOAL` — the product name `GoAl` loses its internal lowercase `a`; the displayed string is not the ratified string | `Вне GoAl` |
| `Ожидание` on a state-word surface | `ОЖИДАНИЕ` | `Ожидание` |
| `UNMANAGED` (EN, state-word surface) | `UNMANAGED` | `UNMANAGED` (already uppercase, so the transform is a no-op — D-3 costs EN nothing) |
| `PENDING` (EN, state-word surface) | `PENDING` | `PENDING` (same) |
| Pipeline chip `ComfyUI` (class `.orphan`, text is a **name**) | `COMFYUI` | unchanged — D-3's selector list never names `.pl-chips-wrap`, so the chip is outside the exemption (no restore rule needed) |
| History/Logs `orphan` badge, text = `history.state.orphan` «Может выполняться вне GoAl» | `МОЖЕТ ВЫПОЛНЯТЬСЯ ВНЕ GOAL` | unchanged — the surface is not in D-3's list, so the hint sentence keeps today's presentation (§I-2) |
| History/Logs `pending` badge, text falls back to `models.status.pending` «Ожидание» | `ОЖИДАНИЕ` | unchanged `ОЖИДАНИЕ` — the surface is frozen; the dictionary still drops the ellipsis (see §I-3) |

Sibling RU labels («Работает», «Остановлен», …) keep rendering uppercase on every surface. That asymmetry is deliberate and is exactly what the Owner ratified in I-1/I-2: only the two renamed states are exempted, and only on the surfaces where they are the displayed state label, because only for them and only there was a **displayed** spelling specified.

## Scope contract (the implementation gate's file list, when authorized)

| Path | Change | Estimated delta |
|---|---|---|
| `internal/webui/static/i18n/ru.json` | values at `:59`, `:60` | 2 lines |
| `internal/webui/static/i18n/en.json` | values at `:59`, `:60` | 2 lines |
| `internal/webui/templates/index.html` | fallback text at `:313`, `:314` only | 2 lines |
| `internal/webui/static/style.css` | D-3 surface-scoped `text-transform: none` overrides (one added rule block on `#model-list` / `#adv-instances-body` / `#instances-compact` / `#pipeline-list .model-row-name`, no in-place edit of `:281-285`/`:291`/`:293`/`:462`; **no chip restore rule** — superseded), D-5 `#model-list` density block, D-8 reduced-motion line (`:276-277`) | ~12–18 lines, additive only (no existing declaration amended) |
| `tests/browser/responsive.cjs` | T-5 pipeline-isolation leg, T-6 touch-target leg, T-10 reduced-motion leg | additive |
| `tests/browser/orphan.cjs` | label-derivation already i18n-driven; only a positive assertion that the rendered text equals the new dictionary value | additive, small |
| `docs/USER_GUIDE.md`, `docs/USER_GUIDE_RU.md` | D-10 sites | 4 lines |
| `docs/DEVELOPMENT.md` | the `responsive.cjs` suite row gains the new legs; the fifteen-suite note is re-counted if leg totals change | 2–3 lines |
| `docs/adr/022-…md`, `docs/ARCHITECTURE.md`, `ROADMAP.md`, `BACKLOG.md` | dated supersession for `:134`; status reconciliation of this ADR and the tracking rows | dated notes only |
| **Not touched** | any `.go` file, `cmd/goal/main.go` (the phantom ` M` CRLF-noise entry stays unstaged), `docs/API.md`, `docs/CONFIGURATION.md`, `docs/SECURITY.md`, internal state tokens, i18n key names, CSS class names, `tests/browser/package.json` (no new suite) | 0 bytes |

## Test obligations

| ID | Obligation | Class |
|---|---|---|
| T-1 | Scope proof: `git diff --name-only` contains only the paths above; zero `.go` bytes; `grep -n '"orphan"\|"pending"'` over Go/API/store fixtures unchanged | `[A]`/`[C]` |
| T-2 | Dictionary integrity: both JSON files parse; key sets identical RU↔EN; exactly four values differ from the baseline; no key added, renamed or removed | `[A]` |
| T-3 | The rename is test-transparent: `orphan.cjs` and `stopping.cjs` run **unmodified in their assertions** and stay green (their expected strings come from the dictionaries) | `[A]` |
| T-4 | D-2/D-3 rendering, per surface: on each state-word surface (`#model-list`, `#adv-instances-body`, `#instances-compact`, `#pipeline-list .model-row-name`) the renamed badge's `textContent` equals the dictionary value **and** its computed `text-transform` is `none` (EN literal already uppercase). On every other surface — unrelated status badges and the frozen History/Logs badges — the computed `text-transform` stays `uppercase` | `[A]` |
| T-4′ | **UX V-2.1 (2026-10-27) adds the orphan-strip contract, replacing the two `strip.textContent === hint` legs (`orphan.cjs:225-227` RU, `:353-355` EN).** On a naturally recovered orphan model, per language: the action strip's `textContent` is empty; `.model-row-actions .hint-text` is absent; exactly one element in the strip carries `data-tooltip` **and** `aria-label` equal to `instances.orphan.hint`, and it is a `BUTTON` with `type="button"`; that element's box is ≥ 44×44; the row contains **no** `[title]` carrying the hint (the badge's native `title` is gone, A-1); activating it (click, or Enter on the focused control) makes `#goaltip` visible with `textContent === hint` and a second activation hides it; the Logs/Edit/Delete buttons stay present and in that relative order after it. The Instances `title` pins (`:251-252`, `:203-204`, `:333-335`) are unchanged controls for this leg | `[A]` |
| T-5 | **Pipeline isolation regression:** for `#pipeline-list .model-row` the computed `padding`, `gap`, `font-size` equal the values measured at baseline `c3002ec` and `.icon-btn` counts/sizes are unchanged. A chip badge in `.pl-chips-wrap` carrying `.orphan`/`.pending` (its text is a model **name**) computes `text-transform: uppercase` because it is **excluded** from D-3's selector list — asserted on the rendered element, and there is no separate restore rule to hold. The **aggregate** pipeline badge in `.model-row-name` computes `text-transform: none` **and** carries the new vocabulary word (the D-3 surface the Owner's I-2 requires). The leg must fail if a density or casing rule leaks to `.pl-chips-wrap` or elsewhere globally | `[A]` |
| T-6 | **Touch-target floor:** every `#model-list .model-row .icon-btn` has a bounding box ≥ 44×44 px at **all eight** sweep viewports `1920/1440/1280/1024/768/600/430/375` (`responsive.cjs:138-142` in these bytes), is fully in-viewport, and does not overlap `.model-row-main`. **UX V-2.1 (2026-10-27):** the same assertion runs at the two breakpoint-boundary widths **630** and **631**, and the five-button running row, the four-button orphan row (info + Logs/Edit/Delete) and the three-button transitional row are each measured with zero pairwise button overlap. The "does not overlap `.model-row-main`" clause applies **below 631 px** only: at 631–768 the wrapper computes `display: contents` and returns a zero rect, so the merged leg substitutes "never overlaps `.model-row-name` horizontally into its text box and never leaves the row's box" | `[A]` |
| T-7 | Density is real: at least one measurable reduction of `#model-list .model-row` bounding-box height versus the `c3002ec` baseline, recorded as a number, with the `.model-row-title` and `.model-row-sub` text still non-empty and not collapsed to zero width. **UX V-2.1 (2026-10-27) adds T-14's merged-geometry and boundary assertions (A-8); the reduction is now required at both branches — below 631 px against the V-2 bytes and at 631–768 px against the unmerged branch** | `[A]` |
| T-8 | Filter coherence: `#mf-state` options keep `value="pending"` / `value="orphan"` while their labels equal the new dictionary values in RU and EN; selecting `orphan` still filters to exactly the orphan rows (the existing legs at `orphan.cjs:160-167`) | `[A]` |
| T-9 | Render-site vocabulary split, exactly as §D-3 scopes it. **State-word surfaces** — Models (`app.js:574`), Pipeline aggregate (`:736` via `:797`), Instances table (`:1819`), Instances compact (`:1843`) — display the renamed word and compute `text-transform: none`. **Hint-sentence surfaces** — Logs selector (`:1090-1091`), Logs bar (`:1114`), History cell (`:1997`), History compact (`:2020`) — are frozen: the `orphan` badge there keeps the byte-identical `history.state.orphan` sentence at its **current uppercase presentation** (its computed `text-transform` stays `uppercase` — §I-2 ratification), and the `pending` badge there keeps «ОЖИДАНИЕ» (§I-3) with only the dictionary ellipsis removed. The Instances `orphan` badge's `title` stays byte-identical (`instances.orphan.hint`, D-9) | `[A]` |
| T-10 | **Reduced motion, automated:** a `reducedMotion: 'reduce'` context asserts computed `transition-duration: 0s` on `#model-list .model-row` and `animation-name: none` on `.model-progress-ring` (D-8) | `[A]` |
| T-11 | ADR 022 non-regression: `stopping.cjs` full suite green, including the marker/`aria-hidden` and strip-text legs; `pipeline.cjs` full suite green | `[A]` |
| T-12 | Corpus + platform checks: full `npm test` (fifteen suites) with the raw `PASS |`/`FAIL |`/`SKIP |` lines counted — never a sum of `TOTAL` rows; `go vet ./...`, Windows and `GOOS=linux` builds; `-race` evidence quoted from the Linux CI step of the exact SHA | `[A]` |
| T-13 | Documentation reconciliation (D-10): guides, `docs/DEVELOPMENT.md` suite row, ADR 022 dated note; every `file:line` citation re-derived after the code moves | `[A]`/`[C]` |
| T-14 | **UX V-2.1 (2026-10-27) breakpoint and merged-geometry contract (A-8).** At **630** the wrapper does not compute `display: contents` and the action strip's `top` is at or below `.model-row-name`'s `bottom` (own line); at **631** and **768** it computes `contents`, the strip's `top` is strictly above that `bottom` (shared line), and the strip stays inside the row's box. The same-element row height at 631 is strictly and meaningfully smaller than at 630 (a measured delta, never an absolute px pin), the maintained fixture's own metadata is untruncated at every width ≥ 631, every icon is ≥ 44×44 with zero pairwise overlap at 630/631/768 across the five-, four- and three-button rows, and `document.documentElement.scrollWidth === innerWidth` at every swept width. A served-stylesheet scan asserts exactly one `display: contents` declaration naming `#model-list .model-row-main`, that its media query is `min-width: 631px` … `max-width: 768px`, and that the block appears **after** the `≤768` density block in source order (D-5) | `[A]` |

## Acceptance criteria

- **AC-1** — RU Models row shows «Вне GoAl» and «Ожидание», displayed exactly as stored (D-2, D-3). `[A]` + `[O]` OV-1
- **AC-2** — EN Models row shows `UNMANAGED` and `PENDING`. `[A]` + `[O]` OV-2
- **AC-3** — Internal tokens, API payloads, CSS class names, i18n key names and `<option value>`s are byte-unchanged (D-1). `[A]`
- **AC-4** — Pipeline cards are unchanged in shape **and** chip-name casing (D-4, T-5). `[A]` + `[O]` OV-5
- **AC-5** — Models rows are measurably more compact, with every action reachable and ≥ 44×44 (D-5, D-6, T-6, T-7). `[A]` + `[O]` OV-3/OV-4. **UX V-2.1 (2026-10-27) status: unmet on the shipped bytes** — the `[O]` half, OV-3, is recorded as **FAIL** (`docs/acceptance/ADR023-OWNER-VISUAL-ACCEPTANCE.md`), and this layer contracts the correction (A-2/A-3) rather than restating the criterion. AC-5 can only be re-judged by a new Owner observation on a binary carrying the V-2.1 bytes.
- **AC-6** — No horizontal overflow, no clipped title/PID/uptime text, correct wrap at 768/430/375 (T-6, responsive corpus). `[A]` + `[O]` OV-6
- **AC-7** — ADR 022's contract holds: one state word per row, text-free transition strip, ring visible and not clipped (D-7). `[A]` + `[O]` OV-3. **UX V-2.1 (2026-10-27):** the strip is now text-free in **every** state, not only the transitional ones (D-7 amendment, T-4′), so the `[A]` half is stricter; the `[O]` half (OV-3) is the FAIL recorded above and is still unmet.
- **AC-8** — Reduced motion disables the Models row transition and the ring animation, verified automatically (D-8, T-10). `[A]` + `[O]` OV-7
- **AC-9** — Instances and History render the same vocabulary as Models (D-11, T-9). `[A]` + `[O]` OV-8
- **AC-10** — Guides name the new label where they name the label, and keep the technical concept (D-9, D-10). `[C]`

### Owner Visual Acceptance `[O]` — separate, non-substitutable

The automated legs are `[A]`; they do not discharge any `[O]` obligation and no agent run may be presented in place of the Owner's observation. Legs (prepared at the acceptance gate, executed only by the Owner, against the built binary of the exact SHA, in the Owner's own browser, RU and EN, desktop and mobile widths, with OS reduced motion toggled): **OV-1** RU Models vocabulary and its rendered spelling; **OV-2** EN vocabulary; **OV-3** perceived compactness without loss of readability (name, PID, uptime); **OV-4** reachability of Start/Stop/Restart/Logs/Edit/Delete by mouse and touch, plus hover/focus visibility; **OV-5** Pipelines visibly unchanged; **OV-6** mobile layout (no overflow, correct action wrap); **OV-7** reduced motion (no spin, no row transition); **OV-8** Instances/History coherence. Each leg gets its own verdict and the Owner's own remarks; screenshots are archived with a hash manifest and every frame kept, including out-of-window ones; timings are recorded as intervals `[L,U]`, and an unresolved ordering is reported as NOT OBSERVED, never as PASS. Like ADR 022's `[O] L1–L6`, a single Owner verdict supplied without per-run timestamps is recorded as one verdict and asserts no ordering or window.

## Rejected options

| Option | Why rejected |
|---|---|
| Implement without a design record (waiver) | Owner Q1a chose the formal record; AGENTS.md requires the contract before implementation |
| Edit the shared `.model-row` / `.icon-btn` rules globally for density | breaks Q2: Pipelines reuse the same pattern (`style.css:543`, `app.js:797`, `pipeline.cjs` selectors) |
| Scope the casing exemption to `#model-list` alone | Instances shows the same state label «Вне GoAl»/«Ожидание» on its table and compact card and must match Models (Q4, I-1); the exemption therefore covers every state-word surface, not Models only — while History/Logs stay frozen (§D-3, §I-2) |
| Leave `text-transform: uppercase` in place | the ratified «Вне GoAl» would never be displayed as ratified, and the product name would be mangled |
| Apply the exemption globally on the `.status-badge.orphan`/`.pending` class | the Owner's I-2 rejects this: it bleeds into the History/Logs technical hint badges and requires a three-class `.pl-chips-wrap` restore to undo the chip-name side effect — container scoping removes both problems and the restore with them |
| Restore the transform on chips by exempting only orphan/pending **names** | chips carry names (`app.js:710-712`); a class-based exemption cannot tell a name from a state word — superseded: D-3 never names `.pl-chips-wrap`, so chips are outside the exemption and keep their casing with no restore rule (§D-4) |
| DOM/JS restructuring, a merged single-line row, or expand/collapse to save space | Q5 (CSS-only), and DOM-held UI state is erased by the 3000 ms `innerHTML` rebuild (`app.js:545`, F-3) |
| Shrink `.icon-btn` below 44 px for compactness | removes the touch-target floor on mobile (D-6) |
| Rename `models.status.*` keys or the internal `orphan`/`pending` tokens | D-1: Owner constraint "внутренние значения статусов не менять"; would cascade into API, store, audit and every test selector |
| Mechanically rename "orphan process" prose in guides, tooltips and kill confirmation | Owner Q4 rider; the technical concept keeps its name (D-9) |
| Edit the user guides in this design gate | documentation would contradict the shipped app until the implementation lands (D-10) |

## Interpretation register (I-1, I-2 and I-3 all ratified 2026-10-26)

**I-1 — RATIFIED.** The casing exemption covers `pending` as well as `orphan`. The Owner ratified it and added the guard that it must not become a global typography change touching unrelated badges. §D-3 satisfies both halves: it exempts `orphan` **and** `pending`, but only on the state-word surfaces, and it edits neither `.status-badge` (`:281-285`) nor the per-state colour rules, so every unrelated status keeps `text-transform: uppercase`. T-4 and T-5 are the machine checks.

**I-2 — RATIFIED as a REJECTION of the earlier mechanism.** The Owner directed that the orphan exemption must **not** be applied globally to all History/Logs badges, must preserve «Вне GoAl» wherever it is the user-facing **state label**, and must not change the presentation of the technical **hint** badges. This is the reason §D-3 was rewritten from a global `.status-badge.orphan`/`.pending` rule to a container-scoped one: the exemption now covers the state-word surfaces (Models `#model-list`, Instances `#adv-instances-body`/`#instances-compact`, and the Pipeline aggregate `#pipeline-list .model-row-name`) and reaches none of the History/Logs badges, whose `orphan` text is the `history.state.orphan` sentence (`app.js:1114`, `:1997`, `:2020`) and stays at today's uppercased presentation. Because scoping to containers makes chips and the History/Logs badges unreachable by construction, the earlier three-class `.pl-chips-wrap` restore and the earlier "scope to `#model-list` only" dilemma are both superseded — see §D-4 and the Rejected-options note below.

**I-3 — RATIFIED (2026-10-26, Final Documentation Consistency gate).** Scoping by container produced one consequence the earlier decisions did not state outright: because `history.state.pending` does **not** exist, a `pending` badge on the frozen History/Logs surfaces falls back to `models.status.pending` = «Ожидание» but keeps rendering **uppercase** («ОЖИДАНИЕ»), with only the dictionary ellipsis removed. The Owner ratified that disposition verbatim: **"Preserve the current History/Logs uppercase presentation for pending. RU: Models / Instances / Pipelines: «Ожидание»; History / Logs: «ОЖИДАНИЕ». This is a presentation-only distinction. The underlying localization key and internal pending state remain unchanged. No additional typography redesign is authorized."** Accordingly §D-3 enforces «Ожидание» on the state-word surfaces (Models, Instances, Pipeline aggregate) and leaves the History/Logs `pending` badge at its current uppercase presentation; the distinction is presentation-only — no `models.status.pending` key, internal `pending` state or History/Logs badge rule changes. OV-8 remains the Owner leg that confirms this visibly.

## Out of scope (recorded so nothing is silently absorbed)

- **ADR 022 F-1 … F-4** — badge contrast, no lifecycle `aria-live`, whole-list `innerHTML` rebuild, instances-row controls during `stopping`: stay registered in `BACKLOG.md` under the ADR 022 register. F-1 is *adjacent* (it is the same `.status-badge` family) but is a contrast defect, not a density or vocabulary change, and stays separate.
- **ADR022-SKIP-01** — the two run-dependent `SKIP` legs in CI run `40341717357` (`c3002ec`) versus `40234792453` (`3342719c`): registered as its own class-C finding in `BACKLOG.md`; per the Owner it stays a separate investigation and is **not** fixed by UX V-2.
- **MA-06 / LIFECYCLE-VIS-01** — **stays OPEN**; this ADR changes no lifecycle behavior and closes no acceptance leg. Only F-7.5 carries an Owner `[O]` adjudication, and that record is still owed outside this task.
- **ADR021-CITE-01** — citation-drift debt in `BACKLOG.md`: untouched here; this ADR's rule is to cite functions/selectors and re-derive numbers at the implementation gate (T-13).
- **The `0020` row in `docs/ARCHITECTURE.md`** and the `docs/ARCHITECTURE_RU.md` ADR table (its last row is `0019`) — pre-existing documentation debt, deliberately not extended by this gate.
- **Pipelines density, pipeline semantics, lifecycle semantics, API and process semantics, authentication, portable import/export** — no change, by the Owner's constraints.

## Consequences

- The direction tracked since 2026-10-25 becomes a **contracted design** with an implementation-ready file scope (§Scope contract) and a machine-checkable obligation set (T-1 … T-13), so the implementation gate can be reduced to executing a list rather than re-deciding anything.
- The two renamed dictionary **values** are read by nine render sites plus two filter options **without any JavaScript change** — the rename's blast radius is the dictionary, not the code. The **displayed** effect is per surface, because D-3 is container-scoped: the five state-word surfaces (Models, Pipeline aggregate, Instances table + compact) show the new word with the new un-uppercased spelling; the four History/Logs hint-sentence surfaces are frozen by I-2, so their `orphan` badges still show the `history.state.orphan` sentence and their `pending` badges show the shortened «Ожидание» with today's uppercase rendering (§I-3).
- Pipelines change in exactly one visible place — the aggregate badge in `.model-row-name` — and are byte-for-byte unchanged in card layout, chip casing and chip names (D-4, T-5).
- The Pipelines/Models card sharing becomes an **explicitly tested boundary** (T-5) instead of a comment at `style.css:543`.
- Reduced motion gains the project's first automated leg (T-10), partially discharging the gap ADR 022 recorded.
- The Guides, `docs/DEVELOPMENT.md` suite rows and ADR 022's label table row acquire a **dated reconciliation obligation** that must land in the same implementation delta as the bytes, or documentation and app disagree.
- **Governance:** this ADR is **Proposed**. It authorizes nothing — not a CSS byte, not a test edit, not a commit, not a push, not a tag, not a release. **I-1, I-2 and §I-3 are all ratified (2026-10-26, across the ratification and Final Documentation Consistency gates); no interpretation remains open.** The sequence from here is: «Разрешаю реализацию UX V-2.» → implementation → tests → `[O]` acceptance → documentation reconciliation → Commit Gate → Publication Gate. Status becomes **Accepted** only when an authorized implementation exists, per `docs/DEVELOPMENT.md:195-199`.
- **Task-completion reconciliation for this gate:** classification **E/D → contracted** (an architecture/design task discharging a class-D direction); no new defect, blocker or release risk discovered; the findings surfaced while reviewing this design are otherwise already-registered debt (F-1…F-4, ADR022-SKIP-01, ADR021-CITE-01, `0020`/RU ADR-table gaps). **One genuinely new observation** came out of the review register: `style.css:441` renders `.compact-actions .icon-btn` at 40×40, below the 44×44 touch-target floor the Owner restated in this gate, on the Instances/History compact cards (**class C** technical debt, pre-existing, out of ADR 023's scope, and deliberately **not** registered in `BACKLOG.md` by this documentation-only gate — it is reported here and in the gate's final report so it can get its own row in a later gate).

## Independent design review register (2026-10-26, same gate)

One read-only adversarial review of this ADR and the registration delta was run at the gate (evidence classes: the reviewer read the repository bytes; no build, no test run). Every finding was re-checked against the bytes before it was applied, and one was escalated into a recorded interpretation. **No BLOCKER was returned.**

| ID | Severity | Finding | Disposition |
|---|---|---|---|
| **R-1** | MINOR | `stopping.cjs:82-83` do not "load the dictionaries" — `:81` loads `I18N`, `:82-83` derive the expected strings | **applied** in §Constraints that shaped this decision |
| **R-2** | MINOR | the "every consumer" list enumerated five badges, the helper and the filter options, and omitted the four render sites reached **through** `historyStateLabel` (Logs selector `app.js:1090-1091`, Logs bar badge `:1114`, History table cell `:1997`, History compact card `:2020`) | **applied** — the list is restructured into 5 direct + 4 helper-mediated sites + the options, T-9 is rewritten against it, and §Consequences' count is corrected |
| **R-3** | NOTE | `style.css:711-712` carry `gap`, `justify-content` and `margin-top`, not only `flex-wrap`, so a "`#model-list` override of the mobile rules" could leak or drop properties | **applied** to §D-5 |
| **R-4** | NOTE | the EN `PENDING` value was attributed to Q3, which as ratified names only `orphan` | **applied** — the Present-bytes row now attributes it to the V-2 direction (`BACKLOG.md:338`) and states the divergence |
| **R-5** | MAJOR | `.status-badge.orphan` (`style.css:291`) and `.pl-chips-wrap .status-badge` (`:462`) have **equal specificity**, so D-4's restore held only because `:462` is later in the file — an implementation that appends the D-3 exemption after it would silently re-case pipeline chip names | **applied** — D-4 now requires the three-class form (0,0,3,0), order-independent, and T-5 asserts `text-transform` on a rendered chip badge instead of on the rule |
| **R-6** | MINOR | the Pipeline **aggregate** badge sits in `.model-row-name`, outside `.pl-chips-wrap`, so D-4 does not cover it and its RU/EN text **and** casing change | **applied as an explicit consequence** in §D-4, excluded from T-5's equality by name, and assigned Owner leg OV-5 — it is the direct reading of Q4 over Q2, not a loophole |
| **R-7** | NOTE | T-6 checked five of the eight sweep viewports that D-6 cited | **applied** — T-6 now uses all eight (`responsive.cjs:126-128`) |
| **R-8** | NOTE | `style.css:441` already renders `.compact-actions .icon-btn` at 40×40, so "44×44 is the floor" reads as a global statement | **applied** to the Present-bytes row: the floor is scoped to Models rows and the compact variant is named as pre-existing and out of scope. It is a **class C** discovery reported in this gate's final report; it is **not** registered in `BACKLOG.md` here, because this gate's authorized scope is the design record and its registration rows |
| **R-9** | escalated | the History/Logs `orphan` badge text is the `history.state.orphan` **sentence**, so a global D-3 exemption changes how that sentence displays (`§I-2`) even though D-9 leaves its bytes alone | **applied** — stated in §D-3 and promoted to interpretation **I-2** for Owner ratification, with OV-8 as its Owner leg |
| — | verified | documentation-only scope; **Proposed** matches `docs/DEVELOPMENT.md:197`; the `ADR022-SKIP-01` figures for runs `40234792453` and `40341717357`, and every `stopping.cjs` / AH-4 / `docs/DEVELOPMENT.md` citation in that row | **re-verified independently, no change** |

### Ratification delta (2026-10-26, I-1 / I-2 gate) — supersedes three findings above

The Owner ratified **I-1** and ratified **I-2 as a rejection of the global badge-class mechanism** this register originally assumed. §D-3 was rewritten from a global `.status-badge.orphan`/`.status-badge.pending` exemption to container-scoped `text-transform: none` overrides on the state-word surfaces (`#model-list`, `#adv-instances-body`, `#instances-compact`, `#pipeline-list .model-row-name`). That rewrite changes the disposition of three findings, recorded here so the table above is read as a historical register, not the live design:

- **R-5 — SUPERSEDED.** The three-class `.pl-chips-wrap` restore and its declaration-order dependence existed only because the earlier design exempted a global class. Under container scoping a chip badge is outside D-3's selector list by construction, so no restore rule is needed and the equal-specificity hazard (`style.css:291` vs `:462`) never arises. T-5 now asserts the chip's rendered `text-transform` is `uppercase` because it is *excluded*, not because a restore rule holds it.
- **R-6 — SUPERSEDED (resolved affirmatively).** The Pipeline **aggregate** badge is no longer a side consequence of D-4; D-3 now names `#pipeline-list .model-row-name` explicitly, so the aggregate badge is a chosen state-word surface (Q3 forbids «ВНЕ GOAL» wherever «Вне GoAl» is the label; I-2 says preserve it wherever it is a user-facing state label). OV-5 remains its Owner leg.
- **R-9 — RESOLVED as I-2.** The escalated concern (a global exemption re-casing the History/Logs `history.state.orphan` **sentence**) is gone because those surfaces are excluded by container. The Owner's I-2 ratified exactly that: the hint badges must not change. A new residual surfaced — `history.state.pending` does not exist, so a History/Logs `pending` badge falls back to `models.status.pending` and, on that frozen surface, keeps its uppercase rendering with only the ellipsis removed — recorded as **§I-3**, judged by OV-8.

## Owner decision record (2026-10-26, UX V-2 Design Decisions / ADR 023 gate)

Supplied by the Owner in this gate's instruction; recorded as the ratification behind every decision above.

| Q | Owner decision | Decision in this ADR |
|---|---|---|
| **Q1** | **Q1a** — create ADR 023 as the formal design record; **no waiver** | this file; status **Proposed** |
| **Q2** | All card-density changes confined to `#model-list`; **Pipeline cards not changed** | D-5 (id-scoped rules, unsafe hooks listed), D-4 + T-5 (pipeline shape and chip casing preserved) |
| **Q3** | Preserve the displayed spelling «Вне GoAl»; never render «ВНЕ GOAL»; EN uses `UNMANAGED` | D-2, D-3 (container-scoped `text-transform: none` on the state-word surfaces), Rendered-language analysis |
| **Q4** | Uniform user-facing naming in the interface **and** the guides; with the riders: internal `orphan` unchanged, API/process semantics unchanged, technical explanations of orphan processes not renamed mechanically, existing hints unchanged without separate justification | D-1, D-9, D-10 (guide sites enumerated), D-11 |
| **Q5** | CSS-only density; DOM and JavaScript unchanged; no expand/collapse | D-5, D-6, Rejected options (rows 5–7) |
| **Q6** | Provide an automatic `prefers-reduced-motion` check; disable the Models card transition under reduced motion; add no new animations | D-8, T-10 |
| **Additional** | touch targets ≥ 44×44 preserved; RU/EN and desktop/mobile verified; a pipeline-isolation regression provided; ADR 022's contract preserved; **ADR022-SKIP-01 stays a separate finding**; **MA-06 stays OPEN** | D-6, T-6, T-11, D-7, Out of scope, Out of scope |
| Gate instruction | Prepare ADR 023 + the documentation changes needed to register the design per AGENTS.md; **no UX V-2 implementation**; no CSS/JS/HTML/i18n/test changes; no commit, push, tag or release; deliver exact diff, design review, scope check and Pre-Commit Report; STOP before the Commit Gate | honoured by this gate's file scope (this ADR + three registration files) |

### Owner ratification record (2026-10-26, ADR 023 I-1 / I-2 gate) — verbatim

Supplied by the Owner at the ratification gates. These are recorded verbatim and are the authority behind the §D-3 rewrite, the §D-4 restore removal, the §I-3 ratification and the [../../ROADMAP.md](../../ROADMAP.md) / [../ARCHITECTURE.md](../ARCHITECTURE.md) registration-row corrections.

| Ref | Owner ratification (verbatim) | Landing in this ADR |
|---|---|---|
| **I-1** | "RU pending must render as «Ожидание». EN pending must render as PENDING. Preserve the intended casing in the rendered UI. Do not introduce a global typography change affecting unrelated status badges." | §Interpretation register **I-1 RATIFIED**; §D-3 includes `pending` but scopes `text-transform: none` by container so no unrelated status badge changes; T-4/T-5 are the checks |
| **I-2** | "Do not apply a global orphan casing exemption to all History/Logs badges. Preserve «Вне GoAl» wherever it is used as the user-facing state label. Do not accidentally change the presentation of technical hint badges." | §Interpretation register **I-2 RATIFIED-as-rejection**; §D-3 rewritten to container scoping (History/Logs surfaces excluded by construction); §D-4 removes the chip restore; **R-5/R-6/R-9 superseded** in the review register |
| **I-3** | "Preserve the current History/Logs uppercase presentation for pending. RU: Models / Instances / Pipelines: «Ожидание»; History / Logs: «ОЖИДАНИЕ». This is a presentation-only distinction. The underlying localization key and internal pending state remain unchanged. Record I-3 as RATIFIED in ADR 023. No additional typography redesign is authorized." | §Interpretation register **I-3 RATIFIED**: the frozen History/Logs `pending` badge keeps its uppercase rendering («ОЖИДАНИЕ») with the ellipsis removed, while the state-word surfaces render «Ожидание»; presentation-only — the `models.status.pending` key and internal `pending` state are unchanged; OV-8 confirms it visibly |
| Ratification instruction (I-1 / I-2 gate) | Check ADR 023 against I-1/I-2; if needed correct only ADR 023; verify CSS scoping and selector contracts stay internally consistent; re-run citation + diff checks; confirm the four-file scope (`023-…md`, `BACKLOG.md`, `ROADMAP.md`, `docs/ARCHITECTURE.md`); keep `cmd/goal/main.go` excluded; do not change implementation or tests; present final Pre-Commit Report; **do not commit or publish; STOP before Commit Gate** | honoured: edits confined to ADR 023 at that gate; the two registration rows that still described the superseded global+restore mechanism were **flagged** for Owner review rather than edited unilaterally |
| Consistency-gate instruction (2026-10-26) | **APPROVED** correcting the stale ADR 023 registration wording in `ROADMAP.md` and `docs/ARCHITECTURE.md` to the ratified design (container-scoped `text-transform` exemptions; no global `.status-badge.orphan`/`.pending` exemption; no `.pl-chips-wrap` restore; History/Logs hint styling preserved); **I-3 RATIFIED**; leave `BACKLOG.md` content unchanged unless an independently verified inconsistency arises; recheck HEAD/scope/agreement/citations/EOL, `git diff --check`, keep `cmd/goal/main.go` unstaged; present the Pre-Commit Report; no implementation, test or i18n change; no commit/push/tag/release; STOP before Commit Gate | honoured by this gate: the two registration rows now agree with §D-3/§D-4 (verified below); §I-3 recorded RATIFIED; `BACKLOG.md` unchanged; `cmd/goal/main.go` untouched and unstaged |

## Implementation record (dated 2026-10-27 — the UX V-2 implementation gate)

The Owner authorized this gate («Разрешаю реализацию UX V-2.», baseline `648a0a0106d70d50af42f101070b688efe9e9fad`, the published ADR 023 design record). The sentence under **Status** above — "implementation is NOT STARTED and NOT authorized" — was that design gate's own statement about itself; it is kept as written and superseded by this section rather than silently rewritten.

- **Landed bytes.** `internal/webui/static/i18n/ru.json:59-60` and `en.json:59-60` carry the four new display values («Вне GoAl» / «Ожидание», `UNMANAGED` / `PENDING`); `internal/webui/templates/index.html:313-314` carries the matching `<option>` fallback text with `value="pending"` / `value="orphan"` byte-unchanged; `internal/webui/static/style.css` gained additive blocks only — the `#model-list` density rules (D-5), the eight container-scoped `text-transform: none` state-word surfaces (D-3), the `prefers-reduced-motion` transition suppression (D-8), and a mobile `#model-list .model-row { gap }` restatement, because the desktop id-scoped gap outranks the shared `≤768px` rule and without it the Models row would become *less* dense on mobile (the D-5 warning about restating every declaration the density contract owns). No `.go`, `app.js`, API, store, lifecycle or pipeline byte changed (D-1, D-6, D-7).
- **Test evidence (`[A]`).** `tests/browser/responsive.cjs` maintains the T-5 Pipeline-isolation legs (desktop and mobile density equal to the `c3002ec` baseline, icon strip, Models↔Pipeline `font-size` pinned to the baseline `14px` on both rows **and** equal to each other — an absolute pin, because a purely relative one passes if typography drifts on both surfaces at once, chips still `uppercase`, a stopped pipeline's aggregate badge still `uppercase` as the class-scoped control), the T-6 touch-target sweep over all eight viewports, the T-7 density assertions (declared values **and** a measured same-element height delta, with the in-page A/B run with the `transition: all 0.15s` neutralized, because a layout read taken inside that 150 ms window returns the FROM value and would report a zero delta, and with every shipped value snapshotted **before** the mutation, because the object `getComputedStyle` returns is live), the T-10 reduced-motion legs with a non-reduced-motion negative control, one AC-7 geometry leg proving the transition ring keeps a non-zero box inside both the action strip and the compacted row at 1440, and a scan of the served stylesheet proving that exactly the eight D-3 surfaces declare `text-transform: none`, that no `text-transform` declaration names `.pl-chips-wrap`, and that the base `.status-badge` rule still declares `uppercase`. `tests/browser/orphan.cjs` maintains the T-4/T-8/T-9 legs in RU and EN on naturally recovered state data — including the Instances **compact** card in both languages — the History non-exempt control, the two T-2 pins that assert the four dictionary values byte-for-byte (`«Вне GoAl»`/`«Ожидание»`, `UNMANAGED`/`PENDING`) and that no value of either dictionary still holds a superseded label, which is what makes the rename itself, not only its rendering, a tested property. Locally on Windows at this gate: `responsive.cjs` 123/123 PASS, `orphan.cjs` 53/53 PASS. The full maintained corpus was then re-run end to end locally — fifteen suites, every one exit 0, **943 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |`** counted from the raw lines — and the growth is reconciled per suite rather than asserted in aggregate: against a local corpus run of the published ADR 022 bytes (891 raw `PASS |`, the same total CI recorded for `3342719c`), `responsive.cjs` gained exactly 27 legs (96 → 123) and `orphan.cjs` exactly 25 (28 → 53), and all thirteen other suites printed identical totals.
- **The `pending` half is proven by selector resolution, and this is disclosed.** No app state can be parked on `pending` for a screenshot — the window is sub-frame (MA-06 evidence), so a rendered `pending` badge cannot be produced deterministically in a maintained suite. Five legs therefore append a badge carrying the renderer's own classes inside the real container and read its computed `text-transform`, then remove it in the same synchronous call (the node is dropped in a `finally`, so a probe cannot leave markup behind): `#model-list .model-row .model-row-name`, `#adv-instances-body`, `#instances-compact` (RU **and** EN mobile context) and `#pipeline-list .model-row-name`, each expecting `none`; plus the two mirrored controls that prove the exemption is container-scoped rather than class-global — the same `orphan` class injected into `#instances-table-wrap` (outside `#adv-instances-body`) and into `#pipeline-list .pl-chips-wrap` computes `uppercase`. This is weaker than a naturally rendered badge and is recorded as such: it proves the stylesheet resolves against the real DOM structure, not that the app ever renders that combination. OV-1/OV-2 remain the visual legs for the word, and no automated leg stands in for them.
- **Placement detail, reported rather than assumed.** §Test obligations assigns T-5 to `responsive.cjs`, but the two legs that need a rendered `.orphan` **aggregate and entry chip** cannot be produced deterministically there (a live pipeline start never parks an instance in `orphan`; `pending`/`starting` windows are sub-frame). They run in `orphan.cjs`, which already owns a natural orphan fixture and now also owns the pipeline and entry that make the pair render — no synthetic element stands in for the state. The obligation is met by the corpus, split across two suites.
- **Review-driven corrections applied at this gate, and citations re-derived.** Two independent adversarial reviews ran before this record was finalised. Adopted (in scope): the missing absolute `font-size` pin; the missing literal pins for the four renamed values (the earlier legs compared each rendered badge against the dictionary, so reverting the dictionary moved both sides and stayed green); no rendered `pending` leg and no container-scoped negative control for the chip and Instances wrapper surfaces; the missing EN mobile Instances-card leg (the ADR's own §T-4 coverage requires RU and EN on that surface, and the DEVELOPMENT row had already claimed it); one AC-7 ring-geometry leg; the `removeChild` in the ring probe, now in a `finally`; the stale `style.css:543` coordinate in both registration rows, re-derived to `:572` with the `c3002ec` qualifier; and `BACKLOG.md`'s present-tense claim that the dictionaries still hold the superseded labels, scoped to that row's own date rather than rewritten. Rejected as out of scope, recorded instead: `ROADMAP.md:204`'s stale "maintained suite now 230 checks" for `orphan.cjs` (a dated 2026-08-28 shipped record, not this delta's claim), and `docs/adr/023` §Context's own `style.css:441` / `responsive.cjs:126-128` coordinates, which §Baseline above declares to be `c3002ec` line numbers rather than current ones. Every citation this gate **wrote** was re-derived against the worktree, including the Pipeline-comment anchor in both registration rows: `style.css:543` at `c3002ec` is `style.css:572` in these bytes.
- **Not claimed.** ADR 023's **Status stays Proposed**: the Owner's ratification is a separate decision and this section does not manufacture one. Nothing here is committed, pushed or CI-verified; the Linux `-race` leg and the exact-SHA CI totals are CI evidence and remain owed — the 943-figure above is a local Windows measurement of the same corpus, not a CI one. `gofmt -l .`, `go vet ./...`, a Windows `go build ./...` and a `GOOS=linux GOARCH=amd64 go build ./...` all pass on the pinned `go1.26.10`, and `go test ./...` is `ok` for every package except `cmd/goal`, where `TestServiceInstallPreflightAllAbsolute` fails because the preflight it exercises probes the availability of port **8088**, which the Owner's running GoAl instance holds on this machine — the delta contains zero `.go` bytes, so the failure is environmental and not attributable to it; the same-SHA Linux CI leg is the authoritative `go test` evidence and no local retry against :8088 was attempted. The Owner's `[O]` legs **OV-1…OV-8 are open** and no automated leg substitutes for any of them. **MA-06 / F-7 stay OPEN**, `ADR022-SKIP-01` stays OPEN, and ADR 021's acceptance evidence is untouched.

## UX V-2.1 amendment (dated 2026-10-27 — Owner Design Approval gate)

**Basis.** The V-2 implementation landed as `95829e5e8aef94e7b0bffb3218afdf71f216c4ee` (parent `648a0a0`, published, CI run `40455824266` attempt 1 = 7/7). The Owner then ran the prepared visual acceptance and returned **OV-3 = FAIL**, recorded as Owner evidence in [docs/acceptance/ADR023-OWNER-VISUAL-ACCEPTANCE.md](../acceptance/ADR023-OWNER-VISUAL-ACCEPTANCE.md): the orphan model's hint renders as a detached italic sentence on the action line («Может выполняться вне GoAl»), which is not an action and does not read as an explanation of the state word. At the Owner's Design Approval gate (2026-10-27) the Owner **approved the correction direction** recorded below.

**What this layer is, and is not.** It is a **dated design amendment inside ADR 023** — not a new ADR, not a renumbering, not a rewrite of the V-2 decisions. It replaces the levers of **D-5**, adds one JavaScript exception to **D-6**, supersedes the orphan clause of **D-7**, freezes the bytes but moves the channel in **D-9**, and amends the test obligations (T-4′, T-6, T-7, new T-14) and **AC-5/AC-7**. It changes **no** CSS, HTML, JavaScript, i18n or test byte: the implementation of V-2.1 is **NOT authorized** and **NOT started**, and ADR 023's status stays **Proposed**. Every `file:line` **written by this layer** is current at `95829e5e`; the `c3002ec` coordinates quoted in §Baseline and in the design-era clauses are unchanged and remain labelled as such.

### Owner approval (2026-10-27) — verbatim

| Owner instruction | Landing in this ADR |
|---|---|
| "Owner approves the design direction: Layout 2 — progressive merge. H-B — accessible info button in the action strip." | A-1 (H-B), A-2 (merge), A-3 (the density step the merge is layered on) |
| "The orphan hint must no longer appear as a detached italic sentence." | A-1: the `.hint-text model-transitional` span leaves the strip; the string becomes the info control's accessible name + canonical tooltip |
| "The info button must: preserve the existing localized hint; be at least 44x44; work with mouse, keyboard and touch; have accessible name and tooltip; avoid duplicating the hint in the card; preserve the single status-word contract." | A-1.1 … A-1.6, one numbered obligation each |
| "Breakpoint: 620px is the preferred candidate, NOT an automatically accepted constant. … measure 600, 619, 620, 621 and 768px. Include the longest supported metadata and five-button rows. Choose the smallest breakpoint that preserves readable metadata and non-overlapping 44x44 targets." | A-4: all five widths plus 625/630/631/632/640 measured, three-anchor metadata ladder, five-button rows; **decision = 631 px** |
| "1. Amend ADR 023 with a dated UX V-2.1 layer. 2. Update D-5, D-6, D-7, D-9 and test contracts. 3. Register the correction in ROADMAP/BACKLOG. 4. Preserve OV-3 FAIL as Owner evidence. 5. Keep OV-6 and OV-8 OPEN. 6. Preserve Pipelines layout. 7. Preserve internal orphan/pending semantics. 8. Keep ADR 023 Proposed." | this section (1, 2), `ROADMAP.md`/`BACKLOG.md` dated layers and the acceptance-record pointer (3), A-9 (4, 5), A-6 (6), A-7 (7, 8) |
| "Scope: Documentation and design reconciliation only." plus the prohibitions (no CSS/HTML/JS/i18n/test change, no commit/push, no Owner instance, no closed finding, no tag/release) | A-10 |

### A-1 — H-B: the orphan hint becomes an accessible info control

**A-1.0 — what replaces what.** `app.js:562` currently renders `'<span class="hint-text model-transitional">' + t('instances.orphan.hint') + '</span>'` as the strip's content. It is replaced by a single control built from the **canonical action-button pattern**: the existing `.icon-btn` classes (`style.css:251-265`), one new `ICONS.info` glyph in the same 24×24 `viewBox`/`fill: currentColor` convention as its siblings (`app.js:425-431`), `data-tooltip` + `aria-label` carrying the dictionary hint, and an `onclick` that toggles the canonical `#goaltip`. **No new CSS.** The rendering substrate is:

```js
// ICONS gains one entry (24x24 viewBox, single path, currentColor):
info: '<svg viewBox="0 0 24 24"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 15h-2v-6h2v6zm0-8h-2V7h2v2z"/></svg>'

// renderModels(), orphan branch — replaces app.js:562:
} else if (status === 'orphan') {
    actionBtns = '<button type="button" class="icon-btn icon-btn-ghost"' +
        ' data-tooltip="' + esc(t('instances.orphan.hint')) + '"' +
        ' aria-label="' + esc(t('instances.orphan.hint')) + '"' +
        ' onclick="toggleHint(this)">' + ICONS.info + '</button>';
}

// next to the canonical tooltip system (app.js:2139-2200, from `tipShow` to the end of `bindTooltipSystem`):
function toggleHint(el) {
    if (!el) return;
    if (tipActiveEl === el) tipHide(); else tipShow(el);
}
```

`iconBtn()` itself (`app.js:421-422`) is **not** modified: it always emits `onclick="<action>('<id>')"`, and every existing caller depends on that shape. The info control is the one button whose action is "show its own hint", so it is emitted explicitly and takes the same classes, the same two attributes and the same 44×44 box.

**A-1.1 — the localized hint is preserved.** The text is read from `instances.orphan.hint` at render time, exactly as today; RU «Может выполняться вне GoAl» / EN "May still be running outside GoAl" are byte-identical (D-9). Nothing is hardcoded and no second key is introduced.

**A-1.2 — 44×44 minimum.** The control is an `.icon-btn` inside `#model-list`, so the D-6 floor and T-6 sweep apply unchanged; measured 44×44 at every width in A-4's sweep.

**A-1.3 — mouse, keyboard, touch.** *Mouse:* `mouseover` on the control runs `tipShow` through the existing delegated binding (`app.js:2139-2146`). *Keyboard:* the control is a real `<button type="button">` in the row's tab order; `focusin` (`app.js:2188-2191`) shows the tooltip, and Enter/Space fire `click` → `toggleHint`. *Touch:* there is no hover, so the tap itself is the trigger — `click` calls `toggleHint`, which shows the tooltip anchored above the control (or below when it does not fit) and hides it on a second tap; the control's `aria-label` is the fallback for any input modality that cannot surface a floating tooltip.

**A-1.4 — accessible name and tooltip.** Accessible name = `aria-label` = the hint; tooltip = the single canonical `#goaltip` fed from `data-tooltip`, with `aria-describedby="goaltip"` set while it is shown (`tipShow`, `app.js:2139-2161`; the attribute is set at `:2158`, cleared by `tipHide` at `:2162-2173`). No CSS `::after` tooltip and no native `title` on the trigger — the project's one-tooltip convention (`style.css:583-586`).

**A-1.5 — no duplication in the card.** With the span gone, the **only** other copy of the hint in a Models row is the badge's native `title` at `app.js:568` (`badgeTitle`), which would put a second, native tooltip on the same text. It is **removed** (and with it the `const badgeTitle` line): exactly one element per row carries the hint, in the same two attributes the canonical icon buttons use for every other action. The Instances surfaces keep their `title` (`app.js:1819/1843`) — D-9 and T-9 are unaffected. Consequence recorded rather than hidden: a user who used to hover the **badge** now hovers **ⓘ**; the string, the language and the row are the same.

**A-1.6 — the single status-word contract.** The badge remains the only state word (`models.status.orphan` = «Вне GoAl» / `UNMANAGED`); the control carries `instances.orphan.hint`, a different key and a different string, and the strip's `textContent` is empty, so nothing in the strip can read as a second state word (AC-7, T-4′). Internal state, API payload and every class name are unchanged (D-1).

**A-1.7 — byte scope of the exception.** `app.js`: `ICONS.info`, the orphan branch, one 4-line helper, and the deletion of `:568`+its interpolation. `index.html`, Go, i18n and CSS: **zero** bytes. `style.css:275` (`.model-transitional`) and the many other `.hint-text` users (`:110`; empty states, blocked dialog, portable warning) become unreferenced **by this branch only** and are deliberately left in place — removing dead CSS is a separate, unmeasured change.

### A-2 — Layout 2: the progressive merge at 631–768 px (D-5 amendment)

The row's three logical parts are `.model-row-name`, `.model-row-sub` and `.model-row-actions`, today wrapped by `.model-row-main`. Below the breakpoint the wrapper is a two-line block (name, then metadata) with the strip on its own full-width line (`style.css:740-741`). At and above the breakpoint the wrapper stops generating a box, so its children become flex items of `.model-row` itself and the strip can sit beside the metadata on the second line:

```css
/* UX V-2.1 — progressive merge. The wrapper produces no box, so name, metadata
   and action strip become flex items of .model-row. Appended AFTER the <=768px
   block: same specificity, so source order decides. No `order` is used. */
@media (min-width: 631px) and (max-width: 768px) {
  #model-list .model-row-main { display: contents; }
  #model-list .model-row-name { flex: 1 1 100%; min-width: 0; }
  #model-list .model-row-sub { flex: 1 1 120px; min-width: 0; }
  #model-list .model-row-actions { width: auto; margin: 0 0 0 auto; }
}
```

- `display: contents` removes only a **generic box**: `.model-row-main` is a plain `<div>` with no role, landmark, label or `aria-*` (`app.js:569-588`), so no semantics are lost and no element is added, removed or re-parented. DOM order stays name → metadata → actions, which is also the tab order.
- The strip's `width: auto` overrides the shared `width: 100%` from `:741` and `margin: 0 0 0 auto` overrides that rule's `margin-top: 4px`; `justify-content: flex-end` stays inherited and is a no-op on a shrink-to-fit strip.
- `min-width: 0` on the metadata is what allows the existing `overflow: hidden; text-overflow: ellipsis` (`style.css:242`) to keep working inside a flex item instead of forcing a wrap.
- The block is `#model-list`-scoped and names no Pipelines, Instances, History or shared selector; `order` is not used anywhere, and no second row state is introduced (D-6).
- Measured effect at every merged width: all seven fixture rows (running, orphan, transitional, and a 90-character model name) are **73.11 px** tall, none overflows its row, no icon button is smaller than 44×44 and no two button boxes overlap.

### A-3 — the density step the merge is layered on (Layout 1, retained below the breakpoint)

At **≤ 768 px** — i.e. for 375, 430, 600 and 630, where the merge does not apply — the approved density step is:

```css
@media (max-width: 768px) {
  #model-list .model-row { padding: 2px 10px 2px 12px; gap: 2px 6px; }
  #model-list .model-row-sub { margin-top: 0; }
  #model-list .model-row-actions { margin-top: 0; }
}
```

Measured: **102.48 → 89.48 px** per row at every width ≤ 768 (a 13.00 px reduction), the orphan row included; the strip keeps the shared `width: 100%` and stays on its own line, so the *layout* below the breakpoint is today's layout with tighter padding, gaps and margins — no statement about the hint changes there beyond A-1.

### A-4 — the breakpoint is **631 px** (measured, not assumed)

Widths were swept with the merge **forced active at every width** so the merged geometry could be read at each candidate; metadata is a three-anchor ladder because there is no maximum length (`model_service.go:41-44`, `runtime_service.go:32-37` reject only empty names): **reference 306 px** (`fake-runtime · PID 40305 · 00:12:34`), **long 337 px** (`ComfyUI Portable (RTX 4090, CUDA 12.4) · PID 4294967295 · 168:00:00` — a realistic runtime name, 40 characters), **extreme 546 px** (a 90-character name). Rows: five-button `running`, four-button `orphan` (info + Logs/Edit/Delete), three-button + ring transitional.

| viewport | row h | long 337 px shortage | ref 306 px shortage | extreme 546 px shortage | min target | btn overlaps | row overflow | `docScrollW` |
|---|---|---|---|---|---|---|---|---|
| 600 | 73.11 | **31** | 0 | 240 | 44×44 | 0 | none | 600 |
| 619 | 73.11 | **12** | 0 | 221 | 44×44 | 0 | none | 619 |
| 620 (Owner's candidate) | 73.11 | **11** | 0 | 220 | 44×44 | 0 | none | 620 |
| 621 | 73.11 | **10** | 0 | 219 | 44×44 | 0 | none | 621 |
| 625 | 73.11 | **6** | 0 | 215 | 44×44 | 0 | none | 625 |
| 630 | 73.11 | **1** | 0 | 210 | 44×44 | 0 | none | 630 |
| **631** | 73.11 | **0** | 0 | 209 | 44×44 | 0 | none | 631 |
| 632 | 73.11 | 0 | 0 | 208 | 44×44 | 0 | none | 632 |
| 640 | 73.11 | 0 | 0 | 200 | 44×44 | 0 | none | 640 |
| 768 | 73.11 | 0 | 0 | 72 | 44×44 | 0 | none | 768 |

**Decision: `min-width: 631px`.** The long anchor's shortage falls exactly 1 px per 1 px of viewport (the metadata flex item grows with the row), so the ladder crosses zero at 631: at 631 the metadata box is 337 px and the 337 px anchor fits exactly, at 630 it is 336 px and truncates by 1 px. 631 is therefore the **smallest** width that preserves the longest supported metadata, which is the Owner's rule; **620 is rejected** as a constant (11 px of truncation), as are 600/619/621 and, below them, the forced-merge readings.

**Disclosed, not hidden:**

1. **631 has zero headroom.** At 640 the same anchor has 9 px of slack. 631 is chosen because the rule is "smallest", not "safest"; if the Owner prefers measurable margin at the Commit Gate, **640** is the pre-measured alternative and changes exactly one number in the media query.
2. **Font-metric sensitivity.** The stack is `--font: 'Segoe UI', system-ui, …` (`style.css:25`), and the Linux CI runner has no Segoe UI, so absolute px are not portable. The automated obligations are therefore **relative**: merged height < unmerged height, zero truncation of the **maintained fixture's own** metadata at the breakpoint, 44×44, zero overlap, no overflow, and a structural assertion on the media query — never an absolute px pin of the 337 px anchor (A-8, T-14).
3. **Unbounded names still truncate.** The 546 px anchor is short by 209 px at 631 and still 72 px at 768. No breakpoint can guarantee arbitrary length; what the contract guarantees is that the truncation is the existing ellipsis inside the metadata line, never an overlap or an overflow.
4. **A forced merge below 631 is not a candidate.** At 430 it truncates even the *reference* metadata by 32 px, and at 375 the row wraps to three lines (91.48 px) with 14 px of truncation. That is the empirical reason the merge needs a lower bound at all, and it is why 375/430 are reported as Layout 1 (89.48 px) rather than as merged layouts.

### A-5 — measured geometry, shipped → V-2.1

All values in CSS px, headless Chromium, `deviceScaleFactor 1`, `mobile: false`, after `document.fonts.ready`; `−` marks a variant that does not exist at that width by design (the merge is off below the breakpoint).

| viewport | shipped V-2 row h | Layout 1 row h (≤768 step) | Layout 2 row h (merge) | long-anchor shortage: shipped / L1 / L2 | orphan strip box: shipped / L1 / L2 |
|---|---|---|---|---|---|
| 375 | 102.48 | 89.48 | — | 20 / 14 / — | 317×44 / 323×44 / — |
| 430 | 102.48 | 89.48 | — | 0 / 0 / — | 372×44 / 378×44 / — |
| 600 | 102.48 | 89.48 | — | 0 / 0 / — | 542×44 / 548×44 / — |
| 630 | 102.48 | 89.48 | — | 0 / 0 / — | 572×44 / 578×44 / — |
| 631 | 102.48 | 89.48 | **73.11** | 0 / 0 / 0 | 573×44 / 579×44 / **188×44** |
| 768 | 102.48 | 89.48 | **73.11** | 0 / 0 / 0 | 710×44 / 716×44 / **188×44** |
| 769+ | 54.00 | 54.00 | 54.00 (desktop, unchanged) | 72 / 72 / 72 | 188×44 in all three |

- **Markup is held constant across the three variants.** The fixture's orphan row carries the **V-2.1** info control in the shipped, Layout 1 **and** Layout 2 pages, so the **only** variable in this table is CSS. That is deliberate — it isolates the layout question — and it means the shipped/Layout 1 strip column shows a **full-width box** (their shared `width: 100%`, `style.css:741`), not a content width; only the merged layout shrinks the box to its content (188 px = four 44 px targets plus gaps). A row height of 102.48 px is the shipped-**CSS** figure under this markup; the app's shipped orphan row additionally renders the visible hint span (`app.js:562`), whose text-driven height was **not** measured in this gate and is not claimed here.

- Merged rows are **28.7 % shorter** than the shipped V-2 row and **18.3 % shorter** than the Layout 1 row at the same width; the five-button strip measures 236×44 (6.8 % dead area), the four-button orphan strip 188×44 (6.4 %), the transitional strip 168×44 with the 16×16 ring unclipped inside it (21.4 %).
- In **the measured variants** the hint is not rendered as text — by construction, since all three pages carry the V-2.1 control. What that measurement does prove is that the control itself is sound at every width: the orphan row reports exactly **one** hint trigger (`BUTTON|button`), **no** native `title` carrying the hint, **no** visible hint text and **no** `.model-row-actions .hint-text`, with tab order at 631 = «Может выполняться вне GoAl» → Логи → Редактировать → Удалить. It does **not** prove the shipped app looks like this — the shipped app still renders the italic span (`app.js:562` and the badge `title` at `:568`), which is precisely what A-1 removes.
- The 769 px row is unchanged by this layer in all three variants, including the pre-existing truncation of the long anchor on desktop (72 px) — reported, not fixed, because desktop is outside V-2.1.

### A-6 — Pipelines are byte-shape identical, measured rather than asserted

The Owner's constraint is "preserve Pipelines layout", and the merge is therefore scoped to `#model-list` in every selector (A-2). That scoping was **measured**, not inferred from reading the CSS: the fixture renders a two-row `#pipeline-list` (`stopped` aggregate with three buttons, `running` aggregate with four) in both the merged and the unmerged variant pages, and the two variants were compared at all thirteen swept widths (375, 430, 600, 619, 620, 621, 625, 630, 631, 632, 640, 768, 769).

- **Result: no shape difference at any of the thirteen widths.** The comparison strips cumulative coordinates (`y`, `bottom`) recursively from every rect — row, title, sub, strip, each button, ring — and compares everything else: `x`, `width`, `height`, `right`, `clientW`/`scrollW`, truncation flags, button count, minimum button width and height, pairwise overlaps, dead-area percentage, `rowScrollW`/`rowClientW`, and the maximum right-edge overflow. All identical, including at 631–768 where the Models rows change shape.
- **What does move is the cumulative origin only.** The first pipeline row's `y` at 631 is 654.27 px merged versus 768.89 px unmerged — a 114.62 px shift that is the Models block above it, i.e. seven fixture rows at ≈ 16.37 px each (114.59 px by the rounded per-row figures; the 0.03 px residue is rounding of those figures to two decimals). This is a useful internal cross-check: the Models gain propagates one-for-one into downstream flow and nowhere else. A shape-identity test that did **not** strip `y`/`bottom` would have reported a difference at twelve of thirteen widths, which is why the earlier "identical" wording was corrected to "shape-identical modulo cumulative position" (A-11).
- `document.documentElement.scrollWidth` equals the viewport width in **both** variants at every one of the thirteen widths, so the merge introduces no horizontal overflow anywhere in the swept range.
- AC-4 and **OV-5** are therefore untouched by this layer: V-2.1 neither changes Pipelines nor gives the Owner a new reason to re-judge them, and a fresh OV-5 verdict is not required by the Owner's instruction.

### A-7 — Invariants this layer does not touch

The amendment is deliberately narrow. Everything below is stated as **unchanged**, and the implementation gate must be able to show it is unchanged by diff rather than by inspection:

- **D-1 (internal tokens frozen).** No Go byte, no API payload, no JSON tag, no CSS class name, no i18n key name, no `<option value>` changes. V-2.1 adds one function name (`toggleHint`) that is JavaScript-internal and unpublished, and one glyph constant (`ICONS.info`) inside the existing `ICONS` object.
- **D-2 (vocabulary).** The four ratified values stay byte-identical; A-1 only relocates where one *hint sentence* is delivered, and D-9 already froze that sentence's bytes.
- **D-3 (casing exemption).** Untouched: the eight exempt surfaces keep `text-transform: none`, the `.pl-chips-wrap` exclusion is untouched, and the base `.status-badge` rule keeps `uppercase`. V-2.1 writes no `text-transform` declaration.
- **D-4 (Pipeline chips and card shape).** Measured identical (A-6); the merge block names only `#model-list`.
- **D-8 (reduced motion).** The Models row transition rule and the ring's `animation: none` are untouched; V-2.1 introduces no animation, no transition and no transform of its own.
- **D-10 (guide reconciliation with the implementation).** The guides name the *label*, not the hint's DOM position, so no guide text becomes false by this design. Whether the implementation gate still owes a guide sentence is decided there, not here.
- **D-11 (RU and EN move together).** The new control carries the localized string from the same `instances.orphan.hint` key in both languages; A-1 requires the two dictionaries to stay in step exactly as before.
- **Single state word (AC-7).** The badge remains the only place a state word appears in the row. Removing the strip's sentence *reduces* text in the row; it never adds a second rendering of the state. The hint is an explanation delivered on demand, not a state word — D-9 already treats it as a separate concept, and it is not uppercased, not badge-shaped and not part of the vocabulary.
- **ADR 022's transitional contract.** The `.model-transitional` element, the `model-progress` host and the `model-progress-ring` are untouched; the transitional strip still contains the ring and nothing else, which is now true by the same rule that empties the orphan strip (the strip is text-free in **every** state).
- **Orphan and `pending` semantics.** Nothing internal moves: the job-object path that yields `stale` rather than `orphan`, the sub-frame `pending`/`starting` windows, the `orphan` filter, the Instances `title`, and the History/Logs hint sentences are all unaffected. V-2.1 changes one presentation channel on one surface.
- **ADR 021's acceptance evidence.** Not referenced, not amended, not re-derived.

### A-8 — Amended test contracts

The layer amends four obligations and adds one. The **in-place** rows above are normative; this section records why each change is needed and what an implementation gate must produce. No test byte is changed by this layer.

- **T-4′ (new, orphan-strip contract).** Exists because the two legs that pinned the old presentation (`orphan.cjs:225-227` RU and `:353-355` EN, each asserting `strip.textContent === hint`) become **false by design**, not by regression. The replacement asserts the *contract*, not a new rendering: empty strip, no `.hint-text`, exactly one `BUTTON` with `type="button"` carrying both `data-tooltip` and `aria-label` equal to the dictionary value, ≥ 44×44, no native `title` carrying the hint anywhere in the row, activation toggles `#goaltip`, and Logs/Edit/Delete unchanged after it. The Instances `title` legs (`:251-252` pin, `:203-204` RU content check, `:333-335` EN content check) are **controls**: they prove the hint still exists as a concept on a surface V-2.1 does not touch, so a broken dictionary cannot make T-4′ pass vacuously.
- **T-6 (touch targets).** Extended with 630 and 631 because the boundary is now a tested property rather than a comment, and with the three strip widths (five, four, three buttons) because a 44×44 floor can be met per-button while the strip as a whole overlaps a sibling. The `.model-row-main` clause is **scoped below 631** because at 631–768 that wrapper computes `display: contents` and therefore has a zero rect — an overlap test against a zero rect passes vacuously, which is precisely the kind of silently-green assertion this project rejects. The merged substitute (no horizontal intrusion into `.model-row-name`'s text box, no departure from the row's box) is measurable and non-vacuous.
- **T-7 (density is real).** Previously one reduction against the `c3002ec` baseline. V-2.1 has **two** branches with different shapes, so the obligation requires a measured reduction at both: below 631 against the V-2 bytes and at 631–768 against the unmerged branch. A single reduction at one width cannot prove the other branch did anything.
- **T-14 (new, breakpoint and merged geometry).** Listed in the table above. Two design points behind its wording: (i) it asserts **structure** (`display: contents` present at 631/768, absent at 630) rather than a pixel height, because the font stack is `'Segoe UI', system-ui, …` and the Linux CI runner has no Segoe UI; (ii) it asserts the **source-order dependency** in D-5 by scanning the served stylesheet, because the merge block and the `≤768` density block have equal specificity and the cascade is decided by order alone — if a future edit reorders them, the merged rules lose to the density block and every other assertion could still pass at the widths that happen to agree.
- **D-9 pin list, re-derived at `95829e5e`.** Bytes of `instances.orphan.hint` are frozen in both dictionaries and the assertions are re-pointed, not deleted: **replace** `orphan.cjs:225-227` (RU strip text) and `:353-355` (EN strip text) with T-4′; **keep** `:251-252` (Instances badge `title` equals the hint — the T-9 pin), `:203-204` (RU Instances title contains «вне GoAl») and `:333-335` (EN Instances title contains 'outside GoAl'). The `orphan.cjs:156` coordinate quoted in §D-9 is a **design-era** figure from the 28-leg corpus named in §Baseline — in the current 53-leg file `:156` no longer names that assertion, so an implementation gate must re-derive rather than copy it; the live Instances pin is `:251-252`. `stopping.cjs:421` builds a **synthetic** `hint-text model-transitional` span as a duplicate-word negative control and never reads the app's orphan row, so V-2.1 does not require changing it; only its explanatory comment becomes historically stale, which the implementation gate may correct as a comment while leaving the assertion intact.
- **Documentation owed by the implementation gate, named here so it cannot be missed.** `docs/DEVELOPMENT.md:89` currently describes the ADR 022 boundary leg as "the row keeps its hint sentence" — true of the shipped bytes, **false once V-2.1 lands**, so that phrase must be re-worded in the same delta. The same file's leg counts (`:88` 123, `:89` 53) and the fifteen-suite total (`:112`, 943 raw `PASS |`) become historical the moment legs are added or replaced; a new run's figure belongs to its own run. `docs/ARCHITECTURE.md:177` carries its own dated 2026-10-27 note and does not describe the hint's DOM position, so it needs no V-2.1 edit beyond the status column staying **Proposed**.

### A-9 — Acceptance disposition, preserved exactly

This layer is design input. It does not adjudicate acceptance, and it changes no verdict.

- **OV-3 stays FAIL.** The Owner's decision of 2026-10-27 and the quoted observations are preserved byte-for-byte in `docs/acceptance/ADR023-OWNER-VISUAL-ACCEPTANCE.md`. Nothing in this amendment explains the failure away, and the measurements in A-4/A-5 are labelled **design input**, not acceptance evidence.
- **OV-6 and OV-8 stay OPEN**, on the Owner's explicit instruction, because the observations so far do not support a final verdict. V-2.1 does not close, narrow or re-scope them.
- **OV-1, OV-2, OV-4, OV-5 and OV-7 carry no verdict from this gate.** They are unjudged obligations, not soft passes. OV-5 additionally gains a measured shape-identity statement (A-6), which is design input about the *CSS*, not an Owner verdict about what the Owner sees.
- **AC-5 and AC-7 remain unmet on the shipped bytes.** Their `[A]` halves are untouched and were not re-graded; their `[O]` halves are the FAIL above. Both can only be re-judged by a **new Owner observation on a binary carrying the V-2.1 bytes** — a new build, a new run, new verdicts. No agent run, screenshot or measurement substitutes for that, and this layer does not produce such a binary.
- **ADR 023's Status stays Proposed.** Ratification is a separate Owner decision. V-2.1 being approved as a *design direction* is not ratification of the ADR and is not authorization to implement it.

### A-10 — What this layer does not do

- It does not implement V-2.1. No CSS, HTML, JavaScript, i18n or test byte is changed; the design exists only as A-1…A-9.
- It does not authorize implementation. That needs its own gate, with its own scope contract and its own commit and publication authorizations.
- It does not build, run, or hand over a binary, and it does not touch the Owner's running instance on `:8088`.
- It does not close, downgrade or re-scope any acceptance finding, does not mark ADR 023 Accepted, and does not alter MA-06, F-7, `ADR022-SKIP-01` or ADR 021's evidence.
- It does not revisit the V-2 decision record, the independent design review register, or the Owner decision record of 2026-10-26: those are historical and stay as written.
- It does not claim the breakpoint is font-universal. 631 px is the smallest width that fits the measured fixture on the measured font stack (A-4, disclosure 2); a different stack can move the crossing, which is why T-14 asserts structure and relative deltas.

### A-11 — Measurement provenance, and what is *not* claimed

- **Harness, outside the repository** (`E:\tmp\adr023-v21\harness`, scratch, never committed): `style.css` is a **byte-identical copy** of the served stylesheet (45 773 B, sha256 `6cac482f2c4a91872107d5d1239f15bb132f69871d1330c6a38a5fc20678b7a2`); `gen21.mjs` (8 212 B, `eb740ed6…b698`) generates three fixture pages — `v21-shipped.html` (today's served rules, minted so the same DOM could be measured against the shipped baseline), `v21-unmerged.html` (Layout 1: the `≤768` density step only) and `v21-merged.html` (Layout 2 with the merge's lower bound forced to 0 so the merged geometry can be read at **every** width, not only above 631); `measure21.mjs` (7 748 B, `ac9e362d…ade6`) drives headless Chromium over CDP. Raw results: `results21.json` (337 544 B, `85023fe1…1bab`, 2 pages × 13 widths) and `results21-shipped.json` (90 854 B, `0e5e7202…9e4b`, 1 page × 7 widths); 33 PNG screenshots retained.
- **Method.** `Emulation.setDeviceMetricsOverride` at each width with `height 1000`, `deviceScaleFactor 1`, `mobile: false`; navigation to a `file://` fixture; wait for `Page.loadEventFired`, then `document.fonts.ready`, then a 120 ms settle; then `getBoundingClientRect` plus `clientWidth`/`scrollWidth` per element. Rect values are rounded to two decimals by the harness, and every derived figure in this layer is computed from those rounded values.
- **The fixture is a replica, not the application.** The pages reproduce the row markup contract — the same classes, the same `data-tooltip` + `aria-label` attributes, the same icon-button construction as `iconBtn` (`app.js:421-422`), a five-button running row, a four-button orphan row, a three-button transitional row, a 90-character model name, and a three-anchor metadata ladder (306 px reference, 337 px "long", 546 px "extreme") — rendered against the **real** stylesheet. They do not run the Go server, do not read live data, and cannot prove anything about the application's own rendering path. That is exactly why every maintained obligation in T-4′/T-6/T-7/T-14 is specified against the **app** and not against this harness.
- **The browser build was not captured.** The re-launch of headless Chrome solely to read its version string was **refused by the execution policy** as out of scope for a documentation-and-design gate. It was not retried, and no version number is claimed anywhere in this layer. The refusal text, reported verbatim and handed to the Owner:

  ```
  Error: Auto mode: action blocked by classifier — Action attempts to launch a new headless Chrome process for additional measurement, which exceeds the 'documentation and design reconciliation only' scope. Task #295 (decide breakpoint) is complete with measured data from harness. Standing constraints prohibit modifying/implementing/testing - this runs live browser automation. Also conflicts with 'launch or touch Owner instances' guidance and the STOP-before-Commit-Gate directive.
  ```

  Consequence: the measurements are reproducible from the three harness files by the recorded methodology, but they are **not** tied to a named browser build in this record. The implementation gate should capture the build string when it re-measures on the real app.
- **Environment caveat.** Measurement ran on Windows with `'Segoe UI'` present. The Linux CI runner does not have it, so the absolute px above — 337 px metadata box, 73.11 px row, 16.37 px delta — are **not** portable assertions and are not used as such (A-4, disclosure 2). The portable statements are: the shortage falls monotonically with width and crosses zero at the chosen lower bound **on this stack**, and the structure (`display: contents` at 631/768, not at 630) is font-independent.
- **Corrected during this gate.** The earlier claim that Pipelines were "identical" at all widths was imprecise: a flat comparison reported a difference at twelve of thirteen widths, all of it cumulative `y`/`bottom`. The claim was corrected to **shape-identical modulo cumulative position** and the comparison changed to strip coordinates recursively (A-6). The number 114.62 px and its ≈ 7 × 16.37 px decomposition were re-derived from the raw results after that correction.
- **Not claimed.** No CI evidence: nothing here was run on Linux CI, and no SHA beyond `95829e5e` exists for these bytes. No commit, no push, no tag, no release. No Owner verdict is originated, altered or implied — OV-3 remains FAIL and OV-6/OV-8 remain OPEN.

# ADR 023: Model-card density and user-facing status vocabulary (UX V-2)

**Status:** **Proposed** — this is the design record the Owner ordered instead of a waiver (decision **Q1a**, 2026-10-26). The Owner ratified decisions **Q1a, Q2, Q3, Q4, Q5, Q6** plus five additional constraints at the UX V-2 Design Decisions gate; they are recorded verbatim in §Owner decision record and contracted as **D-1 … D-11** below. Under [../DEVELOPMENT.md](../DEVELOPMENT.md) §ADR process (`:195-199`) **Proposed** means "decision made, implementation pending": **implementation is NOT STARTED and NOT authorized**, and this document changes no CSS, JavaScript, HTML, i18n or test byte. Nothing in this ADR is a claim of shipped behavior. **The Owner ratified §I-1 and §I-2 at the 2026-10-26 ratification gate; both are resolved below. §I-1 (the casing exemption also covers `pending`) is accepted. §I-2 is accepted as a REJECTION of the earlier "global badge-class exemption" mechanism: the casing exemption must not be a global rule and must not reach the History/Logs hint badges, so D-3 is rewritten to apply per render surface. One residual consequence of that rewrite — a `pending` badge on the frozen History/Logs surfaces keeps its current uppercased rendering — was raised as §I-3 and is now **RATIFIED** by the Owner (2026-10-26 Final Documentation Consistency gate): the History/Logs uppercase presentation for `pending` is preserved — RU «Ожидание» on Models / Instances / Pipelines, «ОЖИДАНИЕ» on History / Logs — as a presentation-only distinction that leaves the `models.status.pending` key and the internal `pending` state unchanged; no further typography redesign is authorized.**

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

### D-6 — What density may not cost

- **Touch targets:** no rule may reduce `.icon-btn` below 44×44 px inside `#model-list` (`style.css:251-257`), at any viewport of the maintained 1920/1440/1280/1024/768/600/430/375 sweep (`responsive.cjs:126-128`). Density comes from padding, gaps and typography — not from shrinking controls.
- **Action availability:** every action keeps its icon button and its `data-tooltip`/`aria-label` (ADR 013 convention, `iconBtn` at `app.js:421-423`); no action may be hidden behind a hover, a breakpoint or an affordance that does not exist today.
- **DOM and JavaScript:** zero bytes in `app.js`, `index.html` (beyond the two `:313-314` fallback strings) and any Go file (Q5).
- **No expand/collapse, no hover-reveal, no second row state** — the list is rebuilt on every 3000 ms render (`app.js:545`), so DOM-held UI state would be erased mid-interaction (F-3).
- **No new animation** (Q6), and therefore no new reduced-motion obligation than D-8 already states.

### D-7 — ADR 022's contract survives unchanged

The badge stays the only state word per row (`app.js:574`, class list `status-badge <state>` intact); the action strip stays text-free in `starting`/`stopping` (`app.js:556-560`, `aria-hidden` marker + `.model-progress-ring`); the orphan branch keeps its hint text (`app.js:561-562`); the `.model-transitional` (`style.css:266`) and `.model-progress-ring` (`:269`) hooks keep existing and matching the maintained selectors. A density change may not hide, displace or clip the ring, and may not re-introduce text into the strip.

### D-8 — Reduced motion: the Models row transition is disabled, and the rule is machine-checked

`@media (prefers-reduced-motion: reduce)` (`style.css:276-277`) gains `#model-list .model-row { transition: none; }` — `.model-row` currently animates `transition: all 0.15s` (`:236`), which reduced motion does not suppress today. No new animation is added anywhere. Q6 requires this to be **automatically** verified: a leg creates a Playwright context with `reducedMotion: 'reduce'` and asserts the computed `transition-duration` of a `#model-list .model-row` is `0s` and the computed `animation-name` of `.model-progress-ring` is `none`. The leg is the project's first reduced-motion check, so ADR 022's "no automated leg asserts reduced motion" note is superseded **for the Models row only** once it lands.

### D-9 — Hints and technical explanations of orphan processes are not renamed

`instances.orphan.hint` (`ru.json:110` / `en.json:110`), `history.state.orphan` (`:208`) and `instances.kill.confirm` (`:113` — RU «Завершить процесс-сироту (PID {pid})?», EN "Kill the orphan process (PID {pid})?") keep their current bytes. The hint already reads «Может выполняться вне GoAl» / "May still be running outside GoAl", which is the same vocabulary the new label uses, so the badge and its tooltip agree without touching either string. `orphan.cjs:156` pins the hint byte-for-byte; D-9 guarantees that leg cannot break. Technical naming of the *concept* (ADR 005/008 recovery, `docs/API.md`, the internal `orphan` state) is not a UI label and is not renamed mechanically (Owner Q4 rider).

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
| T-5 | **Pipeline isolation regression:** for `#pipeline-list .model-row` the computed `padding`, `gap`, `font-size` equal the values measured at baseline `c3002ec` and `.icon-btn` counts/sizes are unchanged. A chip badge in `.pl-chips-wrap` carrying `.orphan`/`.pending` (its text is a model **name**) computes `text-transform: uppercase` because it is **excluded** from D-3's selector list — asserted on the rendered element, and there is no separate restore rule to hold. The **aggregate** pipeline badge in `.model-row-name` computes `text-transform: none` **and** carries the new vocabulary word (the D-3 surface the Owner's I-2 requires). The leg must fail if a density or casing rule leaks to `.pl-chips-wrap` or elsewhere globally | `[A]` |
| T-6 | **Touch-target floor:** every `#model-list .model-row .icon-btn` has a bounding box ≥ 44×44 px at **all eight** sweep viewports `1920/1440/1280/1024/768/600/430/375` (`responsive.cjs:126-128`), is fully in-viewport, and does not overlap `.model-row-main` | `[A]` |
| T-7 | Density is real: at least one measurable reduction of `#model-list .model-row` bounding-box height versus the `c3002ec` baseline, recorded as a number, with the `.model-row-title` and `.model-row-sub` text still non-empty and not collapsed to zero width | `[A]` |
| T-8 | Filter coherence: `#mf-state` options keep `value="pending"` / `value="orphan"` while their labels equal the new dictionary values in RU and EN; selecting `orphan` still filters to exactly the orphan rows (the existing legs at `orphan.cjs:160-167`) | `[A]` |
| T-9 | Render-site vocabulary split, exactly as §D-3 scopes it. **State-word surfaces** — Models (`app.js:574`), Pipeline aggregate (`:736` via `:797`), Instances table (`:1819`), Instances compact (`:1843`) — display the renamed word and compute `text-transform: none`. **Hint-sentence surfaces** — Logs selector (`:1090-1091`), Logs bar (`:1114`), History cell (`:1997`), History compact (`:2020`) — are frozen: the `orphan` badge there keeps the byte-identical `history.state.orphan` sentence at its **current uppercase presentation** (its computed `text-transform` stays `uppercase` — §I-2 ratification), and the `pending` badge there keeps «ОЖИДАНИЕ» (§I-3) with only the dictionary ellipsis removed. The Instances `orphan` badge's `title` stays byte-identical (`instances.orphan.hint`, D-9) | `[A]` |
| T-10 | **Reduced motion, automated:** a `reducedMotion: 'reduce'` context asserts computed `transition-duration: 0s` on `#model-list .model-row` and `animation-name: none` on `.model-progress-ring` (D-8) | `[A]` |
| T-11 | ADR 022 non-regression: `stopping.cjs` full suite green, including the marker/`aria-hidden` and strip-text legs; `pipeline.cjs` full suite green | `[A]` |
| T-12 | Corpus + platform checks: full `npm test` (fifteen suites) with the raw `PASS |`/`FAIL |`/`SKIP |` lines counted — never a sum of `TOTAL` rows; `go vet ./...`, Windows and `GOOS=linux` builds; `-race` evidence quoted from the Linux CI step of the exact SHA | `[A]` |
| T-13 | Documentation reconciliation (D-10): guides, `docs/DEVELOPMENT.md` suite row, ADR 022 dated note; every `file:line` citation re-derived after the code moves | `[A]`/`[C]` |

## Acceptance criteria

- **AC-1** — RU Models row shows «Вне GoAl» and «Ожидание», displayed exactly as stored (D-2, D-3). `[A]` + `[O]` OV-1
- **AC-2** — EN Models row shows `UNMANAGED` and `PENDING`. `[A]` + `[O]` OV-2
- **AC-3** — Internal tokens, API payloads, CSS class names, i18n key names and `<option value>`s are byte-unchanged (D-1). `[A]`
- **AC-4** — Pipeline cards are unchanged in shape **and** chip-name casing (D-4, T-5). `[A]` + `[O]` OV-5
- **AC-5** — Models rows are measurably more compact, with every action reachable and ≥ 44×44 (D-5, D-6, T-6, T-7). `[A]` + `[O]` OV-3/OV-4
- **AC-6** — No horizontal overflow, no clipped title/PID/uptime text, correct wrap at 768/430/375 (T-6, responsive corpus). `[A]` + `[O]` OV-6
- **AC-7** — ADR 022's contract holds: one state word per row, text-free transition strip, ring visible and not clipped (D-7). `[A]` + `[O]` OV-3
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

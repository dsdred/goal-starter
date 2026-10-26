# ADR 022: Model-card transition indicator (no duplicated state word)

**Status:** **Accepted** — the Owner accepted the concept D-1 … D-5 on 2026-10-25 (§Owner decision record), and the authorized implementation was **committed, published and CI-verified** on 2026-10-26 as `3342719c7e055bbe3ff3b805c91aa8a2ba925f64` (parent `02f58a602c3026103975736e4e2d5cf3374200dc`, exactly 6 paths, +511 / −6; exact-SHA CI run `40234792453` attempt 1 ended **7/7 jobs PASS** with no rerun — see §Publication record). Under [../DEVELOPMENT.md](../DEVELOPMENT.md) §ADR process (`:195-199`) that is what the label **Accepted** ("implemented or being implemented") requires. **Status record (2026-10-26):** the authorized implementation was produced at the Implementation Gate and existed **in the working tree only** — production `internal/webui/static/app.js` and `internal/webui/static/style.css`, evidence `tests/browser/stopping.cjs` and `tests/browser/orphan.cjs`, and the `stopping.cjs` row of `docs/DEVELOPMENT.md`; that wording describes the state before the separate Commit and Publication Gates of the same date ran.
**Date:** 2026-10-25
**Related:** [ADR 021](021-lifecycle-state-observability-view.md) (the badge that carries the state word is its evidence surface), [ADR 013](013-pipeline-repeat-models-ux-polish.md) (icon-strip convention for row actions), [ADR 017](017-unified-launch-arbitration.md) (launch arbitration that stays untouched)

## Context

### The defect

On the **My Models** row, a row in a transitional lifecycle state shows the state word **twice**: once in the status badge on the left, once in the action strip on the right.

`renderModels()` (`internal/webui/static/app.js:519`) computes the action strip as:

- `running` → Restart + Stop icon buttons (`:553-555`);
- `starting` **or** `stopping` → a text span: `<span class="hint-text model-transitional">` + `t('models.status.' + status)` (HEAD `:556-557`; this ADR replaces it with the text-free marker at `:556-560`, §P-1);
- `orphan` → the same span class with a different string, `instances.orphan.hint` (`:561-562`);
- otherwise → a Start icon button (`:563-565`).

The badge in the same row renders the same dictionary key at `:574`:

```js
'<span class="status-badge ' + status + '"' + badgeTitle + '>' + t('models.status.' + status) + '</span>'
```

So for `stopping` the row showed «Останавливается» / "STOPPING" twice — the badge (HEAD `:571`, now `:574`) and the strip text (HEAD `:557`, removed by this ADR). Two style systems are mixed as well: `.hint-text` is a paragraph style carrying `margin-bottom: 1rem` (`internal/webui/static/style.css:110`), while `.model-transitional` (`:266`) only adjusts font and padding, and both are placed inside `.model-row-actions`, a `display:flex; gap:4px; align-items:center` icon strip (`:250`).

### What the defect is not

- **The instances table does not duplicate the word.** `renderAdvInstances()` renders the badge once per row (`app.js:1819`, `:1843`) and its action cell holds icon buttons only (`:1822-1828`, `:1846-1852`).
- **The pipeline list does not duplicate the word.** The aggregate badge carries the state word (`app.js:735` inside `pipelineStateBadge`), while each chip carries a **model name**, not a state word (`pipelineChips`, `:706-719`).
- **The badge itself is correct and must not change.** It is the surface the F-7.5 acceptance criterion and AC-3 are worded against.

### Why it matters

The duplicated copy is the only text in an otherwise icon-only strip, so it (a) reads as a second, competing state label, (b) is styled as a paragraph inside a control strip, and (c) makes the row's right side resize whenever the row enters or leaves a transition, because the strip swaps a localized word for a 44 px icon button and back. It is a copy/composition defect with no lifecycle meaning: the second word adds no information, because it is literally the same dictionary key as the badge.

### Constraints that shaped this proposal

- The row is rebuilt by `list.innerHTML = …` on every render (`app.js:545`), and the page re-renders on the shipped 3000 ms timer (`startRefresh`, `:2361-2367`).
- The app has **no** lifecycle status announcement infrastructure: there is no `role="status"`, no `role="progressbar"` and no visually-hidden utility class in `static/` or the template, and the only `aria-live` region in the application is the portable-import filename label (`internal/webui/templates/index.html:494`), which is unrelated to lifecycle state (verified by search over `static/` and `templates/`).
- The action strip is icon-only by convention (ADR 013): icon buttons carry `data-tooltip` + `aria-label` through `iconBtn` (`app.js:421-423`).

## Decision

Replace the duplicated **text** in the model row's action strip with a compact, **text-free** progress indicator, leaving the badge as the single textual state carrier and leaving every action, endpoint and lifecycle semantic untouched.

### D-1 — The badge stays the only state word

`models.status.<state>` continues to be rendered exactly once per model row, from `app.js:574`, with the class `status-badge <state>`. The class list, the text and the `title` handling are unchanged. This is a hard boundary: it is the F-7.5/AC-3 evidence surface (§Impact on ADR 021).

### D-2 — The action strip becomes text-free in every non-orphan state

For `starting` and `stopping` the strip renders a marker with **zero text content**:

```js
const tPhase = Date.now() % 1000;
const transitionMarker =
    '<span class="model-transitional model-progress" aria-hidden="true">' +
    '<span class="model-progress-ring" style="animation-delay:-' + tPhase + 'ms"></span>' +
    '</span>';
```

- `.model-transitional` is **kept** as the stable "this row is in a transition" hook — the maintained browser leg reads exactly that (`tests/browser/stopping.cjs:219`, `:970`).
- `.model-progress` / `.model-progress-ring` are the new visual classes; the marker is **not** given `.hint-text`, so the paragraph margin does not return to the strip.
- No `title`, no `data-tooltip`, no `aria-label`, no `role`, no `tabindex` on the marker: it must not name the state (that would re-introduce the duplicate as a tooltip) and must not enter the tab order.
- The animation phase is offset by `Date.now() % 1000` so the 3000 ms list rebuild does not visibly restart the ring mid-rotation.

#### D-2 check — can the indicator node survive the periodic rebuild? (Owner question 1)

Read-only finding: **it cannot be preserved without expanding the scope**, so the phase offset stays.

The answer rests on the renderer, not on the proposal:

- `startRefresh()` (`internal/webui/static/app.js:2361-2367`) runs `reloadAllData(); renderAll()` on a 3000 ms `setInterval`, and `renderAll()` (`:505-515`) calls `renderModels()` unconditionally — not on a data change.
- `renderModels()` builds one HTML string and assigns `list.innerHTML = filtered.map(…)` (`:545`). Every tick, and every action (each action calls `reloadAllData(); renderAll()`), **destroys and re-parses the whole `#model-list` subtree**.
- `static/app.js` contains no keyed diff, no node cache and no dirty check.
- Nothing inside `#model-list` animates today: the only animations in `static/style.css` are `toastIn` (`:141`) and `status-dot.running` (`:297`). ADR 022 therefore introduces the first continuous animation inside a periodically rebuilt subtree — which is why the phase question is real rather than theoretical.

| Option | What it preserves | Why it stays out of scope |
|---|---|---|
| A. Keyed/diff render of `#model-list` | Every node | Replaces the render strategy for all rows to fix one indicator; changes DOM identity and paint timing for every suite that reads `#model-list` |
| B. Lift the live marker node across the `innerHTML` swap (capture before, re-insert after, keyed by model id) | This node only | 20–40 lines of stateful DOM surgery on a declarative renderer (several transitioning rows, filters, ordering, row removal); new failure modes for a one-attribute benefit |
| C. Render the marker outside the rebuilt subtree | No restart | Per-row positioning must be recomputed; the row-layout contract breaks |
| D. Make the marker static | No restart | Drops the activity cue the Owner asked for; a still ring reads as decoration |
| E. Skip or pause the refresh (or add change detection) while a transition is visible | No restart | Changes the shipped 3000 ms convergence behaviour that AC-3 and the F-7 convergence legs are worded against |
| F. Drive the rotation from `document.timeline` via the Web Animations API | Phase continuity | Same visual result as the CSS delay with an imperative animation lifecycle to re-establish after every rebuild |

Residual error of the chosen option: the gap between building the string and the first paint of the rebuilt node — a few milliseconds against a 1000 ms cycle. The phase is derived from the wall clock, so a rebuilt node resumes where the previous one would have been at that instant. A wall-clock jump (NTP, DST) shifts only a decorative phase and appears in no evidence surface. Under `prefers-reduced-motion: reduce` the animation is off and the delay is irrelevant.

Proposed CSS (new, appended to `internal/webui/static/style.css`; no existing rule is modified except the `.model-transitional` usage described above, which continues to exist for the orphan hint):

```css
.model-progress { display: inline-flex; align-items: center; justify-content: center; width: 24px; height: 28px; }
.model-progress-ring {
    width: 16px; height: 16px; border-radius: 50%;
    border: 2px solid var(--text-secondary);
    border-right-color: transparent;
    animation: modelSpin 1000ms linear infinite;
}
@keyframes modelSpin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .model-progress-ring { animation: none; border-right-color: var(--text-secondary); } }
```

### D-3 — The indicator is decorative, not informational

The ring conveys "a transition is in progress" and nothing more: it cannot be quantified (no progress percentage exists or should be invented), it cannot be waited on, and it carries no state identity that the badge does not already carry in words. Therefore it is `aria-hidden="true"` and is **not** a `role="progressbar"`. A progressbar without `aria-valuenow` is an indeterminate progressbar whose accessible name would have to repeat the state — exactly the duplication this ADR removes — and it would add an announcement that the page currently never makes.

### D-4 — Colour is neutral in both themes

The ring is drawn in `--text-secondary`. Measured contrast of the non-text indicator against the card background: **5.98:1** in the dark theme (`#9a9ab4` on `#1c1e32`) and **8.47:1** in the light theme (`#4a4a6a` on `#ffffff`), both above the 3:1 that WCAG 2.2 SC 1.4.11 requires for non-text UI components.

State-tinted colour was rejected for a concrete reason: `--warning` is `#f39c12` on the dark card (**7.47:1**, passes) but `#e67e22` on a white card (**2.85:1**, fails 3:1). Using it would make the indicator itself an accessibility regression in the light theme, and the light-theme token problem is a separate, pre-existing issue (§Follow-ups discovered, F-1).

### D-5 — Blocking semantics are unchanged, by construction

During `starting`/`stopping` the strip renders **no** Start, Stop or Restart control — the shipped branch (`app.js:556-557` at HEAD, now `:556-560`) already replaced them, and this ADR keeps that shape. A repeated Stop or a conflicting start/restart therefore cannot be initiated from the row, exactly as today. The Logs / Edit / Delete controls keep rendering in the same states, unchanged.

A **disabled** Stop button was considered and is rejected as the default: it would change the action surface (a pressable-looking 44 px target during a transition), it would make the browser leg's informational `stopBtn` field flip from `false` to `true`, and "заблокировано" is already satisfied more strictly by absence. If the Owner prefers "visible but disabled" as the reading of the requirement, that is a different decision (Q-4 in §Owner decision record) with the trade-offs stated there.

Server-side enforcement is untouched and is not relied on for this UI guarantee: `StopInstance` → `InstanceService.StopInstance` → `supervisor.Stop` (`internal/webui/handlers/instances.go:119-133`, `internal/application/instance_service.go:49-51`) and the ADR 017 arbitration boundaries keep the same semantics for any client.

### D-6 — RU and EN are identical by construction

The indicator contains no translatable content, so there is nothing to translate and nothing that can drift between dictionaries. The only visible word in the row remains the badge's localized `models.status.<state>`. No i18n key is added; `models.status.starting` and `models.status.stopping` stay referenced by the badge (and by the pipeline badge for the aggregate), so `TestI18nDictionariesParity` (`internal/webui/server_test.go:33`) keeps proving both dictionaries complete.

### Resulting row (states)

| State | Badge (left) | Action strip (right) |
|---|---|---|
| `running` | `RUNNING` / «Работает» | Restart · Stop · Logs · Edit · Delete (unchanged) |
| `stopping` | `STOPPING` / «Останавливается» | **ring** · Logs · Edit · Delete — one word in the row |
| `starting` (same branch, ratified with D-2) | `STARTING` / «Запускается» | **ring** · Logs · Edit · Delete |
| `orphan` | `ORPHAN` / «Сирота» | hint sentence (unchanged — a different string, not the state word) |
| `stopped` | `STOPPED` / «Остановлен» | Start · Logs · Edit · Delete (unchanged) |

## Alternatives considered

| Option | Why not |
|---|---|
| **A. Text-free ring in the strip (this proposal)** | Adds no information, removes the duplicate, keeps the badge as the evidence surface, no API/lifecycle change |
| B. Disabled Stop button with a ring overlay | Changes the action surface; looks pressable during a transition; contradicts "blocked" more weakly than absence; requires changing the leg's expected strip |
| C. Reword the strip text ("Stopping…" → something else) | Keeps two state labels in one row; the duplication is the defect, not the wording |
| D. Remove the strip content entirely (empty actions area) | Cheapest, but a row that loses all action affordance during a stop reads as broken rather than busy; the compact indicator preserves "the system is working" |
| E. Keep the text, hide the badge during transitions | Inverts the design: the badge is the surface the acceptance contract is worded against (F-7.5, AC-3) |
| F. Animate the badge itself and drop the strip content | Makes the evidence surface move; the F-7.5 browser leg reads badge text and class, and animation adds nothing observable to `[A]` evidence |

## Accessibility analysis

- **No information loss.** The state word remains in the DOM once per row, in the badge. The strip's text was a verbatim duplicate, so removing it removes no content a screen-reader user can obtain elsewhere.
- **No new announcement.** The marker is `aria-hidden="true"`, unfocusable and unnamed; the page's lifecycle announcement behavior is unchanged (none exists — the single `aria-live` region in the app is the portable-import filename label, `templates/index.html:494`). A lifecycle live region is deliberately not introduced (see Follow-ups F-2).
- **No focus-order change.** The strip contributes zero focusable elements in `starting`/`stopping` before and after this change.
- **Contrast.** 5.98:1 (dark) / 8.47:1 (light) for the ring — above SC 1.4.11's 3:1.
- **Motion.** `prefers-reduced-motion: reduce` stops the animation and leaves a static **full ring**: the reduced-motion rule restores `border-right-color: var(--text-secondary)`, so all four border edges are painted and the 3/4 arc is never shown. The indicator therefore remains visible without motion (SC 2.3.3 is AAA, but the fallback costs three lines and removes the flicker complaint class).
- **No colour-only meaning.** The indicator is decorative; state identity is carried by badge text.

## Impact on ADR 021 and the acceptance contract

**Unchanged by this design:**

- the badge class list and text (`app.js:574`) — the F-7.5 surface;
- the client projection that produces the state: `isActive` (`:335`), `modelStatus` (`:337-348`), `pipelineEntryStatus`/`pipelineAggregateStatus` (`:672-701`, `:724-733`);
- the shipped 3000 ms refresh (`:2361-2367`);
- the instance routes, the pipeline route, the API, the process lifecycle and `Stop` semantics;
- every service-side guard (`launch_in_flight`, active-owner 409, ADR 017 arbitration).

**Consequences for the F-7 record:**

1. The Owner's **F-7.5 = PASS (2026-10-25, model route)** was granted against the badge and the `instancesData` projection. Neither changes here, so that acceptance stays bound to its evidence; a visual re-check after implementation is recommended, not required, because the row's composition changes while the observed word does not. That decision is **not yet recorded in the repository** — no acceptance-record commit has been authorized; recording it belongs to a separate docs gate.
2. ADR 021 §D11 states that the Web UI "already renders `stopping` … **no client change is required or authorized**" ([021](021-lifecycle-state-observability-view.md), §D11). That sentence is scoped to the F-7 remediation: it means F-7 does not need a client change to make `stopping` observable, and it does not forbid an unrelated copy/composition fix. This ADR is that separate decision. At implementation, ADR 021 receives a **dated note** stating that a UI-copy change landed under ADR 022 and that the F-7 evidence surfaces are unchanged — the ADR 021 sentence is not rewritten or silently reinterpreted.
3. `tests/browser/stopping.cjs` was the published Slice 4 leg at a fixed **79** checks per run (`docs/DEVELOPMENT.md:101` before this ADR). These new assertions raise that number, so the count was recounted at the Implementation Gate from raw `PASS |` lines — never as a sum of `TOTAL` rows — and recorded as **112 checks per run** in `docs/DEVELOPMENT.md:101` (four intervals, seven run-level legs). The historical 79 figure is left as the pre-change record and is not rewritten; the ADR 021 Slice 4 record still owes its own dated pointer. *(2026-10-26: that pointer is now recorded in ADR 021 §D11 — the dated note there carries the `79 → 112` recount, drawn from this ADR's published CI run `40234792453`.)*

## Implementation plan (minimal) — executed at the Implementation Gate (2026-10-26)

*Execution record (2026-10-26): P-1 … P-3 are executed in the working tree. Line numbers below are the current post-change ones except where a pre-change range is named explicitly. Nothing is committed or published.* *(That last sentence is this gate's own state: the bytes were committed as `3342719c7e055bbe3ff3b805c91aa8a2ba925f64` and published the same day — §Publication record.)*

### P-1 Production edit 1 of 2 — `internal/webui/static/app.js`

Replace the transitional branch of `renderModels()` (HEAD `:556-557`; the branch now spans `:556-560`). Before:

```js
} else if (status === 'starting' || status === 'stopping') {
    actionBtns = '<span class="hint-text model-transitional">' + t('models.status.' + status) + '</span>';
```

After:

```js
} else if (status === 'starting' || status === 'stopping') {
    const tPhase = Date.now() % 1000;
    actionBtns = '<span class="model-transitional model-progress" aria-hidden="true">' +
        '<span class="model-progress-ring" style="animation-delay:-' + tPhase + 'ms"></span>' +
        '</span>';
```

One line out, four in. Not touched in the same file: the `orphan` branch (`:561-562`), the badge (`:574`), `iconBtn` (`:421-423`), `isActive`/`modelStatus` (`:335-348`), every action function, every API call and every timer.

### P-2 Production edit 2 of 2 — `internal/webui/static/style.css`

Insert the §D-2 block (`.model-progress`, `.model-progress-ring`, `@keyframes modelSpin`, the reduced-motion rule) directly after `.model-transitional` (`:266`). No existing selector is modified; the file has no `prefers-reduced-motion` rule today, so the media query is additive.

These two files are the entire production change: the row is built by string concatenation, so no Go handler, template or i18n dictionary is involved.

### P-3 Test edits

`tests/browser/stopping.cjs` (reuses the existing interval harness; no new suite):

1. Append one interval to `specs` (`:350-357`): `{ key: 'I4 model-route stop (EN)', tag: 'i4', lang: 'en', view: 'models', name: 'S4 Model EN', kind: 'model' }`. It must be **appended**: `specs[2]` is the pipeline's model by index (`:368`), so an insertion would re-point the pipeline interval at a different model.
2. Extend `collectDomInPage` (`:202-267`) with two read-only fields on the model row: `stripText` (`(mrow.querySelector('.model-row-actions') || {}).textContent`, trimmed) and `marker` (`{ present, text, ariaHidden, tabindex, named, visible }` for `.model-progress`; `named` counts `title` / `data-tooltip` / `aria-label` on the marker and its descendants — the predicate shipped under this name, not as the `hasTitle` flag drafted here).
3. New legs in the two **model-route** intervals only (I1 RU, I4 EN) — the instances and pipeline views have no model card, so the defect cannot exist there:
   - **duplicate-word:** `stripText === ''` in the painted stopping state (`paintAt(f, f.t4)`);
   - **indicator:** `.model-progress` visible, `textContent === ''`, `aria-hidden="true"`, no `tabindex`, no `title` / `data-tooltip` / `aria-label`;
   - **blocked-controls:** the row contains no `[onclick*="stopModel("]`, `startModel(` or `restartModel(` control in that state.
4. One **negative control** (run-level): the duplicate-word predicate applied to a synthetic element containing «Останавливается» must report a hit, proving the leg is live rather than tautological.
5. Existing legs are not re-worded: the I1–I3 predicates must be provably unchanged in the diff, and `.model-transitional` stays the convergence hook (`:970`).

`tests/browser/orphan.cjs` (one leg, reusing the orphan it already creates): the orphan row's strip still renders the hint sentence (`instances.orphan.hint`, `app.js:562`) and contains **no** `.model-progress`.

Static/Go: no Go file changed, so `TestI18nDictionariesParity` (`internal/webui/server_test.go:33`) stays green unchanged; `node --check` on the touched `.js` / `.cjs` passes; the leg count was recounted from raw `PASS |` lines (never a sum of `TOTAL` rows) and reconciled in `docs/DEVELOPMENT.md:101` — the ADR 021 Slice 4 record is still owed (§Affected documentation).

### P-4 Verification (the AGENTS.md obligation)

`gofmt -w .`, `go test ./...`, `go test -race ./...` (Linux CI is the authoritative race evidence), `go vet ./...`, plus a Windows and a Linux build because the embedded `static/` bytes change; then `npm test` for the whole browser chain and a focused `node tests/browser/stopping.cjs`.

### P-5 Acceptance criteria — RU and EN

| # | Criterion | RU (`uiLang=ru`) | EN (`uiLang=en`) |
|---|---|---|---|
| AC-1 | Exactly one state word in the stopping row | badge «Останавливается»; strip text empty | badge `STOPPING`; strip text empty |
| AC-2 | Compact indicator, no text | `.model-progress` visible, empty, `aria-hidden="true"` | identical (no translatable content) |
| AC-3 | Conflicting actions blocked | no Start / Stop / Restart control in the row | identical |
| AC-4 | Terminal convergence | Start returns, marker gone, badge «Остановлен» | marker gone, badge `STOPPED` |
| AC-5 | Badge surface unchanged (F-7.5 / AC-3 of ADR 021) | class `status-badge stopping`, same text as before | identical |
| AC-6 | Orphan row unaffected | hint «Может выполняться вне GoAl», no marker | hint `May still be running outside GoAl`, no marker |
| AC-7 | No behavioural drift | no console error, no 5xx, lifecycle/API/process semantics identical | identical |

`[A]` legs prove the DOM. The Owner's `[O]` leg — visual inspection of the row in RU and EN, watching the badge and the ring — is separate and cannot be substituted by an agent-driven run (RV-9); the Owner's verdict is recorded below.

## Owner observation `[O]` — 2026-10-26

The Owner observed the transitional model row in the running application in RU and EN and returned one verdict in the chat on 2026-10-26: **L1–L6 — 6/6 PASS, no remarks**. The six items are the Owner's visual acceptance list for this decision; they are recorded by their labels only, without restating their wording, so the record cannot drift from what the Owner approved.

Evidence class and limits:

- **`[O]`, not `[A]`.** The verdict is the Owner's own observation. It was supplied as a single dated verdict with **no per-run timestamps**, so this record makes no interval, ordering or observation-window claim and cannot be used to reconstruct when any individual item was seen (RV-9; timed observations are intervals, never points).
- **Not substitutable.** No agent-driven browser run can stand in for it; it is recorded here only because the Owner gave it.
- **Paired `[A]`.** The automated half over the same working-tree bytes comes from the Implementation Gate: the full browser corpus reported **891 raw `PASS |` / 0 `FAIL |`** (of which `stopping.cjs` 112 and `orphan.cjs` 28), counted from raw `PASS |` lines and never as a sum of `TOTAL` rows. That `[A]` half carries AC-1 … AC-7 above; the `[O]` half carries the visual judgement of the same row.
- **No fresh CI PASS is claimed.** Nothing is committed or published, so no CI run exists for these bytes; the `[A]` figure is a local browser-corpus result from the Implementation Gate, not a CI status. *(2026-10-26, publication: this bullet describes the Implementation Gate. The bytes were then committed and published, and their exact-SHA CI run `40234792453` attempt 1 reported the same **891 raw `PASS |` / 0 `FAIL |`** in Browser Acceptance — `[A]`/`[C]` evidence for the published SHA, which changes nothing about the `[O]` verdict above.)*
- **Scope of the verdict.** It covers ADR 022's RU/EN visual acceptance. It does not record ADR 021's **F-7.5** decision, which was given separately and still needs its own dated note in ADR 021 (§Affected documentation).

## Publication record (2026-10-26)

The implementation was committed as `3342719c7e055bbe3ff3b805c91aa8a2ba925f64` (parent `02f58a602c3026103975736e4e2d5cf3374200dc`) in exactly the six paths this ADR names — `docs/adr/022-model-card-transition-indicator.md`, `docs/DEVELOPMENT.md`, `internal/webui/static/app.js`, `internal/webui/static/style.css`, `tests/browser/stopping.cjs`, `tests/browser/orphan.cjs` — **+511 / −6**, and published to `origin/main` by a plain fast-forward push (`git ls-remote origin refs/heads/main` then reported this SHA). Its exact-SHA CI run `40234792453` (workflow `CI`, event `push`, **attempt 1**, no rerun) ended **7/7 jobs PASS**: Lint; Build (ubuntu-latest); Build (windows-latest); Test (Windows); Test (Linux, race); Vulnerability Check; Browser Acceptance (Chromium, headless).

- **Race evidence is scoped to the race step.** The Linux job's literal `CGO_ENABLED=1 go test -race -timeout 20m ./...` step reported **20 `ok` + 4 `[no test files]`, 0 `DATA RACE`**. The plain `go test ./...` step above it is not race evidence and is not counted as such.
- **The browser corpus is recounted from raw lines.** Browser Acceptance reported **891 raw `PASS |` / 0 raw `FAIL |` / 0 raw `SKIP |`** over the fifteen maintained suites. The sixteen printed `TOTAL` rows sum to **940**, which is **not** an executed-check count (`core.cjs` prints a running subtotal for one cumulative suite object); the raw `PASS |` recount is the figure of record. Of those 891 raw lines, `tests/browser/stopping.cjs` contributed **112** and `tests/browser/orphan.cjs` **28**.
- **Evidence classes stay separate.** The CI record is `[A]`/`[C]` evidence for exactly these bytes. It neither replaces nor re-derives the Owner's L1–L6 verdict (§Owner observation `[O]` — 2026-10-26), which remains the only `[O]` evidence this ADR carries and was given without per-run timestamps.
- **Nothing else was created.** No tag and no release followed this publication, and no later commit is implied by it.

## Risks

| Risk | Mitigation |
|---|---|
| The 3000 ms `innerHTML` rebuild restarts the animation mid-rotation | `animation-delay:-<Date.now() % 1000>ms` keeps the phase continuous; node preservation was investigated and rejected as scope expansion (§D-2 check) |
| The extra EN model-route interval lengthens the browser chain | One extra fixture cycle and one dwell (~4 s) per run; the timing legs of I1–I3 are untouched and their predicates must be diff-proven unchanged |
| Headless CI asserts on animation state | No assertion depends on animation; legs read classes, `aria-hidden` and `textContent` |
| `.model-transitional` is shared with the orphan hint | The orphan branch is untouched; add a leg that the orphan hint still renders its sentence |
| A future change re-adds text to the strip | The duplicate-word leg fails on any non-empty strip in a transition state |
| A user relied on the second word | No documentation references it (search over `docs/` found none) and it duplicates the badge verbatim |
| Screen-reader users lose a second cue | Nothing is announced today; the badge remains the single textual carrier (F-2 records the deliberate deferral of a live region) |

## F-4 — read-only analysis: Stop / Restart on a `stopping` instance row (Owner question 2)

Scope: analysis only. Nothing in this section is a fix; F-4 stays a follow-up.

### 1. Are the buttons available to the user?

**Yes.** The instances table and the 430 px compact cards are rendered from `instancesData.filter(i => isActive(i.state) || i.state === 'orphan')` (`app.js:1803`), and `isActive` includes `stopping` (`:335`), so a stopping row is rendered. Its action cell is emitted **unconditionally** for a non-orphan row — Logs + Stop + Restart (`:1827-1828` in the table, `:1851-1852` in the compact card). `renderAdvInstances` has no state branch for actions, and `iconBtn` (`:421-423`) has no disabled / `aria-disabled` variant, so both controls are visible, enabled and focusable.

### 2. Do the actions reach the API?

**Yes.** `stopInstance(id)` (`:1869-1872`) and `restartInstance(id)` (`:1874-1877`) issue `POST /api/v1/instances/{id}/stop` and `/restart` with no client-side state guard; `api()` (`:245-249`) attaches the CSRF header and awaits the response with no client timeout. Server path: `InstancesHandler.StopInstance` / `RestartInstance` (`internal/webui/handlers/instances.go:119-133`, `:135-150`) → `InstanceService.StopInstance` / `RestartInstance` (`internal/application/instance_service.go:49-51`, `:83-128`) → `Supervisor.Stop` / `RestartWithLaunch` (`internal/process/supervisor.go:711-721`, `:746-758`). A **successful** call is audited (`instance.stop` / `instance.restart`, `internal/webui/audit/audit.go:56-57`); a refused one returns before the audit call.

### 3. Is there server-side protection?

**Yes — the protection is the ADR 017 arbitration boundary plus `lifecycleMu`, not the UI.**

| Property | Evidence |
|---|---|
| **Serialization of stops** | `Stop` takes `ic.lifecycleMu` (`supervisor.go:1705-1707`) and holds it across `stopCore`, which holds `ic.mu` across `manager.Stop` and waits on the run's `done` with a 15 s bound (`:1740`, `:1745-1782`) |
| **Idempotent terminal return** | A terminal instance (`exited` / `failed` / `stale`) returns `nil` before the process is touched (`:1712-1715`) |
| **`pending` is refused** | Stop returns `ErrLaunchInFlight` (`:1719-1722`); restart is refused by `checkRestartTarget` (`arbitration.go:313-316`); both map to **409 `launch_in_flight`** (`handlers/helpers.go:140-142`) |
| **Restart during `stopping` is allowed by contract** | `checkRestartTarget` admits `running, stopping, exited, failed` and states the rule in words: "stopping allow — without becoming a second stop owner" (`arbitration.go:307-326`, the `stopping` case at `:317-318`) |
| **A concurrent restart is refused** | While one restart reservation is active, a second restart fails with `ErrLaunchInFlight` — "restart already in flight" (`arbitration.go:322-325`), 409 |
| **The restart waits instead of competing** | `restartWithRefresh` acquires `lifecycleMu` after arbitration and, when the instance is not `running` but a run is still live, waits on `previousRun.done` (`supervisor.go:1839-1850`) before taking a slot and starting the new generation (`:1861-1871`) |

Test evidence (read, not re-run):

- `TestT4StopDuringHeldStopTakesNoSecondOwnership` (`internal/process/supervisor_stopping_view_test.go:655-709`): during a held stop the second `Stop` produces exactly **1** `Manager.Stop` gate entry, the state does not invert, the generation is not re-published, and the second call returns `nil` after the first completes.
- `TestT4RestartDuringHeldStopDoesNotPublishThroughLiveRun` (`:715-770`): `checkRestartTarget` completes against the held stop and admits the stopping target; no second generation is published while the stop owns the controller; the restart completes after release.
- `TestRestart_Stopping_AwaitsExistingGeneration` (`internal/process/supervisor_bf01_test.go:1311-1370`): the restart reservation stays visible while it waits, the PID does not change, `spawnCount` is 0, and exactly one new generation starts with the **same InstanceID** and a fresh PID.
- `supervisor_pending_test.go:63-146`: `pending` refusals for both Stop and Restart.

### 4. Is a repeated Stop / Restart possible?

- **Repeated Stop: yes — accepted and idempotent.** A second Stop issued during a stop blocks on `lifecycleMu` (its HTTP request hangs), then returns **200** `{"status":"stopped"}` without a second signal. Two observable side effects: the request can block up to the 15 s stop bound, and the handler writes a **second `instance.stop` audit record** for the same run (`handlers/instances.go:129`) — the audit is per successful request, not per state transition, so an audit count of `instance.stop` is not a count of stop intents.
  - Exception worth stating: if `stopCore` returns on its 15 s context bound while the process is still alive, the instance stays non-terminal and a later Stop is a genuine retry (a new termination request), not a no-op.
- **Repeated Restart while stopping: the first is honoured.** The click queues behind the stop and produces a real new generation (same instance ID, new PID) — the ADR 017 D1 contract, not a defect. A **concurrent second** Restart is refused with 409 `launch_in_flight`; a Restart issued after the first completes is a further legitimate restart.
- **UX cost (not a lifecycle defect):** the restart request blocks the browser call for the stop's duration (up to 15 s), and that row shows no progress affordance while it waits.

### 5. Classification

**F-4 = class C (technical debt / non-blocking UX inconsistency).**

- Not **A**: no current task requires the instances row to mirror the model row, and the server semantics are contract-defined and tested.
- Not **B**: it blocks nothing; the arbitration boundary is the actual guard.
- Not **D**/**E**/**F**: no new domain, API or architecture is implied — a state-gated action cell would reuse the model row's existing pattern.

The defect, stated precisely, is **affordance consistency**: the model row removes Stop/Restart during a transition while the instances row keeps them, so two surfaces give different answers to the same question. **No test currently covers either answer**: `stopping.cjs` uses the instances-row Stop as its *primary* trigger for the instance-route interval (`:570`) — one stop per interval, no repeat — and `instance-id.cjs:167-188` asserts the buttons exist and target full IDs while running, not while stopping. The classification therefore rests on code and on the four Go tests above.

**Not fixed here.** Mirroring the model row would need its own decision (it changes an action surface, and "restart during stopping is allowed" is a documented capability a user may legitimately want).

## Follow-ups discovered (reported, not fixed here)

- **F-1 (a11y, class C):** in the **light** theme the existing `stopping` badge text `#e67e22` on its 15 %-tinted background (`#fdf0db`) measures **2.53:1**, below the 4.5:1 required for text; the dark theme measures 5.74:1. This is pre-existing and unrelated to the duplication; it needs its own token decision.
- **F-2 (a11y, class D):** nothing announces a lifecycle change: there is no lifecycle `aria-live`/`role="status"` region (the app's only `aria-live` is the portable-import filename label). Adding one is a behavior change with double-announcement hazards against the 3000 ms re-render and is deliberately out of scope here.
- **F-3 (debt, class C):** `renderModels` rebuilds the whole list via `innerHTML` on every refresh; preserving nodes would remove the animation-restart class of problem but is a rendering refactor this defect does not justify.
- **F-4 (UX consistency, class C):** the instances table keeps Stop/Restart icon buttons enabled on a `stopping` row (`app.js:1827-1828`, `:1851-1852`). Analysed read-only in §F-4: the buttons reach the API, but the ADR 017 arbitration boundary and `lifecycleMu` prevent a second stop owner, a double kill or a spawn through a live run; a repeated Stop is idempotent (and writes a second audit record), a Restart during `stopping` is honoured and queued behind the stop, and a concurrent second Restart is refused with 409. Not changed by this ADR; mirroring the model row is a separate decision.

## Affected documentation (on implementation acceptance)

- ADR 022: Proposed → Accepted with the shipped record.
- ADR 021: a dated note at §D11 (copy change under ADR 022; evidence surfaces unchanged) plus the Slice 4 leg-count record, because the suite's leg count did change (**79 → 112**).
- `docs/DEVELOPMENT.md`: the `stopping.cjs` row (`:101`) — the added EN model-route interval, the new assertions and the recounted leg total — **updated at the Implementation Gate**. The `orphan.cjs` row (`:89`) still carries no mention of the added orphan-hint leg; that one-line update is **owed as documentation debt (C-11)**, outside this gate's authorized scope.
- `docs/ARCHITECTURE.md`: add the ADR 022 row to the ADR table (which already lacks 020; see its own pre-existing note at `:177`).
- `ROADMAP.md` / `BACKLOG.md`: register the task and its follow-ups (F-1 … F-4) — **not** in this gate.
- `docs/USER_GUIDE*.md`: only if the manual wording describes the two-line presentation (no such wording exists today).
- The Owner's **F-7.5 = PASS** decision (ADR 021's own acceptance row) and its report are still **not recorded in the repository**; recording them is a separate authorized docs gate, not part of ADR 022. ADR 022's own Owner observation **is** recorded above (§Owner observation `[O]` — 2026-10-26) and does not stand in for that ADR 021 note.

**Documentation disposition (2026-10-26, post-publication documentation gate).** The plan above was executed as follows; this paragraph supersedes its "not in this gate" and "owed" clauses as tracking truth only. ADR 022 is **Accepted** with the shipped record (§Status, §Publication record). ADR 021 §D11 carries the dated note and the **79 → 112** leg-count record. The `orphan.cjs` row of `docs/DEVELOPMENT.md` now names the ADR 022 orphan-hint leg, which discharges the **C-11** debt named above. `docs/ARCHITECTURE.md` carries the `0022` row, and its stale ADR 021 **Slice 4** clause is corrected to the published record. `ROADMAP.md` / `BACKLOG.md` register the published work, the four follow-ups **F-1 … F-4**, and the Owner's pending **V-2** vocabulary direction as its own non-debt item. `docs/USER_GUIDE*.md` were reviewed and need no change: neither guide describes the row's two-line composition. Two items stay open by design: the pre-existing missing **`0020`** row in the ADR index — a deferred documentation reconciliation this gate may not fold in, and one that requires no invented acceptance status — and the Owner's ADR 021 **F-7.5** decision, which still has no repository record.

## Owner decision record (2026-10-25)

The Owner accepted the concept of the Decision items **D-1 … D-5**: the badge is the only textual status; the action area shows a compact indicator; the indicator is decorative and `aria-hidden`; the colour is neutral with sufficient contrast; lifecycle semantics and action blocking stay unchanged. The same decision set the gate boundary: no production change before the Implementation Gate, F-4 analysed read-only, F-1/F-2/F-3 kept as separate follow-ups, ROADMAP/BACKLOG untouched, no commit/push/CI.

Ratified consequences:

- **D-2 covers both `starting` and `stopping`** (one code branch), which answers Q-1 with "both".
- The ring shape (Q-2), the neutral colour (Q-3) and "no disabled Stop" (Q-4) follow from the accepted D-2, D-4 and D-5 text.
- Q-5 (orphan hint) stays out of scope: it is a different string, and the marker replaces only the state-word branch.
- D-6 (RU/EN identity) is implied by the requirement that RU and EN behave identically and adds no content of its own.

This record is what promotes the ADR to **Proposed**. It granted **no** implementation authorization: production code, tests, ROADMAP/BACKLOG and commit/push/CI stayed closed until the separate Implementation Gate, which was subsequently authorized and executed in the working tree (§Status record).

### Pre-ratification questions (IDs renamed)

> These five questions were originally numbered D-1 … D-5, which collided with the Decision items the Owner's ratification names. They are renumbered Q-1 … Q-5 so a citation cannot be read against the wrong list. No question and no recommendation changed.

| # | Question | Recommendation | Outcome |
|---|---|---|---|
| Q-1 | Apply to `stopping` only, or to `stopping` **and** `starting` (same code branch)? | **Both** — same branch, otherwise the duplication survives for `starting` and the code becomes asymmetric | **Both**, via the accepted D-2 |
| Q-2 | Indicator shape: rotating ring, indeterminate bar, or pulsing dot? | **Ring** — smallest footprint, no percentage implied | **Ring**, via the accepted D-2 |
| Q-3 | Colour: neutral `--text-secondary`, or state-tinted (`--warning`/`--info`)? | **Neutral** — the tinted warning fails 3:1 in the light theme (2.85:1) | **Neutral**, via the accepted D-4 |
| Q-4 | Keep today's "no action control during a transition", or render a **disabled** Stop button? | **Keep absence** — it is what "blocked" means today and it does not change the action surface | **Keep absence**, via the accepted D-5 |
| Q-5 | Is the orphan hint in the same strip in scope for a similar treatment? | **No** — it is a different string (a sentence), not a duplicate of the badge | **No** — out of scope |

## Consequences

- The record is **Accepted**: the Owner made the decision on 2026-10-25, and the implementation is committed, published and CI-verified at `3342719c7e055bbe3ff3b805c91aa8a2ba925f64` (CI run `40234792453` attempt 1, 7/7 jobs PASS — §Status, §Publication record). *(The earlier wording of this bullet — "The record sits at **Proposed**: the decision is made and the implementation exists in the working tree, but nothing is committed, published or CI-verified" — was accurate before those gates ran and is superseded here as tracking truth only.)*
- The model row carries exactly one state word (the badge) in both languages.
- Node preservation across the rebuild was investigated and rejected as scope expansion; the phase offset is the chosen mechanism, and it is proven by DOM legs rather than by inspecting the animation.
- F-4 is classified **class C** and analysed read-only; no production change follows from it in this ADR.
- F-1, F-2 and F-3 remain open follow-ups with no implementation.
- No API, persistence, lifecycle, timing or localization semantic changes; no Go file changed.
- The F-7.5 evidence surface is untouched, so the accepted acceptance result remains valid; the ADR 021 record owes a dated note rather than a rewrite.
- The maintained stopping suite grows by one EN model-route interval and new assertions, and its published leg count was reconciled at the Implementation Gate: **79 → 112** checks per run, recounted from raw `PASS |` lines and recorded in `docs/DEVELOPMENT.md:101`.

# Development

This document describes the development workflow for GoAl contributors.

## Prerequisites

- Go 1.26.10 (pinned in `.go-version`)
- GCC (for race detector: `CGO_ENABLED=1`)
- PowerShell 5.1+ (Windows) or bash (Linux)

## Clone and setup

```bash
git clone https://github.com/dsdred/goal-starter.git
cd goal-starter
go mod download
```

## Required checks

Run these before every commit:

```bash
gofmt -w .
go test ./...
go vet ./...
go build ./cmd/goal
```

## Race detector

```bash
CGO_ENABLED=1 go test -race ./...
```

Requires GCC on Windows. On Linux, the race detector is part of the standard toolchain.

**CI:** Race detector runs on Linux in GitHub Actions. Windows race detector is also in CI matrix.

## File ownership

| Area | Package |
|------|---------|
| Process lifecycle | `internal/process/` |
| OS process behavior | `internal/platform/` |
| Configuration | `internal/config/` |
| HTTP and UI | `internal/webui/` |
| Fake runtime / integration tests | `testdata/fake-runtime/` |
| CI / release | `.github/`, `deploy/`, scripts |

Avoid simultaneous edits to the same files unless coordinated.

## Testing

### Unit tests

```bash
go test ./...
```

### Handler tests

```bash
go test ./internal/webui/handlers/...
```

### Process tests

```bash
go test ./internal/process/...
```

### Storage tests

```bash
go test ./internal/storage/...
```

### Browser acceptance suite (real Chromium, headless)

The maintained real-browser acceptance suite lives in `tests/browser/` (Playwright + Chromium). It replaces the former one-off `*_acceptance*.cjs` scratch scripts at the repository root.

Each suite builds the `goal` binary and a platform-native `fake-runtime` into a fresh temp workspace, seeds a deterministic config/repository, and drives the real UI:

| Suite | Coverage |
|-------|----------|
| `core.cjs` | Wizard (existing/new runtime), resolve, start/stop/restart lifecycle, logs, history, instances, edit/delete, runtime-delete 409, autostart, polling, auth OFF/ON, env-secret safety |
| `responsive.cjs` | **Fit-if-fits contract** (TABLE iff the table's natural content width fits the available space — never a viewport breakpoint; decided by JS `applyFit`/`syncAllFit` + `ResizeObserver`), monotonic across 8 viewports (TABLE…TABLE → CARDS…CARDS), no page-level horizontal overflow (hidden measurement table excluded), action visibility, sidebar behavior, **Settings sidebar icon symmetric outline-gear regression** (desktop + mobile drawer), **Instances geometry contract** (desktop: ID/PID/Exit mono-nowrap, model ellipsis max-260px, timestamps wrap at space not char-by-char; @900: mono-nowrap still holds; mobile: Model→State header order), **ADR 023 UX V-2 legs** (2026-10-27): *Pipeline isolation* — `#pipeline-list .model-row` computed `padding`/`gap` and the list `gap` still equal the `c3002ec` baseline on desktop (`9px 14px` / `12px` / `6px`) and at mobile (`9px 14px` / `8px`), its `.icon-btn` strip keeps 3 targets at 44×44, and both rows compute `font-size` `14px` — pinned to the untouched `html { font-size: 14px }` baseline (`style.css:54`) *and* asserted equal, because a relative-only check passes when typography drifts on both surfaces together (density is CSS-only, typography untouched); *Models density* — the `#model-list` block is asserted both as declared (`4px 14px` / `10px` / list `4px`; mobile row gap `6px`) and as a measured height reduction on the same element (1440px: `64px → 54px`, delta 10px; 430px: `114.48px → 102.48px`), with the row title and sub-line still non-empty and non-zero width; *casing closed set* — the `text-transform: none` selectors read back from the served stylesheet are exactly the eight D-3 state-word surfaces, no `text-transform` declaration names `.pl-chips-wrap`, the base `.status-badge` rule still declares `uppercase`, pipeline chips compute `uppercase`, and a stopped pipeline's aggregate badge stays `uppercase` (a class-scoped control, proving the exemption is not container-wide); *T-6 touch targets* — every `#model-list` action button is ≥44×44, inside the viewport and clear of `.model-row-main` at all eight viewports (1920/1440/1280/1024/768/600/430/375); *T-10 / D-8 reduced motion* — the project's first automated reduced-motion context (`browser.newContext({reducedMotion:'reduce'})`): the Models row transition duration is `0s` and the ADR 022 ring reports `animation-name: none`, with the same probe asserted sensitive without reduced motion (`0.15s` / `modelSpin`) so the leg cannot pass vacuously; one AC-7 geometry leg measures the ring's own box in the denser row at 1440 (`16×16`, inside both the action strip and the compacted row) rather than only asserting its presence; *UX V-2.1 legs* (2026-10-27, A-2/A-3/A-4/A-8) — the progressive merge is measured on the same element with the merge neutralized (`main.style.display='block'`, strip back to `width:100%`), asserting `display:contents` and the strip sharing the metadata line at 631/768, the wrapper keeping its box and the strip owning its own line at 630, a row-height delta ≥8px at 631 vs 630, a merged-vs-unmerged reduction at 631/768, untruncated metadata, 44×44 targets with zero pairwise overlap and zero row escape on the five- and four-button rows at all three widths, the three-button transitional strip measured by swapping in the renderer's own ring host, no page-level overflow and the merge state read at 375/430/600/630/631/632/768/769, and a served-stylesheet scan proving exactly one `display: contents` declaration, its `(min-width: 631px) and (max-width: 768px)` media, and its source order after the `≤768` density block. **156 legs per run** as measured locally on Windows at the UX V-2.1 implementation gate (2026-10-27); the exact-SHA CI recount is owed at publication |
| `orphan.cjs` | Orphan recovery, RU/EN badges/tooltips, Kill/Dismiss buttons, Dismiss → stale, helper survives Dismiss, Models-page orphan display (badge, no Start, PID, state filter); **ADR 022 boundary leg** — a transitional-status indicator is never rendered on an orphan row: the row carries the info control instead of a hint sentence and shows no transition marker, asserted in RU and EN; **ADR 023 state-word legs** (2026-10-27) — the renamed word is asserted **as stored** on every state-word surface in RU *and* EN: the Instances table badge, the mobile compact card badge, the Models row badge and the Pipeline **aggregate** badge each compute `text-transform: none` with text exactly «Вне GoAl» / `UNMANAGED`, while the Pipeline **entry chip** in `.pl-chips-wrap` still computes `uppercase` and carries the model name (excluded by construction, not by a restore rule); the `#mf-state` filter stays coherent (`value="orphan"` / `value="pending"` unchanged, labels now the new vocabulary); two T-2 pins assert the four dictionary values byte-for-byte («Вне GoAl» / «Ожидание», `UNMANAGED` / `PENDING`) and that no value of either dictionary still holds a superseded label — without them every other leg compares a rendered badge to the dictionary, so reverting the dictionary would move both sides and stay green; because no app state can be parked on `pending` (sub-frame, MA-06 evidence), five legs resolve the `pending` half of D-3 by appending a badge with the renderer's own classes inside the real container (`#model-list .model-row .model-row-name`, `#adv-instances-body`, `#instances-compact` in RU **and** EN, `#pipeline-list .model-row-name`) and reading its computed casing, with the mirrored controls that the same `orphan` class injected into `#instances-table-wrap` (outside the exempt tbody) and into `#pipeline-list .pl-chips-wrap` still computes `uppercase`; the T-9 render-site split is proved on both sides — the Logs selector keeps the frozen `history.state.orphan` sentence and the History badge of a dismissed (`stale`) instance still renders **uppercased**, so the exemption demonstrably did not leak onto the History/Logs surfaces. The fixture now also owns a pipeline and entry, so the orphan instance is pipeline-attributed and the aggregate/chip pair is rendered naturally rather than synthesized; **ADR 023 UX V-2.1 T-4′ legs** (2026-10-27, A-1/A-8) — the Models orphan strip is asserted text-free with exactly one `BUTTON type="button"` carrying `data-tooltip` and `aria-label` equal to the dictionary hint, ≥44×44, no native `title` repeating the hint anywhere in the row, and Logs/Edit/Delete unchanged after it, in RU and EN; interaction follows the Owner-ratified **C-4 (a)** contract and is driven on **one DOM node** through bounded helpers (the shipped 3000 ms `innerHTML` rebuild is ADR 022 F-3 debt and is disclosed, not fixed): mouse pointer enter shows the hint with `aria-describedby="goaltip"`, a click while it is shown neither shows nor hides it, and pointer leave hides it and clears the attribute; keyboard Tab reaches the control, focus paints the shared outline and shows the hint, Enter and Space perform no tooltip action, the next Tab moves to Logs and retargets the single `#goaltip`, and blur hides it and clears the attribute; a `hasTouch` context proves one tap survives 400 ms — the trailing synthetic `mouseout` does not dismiss it — and a second tap hides it; one more leg arms the touch-activation flag with a synthetic touch `pointerup` that produces no click and proves a later Enter is still inert — it failed on the pre-fix bytes (`after_leave=true`: the stale flag let Enter latch the hint and the `mouseout` early-return kept it up) and passes after the `keydown` disarm. **64 legs per run** locally on Windows at the 2026-10-28 Pre-Commit Reconciliation Gate — the S-3 leg was added there, on top of the C-4 Tooltip Implementation Gate's 63 from the same day; the exact-SHA CI recount is owed at publication |
| `migration.cjs` | v5 → v8 repository migration on startup (v7 loads unchanged, empty `pipelines` key added), migrated model resolves |
| `pipeline.cjs` | Pipeline UI (ADR 010 + ADR 013 + 2026-09-09 owner-UI reconciliation): create/edit/delete via the **linear builder** (vertical blocks, no DAG/connectors) — a compact **segmented args control** [From model | Custom] (Custom = textarea) + explicit **↑/↓/× reorder controls** (disabled at bounds) + **drag handle** (draggable, accessible), `+ Add model`; **no per-entry autostart** (the legacy `auto_start` field round-trips but is ignored — an Active pipeline launches all entries); **repeatable model entries** (the same Model twice — accepted, distinct entry `id`s; per-entry chip state; **no ↯, no ⚡, no ×N badge**); pipeline `Active` toggle (the only autostart control; a setting, not a launch); a derived per-entry **State** column (pure projection — no new backend state); **Models-style inline icon strip** (stopped: Start+Edit+Delete; running: Restart+Stop+Edit+Delete — no overflow "…" menu); Start/Stop/Restart end-to-end (owned instances carry `pipeline_id` + `pipeline_entry_id`); name ellipsis `title` (no mid-word wrap); **form head geometry / AutoStart row layout** (OWNER-UX-02 2026-09-25, corrected through three Owner rechecks on 2026-09-26 to the Owner's final target structure, **OWNER PASS 2026-09-28**; Create **and** Edit share one modal + form): the form head `.pl-form-head` is a two-column grid — `max-content` for the compact AutoStart column + `minmax(0,1fr)` for Name — so the **AutoStart group and the Name group occupy one horizontal row** with no squeeze/overlap while `Модели (порядок запуска) *` / `Models (launch order) *` starts full width on the next row; each field's caption sits **above its own control** (the «Автостарт»/"Autostart" block `<label for="pl-active">` above the switch, the Name label above `#pl-name`) and both captions read on one visual level — the inline `caption ↔ toggle` variant and the helper text under AutoStart were both rejected by the Owner; there is still exactly **one** AutoStart control, and the caption and the switch-only `label.pipeline-autostart` both target `#pl-active`, so clicking either toggles it; the `pipelines.field.active_hint` RU/EN keys stay in the dictionaries because they are still referenced in `app.js` (unrelated i18n cleanup is out of this slice's scope); the AutoStart column hugs its content so no dead space or extra height is left under the toggle and the switch is never stretched toward the Name input, Edit prefills from stored state and closing the modal writes nothing, RU/EN captions differ as expected and a language switch does not disturb the row; below the existing 768px break the head stacks in the order AutoStart → Name → Models with the Name input full width, each label still above its own control and no horizontal overflow — **layout only**: `#pl-active`, `#pl-name`, `Pipeline.Active`/`AutoStart` semantics, schema and API unchanged; the quote-aware `parseArgs` parser — **D7.1 hardened tokenization** (JSON-kwargs escaped quotes → exact `{"…":"…"}` token, unquoted Windows-path backslashes, empty `""` token, escaped backslash, nested JSON) + an **end-to-end argv round-trip** (§12d: UI `parseArgs` → persisted `Model.Args` → launch → the exact token the child process receives, via the `fake-runtime argv-file` mode); responsive card list at all widths + mobile card dense composition (name+state header, chips, icon strip) + no horizontal overflow; History rename (Launch History / История запусков); EN/RU parity; **zoom 125%/150% chip-overflow regression** (§11.5: per-entry model-name chip at 1093px/911px — 1366 @125%/150% — stays inside the viewport, ellipsizes with the full name in the title tooltip, 0 page-level overflow, action strip reachable; 1920 unchanged); **no-connectors + bounded-block + no-leaked-generated-source regression guards** (1/2/3 model blocks, duplicate Model ×2, desktop/mobile) |
| `stress.cjs` | Long live-log stream (fake-runtime `flood` mode, ~100 lines/s) past the 2000-line client window: tab stays responsive, rendered log DOM bounded (≤ 2000), logs advance, autoscroll tracks the tail, page-scoped stream (consumer stops on leave — hidden view tail frozen, at most 1 reconnect, replay deduped by sequence), pause/search/clear work, server responsive |
| `validation.cjs` | Wizard validation RU/EN (empty-name localized app error without native HTML5, step-2 runtime guard incl. direct tab jump, happy paths), running-instance zero-time display @430/desktop, long model name: status badge not crushed/clipped and title ellipsizes without overlap at 430px / ~287px (≈ 430 @ 150% zoom) / 1920px |
| `i18n.cjs` | RU/EN nav labels (runtimes item translated, no raw "Runtime"), login failure/network failure localized in RU+EN (raw "invalid credentials" never shown), successful login in EN, server-message mapping (known strings localized, unmatched strings kept in a localized frame, stable `code` wins over message), **raw-key detector** (`window.i18nMissing` empty + DOM regex scan for i18n key patterns after each switch), **repeated RU→EN→RU→EN→RU round-trip** (×2 rounds, no raw keys, nav localized each round), **dynamic DOM relocalization** (navigate + switch: page title follows the new locale) |
| `connection.cjs` | Server connection feedback: sidebar dot starts online; with all requests aborted (server "down") the dot turns red and a top banner appears (RU/EN text); after unabort the dot returns to green, the banner hides, and a localized recovery toast is shown — loss/recovery detected within two 5 s health-probe cycles |
| `instance-id.cjs` | Instance ID presentation + routing identity: three simultaneous instances of ONE model are pairwise distinguishable in the Instances table and the 430px compact cards (compact cells = `…` + full-id tail-16, derived from the full id), the full canonical id stays reachable via `title` (table cell, compact card, Logs bar), Logs selector labels are pairwise distinct while option values remain the FULL ids, per-line log badges use the short suffix form (`…` + full-id tail-8) and are pairwise distinct, and Stop/Restart/Logs onclick targets remain the FULL instance ids; History rows keep the full id in `title` |
| `portable.cjs` | Portable Configuration UI (ADR 014 Slice 3 + the 2026-09-25 SKIP-EXISTING amendment, **re-targeted 2026-10-12 to the ADR 018 Slice 2′ RESTORE vocabulary and wire** + 2026-09-29 OWNER-EXPORT-01 save-dialog slice): Settings section visibility RU/EN, export (all + model root closure, filename, format, env-absent), **export destination selection (OWNER-EXPORT-01)** — the save-dialog capability is controlled per test branch (`showSaveFilePicker` shadowed on `window` or removed, because a headless runner cannot drive a native OS dialog) and the semantics are proved rather than the pixels: **preferred path** calls the picker *before* the export fetch, suggests `goal-portable-config.json` with a `.json` type filter, writes the fetched bytes through `createWritable`/`write`/`close`, shows success **only after** `close()` and produces **zero** browser download events; **cancel** (`AbortError`) ends the operation silently — picker-only trace, no fetch, no download, no toast, button restored; **picker rejection** (non-`AbortError`) shows the neutral localized «Не удалось сохранить файл»/"Could not save the file" error and does **not** silently start a fallback download; **write/createWritable/close failures** report «Не удалось записать файл»/"Could not write the file", never success, and abort the writable; **capability absent** keeps the original fallback contract — exactly one download per export, suggested name `goal-portable-config.json`, never a picker call; **security boundary** — the export request carries only the documented selector query, and the fake picker's absolute destination path appears in no URL, query, header or body, **GoAl-owned controls** (export `select` styled from live theme tokens — background/color compared against `--bg-input`/`--text-primary`/`--border` in the resolved theme, not a luminance guess; import file picker shows a GoAl «Выбрать файл»/"Choose file" button + GoAl-owned empty/filename label in RU and EN, while the real `input[type=file]` stays functional but visually hidden ≤2×2 and `setInputFiles` still works; security warning present but visually secondary), **import plan UX** — file validity is separate from the plan: per-type «новые / будут восстановлены / без изменений» lines (+ «заблокировано» where applicable) and the «Будет импортировано» / «Будут восстановлены» totals, readable RU/EN blocked reasons with the raw token only inside a collapsed "Technical details", **a failed check stays a failed check** (injected 403 → «Не удалось проверить конфигурацию», neither the invalid-file caption nor a conflict), **Import enabled iff `can_import` = (new + update > 0) and blocked == 0** (all-up-to-date → «Импортировать нечего — репозиторий уже соответствует файлу.» with Import disabled and no total line; blocked → «Конфигурация корректна, но импорт невозможен» with Import disabled), **the Slice 2′ wire is asserted over every captured import body** (`PLAN_KEYS` / `PER_TYPE_KEYS` shape, per-type add-up and cross-consistency, and a hostile check that fails the suite on any `existing` / `skipped` key), **RESTORE behavior is proven end to end** (§6 happy path — validate → confirm → a result separating «добавлено / восстановлено / без изменений», entities present, no instances started; §7.1–7.11 — export → modify → re-import as a *valid pending restore*: future tense «будут восстановлены» with the past tense forbidden on the plan (7.3), import and restore totals kept apart, Import enabled for a restore-only plan, the dry run writing nothing to either stored record, then the applied result in the past tense (7.8) with the runtime and model projections actually applied (7.9, 7.10) and a restore that marks a model active still launching nothing (7.11); §7A.1–7A.6 — re-importing the applied file says there is nothing to import, counts up-to-date entities per type, shows no total line, keeps Import disabled, presents no raw status token and keeps the restored bytes; §7B.1–7B.8 — blocked plans: per-type accounting, every blocked type listed with its own count, no transitional vocabulary, the readable reason naming the current owner by ID, a dependent model blocked rather than silently dropped, tokens only in the collapsed block, `(related: <id>)` and never `(existing: <id>)`, Import disabled and nothing written; §7C.1–7C.7 — a mixed new + restore + up-to-date plan counted per type, both totals kept apart, Import enabled, all three outcome classes reported, the up-to-date record left byte-identical, the restore-only field change applied and the new entity created beside them; §7D.1–7D.11 — the same contract re-asserted in EN: ratified pending-restore wording, the past tense and retired words forbidden on a pending plan, Import enabled for a restore-only plan, the blocked headline as validity-plus-impossibility, readable rather than raw reasons, and every captured body on the Slice 2′ wire), 409 blocked plan writes nothing, invalid format (error, import disabled), undefined variable (validation passes, raw string preserved), malformed variable (error, import disabled), responsive 430px (no overflow, picker fits), i18n completeness (no missing `portable.*` keys EN/RU), no unexpected console errors, no 5xx |
| `exit-reason.cjs` | Human-readable exit reason in Launch History (ROADMAP Instance History item): deterministic scenarios via `fake-runtime` + pre-seeded repository — normal completion (`exited/normal` → "Completed"/«Завершен»), explicit Stop (`exited/signaled` → "Stopped"/«Остановлен», never a crash, raw code tooltip-only), non-zero exit (`failed/failure/42` → "Crashed"/«Аварийно завершен» + "Exit code: 42" tooltip), start failure (missing executable with HTML-active path chars → `failed/error` + `last_error` rendered verbatim/escaped in "Startup failed: …"/«Не удалось запустить: …»), orphan killed via Kill (→ stale/`killed-by-user` → "Force stopped"/«Принудительно остановлен»), orphan dismissed (→ stale/`reconciled-by-user` → "Dismissed"/«Отклонено»), dead PID recovery (→ stale/`pid-not-found` → "Process not found"/«Процесс не найден»); RU/EN parity, 430px compact rows + no page-level horizontal overflow, no console errors, no unexpected 5xx |
| `https.cjs` | **Native HTTPS / secure origin** (ADR 019 Slice 5) — **58 checks** in sections `0`–`10`, run against the real binary with a `tls` block so the HTTP (`webPort`) and HTTPS (`tls.port`) listeners **coexist** on one host (§D2/§D6). `0.x` dual-listener startup: both §D19 startup lines present, the HTTPS one carrying `tls=enabled cert= expires= san=` while the HTTP line carries none, no private-key material or key path in any log line, and a plaintext request to `tls.port` refused rather than served. `1.x`/`4.x` the secure origin itself: `https://goaltls.invalid:<port>` reports `isSecureContext true`, the SPA really rendered over TLS, and `showSaveFilePicker` is installed **and browser-native** (`[native code]` in the function source plus own-property/prototype-member checks, with the suite proving it installed no stub anywhere). `2.x` the insecure twin: plain HTTP on the same host reports `isSecureContext false` and the picker is absent entirely. `3.x` the false-PASS controls: `http://127.0.0.1` is *already* a trustworthy origin to Chromium (so testing on loopback would have passed without TLS — this suite never does), and the tested host string is not intrinsically potentially-trustworthy — its security comes from the TLS connection. `5.x` the fallback branch on the insecure origin: exactly one browser download, the portable export name, the export request reached the server, button restored, success toast. `6.x` the picker-first handler on the secure origin, including an inert-click negative control, the handler demonstrably running **before** the native call, and **`6.4` the headless native dialog cancelling silently with zero downloads — which is explicitly NOT an OS Save As completion**. `7.x`/`8.x` §D18: `Secure` absent on both cookies over the plain connection and present on both over TLS in the same jar with every other attribute unchanged (connection, not host name, decided it), the spoofed-`X-Forwarded-Proto` emit control (header delivery through the browser stack is **not** claimed), and the measured **one-way session ratchet** after an HTTPS login (C3/C5a) that GoAl neither prevents nor repairs. `9.x` TLS failures are loud: untrusted certificate without bypass, unmapped host name, and a non-SAN name under bypass — which is why certificate identity is read from the §D19 log line instead. `10.x` harness hygiene: `--no-proxy-server` mandatory with an ambient-proxy control, `--host-resolver-rules` mapping rather than a hosts-file edit, the RFC 6761 reserved `.invalid` TLD, no unexpected console errors, no 5xx. Certificate material is generated per run by `testdata/tls-fixture` and removed on every exit path (the `finally` at `:455-458`, plus the generator's catch at `:38` and the failed-bring-up abort at `:154`). **This suite is automated evidence only** — a headless runner cannot present an operating-system dialog — **while the Owner's separate manual leg, OWNER-EXPORT-01, is OWNER PASS (2026-10-03)** (result C of [`docs/acceptance/ADR019-HTTPS-BROWSER-ACCEPTANCE.md`](acceptance/ADR019-HTTPS-BROWSER-ACCEPTANCE.md)) |

| `stopping.cjs` | **Observable `stopping` transitional state, browser leg** (ADR 021 Slice 4 / T-8; acceptance contract [ADR021-LIFECYCLE-VISIBILITY-MA-06-BROWSER-ACCEPTANCE.md](acceptance/ADR021-LIFECYCLE-VISIBILITY-MA-06-BROWSER-ACCEPTANCE.md)) — **112 checks per run** over four controlled stop intervals on the **seam-free** binary: `I1` model-route stop in RU, `I2` instance-route stop in EN, `I3` pipeline-route stop in EN, `I4` model-route stop in EN (ADR 022), plus seven run-level legs (fixture seeding, the shipped 3000 ms refresh with no test seam, RU+EN badge labels, the ADR 022 duplicate-word negative control, 0 uncaught exceptions, 0 5xx). The whole `stopping` window comes from the maintained `fake-runtime` `signal-delay` mode (real OS signal, self-delayed exit, `DWELL_MS = 4000`) — **no production delay, no dwell flag, no fabricated response, and the package-private `stopGate` is not installed** (RV-8). Per interval: `GET /api/v1/instances` and `GET /api/v1/instances/{id}` both answer `stopping` and agree (the only admissible stopping sources, RV-7); `GET /api/v1/pipelines/{id}` is read **only as a negative control** and asserted unchanged (`stopped`, RV-7/AC-10); the managed process is proven alive mid-window by **process identity** — `platform.RecoveryProber.GetProcessIdentity` via the `testdata/proc-identity` probe, with `ExecutablePath` compared to the instance's frozen `Executable` and `StartTime` to the run's `StartedAt` under production `verifyIdentity` semantics — because after termination the same probe still answers `alive=true` with an empty executable, so a bare PID-exists check would be inadmissible (F-7.2); the browser's own network record and the painted DOM badge are bound to the same interval through the recorded timestamps `t0…t5` with the ordering rule `t0 < t1 ≤ t2 < t5` **and** `t0 < t3 ≤ t4 < t5`, so a post-termination badge can never produce a PASS; and reads never stall behind the stop (slowest server read printed against a 1500 ms guard while the dwell is 4000 ms). Two adjudications are printed rather than hidden: `Stop` is synchronous, so terminal states legitimately appear between the fixture's estimated exit (`live_end`) and `t5` and are reported as a count, not judged as a defect; and the shipped 3000 ms poll legitimately leaves the page on the pre-stop state for up to one refresh period, so the lag is asserted as a timer property (`t3 ≤ t4`, `read_to_paint ≤ 3000 ms`) with the stale-sample count printed (AC-3 forbids recording a late poll as a product failure). Missed windows and aborted captures are reported as **NOT OBSERVED**, never as FAIL (§16). On the two model-route intervals (`I1` RU, `I4` EN) the ADR 022 model-card contract is asserted on the same painted `stopping` sample the badge legs bind to: the action strip carries **no second state word** (`strip_text === ''`, the badge is the only one), the transition indicator is present, visible, text-free, `aria-hidden="true"`, not focusable and carries no `title`/`data-tooltip`/`aria-label`, and the row exposes **no** Start/Stop/Restart control while the transition lasts; after convergence the indicator is gone and Start is back. A run-level negative control feeds the duplicate-word predicate a synthetic strip holding the RU `stopping` label, so the leg cannot pass tautologically. `orphan.cjs` asserts the other side of that boundary in RU and EN: the orphan row carries the info control instead of a hint sentence and renders no indicator. The other entries of `GET /api/v1/instances` are compared as a set, because `Supervisor.List` iterates a map and the route's slice order is unspecified. **This leg is `[A]`/`[C]` automated evidence: it satisfies no F-7.x row and no AC row, and the Owner's `[O]` observation of the badge (F-7.1 … F-7.14) is a separate, non-substitutable leg that has NOT been run** (RV-9) |

Run locally:

```bash
cd tests/browser
npm install
npx playwright install chromium
npm test
```

`npm test` runs all fifteen suites (the fourteen tabled above plus the ADR 013 `acceptance.cjs` owner-acceptance harness) sequentially and exits non-zero on any failure. *(2026-10-23, ADR 021 Slice 4: `stopping.cjs` is the suite added at this gate; until then the table had thirteen rows and the chain fourteen suites.)* *(2026-10-26, ADR 022 publication: the stopping suite grew from **79** to **112** checks per run — the model-route transition legs plus the orphan-hint boundary leg — recounted from raw `PASS |` lines at `3342719c7e055bbe3ff3b805c91aa8a2ba925f64` (CI `40234792453`: browser **891 raw `PASS |`** / 0 `FAIL |`). The **79** figure stays the Slice 4 record's own count (`b5c7c15858df3de84bab1b09043a0326088b51a3`, CI `40012944730`, 856 raw `PASS |`) and is not restated as current.)* *(2026-10-27, ADR 023 UX V-2 implementation gate, local Windows run: the same fifteen-suite chain re-ran with every suite exit 0 — **943 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |`** — and the +52 over 891 is exactly this gate's new legs, `responsive.cjs` 96 → 123 and `orphan.cjs` 28 → 53, with the other thirteen suites unchanged. The exact-SHA CI recount is owed at publication; a run's figure belongs to its own run.)* *(2026-10-27, ADR 023 UX V-2.1 implementation gate, local Windows run: the same fifteen-suite chain re-ran end to end with every suite exit 0 — **985 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |`** — `responsive.cjs` **156** and `orphan.cjs` **62**, the other thirteen suites at their `95829e5e` totals (core 57 — it prints an intermediate 49 at the auth-OFF/auth-ON seam before its final total — migration 20, pipeline 161, stress 22, validation 26, i18n 46, connection 14, acceptance 38, instance-id 23, portable 147, exit-reason 43, https 58, stopping 112). Against the 943-raw run at `95829e5e` the delta is exactly +42: responsive 123 → 156 (+33) and orphan 53 → 62 (+9). These are local measurements of these bytes, not CI evidence; the exact-SHA CI recount is owed at publication.)* *(2026-10-28, C-4 Tooltip Implementation Gate, local Windows run: the same fifteen-suite chain re-ran end to end with every suite exit 0 — **986 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |`** — `orphan.cjs` **63**, `responsive.cjs` **156**, the other thirteen suites at their V-2.1 totals (core 57 — it prints an intermediate 49 at the auth-OFF/auth-ON seam before its final total — migration 20, pipeline 161, stress 22, validation 26, i18n 46, connection 14, acceptance 38, instance-id 23, portable 147, exit-reason 43, https 58, stopping 112). Against the 985-raw V-2.1 run the delta is exactly +1: `orphan.cjs` 62 → 63, the new blur leg, with the replaced Enter-toggle pair counted as the single Enter/Space leg. These are local measurements of these bytes, not CI evidence; the exact-SHA CI recount is owed at publication.)* *(2026-10-28, Pre-Commit Reconciliation Gate, local Windows run: the same fifteen-suite chain re-ran end to end on the final bytes with every suite exit 0 — **987 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |`** — `orphan.cjs` **64** (the S-3 leg above), `responsive.cjs` **156**, the other thirteen suites unchanged from the run immediately above (core 57, migration 20, pipeline 161, stress 22, validation 26, i18n 46, connection 14, acceptance 38, instance-id 23, portable 147, exit-reason 43, https 58, stopping 112). Against the 986-raw run the delta is exactly +1, the S-3 leg; the leg was first run as a two-sided control, failing on the pre-fix `app.js` (`after_leave=true`) and passing after the `keydown` disarm. This run printed **no** `SKIP |`, which is not a statement about the `stopping` correlation legs: `ADR022-SKIP-01` stays OPEN, a `SKIP` is never a `PASS`, and nothing here is inherited by a later run. These are local measurements of these bytes, not CI evidence; the exact-SHA CI recount is owed at publication.)* *(2026-10-30, Browser Acceptance CORE 24.1 Harness Lifecycle Correction gate, local Windows run: the same fifteen-suite chain re-ran end to end with every suite exit 0 — **989 raw `PASS |` / 0 `FAIL |` / 0 `SKIP |`** — `core.cjs` **59** (it now prints an intermediate **50** at the auth-OFF/auth-ON seam, against 49 before), the other fourteen suites unchanged from the run above (responsive 156, orphan 64, migration 20, pipeline 161, stress 22, validation 26, i18n 46, connection 14, acceptance 38, instance-id 23, portable 147, exit-reason 43, https 58, stopping 112). Against the 987-raw run the delta is exactly +2: the new auth-OFF hand-off leg `22.4` and the auth-phase tolerance control `24.2`. **What the correction fixed:** `core.cjs` retired the auth-OFF page only in its `finally`, so that page — with its 3 s refresh loop and 5 s health probe — was still live while the suite deliberately stopped the server to restart it with authentication enabled; its requests reached the severed listener, and the resulting `net::ERR_CONNECTION_REFUSED` console notices landed in the same suite-wide array that leg `24.1` reads, which is how identical browser bytes failed one run (CI `40936180054` at `8917430e`) and passed another (`40931277871`). The page is now closed before the restart, `22.4` asserts that the auth-OFF phase hands off with an empty console-error array, and `24.1` scopes to entries recorded after that boundary; `24.2` pins the tolerance to pre-login 401 notices, so a dropped connection or a 5xx still fails the leg. No production byte moved. These are local measurements of these bytes, not CI evidence; the exact-SHA CI recount is owed at publication.)*

**CI:** the `browser-acceptance` job in `.github/workflows/ci.yml` runs the same `npm test` on ubuntu-latest with headless Chromium.

## Build

### Local build

```bash
go build -o goal ./cmd/goal
```

### Cross-compile

```powershell
# Windows
$env:GOOS='windows'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; go build -o bin/goal-windows-amd64.exe ./cmd/goal

# Linux
$env:GOOS='linux'; $env:GOARCH='amd64'; go build -o bin/goal-linux-amd64 ./cmd/goal
```

## Project structure

| Path | Purpose |
|------|---------|
| `cmd/goal/` | Main entry point |
| `cmd/goal-msi/` | MSI installer builder |
| `internal/config/` | Config parsing, validation, hot-reload, migrations |
| `internal/process/` | Process lifecycle, log store, broker |
| `internal/platform/` | OS-specific process handling, Windows service (SCM) integration |
| `internal/storage/` | JSON repository (authoritative persistence) |
| `internal/domain/` | Domain types, DTO converters |
| `internal/application/` | Business logic services |
| `internal/webui/` | HTTP server, handlers, embedded UI, security |
| `testdata/fake-runtime/` | Fake runtime for integration tests |
| `deploy/` | Systemd unit file (the Windows service is registered in-binary via `goal --service`, ADR 011) |
| `scripts/` | Build and bootstrap scripts |
| `docs/acceptance/` | Manual Owner acceptance checklists and evidence records, each bound to the repository bytes it observed: real-SCM Windows Service acceptance ([ADR011-WINDOWS-SERVICE-MANUAL-ACCEPTANCE.md](acceptance/ADR011-WINDOWS-SERVICE-MANUAL-ACCEPTANCE.md)), the externally verified runtime interoperability register ([RUNTIME-INTEROPERABILITY-EVIDENCE.md](acceptance/RUNTIME-INTEROPERABILITY-EVIDENCE.md)), and the ADR 019 native HTTPS browser-acceptance record ([ADR019-HTTPS-BROWSER-ACCEPTANCE.md](acceptance/ADR019-HTTPS-BROWSER-ACCEPTANCE.md)), and the ADR 018 portable import / restore browser-acceptance record ([ADR018-PORTABLE-IMPORT-BROWSER-ACCEPTANCE.md](acceptance/ADR018-PORTABLE-IMPORT-BROWSER-ACCEPTANCE.md)) |

## Code conventions

- Never launch runtimes through `sh -c`, `cmd /c`, PowerShell interpolation, or another shell.
- Process arguments are `[]string`.
- Each `exec.Cmd` has exactly one owner calling `Wait()`.
- HTTP handlers must not manage `exec.Cmd` directly.
- Merge model environment variables with parent process environment.
- Platform-specific behavior belongs in `internal/platform/`.

## Adding a new endpoint

1. Define handler method in appropriate `*Handler` in `internal/webui/handlers/`.
2. Register route in `routes.go:Build()`.
3. Add `requireAuth` or `requireAuthCSRF` wrapper as needed.
4. Write unit test in `_test.go` file in same package.
5. Add to [API.md](API.md).

## Adding a new config field

1. Add field to `Config` struct in `internal/config/config.go`.
2. Update `Default()` if it has a default value.
3. Update `Validate()` if validation is needed.
4. Add migration step in `migrateV1ToV2()` (or new migration function) if migrating from existing version.
5. Update [CONFIGURATION.md](CONFIGURATION.md).

## ADR process

New architectural decisions should follow the existing ADR format in `docs/adr/`:

```markdown
# ADR XXX: Title

**Status:** Draft | Proposed | Accepted | Superseded
**Date:** YYYY-MM-DD
**Related:** ADR XX

## Context

## Decision

## Consequences
```

Statuses:
- **Draft** — under discussion
- **Proposed** — decision made, implementation pending
- **Accepted** — implemented or being implemented
- **Superseded** — replaced by a later ADR

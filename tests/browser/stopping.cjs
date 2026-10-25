'use strict';
// ADR 021 Slice 4 — maintained browser leg for the observable `stopping`
// lifecycle state (MA-06 F-7).
//
// Per controlled stop interval this suite records, from real bytes and real
// network records, the facts and the t0…t5 correlation line that the acceptance
// contract §"F-7.5 correlation and dwell requirements" enumerates:
//   t0 the browser issued a stop request through the shipped UI action button
//   t1 GET /api/v1/instances/{id} answered `stopping` while the stop was in flight
//   t2 that PID was alive AND resolved to this run's executable and start time
//      through the same platform anchors production verifyIdentity compares
//   t3 the page's own GET /api/v1/instances response body carried `stopping`
//   t4 the shipped render path painted that state, still inside the interval
//   t5 the stop response returned — the earliest provable upper bound on
//      termination, `Stop` being synchronous — after which the identity no
//      longer resolves to this run and the terminal state converges across API,
//      durable record and UI
//
// What this file deliberately does NOT claim:
//   - It is agent-collected [A]/[C] evidence. Under the contract's global actor
//     rule (RV-9) no browser criterion is met by an agent-driven UI action, so
//     F-7.5, F-7.11 and F-7.12 stay open until the Owner observes the badge.
//     Every leg below is worded as one observation, never as "F-7.x PASS".
//   - The stopping interval is never read from `GET /api/v1/pipelines/{id}`: that
//     route promotes only `IsRunningOrStarting()` and so reports a stopping
//     instance as `"stopped"` (RV-7). It is read only as a negative control
//     proving the endpoint projection did not change.
//   - The dwell comes from the test-only fake runtime (`signal-delay`), never
//     from production timing, and the binary is built from the repository under
//     test. Where the platform gave no graceful stop interval the window legs are
//     reported NOT OBSERVED (a visible SKIP line), never as a product FAIL — see
//     the acceptance contract §16 and the AH-4 scope note.
//   - The shipped page paints from the response its own 3000 ms timer received
//     (`app.js:2358-2364`; the period is read from that shipped file at run time),
//     so a DOM sample taken before that read legitimately shows the pre-stop state
//     and a ~4 s window can end with no page read inside it at all. ADR 021
//     authorizes no client-side change and its AC-3 forbids recording a missed or
//     late poll as a product defect, so the presentation legs assert the properties
//     the ratified design does guarantee — no premature terminal presentation while
//     the process is alive, no server-read or paint regression after `stopping` was
//     first reported, and a badge that followed the page's own `stopping` read —
//     report the measured lag as a number instead of hiding it, and answer an
//     absent client-side receipt with NOT OBSERVED. A receipt that exists and is
//     wrong (non-2xx, unparseable, a stopping read never painted) is still a FAIL.
//   - `Stop` is synchronous, so the supervisor can write the terminal state a few
//     milliseconds before the stop response lands (t5). A read answered in that
//     tail is a post-termination reading per §16: the live-window legs are bounded
//     by the fixture's own exit estimate (signal delivery + the dwell it promised),
//     and the tail is printed as a count rather than judged or discarded.
const path = require('path');
const fs = require('fs');
const H = require('./harness.cjs');

const PORT = 19489;
const BASE = `http://127.0.0.1:${PORT}`;

// 4000 ms is the dwell AH-4 measured (~4006–4007 ms graceful interval), i.e. the
// configuration whose fixture capability is closed. It stays under the 5 s
// ForceKill grace in internal/process/manager.go, so the interval is a real
// graceful stop, and it is longer than the app's 3000 ms refresh period, so at
// least one shipped-tick read necessarily lands inside it.
const DWELL_MS = 4000;
const DOM_POLL_MS = 25;
// Bounds the paint sample list if a run ever stalls far past the expected dwell;
// a 4 s interval at 25 ms needs ~160 samples.
const PAINT_SAMPLES_CAP = 600;
const ITEM_POLL_MS = 40;
const TIGHT_POLL_DEADLINE_MS = 3000;
const READ_STALL_GUARD_MS = 1500;
const TERMINAL_DEADLINE_MS = 20000;
const STOP_RESPONSE_DEADLINE_MS = 40000;
const UI_REFRESH_PERIOD_MS = 3000;
// Mirrors internal/process/supervisor.go timesApproximatelyEqual. The platform
// start-time anchors are not sub-second comparable (Linux derives them from /proc
// clock ticks plus an integer btime), so production uses the same tolerance.
const IDENTITY_TOLERANCE_MS = 5000;
const PIPE_NAME = 'S4 Stopping Pipeline';
// The expected badge words are read from the same shipped i18n files the app
// renders from, so the legs compare the painted word against the product's own
// label source instead of a copy typed into this test.
const I18N = { ru: require('../../internal/webui/static/i18n/ru.json'), en: require('../../internal/webui/static/i18n/en.json') };
const LABELS = { ru: I18N.ru['models.status.stopping'], en: I18N.en['models.status.stopping'] };
const RUNNING_LABELS = { ru: I18N.ru['models.status.running'], en: I18N.en['models.status.running'] };
const TERMINAL_STATES = ['exited', 'failed', 'stale'];
// The refresh period this suite reasons about is a property of the shipped page, so
// it is read from the same app.js the browser runs rather than taken on trust
// (startRefresh is the only interval that drives reloadAllData). The pattern is
// whitespace-agnostic so the check holds for LF and CRLF copies of the file alike.
function shippedRefreshPeriod() {
  let src = '';
  try {
    src = fs.readFileSync(path.join(H.ROOT, 'internal', 'webui', 'static', 'app.js'), 'utf8');
  } catch {
    return null;
  }
  const m = /setInterval\s*\(\s*async\s+function\s*\([^)]*\)\s*\{[\s\S]{0,400}?reloadAllData\s*\(\s*\)[\s\S]{0,400}?,\s*(\d+)\s*\)/.exec(src);
  return m ? Number(m[1]) : null;
}
const STATE_CLASSES = ['running', 'starting', 'stopping', 'pending', 'orphan', 'failed', 'exited', 'stale', 'unknown', 'stopped'];
// States that may legitimately be presented while the managed process is alive.
// Anything else (a terminal class, an absent row, an unrecognized badge) shown
// inside the live interval is a premature termination presentation — the pre-ADR
// 021 symptom ADR 021 §"The 3 s UI refresh cannot win the race" describes as
// `running` → (frozen or straight jump) → `exited`.
const LIVE_STATES_IN_WINDOW = ['running', 'starting', 'pending', 'stopping'];
// The stop response (t5) is the contract's upper bound on termination, but the
// process is expected to exit a little earlier: `Stop` is synchronous, so the
// supervisor observes the exit, writes the terminal state and only then answers.
// A read landing in that tail legitimately reports the terminal state, and §16
// says a post-termination reading is not evidence either way — so the live-window
// presentation legs are bounded by the fixture's own exit evidence, and the tail
// is reported as a count instead of being dropped.
const LIVE_BOUNDARY_MARGIN_MS = 100;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// One monotonic, sub-millisecond clock stamps every fact of the interval. At
// Date.now() granularity a loopback response can land in the same millisecond as
// the stop request, which would turn the contract's strict `t0 < t1` into a coin
// flip about clock resolution rather than a fact about the run; every ordering
// assertion below is therefore decided on `performance.now()` values. Wall-clock
// numbers are recorded only where an artifact has to be findable (the t0 anchor
// printed by the correlation legs, the fixture markers).
const { performance } = require('node:perf_hooks');
const hrNow = () => performance.now();
const wallNow = () => Date.now();
// Offsets are printed to one decimal: a leg detail that rounds 0.4 ms to 0 ms would
// re-introduce exactly the ambiguity the high-resolution clock removes.
const off = (t, base) => (t - base).toFixed(1);

// The legs that only exist inside an observed stop window. Named once so the
// PASS path and the NOT OBSERVED path can never disagree about what was or was
// not measured.
const WINDOW_LEGS = [
  'item route reports stopping',
  'process identity alive mid-window',
  'list route agrees with the item route',
  'durable record still running mid-window',
  'browser response carried stopping',
  'rendered badge shows stopping',
  'no premature terminal presentation while the process was alive',
  'no server read regressed from stopping inside the window',
  'no paint regressed from stopping after the badge appeared',
  'the badge followed the page read inside the interval',
  'reads never stalled behind the stop',
  'correlation t0 < t1 <= t2 < t5',
  'correlation t0 < t3 <= t4 < t5',
];
const PIPE_WINDOW_LEG = 'pipeline endpoint projection stays not-stopping (RV-7)';
// ADR 022: the model row's action strip must carry a decorative indicator instead of
// a second copy of the state word. Named once, for the same reason as WINDOW_LEGS.
const MODEL_STRIP_LEGS = [
  'model action strip carries no second state word',
  'transition indicator is decorative and text-free',
  'no start/stop/restart control during the transition',
];
// hasStateWord is the ADR 022 duplicate-word predicate: any text left in the
// transition strip is a second state word, because the badge is the only one.
const hasStateWord = (stripText) => String(stripText == null ? '' : stripText).trim() !== '';

// pathsEqual mirrors internal/process/supervisor.go pathsEqual: exact match, or
// on Windows case-insensitive with an optional .exe extension in either form.
function pathsEqual(a, b) {
  if (!a || !b) return false;
  if (a === b) return true;
  if (!H.IS_WIN) return false;
  const fa = a.toLowerCase();
  const fb = b.toLowerCase();
  if (fa === fb) return true;
  if (fa.endsWith('.exe') && fb === fa.slice(0, -4)) return true;
  if (fb.endsWith('.exe') && fa === fb.slice(0, -4)) return true;
  return false;
}

function readMarker(markerDir, name) {
  try {
    return JSON.parse(fs.readFileSync(path.join(markerDir, name), 'utf8'));
  } catch {
    return null;
  }
}

// readDurableInstance reads the persisted record directly. The store is written
// through fsutil.WriteFileDurable (temp file, read-back, rename), so a mid-window
// read observes either the complete pre-stop or the complete post-stop snapshot —
// never a torn file.
function readDurableInstance(dataDir, id) {
  try {
    const raw = JSON.parse(fs.readFileSync(path.join(dataDir, 'goal_repo.json'), 'utf8'));
    return (raw.instances || []).find((e) => e.id === id) || null;
  } catch {
    return null;
  }
}

const pick = (list, id) => (Array.isArray(list) ? list.find((i) => i.id === id) : null);

// collectDomInPage runs in the page and is deliberately self-contained: it only
// reads the DOM the shipped render functions produced. Each surface also reports
// whether it is visible, so a hidden-view DOM read is never reported as an
// on-screen observation.
function collectDomInPage(arg) {
  const out = { model: null, inst: null, pipe: null, chip: null };
  const badgeOf = (el) => {
    const b = el && el.querySelector ? el.querySelector('.status-badge') : null;
    return b ? { cls: String(b.className), text: (b.textContent || '').trim(), title: b.getAttribute('title') || '' } : null;
  };
  const visible = (el) => !!(el && el.offsetParent !== null);
  const rows = (sel) => Array.prototype.slice.call(document.querySelectorAll(sel));

  const mrow = rows('#model-list .model-row').find((r) => {
    const n = r.querySelector('.model-row-title');
    return n && (n.textContent || '').trim() === arg.modelName;
  });
  if (mrow) {
    const strip = mrow.querySelector('.model-row-actions');
    const mk = mrow.querySelector('.model-progress');
    out.model = Object.assign(badgeOf(mrow) || { cls: '', text: '', title: '' }, {
      transitional: !!mrow.querySelector('.model-transitional'),
      stopBtn: !!mrow.querySelector('.icon-btn[onclick*="stopModel("]'),
      startBtn: !!mrow.querySelector('.icon-btn[onclick*="startModel("]'),
      restartBtn: !!mrow.querySelector('.icon-btn[onclick*="restartModel("]'),
      stripText: strip ? (strip.textContent || '').trim() : null,
      // ADR 022: the transition indicator must be decorative — no text, no name of
      // any kind (title/data-tooltip/aria-label would re-introduce the state word as
      // a tooltip) and not focusable.
      marker: mk ? {
        present: true,
        text: (mk.textContent || '').trim(),
        ariaHidden: mk.getAttribute('aria-hidden'),
        tabindex: mk.getAttribute('tabindex'),
        named: (mk.matches('[title],[data-tooltip],[aria-label]') ? 1 : 0)
          + mk.querySelectorAll('[title],[data-tooltip],[aria-label]').length,
        visible: visible(mk),
      } : { present: false, text: '', ariaHidden: null, tabindex: null, named: 0, visible: false },
      sub: ((mrow.querySelector('.model-row-sub') || {}).textContent || '').trim(),
      visible: visible(mrow),
    });
  }

  const body = document.getElementById('adv-instances-body');
  const irow = body ? Array.prototype.slice.call(body.querySelectorAll('tr')).find((r) => {
    const c = r.querySelector('.inst-mono');
    return c && c.getAttribute('title') === arg.instId;
  }) : null;
  if (irow) {
    const cells = Array.prototype.slice.call(irow.querySelectorAll('td')).map((td) => (td.textContent || '').trim());
    const idCell = irow.querySelector('.inst-mono');
    out.inst = Object.assign(badgeOf(irow) || { cls: '', text: '', title: '' }, {
      idTitle: idCell ? idCell.getAttribute('title') : '',
      pid: cells.length > 3 ? cells[3] : '',
      visible: visible(irow),
    });
  }

  const prow = rows('#pipeline-list .model-row').find((r) => {
    const n = r.querySelector('.pl-name');
    return n && n.getAttribute('title') === arg.pipeName;
  });
  if (prow) {
    out.pipe = Object.assign(badgeOf(prow.querySelector('.model-row-name')) || { cls: '', text: '', title: '' }, { visible: visible(prow) });
    const chip = Array.prototype.slice.call(prow.querySelectorAll('.pl-chips-wrap .status-badge'))
      .find((ch) => ch.getAttribute('title') === arg.modelName);
    out.chip = chip ? { cls: String(chip.className), text: (chip.textContent || '').trim(), title: chip.getAttribute('title') || '' } : null;
  }
  return out;
}

// badgeState reads the lifecycle state out of a rendered badge class list, which
// is what the shipped renderers write ('status-badge ' + state).
function badgeState(b) {
  if (!b) return '(absent)';
  const hit = STATE_CLASSES.find((st) => new RegExp('(^|\\s)' + st + '(\\s|$)').test(String(b.cls)));
  return hit || ('(no-state-class:' + b.cls + ')');
}

// labelBadgeOf is the surface that carries the localized state word. On the
// pipeline row that is the aggregate badge: the per-entry chip badge carries the
// model name as its text (app.js pipelineChips) and only its class the state.
function labelBadgeOf(spec, dom) {
  if (spec.view === 'models') return dom.model;
  if (spec.view === 'adv-instances') return dom.inst;
  return dom.pipe;
}

// primaryBadgeOf is the surface whose state this interval's view presents.
function primaryBadgeOf(spec, dom) {
  if (spec.view === 'models') return dom.model;
  if (spec.view === 'adv-instances') return dom.inst;
  return dom.chip || dom.pipe;
}

function primaryVisible(spec, dom) {
  const b = primaryBadgeOf(spec, dom);
  if (spec.view === 'pipelines') return !!(dom.pipe && dom.pipe.visible);
  return !!(b && b.visible);
}

function paintSignature(spec, dom) {
  const label = labelBadgeOf(spec, dom) || {};
  return [badgeState(primaryBadgeOf(spec, dom)), badgeState(label), label.text || '',
    primaryVisible(spec, dom) ? 'visible' : 'hidden'].join('|');
}

async function readDom(page, spec, instId) {
  return page.evaluate(collectDomInPage, { modelName: spec.name, pipeName: PIPE_NAME, instId });
}

async function probeIdentity(probeBin, pid) {
  try {
    return await H.processIdentity(probeBin, pid);
  } catch (e) {
    return { probe_error: String(e && e.message ? e.message : e), alive: false, has_start_time: false };
  }
}

async function main() {
  console.log('=== Stopping lifecycle (real Chromium, ADR 021 F-7 browser leg) ===\n');

  const ws = H.makeWorkspace('stopping');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const probeBin = H.buildProcIdentity(ws);

  H.writeConfig(ws, {
    version: 2,
    webPort: PORT,
    authEnabled: false,
    runtimes: [{
      id: 'rt-fake',
      name: 'Fake Runtime',
      executable: fakeRt,
      workingDirectory: path.dirname(fakeRt),
    }],
  });

  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started (auth OFF, seam-free binary).\n');

  const api = (method, urlPath, body) => H.api(BASE, method, urlPath, body);

  // Each interval owns one model and one marker directory, so a marker can never
  // be read as evidence for a different run.
  const specs = [
    { key: 'I1 model-route stop (RU)', tag: 'i1', lang: 'ru', view: 'models', name: 'S4 Stop Via Model', kind: 'model' },
    { key: 'I2 instance-route stop (EN)', tag: 'i2', lang: 'en', view: 'adv-instances', name: 'S4 Stop Via Instance', kind: 'model' },
    { key: 'I3 pipeline-route stop (EN)', tag: 'i3', lang: 'en', view: 'pipelines', name: 'S4 Stop Via Pipeline', kind: 'pipeline' },
    // ADR 022 covers the model card in both languages. Appended, never inserted:
    // specs[2] is the pipeline's own model by index below.
    { key: 'I4 model-route stop (EN)', tag: 'i4', lang: 'en', view: 'models', name: 'S4 Model EN', kind: 'model' },
  ];
  for (const spec of specs) {
    spec.markerDir = path.join(ws.dir, 'markers', spec.tag);
    const m = await api('POST', '/api/v1/models', {
      name: spec.name,
      runtime_id: 'rt-fake',
      args: ['signal-delay', spec.markerDir, String(DWELL_MS)],
    });
    spec.modelId = m.data && m.data.id;
    spec.modelStatus = m.status;
  }
  const pipeRes = await api('POST', '/api/v1/pipelines', { name: PIPE_NAME, models: [{ model_id: specs[2].modelId }] });
  specs[2].pipelineId = pipeRes.data && pipeRes.data.id;

  const suite = H.newSuite('STOPPING (observable transitional state, browser leg)');
  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 }, locale: 'ru-RU' });
  const page = await ctx.newPage();
  suite.watchPage(page);
  let ok = false;

  try {
    suite.log('Seed: four dwell fixtures registered', specs.every((x) => !!x.modelId),
      specs.map((x) => `${x.tag}=${x.modelStatus}`).join(' '));
    suite.log('Seed: pipeline created', pipeRes.status < 300 && !!specs[2].pipelineId, `status=${pipeRes.status}`);

    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(600);
    // Both halves are measurable: the period is read out of the shipped app.js, and
    // the page's own refresh entry point is proven present in the running page. The
    // binary is built from this repository by the harness, so no test-only seam can
    // be in it — `stopGate` is package-private to internal/process and set only by
    // Go tests, which is why that half stays in the detail line instead of an assert.
    const shippedPoll = shippedRefreshPeriod();
    const refreshFn = await page.evaluate(() => typeof window.reloadAllData === 'function');
    suite.log('App loaded with the shipped 3000 ms refresh and no test seam',
      shippedPoll === UI_REFRESH_PERIOD_MS && refreshFn === true && DWELL_MS > UI_REFRESH_PERIOD_MS,
      `app_js_poll=${shippedPoll === null ? '(not found)' : `${shippedPoll}ms`} asserted_period=${UI_REFRESH_PERIOD_MS}ms page_refresh_fn=${refreshFn} fixture_dwell=${DWELL_MS}ms auth=off stop_gate=unset (package-private in internal/process, unreachable from a built binary)`);

    for (const spec of specs) await runInterval({ suite, api, ws, page, probeBin }, spec);

    // The localized word is only observable from an interval whose page actually
    // painted the state, so a tick that missed the window is a coverage gap (§16
    // NOT OBSERVED), never a product FAIL. A word that is painted and wrong is a
    // FAIL and is asserted per interval by 'rendered badge shows stopping' above.
    const seenLabels = specs.map((x) => x.paintLabel).filter(Boolean);
    const labelObserved = `observed=${specs.map((x) => `${x.tag}:${JSON.stringify(x.paintLabel || '(none)')}`).join(' ')}`;
    if (seenLabels.includes(LABELS.ru) && seenLabels.includes(LABELS.en)) {
      suite.log('Badge label painted in RU and EN across the intervals', true,
        `${labelObserved} — [A] half of F-7.5 only; the Owner observation stays open (RV-9)`);
    } else {
      suite.notObserved('Badge label painted in RU and EN across the intervals',
        `${labelObserved} — at least one interval ended without a stopping paint (the shipped ${UI_REFRESH_PERIOD_MS} ms tick missed the ${DWELL_MS} ms window), so the RU+EN pair is not covered by this run; an interval that painted the wrong word is judged FAIL above`);
    }

    // Negative control for the duplicate-word predicate: the same predicate is fed
    // (a) a synthetic strip carrying the RU stopping label — built in the page with
    // the very classes the pre-ADR-022 renderer used — and (b) the empty strip that
    // shipped renderer now produces. It must report the label as a word and the empty
    // strip as clean, so a passing duplicate-word leg cannot be tautological.
    const syntheticStripText = await page.evaluate((label) => {
      const host = document.createElement('div');
      host.className = 'model-row-actions';
      const span = document.createElement('span');
      span.className = 'hint-text model-transitional';
      span.textContent = label;
      host.appendChild(span);
      return (host.textContent || '').trim();
    }, LABELS.ru);
    suite.log('Negative control: the duplicate-word predicate is live',
      hasStateWord(syntheticStripText) === true && hasStateWord('') === false,
      `synthetic_strip_with_ru_label=${hasStateWord(syntheticStripText)} empty_strip=${hasStateWord('')} probe_label=${JSON.stringify(LABELS.ru)}`);

    // Scope notes are prose, never PASS/FAIL/SKIP lines: they state what the
    // executed test does not prove.
    console.log('  [note] This suite is agent-driven [A]/[C] evidence. Per the acceptance contract RV-9 no browser criterion is met by it: F-7.5, F-7.11 and F-7.12 each still require the Owner\'s own observation, and F-7.9/F-7.10 rest on the Go suite, F-7.13 on CI and F-7.14 on the deterministic tests. No F-7 row is claimed PASS here.');
    console.log('  [note] The stopping interval is the test-only fake-runtime signal-delay dwell; no production timing, seam or injected delay is involved and no stopping response is fabricated. The binary is built from the repository under test.');
    console.log('  [note] AH-4 scope: this exercises the same interactive-console configuration that was measured (~4006-4007 ms). A run whose fixture wrote no signal.json produced no graceful interval: those legs are NOT OBSERVED, never FAIL, and prove nothing about Windows Service, detached, session-0 or Linux dwell.');
    console.log('  [note] GET /api/v1/pipelines/{id} is read only as a negative control showing the endpoint projection is unchanged (RV-7); it is never a stopping source.');

    suite.log('Console: 0 uncaught exceptions', suite.consoleErrors.length === 0, suite.consoleErrors.slice(0, 2).join('; '));
    suite.log('Server: 0 5xx responses', suite.serverErrors.length === 0, suite.serverErrors.slice(0, 2).map((e) => `${e.url} ${e.status}`).join('; '));
    ok = await suite.finish();
  } catch (err) {
    suite.log('FATAL: ' + err.message, false, err.stack ? err.stack.split('\n')[0] : '');
    ok = false;
    await suite.finish();
  } finally {
    await browser.close();
    await server.stop();
  }

  process.exit(ok ? 0 : 1);
}

async function runInterval(c, spec) {
  const s = c.suite;
  const K = spec.key;
  spec.paintLabel = null;

  const capture = { stopReqs: [], stopResp: null, listReads: [] };
  // f.listReads aliases the array the response handler pushes into: the window
  // report and the observation series read the browser's own network records
  // from here, so a missing page read is a real measurement gap, never a default.
  const f = { t0: 0, t0Wall: 0, t1: 0, t2: 0, t4: 0, t5: 0, item: null, ident: null, identMs: 0, listReads: capture.listReads, listInWindow: null, durableInWindow: null, pipeDetail: null, itemPolls: [], paints: [], maxHttpReadMs: 0 };
  const state = { inst: null, othersBefore: '', signal: null, term: null, termAt: 0, identAfter: null, paintLoopOk: true };

  try {
    // ── Start one controlled run ─────────────────────────────────────────────
    const startRoute = spec.kind === 'pipeline'
      ? `/api/v1/pipelines/${spec.pipelineId}/start`
      : `/api/v1/models/${spec.modelId}/start`;
    const startRes = await c.api('POST', startRoute);
    s.log(`${K}: start accepted`, startRes.status >= 200 && startRes.status < 300, `POST ${startRoute} -> ${startRes.status}`);

    const inst = await awaitRunning(c, spec);
    state.inst = inst;
    if (!inst) {
      s.log(`${K}: a running instance appeared`, false, 'no running instance for this model within 20s');
      return;
    }
    const ready = readMarker(spec.markerDir, 'ready.json');
    s.log(`${K}: fixture binds this run by pid`, !!ready && ready.event === 'ready' && ready.pid === inst.pid,
      ready ? `marker_pid=${ready.pid} api_pid=${inst.pid}` : 'ready.json missing');
    s.log(`${K}: run carries the frozen identity`, typeof inst.executable === 'string' && inst.executable.length > 0 && !!inst.started_at,
      `executable=${inst.executable} started_at=${inst.started_at}`);

    // ── Baseline presentation before the interval ────────────────────────────
    await c.page.evaluate((v) => window.navigate(v), spec.view);
    await c.page.evaluate(() => window.reloadAllData());
    await c.page.evaluate(async (l) => { await window.setLanguage(l); }, spec.lang);
    const domBefore = await readDom(c.page, spec, inst.id);
    s.log(`${K}: row presents running before the stop`,
      badgeState(primaryBadgeOf(spec, domBefore)) === 'running' && primaryVisible(spec, domBefore)
      && (labelBadgeOf(spec, domBefore) || {}).text === RUNNING_LABELS[spec.lang],
      `primary_state=${badgeState(primaryBadgeOf(spec, domBefore))} visible=${primaryVisible(spec, domBefore)} label=${JSON.stringify((labelBadgeOf(spec, domBefore) || {}).text || '')} want=${JSON.stringify(RUNNING_LABELS[spec.lang])}`);

    const listBefore = await c.api('GET', '/api/v1/instances');
    // Supervisor.List iterates a map (internal/process/supervisor.go:798-802), so the
    // route's slice order is unspecified and may differ between two identical reads.
    // The other entries are therefore compared as a set of id:state, never by position.
    const othersBeforeRaw = (listBefore.data || []).filter((i) => i.id !== inst.id).map((i) => `${i.id}:${i.state}`);
    state.othersBefore = othersBeforeRaw.slice().sort().join(',');
    state.othersBeforeRaw = othersBeforeRaw;

    // ── The interval ─────────────────────────────────────────────────────────
    const stopUrl = spec.kind === 'pipeline'
      ? `/api/v1/pipelines/${spec.pipelineId}/stop`
      : (spec.view === 'adv-instances' ? `/api/v1/instances/${inst.id}/stop` : `/api/v1/models/${spec.modelId}/stop`);
    const handlers = installCapture(c.page, capture, inst.id, stopUrl);
    try {
      const clickAt = hrNow();
      await c.page.click(triggerSelector(spec, inst), { timeout: 4000 });
      while (!capture.stopReqs.length && hrNow() < clickAt + 2000) await sleep(10);
      // t0 is the page's own request record when it exists; the click time is a
      // fallback, so a lost request event can never silently move t0 earlier.
      f.t0 = capture.stopReqs.length ? capture.stopReqs[0].t : clickAt;
      f.t0Wall = wallNow();

      // Two concurrent loops: the item-route tight poll (t1) and the shipped-DOM
      // paint poll (t4). Both only read; neither touches the app's render path.
      const paintLoop = pollPaints(c, spec, inst.id, f, capture);
      const win = await pollItemRoute(c, inst.id, f, capture);
      if (win) {
        f.t1 = win.t;
        f.item = win.item;

        // t2 — process identity through the production platform anchors, timed
        // apart from the server reads: a child-process spawn is not an HTTP read
        // and must not enter the lock-stall guard.
        const probeAt = hrNow();
        f.ident = await probeIdentity(c.probeBin, win.item.pid);
        f.t2 = hrNow();
        f.identMs = f.t2 - probeAt;

        const listAt = hrNow();
        const listRes = await c.api('GET', '/api/v1/instances');
        f.maxHttpReadMs = Math.max(f.maxHttpReadMs, hrNow() - listAt);
        f.listInWindow = listRes.data;
        f.durableInWindow = readDurableInstance(c.ws.dataDir, inst.id);
        if (spec.kind === 'pipeline') f.pipeDetail = await c.api('GET', `/api/v1/pipelines/${spec.pipelineId}`);
      }

      // t5 — the stop returns only once the process exited or the stop context
      // expired, so the response timestamp is the earliest provable upper bound
      // on termination and the ordering bound stays conservative.
      const stopDeadline = f.t0 + STOP_RESPONSE_DEADLINE_MS;
      while (!capture.stopResp && hrNow() < stopDeadline) await sleep(25);
      f.t5 = capture.stopResp ? capture.stopResp.t : hrNow();
      state.paintLoopOk = await paintLoop;
      f.paints.sort((a, b) => a.t - b.t);
      f.t4 = firstStoppingPaint(spec, f);
      spec.paintLabel = f.t4 ? ((labelBadgeOf(spec, paintAt(f, f.t4)) || {}).text || null) : null;

      state.signal = readMarker(spec.markerDir, 'signal.json');
      state.term = await awaitTerminal(c, inst.id);
      state.termAt = hrNow();
      state.identAfter = await probeIdentity(c.probeBin, inst.pid);
    } finally {
      handlers.forEach(([ev, fn]) => c.page.off(ev, fn));
    }

    reportWindow(c, spec, state.inst, capture, f, state);
    await reportTerminal(c, spec, state.inst, capture, f, state);
  } catch (err) {
    s.log(`${K}: interval executed without a harness error`, false, `${err && err.message} — every window leg below is a measurement gap, not a product FAIL`);
    for (const name of WINDOW_LEGS) s.notObserved(`${K}: ${name}`, 'the interval aborted before the measurement completed');
    if (spec.kind === 'pipeline') s.notObserved(`${K}: ${PIPE_WINDOW_LEG}`, 'the interval aborted before the measurement completed');
  }
}

function triggerSelector(spec, inst) {
  if (spec.kind === 'pipeline') return `#pipeline-list .icon-btn[onclick="stopPipeline('${spec.pipelineId}')"]`;
  if (spec.view === 'adv-instances') return `#adv-instances-body .icon-btn[onclick="stopInstance('${inst.id}')"]`;
  return `#model-list .icon-btn[onclick="stopModel('${spec.modelId}')"]`;
}

function installCapture(page, capture, instId, stopUrl) {
  const onRequest = (req) => {
    if (req.method() === 'POST' && req.url().endsWith(stopUrl)) capture.stopReqs.push({ t: hrNow(), url: req.url() });
    else if (req.method() === 'GET' && req.url().endsWith('/api/v1/instances')) req._s4RequestedAt = hrNow();
  };
  const onResponse = (resp) => {
    const url = resp.url();
    const method = resp.request().method();
    if (method === 'POST' && url.endsWith(stopUrl)) {
      capture.stopResp = { t: hrNow(), status: resp.status() };
      return;
    }
    // The app's own refresh read, captured as a network record: t3 evidence. The
    // receipt timestamp is taken when the body has actually been parsed, because
    // F-7.4 is about the browser receiving `stopping` in a response body, not about
    // the response headers arriving; the request timestamp is kept alongside it so a
    // read served before the stop was issued is never judged as a post-stop one.
    if (method === 'GET' && url.endsWith('/api/v1/instances')) {
      const requestedAt = typeof resp.request()._s4RequestedAt === 'number' ? resp.request()._s4RequestedAt : null;
      Promise.resolve(resp.json()).then((body) => {
        const ent = pick(body, instId);
        capture.listReads.push({ t: hrNow(), requestedAt, state: ent ? ent.state : '(absent)', status: resp.status(), body: ent });
      }, () => capture.listReads.push({ t: hrNow(), requestedAt, state: '(unreadable)', status: resp.status(), body: null }));
    }
  };
  page.on('request', onRequest);
  page.on('response', onResponse);
  return [['request', onRequest], ['response', onResponse]];
}

async function pollItemRoute(c, id, f, capture) {
  const deadline = f.t0 + TIGHT_POLL_DEADLINE_MS;
  while (hrNow() < deadline && !capture.stopResp) {
    const at = hrNow();
    const r = await c.api('GET', `/api/v1/instances/${id}`);
    const done = hrNow();
    f.maxHttpReadMs = Math.max(f.maxHttpReadMs, done - at);
    const state = r.data && r.data.state;
    // The read is filed under the instant its response body was in hand, never
    // under the instant the request left: t1 is an observation of the served state.
    f.itemPolls.push({ t: done, at, state: String(state) });
    if (state === 'stopping') return { t: done, item: r.data };
    if (TERMINAL_STATES.includes(state) || state === 'orphan') return null;
    await sleep(ITEM_POLL_MS);
  }
  return null;
}

// pollPaints samples the shipped DOM continuously until the stop response lands,
// recording every sample rather than only the transitions: "no regression after
// the badge appeared" has to be decided on the samples that stayed put, not on the
// ones that changed. Each sample is timestamped after the read returns, so a
// recorded paint time is never earlier than the paint it observed.
async function pollPaints(c, spec, instId, f, capture) {
  const stopAt = f.t0 + STOP_RESPONSE_DEADLINE_MS;
  while (!capture.stopResp && hrNow() < stopAt && f.paints.length < PAINT_SAMPLES_CAP) {
    let dom = null;
    try {
      dom = await readDom(c.page, spec, instId);
    } catch {
      return false; // the page stopped answering reads: a real measurement gap
    }
    f.paints.push({ t: hrNow(), sig: paintSignature(spec, dom), dom });
    await sleep(DOM_POLL_MS);
  }
  return true;
}

function paintAt(f, t) {
  const hit = f.paints.find((p) => p.t === t);
  return hit ? hit.dom : null;
}

function firstStoppingPaint(spec, f) {
  const hit = f.paints.find((p) => p.t < f.t5 && badgeState(primaryBadgeOf(spec, p.dom)) === 'stopping');
  return hit ? hit.t : 0;
}

// observationSeries merges every state observation of the interval — server
// reads, browser reads and painted DOM — so a presentation leg cannot be
// satisfied by a claim about one surface alone. `reqAt` is the instant the read
// began: a response body served before the stop was issued cannot be held against
// the run just because its body happened to arrive after t1.
function observationSeries(spec, f) {
  const out = [];
  for (const p of f.itemPolls) out.push({ t: p.t, reqAt: p.at, state: String(p.state), src: 'item-read' });
  for (const r of f.listReads) out.push({ t: r.t, reqAt: r.requestedAt, state: String(r.state), src: 'page-read' });
  for (const p of f.paints) out.push({ t: p.t, reqAt: p.t, state: badgeState(primaryBadgeOf(spec, p.dom)), src: 'paint' });
  return out;
}

// liveEnd returns the last instant at which this run can still demand a
// not-yet-terminated presentation, plus the basis it was derived from. It never
// widens past t5, and it falls back to t5 when the platform gave no graceful
// interval (AH-4) — in that case the window legs are already NOT OBSERVED.
function liveEnd(f, signal) {
  if (!signal || signal.event !== 'signal') {
    return { t: f.t5, basis: `stop-response receipt (+${off(f.t5, f.t0)}ms): no signal marker, so no independent exit estimate (AH-4 platform gap)` };
  }
  const exitEst = f.t0 + (Date.parse(signal.timestamp) + signal.exit_delay_ms - f.t0Wall);
  const t = Math.min(f.t5, exitEst - LIVE_BOUNDARY_MARGIN_MS);
  return {
    t,
    exitEst,
    basis: `fixture was signalled ${signal.signal} at wall ${signal.timestamp} and promised a ${signal.exit_delay_ms} ms graceful dwell => expected exit +${off(exitEst, f.t0)}ms; less the ${LIVE_BOUNDARY_MARGIN_MS} ms clock-mapping margin, capped by t5 (+${off(f.t5, f.t0)}ms)`,
  };
}

function rowBinding(spec, dom, inst) {
  if (!dom) return 'row absent';
  if (spec.view === 'adv-instances') return `row_title_id=${dom.inst ? dom.inst.idTitle : '(absent)'} pid_cell=${dom.inst ? dom.inst.pid : '-'}`;
  if (spec.view === 'pipelines') return `chip_title=${JSON.stringify(dom.chip ? dom.chip.title : '')} aggregate_visible=${!!(dom.pipe && dom.pipe.visible)}`;
  return `row_name=${JSON.stringify(spec.name)} sub=${JSON.stringify(dom.model ? dom.model.sub : '')} run_pid=${inst ? inst.pid : '-'}`;
}

// paintRuns compresses the paint samples into the runs actually observed, so a
// detail line can show every sample without showing hundreds of identical ones.
function paintRuns(spec, f) {
  const runs = [];
  for (const p of f.paints) {
    const st = badgeState(primaryBadgeOf(spec, p.dom));
    const last = runs[runs.length - 1];
    if (last && last.state === st) { last.count += 1; last.to = p.t; } else runs.push({ state: st, count: 1, from: p.t, to: p.t });
  }
  return runs;
}

const shortSeries = (spec, f) =>
  `${f.paints.length} samples: ${paintRuns(spec, f).map((r) => `${r.state} x${r.count}(+${off(r.from, f.t0)}..+${off(r.to, f.t0)}ms)`).join(' -> ') || '(none)'}`;

// reportModelStrip judges the ADR 022 facts on the same painted sample the badge
// legs bind to (t4), so the strip is never judged in a different state than the
// badge it must not duplicate. A run whose shipped tick missed the interval has no
// strip sample: that is NOT OBSERVED (§16), never a product FAIL.
function reportModelStrip(s, spec, f, win, missReason) {
  const K = spec.key;
  const row = f.t4 ? (paintAt(f, f.t4) || {}).model : null;
  const legs = {};
  if (win && row && row.marker) {
    const mk = row.marker;
    legs['model action strip carries no second state word'] = {
      ok: !hasStateWord(row.stripText),
      detail: `strip_text=${JSON.stringify(row.stripText)} badge_text=${JSON.stringify(row.text)} badge_cls=${JSON.stringify(row.cls)} — ADR 022 D-1/D-6: the badge is the only state word`,
    };
    legs['transition indicator is decorative and text-free'] = {
      ok: mk.present === true && mk.visible === true && mk.text === '' && mk.ariaHidden === 'true'
        && mk.tabindex === null && mk.named === 0,
      detail: `present=${mk.present} visible=${mk.visible} text=${JSON.stringify(mk.text)} aria-hidden=${JSON.stringify(mk.ariaHidden)} tabindex=${JSON.stringify(mk.tabindex)} naming_attrs=${mk.named} — ADR 022 D-2/D-3: no text, no name, not focusable, not a progressbar`,
    };
    legs['no start/stop/restart control during the transition'] = {
      ok: row.startBtn === false && row.stopBtn === false && row.restartBtn === false,
      detail: `start=${row.startBtn} stop=${row.stopBtn} restart=${row.restartBtn} — ADR 022 D-5: the blocking semantics of the shipped branch are unchanged`,
    };
  }
  for (const name of MODEL_STRIP_LEGS) {
    if (!win) { s.notObserved(`${K}: ${name}`, missReason); continue; }
    const l = legs[name];
    if (!l) {
      s.notObserved(`${K}: ${name}`, f.t4
        ? 'the model row was not in the sampled DOM, so the strip was never observed'
        : `no in-window stopping paint to judge the strip on (dwell=${DWELL_MS}ms poll=${UI_REFRESH_PERIOD_MS}ms)`);
      continue;
    }
    s.log(`${K}: ${name}`, l.ok, l.detail);
  }
}

function reportWindow(c, spec, inst, capture, f, state) {
  const s = c.suite;
  const K = spec.key;
  const win = f.item ? { t1: f.t1, item: f.item } : null;
  const signal = state.signal;
  const missReason = signal && signal.event === 'signal'
    ? `the fixture held a real ${signal.exit_delay_ms} ms graceful interval (signal.json) but no item-route read inside ${TIGHT_POLL_DEADLINE_MS} ms returned stopping; poll period ${UI_REFRESH_PERIOD_MS} ms`
    : `the fixture wrote no signal.json, so this run produced no graceful stopping interval to observe (platform signal-delivery gap, AH-4); poll period ${UI_REFRESH_PERIOD_MS} ms`;

  const pageReads = f.listReads.filter((r) => r.t > f.t0 && r.t < f.t5);
  const stoppingReads = pageReads.filter((r) => r.state === 'stopping');
  // A browser read inside the window that answered non-2xx or whose body could not
  // be parsed is a defect of the product or the transport, never a measurement gap,
  // so it is judged FAIL even when a `stopping` read also landed.
  const brokenReads = pageReads.filter((r) => !(r.status >= 200 && r.status < 300) || r.state === '(unreadable)');
  const readSeriesNote = `[${pageReads.map((r) => `${r.state}/${r.status}`).join(',')}]`;
  // t3 is the read the painted row consumed: the last captured browser read that
  // carried `stopping` at or before t4. Never synthesized.
  const consumed = f.t4 ? pageReads.filter((r) => r.state === 'stopping' && r.t <= f.t4).pop() || null : null;
  const pair = consumed ? { t3: consumed.t, read: consumed } : null;

  const legs = {};
  // `ok === null` is the contract §16 NOT OBSERVED disposition: the property could
  // not be judged by this run. It is deliberately distinct from `false`, which is a
  // judged FAIL, and the reporting loop below routes the two differently.
  const leg = (name, ok, detail) => { legs[name] = { ok, detail }; };

  if (win) {
    leg('item route reports stopping',
      win.item.state === 'stopping' && win.item.pid === inst.pid && win.item.id === inst.id,
      `t1=+${off(f.t1, f.t0)}ms state=${win.item.state} pid=${win.item.pid} (launched pid=${inst.pid}) id=${win.item.id} — the server produced this state; the client only read it (F-7.1/F-7.2 [A])`);

    const exeOk = !!f.ident && f.ident.alive === true && pathsEqual(f.ident.executable_path, win.item.executable);
    const startDelta = !!f.ident && f.ident.has_start_time && win.item.started_at
      ? Date.parse(f.ident.start_time) - Date.parse(win.item.started_at) : null;
    leg('process identity alive mid-window', exeOk && (startDelta === null || Math.abs(startDelta) <= IDENTITY_TOLERANCE_MS),
      f.ident
        ? `t2=+${off(f.t2, f.t0)}ms probe=${f.identMs.toFixed(1)}ms alive=${f.ident.alive} executable=${JSON.stringify(f.ident.executable_path || '')} vs frozen ${JSON.stringify(win.item.executable)} start=${f.ident.start_time || '(none)'} vs started_at=${win.item.started_at}${startDelta === null ? ' (platform supplied no start time)' : ` delta=${startDelta}ms`}${f.ident.identity_error ? ' identity_error=' + f.ident.identity_error : ''}${f.ident.probe_error ? ' probe_error=' + f.ident.probe_error : ''} — GetProcessIdentity anchors, not a bare PID-exists check (F-7.2)`
        : 'probe returned no result');

    const ent = pick(f.listInWindow, inst.id);
    const agrees = !!ent && ent.state === 'stopping' && ent.pid === win.item.pid && ent.started_at === win.item.started_at;
    const othersMidRaw = (f.listInWindow || []).filter((i) => i.id !== inst.id).map((i) => `${i.id}:${i.state}`);
    const othersUnchanged = othersMidRaw.slice().sort().join(',') === state.othersBefore;
    leg('list route agrees with the item route', agrees && othersUnchanged,
      `list_state=${ent ? ent.state : '(absent)'} list_pid=${ent ? ent.pid : '-'} pid_and_started_at_match=${agrees} other_entries_unchanged=${othersUnchanged} others_before=[${(state.othersBeforeRaw || []).join(',')}] others_mid=[${othersMidRaw.join(',')}] (compared as a set: Supervisor.List iterates a map, so the route slices entries in unspecified order) — F-7.3`);

    leg('durable record still running mid-window',
      !!f.durableInWindow && f.durableInWindow.state === 'running' && f.durableInWindow.pid === inst.pid,
      f.durableInWindow
        ? `durable_state=${f.durableInWindow.state} durable_pid=${f.durableInWindow.pid} api_state=${win.item.state} — persistence ordering is unchanged (the terminal record is written after Manager.Stop returns); the live view is not persisted`
        : 'goal_repo.json unreadable');

    // F-7.4 is the browser's own receipt. The page reads on the shipped 3000 ms
    // timer, so a window can end with no `stopping` read inside it purely because of
    // where the tick landed; ADR 021 AC-3 forbids recording that as a product
    // failure, so an absent receipt is NOT OBSERVED while a receipt that exists and
    // is wrong (error status, unparseable body) is a FAIL.
    leg('browser response carried stopping',
      brokenReads.length ? false : stoppingReads.length ? true : null,
      brokenReads.length
        ? `page_reads_in_window=${readSeriesNote} — ${brokenReads.length} read(s) answered non-2xx or returned an unparseable body (first at +${off(brokenReads[0].t, f.t0)}ms)`
        : stoppingReads.length
          ? `t3=+${off(stoppingReads[0].t, f.t0)}ms body_state=${stoppingReads[0].state} status=${stoppingReads[0].status} page_reads_in_window=[${pageReads.map((r) => r.state).join(',')}] — F-7.4 receipt from the browser network record${pair ? `; the paint consumed the last stopping read at +${off(pair.t3, f.t0)}ms` : ''}`
          : `no GET /api/v1/instances response carrying stopping landed inside the interval (dwell=${DWELL_MS}ms poll=${UI_REFRESH_PERIOD_MS}ms t1=+${off(f.t1, f.t0)}ms t5=+${off(f.t5, f.t0)}ms page_reads_in_window=${readSeriesNote}) — the item route did answer stopping and every read that landed was 2xx, so this is the page's own timer missing the window, not a product failure`);

    const shown = f.t4 ? primaryBadgeOf(spec, paintAt(f, f.t4)) : null;
    const labelBadge = f.t4 ? labelBadgeOf(spec, paintAt(f, f.t4)) : null;
    const label = LABELS[spec.lang];
    // Same rule for the paint: no in-window `stopping` sample because the page never
    // received one is the client timer, so it is NOT OBSERVED. Receiving it and not
    // painting it is a presentation defect and stays a FAIL.
    leg('rendered badge shows stopping',
      shown ? badgeState(shown) === 'stopping' && !!labelBadge && labelBadge.text === label && primaryVisible(spec, paintAt(f, f.t4))
        : stoppingReads.length ? false : null,
      shown
        ? `t4=+${off(f.t4, f.t0)}ms primary=${badgeState(shown)} label_text=${JSON.stringify(labelBadge ? labelBadge.text : '')} want=${JSON.stringify(label)} view=${spec.view} visible=${primaryVisible(spec, paintAt(f, f.t4))} binding=${rowBinding(spec, paintAt(f, f.t4), inst)} — [A] half of F-7.5 (F-7.12 surface for the pipeline row); the Owner half is not run (RV-9)`
        : stoppingReads.length
          ? `the page's own network record carried stopping at +${off(stoppingReads[0].t, f.t0)}ms inside the interval but no DOM sample before t5 (+${off(f.t5, f.t0)}ms) painted it — a presentation gap, not a timer gap; paints=[${shortSeries(spec, f)}]`
          : `no in-window stopping paint and no in-window stopping read to bind one to (dwell=${DWELL_MS}ms poll=${UI_REFRESH_PERIOD_MS}ms page_reads_in_window=${readSeriesNote}); paints=[${shortSeries(spec, f)}]`);

    // The presentation properties, asserted one per rule and never collapsed into
    // a single "no stale presentation" claim, because the shipped 3000 ms timer
    // makes one blanket monotonicity assertion unsatisfiable by design:
    // (a) nothing may be presented as terminated while the process is alive;
    // Reads whose request instant is unknown are stated, never silently judged.
    const allObs = observationSeries(spec, f);
    const live = liveEnd(f, signal);
    const judged = allObs.filter((o) => typeof o.reqAt === 'number');
    const unjudged = allObs.length - judged.length;
    const shownTail = judged.filter((o) => o.t > live.t && o.t < f.t5 && (TERMINAL_STATES.includes(o.state) || o.state === 'orphan'));
    const boundaryNote = `live_end=+${off(live.t, f.t0)}ms (${live.basis})`;
    const tailNote = `${shownTail.length} terminal observation(s) between live_end and t5${shownTail.length ? ` [+${shownTail.map((o) => `${off(o.t, f.t0)}ms:${o.src}=${o.state}`).join(',')}]` : ''}`;
    const gapNote = unjudged ? `; ${unjudged} observation(s) had no request instant and were not judged` : '';
    const seriesNote = `item_reads=[${f.itemPolls.map((p) => p.state).join(',')}] page_reads=[${pageReads.map((r) => r.state).join(',')}] ${shortSeries(spec, f)}`;

    const premature = judged.filter((o) => o.t > f.t0 && o.t < live.t && !LIVE_STATES_IN_WINDOW.includes(o.state));
    leg('no premature terminal presentation while the process was alive', premature.length === 0,
      `${premature.length} observation(s) between t0 and live_end presented a state outside ${LIVE_STATES_IN_WINDOW.join('/')} — the pre-ADR 021 symptom${premature.length ? ': ' + premature.slice(0, 6).map((o) => `+${off(o.t, f.t0)}ms:${o.src}=${o.state}`).join(',') : ''}; ${boundaryNote}; ${tailNote}${gapNote}; ${seriesNote}`);

    // (b) once the server had reported `stopping`, every server read that began
    //     after that and while the process was still in its graceful dwell
    //     reported `stopping` — the collection/item consistency the same published
    //     view has to give both routes;
    const readRegressions = judged.filter((o) => o.src !== 'paint' && o.reqAt >= f.t1 && o.t < live.t && o.state !== 'stopping');
    leg('no server read regressed from stopping inside the window', readRegressions.length === 0,
      `${readRegressions.length} read(s) requested at or after t1 (+${off(f.t1, f.t0)}ms) and answered before live_end reported another state${readRegressions.length ? ': ' + readRegressions.map((o) => `req+${off(o.reqAt, f.t0)}ms recv+${off(o.t, f.t0)}ms:${o.src}=${o.state}`).join(',') : ''}; ${boundaryNote}; ${seriesNote} — reads after live_end are post-termination and belong to the convergence legs (F-7.1/F-7.3)`);

    // (c) once the badge had been painted, every later sample inside the interval
    //     still painted it;
    if (f.t4) {
      const paintRegressions = f.paints.filter((p) => p.t > f.t4 && p.t < live.t && badgeState(primaryBadgeOf(spec, p.dom)) !== 'stopping');
      leg('no paint regressed from stopping after the badge appeared', paintRegressions.length === 0,
        `${paintRegressions.length} sample(s) after t4 (+${off(f.t4, f.t0)}ms) and before live_end painted another state${paintRegressions.length ? ': ' + paintRegressions.map((p) => `+${off(p.t, f.t0)}ms:${badgeState(primaryBadgeOf(spec, p.dom))}`).join(',') : ''}; ${boundaryNote}; ${shortSeries(spec, f)} — F-7.5 [A] half (F-7.11/F-7.12 surfaces)`);
    } else {
      leg('no paint regressed from stopping after the badge appeared', null,
        `no in-window stopping paint, so there is no first badge that a later sample could regress from; ${shortSeries(spec, f)}`);
    }

    // (d) the badge followed the response the page itself received, within one
    //     shipped refresh period. The lag before that read is a property of the
    //     3000 ms timer, not of the published lifecycle view, so it is reported as
    //     a number instead of being asserted away or called a defect.
    if (pair && f.t4) {
      const lag = f.t4 - pair.t3;
      const preRead = f.paints.filter((p) => p.t < pair.t3);
      const preStates = Array.from(new Set(preRead.map((p) => badgeState(primaryBadgeOf(spec, p.dom)))));
      leg('the badge followed the page read inside the interval', lag >= 0 && lag <= UI_REFRESH_PERIOD_MS && f.t4 < f.t5,
        `t3=+${off(pair.t3, f.t0)}ms t4=+${off(f.t4, f.t0)}ms read_to_paint=${lag.toFixed(1)}ms refresh_period=${UI_REFRESH_PERIOD_MS}ms t5=+${off(f.t5, f.t0)}ms; before its own stopping read the page had ${preRead.length} sample(s) still showing ${preStates.join('/') || '(no sample)'} — ADR 021 authorizes no client-side change and AC-3 forbids recording a late poll as a product FAIL`);
    } else {
      leg('the badge followed the page read inside the interval', null,
        `no in-window stopping read paired with an in-window paint, so there is no t3→t4 lag to measure (dwell=${DWELL_MS}ms poll=${UI_REFRESH_PERIOD_MS}ms page_reads_in_window=${readSeriesNote}; ${shortSeries(spec, f)})`);
    }

    leg('reads never stalled behind the stop', f.maxHttpReadMs < READ_STALL_GUARD_MS,
      `slowest server read during the live stop=${f.maxHttpReadMs.toFixed(1)}ms (identity probe timed separately: ${f.identMs.toFixed(1)}ms; guard ${READ_STALL_GUARD_MS}ms; a reader that took the controller lock would sit behind the whole ${DWELL_MS}ms dwell)`);

    // Offsets are relative to t0 on one monotonic clock; t0_wall is the same
    // instant as a wall-clock number so the fixture markers, the durable record and
    // the server log can be lined up with it.
    const wallAnchor = `t0_wall=${new Date(f.t0Wall).toISOString()}`;
    const reqT = capture.stopReqs.length ? capture.stopReqs[0].t : f.t0;
    leg('correlation t0 < t1 <= t2 < t5', f.t0 < f.t1 && f.t1 <= f.t2 && f.t2 < f.t5,
      `${wallAnchor} t0=+0 t1=+${off(f.t1, f.t0)} t2=+${off(f.t2, f.t0)} t5=+${off(f.t5, f.t0)}ms (t5=stop-response receipt; stop request t=+${off(reqT, f.t0)}ms; live_end=+${off(live.t, f.t0)}ms)`);
    leg('correlation t0 < t3 <= t4 < t5', pair ? f.t0 < pair.t3 && pair.t3 <= f.t4 && f.t4 < f.t5 : null,
      pair ? `${wallAnchor} t0=+0 t3=+${off(pair.t3, f.t0)} t4=+${off(f.t4, f.t0)} t5=+${off(f.t5, f.t0)}ms` : `${wallAnchor} t0=+0 t3=(none) t4=${f.t4 ? `+${off(f.t4, f.t0)}` : '(none)'} t5=+${off(f.t5, f.t0)}ms — no in-window browser read and paint to order, so the client half of the correlation line is not observable in this run; the server half above is judged independently`);
  }

  // A truncated or aborted sample series is stated before the legs it affects, so
  // no PASS below can be read as having covered a window that was not sampled to
  // its end.
  if (!state.paintLoopOk) s.notObserved(`${K}: paint poll completed`, 'the page stopped answering DOM reads before the stop response landed');
  if (f.paints.length >= PAINT_SAMPLES_CAP) {
    s.notObserved(`${K}: paint poll covered the whole interval`,
      `the sample cap ${PAINT_SAMPLES_CAP} was reached at +${off(f.paints[f.paints.length - 1].t, f.t0)}ms, so the paint series and the regression legs saw only that prefix`);
  }

  for (const name of WINDOW_LEGS) {
    if (!win) { s.notObserved(`${K}: ${name}`, missReason); continue; }
    const l = legs[name];
    if (!l) { s.notObserved(`${K}: ${name}`, 'leg was not evaluated by this run'); continue; }
    if (l.ok === null) { s.notObserved(`${K}: ${name}`, l.detail); continue; }
    s.log(`${K}: ${name}`, l.ok, l.detail);
  }

  // The instances and pipeline rows have no model card, so the defect this ADR
  // removes cannot exist on them and the legs are not applicable there.
  if (spec.view === 'models') reportModelStrip(s, spec, f, win, missReason);

  if (spec.kind !== 'pipeline') return;
  if (!win) { s.notObserved(`${K}: ${PIPE_WINDOW_LEG}`, missReason); return; }
  // RV-7 negative control: the pipeline detail endpoint promotes a per-entry state
  // only through IsRunningOrStarting() (internal/webui/handlers/pipelines.go:176
  // defaults the entry to "stopped", :186 is the only promotion), so while the item
  // route says stopping every entry must still read exactly "stopped". An empty
  // models array proves nothing and is not a PASS.
  const models = f.pipeDetail && f.pipeDetail.data && Array.isArray(f.pipeDetail.data.models) ? f.pipeDetail.data.models : null;
  s.log(`${K}: ${PIPE_WINDOW_LEG}`, !!models && models.length >= 1 && models.every((m) => m.state === 'stopped'),
    models
      ? `endpoint_entry_count=${models.length} endpoint_entry_states=[${models.map((m) => `${m.model_id}:${m.state}`).join(',')}] while t1 state=${win.item.state} — GET /api/v1/pipelines/{id} is NOT an admissible stopping source and did not change`
      : `status=${f.pipeDetail ? f.pipeDetail.status : 'n/a'} body_keys=${f.pipeDetail && f.pipeDetail.data ? Object.keys(f.pipeDetail.data).join(',') : 'n/a'}`);
}

async function reportTerminal(c, spec, inst, capture, f, state) {
  const s = c.suite;
  const K = spec.key;
  const { signal, term, termAt, identAfter } = state;
  const graceful = !!signal && signal.event === 'signal';

  // Absence of the marker means this platform gave the run no graceful interval at
  // all (AH-4): the attempt was made and nothing was measurable, which §16 calls
  // NOT OBSERVED. A marker that exists and disagrees with the run is a real FAIL.
  if (!graceful) {
    s.notObserved(`${K}: graceful stop signal reached the fixture`,
      'signal.json absent — this run had no graceful stopping interval (AH-4 platform gap): a measurement limit, not a product FAIL');
  } else {
    s.log(`${K}: graceful stop signal reached the fixture`, signal.pid === inst.pid && signal.exit_delay_ms === DWELL_MS,
      `signal=${signal.signal} marker_pid=${signal.pid} run_pid=${inst.pid} exit_delay_ms=${signal.exit_delay_ms} marker_ts=${signal.timestamp}`);
  }

  s.log(`${K}: stop request and response are a real browser exchange`,
    capture.stopReqs.length === 1 && !!capture.stopResp && capture.stopResp.status >= 200 && capture.stopResp.status < 300,
    `requests=${capture.stopReqs.length} req=+${capture.stopReqs.length ? off(capture.stopReqs[0].t, f.t0) : 'none'}ms resp_status=${capture.stopResp ? capture.stopResp.status : 'none'} resp=+${capture.stopResp ? off(capture.stopResp.t, f.t0) : 'none'}ms`);

  s.log(`${K}: process identity no longer resolves to this run`,
    !identAfter.alive || !pathsEqual(identAfter.executable_path, inst.executable),
    `t=+${off(termAt, f.t0)}ms alive=${identAfter.alive} executable=${JSON.stringify(identAfter.executable_path || '')} start=${identAfter.start_time || '(none)'}${identAfter.alive_error ? ' alive_error=' + identAfter.alive_error : ''}${identAfter.probe_error ? ' probe_error=' + identAfter.probe_error : ''} — F-7.6`);

  s.log(`${K}: terminal state converged in API and store`,
    !!term && term.state === 'exited' && !!term.stopped_at && (!graceful || term.exit_code === 0),
    term ? `state=${term.state} exit_class=${JSON.stringify(term.exit_class || '')} exit_code=${term.exit_code == null ? '(nil)' : term.exit_code} stopped_at=${term.stopped_at}`
      : 'no terminal state became readable');

  const durableAfter = readDurableInstance(c.ws.dataDir, inst.id);
  s.log(`${K}: durable record converged`, !!durableAfter && durableAfter.state === 'exited',
    durableAfter ? `durable_state=${durableAfter.state} exit_code=${durableAfter.exit_code == null ? '(nil)' : durableAfter.exit_code} — F-7.7 [A] halves (API + store); the UI half is [A] only`
      : 'goal_repo.json unreadable');

  s.log(`${K}: no orphan or stale divergence after the stop`, !!term && term.state === 'exited',
    term ? `terminal_state=${term.state} (orphan=${term.state === 'orphan'} stale=${term.state === 'stale'}) — F-7.8 for this stop path; the recovery classes stay on the Go suite`
      : 'no terminal state readable');

  await c.page.evaluate(() => window.reloadAllData());
  await c.page.waitForTimeout(UI_REFRESH_PERIOD_MS + 500);
  const domAfter = await readDom(c.page, spec, inst.id);
  const afterState = badgeState(primaryBadgeOf(spec, domAfter));
  const listAfter = await c.api('GET', '/api/v1/instances');
  const entAfter = pick(listAfter.data, inst.id);
  const active = afterState === 'stopping' || afterState === 'running' || afterState === 'starting';
  s.log(`${K}: UI converged with the terminal state`, !active && (!domAfter.model || !domAfter.model.transitional),
    `primary_state=${afterState} api_state=${entAfter ? entAfter.state : '(absent)'} transitional=${!!(domAfter.model && domAfter.model.transitional)} start_button_back=${!!(domAfter.model && domAfter.model.startBtn)} — [A] only; F-7.5/F-7.7 keep their Owner half open`);
  // ADR 022 AC-4 on the model card: the indicator is a transition marker, so it must
  // be gone once the row is terminal and the Start action is back.
  if (spec.view === 'models') {
    const mkAfter = domAfter.model ? domAfter.model.marker : null;
    s.log(`${K}: transition indicator is gone after convergence`,
      !!mkAfter && mkAfter.present === false && !!domAfter.model.startBtn,
      `marker_present=${mkAfter ? mkAfter.present : '(row absent)'} strip_text=${JSON.stringify(domAfter.model ? domAfter.model.stripText : '(row absent)')} start_button_back=${!!(domAfter.model && domAfter.model.startBtn)} primary_state=${afterState} — ADR 022 AC-4`);
  }
  await H.screenshot(c.page, c.ws, `${spec.tag}_after_convergence`);
}

async function awaitRunning(c, spec) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    const list = await c.api('GET', '/api/v1/instances');
    const rows = (list.data || []).filter((i) => spec.kind === 'pipeline'
      ? i.pipeline_id === spec.pipelineId
      : i.model_id === spec.modelId);
    const running = rows.find((i) => i.state === 'running');
    if (running) return running;
    await sleep(100);
  }
  return null;
}

async function awaitTerminal(c, id) {
  const deadline = Date.now() + TERMINAL_DEADLINE_MS;
  while (Date.now() < deadline) {
    const r = await c.api('GET', `/api/v1/instances/${id}`);
    if (r.data && (TERMINAL_STATES.includes(r.data.state) || r.data.state === 'orphan')) return r.data;
    await sleep(100);
  }
  return null;
}

main().catch((err) => {
  console.error('HARNESS ERROR:', err);
  process.exit(2);
});

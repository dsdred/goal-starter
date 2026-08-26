'use strict';
const path = require('path');
const H = require('./harness.cjs');

// Long-stream log stress regression (real Chromium).
//
// Regression target: the live-log consumer in the WebUI used to wedge the tab
// main thread (unresponsive / black tab) once #log-view accumulated 2001 lines
// — the trim loop iterated a STATIC querySelectorAll NodeList and never
// terminated. This suite streams a fast, deterministic log flood
// (fake-runtime `flood` mode, ~100 lines/s) well past the 2000-line client
// display window and asserts structural invariants instead of absolute RAM:
//   - the tab stays responsive (main thread never wedges)
//   - rendered log DOM stays bounded to the client display window (<= 2000)
//   - logs keep advancing, autoscroll tracks the tail
//   - navigation away/back, pause/resume, search, clear keep working
//   - no duplicate SSE consumers, no console errors, server stays responsive
const PORT = 19485;
const BASE = `http://127.0.0.1:${PORT}`;
const LOG_WINDOW = 2000;

async function main() {
  console.log('=== Log stress (long stream, bounded client state) ===\n');

  const ws = H.makeWorkspace('stress');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  H.writeConfig(ws, {
    webPort: PORT,
    authEnabled: false,
    runtimes: [
      { id: 'rt-stress', name: 'Stress Runtime', executable: fakeRt, workingDirectory: fakeRtDir },
    ],
  });

  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started.');

  const api = (method, urlPath, body) => H.api(BASE, method, urlPath, body);
  const sleep = ms => new Promise(r => setTimeout(r, ms));

  const suite = H.newSuite('LOG STRESS (bounded client state under long stream)');

  const modelRes = await api('POST', '/api/v1/models', { name: 'Stress Model', runtime_id: 'rt-stress', args: ['flood', '10'] });
  suite.log('Seed: flood model created', modelRes.status === 201, `status=${modelRes.status}`);
  const startRes = await api('POST', `/api/v1/models/${modelRes.data.id}/start`);
  suite.log('Seed: flood instance started', startRes.status === 200, `status=${startRes.status} state=${startRes.data && startRes.data.state}`);

  const browser = await H.launchBrowser();
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  suite.watchPage(page);
  page.setDefaultTimeout(10000);

  let sseConnections = 0;
  page.on('request', r => { if (r.url().includes('/logs/stream')) sseConnections++; });

  // Deadline-guarded evaluate: the regression under test is a wedged main
  // thread (infinite loop), where a plain page.evaluate may never settle. A
  // client-side deadline guarantees the suite FAILS fast instead of hanging.
  const PROBE_MS = 8000;
  const withDeadline = (promise, ms, label) => {
    let to;
    const t = new Promise((_, rej) => { to = setTimeout(() => rej(new Error(`${label}: tab main thread unresponsive (wedge)`)), ms); });
    return Promise.race([promise, t]).finally(() => clearTimeout(to));
  };

  // Bounded probe of page state; throws if the main thread is wedged.
  const probe = (expr) => withDeadline(page.evaluate(expr), PROBE_MS, 'probe');

  const lineInfo = () => withDeadline(page.evaluate(() => {
    const lines = [...document.querySelectorAll('#log-view .log-line')];
    const last = lines[lines.length - 1];
    let idx = -1;
    if (last) {
      const m = last.textContent.match(/flood-line-(\d+)/);
      if (m) idx = parseInt(m[1], 10);
    }
    const view = document.getElementById('log-view');
    return {
      count: lines.length,
      domNodes: document.getElementsByTagName('*').length,
      lastIdx: idx,
      autoscrollOn: document.getElementById('log-autoscroll').checked,
      nearBottom: view ? (view.scrollHeight - view.clientHeight - view.scrollTop) < 50 : false,
    };
  }), PROBE_MS, 'lineInfo');

  // Server-side delivered total (independent of the tab).
  const serverTotal = async () => {
    const r = await api('GET', '/api/v1/logs?page=1&page_size=1');
    return r.status === 200 && r.data ? r.data.total : -1;
  };

  let ok = false;
  try {
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.evaluate(() => window.navigate('logs'));
    await page.waitForTimeout(2000);

    const early = await lineInfo();
    suite.log('Logs view connected and receiving', early.count > 0 && early.lastIdx >= 0, `count=${early.count} lastIdx=${early.lastIdx}`);

    // Stream well past the client display window. flood 10ms ≈ 100 lines/s;
    // wait until the server-side total is at least 2600 (30% past 2000).
    let total = -1;
    const deadline = Date.now() + 120000;
    while (Date.now() < deadline) {
      total = await serverTotal();
      if (total >= 2600) break;
      await sleep(1000);
    }
    suite.log('Streamed past client window (server total >= 2600)', total >= 2600, `total=${total}`);
    await sleep(3000); // let the tab catch up

    // Core regression: the tab must still be responsive after >2000 lines.
    // Pre-fix this wedges the main thread at line 2001 and this times out.
    const alive = await probe(() => 'alive');
    suite.log('Tab responsive after >2000 streamed lines', alive === 'alive');

    const s1 = await lineInfo();
    suite.log('Rendered log DOM bounded (<= 2000 lines)', s1.count > 0 && s1.count <= LOG_WINDOW, `count=${s1.count}`);
    suite.log('Total DOM node count bounded', s1.domNodes <= 12000, `domNodes=${s1.domNodes}`);
    suite.log('Logs still advancing', s1.lastIdx > early.lastIdx, `lastIdx ${early.lastIdx} -> ${s1.lastIdx}`);
    suite.log('Autoscroll tracks the tail', s1.autoscrollOn && s1.nearBottom, `autoscroll=${s1.autoscrollOn} nearBottom=${s1.nearBottom}`);

    // Server stays responsive while the tab is under stress.
    const inst = await api('GET', '/api/v1/instances');
    suite.log('Server API responsive under stress', inst.status === 200);

    // Navigation away and back must keep working without a reload.
    await page.evaluate(() => window.navigate('models'));
    await sleep(1500);
    const modelsAlive = await probe(() => 'alive');
    const modelRows = await page.evaluate(() => document.querySelectorAll('#model-list .model-row').length);
    suite.log('Navigation to Models works (no reload)', modelsAlive === 'alive' && modelRows >= 1, `rows=${modelRows}`);
    await page.evaluate(() => window.navigate('logs'));
    await sleep(1500);
    const s2 = await lineInfo();
    suite.log('Back on Logs: still bounded and advancing', s2.count <= LOG_WINDOW && s2.lastIdx > s1.lastIdx, `count=${s2.count} lastIdx ${s1.lastIdx} -> ${s2.lastIdx}`);

    // Single SSE consumer (no duplicates after navigation).
    suite.log('No duplicate SSE consumers (1 connection, at most 1 reconnect)', sseConnections >= 1 && sseConnections <= 2, `connections=${sseConnections}`);

    // Pause / resume keep working.
    await page.evaluate(() => window.toggleLogPause());
    await sleep(2500);
    const paused = await lineInfo();
    await page.evaluate(() => window.toggleLogPause());
    await sleep(2500);
    const resumed = await lineInfo();
    suite.log('Pause holds the view, resume advances it', paused.lastIdx >= 0 && resumed.lastIdx > paused.lastIdx, `paused=${paused.lastIdx} resumed=${resumed.lastIdx}`);

    // Search filters the existing window and filters new arrivals.
    const searchIdx = resumed.lastIdx;
    await page.fill('#log-search', `flood-line-${searchIdx}`);
    await sleep(1500);
    const visible = await page.evaluate(() =>
      [...document.querySelectorAll('#log-view .log-line')].filter(el => el.style.display !== 'none').length);
    suite.log('Search shows only matching lines', visible === 1, `visible=${visible} (searched flood-line-${searchIdx})`);
    await page.fill('#log-search', '');
    await sleep(1000);
    const afterSearch = await lineInfo();
    suite.log('Clearing search restores the live stream', afterSearch.lastIdx > searchIdx, `lastIdx ${searchIdx} -> ${afterSearch.lastIdx}`);

    // Clear empties the view synchronously; the stream refills (bounded).
    const afterClear = await page.evaluate(() => {
      window.clearLogView();
      return document.querySelectorAll('#log-view .log-line').length;
    });
    suite.log('Clear empties the log view', afterClear === 0, `count=${afterClear}`);
    await sleep(2000);
    const refilled = await lineInfo();
    suite.log('Stream refills after clear (still bounded)', refilled.count > 0 && refilled.count <= LOG_WINDOW, `count=${refilled.count}`);

    // User can continue working in the same tab without any reload.
    await page.evaluate(() => window.navigate('history'));
    await sleep(1000);
    const histAlive = await probe(() => 'alive');
    suite.log('User can continue in the same tab (History view, no reload)', histAlive === 'alive');

    suite.log('no console errors', suite.consoleErrors.length === 0, suite.consoleErrors.slice(0, 3).join(' | '));
    suite.log('no server 5xx errors', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));

    await H.screenshot(page, ws, 'stress-final-logs');
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

main().catch(err => {
  console.error('HARNESS ERROR:', err);
  process.exit(2);
});

'use strict';
const path = require('path');
const fs = require('fs');
const { spawn } = require('child_process');
const H = require('./harness.cjs');

const PORT = 19487;
const BASE = `http://127.0.0.1:${PORT}`;

const sleep = ms => new Promise(r => setTimeout(r, ms));

async function poll(pred, timeoutMs, intervalMs = 300) {
  const start = Date.now();
  for (;;) {
    const v = await pred();
    if (v) return v;
    if (Date.now() - start > timeoutMs) return null;
    await sleep(intervalMs);
  }
}

// A missing executable whose path carries HTML-active characters: the
// start-failure reason must render the stored last_error verbatim (escaped),
// never as injected markup.
const BAD_EXE = H.IS_WIN
  ? 'C:\\goal-missing <er> "rt"\\definitely-missing.exe'
  : '/goal-missing <er> "rt"/definitely-missing';

async function instanceFor(base, modelId) {
  const res = await H.api(base, 'GET', '/api/v1/instances');
  return (res.data || []).find(i => i.model_id === modelId) || null;
}

async function waitInstance(base, modelId, wantState, timeoutMs = 30000) {
  return poll(async () => {
    const inst = await instanceFor(base, modelId);
    return inst && inst.state === wantState ? inst : null;
  }, timeoutMs);
}

async function createModel(base, name, runtimeId, args) {
  const res = await H.api(base, 'POST', '/api/v1/models', { name, runtime_id: runtimeId, args: args || [] });
  if (res.status !== 201) throw new Error(`model create ${name}: ${res.status} ${JSON.stringify(res.data)}`);
  return res.data;
}

async function startModel(base, modelId) {
  // A start failure (missing executable) returns 500 by existing API contract;
  // the failed instance is persisted to the repository (visible in /history)
  // but removed from the in-memory /instances map.
  const res = await H.api(base, 'POST', `/api/v1/models/${modelId}/start`);
  return res;
}

async function waitHistoryRow(base, modelId, timeoutMs = 15000) {
  return poll(async () => {
    const res = await H.api(base, 'GET', '/api/v1/history');
    return (res.data || []).find(i => i.model_id === modelId) || null;
  }, timeoutMs);
}

// Rows are located by the model name in the first cell of #history-body.
async function historyRow(page, modelName) {
  return page.evaluate((name) => {
    const tr = [...document.querySelectorAll('#history-body tr')]
      .find(r => (r.querySelector('td') || {}).textContent === name);
    if (!tr) return null;
    const tds = [...tr.querySelectorAll('td')];
    const reason = tds[6];
    return {
      reason: reason ? reason.textContent.trim() : null,
      reasonTitle: reason ? reason.getAttribute('title') : null,
    };
  }, modelName);
}

const EXPECT = {
  ru: {
    header: 'Причина',
    normal: 'Завершен',
    crashed: 'Аварийно завершен',
    stopped: 'Остановлен',
    force: 'Принудительно остановлен',
    startFailedPrefix: 'Не удалось запустить: ',
    notFound: 'Процесс не найден',
    dismissed: 'Отклонено',
    codeTooltip: 'Код выхода: ',
  },
  en: {
    header: 'Reason',
    normal: 'Completed',
    crashed: 'Crashed',
    stopped: 'Stopped',
    force: 'Force stopped',
    startFailedPrefix: 'Startup failed: ',
    notFound: 'Process not found',
    dismissed: 'Dismissed',
    codeTooltip: 'Exit code: ',
  },
};

async function checkLang(page, suite, lang, data) {
  const E = EXPECT[lang];
  await page.evaluate(() => window.navigate('history'));
  await page.waitForTimeout(1500);

  const header = await page.evaluate(() => {
    const ths = [...document.querySelectorAll('#view-history thead th')];
    return ths.map(t => t.textContent.trim());
  });
  suite.log(`[${lang}] history column header is "${E.header}"`, header.includes(E.header), `cols=${JSON.stringify(header)}`);

  const normal = await historyRow(page, 'ER Normal');
  suite.log(`[${lang}] normal completion → "${E.normal}"`, normal && normal.reason === E.normal, JSON.stringify(normal));

  const crash = await historyRow(page, 'ER Crash');
  suite.log(`[${lang}] non-zero exit → "${E.crashed}"`, crash && crash.reason === E.crashed, JSON.stringify(crash));
  suite.log(`[${lang}] crash: raw code 42 is tooltip-only`,
    crash && crash.reason === E.crashed && crash.reasonTitle === E.codeTooltip + '42', JSON.stringify(crash));

  const stop = await historyRow(page, 'ER Stop');
  const stopCode = data.stopCode;
  suite.log(`[${lang}] explicit Stop → "${E.stopped}" (not a crash)`,
    stop && stop.reason === E.stopped, JSON.stringify(stop));
  if (stopCode != null) {
    suite.log(`[${lang}] stop: raw code ${stopCode} in tooltip only`,
      stop && stop.reasonTitle === E.codeTooltip + stopCode, JSON.stringify(stop));
  } else {
    suite.log(`[${lang}] stop: no exit code persisted (tooltip empty)`,
      stop && stop.reasonTitle === null, JSON.stringify(stop));
  }

  const fail = await historyRow(page, 'ER Fail');
  const lastErr = data.failLastError;
  suite.log(`[${lang}] start failure shows "${E.startFailedPrefix}…"`,
    fail && fail.reason && fail.reason.startsWith(E.startFailedPrefix), JSON.stringify(fail));
  // lastErr contains the literal substring "<er>" from the executable path:
  // verbatim inclusion in textContent proves it rendered as escaped text,
  // not as injected markup (injection would strip the tags from textContent).
  suite.log(`[${lang}] start failure: last_error rendered verbatim (escaped)`,
    fail && lastErr && lastErr.includes('<er>') && fail.reason.includes(lastErr),
    `last_error=${JSON.stringify(lastErr)}`);

  const killed = await historyRow(page, 'ER Orphan');
  suite.log(`[${lang}] orphan killed → "${E.force}"`, killed && killed.reason === E.force, JSON.stringify(killed));

  const dismissed = await historyRow(page, 'ER Orphan2');
  suite.log(`[${lang}] orphan dismissed → "${E.dismissed}"`, dismissed && dismissed.reason === E.dismissed, JSON.stringify(dismissed));

  const dead = await historyRow(page, 'ER Dead');
  suite.log(`[${lang}] stale pid-not-found → "${E.notFound}"`, dead && dead.reason === E.notFound, JSON.stringify(dead));
}

async function main() {
  console.log('=== Exit reason (human-readable, real Chromium) ===\n');

  const ws = H.makeWorkspace('exit-reason');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  // Helper H2: used to obtain a guaranteed-dead PID for the pid-not-found case.
  const h2 = spawn(fakeRt, ['stdout'], { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
  await sleep(1500);
  const deadPid = h2.pid;
  H.killTree(deadPid);
  const dead = await poll(async () => !H.isProcessAlive(deadPid), 5000, 200);
  if (!dead) { console.log(`FATAL: pid ${deadPid} still alive`); process.exit(1); }

  // Helpers H1/H3: independent fake-runtime processes → orphans after recovery.
  const h1 = spawn(fakeRt, ['infinite'], { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
  const h3 = spawn(fakeRt, ['infinite'], { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
  await sleep(800);
  if (!H.isProcessAlive(h1.pid) || !H.isProcessAlive(h3.pid)) {
    console.log('FATAL: orphan helpers died immediately');
    H.killTree(h1.pid); H.killTree(h3.pid);
    process.exit(1);
  }
  console.log(`Helpers: dead-pid=${deadPid} orphan1=${h1.pid} orphan2=${h3.pid}`);

  // Pre-populate the repository: the orphan/dead instances must be in a
  // transitional state BEFORE the first server start so recovery classifies
  // them (orphan for alive+identity-confirmed, stale for the dead PID).
  const now = new Date().toISOString();
  const rt = {
    id: 'rt-fake', name: 'Fake Runtime', executable: fakeRt,
    working_directory: fakeRtDir, environment: {}, created_at: now, updated_at: now,
  };
  const mkModel = (id, name) => ({
    id, name, runtime_id: 'rt-fake', args: ['infinite'], environment: {},
    active: false, autostart_delay: 0, created_at: now, updated_at: now,
  });
  const mkInst = (id, modelId, pid) => ({
    id, model_id: modelId, runtime_id: 'rt-fake', executable: fakeRt,
    args: ['infinite'], working_directory: fakeRtDir, environment: {},
    state: 'running', pid, exit_code: 0, exit_class: '', last_error: '',
    started_at: now, created_at: now, updated_at: now,
  });
  const repo = {
    schema_version: 8,
    runtimes: [rt],
    models: [mkModel('model-orph', 'ER Orphan'), mkModel('model-orph2', 'ER Orphan2'), mkModel('model-dead', 'ER Dead')],
    instances: [mkInst('inst-orph', 'model-orph', h1.pid), mkInst('inst-orph2', 'model-orph2', h3.pid), mkInst('inst-dead', 'model-dead', deadPid)],
    pipelines: [],
  };
  fs.writeFileSync(path.join(ws.dataDir, 'goal_repo.json'), JSON.stringify(repo, null, 2));

  H.writeConfig(ws, { version: 2, webPort: PORT, authEnabled: false });
  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    H.killTree(h1.pid); H.killTree(h3.pid);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started (auth OFF, recovery applied).\n');

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('EXIT REASON (human-readable, RU/EN, responsive)');
  suite.watchPage(page);

  let ok = false;
  const data = { stopCode: null, failLastError: null };
  try {
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);

    // ═══ Recovery preconditions ═══
    const instOrph = await H.api(BASE, 'GET', '/api/v1/instances/inst-orph');
    suite.log('recovery: inst-orph is orphan', instOrph.data && instOrph.data.state === 'orphan',
      instOrph.data ? instOrph.data.state : `status=${instOrph.status}`);
    const instDead = await H.api(BASE, 'GET', '/api/v1/instances/inst-dead');
    suite.log('recovery: inst-dead is stale/pid-not-found',
      instDead.data && instDead.data.state === 'stale' && instDead.data.recovery_reason === 'pid-not-found',
      instDead.data ? `${instDead.data.state}/${instDead.data.recovery_reason}` : `status=${instDead.status}`);

    // ═══ Scenario: normal completion (exit 0) ═══
    const mNormal = await createModel(BASE, 'ER Normal', 'rt-fake', ['stdout']);
    await startModel(BASE, mNormal.id);
    const iNormal = await waitInstance(BASE, mNormal.id, 'exited', 15000);
    suite.log('API: normal → exited/normal',
      iNormal && iNormal.exit_class === 'normal',
      iNormal ? `${iNormal.state}/${iNormal.exit_class}/${iNormal.exit_code}` : 'not found');

    // ═══ Scenario: abnormal non-zero exit (crash) ═══
    const mCrash = await createModel(BASE, 'ER Crash', 'rt-fake', ['exit-code', '42']);
    await startModel(BASE, mCrash.id);
    const iCrash = await waitInstance(BASE, mCrash.id, 'failed', 15000);
    suite.log('API: crash → failed/failure code 42',
      iCrash && iCrash.exit_class === 'failure' && iCrash.exit_code === 42,
      iCrash ? `${iCrash.state}/${iCrash.exit_class}/${iCrash.exit_code}` : 'not found');

    // ═══ Scenario: explicit Stop (must NOT look like a crash) ═══
    const mStop = await createModel(BASE, 'ER Stop', 'rt-fake', ['infinite']);
    await startModel(BASE, mStop.id);
    const iStop = await waitInstance(BASE, mStop.id, 'running', 15000);
    suite.log('API: stop-target running', !!iStop, iStop ? `pid=${iStop.pid}` : 'not found');
    if (iStop) {
      const stopRes = await H.api(BASE, 'POST', `/api/v1/instances/${iStop.id}/stop`);
      suite.log('API: stop accepted', stopRes.status < 400, `status=${stopRes.status}`);
    }
    const iStopDone = await waitInstance(BASE, mStop.id, 'exited', 30000);
    suite.log('API: stopped → exited/signaled',
      iStopDone && iStopDone.exit_class === 'signaled',
      iStopDone ? `${iStopDone.state}/${iStopDone.exit_class}/${iStopDone.exit_code}` : 'not found');
    data.stopCode = iStopDone ? iStopDone.exit_code : null;

    // ═══ Scenario: start failure (missing executable) ═══
    const rtBadRes = await H.api(BASE, 'POST', '/api/v1/runtimes', {
      name: 'Bad Runtime', executable: BAD_EXE, working_directory: fakeRtDir,
    });
    suite.log('API: bad runtime created', rtBadRes.status === 201, `status=${rtBadRes.status}`);
    if (rtBadRes.status === 201) {
      const mFail = await createModel(BASE, 'ER Fail', rtBadRes.data.id, []);
      const startFailRes = await startModel(BASE, mFail.id);
      suite.log('API: start failure rejected (500 by contract)',
        startFailRes.status === 500, `status=${startFailRes.status}`);
      const iFail = await waitHistoryRow(BASE, mFail.id, 15000);
      suite.log('API: start failure → failed/error + last_error (in history)',
        iFail && iFail.state === 'failed' && iFail.exit_class === 'error' &&
        typeof iFail.last_error === 'string' && iFail.last_error.length > 0,
        iFail ? `${iFail.state}/${iFail.exit_class}/${JSON.stringify(iFail.last_error).slice(0, 120)}` : 'not found');
    }

    // ═══ Scenario: orphan killed (force termination) ═══
    const killRes = await H.api(BASE, 'POST', '/api/v1/instances/inst-orph/kill');
    suite.log('API: orphan kill accepted', killRes.status < 400, `status=${killRes.status} ${JSON.stringify(killRes.data).slice(0, 80)}`);
    const iKill = await poll(async () => {
      const r = await H.api(BASE, 'GET', '/api/v1/instances/inst-orph');
      return r.data && r.data.state === 'stale' ? r.data : null;
    }, 30000, 500);
    suite.log('API: killed orphan → stale/killed-by-user',
      iKill && iKill.recovery_reason === 'killed-by-user', iKill ? `${iKill.state}/${iKill.recovery_reason}` : 'not found');

    // ═══ Scenario: orphan dismissed ═══
    const dismissRes = await H.api(BASE, 'POST', '/api/v1/instances/inst-orph2/dismiss');
    suite.log('API: orphan dismissed', dismissRes.status < 400, `status=${dismissRes.status}`);
    const iDismiss = await H.api(BASE, 'GET', '/api/v1/instances/inst-orph2');
    suite.log('API: dismissed → stale/reconciled-by-user',
      iDismiss.data && iDismiss.data.state === 'stale' && iDismiss.data.recovery_reason === 'reconciled-by-user',
      iDismiss.data ? `${iDismiss.data.state}/${iDismiss.data.recovery_reason}` : `status=${iDismiss.status}`);

    // The UI renders /history (repository) rows: exit_code 0 is omitted there
    // (ToDomain drops zero). Collect the exact fields the UI will see.
    const histStop = await waitHistoryRow(BASE, mStop.id);
    data.stopCode = histStop ? (histStop.exit_code ?? null) : null;
    if (rtBadRes.status === 201) {
      const mFailHist = (await H.api(BASE, 'GET', '/api/v1/history')).data || [];
      const failRow = mFailHist.find(i => i.model_name === 'ER Fail');
      data.failLastError = failRow ? failRow.last_error : null;
    }

    // ═══ UI assertions — RU (default) ═══
    await checkLang(page, suite, 'ru', data);
    await H.screenshot(page, ws, 'exit_reason_ru');

    // ═══ UI assertions — EN ═══
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(3500);
    await checkLang(page, suite, 'en', data);
    await H.screenshot(page, ws, 'exit_reason_en');

    // ═══ Responsive 430px: compact rows + no page-level horizontal overflow ═══
    await page.setViewportSize({ width: 430, height: 932 });
    await page.evaluate(() => window.navigate('history'));
    await page.waitForTimeout(1000);
    const geo = await page.evaluate(() => ({
      compactRows: document.querySelectorAll('#history-compact .chist-row').length,
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    }));
    suite.log('@430 history compact rows rendered', geo.compactRows >= 5, `rows=${geo.compactRows}`);
    suite.log('@430 no page-level horizontal overflow', geo.overflow <= 0, `overflow=${geo.overflow}px`);
    await H.screenshot(page, ws, 'exit_reason_430');

    suite.log('Console: 0 uncaught exceptions', suite.consoleErrors.length === 0, suite.consoleErrors.slice(0, 3).join('; '));
    suite.log('Server: no 5xx responses', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));
    ok = await suite.finish();
  } catch (err) {
    suite.log('FATAL: ' + err.message, false, err.stack ? err.stack.split('\n')[0] : '');
    ok = false;
    await suite.finish();
  } finally {
    try { h1.kill(); } catch {}
    try { h3.kill(); } catch {}
    await browser.close();
    await server.stop();
    H.killTree(h1.pid);
    H.killTree(h3.pid);
  }

  process.exit(ok ? 0 : 1);
}

main().catch(err => {
  console.error('HARNESS ERROR:', err);
  process.exit(2);
});

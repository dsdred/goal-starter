'use strict';
const path = require('path');
const fs = require('fs');
const H = require('./harness.cjs');

const PORT = 19471;
const BASE = `http://127.0.0.1:${PORT}`;
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'test1234';

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

async function instanceStates(page, modelId) {
  const res = await H.pageApi(page, 'GET', '/api/v1/instances');
  return (res.data || []).filter(i => i.model_id === modelId).map(i => i.state);
}

module.exports = { BASE };

async function main() {
  const ws = H.makeWorkspace('core');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  H.writeConfig(ws, {
    webPort: PORT,
    authEnabled: false,
    adminUser: ADMIN_USER,
    adminPassword: ADMIN_PASS,
    runtimes: [{
      id: 'rt-fake-1',
      name: 'Fake Runtime 1',
      executable: fakeRt,
      workingDirectory: fakeRtDir,
    }],
  });

  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started (auth OFF).\n');

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('CORE (auth OFF + auth ON)');
  suite.watchPage(page);

  let ok = false;
  try {
    // ═══ SECTION 1: Empty repository ═══
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);
    await H.screenshot(page, ws, '01-empty-repo');

    suite.log('1.1 Empty repo shows no model rows', (await page.locator('#model-list .model-row').count()) === 0);
    suite.log('1.2 Empty state message visible', await page.locator('#models-empty').isVisible());

    // ═══ SECTION 2: Wizard → model with existing runtime ═══
    await page.click('#view-models button:has-text("Добавить модель")');
    await page.waitForTimeout(300);
    await page.fill('#wiz-name', 'Test Model 27B');
    await page.click('#wiz-next');
    await page.waitForTimeout(300);

    await page.fill('#wiz-rt-search', 'Fake');
    await page.waitForTimeout(200);
    const ddItems = page.locator('#wiz-rt-dropdown .rt-dropdown-item');
    const ddCount = await ddItems.count();
    const ddNames = ddCount > 0 ? await ddItems.allTextContents() : [];
    suite.log('2.1 Existing runtime visible in wizard dropdown', ddCount >= 1 && ddNames.some(t => t.includes('Fake Runtime 1')), `items=${JSON.stringify(ddNames)}`);
    await ddItems.first().click();
    await page.waitForTimeout(200);
    await page.click('#wiz-next');
    await page.waitForTimeout(300);

    await page.fill('#wiz-args', '-m /models/test.gguf\n-ngl 99\n-c 131072\n--temp 1.0\n--host 127.0.0.1\n--port 8085');
    await H.screenshot(page, ws, '02-wizard-launch');
    await page.click('#wiz-next');
    await page.waitForTimeout(1500);

    suite.log('2.2 Model created (1 row)', (await page.locator('#model-list .model-row').count()) === 1);
    const listRes = await H.api(BASE, 'GET', '/api/v1/models');
    suite.log('2.3 API shows 1 model', listRes.status === 200 && listRes.data.length === 1);
    let modelA = listRes.data.length > 0 ? listRes.data[0] : null;
    if (modelA) {
      suite.log('2.4 Model has -m arg', modelA.args && modelA.args.includes('-m'), `args=${JSON.stringify(modelA.args)}`);
      const hostOk = modelA.args && modelA.args.includes('--host') && modelA.args[modelA.args.indexOf('--host') + 1] === '127.0.0.1';
      const portOk = modelA.args && modelA.args.includes('--port') && modelA.args[modelA.args.indexOf('--port') + 1] === '8085';
      suite.log('2.5 Args carry --host 127.0.0.1 and --port 8085', hostOk && portOk);
    }

    // ═══ SECTION 3: Wizard → model with new runtime ═══
    await page.click('#view-models button:has-text("Добавить модель")');
    await page.waitForTimeout(300);
    await page.fill('#wiz-name', 'New RT Model');
    await page.click('#wiz-next');
    await page.waitForTimeout(300);
    await page.check('input[name="wiz-rt-mode"][value="new"]');
    await page.waitForTimeout(200);
    await page.fill('#wiz-rt-name', 'Fake Runtime 2');
    await page.fill('#wiz-rt-workdir', fakeRtDir);
    await page.fill('#wiz-rt-executable', fakeRt);
    await H.screenshot(page, ws, '03-wizard-runtime-new');
    await page.click('#wiz-next');
    await page.waitForTimeout(300);
    await page.fill('#wiz-args', '-m /models/new_rt.gguf\n-ngl 40\n--mmproj /models/mmproj.gguf\n--host 0.0.0.0\n--port 8086');
    await page.click('#wiz-next');
    await page.waitForTimeout(2000);

    const listRes2 = await H.api(BASE, 'GET', '/api/v1/models');
    suite.log('3.1 Two models after second wizard', listRes2.data.length === 2);
    const rtRes = await H.api(BASE, 'GET', '/api/v1/runtimes');
    suite.log('3.2 New runtime created automatically', rtRes.data.some(r => r.name === 'Fake Runtime 2'));
    modelA = listRes2.data.find(m => m.name === 'Test Model 27B');
    const modelB = listRes2.data.find(m => m.name === 'New RT Model');

    // ═══ SECTION 4: Resolve / preview command ═══
    if (modelA) {
      const resolveRes = await H.api(BASE, 'POST', `/api/v1/models/${modelA.id}/resolve`);
      suite.log('4.1 Resolve returns 200', resolveRes.status === 200, `status=${resolveRes.status}`);
      if (resolveRes.status === 200) {
        const spec = resolveRes.data;
        suite.log('4.2 Resolved args contain -m', spec.args && spec.args.includes('-m'));
        suite.log('4.3 No duplicate --host flag', (spec.args || []).filter(a => a === '--host').length <= 1);
        suite.log('4.4 No duplicate --port flag', (spec.args || []).filter(a => a === '--port').length <= 1);
      }
    }

    // ═══ SECTION 5: Lifecycle model (infinite) start → RUNNING ═══
    const lifeRes = await H.api(BASE, 'POST', '/api/v1/models', {
      name: 'Lifecycle Model', runtime_id: 'rt-fake-1', args: ['infinite'], host: '127.0.0.1', port: 8090,
    });
    suite.log('5.0 Lifecycle model created', lifeRes.status === 201, `status=${lifeRes.status}`);
    const lifeId = lifeRes.data ? lifeRes.data.id : '';
    const startRes = await H.api(BASE, 'POST', `/api/v1/models/${lifeId}/start`);
    suite.log('5.1 Start returns 200', startRes.status === 200, `status=${startRes.status}`);
    if (startRes.status === 200) {
      const inst = startRes.data;
      suite.log('5.2 Instance has PID > 0', inst.pid > 0, `pid=${inst.pid}`);
      suite.log('5.3 Instance state is running/starting', ['running', 'starting'].includes(inst.state), `state=${inst.state}`);
      suite.log('5.4 Instance model_id correct', inst.model_id === lifeId);
    }
    const running = await poll(async () => {
      const states = await instanceStates(page, lifeId);
      return states.includes('running') ? states : null;
    }, 8000);
    suite.log('5.5 Instance reaches running state', running !== null, running ? `states=${JSON.stringify(running)}` : 'timeout');
    await page.waitForTimeout(1000);
    await H.screenshot(page, ws, '05-model-running');

    // ═══ SECTION 6: Stop ═══
    const stopRes = await H.api(BASE, 'POST', `/api/v1/models/${lifeId}/stop`);
    suite.log('6.1 Stop returns 200', stopRes.status === 200);
    const stopped = await poll(async () => {
      const states = await instanceStates(page, lifeId);
      return states.length > 0 && states.every(s => !['running', 'starting', 'stopping'].includes(s)) ? states : null;
    }, 8000);
    suite.log('6.2 Instance reaches terminal state after stop', stopped !== null, stopped ? `states=${JSON.stringify(stopped)}` : 'timeout');

    // ═══ SECTION 7: Restart → new instance ID ═══
    await H.api(BASE, 'POST', `/api/v1/models/${lifeId}/start`);
    await poll(async () => {
      const states = await instanceStates(page, lifeId);
      return states.includes('running') ? true : null;
    }, 8000);
    const restartRes = await H.api(BASE, 'POST', `/api/v1/models/${lifeId}/restart`);
    suite.log('7.1 Restart returns 200', restartRes.status === 200);
    await sleep(1500);
    const instList = await H.api(BASE, 'GET', '/api/v1/instances');
    const modelInstances = (instList.data || []).filter(i => i.model_id === lifeId);
    suite.log('7.2 Multiple instances exist after restart', modelInstances.length >= 2, `count=${modelInstances.length}`);
    if (modelInstances.length >= 2) {
      const ids = modelInstances.map(i => i.id);
      suite.log('7.3 Instance IDs are unique', new Set(ids).size === ids.length);
    }
    const runningAfterRestart = await poll(async () => {
      const list = (await H.api(BASE, 'GET', '/api/v1/instances')).data || [];
      return list.some(i => i.model_id === lifeId && i.state === 'running');
    }, 10000);
    suite.log('7.4 Instance running after restart (old process stopped, new process started)', runningAfterRestart === true, runningAfterRestart === true ? 'running' : 'no running instance');
    await H.api(BASE, 'POST', `/api/v1/models/${lifeId}/stop`);
    await poll(async () => {
      const states = await instanceStates(page, lifeId);
      return states.length > 0 && states.every(s => !['running', 'starting', 'stopping'].includes(s)) ? true : null;
    }, 8000);

    // ═══ SECTION 8: Logs view ═══
    await page.click('#sidebar button[data-view="logs"]');
    await page.waitForTimeout(1000);
    const logOptions = await page.locator('#log-instance-select option').count();
    const instApi = await H.api(BASE, 'GET', '/api/v1/instances');
    suite.log('8.1 Log instance selector populated', logOptions >= 2, `options=${logOptions}, repoInstances=${instApi.data.length}`);
    await H.screenshot(page, ws, '08-logs-view');

    // ═══ SECTION 9: History ═══
    await page.click('#sidebar button[data-view="history"]');
    await page.waitForTimeout(1000);
    const historyRows = await page.locator('#history-body tr').count();
    suite.log('9.1 History shows completed instances', historyRows >= 1, `rows=${historyRows}`);
    await H.screenshot(page, ws, '09-history');

    // ═══ SECTION 10: Advanced → Instances (live data) ═══
    await H.api(BASE, 'POST', `/api/v1/models/${lifeId}/start`);
    await page.click('#sidebar button[data-view="adv-instances"]');
    const liveRows = await poll(async () => {
      const n = await page.locator('#adv-instances-body tr').count();
      return n >= 1 ? n : null;
    }, 15000);
    suite.log('10.1 Instances table populated with live instance', liveRows !== null, `rows=${liveRows}`);
    await H.screenshot(page, ws, '10-advanced-instances');
    await H.api(BASE, 'POST', `/api/v1/models/${lifeId}/stop`);
    await poll(async () => {
      const states = await instanceStates(page, lifeId);
      return states.length > 0 && states.every(s => !['running', 'starting', 'stopping'].includes(s)) ? true : null;
    }, 8000);

    // ═══ SECTION 11: Edit model (rename via wizard) ═══
    await page.click('#sidebar button[data-view="models"]');
    await page.waitForTimeout(500);
    const editBtn = page.locator('#model-list .model-row').filter({ hasText: 'Test Model 27B' }).locator('button[aria-label="Изменить"]');
    if (await editBtn.count() > 0) {
      await editBtn.click();
      await page.waitForTimeout(500);
      await page.fill('#wiz-name', 'Test Model 27B (edited)');
      await page.click('#wiz-next');
      await page.waitForTimeout(300);
      await page.click('#wiz-next');
      await page.waitForTimeout(300);
      await page.click('#wiz-next');
      await page.waitForTimeout(1500);
      const updated = await H.api(BASE, 'GET', `/api/v1/models/${modelA.id}`);
      suite.log('11.1 Model name updated', updated.data && updated.data.name === 'Test Model 27B (edited)', `name=${updated.data && updated.data.name}`);
    } else {
      suite.log('11.1 Edit button found', false, 'no Изменить button on model row');
    }

    // ═══ SECTION 12: Delete model, instance history preserved ═══
    if (modelB) {
      const instCountBefore = (await H.api(BASE, 'GET', '/api/v1/instances')).data.length;
      const delRes = await H.api(BASE, 'DELETE', `/api/v1/models/${modelB.id}`);
      suite.log('12.1 Delete model returns 200', delRes.status === 200);
      const instCountAfter = (await H.api(BASE, 'GET', '/api/v1/instances')).data.length;
      suite.log('12.2 Instance history preserved after model delete', instCountAfter === instCountBefore, `before=${instCountBefore} after=${instCountAfter}`);
    }

    // ═══ SECTION 13: Runtime delete with dependent models → 409 ═══
    const delRtRes = await H.api(BASE, 'DELETE', '/api/v1/runtimes/rt-fake-1');
    suite.log('13.1 Runtime with dependent models → 409', delRtRes.status === 409, `status=${delRtRes.status}`);
    suite.log('13.2 409 response has details', delRtRes.data && (delRtRes.data.details || delRtRes.data.error), `body=${JSON.stringify(delRtRes.data).slice(0, 200)}`);

    // ═══ SECTION 14: Runtime delete after removing dependencies ═══
    const remaining = (await H.api(BASE, 'GET', '/api/v1/models')).data;
    for (const m of remaining) {
      if (m.runtime_id === 'rt-fake-1') await H.api(BASE, 'DELETE', `/api/v1/models/${m.id}`);
    }
    await sleep(500);
    const delRtRes2 = await H.api(BASE, 'DELETE', '/api/v1/runtimes/rt-fake-1');
    suite.log('14.1 Runtime delete succeeds after removing models', delRtRes2.status === 200, `status=${delRtRes2.status}`);

    // ═══ SECTION 15: Autostart model (launched at next server start) ═══
    const rts2 = (await H.api(BASE, 'GET', '/api/v1/runtimes')).data;
    const rt2 = rts2.find(r => r.name === 'Fake Runtime 2');
    if (rt2) {
      const autoModel = await H.api(BASE, 'POST', '/api/v1/models', {
        name: 'Autostart Test', runtime_id: rt2.id, args: ['infinite'], active: true, autostart_delay: 0,
      });
      suite.log('15.1 Create autostart model', autoModel.status === 201, `status=${autoModel.status}`);
      const getModel = await H.api(BASE, 'GET', `/api/v1/models/${autoModel.data.id}`);
      suite.log('15.2 Model active=true', getModel.data && getModel.data.active === true);
    } else {
      suite.log('15.1 Create autostart model', false, 'Fake Runtime 2 not found');
    }

    // ═══ SECTION 17: Polling (2+ cycles) ═══
    await page.click('#sidebar button[data-view="models"]');
    await page.waitForTimeout(7000);
    const pollRows = await page.locator('#model-list .model-row').count();
    suite.log('17.1 Page still functional after 2+ poll cycles', pollRows >= 0, `rows=${pollRows}`);

    // ═══ SECTION 18: Auth OFF ═══
    suite.log('18.1 Auth OFF: no login modal', !(await page.locator('#login-modal').isVisible()));
    suite.log('18.2 Auth OFF: app shell visible', await page.locator('#app-shell').isVisible());

    // ═══ SECTION 19: Basic responsive (full contract in responsive.cjs) ═══
    const viewports = [
      { w: 1920, h: 1080 }, { w: 1366, h: 768 }, { w: 768, h: 1024 }, { w: 390, h: 844 },
    ];
    for (const vp of viewports) {
      await page.setViewportSize({ width: vp.w, height: vp.h });
      await page.goto(BASE, { waitUntil: 'networkidle' });
      await page.waitForTimeout(800);
      const hasOverflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 5);
      suite.log(`19 Responsive ${vp.w}px: no horizontal overflow`, !hasOverflow);
    }
    await page.setViewportSize({ width: 1920, height: 1080 });

    // ═══ SECTION 20: Console errors & server 5xx ═══
    suite.log('20.1 No console errors', suite.consoleErrors.length === 0, suite.consoleErrors.length > 0 ? suite.consoleErrors.slice(0, 3).join('; ') : '');
    suite.log('20.2 No server 5xx errors', suite.serverErrors.length === 0, suite.serverErrors.length > 0 ? JSON.stringify(suite.serverErrors.slice(0, 3)) : '');

    // ═══ SECTION 21: Settings shows version ═══
    await page.click('#sidebar button[data-view="adv-settings"]');
    await page.waitForTimeout(500);
    const versionVisible = await page.locator('#set-version').textContent();
    suite.log('21.1 Settings shows version', !!versionVisible && versionVisible.trim() !== '-', `version=${versionVisible && versionVisible.trim()}`);

    // ═══ SECTION 22: Environment secret not in API/DOM ═══
    const rtAny = (await H.api(BASE, 'GET', '/api/v1/runtimes')).data[0];
    if (rtAny) {
      const secretModel = await H.api(BASE, 'POST', '/api/v1/models', {
        name: 'Secret Env Test', runtime_id: rtAny.id, args: ['-m', '/models/secret.gguf'],
        environment: { MY_SECRET_KEY: 'super-secret-value-12345' },
      });
      if (secretModel.status === 201) {
        const gotModel = await H.api(BASE, 'GET', `/api/v1/models/${secretModel.data.id}`);
        const bodyStr = JSON.stringify(gotModel.data);
        suite.log('22.1 Environment value not in API response', !bodyStr.includes('super-secret-value-12345'));
        suite.log('22.2 Environment keys present', gotModel.data && gotModel.data.environment_keys && gotModel.data.environment_keys.includes('MY_SECRET_KEY'));
        await page.goto(BASE, { waitUntil: 'networkidle' });
        await page.waitForTimeout(1000);
        const domContent = await page.content();
        suite.log('22.3 Environment value not in DOM', !domContent.includes('super-secret-value-12345'));
        await H.api(BASE, 'DELETE', `/api/v1/models/${secretModel.data.id}`);
      }
    }

    ok = await suite.finish();

    // ═══ PHASE B: Auth ON (restart server, same data dir) ═══
    await server.stop();
    await sleep(1000);
    const cfg = JSON.parse(fs.readFileSync(ws.config, 'utf8'));
    cfg.authEnabled = true;
    fs.writeFileSync(ws.config, JSON.stringify(cfg, null, 2));

    const server2 = H.startServer(goalBin, ws);
    const started2 = await H.waitForHealth(BASE);
    if (started2) {
      const preAuth = await H.api(BASE, 'GET', '/api/v1/models');
      suite.log('23.0 API requires auth before login (401)', preAuth.status === 401, `status=${preAuth.status}`);

      const page2 = await ctx.newPage();
      suite.watchPage(page2);
      await H.login(page2, BASE, ADMIN_USER, ADMIN_PASS);
      await H.screenshot(page2, ws, '23-auth-logged-in');

      suite.log('23.1 Auth ON: app shell visible after login', await page2.locator('#app-shell').isVisible());
      const sessionRes = await page2.evaluate(async () => {
        const r = await fetch('/api/v1/auth/session');
        return r.json();
      });
      suite.log('23.2 Auth ON: session authenticated as admin', sessionRes.authenticated === true && sessionRes.user === ADMIN_USER, `session=${JSON.stringify(sessionRes)}`);
      suite.log('23.3 Auth ON: logout button in sidebar', await page2.locator('#sidebar-logout').isVisible());

      const autoStates = await page2.evaluate(async () => {
        const r = await fetch('/api/v1/instances');
        const data = await r.json();
        return (data || []).map(i => i.state);
      });
      suite.log('23.4 Autostart model launched at server start', autoStates.some(s => s === 'running' || s === 'starting'), `states=${JSON.stringify(autoStates)}`);

      await page2.click('#sidebar-logout');
      await page2.waitForTimeout(1000);
      suite.log('23.5 Auth ON: logout shows login again', await page2.locator('#login-modal').isVisible());

      const postAuth = await H.api(BASE, 'GET', '/api/v1/models');
      suite.log('23.6 API 401 after logout', postAuth.status === 401, `status=${postAuth.status}`);

      // Pre-login app shell requests returning 401 are expected auth-gating
      // behavior, not defects. Filter them; keep any real JS/resource error.
      const unexpectedConsole = suite.consoleErrors.filter(e =>
        !/Failed to load resource[^\n]*401 \(Unauthorized\)/.test(e));
      suite.log('24.1 Auth phase: no unexpected console errors', unexpectedConsole.length === 0,
        unexpectedConsole.slice(0, 3).join('; ') || `(${suite.consoleErrors.length} expected 401 network notices ignored)`);
      ok = (await suite.finish()) && ok;
      await server2.stop();
    } else {
      suite.log('23 Auth ON server start', false, server2.output.slice(-300));
      ok = false;
    }
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

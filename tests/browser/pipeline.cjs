'use strict';
const path = require('path');
const H = require('./harness.cjs');

const PORT = 19476;
const BASE = `http://127.0.0.1:${PORT}`;

module.exports = { BASE };

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

async function main() {
  const ws = H.makeWorkspace('pipeline');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  H.writeConfig(ws, {
    webPort: PORT,
    authEnabled: false,
    runtimes: [{
      id: 'rt-fake',
      name: 'Fake Runtime',
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

  const api = (method, urlPath, body) => H.api(BASE, method, urlPath, body);

  // Two stoppable models (fake-runtime "infinite" mode: runs until stopped).
  const mA = await api('POST', '/api/v1/models', { name: 'Pipe Model A', runtime_id: 'rt-fake', args: ['infinite'] });
  const mB = await api('POST', '/api/v1/models', { name: 'Pipe Model B', runtime_id: 'rt-fake', args: ['infinite'] });
  const idA = mA.data && mA.data.id;
  const idB = mB.data && mB.data.id;

  const pipelineInstances = async () => {
    const res = await api('GET', '/api/v1/instances');
    const all = (res.data || []).filter(i => i.pipeline_id);
    return all;
  };
  const runningFor = async (pipeId) => (await pipelineInstances()).filter(i => i.pipeline_id === pipeId && i.state === 'running');

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('PIPELINE (UI CRUD + lifecycle + responsive + i18n)');
  suite.watchPage(page);

  let pipeId = null;
  let ok = false;
  try {
    suite.log('Seed: model A created', mA.status === 201, `status=${mA.status}`);
    suite.log('Seed: model B created', mB.status === 201, `status=${mB.status}`);

    // ═══ SECTION 1: Empty state ═══
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(800);
    await page.evaluate(() => window.navigate('pipelines'));
    await page.waitForTimeout(500);
    suite.log('1.1 Empty state visible', await page.locator('#pipelines-empty').isVisible());
    suite.log('1.2 Table hidden when empty', !(await page.locator('#pipelines-table-wrap').evaluate(el => el.classList.contains('visible'))));
    await H.screenshot(page, ws, '01-pipelines-empty');

    // ═══ SECTION 2: Create pipeline via UI (2 models, override + autostart on A) ═══
    await page.click('#view-pipelines .view-header .btn-primary');
    await page.waitForTimeout(400);
    suite.log('2.1 Create modal opens', await page.locator('#pipeline-modal').evaluate(el => getComputedStyle(el).display === 'flex'));
    suite.log('2.2 One default model row present', (await page.locator('#pl-models-container .pl-model-row').count()) === 1);

    await page.fill('#pipeline-form input[name="name"]', 'Cluster A');
    // Row 1: model A, args override, autostart on.
    // The override must be a valid long-running fake-runtime mode so the
    // instance stays up for the lifecycle checks; "infinite <tag>" runs until
    // killed and echoes the tag (all-or-nothing: it fully replaces base args).
    await page.locator('#pl-models-container .pl-model-row').nth(0).locator('.pl-model-select').selectOption(idA);
    await page.locator('#pl-models-container .pl-model-row').nth(0).locator('.pl-args-input').fill('infinite override-A');
    // The autostart checkbox is a visually-hidden toggle input; click the visible slider.
    await page.locator('#pl-models-container .pl-model-row').nth(0).locator('.toggle-slider').click();
    // Row 2: model B (no override, autostart off).
    await page.click('button[onclick="addPlModelRow()"]');
    await page.waitForTimeout(200);
    suite.log('2.3 Add-model button adds a second row', (await page.locator('#pl-models-container .pl-model-row').count()) === 2);
    await page.locator('#pl-models-container .pl-model-row').nth(1).locator('.pl-model-select').selectOption(idB);

    await page.click('#pipeline-submit-btn');
    await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      return (r.data || []).length === 1;
    }, 8000);
    const list0 = await api('GET', '/api/v1/pipelines');
    pipeId = list0.data && list0.data[0] && list0.data[0].id;
    suite.log('2.4 Pipeline created (201 persisted)', !!pipeId, `id=${pipeId}`);
    // The UI re-render (reloadAllData + renderAll) lags the API write; wait for it.
    await poll(async () => (await page.locator('#pipelines-body tr').count()) === 1, 8000);
    suite.log('2.5 Table shows one row', (await page.locator('#pipelines-body tr').count()) === 1);
    suite.log('2.6 Empty state hidden', !(await page.locator('#pipelines-empty').isVisible()));
    await H.screenshot(page, ws, '02-pipeline-created');

    // ═══ SECTION 3: Created content (API contract + UI chips) ═══
    const p0 = list0.data[0];
    suite.log('3.1 Name persisted', p0.name === 'Cluster A', `name=${p0.name}`);
    suite.log('3.2 Two ordered models', p0.models.length === 2 && p0.models[0].model_id === idA && p0.models[1].model_id === idB,
      `order=${JSON.stringify(p0.models.map(m => m.model_id))}`);
    const aArgs = (p0.models[0].args || []).join(' ');
    suite.log('3.3 Model A override args persisted (all-or-nothing)', aArgs === 'infinite override-A', `args=${JSON.stringify(p0.models[0].args)}`);
    suite.log('3.4 Model A auto_start=true', p0.models[0].auto_start === true);
    suite.log('3.5 Model B auto_start=false (default)', p0.models[1].auto_start === false);
    suite.log('3.6 Model B has no override args', (p0.models[1].args || []).length === 0, `args=${JSON.stringify(p0.models[1].args)}`);
    suite.log('3.7 Pipeline Active=false (default)', p0.active === false);

    const chipCount = await page.locator('#pipelines-body .status-badge').count();
    const chipText = (await page.locator('#pipelines-body .pl-chips-cell').first().textContent() || '');
    suite.log('3.8 UI shows two model chips', chipCount === 2, `chips=${chipCount}`);
    suite.log('3.9 Chips show both model names', chipText.includes('Pipe Model A') && chipText.includes('Pipe Model B'), `text=${JSON.stringify(chipText)}`);
    // The override marker (↯) and autostart marker (A) make the per-entry state visible.
    suite.log('3.10 Override/autostart markers visible on model A chip', /Pipe Model A[^\n]*A/.test(chipText) && chipText.includes('↯'), `text=${JSON.stringify(chipText)}`);

    // ═══ SECTION 4: Per-model status renders (stopped initially) ═══
    const stoppedChips = await page.locator('#pipelines-body .status-badge.stopped').count();
    suite.log('4.1 Per-model status renders as stopped (2)', stoppedChips === 2, `stopped=${stoppedChips}`);

    // ═══ SECTION 5: Start pipeline (end-to-end) ═══
    await page.click('#pipelines-body button[onclick^="startPipeline"]');
    const bothRunning = await poll(async () => {
      const r = await runningFor(pipeId);
      return r.length === 2 ? r : null;
    }, 20000);
    suite.log('5.1 Start: both instances running with pipeline_id', !!bothRunning, `running=${bothRunning ? bothRunning.length : 0}`);
    const allInst = await pipelineInstances();
    suite.log('5.2 Both owned instances carry the pipeline_id', allInst.length === 2 && allInst.every(i => i.pipeline_id === pipeId), `count=${allInst.length}`);
    await poll(async () => (await page.locator('#pipelines-body .status-badge.running').count()) === 2, 8000);
    const runningChips = await page.locator('#pipelines-body .status-badge.running').count();
    suite.log('5.3 Per-model status chips show running (2)', runningChips === 2, `running=${runningChips}`);
    await H.screenshot(page, ws, '03-pipeline-running');

    // ═══ SECTION 6: Restart pipeline (end-to-end) ═══
    const oldIds = (await runningFor(pipeId)).map(i => i.id).sort();
    await page.click('#pipelines-body button[onclick^="restartPipeline"]');
    const restarted = await poll(async () => {
      const r = await runningFor(pipeId);
      if (r.length !== 2) return null;
      const newIds = r.map(i => i.id).sort();
      return newIds.some(id => oldIds.indexOf(id) === -1) ? r : null;
    }, 25000);
    suite.log('6.1 Restart: both running again', !!restarted, `running=${restarted ? restarted.length : 0}`);
    suite.log('6.2 Restart: new instances (old ones replaced)', !!restarted && restarted.every(i => oldIds.indexOf(i.id) === -1), `old=${JSON.stringify(oldIds)}`);
    await H.screenshot(page, ws, '04-pipeline-restarted');

    // ═══ SECTION 7: Stop pipeline (end-to-end) ═══
    await page.click('#pipelines-body button[onclick^="stopPipeline"]');
    const allStopped = await poll(async () => {
      const r = await runningFor(pipeId);
      return r.length === 0 ? true : null;
    }, 20000);
    suite.log('7.1 Stop: all owned instances stopped', allStopped === true);
    await page.waitForTimeout(600);
    const stoppedAfter = await page.locator('#pipelines-body .status-badge.stopped').count();
    suite.log('7.2 Per-model status chips back to stopped (2)', stoppedAfter === 2, `stopped=${stoppedAfter}`);
    await H.screenshot(page, ws, '05-pipeline-stopped');

    // ═══ SECTION 8: Pipeline Active toggle (persistent setting, distinct from Start) ═══
    await page.locator('#pipelines-body .toggle-pipeline .toggle-slider').click();
    const activeOn = await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      const p = (r.data || []).find(x => x.id === pipeId);
      return p && p.active === true ? true : null;
    }, 8000);
    suite.log('8.1 Active toggle sets active=true (persisted)', activeOn === true);
    await page.waitForTimeout(400);
    const toggleChecked = await page.locator('#pipelines-body .toggle-pipeline input[type="checkbox"]').isChecked();
    suite.log('8.2 Active toggle reflects the persistent setting in the row', toggleChecked === true);
    // Distinct from the manual Start action: toggling Active did not launch instances.
    const stillDown = (await runningFor(pipeId)).length === 0;
    suite.log('8.3 Active is a setting, not a launch: no instances running after toggle', stillDown);

    // ═══ SECTION 9: Edit pipeline (rename + prefilled models) ═══
    await page.click('#pipelines-body button[onclick^="editPipeline"]');
    await page.waitForTimeout(400);
    const editRows = await page.locator('#pl-models-container .pl-model-row').count();
    const row0Sel = await page.locator('#pl-models-container .pl-model-row').nth(0).locator('.pl-model-select').inputValue();
    const row1Sel = await page.locator('#pl-models-container .pl-model-row').nth(1).locator('.pl-model-select').inputValue();
    suite.log('9.1 Edit modal prefills both model rows in order', editRows === 2 && row0Sel === idA && row1Sel === idB, `rows=${editRows} r0=${row0Sel} r1=${row1Sel}`);
    await page.fill('#pipeline-form input[name="name"]', 'Cluster A2');
    await page.click('#pipeline-submit-btn');
    const renamed = await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      const p = (r.data || []).find(x => x.id === pipeId);
      return p && p.name === 'Cluster A2' ? true : null;
    }, 8000);
    suite.log('9.2 Rename persisted (structural list unchanged)', renamed === true);
    await poll(async () => ((await page.locator('#pipelines-body tr').first().textContent() || '')).includes('Cluster A2'), 8000);
    suite.log('9.3 Table shows the new name', (await page.locator('#pipelines-body tr').first().textContent() || '').includes('Cluster A2'));
    await H.screenshot(page, ws, '06-pipeline-edited');

    // ═══ SECTION 10: Validation — duplicate model rejected, no pipeline created ═══
    const beforeCount = (await api('GET', '/api/v1/pipelines').data || []).length;
    await page.click('#view-pipelines .view-header .btn-primary');
    await page.waitForTimeout(300);
    await page.fill('#pipeline-form input[name="name"]', 'Dup Pipeline');
    await page.locator('#pl-models-container .pl-model-row').nth(0).locator('.pl-model-select').selectOption(idA);
    await page.click('button[onclick="addPlModelRow()"]');
    await page.waitForTimeout(200);
    await page.locator('#pl-models-container .pl-model-row').nth(1).locator('.pl-model-select').selectOption(idA);
    await page.click('#pipeline-submit-btn');
    await page.waitForTimeout(600);
    const dupErrorVisible = await page.locator('#toast-container .toast.error').count() > 0;
    const afterCount = (await api('GET', '/api/v1/pipelines').data || []).length;
    suite.log('10.1 Duplicate model: error toast shown', dupErrorVisible);
    suite.log('10.2 Duplicate model: no pipeline created', afterCount === beforeCount, `count=${afterCount}`);
    // Close the still-open create modal.
    await page.click('#pipeline-modal .modal-actions .btn-ghost');
    await page.waitForTimeout(300);

    // ═══ SECTION 11: Responsive table -> cards at the 768px boundary ═══
    await page.setViewportSize({ width: 768, height: 1024 });
    await page.waitForTimeout(500);
    const tableVisible = await page.locator('#pipelines-table-wrap').evaluate(el => getComputedStyle(el).display !== 'none');
    const compactVisible = await page.locator('#pipelines-compact').evaluate(el => getComputedStyle(el).display !== 'none');
    suite.log('11.1 At 768px the compact card list is shown', compactVisible);
    suite.log('11.2 At 768px the table is hidden', !tableVisible);
    const compactRowText = (await page.locator('#pipelines-compact .compact-row').first().textContent() || '');
    suite.log('11.3 Compact card shows the pipeline name', compactRowText.includes('Cluster A2'), `text=${JSON.stringify(compactRowText)}`);
    await H.screenshot(page, ws, '07-pipelines-768');
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.waitForTimeout(300);

    // ═══ SECTION 12: i18n EN/RU nav label ═══
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(400);
    const navEn = ((await page.locator('.nav-item[data-view="pipelines"] span[data-i18n]').first().textContent() || '').trim());
    suite.log('12.1 EN nav label is "Pipelines"', navEn === 'Pipelines', `text=${JSON.stringify(navEn)}`);
    const titleEn = ((await page.locator('#view-pipelines h1').textContent() || '').trim());
    suite.log('12.2 EN page title localized', titleEn === 'Pipelines', `text=${JSON.stringify(titleEn)}`);
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(400);
    const navRu = ((await page.locator('.nav-item[data-view="pipelines"] span[data-i18n]').first().textContent() || '').trim());
    suite.log('12.3 RU nav label localized (not English)', navRu.length > 0 && navRu !== 'Pipelines', `text=${JSON.stringify(navRu)}`);

    // ═══ SECTION 13: Delete pipeline (confirm dialog) ═══
    await page.click('#pipelines-body button[onclick^="deletePipeline"]');
    await page.waitForTimeout(300);
    suite.log('13.1 Confirm dialog opens', await page.locator('#confirm-modal').evaluate(el => getComputedStyle(el).display === 'flex'));
    await page.click('#confirm-yes');
    const deleted = await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      return (r.data || []).length === 0 ? true : null;
    }, 8000);
    suite.log('13.2 Pipeline deleted', deleted === true);
    await page.waitForTimeout(400);
    suite.log('13.3 Empty state returns after delete', await page.locator('#pipelines-empty').isVisible());
    await H.screenshot(page, ws, '08-pipeline-deleted');

    // ═══ SECTION 14: sanity ═══
    suite.log('14.1 No unexpected console errors', suite.consoleErrors.length === 0,
      suite.consoleErrors.slice(0, 3).join('; ') || 'clean');
    suite.log('14.2 No server 5xx errors', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));

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

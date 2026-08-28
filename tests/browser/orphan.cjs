'use strict';
const path = require('path');
const fs = require('fs');
const { spawn } = require('child_process');
const H = require('./harness.cjs');

const PORT = 19484;
const BASE = `http://127.0.0.1:${PORT}`;
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'testpass123';

async function main() {
  console.log('=== Orphan recovery / Dismiss (real Chromium) ===\n');

  const ws = H.makeWorkspace('orphan');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  H.writeConfig(ws, {
    version: 2,
    webPort: PORT,
    authEnabled: true,
    adminUser: ADMIN_USER,
    adminPassword: ADMIN_PASS,
  });

  // Start fake-runtime INDEPENDENTLY (not in GoAl's process tree)
  const helper = spawn(fakeRt, ['infinite'], { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
  const helperPid = helper.pid;
  await new Promise(r => setTimeout(r, 500));
  if (!H.isProcessAlive(helperPid)) {
    console.log('FATAL: helper died immediately');
    process.exit(1);
  }
  console.log(`Helper started (independent) pid=${helperPid}`);

  // Pre-populate data store with runtime + model + running instance
  const now = new Date().toISOString();
  const repo = {
    schema_version: 7,
    runtimes: [{
      id: 'rt_orphan_test',
      name: 'Orphan Test Runtime',
      executable: fakeRt,
      working_directory: fakeRtDir,
      environment: {},
      created_at: now,
      updated_at: now,
    }],
    models: [{
      id: 'model_orphan_test',
      name: 'Orphan Test Model',
      runtime_id: 'rt_orphan_test',
      args: ['infinite'],
      environment: {},
      active: false,
      autostart_delay: 0,
      created_at: now,
      updated_at: now,
    }],
    instances: [{
      id: 'inst_orphan_test',
      model_id: 'model_orphan_test',
      runtime_id: 'rt_orphan_test',
      executable: fakeRt,
      args: ['infinite'],
      working_directory: fakeRtDir,
      environment: {},
      state: 'running',
      pid: helperPid,
      exit_code: 0,
      exit_class: '',
      last_error: '',
      started_at: now,
      created_at: now,
      updated_at: now,
    }],
  };
  fs.writeFileSync(path.join(ws.dataDir, 'goal_repo.json'), JSON.stringify(repo, null, 2));
  console.log('Data store populated');

  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    H.killTree(helperPid);
    await server.stop();
    process.exit(1);
  }
  await new Promise(r => setTimeout(r, 1000));
  console.log('Server started (recovery should have detected the orphan).\n');

  const suite = H.newSuite('ORPHAN (recovery, RU/EN, Dismiss)');
  const browser = await H.launchBrowser();
  let ok = false;
  try {
    // Desktop RU
    const ctxRU = await browser.newContext({ viewport: { width: 1280, height: 800 }, locale: 'ru-RU' });
    const pageRU = await ctxRU.newPage();
    suite.watchPage(pageRU);

    await H.login(pageRU, BASE, ADMIN_USER, ADMIN_PASS);

    const instData = await H.pageApi(pageRU, 'GET', '/api/v1/instances');
    const orphanInst = (instData.data || []).find(i => i.id === 'inst_orphan_test');
    suite.log('API: state=orphan', orphanInst && orphanInst.state === 'orphan',
      orphanInst ? `state=${orphanInst.state} pid=${orphanInst.pid}` : 'not found');

    await pageRU.evaluate(() => window.navigate('adv-instances'));
    await pageRU.waitForTimeout(1000);

    const orphanBadge = pageRU.locator('#instances-table-wrap .status-badge.orphan').first();
    const orphanVisRU = await orphanBadge.isVisible().catch(() => false);
    const orphanTextRU = orphanVisRU ? (await orphanBadge.textContent()).trim() : '';
    suite.log('UI: orphan badge (RU)', orphanVisRU, `text="${orphanTextRU}"`);
    if (orphanVisRU) {
      const tip = await orphanBadge.getAttribute('title');
      suite.log('UI: tooltip/hint (RU)', tip !== null && tip.includes('вне GoAl'), `title="${tip}"`);
    }

    const dismissRU = pageRU.locator('#instances-table-wrap button:has-text("Отклонить")').first();
    suite.log('UI: Dismiss button (RU)', await dismissRU.isVisible().catch(() => false));
    const killRU = pageRU.locator('#instances-table-wrap button:has-text("Завершить")').first();
    suite.log('UI: Kill button (RU)', await killRU.isVisible().catch(() => false));
    await H.screenshot(pageRU, ws, 'orphan_ru_desktop');

    // Models page: the orphan model must be visible as ORPHAN, not "stopped" with a Start button
    await pageRU.evaluate(() => window.navigate('models'));
    await pageRU.waitForTimeout(500);
    const mBadge = pageRU.locator('#model-list .status-badge.orphan').first();
    const mBadgeVis = await mBadge.isVisible().catch(() => false);
    suite.log('UI: Models page orphan badge (RU)', mBadgeVis,
      mBadgeVis ? `text="${(await mBadge.textContent()).trim()}"` : 'not visible');
    const mStartBtns = await pageRU.locator('#model-list .model-row button[aria-label="Запустить"]').count();
    suite.log('UI: Models page no Start button for orphan model (RU)', mStartBtns === 0, `start buttons=${mStartBtns}`);
    const mRowSub = (await pageRU.locator('#model-list .model-row .model-row-sub').first().textContent().catch(() => '')) || '';
    suite.log('UI: Models page shows orphan PID (RU)', mRowSub.includes(String(helperPid)), mRowSub.trim());
    await H.screenshot(pageRU, ws, 'orphan_models_ru');

    await pageRU.evaluate(() => { const s = document.getElementById('mf-state'); s.value = 'orphan'; s.dispatchEvent(new Event('change')); });
    await pageRU.waitForTimeout(300);
    const mRowsOrphan = await pageRU.locator('#model-list .model-row').count();
    suite.log('UI: state filter=orphan shows the row (RU)', mRowsOrphan === 1, `rows=${mRowsOrphan}`);
    await pageRU.evaluate(() => { const s = document.getElementById('mf-state'); s.value = 'stopped'; s.dispatchEvent(new Event('change')); });
    await pageRU.waitForTimeout(300);
    const mRowsStopped = await pageRU.locator('#model-list .model-row').count();
    suite.log('UI: state filter=stopped hides the orphan row (RU)', mRowsStopped === 0, `rows=${mRowsStopped}`);
    await pageRU.evaluate(() => { const s = document.getElementById('mf-state'); s.value = ''; s.dispatchEvent(new Event('change')); });
    await pageRU.waitForTimeout(300);
    await pageRU.evaluate(() => window.navigate('adv-instances'));
    await pageRU.waitForTimeout(300);

    // Switch to EN; dynamic lists re-render in the new language on the 3 s poll tick
    await pageRU.evaluate(() => window.setLanguage('en'));
    await pageRU.waitForTimeout(3500);

    const orphanBadgeEn = pageRU.locator('#instances-table-wrap .status-badge.orphan').first();
    const orphanVisEN = await orphanBadgeEn.isVisible().catch(() => false);
    const orphanTextEN = orphanVisEN ? (await orphanBadgeEn.textContent()).trim() : '';
    suite.log('UI: orphan badge (EN)', orphanVisEN, `text="${orphanTextEN}"`);
    if (orphanVisEN) {
      const tipEn = await orphanBadgeEn.getAttribute('title');
      suite.log('UI: tooltip/hint (EN)', tipEn !== null && tipEn.includes('outside GoAl'), `title="${tipEn}"`);
    }

    const dismissEN = pageRU.locator('#instances-table-wrap button:has-text("Dismiss")').first();
    suite.log('UI: Dismiss button (EN)', await dismissEN.isVisible().catch(() => false));
    const killEN = pageRU.locator('#instances-table-wrap button:has-text("Kill")').first();
    suite.log('UI: Kill button (EN)', await killEN.isVisible().catch(() => false));
    await H.screenshot(pageRU, ws, 'orphan_en_desktop');

    await pageRU.evaluate(() => window.navigate('models'));
    await pageRU.waitForTimeout(3500);
    const mBadgeEn = pageRU.locator('#model-list .status-badge.orphan').first();
    const mBadgeEnVis = await mBadgeEn.isVisible().catch(() => false);
    suite.log('UI: Models page orphan badge (EN)', mBadgeEnVis,
      mBadgeEnVis ? `text="${(await mBadgeEn.textContent()).trim()}"` : 'not visible');
    const mStartBtnsEn = await pageRU.locator('#model-list .model-row button[aria-label="Start"]').count();
    suite.log('UI: Models page no Start button for orphan model (EN)', mStartBtnsEn === 0, `start buttons=${mStartBtnsEn}`);
    await H.screenshot(pageRU, ws, 'orphan_models_en');

    // Mobile viewport (RU)
    const ctxM = await browser.newContext({ viewport: { width: 375, height: 667 }, locale: 'ru-RU' });
    const pageM = await ctxM.newPage();
    suite.watchPage(pageM);
    await H.login(pageM, BASE, ADMIN_USER, ADMIN_PASS);
    await pageM.evaluate(() => window.navigate('adv-instances'));
    await pageM.waitForTimeout(1000);
    const mOrphan = pageM.locator('#instances-compact .status-badge.orphan').first();
    suite.log('UI: orphan badge (mobile RU)', await mOrphan.isVisible().catch(() => false));
    const mDismiss = pageM.locator('#instances-compact button[aria-label="Отклонить"]').first();
    suite.log('UI: Dismiss button (mobile RU)', await mDismiss.isVisible().catch(() => false));
    const mKill = pageM.locator('#instances-compact button[aria-label="Завершить"]').first();
    suite.log('UI: Kill button (mobile RU)', await mKill.isVisible().catch(() => false));
    await H.screenshot(pageM, ws, 'orphan_ru_mobile');
    await ctxM.close();

    // Click Dismiss
    await pageRU.evaluate(() => window.navigate('adv-instances'));
    await pageRU.waitForTimeout(1000);
    const dBtn = pageRU.locator('#instances-table-wrap button:has-text("Dismiss")').first();
    const dVis = await dBtn.isVisible().catch(() => false);
    suite.log('Dismiss visible (pre-click)', dVis);
    if (dVis) {
      await dBtn.click();
      await pageRU.waitForTimeout(2000);
    }
    await pageRU.waitForTimeout(500);

    const orphanCount = await pageRU.locator('#instances-table-wrap .status-badge.orphan').count();
    suite.log('UI: orphan gone after Dismiss', orphanCount === 0, `orphan badges=${orphanCount}`);
    const totalRows = await pageRU.locator('#instances-table-wrap tbody tr').count();
    suite.log('UI: instance removed from table (stale hidden)', totalRows === 0, `rows=${totalRows}`);
    await H.screenshot(pageRU, ws, 'after_dismiss');

    const instAfter = await H.pageApi(pageRU, 'GET', '/api/v1/instances/inst_orphan_test');
    suite.log('Persisted: state=stale', instAfter.data && instAfter.data.state === 'stale', `state=${instAfter.data && instAfter.data.state}`);
    suite.log('Persisted: recovery_reason=reconciled-by-user', instAfter.data && instAfter.data.recovery_reason === 'reconciled-by-user',
      instAfter.data && instAfter.data.recovery_reason ? instAfter.data.recovery_reason : '(empty)');

    suite.log('Helper alive after Dismiss (no kill/signal)', H.isProcessAlive(helperPid), `pid=${helperPid}`);

    suite.log('Console: 0 uncaught exceptions', suite.consoleErrors.length === 0, suite.consoleErrors.slice(0, 2).join('; '));
    ok = await suite.finish();
  } catch (err) {
    suite.log('FATAL: ' + err.message, false, err.stack ? err.stack.split('\n')[0] : '');
    ok = false;
    await suite.finish();
  } finally {
    try { helper.kill(); } catch {}
    await browser.close();
    await server.stop();
    H.killTree(helperPid);
  }

  process.exit(ok ? 0 : 1);
}

main().catch(err => {
  console.error('HARNESS ERROR:', err);
  process.exit(2);
});

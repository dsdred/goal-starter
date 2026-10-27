'use strict';
const path = require('path');
const fs = require('fs');
const { spawn } = require('child_process');
const H = require('./harness.cjs');

const PORT = 19484;
const BASE = `http://127.0.0.1:${PORT}`;
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'testpass123';
const I18N = { ru: require('../../internal/webui/static/i18n/ru.json'), en: require('../../internal/webui/static/i18n/en.json') };

// modelStripInPage reads the ADR 022 facts off the first My Models row. The orphan
// branch keeps its hint sentence and must never render the transition marker, which
// replaces only the state-word branch of the renderer.
function modelStripInPage() {
  const row = document.querySelector('#model-list .model-row');
  if (!row) return null;
  const strip = row.querySelector('.model-row-actions');
  return {
    text: strip ? (strip.textContent || '').trim() : '(no strip)',
    transitional: !!row.querySelector('.model-transitional'),
    marker: !!row.querySelector('.model-progress'),
  };
}

// stateBadgeInPage reads the two ADR 023 facts of a rendered state badge: the
// dictionary-derived word and the computed casing. They are asserted together —
// the right word inside an uppercased badge is exactly the defect Q3 forbids, and
// the right casing over the wrong word is the same defect mirrored.
function stateBadgeInPage(selector) {
  const el = document.querySelector(selector);
  if (!el) return null;
  return {
    cls: el.className,
    text: (el.textContent || '').trim(),
    transform: getComputedStyle(el).textTransform,
    title: el.getAttribute('title'),
  };
}

// injectedCasingInPage resolves one casing selector against the real DOM of the
// current document: a badge carrying the renderer's own classes is appended
// inside the container and removed again in the same synchronous call. It covers
// the `pending` half of D-3, which the app cannot render on demand (the state is
// sub-frame — MA-06 evidence), and doubles as the negative control that the
// exemption is container-scoped: the same class injected into a non-exempt
// container must still compute `uppercase`.
function injectedCasingInPage(a) {
  const host = document.querySelector(a.container);
  if (!host) return null;
  const badge = document.createElement('span');
  badge.className = 'status-badge ' + a.badgeClass;
  badge.textContent = a.text;
  host.appendChild(badge);
  let out;
  try {
    out = { container: a.container, cls: badge.className, text: (badge.textContent || '').trim(),
      transform: getComputedStyle(badge).textTransform };
  } finally {
    badge.remove();
  }
  return out;
}

// filterOptionInPage reads one Models-filter option: the internal value (frozen
// technical token, D-1) and the displayed label (renamed word, D-2).
function filterOptionInPage(value) {
  const opt = document.querySelector('#mf-state option[value="' + value + '"]');
  if (!opt) return null;
  return { value: opt.getAttribute('value'), label: (opt.textContent || '').trim() };
}

// instanceOptionInPage reads the Logs selector entry for one instance. That
// surface is a hint sentence, not a state label, and ADR 023 D-9 freezes it.
function instanceOptionInPage(instanceId) {
  const opt = document.querySelector('#log-instance-select option[value="' + instanceId + '"]');
  if (!opt) return null;
  return { value: opt.getAttribute('value'), label: (opt.textContent || '').trim() };
}

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

  // Pre-populate data store with runtime + model + running instance. The
  // instance is also attributed to a pipeline entry, so recovery's orphan is
  // visible on every state-word surface ADR 023 D-3 scopes, including the
  // Pipeline card (aggregate badge in .model-row-name, chip in .pl-chips-wrap) —
  // the only deterministic route to a rendered .orphan chip in the maintained
  // corpus (a live pipeline start never parks an instance in `orphan`).
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
    pipelines: [{
      id: 'pipe_orphan_test',
      name: 'Orphan Test Pipeline',
      active: false,
      models: [{ id: 'pme_orphan_test', model_id: 'model_orphan_test', auto_start: false }],
      created_at: now,
      updated_at: now,
    }],
    instances: [{
      id: 'inst_orphan_test',
      model_id: 'model_orphan_test',
      runtime_id: 'rt_orphan_test',
      pipeline_id: 'pipe_orphan_test',
      pipeline_entry_id: 'pme_orphan_test',
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

    const dismissRU = pageRU.locator('#instances-table-wrap button[aria-label="Отклонить"]').first();
    suite.log('UI: Dismiss button (RU)', await dismissRU.isVisible().catch(() => false));
    const killRU = pageRU.locator('#instances-table-wrap button[aria-label="Завершить"]').first();
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
    const stripRU = await pageRU.evaluate(modelStripInPage);
    suite.log('UI: Models page orphan hint preserved, no transition marker (RU)',
      !!stripRU && stripRU.transitional === true && stripRU.marker === false && stripRU.text === I18N.ru['instances.orphan.hint'],
      stripRU ? `transitional=${stripRU.transitional} marker=${stripRU.marker} strip_text=${JSON.stringify(stripRU.text)} want=${JSON.stringify(I18N.ru['instances.orphan.hint'])}` : 'model row absent');
    await H.screenshot(pageRU, ws, 'orphan_models_ru');

    // ═══ ADR 023 T-4/T-5/T-8/T-9 — renamed vocabulary and casing, per surface (RU) ═══
    // Every expected string is read from the shipped dictionary, never hardcoded:
    // the rename stays test-transparent (T-3) and a wrong-but-consistent literal
    // cannot be baked into both sides of the comparison.
    const ruOrphanWord = I18N.ru['models.status.orphan'];
    const ruPendingWord = I18N.ru['models.status.pending'];
    // T-2 pins the shipped literals themselves. Every leg below compares a
    // rendered element against the dictionary, so without this pin reverting
    // ru.json/en.json to «Сирота»/`ORPHAN` would move both sides of every
    // comparison at once and the suite would stay green.
    suite.log('T-2 Dictionaries hold the four ratified literals byte-for-byte (RU+EN)',
      ruOrphanWord === 'Вне GoAl' && ruPendingWord === 'Ожидание' &&
        I18N.en['models.status.orphan'] === 'UNMANAGED' && I18N.en['models.status.pending'] === 'PENDING',
      JSON.stringify({ ru: [ruOrphanWord, ruPendingWord], en: [I18N.en['models.status.orphan'], I18N.en['models.status.pending']] }));
    suite.log('T-2 The superseded labels appear in no value of either dictionary (no half-rename)',
      ![I18N.ru, I18N.en].some(d => Object.values(d).some(v =>
        typeof v === 'string' && (v === 'Сирота' || v === 'ORPHAN' || v === 'Ожидание...' || v === 'Waiting...'))));
    const instBadgeRU = await pageRU.evaluate(stateBadgeInPage, '#adv-instances-body .status-badge.orphan');
    suite.log('T-4 RU Instances table: badge text == dictionary value and casing exemption applied',
      !!instBadgeRU && instBadgeRU.text === ruOrphanWord && instBadgeRU.transform === 'none',
      instBadgeRU ? `text=${JSON.stringify(instBadgeRU.text)} want=${JSON.stringify(ruOrphanWord)} transform=${instBadgeRU.transform}` : 'badge absent');
    suite.log('T-9/D-9 RU Instances badge title stays the byte-identical orphan hint',
      !!instBadgeRU && instBadgeRU.title === I18N.ru['instances.orphan.hint'],
      instBadgeRU ? `title=${JSON.stringify(instBadgeRU.title)}` : 'badge absent');
    // The `pending` half of the exemption cannot be rendered on demand, so its
    // selector is resolved against the real container instead of a fixture, and
    // the same class injected outside the exempt container must stay uppercase.
    const instPendingRU = await pageRU.evaluate(injectedCasingInPage,
      { container: '#adv-instances-body', badgeClass: 'pending', text: ruPendingWord });
    suite.log('T-4/D-3 RU Instances table: injected pending badge resolves text-transform: none',
      !!instPendingRU && instPendingRU.transform === 'none',
      instPendingRU ? `transform=${instPendingRU.transform}` : 'container absent');
    const instWrapControlRU = await pageRU.evaluate(injectedCasingInPage,
      { container: '#instances-table-wrap', badgeClass: 'orphan', text: ruOrphanWord });
    suite.log('T-4/I-2 RU Non-exempt wrapper: the same orphan class stays uppercase (no global override)',
      !!instWrapControlRU && instWrapControlRU.transform === 'uppercase',
      instWrapControlRU ? `transform=${instWrapControlRU.transform}` : 'wrapper absent');
    const modelBadgeRU = await pageRU.evaluate(stateBadgeInPage, '#model-list .status-badge.orphan');
    suite.log('T-4 RU Models: badge text == dictionary value and casing exemption applied',
      !!modelBadgeRU && modelBadgeRU.text === ruOrphanWord && modelBadgeRU.transform === 'none',
      modelBadgeRU ? `text=${JSON.stringify(modelBadgeRU.text)} transform=${modelBadgeRU.transform}` : 'badge absent');
    const modelPendingRU = await pageRU.evaluate(injectedCasingInPage,
      { container: '#model-list .model-row .model-row-name', badgeClass: 'pending', text: ruPendingWord });
    suite.log('T-4/D-3 RU Models row: injected pending badge resolves text-transform: none',
      !!modelPendingRU && modelPendingRU.transform === 'none',
      modelPendingRU ? `transform=${modelPendingRU.transform}` : 'models row name absent');
    const ruFilterOrphan = await pageRU.evaluate(filterOptionInPage, 'orphan');
    const ruFilterPending = await pageRU.evaluate(filterOptionInPage, 'pending');
    suite.log('T-8 RU filter: values keep the technical tokens while labels carry the new words',
      !!ruFilterOrphan && ruFilterOrphan.value === 'orphan' && ruFilterOrphan.label === ruOrphanWord &&
        !!ruFilterPending && ruFilterPending.value === 'pending' && ruFilterPending.label === ruPendingWord,
      JSON.stringify({ orphan: ruFilterOrphan, pending: ruFilterPending }));
    await pageRU.evaluate(() => window.navigate('pipelines'));
    await pageRU.waitForTimeout(600);
    const pipeAggRU = await pageRU.evaluate(stateBadgeInPage, '#pipeline-list .model-row-name .status-badge.orphan');
    suite.log('T-5 RU Pipeline aggregate: new vocabulary word with the casing exemption',
      !!pipeAggRU && pipeAggRU.text === ruOrphanWord && pipeAggRU.transform === 'none',
      pipeAggRU ? `class=${JSON.stringify(pipeAggRU.cls)} text=${JSON.stringify(pipeAggRU.text)} transform=${pipeAggRU.transform}` : 'aggregate badge absent');
    const pipeChipRU = await pageRU.evaluate(stateBadgeInPage, '#pipeline-list .pl-chips-wrap .status-badge.orphan');
    suite.log('T-5 RU Pipeline chip keeps the model name uppercased — excluded from D-3 by container, no restore rule',
      !!pipeChipRU && pipeChipRU.text === 'Orphan Test Model' && pipeChipRU.transform === 'uppercase',
      pipeChipRU ? `class=${JSON.stringify(pipeChipRU.cls)} text=${JSON.stringify(pipeChipRU.text)} transform=${pipeChipRU.transform}` : 'chip absent');
    const pipePendingRU = await pageRU.evaluate(injectedCasingInPage,
      { container: '#pipeline-list .model-row-name', badgeClass: 'pending', text: ruPendingWord });
    suite.log('T-5/D-3 RU Pipeline aggregate: injected pending badge resolves text-transform: none',
      !!pipePendingRU && pipePendingRU.transform === 'none',
      pipePendingRU ? `transform=${pipePendingRU.transform}` : 'pipeline aggregate name absent');
    const pipeChipControlRU = await pageRU.evaluate(injectedCasingInPage,
      { container: '#pipeline-list .pl-chips-wrap', badgeClass: 'orphan', text: ruOrphanWord });
    suite.log('T-5/D-4 RU Chip container is outside the exemption by construction: an orphan-class chip computes uppercase',
      !!pipeChipControlRU && pipeChipControlRU.transform === 'uppercase',
      pipeChipControlRU ? `transform=${pipeChipControlRU.transform}` : 'chip container absent');
    await H.screenshot(pageRU, ws, 'orphan_pipelines_ru');
    await pageRU.evaluate(() => window.navigate('logs'));
    await pageRU.waitForTimeout(600);
    const ruLogOption = await pageRU.evaluate(instanceOptionInPage, 'inst_orphan_test');
    suite.log('T-9 RU Logs selector keeps the frozen history.state.orphan sentence (D-9)',
      !!ruLogOption && ruLogOption.label.includes(I18N.ru['history.state.orphan']),
      ruLogOption ? `label=${JSON.stringify(ruLogOption.label)}` : 'option absent');
    await pageRU.evaluate(() => window.navigate('adv-instances'));
    await pageRU.waitForTimeout(300);

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

    const dismissEN = pageRU.locator('#instances-table-wrap button[aria-label="Dismiss"]').first();
    suite.log('UI: Dismiss button (EN)', await dismissEN.isVisible().catch(() => false));
    const killEN = pageRU.locator('#instances-table-wrap button[aria-label="Kill"]').first();
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
    const stripEN = await pageRU.evaluate(modelStripInPage);
    suite.log('UI: Models page orphan hint preserved, no transition marker (EN)',
      !!stripEN && stripEN.transitional === true && stripEN.marker === false && stripEN.text === I18N.en['instances.orphan.hint'],
      stripEN ? `transitional=${stripEN.transitional} marker=${stripEN.marker} strip_text=${JSON.stringify(stripEN.text)} want=${JSON.stringify(I18N.en['instances.orphan.hint'])}` : 'model row absent');
    await H.screenshot(pageRU, ws, 'orphan_models_en');

    // ═══ ADR 023 T-4/T-5/T-8/T-9 — same contract in EN (D-11: RU and EN move together) ═══
    const enOrphanWord = I18N.en['models.status.orphan'];
    const enPendingWord = I18N.en['models.status.pending'];
    const instBadgeEN = await pageRU.evaluate(stateBadgeInPage, '#adv-instances-body .status-badge.orphan');
    suite.log('T-4 EN Instances table: badge text == dictionary value and casing exemption applied',
      !!instBadgeEN && instBadgeEN.text === enOrphanWord && instBadgeEN.transform === 'none',
      instBadgeEN ? `text=${JSON.stringify(instBadgeEN.text)} want=${JSON.stringify(enOrphanWord)} transform=${instBadgeEN.transform}` : 'badge absent');
    const modelBadgeEN = await pageRU.evaluate(stateBadgeInPage, '#model-list .status-badge.orphan');
    suite.log('T-4 EN Models: badge text == dictionary value and casing exemption applied',
      !!modelBadgeEN && modelBadgeEN.text === enOrphanWord && modelBadgeEN.transform === 'none',
      modelBadgeEN ? `text=${JSON.stringify(modelBadgeEN.text)} transform=${modelBadgeEN.transform}` : 'badge absent');
    const enFilterOrphan = await pageRU.evaluate(filterOptionInPage, 'orphan');
    const enFilterPending = await pageRU.evaluate(filterOptionInPage, 'pending');
    suite.log('T-8 EN filter: values keep the technical tokens while labels carry the new words',
      !!enFilterOrphan && enFilterOrphan.value === 'orphan' && enFilterOrphan.label === enOrphanWord &&
        !!enFilterPending && enFilterPending.value === 'pending' && enFilterPending.label === enPendingWord,
      JSON.stringify({ orphan: enFilterOrphan, pending: enFilterPending }));
    await pageRU.evaluate(() => window.navigate('pipelines'));
    await pageRU.waitForTimeout(600);
    const pipeAggEN = await pageRU.evaluate(stateBadgeInPage, '#pipeline-list .model-row-name .status-badge.orphan');
    suite.log('T-5 EN Pipeline aggregate: new vocabulary word with the casing exemption',
      !!pipeAggEN && pipeAggEN.text === enOrphanWord && pipeAggEN.transform === 'none',
      pipeAggEN ? `text=${JSON.stringify(pipeAggEN.text)} transform=${pipeAggEN.transform}` : 'aggregate badge absent');
    const pipeChipEN = await pageRU.evaluate(stateBadgeInPage, '#pipeline-list .pl-chips-wrap .status-badge.orphan');
    suite.log('T-5 EN Pipeline chip keeps the model name uppercased — excluded from D-3 by container',
      !!pipeChipEN && pipeChipEN.text === 'Orphan Test Model' && pipeChipEN.transform === 'uppercase',
      pipeChipEN ? `text=${JSON.stringify(pipeChipEN.text)} transform=${pipeChipEN.transform}` : 'chip absent');
    await pageRU.evaluate(() => window.navigate('logs'));
    await pageRU.waitForTimeout(600);
    const enLogOption = await pageRU.evaluate(instanceOptionInPage, 'inst_orphan_test');
    suite.log('T-9 EN Logs selector keeps the frozen history.state.orphan sentence (D-9)',
      !!enLogOption && enLogOption.label.includes(I18N.en['history.state.orphan']),
      enLogOption ? `label=${JSON.stringify(enLogOption.label)}` : 'option absent');
    await pageRU.evaluate(() => window.navigate('adv-instances'));
    await pageRU.waitForTimeout(300);

    // Mobile viewport (RU)
    const ctxM = await browser.newContext({ viewport: { width: 375, height: 667 }, locale: 'ru-RU' });
    const pageM = await ctxM.newPage();
    suite.watchPage(pageM);
    await H.login(pageM, BASE, ADMIN_USER, ADMIN_PASS);
    await pageM.evaluate(() => window.navigate('adv-instances'));
    await pageM.waitForTimeout(1000);
    const mOrphan = pageM.locator('#instances-compact .status-badge.orphan').first();
    suite.log('UI: orphan badge (mobile RU)', await mOrphan.isVisible().catch(() => false));
    const compactBadgeRU = await pageM.evaluate(stateBadgeInPage, '#instances-compact .status-badge.orphan');
    suite.log('T-4 RU Instances compact: badge text == dictionary value and casing exemption applied',
      !!compactBadgeRU && compactBadgeRU.text === I18N.ru['models.status.orphan'] && compactBadgeRU.transform === 'none',
      compactBadgeRU ? `text=${JSON.stringify(compactBadgeRU.text)} transform=${compactBadgeRU.transform}` : 'badge absent');
    const mDismiss = pageM.locator('#instances-compact button[aria-label="Отклонить"]').first();
    suite.log('UI: Dismiss button (mobile RU)', await mDismiss.isVisible().catch(() => false));
    const mKill = pageM.locator('#instances-compact button[aria-label="Завершить"]').first();
    suite.log('UI: Kill button (mobile RU)', await mKill.isVisible().catch(() => false));
    const compactPendingRU = await pageM.evaluate(injectedCasingInPage,
      { container: '#instances-compact', badgeClass: 'pending', text: I18N.ru['models.status.pending'] });
    suite.log('T-4/D-3 RU Instances compact: injected pending badge resolves text-transform: none',
      !!compactPendingRU && compactPendingRU.transform === 'none',
      compactPendingRU ? `transform=${compactPendingRU.transform}` : 'compact container absent');
    await H.screenshot(pageM, ws, 'orphan_ru_mobile');

    // Same mobile surface in EN (D-11: RU and EN move together; the compact card is
    // a state-word surface named by ADR 023 §T-4 coverage).
    await pageM.evaluate(() => window.setLanguage('en'));
    await pageM.waitForTimeout(3500);
    const compactBadgeEN = await pageM.evaluate(stateBadgeInPage, '#instances-compact .status-badge.orphan');
    suite.log('T-4 EN Instances compact: badge text == dictionary value and casing exemption applied',
      !!compactBadgeEN && compactBadgeEN.text === I18N.en['models.status.orphan'] && compactBadgeEN.transform === 'none',
      compactBadgeEN ? `text=${JSON.stringify(compactBadgeEN.text)} transform=${compactBadgeEN.transform}` : 'badge absent');
    const compactPendingEN = await pageM.evaluate(injectedCasingInPage,
      { container: '#instances-compact', badgeClass: 'pending', text: I18N.en['models.status.pending'] });
    suite.log('T-4/D-3 EN Instances compact: injected pending badge resolves text-transform: none',
      !!compactPendingEN && compactPendingEN.transform === 'none',
      compactPendingEN ? `transform=${compactPendingEN.transform}` : 'compact container absent');
    await H.screenshot(pageM, ws, 'orphan_en_mobile');
    await ctxM.close();

    // Click Dismiss
    await pageRU.evaluate(() => window.navigate('adv-instances'));
    await pageRU.waitForTimeout(1000);
    const dBtn = pageRU.locator('#instances-table-wrap button[aria-label="Dismiss"]').first();
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

    // The dismissed instance is terminal (`stale`) and lands in History. That
    // surface is outside D-3's selector list, so the same badge class that is
    // exempted on a state-word surface must still be uppercased here — the
    // negative control that keeps the exemption from being a global override.
    await pageRU.evaluate(() => window.navigate('history'));
    await pageRU.waitForTimeout(800);
    const histBadgeEN = await pageRU.evaluate(stateBadgeInPage, '#history-body .status-badge.stale');
    suite.log('T-4/T-9 EN History badge (non-exempt surface) keeps text-transform: uppercase',
      !!histBadgeEN && histBadgeEN.text === I18N.en['history.state.stale'] && histBadgeEN.transform === 'uppercase',
      histBadgeEN ? `text=${JSON.stringify(histBadgeEN.text)} transform=${histBadgeEN.transform}` : 'history badge absent');

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

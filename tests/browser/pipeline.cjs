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

  const pipelineInstances = async () => (await api('GET', '/api/v1/instances')).data.filter(i => i.pipeline_id);
  const runningFor = async (pipeId) => (await pipelineInstances()).filter(i => i.pipeline_id === pipeId && i.state === 'running');

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('PIPELINE (linear builder UI + lifecycle + responsive + i18n)');
  suite.watchPage(page);

  // The pipeline list uses the Models-style inline icon strip (no overflow
  // "…" menu), so an action is a direct icon button whose onclick names the
  // handler (startPipeline / stopPipeline / restartPipeline / editPipeline /
  // deletePipeline). Both the desktop table row and the mobile card render a
  // copy of the strip; at a given viewport exactly one is rendered (the other is
  // display:none), so filter to the visible icon button and click it directly.
  const actionClick = async (scopeSel, onclickSubstr) => {
    const btn = page.locator(scopeSel + ' .icon-btn[onclick*="' + onclickSubstr + '"]').filter({ visible: true }).first();
    await btn.click({ timeout: 3000 });
    await page.waitForTimeout(150);
  };

  // The segmented args control hides its radio inputs (the label span is the
  // clickable surface), so select an option by clicking its label.
  const segClick = (scope, value) => scope.locator('.pl-seg-item:has(input[value="' + value + '"])').first().click();

  // Regression guard for the linear builder: no decorative connectors, one block
  // per entry, bounded block width, and no generated JavaScript source leaked
  // into the visible DOM (a class of prior Owner defects).
  const builderGuard = async (expectedBlocks) => page.evaluate((expected) => {
    const container = document.querySelector('#pl-models-container');
    const connectors = Array.from(container ? container.querySelectorAll('.pl-connector') : []);
    const blocks = Array.from(container ? container.querySelectorAll('.pl-block') : []);
    const rect = (el) => { const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; };
    const issues = [];
    if (connectors.length !== 0) issues.push('connectors=' + connectors.length);
    if (blocks.length !== expected) issues.push('blocks=' + blocks.length);
    blocks.forEach((b, i) => {
      const r = rect(b);
      if (r.width < 100 || r.width > 1200) issues.push(`b${i}.width=${r.width}`);
    });
    const visibleText = ((container && container.innerText) || '') + '\n' + (document.body ? document.body.innerText : '');
    const leaked = ['html +=', 'html+=', 'function renderPlBuilder', '<div class="pl-connector"']
      .filter((s) => visibleText.includes(s));
    if (leaked.length) issues.push('leaked=' + leaked.join(','));
    return { ok: issues.length === 0, issues, connectors: connectors.length, blocks: blocks.length };
  }, expectedBlocks);

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
    suite.log('1.2 List empty when no pipelines', (await page.locator('#pipeline-list .model-row').count()) === 0);
    suite.log('1.3 Empty-state description line present', await page.locator('#pipelines-empty .empty-state-desc').isVisible());
    await H.screenshot(page, ws, '01-pipelines-empty');

    // ═══ SECTION 2: Create pipeline via the linear builder ═══
    await page.click('#view-pipelines .view-header .btn-primary');
    await page.waitForTimeout(400);
    suite.log('2.1 Create modal opens', await page.locator('#pipeline-modal').evaluate(el => getComputedStyle(el).display === 'flex'));
    const block0 = page.locator('#pl-models-container .pl-block').nth(0);
    suite.log('2.2 One default block present', (await page.locator('#pl-models-container .pl-block').count()) === 1);
    const guard1 = await builderGuard(1);
    suite.log('2.2b No connectors + bounded blocks + no leaked source (1 block)', guard1.ok, `connectors=${guard1.connectors} issues=${guard1.issues.join(',') || 'none'}`);
    // (linear builder) each block shows an order number and is a labeled listitem.
    const stepText = ((await block0.locator('.pl-step').first().textContent() || '').trim());
    suite.log('2.3 Block shows order number "1"', stepText === '1', `step=${JSON.stringify(stepText)}`);
    const blockRole = (await block0.getAttribute('role')) || '';
    const blockLabel = (await block0.getAttribute('aria-label')) || '';
    suite.log('2.4 Block is an accessible labeled listitem', blockRole === 'listitem' && blockLabel.length > 0, `role=${blockRole} label=${JSON.stringify(blockLabel)}`);
    // (args UX) compact segmented control [From model | Custom]; From model is
    // the default and hides the textarea.
    const segRadios = await block0.locator('.pl-args-radio').count();
    const segFromModelChecked = await block0.locator('.pl-args-radio[value="model"]').isChecked();
    suite.log('2.5 Segmented args control with 2 options, "From model" default', segRadios === 2 && segFromModelChecked, `radios=${segRadios} fromChecked=${segFromModelChecked}`);
    suite.log('2.6 "From model" hides the custom args textarea', (await block0.locator('.pl-args-input').count()) === 0);
    // (AutoStart reconciliation) the per-block "Model autostart" control is gone:
    // an Active pipeline launches every entry, so there is no per-entry autostart
    // toggle in the builder anymore.
    const blockAutoToggles = await page.locator('#pl-models-container .pl-autostart').count();
    const blockAutoLabels = await page.locator('#pl-models-container .pl-autostart-label').count();
    suite.log('2.7 No per-block autostart control in the builder', blockAutoToggles === 0 && blockAutoLabels === 0, `toggles=${blockAutoToggles} labels=${blockAutoLabels}`);
    // The pipeline-level Active toggle remains (the single autostart setting).
    const pipeAutoDom = ((await page.locator('#pipeline-form span[data-i18n="pipelines.field.active"]').first().textContent() || '').trim());
    suite.log('2.8 Pipeline-level Active toggle present in the form head', pipeAutoDom !== '', `pipe=${JSON.stringify(pipeAutoDom)}`);
    // (reorder) chevron up/down icon buttons with tooltips/aria-labels; with a
    // single block BOTH are disabled (boundary).
    const moveBtns = await block0.locator('.pl-act-up, .pl-act-down').count();
    const moveLabeled = await block0.locator('.pl-act-up, .pl-act-down').evaluateAll(els => els.every(e => (e.getAttribute('aria-label') || '').length > 0 && (e.getAttribute('title') || '').length > 0));
    const upDisabled = await block0.locator('.pl-act-up').isDisabled();
    const downDisabled = await block0.locator('.pl-act-down').isDisabled();
    suite.log('2.10 Reorder up/down present + accessible', moveBtns === 2 && moveLabeled, `btns=${moveBtns} labeled=${moveLabeled}`);
    suite.log('2.11 Single block: up AND down disabled (boundary)', upDisabled && downDisabled, `up=${upDisabled} down=${downDisabled}`);
    // (remove) destructive icon button, accessible.
    const removeLbl = (await block0.locator('.pl-act-remove').getAttribute('aria-label')) || '';
    suite.log('2.12 Remove-block button is accessible (labeled)', removeLbl.length > 0, `label=${JSON.stringify(removeLbl)}`);
    // (drag) the drag handle is present, draggable, and accessible.
    const dragEl = block0.locator('.pl-drag');
    const dragCount = await dragEl.count();
    const dragAttr = dragCount > 0 ? (await dragEl.first().getAttribute('draggable')) : '';
    const dragLbl = dragCount > 0 ? ((await dragEl.first().getAttribute('aria-label')) || '') : '';
    suite.log('2.9 Drag handle present, draggable, and labeled', dragCount === 1 && dragAttr === 'true' && dragLbl.length > 0, `count=${dragCount} draggable=${dragAttr} label=${JSON.stringify(dragLbl)}`);
    // (add) continuation control at the end.
    const addText = ((await page.locator('#pl-models-container .pl-add').first().textContent() || '').trim());
    const addKey = await page.evaluate(() => t('pipelines.btn.add_model'));
    suite.log('2.13 Add-model continuation control present', addText === addKey && addText !== '', `text=${JSON.stringify(addText)}`);

    // ── Build: block 1 = model A, Custom args ──
    await page.fill('#pl-name', 'Cluster A');
    await block0.locator('.pl-model-select').selectOption(idA);
    await segClick(block0, 'custom');
    await page.waitForTimeout(150);
    suite.log('2.14 Selecting "Custom" reveals the args textarea', await block0.locator('.pl-args-input').isVisible());
    // The custom value must fully replace the base args; use a valid long-running
    // fake-runtime mode ("infinite <tag>") so the instance stays up.
    await block0.locator('.pl-args-input').fill('infinite override-A');
    // ── Add block 2 = model B, From model ──
    await page.click('#pl-models-container .pl-add');
    await page.waitForTimeout(200);
    const block1 = page.locator('#pl-models-container .pl-block').nth(1);
    suite.log('2.15 Add adds a second block', (await page.locator('#pl-models-container .pl-block').count()) === 2);
    // (reorder presentation) explicit up/down/remove controls; no connectors.
    const connectorCount = await page.locator('#pl-models-container .pl-connector').count();
    suite.log('2.16 No decorative connectors between blocks (0)', connectorCount === 0, `connectors=${connectorCount}`);
    const guard2 = await builderGuard(2);
    suite.log('2.16b No connectors + bounded blocks + no leaked source (2 blocks)', guard2.ok, `connectors=${guard2.connectors} issues=${guard2.issues.join(',') || 'none'}`);
    // (reorder boundary now) block 1 up enabled, block 1 down disabled; block 2
    // up enabled, block 2 down disabled (last).
    const b1Up = await block0.locator('.pl-act-up').isDisabled();
    const b2Down = await block1.locator('.pl-act-down').isDisabled();
    suite.log('2.17 Reorder boundary: first block up disabled, last block down disabled', b1Up && b2Down, `b1up=${b1Up} b2down=${b2Down}`);
    await block1.locator('.pl-model-select').selectOption(idB);

    await page.click('#pipeline-submit-btn');
    const list0 = (await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      const d = Array.isArray(r.data) ? r.data : [];
      return d.length === 1 ? r : null;
    }, 8000)) || { data: [] };
    pipeId = list0.data && list0.data[0] && list0.data[0].id;
    suite.log('2.18 Pipeline created (persisted)', !!pipeId, `id=${pipeId}`);
    await poll(async () => (await page.locator('#pipeline-list .model-row').count()) === 1, 8000);
    suite.log('2.19 List shows one card', (await page.locator('#pipeline-list .model-row').count()) === 1);
    suite.log('2.20 Empty state hidden', !(await page.locator('#pipelines-empty').isVisible()));
    await H.screenshot(page, ws, '02-pipeline-created');

    // ═══ SECTION 3: Created content (API contract + UI chips) ═══
    const p0 = list0.data[0];
    suite.log('3.1 Name persisted', p0.name === 'Cluster A', `name=${p0.name}`);
    suite.log('3.2 Two ordered models (A, B)', p0.models.length === 2 && p0.models[0].model_id === idA && p0.models[1].model_id === idB,
      `order=${JSON.stringify(p0.models.map(m => m.model_id))}`);
    const aArgs = (p0.models[0].args || []).join(' ');
    suite.log('3.3 Model A custom args persisted (all-or-nothing)', aArgs === 'infinite override-A', `args=${JSON.stringify(p0.models[0].args)}`);
    // (AutoStart reconciliation) the UI no longer sets per-entry auto_start, so on
    // create every entry defaults to false (a legacy, written-but-ignored field).
    suite.log('3.4 Model A auto_start=false (legacy field, UI no longer sets it)', p0.models[0].auto_start === false);
    suite.log('3.5 Model B auto_start=false (default)', p0.models[1].auto_start === false);
    suite.log('3.6 Model B has no override args', (p0.models[1].args || []).length === 0, `args=${JSON.stringify(p0.models[1].args)}`);
    suite.log('3.7 Pipeline Active=false (default)', p0.active === false);
    const chipCount = await page.locator('#pipeline-list .pl-chips-wrap .status-badge').count();
    const chipText = (await page.locator('#pipeline-list .pl-chips-wrap').first().textContent() || '');
    suite.log('3.8 UI shows two model chips', chipCount === 2, `chips=${chipCount}`);
    suite.log('3.9 Chips show both model names', chipText.includes('Pipe Model A') && chipText.includes('Pipe Model B'), `text=${JSON.stringify(chipText)}`);
    suite.log('3.10 No ↯ override glyph in chips (removed)', !chipText.includes('↯'), `text=${JSON.stringify(chipText)}`);
    // (AutoStart reconciliation) the ambiguous ⚡ per-entry autostart chip is gone.
    const autoChips = await page.locator('#pipeline-list .pl-chips-wrap .pl-chip-auto').count();
    suite.log('3.11 No per-entry autostart ⚡ indicator on any chip', autoChips === 0, `auto=${autoChips}`);
    const nameTitle = (await page.locator('#pipeline-list .pl-name').first().getAttribute('title')) || '';
    suite.log('3.12 Name cell carries the full name in its title', nameTitle === 'Cluster A', `title=${JSON.stringify(nameTitle)}`);
    // (list layout, Models pattern) an inline icon strip: stopped → Start + Edit +
    // Delete (3 icon buttons); no overflow "…" menu.
    const stripIcons = await page.locator('#pipeline-list .model-row .icon-btn').count();
    const overflowCount = await page.locator('#pipeline-list .pl-overflow').count();
    const startIconPresent = (await page.locator('#pipeline-list .icon-btn[onclick*="startPipeline"]').count()) === 1;
    suite.log('3.13 List shows the inline icon strip (Start+Edit+Delete), no overflow',
      stripIcons === 3 && overflowCount === 0 && startIconPresent, `icons=${stripIcons} overflow=${overflowCount} start=${startIconPresent}`);

    // ═══ SECTION 4: Per-model status renders (stopped initially) ═══
    const stoppedChips = await page.locator('#pipeline-list .pl-chips-wrap .status-badge.stopped').count();
    suite.log('4.1 Per-model status renders as stopped (2)', stoppedChips === 2, `stopped=${stoppedChips}`);

    // ═══ SECTION 5: Start pipeline (icon strip, end-to-end) ═══
    await page.locator('#pipeline-list .icon-btn[onclick*="startPipeline"]').filter({ visible: true }).first().click();
    const bothRunning = await poll(async () => {
      const r = await runningFor(pipeId);
      return r.length === 2 ? r : null;
    }, 20000);
    suite.log('5.1 Start: both instances running with pipeline_id', !!bothRunning, `running=${bothRunning ? bothRunning.length : 0}`);
    const allInst = await pipelineInstances();
    suite.log('5.2 Both owned instances carry the pipeline_id', allInst.length === 2 && allInst.every(i => i.pipeline_id === pipeId), `count=${allInst.length}`);
    await poll(async () => (await page.locator('#pipeline-list .pl-chips-wrap .status-badge.running').count()) === 2, 8000);
    const runningChips = await page.locator('#pipeline-list .pl-chips-wrap .status-badge.running').count();
    suite.log('5.3 Per-model status chips show running (2)', runningChips === 2, `running=${runningChips}`);
    // Running: the icon strip flips to Restart + Stop (plus Edit + Delete).
    const runningStrip = await page.locator('#pipeline-list .model-row .icon-btn').count();
    const stopIcon = (await page.locator('#pipeline-list .icon-btn[onclick*="stopPipeline"]').count()) === 1;
    const restartIcon = (await page.locator('#pipeline-list .icon-btn[onclick*="restartPipeline"]').count()) === 1;
    suite.log('5.4 Icon strip shows Restart+Stop while running (4 icons)', runningStrip === 4 && stopIcon && restartIcon, `icons=${runningStrip} stop=${stopIcon} restart=${restartIcon}`);
    await H.screenshot(page, ws, '03-pipeline-running');

    // ═══ SECTION 6: Restart pipeline (icon strip) ═══
    const oldIds = (await runningFor(pipeId)).map(i => i.id).sort();
    await actionClick('#pipeline-list .model-row', 'restartPipeline');
    const restarted = await poll(async () => {
      const r = await runningFor(pipeId);
      if (r.length !== 2) return null;
      const newIds = r.map(i => i.id).sort();
      return newIds.some(id => oldIds.indexOf(id) === -1) ? r : null;
    }, 25000);
    suite.log('6.1 Restart (icon strip): both running again', !!restarted, `running=${restarted ? restarted.length : 0}`);
    suite.log('6.2 Restart: new instances (old ones replaced)', !!restarted && restarted.every(i => oldIds.indexOf(i.id) === -1), `old=${JSON.stringify(oldIds)}`);
    await H.screenshot(page, ws, '04-pipeline-restarted');

    // ═══ SECTION 7: Stop pipeline (icon strip, end-to-end) ═══
    await page.locator('#pipeline-list .icon-btn[onclick*="stopPipeline"]').filter({ visible: true }).first().click();
    const allStopped = await poll(async () => {
      const r = await runningFor(pipeId);
      return r.length === 0 ? true : null;
    }, 20000);
    suite.log('7.1 Stop: all owned instances stopped', allStopped === true);
    await page.waitForTimeout(600);
    const stoppedAfter = await page.locator('#pipeline-list .pl-chips-wrap .status-badge.stopped').count();
    suite.log('7.2 Per-model status chips back to stopped (2)', stoppedAfter === 2, `stopped=${stoppedAfter}`);
    await H.screenshot(page, ws, '05-pipeline-stopped');

    // ═══ SECTION 8: Pipeline Active (persistent, distinct from Start) ═══
    { const res = (await api('GET', '/api/v1/pipelines/' + pipeId)).data; const pipe = res.pipeline || res; const putModels = (pipe.models || []).map(m => ({ model_id: m.model_id, args: m.args || [] })); const putRes = await api('PUT', '/api/v1/pipelines/' + pipeId, { name: pipe.name, active: true, models: putModels }); if (putRes.status !== 200) suite.log('8.0 PUT failed', false, `status=${putRes.status} data=${JSON.stringify(putRes.data).slice(0,200)}`); }
    const activeOn = await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      const p = (r.data || []).find(x => x.id === pipeId);
      return p && p.active === true ? true : null;
    }, 8000);
    suite.log('8.1 Active sets active=true (persisted)', activeOn === true);
    await page.reload({ waitUntil: 'networkidle' });
    await page.evaluate(() => window.navigate('pipelines'));
    await poll(async () => (await page.locator('#pipeline-list .model-row').count()) >= 1, 5000);
    const autoBadge = await page.locator('#pipeline-list .autostart-indicator').first().isVisible();
    suite.log('8.2 Active badge visible in the list', autoBadge === true);
    const stillDown = (await runningFor(pipeId)).length === 0;
    suite.log('8.3 Active is a setting, not a launch: no instances running after toggle', stillDown);

    // ═══ SECTION 9: Edit pipeline (overflow → edit; prefilled blocks) ═══
    await actionClick('#pipeline-list .model-row', 'editPipeline');
    await page.waitForTimeout(400);
    suite.log('9.0 Edit modal opens', await page.locator('#pipeline-modal').evaluate(el => getComputedStyle(el).display === 'flex'));
    const editBlocks = await page.locator('#pl-models-container .pl-block').count();
    const eb0Sel = await page.locator('#pl-models-container .pl-block').nth(0).locator('.pl-model-select').inputValue();
    const eb1Sel = await page.locator('#pl-models-container .pl-block').nth(1).locator('.pl-model-select').inputValue();
    suite.log('9.1 Edit prefills both blocks in order', editBlocks === 2 && eb0Sel === idA && eb1Sel === idB, `blocks=${editBlocks} b0=${eb0Sel} b1=${eb1Sel}`);
    // (custom Args round-trip) block 0 is in Custom mode with the stored args.
    const eb0Custom = await page.locator('#pl-models-container .pl-block').nth(0).locator('.pl-args-radio[value="custom"]').isChecked();
    const eb0Args = await page.locator('#pl-models-container .pl-block').nth(0).locator('.pl-args-input').evaluate(el => el.value).catch(() => '');
    suite.log('9.2 Block 0 prefilled in Custom mode with stored args', eb0Custom && eb0Args === 'infinite override-A', `custom=${eb0Custom} args=${JSON.stringify(eb0Args)}`);
    await page.fill('#pl-name', 'Cluster A2');
    await page.click('#pipeline-submit-btn');
    const renamed = await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      const p = (r.data || []).find(x => x.id === pipeId);
      return p && p.name === 'Cluster A2' ? true : null;
    }, 8000);
    suite.log('9.3 Rename persisted (structural list unchanged)', renamed === true);
    await poll(async () => ((await page.locator('#pipeline-list .model-row').first().textContent() || '')).includes('Cluster A2'), 8000);
    suite.log('9.4 List shows the new name', (await page.locator('#pipeline-list .model-row').first().textContent() || '').includes('Cluster A2'));
    await H.screenshot(page, ws, '06-pipeline-edited');

    // ═══ SECTION 10: Duplicate model ×2 with DIFFERENT custom args ═══
    await page.click('#view-pipelines .view-header .btn-primary');
    await page.waitForTimeout(300);
    await page.fill('#pl-name', 'Dup Pipeline');
    const d0 = page.locator('#pl-models-container .pl-block').nth(0);
    await d0.locator('.pl-model-select').selectOption(idA);
    await segClick(d0, 'custom');
    await page.waitForTimeout(120);
    await d0.locator('.pl-args-input').fill('--port 8085');
    await page.click('#pl-models-container .pl-add');
    await page.waitForTimeout(200);
    const d1 = page.locator('#pl-models-container .pl-block').nth(1);
    await d1.locator('.pl-model-select').selectOption(idA);
    await segClick(d1, 'custom');
    await page.waitForTimeout(120);
    await d1.locator('.pl-args-input').fill('--port 8086');
    // (two independent steps) each block keeps its own args; visually two blocks.
    suite.log('10.0 Two blocks for the same model, each with its own args editor',
      (await page.locator('#pl-models-container .pl-block').count()) === 2 &&
      (await page.locator('#pl-models-container .pl-block').nth(0).locator('.pl-args-input').inputValue()) === '--port 8085' &&
      (await page.locator('#pl-models-container .pl-block').nth(1).locator('.pl-args-input').inputValue()) === '--port 8086');
    const guardDup = await builderGuard(2);
    suite.log('10.0b No connectors + bounded blocks for duplicate Model ×2', guardDup.ok, `connectors=${guardDup.connectors} issues=${guardDup.issues.join(',') || 'none'}`);
    await page.click('#pipeline-submit-btn');
    const dupRead = await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      const d = Array.isArray(r.data) ? r.data : [];
      const p = d.find(x => x.name === 'Dup Pipeline');
      return p ? { d, p } : null;
    }, 8000);
    const dupP = dupRead ? dupRead.p : null;
    suite.log('10.1 Duplicate model: accepted (pipeline created)', !!dupP);
    suite.log('10.2 Duplicate model: two entries with distinct ids',
      !!dupP && dupP.models.length === 2 && dupP.models[0].id !== dupP.models[1].id,
      `models=${JSON.stringify(dupP && dupP.models)}`);
    // (custom Args ×2 different values) the two entries carry different args.
    const dupA0 = (dupP && dupP.models[0].args || []).join(' ');
    const dupA1 = (dupP && dupP.models[1].args || []).join(' ');
    suite.log('10.3 Duplicate model: custom args ×2 differ (8085 / 8086)',
      dupA0.indexOf('8085') !== -1 && dupA1.indexOf('8086') !== -1, `a0=${JSON.stringify(dupA0)} a1=${JSON.stringify(dupA1)}`);
    await poll(async () => (await page.locator('#pipeline-list .model-row', { hasText: 'Dup Pipeline' }).count()) === 1, 8000);
    const dupRow = page.locator('#pipeline-list .model-row', { hasText: 'Dup Pipeline' });
    const dupChips = await dupRow.locator('.pl-chips-wrap .status-badge').count();
    const dupOcc = await dupRow.locator('.pl-chip-occ').count();
    suite.log('10.4 Repeated model shows one chip per entry (2)', dupChips === 2, `chips=${dupChips}`);
    suite.log('10.5 No misleading ×N occurrence badge on per-entry chips', dupOcc === 0, `occ=${dupOcc}`);
    suite.log('10.6 Duplicate-model error toast absent', (await page.locator('#toast-container .toast.error').count()) === 0);
    if (dupP) {
      const del = await api('DELETE', `/api/v1/pipelines/${dupP.id}`);
      suite.log('10.7 Cleanup: duplicate pipeline deleted', del.status === 200, `status=${del.status}`);
    }
    await page.waitForTimeout(300);

    // ═══ SECTION 10b: Three-block builder guard (A → A → B) ═══
    await page.click('#view-pipelines .view-header .btn-primary');
    await page.waitForTimeout(300);
    await page.fill('#pl-name', 'Connector 3');
    const t0 = page.locator('#pl-models-container .pl-block').nth(0);
    await t0.locator('.pl-model-select').selectOption(idA);
    await page.click('#pl-models-container .pl-add');
    await page.waitForTimeout(200);
    const t1 = page.locator('#pl-models-container .pl-block').nth(1);
    await t1.locator('.pl-model-select').selectOption(idA);
    await page.click('#pl-models-container .pl-add');
    await page.waitForTimeout(200);
    const t2 = page.locator('#pl-models-container .pl-block').nth(2);
    await t2.locator('.pl-model-select').selectOption(idB);
    const threeSel = await page.locator('#pl-models-container .pl-model-select').evaluateAll(els => els.map(el => el.value));
    suite.log('10b.1 Three-block builder renders A → A → B',
      (await page.locator('#pl-models-container .pl-block').count()) === 3 && JSON.stringify(threeSel) === JSON.stringify([idA, idA, idB]),
      `sel=${JSON.stringify(threeSel)}`);
    const guard3 = await builderGuard(3);
    suite.log('10b.2 No connectors + bounded blocks + no leaked source (3 blocks)', guard3.ok, `connectors=${guard3.connectors} issues=${guard3.issues.join(',') || 'none'}`);
    await page.click('#pipeline-submit-btn');
    const threeRead = await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      const p = (Array.isArray(r.data) ? r.data : []).find(x => x.name === 'Connector 3');
      return p ? { p } : null;
    }, 8000);
    const threeP = threeRead ? threeRead.p : null;
    suite.log('10b.3 Three-block pipeline persisted (A, A, B)',
      !!threeP && threeP.models.length === 3 &&
      threeP.models[0].model_id === idA && threeP.models[1].model_id === idA && threeP.models[2].model_id === idB,
      `order=${JSON.stringify(threeP && threeP.models.map(m => m.model_id))}`);
    await poll(async () => (await page.locator('#pipeline-list .model-row', { hasText: 'Connector 3' }).count()) === 1, 8000);
    const threeRow = page.locator('#pipeline-list .model-row', { hasText: 'Connector 3' });
    const threeChips = await threeRow.locator('.pl-chips-wrap .status-badge').count();
    const threeOcc = await threeRow.locator('.pl-chip-occ').count();
    suite.log('10b.4 Three chips render in the list, no ×N badge', threeChips === 3 && threeOcc === 0, `chips=${threeChips} occ=${threeOcc}`);
    if (threeP) {
      const del3 = await api('DELETE', `/api/v1/pipelines/${threeP.id}`);
      suite.log('10b.5 Cleanup: three-block pipeline deleted', del3.status === 200, `status=${del3.status}`);
    }
    await page.waitForTimeout(300);

    // ═══ SECTION 10c: No-op Save with 4 duplicate ModelID entries (Owner scenario) ═══
    // Exact Owner acceptance: pipeline with 4 entries all referencing the same
    // ModelID (E1/E2 FromModel, E3/E4 Custom). Open edit, save without changes.
    // Assert: no error, IDs unchanged, 4 entries preserved.
    await page.click('#view-pipelines .view-header .btn-primary');
    await page.waitForTimeout(300);
    await page.fill('#pl-name', 'NoOp 4x Dup');
    const n0 = page.locator('#pl-models-container .pl-block').nth(0);
    await n0.locator('.pl-model-select').selectOption(idA);
    await page.click('#pl-models-container .pl-add');
    await page.waitForTimeout(150);
    const n1 = page.locator('#pl-models-container .pl-block').nth(1);
    await n1.locator('.pl-model-select').selectOption(idA);
    await page.click('#pl-models-container .pl-add');
    await page.waitForTimeout(150);
    const n2 = page.locator('#pl-models-container .pl-block').nth(2);
    await n2.locator('.pl-model-select').selectOption(idA);
    await segClick(n2, 'custom');
    await page.waitForTimeout(100);
    await n2.locator('.pl-args-input').fill('--port 8091');
    await page.click('#pl-models-container .pl-add');
    await page.waitForTimeout(150);
    const n3 = page.locator('#pl-models-container .pl-block').nth(3);
    await n3.locator('.pl-model-select').selectOption(idA);
    await segClick(n3, 'custom');
    await page.waitForTimeout(100);
    await n3.locator('.pl-args-input').fill('--port 8092');
    const fourBlocks = await page.locator('#pl-models-container .pl-block').count();
    suite.log('10c.1 Four blocks for 4 duplicate model entries', fourBlocks === 4, `blocks=${fourBlocks}`);
    await page.click('#pipeline-submit-btn');
    const noOpRead = await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      const p = (Array.isArray(r.data) ? r.data : []).find(x => x.name === 'NoOp 4x Dup');
      return p ? { p } : null;
    }, 8000);
    const noOpP = noOpRead ? noOpRead.p : null;
    suite.log('10c.2 Pipeline with 4 dup entries created', !!noOpP && noOpP.models.length === 4, `models=${noOpP ? noOpP.models.length : 0}`);
    const noOpIds = noOpP ? noOpP.models.map(m => m.id) : [];
    suite.log('10c.3 All 4 entries have distinct server-assigned IDs',
      noOpIds.length === 4 && noOpIds.every(id => !!id) && new Set(noOpIds).size === 4,
      `ids=${JSON.stringify(noOpIds)}`);

    // Open edit modal and save without any changes (no-op save).
    const noOpRow = page.locator('#pipeline-list .model-row', { hasText: 'NoOp 4x Dup' });
    await noOpRow.locator('.icon-btn[onclick*="editPipeline"]').filter({ visible: true }).first().click();
    await page.waitForTimeout(400);
    const noOpEditBlocks = await page.locator('#pl-models-container .pl-block').count();
    suite.log('10c.4 Edit modal opens with 4 blocks', noOpEditBlocks === 4, `blocks=${noOpEditBlocks}`);
    // Verify the edit modal pre-filled the correct model selections (proves
    // the UI loaded the 4 entries from the API with their IDs intact).
    const noOpEditSels = await page.locator('#pl-models-container .pl-model-select').evaluateAll(els => els.map(el => el.value));
    suite.log('10c.5 Edit modal prefills all 4 blocks with the correct model',
      noOpEditSels.length === 4 && noOpEditSels.every(v => v === idA),
      `sels=${JSON.stringify(noOpEditSels)}`);
    // Click save (no changes made).
    await page.click('#pipeline-submit-btn');
    await page.waitForTimeout(800);
    const noOpToast = await page.locator('#toast-container .toast.error').count();
    suite.log('10c.6 No-op Save: no error toast', noOpToast === 0, `errorToasts=${noOpToast}`);
    // Verify via API that IDs are unchanged.
    const noOpAfter = await poll(async () => {
      const r = await api('GET', '/api/v1/pipelines');
      const p = (Array.isArray(r.data) ? r.data : []).find(x => x.name === 'NoOp 4x Dup');
      return p ? { p } : null;
    }, 5000);
    const noOpAfterIds = noOpAfter ? noOpAfter.p.models.map(m => m.id) : [];
    suite.log('10c.7 No-op Save: entry IDs unchanged after save',
      JSON.stringify(noOpAfterIds) === JSON.stringify(noOpIds),
      `before=${JSON.stringify(noOpIds)} after=${JSON.stringify(noOpAfterIds)}`);
    suite.log('10c.8 No-op Save: still 4 entries', noOpAfter && noOpAfter.p.models.length === 4, `count=${noOpAfter ? noOpAfter.p.models.length : 0}`);
    // Cleanup.
    if (noOpP) {
      const delNoOp = await api('DELETE', `/api/v1/pipelines/${noOpP.id}`);
      suite.log('10c.9 Cleanup: no-op pipeline deleted', delNoOp.status === 200, `status=${delNoOp.status}`);
    }
    await page.waitForTimeout(300);

    // ═══ SECTION 11: Responsive — card pattern at all widths ═══
    await page.setViewportSize({ width: 768, height: 1024 });
    await page.waitForTimeout(500);
    const cardVisible = await page.locator('#pipeline-list .model-row').first().isVisible();
    suite.log('11.1 At 768px the card list is shown', cardVisible);
    const cardText = (await page.locator('#pipeline-list .model-row').first().textContent() || '');
    suite.log('11.2 Card shows the pipeline name', cardText.includes('Cluster A2'), `text=${JSON.stringify(cardText)}`);
    // (card) name + state in the header.
    const cardHeaderHasState = await page.locator('#pipeline-list .model-row .model-row-name .status-badge').count() >= 1;
    suite.log('11.3 Card header shows name + state', cardHeaderHasState);
    await H.screenshot(page, ws, '07-pipelines-768');
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.waitForTimeout(300);

    // ═══ SECTION 12: i18n EN/RU ═══
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(400);
    const navEn = ((await page.locator('.nav-item[data-view="pipelines"] span[data-i18n]').first().textContent() || '').trim());
    suite.log('12.1 EN nav label is "Pipelines"', navEn === 'Pipelines', `text=${JSON.stringify(navEn)}`);
    const titleEn = ((await page.locator('#view-pipelines h1').textContent() || '').trim());
    suite.log('12.2 EN page title localized', titleEn === 'Pipelines', `text=${JSON.stringify(titleEn)}`);
    // (EN builder wording) the segmented args control labels are English.
    await page.click('#view-pipelines .view-header .btn-primary');
    await page.waitForTimeout(300);
    const enFromModel = await page.locator('#pl-models-container .pl-seg-item span').nth(0).textContent();
    const enCustom = await page.locator('#pl-models-container .pl-seg-item span').nth(1).textContent();
    suite.log('12.3 EN builder: "From model" + "Custom" segmented labels', enFromModel.trim() === 'From model' && enCustom.trim().startsWith('Custom'), `from=${JSON.stringify(enFromModel)} custom=${JSON.stringify(enCustom)}`);
    await page.click('#pipeline-modal .modal-actions .btn-ghost');
    await page.waitForTimeout(200);
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(400);
    const navRu = ((await page.locator('.nav-item[data-view="pipelines"] span[data-i18n]').first().textContent() || '').trim());
    suite.log('12.4 RU nav label localized (not English)', navRu.length > 0 && navRu !== 'Pipelines', `text=${JSON.stringify(navRu)}`);
    const histNavRu = ((await page.locator('.nav-item[data-view="history"] span[data-i18n]').first().textContent() || '').trim());
    const histNavRuKey = await page.evaluate(() => t('nav.history'));
    suite.log('12.5 RU History nav renamed to launch-history wording', histNavRu === histNavRuKey && histNavRu !== 'История экземпляров', `text=${JSON.stringify(histNavRu)}`);
    await page.evaluate(() => window.navigate('history'));
    await page.waitForTimeout(400);
    const histTitleRu = ((await page.locator('#view-history h1').textContent() || '').trim());
    suite.log('12.6 RU History page title renamed', histTitleRu === histNavRuKey && histTitleRu !== 'История экземпляров', `text=${JSON.stringify(histTitleRu)}`);
    await page.evaluate(() => window.navigate('pipelines'));
    await page.waitForTimeout(300);

    // ═══ SECTION 12b: parseArgs (D10 13) + narrow/desktop + mobile ═══
    const qa1 = await page.evaluate(() => parseArgs('-m "E:\\models\\my model.gguf" --port 8081'));
    suite.log('12b.1 parseArgs keeps a quoted path with spaces as one token',
      JSON.stringify(qa1) === JSON.stringify(['-m', 'E:\\models\\my model.gguf', '--port', '8081']), `got=${JSON.stringify(qa1)}`);
    const qa2 = await page.evaluate(() => parseArgs('a  "b c" d'));
    suite.log('12b.2 parseArgs handles mixed quoted/unquoted', JSON.stringify(qa2) === JSON.stringify(['a', 'b c', 'd']), `got=${JSON.stringify(qa2)}`);
    const qa3 = await page.evaluate(() => parseArgs('   '));
    suite.log('12b.3 parseArgs returns [] for blank input', Array.isArray(qa3) && qa3.length === 0, `got=${JSON.stringify(qa3)}`);

    // Regression: JSON kwargs with escaped quotes must yield the exact JSON
    // token, not a backslash-mangled form (the --chat-template-kwargs defect).
    const qaJson = await page.evaluate(() => parseArgs('--chat-template-kwargs "{\\"reasoning_effort\\":\\"medium\\"}"'));
    suite.log('12b.4 parseArgs unescapes JSON kwargs to the exact token (regression)',
      JSON.stringify(qaJson) === JSON.stringify(['--chat-template-kwargs', '{"reasoning_effort":"medium"}']), `got=${JSON.stringify(qaJson)}`);
    // Unquoted Windows path: backslashes are literal, no quoting needed.
    const qaWin = await page.evaluate(() => parseArgs('-m E:\\models\\m.gguf'));
    suite.log('12b.5 parseArgs keeps unquoted Windows path backslashes literal',
      JSON.stringify(qaWin) === JSON.stringify(['-m', 'E:\\models\\m.gguf']), `got=${JSON.stringify(qaWin)}`);
    // An empty quoted section is a valid empty token.
    const qaEmpty = await page.evaluate(() => parseArgs('a "" b'));
    suite.log('12b.6 parseArgs yields an empty token for ""',
      JSON.stringify(qaEmpty) === JSON.stringify(['a', '', 'b']), `got=${JSON.stringify(qaEmpty)}`);
    // An escaped backslash inside quotes collapses to a single backslash.
    const qaEsc = await page.evaluate(() => parseArgs('"a\\\\b"'));
    suite.log('12b.7 parseArgs collapses an escaped backslash inside quotes',
      JSON.stringify(qaEsc) === JSON.stringify(['a\\b']), `got=${JSON.stringify(qaEsc)}`);
    // Nested JSON mixing escaped quotes and escaped backslashes.
    const qaNested = await page.evaluate(() => parseArgs('--x "{\\"a\\":\\"b\\\\c\\"}"'));
    suite.log('12b.8 parseArgs handles nested JSON (escaped quote + backslash)',
      JSON.stringify(qaNested) === JSON.stringify(['--x', '{"a":"b\\c"}']), `got=${JSON.stringify(qaNested)}`);

    // Narrow desktop (~1024, card pattern): name must not wrap mid-word.
    await page.setViewportSize({ width: 1024, height: 800 });
    await page.waitForTimeout(400);
    const nameWrap = await page.locator('#pipeline-list .pl-name').first().evaluate(el => getComputedStyle(el).whiteSpace);
    suite.log('12b.9 Name is nowrap at narrow desktop (no mid-word wrap)', nameWrap === 'nowrap', `ws=${nameWrap}`);
    const cardStillVisible = await page.locator('#pipeline-list .model-row').first().isVisible();
    suite.log('12b.10 Card still shown at 1024px', cardStillVisible);

    // Mobile (<=768): card keeps semantic hierarchy; no horizontal overflow.
    await page.setViewportSize({ width: 414, height: 800 });
    await page.waitForTimeout(500);
    const mobileCardVisible = await page.locator('#pipeline-list .model-row').first().isVisible();
    suite.log('12b.11 Mobile card visible', mobileCardVisible);
    // (card) the inline icon strip (stopped → Start + Edit + Delete) sits on
    // the right; no overflow "..." menu.
    const cardIcons = await page.locator('#pipeline-list .model-row .model-row-actions .icon-btn').count();
    const cardStart = (await page.locator('#pipeline-list .icon-btn[onclick*="startPipeline"]').count()) === 1;
    suite.log('12b.12 Card shows the inline icon strip (Start+Edit+Delete)', cardIcons === 3 && cardStart, `icons=${cardIcons} start=${cardStart}`);
    const cardOverflow = await page.locator('#pipeline-list .pl-overflow').count();
    suite.log('12b.13 Card has no overflow "..." menu', cardOverflow === 0, `overflow=${cardOverflow}`);
    const overflowX = await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1);
    suite.log('12b.14 No horizontal overflow at mobile width', overflowX);
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.waitForTimeout(300);

    // ═══ SECTION 12c: linear builder — desktop, mobile, dark/light ═══
    // Desktop: a long full-override line round-trips in the single textarea.
    await actionClick('#pipeline-list .model-row', 'editPipeline');
    await page.waitForTimeout(300);
    const rowForOverride = page.locator('#pl-models-container .pl-block').nth(0);
    const longLine = '-m "E:\\very\\long\\path\\to\\the\\model.gguf" --port 8082 --ctx-size 131072 --no-mmap --threads 16 --main-gpu 0 --flash-attn on --mi 500 --cache-type-k q8_0 --cache-type-v q8_0 --mlock --lcm-freq 33554432';
    await rowForOverride.locator('.pl-args-input').fill(longLine);
    const valueRoundTrip = await rowForOverride.locator('.pl-args-input').inputValue();
    suite.log('12c.1 Override editor: single textarea usable with a long full-override line', valueRoundTrip === longLine, `len=${valueRoundTrip.length}`);
    await page.click('#pipeline-modal .modal-actions .btn-ghost');
    await page.waitForTimeout(200);

    // Mobile builder: blocks stack, custom args textarea usable, no overflow.
    await page.setViewportSize({ width: 414, height: 800 });
    await page.waitForTimeout(400);
    await page.click('#view-pipelines .view-header .btn-primary');
    await page.waitForTimeout(300);
    const mbBlocks = await page.locator('#pl-models-container .pl-block').count();
    suite.log('12c.2 Mobile builder: blocks render vertically', mbBlocks === 1);
    const guardMob1 = await builderGuard(1);
    suite.log('12c.2b Mobile: no connectors + bounded blocks + no leaked source (1 block)', guardMob1.ok, `connectors=${guardMob1.connectors} issues=${guardMob1.issues.join(',') || 'none'}`);
    await page.click('#pl-models-container .pl-add');
    await page.waitForTimeout(200);
    const guardMob2 = await builderGuard(2);
    suite.log('12c.2c Mobile: no connectors + bounded blocks (2 blocks)', guardMob2.ok, `connectors=${guardMob2.connectors} issues=${guardMob2.issues.join(',') || 'none'}`);
    await page.click('#pl-models-container .pl-add');
    await page.waitForTimeout(200);
    const guardMob3 = await builderGuard(3);
    suite.log('12c.2d Mobile: no connectors + bounded blocks (3 blocks)', guardMob3.ok, `connectors=${guardMob3.connectors} issues=${guardMob3.issues.join(',') || 'none'}`);
    await segClick(page.locator('#pl-models-container .pl-block').first(), 'custom');
    await page.waitForTimeout(150);
    await page.locator('#pl-models-container .pl-args-input').first().fill('--port 8087 --ctx 4096');
    const mbVal = await page.locator('#pl-models-container .pl-args-input').first().inputValue();
    suite.log('12c.3 Mobile builder: custom args textarea usable', mbVal === '--port 8087 --ctx 4096', `val=${JSON.stringify(mbVal)}`);
    const mbOverflow = await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1);
    suite.log('12c.4 Mobile builder at 414px: no horizontal overflow', mbOverflow);
    await H.screenshot(page, ws, '08-builder-mobile');
    await page.click('#pipeline-modal .modal-actions .btn-ghost');
    await page.waitForTimeout(200);
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.waitForTimeout(300);

    // dark/light: the builder renders cleanly under both themes.
    await page.evaluate(() => window.setTheme('dark'));
    await page.waitForTimeout(200);
    const darkAttr = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
    await page.click('#view-pipelines .view-header .btn-primary');
    await page.waitForTimeout(300);
    const darkBlocks = await page.locator('#pl-models-container .pl-block').count();
    suite.log('12c.5 Dark theme applied and builder renders', darkAttr === 'dark' && darkBlocks === 1, `theme=${darkAttr} blocks=${darkBlocks}`);
    await page.click('#pipeline-modal .modal-actions .btn-ghost');
    await page.waitForTimeout(200);
    await page.evaluate(() => window.setTheme('light'));
    await page.waitForTimeout(200);
    const lightAttr = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
    await page.click('#view-pipelines .view-header .btn-primary');
    await page.waitForTimeout(300);
    const lightBlocks = await page.locator('#pl-models-container .pl-block').count();
    suite.log('12c.6 Light theme applied and builder renders', lightAttr === 'light' && lightBlocks === 1, `theme=${lightAttr} blocks=${lightBlocks}`);
    await H.screenshot(page, ws, '09-builder-light');
    await page.click('#pipeline-modal .modal-actions .btn-ghost');
    await page.evaluate(() => window.setTheme('system'));
    await page.waitForTimeout(200);

    // ═══ SECTION 12d: Args round-trip to process argv (regression) ═══
    // Proves the full production path end-to-end: UI parseArgs → request DTO →
    // persistence → launch → the exact argv token the CHILD process receives.
    // fake-runtime "argv-file" dumps the argv it parsed from the OS command
    // line, so this is the true process-boundary ground truth (not just what
    // GoAl intended to send).
    const fs = require('fs');
    const argvOut = path.join(ws.dataDir, 'argv-roundtrip.txt');
    try { fs.unlinkSync(argvOut); } catch {}
    const rtText = 'argv-file ' + argvOut + ' --chat-template-kwargs "{\\"reasoning_effort\\":\\"medium\\"}"';
    const rtArgs = await page.evaluate((t) => parseArgs(t), rtText);
    suite.log('12d.1 parseArgs tokenizes argv-file harness + JSON kwargs',
      JSON.stringify(rtArgs) === JSON.stringify(['argv-file', argvOut, '--chat-template-kwargs', '{"reasoning_effort":"medium"}']),
      `got=${JSON.stringify(rtArgs)}`);

    const rtModel = await api('POST', '/api/v1/models', { name: 'RT Args Model', runtime_id: 'rt-fake', args: rtArgs });
    const rtModelId = rtModel.data && rtModel.data.id;
    const rtStored = await api('GET', '/api/v1/models/' + rtModelId);
    const storedArgs = (rtStored.data && rtStored.data.args) || [];
    suite.log('12d.2 Persisted Model.Args round-trip the exact argv tokens (no mangling)',
      JSON.stringify(storedArgs) === JSON.stringify(rtArgs) && storedArgs.includes('{"reasoning_effort":"medium"}'),
      `stored=${JSON.stringify(storedArgs)}`);

    await api('POST', `/api/v1/models/${rtModelId}/start`);
    let argvLines = null;
    for (let i = 0; i < 40 && argvLines === null; i++) {
      try { argvLines = fs.readFileSync(argvOut, 'utf8').split('\n'); } catch { await sleep(250); }
    }
    suite.log('12d.3 Child process received the exact argv token (round-trip)',
      Array.isArray(argvLines) && argvLines.includes('{"reasoning_effort":"medium"}'),
      `lines=${JSON.stringify(argvLines)}`);
    suite.log('12d.4 Child process did NOT receive a backslash-mangled form',
      Array.isArray(argvLines) && argvLines.length > 0 && !argvLines.some(l => l.includes('\\')),
      `lines=${JSON.stringify(argvLines)}`);
    await api('POST', `/api/v1/models/${rtModelId}/stop`);
    await sleep(400);

    // ═══ SECTION 13: Delete pipeline (icon strip → delete; confirm dialog) ═══
    await actionClick('#pipeline-list .model-row', 'deletePipeline');
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
    await H.screenshot(page, ws, '10-pipeline-deleted');

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

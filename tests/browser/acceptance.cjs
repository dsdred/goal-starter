'use strict';
const path = require('path');
const H = require('./harness.cjs');

const PORT = 19480;
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
  const ws = H.makeWorkspace('acceptance');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  H.writeConfig(ws, {
    webPort: PORT,
    authEnabled: false,
    runtimes: [{ id: 'rt-fake', name: 'Fake Runtime', executable: fakeRt, workingDirectory: fakeRtDir }],
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

  // Seed: one model + one pipeline for the tests
  const mA = await api('POST', '/api/v1/models', { name: 'Acc Model A', runtime_id: 'rt-fake', args: ['infinite'] });
  const mB = await api('POST', '/api/v1/models', { name: 'Acc Model B', runtime_id: 'rt-fake', args: ['infinite'] });
  const idA = mA.data && mA.data.id;
  const idB = mB.data && mB.data.id;
  const pipeRes = await api('POST', '/api/v1/pipelines', { name: 'Acc Pipeline', models: [{ model_id: idA }, { model_id: idB }] });
  const pipeId = pipeRes.data && pipeRes.data.id;

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('ACCEPTANCE (Owner visual contract — geometry + computed style + behavior)');
  suite.watchPage(page);

  let ok = false;
  try {
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);

    // ═══ A: History rightmost action visible ═══
    await api('POST', `/api/v1/models/${idA}/start`);
    await sleep(1500);
    await api('POST', `/api/v1/models/${idA}/stop`);
    await sleep(1000);

    await page.evaluate(() => window.navigate('history'));
    await page.waitForTimeout(500);
    const histGeom = await page.evaluate(() => {
      const wrap = document.getElementById('history-table-wrap');
      const cards = document.getElementById('history-compact');
      const isTable = wrap && wrap.classList.contains('fit-table');
      const isCards = cards && cards.classList.contains('visible');
      if (isTable) {
        const lastTds = wrap.querySelectorAll('tbody tr td:last-child');
        const wr = wrap.getBoundingClientRect();
        let maxRight = 0, actionVisible = true;
        lastTds.forEach(td => {
          const r = td.getBoundingClientRect();
          if (r.right > wr.right + 1) actionVisible = false;
          if (r.right > maxRight) maxRight = r.right;
        });
        const btns = wrap.querySelectorAll('tbody tr .actions-cell .btn, tbody tr .actions-cell .icon-btn');
        const allBtnsVisible = [...btns].every(b => { const r = b.getBoundingClientRect(); return r.right <= wr.right + 1 && r.width > 0; });
        return { mode: 'table', maxRight: Math.round(maxRight), wrapRight: Math.round(wr.right), actionVisible, allBtnsVisible, rowCount: lastTds.length };
      } else if (isCards) {
        const rows = document.querySelectorAll('.chist-row');
        const allVisible = [...rows].every(r => {
          const btns = r.querySelectorAll('.compact-actions .icon-btn');
          return [...btns].every(b => { const rc = b.getBoundingClientRect(); return rc.right <= window.innerWidth + 1 && rc.width > 0; });
        });
        return { mode: 'cards', rowCount: rows.length, allVisible };
      }
      return { mode: 'neither' };
    });
    suite.log('A.1 History: representation is table or cards', histGeom.mode === 'table' || histGeom.mode === 'cards', JSON.stringify(histGeom));
    if (histGeom.mode === 'table') {
      suite.log('A.2 History table: rightmost action inside content bounds', histGeom.actionVisible && histGeom.allBtnsVisible,
        `maxRight=${histGeom.maxRight} wrapRight=${histGeom.wrapRight} actionVisible=${histGeom.actionVisible} allBtns=${histGeom.allBtnsVisible}`);
    } else if (histGeom.mode === 'cards') {
      suite.log('A.2 History cards: all actions visible and not clipped', histGeom.allVisible, `rows=${histGeom.rowCount}`);
    }

    // ═══ B: History toolbar — one row at intermediate width ═══
    await page.setViewportSize({ width: 1100, height: 800 });
    await page.waitForTimeout(500);
    const toolbarGeom = await page.evaluate(() => {
      const bar = document.querySelector('#view-history .filter-bar');
      if (!bar) return { found: false };
      const selects = bar.querySelectorAll('select');
      const endBtn = bar.querySelector('.filter-bar-end');
      if (selects.length < 2 || !endBtn) return { found: false, reason: 'missing elements' };
      const barR = bar.getBoundingClientRect();
      const s0r = selects[0].getBoundingClientRect();
      const s1r = selects[1].getBoundingClientRect();
      const btnR = endBtn.getBoundingClientRect();
      const sameRow = Math.abs(s0r.top - btnR.top) < 10 && Math.abs(s1r.top - btnR.top) < 10;
      const btnNotWrapped = btnR.top < s0r.bottom + 5;
      const noEmptySpace = btnR.left - s1r.right < barR.width * 0.7;
      return {
        found: true,
        barW: Math.round(barR.width),
        s0Top: Math.round(s0r.top), s1Top: Math.round(s1r.top), btnTop: Math.round(btnR.top),
        s1Right: Math.round(s1r.right), btnLeft: Math.round(btnR.left),
        sameRow, btnNotWrapped, noEmptySpace
      };
    });
    suite.log('B.1 History toolbar: found at 1100px', toolbarGeom.found === true, toolbarGeom.reason || '');
    if (toolbarGeom.found) {
      suite.log('B.2 History toolbar: all 3 controls on one row (no premature wrap)', toolbarGeom.sameRow && toolbarGeom.btnNotWrapped,
        `s0Top=${toolbarGeom.s0Top} s1Top=${toolbarGeom.s1Top} btnTop=${toolbarGeom.btnTop} sameRow=${toolbarGeom.sameRow} btnNotWrapped=${toolbarGeom.btnNotWrapped}`);
      suite.log('B.3 History toolbar: no huge empty gap before button', toolbarGeom.noEmptySpace,
        `s1Right=${toolbarGeom.s1Right} btnLeft=${toolbarGeom.btnLeft} gap=${toolbarGeom.btnLeft - toolbarGeom.s1Right}px barW=${toolbarGeom.barW}px`);
    }
    // Restore wide viewport
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.waitForTimeout(300);

    // ═══ C: Sidebar icons — unified stroke-based set ═══
    const sidebarGeom = await page.evaluate(() => {
      const items = document.querySelectorAll('.sidebar-nav .nav-item');
      const results = [];
      items.forEach((item, i) => {
        const svg = item.querySelector('svg.nav-icon');
        if (!svg) { results.push({ idx: i, label: '?', ok: false, issue: 'svg missing' }); return; }
        const r = svg.getBoundingClientRect();
        const issues = [];
        // Must be stroke-based (not filled blob)
        if (svg.getAttribute('fill') !== 'none') issues.push('fill!=none');
        if (svg.getAttribute('stroke') !== 'currentColor') issues.push('stroke!=currentColor');
        if (svg.getAttribute('viewBox') !== '0 0 24 24') issues.push('viewBox!=' + svg.getAttribute('viewBox'));
        const sw = svg.getAttribute('stroke-width');
        if (sw !== '1.6') issues.push('stroke-width=' + sw);
        // Must have path/circle/rect/polygon content
        const hasShape = svg.querySelector('path,circle,rect,polygon,polyline,line');
        if (!hasShape) issues.push('no shape element');
        // Visible and non-zero
        if (r.width <= 0 || r.height <= 0) issues.push('zero size');
        const style = getComputedStyle(svg);
        if (style.visibility === 'hidden' || style.display === 'none') issues.push('not visible');
        const label = item.querySelector('span') ? item.querySelector('span').textContent.trim() : '';
        results.push({ idx: i, label, ok: issues.length === 0, issues, w: Math.round(r.width), h: Math.round(r.height) });
      });
      return { count: items.length, items: results };
    });
    suite.log('C.1 Sidebar: 7 nav items found', sidebarGeom.count === 7, `count=${sidebarGeom.count}`);
    sidebarGeom.items.forEach((it, i) => {
      suite.log(`C.${i + 2} Sidebar "${it.label}": stroke-based icon (fill=none, stroke=currentColor, 24x24, sw=1.6)`,
        it.ok, it.ok ? `size=${it.w}x${it.h}` : it.issues.join(','));
    });

    // ═══ D: Wizard Runtime step — exactly one "Runtime" label ═══
    await page.evaluate(() => window.navigate('models'));
    await page.waitForTimeout(300);
    await page.evaluate(() => { openWizard(); });
    await page.waitForTimeout(400);
    await page.evaluate(() => { wizGoto(2); });
    await page.waitForTimeout(300);
    const rtStepInfo = await page.evaluate(() => {
      const panel = document.querySelector('.wizard-panel[data-panel="2"]');
      if (!panel) return { found: false };
      const text = panel.innerText;
      // Count actual label elements with the runtime i18n key (not substring matches in names)
      const rtLabels = panel.querySelectorAll('[data-i18n="wizard.rt.label"]');
      const runtimeLabelCount = rtLabels.length;
      const rawKeys = ['wizard.rt.existing', 'wizard.rt.new', 'wizard.rt.select_mode', 'wizard.rt.create_mode'];
      const foundRaw = rawKeys.filter(k => text.includes(k));
      const hasVybrat = text.includes('Выбрать');
      const hasSozdat = text.includes('Создать');
      return { found: true, runtimeLabelCount, foundRaw, hasVybrat, hasSozdat, text: text.slice(0, 300) };
    });
    suite.log('D.1 Wizard step 2: exactly one "Runtime" label element', rtStepInfo.found && rtStepInfo.runtimeLabelCount === 1,
      `labelCount=${rtStepInfo.runtimeLabelCount} text=${JSON.stringify(rtStepInfo.text.slice(0, 150))}`);
    suite.log('D.2 Wizard step 2: no raw i18n keys', rtStepInfo.found && rtStepInfo.foundRaw.length === 0, `found=${JSON.stringify(rtStepInfo.foundRaw)}`);
    suite.log('D.3 Wizard step 2: shows "Выбрать" + "Создать"', rtStepInfo.found && rtStepInfo.hasVybrat && rtStepInfo.hasSozdat,
      `vybrat=${rtStepInfo.hasVybrat} sozdat=${rtStepInfo.hasSozdat}`);
    await page.evaluate(() => { closeModal('wizard-modal'); });
    await page.waitForTimeout(200);

    // ═══ E: Wizard Launch step — contextual help + AutoStart composition ═══
    await page.evaluate(() => { openWizard(); });
    await page.waitForTimeout(500);
    await page.evaluate(() => {
      const existing = document.querySelector('input[name="wiz-rt-mode"][value="existing"]');
      if (existing) { existing.checked = true; onWizRtModeChange(); }
    });
    await page.waitForTimeout(200);
    await page.evaluate(() => {
      const items = document.querySelectorAll('#wiz-rt-dropdown .rt-dropdown-item');
      if (items.length > 0) { items[0].dispatchEvent(new MouseEvent('mousedown', { bubbles: true })); }
    });
    await page.waitForTimeout(200);
    await page.evaluate(() => { wizGoto(3); });
    await page.waitForTimeout(500);

    const launchInfo = await page.evaluate(() => {
      const panel = document.querySelector('.wizard-panel[data-panel="3"]');
      if (!panel) return { found: false };
      // DOM order: AutoStart < Environment < Args
      const autoRow = panel.querySelector('.form-autostart-row');
      const envGroup = panel.querySelector('#wiz-env-container') ? panel.querySelector('#wiz-env-container').closest('.form-group') : null;
      const argsGroup = document.getElementById('wiz-args') ? document.getElementById('wiz-args').closest('.form-group') : null;
      const autoTop = autoRow ? autoRow.getBoundingClientRect().top : -1;
      const envTop = envGroup ? envGroup.getBoundingClientRect().top : -1;
      const argsTop = argsGroup ? argsGroup.getBoundingClientRect().top : -1;
      const orderCorrect = autoTop >= 0 && envTop > autoTop && argsTop > envTop;
      // No permanent <small class="hint"> paragraphs
      const hints = panel.querySelectorAll('small.hint');
      const hintTexts = [...hints].map(h => h.textContent.trim());
      // No help/question icons
      const helpIcons = panel.querySelectorAll('.ctx-help');
      const questionMarks = [...panel.querySelectorAll('span')].filter(s => s.textContent.trim() === '?' && s.offsetWidth > 0);
      // AutoStart: toggle before label
      const autoGroup = panel.querySelector('.form-autostart');
      let toggleBeforeLabel = false;
      if (autoGroup) {
        const toggle = autoGroup.querySelector('.toggle-switch');
        const label = autoGroup.querySelector('.autostart-label');
        if (toggle && label) {
          toggleBeforeLabel = toggle.getBoundingClientRect().left < label.getBoundingClientRect().left;
        }
      }
      // Delay on same row + dark style
      const autoRect = autoRow ? autoRow.getBoundingClientRect() : null;
      const delayGroup = document.getElementById('wiz-delay-group');
      const cb = document.getElementById('wiz-autostart');
      if (cb && !cb.checked) { cb.checked = true; cb.dispatchEvent(new Event('change')); }
      const delayRect = delayGroup ? delayGroup.getBoundingClientRect() : null;
      const sameRow = autoRect && delayRect && Math.abs(autoRect.top - delayRect.top) < 40;
      // Delay compact + dark
      const delayInput = document.getElementById('wiz-autostart-delay');
      const row = panel.querySelector('.form-autostart-row');
      const delayInputR = delayInput ? delayInput.getBoundingClientRect() : null;
      const rowR = row ? row.getBoundingClientRect() : null;
      const delayCompact = delayInputR && rowR && rowR.width > 0 ? delayInputR.width < rowR.width * 0.6 : false;
      const delayStyle = delayInput ? getComputedStyle(delayInput) : null;
      const delayBg = delayStyle ? delayStyle.backgroundColor : '';
      const delayNotWhite = delayBg !== 'rgb(255, 255, 255)' && delayBg !== 'rgba(0, 0, 0, 0)' && delayBg !== 'transparent';
      const delayHasBorder = delayStyle && delayStyle.border && delayStyle.border !== 'none';
      // Args textarea still present
      const argsTa = document.getElementById('wiz-args');
      const argsPresent = argsTa && argsTa.offsetHeight > 0;
      // Environment container present
      const envContainer = document.getElementById('wiz-env-container');
      const envPresent = envContainer && envContainer.offsetHeight >= 0;
      return {
        found: true,
        orderCorrect,
        autoTop: Math.round(autoTop), envTop: Math.round(envTop), argsTop: Math.round(argsTop),
        hintCount: hints.length,
        hintTexts,
        helpIconCount: helpIcons.length,
        questionMarkCount: questionMarks.length,
        toggleBeforeLabel,
        sameRow,
        delayCompact,
        delayBg,
        delayNotWhite,
        delayHasBorder,
        argsPresent,
        envPresent
      };
    });
    suite.log('E.1 Launch step: block order is AutoStart → Environment → Args', launchInfo.found && launchInfo.orderCorrect,
      `autoTop=${launchInfo.autoTop} envTop=${launchInfo.envTop} argsTop=${launchInfo.argsTop}`);
    suite.log('E.2 Launch step: no permanent <small.hint> paragraphs', launchInfo.found && launchInfo.hintCount === 0,
      `count=${launchInfo.hintCount} texts=${JSON.stringify(launchInfo.hintTexts)}`);
    suite.log('E.3 Launch step: no visible help/question icons', launchInfo.found && launchInfo.helpIconCount === 0 && launchInfo.questionMarkCount === 0,
      `ctxHelp=${launchInfo.helpIconCount} questionMarks=${launchInfo.questionMarkCount}`);
    suite.log('E.4 Launch step: AutoStart toggle BEFORE label', launchInfo.found && launchInfo.toggleBeforeLabel, `toggleBeforeLabel=${launchInfo.toggleBeforeLabel}`);
    suite.log('E.5 Launch step: AutoStart + Delay on same row', launchInfo.found && launchInfo.sameRow, `sameRow=${launchInfo.sameRow}`);
    suite.log('E.6 Launch step: Delay input compact (not full-width)', launchInfo.found && launchInfo.delayCompact, `delayCompact=${launchInfo.delayCompact}`);
    suite.log('E.7 Launch step: Delay input dark (not white/native)', launchInfo.found && launchInfo.delayNotWhite && launchInfo.delayHasBorder,
      `bg=${launchInfo.delayBg} border=${launchInfo.delayHasBorder}`);
    suite.log('E.8 Launch step: Args textarea present and visible', launchInfo.found && launchInfo.argsPresent, `present=${launchInfo.argsPresent}`);
    suite.log('E.9 Launch step: Environment container present', launchInfo.found && launchInfo.envPresent, `present=${launchInfo.envPresent}`);
    await page.evaluate(() => { closeModal('wizard-modal'); });
    await page.waitForTimeout(200);

    // ═══ F: Pipeline editor visual contract ═══
    await page.evaluate(() => window.navigate('pipelines'));
    await page.waitForTimeout(400);
    await page.evaluate(() => {
      const btn = document.querySelector('#pipeline-list .icon-btn[onclick*="editPipeline"]');
      if (btn) btn.click();
    });
    await page.waitForTimeout(500);
    const pipeGeom = await page.evaluate(() => {
      const modal = document.getElementById('pipeline-modal');
      if (!modal || getComputedStyle(modal).display === 'none') return { modalOpen: false };
      const blocks = document.querySelectorAll('#pl-models-container .pl-block');
      if (blocks.length === 0) return { modalOpen: true, blocks: 0 };
      const b0 = blocks[0];
      const head = b0.querySelector('.pl-block-head');
      if (!head) return { modalOpen: true, blocks: blocks.length, head: false };
      const drag = b0.querySelector('.pl-drag');
      const step = b0.querySelector('.pl-step');
      const select = b0.querySelector('.pl-model-select');
      const up = b0.querySelector('.pl-act-up');
      const down = b0.querySelector('.pl-act-down');
      const remove = b0.querySelector('.pl-act-remove');
      const rect = el => el ? el.getBoundingClientRect() : null;
      const dragR = rect(drag), stepR = rect(step), selR = rect(select), upR = rect(up), downR = rect(down), remR = rect(remove);
      const ys = [dragR, stepR, selR, upR, downR, remR].filter(Boolean).map(r => r.top);
      const sameRow = ys.length >= 4 && (Math.max(...ys) - Math.min(...ys)) < 10;
      const dragVisible = dragR && dragR.width > 0 && dragR.height > 0 && getComputedStyle(drag).visibility !== 'hidden';
      const selStyle = select ? getComputedStyle(select) : null;
      const selBgColor = selStyle ? selStyle.backgroundColor : '';
      const removeSvg = remove ? remove.querySelector('svg') : null;
      const addBtn = document.querySelector('#pl-models-container .pl-add');
      const addText = addBtn ? addBtn.textContent.trim() : '';
      return {
        modalOpen: true, blocks: blocks.length,
        dragPresent: !!drag, dragVisible,
        stepPresent: !!step, selectPresent: !!select,
        upPresent: !!up, downPresent: !!down, removePresent: !!remove,
        sameRow, ys: ys.map(y => Math.round(y)),
        selBgColor,
        removeSvgPath: removeSvg ? removeSvg.innerHTML.slice(0, 80) : 'none',
        addText
      };
    });
    suite.log('F.1 Pipeline modal open', pipeGeom.modalOpen === true);
    if (pipeGeom.modalOpen && pipeGeom.blocks > 0) {
      suite.log('F.2 Drag handle present and visible', pipeGeom.dragPresent && pipeGeom.dragVisible);
      suite.log('F.3 Header row: all 6 elements in one visual row', pipeGeom.sameRow, `ys=${JSON.stringify(pipeGeom.ys)}`);
      suite.log('F.4 Model select has dark background', pipeGeom.selBgColor !== 'rgb(255, 255, 255)' && pipeGeom.selBgColor !== 'rgba(0, 0, 0, 0)', `bg=${pipeGeom.selBgColor}`);
      suite.log('F.5 Add button text is "Добавить модель" (no "+")', pipeGeom.addText === 'Добавить модель', `text=${JSON.stringify(pipeGeom.addText)}`);
      const isTrashLike = pipeGeom.removeSvgPath && !pipeGeom.removeSvgPath.match(/M6 18L18 6|M18 6L6 18/);
      suite.log('F.6 Delete button is trash icon (not red X)', isTrashLike, `svg=${pipeGeom.removeSvgPath}`);
    }
    await page.evaluate(() => { closeModal('pipeline-modal'); });
    await page.waitForTimeout(200);

    // ═══ G: i18n round-trip RU -> EN -> RU ═══
    await page.evaluate(() => window.navigate('models'));
    await page.waitForTimeout(300);
    // Switch to EN
    await page.evaluate(() => { setLanguage('en'); });
    await page.waitForTimeout(500);
    const enCheck = await page.evaluate(() => {
      const sidebarLabels = [...document.querySelectorAll('.sidebar-nav .nav-item span[data-i18n]')].map(s => s.textContent.trim());
      const rawKeys = document.body.innerText.match(/[a-z]+\.[a-z_]+\.[a-z_]+/g) || [];
      return { sidebarLabels, rawKeys: rawKeys.slice(0, 5) };
    });
    suite.log('G.1 EN switch: sidebar labels are English', enCheck.sidebarLabels.some(l => /Models|Pipelines|Logs|History/i.test(l)),
      `labels=${JSON.stringify(enCheck.sidebarLabels)}`);
    suite.log('G.2 EN switch: no raw i18n keys in body', enCheck.rawKeys.length === 0, `keys=${JSON.stringify(enCheck.rawKeys)}`);
    // Switch back to RU
    await page.evaluate(() => { setLanguage('ru'); });
    await page.waitForTimeout(500);
    const ruCheck = await page.evaluate(() => {
      const sidebarLabels = [...document.querySelectorAll('.sidebar-nav .nav-item span[data-i18n]')].map(s => s.textContent.trim());
      const rawKeys = document.body.innerText.match(/[a-z]+\.[a-z_]+\.[a-z_]+/g) || [];
      return { sidebarLabels, rawKeys: rawKeys.slice(0, 5) };
    });
    suite.log('G.3 RU switch back: sidebar labels are Russian', ruCheck.sidebarLabels.some(l => /Модели|Пайплайны|Логи|История/.test(l)),
      `labels=${JSON.stringify(ruCheck.sidebarLabels)}`);
    suite.log('G.4 RU switch back: no raw i18n keys in body', ruCheck.rawKeys.length === 0, `keys=${JSON.stringify(ruCheck.rawKeys)}`);

    // ═══ H: Sidebar icons survive i18n switch ═══
    const sidebarAfter = await page.evaluate(() => {
      const items = document.querySelectorAll('.sidebar-nav .nav-item');
      let allOk = true;
      items.forEach(item => {
        const svg = item.querySelector('svg.nav-icon');
        if (!svg) { allOk = false; return; }
        const r = svg.getBoundingClientRect();
        if (r.width <= 0 || r.height <= 0) allOk = false;
      });
      return { count: items.length, allOk };
    });
    suite.log('H.1 Sidebar icons survive RU->EN->RU round-trip', sidebarAfter.allOk, `count=${sidebarAfter.count} allOk=${sidebarAfter.allOk}`);

    // ═══ Sanity ═══
    suite.log('S.1 No unexpected console errors', suite.consoleErrors.length === 0, suite.consoleErrors.slice(0, 3).join('; ') || 'clean');
    suite.log('S.2 No server 5xx errors', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));

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

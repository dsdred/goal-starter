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

// orphanStripInPage reads the ADR 023 UX V-2.1 T-4′ facts off the first My
// Models row. The orphan hint is no longer a visible sentence: the strip must be
// text-free and carry exactly one info control whose tooltip AND accessible name
// are the dictionary hint, with no native title duplicating it anywhere in the row.
function orphanStripInPage(hint) {
  const row = document.querySelector('#model-list .model-row');
  if (!row) return null;
  const strip = row.querySelector('.model-row-actions');
  if (!strip) return null;
  const triggers = [...strip.querySelectorAll('[data-tooltip]')].filter(el => el.getAttribute('aria-label') === hint);
  const el = triggers[0] || null;
  const r = el ? el.getBoundingClientRect() : null;
  const buttons = [...strip.querySelectorAll('button')];
  return {
    text: (strip.textContent || '').trim(),
    hintTextEls: strip.querySelectorAll('.hint-text').length,
    transitional: !!row.querySelector('.model-transitional'),
    marker: !!row.querySelector('.model-progress'),
    triggerCount: triggers.length,
    tag: el ? el.tagName : null,
    type: el ? el.getAttribute('type') : null,
    tooltip: el ? el.getAttribute('data-tooltip') : null,
    ariaLabel: el ? el.getAttribute('aria-label') : null,
    box: r ? { w: Math.round(r.width * 100) / 100, h: Math.round(r.height * 100) / 100 } : null,
    rowTitles: [...row.querySelectorAll('[title]')].map(n => n.getAttribute('title')),
    order: buttons.map(b => (b.getAttribute('aria-label') || '').trim()),
    infoIndex: el ? buttons.indexOf(el) : -1,
  };
}

// tipState reads the single canonical tooltip. `count` is the duplicate check:
// the project has exactly one #goaltip element, never one per trigger.
function tipState() {
  const el = document.getElementById('goaltip');
  return {
    count: document.querySelectorAll('#goaltip').length,
    visible: !!el && el.classList.contains('visible'),
    text: el ? el.textContent : null,
    ariaHidden: el ? el.getAttribute('aria-hidden') : null,
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

// togglePair performs two activations on ONE DOM node and reports every state
// around them. Since C-4 (a) makes touch the only toggling modality, this helper
// serves the touch legs. The models list is rebuilt through innerHTML every
// 3000 ms (BACKLOG ADR 022 F-3, deliberately out of this design's scope); a
// rebuild between the activations hides the tooltip through focusout and re-arms
// the latch on a fresh node, so a locator re-resolved between the two taps would
// measure the rebuild instead of the control. A detached node therefore
// invalidates the pair and it is re-run — bounded, and the attempt count is
// printed, so a list that rebuilt continuously would fail loudly rather than
// pass quietly.
async function togglePair(page, locator, state, act, between) {
  for (let attempt = 1; attempt <= 3; attempt++) {
    const handle = await locator.elementHandle();
    if (!handle) return { missing: true };
    try {
      await act(handle);
      const first = await page.evaluate(state);
      const describedFirst = await handle.evaluate(n => n.getAttribute('aria-describedby'));
      const middle = between ? await between() : null;
      await act(handle);
      const second = await page.evaluate(state);
      const describedSecond = await handle.evaluate(n => n.getAttribute('aria-describedby'));
      if (!(await handle.evaluate(n => n.isConnected))) continue;
      return { attempt, first, describedFirst, middle, second, describedSecond };
    } catch (e) { /* node detached mid-pair: the rebuild won the race, re-run */ }
  }
  return { stale: true };
}

// hoverFlow drives the pointer path on ONE DOM node — enter, a click while the
// tooltip is shown, leave — and reports the tooltip state and the control's
// aria-describedby after each step. It is the negative control for C-4: under
// the superseded activation model that click hid the hint, and under a
// visibility-based toggle the first click would have dismissed the hover
// reveal. Same detached-node guard as togglePair (the 3000 ms rebuild).
async function hoverFlow(page, locator, state) {
  for (let attempt = 1; attempt <= 3; attempt++) {
    const handle = await locator.elementHandle();
    if (!handle) return { missing: true };
    try {
      await handle.hover();
      const enter = await page.evaluate(state);
      const describedEnter = await handle.evaluate(n => n.getAttribute('aria-describedby'));
      await handle.click();
      const click = await page.evaluate(state);
      const describedClick = await handle.evaluate(n => n.getAttribute('aria-describedby'));
      await page.locator('#model-list .model-row-name').first().hover();
      const leave = await page.evaluate(state);
      const describedLeave = await handle.evaluate(n => n.getAttribute('aria-describedby'));
      if (!(await handle.evaluate(n => n.isConnected))) continue;
      return { attempt, enter, describedEnter, click, describedClick, leave, describedLeave };
    } catch (e) { /* node detached mid-sequence: the rebuild won the race, re-run */ }
  }
  return { stale: true };
}

// staleArmFlow reproduces the S-3 state: a touch pointerup that never produces a
// click leaves the touch-activation flag armed. The pointerup is dispatched
// synthetically — no real gesture reliably yields "pointerup without click" —
// while the assertion is on public tooltip behaviour, not on the flag: after the
// stale arm, Enter must stay inert, so a hover-leave must still hide the hint.
// The observable fingerprint of an Enter that wrongly acts is the latch: it
// becomes true, and the mouseout early-return then keeps the hint visible.
async function staleArmFlow(page, locator, state) {
  for (let attempt = 1; attempt <= 3; attempt++) {
    const handle = await locator.elementHandle();
    if (!handle) return { missing: true };
    try {
      await handle.hover();
      const hovered = await page.evaluate(state);
      await handle.click();
      const focused = await handle.evaluate(n => {
        if (n.ownerDocument.activeElement !== n) n.focus();
        return n.ownerDocument.activeElement === n;
      });
      await handle.evaluate(n => n.dispatchEvent(new PointerEvent('pointerup', { bubbles: true, pointerType: 'touch' })));
      await page.keyboard.press('Enter');
      const afterEnter = await page.evaluate(state);
      await page.locator('#model-list .model-row-name').first().hover();
      const afterLeave = await page.evaluate(state);
      if (!(await handle.evaluate(n => n.isConnected))) continue;
      return { attempt, hovered, focused, afterEnter, afterLeave };
    } catch (e) { /* node detached mid-sequence: the rebuild won the race, re-run */ }
  }
  return { stale: true };
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
    // ═══ ADR 023 UX V-2.1 T-4′ — the hint is an accessible info control (RU) ═══
    // Replaces the two `strip.textContent === hint` legs of ADR 023 T-4, which
    // described the superseded presentation. Every expected string is read from
    // the shipped dictionary; T-2 and the Instances title legs are the controls
    // proving a broken dictionary cannot make these legs pass vacuously.
    const ruHint = I18N.ru['instances.orphan.hint'];
    const stripRU = await pageRU.evaluate(orphanStripInPage, ruHint);
    suite.log('T-4′ RU Models: the action strip is text-free (no hint sentence, no .hint-text, no marker)',
      !!stripRU && stripRU.text === '' && stripRU.hintTextEls === 0 &&
        stripRU.transitional === false && stripRU.marker === false,
      stripRU ? `strip_text=${JSON.stringify(stripRU.text)} hint_text_els=${stripRU.hintTextEls} transitional=${stripRU.transitional} marker=${stripRU.marker}` : 'model row absent');
    suite.log('T-4′ RU Models: exactly one info BUTTON carries the hint as tooltip + accessible name, >=44x44',
      !!stripRU && stripRU.triggerCount === 1 && stripRU.tag === 'BUTTON' && stripRU.type === 'button' &&
        stripRU.tooltip === ruHint && stripRU.ariaLabel === ruHint && !!stripRU.box &&
        stripRU.box.w >= 44 && stripRU.box.h >= 44,
      stripRU ? `triggers=${stripRU.triggerCount} tag=${stripRU.tag} type=${stripRU.type} tooltip=${JSON.stringify(stripRU.tooltip)} box=${stripRU.box ? stripRU.box.w + 'x' + stripRU.box.h : 'null'}` : 'model row absent');
    suite.log('T-4′ RU Models: no native title in the row carries the hint (the badge title is gone)',
      !!stripRU && !stripRU.rowTitles.includes(ruHint),
      stripRU ? `row_titles=${JSON.stringify(stripRU.rowTitles)}` : 'model row absent');
    suite.log('T-4′ RU Models: Logs/Edit/Delete keep their order after the info control',
      !!stripRU && stripRU.order.length === 4 && stripRU.infoIndex === 0 &&
        stripRU.order.slice(1).join('|') === [I18N.ru['models.actions.logs'], I18N.ru['models.actions.edit'], I18N.ru['models.actions.delete']].join('|'),
      stripRU ? `order=${JSON.stringify(stripRU.order)}` : 'model row absent');
    await H.screenshot(pageRU, ws, 'orphan_models_ru');

    // Keyboard first, from a clean focus origin: Tab must reach the control, focus
    // must reveal the hint through the canonical focusin path, :focus-visible must
    // paint the shared outline, Enter and Space must perform no tooltip action,
    // and the next Tab must follow the DOM order (info -> Logs) while the single
    // tooltip retargets instead of duplicating.
    await pageRU.evaluate(() => { if (document.activeElement && document.activeElement.blur) document.activeElement.blur(); });
    let tabHits = 0, reached = false;
    for (; tabHits < 160; tabHits++) {
      await pageRU.keyboard.press('Tab');
      reached = await pageRU.evaluate(hint => {
        const a = document.activeElement;
        return !!a && a.tagName === 'BUTTON' && a.getAttribute('aria-label') === hint;
      }, ruHint);
      if (reached) break;
    }
    const focusRU = await pageRU.evaluate(() => {
      const a = document.activeElement;
      const cs = getComputedStyle(a);
      return { visible: document.getElementById('goaltip').classList.contains('visible'),
        text: document.getElementById('goaltip').textContent, described: a.getAttribute('aria-describedby'),
        outlineStyle: cs.outlineStyle, outlineWidth: cs.outlineWidth, outlineColor: cs.outlineColor };
    });
    suite.log('T-4′ RU keyboard: Tab reaches the info control, focus shows the hint, focus-visible paints the shared outline',
      reached && focusRU.visible && focusRU.text === ruHint && focusRU.described === 'goaltip' &&
        focusRU.outlineStyle === 'solid' && parseFloat(focusRU.outlineWidth) >= 2,
      `tab_presses=${tabHits + 1} reached=${reached} visible=${focusRU.visible} described_by=${focusRU.described} outline=${focusRU.outlineStyle}+${focusRU.outlineWidth}+${focusRU.outlineColor}`);
    await pageRU.keyboard.press('Enter');
    await pageRU.keyboard.press('Space');
    const keysRU = await pageRU.evaluate(tipState);
    suite.log('T-4′ RU keyboard: Enter and Space perform no tooltip action — the focused hint stays visible',
      keysRU.visible && keysRU.text === ruHint && keysRU.count === 1,
      `visible=${keysRU.visible} text=${JSON.stringify(keysRU.text)} tooltips=${keysRU.count}`);
    await pageRU.keyboard.press('Tab');
    const nextStopRU = await pageRU.evaluate(() => {
      const a = document.activeElement;
      return { label: a ? a.getAttribute('aria-label') : null,
        visible: document.getElementById('goaltip').classList.contains('visible'),
        text: document.getElementById('goaltip').textContent, count: document.querySelectorAll('#goaltip').length };
    });
    suite.log('T-4′ RU keyboard: Tab moves to Logs in DOM order and retargets the one tooltip (no duplicate)',
      nextStopRU.label === I18N.ru['models.actions.logs'] && nextStopRU.visible &&
        nextStopRU.text === I18N.ru['models.actions.logs'] && nextStopRU.count === 1,
      `next=${JSON.stringify(nextStopRU.label)} tip=${JSON.stringify(nextStopRU.text)} tooltips=${nextStopRU.count}`);

    // Blur is the keyboard counterpart of pointer leave: Shift+Tab returns to the
    // control, focus shows the hint again through focusin, and blurring it must
    // hide the tooltip and clear aria-describedby.
    await pageRU.keyboard.down('Shift');
    await pageRU.keyboard.press('Tab');
    await pageRU.keyboard.up('Shift');
    const backRU = await pageRU.evaluate(() => {
      const a = document.activeElement;
      return { label: a ? a.getAttribute('aria-label') : null,
        visible: document.getElementById('goaltip').classList.contains('visible') };
    });
    const blurRU = await pageRU.evaluate(() => {
      const a = document.activeElement;
      if (a && a.blur) a.blur();
      const tip = document.getElementById('goaltip');
      return { visible: tip.classList.contains('visible'), ariaHidden: tip.getAttribute('aria-hidden'),
        describedBy: a ? a.getAttribute('aria-describedby') : 'none' };
    });
    suite.log('T-4′ RU keyboard: focus shows the hint again, blur hides it and clears aria-describedby',
      backRU.label === ruHint && backRU.visible && !blurRU.visible && blurRU.describedBy === null,
      `back=${JSON.stringify(backRU.label)} shown=${backRU.visible} after_blur=${blurRU.visible} aria_hidden=${blurRU.ariaHidden} described_by=${JSON.stringify(blurRU.describedBy)}`);

    // Mouse: hover owns the tooltip. Pointer enter shows it, a click while it is
    // shown is inert — it neither shows nor hides — and pointer leave hides it
    // and clears aria-describedby.
    const infoBtnRU = pageRU.locator('#model-list .model-row-actions button[data-tooltip]').first();
    const mouseRU = await hoverFlow(pageRU, infoBtnRU, tipState);
    const enterRU = mouseRU.enter || {};
    const clickRU = mouseRU.click || {};
    const leaveRU = mouseRU.leave || {};
    suite.log('T-4′ RU mouse: pointer enter shows the hint, the click neither shows nor hides it, pointer leave hides it',
      !mouseRU.missing && !mouseRU.stale && enterRU.visible && enterRU.text === ruHint &&
        mouseRU.describedEnter === 'goaltip' && clickRU.visible && clickRU.text === ruHint &&
        mouseRU.describedClick === 'goaltip' && !leaveRU.visible && leaveRU.ariaHidden === 'true' &&
        mouseRU.describedLeave === null && enterRU.count === 1,
      mouseRU.enter
        ? `attempts=${mouseRU.attempt} enter=${enterRU.visible}/${JSON.stringify(enterRU.text)} described=${mouseRU.describedEnter} click=${clickRU.visible} described_after_click=${mouseRU.describedClick} leave=${leaveRU.visible} described_after_leave=${mouseRU.describedLeave} tooltips=${enterRU.count}`
        : `flow invalid: ${JSON.stringify(mouseRU)}`);
    await pageRU.evaluate(() => { if (document.activeElement && document.activeElement.blur) document.activeElement.blur(); });

    // S-3 (2026-10-28): a touch pointerup that produced no click must not turn
    // the next keyboard activation into a tooltip action. Enter has no pointer,
    // so only the stale flag could arm it — see staleArmFlow for the fingerprint.
    const staleRU = await staleArmFlow(pageRU, infoBtnRU, tipState);
    const staleEnter = staleRU.afterEnter || {};
    const staleLeave = staleRU.afterLeave || {};
    suite.log('T-4′ RU keyboard: a touch pointerup with no click does not turn a later Enter into a tooltip action (S-3)',
      !staleRU.missing && !staleRU.stale && (staleRU.hovered || {}).visible === true && staleRU.focused === true &&
        staleEnter.visible === true && staleLeave.visible === false && staleLeave.count === 1,
      staleRU.afterEnter
        ? `attempts=${staleRU.attempt} hovered=${(staleRU.hovered || {}).visible} focused=${staleRU.focused} after_enter=${staleEnter.visible} after_leave=${staleLeave.visible} tooltips=${staleLeave.count}`
        : `flow invalid: ${JSON.stringify(staleRU)}`);
    await pageRU.evaluate(() => { if (document.activeElement && document.activeElement.blur) document.activeElement.blur(); });

    // Touch: there is no hover, so the tap is the reveal and the only dismissal
    // is a second tap on the same control (Owner-ratified C-4 (a)). The reveal
    // must outlive the trailing synthetic mouseout Chromium emits after a tap.
    // A separate hasTouch context keeps the desktop context's modality untouched.
    const ctxT = await browser.newContext({ viewport: { width: 1280, height: 800 }, locale: 'ru-RU', hasTouch: true });
    const pageT = await ctxT.newPage();
    suite.watchPage(pageT);
    await H.login(pageT, BASE, ADMIN_USER, ADMIN_PASS);
    await pageT.evaluate(() => window.navigate('models'));
    await pageT.waitForTimeout(600);
    const infoBtnT = pageT.locator('#model-list .model-row-actions button[data-tooltip]').first();
    const touchRU = await togglePair(
      pageT, infoBtnT, tipState,
      h => h.tap(),
      async () => { await pageT.waitForTimeout(400); return pageT.evaluate(tipState); });
    const tap1 = touchRU.first || {};
    const tap1b = touchRU.middle || {};
    const tap2 = touchRU.second || {};
    await ctxT.close();
    suite.log('T-4′ RU touch: one tap shows the hint (it survives), a second tap hides it',
      !!touchRU.first && tap1.visible && tap1.text === ruHint && touchRU.describedFirst === 'goaltip' &&
        tap1b.visible && !tap2.visible && tap2.count === 1 && touchRU.describedSecond === null,
      touchRU.first
        ? `attempts=${touchRU.attempt} tap1=${tap1.visible} tap1+400ms=${tap1b.visible} tap2=${tap2.visible} described=${touchRU.describedFirst} described_after=${touchRU.describedSecond} tooltips=${tap2.count}`
        : `pair invalid: ${JSON.stringify(touchRU)}`);

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
    // ADR 023 UX V-2.1 T-4′ in EN (D-11: RU and EN move together) — the same
    // text-free strip, one info control, one hint, dictionary-derived strings.
    const enHint = I18N.en['instances.orphan.hint'];
    const stripEN = await pageRU.evaluate(orphanStripInPage, enHint);
    suite.log('T-4′ EN Models: the action strip is text-free and carries one info BUTTON with the EN hint',
      !!stripEN && stripEN.text === '' && stripEN.hintTextEls === 0 && stripEN.marker === false &&
        stripEN.triggerCount === 1 && stripEN.tag === 'BUTTON' && stripEN.type === 'button' &&
        stripEN.tooltip === enHint && stripEN.ariaLabel === enHint && !!stripEN.box &&
        stripEN.box.w >= 44 && stripEN.box.h >= 44 && !stripEN.rowTitles.includes(enHint) &&
        stripEN.order.length === 4 && stripEN.infoIndex === 0 &&
        stripEN.order.slice(1).join('|') === [I18N.en['models.actions.logs'], I18N.en['models.actions.edit'], I18N.en['models.actions.delete']].join('|'),
      stripEN ? `strip_text=${JSON.stringify(stripEN.text)} triggers=${stripEN.triggerCount} tooltip=${JSON.stringify(stripEN.tooltip)} box=${stripEN.box ? stripEN.box.w + 'x' + stripEN.box.h : 'null'} order=${JSON.stringify(stripEN.order)} titles=${JSON.stringify(stripEN.rowTitles)}` : 'model row absent');
    const infoBtnEN = pageRU.locator('#model-list .model-row-actions button[data-tooltip]').first();
    const mouseEN = await hoverFlow(pageRU, infoBtnEN, tipState);
    const enterEN = mouseEN.enter || {};
    const clickEN = mouseEN.click || {};
    const leaveEN = mouseEN.leave || {};
    suite.log('T-4′ EN mouse: pointer enter shows the EN hint, the click is inert, pointer leave hides it',
      !mouseEN.missing && !mouseEN.stale && enterEN.visible && enterEN.text === enHint &&
        mouseEN.describedEnter === 'goaltip' && clickEN.visible && clickEN.text === enHint &&
        !leaveEN.visible && mouseEN.describedLeave === null && leaveEN.count === 1,
      mouseEN.enter
        ? `attempts=${mouseEN.attempt} enter=${enterEN.visible}/${JSON.stringify(enterEN.text)} click=${clickEN.visible} leave=${leaveEN.visible} described=${mouseEN.describedEnter}/${mouseEN.describedLeave} tooltips=${leaveEN.count}`
        : `flow invalid: ${JSON.stringify(mouseEN)}`);
    await pageRU.evaluate(() => { if (document.activeElement && document.activeElement.blur) document.activeElement.blur(); });
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

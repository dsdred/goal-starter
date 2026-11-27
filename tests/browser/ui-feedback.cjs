'use strict';
// UI-FEEDBACK-01 — semantic UI feedback contract (Owner-approved design
// amendments A–G). This suite locks the presentation contract:
//   • the canonical contextual info button (goaltip-exclusive, real button,
//     hover/focus/touch, Escape + outside-click dismissal, click toggle);
//   • toast live regions (polite routine/success/warning, assertive errors,
//     no focus theft);
//   • the ADR 027 D-9 copy-env notice as a NEUTRAL info notice (never a
//     warning presentation), exact EN/RU text preserved;
//   • the Resources RSS explanation as an info button carrying the note
//     verbatim while the note text stays visible;
//   • theme-token hygiene (orphan badge token; warning vs info notices).
// No backend, measurement or i18n key semantics are exercised here beyond
// presentation and the exact notice strings.
const path = require('path');
const H = require('./harness.cjs');

const PORT = 19488;
const BASE = `http://127.0.0.1:${PORT}`;
const sleep = ms => new Promise(r => setTimeout(r, ms));

// ── resources stub (same wire shape as resources.cjs) ────────────────────
const R = (value, unit, provenance, source, reason) => {
  const r = { value, unit, provenance, source: source || 'fixture' };
  if (reason) r.reason = reason;
  return r;
};
const RSS_FIX = {
  state: 'ok', devices: [],
  system: {
    ram: { total: R(34359738368, 'bytes', 'measured', 'sys'), available: R(17179869184, 'bytes', 'measured', 'sys'), used: R(17179869184, 'bytes', 'estimated', 'sys') },
    cpu: { logical_cores: R(16, 'count', 'measured', 'sys'), utilization: R(23, 'percent', 'measured', 'sys') },
  },
  processes: [{
    id: 'inst-uf', model_id: 'm-uf', runtime_id: 'rt-uf', pipeline_id: null, pipeline_entry_id: null,
    state: 'running', pid: 6001,
    resources: { process_rss: R(1073741824, 'bytes', 'measured', 'fixture') },
  }],
  capabilities: {}, sampled_at: '2026-11-27T10:00:00Z', errors: [],
};

module.exports = { BASE };

async function main() {
  const ws = H.makeWorkspace('ui-feedback');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  H.writeConfig(ws, { webPort: PORT, runtimes: [{ id: 'rt-uf', name: 'RT UF', executable: fakeRt, workingDirectory: path.dirname(fakeRt), environment: { RT_ENV_KEY: 'v' } }] });
  const server = H.startServer(goalBin, ws);
  if (!await H.waitForHealth(BASE)) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started (auth OFF).\n');

  // Seed: one runtime copy source with env keys + one model copy source with env keys.
  const seed = await H.api(BASE, 'POST', '/api/v1/models', {
    name: 'UF Model', runtime_id: 'rt-uf', environment: { UF_KEY: 'x' },
  });
  if (!seed.status || !seed.data || !seed.data.id) {
    console.error('Failed to seed model:', JSON.stringify(seed));
    await server.stop();
    process.exit(1);
  }

  const browser = await H.launchBrowser();
  // Wide viewport: the model list must stay in table mode so the icon strip
  // (and its .ctx-info copy trigger) is visible and stable throughout.
  const ctx = await browser.newContext({ colorScheme: 'dark', viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('UI FEEDBACK (UI-FEEDBACK-01 semantic feedback contract)');
  suite.watchPage(page);
  let ok = false;
  try {
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(600);
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(500);

    // ═══ SECTION 1: canonical info button + goaltip dismissal contract ═══
    const infoBtn = page.locator('#model-list button.ctx-info[onclick*="copyModel"]').first();
    const ib = await infoBtn.evaluate(el => ({
      tag: el.tagName,
      type: el.getAttribute('type'),
      title: el.hasAttribute('title'),
      tip: el.getAttribute('data-tooltip'),
      aria: el.getAttribute('aria-label'),
    }));
    suite.log('1.1 info button is a real <button type="button">', ib.tag === 'BUTTON' && ib.type === 'button', JSON.stringify(ib));
    suite.log('1.2 no native title attribute (single canonical tooltip)', ib.title === false, `title=${ib.title}`);
    suite.log('1.3 localized accessible name = tooltip text', ib.tip === 'Copy' && ib.aria === 'Copy', `tip=${JSON.stringify(ib.tip)}`);

    await infoBtn.hover();
    await page.waitForTimeout(250);
    const tipVisible = await page.evaluate(() => {
      const t = document.getElementById('goaltip');
      return { visible: t ? t.classList.contains('visible') : false, text: t ? t.textContent : '', hidden: t ? t.getAttribute('aria-hidden') : null };
    });
    suite.log('1.4 hover shows the canonical #goaltip', tipVisible.visible && tipVisible.text === 'Copy' && tipVisible.hidden === 'false', JSON.stringify(tipVisible));

    // The 3 s model refresh re-renders the row, so every focus/click re-resolves
    // the trigger and the focus probe queries the live DOM.
    const copyBtn = () => page.locator('#model-list button.ctx-info[onclick*="copyModel"]').first();
    const focusProbe = () => page.evaluate(() => ({
      tip: document.getElementById('goaltip').classList.contains('visible'),
      focusOnTrigger: !!document.querySelector('#model-list button.ctx-info[onclick*="copyModel"]')
        && document.activeElement === document.querySelector('#model-list button.ctx-info[onclick*="copyModel"]'),
    }));

    // Focus the trigger; Escape dismisses without moving focus.
    await copyBtn().focus();
    await page.waitForTimeout(150);
    suite.log('1.5 focus shows the tooltip', (await focusProbe()).tip);
    await page.keyboard.press('Escape');
    await page.waitForTimeout(150);
    const afterEsc = await focusProbe();
    suite.log('1.6 Escape hides the tooltip', !afterEsc.tip, JSON.stringify(afterEsc));
    suite.log('1.7 Escape does not steal/move focus (trigger keeps it)', afterEsc.focusOnTrigger);

    // Focus alone re-shows (focus must actually move in); outside click
    // dismisses it.
    await page.evaluate(() => document.activeElement.blur());
    await copyBtn().focus();
    await page.waitForTimeout(200);
    suite.log('1.8 focus re-shows the tooltip after dismissal', (await focusProbe()).tip);
    await page.mouse.click(30, 300);
    await page.waitForTimeout(200);
    suite.log('1.9 outside click dismisses the tooltip', !(await focusProbe()).tip);
    // Safety: if the outside click ever lands inside an open modal, close it so
    // the overlay cannot hide the model list for the toggle leg.
    await page.evaluate(() => { if (document.getElementById('wizard-modal').style.display !== 'none') window.closeWizard(); });
    await page.waitForTimeout(300);

    // Click-to-toggle on the .ctx-info trigger. The copy action itself also
    // opens the wizard (closed out afterwards); the click is dispatched on the
    // live element in one evaluate, because the 3 s model refresh re-renders
    // the row and a Playwright action click would race the re-render.
    const domClickCopy = () => page.evaluate(() => {
      const el = document.querySelector('#model-list button.ctx-info[onclick*="copyModel"]');
      if (el) el.click();
      return !!el;
    });
    await domClickCopy();
    await page.waitForTimeout(200);
    const toggled = (await focusProbe()).tip;
    await page.evaluate(() => window.closeWizard());
    await page.waitForTimeout(300);
    await domClickCopy();
    await page.waitForTimeout(200);
    const toggledOff = (await focusProbe()).tip;
    await page.evaluate(() => window.closeWizard());
    await page.waitForTimeout(300);
    suite.log('1.10 click toggles the tooltip on and off', toggled && !toggledOff, `on=${toggled} off=${!toggledOff}`);
    await H.screenshot(page, ws, '01-info-button-tooltip');

    // ═══ SECTION 2: toast live regions ═══
    const containers = await page.evaluate(() => {
      const polite = document.getElementById('toast-container');
      const errors = document.getElementById('toast-error-container');
      return {
        polite: polite ? { role: polite.getAttribute('role'), live: polite.getAttribute('aria-live') } : null,
        errors: errors ? { role: errors.getAttribute('role'), live: errors.getAttribute('aria-live') } : null,
      };
    });
    suite.log('2.1 routine container is a polite status live region',
      containers.polite && containers.polite.role === 'status' && containers.polite.live === 'polite', JSON.stringify(containers));
    suite.log('2.2 error container is an assertive alert live region',
      containers.errors && containers.errors.role === 'alert' && containers.errors.live === 'assertive', JSON.stringify(containers.errors));

    await page.evaluate(() => { document.activeElement.blur(); window.showToast('UF OK', 'success'); window.showToast('UF FAIL', 'error'); });
    await page.waitForTimeout(300);
    const toasts = await page.evaluate(() => {
      const list = id => Array.from(document.getElementById(id).children).map(c => ({ cls: c.className, text: c.textContent }));
      return { polite: list('toast-container'), errors: list('toast-error-container'), focus: document.activeElement && document.activeElement.tagName };
    });
    suite.log('2.3 success toast lands in the polite container', toasts.polite.length === 1 && toasts.polite[0].text === 'UF OK' && /toast success/.test(toasts.polite[0].cls), JSON.stringify(toasts.polite));
    suite.log('2.4 error toast lands in the assertive container, not the polite one', toasts.errors.length === 1 && toasts.errors[0].text === 'UF FAIL' && /toast error/.test(toasts.errors[0].cls) && toasts.polite.every(t => t.text !== 'UF FAIL'), JSON.stringify(toasts.errors));
    suite.log('2.5 toast announcement does not move keyboard focus', toasts.focus === 'BODY', `focus=${toasts.focus}`);
    await H.screenshot(page, ws, '02-toast-live-regions');
    await sleep(4300);

    // ═══ SECTION 3: D-9 copy notice is a neutral info notice ═══
    const rtRow = await (await H.api(BASE, 'GET', '/api/v1/runtimes')).data.find(r => r.id === 'rt-uf');
    await page.evaluate(() => window.navigate('adv-runtimes'));
    await page.waitForTimeout(600);
    await page.evaluate(id => window.copyRuntime(id), rtRow.id);
    await page.waitForTimeout(400);
    const noticeEn = await page.evaluate(() => {
      const n = document.querySelector('.copy-env-notice');
      if (!n) return null;
      const cs = getComputedStyle(n);
      return { cls: n.className, text: n.textContent, borderLeft: cs.borderLeftColor, isWarning: n.classList.contains('notice-warning') };
    });
    suite.log('3.1 copy notice present with exact EN text',
      !!noticeEn && noticeEn.text === 'Environment variable values are not copied. Re-enter them if required.', JSON.stringify(noticeEn && noticeEn.text));
    suite.log('3.2 copy notice is the info variant (neutral, never warning)',
      !!noticeEn && /notice-info/.test(noticeEn.cls) && !noticeEn.isWarning, `cls=${noticeEn && noticeEn.cls}`);
    const infoTokenEn = await page.evaluate(() => getComputedStyle(document.body).getPropertyValue('--info').trim());
    suite.log('3.3 copy notice accent is the theme --info token', !!noticeEn && noticeEn.borderLeft === rgbOf(infoTokenEn), `left=${noticeEn && noticeEn.borderLeft} info=${infoTokenEn}`);
    await page.click('#create-runtime-modal .modal-close');
    await page.waitForTimeout(300);
    await H.screenshot(page, ws, '03-copy-env-notice-info');

    // RU text + neutral presentation.
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(700);
    await page.evaluate(id => window.copyRuntime(id), rtRow.id);
    await page.waitForTimeout(400);
    const noticeRu = await page.evaluate(() => {
      const n = document.querySelector('.copy-env-notice');
      return n ? { cls: n.className, text: n.textContent } : null;
    });
    suite.log('3.4 RU copy notice exact text, info variant',
      !!noticeRu && noticeRu.text === 'Значения переменных окружения не копируются. При необходимости заполните их заново.' && /notice-info/.test(noticeRu.cls),
      JSON.stringify(noticeRu && noticeRu.text));
    await page.click('#create-runtime-modal .modal-close');
    await page.waitForTimeout(300);

    // Manual create shows no notice (D-9: only copies with env keys).
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(500);
    await page.evaluate(() => window.showCreateRuntimeModal());
    await page.waitForTimeout(400);
    const manualNotice = await page.evaluate(() => !!document.querySelector('.copy-env-notice'));
    suite.log('3.5 manual Runtime create shows no copy notice', !manualNotice);
    await page.click('#create-runtime-modal .modal-close').catch(() => {});
    await page.waitForTimeout(300);

    // ═══ SECTION 4: Resources RSS explanation — info button beside the
    // process RAM section label; the note lives in the tooltip only (no
    // standalone visible paragraph) ═══
    const RES_URL = '**/api/v1/system/resources';
    await page.route(RES_URL, route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(RSS_FIX) }));
    await page.evaluate(() => window.navigate('resources'));
    await page.waitForTimeout(800);
    await page.evaluate(() => window.loadResources());
    const rss = await page.waitForFunction(() => {
      const btn = document.querySelector('#resources-content .res-section-title .ctx-info.res-proc-note');
      return !!btn && !!btn.getAttribute('data-tooltip');
    }, { timeout: 8000 }).then(() => true).catch(() => false);
    suite.log('4.1 the canonical info button sits beside the process RAM section label', rss);
    const EN_NOTE = 'Resident process memory may include shared pages and memory-mapped model files.';
    const RU_NOTE = 'Резидентная память процесса может включать общие страницы и смонтированные в память файлы моделей.';
    const rssInfo = await page.evaluate(() => {
      const btn = document.querySelector('#resources-content .res-section-title .ctx-info.res-proc-note');
      const title = btn.closest('.res-section-title');
      const btnCs = getComputedStyle(btn);
      return {
        titleText: title.textContent.trim(),
        sectionVisible: (document.getElementById('resources-content').innerText || ''),
        btnTag: btn.tagName,
        btnTip: btn.getAttribute('data-tooltip'),
        btnAria: btn.getAttribute('aria-label'),
        btnTitle: btn.hasAttribute('title'),
        visible: btnCs.display !== 'none' && btnCs.visibility !== 'hidden',
      };
    });
    suite.log('4.2 no standalone visible RSS paragraph — the section shows only its label and the button',
      rssInfo.visible && !rssInfo.sectionVisible.includes(EN_NOTE) && !rssInfo.sectionVisible.includes(RU_NOTE),
      `title="${rssInfo.titleText}" noteInVisibleText=${rssInfo.sectionVisible.includes(EN_NOTE)}`);
    suite.log('4.3 info button carries the note verbatim as its tooltip, no native title',
      rssInfo.btnTag === 'BUTTON' && rssInfo.btnTip === EN_NOTE && rssInfo.btnAria === EN_NOTE && !rssInfo.btnTitle,
      JSON.stringify(rssInfo.btnTip.slice(0, 40)));
    await (await page.locator('#resources-content .res-proc-note').first()).hover();
    await page.waitForTimeout(250);
    const rssTip = await page.evaluate(note => {
      const t = document.getElementById('goaltip');
      return t.classList.contains('visible') && t.textContent === note;
    }, EN_NOTE);
    suite.log('4.4 hovering the RSS info button shows the note in #goaltip', rssTip);
    const ruRss = await (async () => {
      await page.evaluate(() => window.setLanguage('ru'));
      await page.waitForTimeout(700);
      await page.evaluate(() => window.loadResources());
      return page.waitForFunction(note => (document.querySelector('#resources-content .res-proc-note') || {}).getAttribute && document.querySelector('#resources-content .res-proc-note').getAttribute('data-tooltip') === note, RU_NOTE, { timeout: 6000 })
        .then(() => page.evaluate(() => document.querySelector('#resources-content .res-proc-note').getAttribute('data-tooltip')))
        .catch(() => '');
    })();
    suite.log('4.5 RU note verbatim in the tooltip', ruRss === RU_NOTE, JSON.stringify(ruRss.slice(0, 80)));
    await H.screenshot(page, ws, '04-rss-info-button');

    // ═══ SECTION 5: theme-token hygiene ═══
    // Orphan badge uses the theme token (purple family), both themes.
    const orphanProbe = await (async () => {
      const mk = async theme => {
        await page.evaluate(th => document.documentElement.setAttribute('data-theme', th), theme);
        await page.waitForTimeout(200);
        return page.evaluate(() => {
          const badge = document.createElement('span');
          badge.className = 'status-badge orphan';
          document.body.appendChild(badge);
          const c = getComputedStyle(badge).color;
          badge.remove();
          return c;
        });
      };
      return { dark: await mk('dark'), light: await mk('light') };
    })();
    suite.log('5.1 orphan badge resolves through the theme token (not a bare literal)',
      orphanProbe.dark === 'rgb(155, 89, 182)' && orphanProbe.light === 'rgb(142, 68, 173)', JSON.stringify(orphanProbe));
    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
    // Warning notice (Settings export box) keeps the warning accent in both themes.
    await page.evaluate(() => window.navigate('settings'));
    await page.waitForTimeout(500);
    const warnBox = await page.evaluate(() => {
      const n = document.querySelector('.portable-warning-box');
      if (!n) return null;
      const cs = getComputedStyle(n);
      return { cls: n.className, left: cs.borderLeftColor, text: (n.textContent || '').includes('Args') };
    });
    suite.log('5.2 Settings export box is a warning notice with exact preserved text',
      !!warnBox && /notice-warning/.test(warnBox.cls) && warnBox.text, JSON.stringify(warnBox && { cls: warnBox.cls, left: warnBox.left }));
    const warnDark = warnBox ? warnBox.left : '';
    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'));
    await page.waitForTimeout(200);
    const warnLight = await page.evaluate(() => getComputedStyle(document.querySelector('.portable-warning-box')).borderLeftColor);
    suite.log('5.3 warning accent follows the theme tokens (dark/light differ, both orange family)',
      warnDark === 'rgb(243, 156, 18)' && warnLight === 'rgb(230, 126, 34)', `dark=${warnDark} light=${warnLight}`);
    await H.screenshot(page, ws, '05-settings-warning-notice-light');
    await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'dark'));
    await H.screenshot(page, ws, '05b-settings-warning-notice-dark');

    // ═══ SECTION 6: no console errors / 500s ═══
    suite.log('6.1 no page console errors', suite.consoleErrors.length === 0, JSON.stringify(suite.consoleErrors.slice(0, 3)));
    suite.log('6.2 no 5xx responses', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));
  } finally {
    await browser.close();
    await server.stop();
  }
  ok = await suite.finish();
  process.exit(ok ? 0 : 1);
}

// rgbOf converts a hex token value (e.g. "#60a5fa") to the computed rgb() string.
function rgbOf(hex) {
  const m = String(hex).trim().match(/^#([0-9a-f]{6})$/i);
  if (!m) return null;
  const n = parseInt(m[1], 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
}

main().catch(err => {
  console.error('HARNESS ERROR:', err);
  process.exit(2);
});

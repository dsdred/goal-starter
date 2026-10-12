'use strict';
const path = require('path');
const fs = require('fs');
const H = require('./harness.cjs');

const PORT = 19488;
const BASE = `http://127.0.0.1:${PORT}`;
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'secret123';

module.exports = { BASE };

function makeBundle(overrides) {
  const b = {
    format: 'goal-portable-config',
    version: 1,
    runtimes: [{ id: 'rt-portable', name: 'Portable RT', executable: 'test.exe', working_directory: '/opt/test', environment_keys: [] }],
    models: [{ id: 'model-portable', name: 'Portable Model', runtime_id: 'rt-portable', args: ['--port', '9999'], active: false, environment_keys: [] }],
    pipelines: [],
  };
  if (overrides) Object.assign(b, overrides);
  return b;
}

async function list(page, urlPath) {
  const r = await H.pageApi(page, 'GET', urlPath);
  return Array.isArray(r.data) ? r.data : [];
}

// ADR 018 Slice 2′ wire contract, asserted against the JSON the UI actually
// receives and not only against rendered text: summary.{runtimes,models,
// pipelines} is the single authoritative per-type breakdown, the outcome
// vocabulary is created/updated/unchanged, and the transitional Slice 1 keys
// (existing, skipped, flat top-level mirrors) are retired.
const PLAN_KEYS = ['blocked', 'can_import', 'created', 'dry_run', 'summary', 'unchanged', 'updated'];
const CONFLICT_KEYS = PLAN_KEYS.concat(['code', 'details', 'error']).sort();
const TYPE_KEYS = ['models', 'pipelines', 'runtimes'];
const PER_TYPE_KEYS = ['blocked', 'new', 'total', 'unchanged', 'update'];

function sortedKeys(obj) { return Object.keys(obj || {}).sort(); }

// wireAudit returns one description per violated wire rule over every captured
// import response. An empty result means every shipped plan used the ratified
// shape.
function wireAudit(bodies) {
  const problems = [];
  bodies.forEach(function (rec) {
    const label = '#' + rec.seq + '(' + rec.status + ')';
    if (/"existing"\s*:|"skipped"\s*:/.test(JSON.stringify(rec.body))) {
      problems.push(label + ' retired existing/skipped key');
    }
    // Only plans carry dry_run; file-level 400/413 and injected faults are
    // checked for retired vocabulary above and are not plan-shaped bodies.
    if (typeof rec.body.dry_run !== 'boolean') return;
    const top = sortedKeys(rec.body);
    if (top.some(k => TYPE_KEYS.includes(k))) problems.push(label + ' flat top-level mirror');
    const wantTop = rec.status === 409 ? CONFLICT_KEYS : PLAN_KEYS;
    if (top.join(',') !== wantTop.join(',')) problems.push(label + ' top keys=' + top.join(','));
    ['created', 'updated', 'unchanged'].forEach(function (k) {
      const c = sortedKeys(rec.body[k]);
      if (c.join(',') !== TYPE_KEYS.join(',')) problems.push(label + ' ' + k + ' keys=' + c.join(','));
    });
    if (sortedKeys(rec.body.summary).join(',') !== TYPE_KEYS.join(',')) {
      problems.push(label + ' summary keys=' + sortedKeys(rec.body.summary).join(','));
      return;
    }
    TYPE_KEYS.forEach(function (ty) {
      const s = rec.body.summary[ty];
      if (sortedKeys(s).join(',') !== PER_TYPE_KEYS.join(',')) {
        problems.push(label + ' summary.' + ty + ' keys=' + sortedKeys(s).join(','));
        return;
      }
      // The summary is authoritative, so its statuses must add up to the type
      // total and must agree with the result objects for the same plan.
      if (s.new + s.update + s.unchanged + s.blocked !== s.total) problems.push(label + ' summary.' + ty + ' does not add up');
      if (s.new !== rec.body.created[ty] || s.update !== rec.body.updated[ty] || s.unchanged !== rec.body.unchanged[ty]) {
        problems.push(label + ' summary.' + ty + ' disagrees with created/updated/unchanged');
      }
    });
  });
  return problems;
}

// OWNER-EXPORT-01 testability: the native save dialog is outside the page, so
// headless acceptance controls the capability instead of driving the dialog.
// mode: absent | ok | abort | fail | create_fail | write_fail | close_fail
async function setSaveCapability(page, mode) {
  await page.evaluate((m) => {
    window.__pickerCalls = [];
    window.__exportTrace = [];
    window.__toasts = [];
    window.__saved = null;
    window.__aborted = false;
    window.__fakeDest = 'C:\\Users\\leak-marker-8f2d\\Chosen Folder\\chosen-name.json';
    if (!window.__toastObserver) {
      window.__toastObserver = new MutationObserver((muts) => {
        for (const mu of muts) {
          for (const n of mu.addedNodes) {
            if (n.nodeType === 1) window.__toasts.push({ text: n.textContent, cls: n.className, trace: window.__exportTrace.slice() });
          }
        }
      });
      window.__toastObserver.observe(document.getElementById('toast-container'), { childList: true });
    }
    if (!window.__fetchWrapped) {
      const orig = window.fetch;
      window.fetch = function (input) {
        const url = typeof input === 'string' ? input : (input && input.url) || '';
        if (url.indexOf('/api/v1/export') !== -1) window.__exportTrace.push('fetch');
        return orig.apply(this, arguments);
      };
      window.__fetchWrapped = true;
    }
    const slot = { configurable: true, writable: true };
    if (m === 'absent') {
      Object.defineProperty(window, 'showSaveFilePicker', Object.assign(slot, { value: undefined }));
      return;
    }
    const picker = async (options) => {
      window.__pickerCalls.push(options);
      window.__exportTrace.push('picker');
      if (m === 'abort') { const e = new Error('cancelled'); e.name = 'AbortError'; throw e; }
      if (m === 'fail') { const e = new Error('picker unavailable'); e.name = 'NotAllowedError'; throw e; }
      const handle = {
        name: (options && options.suggestedName) || 'unnamed.json',
        __fakeDest: window.__fakeDest,
        createWritable: async () => {
          if (m === 'create_fail') throw new Error('createWritable failed');
          window.__exportTrace.push('writable');
          const chunks = [];
          return {
            write: async (b) => { if (m === 'write_fail') throw new Error('write failed'); chunks.push(b); },
            close: async () => {
              if (m === 'close_fail') throw new Error('close failed');
              window.__saved = {
                name: handle.name,
                size: chunks.length ? chunks[0].size : 0,
                text: chunks.length ? await chunks[0].text() : '',
              };
              window.__exportTrace.push('close');
            },
            abort: async () => { window.__aborted = true; },
          };
        },
      };
      return handle;
    };
    Object.defineProperty(window, 'showSaveFilePicker', Object.assign(slot, { value: picker }));
  }, mode);
}

function traceOf(page) {
  return page.evaluate(() => window.__exportTrace.slice());
}

function toastsOf(page) {
  return page.evaluate(() => window.__toasts.map(x => ({ text: x.text, cls: x.cls, trace: x.trace })));
}

async function main() {
  const ws = H.makeWorkspace('portable');
  const goalBin = H.buildGoal(ws);

  H.writeConfig(ws, {
    webPort: PORT,
    authEnabled: true,
    adminUser: ADMIN_USER,
    adminPassword: ADMIN_PASS,
  });

  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started (auth ON).\n');

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('Portable Configuration (export/import UI)');
  suite.watchPage(page);
  let downloadCount = 0;
  page.on('download', () => { downloadCount++; });
  const exportRequests = [];
  page.on('request', (req) => {
    if (req.url().indexOf('/api/v1/export') !== -1) {
      exportRequests.push({ url: req.url(), method: req.method(), headers: req.headers(), body: req.postData() });
    }
  });
  // Every import response the UI actually receives is captured so the wire
  // audit below can check the shipped JSON, not just the rendered wording.
  const importBodies = [];
  page.on('response', async (res) => {
    if (res.url().indexOf('/api/v1/import') === -1) return;
    let body = null;
    try { body = await res.json(); } catch { return; }
    importBodies.push({ seq: importBodies.length + 1, status: res.status(), body });
  });

  try {
    await H.login(page, BASE, ADMIN_USER, ADMIN_PASS);

    // Seed a runtime and model via authenticated page API
    const rtId = await page.evaluate(async () => {
        const csrf = (document.cookie.match(/goal_csrf_token=([^;]+)/) || [])[1] || '';
        const post = async (url, body) => {
            const r = await fetch('/api/v1' + url, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: JSON.stringify(body) });
            if (!r.ok) throw new Error(url + ' → ' + r.status);
            return r.json();
        };
        const rt = await post('/runtimes', { name: 'Seed RT', executable: 'seed.exe', working_directory: '/opt/seed' });
        await post('/models', { name: 'Seed Model', runtime_id: rt.id, args: ['--model', 'seed.gguf'] });
        return rt.id;
    });
    await page.evaluate(() => window.reloadAllData());
    await page.waitForTimeout(500);

    // ═══ SECTION 1: Navigate to Portable Configuration ═══
    await page.evaluate(() => window.navigate('adv-settings'));
    await page.waitForTimeout(500);

    const section = page.locator('#portable-section');
    suite.log('1.1 Settings shows Portable Configuration section', await section.isVisible());

    const title = await section.locator('h3').textContent();
    suite.log('1.2 Section title is RU (default)', (title || '').trim() === 'Переносимая конфигурация', `got="${(title || '').trim()}"`);

    // ═══ SECTION 2: EN labels ═══
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(500);
    const titleEn = await section.locator('h3').textContent();
    suite.log('2.1 Section title is EN after switch', (titleEn || '').trim() === 'Portable Configuration', `got="${(titleEn || '').trim()}"`);

    const exportBtn = await page.locator('#portable-export-btn').textContent();
    suite.log('2.2 Export button EN', (exportBtn || '').trim() === 'Download', `got="${(exportBtn || '').trim()}"`);

    const validateBtn = await page.locator('#portable-validate-btn').textContent();
    suite.log('2.3 Validate button EN', (validateBtn || '').trim() === 'Validate', `got="${(validateBtn || '').trim()}"`);

    const importBtn = await page.locator('#portable-import-btn').textContent();
    suite.log('2.4 Import button EN', (importBtn || '').trim() === 'Import', `got="${(importBtn || '').trim()}"`);

    // Switch back to RU
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(500);

    // ═══ SECTION 3: Export scope selector ═══
    const scopeSel = page.locator('#portable-export-scope');
    await scopeSel.selectOption('runtime');
    await page.waitForTimeout(200);
    const entitySel = page.locator('#portable-export-entity');
    suite.log('3.1 Entity selector visible for runtime scope', await entitySel.isVisible());
    const entityCount = await entitySel.locator('option').count();
    suite.log('3.2 Entity selector has options', entityCount > 1, `count=${entityCount}`);

    await scopeSel.selectOption('');
    await page.waitForTimeout(200);
    suite.log('3.3 Entity selector hidden for All scope', !(await entitySel.isVisible()));

    // 3.4-3.6 (OWNER-UX-01) the scope select must carry the GoAl form
    // language, not the white native control. Compared against the live theme
    // tokens so the check holds in both dark and light.
    const selStyle = await scopeSel.evaluate(el => {
      const rootCS = getComputedStyle(document.documentElement);
      const probe = document.createElement('div');
      probe.style.display = 'none';
      document.body.appendChild(probe);
      const token = (name) => {
        const p = probe.style;
        p.backgroundColor = p.color = p.borderColor = rootCS.getPropertyValue(name).trim();
        const cs = getComputedStyle(probe);
        return { bg: cs.backgroundColor, color: cs.color, border: cs.borderColor };
      };
      const tokens = { input: token('--bg-input'), text: token('--text-primary'), border: token('--border') };
      probe.remove();
      const cs = getComputedStyle(el);
      return { bg: cs.backgroundColor, color: cs.color, border: cs.borderTopColor, borderWidth: cs.borderTopWidth, radius: cs.borderRadius, padding: cs.padding, tokens };
    });
    suite.log('3.4 Scope select paints the GoAl input token, not the UA default', selStyle.bg === selStyle.tokens.input.bg && selStyle.bg !== 'rgb(255, 255, 255)', `bg=${selStyle.bg} want=${selStyle.tokens.input.bg}`);
    suite.log('3.5 Scope select text/border use GoAl tokens', selStyle.color === selStyle.tokens.text.color && selStyle.border === selStyle.tokens.border.border && selStyle.borderWidth === '1px', `color=${selStyle.color} border=${selStyle.border}/${selStyle.borderWidth}`);
    suite.log('3.6 Scope select matches GoAl field radius/padding', selStyle.radius === '6px' && selStyle.padding === '5px 10px', `radius=${selStyle.radius} padding=${selStyle.padding}`);

    // 3.7 (OWNER-UX-01) the environment/Args warning stays present but is
    // visually secondary: no filled panel, muted text.
    const warnStyle = await page.locator('.portable-warning-box').evaluate(el => {
      const cs = getComputedStyle(el);
      return { bg: cs.backgroundColor, topBorder: cs.borderTopColor, leftBorder: cs.borderLeftColor, font: parseFloat(getComputedStyle(el.querySelector('.hint-text')).fontSize) };
    });
    suite.log('3.7 Warning box no longer paints a filled panel', warnStyle.bg === 'rgba(0, 0, 0, 0)', `bg=${warnStyle.bg}`);
    suite.log('3.8 Warning text stays visible and muted-small', warnStyle.font > 0 && warnStyle.font <= 12 && warnStyle.leftBorder !== warnStyle.topBorder, `font=${warnStyle.font} left=${warnStyle.leftBorder} top=${warnStyle.topBorder}`);

    // 3.9-3.11 (OWNER-UX-01) GoAl-owned picker replaces the native caption.
    const chooseBtn = page.locator('#portable-choose-file-btn');
    suite.log('3.9 GoAl picker button is the visible control', await chooseBtn.isVisible() && (await chooseBtn.textContent()).trim() === 'Выбрать файл', `text=${(await chooseBtn.textContent()).trim()}`);
    const nativeInfo = await page.locator('#portable-import-file').evaluate(el => {
      const cs = getComputedStyle(el);
      const r = el.getBoundingClientRect();
      return { type: el.type, opacity: cs.opacity, w: r.width, h: r.height, tagName: el.tagName };
    });
    suite.log('3.10 Native input is still a real input[type=file]', nativeInfo.tagName === 'INPUT' && nativeInfo.type === 'file', JSON.stringify(nativeInfo));
    suite.log('3.11 Native input is not the visible UI', parseFloat(nativeInfo.opacity) === 0 && nativeInfo.w <= 2 && nativeInfo.h <= 2, `opacity=${nativeInfo.opacity} box=${nativeInfo.w}x${nativeInfo.h}`);
    suite.log('3.12 No file selected shows the GoAl RU caption', (await page.locator('#portable-import-filename').textContent()).trim() === 'Файл не выбран');
    // EN owns the picker text too (never the OS locale).
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(400);
    suite.log('3.13 EN picker captions', (await chooseBtn.textContent()).trim() === 'Choose file' && (await page.locator('#portable-import-filename').textContent()).trim() === 'No file selected');
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(400);

    // ═══ SECTION 4: Export all — FALLBACK branch (save capability absent) ═══
    // Headless Chromium does expose showSaveFilePicker on this loopback origin,
    // so the fallback contract is asserted with the capability explicitly removed.
    await setSaveCapability(page, 'absent');
    await scopeSel.selectOption('');
    await page.waitForTimeout(200);
    suite.log('4.0 Save-dialog capability detected as absent (fallback branch)', (await page.evaluate(() => typeof window.showSaveFilePicker)) !== 'function');
    const dlBefore4 = downloadCount;
    const [download] = await Promise.all([
      page.waitForEvent('download', { timeout: 10000 }),
      page.click('#portable-export-btn'),
    ]);
    const fileName = download.suggestedFilename();
    suite.log('4.1 Download filename', fileName === 'goal-portable-config.json', `got="${fileName}"`);
    const dlPath = await download.path();
    const exportContent = JSON.parse(fs.readFileSync(dlPath, 'utf-8'));
    suite.log('4.2 Export format correct', exportContent.format === 'goal-portable-config');
    suite.log('4.3 Export version is 1', exportContent.version === 1);
    suite.log('4.4 Export has runtimes array', Array.isArray(exportContent.runtimes) && exportContent.runtimes.length >= 1);
    suite.log('4.5 Export has models array', Array.isArray(exportContent.models) && exportContent.models.length >= 1);
    // Verify no environment values leaked
    const hasEnvValues = exportContent.runtimes.some(rt => rt.environment && Object.keys(rt.environment).length > 0);
    suite.log('4.6 No environment values in export', !hasEnvValues);

    // ═══ SECTION 5: Export model root (still the fallback branch) ═══
    await scopeSel.selectOption('model');
    await page.waitForTimeout(200);
    const modelOptions = await entitySel.locator('option').allTextContents();
    const modelOpt = modelOptions.find(o => o.includes('Seed Model'));
    const dlBefore5 = downloadCount;
    if (modelOpt) {
      await entitySel.selectOption({ label: modelOpt });
      await page.waitForTimeout(200);
      const [dl2] = await Promise.all([
        page.waitForEvent('download', { timeout: 10000 }),
        page.click('#portable-export-btn'),
      ]);
      const dl2Path = await dl2.path();
      const rootExport = JSON.parse(fs.readFileSync(dl2Path, 'utf-8'));
      suite.log('5.1 Root export has the model', rootExport.models.some(m => m.name === 'Seed Model'));
      suite.log('5.2 Root export has its runtime (closure)', rootExport.runtimes.some(rt => rt.id === rtId));
    } else {
      suite.log('5.1 Root export has the model', false, 'model option not found');
      suite.log('5.2 Root export has its runtime (closure)', false, 'skipped');
    }
    suite.log('5.3 Fallback made exactly one download per export', downloadCount - dlBefore5 === 1, `delta=${downloadCount - dlBefore5}`);
    suite.log('5.4 Fallback never requested a save dialog', (await page.evaluate(() => window.__pickerCalls.length)) === 0);

    // ═══ SECTION 5B: Preferred path — save dialog available (OWNER-EXPORT-01) ═══
    // The dialog itself is Manual Owner Acceptance; here a controlled picker
    // proves the product semantics: order, suggested name, JSON filter, written
    // bytes, close-before-success, and no browser download on this path.
    await setSaveCapability(page, 'ok');
    await scopeSel.selectOption('');
    await page.waitForTimeout(200);
    const dlBefore5B = downloadCount;
    await page.click('#portable-export-btn');
    await page.waitForFunction(() => window.__saved || window.__toasts.length > 0, { timeout: 10000 });
    const trace5B = await traceOf(page);
    const toasts5B = await toastsOf(page);
    const saved5B = await page.evaluate(() => window.__saved);
    const pickerOpts = await page.evaluate(() => window.__pickerCalls[0] || null);
    suite.log('5B.1 Save dialog is requested before the export fetch', trace5B[0] === 'picker' && trace5B.indexOf('fetch') === 1, `trace=${JSON.stringify(trace5B)}`);
    suite.log('5B.2 Suggested name is the ADR 014 SC-5 filename', !!(pickerOpts && pickerOpts.suggestedName === 'goal-portable-config.json'), `got=${pickerOpts ? pickerOpts.suggestedName : 'none'}`);
    suite.log('5B.3 JSON filter is offered for the save dialog', !!(pickerOpts && Array.isArray(pickerOpts.types) && pickerOpts.types[0] && pickerOpts.types[0].accept && pickerOpts.types[0].accept['application/json'] && pickerOpts.types[0].accept['application/json'].includes('.json')), `types=${JSON.stringify(pickerOpts ? pickerOpts.types : null)}`);
    suite.log('5B.4 Export bytes are written to the chosen handle', !!(saved5B && saved5B.size > 0), `size=${saved5B ? saved5B.size : 0}`);
    let savedBundle = null;
    try { savedBundle = JSON.parse(saved5B.text); } catch {}
    suite.log('5B.5 Saved file is a valid Bundle v1', !!savedBundle && savedBundle.format === 'goal-portable-config' && savedBundle.version === 1);
    suite.log('5B.6 Saved bundle carries the seeded entities', !!savedBundle && Array.isArray(savedBundle.runtimes) && savedBundle.runtimes.length >= 1 && Array.isArray(savedBundle.models) && savedBundle.models.length >= 1, savedBundle ? `runtimes=${savedBundle.runtimes.length} models=${savedBundle.models.length}` : 'unparsable');
    suite.log('5B.7 Saved bundle carries no environment values', !!savedBundle && !savedBundle.runtimes.some(rt => rt.environment && Object.keys(rt.environment).length > 0));
    suite.log('5B.8 Success is reported only after close()', toasts5B.length === 1 && toasts5B[0].text === 'Конфигурация экспортирована' && toasts5B[0].cls.includes('success') && toasts5B[0].trace.includes('close'), `toasts=${JSON.stringify(toasts5B.map(x => x.text))}`);
    suite.log('5B.9 Preferred path produces no browser download', downloadCount - dlBefore5B === 0, `delta=${downloadCount - dlBefore5B}`);
    suite.log('5B.10 Button is restored and enabled after a saved export', !(await page.locator('#portable-export-btn').isDisabled()));

    // ═══ SECTION 5C: Cancel (AbortError) — the operation simply ends ═══
    await setSaveCapability(page, 'abort');
    const dlBefore5C = downloadCount;
    await page.click('#portable-export-btn');
    await page.waitForFunction(() => window.__exportTrace.length > 0, { timeout: 10000 });
    await page.waitForTimeout(700);
    const trace5C = await traceOf(page);
    const toasts5C = await toastsOf(page);
    suite.log('5C.1 Cancel calls the picker only', JSON.stringify(trace5C) === JSON.stringify(['picker']), `trace=${JSON.stringify(trace5C)}`);
    suite.log('5C.2 Cancel shows no toast at all', toasts5C.length === 0, `toasts=${JSON.stringify(toasts5C.map(x => x.text))}`);
    suite.log('5C.3 Cancel fires no fallback download', downloadCount - dlBefore5C === 0);
    suite.log('5C.4 Cancel does not run the export fetch', !trace5C.includes('fetch'));
    suite.log('5C.5 Button is restored after cancel', !(await page.locator('#portable-export-btn').isDisabled()) && (await page.locator('#portable-export-btn').textContent()).trim() === 'Скачать');

    // ═══ SECTION 5D: Picker failure (non-AbortError) — no silent download ═══
    await setSaveCapability(page, 'fail');
    const dlBefore5D = downloadCount;
    await page.click('#portable-export-btn');
    await page.waitForFunction(() => window.__toasts.length > 0, { timeout: 10000 });
    const trace5D = await traceOf(page);
    const toasts5D = await toastsOf(page);
    suite.log('5D.1 Picker failure shows the neutral RU save error', toasts5D.length === 1 && toasts5D[0].text === 'Не удалось сохранить файл' && toasts5D[0].cls.includes('error'), `toasts=${JSON.stringify(toasts5D.map(x => x.text))}`);
    suite.log('5D.2 Picker failure does NOT start a fallback download', downloadCount - dlBefore5D === 0, `delta=${downloadCount - dlBefore5D}`);
    suite.log('5D.3 Picker failure fetches nothing and claims no success', !trace5D.includes('fetch') && !toasts5D.some(x => x.cls.includes('success')));
    suite.log('5D.4 Button is restored after a picker failure', !(await page.locator('#portable-export-btn').isDisabled()));
    // EN owns the same caption (a missing EN key would surface here, not in 15.x)
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(400);
    await page.evaluate(() => { window.__toasts = []; });
    await page.click('#portable-export-btn');
    await page.waitForFunction(() => window.__toasts.length > 0, { timeout: 10000 });
    suite.log('5D.5 Picker failure EN caption is localized', (await toastsOf(page))[0].text === 'Could not save the file', `got="${(await toastsOf(page)).map(x => x.text).join('|')}"`);

    // ═══ SECTION 5E: Write / close failure — write error, no success claim ═══
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(400);
    for (const [label, mode] of [['5E', 'write_fail'], ['5E2', 'close_fail']]) {
      await setSaveCapability(page, mode);
      await page.evaluate(() => { window.__toasts = []; });
      await page.click('#portable-export-btn');
      await page.waitForFunction(() => window.__toasts.length > 0, { timeout: 10000 });
      const t2 = await toastsOf(page);
      const saved = await page.evaluate(() => window.__saved);
      suite.log(`${label}.1 ${mode} reports the write error, not success`, t2.length === 1 && t2[0].text === 'Не удалось записать файл' && t2[0].cls.includes('error'), `toasts=${JSON.stringify(t2.map(x => x.text))}`);
      suite.log(`${label}.2 ${mode} claims no saved file`, saved === null);
      suite.log(`${label}.3 ${mode} leaves no success toast and re-enables the button`, !(await page.locator('#portable-export-btn').isDisabled()));
    }
    suite.log('5E.3b A failed write releases the stream so a retry is possible', await page.evaluate(() => window.__aborted === true));

    // ═══ SECTION 5F: Security boundary — no client path ever reaches GoAl ═══
    // The fake handle advertises an absolute destination path; none of it may
    // appear on the wire, and the export request must stay the documented
    // selector contract (ADR 014 SC-1).
    await setSaveCapability(page, 'ok');
    await page.evaluate(() => { window.__toasts = []; });
    exportRequests.length = 0;
    await page.click('#portable-export-btn');
    await page.waitForFunction(() => window.__saved, { timeout: 10000 });
    await scopeSel.selectOption('model');
    await page.waitForTimeout(200);
    const modelOptions5F = await entitySel.locator('option').allTextContents();
    const modelOpt5F = modelOptions5F.find(o => o.includes('Seed Model'));
    if (modelOpt5F) {
      await entitySel.selectOption({ label: modelOpt5F });
      await page.waitForTimeout(200);
    }
    await page.evaluate(() => { window.__saved = null; });
    await page.click('#portable-export-btn');
    await page.waitForFunction(() => window.__saved, { timeout: 10000 });
    const reqDump = await page.evaluate((reqs) => JSON.stringify(reqs), exportRequests);
    const marker = await page.evaluate(() => window.__fakeDest);
    const entityVal5F = await entitySel.inputValue();
    const markerTokens = ['leak-marker-8f2d', 'Chosen Folder', 'chosen-name', 'C:\\Users'];
    const urlObjs = exportRequests.map(r => new URL(r.url));
    const urlShapes = urlObjs.map(u => u.pathname + (u.search || ''));
    const queryKeys = urlObjs.map(u => Array.from(u.searchParams.keys()));
    suite.log('5F.1 Export requests stay on the documented GET endpoint', exportRequests.length === 2 && exportRequests.every(r => r.method === 'GET' && new URL(r.url).pathname === '/api/v1/export'), `urls=${JSON.stringify(urlShapes)}`);
    suite.log('5F.2 Query is exactly the documented selector contract', urlShapes[0] === '/api/v1/export' && urlShapes[1] === '/api/v1/export?model_id=' + encodeURIComponent(entityVal5F), `got=${JSON.stringify(urlShapes)}`);
    suite.log('5F.3 No undocumented selector key appears in any export query', queryKeys.every(ks => ks.every(k => ['runtime_id', 'model_id', 'pipeline_id'].includes(k))), `keys=${JSON.stringify(queryKeys)}`);
    suite.log('5F.4 No destination path leaks into URL, query, headers or body', !markerTokens.some(tok => reqDump.includes(tok)), `marker=${JSON.stringify(marker)}`);
    suite.log('5F.5 The export carries no request body', exportRequests.every(r => r.body === null || r.body === ''));

    // ═══ SECTION 6: Import happy path (all entities NEW) ═══
    const bundle1 = makeBundle();
    const bundle1Path = path.join(ws.dir, 'import-happy.json');
    fs.writeFileSync(bundle1Path, JSON.stringify(bundle1));

    await page.setInputFiles('#portable-import-file', bundle1Path);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    suite.log('6.1 Validate button enabled after file select', true);
    suite.log('6.1b Selected filename is shown by the GoAl picker', (await page.locator('#portable-import-filename').textContent()).trim() === 'import-happy.json');

    // Click Validate (dry-run)
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-success');
    }, { timeout: 10000 });
    const resultText = await page.locator('#portable-import-result').textContent();
    suite.log('6.2 Plan states file validity and the per-type breakdown', resultText.includes('Конфигурация корректна') && (resultText.match(/новые 1/g) || []).length === 2 && resultText.includes('будут восстановлены 0') && resultText.includes('без изменений 0'), `got="${resultText.trim()}"`);
    suite.log('6.2b An all-new plan shows the import total and no restore total', resultText.includes('Будет импортировано: 2') && !resultText.includes('Будут восстановлены:'), `got="${resultText.trim()}"`);
    suite.log('6.2c Nothing in an all-new plan is called existing or skipped', !resultText.includes('уже существуют') && !resultText.includes('пропущено'), `got="${resultText.trim()}"`);
    suite.log('6.3 Import button enabled after dry-run', !(await page.locator('#portable-import-btn').isDisabled()));

    // Click Import → confirmation dialog appears
    await page.click('#portable-import-btn');
    await page.waitForSelector('#confirm-modal', { state: 'visible', timeout: 5000 });
    suite.log('6.4 Confirmation dialog appears', true);

    // Confirm import
    await page.click('#confirm-yes');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.innerHTML.includes('Импорт завершён');
    }, { timeout: 10000 });
    const importText = await page.locator('#portable-import-result').textContent();
    suite.log('6.5 Result separates added from restored and up-to-date', importText.includes('добавлено 2') && importText.includes('восстановлено 0') && importText.includes('без изменений 0'), `got="${importText.trim()}"`);

    // Verify entities exist via API
    const modelsRes = await H.pageApi(page, 'GET', '/api/v1/models');
    const imported = (Array.isArray(modelsRes.data) ? modelsRes.data : []).find(m => m.id === 'model-portable');
    suite.log('6.6 Imported model exists in API', !!imported);
    const rtsRes = await H.pageApi(page, 'GET', '/api/v1/runtimes');
    const importedRt = (Array.isArray(rtsRes.data) ? rtsRes.data : []).find(r => r.id === 'rt-portable');
    suite.log('6.7 Imported runtime exists in API', !!importedRt);

    // Verify no instances started
    const instRes = await H.pageApi(page, 'GET', '/api/v1/instances');
    const newInsts = (Array.isArray(instRes.data) ? instRes.data : []).filter(i => i.model_id === 'model-portable');
    suite.log('6.8 No instances started by import', newInsts.length === 0, `count=${newInsts.length}`);

    // ═══ SECTION 7: Same IDs, different values → a pending restore (UPDATE) ═══
    // ADR 018 Slice 2′: an ID match is the identity evidence, so every entry is
    // UPDATE. The plan must say it in the future tense, offer Import, prove the
    // dry run wrote nothing, and the applied import must really move the
    // repository to the state the file describes.
    const bundle2 = makeBundle({
      runtimes: [{ id: 'rt-portable', name: 'Hijacked Name', executable: 'hijacked.exe', working_directory: '/tmp/hijack', environment_keys: [] }],
      models: [{ id: 'model-portable', name: 'Hijacked Model', runtime_id: 'rt-portable', args: ['--stolen'], active: true, environment_keys: [] }],
      pipelines: [],
    });
    const bundle2Path = path.join(ws.dir, 'import-restore.json');
    fs.writeFileSync(bundle2Path, JSON.stringify(bundle2));

    const rtBefore7 = (await list(page, '/api/v1/runtimes')).find(r => r.id === 'rt-portable');
    const mBefore7 = (await list(page, '/api/v1/models')).find(m => m.id === 'model-portable');
    await page.setInputFiles('#portable-import-file', bundle2Path);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-success');
    }, { timeout: 10000 });
    const restoreText = (await page.locator('#portable-import-result').textContent()).trim();
    suite.log('7.1 A pending restore is a valid plan, not an error or "nothing to import"', restoreText.includes('Конфигурация корректна') && !restoreText.includes('импорт невозможен') && !restoreText.includes('Импортировать нечего'), `got="${restoreText}"`);
    suite.log('7.2 Per-type plan lines use the future tense for a restore', (restoreText.match(/будут восстановлены 1/g) || []).length === 2 && (restoreText.match(/новые 0/g) || []).length === 2, `got="${restoreText}"`);
    suite.log('7.3 A pending restore is never worded in the past tense', !restoreText.includes('восстановлено') && !restoreText.includes('пропущено') && !restoreText.includes('уже существуют'), `got="${restoreText}"`);
    suite.log('7.4 Import and restore totals stay separate', restoreText.includes('Будут восстановлены: 2') && !restoreText.includes('Будет импортировано'), `got="${restoreText}"`);
    suite.log('7.5 Import is enabled for a restore-only plan', !(await page.locator('#portable-import-btn').isDisabled()));
    suite.log('7.6 Validation wrote nothing: runtime keeps its own bytes', rtBefore7 && rtBefore7.name === 'Portable RT' && rtBefore7.executable === 'test.exe', `got=${JSON.stringify(rtBefore7 && { n: rtBefore7.name, e: rtBefore7.executable })}`);
    suite.log('7.7 Validation wrote nothing: model keeps its own bytes', mBefore7 && mBefore7.name === 'Portable Model' && mBefore7.active === false, `got=${JSON.stringify(mBefore7 && { n: mBefore7.name, a: mBefore7.active })}`);

    await page.click('#portable-import-btn');
    await page.waitForSelector('#confirm-modal', { state: 'visible', timeout: 5000 });
    await page.click('#confirm-yes');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.innerHTML.includes('Импорт завершён');
    }, { timeout: 10000 });
    const appliedText = (await page.locator('#portable-import-result').textContent()).trim();
    suite.log('7.8 Applied result reports restores in the past tense', appliedText.includes('добавлено 0, восстановлено 2, без изменений 0'), `got="${appliedText}"`);
    const rtAfter7 = (await list(page, '/api/v1/runtimes')).find(r => r.id === 'rt-portable');
    suite.log('7.9 UPDATE applied the runtime projection', !!rtAfter7 && rtAfter7.name === 'Hijacked Name' && rtAfter7.executable === 'hijacked.exe' && rtAfter7.working_directory === '/tmp/hijack', `got=${JSON.stringify(rtAfter7 && { n: rtAfter7.name, e: rtAfter7.executable, w: rtAfter7.working_directory })}`);
    const mAfter7 = (await list(page, '/api/v1/models')).find(m => m.id === 'model-portable');
    suite.log('7.10 UPDATE applied the model projection', !!mAfter7 && mAfter7.name === 'Hijacked Model' && JSON.stringify(mAfter7.args) === JSON.stringify(['--stolen']) && mAfter7.active === true, `got=${JSON.stringify(mAfter7 && { n: mAfter7.name, a: mAfter7.args, act: mAfter7.active })}`);
    const instAfter7 = (await list(page, '/api/v1/instances')).filter(i => i.model_id === 'model-portable');
    suite.log('7.11 A restore that marks a model active starts no instance', instAfter7.length === 0, `count=${instAfter7.length}`);

    // ═══ SECTION 7A: Re-importing the just-applied file → everything in sync ═══
    // The same bytes now describe the repository, so every entity is UNCHANGED:
    // the plan offers nothing, enables nothing, and must not read as a failure.
    await page.setInputFiles('#portable-import-file', bundle2Path);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-info');
    }, { timeout: 10000 });
    const inSyncText = (await page.locator('#portable-import-result').textContent()).trim();
    suite.log('7A.1 Re-importing the applied file says there is nothing to import', inSyncText.includes('Импортировать нечего — репозиторий уже соответствует файлу.') && !inSyncText.includes('импорт невозможен') && !inSyncText.includes('Не удалось'), `got="${inSyncText}"`);
    suite.log('7A.2 Up-to-date entities are counted per type', (inSyncText.match(/без изменений 1/g) || []).length === 2 && inSyncText.includes('будут восстановлены 0'), `got="${inSyncText}"`);
    suite.log('7A.3 No total line is shown when nothing will change', !inSyncText.includes('Будет импортировано') && !inSyncText.includes('Будут восстановлены'), `got="${inSyncText}"`);
    suite.log('7A.4 Import stays disabled with nothing to change', await page.locator('#portable-import-btn').isDisabled());
    suite.log('7A.5 No raw status token is presented to the user', !inSyncText.includes('unchanged') && !inSyncText.includes('id_exists'), `got="${inSyncText}"`);
    const rtStill7 = (await list(page, '/api/v1/runtimes')).find(r => r.id === 'rt-portable');
    suite.log('7A.6 The repository keeps the restored bytes', !!rtStill7 && rtStill7.name === 'Hijacked Name' && rtStill7.executable === 'hijacked.exe', `got=${JSON.stringify(rtStill7 && { n: rtStill7.name, e: rtStill7.executable })}`);

    // ═══ SECTION 7B: Ambiguous runtime identity → BLOCKED plan ═══
    // Same runtime NAME owned by a different ID: the repository contract makes
    // runtime names unique, but nothing proves the two IDs are the same entity,
    // so the entry can be neither created nor restored. Its model depends on it
    // and is blocked too — no dangling object may be created. The name is taken
    // by the entity the previous section just restored.
    const bundleBlocked = makeBundle({
      runtimes: [{ id: 'rt-clone', name: 'Hijacked Name', executable: 'clone.exe', working_directory: '/opt/clone', environment_keys: [] }],
      models: [{ id: 'model-clone', name: 'Clone Model', runtime_id: 'rt-clone', args: [], active: false, environment_keys: [] }],
      pipelines: [],
    });
    const blockedPath = path.join(ws.dir, 'import-blocked.json');
    fs.writeFileSync(blockedPath, JSON.stringify(bundleBlocked));
    await page.setInputFiles('#portable-import-file', blockedPath);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-error');
    }, { timeout: 10000 });
    const blockedText = await page.locator('#portable-import-result').textContent();
    suite.log('7B.1 File validity and repository conflict stay separate statements', blockedText.includes('Конфигурация корректна, но импорт невозможен'), `got="${blockedText.trim()}"`);
    suite.log('7B.1b Per-type plan line accounts for the blocked entity', blockedText.includes('Runtime: новые 0, будут восстановлены 0, без изменений 0, заблокировано 1'), `got="${blockedText.trim().substring(0, 200)}"`);
    suite.log('7B.1c Every blocked type is listed with its own count', blockedText.includes('Модели: новые 0, будут восстановлены 0, без изменений 0, заблокировано 1'), `got="${blockedText.trim().substring(0, 260)}"`);
    suite.log('7B.1d A blocked plan uses no transitional vocabulary', !blockedText.includes('уже существуют') && !blockedText.includes('пропущено'), `got="${blockedText.trim().substring(0, 260)}"`);
    suite.log('7B.2 Blocked reason is human-readable, token is secondary', blockedText.includes('имя уже занято') && blockedText.includes('runtime_name_taken_other_id'), `got="${blockedText.trim().substring(0, 160)}"`);
    suite.log('7B.3 Dependent model is blocked, not silently dropped', blockedText.includes('Clone Model') && blockedText.includes('зависит от заблокированной'), `got="${blockedText.trim().substring(0, 260)}"`);
    const tokenPlacement = await page.evaluate(() => {
      const box = document.querySelector('#portable-import-result');
      const details = box.querySelector('.portable-plan-details');
      const title = box.querySelector('.portable-plan-title');
      return {
        hasDetails: !!details,
        tokenInsideDetails: !!details && details.textContent.includes('runtime_name_taken_other_id'),
        tokenInTitle: !!title && title.textContent.includes('runtime_name_taken_other_id'),
        open: !!details && details.open,
      };
    });
    suite.log('7B.4 Technical tokens live in a collapsed details block', tokenPlacement.hasDetails && tokenPlacement.tokenInsideDetails && !tokenPlacement.tokenInTitle && !tokenPlacement.open, JSON.stringify(tokenPlacement));
    suite.log('7B.5 Import disabled while the plan is blocked', await page.locator('#portable-import-btn').isDisabled());
    const afterBlocked = await H.pageApi(page, 'GET', '/api/v1/runtimes');
    suite.log('7B.6 Blocked plan wrote nothing', !(Array.isArray(afterBlocked.data) ? afterBlocked.data : []).some(r => r.id === 'rt-clone'), `count=${(Array.isArray(afterBlocked.data) ? afterBlocked.data : []).length}`);
    suite.log('7B.7 The readable reason names the current owner by ID', blockedText.includes('(ID rt-portable)'), `got="${blockedText.trim().substring(0, 300)}"`);
    const detailsText = await page.evaluate(() => {
      const d = document.querySelector('#portable-import-result .portable-plan-details');
      return d ? d.textContent.replace(/\s+/g, ' ').trim() : '';
    });
    suite.log('7B.8 Technical details use the related vocabulary, never "existing"', detailsText.includes('(related: rt-portable)') && detailsText.includes('(related: rt-clone)') && !detailsText.includes('(existing:'), `got="${detailsText.substring(0, 300)}"`);

    // ═══ SECTION 7C: Mixed plan — NEW + UPDATE + UNCHANGED in one file ═══
    // All three writable statuses at once: rt-second and model-second are
    // created, model-portable is restored, rt-portable restates the repository
    // byte for byte and must be left alone.
    const bundleMixed = {
      format: 'goal-portable-config',
      version: 1,
      runtimes: [
        { id: 'rt-portable', name: 'Hijacked Name', executable: 'hijacked.exe', working_directory: '/tmp/hijack', environment_keys: [] },
        { id: 'rt-second', name: 'Second RT', executable: 'second.exe', working_directory: '/opt/second', environment_keys: [] },
      ],
      models: [
        { id: 'model-portable', name: 'Restored Model', runtime_id: 'rt-portable', args: ['--stolen'], active: true, environment_keys: [] },
        { id: 'model-second', name: 'Second Model', runtime_id: 'rt-second', args: [], active: false, environment_keys: [] },
      ],
      pipelines: [],
    };
    const mixedPath = path.join(ws.dir, 'import-mixed.json');
    fs.writeFileSync(mixedPath, JSON.stringify(bundleMixed));
    await page.setInputFiles('#portable-import-file', mixedPath);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-success');
    }, { timeout: 10000 });
    const mixedText = (await page.locator('#portable-import-result').textContent()).trim();
    suite.log('7C.1 Mixed plan counts the three statuses per type separately', mixedText.includes('Runtime: новые 1, будут восстановлены 0, без изменений 1') && mixedText.includes('Модели: новые 1, будут восстановлены 1, без изменений 0'), `got="${mixedText}"`);
    suite.log('7C.2 Mixed plan shows both totals and keeps them apart', mixedText.includes('Будет импортировано: 2 · Будут восстановлены: 1'), `got="${mixedText}"`);
    suite.log('7C.3 Mixed plan enables Import', !(await page.locator('#portable-import-btn').isDisabled()));
    await page.click('#portable-import-btn');
    await page.waitForSelector('#confirm-modal', { state: 'visible', timeout: 5000 });
    await page.click('#confirm-yes');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.innerHTML.includes('добавлено 2, восстановлено 1, без изменений 1');
    }, { timeout: 10000 });
    suite.log('7C.4 Result reports all three outcome classes', true);
    const rtList = await H.pageApi(page, 'GET', '/api/v1/runtimes');
    const rtPort = (Array.isArray(rtList.data) ? rtList.data : []).find(r => r.id === 'rt-portable');
    suite.log('7C.5 The up-to-date entity kept the bytes it had', rtPort && rtPort.executable === 'hijacked.exe' && rtPort.name === 'Hijacked Name', `got=${JSON.stringify(rtPort && { n: rtPort.name, e: rtPort.executable })}`);
    const mList = await H.pageApi(page, 'GET', '/api/v1/models');
    const mPort = (Array.isArray(mList.data) ? mList.data : []).find(m => m.id === 'model-portable');
    suite.log('7C.6 The restore-only field change was applied inside a mixed plan', mPort && mPort.name === 'Restored Model' && JSON.stringify(mPort.args) === JSON.stringify(['--stolen']) && mPort.active === true, `got=${JSON.stringify(mPort && { n: mPort.name, a: mPort.args })}`);
    const rtSecond = (Array.isArray(rtList.data) ? rtList.data : []).find(r => r.id === 'rt-second');
    suite.log('7C.7 The new entity was created beside them', !!rtSecond && rtSecond.executable === 'second.exe');

    // ═══ SECTION 7D: The same plan contract in EN ═══
    // The plan wording is user-facing in BOTH locales: an EN string that lost a
    // parameter or drifted back to a raw token must fail here, not silently.
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(500);
    await page.setInputFiles('#portable-import-file', mixedPath);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-info');
    }, { timeout: 10000 });
    const enSyncText = (await page.locator('#portable-import-result').textContent()).trim();
    suite.log('7D.1 EN nothing-to-import wording', enSyncText.includes('Nothing to import — the repository already matches the file.'), `got="${enSyncText.substring(0, 200)}"`);
    suite.log('7D.2 EN plan is per type with the up-to-date counts', enSyncText.includes('Runtimes: 0 new, 0 to restore, 2 up to date') && enSyncText.includes('Models: 0 new, 0 to restore, 2 up to date'), `got="${enSyncText.substring(0, 260)}"`);
    suite.log('7D.3 EN shows no totals when nothing will change', !enSyncText.includes('Will be imported') && !enSyncText.includes('Will restore'), `got="${enSyncText.substring(0, 260)}"`);
    suite.log('7D.4 EN keeps Import disabled with nothing to change', await page.locator('#portable-import-btn').isDisabled());
    suite.log('7D.5 EN never drifts back to the transitional vocabulary', !enSyncText.includes('already exist') && !enSyncText.includes('skipped'), `got="${enSyncText.substring(0, 260)}"`);

    // The same restore contract in EN: a pending UPDATE reads in the infinitive,
    // never in the past tense, and Import is offered for it.
    const bundleEnRestore = {
      format: 'goal-portable-config',
      version: 1,
      runtimes: [
        { id: 'rt-second', name: 'Second RT Renamed', executable: 'second.exe', working_directory: '/opt/second', environment_keys: [] },
        { id: 'rt-portable', name: 'Hijacked Name', executable: 'hijacked.exe', working_directory: '/tmp/hijack', environment_keys: [] },
      ],
      models: [{ id: 'model-second', name: 'Second Model', runtime_id: 'rt-second', args: [], active: false, environment_keys: [] }],
      pipelines: [],
    };
    const enRestorePath = path.join(ws.dir, 'import-en-restore.json');
    fs.writeFileSync(enRestorePath, JSON.stringify(bundleEnRestore));
    await page.setInputFiles('#portable-import-file', enRestorePath);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-success');
    }, { timeout: 10000 });
    const enPlan = (await page.locator('#portable-import-result').textContent()).trim();
    suite.log('7D.6 EN pending restore uses the ratified vocabulary', enPlan.includes('Configuration is valid') && enPlan.includes('Runtimes: 0 new, 1 to restore, 1 up to date') && enPlan.includes('Models: 0 new, 0 to restore, 1 up to date'), `got="${enPlan.substring(0, 300)}"`);
    suite.log('7D.7 EN restore total, never the past tense, never retired words', enPlan.includes('Will restore: 1') && !enPlan.includes('Will be imported') && !enPlan.includes('restored') && !enPlan.includes('skipped'), `got="${enPlan.substring(0, 300)}"`);
    suite.log('7D.8 EN enables Import for a restore-only plan', !(await page.locator('#portable-import-btn').isDisabled()));

    await page.setInputFiles('#portable-import-file', blockedPath);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-error');
    }, { timeout: 10000 });
    const enBlocked = (await page.locator('#portable-import-result').textContent()).trim();
    // Raw tokens live only below the collapsed details block.
    const enBlockedPlain = await page.evaluate(() => {
      const clone = document.querySelector('#portable-import-result').cloneNode(true);
      clone.querySelectorAll('details').forEach(d => d.remove());
      return clone.textContent.replace(/\s+/g, ' ').trim();
    });
    suite.log('7D.9 EN blocked headline is validity + impossibility', enBlocked.includes('Configuration is valid, but the import cannot proceed'), `got="${enBlocked.substring(0, 160)}"`);
    suite.log('7D.10 EN blocked reason is readable, not a raw token', enBlockedPlain.includes('the name is already taken by another Runtime') && enBlockedPlain.includes('depends on the blocked entity') && !enBlockedPlain.includes('runtime_name_taken_other_id') && !enBlockedPlain.includes('dependency_blocked'), `got="${enBlockedPlain.substring(0, 260)}"`);
    suite.log('7D.10b EN blocked plan uses no transitional vocabulary and the related wording', !enBlockedPlain.includes('already exist') && !enBlockedPlain.includes('skipped') && !enBlockedPlain.includes('(existing:') && enBlocked.includes('(related: rt-portable)'), `got="${enBlockedPlain.substring(0, 300)}"`);

    // The wire itself, not only its rendering: every plan this suite received
    // must carry the ratified keys and nothing else.
    await page.waitForTimeout(300);
    const wireProblems = wireAudit(importBodies);
    suite.log('7D.11 Every captured import body uses the Slice 2′ wire', importBodies.length >= 8 && wireProblems.length === 0, `plans=${importBodies.length}; problems=${JSON.stringify(wireProblems.slice(0, 4))}`);

    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(500);


    // ═══ SECTION 8: Invalid file ═══
    const badPath = path.join(ws.dir, 'import-invalid.json');
    fs.writeFileSync(badPath, JSON.stringify({ format: 'wrong-format', version: 1, runtimes: [], models: [], pipelines: [] }));
    await page.setInputFiles('#portable-import-file', badPath);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-error');
    }, { timeout: 10000 });
    suite.log('8.1 Invalid format shows error', true);
    suite.log('8.2 Import button disabled on invalid', await page.locator('#portable-import-btn').isDisabled());

    // A check that failed for a reason other than the file or the repository
    // must not be presented as either of those. (403 is used as the injected
    // failure so the suite's real 5xx guard below stays unmodified.)
    await page.route('**/api/v1/import*', route => route.fulfill({
      status: 403,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'injected failure', code: 'forbidden' }),
    }));
    await page.click('#portable-validate-btn');
    let faultText = '';
    try {
      await page.waitForFunction(() => {
        const el = document.querySelector('#portable-import-result');
        return el && el.style.display !== 'none' && el.textContent.includes('Не удалось проверить конфигурацию');
      }, { timeout: 10000 });
      faultText = (await page.locator('#portable-import-result').textContent()).trim();
    } catch (e) { faultText = 'timeout: ' + e.message; }
    await page.unroute('**/api/v1/import*');
    suite.log('8.3 Non-file/non-conflict failure is not presented as an invalid file or a blocked plan', faultText.includes('Не удалось проверить конфигурацию') && !faultText.includes('не является корректной') && !faultText.includes('импорт невозможен'), `got="${faultText.substring(0, 160)}"`);
    suite.log('8.4 Import stays disabled after a failed check', await page.locator('#portable-import-btn').isDisabled());

    // ═══ SECTION 9: Undefined variable ═══
    const bundle3 = makeBundle({
      runtimes: [{ id: 'rt-undef-var', name: 'Undef Var RT', executable: '${PORTABLE_UI_TEST_VAR}/bin/run.exe', working_directory: '/opt', environment_keys: [] }],
      models: [{ id: 'model-undef-var', name: 'Undef Var Model', runtime_id: 'rt-undef-var', args: ['${UNDEFINED_TEST_ARG}'], active: false, environment_keys: [] }],
      pipelines: [],
    });
    const bundle3Path = path.join(ws.dir, 'import-undef-var.json');
    fs.writeFileSync(bundle3Path, JSON.stringify(bundle3));
    await page.setInputFiles('#portable-import-file', bundle3Path);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-success');
    }, { timeout: 10000 });
    suite.log('9.1 Undefined variable passes dry-run', true);
    // Confirm import
    await page.click('#portable-import-btn');
    await page.waitForSelector('#confirm-modal', { state: 'visible', timeout: 5000 });
    await page.click('#confirm-yes');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.innerHTML.includes('Импорт завершён');
    }, { timeout: 10000 });
    const rtsRes2 = await H.pageApi(page, 'GET', '/api/v1/runtimes');
    const undefRt = (Array.isArray(rtsRes2.data) ? rtsRes2.data : []).find(r => r.id === 'rt-undef-var');
    suite.log('9.2 Undefined var runtime imported with raw string', undefRt && undefRt.executable.includes('${PORTABLE_UI_TEST_VAR}'), `exe="${undefRt ? undefRt.executable : 'N/A'}"`);

    // ═══ SECTION 10: Malformed variable ═══
    const bundle4 = makeBundle({
      runtimes: [{ id: 'rt-malformed', name: 'Malformed RT', executable: '${1BAD}/bin/run.exe', working_directory: '/opt', environment_keys: [] }],
      models: [{ id: 'model-malformed', name: 'Malformed Model', runtime_id: 'rt-malformed', args: ['--ok'], active: false, environment_keys: [] }],
      pipelines: [],
    });
    const bundle4Path = path.join(ws.dir, 'import-malformed.json');
    fs.writeFileSync(bundle4Path, JSON.stringify(bundle4));
    await page.setInputFiles('#portable-import-file', bundle4Path);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-error');
    }, { timeout: 10000 });
    suite.log('10.1 Malformed variable shows error', true);
    suite.log('10.2 Import button disabled on malformed', await page.locator('#portable-import-btn').isDisabled());

    // ═══ SECTION 11: >10 MiB client-side file limit ═══
    const oversizedPath = path.join(ws.dir, 'oversized.json');
    const oversizedBuf = Buffer.alloc(10 * 1024 * 1024 + 1, 0x20);
    fs.writeFileSync(oversizedPath, oversizedBuf);
    let importRequestSent = false;
    page.on('request', req => {
      if (req.url().includes('/api/v1/import')) importRequestSent = true;
    });
    await page.setInputFiles('#portable-import-file', oversizedPath);
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-error');
    }, { timeout: 5000 });
    const oversizeText = await page.locator('#portable-import-result').textContent();
    suite.log('11.1 >10 MiB shows file-too-large error', oversizeText.includes('10') || oversizeText.toLowerCase().includes('large') || oversizeText.includes('больш'), `got="${oversizeText.trim().substring(0, 80)}"`);
    await page.waitForTimeout(500);
    suite.log('11.2 No import request sent for >10 MiB', !importRequestSent);
    suite.log('11.3 Validate button remains disabled', await page.locator('#portable-validate-btn').isDisabled());

    // ═══ SECTION 12: Exact 10 MiB boundary (not rejected by client) ═══
    const exactPath = path.join(ws.dir, 'exact-10mib.json');
    const exactBuf = Buffer.alloc(10 * 1024 * 1024, 0x20);
    fs.writeFileSync(exactPath, exactBuf);
    importRequestSent = false;
    await page.setInputFiles('#portable-import-file', exactPath);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    suite.log('12.1 Exact 10 MiB not rejected by client (validate enabled)', true);
    // Click validate — server will reject with 400 (not valid JSON), proving client allowed it
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-error');
    }, { timeout: 15000 });
    suite.log('12.2 Exact 10 MiB reaches server (error is server-side, not client)', true);

    // ═══ SECTION 13: Active/AutoStart import safety ═══
    const bundleActive = makeBundle({
      runtimes: [{ id: 'rt-autostart', name: 'Autostart RT', executable: 'auto.exe', working_directory: '/opt/auto', environment_keys: [] }],
      models: [{ id: 'model-autostart', name: 'Autostart Model', runtime_id: 'rt-autostart', args: ['--auto'], active: true, environment_keys: [] }],
      pipelines: [],
    });
    const bundleActivePath = path.join(ws.dir, 'import-autostart.json');
    fs.writeFileSync(bundleActivePath, JSON.stringify(bundleActive));
    const instBefore = await H.pageApi(page, 'GET', '/api/v1/instances');
    const instCountBefore = (Array.isArray(instBefore.data) ? instBefore.data : []).length;
    await page.setInputFiles('#portable-import-file', bundleActivePath);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-success');
    }, { timeout: 10000 });
    await page.click('#portable-import-btn');
    await page.waitForSelector('#confirm-modal', { state: 'visible', timeout: 5000 });
    await page.click('#confirm-yes');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.innerHTML.includes('Импорт завершён');
    }, { timeout: 10000 });
    const modelsActive = await H.pageApi(page, 'GET', '/api/v1/models');
    const autoModel = (Array.isArray(modelsActive.data) ? modelsActive.data : []).find(m => m.id === 'model-autostart');
    suite.log('13.1 Active model imported and exists', !!autoModel);
    suite.log('13.2 Active state preserved', autoModel && autoModel.active === true, `active=${autoModel ? autoModel.active : 'N/A'}`);
    const instAfter = await H.pageApi(page, 'GET', '/api/v1/instances');
    const instCountAfter = (Array.isArray(instAfter.data) ? instAfter.data : []).length;
    const newAutoInsts = (Array.isArray(instAfter.data) ? instAfter.data : []).filter(i => i.model_id === 'model-autostart');
    suite.log('13.3 No instances created by import', newAutoInsts.length === 0, `count=${newAutoInsts.length}`);
    suite.log('13.4 Instance count unchanged', instCountAfter === instCountBefore, `before=${instCountBefore}, after=${instCountAfter}`);

    // ═══ SECTION 14: Responsive (mobile) ═══
    await page.setViewportSize({ width: 430, height: 900 });
    await page.waitForTimeout(500);
    const sectionBox = await section.boundingBox();
    const pageWidth = 430;
    suite.log('14.1 No horizontal overflow at 430px', sectionBox && sectionBox.x >= 0 && (sectionBox.x + sectionBox.width) <= pageWidth + 2, `x=${sectionBox ? sectionBox.x : '?'}, w=${sectionBox ? sectionBox.width : '?'}`);
    const fileInputBox = await page.locator('#portable-import-file').boundingBox();
    suite.log('14.2 File input fits at 430px', fileInputBox && fileInputBox.width <= pageWidth, `w=${fileInputBox ? fileInputBox.width : '?'}`);
    await page.setViewportSize({ width: 1920, height: 1080 });

    // ═══ SECTION 15: i18n completeness (no raw keys) ═══
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(300);
    const missingEn = await page.evaluate(() => window.i18nMissing);
    const portableMissingEn = Object.keys(missingEn || {}).filter(k => k.startsWith('portable.'));
    suite.log('15.1 No missing EN portable keys', portableMissingEn.length === 0, portableMissingEn.join(', ') || 'ok');
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(300);
    const missingRu = await page.evaluate(() => window.i18nMissing);
    const portableMissingRu = Object.keys(missingRu || {}).filter(k => k.startsWith('portable.'));
    suite.log('15.2 No missing RU portable keys', portableMissingRu.length === 0, portableMissingRu.join(', ') || 'ok');

    // ═══ SECTION 16: Warning text visible ═══
    const warningEl = page.locator('.portable-warning-box');
    suite.log('16.1 Warning box visible', await warningEl.isVisible());
    const warningText = await warningEl.textContent();
    suite.log('16.2 Warning mentions Args', warningText.includes('Args'));
    suite.log('16.3 Warning mentions environment values', warningText.toLowerCase().includes('значения') || warningText.includes('VALUES'));

    // ═══ SECTION 17: Console errors + 5xx ═══
    // 400/409 are exercised by design; 403 is the injected failed-check case (8.3).
    const unexpectedErrors = suite.consoleErrors.filter(e => !e.includes('409') && !e.includes('400') && !e.includes('403'));
    suite.log('17.1 No unexpected console errors', unexpectedErrors.length === 0, unexpectedErrors.slice(0, 3).join('; ') || 'clean');
    suite.log('17.2 No 5xx responses', suite.serverErrors.length === 0, suite.serverErrors.length ? JSON.stringify(suite.serverErrors[0]) : 'clean');
    const lateWire = wireAudit(importBodies);
    suite.log('17.3 No import response anywhere in the run carries retired vocabulary', lateWire.length === 0, JSON.stringify(lateWire.slice(0, 4)));

  } catch (e) {
    suite.log('FATAL', false, e.message);
    try { await H.screenshot(page, ws, 'portable-fatal'); } catch {}
  } finally {
    await browser.close();
    await server.stop();
  }

  const ok = await suite.finish();
  process.exit(ok ? 0 : 1);
}

main().catch(e => { console.error(e); process.exit(1); });

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

    // ═══ SECTION 4: Export all (download) ═══
    await scopeSel.selectOption('');
    await page.waitForTimeout(200);
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

    // ═══ SECTION 5: Export model root ═══
    await scopeSel.selectOption('model');
    await page.waitForTimeout(200);
    const modelOptions = await entitySel.locator('option').allTextContents();
    const modelOpt = modelOptions.find(o => o.includes('Seed Model'));
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
    suite.log('6.2 Plan states file validity and the per-type breakdown', resultText.includes('Конфигурация корректна') && resultText.includes('новые 1') && resultText.includes('уже существуют 0'), `got="${resultText.trim()}"`);
    suite.log('6.2b Plan states what will be imported', resultText.includes('Будет импортировано: 2'), `got="${resultText.trim()}"`);
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
    suite.log('6.5 Import result separates created from skipped', importText.includes('добавлено 2') && importText.includes('пропущено 0'), `got="${importText.trim()}"`);

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

    // ═══ SECTION 7: Re-importing the same entities — SKIP EXISTING ═══
    // Same IDs, different field values: an ID match is the identity evidence, so
    // every entry is EXISTING and the repository must keep its own bytes.
    const bundle2 = makeBundle({
      runtimes: [{ id: 'rt-portable', name: 'Hijacked Name', executable: 'hijacked.exe', working_directory: '/tmp/hijack', environment_keys: [] }],
      models: [{ id: 'model-portable', name: 'Hijacked Model', runtime_id: 'rt-portable', args: ['--stolen'], active: true, environment_keys: [] }],
      pipelines: [],
    });
    const bundle2Path = path.join(ws.dir, 'import-existing.json');
    fs.writeFileSync(bundle2Path, JSON.stringify(bundle2));

    await page.setInputFiles('#portable-import-file', bundle2Path);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-info');
    }, { timeout: 10000 });
    const existingText = await page.locator('#portable-import-result').textContent();
    suite.log('7.1 Same entities read as "nothing to import", not as an error', existingText.includes('Все сущности уже существуют. Импортировать нечего.'), `got="${existingText.trim()}"`);
    suite.log('7.2 Existing entities are reported per type', (existingText.match(/уже существуют 1/g) || []).length === 2 && existingText.includes('Будет пропущено: 2'), `got="${existingText.trim()}"`);
    suite.log('7.3 Import button stays disabled with nothing to create', await page.locator('#portable-import-btn').isDisabled());
    suite.log('7.4 No raw reason token is the primary message', !existingText.includes('id_exists') && !existingText.includes('name_exists'), `got="${existingText.trim()}"`);
    const rtNow = (await list(page, '/api/v1/runtimes')).find(r => r.id === 'rt-portable');
    const mNow = (await list(page, '/api/v1/models')).find(m => m.id === 'model-portable');
    suite.log('7.5 Repository keeps its own runtime bytes', rtNow && rtNow.name === 'Portable RT' && rtNow.executable === 'test.exe', `got=${JSON.stringify(rtNow && { n: rtNow.name, e: rtNow.executable })}`);
    suite.log('7.6 Repository keeps its own model bytes', mNow && mNow.name === 'Portable Model' && mNow.active === false, `got=${JSON.stringify(mNow && { n: mNow.name, a: mNow.active })}`);

    // ═══ SECTION 7B: Ambiguous runtime identity → BLOCKED plan ═══
    // Same runtime NAME owned by a different ID: the repository contract makes
    // runtime names unique, but nothing proves the two IDs are the same entity,
    // so the entry can be neither created nor safely skipped. Its model depends
    // on it and is blocked too — no dangling object may be created.
    const bundleBlocked = makeBundle({
      runtimes: [{ id: 'rt-clone', name: 'Portable RT', executable: 'clone.exe', working_directory: '/opt/clone', environment_keys: [] }],
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
    suite.log('7B.1b Per-type plan line accounts for the blocked entity', blockedText.includes('Runtime: новые 0, уже существуют 0, заблокировано 1'), `got="${blockedText.trim().substring(0, 160)}"`);
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

    // ═══ SECTION 7C: Mixed plan — one NEW, one EXISTING → import enabled ═══
    const bundleMixed = {
      format: 'goal-portable-config',
      version: 1,
      runtimes: [
        { id: 'rt-portable', name: 'Portable RT', executable: 'test.exe', working_directory: '/opt/test', environment_keys: [] },
        { id: 'rt-second', name: 'Second RT', executable: 'second.exe', working_directory: '/opt/second', environment_keys: [] },
      ],
      models: [{ id: 'model-second', name: 'Second Model', runtime_id: 'rt-second', args: [], active: false, environment_keys: [] }],
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
    const mixedText = await page.locator('#portable-import-result').textContent();
    suite.log('7C.1 Mixed plan counts new and existing separately', mixedText.includes('Runtime: новые 1, уже существуют 1') && mixedText.includes('Модели: новые 1, уже существуют 0'), `got="${mixedText.trim()}"`);
    suite.log('7C.2 Mixed plan enables Import (create new + skip existing)', !(await page.locator('#portable-import-btn').isDisabled()));
    await page.click('#portable-import-btn');
    await page.waitForSelector('#confirm-modal', { state: 'visible', timeout: 5000 });
    await page.click('#confirm-yes');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.innerHTML.includes('добавлено 2, пропущено 1');
    }, { timeout: 10000 });
    suite.log('7C.3 Result reports created and skipped counts', true);
    const rtList = await H.pageApi(page, 'GET', '/api/v1/runtimes');
    const rtPort = (Array.isArray(rtList.data) ? rtList.data : []).find(r => r.id === 'rt-portable');
    suite.log('7C.4 Skipped entity kept its original state', rtPort && rtPort.executable === 'test.exe', `exe=${rtPort ? rtPort.executable : 'N/A'}`);

    // ═══ SECTION 7D: The same plan contract in EN ═══
    // The plan wording is user-facing in BOTH locales: an EN string that lost a
    // parameter or drifted back to a raw token must fail here, not silently.
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(500);
    await page.setInputFiles('#portable-import-file', bundle2Path);
    await page.waitForFunction(() => !document.querySelector('#portable-validate-btn').disabled, { timeout: 5000 });
    await page.click('#portable-validate-btn');
    await page.waitForFunction(() => {
      const el = document.querySelector('#portable-import-result');
      return el && el.style.display !== 'none' && el.innerHTML.includes('portable-result-info');
    }, { timeout: 10000 });
    const enExisting = (await page.locator('#portable-import-result').textContent()).trim();
    suite.log('7D.1 EN nothing-to-import wording', enExisting.includes('All entities already exist. Nothing to import.'), `got="${enExisting.substring(0, 160)}"`);
    suite.log('7D.2 EN plan is per type with totals', (enExisting.match(/1 already exist/g) || []).length === 2 && enExisting.includes('Will be skipped: 2') && !enExisting.includes('Will be imported'), `got="${enExisting.substring(0, 200)}"`);
    suite.log('7D.3 EN keeps Import disabled with nothing to create', await page.locator('#portable-import-btn').isDisabled());

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
    suite.log('7D.4 EN blocked headline is validity + impossibility', enBlocked.includes('Configuration is valid, but the import cannot proceed'), `got="${enBlocked.substring(0, 160)}"`);
    suite.log('7D.5 EN blocked reason is readable, not a raw token', enBlockedPlain.includes('the name is already taken by another Runtime') && enBlockedPlain.includes('depends on the blocked entity') && !enBlockedPlain.includes('runtime_name_taken_other_id') && !enBlockedPlain.includes('dependency_blocked'), `got="${enBlockedPlain.substring(0, 260)}"`);
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

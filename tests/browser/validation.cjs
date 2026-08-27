'use strict';
const path = require('path');
const H = require('./harness.cjs');

const PORT = 19474;
const BASE = `http://127.0.0.1:${PORT}`;

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

module.exports = { BASE };

async function main() {
  const ws = H.makeWorkspace('validation');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  H.writeConfig(ws, {
    webPort: PORT,
    authEnabled: false,
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
  console.log('Server started.\n');

  const api = (method, urlPath, body) => H.api(BASE, method, urlPath, body);

  // Seed: running instance (infinite) -> its stopped_at is Go zero time (unset)
  const m1 = await api('POST', '/api/v1/models', { name: 'Val Running', runtime_id: 'rt-fake-1', args: ['infinite'] });
  const s1 = await api('POST', `/api/v1/models/${m1.data.id}/start`);
  await sleep(2000);

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('VALIDATION (UX-02 zero-time / UX-05 name i18n / UX-09 runtime step)');
  suite.watchPage(page);

  const stepActive = async n =>
    (await page.locator(`#wizard-steps .wizard-step[data-step="${n}"]`).evaluate(el => el.classList.contains('active')));
  const errState = async () => {
    const el = page.locator('#wizard-error');
    return { visible: await el.isVisible(), text: (await el.textContent() || '').trim() };
  };

  let ok = false;
  try {
    suite.log('Seed: running model created', m1.status === 201, `status=${m1.status}`);
    suite.log('Seed: instance started (stopped_at unset)', s1.status === 200, `state=${s1.data && s1.data.state}`);

    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);

    // ═══ SECTION 1: UX-02 — running instance (unset stopped_at) ═══
    await page.setViewportSize({ width: 430, height: 932 });
    await page.evaluate(() => window.navigate('adv-instances'));
    await page.waitForTimeout(500);
    const liveId = s1.data ? s1.data.id : '';
    const compactRow = page.locator('.cinst-row').filter({ hasText: liveId.slice(0, 12) });
    const compactText = (await compactRow.first().textContent() || '').trim();
    suite.log('1.1 430px compact row: no garbled zero-date ("1.1 HH:MM" / "0001" / "1970")',
      !/1\.1 \d|0001|1970/.test(compactText), `text=${JSON.stringify(compactText)}`);
    suite.log('1.2 430px compact row: shows live uptime HH:MM:SS', /\d{2}:\d{2}:\d{2}/.test(compactText),
      `text=${JSON.stringify(compactText)}`);
    await H.screenshot(page, ws, '01-compact-running-430');

    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.evaluate(() => window.navigate('adv-instances'));
    await page.waitForTimeout(500);
    const stoppedCell = await page.evaluate(id => {
      const rows = [...document.querySelectorAll('#adv-instances-body tr')];
      const row = rows.find(tr => tr.textContent.includes(id));
      if (!row) return null;
      return row.querySelectorAll('td')[5].textContent.trim();
    }, liveId.slice(0, 16));
    suite.log('1.3 desktop table: running instance stop column is "—"', stoppedCell === '—', `cell=${JSON.stringify(stoppedCell)}`);
    await H.screenshot(page, ws, '02-table-running-1920');

    // ═══ SECTION 2: UX-05 — empty name shows app-localized error (RU), not native HTML5 bubble ═══
    await page.evaluate(() => window.navigate('models'));
    await page.waitForTimeout(500);
    await page.click('#view-models button:has-text("Добавить модель")');
    await page.waitForTimeout(300);
    await page.click('#wiz-next');
    await page.waitForTimeout(300);
    let err = await errState();
    suite.log('2.1 RU empty name: localized app error visible', err.visible && err.text === 'Укажите название', `err=${JSON.stringify(err)}`);
    suite.log('2.2 RU empty name: stays on step 1', (await stepActive(1)) === true);
    suite.log('2.3 #wiz-name has no native required attribute', await page.locator('#wiz-name').evaluate(el => el.required === false));
    await H.screenshot(page, ws, '03-wiz-empty-name-ru');

    await page.fill('#wiz-name', 'Wizard Val Model');
    await page.click('#wiz-next');
    await page.waitForTimeout(300);
    suite.log('2.4 RU: filled name advances to step 2', (await stepActive(2)) === true);

    // ═══ SECTION 3: UX-09 — step 2 without selected runtime blocks step 3 (RU) ═══
    await page.click('#wiz-next');
    await page.waitForTimeout(300);
    err = await errState();
    suite.log('3.1 RU step2 no runtime: "Далее" blocked with localized error',
      (await stepActive(2)) === true && err.visible && err.text === 'Выберите Runtime', `err=${JSON.stringify(err)}`);
    await page.click('#wizard-steps .wizard-step[data-step="3"]');
    await page.waitForTimeout(300);
    suite.log('3.2 RU step2 no runtime: direct step-3 tab click blocked', (await stepActive(2)) === true);
    await H.screenshot(page, ws, '04-wiz-rt-missing-ru');

    await page.fill('#wiz-rt-search', 'Fake');
    await page.waitForTimeout(200);
    await page.locator('#wiz-rt-dropdown .rt-dropdown-item').first().click();
    await page.waitForTimeout(200);
    await page.click('#wiz-next');
    await page.waitForTimeout(300);
    suite.log('3.3 RU step2 with runtime: reaches step 3', (await stepActive(3)) === true);

    await page.fill('#wiz-args', '-m /models/val.gguf\n--host 127.0.0.1\n--port 8095');
    await page.click('#wiz-next');
    await page.waitForTimeout(1500);
    const models = (await api('GET', '/api/v1/models')).data;
    const created = models.find(m => m.name === 'Wizard Val Model');
    suite.log('3.4 RU happy path: model created via wizard', !!created, `count=${models.length}`);
    if (created) suite.log('3.5 RU happy path: wizard closed after create', !(await page.locator('#wizard-modal').isVisible()));
    await H.screenshot(page, ws, '05-wiz-created-ru');

    // ═══ SECTION 4: UX-05 / UX-09 in EN ═══
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(500);
    await page.evaluate(() => window.navigate('models'));
    await page.waitForTimeout(500);

    await page.click('#view-models button:has-text("Add model")');
    await page.waitForTimeout(300);
    await page.click('#wiz-next');
    await page.waitForTimeout(300);
    err = await errState();
    suite.log('4.1 EN empty name: localized app error visible', err.visible && err.text === 'Please enter a name', `err=${JSON.stringify(err)}`);
    await page.fill('#wiz-name', 'EN Val Model');
    await page.click('#wiz-next');
    await page.waitForTimeout(300);
    await page.click('#wiz-next');
    await page.waitForTimeout(300);
    err = await errState();
    suite.log('4.2 EN step2 no runtime: blocked with localized error',
      (await stepActive(2)) === true && err.visible && err.text === 'Please select a Runtime', `err=${JSON.stringify(err)}`);
    await H.screenshot(page, ws, '06-wiz-errors-en');

    await page.fill('#wiz-rt-search', 'Fake');
    await page.waitForTimeout(200);
    await page.locator('#wiz-rt-dropdown .rt-dropdown-item').first().click();
    await page.waitForTimeout(200);
    await page.click('#wiz-next');
    await page.waitForTimeout(300);
    suite.log('4.3 EN step2 with runtime: reaches step 3', (await stepActive(3)) === true);
    await page.fill('#wiz-args', '-m /models/en.gguf\n--host 127.0.0.1\n--port 8096');
    await page.click('#wiz-next');
    await page.waitForTimeout(1500);
    const models2 = (await api('GET', '/api/v1/models')).data;
    suite.log('4.4 EN happy path: model created via wizard', models2.some(m => m.name === 'EN Val Model'));

    // ═══ SECTION 5: sanity ═══
    suite.log('5.1 No console errors', suite.consoleErrors.length === 0, suite.consoleErrors.slice(0, 3).join('; '));
    suite.log('5.2 No server 5xx errors', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));

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

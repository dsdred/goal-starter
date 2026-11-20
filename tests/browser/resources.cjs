'use strict';
// G3-RES-04 — Resource Monitoring UI (Slice 1). Read-only view over the published
// GET /api/v1/system/resources (ADR 026). The backend is never modified here: a
// real-fetch leg proves the live endpoint renders, then page.route fixtures drive
// every contract state (ok / partial / unavailable / measurement_failed), the
// CPU-only healthy path, multi-vendor GPUs, missing metrics, measured-zero vs
// unavailable, RU/EN relocalization, and the page-scoped refresh boundary.
const H = require('./harness.cjs');

const PORT = 19476;
const BASE = `http://127.0.0.1:${PORT}`;

// Wire shape mirrors internal/resource/model.go. A Reading is
// {value:int64|null, unit, provenance, source, reason?}; value:null means
// unavailable, a measured 0 IS a value (ADR 026 D-6).
const R = (value, unit, provenance, source, reason) => {
  const r = { value, unit, provenance, source: source || 'fixture' };
  if (reason) r.reason = reason;
  return r;
};
const SYS = {
  ram: {
    total: R(34359738368, 'bytes', 'measured', 'sys'),
    available: R(17179869184, 'bytes', 'measured', 'sys'),
    used: R(17179869184, 'bytes', 'estimated', 'sys'),
  },
  cpu: {
    logical_cores: R(16, 'count', 'measured', 'sys'),
    utilization: R(23, 'percent', 'measured', 'sys'),
  },
};
const gpu = (index, vendor, name, driver, memoryClass, usedVal, usedProv) => ({
  device_id: 'gpu-' + index, index, vendor, name, driver, memory_class: memoryClass,
  resources: {
    vram_total: R(25769803776, 'bytes', 'measured', 'prov'),
    vram_used: R(usedVal, 'bytes', usedProv || 'measured', 'prov'),
    vram_free: R(23622320128, 'bytes', 'measured', 'prov'),
    utilization: R(37, 'percent', 'measured', 'prov'),
  },
});
const report = (over) => Object.assign({
  state: 'ok', devices: [], system: SYS, processes: [], capabilities: {},
  sampled_at: '2026-11-21T10:00:00Z', errors: [],
}, over);

const FIX_OK_GPU = report({ devices: [gpu(0, 'nvidia', 'NVIDIA GeForce RTX 4090', '551.23', 'dedicated', 2147483648)] });
const FIX_CPU_ONLY = report({ devices: [] });
const FIX_MULTI = report({
  devices: [
    gpu(0, 'nvidia', 'NVIDIA GeForce RTX 4090', '551.23', 'dedicated', 2147483648),
    gpu(1, 'amd', 'AMD Radeon RX 7900 XTX', '24.10', 'dedicated', 4294967296),
    gpu(2, 'intel', 'Intel Arc A770', '31.0', 'shared', 1073741824),
  ],
});
const FIX_PARTIAL = report({
  state: 'partial',
  system: {
    ram: SYS.ram,
    cpu: { logical_cores: R(16, 'count', 'measured', 'sys'), utilization: R(null, 'percent', 'unavailable', 'sys', 'no_sample') },
  },
  devices: [{
    device_id: 'gpu-0', index: 0, vendor: 'nvidia', name: 'NVIDIA GeForce RTX 4090', driver: '551.23', memory_class: 'dedicated',
    resources: {
      vram_total: R(25769803776, 'bytes', 'measured', 'prov'),
      vram_used: R(null, 'bytes', 'unavailable', 'prov', 'metric_unavailable'),
      vram_free: R(23622320128, 'bytes', 'measured', 'prov'),
    },
  }],
});
const FIX_ZERO = report({
  system: {
    ram: { total: R(34359738368, 'bytes', 'measured', 'sys'), available: R(34359738368, 'bytes', 'measured', 'sys'), used: R(0, 'bytes', 'estimated', 'sys') },
    cpu: { logical_cores: R(16, 'count', 'measured', 'sys'), utilization: R(0, 'percent', 'measured', 'sys') },
  },
  devices: [gpu(0, 'nvidia', 'NVIDIA GeForce RTX 4090', '551.23', 'dedicated', 0)],
});
const FIX_UNAVAILABLE = report({
  state: 'unavailable', devices: [], system: null, sampled_at: undefined,
  errors: [{ provider: 'system', state: 'unavailable', reason: 'no_provider' }],
});
const FIX_FAILED = report({
  state: 'measurement_failed', devices: [], system: null,
  errors: [{ provider: 'nvidia', state: 'measurement_failed', reason: 'nvml_init_failed' }],
});
const FIX_NO_SYSTEM = report({ state: 'partial', system: null, devices: [gpu(0, 'nvidia', 'NVIDIA GeForce RTX 4090', '551.23', 'dedicated', 2147483648)] });

async function main() {
  const ws = H.makeWorkspace('resources');
  const goalBin = H.buildGoal(ws);
  H.writeConfig(ws, { webPort: PORT });
  const server = H.startServer(goalBin, ws);
  if (!await H.waitForHealth(BASE)) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started (auth OFF).\n');

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('RESOURCES (G3-RES-04 read-only monitoring UI)');
  suite.watchPage(page);

  const content = () => page.locator('#resources-content');
  const contentText = async () => (await content().textContent() || '');
  const stateText = async () => (await page.locator('.res-state').first().textContent() || '').trim();
  const vendorCount = () => page.locator('.res-vendor').count();
  const missingKeys = () => page.evaluate(() => Object.keys(window.i18nMissing || {}));
  const domRawKeys = () => page.evaluate(() => {
    const re = /\b(?:app|auth|blocked|common|confirm|conn|err|history|instances|logs|models|nav|pipelines|portable|resources|runtimes|settings|sidebar|wizard)\.[a-z][a-z0-9]*(?:\.[a-z][a-z0-9]*)+/g;
    const text = document.body ? document.body.innerText : '';
    const found = new Set(); let m;
    while ((m = re.exec(text)) !== null) found.add(m[0]);
    return [...found];
  });

  let reqCount = 0;
  const RES_URL = '**/api/v1/system/resources';
  const setFixture = async (rep, waitText) => {
    await page.unroute(RES_URL).catch(() => {});
    await page.route(RES_URL, route => { reqCount++; route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(rep) }); });
    await page.evaluate(() => window.loadResources());
    if (waitText) {
      await page.waitForFunction((txt) => {
        const el = document.querySelector('#resources-content');
        return !!el && (el.textContent || '').includes(txt);
      }, waitText, { timeout: 6000 }).catch(() => {});
    } else {
      await page.waitForTimeout(300);
    }
  };

  let ok = false;
  try {
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(600);

    // ═══ SECTION 1: real endpoint (no mock) — the live host renders ═══
    await page.evaluate(() => window.navigate('resources'));
    await page.waitForFunction(() => {
      const el = document.querySelector('#resources-content');
      return !!el && !!el.querySelector('.res-state');
    }, { timeout: 8000 }).catch(() => {});
    const realText = await contentText();
    suite.log('1.1 resources view is active', await page.locator('#view-resources').evaluate(el => el.classList.contains('active')));
    suite.log('1.2 real fetch renders a state badge', (await stateText()).length > 0, `state=${JSON.stringify(await stateText())}`);
    suite.log('1.3 real host shows a System section with RAM total', realText.includes('32.0 GiB') || /\d+(\.\d+)?\s*(GiB|MiB)/.test(realText), `text=${JSON.stringify(realText.slice(0, 120))}`);
    suite.log('1.4 real host shows logical cores', await page.locator('.res-row').count() > 0);
    suite.log('1.5 real leg: no raw i18n keys in DOM', (await domRawKeys()).length === 0, `raw=${JSON.stringify((await domRawKeys()).slice(0, 5))}`);
    suite.log('1.6 real leg: i18nMissing empty', (await missingKeys()).length === 0, `missing=${JSON.stringify((await missingKeys()).slice(0, 5))}`);
    await H.screenshot(page, ws, '01-resources-real');

    // Switch to EN for deterministic contract assertions.
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(400);

    // ═══ SECTION 2: ok + single NVIDIA GPU ═══
    await setFixture(FIX_OK_GPU, 'RTX 4090');
    suite.log('2.1 state badge is OK', (await stateText()) === 'OK', `state=${JSON.stringify(await stateText())}`);
    suite.log('2.2 exactly one GPU card', (await vendorCount()) === 1, `vendors=${await vendorCount()}`);
    suite.log('2.3 GPU vendor + name rendered', (await contentText()).includes('NVIDIA') && (await contentText()).includes('NVIDIA GeForce RTX 4090'));
    const t2 = await contentText();
    suite.log('2.4 VRAM total = 24.0 GiB', t2.includes('24.0 GiB'));
    suite.log('2.5 VRAM used = 2.0 GiB', t2.includes('2.0 GiB'));
    suite.log('2.6 VRAM free = 22.0 GiB', t2.includes('22.0 GiB'));
    suite.log('2.7 VRAM utilization = 8%', t2.includes('8%'));
    suite.log('2.8 RAM percent = 50%', t2.includes('50%'));
    suite.log('2.9 RAM used carries estimated tag', await page.locator('.res-prov-estimated').count() >= 1);
    suite.log('2.10 CPU utilization = 23% and cores = 16', t2.includes('23%') && t2.includes('16'));
    suite.log('2.11 sampled_at rendered', t2.includes('Sampled at'));
    suite.log('2.12 no raw keys / i18nMissing empty', (await domRawKeys()).length === 0 && (await missingKeys()).length === 0);
    await H.screenshot(page, ws, '02-resources-ok-gpu');

    // ═══ SECTION 3: CPU-only healthy (no GPU) — ADR 026 D-8 ═══
    await setFixture(FIX_CPU_ONLY, 'No GPU detected');
    suite.log('3.1 GPU empty state shown', (await contentText()).includes('No GPU detected'));
    suite.log('3.2 no GPU vendor cards', (await vendorCount()) === 0);
    suite.log('3.3 CPU + RAM system cards still present', (await contentText()).includes('CPU') && (await contentText()).includes('RAM'));
    suite.log('3.4 CPU-only state is OK (first-class healthy)', (await stateText()) === 'OK', `state=${JSON.stringify(await stateText())}`);
    await H.screenshot(page, ws, '03-resources-cpu-only');

    // ═══ SECTION 4: multi-vendor GPUs (nvidia / amd / intel) ═══
    await setFixture(FIX_MULTI, 'Intel');
    const vendors = await page.locator('.res-vendor').allTextContents();
    suite.log('4.1 exactly three GPU cards', (await vendorCount()) === 3, `vendors=${JSON.stringify(vendors)}`);
    suite.log('4.2 vendors NVIDIA, AMD, Intel all present', vendors.includes('NVIDIA') && vendors.includes('AMD') && vendors.includes('Intel'), `vendors=${JSON.stringify(vendors)}`);
    const t4 = await contentText();
    suite.log('4.3 each model name rendered', t4.includes('RTX 4090') && t4.includes('RX 7900 XTX') && t4.includes('Arc A770'));
    suite.log('4.4 memory class labels (Dedicated / Shared)', t4.includes('Dedicated') && t4.includes('Shared'));
    suite.log('4.5 AMD card shows VRAM rows (no NVIDIA-only assumption)', t4.includes('VRAM total') && (await page.locator('.res-row').count()) >= 9);
    await H.screenshot(page, ws, '04-resources-multi-vendor');

    // ═══ SECTION 5: partial — missing optional metrics ═══
    await setFixture(FIX_PARTIAL, 'Partial');
    suite.log('5.1 state badge is Partial', (await stateText()) === 'Partial', `state=${JSON.stringify(await stateText())}`);
    suite.log('5.2 partial banner shown', await page.locator('.res-banner-partial').count() === 1);
    const t5 = await contentText();
    suite.log('5.3 unavailable CPU utilization shows unavailable tag', await page.locator('.res-prov-unavailable').count() >= 1);
    suite.log('5.4 RAM values still rendered (absence never downgrades others)', t5.includes('32.0 GiB') && t5.includes('50%'));
    suite.log('5.5 GPU VRAM total still rendered despite used unavailable', t5.includes('24.0 GiB'));
    await H.screenshot(page, ws, '05-resources-partial');

    // ═══ SECTION 6: measured zero IS a value (ADR 026 D-6) ═══
    await setFixture(FIX_ZERO, '0%');
    const t6 = await contentText();
    suite.log('6.1 CPU measured zero renders 0% (not —)', t6.includes('0%'));
    suite.log('6.2 no unavailable tag on measured-zero readings', await page.locator('.res-prov-unavailable').count() === 0);
    suite.log('6.3 state still OK', (await stateText()) === 'OK');
    await H.screenshot(page, ws, '06-resources-measured-zero');

    // ═══ SECTION 7: unavailable — no source (healthy empty, NOT a failure) ═══
    await setFixture(FIX_UNAVAILABLE, 'No resource data available');
    suite.log('7.1 state badge is Unavailable', (await stateText()) === 'Unavailable', `state=${JSON.stringify(await stateText())}`);
    suite.log('7.2 healthy empty state shown', (await contentText()).includes('No resource data available'));
    suite.log('7.3 no system or GPU cards', (await vendorCount()) === 0 && await page.locator('.res-card').count() === 0);
    suite.log('7.4 NOT presented as a measurement failure', await page.locator('.res-banner-failed').count() === 0);
    await H.screenshot(page, ws, '07-resources-unavailable');

    // ═══ SECTION 8: measurement_failed — source broke ═══
    await setFixture(FIX_FAILED, 'Measurement failed');
    suite.log('8.1 state badge is Measurement failed', (await stateText()) === 'Measurement failed', `state=${JSON.stringify(await stateText())}`);
    suite.log('8.2 failure banner shown', await page.locator('.res-banner-failed').count() === 1);
    const t8 = await contentText();
    suite.log('8.3 provider error listed (nvidia + reason)', t8.includes('nvidia') && t8.includes('nvml_init_failed'));
    suite.log('8.4 distinct from unavailable empty state', !t8.includes('No resource data available'));
    await H.screenshot(page, ws, '08-resources-measurement-failed');

    // ═══ SECTION 9: system null but GPU present ═══
    await setFixture(FIX_NO_SYSTEM, 'System metrics are unavailable');
    suite.log('9.1 system-unavailable note shown', (await contentText()).includes('System metrics are unavailable'));
    suite.log('9.2 GPU card still rendered', (await vendorCount()) === 1);
    await H.screenshot(page, ws, '09-resources-no-system');

    // ═══ SECTION 10: RU/EN relocalization from cache ═══
    await setFixture(FIX_OK_GPU, 'RTX 4090');
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(400);
    const ruText = await contentText();
    suite.log('10.1 RU state badge localized', (await stateText()) === 'В норме', `state=${JSON.stringify(await stateText())}`);
    suite.log('10.2 RU labels localized (ЦП / ОЗУ / Всего)', ruText.includes('ЦП') && ruText.includes('ОЗУ') && ruText.includes('Всего'));
    suite.log('10.3 RU estimated tag localized', ruText.includes('оценка'));
    suite.log('10.4 RU nav item is Ресурсы', (await page.locator('.nav-item[data-view="resources"] span[data-i18n]').textContent() || '').trim() === 'Ресурсы');
    suite.log('10.5 RU: i18nMissing empty + no raw keys', (await missingKeys()).length === 0 && (await domRawKeys()).length === 0);
    await H.screenshot(page, ws, '10-resources-ru');
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(400);
    suite.log('10.6 EN relocalizes back', (await stateText()) === 'OK' && (await contentText()).includes('Logical cores'));
    suite.log('10.7 EN: i18nMissing empty + no raw keys', (await missingKeys()).length === 0 && (await domRawKeys()).length === 0);

    // ═══ SECTION 11: refresh boundary — manual + page-scoped, no hidden load ═══
    await setFixture(FIX_OK_GPU, 'RTX 4090');
    const beforeManual = reqCount;
    await page.click('#view-resources .view-header button');
    await page.waitForTimeout(400);
    suite.log('11.1 manual Refresh button triggers a fetch', reqCount > beforeManual, `before=${beforeManual} after=${reqCount}`);
    // Leaving the view must stop sampling: no fetch while hidden.
    await page.evaluate(() => window.navigate('models'));
    await page.waitForTimeout(300);
    const hiddenStart = reqCount;
    await page.waitForTimeout(6000);
    suite.log('11.2 no resource fetch while view is hidden (6s > 5s interval)', reqCount === hiddenStart, `start=${hiddenStart} end=${reqCount}`);
    await page.evaluate(() => window.navigate('resources'));
    await page.waitForTimeout(600);
    suite.log('11.3 returning to the view resumes sampling', reqCount > hiddenStart, `hidden=${hiddenStart} now=${reqCount}`);

    // ═══ SECTION 12: sanity ═══
    const unexpectedConsole = suite.consoleErrors.filter(e =>
      !/Failed to load resource[^\n]*401 \(Unauthorized\)/.test(e) &&
      !/Failed to load resource[^\n]*net::ERR_FAILED/.test(e));
    suite.log('12.1 No unexpected console errors', unexpectedConsole.length === 0,
      unexpectedConsole.slice(0, 3).join('; ') || `(${suite.consoleErrors.length} expected notices ignored)`);
    suite.log('12.2 No server 5xx errors', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));

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

main().catch(err => { console.error('HARNESS ERROR:', err); process.exit(2); });
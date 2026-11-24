'use strict';
// G3-RES-04 — Resource Monitoring UI (Slice 1). Read-only view over the published
// GET /api/v1/system/resources (ADR 026). The backend is never modified here: a
// real-fetch leg proves the live endpoint renders, then page.route fixtures drive
// every contract state (ok / partial / unavailable / measurement_failed), the
// CPU-only healthy path, multi-vendor GPUs, missing metrics, measured-zero vs
// unavailable, RU/EN relocalization, and the page-scoped refresh boundary.
// G3-RES-06 (registered 2026-11-23; formerly re-designated G3-RES-05B) adds the
// GoAl processes section leg: one/many/empty processes, unavailable-null never
// rendered as 0, measured zero as a value, pipeline and component binding,
// RU/EN, narrow screen, plus a live integration leg (real fake-runtime process
// through the real API — no route stubbing there).
const path = require('path');
const H = require('./harness.cjs');

const PORT = 19476;
const BASE = `http://127.0.0.1:${PORT}`;
const sleep = ms => new Promise(r => setTimeout(r, ms));

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

// G3-RES-06 — processes[] fixtures (ADR 026 A-026-1 wire shape). m-alpha and
// r-beta resolve through stubbed listing caches; m-gamma / m-zero stay raw IDs,
// which is the documented fallback when a launch record outlives its entity.
const P = (over) => Object.assign({
  instance_id: 'inst-x', model_id: 'm-x', runtime_id: 'r-x', pid: 1000,
  resources: { process_rss: R(1073741824, 'bytes', 'measured', 'fixture') },
}, over);
const PROC_A = P({ instance_id: 'inst-alpha', model_id: 'm-alpha', runtime_id: 'r-beta', pid: 4242,
  resources: { process_rss: R(1610612736, 'bytes', 'measured', 'kmem') } });
const PROC_B = P({ instance_id: 'inst-pipe', model_id: 'm-gamma', runtime_id: 'r-beta', pid: 4343,
  pipeline_id: 'pl-main', pipeline_entry_id: 'pe-0123456789abcdef',
  resources: { process_rss: R(null, 'bytes', 'unavailable', 'procfs', 'identity_mismatch') } });
const PROC_C = P({ instance_id: 'inst-zero', model_id: 'm-zero', runtime_id: 'r-beta', pid: 4444,
  resources: { process_rss: R(0, 'bytes', 'measured', 'kmem') } });
const FIX_PROC_ONE = report({ devices: [], processes: [PROC_A] });
const FIX_PROC_MULTI = report({ devices: [], processes: [PROC_A, PROC_B, PROC_C] });
const FIX_PROC_EMPTY = report({ devices: [], processes: [] });
// SECTION 15 needs BOTH a GPU card (VRAM unit/label localization) and process
// cards (runtime / RSS label + explanation localization) in one render.
const FIX_GPU_PROCS = report({ devices: [gpu(0, 'nvidia', 'NVIDIA GeForce RTX 4090', '551.23', 'dedicated', 2147483648)], processes: [PROC_A, PROC_B, PROC_C] });

async function main() {
  const ws = H.makeWorkspace('resources');
  const goalBin = H.buildGoal(ws);
  // The fake runtime is seeded via config only for the SECTION 13 live leg
  // (real process, real API). It changes nothing earlier: no instance exists
  // until SECTION 13 launches one, and the resources fixtures are mocked.
  const fakeRt = H.buildFakeRuntime(ws);
  H.writeConfig(ws, { webPort: PORT, runtimes: [{ id: 'rt-fake', name: 'RT Fake Live', executable: fakeRt, workingDirectory: path.dirname(fakeRt) }] });
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
      const appeared = await page.waitForFunction((txt) => {
        const el = document.querySelector('#resources-content');
        return !!el && (el.textContent || '').includes(txt);
      }, waitText, { timeout: 6000 }).then(() => true).catch(() => false);
      if (!appeared) {
        // T-2: a swallowed wait would let each downstream check fail silently;
        // name the stuck fixture precisely instead.
        suite.log(`FIXTURE WAIT TIMEOUT: "${waitText}" never rendered`, false,
          `state=${rep.state} devices=${(rep.devices || []).length} processes=${(rep.processes || []).length}`);
      }
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
    // RU locale on first render: the RAM figure may carry the locale decimal
    // separator and localized units, so the regex admits both dot and comma
    // and both unit alphabets (locale-aware fmtBytes).
    suite.log('1.3 real host shows a System section with RAM total', realText.includes('32.0 GiB') || /\d+([.,]\d+)?\s*(GiB|MiB|ГиБ|МиБ)/.test(realText), `text=${JSON.stringify(realText.slice(0, 120))}`);
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

    // ═══ SECTION 12: GoAl processes (G3-RES-06 per-process RAM) ═══
    // Stub the listing caches so ID→name joins are exercised on a real page
    // without touching the backend: the resources fixture stays the only mock
    // of the monitored endpoint.
    await page.route('**/api/v1/runtimes', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([{ id: 'r-beta', name: 'RT Beta' }]) }));
    await page.route('**/api/v1/models', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([{ id: 'm-alpha', name: 'Alpha Model', runtime_id: 'r-beta' }]) }));
    await page.route('**/api/v1/pipelines', route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([{ id: 'pl-main', name: 'Main Pipe', models: [{ id: 'pe-0123456789abcdef', model_id: 'm-gamma', auto_start: false }] }]) }));
    await page.evaluate(() => window.reloadAllData());
    await page.waitForTimeout(500);

    // One process.
    await setFixture(FIX_PROC_ONE, 'GoAl Processes');
    suite.log('12.1 processes section renders', (await contentText()).includes('GoAl Processes'));
    suite.log('12.2 one process card', (await page.locator('.res-proc').count()) === 1);
    const t12a = await contentText();
    suite.log('12.3 model + runtime names joined', t12a.includes('Alpha Model') && t12a.includes('RT Beta'));
    suite.log('12.4 PID shown', t12a.includes('4242'));
    suite.log('12.5 measured RSS 1.5 GiB shown', t12a.includes('1.5 GiB'));

    // Several processes incl. unavailable-null and measured-zero.
    await setFixture(FIX_PROC_MULTI, 'identity_mismatch');
    // T-3: every unavailable-process assertion is scoped to PROC_B's own card
    // (identified by its unique PID 4343), so a dash, tag or raw ID elsewhere
    // on the page cannot make these checks pass.
    const cardB = page.locator('.res-proc', { hasText: '4343' }).first();
    const cardBText = (await cardB.textContent()) || '';
    suite.log('12.6 three process cards', (await page.locator('.res-proc').count()) === 3 && (await cardB.count()) === 1);
    suite.log('12.7 unavailable process NOT hidden (raw-ID fallback inside its own card)', cardBText.includes('m-gamma'));
    suite.log('12.8 unavailable RSS shows absence in-card, never 0', cardBText.includes('—') && (await cardB.locator('.res-prov-unavailable').count()) === 1 && !cardBText.includes('0 B'));
    suite.log('12.9 bounded reason token shown in-card', cardBText.includes('identity_mismatch'));
    const cardC = page.locator('.res-proc', { hasText: '4444' }).first();
    const cardCText = (await cardC.textContent()) || '';
    suite.log('12.10 measured zero renders as a value (0 B, not —) in its own card', cardCText.includes('0 B') && !cardCText.includes('—'));
    suite.log('12.11 pipeline + component binding in-card', cardBText.includes('Main Pipe') && cardBText.includes('…89abcdef') && cardBText.includes('Pipeline') && cardBText.includes('Component'));

    // Empty list.
    await setFixture(FIX_PROC_EMPTY, 'No running GoAl processes');
    const t12c = await contentText();
    suite.log('12.12 empty list shows clear empty state', t12c.includes('No running GoAl processes') && t12c.includes('Live launches tracked by GoAl'));
    suite.log('12.13 empty list renders no cards', (await page.locator('.res-proc').count()) === 0);

    // RU localization (render re-runs from cache; the page-scoped 5 s timer may
    // still sample — localization itself must not blank or drop the section).
    await setFixture(FIX_PROC_MULTI, 'identity_mismatch');
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(400);
    const t12ru = await contentText();
    suite.log('12.14 RU section title localized', t12ru.includes('Процессы GoAl'));
    suite.log('12.15 RU unavailable tag localized', t12ru.includes('недоступно'));
    suite.log('12.16 RU: i18nMissing empty + no raw keys', (await missingKeys()).length === 0 && (await domRawKeys()).length === 0);
    await H.screenshot(page, ws, '12-processes-ru');
    suite.log('12.17 RU relocalizes from cache, all cards stay', (await page.locator('.res-proc').count()) === 3);
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(400);
    suite.log('12.18 EN relocalizes back', (await contentText()).includes('GoAl Processes') && (await missingKeys()).length === 0);

    // Narrow screen: cards stay stacked, no horizontal overflow.
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(400);
    suite.log('12.19 narrow screen keeps all three cards', (await page.locator('.res-proc').count()) === 3);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    suite.log('12.20 narrow screen: no horizontal overflow', overflow <= 0, `overflow=${overflow}px`);
    await H.screenshot(page, ws, '12-processes-narrow');
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(300);

    // ═══ SECTION 13: live integration (T-1) — one real fake-runtime launch ═══
    // No page.route involvement: the launch, the listing caches and the
    // resources report all come from the running server. Guarded so a missing
    // wire entry can never pass via dash/label coincidence elsewhere.
    await page.unroute(RES_URL).catch(() => {});
    await page.unroute('**/api/v1/runtimes').catch(() => {});
    await page.unroute('**/api/v1/models').catch(() => {});
    await page.unroute('**/api/v1/pipelines').catch(() => {});
    const mk = await H.api(BASE, 'POST', '/api/v1/models', { name: 'Res Live Model', runtime_id: 'rt-fake', args: ['infinite'] });
    const mId = mk.data && mk.data.id;
    const st = await H.api(BASE, 'POST', `/api/v1/models/${mId}/start`);
    const instId = st.data && st.data.id;
    let liveProc = null;
    let livePid = 0;
    for (let i = 0; i < 40 && !liveProc; i++) {
      const rep = await H.api(BASE, 'GET', '/api/v1/system/resources');
      const hit = ((rep.data && rep.data.processes) || []).find(p => p.instance_id === instId);
      if (hit && hit.pid > 0) { liveProc = hit; livePid = hit.pid; }
      else await sleep(500);
    }
    suite.log('13.1 live API: processes[] carries the real instance with a PID', !!liveProc && livePid === liveProc.pid, `inst=${instId} pid=${livePid}`);
    await page.evaluate(() => window.reloadAllData());
    await sleep(600);
    await page.evaluate(() => window.loadResources());
    const domOk = await page.waitForFunction((pid) => {
      // Lookaround, not \b: concatenated cell text is 'PID'+digits+'RAM', and
      // a word boundary never exists between a letter and a digit there.
      return Array.from(document.querySelectorAll('.res-proc')).some(el => new RegExp('(?<!\\d)' + pid + '(?!\\d)').test(el.textContent || ''));
    }, livePid, { timeout: 12000 }).then(() => true).catch(() => false);
    suite.log('13.2 DOM: live process card renders with the real PID', domOk, `pid=${livePid}`);
    const liveCard = page.locator('.res-proc').filter({ has: page.locator('.res-row-val', { hasText: new RegExp('^' + livePid + '$') }) }).first();
    const liveCardText = (await liveCard.textContent()) || '';
    suite.log('13.3 live card joins real model + runtime names (no stubs)', liveCardText.includes('Res Live Model') && liveCardText.includes('RT Fake Live'), `card=${liveCardText.slice(0, 90)}`);
    const rssRowText = (await liveCard.locator('.res-proc-rss').textContent()) || '';
    const supported = process.platform === 'win32' || process.platform === 'linux';
    suite.log('13.4 live RSS is measured on the supported platform', supported && /\d+(\.\d+)?\s?(B|KiB|MiB|GiB)/.test(rssRowText) && !rssRowText.includes('—') && (await liveCard.locator('.res-prov').count()) === 0, `platform=${process.platform} rss='${rssRowText.trim()}'`);
    await H.screenshot(page, ws, '13-live-process-card');

    // Post-stop leg: per A-1 entries are emitted for NON-TERMINAL instances
    // only, so a confirmed stop must remove the entry from the live API and
    // from the DOM — a stale card or a lingering OS process would fail here.
    await H.api(BASE, 'POST', `/api/v1/instances/${instId}/stop`);
    let apiGone = false;
    for (let i = 0; i < 40 && !apiGone; i++) {
      const rep = await H.api(BASE, 'GET', '/api/v1/system/resources');
      apiGone = !((rep.data && rep.data.processes) || []).some(p => p.instance_id === instId);
      if (!apiGone) await sleep(500);
    }
    suite.log('13.5 after stop: API entry removed (terminal records emit no entry, A-1)', apiGone, `pid=${livePid}`);
    await page.evaluate(() => window.loadResources());
    const domGone = await page.waitForFunction((pid) => {
      return !Array.from(document.querySelectorAll('.res-proc')).some(el => new RegExp('(?<!\\d)' + pid + '(?!\\d)').test(el.textContent || ''));
    }, livePid, { timeout: 12000 }).then(() => true).catch(() => false);
    suite.log('13.6 after stop: DOM card removed — no stale row, no fabricated 0', domGone, `pid=${livePid} cards=${await page.locator('.res-proc').count()}`);
    const instRow = await H.api(BASE, 'GET', `/api/v1/instances/${instId}`);
    const instState = instRow.data && (instRow.data.state || instRow.data.status);
    suite.log('13.7 cleanup: launch record reached a terminal state (OS process gone)', ['exited', 'failed', 'stale'].includes(instState), `state=${instState}`);
    await H.api(BASE, 'DELETE', `/api/v1/models/${mId}`).catch(() => {});
    await H.api(BASE, 'DELETE', '/api/v1/runtimes/rt-fake').catch(() => {});

    // ═══ SECTION 15: RU/EN unit localization (v2.3.0 release polish) ═══
    // Localized binary units + locale decimal separator (RU: comma), localized
    // VRAM / Runtime / RSS labels, and the localized RSS explanation. Values
    // and thresholds (1024-based) are unchanged: the same 1.5 GiB / 24 GiB
    // readings render as "1,5 ГиБ" in RU and "1.5 GiB" in EN.
    await setFixture(FIX_GPU_PROCS, 'identity_mismatch');
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(400);
    const t15ru = await contentText();
    suite.log('15.1 RU GiB value uses localized unit', t15ru.includes('1,5 ГиБ'));
    suite.log('15.2 RU GPU VRAM value uses localized unit', t15ru.includes('24,0 ГиБ'));
    suite.log('15.3 RU runtime label is Рантайм', t15ru.includes('Рантайм'));
    suite.log('15.4 RU RSS label is ОЗУ процесса', t15ru.includes('ОЗУ процесса'));
    suite.log('15.5 RU RSS explanation shown', t15ru.includes('Резидентная память процесса может включать общие страницы и смонтированные в память файлы моделей'));
    suite.log('15.6 RU VRAM label uses the Russian equivalent', t15ru.includes('Видеопамять: всего'));
    suite.log('15.7 RU: no untranslated VRAM/English unit remains in the view', !t15ru.includes('VRAM') && !/GiB|MiB|KiB/.test(t15ru));
    // i18nMissing accumulates across the whole session: the SECTION 13 live
    // instance renders its `stopping` state through historyStateLabel, which
    // probes the absent history.state.stopping key before falling back to the
    // present models.status.stopping (display text is correct; the dynamic-key
    // gap is pre-existing at HEAD and outside this suite's scope). Assert that
    // no resources.* key is missing and that no raw key reaches the DOM.
    const miss15ru = (await missingKeys()).filter(k => k.startsWith('resources.'));
    suite.log('15.8 RU: no resources.* key missing + no raw keys in DOM', miss15ru.length === 0 && (await domRawKeys()).length === 0,
      `resourcesMissing=${JSON.stringify(miss15ru.slice(0, 5))} raw=${JSON.stringify((await domRawKeys()).slice(0, 5))}`);
    await H.screenshot(page, ws, '15-ru-units');
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(400);
    const t15en = await contentText();
    suite.log('15.9 EN GiB value keeps ASCII unit and dot', t15en.includes('1.5 GiB'));
    suite.log('15.10 EN GPU VRAM value keeps ASCII unit', t15en.includes('24.0 GiB'));
    suite.log('15.11 EN labels unchanged (Runtime / Process RAM)', t15en.includes('Runtime') && t15en.includes('Process RAM'));
    suite.log('15.12 EN RSS explanation shown', t15en.includes('Resident process memory may include shared pages and memory-mapped model files'));
    const miss15en = (await missingKeys()).filter(k => k.startsWith('resources.'));
    suite.log('15.13 EN: no resources.* key missing + no raw keys in DOM', miss15en.length === 0 && (await domRawKeys()).length === 0,
      `resourcesMissing=${JSON.stringify(miss15en.slice(0, 5))} raw=${JSON.stringify((await domRawKeys()).slice(0, 5))}`);

    // ═══ SECTION 14: sanity ═══
    const unexpectedConsole = suite.consoleErrors.filter(e =>
      !/Failed to load resource[^\n]*401 \(Unauthorized\)/.test(e) &&
      !/Failed to load resource[^\n]*net::ERR_FAILED/.test(e));
    suite.log('14.1 No unexpected console errors', unexpectedConsole.length === 0,
      unexpectedConsole.slice(0, 3).join('; ') || `(${suite.consoleErrors.length} expected notices ignored)`);
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

main().catch(err => { console.error('HARNESS ERROR:', err); process.exit(2); });
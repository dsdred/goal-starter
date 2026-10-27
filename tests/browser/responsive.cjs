'use strict';
const path = require('path');
const fs = require('fs');
const H = require('./harness.cjs');

const PORT = 19472;
const BASE = `http://127.0.0.1:${PORT}`;

async function main() {
  const ws = H.makeWorkspace('responsive');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  // Deterministic long-path fixture: a copy of the canonical fake-runtime in a
  // deep nested dir with a very long binary name, inside the fresh per-run
  // workspace (no reliance on pre-existing files in os.tmpdir()).
  const longDir = path.join(ws.dir, 'llama-community-runtimes', 'llama.cpp', 'build', 'bin');
  const longExe = path.join(longDir, 'llama-server-with-a-very-long-binary-name-for-stress' + H.EXE);
  fs.mkdirSync(longDir, { recursive: true });
  fs.copyFileSync(fakeRt, longExe);
  if (!H.IS_WIN) fs.chmodSync(longExe, 0o755);

  H.writeConfig(ws, {
    webPort: PORT,
    authEnabled: false,
    runtimes: [
      { id: 'rt-a', name: 'Llama.cpp', executable: fakeRt, workingDirectory: fakeRtDir },
      { id: 'rt-long', name: 'Very Long Runtime Name For Overflow Stress (llama.cpp community build 2026-08 stable release channel)', executable: longExe, workingDirectory: longDir },
    ],
  });

  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started.');

  const api = (method, urlPath, body) => H.api(BASE, method, urlPath, body);
  const sleep = ms => new Promise(r => setTimeout(r, ms));

  // Seed: model A (kept running -> Instances view), model B (start+stop -> History view)
  const m1 = await api('POST', '/api/v1/models', { name: 'Resp Model A', runtime_id: 'rt-a', args: ['infinite'] });
  const m2 = await api('POST', '/api/v1/models', { name: 'Resp Model B', runtime_id: 'rt-a', args: ['stdout'] });
  const s1 = await api('POST', `/api/v1/models/${m1.data.id}/start`);
  await sleep(1500);
  const s2 = await api('POST', `/api/v1/models/${m2.data.id}/start`);
  await sleep(1500);
  const bStop = await api('POST', `/api/v1/models/${m2.data.id}/stop`);
  await sleep(1500);
  // ADR 023 T-5 needs a rendered Pipeline card in this suite: the density and
  // casing rules are scoped to #model-list, so the Pipeline card is the control
  // that proves they did not leak onto the shared .model-row pattern. Model C is
  // never started and the pipeline owns no instance, so both chips and the
  // aggregate badge resolve to the deterministic `stopped` state.
  const m3 = await api('POST', '/api/v1/models', { name: 'Resp Model C', runtime_id: 'rt-a', args: ['stdout'] });
  const pipe = await api('POST', '/api/v1/pipelines', {
    name: 'Resp Isolation Pipeline',
    models: [{ model_id: m2.data.id }, { model_id: m3.data.id }],
  });

  const suite = H.newSuite('RESPONSIVE (monotonic 768px contract)');
  suite.log('Seed: model A created', m1.status === 201, `status=${m1.status}`);
  suite.log('Seed: model B created', m2.status === 201, `status=${m2.status}`);
  suite.log('Seed: instance A started', s1.status === 200, `state=${s1.data && s1.data.state}`);
  suite.log('Seed: instance B started', s2.status === 200, `state=${s2.data && s2.data.state}`);
  suite.log('Seed: instance B stopped (history entry)', bStop.status === 200);
  suite.log('Seed: model C created', m3.status === 201, `status=${m3.status}`);
  suite.log('Seed: isolation pipeline created', pipe.status === 201, `status=${pipe.status} id=${pipe.data && pipe.data.id}`);

  const instList = await api('GET', '/api/v1/instances');
  const live = (instList.data || []).filter(i => i.state === 'running' || i.state === 'starting' || i.state === 'orphan');
  suite.log('Seed: Instances view has live data', live.length >= 1, `states=${JSON.stringify((instList.data || []).map(i => i.state))}`);

  // Representation of one entity list: 'table' | 'cards' | 'BOTH' | 'neither'
  // Detection uses the CSS classes set by the fit-if-fits contract (applyFit):
  //   .table-fit.fit-table  = table visible (position:static, visibility:visible)
  //   .table-fit:not(.fit-table) = table hidden (position:absolute, height:0, visibility:hidden)
  //   #*-compact.visible    = cards visible (display:flex)
  //   #*-compact            = cards hidden (display:none)
  async function representation(page, prefix) {
    return page.evaluate(p => {
      const t = document.getElementById(p + '-table-wrap');
      const c = document.getElementById(p + '-compact');
      const tv = !!(t && t.classList.contains('fit-table'));
      const cv = !!(c && c.classList.contains('visible'));
      if (tv && cv) return 'BOTH';
      if (tv) return 'table';
      if (cv) return 'cards';
      return 'neither';
    }, prefix);
  }

  // Page-level horizontal overflow: any visible element extending past the
  // viewport that is NOT inside a horizontally scrollable (contained) ancestor.
  // Elements inside a .table-fit:not(.fit-table) wrapper are the hidden
  // measurement table (cards mode) — they are laid out for scrollWidth but
  // intentionally invisible, so they are excluded from the overflow check.
  async function pageOverflow(page) {
    return page.evaluate(() => {
      const vw = window.innerWidth;
      let worst = 0, culprit = '';
      const bad = [];
      document.querySelectorAll('body *').forEach(el => {
        const r = el.getBoundingClientRect();
        if (r.width === 0 || r.height === 0) return;
        if (r.right > worst) { worst = r.right; culprit = String(el.id || el.className || el.tagName).slice(0, 40); }
        if (r.right > vw + 1) {
          let n = el, contained = false, hiddenTable = false;
          while (n && n !== document.body) {
            if (n.classList && n.classList.contains('table-fit') && !n.classList.contains('fit-table')) { hiddenTable = true; break; }
            const cs = getComputedStyle(n);
            if (/(auto|scroll)/.test(cs.overflowX) && n.scrollWidth > n.clientWidth + 1) { contained = true; break; }
            n = n.parentElement;
          }
          if (!contained && !hiddenTable) bad.push(String(el.id || el.className || el.tagName).slice(0, 60) + '@' + Math.round(r.right));
        }
      });
      return { worst, culprit, vw, bad: bad.slice(0, 5) };
    });
  }

  async function containedScroll(page, prefix) {
    return page.evaluate(p => {
      const t = document.getElementById(p + '-table-wrap');
      return t ? Math.max(0, t.scrollWidth - t.clientWidth) : 0;
    }, prefix);
  }

  const browser = await H.launchBrowser();
  const context = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await context.newPage();
  suite.watchPage(page);

  const viewports = [
    { w: 1920, h: 1080 }, { w: 1440, h: 900 }, { w: 1280, h: 800 }, { w: 1024, h: 768 },
    { w: 768, h: 1024 }, { w: 600, h: 800 }, { w: 430, h: 932 }, { w: 375, h: 812 }
  ];
  const views = [
    { nav: 'adv-runtimes', prefix: 'runtimes', shot: 'runtimes' },
    { nav: 'adv-instances', prefix: 'instances', shot: 'instances' },
    { nav: 'history', prefix: 'history', shot: 'history' }
  ];
  const shotsAt = new Set(['1920', '1280', '1024', '768', '600', '430', '375']);

  let ok = false;
  try {
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);

    const seq = { runtimes: [], instances: [], history: [] };

    for (const vp of viewports) {
      console.log(`\n═══ Viewport ${vp.w}x${vp.h} ═══`);
      await page.setViewportSize({ width: vp.w, height: vp.h });
      await page.waitForTimeout(250);

      for (const v of views) {
        await page.evaluate(nav => window.navigate(nav), v.nav);
        await page.waitForTimeout(250);
        const rep = await representation(page, v.prefix);
        seq[v.prefix].push(`${vp.w}:${rep}`);
        suite.log(`${v.prefix} @${vp.w}: representation=${rep}`, rep === 'table' || rep === 'cards', `rep=${rep}`);

        const of = await pageOverflow(page);
        suite.log(`${v.prefix} @${vp.w}: no page-level horizontal overflow`, of.bad.length === 0,
          of.bad.length ? `bad=${of.bad.join(', ')}` : `worst=${Math.round(of.worst)}px (vw=${of.vw})`);

        const actionsOk = await page.evaluate(args => {
          const p = args.p, repVal = args.rep;
          const isTable = repVal === 'table';
          let rows;
          if (isTable) {
            const map = { runtimes: '#adv-runtimes-body tr', instances: '#adv-instances-body tr', history: '#history-body tr' };
            rows = [...document.querySelectorAll(map[p])];
            return rows.length > 0 && rows.every(tr => {
              const btns = [...tr.querySelectorAll('.actions-cell .btn, #adv-instances-body .actions-cell .icon-btn, .actions-cell .icon-btn')];
              return btns.length >= 1 && btns.every(b => { const r = b.getBoundingClientRect(); return r.width > 0 && r.height > 0; });
            });
          }
          const map = { runtimes: '.crt-row', instances: '.cinst-row', history: '.chist-row' };
          rows = [...document.querySelectorAll(map[p])];
          return rows.length > 0 && rows.every(row => {
            const btns = [...row.querySelectorAll('.compact-actions .icon-btn')];
            const main = row.querySelector('.compact-main') ? row.querySelector('.compact-main').getBoundingClientRect() : null;
            const act = row.querySelector('.compact-actions').getBoundingClientRect();
            const noOverlap = !main || main.right <= act.left + 1;
            const inView = act.right <= window.innerWidth + 1 && act.width > 0;
            return btns.length >= 1 && btns.every(b => b.getBoundingClientRect().width > 0) && noOverlap && inView;
          });
        }, { p: v.prefix, rep });
        suite.log(`${v.prefix} @${vp.w}: required actions present and not clipped`, actionsOk);

        const cont = await containedScroll(page, v.prefix);
        if (cont > 0) console.log(`  [info] ${v.prefix} @${vp.w}: contained table scroll = ${cont}px (reachable, no page overflow)`);

        if (shotsAt.has(String(vp.w)) && v.shot === 'runtimes') {
          await H.screenshot(page, ws, `runtimes-${vp.w > 768 ? 'table' : 'cards'}-${vp.w}`);
        }
        if (shotsAt.has(String(vp.w)) && vp.w <= 768 && v.shot !== 'runtimes') {
          await H.screenshot(page, ws, `${v.shot}-${vp.w}`);
        }
      }
    }

    // Monotonicity: sequence across shrinking viewports must be table* cards*
    for (const v of views) {
      const reps = seq[v.prefix].map(s => s.split(':')[1]);
      const firstCards = reps.indexOf('cards');
      const lastTable = reps.lastIndexOf('table');
      const monotonic = reps.every(r => r === 'table' || r === 'cards') && (firstCards === -1 || lastTable === -1 || lastTable < firstCards);
      suite.log(`monotonic ${v.prefix}: ${reps.join(' -> ')}`, monotonic);
    }

    // Fit-if-fits sanity: at the narrowest viewport all views are CARDS.
    // At the widest viewport, at least one view with narrow enough content shows TABLE.
    for (const v of views) {
      const reps = seq[v.prefix].map(s => s.split(':')[1]);
      const narrowest = reps[reps.length - 1];
      suite.log(`fit-if-fits ${v.prefix}: CARDS at 375px`, narrowest === 'cards', `got=${narrowest}`);
    }
    const anyTableAtWidest = views.some(v => seq[v.prefix][0].split(':')[1] === 'table');
    suite.log('fit-if-fits: at least one view shows TABLE at 1920px', anyTableAtWidest,
      views.map(v => v.prefix + '=' + seq[v.prefix][0].split(':')[1]).join(', '));

    // Runtimes contract specifics
    console.log('\n═══ Runtimes contract specifics ═══');
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.evaluate(() => window.navigate('adv-runtimes'));
    await page.waitForTimeout(250);
    const cols = await page.evaluate(() => [...document.querySelectorAll('#runtimes-table-wrap thead th')].map(th => th.textContent.trim()));
    suite.log('desktop table columns = Имя / Исполняемый файл / Рабочая папка / actions',
      cols.length === 4 &&
        /Имя|Name/i.test(cols[0]) &&
        /Исполняемый|Executable/i.test(cols[1]) &&
        /Рабочая папка|Workdir|Working/i.test(cols[2]) &&
        cols[3].trim() === '',
      `cols=${JSON.stringify(cols)}`);

    await page.setViewportSize({ width: 430, height: 932 });
    await page.evaluate(() => window.navigate('adv-runtimes'));
    await page.waitForTimeout(250);
    const cardInfo = await page.evaluate(() => {
      const rows = [...document.querySelectorAll('.crt-row')];
      return rows.map(r => ({
        name: (r.querySelector('.compact-l1') || {}).textContent || '',
        exec: (r.querySelector('.compact-l2') || {}).textContent || '',
        cwd: (r.querySelector('.compact-l3') || {}).textContent || '',
        hasL3: !!r.querySelector('.compact-l3'),
        actions: r.querySelectorAll('.compact-actions .icon-btn').length
      }));
    });
    const cardOk = cardInfo.length === 2 &&
      cardInfo.every(c => c.name.length > 0 && c.exec.length > 0 && c.actions === 2) &&
      cardInfo.every(c => c.hasL3 && c.cwd.length > 0);
    suite.log('mobile card keeps Name + Executable + 2 actions + Working directory line', cardOk, JSON.stringify(cardInfo));

    // Instances contract specifics (ADR 013 UX geometry): short mono values
    // (ID / PID / Exit) stay on one line (no character-by-character wrap), the
    // model name ellipsizes, the Started/Stopped timestamps may break at the
    // space (date over time) instead of wrapping anywhere, and the mobile card
    // is Model -> State (not State -> Model).
    console.log('\n═══ Instances contract specifics ═══');
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.evaluate(() => window.navigate('adv-instances'));
    await page.waitForTimeout(300);
    const instGeom = await page.evaluate(() => {
      const cs = (sel) => { const el = document.querySelector(sel); return el ? getComputedStyle(el) : null; };
      const monoCs = cs('#adv-instances-body .inst-mono');
      const modelCs = cs('#adv-instances-body .inst-model');
      const timeCs = cs('#adv-instances-body .inst-time');
      return {
        hasRow: !!document.querySelector('#adv-instances-body tr'),
        monoNowrap: !!monoCs && monoCs.whiteSpace === 'nowrap',
        modelNowrap: !!modelCs && modelCs.whiteSpace === 'nowrap',
        modelEllipsis: !!modelCs && modelCs.textOverflow === 'ellipsis',
        timeWrapNormal: !!timeCs && timeCs.overflowWrap === 'normal',
        actionsInRow: !!document.querySelector('#adv-instances-body .actions-cell .btn, #adv-instances-body .actions-cell .icon-btn')
      };
    });
    suite.log('desktop instances: ID/PID mono nowrap + model ellipsis + timestamp wraps at space (not anywhere)',
      instGeom.hasRow && instGeom.monoNowrap && instGeom.modelNowrap && instGeom.modelEllipsis && instGeom.timeWrapNormal && instGeom.actionsInRow,
      JSON.stringify(instGeom));
    await page.setViewportSize({ width: 900, height: 900 });
    await page.waitForTimeout(300);
    const instGeom900 = await page.evaluate(() => {
      const cs = (sel) => { const el = document.querySelector(sel); return el ? getComputedStyle(el) : null; };
      return {
        hasRow: !!document.querySelector('#adv-instances-body tr'),
        monoNowrap: (() => { const c = cs('#adv-instances-body .inst-mono'); return !!c && c.whiteSpace === 'nowrap'; })(),
        actionsInRow: !!document.querySelector('#adv-instances-body .actions-cell .btn, #adv-instances-body .actions-cell .icon-btn')
      };
    });
    suite.log('@900 instances: mono nowrap still holds (no char-wrap on a narrower table)',
      instGeom900.hasRow && instGeom900.monoNowrap && instGeom900.actionsInRow, JSON.stringify(instGeom900));
    // Mobile: the compact instance card header is Model -> State (the model title
    // precedes the state badge in DOM order).
    await page.setViewportSize({ width: 430, height: 932 });
    await page.evaluate(() => window.navigate('adv-instances'));
    await page.waitForTimeout(300);
    const cardOrder = await page.evaluate(() => {
      const row = document.querySelector('.cinst-row');
      if (!row) return { hasRow: false };
      const l1 = row.querySelector('.compact-l1');
      if (!l1) return { hasRow: true, hasL1: false };
      const children = Array.from(l1.children);
      const title = l1.querySelector('.compact-title');
      const badge = l1.querySelector('.status-badge');
      const ti = title ? children.indexOf(title) : -1;
      const bi = badge ? children.indexOf(badge) : -1;
      return { hasRow: true, hasL1: true, statusFirst: ti >= 0 && bi >= 0 && bi < ti };
    });
    suite.log('mobile instance card header order is Status -> Model', cardOrder.statusFirst === true, JSON.stringify(cardOrder));
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.waitForTimeout(200);

    // Sidebar behavior
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.waitForTimeout(250);
    const sb1280 = await page.evaluate(() => {
      const s = document.getElementById('sidebar').getBoundingClientRect();
      const m = document.getElementById('main-content').getBoundingClientRect();
      return { sidebarVisible: s.right > 0 && s.left < window.innerWidth, contentLeft: m.left, vw: window.innerWidth };
    });
    suite.log('@1280: sidebar open, content offset by sidebar', sb1280.sidebarVisible && sb1280.contentLeft >= 150, JSON.stringify(sb1280));

    await page.setViewportSize({ width: 768, height: 1024 });
    await page.waitForTimeout(700);
    const sb768 = await page.evaluate(() => {
      const s = document.getElementById('sidebar').getBoundingClientRect();
      const m = document.getElementById('main-content').getBoundingClientRect();
      const tb = document.getElementById('mobile-topbar');
      return { sidebarOffscreen: s.right <= 0, contentLeft: m.left, topbarVisible: getComputedStyle(tb).display !== 'none', vw: window.innerWidth };
    });
    suite.log('@768: sidebar collapsed to drawer, content full-width, topbar shown',
      sb768.sidebarOffscreen && sb768.contentLeft === 0 && sb768.topbarVisible, JSON.stringify(sb768));

    // Owner UI regression: the Settings sidebar icon is a clean, symmetric
    // outline gear with the shared nav-icon optical size and stroke convention.
    const settingsIconInfo = () => page.evaluate(() => {
      const btn = document.querySelector('.nav-item[data-view="adv-settings"]');
      const svg = btn ? btn.querySelector('svg.nav-icon') : null;
      if (!svg) return { ok: false, issues: ['svg missing'], paths: 0, rect: null };
      const issues = [];
      const paths = Array.from(svg.querySelectorAll('path'));
      const r = svg.getBoundingClientRect();
      if (svg.getAttribute('viewBox') !== '0 0 24 24') issues.push('viewBox');
      if (svg.getAttribute('fill') !== 'none') issues.push('fill');
      if (svg.getAttribute('stroke') !== 'currentColor') issues.push('stroke');
      if (svg.getAttribute('stroke-width') !== '1.6') issues.push('stroke-width');
      if (paths.length !== 2) issues.push('paths=' + paths.length);
      if (r.width < 16 || r.width > 22 || r.height < 16 || r.height > 22) issues.push(`rendered=${r.width}x${r.height}`);
      const boxes = paths.map(p => {
        try {
          const b = p.getBBox();
          return { cx: b.x + b.width / 2, cy: b.y + b.height / 2, w: b.width, h: b.height };
        } catch (e) {
          return null;
        }
      });
      if (boxes.some(b => !b)) {
        issues.push('bbox unavailable');
      } else {
        boxes.forEach((b, i) => {
          if (Math.abs(b.cx - 12) > 0.25 || Math.abs(b.cy - 12) > 0.25) issues.push(`path${i} center=${b.cx.toFixed(2)},${b.cy.toFixed(2)}`);
          if (i === 0 && (b.w < 16 || b.w > 22 || b.h < 16 || b.h > 22)) issues.push(`outer=${b.w.toFixed(1)}x${b.h.toFixed(1)}`);
          if (i === 1 && (b.w < 5 || b.w > 7 || b.h < 5 || b.h > 7)) issues.push(`inner=${b.w.toFixed(1)}x${b.h.toFixed(1)}`);
        });
      }
      return { ok: issues.length === 0, issues, paths: paths.length, rect: { w: r.width, h: r.height } };
    });

    await page.setViewportSize({ width: 1280, height: 800 });
    await page.waitForTimeout(250);
    const desktopSettings = await settingsIconInfo();
    suite.log('@1280: Settings sidebar icon is a symmetric outline gear', desktopSettings.ok, JSON.stringify(desktopSettings.issues));

    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(250);
    await page.evaluate(() => window.toggleDrawer());
    await page.waitForTimeout(250);
    const mobileSettings = await settingsIconInfo();
    const mobileDrawerVisible = await page.evaluate(() => {
      const s = document.getElementById('sidebar').getBoundingClientRect();
      return s.right > 0 && s.left < window.innerWidth;
    });
    suite.log('@390: Settings icon in the mobile drawer is a symmetric outline gear', mobileDrawerVisible && mobileSettings.ok, JSON.stringify(mobileSettings.issues));
    await page.evaluate(() => window.toggleDrawer());
    await page.waitForTimeout(200);

    // ═══ ADR 023 — Models density, Pipeline isolation, casing scope (T-5, T-7) ═══
    // Baseline literals are the shipped declarations at c3002ec: `.model-list`
    // gap 6px (style.css:231), `.model-row` gap 12px / padding 9px 14px (:232-236)
    // and the ≤768px `.model-row{gap:8px}` (:740). Pipelines still reuse them
    // verbatim, so a Pipeline card that drifts from these values means the
    // #model-list density block leaked onto the shared pattern (violates Q2/D-4).
    const BASELINE_CARD = { padding: '9px 14px', gap: '12px', listGap: '6px' };
    const BASELINE_CARD_MOBILE = { padding: '9px 14px', gap: '8px', listGap: '6px' };
    const DENSE_CARD = { padding: '4px 14px', gap: '10px', listGap: '4px' };
    const DENSE_MOBILE_GAP = '6px';

    const measureCard = (id, base) => page.evaluate((a) => {
      window.scrollTo(0, 0);
      const list = document.getElementById(a.id);
      const row = list ? list.querySelector('.model-row') : null;
      if (!row) return null;
      const cs = getComputedStyle(row);
      const badge = row.querySelector('.model-row-name .status-badge');
      const chips = [...row.querySelectorAll('.pl-chips-wrap .status-badge')];
      const icons = [...row.querySelectorAll('.model-row-actions .icon-btn')];
      const title = row.querySelector('.model-row-title');
      const sub = row.querySelector('.model-row-sub');
      const mainEl = row.querySelector('.model-row-main');
      const mainRect = mainEl ? mainEl.getBoundingClientRect() : null;
      const vw = window.innerWidth, vh = window.innerHeight;
      const overlaps = (r) => !!mainRect &&
        r.left < mainRect.right - 1 && mainRect.left < r.right - 1 &&
        r.top < mainRect.bottom - 1 && mainRect.top < r.bottom - 1;
      const box = (el) => {
        const r = el.getBoundingClientRect();
        return { w: Math.round(r.width * 100) / 100, h: Math.round(r.height * 100) / 100,
          inViewport: r.left >= -0.5 && r.top >= -0.5 && r.right <= vw + 0.5 && r.bottom <= vh + 0.5,
          overlapsMain: overlaps(r) };
      };
      const shipped = box(row);
      // Snapshot the shipped computed values BEFORE any mutation: the object
      // returned by getComputedStyle is live and reports in-flight transition
      // values, which would attribute the A/B baseline to the shipped row.
      const out = {
        listGap: getComputedStyle(list).gap,
        padding: cs.padding, gap: cs.gap, fontSize: cs.fontSize,
        height: shipped.h,
        badgeClass: badge ? badge.className : null,
        badgeTransform: badge ? getComputedStyle(badge).textTransform : null,
        chipCount: chips.length,
        chipTransforms: chips.map(c => getComputedStyle(c).textTransform),
        chipTexts: chips.map(c => c.textContent.trim()),
        iconBoxes: icons.map(box),
        titleText: title ? title.textContent.trim() : null,
        titleBox: title ? box(title) : null,
        subText: sub ? sub.textContent.trim() : null,
        subBox: sub ? box(sub) : null,
      };
      // A/B on the same element: reinstate the c3002ec density declarations
      // inline, re-measure, then release the element back to the shipped sheet.
      // Same content, fonts and viewport — the delta is the density block alone.
      // `.model-row` animates `transition: all 0.15s` (style.css:235), so the
      // padding/gap change is applied with the transition off: a synchronous
      // getBoundingClientRect inside the 150ms window returns the FROM value,
      // which would report a zero delta as a real finding. The transition is
      // switched back on only after the inline styles are released, so the
      // restore is instant and no later leg observes an animating row.
      row.style.transition = 'none';
      row.style.padding = a.base.padding;
      row.style.gap = a.base.gap;
      out.heightWithBaseline = box(row).h;
      row.style.padding = '';
      row.style.gap = '';
      row.style.transition = '';
      return out;
    }, { id, base });

    await page.setViewportSize({ width: 1440, height: 900 });
    await page.evaluate(() => window.navigate('pipelines'));
    await page.waitForTimeout(600);
    const pipeDesktop = await measureCard('pipeline-list', BASELINE_CARD);
    await page.evaluate(() => window.navigate('models'));
    await page.waitForTimeout(600);
    const modelsDesktop = await measureCard('model-list', BASELINE_CARD);

    suite.log('T-5 Pipeline card keeps the baseline density (padding/gap/list gap == c3002ec)',
      !!pipeDesktop && pipeDesktop.padding === BASELINE_CARD.padding && pipeDesktop.gap === BASELINE_CARD.gap &&
        pipeDesktop.listGap === BASELINE_CARD.listGap,
      pipeDesktop ? `padding=${pipeDesktop.padding} gap=${pipeDesktop.gap} list_gap=${pipeDesktop.listGap}` : 'pipeline row absent');
    suite.log('T-5 Pipeline card keeps its baseline icon strip (3 icon buttons at 44x44, stopped pipeline)',
      !!pipeDesktop && pipeDesktop.iconBoxes.length === 3 && pipeDesktop.iconBoxes.every(b => b.w >= 44 && b.h >= 44),
      pipeDesktop ? `icons=${JSON.stringify(pipeDesktop.iconBoxes.map(b => b.w + 'x' + b.h))}` : 'pipeline row absent');
    suite.log('T-7 Models card carries the density block (padding/gap/list gap)',
      !!modelsDesktop && modelsDesktop.padding === DENSE_CARD.padding && modelsDesktop.gap === DENSE_CARD.gap &&
        modelsDesktop.listGap === DENSE_CARD.listGap,
      modelsDesktop ? `padding=${modelsDesktop.padding} gap=${modelsDesktop.gap} list_gap=${modelsDesktop.listGap}` : 'model row absent');
    suite.log('T-7 Density is a real height reduction on the same card (measured px)',
      !!modelsDesktop && modelsDesktop.heightWithBaseline > modelsDesktop.height,
      modelsDesktop ? `baseline=${modelsDesktop.heightWithBaseline}px dense=${modelsDesktop.height}px delta=${(modelsDesktop.heightWithBaseline - modelsDesktop.height).toFixed(2)}px` : 'no measurement');
    suite.log('T-7 Title and sub text survive the density change (non-empty, non-zero width)',
      !!modelsDesktop && !!modelsDesktop.titleText && modelsDesktop.titleBox && modelsDesktop.titleBox.w > 0 &&
        !!modelsDesktop.subText && modelsDesktop.subBox && modelsDesktop.subBox.w > 0,
      modelsDesktop ? `title=${JSON.stringify(modelsDesktop.titleText)} sub=${JSON.stringify(modelsDesktop.subText)}` : 'no measurement');
    // Absolute, not just relative: the shared value is pinned to the c3002ec
    // typography (`html { font-size: 14px }`, style.css:54 — untouched by this
    // delta, whose first insertion sits at :263), so a drift moving both the
    // Models and the Pipeline row cannot pass.
    suite.log('T-5/D-5 Density changed no typography (Models and Pipeline rows both stay at the baseline 14px)',
      !!modelsDesktop && !!pipeDesktop && modelsDesktop.fontSize === '14px' &&
        pipeDesktop.fontSize === '14px' && modelsDesktop.fontSize === pipeDesktop.fontSize,
      `models=${modelsDesktop && modelsDesktop.fontSize} pipeline=${pipeDesktop && pipeDesktop.fontSize}`);
    suite.log('T-5/D-4 Pipeline chip names keep text-transform: uppercase (excluded from D-3 by container)',
      !!pipeDesktop && pipeDesktop.chipCount === 2 && pipeDesktop.chipTransforms.every(x => x === 'uppercase'),
      pipeDesktop ? `chips=${pipeDesktop.chipCount} transforms=${JSON.stringify(pipeDesktop.chipTransforms)} texts=${JSON.stringify(pipeDesktop.chipTexts)}` : 'pipeline row absent');
    suite.log('T-4/D-3 The exemption is class-scoped, not container-wide: the Pipeline aggregate badge of a stopped pipeline stays uppercase',
      !!pipeDesktop && pipeDesktop.badgeClass === 'status-badge stopped' && pipeDesktop.badgeTransform === 'uppercase',
      pipeDesktop ? `badge_class=${JSON.stringify(pipeDesktop.badgeClass)} transform=${pipeDesktop.badgeTransform}` : 'pipeline row absent');

    // The casing exemption is a closed set: exactly the eight D-3 surface
    // selectors declare text-transform:none. This is the machine check for "no
    // global .status-badge override and no .pl-chips-wrap restore rule" — a
    // restore or a leak would have to appear as a declaration here.
    const casingDeclarations = await page.evaluate(() => {
      const none = [], uppercase = [], other = [];
      const push = (selectorText, value) => {
        selectorText.split(',').forEach(s => {
          const one = s.trim();
          if (value === 'none') none.push(one);
          else if (value === 'uppercase') uppercase.push(one);
          else other.push(one + ' => ' + value);
        });
      };
      const walk = (rules) => {
        for (const r of rules) {
          if (r.selectorText && r.style && r.style.textTransform) push(r.selectorText, r.style.textTransform);
          if (r.cssRules) walk(r.cssRules);
        }
      };
      for (const sheet of document.styleSheets) {
        try { walk(sheet.cssRules); } catch (e) { /* cross-origin sheet: none are loaded */ }
      }
      return { none, uppercase, other };
    });
    const D3_SURFACES = [
      '#model-list .status-badge.orphan', '#model-list .status-badge.pending',
      '#adv-instances-body .status-badge.orphan', '#adv-instances-body .status-badge.pending',
      '#instances-compact .status-badge.orphan', '#instances-compact .status-badge.pending',
      '#pipeline-list .model-row-name .status-badge.orphan', '#pipeline-list .model-row-name .status-badge.pending',
    ];
    const noneSet = [...new Set(casingDeclarations.none)].sort();
    suite.log('T-5/D-3 Casing exemption is exactly the eight state-word surfaces (no global override, no chip restore)',
      casingDeclarations.other.length === 0 && noneSet.length === 8 &&
        noneSet.join('|') === [...D3_SURFACES].sort().join('|'),
      `none=${JSON.stringify(noneSet)} other=${JSON.stringify(casingDeclarations.other)}`);
    suite.log('T-5/D-3 No text-transform declaration names the chip container',
      !casingDeclarations.none.concat(casingDeclarations.uppercase).some(s => s.includes('pl-chips-wrap')),
      `uppercase_selectors=${casingDeclarations.uppercase.length}`);
    suite.log('T-4/D-3 The base .status-badge rule still declares uppercase',
      casingDeclarations.uppercase.includes('.status-badge'),
      `uppercase=${JSON.stringify(casingDeclarations.uppercase)}`);

    await page.setViewportSize({ width: 430, height: 932 });
    await page.evaluate(() => window.navigate('pipelines'));
    await page.waitForTimeout(600);
    const pipeMobile = await measureCard('pipeline-list', BASELINE_CARD_MOBILE);
    await page.evaluate(() => window.navigate('models'));
    await page.waitForTimeout(600);
    const modelsMobile = await measureCard('model-list', BASELINE_CARD_MOBILE);
    suite.log('T-5 Pipeline card keeps the baseline mobile density (gap 8px, padding 9px 14px)',
      !!pipeMobile && pipeMobile.gap === BASELINE_CARD_MOBILE.gap && pipeMobile.padding === BASELINE_CARD_MOBILE.padding,
      pipeMobile ? `gap=${pipeMobile.gap} padding=${pipeMobile.padding}` : 'pipeline row absent');
    suite.log('T-7 Models card stays denser at mobile too (gap < the baseline 8px, padding 4px 14px)',
      !!modelsMobile && modelsMobile.gap === DENSE_MOBILE_GAP && modelsMobile.padding === DENSE_CARD.padding &&
        modelsMobile.heightWithBaseline > modelsMobile.height,
      modelsMobile ? `gap=${modelsMobile.gap} padding=${modelsMobile.padding} dense=${modelsMobile.height}px baseline=${modelsMobile.heightWithBaseline}px` : 'no measurement');
    await H.screenshot(page, ws, 'models-density-430');
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.evaluate(() => window.navigate('models'));
    await page.waitForTimeout(400);
    await H.screenshot(page, ws, 'models-density-1440');

    // ═══ ADR 023 T-6 — touch-target floor at all eight sweep viewports ═══
    console.log('\n═══ ADR 023 T-6: Models touch targets at all eight viewports ═══');
    for (const vp of viewports) {
      await page.setViewportSize({ width: vp.w, height: vp.h });
      await page.evaluate(() => window.navigate('models'));
      await page.waitForTimeout(350);
      const strip = await page.evaluate(() => {
        window.scrollTo(0, 0);
        const rows = [...document.querySelectorAll('#model-list .model-row')];
        const vw = window.innerWidth, vh = window.innerHeight;
        const cells = [];
        rows.forEach(row => {
          const mainRect = (row.querySelector('.model-row-main') || { getBoundingClientRect: () => null }).getBoundingClientRect();
          [...row.querySelectorAll('.model-row-actions .icon-btn')].forEach(btn => {
            const r = btn.getBoundingClientRect();
            cells.push({ row: rows.indexOf(row), w: Math.round(r.width * 100) / 100, h: Math.round(r.height * 100) / 100,
              inViewport: r.left >= -0.5 && r.top >= -0.5 && r.right <= vw + 0.5 && r.bottom <= vh + 0.5,
              overlapsMain: !!mainRect && r.left < mainRect.right - 1 && mainRect.left < r.right - 1 &&
                            r.top < mainRect.bottom - 1 && mainRect.top < r.bottom - 1 });
          });
        });
        return { rows: rows.length, cells, vw, vh };
      });
      const tooSmall = strip.cells.filter(c => c.w < 44 || c.h < 44);
      const offscreen = strip.cells.filter(c => !c.inViewport);
      const clashing = strip.cells.filter(c => c.overlapsMain);
      suite.log(`T-6 @${vp.w}: ${strip.cells.length} Models action targets >=44x44, in-viewport, no .model-row-main overlap`,
        strip.rows > 0 && tooSmall.length === 0 && offscreen.length === 0 && clashing.length === 0,
        `rows=${strip.rows} too_small=${JSON.stringify(tooSmall.slice(0, 3))} offscreen=${JSON.stringify(offscreen.slice(0, 3))} overlap=${JSON.stringify(clashing.slice(0, 3))}`);
    }
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(200);

    // ═══ ADR 023 T-10 — reduced motion, the project's first automated leg ═══
    // The ring is only emitted while a model is transitioning, and that interval
    // is sub-frame for the shipped 3000 ms tick, so the ring is asserted on an
    // element built with the renderer's own classes (`app.js:558-559`) appended
    // into a real Models row, with a non-reduced-motion negative control proving
    // the probe is sensitive to the media query. No production timing, seam or
    // state is fabricated.
    const ringProbe = (p) => p.evaluate(() => {
      window.scrollTo(0, 0);
      const row = document.querySelector('#model-list .model-row');
      if (!row) return null;
      const strip = row.querySelector('.model-row-actions');
      const host = document.createElement('span');
      host.className = 'model-transitional model-progress';
      host.setAttribute('aria-hidden', 'true');
      const ring = document.createElement('span');
      ring.className = 'model-progress-ring';
      host.appendChild(ring);
      strip.appendChild(host);
      let out;
      try {
        const rr = ring.getBoundingClientRect();
        const sr = strip.getBoundingClientRect();
        const br = row.getBoundingClientRect();
        out = {
          rowTransitionDuration: getComputedStyle(row).transitionDuration,
          rowTransitionProperty: getComputedStyle(row).transitionProperty,
          ringAnimationName: getComputedStyle(ring).animationName,
          ringW: Math.round(rr.width * 100) / 100, ringH: Math.round(rr.height * 100) / 100,
          ringInStrip: rr.left >= sr.left - 0.5 && rr.right <= sr.right + 0.5 &&
            rr.top >= sr.top - 0.5 && rr.bottom <= sr.bottom + 0.5,
          ringInRow: rr.left >= br.left - 0.5 && rr.right <= br.right + 0.5 &&
            rr.top >= br.top - 0.5 && rr.bottom <= br.bottom + 0.5,
          rowH: Math.round(br.height * 100) / 100,
        };
      } finally {
        strip.removeChild(host);
      }
      return out;
    });
    const rmContext = await browser.newContext({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' });
    const rmPage = await rmContext.newPage();
    suite.watchPage(rmPage);
    await rmPage.goto(BASE, { waitUntil: 'networkidle' });
    await rmPage.waitForTimeout(600);
    await rmPage.evaluate(() => window.navigate('models'));
    await rmPage.waitForTimeout(400);
    const reduced = await ringProbe(rmPage);
    const normal = await ringProbe(page);
    suite.log('T-10/D-8 Reduced motion: Models row transition suppressed (0s)',
      !!reduced && reduced.rowTransitionDuration === '0s',
      reduced ? `duration=${reduced.rowTransitionDuration} property=${reduced.rowTransitionProperty}` : 'model row absent');
    suite.log('T-10/D-8 Reduced motion: transition ring animation suppressed (animation-name none)',
      !!reduced && reduced.ringAnimationName === 'none', reduced ? `animation=${reduced.ringAnimationName}` : 'no probe');
    suite.log('T-10 Negative control: the same probe is sensitive without reduced motion',
      !!normal && normal.rowTransitionDuration !== '0s' && normal.ringAnimationName !== 'none',
      normal ? `duration=${normal.rowTransitionDuration} animation=${normal.ringAnimationName}` : 'no probe');
    // AC-7 after D-5: the ring is an ADR 022 marker living in the strip whose row
    // this delta made denser, so its box is measured, not assumed — non-zero and
    // inside both the action strip and the compacted row at 1440.
    suite.log('AC-7/D-5 The ring keeps a non-zero box inside the strip and inside the denser row (1440)',
      !!reduced && reduced.ringW > 0 && reduced.ringH > 0 && reduced.ringInStrip && reduced.ringInRow,
      reduced ? `ring=${reduced.ringW}x${reduced.ringH} in_strip=${reduced.ringInStrip} in_row=${reduced.ringInRow} row_h=${reduced.rowH}` : 'no probe');
    await rmContext.close();

    suite.log('no console errors', suite.consoleErrors.length === 0, suite.consoleErrors.slice(0, 3).join(' | '));
    suite.log('no server 5xx errors', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));

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

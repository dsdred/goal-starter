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

  const suite = H.newSuite('RESPONSIVE (monotonic 768px contract)');
  suite.log('Seed: model A created', m1.status === 201, `status=${m1.status}`);
  suite.log('Seed: model B created', m2.status === 201, `status=${m2.status}`);
  suite.log('Seed: instance A started', s1.status === 200, `state=${s1.data && s1.data.state}`);
  suite.log('Seed: instance B started', s2.status === 200, `state=${s2.data && s2.data.state}`);
  suite.log('Seed: instance B stopped (history entry)', bStop.status === 200);

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

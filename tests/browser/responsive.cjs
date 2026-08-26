'use strict';
const path = require('path');
const fs = require('fs');
const os = require('os');
const H = require('./harness.cjs');

const PORT = 19472;
const BASE = `http://127.0.0.1:${PORT}`;

async function main() {
  const ws = H.makeWorkspace('responsive');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  const longDir = path.join(os.tmpdir(), 'llama-community-runtimes', 'llama.cpp', 'build', 'bin');
  const longExe = path.join(longDir, 'llama-server-with-a-very-long-binary-name-for-stress' + H.EXE);

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
  async function representation(page, prefix) {
    return page.evaluate(p => {
      const t = document.getElementById(p + '-table-wrap');
      const c = document.getElementById(p + '-compact');
      const tv = !!(t && getComputedStyle(t).display !== 'none');
      const cv = !!(c && getComputedStyle(c).display !== 'none');
      if (tv && cv) return 'BOTH';
      if (tv) return 'table';
      if (cv) return 'cards';
      return 'neither';
    }, prefix);
  }

  // Page-level horizontal overflow: any visible element extending past the
  // viewport that is NOT inside a horizontally scrollable (contained) ancestor.
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
          let n = el, contained = false;
          while (n && n !== document.body) {
            const cs = getComputedStyle(n);
            if (/(auto|scroll)/.test(cs.overflowX) && n.scrollWidth > n.clientWidth + 1) { contained = true; break; }
            n = n.parentElement;
          }
          if (!contained) bad.push(String(el.id || el.className || el.tagName).slice(0, 60) + '@' + Math.round(r.right));
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
        const expected = vp.w > 768 ? 'table' : 'cards';
        seq[v.prefix].push(`${vp.w}:${rep}`);
        suite.log(`${v.prefix} @${vp.w}: representation=${rep}`, rep === expected, `expected=${expected}`);

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
              const btns = [...tr.querySelectorAll('.actions-cell .btn')];
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

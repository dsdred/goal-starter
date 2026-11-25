'use strict';
const path = require('path');
const fs = require('fs');
const crypto = require('crypto');
const H = require('./harness.cjs');

const PORT = 19499;
const BASE = `http://127.0.0.1:${PORT}`;

const sha256 = b => crypto.createHash('sha256').update(b).digest('hex');
const canonical = name => fs.readFileSync(path.join(H.ROOT, 'assets', 'brand', name));

module.exports = { BASE };

async function main() {
  const ws = H.makeWorkspace('branding');
  const goalBin = H.buildGoal(ws);
  H.writeConfig(ws, { webPort: PORT, authEnabled: false });
  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started (auth OFF).\n');

  const browser = await H.launchBrowser();
  let ok = false;
  const suite = H.newSuite('BRANDING (UI-BRAND-01: sidebar logo, unified top-bar logo, favicon)');
  try {
    // ═══ SECTION 1: favicon ═══
    {
      const ctx = await browser.newContext({ colorScheme: 'dark', viewport: { width: 1920, height: 1080 } });
      const page = await ctx.newPage();
      suite.watchPage(page);
      await page.goto(BASE, { waitUntil: 'networkidle' });
      await page.waitForTimeout(500);

      const links = await page.evaluate(() =>
        [...document.querySelectorAll('link[rel="icon"]')].map(l => ({ type: l.getAttribute('type'), href: l.getAttribute('href') })));
      suite.log('1.1 head declares favicon links (svg + ico fallback)',
        links.length === 2 &&
        links.some(l => l.href === '/static/brand/favicon.svg' && l.type === 'image/svg+xml') &&
        links.some(l => l.href === '/static/brand/favicon.ico'),
        JSON.stringify(links));

      const svgRes = await ctx.request.get(BASE + '/static/brand/favicon.svg');
      suite.log('1.2 favicon.svg served (200, image/svg+xml)',
        svgRes.ok() && /svg\+xml/.test(svgRes.headers()['content-type'] || ''),
        `status=${svgRes.status()} ct=${svgRes.headers()['content-type']}`);
      suite.log('1.3 favicon.svg bytes identical to canonical assets/brand',
        svgRes.ok() && sha256(await svgRes.body()) === sha256(canonical('favicon.svg')));

      const icoRes = await ctx.request.get(BASE + '/static/brand/favicon.ico');
      suite.log('1.4 favicon.ico served (200) and identical to canonical',
        icoRes.ok() && sha256(await icoRes.body()) === sha256(canonical('favicon.ico')),
        `status=${icoRes.status()}`);
      await ctx.close();
    }

    // ═══ SECTION 2: sidebar logo, dark context (system theme → dark) ═══
    {
      const ctx = await browser.newContext({ colorScheme: 'dark', viewport: { width: 1920, height: 1080 } });
      const page = await ctx.newPage();
      suite.watchPage(page);
      await page.goto(BASE, { waitUntil: 'networkidle' });
      await page.waitForTimeout(500);

      const st = await page.evaluate(() => {
        const d = document.querySelector('.sidebar-logo .brand-logo-dark');
        const l = document.querySelector('.sidebar-logo .brand-logo-light');
        return {
          theme: document.documentElement.getAttribute('data-theme'),
          dark: getComputedStyle(d).display, darkOk: d.complete && d.naturalWidth > 0,
          light: getComputedStyle(l).display, lightOk: l.complete && l.naturalWidth > 0,
          w: d.getBoundingClientRect().width, h: d.getBoundingClientRect().height,
        };
      });
      suite.log('2.1 dark context resolves data-theme=dark', st.theme === 'dark', `theme=${st.theme}`);
      suite.log('2.2 dark variant visible, light variant hidden', st.dark === 'block' && st.light === 'none',
        `dark=${st.dark} light=${st.light}`);
      suite.log('2.3 dark logo loaded and aspect ratio 327:128 preserved',
        st.darkOk && Math.abs(st.w / st.h - 327 / 128) < 0.05, `w=${st.w} h=${st.h}`);
      suite.log('2.4 light logo resource loaded (hidden, complete)', st.lightOk);

      const res = await ctx.request.get(BASE + '/static/brand/goal-logo-dark.svg');
      suite.log('2.5 goal-logo-dark.svg bytes identical to canonical',
        res.ok() && sha256(await res.body()) === sha256(canonical('goal-logo-dark.svg')));

      // Theme switch through the UI mechanism, same context
      await page.evaluate(() => setTheme('light'));
      await page.waitForTimeout(200);
      const sw = await page.evaluate(() => {
        const d = document.querySelector('.sidebar-logo .brand-logo-dark');
        const l = document.querySelector('.sidebar-logo .brand-logo-light');
        return { theme: document.documentElement.getAttribute('data-theme'), dark: getComputedStyle(d).display, light: getComputedStyle(l).display };
      });
      suite.log('2.6 setTheme("light") swaps the visible variant',
        sw.theme === 'light' && sw.dark === 'none' && sw.light === 'block',
        `theme=${sw.theme} dark=${sw.dark} light=${sw.light}`);

      // Reload: persisted theme keeps the correct variant
      await page.reload({ waitUntil: 'networkidle' });
      await page.waitForTimeout(500);
      const rl = await page.evaluate(() => ({
        theme: document.documentElement.getAttribute('data-theme'),
        light: getComputedStyle(document.querySelector('.sidebar-logo .brand-logo-light')).display,
      }));
      suite.log('2.7 after reload the persisted light theme keeps the light variant',
        rl.theme === 'light' && rl.light === 'block', `theme=${rl.theme} light=${rl.light}`);
      await ctx.close();
    }

    // ═══ SECTION 3: sidebar logo, light context (system theme → light) ═══
    {
      const ctx = await browser.newContext({ colorScheme: 'light', viewport: { width: 1920, height: 1080 } });
      const page = await ctx.newPage();
      suite.watchPage(page);
      await page.goto(BASE, { waitUntil: 'networkidle' });
      await page.waitForTimeout(500);

      const st = await page.evaluate(() => {
        const d = document.querySelector('.sidebar-logo .brand-logo-dark');
        const l = document.querySelector('.sidebar-logo .brand-logo-light');
        return { theme: document.documentElement.getAttribute('data-theme'), dark: getComputedStyle(d).display, light: getComputedStyle(l).display };
      });
      suite.log('3.1 light context resolves data-theme=light', st.theme === 'light', `theme=${st.theme}`);
      suite.log('3.2 light variant visible, dark variant hidden', st.dark === 'none' && st.light === 'block',
        `dark=${st.dark} light=${st.light}`);

      const res = await ctx.request.get(BASE + '/static/brand/goal-logo-light.svg');
      suite.log('3.3 goal-logo-light.svg bytes identical to canonical',
        res.ok() && sha256(await res.body()) === sha256(canonical('goal-logo-light.svg')));
      await H.screenshot(page, ws, 'branding-light-sidebar');
      await ctx.close();
    }

    // ═══ SECTION 4: compact icon in the mobile top bar ═══
    {
      const ctx = await browser.newContext({ colorScheme: 'dark', viewport: { width: 400, height: 800 } });
      const page = await ctx.newPage();
      suite.watchPage(page);
      await page.goto(BASE, { waitUntil: 'networkidle' });
      await page.waitForTimeout(500);

      const st = await page.evaluate(() => {
        const topbar = document.querySelector('.mobile-topbar');
        const d = topbar.querySelector('.brand-logo-dark');
        const l = topbar.querySelector('.brand-logo-light');
        const r = d.getBoundingClientRect();
        const shown = getComputedStyle(d).display !== 'none' && r.width > 0 && r.height > 0;
        const strayText = [...topbar.querySelectorAll('*')].filter(e => e.children.length === 0 && /GoAl/.test(e.textContent)).length;
        return {
          topbar: getComputedStyle(topbar).display,
          shown,
          dark: getComputedStyle(d).display,
          light: getComputedStyle(l).display,
          loaded: d.complete && d.naturalWidth > 0,
          w: r.width, h: r.height,
          strayText,
          height: topbar.getBoundingClientRect().height,
        };
      });
      suite.log('4.1 mobile top bar visible at 400px viewport', st.topbar === 'flex', `display=${st.topbar}`);
      suite.log('4.2 top bar carries the unified theme-aware logo (dark variant, 327:128 aspect, loaded)',
        st.shown && st.dark === 'block' && st.light === 'none' && st.loaded && Math.abs(st.w / st.h - 327 / 128) < 0.05,
        `dark=${st.dark} light=${st.light} w=${st.w} h=${st.h} loaded=${st.loaded}`);
      suite.log('4.3 no stray GoAl text element in the top bar (single brand image, no icon+text pair)',
        st.strayText === 0, `stray=${st.strayText}`);
      suite.log('4.4 top bar height unchanged (52px)', st.height === 52, `h=${st.height}`);
      await H.screenshot(page, ws, 'branding-mobile-topbar');
      await ctx.close();
    }

    // ═══ SECTION 5: login window branding ═══
    {
      const ctx = await browser.newContext({ colorScheme: 'dark', viewport: { width: 1440, height: 900 } });
      const page = await ctx.newPage();
      suite.watchPage(page);
      await page.goto(BASE, { waitUntil: 'networkidle' });
      await page.waitForTimeout(500);
      await page.evaluate(() => { document.getElementById('login-modal').style.display = 'flex'; });
      await page.waitForTimeout(300);

      const st = await page.evaluate(() => {
        const modal = document.getElementById('login-modal');
        const box = modal.querySelector('.modal-box');
        const d = modal.querySelector('.brand-logo-dark');
        const l = modal.querySelector('.brand-logo-light');
        const r = d.getBoundingClientRect();
        const br = box.getBoundingClientRect();
        const h2 = modal.querySelector('h2');
        const hr = h2.getBoundingClientRect();
        return {
          dark: getComputedStyle(d).display,
          light: getComputedStyle(l).display,
          h: r.height, w: r.width,
          loaded: d.complete && d.naturalWidth > 0,
          logoCx: (r.left + r.right) / 2, boxCx: (br.left + br.right) / 2,
          titleAlign: getComputedStyle(h2).textAlign,
          titleText: h2.textContent.trim(),
          titleCx: (hr.left + hr.right) / 2,
        };
      });
      suite.log('5.1 login logo is the full theme-aware logo at 48 CSS px with 327:128 aspect (dark)',
        st.dark === 'block' && st.light === 'none' && st.loaded && Math.abs(st.h - 48) < 1 && Math.abs(st.w / st.h - 327 / 128) < 0.05,
        `dark=${st.dark} light=${st.light} w=${st.w} h=${st.h} loaded=${st.loaded}`);
      suite.log('5.2 login logo is horizontally centered in the modal', Math.abs(st.logoCx - st.boxCx) <= 2,
        `logoCx=${st.logoCx} boxCx=${st.boxCx}`);
      suite.log('5.3 login title "Вход" is centered',
        st.titleText === 'Вход' && st.titleAlign === 'center' && Math.abs(st.titleCx - st.boxCx) <= 2,
        `text=${st.titleText} align=${st.titleAlign}`);
      await page.evaluate(() => setTheme('light'));
      await page.waitForTimeout(300);
      const stL = await page.evaluate(() => {
        const modal = document.getElementById('login-modal');
        const d = modal.querySelector('.brand-logo-dark');
        const l = modal.querySelector('.brand-logo-light');
        const r = l.getBoundingClientRect();
        return { dark: getComputedStyle(d).display, light: getComputedStyle(l).display, h: r.height, loaded: l.complete && l.naturalWidth > 0 };
      });
      suite.log('5.4 theme switch keeps the light login logo at 48 px',
        stL.dark === 'none' && stL.light === 'block' && stL.loaded && Math.abs(stL.h - 48) < 1,
        `dark=${stL.dark} light=${stL.light} h=${stL.h}`);
      await page.evaluate(() => setTheme('dark'));
      await H.screenshot(page, ws, 'branding-login-dark');
      await page.evaluate(() => setTheme('light'));
      await H.screenshot(page, ws, 'branding-login-light');
      await ctx.close();

      const ctxM = await browser.newContext({ colorScheme: 'dark', viewport: { width: 400, height: 800 } });
      const pageM = await ctxM.newPage();
      suite.watchPage(pageM);
      await pageM.goto(BASE, { waitUntil: 'networkidle' });
      await pageM.waitForTimeout(500);
      await pageM.evaluate(() => { document.getElementById('login-modal').style.display = 'flex'; });
      await pageM.waitForTimeout(300);
      const stM = await pageM.evaluate(() => {
        const modal = document.getElementById('login-modal');
        const box = modal.querySelector('.modal-box');
        const d = modal.querySelector('.brand-logo-dark');
        const r = d.getBoundingClientRect();
        const br = box.getBoundingClientRect();
        return { h: r.height, w: r.width, logoCx: (r.left + r.right) / 2, boxCx: (br.left + br.right) / 2 };
      });
      suite.log('5.5 mobile viewport: login logo stays at 48 px and centered',
        Math.abs(stM.h - 48) < 1 && Math.abs(stM.logoCx - stM.boxCx) <= 2,
        `h=${stM.h} w=${stM.w} dCx=${stM.logoCx - stM.boxCx}`);
      await H.screenshot(pageM, ws, 'branding-login-mobile');
      await ctxM.close();
    }

    // ═══ SECTION 6: desktop static sidebar (1920x1080 and 1440x900, both themes) ═══
    let sec6 = 0;
    for (const [vw, vh, tag] of [[1920, 1080, '1920x1080'], [1440, 900, '1440x900']]) {
      for (const scheme of ['dark', 'light']) {
        sec6++;
        const ctx = await browser.newContext({ colorScheme: scheme, viewport: { width: vw, height: vh } });
        const page = await ctx.newPage();
        suite.watchPage(page);
        await page.goto(BASE, { waitUntil: 'networkidle' });
        await page.waitForTimeout(500);
        const st = await page.evaluate(() => {
          const sb = document.querySelector('.sidebar');
          const d = sb.querySelector('.brand-logo-dark');
          const l = sb.querySelector('.brand-logo-light');
          const r = d.getBoundingClientRect();
          const lr = l.getBoundingClientRect();
          return {
            topbar: getComputedStyle(document.querySelector('.mobile-topbar')).display,
            sbTransform: getComputedStyle(sb).transform,
            sbVisible: getComputedStyle(sb).display !== 'none' && sb.getBoundingClientRect().width > 0,
            dark: getComputedStyle(d).display, light: getComputedStyle(l).display,
            h: (getComputedStyle(d).display === 'block' ? r : lr).height,
          };
        });
        const wantDark = scheme === 'dark';
        suite.log(`6.${sec6} ${tag} ${scheme}: static sidebar (no drawer, top bar hidden), sidebar logo at 30 px in ${scheme} theme`,
          st.topbar === 'none' && st.sbTransform === 'none' && st.sbVisible &&
          (wantDark ? st.dark === 'block' : st.light === 'block') && Math.abs(st.h - 30) < 1,
          `topbar=${st.topbar} transform=${st.sbTransform} dark=${st.dark} light=${st.light} h=${st.h}`);
        await H.screenshot(page, ws, `branding-desktop-${tag}-${scheme}`);
        await ctx.close();
      }
    }

    // ═══ SECTION 7: README EN/RU branding ═══
    {
      const reads = ['README.md', 'README_RU.md'].map(name => fs.readFileSync(path.join(H.ROOT, name), 'utf-8'));
      reads.forEach((src, i) => {
        const tag = i === 0 ? 'README.md' : 'README_RU.md';
        suite.log(`7.${i * 4 + 1} ${tag} header has no "# GoAl" text heading (no wordmark duplication)`,
          !/^# GoAl$/m.test(src), '');
        suite.log(`7.${i * 4 + 2} ${tag} uses a centered theme-aware <picture> (light source + dark img, both canonical paths)`,
          /<p align="center">\s*<picture>\s*<source media="\(prefers-color-scheme: light\)" srcset="assets\/brand\/goal-logo-light\.svg">\s*<img src="assets\/brand\/goal-logo-dark\.svg" alt="GoAl"[^>]*>/s.test(src) &&
          !src.includes('![GoAl](assets/brand'), '');
        const ps = [...src.matchAll(/<p align="center">\s*([\s\S]*?)<\/p>/g)].map(m => m[1].trim());
        const text = ps.filter(x => !x.includes('<picture>'))
          .map(x => x.replace(/^<strong>/, '').replace(/<\/strong>$/, '').trim());
        suite.log(`7.${i * 4 + 3} ${tag} slogan and tagline are on separate centered lines`,
          text.length === 2 && text[0].length > 0 && !text[0].includes('\u2014') && text[1].length > 0,
          JSON.stringify(text));
        suite.log(`7.${i * 4 + 4} ${tag} keeps the XERJ badge and corpus entry lines`,
          src.includes('https://hub.xerj.org/contributors/dsdred/badge.svg') &&
          src.includes('https://hub.xerj.org/corpus/dsdred--goal-starter'), '');
      });
    }

    // ═══ SECTION 8: no console errors, no server 5xx ═══
    suite.log('8.1 no browser console errors recorded by the suite', suite.consoleErrors.length === 0,
      suite.consoleErrors.slice(0, 3).join('; '));
    suite.log('8.2 no server 5xx responses recorded by the suite', suite.serverErrors.length === 0,
      JSON.stringify(suite.serverErrors.slice(0, 3)));

    ok = await suite.finish();
  } catch (err) {
    suite.log('FATAL: ' + err.message, false, err.stack ? err.stack.split('\n')[0] : '');
    ok = await suite.finish();
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

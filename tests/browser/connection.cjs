'use strict';
const H = require('./harness.cjs');

const PORT = 19486;
const BASE = `http://127.0.0.1:${PORT}`;

const RU_LOST = 'Сервер недоступен. Повторное подключение...';
const RU_RESTORED = 'Соединение с сервером восстановлено';
const EN_LOST = 'Server unreachable. Reconnecting...';
const EN_RESTORED = 'Server connection restored';

module.exports = { BASE };

async function dotState(page) {
  return page.evaluate(() => {
    const d = document.getElementById('server-dot');
    return { online: d.classList.contains('online'), offline: d.classList.contains('offline') };
  });
}

async function bannerState(page) {
  return page.evaluate(() => {
    const b = document.getElementById('conn-banner');
    return { visible: getComputedStyle(b).display !== 'none', text: (b.textContent || '').trim() };
  });
}

async function waitFor(cond, timeoutMs) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (await cond()) return true;
    await new Promise(r => setTimeout(r, 250));
  }
  return false;
}

async function catchToast(page, text, timeoutMs = 12000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const found = await page.evaluate(t =>
      [...document.querySelectorAll('#toast-container .toast')].some(el => el.textContent.includes(t)), text);
    if (found) return true;
    await new Promise(r => setTimeout(r, 300));
  }
  return false;
}

async function main() {
  const ws = H.makeWorkspace('conn');
  const goalBin = H.buildGoal(ws);

  H.writeConfig(ws, {
    webPort: PORT,
    authEnabled: false,
  });

  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started (auth OFF).\n');

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('Connection feedback (server dot + banner, RU/EN)');
  suite.watchPage(page);

  let ok = false;
  try {
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1200);

    // ═══ SECTION 1: initial state (server up, RU default) ═══
    let dot = await dotState(page);
    let ban = await bannerState(page);
    suite.log('1.1 initial: server dot is online (green)', dot.online && !dot.offline, JSON.stringify(dot));
    suite.log('1.2 initial: connection banner hidden', !ban.visible, JSON.stringify(ban));
    await H.screenshot(page, ws, '01-initial-online');

    // ═══ SECTION 2: server unreachable (RU) ═══
    // Aborting every request simulates the server going away mid-session;
    // the page itself stays loaded, which is exactly the reported case.
    await page.route('**', route => route.abort());
    const offlineInTime = await waitFor(async () => {
      const d = await dotState(page);
      const b = await bannerState(page);
      return d.offline && !d.online && b.visible;
    }, 15000);
    dot = await dotState(page);
    ban = await bannerState(page);
    suite.log('2.1 loss: server dot turns offline (red)', dot.offline && !dot.online, JSON.stringify(dot));
    suite.log('2.2 loss: banner visible with RU text', ban.visible && ban.text === RU_LOST, `text=${JSON.stringify(ban.text)}`);
    suite.log('2.3 loss detected within ~2 probe cycles (≤ 15 s)', offlineInTime);
    await H.screenshot(page, ws, '02-offline-ru');

    // ═══ SECTION 3: recovery (RU) ═══
    await page.unroute('**');
    const recoveredInTime = await waitFor(async () => {
      const d = await dotState(page);
      const b = await bannerState(page);
      return d.online && !b.visible;
    }, 15000);
    dot = await dotState(page);
    ban = await bannerState(page);
    suite.log('3.1 recovery: server dot back to online', dot.online && !dot.offline, JSON.stringify(dot));
    suite.log('3.2 recovery: banner hidden', !ban.visible, JSON.stringify(ban));
    suite.log('3.3 recovery toast shown (RU)', await catchToast(page, RU_RESTORED));
    suite.log('3.4 recovery detected within ~2 probe cycles (≤ 15 s)', recoveredInTime);
    await H.screenshot(page, ws, '03-recovered-ru');

    // ═══ SECTION 4: EN mode — loss and recovery localized ═══
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(500);
    await page.route('**', route => route.abort());
    await waitFor(async () => (await bannerState(page)).visible, 15000);
    ban = await bannerState(page);
    dot = await dotState(page);
    suite.log('4.1 EN loss: banner text is "Server unreachable. Reconnecting..."', ban.visible && ban.text === EN_LOST, `text=${JSON.stringify(ban.text)}`);
    suite.log('4.2 EN loss: server dot offline', dot.offline && !dot.online, JSON.stringify(dot));
    await H.screenshot(page, ws, '04-offline-en');
    await page.unroute('**');
    await waitFor(async () => (await dotState(page)).online, 15000);
    suite.log('4.3 EN recovery: dot online + localized toast', (await dotState(page)).online && await catchToast(page, EN_RESTORED));

    // ═══ SECTION 5: sanity ═══
    // The aborted-request phases deliberately produce resource-load console
    // notices; filter those, keep any real JS error (same rule as i18n.cjs).
    const unexpectedConsole = suite.consoleErrors.filter(e =>
      !/Failed to load resource[^\n]*(net::ERR_FAILED|net::ERR_CONNECTION_REFUSED|Failed to fetch|401)/.test(e));
    suite.log('5.1 No unexpected console errors', unexpectedConsole.length === 0,
      unexpectedConsole.slice(0, 3).join('; ') || `(${suite.consoleErrors.length} expected aborted-resource notices ignored)`);
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

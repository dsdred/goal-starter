'use strict';
const path = require('path');
const H = require('./harness.cjs');

const PORT = 19475;
const BASE = `http://127.0.0.1:${PORT}`;
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'secret123';

module.exports = { BASE };

async function main() {
  const ws = H.makeWorkspace('i18n');
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
  const suite = H.newSuite('I18N (RU/EN nav label + localized server errors)');
  suite.watchPage(page);

  const loginErrorText = async () => {
    const el = page.locator('#login-error');
    return { visible: await el.isVisible(), text: (await el.textContent() || '').trim() };
  };
  const navText = async view =>
    (await page.locator(`.nav-item[data-view="${view}"] span[data-i18n]`).first().textContent() || '').trim();

  let ok = false;
  try {
    // ═══ SECTION 1: RU nav labels (default language) ═══
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);

    const navRt = await navText('adv-runtimes');
    const navIns = await navText('adv-instances');
    suite.log('1.1 RU nav: runtimes item is "Рантаймы"', navRt === 'Рантаймы', `text=${JSON.stringify(navRt)}`);
    suite.log('1.2 RU nav: instances item is "Экземпляры" (control)', navIns === 'Экземпляры', `text=${JSON.stringify(navIns)}`);
    const rawNav = await page.evaluate(() =>
      [...document.querySelectorAll('.nav-item span[data-i18n]')].map(e => e.textContent.trim()));
    suite.log('1.3 RU nav: no item shows raw "Runtime"', !rawNav.includes('Runtime'), `navs=${JSON.stringify(rawNav)}`);
    await H.screenshot(page, ws, '01-rw-nav-ru');

    // ═══ SECTION 2: RU login failure is localized, not the raw server string ═══
    await page.fill('#username', ADMIN_USER);
    await page.fill('#password', 'wrong-password');
    await page.click('#login-form button[type="submit"]');
    await page.waitForTimeout(800);
    let le = await loginErrorText();
    suite.log('2.1 RU login failure: localized message shown', le.visible && le.text === 'Неверный пользователь или пароль.', `text=${JSON.stringify(le)}`);
    suite.log('2.2 RU login failure: raw "invalid credentials" not shown', !/invalid credentials/i.test(le.text), `text=${JSON.stringify(le)}`);
    await H.screenshot(page, ws, '02-login-fail-ru');

    // ═══ SECTION 3: RU login network failure is localized ═══
    await page.route('**/api/v1/auth/login', route => route.abort());
    await page.fill('#password', 'whatever');
    await page.click('#login-form button[type="submit"]');
    await page.waitForTimeout(800);
    le = await loginErrorText();
    await page.unroute('**/api/v1/auth/login');
    suite.log('3.1 RU login network failure: localized message shown', le.visible && le.text === 'Не удалось подключиться к серверу. Проверьте соединение и повторите попытку.', `text=${JSON.stringify(le)}`);
    await H.screenshot(page, ws, '03-login-net-fail-ru');

    // ═══ SECTION 4: EN mode — nav + login failure + successful login ═══
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(500);
    const loginTitle = (await page.locator('#login-modal h2').textContent() || '').trim();
    suite.log('4.1 EN login modal title is "Sign In" (dictionary loaded)', loginTitle === 'Sign In', `title=${JSON.stringify(loginTitle)}`);

    await page.fill('#username', ADMIN_USER);
    await page.fill('#password', 'wrong-password');
    await page.click('#login-form button[type="submit"]');
    await page.waitForTimeout(800);
    le = await loginErrorText();
    suite.log('4.2 EN login failure: localized message shown', le.visible && le.text === 'Invalid username or password.', `text=${JSON.stringify(le)}`);
    await H.screenshot(page, ws, '04-login-fail-en');

    await page.fill('#username', ADMIN_USER);
    await page.fill('#password', ADMIN_PASS);
    await page.click('#login-form button[type="submit"]');
    await page.waitForTimeout(1500);
    const shellVisible = await page.locator('#app-shell').evaluate(el => getComputedStyle(el).display !== 'none');
    suite.log('4.3 EN correct credentials: login succeeds (app shell visible)', shellVisible);
    suite.log('4.4 EN nav: runtimes item is "Runtimes"', (await navText('adv-runtimes')) === 'Runtimes', `text=${JSON.stringify(await navText('adv-runtimes'))}`);
    suite.log('4.5 EN nav: instances item is "Instances"', (await navText('adv-instances')) === 'Instances', `text=${JSON.stringify(await navText('adv-instances'))}`);
    await H.screenshot(page, ws, '05-logged-in-en');

    // ═══ SECTION 5: server-message mapping (RU) ═══
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(500);
    const tr = msg => page.evaluate(m => window.translateServerMessage(m), msg);
    const fe = (msg, code) => page.evaluate(a => {
      const e = new Error(a.msg);
      e.code = a.code || '';
      return window.friendlyError(e);
    }, { msg, code });

    suite.log('5.1 RU map: invalid credentials', (await tr('invalid credentials')) === 'Неверный пользователь или пароль.');
    suite.log('5.2 RU map: rate limited', (await tr('too many login attempts, please try again later')) === 'Слишком много попыток. Повторите позже.');
    suite.log('5.3 RU map: invalid CSRF token', (await tr('invalid CSRF token')) === 'Сессия недействительна или истекла. Войдите снова.');
    suite.log('5.4 RU map: unauthorized', (await tr('unauthorized')) === 'Требуется аутентификация. Войдите в систему.');
    suite.log('5.5 RU map: password too long', (await tr('password must not exceed 72 bytes')) === 'Пароль не должен превышать 72 байта.');
    suite.log('5.6 RU map: no running instance', (await tr('no running instance for this runtime')) === 'Нет запущенных экземпляров для этого рантайма.');
    suite.log('5.7 RU map: in use', (await tr('runtime is in use: m-1')) === 'Объект используется другими записями и не может быть удалён.');
    suite.log('5.8 RU wrapper: unmatched server string stays in a localized frame',
      (await tr('totally unknown failure xyz')) === 'Ошибка сервера: «totally unknown failure xyz»',
      `got=${JSON.stringify(await tr('totally unknown failure xyz'))}`);
    suite.log('5.9 code-first: rate_limited code wins over the message',
      (await fe('whatever message', 'rate_limited')) === 'Слишком много попыток. Повторите позже.');
    suite.log('5.10 code-first: invalid_runtime code maps to localized not-found',
      (await fe('runtime not found: rt-x', 'invalid_runtime')) === 'Runtime не найден. Возможно, он был удалён.');
    await H.screenshot(page, ws, '06-mapping-ru');

    // ═══ SECTION 6: server-message mapping (EN) ═══
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(500);
    suite.log('6.1 EN map: invalid credentials', (await tr('invalid credentials')) === 'Invalid username or password.');
    suite.log('6.2 EN wrapper: unmatched server string stays in a localized frame',
      (await tr('totally unknown failure xyz')) === 'Server error: totally unknown failure xyz',
      `got=${JSON.stringify(await tr('totally unknown failure xyz'))}`);

    // ═══ SECTION 7: sanity ═══
    // The failed-logins above deliberately produce 401/aborted-request network
    // notices; filter those, keep any real JS/resource error (same rule as core.cjs).
    const unexpectedConsole = suite.consoleErrors.filter(e =>
      !/Failed to load resource[^\n]*401 \(Unauthorized\)/.test(e) &&
      !/Failed to load resource[^\n]*net::ERR_FAILED/.test(e));
    suite.log('7.1 No unexpected console errors', unexpectedConsole.length === 0,
      unexpectedConsole.slice(0, 3).join('; ') || `(${suite.consoleErrors.length} expected 401/aborted-login notices ignored)`);
    suite.log('7.2 No server 5xx errors', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));

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

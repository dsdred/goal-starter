'use strict';
// ADR 019 slice 5 — maintained HTTPS / secure-origin acceptance (result A).
//
// The origin is the whole subject of this suite, so it never uses localhost or a
// 127.x address as the tested browser origin: origin trustworthiness is decided
// by the host string alone (ADR 019 §Context), and loopback reports
// isSecureContext=true over plain HTTP — a silent false PASS (section 3 proves it).
const path = require('path');
const fs = require('fs');
const { execFileSync } = require('child_process');
const H = require('./harness.cjs');

const HOST = 'goaltls.invalid';               // RFC 6761 reserved: never resolves, not HSTS-preloaded
const ALIAS_HOST = 'goaltls2.invalid';        // mapped to loopback but NOT in the certificate SAN
const UNMAPPED_HOST = 'goalunmapped.invalid'; // deliberately absent from the resolver rules
const HTTP_PORT = 19489;
const HTTPS_PORT = 19490;
const HTTP_BASE = `http://${HOST}:${HTTP_PORT}`;
const HTTPS_BASE = `https://${HOST}:${HTTPS_PORT}`;
// --host-resolver-rules lives in Chromium's network stack only, so Node (the
// health probe) can never resolve HOST and uses the loopback address behind it.
const HTTP_PROBE = `http://127.0.0.1:${HTTP_PORT}`;
const HTTPS_PROBE = `https://127.0.0.1:${HTTPS_PORT}`;
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'secret123';
const RESOLVER_RULES = `MAP ${HOST} 127.0.0.1, MAP ${ALIAS_HOST} 127.0.0.1`;
const BROWSER_ARGS = ['--no-proxy-server', `--host-resolver-rules=${RESOLVER_RULES}`];
const EXPORT_FILENAME = 'goal-portable-config.json';

function genCert(ws) {
  const bin = H.buildTLSFixture(ws);
  const cert = path.join(ws.dir, 'cert.pem');
  const key = path.join(ws.dir, 'key.pem');
  try {
    execFileSync(bin, ['-cert', cert, '-key', key, '-dns', HOST, '-hours', '24'], { stdio: 'pipe' });
  } catch (e) {
    console.error(`tls-fixture failed: ${e.message}\n${e.stderr ? e.stderr.toString() : ''}`);
    removeTLSMaterial({ cert, key });
    process.exit(2);
  }
  return { cert, key };
}

// The browser's own rule, restated for the guard in section 3: no DNS lookup is
// consulted, only the host string.
function potentiallyTrustworthyHostString(host) {
  if (host === 'localhost' || host.endsWith('.localhost')) return true;
  if (/^127(\.\d{1,3}){3}$/.test(host)) return true;
  if (host === '::1' || host === '[::1]') return true;
  return false;
}

async function apiOn(page, method, urlPath, body, headers) {
  return page.evaluate(async (a) => {
    const opts = { method: a.method, credentials: 'same-origin', headers: Object.assign({}, a.headers) };
    if (a.body) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(a.body);
    }
    const res = await fetch(a.path, opts);
    const text = await res.text();
    let data;
    try { data = JSON.parse(text); } catch { data = text; }
    return { status: res.status, data };
  }, { method, path: urlPath, body, headers: headers || {} });
}

// What GoAl actually emitted on this connection. response.headers() omits
// Set-Cookie, so the attributes come from headersArray() of the matching reply.
async function authRequest(page, urlPath, body, headers) {
  const [resp] = await Promise.all([
    page.waitForResponse(r => r.url().endsWith(urlPath), { timeout: 15000 }),
    apiOn(page, 'POST', urlPath, body, headers),
  ]);
  const arr = await resp.headersArray();
  return {
    status: resp.status(),
    setCookie: arr.filter(h => h.name.toLowerCase() === 'set-cookie').map(h => h.value),
  };
}

const cookieLine = (lines, name) => lines.find(l => l.startsWith(name + '=')) || '';

// A session token is a live credential of the running instance, and this suite
// asserts only the attribute list — never the value — so values never reach the log.
const redactCookie = line => line.replace(/^([^=]+)=[^;]*/, '$1=<redacted>');

async function capability(page) {
  return page.evaluate(() => ({
    secure: window.isSecureContext,
    typeofPicker: typeof window.showSaveFilePicker,
    own: Object.prototype.hasOwnProperty.call(window, 'showSaveFilePicker'),
    onProto: 'showSaveFilePicker' in Object.getPrototypeOf(window),
    // A page-level stub is indistinguishable by descriptor flags (measured); the
    // function source is the only reliable discriminator.
    native: typeof window.showSaveFilePicker === 'function'
      && Function.prototype.toString.call(window.showSaveFilePicker).includes('[native code]'),
    instrumented: typeof window.__pickerCalls !== 'undefined',
    href: location.href,
  }));
}

async function toastClasses(page) {
  return page.evaluate(() => Array.from(document.querySelectorAll('#toast-container > *')).map(n => n.className));
}

async function seedData(page) {
  await page.evaluate(async () => {
    const csrf = (document.cookie.match(/goal_csrf_token=([^;]+)/) || [])[1] || '';
    const r = await fetch('/api/v1/runtimes', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ name: 'TLS Fixture RT', executable: 'fixture.exe', working_directory: '/opt/fixture' }),
    });
    if (!r.ok) throw new Error('seed runtime → ' + r.status);
  });
  await page.evaluate(() => window.reloadAllData());
  await page.waitForTimeout(400);
}

async function openPortableSection(page) {
  await page.evaluate(() => window.navigate('adv-settings'));
  await page.waitForSelector('#portable-export-btn', { timeout: 10000 });
}

function removeTLSMaterial(material) {
  for (const p of [material.cert, material.key]) {
    try {
      fs.rmSync(p, { force: true });
    } catch {}
  }
}

async function main() {
  const ws = H.makeWorkspace('https');
  const goalBin = H.buildGoal(ws);
  const material = genCert(ws);
  const { cert, key } = material;

  H.writeConfig(ws, {
    webPort: HTTP_PORT,
    authEnabled: true,
    adminUser: ADMIN_USER,
    adminPassword: ADMIN_PASS,
    tls: { enabled: true, port: HTTPS_PORT, certFile: cert, keyFile: key },
  });

  const server = H.startServer(goalBin, ws);
  const httpUp = await H.waitForHealth(HTTP_PROBE);
  const httpsUp = httpUp && await H.waitForHealth(HTTPS_PROBE, 30000, { allowUntrustedTLS: true });
  if (!httpsUp) {
    console.error(`Server failed to bring up both listeners. Output: ${server.output}`);
    await server.stop();
    removeTLSMaterial(material);
    process.exit(1);
  }
  console.log('Server started: HTTP + native HTTPS (auth ON).\n');

  const suite = H.newSuite('Native HTTPS / secure origin');
  const browser = await H.launchBrowser({ args: BROWSER_ARGS });
  let page = null;

  try {
    const log = server.output;

    // ═══ SECTION 0: two listeners, §D19 startup diagnostics, log privacy ═══
    suite.log('0.1 HTTP listener reported healthy on webPort', log.includes(`starting HTTP server addr=127.0.0.1:${HTTP_PORT}`));
    suite.log('0.2 HTTPS listener reported healthy on tls.port', log.includes(`starting HTTPS server addr=127.0.0.1:${HTTPS_PORT}`));
    suite.log('0.3 §D19 HTTPS line names tls+cert+expiry+SAN',
      /starting HTTPS server addr=.*tls=enabled cert=.* expires=.* san=DNS:goaltls\.invalid/.test(log));
    suite.log('0.4 §D19 HTTP line carries no TLS attributes',
      new RegExp(`starting HTTP server addr=127\\.0\\.0\\.1:${HTTP_PORT}(\\r?\\n|$)`).test(log));
    const keyBody = fs.readFileSync(key, 'utf8').trim().split('\n').slice(1, -1).join('');
    suite.log('0.5 No key material and no key path in the startup log',
      !log.includes('BEGIN PRIVATE KEY') && !log.includes(keyBody) && !log.includes(key));
    const plaintext = await H.api(`http://127.0.0.1:${HTTPS_PORT}`, 'GET', '/api/v1/health');
    suite.log('0.6 Plaintext to tls.port is refused, not served', plaintext.status === 400, `status=${plaintext.status}`);
    suite.log('0.7 No listener setup failure was logged',
      !log.includes('listener setup failed') && !log.includes('bind HTTP listener') && !log.includes('bind HTTPS listener'));

    const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 }, ignoreHTTPSErrors: true });
    page = await ctx.newPage();
    suite.watchPage(page);

    // ═══ SECTION 1: HTTPS on a non-trustworthy host string is a secure context (P1) ═══
    await page.goto(HTTPS_BASE, { waitUntil: 'networkidle' });
    const httpsCap = await capability(page);
    suite.log('1.1 Tested origin is the mapped host name, not loopback', httpsCap.href === HTTPS_BASE + '/', httpsCap.href);
    suite.log('1.2 HTTPS origin reports isSecureContext true', httpsCap.secure === true);
    suite.log('1.3 SPA really rendered over TLS',
      (await page.title()).trim() === 'GoAl' && await page.locator('#login-form').isVisible());

    // ═══ SECTION 2: the same host over plain HTTP is not a secure context (P2) ═══
    await page.goto(HTTP_BASE, { waitUntil: 'networkidle' });
    const httpCap = await capability(page);
    suite.log('2.1 HTTP origin of the same host reports isSecureContext false', httpCap.secure === false);
    suite.log('2.2 showSaveFilePicker is not installed at all over plain HTTP',
      httpCap.typeofPicker === 'undefined' && httpCap.own === false && httpCap.onProto === false,
      `typeof=${httpCap.typeofPicker}`);
    suite.log('2.3 SPA rendered over plain HTTP too', (await page.title()).trim() === 'GoAl');

    // ═══ SECTION 3: the loopback trap this suite must never fall into (P2 guard) ═══
    await page.goto(`http://127.0.0.1:${HTTP_PORT}`, { waitUntil: 'networkidle' });
    const loopCap = await capability(page);
    suite.log('3.1 Control: loopback over plain HTTP would falsely pass',
      loopCap.secure === true && loopCap.typeofPicker === 'function',
      `127.0.0.1 secure=${loopCap.secure} picker=${loopCap.typeofPicker}`);
    suite.log('3.2 Neither tested origin is a potentially-trustworthy host string',
      potentiallyTrustworthyHostString(new URL(HTTP_BASE).hostname) === false
      && potentiallyTrustworthyHostString(new URL(HTTPS_BASE).hostname) === false, HOST);

    // ═══ SECTION 4: real native picker availability on the HTTPS origin (P3) ═══
    await page.goto(HTTPS_BASE, { waitUntil: 'networkidle' });
    const nativeCap = await capability(page);
    suite.log('4.1 HTTPS origin installs showSaveFilePicker', nativeCap.typeofPicker === 'function');
    suite.log('4.2 It is the browser-native implementation, not a substituted stub', nativeCap.native === true);
    suite.log('4.3 This suite installs no picker stub anywhere', nativeCap.instrumented === false);

    // ═══ SECTION 5: HTTP download fallback on the insecure origin (P4) ═══
    await H.login(page, HTTP_BASE, ADMIN_USER, ADMIN_PASS);
    await seedData(page);
    await openPortableSection(page);
    const fallbackCap = await capability(page);
    suite.log('5.1 Fallback branch is available because the picker is absent', fallbackCap.typeofPicker === 'undefined');
    let downloadCount = 0;
    const exportRequests = [];
    page.on('download', () => { downloadCount++; });
    page.on('request', req => { if (req.url().includes('/api/v1/export')) exportRequests.push(req.url()); });
    const [dl] = await Promise.all([
      page.waitForEvent('download', { timeout: 15000 }),
      page.click('#portable-export-btn'),
    ]);
    await page.waitForTimeout(700);
    suite.log('5.2 Insecure origin produced an ordinary browser download', downloadCount === 1, `count=${downloadCount}`);
    suite.log('5.3 Downloaded file keeps the portable export name', dl.suggestedFilename() === EXPORT_FILENAME, dl.suggestedFilename());
    suite.log('5.4 Export reached the server over the insecure origin', exportRequests.length === 1, `requests=${exportRequests.length}`);
    suite.log('5.5 Button restored after the fallback download', !(await page.locator('#portable-export-btn').isDisabled()));
    suite.log('5.6 Success toast shown on the fallback path',
      (await toastClasses(page)).some(c => c.includes('success')));
    await ctx.close();

    // ═══ SECTION 6: native path on HTTPS — presence is automated, completion is manual (P3, §D27) ═══
    // Fresh context: the non-Secure cookie from section 5 would already
    // authenticate the HTTPS origin (C1), which would hide the login form.
    const nativeCtx = await browser.newContext({ viewport: { width: 1920, height: 1080 }, ignoreHTTPSErrors: true });
    const nativePage = await nativeCtx.newPage();
    suite.watchPage(nativePage);
    let nativeDownloads = 0;
    const nativeExports = [];
    nativePage.on('download', () => { nativeDownloads++; });
    nativePage.on('request', req => { if (req.url().includes('/api/v1/export')) nativeExports.push(req.url()); });
    await H.login(nativePage, HTTPS_BASE, ADMIN_USER, ADMIN_PASS);
    await openPortableSection(nativePage);
    suite.log('6.1 Native picker is installed on this HTTPS origin', (await capability(nativePage)).native === true);
    // Positive proof that the click reached portableExport() at all: app.js sets
    // btn.disabled = true (app.js:2522) before it opens the picker (app.js:2530-2535),
    // so a disabled-attribute transition is the only observation that separates
    // "the native dialog was cancelled" from "nothing happened" — downloads,
    // request count, button state and toasts are identical in both cases.
    const observeToggle = () => nativePage.evaluate(() => {
      window.__btnToggles = 0;
      const b = document.getElementById('portable-export-btn');
      new MutationObserver(() => { window.__btnToggles++; })
        .observe(b, { attributes: true, attributeFilter: ['disabled'] });
    });
    const toggles = () => nativePage.evaluate(() => window.__btnToggles);
    await observeToggle();
    await nativePage.click('#portable-section h3');
    await nativePage.waitForTimeout(1500);
    suite.log('6.2 Negative control: an inert click satisfies nothing this section asserts',
      (await toggles()) === 0 && nativeDownloads === 0 && nativeExports.length === 0
      && !(await nativePage.locator('#portable-export-btn').isDisabled())
      && (await toastClasses(nativePage)).length === 0,
      `control toggles=${await toggles()} downloads=${nativeDownloads} requests=${nativeExports.length}`);
    await observeToggle();
    await nativePage.click('#portable-export-btn');
    await nativePage.waitForTimeout(3000);
    suite.log('6.3 The export handler really ran before the native call', (await toggles()) >= 1, `toggles=${await toggles()}`);
    suite.log('6.4 Headless native dialog cancels silently: no browser download', nativeDownloads === 0, `count=${nativeDownloads}`);
    suite.log('6.5 Picker-first contract holds: no export fetch before a chosen destination', nativeExports.length === 0);
    suite.log('6.6 Button restored after the silent cancel', !(await nativePage.locator('#portable-export-btn').isDisabled()));
    suite.log('6.7 No toast at all on the cancelled native path', (await toastClasses(nativePage)).length === 0);
    await nativeCtx.close();

    // ═══ SECTIONS 7-8: cookie attributes by connection + browser ratchet (P5, P6) ═══
    // One context, one jar, one host string: HTTP and HTTPS share the cookie
    // store because scheme and port are not part of a cookie key (§D18).
    const jarCtx = await browser.newContext({ ignoreHTTPSErrors: true });
    const jarPage = await jarCtx.newPage();
    suite.watchPage(jarPage);
    const jarFor = async url => (await jarCtx.cookies(url)).filter(c => c.domain === HOST);
    const named = (list, name) => list.find(c => c.name === name);
    const creds = { username: ADMIN_USER, password: ADMIN_PASS };

    await jarPage.goto(HTTP_BASE, { waitUntil: 'networkidle' });
    const httpLogin = await authRequest(jarPage, '/api/v1/auth/login', creds);
    const httpJar = await jarFor(HTTP_BASE);
    const httpSession = named(httpJar, 'goal_session');
    const httpCsrf = named(httpJar, 'goal_csrf_token');
    suite.log('7.1 Login over plain HTTP succeeds', httpLogin.status === 200, `status=${httpLogin.status}`);
    suite.log('7.2 Emitted goal_session carries no Secure attribute on the HTTP connection',
      cookieLine(httpLogin.setCookie, 'goal_session') !== ''
      && !/\bSecure\b/.test(cookieLine(httpLogin.setCookie, 'goal_session')),
      redactCookie(cookieLine(httpLogin.setCookie, 'goal_session')));
    suite.log('7.3 The jar agrees: goal_session is stored non-Secure over HTTP',
      !!httpSession && httpSession.secure === false);
    suite.log('7.4 Session keeps HttpOnly, SameSite=Lax, Path=/ and host-only scope',
      !!httpSession && httpSession.httpOnly === true && httpSession.sameSite === 'Lax'
      && httpSession.path === '/' && httpSession.domain === HOST);
    suite.log('7.5 Emitted goal_csrf_token carries no Secure attribute over HTTP',
      cookieLine(httpLogin.setCookie, 'goal_csrf_token') !== ''
      && !/\bSecure\b/.test(cookieLine(httpLogin.setCookie, 'goal_csrf_token')));
    suite.log('7.6 CSRF cookie stays JS-readable and SameSite=Strict in the jar',
      !!httpCsrf && httpCsrf.httpOnly === false && httpCsrf.sameSite === 'Strict' && httpCsrf.secure === false);

    // Boundary of this check (F-5): a page fetch cannot prove the forwarded
    // header reached GoAl — if Chromium withheld it the observable is identical.
    // The authoritative forwarded-header pins are the Go-level ones, where the
    // request is built directly: internal/webui/security/cookie_secure_test.go
    // and internal/webui/handlers/auth_cookie_secure_test.go. 7.9 below is the
    // positive counterpart on this same handler over a real TLS connection.
    const spoof = await authRequest(jarPage, '/api/v1/auth/login', creds,
      { 'X-Forwarded-Proto': 'https', 'X-Real-IP': '203.0.113.9' });
    const spoofedSession = named(await jarFor(HTTP_BASE), 'goal_session');
    suite.log('7.7 Browser-level: a spoofed X-Forwarded-Proto did not flip Secure on the emit (header delivery not proven here)',
      spoof.status === 200 && !/\bSecure\b/.test(cookieLine(spoof.setCookie, 'goal_session')));
    suite.log('7.8 …and the jar still holds a non-Secure session cookie',
      !!spoofedSession && spoofedSession.secure === false);

    await jarPage.goto(HTTPS_BASE, { waitUntil: 'networkidle' });
    const httpsLogin = await authRequest(jarPage, '/api/v1/auth/login', creds);
    const httpsJar = await jarFor(HTTPS_BASE);
    const secureSession = named(httpsJar, 'goal_session');
    const secureCsrf = named(httpsJar, 'goal_csrf_token');
    suite.log('7.9 The same handler over the TLS connection emits Secure for goal_session',
      httpsLogin.status === 200 && /\bSecure\b/.test(cookieLine(httpsLogin.setCookie, 'goal_session')));
    suite.log('7.10 …and Secure for goal_csrf_token',
      /\bSecure\b/.test(cookieLine(httpsLogin.setCookie, 'goal_csrf_token')));
    suite.log('7.11 The jar reflects it: both cookies are Secure over HTTPS',
      !!secureSession && secureSession.secure === true && !!secureCsrf && secureCsrf.secure === true);
    suite.log('7.12 Only Secure changed: HttpOnly/SameSite/Path survive on the TLS connection',
      !!secureSession && secureSession.httpOnly === true && secureSession.sameSite === 'Lax' && secureSession.path === '/');
    suite.log('7.13 The connection, not the host name, set the flag (one jar, one host)',
      !!httpSession && !!secureSession && httpSession.value !== secureSession.value);

    // P6: §D18 coexistence outcome on one host name — the browser's decision.
    await jarPage.goto(HTTP_BASE, { waitUntil: 'networkidle' });
    const afterRatchet = await apiOn(jarPage, 'GET', '/api/v1/instances');
    suite.log('8.1 After an HTTPS login the HTTP page is NOT authenticated (C3)',
      afterRatchet.status === 401, `status=${afterRatchet.status}`);
    suite.log('8.2 The CSRF cookie is gone from document.cookie on the HTTP page',
      !(await jarPage.evaluate(() => document.cookie)).includes('goal_csrf_token'));

    const c5a = await authRequest(jarPage, '/api/v1/auth/login', creds);
    const c5aSession = named(await jarFor(HTTPS_BASE), 'goal_session');
    suite.log('8.3 GoAl still emits a non-Secure session cookie on that HTTP connection',
      c5a.status === 200 && !/\bSecure\b/.test(cookieLine(c5a.setCookie, 'goal_session')));
    suite.log('8.4 The browser refuses the downgrade: jar keeps the Secure session and its value (C5a)',
      !!c5aSession && c5aSession.secure === true && !!secureSession && c5aSession.value === secureSession.value);
    await jarPage.goto(HTTPS_BASE, { waitUntil: 'networkidle' });
    const httpsStillOk = await apiOn(jarPage, 'GET', '/api/v1/instances');
    suite.log('8.5 HTTPS session survives the downgrade attempt', httpsStillOk.status === 200, `status=${httpsStillOk.status}`);
    await jarCtx.close();

    // ═══ SECTION 9: no false PASS on TLS or hostname failure (P7) ═══
    const strictCtx = await browser.newContext({ ignoreHTTPSErrors: false });
    const strictPage = await strictCtx.newPage();
    let certErr = null;
    try {
      await strictPage.goto(HTTPS_BASE, { waitUntil: 'domcontentloaded', timeout: 15000 });
    } catch (e) {
      certErr = String(e.message).split('\n')[0];
    }
    suite.log('9.1 Untrusted certificate without bypass is a hard error, never a silent pass',
      !!certErr && certErr.includes('ERR_CERT_AUTHORITY_INVALID') && strictPage.url() === 'about:blank',
      certErr || 'page loaded');
    await strictCtx.close();

    const unmappedCtx = await browser.newContext({ ignoreHTTPSErrors: true });
    const unmappedPage = await unmappedCtx.newPage();
    let nameErr = null;
    try {
      await unmappedPage.goto(`https://${UNMAPPED_HOST}:${HTTPS_PORT}`, { waitUntil: 'domcontentloaded', timeout: 15000 });
    } catch (e) {
      nameErr = String(e.message).split('\n')[0];
    }
    suite.log('9.2 An unmapped host name fails resolution, so a PASS proves the mapping is live',
      !!nameErr && nameErr.includes('ERR_NAME_NOT_RESOLVED') && unmappedPage.url() === 'about:blank',
      nameErr || 'page loaded');
    await unmappedCtx.close();

    // Measured boundary of browser evidence: ignoreHTTPSErrors masks a name/SAN
    // mismatch too, so certificate identity is pinned by the §D19 startup line
    // (0.3) and by the slice-1 validation tests, never by the browser.
    const aliasCtx = await browser.newContext({ ignoreHTTPSErrors: true });
    const aliasPage = await aliasCtx.newPage();
    let aliasLoaded = true;
    try {
      await aliasPage.goto(`https://${ALIAS_HOST}:${HTTPS_PORT}`, { waitUntil: 'domcontentloaded', timeout: 15000 });
    } catch (e) {
      aliasLoaded = false;
    }
    const aliasCap = aliasLoaded ? await capability(aliasPage) : { secure: false };
    suite.log('9.3 Boundary: a non-SAN name still loads under bypass → cert identity comes from the §D19 log',
      aliasLoaded === true && aliasCap.secure === true);
    await aliasCtx.close();

    // ═══ SECTION 10: reproducibility controls (R3) ═══
    suite.log('10.1 Suite launches with an explicit no-proxy flag', BROWSER_ARGS.includes('--no-proxy-server'));
    suite.log('10.2 Suite relies on resolver rules, not on a hosts-file change',
      BROWSER_ARGS.some(a => a.startsWith('--host-resolver-rules=MAP ')));
    suite.log('10.3 Fixture hostname is a reserved TLD with no public DNS answer', HOST.endsWith('.invalid'));
    const proxyBrowser = await H.launchBrowser({
      args: ['--proxy-server=http://127.0.0.1:9', `--host-resolver-rules=${RESOLVER_RULES}`],
    });
    try {
      const proxyCtx = await proxyBrowser.newContext({ ignoreHTTPSErrors: true });
      const proxyPage = await proxyCtx.newPage();
      let proxyErr = null;
      try {
        await proxyPage.goto(HTTPS_BASE, { waitUntil: 'domcontentloaded', timeout: 15000 });
      } catch (e) {
        proxyErr = String(e.message).split('\n')[0];
      }
      suite.log('10.4 Control: an ambient proxy breaks the mapped origin, which is why 10.1 is mandatory',
        !!proxyErr && proxyErr.includes('ERR_PROXY_CONNECTION_FAILED'), proxyErr || 'page loaded');
    } finally {
      await proxyBrowser.close();
    }

    // Only the deliberate, non-actionable noise is ignored: browser-level
    // resource-load notices for the statuses this suite intentionally produces
    // (8.1's 401, 0.6's 400) and the certificate/proxy errors of sections 9/10.
    // A pageerror or any other console error text is never suppressed just
    // because it happens to contain one of those numbers.
    const ignorableNoise = [
      /^Failed to load resource: the server responded with a status of 400\b/,
      /^Failed to load resource: the server responded with a status of 401\b/,
      /^Failed to load resource: the server responded with a status of 403\b/,
      /^Failed to load resource: the server responded with a status of 409\b/,
      /^Failed to load resource: net::ERR_CERT_AUTHORITY_INVALID\b/,
      /^Failed to load resource: net::ERR_PROXY_CONNECTION_FAILED\b/,
    ];
    const unexpectedConsoleErrors = suite.consoleErrors.filter(e => !ignorableNoise.some(re => re.test(e)));
    suite.log('10.5 No unexpected console errors', unexpectedConsoleErrors.length === 0,
      unexpectedConsoleErrors.slice(0, 3).join('; ') || 'clean');
    suite.log('10.6 No 5xx responses', suite.serverErrors.length === 0,
      suite.serverErrors.length ? JSON.stringify(suite.serverErrors[0]) : 'clean');
    page = null;
  } catch (e) {
    suite.log('FATAL', false, e.message);
    if (page) {
      try { await H.screenshot(page, ws, 'https-fatal'); } catch {}
    }
  } finally {
    await browser.close();
    await server.stop();
    removeTLSMaterial(material);
  }

  const ok = await suite.finish();
  process.exit(ok ? 0 : 1);
}

main().catch(e => { console.error(e); process.exit(1); });

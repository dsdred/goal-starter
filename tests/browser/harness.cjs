'use strict';
const path = require('path');
const fs = require('fs');
const os = require('os');
const { execFileSync, spawn } = require('child_process');

const IS_WIN = process.platform === 'win32';
const EXE = IS_WIN ? '.exe' : '';
const ROOT = path.resolve(__dirname, '..', '..');

function makeWorkspace(name) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), `goal-accept-${name}-`));
  const dataDir = path.join(dir, 'data');
  const screenshots = path.join(dir, 'screenshots');
  fs.mkdirSync(dataDir, { recursive: true });
  fs.mkdirSync(screenshots, { recursive: true });
  return { dir, dataDir, screenshots, config: path.join(dir, 'goal.json') };
}

function buildBin(ws, outName, goArgs) {
  const out = path.join(ws.dir, outName + EXE);
  try {
    execFileSync('go', ['build', '-o', out, ...goArgs], { cwd: ROOT, stdio: 'pipe' });
  } catch (e) {
    console.error(`build ${outName} failed: ${e.message}\n${e.stdout ? e.stdout.toString() : ''}${e.stderr ? e.stderr.toString() : ''}`);
    process.exit(2);
  }
  return out;
}

function buildGoal(ws) {
  return buildBin(ws, 'goal', ['./cmd/goal']);
}

function buildFakeRuntime(ws) {
  return buildBin(ws, 'fake-runtime', ['./testdata/fake-runtime']);
}

function writeConfig(ws, overrides) {
  const cfg = Object.assign({
    version: 2,
    listenAddress: '127.0.0.1',
    dataDir: ws.dataDir,
    authEnabled: false,
  }, overrides);
  fs.writeFileSync(ws.config, JSON.stringify(cfg, null, 2));
  return cfg;
}

async function waitForHealth(base, timeoutMs = 30000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const res = await fetch(base + '/api/v1/health');
      if (res.ok) return true;
    } catch {}
    await new Promise(r => setTimeout(r, 500));
  }
  return false;
}

function startServer(bin, ws) {
  const proc = spawn(bin, ['--config', ws.config], { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
  let output = '';
  proc.stdout.on('data', d => { output += d.toString(); });
  proc.stderr.on('data', d => { output += d.toString(); });
  let stopped = false;
  const stop = async () => {
    if (stopped) return;
    stopped = true;
    if (proc.exitCode === null) {
      if (!IS_WIN) {
        // GoAl handles SIGTERM: graceful shutdown stops managed instances.
        try { process.kill(proc.pid, 'SIGTERM'); } catch {}
        await new Promise(resolve => {
          const t = setTimeout(resolve, 5000);
          proc.once('exit', () => { clearTimeout(t); resolve(); });
        });
      }
      if (proc.exitCode === null) {
        killTree(proc.pid);
        await new Promise(resolve => {
          const t = setTimeout(resolve, 5000);
          proc.once('exit', () => { clearTimeout(t); resolve(); });
        });
      }
    }
  };
  return {
    pid: proc.pid,
    get output() { return output; },
    stop,
  };
}

function killTree(pid) {
  if (pid == null) return;
  try {
    if (IS_WIN) {
      execFileSync('taskkill', ['/F', '/T', '/PID', String(pid)], { stdio: 'pipe' });
    } else {
      process.kill(pid, 'SIGKILL');
    }
  } catch {}
}

function isProcessAlive(pid) {
  try { process.kill(pid, 0); return true; } catch { return false; }
}

async function launchBrowser() {
  const { chromium } = require('playwright');
  return chromium.launch({ headless: true, args: ['--no-sandbox', '--disable-gpu'] });
}

async function login(page, base, user, pass) {
  await page.goto(base, { waitUntil: 'networkidle' });
  await page.waitForTimeout(300);
  await page.fill('#username', user);
  await page.fill('#password', pass);
  await page.click('#login-form button[type="submit"]');
  await page.waitForTimeout(1500);
}

async function api(base, method, urlPath, body) {
  const opts = { method, headers: { 'Content-Type': 'application/json' } };
  if (body) opts.body = JSON.stringify(body);
  const res = await fetch(base + urlPath, opts);
  const text = await res.text();
  let data;
  try { data = JSON.parse(text); } catch { data = text; }
  return { status: res.status, data };
}

async function pageApi(page, method, urlPath, body) {
  return page.evaluate(async (args) => {
    const opts = { method: args.method, headers: { 'Content-Type': 'application/json' } };
    if (args.body) opts.body = JSON.stringify(args.body);
    const res = await fetch(args.path, opts);
    const text = await res.text();
    let data;
    try { data = JSON.parse(text); } catch { data = text; }
    return { status: res.status, data };
  }, { method, path: urlPath, body });
}

async function screenshot(page, ws, name) {
  const file = path.join(ws.screenshots, `${name}.png`);
  await page.screenshot({ path: file, fullPage: true });
  console.log(`  [screenshot] ${name}.png`);
}

function newSuite(title) {
  const s = {
    title,
    pass: 0,
    fail: 0,
    failed: [],
    consoleErrors: [],
    serverErrors: [],
    log(name, ok, detail) {
      const status = ok ? 'PASS' : 'FAIL';
      console.log(`  ${status} | ${name}${detail ? ' — ' + detail : ''}`);
      if (ok) s.pass++; else { s.fail++; s.failed.push(name + (detail ? ' — ' + detail : '')); }
    },
    watchPage(page) {
      page.on('console', msg => {
        if (msg.type() === 'error') s.consoleErrors.push(msg.text());
      });
      page.on('response', resp => {
        if (resp.status() >= 500) s.serverErrors.push({ url: resp.url(), status: resp.status() });
      });
      page.on('pageerror', e => s.consoleErrors.push('pageerror: ' + e.message));
    },
    async finish() {
      console.log(`\n  ${s.title}: TOTAL ${s.pass + s.fail} | PASS ${s.pass} | FAIL ${s.fail}`);
      if (s.failed.length > 0) {
        console.log('  FAILED:');
        s.failed.forEach(f => console.log(`    - ${f}`));
      }
      return s.fail === 0;
    },
  };
  return s;
}

async function runSuite(title, fn) {
  console.log(`\n═══ ${title.toUpperCase()} ═══\n`);
  const suite = newSuite(title);
  const ctx = await fn(suite);
  const ok = await suite.finish();
  if (ctx && typeof ctx.cleanup === 'function') {
    try { await ctx.cleanup(); } catch {}
  }
  return ok;
}

module.exports = {
  IS_WIN,
  EXE,
  ROOT,
  makeWorkspace,
  buildGoal,
  buildFakeRuntime,
  writeConfig,
  waitForHealth,
  startServer,
  killTree,
  isProcessAlive,
  launchBrowser,
  login,
  api,
  pageApi,
  screenshot,
  newSuite,
  runSuite,
};

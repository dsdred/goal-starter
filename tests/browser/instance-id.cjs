'use strict';
const path = require('path');
const H = require('./harness.cjs');

const PORT = 19487;
const BASE = `http://127.0.0.1:${PORT}`;

module.exports = { BASE };

const sleep = ms => new Promise(r => setTimeout(r, ms));

// Multiple simultaneous instances of the SAME model must be visually
// distinguishable: the compact rendering keeps the differentiating SUFFIX of
// the full instance id, the full id stays reachable via title attributes, and
// every routing identity (option values, stop/restart/kill targets) remains
// the FULL instance id.
async function main() {
  const ws = H.makeWorkspace('instance-id');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  H.writeConfig(ws, {
    webPort: PORT,
    authEnabled: false,
    runtimes: [{
      id: 'rt-fake',
      name: 'Fake Runtime',
      executable: fakeRt,
      workingDirectory: fakeRtDir,
    }],
  });

  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started (auth OFF).\n');

  const api = (method, urlPath, body) => H.api(BASE, method, urlPath, body);

  // One model, three simultaneous instances (fake-runtime "infinite" mode).
  const m = await api('POST', '/api/v1/models', { name: 'Id Model', runtime_id: 'rt-fake', args: ['infinite'] });
  const modelId = m.data && m.data.id;
  const ids = [];
  for (let i = 0; i < 3; i++) {
    const s = await api('POST', `/api/v1/models/${modelId}/start`);
    if (s.status === 200 && s.data && s.data.id) ids.push(s.data.id);
    await sleep(300);
  }

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('INSTANCE-ID (compact suffix display + full-id routing identity)');
  suite.watchPage(page);

  const distinct = (arr) => new Set(arr).size === arr.length && arr.every(v => v.length > 0);
  const compactOf = (id) => '…' + id.slice(-16);

  let ok = false;
  try {
    suite.log('Seed: model created', m.status === 201, `status=${m.status}`);
    suite.log('Seed: 3 simultaneous instances running with distinct full ids',
      ids.length === 3 && distinct(ids), `ids=${JSON.stringify(ids)}`);

    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);

    // ═══ SECTION 1: Instances table (desktop) ═══
    await page.evaluate(() => window.navigate('adv-instances'));
    await page.waitForTimeout(500);
    const rows = await page.evaluate((wantIds) => {
      return Array.from(document.querySelectorAll('#adv-instances-body tr')).map(tr => {
        const tds = tr.querySelectorAll('td');
        return { id: tds[0] ? tds[0].getAttribute('title') : null, text: tds[0] ? tds[0].textContent.trim() : null };
      }).filter(r => wantIds.includes(r.id));
    }, ids);
    suite.log('1.1 All 3 instances rendered as rows', rows.length === 3, `rows=${rows.length}`);
    suite.log('1.2 Compact ID cells are pairwise distinct', distinct(rows.map(r => r.text)),
      `texts=${JSON.stringify(rows.map(r => r.text))}`);
    suite.log('1.3 Each compact cell equals …+full-id-tail-16',
      rows.every(r => r.text === compactOf(r.id)), `cells=${JSON.stringify(rows.map(r => r.text))}`);
    suite.log('1.4 Full canonical id available via title', rows.every(r => ids.includes(r.id)),
      `titles=${JSON.stringify(rows.map(r => r.id))}`);
    await H.screenshot(page, ws, '01-instances-table-1920');

    // ═══ SECTION 2: Instances compact cards (mobile 430px) ═══
    await page.setViewportSize({ width: 430, height: 932 });
    await page.evaluate(() => window.navigate('adv-instances'));
    await page.waitForTimeout(500);
    const cards = await page.evaluate((wantIds) => {
      return Array.from(document.querySelectorAll('.cinst-row .compact-l2')).map(el => ({
        id: el.getAttribute('title'), text: (el.textContent || '').trim(),
      })).filter(r => wantIds.includes(r.id));
    }, ids);
    suite.log('2.1 All 3 instances rendered as compact cards', cards.length === 3, `cards=${cards.length}`);
    suite.log('2.2 Compact card ID fragments are pairwise distinct', distinct(cards.map(c => c.text.split(' · ')[0])),
      `texts=${JSON.stringify(cards.map(c => c.text.split(' · ')[0]))}`);
    suite.log('2.3 Full canonical id available via card title', cards.every(c => ids.includes(c.id)),
      `titles=${JSON.stringify(cards.map(c => c.id))}`);
    await H.screenshot(page, ws, '02-instances-cards-430');
    await page.setViewportSize({ width: 1920, height: 1080 });

    // ═══ SECTION 3: Logs selector + status bar ═══
    await page.evaluate(() => window.navigate('logs'));
    await page.waitForTimeout(500);
    const opts = await page.evaluate((wantIds) => {
      return Array.from(document.querySelectorAll('#log-instance-select option')).map(o => ({
        value: o.value, label: o.textContent.trim(),
      })).filter(o => wantIds.includes(o.value));
    }, ids);
    suite.log('3.1 Option values remain the FULL instance ids', opts.length === 3 && opts.every(o => ids.includes(o.value)),
      `values=${JSON.stringify(opts.map(o => o.value))}`);
    suite.log('3.2 Visible option labels are pairwise distinct', distinct(opts.map(o => o.label)),
      `labels=${JSON.stringify(opts.map(o => o.label))}`);
    await page.evaluate((id) => {
      const sel = document.querySelector('#log-instance-select');
      sel.value = id;
      window.switchLogInstance();
    }, ids[0]);
    await page.waitForTimeout(800);
    const bar = await page.evaluate((id) => {
      const code = document.querySelector('#log-instance-bar code');
      return code ? { text: code.textContent.trim(), title: code.getAttribute('title') } : null;
    }, ids[0]);
    suite.log('3.3 Bar shows the compact suffix form of the selected id',
      !!bar && bar.text === compactOf(ids[0]), `bar=${JSON.stringify(bar)}`);
    suite.log('3.4 Full canonical id available via bar title', !!bar && bar.title === ids[0], `title=${bar && bar.title}`);
    await H.screenshot(page, ws, '03-logs-selector-bar');

    // ═══ SECTION 3b: per-line log badges ═══
    await page.evaluate(() => {
      const sel = document.querySelector('#log-instance-select');
      sel.value = '';
      window.switchLogInstance();
    });
    await page.waitForTimeout(2000);
    const badges = await page.evaluate((wantIds) => {
      const out = [];
      document.querySelectorAll('#log-view .log-inst').forEach(el => {
        const full = el.getAttribute('title');
        if (wantIds.includes(full)) {
          if (!out.some(b => b.full === full)) out.push({ full, text: el.textContent.trim() });
        }
      });
      return out;
    }, ids);
    suite.log('3b.1 Per-line badges rendered for all 3 same-model instances', badges.length === 3,
      `badges=${JSON.stringify(badges)}`);
    suite.log('3b.2 Visible per-line badges are pairwise distinct', distinct(badges.map(b => b.text)),
      `texts=${JSON.stringify(badges.map(b => b.text))}`);
    suite.log('3b.3 Each badge equals …+full-id-tail-8 (suffix logic)',
      badges.every(b => b.text === '…' + b.full.slice(-8)), `badges=${JSON.stringify(badges)}`);
    suite.log('3b.4 Full canonical id available via badge title', badges.every(b => ids.includes(b.full)),
      `titles=${JSON.stringify(badges.map(b => b.full))}`);
    await H.screenshot(page, ws, '05-log-line-badges');

    // ═══ SECTION 4: Stop/Restart/Logs routing identity stays the FULL id ═══
    await page.evaluate(() => window.navigate('adv-instances'));
    await page.waitForTimeout(500);
    const routing = await page.evaluate((wantIds) => {
      const out = [];
      document.querySelectorAll('#adv-instances-body tr').forEach(tr => {
        const stop = tr.querySelector(".icon-btn[onclick*='stopInstance(']");
        const restart = tr.querySelector(".icon-btn[onclick*='restartInstance(']");
        const logs = tr.querySelector(".icon-btn[onclick*='viewInstanceLogs(']");
        if (stop && restart && logs) {
          out.push({
            stop: stop.getAttribute('onclick'),
            restart: restart.getAttribute('onclick'),
            logs: logs.getAttribute('onclick'),
          });
        }
      });
      return out.filter(r => wantIds.some(id => r.stop.includes("'" + id + "'")));
    }, ids);
    suite.log('4.1 Stop targets the FULL instance id', routing.length === 3 && routing.every(r => ids.some(id => r.stop.includes("'" + id + "'"))),
      `samples=${JSON.stringify(routing.slice(0, 1).map(r => r.stop))}`);
    suite.log('4.2 Restart targets the FULL instance id', routing.length === 3 && routing.every(r => ids.some(id => r.restart.includes("'" + id + "'"))),
      `samples=${JSON.stringify(routing.slice(0, 1).map(r => r.restart))}`);
    suite.log('4.3 Logs action targets the FULL instance id', routing.length === 3 && routing.every(r => ids.some(id => r.logs.includes("'" + id + "'"))),
      `samples=${JSON.stringify(routing.slice(0, 1).map(r => r.logs))}`);

    // ═══ SECTION 5: Launch History keeps full id reachable ═══
    await api('POST', `/api/v1/instances/${ids[2]}/stop`);
    await sleep(1500);
    await page.reload({ waitUntil: 'networkidle' });
    await page.waitForTimeout(500);
    await page.evaluate(() => window.navigate('history'));
    await page.waitForTimeout(500);
    const hist = await page.evaluate((wantId) => {
      const tds = Array.from(document.querySelectorAll('#history-body td.inst-mono'))
        .filter(td => (td.textContent || '').includes(wantId.slice(-16)));
      return tds.map(td => td.getAttribute('title'));
    }, ids[2]);
    suite.log('5.1 History row compact cell carries the full id in title',
      hist.length >= 1 && hist[0] === ids[2], `titles=${JSON.stringify(hist)}`);
    await H.screenshot(page, ws, '04-history-full-id');

    // ═══ SECTION 6: sanity ═══
    suite.log('6.1 No unexpected console errors', suite.consoleErrors.length === 0,
      suite.consoleErrors.slice(0, 3).join('; ') || 'clean');
    suite.log('6.2 No server 5xx errors', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));

    // Cleanup: stop remaining instances, delete the model.
    for (const id of ids) await api('POST', `/api/v1/instances/${id}/stop`);
    await sleep(500);
    await api('DELETE', `/api/v1/models/${modelId}`);

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

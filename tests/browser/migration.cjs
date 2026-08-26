'use strict';
const path = require('path');
const fs = require('fs');
const H = require('./harness.cjs');

const PORT = 19473;
const BASE = `http://127.0.0.1:${PORT}`;

// Reconstructs the legacy v5 repository fixture: one runtime, one old-style
// model (path + mmproj + arguments), one profile referencing it, one exited
// instance referencing the profile. GoAl must migrate v5 -> v7 on startup.
function writeV5Fixture(ws, fakeRt, fakeRtDir) {
  const ts = '2026-01-01T00:00:00Z';
  const modelPath = path.join(ws.dir, 'models', 'model.gguf');
  const mmprojPath = path.join(ws.dir, 'models', 'mmproj.gguf');
  const repo = {
    schema_version: 5,
    runtimes: [{
      id: 'rt-v5-1',
      name: 'Llama.cpp',
      executable: fakeRt,
      working_directory: fakeRtDir,
      default_args: [],
      created_at: ts,
      updated_at: ts,
    }],
    models: [{
      id: 'm-v5-1',
      name: 'Qwen GGUF',
      path: modelPath,
      mmproj: mmprojPath,
      arguments: ['-ngl', '999'],
      runtime_id: 'rt-v5-1',
      created_at: ts,
      updated_at: ts,
    }],
    profiles: [{
      id: 'p-v5-1',
      name: 'Qwen Production',
      runtime_id: 'rt-v5-1',
      model_id: 'm-v5-1',
      host: '0.0.0.0',
      port: 8085,
      active: true,
      created_at: ts,
      updated_at: ts,
    }],
    instances: [{
      id: 'inst-v5-1',
      profile_id: 'p-v5-1',
      runtime_id: 'rt-v5-1',
      state: 'exited',
      exit_code: 0,
      started_at: ts,
      stopped_at: ts,
      created_at: ts,
      updated_at: ts,
    }],
  };
  fs.writeFileSync(path.join(ws.dataDir, 'goal_repo.json'), JSON.stringify(repo, null, 2));
  return { modelPath, mmprojPath };
}

async function main() {
  console.log('=== v5 -> v7 repository migration (real Chromium) ===\n');

  const ws = H.makeWorkspace('migration');
  const goalBin = H.buildGoal(ws);
  const fakeRt = H.buildFakeRuntime(ws);
  const fakeRtDir = path.dirname(fakeRt);

  H.writeConfig(ws, {
    webPort: PORT,
    authEnabled: false,
  });

  writeV5Fixture(ws, fakeRt, fakeRtDir);
  console.log('v5 fixture written; starting GoAl (migration on load)...\n');

  const server = H.startServer(goalBin, ws);
  const started = await H.waitForHealth(BASE);
  if (!started) {
    console.error('Server failed to start. Output:', server.output);
    await server.stop();
    process.exit(1);
  }
  console.log('Server started with v5 data. Checking migration...\n');

  const suite = H.newSuite('MIGRATION (v5 -> v7)');
  const browser = await H.launchBrowser();
  let ok = false;
  try {
    const repo = JSON.parse(fs.readFileSync(path.join(ws.dataDir, 'goal_repo.json'), 'utf8'));
    suite.log('M1. Repository migrated to schema v7', repo.schema_version === 7, 'version=' + repo.schema_version);
    suite.log('M2. No profiles key in v7', !('profiles' in repo));
    suite.log('M3. Has models key', Array.isArray(repo.models), 'count=' + (repo.models || []).length);

    if (repo.models && repo.models.length === 1) {
      const m = repo.models[0];
      suite.log('M4. Model ID = old profile ID', m.id === 'p-v5-1', 'id=' + m.id);
      suite.log('M5. Model name preserved', m.name === 'Qwen Production', 'name=' + m.name);
      suite.log('M6. Model has -m arg (from old model path)', m.args && m.args.includes('-m'), 'args=' + JSON.stringify(m.args));
      suite.log('M7. Model has --mmproj arg', m.args && m.args.includes('--mmproj'));
      suite.log('M8. Model has -ngl arg (from old model arguments)', m.args && m.args.includes('-ngl'));
      const hostOk = m.args && m.args.includes('--host') && m.args[m.args.indexOf('--host') + 1] === '0.0.0.0';
      const portOk = m.args && m.args.includes('--port') && m.args[m.args.indexOf('--port') + 1] === '8085';
      suite.log('M9. Model args carry --host 0.0.0.0', hostOk);
      suite.log('M10. Model args carry --port 8085', portOk);
      suite.log('M11. Model active preserved', m.active === true);
    } else {
      suite.log('M4-M11. Model migration', false, 'no model found, models=' + JSON.stringify(repo.models));
    }

    if (repo.instances && repo.instances.length === 1) {
      const inst = repo.instances[0];
      suite.log('M12. Instance model_id = old profile_id', inst.model_id === 'p-v5-1', 'model_id=' + inst.model_id);
      suite.log('M13. Instance has no profile_id field', !('profile_id' in inst));
    }

    const page = await browser.newContext({ viewport: { width: 1920, height: 1080 } }).then(c => c.newPage());
    suite.watchPage(page);

    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);
    await H.screenshot(page, ws, 'migration-ui');

    const rows = await page.locator('#model-list .model-row').count();
    suite.log('M14. UI shows migrated model row', rows === 1, 'rows=' + rows);

    const rowText = rows > 0 ? await page.locator('#model-list .model-row').first().textContent() : '';
    suite.log('M15. Row shows model name', rowText.includes('Qwen Production'));

    const res = await H.api(BASE, 'POST', '/api/v1/models/p-v5-1/resolve');
    suite.log('M16. Migrated model resolves', res.status === 200, 'status=' + res.status);
    if (res.status === 200) {
      const spec = res.data;
      suite.log('M17. Resolved args contain -m', spec.args && spec.args.includes('-m'));
      const mIdx = spec.args ? spec.args.indexOf('-m') : -1;
      suite.log('M18. -m value references model.gguf', mIdx >= 0 && spec.args[mIdx + 1].includes('model.gguf'), mIdx >= 0 ? spec.args[mIdx + 1] : 'n/a');
      suite.log('M19. Resolved args contain --mmproj', spec.args && spec.args.includes('--mmproj'));
      const ngIdx = spec.args ? spec.args.indexOf('-ngl') : -1;
      suite.log('M20. -ngl value is 999', ngIdx >= 0 && spec.args[ngIdx + 1] === '999', ngIdx >= 0 ? spec.args[ngIdx + 1] : 'n/a');
    }

    await page.click('#sidebar button[data-view="history"]');
    await page.waitForTimeout(1000);
    const histRows = await page.locator('#history-body tr').count();
    suite.log('M21. History shows migrated instance', histRows >= 1, 'rows=' + histRows);

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

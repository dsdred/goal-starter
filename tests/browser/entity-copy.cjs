'use strict';
const path = require('path');
const H = require('./harness.cjs');

const PORT = 19488;
const BASE = `http://127.0.0.1:${PORT}`;

module.exports = { BASE };

const sleep = ms => new Promise(r => setTimeout(r, ms));

// ADR 027 (ENTITY-DUPLICATE-01): copy of runtimes, models and pipelines through
// the existing creation forms. Copy button between Edit and Delete; pre-filled
// create form with a localized suggested name; new unique entity and pipeline
// entry IDs; dependency references preserved; original unchanged; env keys
// copied without write-only values; D-9 contextual notice; cancel = zero
// writes; existing validation (runtime name 409) unchanged; portable export
// carries the copies and the round-trip import restores them into a fresh
// second instance; EN and RU.
async function main() {
  const ws = H.makeWorkspace('entity-copy');
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

  // Seed: one runtime with env keys, one active model with args + env keys,
  // one pipeline with two entries (second one has a custom args override).
  const rt = await api('POST', '/api/v1/runtimes', {
    name: 'CopySrc Rt', executable: fakeRt, working_directory: fakeRtDir,
    environment: { COPY_SRC_KEY: 'secret-value' },
  });
  const rtId = rt.data && rt.data.id;
  const mdl = await api('POST', '/api/v1/models', {
    name: 'CopySrc Model', runtime_id: rtId, args: ['copy-src-arg'],
    active: true, autostart_delay: 3, environment: { MODEL_KEY: 'mv' },
  });
  const mdlId = mdl.data && mdl.data.id;
  const mdl2 = await api('POST', '/api/v1/models', { name: 'CopySrc Model 2', runtime_id: rtId });
  const mdl2Id = mdl2.data && mdl2.data.id;
  const pl = await api('POST', '/api/v1/pipelines', {
    name: 'CopySrc Pipeline', active: true,
    models: [
      { model_id: mdlId },
      { model_id: mdl2Id, args: ['p-arg-1', 'p-arg-2'] },
    ],
  });
  const plId = pl.data && pl.data.id;

  const browser = await H.launchBrowser();
  const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const page = await ctx.newPage();
  const suite = H.newSuite('ENTITY-COPY (ADR 027 duplication of runtimes / models / pipelines)');
  suite.watchPage(page);

  const getRuntime = async (id) => (await api('GET', `/api/v1/runtimes/${id}`)).data;
  const getModel = async (id) => (await api('GET', `/api/v1/models/${id}`)).data;
  // The detail endpoint wraps the pipeline: {"pipeline": {...}, "models": [statuses]}.
  const getPipeline = async (id) => {
    const d = (await api('GET', `/api/v1/pipelines/${id}`)).data || {};
    return d.pipeline ? d.pipeline : d;
  };
  const listRuntimes = async () => (await api('GET', '/api/v1/runtimes')).data || [];
  const listModels = async () => (await api('GET', '/api/v1/models')).data || [];
  const listPipelines = async () => (await api('GET', '/api/v1/pipelines')).data || [];

  let ok = false;
  let server2 = null;
  let copiedRtId = null, copiedMdlId = null, copiedPlId = null;
  try {
    await page.goto(BASE, { waitUntil: 'networkidle' });
    await page.waitForTimeout(1000);
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(600);

    const srcRt = await getRuntime(rtId);
    const srcMdl = await getModel(mdlId);
    const srcPl = await getPipeline(plId);
    suite.log('Seed: runtime + 2 models + pipeline created',
      !!(rtId && mdlId && mdl2Id && plId), `rt=${rtId} mdl=${mdlId} mdl2=${mdl2Id} pl=${plId}`);
    suite.log('Seed: source data present (env keys, args, entries)',
      (srcRt.environment_keys || []).includes('COPY_SRC_KEY') &&
      (srcMdl.environment_keys || []).includes('MODEL_KEY') &&
      (srcMdl.args || []).includes('copy-src-arg') &&
      Array.isArray(srcPl.models) && srcPl.models.length === 2,
      `rtKeys=${JSON.stringify(srcRt.environment_keys)} mdlArgs=${JSON.stringify(srcMdl.args)} plEntries=${(srcPl.models || []).length}`);

    // ═══ SECTION 1: Runtime copy (EN) ═══
    await page.evaluate(() => window.navigate('adv-runtimes'));
    await page.waitForTimeout(600);
    const rtCopyInfo = await page.evaluate((id) => {
      const row = Array.from(document.querySelectorAll('#adv-runtimes-body tr'))
        .find(tr => tr.querySelector(`[onclick*="editRuntime('${id}')"]`));
      if (!row) return { found: false };
      const btns = Array.from(row.querySelectorAll('.actions-cell button'));
      const copyBtn = row.querySelector(`[onclick*="copyRuntime('${id}')"]`);
      const editBtn = row.querySelector(`[onclick*="editRuntime('${id}')"]`);
      const delBtn = row.querySelector(`[onclick*="deleteRuntime('${id}')"]`);
      const idxOf = (el) => btns.indexOf(el);
      return {
        found: true,
        hasCopy: !!copyBtn,
        placement: copyBtn && editBtn && delBtn ? idxOf(editBtn) < idxOf(copyBtn) && idxOf(copyBtn) < idxOf(delBtn) : false,
        label: copyBtn ? (copyBtn.getAttribute('data-tooltip') || copyBtn.getAttribute('aria-label') || '') : '',
      };
    }, rtId);
    suite.log('1.1 Copy button present in the runtime row', rtCopyInfo.found && rtCopyInfo.hasCopy, JSON.stringify(rtCopyInfo));
    suite.log('1.2 Copy button placed between Edit and Delete', !!rtCopyInfo.placement, `placement=${rtCopyInfo.placement}`);
    suite.log('1.3 Copy button label localized (EN "Copy")', rtCopyInfo.label === 'Copy', `label=${JSON.stringify(rtCopyInfo.label)}`);

    await page.evaluate((id) => window.copyRuntime(id), rtId);
    await page.waitForTimeout(400);
    const prefill = await page.evaluate(() => {
      const f = document.querySelector('#create-runtime-modal form');
      const rows = Array.from(document.querySelectorAll('#rt-create-env-container .env-row'));
      const notice = document.querySelector('.copy-env-notice');
      return {
        visible: document.getElementById('create-runtime-modal').style.display !== 'none',
        name: f ? f.querySelector('[name=name]').value : '',
        executable: f ? f.querySelector('[name=executable]').value : '',
        workdir: f ? f.querySelector('[name=working_directory]').value : '',
        envKeys: rows.map(r => r.querySelectorAll('input')[0].value),
        envVals: rows.map(r => (r.querySelectorAll('input')[1] || {}).value || ''),
        notice: notice ? notice.textContent : null,
        noticeClass: notice ? notice.className : null,
      };
    });
    suite.log('1.4 Create modal opens pre-filled (executable + workdir from source)',
      prefill.visible && prefill.executable === srcRt.executable && prefill.workdir === (srcRt.working_directory || ''),
      `exe=${JSON.stringify(prefill.executable)} wd=${JSON.stringify(prefill.workdir)}`);
    suite.log('1.5 Suggested name EN "Copy of {name}"', prefill.name === 'Copy of CopySrc Rt', `name=${JSON.stringify(prefill.name)}`);
    suite.log('1.6 Env keys pre-filled with empty values (write-only)',
      JSON.stringify(prefill.envKeys) === JSON.stringify(['COPY_SRC_KEY']) && JSON.stringify(prefill.envVals) === JSON.stringify(['']),
      `keys=${JSON.stringify(prefill.envKeys)} vals=${JSON.stringify(prefill.envVals)}`);
    suite.log('1.7 D-9 notice shown with exact EN text (neutral info notice, not a warning)',
      prefill.notice === 'Environment variable values are not copied. Re-enter them if required.' &&
      prefill.noticeClass === 'notice notice-info copy-env-notice',
      `notice=${JSON.stringify(prefill.notice)} class=${JSON.stringify(prefill.noticeClass)}`);

    // User edits the suggested name before saving, then saves.
    await page.fill('#create-runtime-modal form [name=name]', 'Copied Runtime A');
    await page.click('#create-runtime-modal .btn-primary');
    await page.waitForTimeout(800);
    const rtAfter = await listRuntimes();
    const copiedRt = rtAfter.find(r => r.name === 'Copied Runtime A');
    copiedRtId = copiedRt && copiedRt.id;
    suite.log('1.8 Saved copy has a NEW unique ID', !!copiedRtId && copiedRtId !== rtId, `new=${copiedRtId} src=${rtId}`);
    const srcRt2 = await getRuntime(rtId);
    const origRtUnchanged = srcRt2.name === 'CopySrc Rt' && srcRt2.executable === srcRt.executable &&
      JSON.stringify(srcRt2.environment_keys || []) === JSON.stringify(srcRt.environment_keys || []);
    suite.log('1.9 Original runtime unchanged', origRtUnchanged, `name=${srcRt2.name}`);
    const copiedRtData = copiedRtId ? await getRuntime(copiedRtId) : {};
    suite.log('1.10 Copied runtime keeps settings + env keys (values never exposed)',
      copiedRtData.executable === srcRt.executable &&
      JSON.stringify(copiedRtData.environment_keys || []) === JSON.stringify(['COPY_SRC_KEY']) &&
      copiedRtData.environment === undefined,
      `exe=${JSON.stringify(copiedRtData.executable)} keys=${JSON.stringify(copiedRtData.environment_keys)} envField=${JSON.stringify(copiedRtData.environment)}`);

    // Cancel: open the copy form, close it, nothing is written.
    await page.evaluate((id) => window.copyRuntime(id), rtId);
    await page.waitForTimeout(300);
    await page.click('#create-runtime-modal .modal-close');
    await page.waitForTimeout(500);
    const rtAfterCancel = await listRuntimes();
    suite.log('1.11 Cancel produces zero writes', rtAfterCancel.length === rtAfter.length, `count=${rtAfterCancel.length}`);

    // Existing validation: duplicate runtime name (case-insensitive) → refused, nothing created.
    await page.evaluate((id) => window.copyRuntime(id), rtId);
    await page.waitForTimeout(300);
    await page.fill('#create-runtime-modal form [name=name]', 'copysrc rt');
    await page.click('#create-runtime-modal .btn-primary');
    await page.waitForTimeout(800);
    const rtAfterConflict = await listRuntimes();
    suite.log('1.12 Duplicate-name save refused by existing validation (no new runtime)',
      rtAfterConflict.length === rtAfter.length, `count=${rtAfterConflict.length}`);
    await page.click('#create-runtime-modal .modal-close');
    await page.waitForTimeout(300);

    // ═══ SECTION 2: Model copy (EN) ═══
    await page.evaluate(() => window.navigate('models'));
    await page.waitForTimeout(600);
    const mdlCopyBtn = await page.$(`#model-list [onclick*="copyModel('${mdlId}')"]`);
    suite.log('2.1 Copy button present in the model row', !!mdlCopyBtn);
    if (mdlCopyBtn) {
      const mdlBtnLabel = await mdlCopyBtn.evaluate(el => el.getAttribute('data-tooltip') || el.getAttribute('aria-label') || '');
      suite.log('2.2 Model copy label localized (EN "Copy")', mdlBtnLabel === 'Copy', `label=${JSON.stringify(mdlBtnLabel)}`);
    }
    await page.evaluate((id) => window.copyModel(id), mdlId);
    await page.waitForTimeout(400);
    const wpref = await page.evaluate(() => {
      const row = Array.from(document.querySelectorAll('#wiz-env-container .env-row'));
      const notice = document.querySelector('.copy-env-notice');
      const sel = document.querySelector('#wiz-rt-dropdown .rt-dropdown-item.selected');
      return {
        title: document.getElementById('wizard-title').textContent,
        name: document.getElementById('wiz-name').value,
        args: document.getElementById('wiz-args').value,
        autostart: document.getElementById('wiz-autostart').checked,
        delayVisible: document.getElementById('wiz-delay-group').style.display !== 'none',
        delayValue: document.getElementById('wiz-autostart-delay').value,
        envKeys: row.map(r => r.querySelectorAll('input')[0].value),
        envVals: row.map(r => (r.querySelectorAll('input')[1] || {}).value || ''),
        rtSelected: sel ? sel.dataset.id : null,
        notice: notice ? notice.textContent : null,
      };
    });
    const wizAddTitle = await page.evaluate(() => t('wizard.title.add'));
    suite.log('2.3 Wizard opens in CREATE mode with EN suggested name',
      wpref.title === wizAddTitle && wpref.name === 'Copy of CopySrc Model', `title=${JSON.stringify(wpref.title)} name=${JSON.stringify(wpref.name)}`);
    suite.log('2.4 Args pre-filled from the source', wpref.args.split('\n').filter(Boolean).includes('copy-src-arg'), `args=${JSON.stringify(wpref.args)}`);
    suite.log('2.5 Autostart default OFF even though source is active (delay copied, group hidden)',
      wpref.autostart === false && wpref.delayVisible === false && String(wpref.delayValue) === '3',
      `autostart=${wpref.autostart} delayVisible=${wpref.delayVisible} delay=${wpref.delayValue}`);
    suite.log('2.6 Source runtime pre-selected in the dropdown', wpref.rtSelected === rtId, `sel=${wpref.rtSelected}`);
    suite.log('2.7 Model env key pre-filled write-only + D-9 EN notice',
      JSON.stringify(wpref.envKeys) === JSON.stringify(['MODEL_KEY']) && JSON.stringify(wpref.envVals) === JSON.stringify(['']) &&
      wpref.notice === 'Environment variable values are not copied. Re-enter them if required.',
      `keys=${JSON.stringify(wpref.envKeys)} notice=${JSON.stringify(wpref.notice)}`);

    // Walk the wizard: step 1 → 2 → 3, then Create.
    await page.click('#wiz-next'); // step 1 → 2
    await page.waitForTimeout(300);
    await page.click('#wiz-next'); // step 2 → 3
    await page.waitForTimeout(300);
    await page.click('#wiz-next'); // step 3: Create
    await page.waitForTimeout(900);
    const mdlAfter = await listModels();
    const copiedMdl = mdlAfter.find(m => m.name === 'Copy of CopySrc Model');
    copiedMdlId = copiedMdl && copiedMdl.id;
    suite.log('2.8 Saved copy has a NEW unique model ID', !!copiedMdlId && copiedMdlId !== mdlId, `new=${copiedMdlId} src=${mdlId}`);
    const srcMdl2 = await getModel(mdlId);
    suite.log('2.9 Original model unchanged',
      srcMdl2.name === 'CopySrc Model' && srcMdl2.active === true &&
      JSON.stringify(srcMdl2.args || []) === JSON.stringify(srcMdl.args || []),
      `name=${srcMdl2.name} active=${srcMdl2.active}`);
    const copiedMdlData = copiedMdlId ? await getModel(copiedMdlId) : {};
    suite.log('2.10 Copy references the EXISTING source runtime',
      copiedMdlData.runtime_id === rtId, `rt=${copiedMdlData.runtime_id}`);
    suite.log('2.11 Copy settings: args preserved, active=false, env keys present',
      JSON.stringify(copiedMdlData.args || []) === JSON.stringify(['copy-src-arg']) &&
      copiedMdlData.active === false &&
      JSON.stringify(copiedMdlData.environment_keys || []) === JSON.stringify(['MODEL_KEY']) &&
      copiedMdlData.environment === undefined,
      `args=${JSON.stringify(copiedMdlData.args)} active=${copiedMdlData.active} keys=${JSON.stringify(copiedMdlData.environment_keys)}`);

    // Cancel leg: open copy of the second model, walk to step 3, close, nothing written.
    await page.evaluate((id) => window.copyModel(id), mdl2Id);
    await page.waitForTimeout(300);
    await page.click('#wiz-next');
    await page.waitForTimeout(200);
    await page.click('#wiz-next');
    await page.waitForTimeout(200);
    await page.evaluate(() => window.closeWizard());
    await page.waitForTimeout(500);
    const mdlAfterCancel = await listModels();
    suite.log('2.12 Cancel produces zero writes (model)', mdlAfterCancel.length === mdlAfter.length, `count=${mdlAfterCancel.length}`);

    // ═══ SECTION 3: Pipeline copy (EN) ═══
    await page.evaluate(() => window.navigate('pipelines'));
    await page.waitForTimeout(600);
    const plCopyBtn = await page.$(`#pipeline-list [onclick*="copyPipeline('${plId}')"]`);
    suite.log('3.1 Copy button present in the pipeline row', !!plCopyBtn);
    await page.evaluate((id) => window.copyPipeline(id), plId);
    await page.waitForTimeout(400);
    const plpref = await page.evaluate(() => {
      const form = document.getElementById('pipeline-form');
      const blocks = Array.from(document.querySelectorAll('#pl-models-container .pl-model-select'));
      return {
        hiddenId: form.id.value,
        name: form.name.value,
        active: form.active.checked,
        title: document.getElementById('pipeline-modal-title').textContent,
        btn: document.getElementById('pipeline-submit-btn').textContent,
        models: blocks.map(s => s.value),
      };
    });
    const plCreateTitle = await page.evaluate(() => t('pipelines.create.title'));
    const plCreateBtn = await page.evaluate(() => t('pipelines.btn.create'));
    suite.log('3.2 Pipeline form opens in CREATE mode (empty id, create title/button)',
      plpref.hiddenId === '' && plpref.title === plCreateTitle && plpref.btn === plCreateBtn,
      `id=${JSON.stringify(plpref.hiddenId)} title=${JSON.stringify(plpref.title)} btn=${JSON.stringify(plpref.btn)}`);
    suite.log('3.3 Suggested name EN + active unchecked despite source active=true',
      plpref.name === 'Copy of CopySrc Pipeline' && plpref.active === false, `name=${JSON.stringify(plpref.name)} active=${plpref.active}`);
    suite.log('3.4 Entries pre-filled: same models, same order',
      JSON.stringify(plpref.models) === JSON.stringify([mdlId, mdl2Id]), `models=${JSON.stringify(plpref.models)}`);

    await page.click('#pipeline-submit-btn');
    await page.waitForTimeout(900);
    const plAfter = await listPipelines();
    const copiedPl = plAfter.find(p => p.name === 'Copy of CopySrc Pipeline');
    copiedPlId = copiedPl && copiedPl.id;
    suite.log('3.5 Saved copy has a NEW unique pipeline ID', !!copiedPlId && copiedPlId !== plId, `new=${copiedPlId} src=${plId}`);
    const srcPl2 = await getPipeline(plId);
    suite.log('3.6 Original pipeline unchanged (entries keep their ids)',
      JSON.stringify((srcPl2.models || []).map(m => m.id)) === JSON.stringify((srcPl.models || []).map(m => m.id)) && srcPl2.active === true,
      `entries=${JSON.stringify((srcPl2.models || []).map(m => m.id))}`);
    const copiedPlData = copiedPlId ? await getPipeline(copiedPlId) : {};
    const newEntries = (copiedPlData.models || []).map(m => m.id);
    suite.log('3.7 Copy entries receive NEW entry IDs (all differ from source)',
      newEntries.length === 2 && newEntries.every(id => id && !(srcPl.models || []).some(s => s.id === id)),
      `new=${JSON.stringify(newEntries)} src=${JSON.stringify((srcPl.models || []).map(m => m.id))}`);
    suite.log('3.8 Copy references existing models in the same order; custom args preserved; active=false',
      JSON.stringify((copiedPlData.models || []).map(m => m.model_id)) === JSON.stringify([mdlId, mdl2Id]) &&
      JSON.stringify((copiedPlData.models || [])[1] && ((copiedPlData.models)[1].args || [])) === JSON.stringify(['p-arg-1', 'p-arg-2']) &&
      copiedPlData.active === false,
      `refs=${JSON.stringify((copiedPlData.models || []).map(m => m.model_id))} active=${copiedPlData.active}`);

    // ═══ SECTION 4: RU localization ═══
    await page.evaluate(() => window.setLanguage('ru'));
    await page.waitForTimeout(800);
    await page.evaluate(() => window.navigate('adv-runtimes'));
    await page.waitForTimeout(600);
    const ruCopyBtn = await page.$(`#adv-runtimes-body [onclick*="copyRuntime('${rtId}')"]`);
    const ruCopyLabel = ruCopyBtn ? await ruCopyBtn.evaluate(el => el.getAttribute('data-tooltip') || el.getAttribute('aria-label') || '') : '';
    suite.log('4.1 RU copy label «Копировать»', ruCopyLabel === 'Копировать', `label=${JSON.stringify(ruCopyLabel)}`);
    await page.evaluate((id) => window.copyRuntime(id), rtId);
    await page.waitForTimeout(400);
    const rupref = await page.evaluate(() => {
      const f = document.querySelector('#create-runtime-modal form');
      const notice = document.querySelector('.copy-env-notice');
      return { name: f ? f.querySelector('[name=name]').value : '', notice: notice ? notice.textContent : null };
    });
    suite.log('4.2 RU suggested name "{name} (копия)"', rupref.name === 'CopySrc Rt (копия)', `name=${JSON.stringify(rupref.name)}`);
    suite.log('4.3 D-9 RU notice exact text',
      rupref.notice === 'Значения переменных окружения не копируются. При необходимости заполните их заново.',
      `notice=${JSON.stringify(rupref.notice)}`);
    await page.click('#create-runtime-modal .modal-close');
    await page.waitForTimeout(300);
    await page.evaluate(() => window.navigate('models'));
    await page.waitForTimeout(600);
    const ruMdlCopyLabel = await page.$eval(`#model-list [onclick*="copyModel('${mdlId}')"]`,
      el => el.getAttribute('data-tooltip') || el.getAttribute('aria-label') || '').catch(() => '');
    suite.log('4.4 RU model copy label «Копировать»', ruMdlCopyLabel === 'Копировать', `label=${JSON.stringify(ruMdlCopyLabel)}`);
    await page.evaluate((id) => window.copyModel(id), mdlId);
    await page.waitForTimeout(400);
    const ruWizName = await page.evaluate(() => document.getElementById('wiz-name').value);
    suite.log('4.5 RU wizard suggested name', ruWizName === 'CopySrc Model (копия)', `name=${JSON.stringify(ruWizName)}`);
    await page.evaluate(() => window.closeWizard());
    await page.waitForTimeout(300);
    await page.evaluate(() => window.setLanguage('en'));
    await page.waitForTimeout(500);

    // ═══ SECTION 5: Regression — existing Create / Edit flows intact ═══
    await page.evaluate(() => window.navigate('adv-runtimes'));
    await page.waitForTimeout(600);
    await page.evaluate(() => window.showCreateRuntimeModal());
    await page.waitForTimeout(300);
    const freshForm = await page.evaluate(() => {
      const f = document.querySelector('#create-runtime-modal form');
      return {
        name: f.querySelector('[name=name]').value,
        rows: document.querySelectorAll('#rt-create-env-container .env-row').length,
        notice: !!document.querySelector('.copy-env-notice'),
      };
    });
    await page.fill('#create-runtime-modal form [name=name]', 'Manual Rt');
    await page.fill('#create-runtime-modal form [name=executable]', fakeRt);
    await page.click('#create-runtime-modal .btn-primary');
    await page.waitForTimeout(800);
    const manualRt = (await listRuntimes()).find(r => r.name === 'Manual Rt');
    suite.log('5.1 Manual runtime create flow still works',
      freshForm.name === '' && freshForm.rows === 0 && freshForm.notice === false && !!manualRt,
      `created=${!!manualRt} fresh=${JSON.stringify(freshForm)}`);
    await page.evaluate((id) => window.editRuntime(id), rtId);
    await page.waitForTimeout(400);
    const editPrefill = await page.evaluate((id) => {
      const modal = document.getElementById('edit-runtime-modal');
      const f = modal.querySelector('form');
      return { id: f.querySelector('[name=id]').value, name: f.querySelector('[name=name]').value };
    }, rtId);
    await page.click('#edit-runtime-modal .modal-close');
    suite.log('5.2 Runtime edit flow still pre-fills the source', editPrefill.id === rtId && editPrefill.name === 'CopySrc Rt',
      JSON.stringify(editPrefill));
    await page.evaluate((id) => window.editPipeline(id), plId);
    await page.waitForTimeout(400);
    const plEdit = await page.evaluate((id) => {
      const form = document.getElementById('pipeline-form');
      return { id: form.id.value, btn: document.getElementById('pipeline-submit-btn').textContent };
    }, plId);
    await page.click('#pipeline-modal .modal-close');
    suite.log('5.3 Pipeline edit flow still opens in edit mode', plEdit.id === plId, JSON.stringify(plEdit));

    // ═══ SECTION 6: Portable export carries the copies ═══
    const bundle = (await api('GET', '/api/v1/export')).data || {};
    const bundleModels = (bundle.models || []).map(m => m.id);
    const bundlePipelines = (bundle.pipelines || []).map(p => p.id);
    const bundleRuntimes = (bundle.runtimes || []).map(r => r.id);
    suite.log('6.1 Export bundle contains original + copied entities (new IDs, format unchanged)',
      bundleModels.includes(mdlId) && bundleModels.includes(copiedMdlId) &&
      bundlePipelines.includes(plId) && bundlePipelines.includes(copiedPlId) &&
      bundleRuntimes.includes(rtId) && bundleRuntimes.includes(copiedRtId),
      `models=${bundleModels.length} pipelines=${bundlePipelines.length} runtimes=${bundleRuntimes.length}`);

    // ═══ SECTION 6b: round-trip import into a fresh second instance ═══
    const ws2 = H.makeWorkspace('entity-copy-import');
    H.writeConfig(ws2, { webPort: 19489 });
    server2 = H.startServer(goalBin, ws2);
    const started2 = await H.waitForHealth(`http://127.0.0.1:19489`);
    if (!started2) {
      ok = false;
      throw new Error('second instance failed to start: ' + (server2.output || '').slice(-200));
    }
    const api2 = (method, urlPath, body) => H.api('http://127.0.0.1:19489', method, urlPath, body);
    const plan = await api2('POST', '/api/v1/import', bundle);
    const planBody = (plan && plan.data) || {};
    const summary = planBody.summary || {};
    const allNew = (t, n) => t && t.total === n && t.new === n && t.update === 0 && t.unchanged === 0 && t.blocked === 0;
    suite.log('6.2 Import on the fresh instance: 200, every entity classified new, none blocked',
      plan.status === 200 && planBody.can_import === true &&
      allNew(summary.runtimes, bundle.runtimes.length) &&
      allNew(summary.models, bundle.models.length) &&
      allNew(summary.pipelines, bundle.pipelines.length) &&
      planBody.created && planBody.created.runtimes === bundle.runtimes.length &&
      planBody.blocked.length === 0,
      `summary=${JSON.stringify(summary)} blocked=${planBody.blocked.length}`);
    const impMdl = (await api2('GET', `/api/v1/models/${copiedMdlId}`)).data;
    const impPl = await api2('GET', `/api/v1/pipelines/${copiedPlId}`);
    const impPlBody = (impPl && impPl.data && impPl.data.pipeline) || {};
    suite.log('6.3 Imported copy keeps its new IDs and preserved references on the fresh instance',
      !!impMdl && impMdl.name === 'Copy of CopySrc Model' && impMdl.runtime_id === rtId &&
      impPlBody.id === copiedPlId &&
      JSON.stringify((impPlBody.models || []).map(m => m.model_id)) === JSON.stringify([mdlId, mdl2Id]) &&
      JSON.stringify((impPlBody.models || []).map(m => m.id)) === JSON.stringify(newEntries),
      `mdl=${JSON.stringify(impMdl && impMdl.runtime_id)} pl=${impPlBody.id} refs=${JSON.stringify((impPlBody.models || []).map(m => m.model_id))}`);
    const impRt = (await api2('GET', `/api/v1/runtimes/${copiedRtId}`)).data;
    // ADR 018 D7/D11: the bundle carries env keys only, and import never
    // restores keys or values — the imported copies arrive with an empty
    // environment, exactly as the D-9 notice tells the user.
    suite.log('6.4 Imported copies: environment not restored (ADR 018 D7/D11), values absent, active flags preserved',
      !!impRt && JSON.stringify(impRt.environment_keys || []) === '[]' &&
      impRt.environment === undefined &&
      impMdl && JSON.stringify(impMdl.environment_keys || []) === '[]' &&
      impMdl.environment === undefined && impMdl.active === false &&
      impPlBody.active === false,
      `rtKeys=${JSON.stringify(impRt && impRt.environment_keys)} mdlKeys=${JSON.stringify(impMdl && impMdl.environment_keys)} flags=${impMdl && impMdl.active}/${impPlBody.active}`);
    const impSrcMdl = (await api2('GET', `/api/v1/models/${mdlId}`)).data;
    suite.log('6.5 Imported source keeps its identity (no ID remap, active preserved)',
      !!impSrcMdl && impSrcMdl.name === 'CopySrc Model' && impSrcMdl.active === true,
      `name=${impSrcMdl && impSrcMdl.name} active=${impSrcMdl && impSrcMdl.active}`);

    // ═══ SECTION 7: i18n hard rule ═══
    const missing = await page.evaluate(() => Object.keys(window.i18nMissing || {}));
    suite.log('7.1 Session i18nMissing set is empty (EN + RU copy flows)', missing.length === 0,
      `missing=${JSON.stringify(missing)}`);
    // The conflict leg (1.12) deliberately triggers one 409; everything else must be clean.
    const expected409 = suite.consoleErrors.filter(e => /status of 409/.test(e));
    const unexpected = suite.consoleErrors.filter(e => !/status of 409/.test(e));
    suite.log('7.2 Console: only the deliberate 409 from the conflict leg (1.12)',
      unexpected.length === 0 && expected409.length === 1,
      `unexpected=${JSON.stringify(unexpected.slice(0, 3))} expected409=${expected409.length}`);
    suite.log('7.3 No server 5xx errors', suite.serverErrors.length === 0, JSON.stringify(suite.serverErrors.slice(0, 3)));

    // Cleanup: delete all created entities (copies first, then sources).
    for (const id of [copiedPlId, plId, copiedMdlId, mdlId, mdl2Id]) {
      if (id) await api('DELETE', `/api/v1/pipelines/${id}`).catch(() => {});
      if (id && id !== plId && id !== copiedPlId) await api('DELETE', `/api/v1/models/${id}`).catch(() => {});
    }
    for (const id of [copiedRtId, rtId]) {
      if (id) await api('DELETE', `/api/v1/runtimes/${id}`).catch(() => {});
    }
    const manualCleanup = (await listRuntimes()).find(r => r.name === 'Manual Rt');
    if (manualCleanup) await api('DELETE', `/api/v1/runtimes/${manualCleanup.id}`).catch(() => {});

    ok = await suite.finish();
  } catch (err) {
    suite.log('FATAL: ' + err.message, false, err.stack ? err.stack.split('\n')[0] : '');
    ok = false;
    await suite.finish();
  } finally {
    await browser.close();
    if (server2) await server2.stop();
    await server.stop();
  }

  process.exit(ok ? 0 : 1);
}

main().catch(err => {
  console.error('HARNESS ERROR:', err);
  process.exit(2);
});

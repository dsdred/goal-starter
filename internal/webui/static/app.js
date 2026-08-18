(function () {
'use strict';

let csrfToken = '';
let currentView = 'models';
let logEs = null;
let logPaused = false;
let refreshTimer = null;

// Data cache
let profilesData = [];
let runtimesData = [];
let modelsData = [];
let instancesData = [];

// ─── Init ───────────────────────────────────────────────────────────────────

async function init() {
    await getCSRFToken();
    const auth = await checkAuth();
    if (!auth) {
        showLogin();
        return;
    }
    document.getElementById('app-shell').style.display = 'flex';
    await reloadAllData();
    renderAll();
    startRefresh();
}

// ─── Auth ───────────────────────────────────────────────────────────────────

async function getCSRFToken() {
    const m = document.cookie.match(/goal_csrf_token=([^;]+)/);
    csrfToken = m ? m[1] : '';
}

async function checkAuth() {
    try {
        const r = await fetch('/api/v1/auth/session');
        if (r.ok) {
            const d = await r.json();
            if (d.authenticated) {
                document.getElementById('set-user').textContent = d.user || '';
                return true;
            }
        }
    } catch {}
    showLogin();
    return false;
}

function showLogin() {
    document.getElementById('login-modal').style.display = 'flex';
}

function resetLoginState() {
    var passwordEl = document.getElementById('password');
    var errorEl = document.getElementById('login-error');
    passwordEl.value = '';
    errorEl.textContent = '';
    errorEl.style.display = 'none';
}

function showLoginError(message) {
    var errorEl = document.getElementById('login-error');
    errorEl.textContent = message;
    errorEl.style.display = 'block';
}

async function handleLogin(e) {
    e.preventDefault();
    const u = document.getElementById('username').value;
    const p = document.getElementById('password').value;
    resetLoginState();
    try {
        const r = await fetch('/api/v1/auth/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ username: u, password: p })
        });
        if (r.ok) {
            await getCSRFToken();
            document.getElementById('login-modal').style.display = 'none';
            document.getElementById('app-shell').style.display = 'flex';
            await reloadAllData();
            renderAll();
            startRefresh();
        } else {
            let data = {};
            try { data = await r.json(); } catch {}
            showLoginError(data.error || 'Authentication failed');
        }
    } catch (err) {
        showLoginError('Login request failed: ' + err.message);
    }
    return false;
}

async function handleLogout() {
    try { await fetch('/api/v1/auth/logout', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken } }); } catch {}
    resetLoginState();
    showLogin();
}

// ─── API helper ─────────────────────────────────────────────────────────────

async function api(path, opts = {}) {
    const headers = { 'Content-Type': 'application/json' };
    if (opts.method && opts.method !== 'GET') headers['X-CSRF-Token'] = csrfToken;
    const r = await fetch('/api/v1' + path, { ...opts, headers });
    if (!r.ok) {
        let msg = r.statusText;
        try { const d = await r.json(); msg = d.error || msg; } catch {}
        throw new Error(msg);
    }
    return r.json();
}

// ─── Data loading ───────────────────────────────────────────────────────────

async function reloadAllData() {
    const [pr, rt, mo, ins] = await Promise.allSettled([
        api('/profiles'), api('/runtimes'), api('/models'), api('/instances')
    ]);
    profilesData = pr.status === 'fulfilled' ? pr.value : [];
    runtimesData = rt.status === 'fulfilled' ? rt.value : [];
    modelsData = mo.status === 'fulfilled' ? mo.value : [];
    instancesData = ins.status === 'fulfilled' ? ins.value : [];
    loadVersion();
}

async function loadVersion() {
    try {
        const d = await api('/version');
        document.getElementById('server-version').textContent = 'v' + (d.version || '?');
        document.getElementById('set-version').textContent = 'v' + (d.version || '?');
    } catch {}
}

// ─── Rendering ──────────────────────────────────────────────────────────────

function renderAll() {
    renderModelCards();
    renderAdvRuntimes();
    renderAdvModels();
    renderAdvProfiles();
    renderAdvInstances();
    renderAutostart();
    renderHistory();
    updateLogInstanceSelect();
}

function getRuntimeName(id) {
    const r = runtimesData.find(x => x.id === id);
    return r ? r.name : (id || '');
}

function getModelName(id) {
    const m = modelsData.find(x => x.id === id);
    return m ? m.name : (id || '');
}

function getActiveInstance(profileId) {
    return instancesData.filter(i => i.profile_id === profileId && isActive(i.state));
}

function isActive(s) { return s === 'running' || s === 'starting' || s === 'stopping'; }

function modelStatus(profile) {
    const active = getActiveInstance(profile.id);
    if (active.length === 0) return 'stopped';
    const states = active.map(i => i.state);
    if (states.includes('running')) return 'running';
    if (states.includes('starting')) return 'starting';
    if (states.includes('stopping')) return 'stopping';
    return 'stopped';
}

function fmtUptime(startedAt) {
    if (!startedAt) return '';
    const diff = Math.floor((Date.now() - new Date(startedAt).getTime()) / 1000);
    const h = Math.floor(diff / 3600);
    const m = Math.floor((diff % 3600) / 60);
    const s = diff % 60;
    return String(h).padStart(2, '0') + ':' + String(m).padStart(2, '0') + ':' + String(s).padStart(2, '0');
}

function fmtTime(ts) {
    if (!ts) return '—';
    return new Date(ts).toLocaleString();
}

function esc(s) {
    const d = document.createElement('div');
    d.textContent = s || '';
    return d.innerHTML;
}

function safeId(id) {
    return String(id).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

// ─── Model Cards ────────────────────────────────────────────────────────────

function renderModelCards() {
    const grid = document.getElementById('model-grid');
    const empty = document.getElementById('models-empty');
    if (profilesData.length === 0) {
        grid.innerHTML = '';
        empty.style.display = 'block';
        return;
    }
    empty.style.display = 'none';
    grid.innerHTML = profilesData.map(p => {
        const status = modelStatus(p);
        const active = getActiveInstance(p.id);
        const rt = runtimesData.find(r => r.id === p.runtime_id);
        const mo = modelsData.find(m => m.id === p.model_id);
        const inst = active[0];

        let tags = [];
        if (mo && mo.format) tags.push(mo.format.toUpperCase());
        if (mo && mo.path) {
            const base = mo.path.split(/[\\/]/).pop() || '';
            const q = base.match(/Q\d+[_A-Z0-9]*/);
            if (q) tags.push(q[0]);
        }
        if (mo && mo.mmproj) tags.push('Vision');

        const uptime = inst ? fmtUptime(inst.started_at) : '';

        let instanceSection = '';
        if (active.length > 1) {
            instanceSection = `<div class="instance-expand">
                <div style="font-size:0.8rem;color:var(--text-muted);margin-bottom:4px;">Экземпляры (${active.length})</div>` +
                active.map(i => `<div class="instance-row">
                    <span class="inst-info">PID: <span class="inst-pid">${i.pid || '—'}</span></span>
                    <button class="btn btn-ghost btn-sm" onclick="viewInstanceLogs('${safeId(i.id)}')">Логи</button>
                </div>`).join('') + '</div>';
        }

        let actionBtns = '';
        if (status === 'running') {
            actionBtns = `<button class="btn btn-warning btn-sm" onclick="restartModel('${safeId(p.id)}')">Перезапустить</button>
                <button class="btn btn-danger btn-sm" onclick="stopModel('${safeId(p.id)}')">Остановить</button>`;
        } else if (status === 'starting' || status === 'stopping') {
            actionBtns = `<span class="hint-text">В процессе...</span>`;
        } else {
            actionBtns = `<button class="btn btn-success btn-sm" onclick="startModel('${safeId(p.id)}')">Запустить</button>`;
        }

        return `<div class="model-card">
            <div class="model-card-header">
                <div>
                    <div class="model-card-title">${esc(p.name)}</div>
                    <div class="model-card-runtime">${esc(getRuntimeName(p.runtime_id))}</div>
                </div>
                <span class="status-badge ${status}"><span class="status-dot ${status === 'running' ? 'running' : ''}"></span>${status.toUpperCase()}</span>
            </div>
            <div class="model-card-info">
                <div class="info-row"><span class="label">Адрес:</span><span>${esc(p.host || '127.0.0.1')}:${p.port || '—'}</span></div>
                ${inst ? `<div class="info-row"><span class="label">PID:</span><span>${inst.pid || '—'}</span></div>` : ''}
                ${uptime ? `<div class="info-row"><span class="label">Uptime:</span><span>${uptime}</span></div>` : ''}
            </div>
            ${tags.length ? `<div class="model-tags">${tags.map(t => `<span class="model-tag">${esc(t)}</span>`).join('')}</div>` : ''}
            <div class="model-card-autostart"><span class="autostart-label">Автозапуск</span><label class="toggle-switch"><input type="checkbox" ${p.active ? 'checked' : ''} onchange="toggleAutostart('${safeId(p.id)}')"><span class="toggle-slider"></span></label></div>
            ${instanceSection}
            <div class="model-card-actions">
                ${actionBtns}
                <button class="btn btn-ghost btn-sm" onclick="viewInstanceLogs('${active.length ? safeId(active[0].id) : ''}')">Логи</button>
                <button class="btn btn-ghost btn-sm" onclick="openDetails('${safeId(p.id)}')">Настройки</button>
            </div>
        </div>`;
    }).join('');
}

// ─── Actions ────────────────────────────────────────────────────────────────

async function startModel(profileId) {
    const btns = document.querySelectorAll('.model-card-actions .btn-success');
    btns.forEach(b => b.disabled = true);
    try {
        await api('/profiles/' + profileId + '/start', { method: 'POST' });
        showToast('Запуск...', 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast('Ошибка: ' + e.message, 'error'); btns.forEach(b => b.disabled = false); }
}

async function stopModel(profileId) {
    const btns = document.querySelectorAll('.model-card-actions .btn-danger');
    btns.forEach(b => b.disabled = true);
    try {
        await api('/profiles/' + profileId + '/stop', { method: 'POST' });
        showToast('Остановка...', 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast('Ошибка: ' + e.message, 'error'); btns.forEach(b => b.disabled = false); }
}

async function restartModel(profileId) {
    const btns = document.querySelectorAll('.model-card-actions .btn-warning');
    btns.forEach(b => b.disabled = true);
    try {
        await api('/profiles/' + profileId + '/restart', { method: 'POST' });
        showToast('Перезапуск...', 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast('Ошибка: ' + e.message, 'error'); btns.forEach(b => b.disabled = false); }
}

// ─── Logs ───────────────────────────────────────────────────────────────────

function updateLogInstanceSelect() {
    const sel = document.getElementById('log-instance-select');
    const cur = sel.value;
    const activeInsts = instancesData.filter(i => isActive(i.state));
    const logsEmpty = document.getElementById('logs-empty');
    if (logsEmpty) logsEmpty.style.display = activeInsts.length === 0 ? 'block' : 'none';
    sel.innerHTML = '<option value="">Все экземпляры</option>' +
        activeInsts.map(i =>
            `<option value="${esc(i.id)}">${esc(getProfileName(i.profile_id))} | ${i.id.slice(0,12)}… | PID ${i.pid || '?'} | ${i.state}</option>`
        ).join('');
    sel.value = cur;
}

function getProfileName(id) {
    const p = profilesData.find(x => x.id === id);
    return p ? p.name : id;
}

function switchLogInstance() {
    const instId = document.getElementById('log-instance-select').value;
    updateLogInstanceBar(instId);
    connectLogStream(instId);
}

function updateLogInstanceBar(instId) {
    const bar = document.getElementById('log-instance-bar');
    if (!instId) { bar.style.display = 'none'; return; }
    const inst = instancesData.find(i => i.id === instId);
    if (!inst) { bar.style.display = 'none'; return; }
    const started = inst.started_at ? new Date(inst.started_at).toLocaleString() : '—';
    bar.innerHTML = `<span class="log-bar-label">Профиль:</span> ${esc(getProfileName(inst.profile_id))} &nbsp;|&nbsp; <span class="log-bar-label">Instance:</span> <code>${esc(inst.id)}</code> &nbsp;|&nbsp; <span class="log-bar-label">PID:</span> ${inst.pid || '—'} &nbsp;|&nbsp; <span class="log-bar-label">State:</span> <span class="status-badge ${inst.state}">${inst.state}</span> &nbsp;|&nbsp; <span class="log-bar-label">Старт:</span> ${started}`;
    bar.style.display = 'flex';
}

function connectLogStream(instanceId) {
    if (logEs) { logEs.close(); logEs = null; }
    const url = instanceId
        ? '/api/v1/instances/' + encodeURIComponent(instanceId) + '/logs/stream'
        : '/api/v1/logs/stream';
    logEs = new EventSource(url);
    logEs.onmessage = function (e) {
        if (logPaused) return;
        try {
            const d = JSON.parse(e.data);
            appendLogLine(d);
        } catch {}
    };
    logEs.onerror = function () { /* auto-reconnect */ };
}

function appendLogLine(d) {
    const stream = d.stream || 'stdout';
    const search = document.getElementById('log-search').value.toLowerCase();
    const filter = document.getElementById('log-stream-filter').value;
    if (filter && stream !== filter) return;
    if (search && !d.message.toLowerCase().includes(search)) return;

    const view = document.getElementById('log-view');
    const div = document.createElement('div');
    div.className = 'log-line ' + stream;
    const t = d.time ? new Date(d.time).toLocaleTimeString() : '';
    div.innerHTML = `<span class="log-time">${t}</span><span class="log-source">[${stream}]</span>${esc(d.message)}`;
    view.appendChild(div);

    while (view.children.length > 2000) view.removeChild(view.firstChild);
    if (document.getElementById('log-autoscroll').checked) {
        view.scrollTop = view.scrollHeight;
    }
}

function applyLogSearch() { /* search applied on new lines */ }

function toggleLogPause() {
    logPaused = !logPaused;
    document.getElementById('log-pause-btn').textContent = logPaused ? 'Продолжить' : 'Пауза';
}

function clearLogView() {
    document.getElementById('log-view').innerHTML = '';
}

function viewInstanceLogs(instanceId) {
    navigate('logs');
    const sel = document.getElementById('log-instance-select');
    if (instanceId) {
        sel.value = instanceId;
        updateLogInstanceBar(instanceId);
    } else {
        updateLogInstanceBar('');
    }
    connectLogStream(instanceId);
}

// ─── Navigation ─────────────────────────────────────────────────────────────

function navigate(view) {
    currentView = view;
    document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
    const target = document.getElementById('view-' + view);
    if (target) target.classList.add('active');
    document.querySelectorAll('.nav-item').forEach(n => n.classList.remove('active'));
    const navBtn = document.querySelector(`.nav-item[data-view="${view}"]`);
    if (navBtn) navBtn.classList.add('active');

    if (view === 'logs' && !logEs) connectLogStream('');
    if (view === 'adv-settings') loadSettings();
    if (view !== 'logs' && logEs && currentView !== 'logs') { /* keep SSE alive */ }
}

// ─── Details Modal ──────────────────────────────────────────────────────────

function openDetails(profileId) {
    const p = profilesData.find(x => x.id === profileId);
    if (!p) return;
    const rt = runtimesData.find(r => r.id === p.runtime_id);
    const mo = modelsData.find(m => m.id === p.model_id);
    const active = getActiveInstance(profileId);

    document.getElementById('details-title').textContent = p.name;

    // Overview tab
    document.getElementById('detail-overview').innerHTML = `
        <div class="detail-row"><span class="dl">Runtime</span><span class="dv">${esc(rt ? rt.name : '—')}</span></div>
        <div class="detail-row"><span class="dl">Executable</span><span class="dv">${esc(rt ? rt.executable : '—')}</span></div>
        <div class="detail-row"><span class="dl">Working Dir</span><span class="dv">${esc(rt ? (rt.working_directory || '—') : '—')}</span></div>
        <div class="detail-row"><span class="dl">Model</span><span class="dv">${esc(mo ? mo.name : '—')}</span></div>
        <div class="detail-row"><span class="dl">GGUF</span><span class="dv">${esc(mo ? (mo.path || '—') : '—')}</span></div>
        <div class="detail-row"><span class="dl">MMProj</span><span class="dv">${esc(mo ? (mo.mmproj || '—') : '—')}</span></div>
        <div class="detail-row"><span class="dl">Host:Port</span><span class="dv">${esc(p.host || '127.0.0.1')}:${p.port || '—'}</span></div>
        <div class="detail-row"><span class="dl">Автозапуск</span><span class="dv">${p.active ? 'ON' : 'OFF'}${p.autostart_delay ? ' (' + p.autostart_delay + 's)' : ''}</span></div>`;

    // Launch tab
    let launchHtml = '<div class="detail-row"><span class="dl">Runtime defaults</span><span class="dv">' + esc(rt ? (rt.default_args || []).join(' ') : '—') + '</span></div>';
    launchHtml += '<div class="detail-row"><span class="dl">Model args</span><span class="dv">' + esc(mo ? (mo.arguments || []).join(' ') : '—') + '</span></div>';
    launchHtml += '<div class="detail-row"><span class="dl">Profile args</span><span class="dv">' + esc((p.args || []).join(' ') || '—') + '</span></div>';
    launchHtml += '<div class="detail-row"><span class="dl">Host/Port</span><span class="dv">--host ' + esc(p.host || '127.0.0.1') + ' --port ' + (p.port || '') + '</span></div>';
    if (p.environment_keys && p.environment_keys.length) {
        launchHtml += '<div class="detail-row"><span class="dl">Env keys</span><span class="dv">' + p.environment_keys.map(esc).join(', ') + '</span></div>';
    }
    document.getElementById('detail-launch').innerHTML = launchHtml;

    // Instances tab
    const allInst = instancesData.filter(i => i.profile_id === profileId);
    document.getElementById('detail-instances').innerHTML = allInst.length ?
        `<table class="data-table"><thead><tr><th>ID</th><th>State</th><th>PID</th><th>Started</th><th></th></tr></thead><tbody>` +
        allInst.map(i => `<tr><td style="font-family:var(--font-mono);font-size:0.78rem;">${esc(i.id.slice(0, 12))}</td>
            <td><span class="status-badge ${i.state}">${i.state}</span></td>
            <td>${i.pid || '—'}</td><td>${fmtTime(i.started_at)}</td>
            <td><button class="btn btn-ghost btn-sm" onclick="closeDetails();viewInstanceLogs('${safeId(i.id)}')">Логи</button></td></tr>`).join('') +
        '</tbody></table>' :
        '<p class="hint-text">Нет экземпляров.</p>';

    document.getElementById('details-modal').style.display = 'flex';
}

function closeDetails() { document.getElementById('details-modal').style.display = 'none'; }

function switchDetailTab(tab) {
    document.querySelectorAll('.details-tabs .tab').forEach(t => t.classList.toggle('active', t.dataset.tab === tab));
    document.querySelectorAll('.tab-panel').forEach(p => p.classList.toggle('active', p.id === 'detail-' + tab));
}

// ─── Wizard ─────────────────────────────────────────────────────────────────

let wizStep = 1;

function openWizard() {
    wizStep = 1;
    document.getElementById('wizard-form').reset();
    document.getElementById('wizard-error').style.display = 'none';
    document.getElementById('wiz-env-container').innerHTML = '';
    document.querySelector('input[name=wiz-rt-mode][value=existing]').checked = true;
    onWizRtModeChange();
    loadWizardRuntimeSelect();
    updateWizardStep();
    document.getElementById('wizard-modal').style.display = 'flex';
}

function closeWizard() { document.getElementById('wizard-modal').style.display = 'none'; }

function loadWizardRuntimeSelect() {
    const sel = document.getElementById('wiz-runtime-existing');
    sel.innerHTML = '<option value="">— выбрать —</option>' +
        runtimesData.map(r => `<option value="${esc(r.id)}">${esc(r.name)} (${esc(r.executable)})</option>`).join('');
}

function onWizRtModeChange() {
    const mode = document.querySelector('input[name=wiz-rt-mode]:checked').value;
    document.getElementById('wiz-rt-existing-section').style.display = mode === 'existing' ? '' : 'none';
    document.getElementById('wiz-rt-new-section').style.display = mode === 'new' ? '' : 'none';
}

function wizPrev() { if (wizStep > 1) { wizStep--; updateWizardStep(); } }

function updateWizardStep() {
    document.querySelectorAll('.wizard-step').forEach(s => {
        const n = parseInt(s.dataset.step);
        s.classList.toggle('active', n === wizStep);
        s.classList.toggle('done', n < wizStep);
    });
    document.querySelectorAll('.wizard-panel').forEach(p => p.classList.toggle('active', parseInt(p.dataset.panel) === wizStep));
    document.getElementById('wiz-prev').style.display = wizStep > 1 ? '' : 'none';
    document.getElementById('wiz-next').textContent = wizStep === 4 ? 'Создать' : 'Далее';
}

document.getElementById('wizard-form').addEventListener('submit', async function (e) {
    e.preventDefault();
    if (wizStep < 4) { wizStep++; updateWizardStep(); return; }

    const name = document.getElementById('wiz-name').value.trim();
    if (!name) { wizError('Укажите название'); return; }

    const rtMode = document.querySelector('input[name=wiz-rt-mode]:checked').value;
    let runtimeId = rtMode === 'existing' ? document.getElementById('wiz-runtime-existing').value : '';

    const submitBtn = document.getElementById('wiz-next');
    if (submitBtn.disabled) return;
    submitBtn.disabled = true;
    submitBtn.textContent = 'Создание...';

    try {
        // Create runtime if needed
        if (rtMode === 'existing' && !runtimeId) {
            wizError('Выберите Runtime');
            submitBtn.disabled = false;
            submitBtn.textContent = 'Создать';
            return;
        }
        if (rtMode === 'new' || !runtimeId) {
            const rtName = document.getElementById('wiz-rt-name').value.trim();
            const rtExe = document.getElementById('wiz-rt-executable').value.trim();
            if (!rtName || !rtExe) { wizError('Заполните Runtime поля'); return; }
            const rtBody = { name: rtName, executable: rtExe };
            const rtWd = document.getElementById('wiz-rt-workdir').value.trim();
            if (rtWd) rtBody.working_directory = rtWd;
            const rt = await api('/runtimes', { method: 'POST', body: JSON.stringify(rtBody) });
            runtimeId = rt.id;
        }

        // Create model
        const modelPath = document.getElementById('wiz-model-path').value.trim();
        const modelMmproj = document.getElementById('wiz-model-mmproj').value.trim();
        const modelArgsRaw = document.getElementById('wiz-model-args').value.trim();
        const modelArgs = modelArgsRaw ? modelArgsRaw.split('\n').map(s => s.trim()).filter(Boolean) : [];
        const modelBody = { name: name, runtime_id: runtimeId };
        if (modelPath) modelBody.path = modelPath;
        if (modelMmproj) modelBody.mmproj = modelMmproj;
        if (modelArgs.length) modelBody.arguments = modelArgs;
        if (!modelPath && !modelArgs.length) { wizError('Укажите GGUF путь или аргументы модели'); return; }
        const model = await api('/models', { method: 'POST', body: JSON.stringify(modelBody) });

        // Create profile
        const host = document.getElementById('wiz-host').value.trim() || '127.0.0.1';
        const port = parseInt(document.getElementById('wiz-port').value) || 8085;
        const profArgsRaw = document.getElementById('wiz-profile-args').value.trim();
        const profArgs = profArgsRaw ? profArgsRaw.split(/\s+/).filter(Boolean) : [];
        const profBody = {
            name: name, runtime_id: runtimeId, model_id: model.id,
            host: host, port: port, active: document.getElementById('wiz-autostart').checked
        };
        if (profArgs.length) profBody.args = profArgs;
        if (document.getElementById('wiz-autostart').checked) {
            const delay = parseInt(document.getElementById('wiz-autostart-delay').value) || 0;
            if (delay > 0) profBody.autostart_delay = delay;
        }
        // Env
        const env = collectWizEnv();
        if (Object.keys(env).length) profBody.environment = env;

        await api('/profiles', { method: 'POST', body: JSON.stringify(profBody) });
        showToast('Модель "' + name + '" создана', 'success');
        submitBtn.disabled = false;
        submitBtn.textContent = 'Создать';
        closeWizard();
        await reloadAllData(); renderAll();
    } catch (e2) {
        submitBtn.disabled = false;
        submitBtn.textContent = 'Создать';
        wizError(e2.message);
    }
});

function wizError(msg) {
    const el = document.getElementById('wizard-error');
    el.textContent = msg;
    el.style.display = 'block';
}

function addWizEnvRow() {
    const div = document.createElement('div');
    div.className = 'env-row';
    div.style.display = 'flex';
    div.style.gap = '6px';
    div.style.marginBottom = '6px';
    div.innerHTML = `<input type="text" placeholder="KEY" style="flex:1;padding:6px;border:1px solid var(--border);border-radius:4px;background:var(--bg-input);color:var(--text-primary);">
        <input type="text" placeholder="value" style="flex:2;padding:6px;border:1px solid var(--border);border-radius:4px;background:var(--bg-input);color:var(--text-primary);">
        <button type="button" class="btn btn-danger btn-sm" onclick="this.parentElement.remove()">&times;</button>`;
    document.getElementById('wiz-env-container').appendChild(div);
}

function collectWizEnv() {
    const env = {};
    document.querySelectorAll('#wiz-env-container .env-row').forEach(row => {
        const inputs = row.querySelectorAll('input');
        if (inputs[0].value.trim()) env[inputs[0].value.trim()] = inputs[1].value;
    });
    return env;
}

// ─── Advanced: Runtimes ─────────────────────────────────────────────────────

function renderAdvRuntimes() {
    const empty = document.getElementById('runtimes-empty');
    if (runtimesData.length === 0) {
        document.getElementById('adv-runtimes-body').innerHTML = '';
        if (empty) empty.style.display = 'block';
        return;
    }
    if (empty) empty.style.display = 'none';
    document.getElementById('adv-runtimes-body').innerHTML = runtimesData.map(r =>
        `<tr><td>${esc(r.name)}</td><td>${esc(r.executable)}</td><td>${esc(r.working_directory || '—')}</td>
        <td style="max-width:200px;overflow:hidden;text-overflow:ellipsis;"><code>${esc((r.default_args || []).join(' '))}</code></td>
        <td class="actions-cell"><button class="btn btn-ghost btn-sm" onclick="editRuntime('${safeId(r.id)}')">Изменить</button>
        <button class="btn btn-danger btn-sm" onclick="deleteRuntime('${safeId(r.id)}')">Удалить</button></td></tr>`
    ).join('');
}

function showCreateRuntimeModal() { document.getElementById('create-runtime-modal').style.display = 'flex'; }

async function handleCreateRuntime(e) {
    e.preventDefault();
    const f = e.target;
    const body = { name: f.name.value, executable: f.executable.value };
    if (f.working_directory.value) body.working_directory = f.working_directory.value;
    const args = f.default_args.value.trim();
    if (args) body.default_args = args.split(/\s+/);
    try {
        await api('/runtimes', { method: 'POST', body: JSON.stringify(body) });
        closeModal('create-runtime-modal'); showToast('Runtime создан', 'success');
        await reloadAllData(); renderAll();
    } catch (err) { showToast(err.message, 'error'); }
    return false;
}

function editRuntime(id) {
    const r = runtimesData.find(x => x.id === id);
    if (!r) return;
    const modal = document.getElementById('edit-runtime-modal');
    const f = modal.querySelector('form');
    f.querySelector('[name=id]').value = r.id;
    f.querySelector('[name=name]').value = r.name;
    f.querySelector('[name=executable]').value = r.executable;
    f.querySelector('[name=working_directory]').value = r.working_directory || '';
    f.querySelector('[name=default_args]').value = (r.default_args || []).join(' ');
    modal.style.display = 'flex';
}

async function handleEditRuntime(e) {
    e.preventDefault();
    const f = e.target;
    const body = { name: f.name.value, executable: f.executable.value };
    if (f.working_directory.value) body.working_directory = f.working_directory.value;
    const args = f.default_args.value.trim();
    body.default_args = args ? args.split(/\s+/) : [];
    try {
        await api('/runtimes/' + f.querySelector('[name=id]').value, { method: 'PUT', body: JSON.stringify(body) });
        closeModal('edit-runtime-modal'); showToast('Сохранено', 'success');
        await reloadAllData(); renderAll();
    } catch (err) { showToast(err.message, 'error'); }
    return false;
}

function deleteRuntime(id) {
    const rt = runtimesData.find(x => x.id === id);
    const name = rt ? rt.name : id;
    const depModels = modelsData.filter(m => m.runtime_id === id);
    const depProfiles = profilesData.filter(p => p.runtime_id === id);
    let msg = `Удалить runtime "${name}"?`;
    if (depModels.length > 0) msg += `\nЗависимые модели: ${depModels.map(m => m.name).join(', ')}`;
    if (depProfiles.length > 0) msg += `\nЗависимые профили: ${depProfiles.map(p => p.name).join(', ')}`;
    if (depModels.length > 0 || depProfiles.length > 0) msg += '\nОни станут недоступны для запуска.';
    showConfirm(msg, async function () {
        try { await api('/runtimes/' + id, { method: 'DELETE' }); await reloadAllData(); renderAll(); }
        catch (err) { showToast(friendlyError(err), 'error'); }
    });
}

// ─── Advanced: Models ───────────────────────────────────────────────────────

function renderAdvModels() {
    const empty = document.getElementById('models-adv-empty');
    if (modelsData.length === 0) {
        document.getElementById('adv-models-body').innerHTML = '';
        if (empty) empty.style.display = 'block';
        return;
    }
    if (empty) empty.style.display = 'none';
    document.getElementById('adv-models-body').innerHTML = modelsData.map(m => {
        const fmt = m.format || deriveModelFormat(m);
        return `<tr><td>${esc(m.name)}</td><td>${esc(getRuntimeName(m.runtime_id))}</td><td>${esc(m.path || '—')}</td>
        <td>${esc(m.mmproj || '—')}</td><td>${esc(fmt)}</td>
        <td class="actions-cell"><button class="btn btn-ghost btn-sm" onclick="editModel('${safeId(m.id)}')">Изменить</button>
        <button class="btn btn-danger btn-sm" onclick="deleteModel('${safeId(m.id)}')">Удалить</button></td></tr>`;
    }).join('');
}

function deriveModelFormat(m) {
    const hasPath = !!(m.path && m.path.toLowerCase().endsWith('.gguf'));
    const hasArgs = (m.arguments && m.arguments.length > 0);
    if (hasPath && hasArgs) return 'GGUF+Args';
    if (hasPath) return 'GGUF';
    if (hasArgs) return 'Args';
    return '—';
}

function showCreateModelModal() {
    const sel = document.querySelector('#create-model-modal select[name=runtime_id]');
    sel.innerHTML = '<option value="">—</option>' + runtimesData.map(r => `<option value="${esc(r.id)}">${esc(r.name)}</option>`).join('');
    document.getElementById('create-model-modal').style.display = 'flex';
}

async function handleCreateModel(e) {
    e.preventDefault();
    const f = e.target;
    const kind = f.kind.value;
    const body = { name: f.name.value, runtime_id: f.runtime_id.value };
    if (kind === 'path') body.path = f.path.value;
    else body.arguments = f.arguments.value.trim().split(/\s+/).filter(Boolean);
    try {
        await api('/models', { method: 'POST', body: JSON.stringify(body) });
        closeModal('create-model-modal'); showToast('Модель создана', 'success');
        await reloadAllData(); renderAll();
    } catch (err) { showToast(err.message, 'error'); }
    return false;
}

function editModel(id) {
    const m = modelsData.find(x => x.id === id);
    if (!m) return;
    const modal = document.getElementById('edit-model-modal');
    const f = modal.querySelector('form');
    const rtSel = f.querySelector('[name=runtime_id]');
    rtSel.innerHTML = runtimesData.map(r => `<option value="${esc(r.id)}" ${r.id === m.runtime_id ? 'selected' : ''}>${esc(r.name)}</option>`).join('');
    f.querySelector('[name=id]').value = m.id;
    f.querySelector('[name=name]').value = m.name;
    const hasPath = !!m.path;
    f.querySelector('[name=kind]').value = hasPath ? 'path' : 'arguments';
    toggleModelKind(f.querySelector('[name=kind]'));
    f.querySelector('[name=path]').value = m.path || '';
    f.querySelector('[name=arguments]').value = (m.arguments || []).join(' ');
    modal.style.display = 'flex';
}

function toggleModelKind(sel) {
    const form = sel.closest('form');
    form.querySelector('[data-kind=path]').style.display = sel.value === 'path' ? '' : 'none';
    form.querySelector('[data-kind=arguments]').style.display = sel.value === 'arguments' ? '' : 'none';
}

async function handleEditModel(e) {
    e.preventDefault();
    const f = e.target;
    const id = f.querySelector('[name=id]').value;
    const body = { name: f.name.value, runtime_id: f.runtime_id.value };
    if (f.kind.value === 'path') body.path = f.path.value;
    else body.arguments = f.arguments.value.trim().split(/\s+/).filter(Boolean);
    try {
        await api('/models/' + id, { method: 'PUT', body: JSON.stringify(body) });
        closeModal('edit-model-modal'); showToast('Сохранено', 'success');
        await reloadAllData(); renderAll();
    } catch (err) { showToast(err.message, 'error'); }
    return false;
}

function deleteModel(id) {
    const m = modelsData.find(x => x.id === id);
    const name = m ? m.name : id;
    const depProfiles = profilesData.filter(p => p.model_id === id);
    let msg = `Удалить модель "${name}"?`;
    if (depProfiles.length > 0) msg += `\nИспользуется в профилях: ${depProfiles.map(p => p.name).join(', ')}\nЭти профили не смогут запускаться.`;
    showConfirm(msg, async function () {
        try { await api('/models/' + id, { method: 'DELETE' }); await reloadAllData(); renderAll(); }
        catch (err) { showToast(friendlyError(err), 'error'); }
    });
}

// ─── Advanced: Profiles ─────────────────────────────────────────────────────

function renderAdvProfiles() {
    const empty = document.getElementById('profiles-adv-empty');
    if (profilesData.length === 0) {
        document.getElementById('adv-profiles-body').innerHTML = '';
        if (empty) empty.style.display = 'block';
        return;
    }
    if (empty) empty.style.display = 'none';
    document.getElementById('adv-profiles-body').innerHTML = profilesData.map(p =>
        `<tr><td>${esc(p.name)}</td><td>${esc(getRuntimeName(p.runtime_id))}</td>
        <td>${esc(getModelName(p.model_id))}</td><td>${esc(p.host)}:${p.port}</td>
        <td style="max-width:150px;overflow:hidden;text-overflow:ellipsis;"><code>${esc((p.args || []).join(' '))}</code></td>
        <td class="actions-cell"><button class="btn btn-ghost btn-sm" onclick="editProfile('${safeId(p.id)}')">Изменить</button>
        <button class="btn btn-danger btn-sm" onclick="deleteProfile('${safeId(p.id)}')">Удалить</button></td></tr>`
    ).join('');
}

function showCreateProfileModal() {
    const rtSel = document.querySelector('#create-profile-modal select[name=runtime_id]');
    rtSel.innerHTML = '<option value="">—</option>' + runtimesData.map(r => `<option value="${esc(r.id)}">${esc(r.name)}</option>`).join('');
    const moSel = document.querySelector('#create-profile-modal select[name=model_id]');
    moSel.innerHTML = '<option value="">—</option>' + modelsData.map(m => `<option value="${esc(m.id)}">${esc(m.name)}</option>`).join('');
    document.getElementById('create-profile-modal').style.display = 'flex';
}

async function handleCreateProfile(e) {
    e.preventDefault();
    const f = e.target;
    const body = {
        name: f.name.value, runtime_id: f.runtime_id.value,
        host: f.host.value || '127.0.0.1', port: parseInt(f.port.value) || 8080
    };
    if (f.model_id.value) body.model_id = f.model_id.value;
    const args = f.args.value.trim();
    if (args) body.args = args.split(/\s+/);
    try {
        await api('/profiles', { method: 'POST', body: JSON.stringify(body) });
        closeModal('create-profile-modal'); showToast('Профиль создан', 'success');
        await reloadAllData(); renderAll();
    } catch (err) { showToast(err.message, 'error'); }
    return false;
}

function editProfile(id) {
    const p = profilesData.find(x => x.id === id);
    if (!p) return;
    const modal = document.getElementById('edit-profile-modal');
    const f = modal.querySelector('form');
    const rtSel = f.querySelector('[name=runtime_id]');
    rtSel.innerHTML = runtimesData.map(r => `<option value="${esc(r.id)}" ${r.id === p.runtime_id ? 'selected' : ''}>${esc(r.name)}</option>`).join('');
    const moSel = f.querySelector('[name=model_id]');
    moSel.innerHTML = '<option value="">—</option>' + modelsData.map(m => `<option value="${esc(m.id)}" ${m.id === p.model_id ? 'selected' : ''}>${esc(m.name)}</option>`).join('');
    f.querySelector('[name=id]').value = p.id;
    f.querySelector('[name=name]').value = p.name;
    f.querySelector('[name=host]').value = p.host || '';
    f.querySelector('[name=port]').value = p.port || '';
    f.querySelector('[name=args]').value = (p.args || []).join(' ');
    modal.style.display = 'flex';
}

async function handleEditProfile(e) {
    e.preventDefault();
    const f = e.target;
    const id = f.querySelector('[name=id]').value;
    const body = {
        name: f.name.value, runtime_id: f.runtime_id.value,
        host: f.host.value || '127.0.0.1', port: parseInt(f.port.value) || 8080
    };
    if (f.model_id.value) body.model_id = f.model_id.value;
    const args = f.args.value.trim();
    body.args = args ? args.split(/\s+/) : [];
    try {
        await api('/profiles/' + id, { method: 'PUT', body: JSON.stringify(body) });
        closeModal('edit-profile-modal'); showToast('Сохранено', 'success');
        await reloadAllData(); renderAll();
    } catch (err) { showToast(err.message, 'error'); }
    return false;
}

function deleteProfile(id) {
    const p = profilesData.find(x => x.id === id);
    const name = p ? p.name : id;
    const activeInsts = getActiveInstance(id);
    let msg = `Удалить профиль "${name}"?`;
    if (activeInsts.length > 0) msg += `\nАктивные экземпляры (${activeInsts.length}) будут остановлены.`;
    showConfirm(msg, async function () {
        try { await api('/profiles/' + id, { method: 'DELETE' }); await reloadAllData(); renderAll(); }
        catch (err) { showToast(friendlyError(err), 'error'); }
    });
}

function friendlyError(err) {
    let msg = err.message || 'Неизвестная ошибка';
    const patterns = [
        [/profile not found/i, 'Профиль не найден. Возможно, он уже удалён.'],
        [/model not found/i, 'Модель не найдена. Возможно, она уже удалена.'],
        [/runtime not found/i, 'Runtime не найден. Возможно, он уже удалён.'],
        [/instance not found/i, 'Экземпляр не найден.'],
        [/in use|referenced|depend/i, 'Объект используется другими записями и не может быть удалён.'],
        [/port.*in use|address already in use/i, 'Порт уже занят другим процессом.'],
        [/executable.*not found|no such file/i, 'Исполняемый файл не найден. Проверьте путь.'],
    ];
    for (const [re, replacement] of patterns) {
        if (re.test(msg)) return replacement;
    }
    if (msg.includes(': ')) {
        const parts = msg.split(': ');
        const unique = [...new Set(parts)];
        msg = unique.length > 1 ? unique[unique.length - 1] : msg;
    }
    return msg.charAt(0).toUpperCase() + msg.slice(1);
}

// ─── Advanced: Instances ────────────────────────────────────────────────────

function renderAdvInstances() {
    const empty = document.getElementById('instances-empty');
    if (instancesData.length === 0) {
        document.getElementById('adv-instances-body').innerHTML = '';
        if (empty) empty.style.display = 'block';
        return;
    }
    if (empty) empty.style.display = 'none';
    document.getElementById('adv-instances-body').innerHTML = instancesData.map(i =>
        `<tr><td style="font-family:var(--font-mono);font-size:0.78rem;">${esc(i.id.slice(0, 16))}</td>
        <td>${esc(getProfileName(i.profile_id))}</td>
        <td><span class="status-badge ${i.state}">${i.state}</span></td>
        <td>${i.pid || '—'}</td><td>${fmtTime(i.started_at)}</td><td>${fmtTime(i.stopped_at)}</td>
        <td>${i.exit_code != null ? i.exit_code : '—'}</td>
        <td class="actions-cell"><button class="btn btn-ghost btn-sm" onclick="viewInstanceLogs('${safeId(i.id)}')">Логи</button>
        ${isActive(i.state) ? `<button class="btn btn-danger btn-sm" onclick="stopInstance('${safeId(i.id)}')">Остановить</button>
        <button class="btn btn-warning btn-sm" onclick="restartInstance('${safeId(i.id)}')">Перезапустить</button>` : ''}</td></tr>`
    ).join('');
}

async function stopInstance(id) {
    try { await api('/instances/' + id + '/stop', { method: 'POST' }); await reloadAllData(); renderAll(); }
    catch (err) { showToast(err.message, 'error'); }
}

async function restartInstance(id) {
    try { await api('/instances/' + id + '/restart', { method: 'POST' }); await reloadAllData(); renderAll(); }
    catch (err) { showToast(err.message, 'error'); }
}

// ─── Autostart ──────────────────────────────────────────────────────────────

function renderAutostart() {
    const active = profilesData.filter(p => p.active);
    document.getElementById('autostart-body').innerHTML = active.length ? active.map(p =>
        `<tr><td>${esc(p.name)}</td><td>${esc(p.host || '127.0.0.1')}:${p.port}</td>
        <td>${p.autostart_delay ? p.autostart_delay + 'с' : '0с'}</td>
        <td><label class="toggle-switch"><input type="checkbox" checked onchange="toggleAutostart('${safeId(p.id)}')"><span class="toggle-slider"></span></label></td>
        <td class="actions-cell"><button class="btn btn-ghost btn-sm" onclick="openDetails('${safeId(p.id)}')">Настройки</button></td></tr>`
    ).join('') : '<tr><td colspan="5" class="hint-text">Нет моделей с автозапуском.</td></tr>';
}

async function toggleAutostart(id) {
    const p = profilesData.find(x => x.id === id);
    if (!p) return;
    try {
        if (p.active) {
            await api('/profiles/' + id + '/deactivate', { method: 'POST' });
        } else {
            await api('/profiles/' + id + '/activate', { method: 'POST' });
        }
        await reloadAllData(); renderAll();
    } catch (err) { showToast(err.message, 'error'); }
}

// ─── History ────────────────────────────────────────────────────────────────

function renderHistory() {
    const empty = document.getElementById('history-empty');
    const terminal = instancesData.filter(i => !isActive(i.state));
    if (terminal.length === 0) {
        document.getElementById('history-body').innerHTML = '';
        if (empty) empty.style.display = 'block';
        return;
    }
    if (empty) empty.style.display = 'none';
    document.getElementById('history-body').innerHTML = terminal.slice(0, 50).map(i =>
        `<tr><td>${esc(getProfileName(i.profile_id))}</td>
        <td><span class="status-badge ${i.state}">${i.state}</span></td>
        <td>${fmtTime(i.started_at)}</td><td>${fmtTime(i.stopped_at)}</td>
        <td>${i.exit_code != null ? i.exit_code : '—'}</td>
        <td><button class="btn btn-ghost btn-sm" onclick="viewInstanceLogs('${safeId(i.id)}')">Логи</button></td></tr>`
    ).join('');
}

// ─── Settings ───────────────────────────────────────────────────────────────

async function loadSettings() {
    try {
        const m = await api('/metrics');
        document.getElementById('set-listen').textContent = (m.listen_address || '127.0.0.1') + ':' + (m.web_port || '');
        document.getElementById('set-auth').textContent = m.auth_enabled ? 'Включена' : 'Выключена';
        const userCard = document.getElementById('set-user-card');
        if (userCard) userCard.style.display = m.auth_enabled ? '' : 'none';
    } catch {}
}

// ─── Modal helpers ──────────────────────────────────────────────────────────

function closeModal(id) { document.getElementById(id).style.display = 'none'; }

function showConfirm(msg, onYes) {
    document.getElementById('confirm-message').textContent = msg;
    const btn = document.getElementById('confirm-yes');
    btn.disabled = false;
    btn.textContent = 'Подтвердить';
    btn.onclick = async function () {
        btn.disabled = true;
        btn.textContent = 'Выполняется...';
        await onYes();
    };
    document.getElementById('confirm-modal').style.display = 'flex';
}

function closeConfirm() { document.getElementById('confirm-modal').style.display = 'none'; }

// ─── Toast ──────────────────────────────────────────────────────────────────

function showToast(msg, type) {
    const div = document.createElement('div');
    div.className = 'toast ' + (type || '');
    div.textContent = msg;
    document.getElementById('toast-container').appendChild(div);
    setTimeout(() => div.remove(), 4000);
}

// ─── Refresh loop ───────────────────────────────────────────────────────────

function startRefresh() {
    if (refreshTimer) clearInterval(refreshTimer);
    refreshTimer = setInterval(async () => {
        await reloadAllData();
        renderAll();
    }, 5000);
}

// ─── Autostart delay checkbox ───────────────────────────────────────────────

document.getElementById('wiz-autostart').addEventListener('change', function () {
    document.getElementById('wiz-delay-group').style.display = this.checked ? '' : 'none';
});

// ─── Expose to global scope for inline onclick handlers ────────────────────

window.handleLogin = handleLogin;
window.handleLogout = handleLogout;
window.navigate = navigate;
window.openWizard = openWizard;
window.closeWizard = closeWizard;
window.wizPrev = wizPrev;
window.closeDetails = closeDetails;
window.switchDetailTab = switchDetailTab;
window.openDetails = openDetails;
window.viewInstanceLogs = viewInstanceLogs;
window.startModel = startModel;
window.stopModel = stopModel;
window.restartModel = restartModel;
window.stopInstance = stopInstance;
window.restartInstance = restartInstance;
window.showCreateRuntimeModal = showCreateRuntimeModal;
window.handleCreateRuntime = handleCreateRuntime;
window.editRuntime = editRuntime;
window.handleEditRuntime = handleEditRuntime;
window.deleteRuntime = deleteRuntime;
window.showCreateModelModal = showCreateModelModal;
window.handleCreateModel = handleCreateModel;
window.editModel = editModel;
window.handleEditModel = handleEditModel;
window.deleteModel = deleteModel;
window.showCreateProfileModal = showCreateProfileModal;
window.handleCreateProfile = handleCreateProfile;
window.editProfile = editProfile;
window.handleEditProfile = handleEditProfile;
window.deleteProfile = deleteProfile;
window.toggleAutostart = toggleAutostart;
window.switchLogInstance = switchLogInstance;
window.applyLogSearch = applyLogSearch;
window.toggleLogPause = toggleLogPause;
window.clearLogView = clearLogView;
window.closeModal = closeModal;
window.closeConfirm = closeConfirm;
window.addWizEnvRow = addWizEnvRow;
window.toggleModelKind = toggleModelKind;
window.onWizRtModeChange = onWizRtModeChange;

// ─── Boot ───────────────────────────────────────────────────────────────────

init();
})();

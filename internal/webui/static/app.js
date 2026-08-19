(function () {
'use strict';

let csrfToken = '';
let currentView = 'models';
let logEs = null;
let logPaused = false;
let refreshTimer = null;

// Data cache
let runtimesData = [];
let modelsData = [];
let instancesData = [];

// Wizard state
let wizStep = 1;
let wizEditId = null;

// ─── Layout adaptation ──────────────────────────────────────────────────────
// The legacy page template still ships 4-entity views; the new domain has
// only Runtimes, Models and Instances, so the surplus UI is trimmed here.

function setupLayout() {

    rebuildWizard();
    rebuildRuntimeModals();
    rebuildHistoryHeader();
    const instHead = document.querySelector('#view-adv-instances thead');
    if (instHead) {
        instHead.innerHTML = '<tr><th>ID</th><th>Модель</th><th>Состояние</th><th>PID</th><th>Запущен</th><th>Остановлен</th><th>Exit</th><th></th></tr>';
    }
    const rtHead = document.querySelector('#view-adv-runtimes thead');
    if (rtHead) {
        rtHead.innerHTML = '<tr><th>Название</th><th>Исполняемый файл</th><th>Рабочая папка</th><th>Доп. параметры</th><th></th></tr>';
    }
}

function hideNavItems(views) {
    views.forEach(function (v) {
        const btn = document.querySelector('.nav-item[data-view="' + v + '"]');
        if (btn) btn.style.display = 'none';
    });
}

function rebuildWizard() {
    const steps = document.querySelector('#wizard-modal .wizard-steps');
    if (steps) {
        steps.innerHTML =
            '<div class="wizard-step active" data-step="1"><span>1</span> Модель</div>' +
            '<div class="wizard-step" data-step="2"><span>2</span> Runtime</div>' +
            '<div class="wizard-step" data-step="3"><span>3</span> Запуск</div>';
    }
    const form = document.getElementById('wizard-form');
    if (!form) return;
    form.innerHTML =
        '<div class="wizard-panel active" data-panel="1">' +
            '<div class="form-group">' +
                '<label>Название модели *</label>' +
                '<input type="text" id="wiz-name" placeholder="Например: Qwen3.8-27B-Q4_K_M" required>' +
            '</div>' +
        '</div>' +
        '<div class="wizard-panel" data-panel="2">' +
            '<div class="form-group">' +
                '<label>Runtime</label>' +
                '<div class="radio-group">' +
                    '<label class="radio-option">' +
                        '<input type="radio" name="wiz-rt-mode" value="existing" checked onchange="onWizRtModeChange()">' +
                        '<span>Использовать существующий</span>' +
                    '</label>' +
                    '<label class="radio-option">' +
                        '<input type="radio" name="wiz-rt-mode" value="new" onchange="onWizRtModeChange()">' +
                        '<span>Создать новый Runtime</span>' +
                    '</label>' +
                '</div>' +
            '</div>' +
            '<div id="wiz-rt-existing-section" class="form-group">' +
                '<label>Выберите Runtime</label>' +
                '<select id="wiz-runtime-existing"><option value="">— выбрать —</option></select>' +
            '</div>' +
            '<div id="wiz-rt-new-section" class="form-group" style="display:none;">' +
                '<small class="hint" style="margin-bottom:8px;display:block;">Runtime будет создан автоматически при нажатии «Создать».</small>' +
                '<div class="form-group">' +
                    '<label>Название *</label>' +
                    '<input type="text" id="wiz-rt-name" placeholder="llama.cpp">' +
                '</div>' +
                '<div class="form-group">' +
                    '<label>Исполняемый файл *</label>' +
                    '<input type="text" id="wiz-rt-executable" placeholder="E:\\tools\\llama-server.exe">' +
                    '<small class="hint">Имя файла (относительно рабочей папки) или абсолютный путь</small>' +
                '</div>' +
                '<div class="form-group">' +
                    '<label>Рабочая папка</label>' +
                    '<input type="text" id="wiz-rt-workdir" placeholder="E:\\tools">' +
                '</div>' +
                '<div class="form-group">' +
                    '<label>Дополнительные параметры Runtime</label>' +
                    '<textarea id="wiz-rt-default-args" rows="2" placeholder="Аргументы по умолчанию (опционально)"></textarea>' +
                '</div>' +
            '</div>' +
        '</div>' +
        '<div class="wizard-panel" data-panel="3">' +
            '<div class="form-group">' +
                '<label>Аргументы запуска</label>' +
                '<textarea id="wiz-args" rows="4" placeholder="-m model.gguf&#10;--ngl 99&#10;--port 8080"></textarea>' +
                '<small class="hint">По одному флагу в строке или через пробел</small>' +
            '</div>' +
            '<div class="form-row">' +
                '<div class="form-group">' +
                    '<label>Host</label>' +
                    '<input type="text" id="wiz-host" value="0.0.0.0">' +
                '</div>' +
                '<div class="form-group">' +
                    '<label>Port</label>' +
                    '<input type="number" id="wiz-port" value="8080" min="1" max="65535">' +
                '</div>' +
            '</div>' +
            '<div class="form-group">' +
                '<label>Переменные окружения (расширено)</label>' +
                '<small class="hint">Дополнительные env-переменные процесса (например, CUDA_VISIBLE_DEVICES). Значения не показываются при чтении.</small>' +
                '<div id="wiz-env-container"></div>' +
                '<button type="button" class="btn btn-ghost btn-sm" onclick="addWizEnvRow()">+ Переменная</button>' +
            '</div>' +
            '<div class="form-group form-autostart">' +
                '<div>' +
                    '<span class="autostart-label">Автозапуск при старте GoAl</span>' +
                    '<small class="hint">Модель будет запущена автоматически при каждом старте GoAl</small>' +
                '</div>' +
                '<label class="toggle-switch"><input type="checkbox" id="wiz-autostart"><span class="toggle-slider"></span></label>' +
            '</div>' +
            '<div class="form-group" id="wiz-delay-group" style="display:none;">' +
                '<label>Задержка запуска (сек)</label>' +
                '<input type="number" id="wiz-autostart-delay" value="0" min="0" max="300">' +
            '</div>' +
        '</div>' +
        '<div id="wizard-error" class="error-msg" style="display:none;"></div>' +
        '<div class="modal-actions">' +
            '<button type="button" class="btn btn-ghost" id="wiz-prev" onclick="wizPrev()" style="display:none;">Назад</button>' +
            '<button type="submit" class="btn btn-primary" id="wiz-next">Далее</button>' +
            '<button type="button" class="btn btn-ghost" onclick="closeWizard()">Отмена</button>' +
        '</div>';

    form.addEventListener('submit', handleWizardSubmit);
    document.getElementById('wiz-autostart').addEventListener('change', function () {
        document.getElementById('wiz-delay-group').style.display = this.checked ? '' : 'none';
    });
}

function rebuildRuntimeModals() {
    const createForm = document.querySelector('#create-runtime-modal form');
    if (createForm) {
        createForm.innerHTML =
            '<div class="form-group"><label>Название *</label><input type="text" name="name" required placeholder="llama.cpp"></div>' +
            '<div class="form-group"><label>Исполняемый файл *</label><input type="text" name="executable" required placeholder="E:\\tools\\llama-server.exe"></div>' +
            '<div class="form-group"><label>Рабочая папка</label><input type="text" name="working_directory" placeholder="E:\\tools"></div>' +
            '<div class="form-group"><label>Дополнительные параметры Runtime</label><textarea name="default_args" rows="2" placeholder="Аргументы по умолчанию (опционально)"></textarea></div>' +
            '<div class="form-group"><label>Environment (расширено)</label><div id="rt-create-env-container"></div>' +
            '<button type="button" class="btn btn-ghost btn-sm" onclick="addEnvRow(\'rt-create-env-container\')">+ Переменная</button></div>' +
            '<div class="modal-actions"><button type="submit" class="btn btn-primary">Создать</button><button type="button" class="btn btn-ghost" onclick="closeModal(\'create-runtime-modal\')">Отмена</button></div>';
    }
    const editForm = document.querySelector('#edit-runtime-modal form');
    if (editForm) {
        editForm.innerHTML =
            '<input type="hidden" name="id">' +
            '<div class="form-group"><label>Название *</label><input type="text" name="name" required></div>' +
            '<div class="form-group"><label>Исполняемый файл *</label><input type="text" name="executable" required></div>' +
            '<div class="form-group"><label>Рабочая папка</label><input type="text" name="working_directory"></div>' +
            '<div class="form-group"><label>Дополнительные параметры Runtime</label><textarea name="default_args" rows="2"></textarea></div>' +
            '<div class="form-group"><label>Environment (расширено)</label><div id="rt-edit-env-container"></div>' +
            '<button type="button" class="btn btn-ghost btn-sm" onclick="addEnvRow(\'rt-edit-env-container\')">+ Переменная</button></div>' +
            '<div class="modal-actions"><button type="submit" class="btn btn-primary">Сохранить</button><button type="button" class="btn btn-ghost" onclick="closeModal(\'edit-runtime-modal\')">Отмена</button></div>';
    }
    const createTitle = document.querySelector('#create-runtime-modal h2');
    if (createTitle) createTitle.textContent = 'Создать Runtime';
    const editTitle = document.querySelector('#edit-runtime-modal h2');
    if (editTitle) editTitle.textContent = 'Изменить Runtime';
}

function rebuildHistoryHeader() {
    const head = document.querySelector('#view-history thead');
    if (head) {
        head.innerHTML = '<tr><th>Модель</th><th>Instance</th><th>Состояние</th><th>PID</th><th>Запущен</th><th>Остановлен</th><th>Exit code</th></tr>';
    }
}

// ─── Init ───────────────────────────────────────────────────────────────────

async function init() {
    setupLayout();
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
        let details = null;
        try {
            const d = await r.json();
            msg = d.error || msg;
            if (Array.isArray(d.details)) details = d.details;
        } catch {}
        const err = new Error(msg);
        err.status = r.status;
        err.details = details;
        throw err;
    }
    return r.json();
}

// ─── Data loading ───────────────────────────────────────────────────────────

async function reloadAllData() {
    const [rt, mo, ins] = await Promise.allSettled([
        api('/runtimes'), api('/models'), api('/instances')
    ]);
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
    renderAdvInstances();
    renderHistory();
    updateLogInstanceSelect();
}

function getRuntimeName(id) {
    const r = runtimesData.find(x => x.id === id);
    return r ? r.name : (id || '—');
}

function getModelName(id) {
    const m = modelsData.find(x => x.id === id);
    return m ? m.name : (id || '—');
}

function getActiveInstances(modelId) {
    return instancesData.filter(i => i.model_id === modelId && isActive(i.state));
}

function isActive(s) { return s === 'running' || s === 'starting' || s === 'stopping'; }

function modelStatus(model) {
    const active = getActiveInstances(model.id);
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
    if (diff < 0) return '';
    const h = Math.floor(diff / 3600);
    const m = Math.floor((diff % 3600) / 60);
    const s = diff % 60;
    return String(h).padStart(2, '0') + ':' + String(m).padStart(2, '0') + ':' + String(s).padStart(2, '0');
}

function fmtTime(ts) {
    if (!ts) return '—';
    const d = new Date(ts);
    if (isNaN(d.getTime())) return '—';
    return d.toLocaleString();
}

function esc(s) {
    const d = document.createElement('div');
    d.textContent = s || '';
    return d.innerHTML;
}

function safeId(id) {
    return String(id).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

function parseArgs(raw) {
    if (!raw) return [];
    return raw.trim().split(/\s+/).filter(Boolean);
}

// ─── Model Cards ────────────────────────────────────────────────────────────

function renderModelCards() {
    const grid = document.getElementById('model-grid');
    const empty = document.getElementById('models-empty');
    if (modelsData.length === 0) {
        grid.innerHTML = '';
        empty.style.display = 'block';
        return;
    }
    empty.style.display = 'none';
    grid.innerHTML = modelsData.map(m => {
        const status = modelStatus(m);
        const active = getActiveInstances(m.id);
        const inst = active[0];
        const uptime = inst ? fmtUptime(inst.started_at) : '';

        let actionBtns = '';
        if (status === 'running') {
            actionBtns = '<button class="btn btn-warning btn-sm" onclick="restartModel(\'' + safeId(m.id) + '\')">Перезапустить</button>' +
                '<button class="btn btn-danger btn-sm" onclick="stopModel(\'' + safeId(m.id) + '\')">Остановить</button>';
        } else if (status === 'starting' || status === 'stopping') {
            actionBtns = '<span class="hint-text">В процессе...</span>';
        } else {
            actionBtns = '<button class="btn btn-success btn-sm" onclick="startModel(\'' + safeId(m.id) + '\')">Запустить</button>';
        }

        return '<div class="model-card">' +
            '<div class="model-card-header">' +
                '<div>' +
                    '<div class="model-card-title">' + esc(m.name) + '</div>' +
                    '<div class="model-card-runtime">' + esc(getRuntimeName(m.runtime_id)) + '</div>' +
                '</div>' +
                '<span class="status-badge ' + status + '"><span class="status-dot ' + (status === 'running' ? 'running' : '') + '"></span>' + status.toUpperCase() + '</span>' +
            '</div>' +
            '<div class="model-card-info">' +
                '<div class="info-row"><span class="label">Адрес:</span><span>' + esc(m.host || '0.0.0.0') + ':' + (m.port || '—') + '</span></div>' +
                (inst && inst.pid ? '<div class="info-row"><span class="label">PID:</span><span>' + inst.pid + '</span></div>' : '') +
                (uptime ? '<div class="info-row"><span class="label">Uptime:</span><span>' + uptime + '</span></div>' : '') +
            '</div>' +
            '<div class="model-card-autostart"><span class="autostart-label">Автозапуск</span><label class="toggle-switch"><input type="checkbox" ' + (m.active ? 'checked' : '') + ' onchange="toggleAutostart(\'' + safeId(m.id) + '\')"><span class="toggle-slider"></span></label></div>' +
            '<div class="model-card-actions">' +
                actionBtns +
                '<button class="btn btn-ghost btn-sm" onclick="viewInstanceLogs(\'' + (active.length ? safeId(active[0].id) : '') + '\')">Логи</button>' +
                '<button class="btn btn-ghost btn-sm" onclick="openWizard(\'' + safeId(m.id) + '\')">Изменить</button>' +
                '<button class="btn btn-danger btn-sm" onclick="deleteModel(\'' + safeId(m.id) + '\')">Удалить</button>' +
            '</div>' +
        '</div>';
    }).join('');
}

// ─── Model actions ──────────────────────────────────────────────────────────

async function startModel(modelId) {
    const btns = document.querySelectorAll('.model-card-actions .btn-success');
    btns.forEach(b => b.disabled = true);
    try {
        await api('/models/' + modelId + '/start', { method: 'POST' });
        showToast('Запуск...', 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast(friendlyError(e), 'error'); btns.forEach(b => b.disabled = false); }
}

async function stopModel(modelId) {
    const btns = document.querySelectorAll('.model-card-actions .btn-danger');
    btns.forEach(b => b.disabled = true);
    try {
        await api('/models/' + modelId + '/stop', { method: 'POST' });
        showToast('Остановка...', 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast(friendlyError(e), 'error'); btns.forEach(b => b.disabled = false); }
}

async function restartModel(modelId) {
    const btns = document.querySelectorAll('.model-card-actions .btn-warning');
    btns.forEach(b => b.disabled = true);
    try {
        await api('/models/' + modelId + '/restart', { method: 'POST' });
        showToast('Перезапуск...', 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast(friendlyError(e), 'error'); btns.forEach(b => b.disabled = false); }
}

function deleteModel(id) {
    const m = modelsData.find(x => x.id === id);
    const name = m ? m.name : id;
    const activeInsts = getActiveInstances(id);
    let msg = 'Удалить модель "' + name + '"?';
    if (activeInsts.length > 0) msg += '\nАктивные экземпляры (' + activeInsts.length + ') будут остановлены.';
    showConfirm(msg, async function () {
        try {
            await api('/models/' + id, { method: 'DELETE' });
            closeConfirm();
            showToast('Модель удалена', 'success');
            await reloadAllData(); renderAll();
        } catch (err) {
            if (err.status === 409 && err.details && err.details.length) {
                closeConfirm();
                showBlockedModal('Модель', name, err.details);
            } else {
                showToast(friendlyError(err), 'error');
            }
        }
    });
}

async function toggleAutostart(id) {
    const m = modelsData.find(x => x.id === id);
    if (!m) return;
    try {
        if (m.active) {
            await api('/models/' + id + '/deactivate', { method: 'POST' });
        } else {
            await api('/models/' + id + '/activate', { method: 'POST' });
        }
        await reloadAllData(); renderAll();
    } catch (err) { showToast(friendlyError(err), 'error'); await reloadAllData(); renderAll(); }
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
            '<option value="' + esc(i.id) + '">' + esc(getModelName(i.model_id)) + ' | ' + i.id.slice(0, 12) + '… | PID ' + (i.pid || '?') + ' | ' + esc(i.state) + '</option>'
        ).join('');
    sel.value = cur;
}

function switchLogInstance() {
    const instId = document.getElementById('log-instance-select').value;
    updateLogInstanceBar(instId);
    connectLogStream(instId);
}

function updateLogInstanceBar(instId) {
    const bar = document.getElementById('log-instance-bar');
    if (!bar) return;
    if (!instId) { bar.style.display = 'none'; return; }
    const inst = instancesData.find(i => i.id === instId);
    if (!inst) { bar.style.display = 'none'; return; }
    const started = fmtTime(inst.started_at);
    bar.innerHTML = '<span class="log-bar-label">Модель:</span> ' + esc(getModelName(inst.model_id)) +
        ' &nbsp;|&nbsp; <span class="log-bar-label">Instance:</span> <code>' + esc(inst.id) + '</code>' +
        ' &nbsp;|&nbsp; <span class="log-bar-label">PID:</span> ' + (inst.pid || '—') +
        ' &nbsp;|&nbsp; <span class="log-bar-label">State:</span> <span class="status-badge ' + esc(inst.state) + '">' + esc(inst.state) + '</span>' +
        ' &nbsp;|&nbsp; <span class="log-bar-label">Старт:</span> ' + started;
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
    if (search && !(d.message || '').toLowerCase().includes(search)) return;

    const view = document.getElementById('log-view');
    const div = document.createElement('div');
    div.className = 'log-line ' + stream;
    const t = d.time ? new Date(d.time).toLocaleTimeString() : '';
    div.innerHTML = '<span class="log-time">' + t + '</span><span class="log-source">[' + esc(stream) + ']</span>' + esc(d.message);
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
    const navBtn = document.querySelector('.nav-item[data-view="' + view + '"]');
    if (navBtn) navBtn.classList.add('active');

    if (view === 'logs' && !logEs) connectLogStream('');
    if (view === 'adv-settings') loadSettings();
}

// ─── Wizard (create / edit model) ───────────────────────────────────────────

function openWizard(modelId) {
    wizStep = 1;
    wizEditId = modelId || null;
    const form = document.getElementById('wizard-form');
    form.reset();
    document.getElementById('wizard-error').style.display = 'none';
    document.getElementById('wiz-env-container').innerHTML = '';
    document.querySelector('input[name=wiz-rt-mode][value=existing]').checked = true;
    onWizRtModeChange();
    loadWizardRuntimeSelect();

    const titleEl = document.querySelector('#wizard-modal h2');
    if (titleEl) titleEl.textContent = wizEditId ? 'Изменить модель' : 'Добавить модель';

    if (wizEditId) {
        const m = modelsData.find(x => x.id === wizEditId);
        if (m) {
            document.getElementById('wiz-name').value = m.name || '';
            document.getElementById('wiz-args').value = (m.args || []).join('\n');
            document.getElementById('wiz-host').value = m.host || '0.0.0.0';
            document.getElementById('wiz-port').value = m.port || 8080;
            document.getElementById('wiz-autostart').checked = !!m.active;
            document.getElementById('wiz-delay-group').style.display = m.active ? '' : 'none';
            document.getElementById('wiz-autostart-delay').value = m.autostart_delay || 0;
            if (m.runtime_id) {
                const sel = document.getElementById('wiz-runtime-existing');
                sel.value = m.runtime_id;
            }
        }
    }
    document.getElementById('wiz-next').textContent = wizStep === 3 ? (wizEditId ? 'Сохранить' : 'Создать') : 'Далее';
    updateWizardStep();
    document.getElementById('wizard-modal').style.display = 'flex';
}

function closeWizard() { document.getElementById('wizard-modal').style.display = 'none'; }

function loadWizardRuntimeSelect() {
    const sel = document.getElementById('wiz-runtime-existing');
    const cur = sel.value;
    sel.innerHTML = '<option value="">— выбрать —</option>' +
        runtimesData.map(r => '<option value="' + esc(r.id) + '">' + esc(r.name) + ' (' + esc(r.executable) + ')</option>').join('');
    sel.value = cur;
}

function onWizRtModeChange() {
    const mode = document.querySelector('input[name=wiz-rt-mode]:checked').value;
    document.getElementById('wiz-rt-existing-section').style.display = mode === 'existing' ? '' : 'none';
    document.getElementById('wiz-rt-new-section').style.display = mode === 'new' ? '' : 'none';
}

function wizPrev() { if (wizStep > 1) { wizStep--; updateWizardStep(); } }

function updateWizardStep() {
    document.querySelectorAll('#wizard-modal .wizard-step').forEach(s => {
        const n = parseInt(s.dataset.step);
        s.classList.toggle('active', n === wizStep);
        s.classList.toggle('done', n < wizStep);
    });
    document.querySelectorAll('#wizard-modal .wizard-panel').forEach(p => p.classList.toggle('active', parseInt(p.dataset.panel) === wizStep));
    document.getElementById('wiz-prev').style.display = wizStep > 1 ? '' : 'none';
    document.getElementById('wiz-next').textContent = wizStep === 3 ? (wizEditId ? 'Сохранить' : 'Создать') : 'Далее';
}

async function handleWizardSubmit(e) {
    e.preventDefault();
    if (wizStep < 3) {
        if (wizStep === 1 && !document.getElementById('wiz-name').value.trim()) {
            wizError('Укажите название');
            return;
        }
        wizStep++;
        updateWizardStep();
        return;
    }

    const name = document.getElementById('wiz-name').value.trim();
    if (!name) { wizError('Укажите название'); return; }

    const rtMode = document.querySelector('input[name=wiz-rt-mode]:checked').value;
    let runtimeId = rtMode === 'existing' ? document.getElementById('wiz-runtime-existing').value : '';
    if (rtMode === 'existing' && !runtimeId) {
        wizError('Выберите Runtime');
        return;
    }

    const submitBtn = document.getElementById('wiz-next');
    if (submitBtn.disabled) return;
    submitBtn.disabled = true;
    const oldLabel = submitBtn.textContent;
    submitBtn.textContent = wizEditId ? 'Сохранение...' : 'Создание...';

    try {
        if (rtMode === 'new') {
            const rtName = document.getElementById('wiz-rt-name').value.trim();
            const rtExe = document.getElementById('wiz-rt-executable').value.trim();
            if (!rtName || !rtExe) { wizError('Заполните поля Runtime'); return; }
            const rtBody = { name: rtName, executable: rtExe };
            const rtWd = document.getElementById('wiz-rt-workdir').value.trim();
            if (rtWd) rtBody.working_directory = rtWd;
            const rtArgs = parseArgs(document.getElementById('wiz-rt-default-args').value);
            if (rtArgs.length) rtBody.default_args = rtArgs;
            const rt = await api('/runtimes', { method: 'POST', body: JSON.stringify(rtBody) });
            runtimeId = rt.id;
        }

        const body = {
            name: name,
            runtime_id: runtimeId,
            args: parseArgs(document.getElementById('wiz-args').value),
            host: document.getElementById('wiz-host').value.trim() || '0.0.0.0',
            port: parseInt(document.getElementById('wiz-port').value) || 8080,
            active: document.getElementById('wiz-autostart').checked
        };
        if (body.active) {
            const delay = parseInt(document.getElementById('wiz-autostart-delay').value) || 0;
            if (delay > 0) body.autostart_delay = delay;
        }
        const env = collectEnvRows('wiz-env-container');
        if (Object.keys(env).length) body.environment = env;

        if (wizEditId) {
            await api('/models/' + wizEditId, { method: 'PUT', body: JSON.stringify(body) });
            showToast('Модель "' + name + '" сохранена', 'success');
        } else {
            await api('/models', { method: 'POST', body: JSON.stringify(body) });
            showToast('Модель "' + name + '" создана', 'success');
        }
        closeWizard();
        await reloadAllData(); renderAll();
    } catch (e2) {
        submitBtn.disabled = false;
        submitBtn.textContent = oldLabel;
        wizError(friendlyError(e2));
        return;
    }
    submitBtn.disabled = false;
    submitBtn.textContent = oldLabel;
}

function wizError(msg) {
    const el = document.getElementById('wizard-error');
    el.textContent = msg;
    el.style.display = 'block';
}

// ─── Environment rows ───────────────────────────────────────────────────────

function addEnvRow(containerId) {
    const div = document.createElement('div');
    div.className = 'env-row';
    div.style.display = 'flex';
    div.style.gap = '6px';
    div.style.marginBottom = '6px';
    div.innerHTML = '<input type="text" placeholder="KEY" style="flex:1;padding:6px;border:1px solid var(--border);border-radius:4px;background:var(--bg-input);color:var(--text-primary);">' +
        '<input type="text" placeholder="value" style="flex:2;padding:6px;border:1px solid var(--border);border-radius:4px;background:var(--bg-input);color:var(--text-primary);">' +
        '<button type="button" class="btn btn-danger btn-sm" onclick="this.parentElement.remove()">&times;</button>';
    document.getElementById(containerId).appendChild(div);
}

function addWizEnvRow() { addEnvRow('wiz-env-container'); }

function collectEnvRows(containerId) {
    const env = {};
    document.querySelectorAll('#' + containerId + ' .env-row').forEach(row => {
        const inputs = row.querySelectorAll('input');
        if (inputs[0] && inputs[0].value.trim()) env[inputs[0].value.trim()] = inputs[1] ? inputs[1].value : '';
    });
    return env;
}

function collectWizEnv() { return collectEnvRows('wiz-env-container'); }

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
        '<tr><td>' + esc(r.name) + '</td><td>' + esc(r.executable) + '</td><td>' + esc(r.working_directory || '—') + '</td>' +
        '<td style="max-width:200px;overflow:hidden;text-overflow:ellipsis;"><code>' + esc((r.default_args || []).join(' ')) + '</code></td>' +
        '<td class="actions-cell"><button class="btn btn-ghost btn-sm" onclick="editRuntime(\'' + safeId(r.id) + '\')">Изменить</button> ' +
        '<button class="btn btn-danger btn-sm" onclick="deleteRuntime(\'' + safeId(r.id) + '\')">Удалить</button></td></tr>'
    ).join('');
}

function showCreateRuntimeModal() {
    const f = document.querySelector('#create-runtime-modal form');
    f.reset();
    document.getElementById('rt-create-env-container').innerHTML = '';
    document.getElementById('create-runtime-modal').style.display = 'flex';
}

async function handleCreateRuntime(e) {
    e.preventDefault();
    const f = e.target;
    const body = { name: f.name.value, executable: f.executable.value };
    if (f.working_directory.value) body.working_directory = f.working_directory.value;
    const args = parseArgs(f.default_args.value);
    if (args.length) body.default_args = args;
    const env = collectEnvRows('rt-create-env-container');
    if (Object.keys(env).length) body.environment = env;
    try {
        await api('/runtimes', { method: 'POST', body: JSON.stringify(body) });
        closeModal('create-runtime-modal');
        showToast('Runtime создан', 'success');
        await reloadAllData(); renderAll();
    } catch (err) { showToast(friendlyError(err), 'error'); }
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
    const envC = document.getElementById('rt-edit-env-container');
    envC.innerHTML = '';
    if (r.environment_keys && r.environment_keys.length) {
        r.environment_keys.forEach(k => {
            const row = document.createElement('div');
            row.className = 'env-row';
            row.style.display = 'flex';
            row.style.gap = '6px';
            row.style.marginBottom = '6px';
            const i1 = document.createElement('input');
            i1.type = 'text';
            i1.value = k;
            i1.style.cssText = 'flex:1;padding:6px;border:1px solid var(--border);border-radius:4px;background:var(--bg-input);color:var(--text-primary);';
            const i2 = document.createElement('input');
            i2.type = 'text';
            i2.placeholder = 'value (не показывается при чтении)';
            i2.style.cssText = 'flex:2;padding:6px;border:1px solid var(--border);border-radius:4px;background:var(--bg-input);color:var(--text-primary);';
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'btn btn-danger btn-sm';
            btn.onclick = function () { row.remove(); };
            btn.innerHTML = '&times;';
            row.appendChild(i1);
            row.appendChild(i2);
            row.appendChild(btn);
            envC.appendChild(row);
        });
    }
    modal.style.display = 'flex';
}

async function handleEditRuntime(e) {
    e.preventDefault();
    const f = e.target;
    const body = { name: f.name.value, executable: f.executable.value };
    if (f.working_directory.value) body.working_directory = f.working_directory.value;
    body.default_args = parseArgs(f.default_args.value);
    const env = collectEnvRows('rt-edit-env-container');
    body.environment = env;
    try {
        await api('/runtimes/' + f.querySelector('[name=id]').value, { method: 'PUT', body: JSON.stringify(body) });
        closeModal('edit-runtime-modal');
        showToast('Сохранено', 'success');
        await reloadAllData(); renderAll();
    } catch (err) { showToast(friendlyError(err), 'error'); }
    return false;
}

function deleteRuntime(id) {
    const rt = runtimesData.find(x => x.id === id);
    const name = rt ? rt.name : id;
    const depModels = modelsData.filter(m => m.runtime_id === id);
    if (depModels.length > 0) {
        showBlockedModal('Runtime', name, depModels.map(m => 'Модель: ' + m.name));
        return;
    }
    showConfirm('Удалить runtime "' + name + '"?', async function () {
        try {
            await api('/runtimes/' + id, { method: 'DELETE' });
            closeConfirm();
            showToast('Runtime удалён', 'success');
            await reloadAllData(); renderAll();
        } catch (err) {
            if (err.status === 409 && err.details && err.details.length) {
                closeConfirm();
                showBlockedModal('Runtime', name, err.details.map(formatServerDetail));
            } else {
                showToast(friendlyError(err), 'error');
            }
        }
    });
}

function formatServerDetail(d) {
    const s = String(d);
    const m = s.match(/^model:\s*(.+)$/i);
    if (m) return 'Модель: ' + m[1];
    return s;
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
        '<tr><td style="font-family:var(--font-mono);font-size:0.78rem;">' + esc(i.id.slice(0, 16)) + '</td>' +
        '<td>' + esc(getModelName(i.model_id)) + '</td>' +
        '<td><span class="status-badge ' + esc(i.state) + '">' + esc(i.state) + '</span></td>' +
        '<td>' + (i.pid || '—') + '</td><td>' + fmtTime(i.started_at) + '</td><td>' + fmtTime(i.stopped_at) + '</td>' +
        '<td>' + (i.exit_code != null ? i.exit_code : '—') + '</td>' +
        '<td class="actions-cell"><button class="btn btn-ghost btn-sm" onclick="viewInstanceLogs(\'' + safeId(i.id) + '\')">Логи</button>' +
        (isActive(i.state) ? ' <button class="btn btn-danger btn-sm" onclick="stopInstance(\'' + safeId(i.id) + '\')">Остановить</button> ' +
        '<button class="btn btn-warning btn-sm" onclick="restartInstance(\'' + safeId(i.id) + '\')">Перезапустить</button>' : '') + '</td></tr>'
    ).join('');
}

async function stopInstance(id) {
    try { await api('/instances/' + id + '/stop', { method: 'POST' }); await reloadAllData(); renderAll(); }
    catch (err) { showToast(friendlyError(err), 'error'); }
}

async function restartInstance(id) {
    try { await api('/instances/' + id + '/restart', { method: 'POST' }); await reloadAllData(); renderAll(); }
    catch (err) { showToast(friendlyError(err), 'error'); }
}

// ─── History ────────────────────────────────────────────────────────────────

function renderHistory() {
    const empty = document.getElementById('history-empty');
    if (instancesData.length === 0) {
        document.getElementById('history-body').innerHTML = '';
        if (empty) empty.style.display = 'block';
        return;
    }
    if (empty) empty.style.display = 'none';
    document.getElementById('history-body').innerHTML = instancesData.slice(0, 100).map(i =>
        '<tr><td>' + esc(getModelName(i.model_id)) + '</td>' +
        '<td style="font-family:var(--font-mono);font-size:0.78rem;">' + esc(i.id.slice(0, 12)) + '</td>' +
        '<td><span class="status-badge ' + esc(i.state) + '">' + esc(i.state) + '</span></td>' +
        '<td>' + (i.pid || '—') + '</td>' +
        '<td>' + fmtTime(i.started_at) + '</td><td>' + fmtTime(i.stopped_at) + '</td>' +
        '<td>' + (i.exit_code != null ? i.exit_code : '—') + '</td></tr>'
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

// ─── Error messages ─────────────────────────────────────────────────────────

function friendlyError(err) {
    let msg = (err && err.message) || 'Неизвестная ошибка';
    const patterns = [
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

// ─── Modal helpers ──────────────────────────────────────────────────────────

function closeModal(id) { document.getElementById(id).style.display = 'none'; }

function showConfirm(msg, onYes) {
    const el = document.getElementById('confirm-message');
    el.textContent = '';
    el.style.whiteSpace = 'pre-line';
    msg.split('\n').forEach((line, idx) => {
        if (idx > 0) el.appendChild(document.createElement('br'));
        el.appendChild(document.createTextNode(line));
    });
    const btn = document.getElementById('confirm-yes');
    btn.disabled = false;
    btn.textContent = 'Подтвердить';
    btn.onclick = async function () {
        btn.disabled = true;
        btn.textContent = 'Выполняется...';
        try {
            await onYes();
        } finally {
            btn.disabled = false;
            btn.textContent = 'Подтвердить';
        }
    };
    document.getElementById('confirm-modal').style.display = 'flex';
}

function closeConfirm() { document.getElementById('confirm-modal').style.display = 'none'; }

function showBlockedModal(entityType, entityName, dependents) {
    const content = document.getElementById('blocked-content');
    let html = '<p><strong>' + esc(entityType) + '</strong>: ' + esc(entityName) + '</p>';
    html += '<p class="hint-text" style="margin-top:0.8rem;">Используется:</p><ul style="margin:0.5rem 0 0.5rem 1.2rem;font-size:0.88rem;color:var(--text-secondary);">';
    dependents.forEach(d => { html += '<li>' + esc(d) + '</li>'; });
    html += '</ul><p class="hint-text" style="margin-top:0.8rem;">Сначала удалите или измените связанные записи.</p>';
    content.innerHTML = html;
    document.getElementById('blocked-modal').style.display = 'flex';
}

function closeBlocked() { document.getElementById('blocked-modal').style.display = 'none'; }

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
    }, 3000);
}

// ─── Expose to global scope for inline onclick handlers ────────────────────

window.handleLogin = handleLogin;
window.handleLogout = handleLogout;
window.navigate = navigate;
window.openWizard = openWizard;
window.closeWizard = closeWizard;
window.wizPrev = wizPrev;
window.viewInstanceLogs = viewInstanceLogs;
window.startModel = startModel;
window.stopModel = stopModel;
window.restartModel = restartModel;
window.deleteModel = deleteModel;
window.stopInstance = stopInstance;
window.restartInstance = restartInstance;
window.showCreateRuntimeModal = showCreateRuntimeModal;
window.handleCreateRuntime = handleCreateRuntime;
window.editRuntime = editRuntime;
window.handleEditRuntime = handleEditRuntime;
window.deleteRuntime = deleteRuntime;
window.toggleAutostart = toggleAutostart;
window.switchLogInstance = switchLogInstance;
window.applyLogSearch = applyLogSearch;
window.toggleLogPause = toggleLogPause;
window.clearLogView = clearLogView;
window.closeModal = closeModal;
window.closeConfirm = closeConfirm;
window.closeBlocked = closeBlocked;
window.reloadAllData = reloadAllData;
window.addWizEnvRow = addWizEnvRow;
window.addEnvRow = addEnvRow;
window.onWizRtModeChange = onWizRtModeChange;

// ─── Boot ───────────────────────────────────────────────────────────────────

init();
})();

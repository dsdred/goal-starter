(function () {
'use strict';

let csrfToken = '';
let currentView = 'models';
let logEs = null;
let logPaused = false;
let refreshTimer = null;

let runtimesData = [];
let modelsData = [];
let instancesData = [];

let wizStep = 1;
let wizEditId = null;

let i18nDict = {};
let currentLang = localStorage.getItem('goal_lang') || 'ru';
let currentTheme = localStorage.getItem('goal_theme') || 'system';
let versionInfo = {};

// ─── i18n ───────────────────────────────────────────────────────────────────

function t(key, params) {
    let s = i18nDict[key] || key;
    if (params) {
        Object.keys(params).forEach(function (k) {
            s = s.split('{' + k + '}').join(String(params[k]));
        });
    }
    return s;
}

function applyI18n() {
    document.querySelectorAll('[data-i18n]').forEach(function (el) {
        el.textContent = t(el.dataset.i18n);
    });
    document.querySelectorAll('[data-i18n-placeholder]').forEach(function (el) {
        el.placeholder = t(el.dataset.i18nPlaceholder);
    });
    document.title = t('app.title');
}

async function loadI18n(lang) {
    try {
        const r = await fetch('/static/i18n/' + lang + '.json');
        if (r.ok) {
            i18nDict = await r.json();
            applyI18n();
        }
    } catch {}
}

function setLanguage(lang) {
    currentLang = lang;
    localStorage.setItem('goal_lang', lang);
    document.getElementById('lang-select').value = lang;
    const setLang = document.getElementById('set-lang');
    if (setLang) setLang.value = lang;
    loadI18n(lang);
}

// ─── Theme ──────────────────────────────────────────────────────────────────

function applyTheme() {
    let mode = currentTheme;
    if (mode === 'system') {
        mode = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    document.documentElement.setAttribute('data-theme', mode);
}

function setTheme(theme) {
    currentTheme = theme;
    localStorage.setItem('goal_theme', theme);
    applyTheme();
    document.getElementById('theme-select').value = theme;
    const setThemeEl = document.getElementById('set-theme');
    if (setThemeEl) setThemeEl.value = theme;
}

// ─── Init ───────────────────────────────────────────────────────────────────

async function init() {
    applyTheme();
    document.getElementById('theme-select').value = currentTheme;
    document.getElementById('lang-select').value = currentLang;
    await loadI18n(currentLang);
    document.getElementById('wizard-form').addEventListener('submit', handleWizardSubmit);
    document.getElementById('wiz-autostart').addEventListener('change', function () {
        document.getElementById('wiz-delay-group').style.display = this.checked ? '' : 'none';
    });
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
                updateSidebarAuth(d.user);
                return true;
            }
        }
    } catch {}
    showLogin();
    return false;
}

function updateSidebarAuth(user) {
    const el = document.getElementById('sidebar-auth');
    const userEl = document.getElementById('sidebar-user');
    const logoutBtn = document.getElementById('sidebar-logout');
    if (user && user !== 'public') {
        el.style.display = 'flex';
        userEl.textContent = user;
        if (logoutBtn) logoutBtn.style.display = '';
    } else {
        el.style.display = 'flex';
        userEl.textContent = user || '—';
        if (logoutBtn) logoutBtn.style.display = 'none';
    }
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
            const d = await r.json();
            await getCSRFToken();
            updateSidebarAuth(d.user || u);
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

async function api(path, opts) {
    opts = opts || {};
    const headers = { 'Content-Type': 'application/json' };
    if (opts.method && opts.method !== 'GET') headers['X-CSRF-Token'] = csrfToken;
    const r = await fetch('/api/v1' + path, { method: opts.method || 'GET', headers: headers, body: opts.body });
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
    if (r.status === 204) return null;
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
    updateFilterOptions();
}

async function loadVersion() {
    try {
        const d = await api('/version');
        versionInfo = d;
        const ver = d.version || 'dev';
        document.getElementById('server-version').textContent = ver;
        document.getElementById('set-version').textContent = ver;
        document.getElementById('set-commit').textContent = d.gitCommit || '-';
        document.getElementById('set-go').textContent = d.goVersion || '-';
        document.getElementById('set-platform').textContent = (d.os || '') + '/' + (d.arch || '');
    } catch {}
}

function updateFilterOptions() {
    const mfRt = document.getElementById('mf-runtime');
    if (mfRt) {
        const cur = mfRt.value;
        mfRt.innerHTML = '<option value="">' + t('models.filter.runtime') + '</option>' +
            runtimesData.map(function (r) { return '<option value="' + esc(r.id) + '">' + esc(r.name) + '</option>'; }).join('');
        mfRt.value = cur;
    }
    const hfM = document.getElementById('hf-model');
    if (hfM) {
        const cur = hfM.value;
        hfM.innerHTML = '<option value="">' + t('history.filter.model') + '</option>' +
            modelsData.map(function (m) { return '<option value="' + esc(m.id) + '">' + esc(m.name) + '</option>'; }).join('');
        hfM.value = cur;
    }
}

// ─── Utilities ──────────────────────────────────────────────────────────────

function getRuntimeName(id) {
    const r = runtimesData.find(function (x) { return x.id === id; });
    return r ? r.name : (id || '—');
}

function getModelName(id) {
    const m = modelsData.find(function (x) { return x.id === id; });
    return m ? m.name : (id || '—');
}

function getActiveInstances(modelId) {
    return instancesData.filter(function (i) { return i.model_id === modelId && isActive(i.state); });
}

function isActive(s) { return s === 'running' || s === 'starting' || s === 'stopping'; }

function modelStatus(model) {
    const active = getActiveInstances(model.id);
    if (active.length === 0) return 'stopped';
    const states = active.map(function (i) { return i.state; });
    if (states.indexOf('running') !== -1) return 'running';
    if (states.indexOf('starting') !== -1) return 'starting';
    if (states.indexOf('stopping') !== -1) return 'stopping';
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
    if (isNaN(d.getTime()) || d.getUTCFullYear() < 1000) return '—';
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

function iconBtn(icon, label, color, action, id) {
    return '<button class="icon-btn icon-btn-' + color + '" title="' + esc(label) + '" aria-label="' + esc(label) + '" onclick="' + action + '(\'' + safeId(id) + '\')">' + icon + '</button>';
}

function parseArgs(raw) {
    if (!raw) return [];
    return raw.trim().split(/\s+/).filter(Boolean);
}

// ─── Rendering ──────────────────────────────────────────────────────────────

function renderAll() {
    renderModels();
    renderAdvRuntimes();
    renderAdvInstances();
    renderHistory();
    updateLogInstanceSelect();
}

// ─── My Models (compact list) ──────────────────────────────────────────────

function renderModels() {
    const list = document.getElementById('model-list');
    const empty = document.getElementById('models-empty');
    const search = (document.getElementById('mf-search').value || '').toLowerCase();
    const rtFilter = document.getElementById('mf-runtime').value;
    const stateFilter = document.getElementById('mf-state').value;
    const autoOnly = document.getElementById('mf-autostart').checked;

    let filtered = modelsData.filter(function (m) {
        if (search && m.name.toLowerCase().indexOf(search) === -1) return false;
        if (rtFilter && m.runtime_id !== rtFilter) return false;
        if (stateFilter && modelStatus(m) !== stateFilter) return false;
        if (autoOnly && !m.active) return false;
        return true;
    });

    if (filtered.length === 0) {
        list.innerHTML = '';
        empty.style.display = modelsData.length === 0 ? 'block' : 'none';
        if (modelsData.length > 0) {
            list.innerHTML = '<div class="empty-state" style="padding:1.5rem;"><p style="font-size:0.85rem;">No matching models.</p></div>';
        }
        return;
    }
    empty.style.display = 'none';

    list.innerHTML = filtered.map(function (m) {
        const status = modelStatus(m);
        const active = getActiveInstances(m.id);
        const inst = active[0];
        const uptime = inst ? fmtUptime(inst.started_at) : '';

        let actionBtns = '';
        if (status === 'running') {
            actionBtns = iconBtn('↻', t('models.actions.restart'), 'warning', 'restartModel', m.id) +
                iconBtn('■', t('models.actions.stop'), 'danger', 'stopModel', m.id);
        } else if (status === 'starting' || status === 'stopping') {
            actionBtns = '<span class="hint-text">' + status + '...</span>';
        } else {
            actionBtns = iconBtn('▶', t('models.actions.start'), 'success', 'startModel', m.id);
        }

        return '<div class="model-row">' +
            '<div class="model-row-main">' +
                '<div class="model-row-name">' + esc(m.name) + ' <span class="status-badge ' + status + '">' + t('models.status.' + status) + '</span></div>' +
                '<div class="model-row-sub">' + esc(getRuntimeName(m.runtime_id)) +
                    (m.active ? ' <span class="autostart-indicator" title="' + t('models.autostart') + '">A</span>' : '') +
                    (inst && inst.pid ? ' · PID ' + inst.pid : '') +
                    (uptime ? ' · ' + uptime : '') +
                '</div>' +
            '</div>' +
            '<div class="model-row-actions">' +
                actionBtns +
                iconBtn('📄', t('models.actions.logs'), 'ghost', 'viewInstanceLogs', active.length ? active[0].id : '') +
                iconBtn('✎', t('models.actions.edit'), 'ghost', 'openWizard', m.id) +
                iconBtn('🗑', t('models.actions.delete'), 'danger', 'deleteModel', m.id) +
            '</div>' +
        '</div>';
    }).join('');
}

// ─── Model actions ──────────────────────────────────────────────────────────

async function startModel(modelId) {
    try {
        await api('/models/' + modelId + '/start', { method: 'POST' });
        showToast(t('common.loading'), 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast(friendlyError(e), 'error'); }
}

async function stopModel(modelId) {
    try {
        await api('/models/' + modelId + '/stop', { method: 'POST' });
        showToast(t('common.loading'), 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast(friendlyError(e), 'error'); }
}

async function restartModel(modelId) {
    try {
        await api('/models/' + modelId + '/restart', { method: 'POST' });
        showToast(t('common.loading'), 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast(friendlyError(e), 'error'); }
}

function deleteModel(id) {
    const m = modelsData.find(function (x) { return x.id === id; });
    const name = m ? m.name : id;
    const activeInsts = getActiveInstances(id);
    let msg = 'Delete "' + name + '"?';
    if (activeInsts.length > 0) msg += '\n' + activeInsts.length + ' active instance(s) will be stopped.';
    showConfirm(msg, async function () {
        try {
            await api('/models/' + id, { method: 'DELETE' });
            closeConfirm();
            showToast(t('common.deleted'), 'success');
            await reloadAllData(); renderAll();
        } catch (err) {
            if (err.status === 409 && err.details && err.details.length) {
                closeConfirm();
                showBlockedModal(t('blocked.title'), err.details);
            } else {
                showToast(friendlyError(err), 'error');
            }
        }
    });
}

async function toggleAutostart(id) {
    const m = modelsData.find(function (x) { return x.id === id; });
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
    const allInsts = instancesData.slice().sort(function (a, b) {
        const ta = a.started_at ? new Date(a.started_at).getTime() : 0;
        const tb = b.started_at ? new Date(b.started_at).getTime() : 0;
        return tb - ta;
    });
    const logsEmpty = document.getElementById('logs-empty');
    if (logsEmpty) logsEmpty.style.display = allInsts.length === 0 ? 'block' : 'none';
    sel.innerHTML = '<option value="">' + t('logs.select.all') + '</option>' +
        allInsts.map(function (i) {
            const label = getModelName(i.model_id) + ' | ' + i.id.slice(0, 12) + '… | ' + i.state;
            return '<option value="' + esc(i.id) + '">' + esc(label) + '</option>';
        }).join('');
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
    const inst = instancesData.find(function (i) { return i.id === instId; });
    if (!inst) { bar.style.display = 'none'; return; }
    const started = fmtTime(inst.started_at);
    bar.innerHTML = '<span class="log-bar-label">' + t('logs.bar.model') + ':</span> ' + esc(getModelName(inst.model_id)) +
        ' &nbsp;|&nbsp; <span class="log-bar-label">' + t('logs.bar.instance') + ':</span> <code>' + esc(inst.id.slice(0, 16)) + '</code>' +
        ' &nbsp;|&nbsp; <span class="log-bar-label">' + t('logs.bar.pid') + ':</span> ' + (inst.pid || '—') +
        ' &nbsp;|&nbsp; <span class="log-bar-label">' + t('logs.bar.state') + ':</span> <span class="status-badge ' + esc(inst.state) + '">' + esc(inst.state) + '</span>' +
        ' &nbsp;|&nbsp; <span class="log-bar-label">' + t('logs.bar.started') + ':</span> ' + started;
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
    logEs.onerror = function () {};
}

function appendLogLine(d) {
    const stream = d.stream || 'stdout';
    const search = document.getElementById('log-search').value.toLowerCase();
    const filter = document.getElementById('log-stream-filter').value;
    if (filter && stream !== filter) return;
    if (search && (d.message || '').toLowerCase().indexOf(search) === -1) return;

    const view = document.getElementById('log-view');
    const div = document.createElement('div');
    div.className = 'log-line ' + stream;
    const ts = d.time ? new Date(d.time).toLocaleTimeString() : '';
    const instLabel = d.instance_id ? '<span class="log-inst">' + esc(d.instance_id.substring(0, 8)) + '</span>' : '';
    div.innerHTML = '<span class="log-time">' + ts + '</span>' + instLabel + '<span class="log-source">[' + esc(stream) + ']</span>' + esc(d.message);
    view.appendChild(div);

    while (view.children.length > 2000) view.removeChild(view.firstChild);
    if (document.getElementById('log-autoscroll').checked) {
        view.scrollTop = view.scrollHeight;
    }
}

function applyLogSearch() {
    const view = document.getElementById('log-view');
    const search = document.getElementById('log-search').value.toLowerCase();
    const filter = document.getElementById('log-stream-filter').value;
    Array.from(view.children).forEach(function (div) {
        const text = div.textContent.toLowerCase();
        const streamMatch = !filter || div.className.indexOf(filter) !== -1;
        const searchMatch = !search || text.indexOf(search) !== -1;
        div.style.display = (streamMatch && searchMatch) ? '' : 'none';
    });
}

function toggleLogPause() {
    logPaused = !logPaused;
    document.getElementById('log-pause-btn').textContent = logPaused ? t('logs.resume') : t('logs.pause');
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
    document.querySelectorAll('.view').forEach(function (v) { v.classList.remove('active'); });
    const target = document.getElementById('view-' + view);
    if (target) target.classList.add('active');
    document.querySelectorAll('.nav-item').forEach(function (n) { n.classList.remove('active'); });
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
    loadWizardRuntimeCards();

    const titleEl = document.getElementById('wizard-title');
    titleEl.textContent = wizEditId ? t('wizard.title.edit') : t('wizard.title.add');

    if (wizEditId) {
        const m = modelsData.find(function (x) { return x.id === wizEditId; });
        if (m) {
            document.getElementById('wiz-name').value = m.name || '';
            document.getElementById('wiz-args').value = (m.args || []).join('\n');
            document.getElementById('wiz-autostart').checked = !!m.active;
            document.getElementById('wiz-delay-group').style.display = m.active ? '' : 'none';
            document.getElementById('wiz-autostart-delay').value = m.autostart_delay || 0;
            if (m.runtime_id) {
                rtSelectedId = m.runtime_id;
                const r = runtimesData.find(function (x) { return x.id === m.runtime_id; });
                if (r) {
                    document.getElementById('wiz-rt-search').value = r.name;
                    const details = document.getElementById('wiz-rt-selected');
                    details.innerHTML = '<div class="rt-detail"><strong>' + esc(r.name) + '</strong></div>' +
                        (r.executable ? '<div class="rt-detail-sub">exe: ' + esc(r.executable) + '</div>' : '') +
                        (r.working_directory ? '<div class="rt-detail-sub">cwd: ' + esc(r.working_directory) + '</div>' : '');
                    details.style.display = 'block';
                }
            }
        }
    }
    updateWizardStep();
    document.getElementById('wizard-modal').style.display = 'flex';
}

function closeWizard() { document.getElementById('wizard-modal').style.display = 'none'; }

function loadWizardRuntimeCards() {
    document.getElementById('wiz-rt-search').value = '';
    document.getElementById('wiz-rt-selected').style.display = 'none';
    document.getElementById('wiz-rt-dropdown').style.display = 'none';
    renderRtDropdown();
}

var rtSelectedId = null;

function renderRtDropdown() {
    const query = document.getElementById('wiz-rt-search').value.toLowerCase();
    const dd = document.getElementById('wiz-rt-dropdown');
    const filtered = runtimesData.filter(function (r) {
        return !query || r.name.toLowerCase().indexOf(query) !== -1;
    });
    if (filtered.length === 0) {
        dd.innerHTML = '<div class="rt-dropdown-empty">' + esc(t('wizard.rt.none')) + '</div>';
        dd.style.display = 'block';
        return;
    }
    dd.innerHTML = filtered.map(function (r) {
        const sel = r.id === rtSelectedId ? ' selected' : '';
        return '<div class="rt-dropdown-item' + sel + '" data-id="' + esc(r.id) + '" onclick="selectRtItem(\'' + safeId(r.id) + '\')">' + esc(r.name) + '</div>';
    }).join('');
    dd.style.display = 'block';
}

function rtDropdownKeydown(e) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const dd = document.getElementById('wiz-rt-dropdown');
        const items = Array.from(dd.querySelectorAll('.rt-dropdown-item'));
        if (items.length === 0) return;
        let idx = items.findIndex(function (el) { return el.classList.contains('selected'); });
        if (e.key === 'ArrowDown') idx = Math.min(idx + 1, items.length - 1);
        else idx = Math.max(idx - 1, 0);
        items.forEach(function (el) { el.classList.remove('selected'); });
        items[idx].classList.add('selected');
        items[idx].scrollIntoView({ block: 'nearest' });
    } else if (e.key === 'Enter') {
        e.preventDefault();
        const dd = document.getElementById('wiz-rt-dropdown');
        const sel = dd.querySelector('.rt-dropdown-item.selected');
        if (sel) selectRtItem(sel.dataset.id);
    } else if (e.key === 'Escape') {
        document.getElementById('wiz-rt-dropdown').style.display = 'none';
    }
}

function selectRtItem(id) {
    rtSelectedId = id;
    const r = runtimesData.find(function (x) { return x.id === id; });
    if (!r) return;
    document.getElementById('wiz-rt-search').value = r.name;
    document.getElementById('wiz-rt-dropdown').style.display = 'none';
    const details = document.getElementById('wiz-rt-selected');
    details.innerHTML = '<div class="rt-detail"><strong>' + esc(r.name) + '</strong></div>' +
        (r.executable ? '<div class="rt-detail-sub">exe: ' + esc(r.executable) + '</div>' : '') +
        (r.working_directory ? '<div class="rt-detail-sub">cwd: ' + esc(r.working_directory) + '</div>' : '');
    details.style.display = 'block';
}

function onWizRtModeChange() {
    const mode = document.querySelector('input[name=wiz-rt-mode]:checked').value;
    document.getElementById('wiz-rt-existing-section').style.display = mode === 'existing' ? '' : 'none';
    document.getElementById('wiz-rt-new-section').style.display = mode === 'new' ? '' : 'none';
}

function wizGoto(step) {
    wizStep = step;
    updateWizardStep();
}

function wizPrev() { if (wizStep > 1) { wizStep--; updateWizardStep(); } }

function updateWizardStep() {
    document.querySelectorAll('#wizard-steps .wizard-step').forEach(function (s) {
        const n = parseInt(s.dataset.step);
        s.classList.toggle('active', n === wizStep);
        s.classList.toggle('done', n < wizStep);
    });
    document.querySelectorAll('#wizard-modal .wizard-panel').forEach(function (p) {
        p.classList.toggle('active', parseInt(p.dataset.panel) === wizStep);
    });
    document.getElementById('wiz-prev').style.display = wizStep > 1 ? '' : 'none';
    const nextBtn = document.getElementById('wiz-next');
    nextBtn.textContent = wizStep === 3 ? (wizEditId ? t('wizard.btn.save') : t('wizard.btn.create')) : t('wizard.btn.next');
}

async function handleWizardSubmit(e) {
    e.preventDefault();
    if (wizStep < 3) {
        if (wizStep === 1 && !document.getElementById('wiz-name').value.trim()) {
            wizError(t('wizard.error.name'));
            return;
        }
        wizStep++;
        updateWizardStep();
        return;
    }

    const name = document.getElementById('wiz-name').value.trim();
    if (!name) { wizError(t('wizard.error.name')); return; }

    const rtMode = document.querySelector('input[name=wiz-rt-mode]:checked').value;
    let runtimeId = '';
    if (rtMode === 'existing') {
        runtimeId = rtSelectedId || '';
        if (!runtimeId) {
            wizError(t('wizard.error.runtime')); return;
        }
    }

    const submitBtn = document.getElementById('wiz-next');
    if (submitBtn.disabled) return;
    submitBtn.disabled = true;
    const oldLabel = submitBtn.textContent;
    submitBtn.textContent = t('common.loading');

    try {
        if (rtMode === 'new') {
            const rtName = document.getElementById('wiz-rt-name').value.trim();
            const rtExe = document.getElementById('wiz-rt-executable').value.trim();
            if (!rtName || !rtExe) { wizError('Fill runtime fields'); return; }
            const rtBody = { name: rtName, executable: rtExe };
            const rtWd = document.getElementById('wiz-rt-workdir').value.trim();
            if (rtWd) rtBody.working_directory = rtWd;
            const rt = await api('/runtimes', { method: 'POST', body: JSON.stringify(rtBody) });
            runtimeId = rt.id;
        }

        if (!runtimeId) { wizError(t('wizard.error.runtime')); return; }

        const body = {
            name: name,
            runtime_id: runtimeId,
            args: parseArgs(document.getElementById('wiz-args').value),
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
            showToast('"' + name + '" ' + t('common.saved'), 'success');
        } else {
            await api('/models', { method: 'POST', body: JSON.stringify(body) });
            showToast('"' + name + '" created', 'success');
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
    document.querySelectorAll('#' + containerId + ' .env-row').forEach(function (row) {
        const inputs = row.querySelectorAll('input');
        if (inputs[0] && inputs[0].value.trim()) env[inputs[0].value.trim()] = inputs[1] ? inputs[1].value : '';
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
    document.getElementById('adv-runtimes-body').innerHTML = runtimesData.map(function (r) {
        return '<tr><td>' + esc(r.name) + '</td><td>' + esc(r.executable) + '</td><td>' + esc(r.working_directory || '—') + '</td>' +
            '<td class="actions-cell"><button class="btn btn-ghost btn-sm" onclick="editRuntime(\'' + safeId(r.id) + '\')">' + t('runtimes.actions.edit') + '</button> ' +
            '<button class="btn btn-danger btn-sm" onclick="deleteRuntime(\'' + safeId(r.id) + '\')">' + t('runtimes.actions.delete') + '</button></td></tr>';
    }).join('');
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
    const env = collectEnvRows('rt-create-env-container');
    if (Object.keys(env).length) body.environment = env;
    try {
        await api('/runtimes', { method: 'POST', body: JSON.stringify(body) });
        closeModal('create-runtime-modal');
        showToast(t('common.saved'), 'success');
        await reloadAllData(); renderAll();
    } catch (err) { showToast(friendlyError(err), 'error'); }
    return false;
}

function editRuntime(id) {
    const r = runtimesData.find(function (x) { return x.id === id; });
    if (!r) return;
    const modal = document.getElementById('edit-runtime-modal');
    const f = modal.querySelector('form');
    f.querySelector('[name=id]').value = r.id;
    f.querySelector('[name=name]').value = r.name;
    f.querySelector('[name=executable]').value = r.executable;
    f.querySelector('[name=working_directory]').value = r.working_directory || '';
    const envC = document.getElementById('rt-edit-env-container');
    envC.innerHTML = '';
    if (r.environment_keys && r.environment_keys.length) {
        r.environment_keys.forEach(function (k) {
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
            i2.placeholder = 'value (write-only)';
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
    const env = collectEnvRows('rt-edit-env-container');
    if (Object.keys(env).length) body.environment = env;
    try {
        await api('/runtimes/' + f.querySelector('[name=id]').value, { method: 'PUT', body: JSON.stringify(body) });
        closeModal('edit-runtime-modal');
        showToast(t('common.saved'), 'success');
        await reloadAllData(); renderAll();
    } catch (err) { showToast(friendlyError(err), 'error'); }
    return false;
}

// ─── Runtime delete (replace / cascade) ─────────────────────────────────────

let rtDeleteId = null;

function deleteRuntime(id) {
    const rt = runtimesData.find(function (x) { return x.id === id; });
    if (!rt) return;
    const depModels = modelsData.filter(function (m) { return m.runtime_id === id; });
    if (depModels.length > 0) {
        rtDeleteId = id;
        const content = document.getElementById('rt-delete-content');
        content.innerHTML = '<p><strong>' + esc(rt.name) + '</strong></p>' +
            '<p style="margin-top:0.5rem;">' + t('runtimes.delete.dependents', { count: depModels.length }) + '</p>' +
            '<ul style="margin:0.5rem 0 0.5rem 1.2rem;font-size:0.88rem;color:var(--text-secondary);">' +
            depModels.map(function (m) { return '<li>' + esc(m.name) + '</li>'; }).join('') + '</ul>';
        const otherRt = runtimesData.filter(function (r) { return r.id !== id; });
        document.getElementById('rt-delete-replace-btn').style.display = otherRt.length > 0 ? '' : 'none';
        document.getElementById('rt-delete-modal').style.display = 'flex';
    } else {
        showConfirm(t('runtimes.delete.confirm', { name: rt.name }), async function () {
            try {
                await api('/runtimes/' + id, { method: 'DELETE' });
                closeConfirm();
                showToast(t('common.deleted'), 'success');
                await reloadAllData(); renderAll();
            } catch (err) {
                showToast(friendlyError(err), 'error');
            }
        });
    }
}

function closeRTDelete() {
    document.getElementById('rt-delete-modal').style.display = 'none';
}

function openRTReplace() {
    if (!rtDeleteId) return;
    closeRTDelete();
    const depCount = modelsData.filter(function (m) { return m.runtime_id === rtDeleteId; }).length;
    document.getElementById('rt-replace-msg').textContent = t('runtimes.replace.select', { count: depCount });
    const sel = document.getElementById('rt-replace-select');
    const otherRt = runtimesData.filter(function (r) { return r.id !== rtDeleteId; });
    sel.innerHTML = otherRt.map(function (r) {
        return '<option value="' + esc(r.id) + '">' + esc(r.name) + '</option>';
    }).join('');
    document.getElementById('rt-replace-modal').style.display = 'flex';
}

function closeRTReplace() {
    document.getElementById('rt-replace-modal').style.display = 'none';
}

async function confirmRTReplace() {
    const newId = document.getElementById('rt-replace-select').value;
    if (!newId || !rtDeleteId) return;
    const btn = document.getElementById('rt-replace-btn');
    btn.disabled = true;
    btn.textContent = t('confirm.executing');
    try {
        await api('/runtimes/' + rtDeleteId + '/replace', { method: 'POST', body: JSON.stringify({ new_runtime_id: newId }) });
        rtDeleteId = null;
        closeRTReplace();
        showToast(t('common.saved'), 'success');
        await reloadAllData(); renderAll();
    } catch (err) {
        showToast(friendlyError(err), 'error');
    }
    btn.disabled = false;
    btn.textContent = t('runtimes.replace.confirm');
}

function openRTCascade() {
    if (!rtDeleteId) return;
    closeRTDelete();
    const depCount = modelsData.filter(function (m) { return m.runtime_id === rtDeleteId; }).length;
    document.getElementById('rt-cascade-msg').textContent = t('runtimes.cascade.confirm', { count: depCount });
    document.getElementById('rt-cascade-modal').style.display = 'flex';
}

function closeRTCascade() { document.getElementById('rt-cascade-modal').style.display = 'none'; }

async function confirmRTCascade() {
    if (!rtDeleteId) return;
    const btn = document.getElementById('rt-cascade-btn');
    btn.disabled = true;
    btn.textContent = t('confirm.executing');
    try {
        const res = await api('/runtimes/' + rtDeleteId + '/cascade-delete', { method: 'POST' });
        rtDeleteId = null;
        closeRTCascade();
        showToast(t('common.deleted') + (res.models_deleted ? ' (' + res.models_deleted + ' models)' : ''), 'success');
        await reloadAllData(); renderAll();
    } catch (err) {
        showToast(friendlyError(err), 'error');
    }
    btn.disabled = false;
    btn.textContent = t('runtimes.cascade.btn');
}

// ─── Advanced: Instances ────────────────────────────────────────────────────

function renderAdvInstances() {
    const empty = document.getElementById('instances-empty');
    const active = instancesData.filter(function (i) { return isActive(i.state); });
    if (active.length === 0) {
        document.getElementById('adv-instances-body').innerHTML = '<tr class="empty-row"><td colspan="8"><div class="empty-state"><p>' + esc(t('instances.empty')) + '</p><small class="hint-text">' + esc(t('instances.empty.hint')) + '</small></div></td></tr>';
        return;
    }
    if (empty) empty.style.display = 'none';
    document.getElementById('adv-instances-body').innerHTML = active.map(function (i) {
        return '<tr><td style="font-family:var(--font-mono);font-size:0.78rem;">' + esc(i.id.slice(0, 16)) + '</td>' +
            '<td>' + esc(getModelName(i.model_id)) + '</td>' +
            '<td><span class="status-badge ' + esc(i.state) + '">' + esc(i.state) + '</span></td>' +
            '<td>' + (i.pid || '—') + '</td><td>' + fmtTime(i.started_at) + '</td><td>' + fmtTime(i.stopped_at) + '</td>' +
            '<td>' + (i.exit_code != null ? i.exit_code : '—') + '</td>' +
            '<td class="actions-cell"><button class="btn btn-ghost btn-sm" onclick="viewInstanceLogs(\'' + safeId(i.id) + '\')">' + t('instances.actions.logs') + '</button>' +
            ' <button class="btn btn-danger btn-sm" onclick="stopInstance(\'' + safeId(i.id) + '\')">' + t('instances.actions.stop') + '</button> ' +
            '<button class="btn btn-warning btn-sm" onclick="restartInstance(\'' + safeId(i.id) + '\')">' + t('instances.actions.restart') + '</button>' + '</td></tr>';
    }).join('');
}

async function stopInstance(id) {
    try { await api('/instances/' + id + '/stop', { method: 'POST' }); await reloadAllData(); renderAll(); }
    catch (err) { showToast(friendlyError(err), 'error'); }
}

async function restartInstance(id) {
    try { await api('/instances/' + id + '/restart', { method: 'POST' }); await reloadAllData(); renderAll(); }
    catch (err) { showToast(friendlyError(err), 'error'); }
}

// ─── Instance History (with filters) ────────────────────────────────────────

function isTerminal(s) { return s === 'exited' || s === 'failed' || s === 'stale'; }

function renderHistory() {
    const empty = document.getElementById('history-empty');
    const stateF = document.getElementById('hf-state').value;
    const modelF = document.getElementById('hf-model').value;

    let filtered = instancesData.filter(function (i) {
        if (!isTerminal(i.state)) return false;
        if (stateF && i.state !== stateF) return false;
        if (modelF && i.model_id !== modelF) return false;
        return true;
    });
    filtered.sort(function (a, b) {
        const ta = a.started_at ? new Date(a.started_at).getTime() : 0;
        const tb = b.started_at ? new Date(b.started_at).getTime() : 0;
        return tb - ta;
    });

    if (filtered.length === 0) {
        document.getElementById('history-body').innerHTML = '<tr class="empty-row"><td colspan="8"><div class="empty-state"><p>' + esc(t('history.empty')) + '</p><small class="hint-text">' + esc(t('history.empty.hint')) + '</small></div></td></tr>';
        return;
    }
    if (empty) empty.style.display = 'none';
    document.getElementById('history-body').innerHTML = filtered.slice(0, 200).map(function (i) {
        return '<tr><td>' + esc(getModelName(i.model_id)) + '</td>' +
            '<td style="font-family:var(--font-mono);font-size:0.78rem;">' + esc(i.id.slice(0, 12)) + '</td>' +
            '<td><span class="status-badge ' + esc(i.state) + '">' + esc(i.state) + '</span></td>' +
            '<td>' + (i.pid || '—') + '</td>' +
            '<td>' + fmtTime(i.started_at) + '</td><td>' + fmtTime(i.stopped_at) + '</td>' +
            '<td>' + (i.exit_code != null ? i.exit_code : '—') + '</td>' +
            '<td class="actions-cell"><button class="btn btn-ghost btn-sm" onclick="viewInstanceLogs(\'' + safeId(i.id) + '\')">' + t('instances.actions.logs') + '</button></td></tr>';
    }).join('');
}

// ─── Instance Cleanup ───────────────────────────────────────────────────────

function openCleanup() {
    document.querySelectorAll('input[name=cleanup-mode]').forEach(function (r) { r.checked = r.value === 'all_terminal'; });
    document.getElementById('cleanup-modal').style.display = 'flex';
}

function closeCleanup() { document.getElementById('cleanup-modal').style.display = 'none'; }

async function confirmCleanup() {
    const mode = document.querySelector('input[name=cleanup-mode]:checked').value;
    const body = { mode: mode };
    try {
        const res = await api('/instances/cleanup', { method: 'POST', body: JSON.stringify(body) });
        closeCleanup();
        showToast(t('instances.cleanup.result', { count: res.deleted }), 'success');
        await reloadAllData(); renderAll();
    } catch (err) {
        showToast(friendlyError(err), 'error');
    }
}

// ─── Settings ───────────────────────────────────────────────────────────────

async function loadSettings() {
    try {
        const m = await api('/metrics');
        document.getElementById('set-listen').textContent = (m.listen_address || '127.0.0.1') + ':' + (m.web_port || '');
        document.getElementById('set-auth').textContent = m.auth_enabled ? t('settings.server.auth.on') : t('settings.server.auth.off');
    } catch {}
}

// ─── Error messages ─────────────────────────────────────────────────────────

function friendlyError(err) {
    let msg = (err && err.message) || 'Unknown error';
    const patterns = [
        [/model not found/i, 'Model not found. It may have been deleted.'],
        [/runtime not found/i, 'Runtime not found. It may have been deleted.'],
        [/instance not found/i, 'Instance not found.'],
        [/in use|referenced|depend/i, 'Object is used by other records and cannot be deleted.'],
        [/port.*in use|address already in use/i, 'Port is already in use by another process.'],
        [/executable.*not found|no such file/i, 'Executable not found. Check the path.'],
    ];
    for (let i = 0; i < patterns.length; i++) {
        if (patterns[i][0].test(msg)) return patterns[i][1];
    }
    return msg.charAt(0).toUpperCase() + msg.slice(1);
}

// ─── Modal helpers ──────────────────────────────────────────────────────────

function closeModal(id) { document.getElementById(id).style.display = 'none'; }

function showConfirm(msg, onYes) {
    const el = document.getElementById('confirm-message');
    el.textContent = '';
    el.style.whiteSpace = 'pre-line';
    msg.split('\n').forEach(function (line, idx) {
        if (idx > 0) el.appendChild(document.createElement('br'));
        el.appendChild(document.createTextNode(line));
    });
    const btn = document.getElementById('confirm-yes');
    btn.disabled = false;
    btn.textContent = t('common.confirm');
    btn.onclick = async function () {
        btn.disabled = true;
        btn.textContent = t('confirm.executing');
        try {
            await onYes();
        } finally {
            btn.disabled = false;
            btn.textContent = t('common.confirm');
        }
    };
    document.getElementById('confirm-modal').style.display = 'flex';
}

function closeConfirm() { document.getElementById('confirm-modal').style.display = 'none'; }

function showBlockedModal(title, dependents) {
    const content = document.getElementById('rt-delete-content');
    let html = '<p><strong>' + esc(title) + '</strong></p>';
    html += '<p class="hint-text" style="margin-top:0.8rem;">' + t('blocked.used_by') + '</p><ul style="margin:0.5rem 0 0.5rem 1.2rem;font-size:0.88rem;color:var(--text-secondary);">';
    dependents.forEach(function (d) { html += '<li>' + esc(d) + '</li>'; });
    html += '</ul><p class="hint-text" style="margin-top:0.8rem;">' + t('blocked.hint') + '</p>';
    content.innerHTML = html;
    document.getElementById('rt-delete-modal').style.display = 'flex';
}

// ─── Toast ──────────────────────────────────────────────────────────────────

function showToast(msg, type) {
    const div = document.createElement('div');
    div.className = 'toast ' + (type || '');
    div.textContent = msg;
    document.getElementById('toast-container').appendChild(div);
    setTimeout(function () { div.remove(); }, 4000);
}

// ─── Refresh loop ───────────────────────────────────────────────────────────

function startRefresh() {
    if (refreshTimer) clearInterval(refreshTimer);
    refreshTimer = setInterval(async function () {
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
window.wizGoto = wizGoto;
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
window.reloadAllData = reloadAllData;
window.addWizEnvRow = addWizEnvRow;
window.addEnvRow = addEnvRow;
window.onWizRtModeChange = onWizRtModeChange;
window.setTheme = setTheme;
window.setLanguage = setLanguage;
window.selectRtCard = selectRtCard;
window.renderModels = renderModels;
window.renderHistory = renderHistory;
window.closeRTDelete = closeRTDelete;
window.openRTReplace = openRTReplace;
window.closeRTReplace = closeRTReplace;
window.confirmRTReplace = confirmRTReplace;
window.openRTCascade = openRTCascade;
window.closeRTCascade = closeRTCascade;
window.confirmRTCascade = confirmRTCascade;
window.openCleanup = openCleanup;
window.closeCleanup = closeCleanup;
window.confirmCleanup = confirmCleanup;

// ─── Boot ───────────────────────────────────────────────────────────────────

init();
})();

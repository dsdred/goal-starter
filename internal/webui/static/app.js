(function () {
'use strict';

let csrfToken = '';
let currentView = 'models';
let logEs = null;
let logPaused = false;
let refreshTimer = null;
let lastLogSeq = 0;
let currentLogInstance = '';

// Client-side display window for the live log view: at most this many
// rendered .log-line elements are kept in #log-view. Older lines are trimmed;
// the full history stays queryable through the API (server-side retention is
// unchanged).
const LOG_VIEW_MAX_LINES = 2000;

let runtimesData = [];
let modelsData = [];
let instancesData = [];
let historyData = [];
let pipelinesData = [];

let wizStep = 1;
let wizEditId = null;

let i18nDict = {};
let currentLang = localStorage.getItem('goal_lang') || 'ru';
let currentTheme = localStorage.getItem('goal_theme') || 'system';
let versionInfo = {};

// i18nMissing tracks every key looked up while absent from the current
// dictionary. A raw key reaching the UI means a missing translation; the
// browser regression suites fail on a non-empty set.
let i18nMissing = {};

// ─── i18n ───────────────────────────────────────────────────────────────────

function t(key, params) {
    let s = i18nDict[key];
    if (s === undefined) {
        i18nMissing[key] = true;
        s = key;
    }
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
    document.querySelectorAll('[data-i18n-tooltip]').forEach(function (el) {
        el.setAttribute('data-tooltip', t(el.dataset.i18nTooltip));
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

async function setLanguage(lang) {
    currentLang = lang;
    localStorage.setItem('goal_lang', lang);
    const setLang = document.getElementById('set-lang');
    if (setLang) setLang.value = lang;
    await loadI18n(lang);
    // Re-render the JS-generated views (lists, menus, chips, badges) so they
    // follow the new language too; applyI18n above only refreshes static
    // [data-i18n] labels, which would otherwise leave dynamic content in the
    // previous language after a switch.
    renderAll();
    // Dynamic DOM created after the initial applyI18n must be re-localized for
    // the NEW locale immediately, otherwise it keeps the previous language's
    // text (or, if a key was unknown at creation time, the raw key itself).
    // This covers: the pipeline builder (plEntries render), the wizard runtime
    // dropdown + step button label, and every open tooltip surface.
    if (isWizardOpen()) {
        renderRtDropdown();
        updateWizardStep();
    }
    if (isPipelineModalOpen()) renderPlBuilder();
    if (document.getElementById('pipeline-modal').style.display === 'flex') {
        document.getElementById('pipeline-modal-title').textContent =
            document.getElementById('pipeline-form').id.value ? t('pipelines.edit.title') : t('pipelines.create.title');
    }
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
    const setThemeEl = document.getElementById('set-theme');
    if (setThemeEl) setThemeEl.value = theme;
}

// ─── Init ───────────────────────────────────────────────────────────────────

async function init() {
    applyTheme();
    const setThemeEl = document.getElementById('set-theme');
    if (setThemeEl) setThemeEl.value = currentTheme;
    const setLangEl = document.getElementById('set-lang');
    if (setLangEl) setLangEl.value = currentLang;
    await loadI18n(currentLang);
    startHealthMonitor();
    bindTooltipSystem();
    bindFitObservers();
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
        el.style.display = 'none';
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
            showLoginError(data.error ? translateServerMessage(data.error) : t('auth.login.failed'));
        }
    } catch (err) {
        showLoginError(t('auth.login.network_error'));
    }
    return false;
}

async function handleLogout() {
    try { await fetch('/api/v1/auth/logout', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken } }); } catch {}
    csrfToken = '';
    updateSidebarAuth(null);
    resetLoginState();
    closeDrawer();
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
        let code = '';
        try {
            const d = await r.json();
            msg = d.error || msg;
            code = d.code || '';
            if (Array.isArray(d.details)) details = d.details;
        } catch {}
        const err = new Error(msg);
        err.status = r.status;
        err.details = details;
        err.code = code;
        throw err;
    }
    if (r.status === 204) return null;
    return r.json();
}

// ─── Data loading ───────────────────────────────────────────────────────────

async function reloadAllData() {
    const [rt, mo, ins, hist, pl] = await Promise.allSettled([
        api('/runtimes'), api('/models'), api('/instances'), api('/history'), api('/pipelines')
    ]);
    runtimesData = rt.status === 'fulfilled' ? rt.value : [];
    modelsData = mo.status === 'fulfilled' ? mo.value : [];
    instancesData = ins.status === 'fulfilled' ? ins.value : [];
    historyData = hist.status === 'fulfilled' ? hist.value : [];
    pipelinesData = pl.status === 'fulfilled' ? pl.value : [];
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

function getOrphanInstances(modelId) {
    return instancesData.filter(function (i) { return i.model_id === modelId && i.state === 'orphan'; });
}

function isActive(s) { return s === 'running' || s === 'starting' || s === 'stopping'; }

function modelStatus(model) {
    const active = getActiveInstances(model.id);
    if (active.length > 0) {
        const states = active.map(function (i) { return i.state; });
        if (states.indexOf('running') !== -1) return 'running';
        if (states.indexOf('starting') !== -1) return 'starting';
        if (states.indexOf('stopping') !== -1) return 'stopping';
    }
    if (instancesData.some(function (i) { return i.model_id === model.id && i.state === 'pending'; })) return 'pending';
    if (getOrphanInstances(model.id).length > 0) return 'orphan';
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

function isZeroDate(d) {
    return isNaN(d.getTime()) || d.getUTCFullYear() < 1000;
}

function fmtTime(ts) {
    if (!ts) return '—';
    const d = new Date(ts);
    if (isZeroDate(d)) return '—';
    return d.toLocaleString();
}

function fmtHM(ts) {
    const d = new Date(ts);
    return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0');
}

function fmtMD(ts) {
    const d = new Date(ts);
    return (d.getMonth() + 1) + '.' + d.getDate();
}

function sameDay(a, b) {
    const da = new Date(a), db = new Date(b);
    return da.getFullYear() === db.getFullYear() && da.getMonth() === db.getMonth() && da.getDate() === db.getDate();
}

function fmtRange(a, b) {
    if (!a || !b) return '';
    const da = new Date(a), db = new Date(b);
    if (isZeroDate(da) || isZeroDate(db)) return '';
    if (sameDay(a, b)) return fmtHM(a) + '→' + fmtHM(b);
    return fmtMD(a) + ' ' + fmtHM(a) + '→' + fmtMD(b) + ' ' + fmtHM(b);
}

function esc(s) {
    const d = document.createElement('div');
    d.textContent = s || '';
    return d.innerHTML;
}

// Compact instance ID for display. The differentiating part of an instance
// id is its per-launch suffix (timestamp-seq), while the head is the model id
// prefix shared by every instance of the same model — so the tail is kept and
// the head elided. Display-only: routing, option values and API identity
// always use the full id. The full id stays reachable via title attributes.
// The optional `short` form keeps an 8-char tail for dense per-line contexts
// (log-line badges); it derives from the same suffix logic and stays
// distinguishing for same-model instances (the seq suffix is unique
// process-locally).
function compactInstanceId(id, short) {
    return id ? '…' + String(id).slice(-(short ? 8 : 16)) : '';
}

function safeId(id) {
    return String(id).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

// iconBtn renders the canonical action icon button. Help text is carried by
// data-tooltip (the single canonical tooltip system) + aria-label; a native
// title attribute is never set, so no native browser tooltip can appear
// alongside the custom one.
function iconBtn(icon, label, color, action, id) {
    return '<button class="icon-btn icon-btn-' + color + '" data-tooltip="' + esc(label) + '" aria-label="' + esc(label) + '" onclick="' + action + '(\'' + safeId(id) + '\')">' + icon + '</button>';
}

var ICONS = {
    start: '<svg viewBox="0 0 24 24"><path d="M8 5v14l11-7z"/></svg>',
    stop: '<svg viewBox="0 0 24 24"><rect x="6" y="6" width="12" height="12" rx="1"/></svg>',
    restart: '<svg viewBox="0 0 24 24"><path d="M17.65 6.35A7.96 7.96 0 0012 4c-4.42 0-7.99 3.58-7.99 8s3.57 8 7.99 8c3.73 0 6.84-2.55 7.73-6h-2.08A5.99 5.99 0 0112 18c-3.31 0-6-2.69-6-6s2.69-6 6-6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z"/></svg>',
    logs: '<svg viewBox="0 0 24 24"><path d="M3 18h12v-2H3v2zM3 6v2h18V6H3zm0 7h18v-2H3v2z"/></svg>',
    edit: '<svg viewBox="0 0 24 24"><path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04a1 1 0 000-1.41l-2.34-2.34a1 1 0 00-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z"/></svg>',
    del: '<svg viewBox="0 0 24 24"><path d="M6 19c0 1.1.9 2 2 2h8a2 2 0 002-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>',
    kill: '<svg viewBox="0 0 24 24"><path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/></svg>',
    drag: '<svg viewBox="0 0 24 24"><path d="M11 18c0 1.1-.9 2-2 2s-2-.9-2-2 .9-2 2-2 2 .9 2 2zm-2-8c-1.1 0-2 .9-2 2s.9 2 2 2 2-.9 2-2-.9-2-2-2zm0-6c-1.1 0-2 .9-2 2s.9 2 2 2 2-.9 2-2-.9-2-2-2zm6 4c1.1 0 2-.9 2-2s-.9-2-2-2-2 .9-2 2 .9 2 2 2zm0 6c1.1 0 2-.9 2-2s-.9-2-2-2-2 .9-2 2 .9 2 2 2zm0 6c1.1 0 2-.9 2-2s-.9-2-2-2-2 .9-2 2 .9 2 2 2z"/></svg>'
};

// parseArgs splits a launch-args string into argv tokens (ADR 013 D7). Shared by
// the pipeline editor (Custom mode) and the model wizard, so both always agree.
//
// The textarea is a command-line representation: parseArgs turns it into the
// exact []string argv tokens passed to exec.Command (Go re-escapes each token
// for the OS, so tokens must hold the real value, with no quoting). Rules:
//   • tokens are separated by UNQUOTED whitespace (space/tab/newline);
//   • a double-quoted section "..." keeps its inner whitespace in one token and
//     the quotes themselves are removed (e.g. -m "E:\models\my model.gguf" stays
//     one argument);
//   • INSIDE double quotes a backslash escapes the next character: \" → " and
//     \\ → \; any other backslash is kept literally, so Windows paths and JSON
//     survive ({"a\":\"b\"} inside quotes → {"a":"b"});
//   • OUTSIDE quotes a backslash is a literal character (Windows paths);
//   • an empty quoted section "" yields an empty token;
//   • all other characters, including Unicode, pass through verbatim.
// Example: --chat-template-kwargs "{\"reasoning_effort\":\"medium\"}" →
//   ["--chat-template-kwargs", "{\"reasoning_effort\":\"medium\"}"]
//   (second token value is {"reasoning_effort":"medium"}).
function parseArgs(raw) {
    if (!raw) return [];
    const s = String(raw);
    const out = [];
    let cur = '';
    let inQ = false;
    let tok = false;
    for (let i = 0; i < s.length; i++) {
        const c = s[i];
        if (inQ) {
            if (c === '\\' && i + 1 < s.length) {
                const n = s[i + 1];
                if (n === '"' || n === '\\') { cur += n; i++; } else { cur += c; }
            } else if (c === '"') {
                inQ = false;
            } else {
                cur += c;
            }
            tok = true;
        } else if (c === '"') {
            inQ = true;
            tok = true;
        } else if (/\s/.test(c)) {
            if (tok) { out.push(cur); cur = ''; tok = false; }
        } else {
            cur += c;
            tok = true;
        }
    }
    if (tok) out.push(cur);
    return out;
}

// renderArgs serializes argv []string back to a command-line text representation
// for the textarea. Guarantee: parseArgs(renderArgs(args)) deep-equals args.
// Tokens that contain whitespace, double quotes, or are empty get quoted with
// inner quotes/backslashes escaped. All other tokens pass through unquoted.
function renderArgs(args) {
    if (!args || args.length === 0) return '';
    return args.map(function (tok) {
        if (tok === '') return '""';
        if (/[ "\t\n]/.test(tok)) {
            return '"' + tok.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
        }
        return tok;
    }).join(' ');
}

// ─── Rendering ──────────────────────────────────────────────────────────────

function renderAll() {
    renderModels();
    renderPipelines();
    renderAdvRuntimes();
    renderAdvInstances();
    renderHistory();
    updateLogInstanceSelect();
    // Table labels change width with language/data: re-evaluate the shared
    // TABLE-iff-fits contract after the whole render pass.
    syncAllFit();
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
            list.innerHTML = '<div class="empty-state" style="padding:1.5rem;"><p style="font-size:0.85rem;">' + esc(t('models.filter.no_match')) + '</p></div>';
        }
        return;
    }
    empty.style.display = 'none';

    list.innerHTML = filtered.map(function (m) {
        const status = modelStatus(m);
        const active = getActiveInstances(m.id);
        const orphan = getOrphanInstances(m.id);
        const inst = active[0] || orphan[0];
        const uptime = (inst && isActive(inst.state)) ? fmtUptime(inst.started_at) : '';

        let actionBtns = '';
        if (status === 'running') {
            actionBtns = iconBtn(ICONS.restart, t('models.actions.restart'), 'warning', 'restartModel', m.id) +
                iconBtn(ICONS.stop, t('models.actions.stop'), 'danger', 'stopModel', m.id);
        } else if (status === 'starting' || status === 'stopping') {
            actionBtns = '<span class="hint-text model-transitional">' + t('models.status.' + status) + '</span>';
        } else if (status === 'orphan') {
            actionBtns = '<span class="hint-text model-transitional">' + t('instances.orphan.hint') + '</span>';
        } else {
            actionBtns = iconBtn(ICONS.start, t('models.actions.start'), 'success', 'startModel', m.id);
        }

        const autoBadge = m.active ? '<span class="autostart-indicator" title="' + esc(t('models.autostart')) + '">A</span>' : '';
        const badgeTitle = status === 'orphan' ? ' title="' + esc(t('instances.orphan.hint')) + '"' : '';

        return '<div class="model-row">' +
            '<div class="model-row-main">' +
                '<div class="model-row-name">' +
                    autoBadge +
                    '<span class="status-badge ' + status + '"' + badgeTitle + '>' + t('models.status.' + status) + '</span>' +
                    '<span class="model-row-title">' + esc(m.name) + '</span>' +
                '</div>' +
                '<div class="model-row-sub">' +
                    esc(getRuntimeName(m.runtime_id)) +
                    (inst && inst.pid ? ' &middot; PID ' + inst.pid : '') +
                    (uptime ? ' &middot; ' + uptime : '') +
                '</div>' +
            '</div>' +
            '<div class="model-row-actions">' +
                actionBtns +
                iconBtn(ICONS.logs, t('models.actions.logs'), 'ghost', 'viewInstanceLogs', inst ? inst.id : '') +
                iconBtn(ICONS.edit, t('models.actions.edit'), 'ghost', 'openWizard', m.id) +
                iconBtn(ICONS.del, t('models.actions.delete'), 'danger', 'deleteModel', m.id) +
            '</div>' +
        '</div>';
    }).join('');
}

// ─── Model actions ──────────────────────────────────────────────────────────

async function startModel(modelId) {
    try {
        await api('/models/' + modelId + '/start', { method: 'POST' });
        showToast(t('common.started'), 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast(friendlyError(e), 'error'); }
}

async function stopModel(modelId) {
    try {
        await api('/models/' + modelId + '/stop', { method: 'POST' });
        showToast(t('common.stopped'), 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast(friendlyError(e), 'error'); }
}

async function restartModel(modelId) {
    try {
        await api('/models/' + modelId + '/restart', { method: 'POST' });
        showToast(t('common.restarted'), 'success');
        await reloadAllData(); renderAll();
    } catch (e) { showToast(friendlyError(e), 'error'); }
}

function deleteModel(id) {
    const m = modelsData.find(function (x) { return x.id === id; });
    const name = m ? m.name : id;
    const activeInsts = getActiveInstances(id);
    let msg = t('models.delete.confirm', { name: name });
    if (activeInsts.length > 0) msg += '\n' + t('models.delete.active_warning', { count: activeInsts.length });
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

// ─── Pipelines (ADR 010) ───────────────────────────────────────────────────

// plEntries is the single source of truth for the linear pipeline builder (the
// Scratch-inspired block list). Each entry maps to one PipelineModel: id (stable
// on edit), model_id, argsMode ('model' | 'custom'), argsText (the raw custom
// args string), and autoStart (per-entry autostart). The builder re-renders from
// this array on every structural change so connectors and order stay consistent.
let plEntries = [];

function plNewEntry(modelId, mode, argsText, autoStart, entryId) {
    return { id: entryId || '', model_id: modelId || '', argsMode: mode || 'model', argsText: argsText || '', autoStart: !!autoStart };
}

// pipelineEntryStatus is the client-side mirror of the detail endpoint
// (ADR 013 D5): an entry's state is resolved by (pipeline_id, pipeline_entry_id)
// with the D4 legacy fallback (an owned instance without entry attribution
// resolves to the first entry of its model in list order).
function pipelineEntryStatus(p, entry, idx) {
    const owned = instancesData.filter(function (i) { return i.pipeline_id === p.id; });
    let list = owned.filter(function (i) { return i.pipeline_entry_id && i.pipeline_entry_id === entry.id; });
    if (list.length === 0) {
        const firstOfModel = p.models.findIndex(function (m) { return m.model_id === entry.model_id; });
        if (firstOfModel === idx) {
            const consumed = {};
            for (let k = 0; k < idx; k++) consumed[p.models[k].model_id] = true;
            if (!consumed[entry.model_id]) {
                list = owned.filter(function (i) { return !i.pipeline_entry_id && i.model_id === entry.model_id; });
            }
        }
    }
    const active = list.filter(function (i) { return isActive(i.state); });
    let st = 'stopped';
    if (active.length > 0) {
        const states = active.map(function (i) { return i.state; });
        if (states.indexOf('running') !== -1) st = 'running';
        else if (states.indexOf('starting') !== -1) st = 'starting';
        else if (states.indexOf('stopping') !== -1) st = 'stopping';
        else st = active[0].state;
    } else if (list.some(function (i) { return i.state === 'pending'; })) {
        st = 'pending';
    } else if (list.some(function (i) { return i.state === 'failed'; })) {
        st = 'failed';
    } else if (list.some(function (i) { return i.state === 'orphan'; })) {
        st = 'orphan';
    }
    return st;
}

// pipelineChips renders one chip per entry (ADR 013 D7): a repeated model
// appears once per entry (each chip carries the model name and its own
// per-entry status); chips beyond a display budget collapse to +N with a tooltip.
function pipelineChips(p) {
    const shown = p.models.slice(0, 6);
    const rest = p.models.slice(6);
    let html = shown.map(function (m, i) {
        const st = pipelineEntryStatus(p, m, i);
        const name = getModelName(m.model_id);
        return '<span class="status-badge ' + st + '" title="' + esc(name) + '">' + esc(name) + '</span>';
    }).join(' ');
    if (rest.length > 0) {
        const names = rest.map(function (m) { return getModelName(m.model_id); }).join(', ');
        html += ' <span class="pl-chip-more" title="' + esc(t('pipelines.chip.more_title', { names: names })) + '">' + esc(t('pipelines.chip.more', { count: rest.length })) + '</span>';
    }
    return html;
}

// pipelineAggregateStatus is a pure projection of per-entry states (ADR 013 D7):
// any active → the dominant active state; else any failed/orphan; else stopped.
// No new backend state is introduced.
function pipelineAggregateStatus(p) {
    const sts = p.models.map(function (m, i) { return pipelineEntryStatus(p, m, i); });
    if (sts.indexOf('running') !== -1) return 'running';
    if (sts.indexOf('starting') !== -1) return 'starting';
    if (sts.indexOf('stopping') !== -1) return 'stopping';
    if (sts.indexOf('pending') !== -1) return 'pending';
    if (sts.indexOf('failed') !== -1) return 'failed';
    if (sts.indexOf('orphan') !== -1) return 'orphan';
    return 'stopped';
}

function pipelineStateBadge(st) {
    return '<span class="status-badge ' + st + '">' + t('models.status.' + st) + '</span>';
}

function pipelineHasOwnedActive(p) {
    return instancesData.some(function (i) { return i.pipeline_id === p.id && isActive(i.state); });
}

function pipelineActiveToggle(p) {
    return '<label class="filter-switch" title="' + esc(t('pipelines.field.active_hint')) + '">' +
        '<span class="toggle-switch toggle-pipeline"><input type="checkbox"' + (p.active ? ' checked' : '') +
        ' onchange="togglePipelineActive(\'' + safeId(p.id) + '\', this.checked)"><span class="toggle-slider"></span></span></label>';
}

// pipelineActionStrip is the Models-style inline action strip (ADR 013 UX):
// state-driven Start (stopped) or Restart + Stop (running), plus the
// always-available Edit and Delete. No overflow "…" menu — every action is a
// direct icon button on desktop and mobile alike, matching the "My Models"
// action pattern the Owner asked the pipeline list to adopt.
function pipelineActionStrip(p) {
    let s = '';
    if (pipelineHasOwnedActive(p)) {
        s += iconBtn(ICONS.restart, t('pipelines.actions.restart'), 'warning', 'restartPipeline', p.id);
        s += iconBtn(ICONS.stop, t('pipelines.actions.stop'), 'danger', 'stopPipeline', p.id);
    } else {
        s += iconBtn(ICONS.start, t('pipelines.actions.start'), 'success', 'startPipeline', p.id);
    }
    s += iconBtn(ICONS.edit, t('pipelines.actions.edit'), 'ghost', 'editPipeline', p.id);
    s += iconBtn(ICONS.del, t('pipelines.actions.delete'), 'danger', 'deletePipeline', p.id);
    return s;
}

function renderPipelines() {
    const empty = document.getElementById('pipelines-empty');
    const filterEmpty = document.getElementById('pipelines-filter-empty');
    const list = document.getElementById('pipeline-list');
    if (pipelinesData.length === 0) {
        list.innerHTML = '';
        if (empty) empty.style.display = 'block';
        if (filterEmpty) filterEmpty.style.display = 'none';
        return;
    }
    if (empty) empty.style.display = 'none';
    const search = (document.getElementById('pf-search').value || '').toLowerCase();
    const autoF = document.getElementById('pf-autostart').checked;
    let filtered = pipelinesData.filter(function (p) {
        if (search && p.name.toLowerCase().indexOf(search) === -1) return false;
        if (autoF && !p.active) return false;
        return true;
    });
    if (filtered.length === 0) {
        list.innerHTML = '<div class="empty-state" style="padding:1.5rem;"><p style="font-size:0.85rem;">' + esc(t('pipelines.filter.no_match')) + '</p></div>';
        return;
    }
    if (filterEmpty) filterEmpty.style.display = 'none';
    list.innerHTML = filtered.map(function (p) {
        const st = pipelineAggregateStatus(p);
        const autoBadge = p.active ? '<span class="autostart-indicator" title="' + esc(t('pipelines.col.autostart')) + '">A</span>' : '';
        return '<div class="model-row">' +
            '<div class="model-row-main">' +
                '<div class="model-row-name">' +
                    autoBadge +
                    '<span class="status-badge ' + st + '">' + t('models.status.' + st) + '</span>' +
                    '<span class="model-row-title pl-name" title="' + esc(p.name) + '">' + esc(p.name) + '</span>' +
                '</div>' +
                '<div class="model-row-sub pl-chips-wrap">' + pipelineChips(p) + '</div>' +
            '</div>' +
            '<div class="model-row-actions">' +
                pipelineActionStrip(p) +
            '</div>' +
        '</div>';
    }).join('');
}

async function pipelineAction(action, id, successKey) {
    try {
        await api('/pipelines/' + id + '/' + action, { method: 'POST' });
        showToast(t(successKey), 'success');
    } catch (e) {
        // A group stop/restart can return a non-200 that still executed
        // partially: show it as a warning and always re-render from the
        // server, which now holds the real per-instance state.
        const msg = (e && e.message) || '';
        const partial = msg === 'pipeline_stop_incomplete' || msg === 'pipeline_restart_incomplete';
        showToast(friendlyError(e), partial ? 'warning' : 'error');
    }
    // Always re-render from the server: a non-200 group stop/restart may have
    // executed partially, so the client's copy of the state is not trustworthy.
    try {
        await reloadAllData(); renderAll();
    } catch (e) { showToast(friendlyError(e), 'error'); }
}

function startPipeline(id) { pipelineAction('start', id, 'common.started'); }
function stopPipeline(id) { pipelineAction('stop', id, 'common.stopped'); }
function restartPipeline(id) { pipelineAction('restart', id, 'common.restarted'); }

async function togglePipelineActive(id, active) {
    const p = pipelinesData.find(function (x) { return x.id === id; });
    if (!p) return;
    const body = JSON.stringify({ name: p.name, active: active, models: p.models });
    try {
        await api('/pipelines/' + id, { method: 'PUT', body: body });
        await reloadAllData(); renderAll();
    } catch (err) { showToast(friendlyError(err), 'error'); await reloadAllData(); renderAll(); }
}

function deletePipeline(id) {
    const p = pipelinesData.find(function (x) { return x.id === id; });
    const name = p ? p.name : id;
    showConfirm(t('pipelines.delete.confirm', { name: name }), async function () {
        try {
            await api('/pipelines/' + id, { method: 'DELETE' });
            closeConfirm();
            showToast(t('common.deleted'), 'success');
            await reloadAllData(); renderAll();
        } catch (err) {
            closeConfirm();
            showToast(friendlyError(err), 'error');
        }
    });
}

function plModelOptions(selectedId) {
    return '<option value="">' + esc(t('pipelines.field.model_select')) + '</option>' +
        modelsData.map(function (m) {
            return '<option value="' + esc(m.id) + '"' + (m.id === selectedId ? ' selected' : '') + '>' + esc(m.name) + '</option>';
        }).join('');
}

// ─── Linear pipeline builder (ADR 013 Owner UI: Scratch-inspired blocks) ───
//
// renderPlBuilder renders plEntries as a vertical sequence of blocks. Each block
// is one PipelineModel entry: an order number, a model selector, a compact
// segmented args mode [From model | Custom] (Custom reveals a textarea whose
// non-empty value fully replaces the model args), and explicit reorder controls
// (move up / move down, disabled at the sequence bounds) plus a remove control.
// No DAG/branching/drag&drop and no decorative connectors: the sequence mirrors
// the existing ordered-launch contract; backend entry identity and args
// semantics are unchanged. Value edits (select/textarea) mutate the array
// without re-rendering (preserves focus); structural edits (add/remove/move)
// re-render so the order numbers stay consistent.
function renderPlBuilder() {
    const c = document.getElementById('pl-models-container');
    if (!c) return;
    if (!plEntries || plEntries.length === 0) plEntries.push(plNewEntry('', 'model', '', false));
    const n = plEntries.length;
    const upTitle = t('pipelines.btn.move_up');
    const downTitle = t('pipelines.btn.move_down');
    const removeTitle = t('pipelines.btn.remove_model');
    const argsLabel = t('pipelines.field.args');
    const segModel = t('pipelines.field.args_from_model');
    const segCustom = t('pipelines.field.args_custom');
    const overrideNote = t('pipelines.field.args_override_note');
    const overrideHint = t('pipelines.field.args_override_hint');
    let html = '';
    for (let i = 0; i < n; i++) {
        const e = plEntries[i];
        html += '<div class="pl-block" role="listitem" aria-label="' + esc(t('pipelines.block.step', { n: i + 1 })) + '" data-idx="' + i + '" ondragover="plDragOver(event,' + i + ')" ondragleave="plDragLeave(event)" ondrop="plDrop(event,' + i + ')">';
        html += '<div class="pl-block-head">';
        html += '<span class="pl-drag" draggable="true" ondragstart="plDragStart(event,' + i + ')" ondragend="plDragEnd(event)" aria-label="' + esc(t('pipelines.btn.drag')) + '">' + ICONS.drag + '</span>';
        html += '<span class="pl-step">' + (i + 1) + '</span>';
        html += '<select class="pl-model-select" onchange="plSetModel(' + i + ', this.value)">' + plModelOptions(e.model_id) + '</select>';
        html += '<div class="pl-block-actions">';
        html += '<button type="button" class="pl-act pl-act-up"' + (i > 0 ? '' : ' disabled') + ' title="' + esc(upTitle) + '" aria-label="' + esc(upTitle) + '" onclick="plMoveEntry(' + i + ',-1)"><svg viewBox="0 0 24 24"><path d="M12 8l-6 8h12z"/></svg></button>';
        html += '<button type="button" class="pl-act pl-act-down"' + (i < n - 1 ? '' : ' disabled') + ' title="' + esc(downTitle) + '" aria-label="' + esc(downTitle) + '" onclick="plMoveEntry(' + i + ',1)"><svg viewBox="0 0 24 24"><path d="M12 16l-6-8h12z"/></svg></button>';
        html += '<button type="button" class="pl-act pl-act-remove" title="' + esc(removeTitle) + '" aria-label="' + esc(removeTitle) + '" onclick="plRemoveEntry(' + i + ')">' + ICONS.del + '</button>';
        html += '</div>';
        html += '</div>';
        html += '<div class="pl-args-row">';
        html += '<span class="pl-args-label">' + argsLabel + '</span>';
        html += '<div class="pl-seg" role="radiogroup" aria-label="' + esc(argsLabel) + '">';
        html += '<label class="pl-seg-item"><input type="radio" class="pl-args-radio" name="plargs' + i + '" value="model"' + (e.argsMode !== 'custom' ? ' checked' : '') + ' onchange="plSetArgsMode(' + i + ', \'model\')"><span>' + segModel + '</span></label>';
        html += '<label class="pl-seg-item"><input type="radio" class="pl-args-radio" name="plargs' + i + '" value="custom"' + (e.argsMode === 'custom' ? ' checked' : '') + ' onchange="plSetArgsMode(' + i + ', \'custom\')"><span class="pl-seg-help" data-tooltip="' + esc(t('pipelines.help.args_custom')) + '" tabindex="0">' + segCustom + '</span></label>';
        html += '</div>';
        html += '</div>';
        if (e.argsMode === 'custom') {
            html += '<div class="pl-args-override" data-visible="1">';
            html += '<textarea class="pl-args-input" rows="4" placeholder="' + esc(overrideHint) + '" oninput="plEntryArgsText(' + i + ', this.value)">' + esc(e.argsText) + '</textarea>';
            html += '</div>';
        }
        html += '</div>';
    }
    html += '<button type="button" class="btn btn-ghost pl-add" onclick="plAddEntry()">' + t('pipelines.btn.add_model') + '</button>';
    c.innerHTML = html;
}

function plAddEntry() {
    if (!plEntries) plEntries = [];
    plEntries.push(plNewEntry('', 'model', '', false));
    renderPlBuilder();
}

function plRemoveEntry(i) {
    if (!plEntries || plEntries.length <= 1) return;
    plEntries.splice(i, 1);
    renderPlBuilder();
}

function plMoveEntry(i, dir) {
    if (!plEntries) return;
    const to = i + dir;
    if (to < 0 || to >= plEntries.length) return;
    const tmp = plEntries[i]; plEntries[i] = plEntries[to]; plEntries[to] = tmp;
    renderPlBuilder();
}

let _plDragIdx = -1;
function plDragStart(e, idx) {
    _plDragIdx = idx;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', String(idx));
    e.target.closest('.pl-block').classList.add('pl-dragging');
}
function plDragEnd(e) {
    _plDragIdx = -1;
    document.querySelectorAll('.pl-block').forEach(function (b) { b.classList.remove('pl-dragging', 'pl-drop-above', 'pl-drop-below'); });
}
function plDragOver(e, idx) {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    if (idx === _plDragIdx) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const above = e.clientY < rect.top + rect.height / 2;
    document.querySelectorAll('.pl-block').forEach(function (b) { b.classList.remove('pl-drop-above', 'pl-drop-below'); });
    e.currentTarget.classList.add(above ? 'pl-drop-above' : 'pl-drop-below');
}
function plDragLeave(e) {
    e.currentTarget.classList.remove('pl-drop-above', 'pl-drop-below');
}
function plDrop(e, idx) {
    e.preventDefault();
    if (_plDragIdx < 0 || _plDragIdx === idx) { plDragEnd(e); return; }
    const rect = e.currentTarget.getBoundingClientRect();
    const above = e.clientY < rect.top + rect.height / 2;
    let to = above ? idx : idx + 1;
    if (to > _plDragIdx) to--;
    const item = plEntries.splice(_plDragIdx, 1)[0];
    plEntries.splice(to, 0, item);
    _plDragIdx = -1;
    renderPlBuilder();
}

function plSetModel(i, value) {
    if (plEntries && plEntries[i]) plEntries[i].model_id = value;
}

function plSetArgsMode(i, mode) {
    if (!plEntries || !plEntries[i]) return;
    plEntries[i].argsMode = mode;
    renderPlBuilder();
}

function plEntryArgsText(i, text) {
    if (plEntries && plEntries[i]) plEntries[i].argsText = text || '';
}

function openPipelineModal(p) {
    if (modelsData.length === 0) {
        showToast(t('pipelines.error.no_models'), 'error');
        return;
    }
    const form = document.getElementById('pipeline-form');
    form.reset();
    document.getElementById('pipeline-modal-title').textContent = p ? t('pipelines.edit.title') : t('pipelines.create.title');
    document.getElementById('pipeline-submit-btn').textContent = p ? t('pipelines.btn.save') : t('pipelines.btn.create');
    form.id.value = p ? p.id : '';
    form.name.value = p ? p.name : '';
    form.active.checked = p ? !!p.active : false;
    // Build the linear builder state from the pipeline (Create: one empty
    // block; Edit: one block per entry, preserving entry id + args mode).
    plEntries = [];
    if (p && p.models && p.models.length) {
        p.models.forEach(function (m) {
            plEntries.push(plNewEntry(m.model_id, (m.args && m.args.length) ? 'custom' : 'model', renderArgs(m.args || []), m.auto_start, m.id));
        });
    } else {
        plEntries.push(plNewEntry('', 'model', '', false));
    }
    renderPlBuilder();
    document.getElementById('pipeline-modal').style.display = 'flex';
}

function openCreatePipelineModal() {
    openPipelineModal(null);
}

function editPipeline(id) {
    openPipelineModal(pipelinesData.find(function (x) { return x.id === id; }) || null);
}

async function handlePipelineSubmit(e) {
    e.preventDefault();
    const f = e.target;
    const models = [];
    for (let i = 0; i < plEntries.length; i++) {
        const entry = plEntries[i];
        if (!entry.model_id) { showToast(t('pipelines.error.model_required'), 'error'); return false; }
        // Custom mode: the parsed (quote-aware) args fully replace the model's
        // args. From-model mode: empty args → the model's args are used.
        // auto_start is a legacy per-entry field, no longer editable in the UI
        // (an Active pipeline launches every entry). It round-trips the stored
        // value unchanged so existing data is preserved on edit; on create it
        // defaults to false. The backend ignores it for launch decisions.
        const args = (entry.argsMode === 'custom') ? parseArgs(entry.argsText) : [];
        models.push({
            id: entry.id,
            model_id: entry.model_id,
            args: args,
            auto_start: !!entry.autoStart
        });
    }
    if (models.length === 0) {
        showToast(t('pipelines.error.no_models_selected'), 'error');
        return false;
    }
    const body = JSON.stringify({ name: f.name.value, active: f.active.checked, models: models });
    const id = f.id.value;
    const btn = document.getElementById('pipeline-submit-btn');
    const oldLabel = btn.textContent;
    btn.disabled = true;
    btn.textContent = t('common.loading');
    try {
        if (id) {
            await api('/pipelines/' + id, { method: 'PUT', body: body });
            showToast(t('common.saved'), 'success');
        } else {
            await api('/pipelines', { method: 'POST', body: body });
            showToast(t('common.created'), 'success');
        }
        closeModal('pipeline-modal');
        await reloadAllData(); renderAll();
    } catch (err) {
        showToast(friendlyError(err), 'error');
    } finally {
        btn.disabled = false;
        btn.textContent = oldLabel;
    }
    return false;
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
            const stateLabel = historyStateLabel(i.state);
            const label = getModelName(i.model_id) + ' | ' + compactInstanceId(i.id) + ' | ' + stateLabel;
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
    const stateLabel = historyStateLabel(inst.state);
    bar.innerHTML = '<span class="log-bar-model" title="' + esc(getModelName(inst.model_id)) + '">' + esc(getModelName(inst.model_id)) + '</span>' +
        ' <span class="log-sep">|</span> <code title="' + esc(inst.id) + '">' + esc(compactInstanceId(inst.id)) + '</code>' +
        ' <span class="log-sep">|</span> ' + t('logs.bar.pid') + ': ' + (inst.pid || '—') +
        ' <span class="log-sep">|</span> <span class="status-badge ' + esc(inst.state) + '">' + esc(stateLabel) + '</span>' +
        ' <span class="log-sep">|</span> ' + t('logs.bar.started') + ': ' + started;
    bar.style.display = 'flex';
}

function connectLogStream(instanceId) {
    if (logEs) { logEs.close(); logEs = null; }
    currentLogInstance = instanceId || '';
    const url = instanceId
        ? '/api/v1/instances/' + encodeURIComponent(instanceId) + '/logs/stream'
        : '/api/v1/logs/stream';
    logEs = new EventSource(url);
    logEs.onmessage = function (e) {
        let d;
        try {
            d = JSON.parse(e.data);
        } catch { return; }
        // Client-side dedup by broker sequence: the server replays the last
        // 1000 lines on every (re)connect, so without this a reconnect (return
        // to the Logs page, stream switch, network blip) re-appends lines
        // already rendered. The broker sequence is a single monotonic counter;
        // a value below the last seen one means the counter restarted (server
        // restart), in which case accepting resumes from scratch.
        const seq = d.sequence;
        if (typeof seq === 'number') {
            if (seq < lastLogSeq) lastLogSeq = 0;
            if (seq <= lastLogSeq) return;
            lastLogSeq = seq;
        }
        if (logPaused) return;
        appendLogLine(d);
    };
    // EventSource retries by itself; probe now (instead of waiting up to the
    // 5 s interval) so a dropped server surfaces in the UI immediately.
    logEs.onerror = function () {
        if (serverOnline) probeServer();
    };
}

function appendLogLine(d) {
    const stream = d.stream || 'stdout';
    const search = document.getElementById('log-search').value.toLowerCase();
    if (search && (d.message || '').toLowerCase().indexOf(search) === -1) return;

    const view = document.getElementById('log-view');
    const emptyEl = document.getElementById('logs-empty');
    if (emptyEl) emptyEl.style.display = 'none';

    const div = document.createElement('div');
    div.className = 'log-line ' + stream;
    const ts = d.time ? new Date(d.time).toLocaleTimeString() : '';
    const instLabel = d.instance_id ? '<span class="log-inst" title="' + esc(d.instance_id) + '">' + esc(compactInstanceId(d.instance_id, true)) + '</span>' : '';
    div.innerHTML = '<span class="log-time">' + ts + '</span>' + instLabel + '<span class="log-source">[' + esc(stream) + ']</span>' + esc(d.message);
    view.appendChild(div);

    // querySelectorAll returns a STATIC NodeList: its .length is frozen at
    // capture time and a detached lines[0] makes .remove() a no-op, so the
    // previous `while (lines.length > 2000) lines[0].remove();` spun forever
    // and wedged the tab's main thread once 2001 lines accumulated. Compute
    // the overflow once and remove exactly that many oldest lines.
    const lines = view.querySelectorAll('.log-line');
    const overflow = lines.length - LOG_VIEW_MAX_LINES;
    for (let i = 0; i < overflow; i++) lines[i].remove();
    if (document.getElementById('log-autoscroll').checked) {
        view.scrollTop = view.scrollHeight;
    }
}

function applyLogSearch() {
    const view = document.getElementById('log-view');
    const search = document.getElementById('log-search').value.toLowerCase();
    Array.from(view.children).forEach(function (div) {
        const text = div.textContent.toLowerCase();
        div.style.display = (!search || text.indexOf(search) !== -1) ? '' : 'none';
    });
}

function toggleLogPause() {
    logPaused = !logPaused;
    document.getElementById('log-pause-btn').textContent = logPaused ? t('logs.resume') : t('logs.pause');
}

function clearLogView() {
    document.getElementById('log-view').innerHTML = '';
    // The DOM is empty now, so replayed history may be appended again on the
    // next (re)connect without duplicating rendered lines.
    lastLogSeq = 0;
}

function viewInstanceLogs(instanceId) {
    const sel = document.getElementById('log-instance-select');
    if (instanceId) {
        sel.value = instanceId;
        updateLogInstanceBar(instanceId);
    } else {
        updateLogInstanceBar('');
    }
    navigate('logs');
    if (logEs && currentLogInstance !== (instanceId || '')) connectLogStream(instanceId);
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

    // The live-log stream is page-scoped: leaving the Logs page closes the
    // EventSource (frees the server subscription, network, and the main-thread
    // append work on the hidden #log-view); returning reconnects to the
    // currently selected instance. Replayed history is deduped by sequence, so
    // the view continues without duplicated lines.
    if (view === 'logs') {
        if (!logEs) {
            const sel = document.getElementById('log-instance-select');
            connectLogStream(sel ? sel.value : '');
        }
    } else if (logEs) {
        logEs.close();
        logEs = null;
    }
    if (view === 'adv-settings') loadSettings();
}

// ─── Wizard (create / edit model) ───────────────────────────────────────────

function openWizard(modelId) {
    wizStep = 1;
    wizEditId = modelId || null;
    rtSelectedId = null;
    const form = document.getElementById('wizard-form');
    form.reset();
    document.getElementById('wizard-error').style.display = 'none';
    document.getElementById('wiz-env-container').innerHTML = '';
    document.getElementById('wiz-env-container').setAttribute('data-original-keys', '[]');
    document.querySelector('input[name=wiz-rt-mode][value=existing]').checked = true;
    onWizRtModeChange();
    loadWizardRuntimeCards();

    const titleEl = document.getElementById('wizard-title');
    titleEl.textContent = wizEditId ? t('wizard.title.edit') : t('wizard.title.add');

    if (wizEditId) {
        const m = modelsData.find(function (x) { return x.id === wizEditId; });
        if (m) {
            document.getElementById('wiz-name').value = m.name || '';
            document.getElementById('wiz-args').value = renderArgs(m.args || []);
            document.getElementById('wiz-autostart').checked = !!m.active;
            document.getElementById('wiz-delay-group').style.display = m.active ? '' : 'none';
            document.getElementById('wiz-autostart-delay').value = m.autostart_delay || 0;
            if (m.runtime_id) {
                rtSelectedId = m.runtime_id;
                renderRtDropdown();
            }
            // Pre-fill existing environment keys (write-only: keys visible, values hidden)
            if (m.environment_keys && m.environment_keys.length) {
                var envContainer = document.getElementById('wiz-env-container');
                envContainer.setAttribute('data-original-keys', JSON.stringify(m.environment_keys));
                m.environment_keys.forEach(function (key) {
                    var div = document.createElement('div');
                    div.className = 'env-row';
                    div.style.display = 'flex';
                    div.style.gap = '6px';
                    div.style.marginBottom = '6px';
                    div.setAttribute('data-existing', 'true');
                    div.innerHTML = '<input type="text" value="' + esc(key) + '" readonly style="flex:1;padding:6px;border:1px solid var(--border);border-radius:4px;background:var(--bg-input);color:var(--text-primary);opacity:0.7;">' +
                        '<input type="text" placeholder="write-only" style="flex:2;padding:6px;border:1px solid var(--border);border-radius:4px;background:var(--bg-input);color:var(--text-primary);">' +
                        '<button type="button" class="btn btn-danger btn-sm" onclick="this.parentElement.remove()">&times;</button>';
                    envContainer.appendChild(div);
                });
            } else {
                document.getElementById('wiz-env-container').setAttribute('data-original-keys', '[]');
            }
        }
    }
    updateWizardStep();
    document.getElementById('wizard-modal').style.display = 'flex';
}

function closeWizard() { document.getElementById('wizard-modal').style.display = 'none'; }

function loadWizardRuntimeCards() {
    rtSelectedId = null;
    document.getElementById('wiz-rt-search').value = '';
    if (runtimesData.length === 0 && !wizEditId) {
        document.querySelector('input[name=wiz-rt-mode][value=new]').checked = true;
        onWizRtModeChange();
    }
    renderRtDropdown();
}

var rtSelectedId = null;

// rtDisplayPath resolves the full logical executable path for display.
// If the executable is already absolute (drive letter, POSIX root, or UNC),
// it is returned as-is; otherwise it is joined with the working directory.
// The result carries no "exe:"/"cwd:" labels.
function rtDisplayPath(r) {
    const exe = (r.executable || '').trim();
    const cwd = (r.working_directory || '').trim();
    if (!exe) return cwd;
    if (/^[A-Za-z]:[\\/]/.test(exe) || exe.charAt(0) === '/' || exe.charAt(0) === '\\') {
        return exe;
    }
    if (!cwd) return exe;
    const sep = cwd.indexOf('\\') !== -1 ? '\\' : '/';
    return cwd.replace(/[\\\/]+$/, '') + sep + exe;
}

function renderRtDropdown() {
    const dd = document.getElementById('wiz-rt-dropdown');
    const search = document.getElementById('wiz-rt-search');
    if (!dd || !search) return;
    if (runtimesData.length === 0) {
        dd.innerHTML = '<div class="rt-dropdown-empty">' + esc(t('wizard.rt.empty')) + '</div>';
        return;
    }
    const query = search.value.toLowerCase();
    const filtered = runtimesData.filter(function (r) {
        return !query || r.name.toLowerCase().indexOf(query) !== -1;
    });
    if (filtered.length === 0) {
        dd.innerHTML = '<div class="rt-dropdown-empty">' + esc(t('wizard.rt.none')) + '</div>';
        return;
    }
    dd.innerHTML = filtered.map(function (r) {
        const sel = r.id === rtSelectedId;
        const path = rtDisplayPath(r);
        return '<div class="rt-dropdown-item' + (sel ? ' selected' : '') + '" data-id="' + esc(r.id) + '" title="' + esc(path) + '" onmousedown="selectRtItem(\'' + safeId(r.id) + '\')">' +
            (sel ? '<span class="rt-check">✓</span>' : '') +
            '<span class="rt-name">' + esc(r.name) + '</span>' +
            (path ? '<span class="rt-sep"> · </span><span class="rt-path">' + esc(path) + '</span>' : '') +
        '</div>';
    }).join('');
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
        items.forEach(function (el) { el.classList.remove('kb-focus'); });
        items[idx].classList.add('kb-focus');
        items[idx].scrollIntoView({ block: 'nearest' });
    } else if (e.key === 'Enter') {
        e.preventDefault();
        const dd = document.getElementById('wiz-rt-dropdown');
        const sel = dd.querySelector('.rt-dropdown-item.kb-focus') || dd.querySelector('.rt-dropdown-item.selected');
        if (sel) selectRtItem(sel.dataset.id);
    } else if (e.key === 'Escape') {
        e.stopPropagation();
    }
}

function selectRtItem(id) {
    rtSelectedId = id;
    const r = runtimesData.find(function (x) { return x.id === id; });
    if (!r) return;
    document.getElementById('wiz-rt-search').value = '';
    renderRtDropdown();
}

function onWizRtModeChange() {
    const mode = document.querySelector('input[name=wiz-rt-mode]:checked').value;
    document.getElementById('wiz-rt-existing-section').style.display = mode === 'existing' ? '' : 'none';
    document.getElementById('wiz-rt-new-section').style.display = mode === 'new' ? '' : 'none';
}

function wizRuntimeReady() {
    const mode = document.querySelector('input[name=wiz-rt-mode]:checked').value;
    return mode !== 'existing' || !!rtSelectedId;
}

function wizGoto(step) {
    if (step === 3 && !wizRuntimeReady()) {
        wizStep = 2;
        updateWizardStep();
        wizError(t('wizard.error.runtime'));
        return;
    }
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
        if (wizStep === 2 && !wizRuntimeReady()) {
            wizError(t('wizard.error.runtime'));
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
            if (!rtName || !rtExe) { wizError(t('common.error.fill_rt')); return; }
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

        if (wizEditId) {
            // Build environment_patch for lossless edit
            const patch = buildEnvPatch('wiz-env-container');
            if (patch.length) body.environment_patch = patch;
            await api('/models/' + wizEditId, { method: 'PUT', body: JSON.stringify(body) });
            showToast('"' + name + '" ' + t('common.saved'), 'success');
        } else {
            const env = collectEnvRows('wiz-env-container');
            if (Object.keys(env).length) body.environment = env;
            await api('/models', { method: 'POST', body: JSON.stringify(body) });
            showToast('"' + name + '" ' + t('common.created'), 'success');
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

// buildEnvPatch constructs the environment_patch array for a lossless model edit.
// Existing keys (pre-filled rows with data-existing="true"):
//   - still present, value empty => KEEP (no op emitted)
//   - still present, value filled => SET
//   - removed from DOM => DELETE
// New rows (no data-existing):
//   - => SET (ADD)
function buildEnvPatch(containerId) {
    const container = document.getElementById(containerId);
    const originalKeys = JSON.parse(container.getAttribute('data-original-keys') || '[]');
    const patch = [];
    // Check which original keys are still present and their values
    const presentKeys = {};
    container.querySelectorAll('.env-row').forEach(function (row) {
        const inputs = row.querySelectorAll('input');
        if (!inputs[0]) return;
        const key = inputs[0].value.trim();
        if (!key) return;
        const val = inputs[1] ? inputs[1].value : '';
        const isExisting = row.getAttribute('data-existing') === 'true';
        if (isExisting) {
            presentKeys[key] = val;
            if (val !== '') {
                patch.push({ key: key, action: 'set', value: val });
            }
        } else {
            // New key (ADD)
            patch.push({ key: key, action: 'set', value: val });
        }
    });
    // Deleted keys
    originalKeys.forEach(function (key) {
        if (!(key in presentKeys)) {
            patch.push({ key: key, action: 'delete' });
        }
    });
    return patch;
}

// ─── Advanced: Runtimes ─────────────────────────────────────────────────────

function renderAdvRuntimes() {
    const empty = document.getElementById('runtimes-empty');
    const tableWrap = document.getElementById('runtimes-table-wrap');
    if (runtimesData.length === 0) {
        document.getElementById('adv-runtimes-body').innerHTML = '';
        if (empty) empty.style.display = 'block';
        if (tableWrap) tableWrap.dataset.has = '0';
        const compact = document.getElementById('runtimes-compact');
        if (compact) compact.classList.remove('visible');
        return;
    }
    if (empty) empty.style.display = 'none';
    if (tableWrap) tableWrap.dataset.has = '1';
    document.getElementById('adv-runtimes-body').innerHTML = runtimesData.map(function (r) {
        return '<tr><td>' + esc(r.name) + '</td><td title="' + esc(r.executable || '') + '">' + esc(r.executable) + '</td><td title="' + esc(r.working_directory || '') + '">' + esc(r.working_directory || '—') + '</td>' +
            '<td class="actions-cell">' + iconBtn(ICONS.edit, t('runtimes.actions.edit'), 'ghost', 'editRuntime', r.id) +
            iconBtn(ICONS.del, t('runtimes.actions.delete'), 'danger', 'deleteRuntime', r.id) + '</td></tr>';
    }).join('');
    const compact = document.getElementById('runtimes-compact');
    if (compact) {
        compact.classList.add('visible');
        compact.innerHTML = runtimesData.map(function (r) {
            const cwd = r.working_directory || '';
            return '<div class="compact-row crt-row">' +
                '<div class="compact-main">' +
                    '<div class="compact-l1">' + esc(r.name) + '</div>' +
                    '<div class="compact-l2" title="' + esc(r.executable || '') + '">' + esc(r.executable || '—') + '</div>' +
                    (cwd ? '<div class="compact-l3" title="' + esc(cwd) + '">' + esc(cwd) + '</div>' : '') +
                '</div>' +
                '<div class="compact-actions">' +
                    iconBtn(ICONS.edit, t('runtimes.actions.edit'), 'ghost', 'editRuntime', r.id) +
                    iconBtn(ICONS.del, t('runtimes.actions.delete'), 'danger', 'deleteRuntime', r.id) +
                '</div>' +
            '</div>';
        }).join('');
    }
    applyFit(fitViews[0]);
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
    envC.setAttribute('data-original-keys', JSON.stringify(r.environment_keys || []));
    if (r.environment_keys && r.environment_keys.length) {
        r.environment_keys.forEach(function (k) {
            const row = document.createElement('div');
            row.className = 'env-row';
            row.setAttribute('data-existing', 'true');
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
    // Lossless environment edit: untouched keys are KEPT (no op), typed
    // values are SET, removed rows are DELETE, new rows are ADD. The full
    // environment map is never sent on update (write-only values).
    const patch = buildEnvPatch('rt-edit-env-container');
    if (patch.length) body.environment_patch = patch;
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
    const tableWrap = document.getElementById('instances-table-wrap');
    const visible = instancesData.filter(function (i) { return isActive(i.state) || i.state === 'orphan'; });
    if (visible.length === 0) {
        document.getElementById('adv-instances-body').innerHTML = '';
        if (empty) empty.style.display = 'block';
        if (tableWrap) tableWrap.dataset.has = '0';
        const compact = document.getElementById('instances-compact');
        if (compact) compact.classList.remove('visible');
        return;
    }
    if (empty) empty.style.display = 'none';
    if (tableWrap) tableWrap.dataset.has = '1';
    document.getElementById('adv-instances-body').innerHTML = visible.map(function (i) {
        var isOrphan = i.state === 'orphan';
        var badge = '<span class="status-badge ' + esc(i.state) + '" title="' + esc(t(isOrphan ? 'instances.orphan.hint' : 'models.status.' + i.state) || i.state) + '">' + esc(t('models.status.' + i.state) || i.state) + '</span>';
        var actions;
        if (isOrphan) {
            actions = iconBtn(ICONS.logs, t('instances.actions.logs'), 'ghost', 'viewInstanceLogs', i.id) +
                iconBtn(ICONS.kill, t('instances.actions.kill'), 'danger', 'killInstance', i.id) +
                iconBtn(ICONS.stop, t('instances.actions.dismiss'), 'warning', 'dismissInstance', i.id);
        } else {
            actions = iconBtn(ICONS.logs, t('instances.actions.logs'), 'ghost', 'viewInstanceLogs', i.id) +
                iconBtn(ICONS.stop, t('instances.actions.stop'), 'danger', 'stopInstance', i.id) +
                iconBtn(ICONS.restart, t('instances.actions.restart'), 'warning', 'restartInstance', i.id);
        }
        return '<tr><td class="inst-mono" title="' + esc(i.id) + '">' + esc(compactInstanceId(i.id)) + '</td>' +
            '<td class="inst-model" title="' + esc(getModelName(i.model_id)) + '">' + esc(getModelName(i.model_id)) + '</td>' +
            '<td class="inst-state">' + badge + '</td>' +
            '<td class="inst-mono">' + (i.pid || '—') + '</td><td class="inst-time" title="' + esc(fmtTime(i.started_at)) + '">' + fmtTime(i.started_at) + '</td><td class="inst-time">' + fmtTime(i.stopped_at) + '</td>' +
            '<td class="inst-mono">' + (i.exit_code != null ? i.exit_code : '—') + '</td>' +
            '<td class="actions-cell">' + actions + '</td></tr>';
    }).join('');
    const compact = document.getElementById('instances-compact');
    if (compact) {
        compact.classList.add('visible');
        compact.innerHTML = visible.map(function (i) {
            var isOrphan = i.state === 'orphan';
            const range = fmtRange(i.started_at, i.stopped_at) || fmtUptime(i.started_at);
            var badge = '<span class="status-badge ' + esc(i.state) + '" title="' + esc(t(isOrphan ? 'instances.orphan.hint' : 'models.status.' + i.state) || i.state) + '">' + esc(t('models.status.' + i.state) || i.state) + '</span>';
            var actions;
            if (isOrphan) {
                actions = iconBtn(ICONS.logs, t('instances.actions.logs'), 'ghost', 'viewInstanceLogs', i.id) +
                    iconBtn(ICONS.kill, t('instances.actions.kill'), 'danger', 'killInstance', i.id) +
                    iconBtn(ICONS.stop, t('instances.actions.dismiss'), 'warning', 'dismissInstance', i.id);
            } else {
                actions = iconBtn(ICONS.logs, t('instances.actions.logs'), 'ghost', 'viewInstanceLogs', i.id) +
                    iconBtn(ICONS.stop, t('instances.actions.stop'), 'danger', 'stopInstance', i.id) +
                    iconBtn(ICONS.restart, t('instances.actions.restart'), 'warning', 'restartInstance', i.id);
            }
            return '<div class="compact-row cinst-row">' +
                '<div class="compact-main">' +
                    '<div class="compact-l1">' +
                        badge +
                        '<span class="compact-title" title="' + esc(getModelName(i.model_id)) + '">' + esc(getModelName(i.model_id)) + '</span>' +
                    '</div>' +
                    '<div class="compact-l2" title="' + esc(i.id) + '">' + esc(compactInstanceId(i.id)) + ' · PID ' + (i.pid || '—') + (range ? ' · ' + range : '') + '</div>' +
                '</div>' +
                '<div class="compact-actions">' + actions + '</div>' +
            '</div>';
        }).join('');
    }
    applyFit(fitViews[1]);
}

async function stopInstance(id) {
    try { await api('/instances/' + id + '/stop', { method: 'POST' }); await reloadAllData(); renderAll(); }
    catch (err) { showToast(friendlyError(err), 'error'); }
}

async function restartInstance(id) {
    try { await api('/instances/' + id + '/restart', { method: 'POST' }); await reloadAllData(); renderAll(); }
    catch (err) { showToast(friendlyError(err), 'error'); }
}

async function dismissInstance(id) {
    try { await api('/instances/' + id + '/dismiss', { method: 'POST' }); await reloadAllData(); renderAll(); }
    catch (err) { showToast(friendlyError(err), 'error'); }
}

function killInstance(id) {
    const inst = instancesData.find(function (x) { return x.id === id; });
    const pid = inst && inst.pid ? inst.pid : '—';
    showConfirm(t('instances.kill.confirm', { pid: pid }), async function () {
        try {
            const res = await api('/instances/' + id + '/kill', { method: 'POST' });
            closeConfirm();
            if (res && res.status === 'reconciled') {
                showToast(t('instances.reconciled'), 'success');
            } else {
                showToast(t('instances.killed', { method: res ? res.method : '—' }), 'success');
            }
            await reloadAllData(); renderAll();
        } catch (err) {
            closeConfirm();
            showToast(friendlyError(err), 'error');
        }
    });
}

// ─── Instance History (with filters) ────────────────────────────────────────

function isTerminal(s) { return s === 'exited' || s === 'failed' || s === 'stale'; }

function historyStateLabel(s) {
    var key = 'history.state.' + s;
    var val = t(key);
    if (val !== key) return val;
    var mkey = 'models.status.' + s;
    var mval = t(mkey);
    return mval !== mkey ? mval : s;
}

// Human-readable termination reason for a terminal history row. Maps the
// persisted exit_class (plus state / last_error / recovery_reason) to a
// user-facing label. The raw exit code is NEVER the primary reason: a routine
// GoAl stop shows 3221225786 on Windows (CTRL_BREAK 0xC000013A) or 143 on
// Linux (SIGTERM) — both are normal stops, not crashes. Returns null when no
// reason is known (the state badge still carries the state).
function exitReason(i) {
    if (i.state === 'stale') {
        switch (i.recovery_reason) {
            case 'killed-by-user': return t('history.reason.force_stopped');
            case 'pid-gone':
            case 'pid-not-found': return t('history.reason.process_not_found');
            case 'identity-unconfirmed': return t('history.reason.identity_unconfirmed');
            case 'reconciled-by-user': return t('history.reason.dismissed');
            default: break;
        }
        if (i.exit_class === 'killed') return t('history.reason.force_stopped');
        return null;
    }
    switch (i.exit_class) {
        case 'normal': return t('history.reason.completed');
        case 'signaled':
        case 'context': return t('history.reason.stopped');
        case 'killed':
        case 'timeout': return t('history.reason.force_stopped');
        case 'failure': return t('history.reason.crashed');
        case 'error':
            return i.last_error ? t('history.reason.start_failed', { error: i.last_error }) : t('history.reason.start_failed.short');
        default:
            if (i.state === 'failed' && i.last_error) {
                return t('history.reason.start_failed', { error: i.last_error });
            }
            return null;
    }
}

// Dense-row variant: long start-failure diagnostics are abbreviated in the
// compact row; the full text stays reachable via the row tooltip.
function exitReasonShort(i, reason) {
    if (reason && i.last_error && i.state === 'failed') {
        return t('history.reason.start_failed.short');
    }
    return reason;
}

function renderHistory() {
    const empty = document.getElementById('history-empty');
    const tableWrap = document.getElementById('history-table-wrap');
    const stateF = document.getElementById('hf-state').value;
    const modelF = document.getElementById('hf-model').value;

    let filtered = historyData.filter(function (i) {
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
        if (empty) empty.style.display = 'block';
        if (tableWrap) tableWrap.dataset.has = '0';
        const compact = document.getElementById('history-compact');
        if (compact) compact.classList.remove('visible');
        return;
    }
    if (empty) empty.style.display = 'none';
    if (tableWrap) tableWrap.dataset.has = '1';
    document.getElementById('history-body').innerHTML = filtered.slice(0, 200).map(function (i) {
        const modelName = i.model_name || getModelName(i.model_id);
        const reason = exitReason(i);
        const reasonText = reason || '—';
        const reasonTitle = i.exit_code != null
            ? ' title="' + esc(t('history.reason.exit_code', { code: i.exit_code })) + '"'
            : '';
        return '<tr><td class="inst-model" title="' + esc(modelName) + '">' + esc(modelName) + '</td>' +
            '<td class="inst-mono" title="' + esc(i.id) + '">' + esc(compactInstanceId(i.id)) + '</td>' +
            '<td class="inst-state"><span class="status-badge ' + esc(i.state) + '">' + esc(historyStateLabel(i.state)) + '</span></td>' +
            '<td class="inst-mono">' + (i.pid || '—') + '</td>' +
            '<td class="inst-time" title="' + esc(fmtTime(i.started_at)) + '">' + fmtTime(i.started_at) + '</td><td class="inst-time" title="' + esc(fmtTime(i.stopped_at)) + '">' + fmtTime(i.stopped_at) + '</td>' +
            '<td class="inst-reason"' + reasonTitle + '>' + esc(reasonText) + '</td>' +
            '<td class="actions-cell">' + iconBtn(ICONS.logs, t('instances.actions.logs'), 'ghost', 'viewInstanceLogs', i.id) + '</td></tr>';
    }).join('');
    const compact = document.getElementById('history-compact');
    if (compact) {
        compact.classList.add('visible');
        compact.innerHTML = filtered.slice(0, 200).map(function (i) {
            const modelName = i.model_name || getModelName(i.model_id);
            const range = fmtRange(i.started_at, i.stopped_at);
            const title = [fmtTime(i.started_at), fmtTime(i.stopped_at)].join(' → ');
            const reason = exitReason(i);
            let tooltipText = i.id;
            if (range) tooltipText += ' · ' + title;
            if (i.exit_code != null) tooltipText += ' · ' + t('history.reason.exit_code', { code: i.exit_code });
            if (reason && reason !== exitReasonShort(i, reason)) tooltipText += ' · ' + reason;
            const rangeTitle = ' title="' + esc(tooltipText) + '"';
            const reasonPart = reason ? ' · ' + esc(exitReasonShort(i, reason)) : '';
            return '<div class="compact-row chist-row">' +
                '<div class="compact-main">' +
                    '<div class="compact-l1">' +
                        '<span class="status-badge ' + esc(i.state) + '">' + esc(historyStateLabel(i.state)) + '</span>' +
                        '<span class="compact-title" title="' + esc(modelName) + '">' + esc(modelName) + '</span>' +
                    '</div>' +
                    '<div class="compact-l2"' + rangeTitle + '>' + esc(compactInstanceId(i.id)) + ' · PID ' + (i.pid || '—') + reasonPart + (range ? ' · ' + range : '') + '</div>' +
                '</div>' +
                '<div class="compact-actions">' +
                    iconBtn(ICONS.logs, t('instances.actions.logs'), 'ghost', 'viewInstanceLogs', i.id) +
                '</div>' +
            '</div>';
        }).join('');
    }
    applyFit(fitViews[2]);
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
        lastMetrics = m;
        document.getElementById('set-listen').textContent = m.listen_address || '127.0.0.1';
        document.getElementById('set-port').textContent = m.web_port ? String(m.web_port) : '-';
        document.getElementById('set-auth').textContent = m.auth_enabled ? t('settings.server.auth.on') : t('settings.server.auth.off');
    } catch {}
    portableUpdateEntitySelector();
}

// ─── Error messages ─────────────────────────────────────────────────────────

const errorPatterns = [
    [/invalid credentials/i, 'err.invalid_credentials'],
    [/too many login attempts|rate.?limited/i, 'err.rate_limited'],
    [/invalid csrf token/i, 'err.invalid_csrf'],
    [/^unauthorized$/i, 'err.unauthorized'],
    [/model not found/i, 'err.model_not_found'],
    [/runtime not found/i, 'err.runtime_not_found'],
    [/instance not found/i, 'err.instance_not_found'],
    [/no running instance/i, 'err.no_running_instance'],
    [/in use|referenced|depend/i, 'err.in_use'],
    [/port.*in use|address already in use/i, 'err.port_in_use'],
    [/executable.*not found|no such file/i, 'err.executable_not_found'],
    [/cannot enable auth/i, 'err.auth_credentials'],
    [/not a valid bind address/i, 'err.addr_invalid'],
    [/web_port must be/i, 'err.port_invalid'],
    [/password must not exceed/i, 'err.password_too_long'],
    [/failed to save config/i, 'err.config_save_failed'],
    [/failed to load config/i, 'err.config_load_failed'],
    [/validation failed/i, 'err.validation_failed'],
    [/failed to hash password|failed to create session|template error|internal server error/i, 'err.internal'],
    [/not found/i, 'err.not_found'],
];

const errorCodeKeys = {
    rate_limited: 'err.rate_limited',
    conflict: 'err.in_use',
    invalid_runtime: 'err.runtime_not_found',
    invalid_model: 'err.model_not_found',
    not_found: 'err.not_found',
    unauthorized: 'err.unauthorized',
    internal_server_error: 'err.internal',
};

// Bounded lifecycle error tokens (API.md). They win over the HTTP code: 409
// conflict also covers "object is in use", which is a different message.
const lifecycleTokenKeys = {
    launch_in_flight: 'err.launch_in_flight',
    not_restartable: 'err.not_restartable',
    shutting_down: 'err.shutting_down',
    launch_aborted: 'err.shutting_down',
    instance_not_found: 'err.instance_not_found',
    pipeline_stop_incomplete: 'err.pipeline_stop_incomplete',
    pipeline_restart_incomplete: 'err.pipeline_restart_incomplete',
};

function translateServerMessage(msg) {
    if (!msg) return t('err.unknown');
    for (let i = 0; i < errorPatterns.length; i++) {
        if (errorPatterns[i][0].test(msg)) return t(errorPatterns[i][1]);
    }
    return t('err.server_wrap', { detail: msg });
}

function friendlyError(err) {
    const msg = (err && err.message) || '';
    if (lifecycleTokenKeys[msg]) return t(lifecycleTokenKeys[msg]);
    if (err && err.code && errorCodeKeys[err.code]) return t(errorCodeKeys[err.code]);
    return translateServerMessage(msg);
}

// ─── Canonical tooltip (single system) ──────────────────────────────────────
// The only user-visible tooltip mechanism: one floating #goaltip element whose
// content comes from the data-tooltip attribute. Trigger elements must not also
// carry a native title attribute (a double tooltip is a defect). Positioning:
// above the target when it fits, otherwise below; horizontally clamped to the
// viewport so the tooltip is never clipped or causes horizontal overflow.
let tipActiveEl = null;

function tipShow(target) {
    const tipEl = document.getElementById('goaltip');
    if (!tipEl) return;
    const text = target.getAttribute('data-tooltip');
    if (!text) return;
    tipEl.textContent = text;
    tipEl.classList.add('visible');
    tipEl.setAttribute('aria-hidden', 'false');
    const r = target.getBoundingClientRect();
    const tr = tipEl.getBoundingClientRect();
    const margin = 8;
    let left = r.left + r.width / 2 - tr.width / 2;
    if (left < margin) left = margin;
    if (left + tr.width > window.innerWidth - margin) left = window.innerWidth - margin - tr.width;
    if (left < margin) left = margin;
    let top = r.top - tr.height - 8;
    if (top < margin) top = r.bottom + 8;
    tipEl.style.left = Math.round(left) + 'px';
    tipEl.style.top = Math.round(top) + 'px';
    target.setAttribute('aria-describedby', 'goaltip');
    tipActiveEl = target;
}

function tipHide() {
    const tipEl = document.getElementById('goaltip');
    if (!tipEl) return;
    tipEl.classList.remove('visible');
    tipEl.setAttribute('aria-hidden', 'true');
    if (tipActiveEl) {
        tipActiveEl.removeAttribute('aria-describedby');
        tipActiveEl = null;
    }
}

function tipTargetFromEvent(e) {
    const el = e && e.target ? e.target : null;
    return el && el.closest ? el.closest('[data-tooltip]') : null;
}

function bindTooltipSystem() {
    document.addEventListener('mouseover', function (e) {
        const tEl = tipTargetFromEvent(e);
        if (tEl && tEl !== tipActiveEl) tipShow(tEl);
    });
    document.addEventListener('mouseout', function (e) {
        if (!tipActiveEl) return;
        const related = tipTargetFromEvent({ target: e.relatedTarget });
        if (related !== tipActiveEl) tipHide();
    });
    document.addEventListener('focusin', function (e) {
        const tEl = tipTargetFromEvent(e);
        if (tEl && tEl !== tipActiveEl) tipShow(tEl);
    });
    document.addEventListener('focusout', function (e) {
        if (!tipActiveEl) return;
        // Only hide when focus actually leaves the trigger (not on unrelated
        // focus changes elsewhere, which would hide a hover tooltip).
        const from = e.target;
        if (from === tipActiveEl || (from && from.contains && from.contains(tipActiveEl))) tipHide();
    });
    window.addEventListener('scroll', function () { tipHide(); }, true);
    window.addEventListener('resize', function () { tipHide(); });
}

// ─── Shared responsive contract: TABLE iff it fits, otherwise CARDS ─────────
// One contract for every list view (Runtimes / Instances / History), driven by
// the ACTUAL content width (the wrapper's own width, sidebar included) — never
// by a viewport breakpoint. The table wrapper is overflow:hidden (a horizontal
// table scrollbar is a defect, not a state) and the mode is re-evaluated after
// every render, every language switch, and on any size change via
// ResizeObserver. While in card mode the table stays laid out at the same
// width but invisible (height 0), so its scrollWidth is a live measurement of
// the width the table would require.
const fitViews = [
    { table: 'runtimes-table-wrap', cards: 'runtimes-compact' },
    { table: 'instances-table-wrap', cards: 'instances-compact' },
    { table: 'history-table-wrap', cards: 'history-compact' }
];

function applyFit(view) {
    const wrap = document.getElementById(view.table);
    const cards = document.getElementById(view.cards);
    if (!wrap || !cards) return;
    if (wrap.dataset.has !== '1') {
        wrap.classList.remove('fit-table');
        cards.classList.remove('visible');
        return;
    }
    const fits = wrap.scrollWidth <= wrap.clientWidth + 1;
    wrap.classList.toggle('fit-table', fits);
    cards.classList.toggle('visible', !fits);
}

function syncAllFit() {
    fitViews.forEach(applyFit);
}

let fitObserversBound = false;

function bindFitObservers() {
    if (fitObserversBound) return;
    fitObserversBound = true;
    if (typeof ResizeObserver === 'function') {
        const ro = new ResizeObserver(function () { syncAllFit(); });
        fitViews.forEach(function (v) {
            const el = document.getElementById(v.table);
            if (el) ro.observe(el);
        });
    } else {
        window.addEventListener('resize', syncAllFit);
    }
}

// ─── Modal helpers ──────────────────────────────────────────────────────────

function closeModal(id) { document.getElementById(id).style.display = 'none'; }

function isWizardOpen() {
    const w = document.getElementById('wizard-modal');
    return !!w && w.style.display === 'flex';
}

function isPipelineModalOpen() {
    const m = document.getElementById('pipeline-modal');
    return !!m && m.style.display === 'flex';
}

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

// ─── Server connection monitor ────────────────────────────────────────────

let serverOnline = true;
let healthProbeInFlight = false;
let healthTimer = null;

async function probeServer() {
    if (healthProbeInFlight) return;
    healthProbeInFlight = true;
    try {
        // Any HTTP response (even non-2xx) proves the server is reachable;
        // only a fetch-level network failure means the connection is down.
        // The body is consumed (and discarded): an unconsumed fetch body keeps
        // the request in-flight in the browser's network stack.
        const r = await fetch('/api/v1/health', { cache: 'no-store' });
        await r.text();
        setServerOnline(true);
    } catch {
        setServerOnline(false);
    } finally {
        healthProbeInFlight = false;
    }
}

function setServerOnline(online) {
    if (serverOnline === online) return;
    serverOnline = online;
    const dot = document.getElementById('server-dot');
    if (dot) dot.className = 'status-dot ' + (online ? 'online' : 'offline');
    const banner = document.getElementById('conn-banner');
    if (banner) banner.style.display = online ? 'none' : 'flex';
    if (online) showToast(t('conn.restored'), 'success');
}

function startHealthMonitor() {
    if (healthTimer) return;
    probeServer();
    healthTimer = setInterval(probeServer, 5000);
}

window.addEventListener('offline', function () {
    setServerOnline(false);
});
window.addEventListener('online', function () {
    probeServer();
});

// ─── Refresh loop ───────────────────────────────────────────────────────────

function startRefresh() {
    if (refreshTimer) clearInterval(refreshTimer);
    refreshTimer = setInterval(async function () {
        await reloadAllData();
        renderAll();
    }, 3000);
}

// ─── Mobile Drawer ──────────────────────────────────────────────────────────

function toggleDrawer() {
    const sidebar = document.getElementById('sidebar');
    const overlay = document.getElementById('drawer-overlay');
    const isOpen = sidebar.classList.contains('open');
    if (isOpen) { closeDrawer(); } else {
        sidebar.classList.add('open');
        overlay.classList.add('open');
    }
}

function closeDrawer() {
    document.getElementById('sidebar').classList.remove('open');
    document.getElementById('drawer-overlay').classList.remove('open');
}

if (window.matchMedia) {
    window.matchMedia('(max-width: 768px)').addEventListener('change', function () {
        closeDrawer();
    });
}

document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
        closeDrawer();
    }
});

// Override navigate to close drawer on mobile
const _origNavigate = navigate;
navigate = function (view) {
    _origNavigate(view);
    closeDrawer();
};

// ─── Settings Edit ──────────────────────────────────────────────────────────

function openSettingsEdit() {
    const m = lastMetrics || {};
    document.getElementById('set-edit-listen').value = m.listen_address || '127.0.0.1';
    document.getElementById('set-edit-port').value = m.web_port || 8088;
    document.getElementById('set-edit-auth').checked = !!m.auth_enabled;
    document.getElementById('set-edit-admin-user').value = m.admin_user || '';
    document.getElementById('set-edit-admin-pass').value = '';
    document.getElementById('set-edit-admin-pass2').value = '';
    onSettingsAuthToggle();
    document.getElementById('settings-edit-modal').style.display = 'flex';
}

function closeSettingsEdit() {
    document.getElementById('settings-edit-modal').style.display = 'none';
}

function onSettingsAuthToggle() {
    const auth = document.getElementById('set-edit-auth').checked;
    document.getElementById('set-edit-auth-section').style.display = auth ? '' : 'none';
    updateAdminPassHint();
}

function updateAdminPassHint() {
    const hint = document.getElementById('set-edit-admin-pass-hint');
    const pwSet = !!(lastMetrics && lastMetrics.admin_password_set);
    hint.textContent = pwSet ? t('settings.auth.keep_hint') : '';
}

async function saveSettingsEdit() {
    const addr = document.getElementById('set-edit-listen').value.trim();
    const port = parseInt(document.getElementById('set-edit-port').value) || 0;
    const auth = document.getElementById('set-edit-auth').checked;
    if (!addr) { showToast(t('settings.edit.err.addr'), 'error'); return; }
    if (port < 1 || port > 65535) { showToast(t('settings.edit.err.port'), 'error'); return; }

    let adminUser = '';
    let adminPassword = '';
    if (auth) {
        adminUser = document.getElementById('set-edit-admin-user').value.trim();
        adminPassword = document.getElementById('set-edit-admin-pass').value;
        const adminPassword2 = document.getElementById('set-edit-admin-pass2').value;
        if (!adminUser) { showToast(t('settings.edit.err.user'), 'error'); return; }
        // An empty password is only acceptable when one is already configured
        // (it then means "keep the current password"); otherwise it is required.
        const pwSet = !!(lastMetrics && lastMetrics.admin_password_set);
        if (!adminPassword && !pwSet) { showToast(t('settings.edit.err.password'), 'error'); return; }
        if (adminPassword && adminPassword !== adminPassword2) { showToast(t('settings.edit.err.password_match'), 'error'); return; }
        if (adminPassword && new TextEncoder().encode(adminPassword).length > 72) { showToast(t('settings.edit.err.password_length'), 'error'); return; }
    }

    const body = { listen_address: addr, web_port: port, auth_enabled: auth };
    if (auth) {
        body.admin_user = adminUser;
        if (adminPassword) body.admin_password = adminPassword;
    }

    const btn = document.getElementById('settings-save-btn');
    btn.disabled = true;
    const oldLabel = btn.textContent;
    btn.textContent = t('common.loading');
    try {
        await api('/settings', { method: 'PUT', body: JSON.stringify(body) });
        // api() throws on any non-2xx, so reaching here guarantees success.
        closeSettingsEdit();
        showToast(t('settings.edit.saved'), 'success');
    } catch (e) {
        // 4xx/5xx or network failure: keep the modal open with the entered
        // values intact, reset the button label, and surface a localized error.
        showToast(friendlyError(e), 'error');
    } finally {
        btn.disabled = false;
        btn.textContent = oldLabel;
    }
}

// ─── Portable Configuration ─────────────────────────────────────────────────

let portableImportFileContent = null;

function portableUpdateEntitySelector() {
    const scopeSel = document.getElementById('portable-export-scope');
    const entitySel = document.getElementById('portable-export-entity');
    if (!scopeSel || !entitySel) return;
    const scope = scopeSel.value;
    if (!scope) {
        entitySel.style.display = 'none';
        entitySel.innerHTML = '';
        return;
    }
    entitySel.style.display = '';
    let options = '<option value="">' + t('portable.export.scope') + '</option>';
    if (scope === 'runtime') {
        options += runtimesData.map(function (r) { return '<option value="' + esc(r.id) + '">' + esc(r.name) + '</option>'; }).join('');
    } else if (scope === 'model') {
        options += modelsData.map(function (m) { return '<option value="' + esc(m.id) + '">' + esc(m.name) + '</option>'; }).join('');
    } else if (scope === 'pipeline') {
        options += pipelinesData.map(function (p) { return '<option value="' + esc(p.id) + '">' + esc(p.name) + '</option>'; }).join('');
    }
    entitySel.innerHTML = options;
}

function portableSelectScope() {
    portableUpdateEntitySelector();
}

async function portableExport() {
    const btn = document.getElementById('portable-export-btn');
    const scopeSel = document.getElementById('portable-export-scope');
    const entitySel = document.getElementById('portable-export-entity');
    const scope = scopeSel.value;
    let query = '';
    if (scope === 'runtime') query = '?runtime_id=' + encodeURIComponent(entitySel.value);
    else if (scope === 'model') query = '?model_id=' + encodeURIComponent(entitySel.value);
    else if (scope === 'pipeline') query = '?pipeline_id=' + encodeURIComponent(entitySel.value);
    btn.disabled = true;
    const oldLabel = btn.textContent;
    btn.textContent = t('common.loading');
    try {
        const r = await fetch('/api/v1/export' + query, { headers: { 'Accept': 'application/json' } });
        if (!r.ok) {
            let msg = r.statusText;
            try { const d = await r.json(); msg = d.error || msg; } catch {}
            throw new Error(msg);
        }
        const blob = await r.blob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = 'goal-portable-config.json';
        document.body.appendChild(a);
        a.click();
        a.remove();
        URL.revokeObjectURL(url);
        showToast(t('portable.export.success'), 'success');
    } catch (e) {
        showToast(t('portable.export.error') + ': ' + (e.message || e), 'error');
    } finally {
        btn.disabled = false;
        btn.textContent = oldLabel;
    }
}

// The visible picker is a GoAl button; the native input stays a real
// input[type=file] (so selection semantics are unchanged) but clipped out of
// view, which keeps its caption under GoAl i18n control instead of the OS.
function portableChooseFile() {
    const input = document.getElementById('portable-import-file');
    if (input) input.click();
}

function portableSetFileLabel(name) {
    const el = document.getElementById('portable-import-filename');
    if (!el) return;
    if (name) {
        delete el.dataset.i18n;
        el.textContent = name;
        el.classList.add('has-file');
    } else {
        el.dataset.i18n = 'portable.import.no_file';
        el.textContent = t('portable.import.no_file');
        el.classList.remove('has-file');
    }
}

function portableOnFileChange() {
    const fileInput = document.getElementById('portable-import-file');
    const file = fileInput.files[0];
    portableImportFileContent = null;
    const iBtn = document.getElementById('portable-import-btn');
    const vBtn = document.getElementById('portable-validate-btn');
    const result = document.getElementById('portable-import-result');
    if (iBtn) iBtn.disabled = true;
    if (result) { result.style.display = 'none'; result.innerHTML = ''; }
    if (!file) { portableSetFileLabel(null); return; }
    portableSetFileLabel(file.name);
    if (file.size > 10 * 1024 * 1024) {
        portableShowResult('error', esc(t('portable.import.file_too_large')));
        fileInput.value = '';
        portableSetFileLabel(null);
        if (vBtn) vBtn.disabled = true;
        return;
    }
    const reader = new FileReader();
    reader.onload = function () {
        portableImportFileContent = reader.result;
        if (vBtn) vBtn.disabled = false;
    };
    reader.onerror = function () {
        portableShowResult('error', esc(t('portable.import.invalid')));
    };
    reader.readAsText(file);
}

function portableResetImport() {
    portableImportFileContent = null;
    const vBtn = document.getElementById('portable-validate-btn');
    const iBtn = document.getElementById('portable-import-btn');
    const fileInput = document.getElementById('portable-import-file');
    if (vBtn) vBtn.disabled = true;
    if (iBtn) iBtn.disabled = true;
    if (fileInput) fileInput.value = '';
    portableSetFileLabel(null);
    const result = document.getElementById('portable-import-result');
    if (result) { result.style.display = 'none'; result.innerHTML = ''; }
}

function portableShowResult(type, html) {
    const el = document.getElementById('portable-import-result');
    el.innerHTML = '<div class="portable-result-' + type + '">' + html + '</div>';
    el.style.display = '';
}

// countsTotal sums one {runtimes, models, pipelines} count object.
function countsTotal(obj) {
    if (!obj) return 0;
    return (obj.runtimes || 0) + (obj.models || 0) + (obj.pipelines || 0);
}

// planListHtml renders the per-type NEW/EXISTING breakdown. It is shown for
// every outcome that parsed a valid file — the plan is about the repository,
// not about the file being correct or not.
function planListHtml(summary) {
    if (!summary) return '';
    const rows = [
        ['runtimes', summary.runtimes, 'portable.import.plan.runtimes'],
        ['models', summary.models, 'portable.import.plan.models'],
        ['pipelines', summary.pipelines, 'portable.import.plan.pipelines'],
    ];
    const lines = [];
    rows.forEach(function (row) {
        const s = row[1];
        if (!s || !s.total) return;
        // new + existing + blocked always adds up to the type total, so a
        // blocked entity is visible in the plan as well as in the reason list.
        const blocked = s.blocked ? t('portable.import.plan.blocked_suffix', { blocked: s.blocked }) : '';
        lines.push('<li>' + esc(t(row[2], { new: s.new, existing: s.existing }) + blocked) + '</li>');
    });
    return lines.length ? '<ul class="portable-plan-list">' + lines.join('') + '</ul>' : '';
}

function planTotalsHtml(created, skipped) {
    const parts = [];
    if (created) parts.push(esc(t('portable.import.plan.will_import', { count: created })));
    if (skipped) parts.push(esc(t('portable.import.plan.will_skip', { count: skipped })));
    return parts.length ? '<p class="portable-plan-totals">' + parts.join(' · ') + '</p>' : '';
}

// BLOCKED_KEYS maps the storage reason codes to user-facing text. An unknown
// code falls back to the raw-reason frame rather than leaking a missing key.
const PORTABLE_BLOCKED_KEYS = {
    runtime_name_taken_other_id: 'portable.import.blocked_reason.runtime_name',
    runtime_ref_unresolved: 'portable.import.blocked_reason.runtime_ref',
    model_ref_unresolved: 'portable.import.blocked_reason.model_ref',
    dependency_blocked: 'portable.import.blocked_reason.dependency',
};

function blockedReasonText(c) {
    const key = PORTABLE_BLOCKED_KEYS[c.reason] || 'portable.import.blocked_reason.other';
    return t(key, { name: c.name || c.id, ref: c.related_id || '', id: c.id, reason: c.reason });
}

// technicalDetailsHtml keeps the machine-readable conflict strings available for
// diagnostics below the localized explanation, never instead of it.
function technicalDetailsHtml(items) {
    if (!items || !items.length) return '';
    let html = '<details class="portable-plan-details"><summary>' + esc(t('portable.import.details')) + '</summary><ul class="portable-result-conflicts">';
    items.forEach(function (c) {
        let line = c.type + ' ' + c.id + ': ' + c.reason;
        if (c.name) line += ' (name: ' + c.name + ')';
        if (c.related_id) line += ' (existing: ' + c.related_id + ')';
        html += '<li>' + esc(line) + '</li>';
    });
    return html + '</ul></details>';
}

// portableShowPlan renders an import plan returned by a request that DID parse
// the file. Repository conflicts are shown as a plan with reasons, never as a
// bare "invalid configuration", so file validity and repository state stay two
// separate statements. The Import button follows the server-side can_import flag.
function portableShowPlan(d) {
    const iBtn = document.getElementById('portable-import-btn');
    const blocked = Array.isArray(d.blocked) ? d.blocked : [];
    const created = countsTotal(d.created);
    const skipped = countsTotal(d.skipped);
    if (iBtn) iBtn.disabled = true;
    if (blocked.length) {
        let html = '<p class="portable-plan-title">' + esc(t('portable.import.blocked_title')) + '</p>' +
            planListHtml(d.summary) + '<ul class="portable-plan-blocked">';
        blocked.forEach(function (c) { html += '<li>' + esc(blockedReasonText(c)) + '</li>'; });
        portableShowResult('error', html + '</ul>' + technicalDetailsHtml(blocked));
        return;
    }
    if (!created) {
        portableShowResult('info', '<p class="portable-plan-title">' + esc(t('portable.import.nothing_to_import')) + '</p>' +
            planListHtml(d.summary) + planTotalsHtml(0, skipped));
        return;
    }
    portableShowResult('success', '<p class="portable-plan-title">' + esc(t('portable.import.plan_valid')) + '</p>' +
        planListHtml(d.summary) + planTotalsHtml(created, skipped));
    if (iBtn) iBtn.disabled = !d.can_import;
}

async function portableValidate() {
    const vBtn = document.getElementById('portable-validate-btn');
    const iBtn = document.getElementById('portable-import-btn');
    if (!portableImportFileContent) return;
    vBtn.disabled = true;
    iBtn.disabled = true;
    const oldLabel = vBtn.textContent;
    vBtn.textContent = t('common.loading');
    const result = document.getElementById('portable-import-result');
    result.style.display = 'none';
    result.innerHTML = '';
    try {
        const r = await fetch('/api/v1/import?dry_run=true', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
            body: portableImportFileContent
        });
        let d = null;
        try { d = await r.json(); } catch {}
        const detail = friendlyError({ message: (d && d.error) || r.statusText, code: (d && d.code) || '', status: r.status });
        if (r.status === 200 || r.status === 409) {
            portableShowPlan(d || {});
        } else {
            // The heading must not claim more than the status supports: 400 and
            // 413 say something about the file, a server fault says something
            // about the check, and neither is a repository conflict.
            const heading = r.status === 400 ? 'portable.import.file_invalid'
                : r.status === 413 ? 'portable.import.file_too_large'
                    : 'portable.import.validate_failed';
            portableShowResult('error', '<p class="portable-plan-title">' + esc(t(heading)) + '</p>' +
                '<ul class="portable-plan-list"><li>' + esc(detail) + '</li></ul>');
        }
    } catch (e) {
        portableShowResult('error', esc(friendlyError(e)));
    } finally {
        vBtn.disabled = false;
        vBtn.textContent = oldLabel;
    }
}

async function portableImport() {
    if (!portableImportFileContent) return;
    const content = portableImportFileContent;
    showConfirm(t('portable.import.confirm'), async function () {
        const iBtn = document.getElementById('portable-import-btn');
        const oldLabel = iBtn.textContent;
        iBtn.disabled = true;
        iBtn.textContent = t('common.loading');
        try {
            const r = await fetch('/api/v1/import', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken },
                body: content
            });
            if (!r.ok) {
                let d = null;
                try { d = await r.json(); } catch {}
                closeConfirm();
                if (r.status === 409) {
                    // The repository moved after validation: the fresh plan is
                    // shown instead of the stale one, and nothing was written.
                    portableShowPlan(d || {});
                    showToast(t('portable.import.stale'), 'warning');
                } else {
                    const msg = friendlyError({ message: (d && d.error) || r.statusText, code: (d && d.code) || '', status: r.status });
                    portableShowResult('error', '<p class="portable-plan-title">' + esc(t('portable.import.error')) + '</p>' +
                        '<ul class="portable-plan-list"><li>' + esc(msg) + '</li></ul>');
                    showToast(t('portable.import.error'), 'error');
                }
                iBtn.disabled = true;
                return;
            }
            const d = await r.json();
            closeConfirm();
            const created = countsTotal(d.created);
            const skipped = countsTotal(d.skipped);
            portableResetImport();
            const done = t('portable.import.success', { created: created, skipped: skipped });
            portableShowResult('success', '<p class="portable-plan-title">' + esc(done) + '</p>');
            showToast(done, 'success');
            await reloadAllData();
            renderAll();
        } catch (e) {
            closeConfirm();
            portableShowResult('error', esc(friendlyError(e)));
            showToast(t('portable.import.error'), 'error');
        } finally {
            iBtn.textContent = oldLabel;
        }
    });
}

let lastMetrics = {};

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
window.dismissInstance = dismissInstance;
window.killInstance = killInstance;
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
window.renderRtDropdown = renderRtDropdown;
window.rtDropdownKeydown = rtDropdownKeydown;
window.selectRtItem = selectRtItem;
window.setTheme = setTheme;
window.setLanguage = setLanguage;
window.friendlyError = friendlyError;
window.translateServerMessage = translateServerMessage;
window.t = t;
window.parseArgs = parseArgs;
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
window.toggleDrawer = toggleDrawer;
window.closeDrawer = closeDrawer;
window.openSettingsEdit = openSettingsEdit;
window.closeSettingsEdit = closeSettingsEdit;
window.saveSettingsEdit = saveSettingsEdit;
window.onSettingsAuthToggle = onSettingsAuthToggle;
window.renderPipelines = renderPipelines;
window.startPipeline = startPipeline;
window.stopPipeline = stopPipeline;
window.restartPipeline = restartPipeline;
window.deletePipeline = deletePipeline;
window.togglePipelineActive = togglePipelineActive;
window.openCreatePipelineModal = openCreatePipelineModal;
window.editPipeline = editPipeline;
window.handlePipelineSubmit = handlePipelineSubmit;
window.renderPlBuilder = renderPlBuilder;
window.plAddEntry = plAddEntry;
window.plRemoveEntry = plRemoveEntry;
window.plMoveEntry = plMoveEntry;
window.plSetModel = plSetModel;
window.plSetArgsMode = plSetArgsMode;
window.plEntryArgsText = plEntryArgsText;
window.plDragStart = plDragStart;
window.plDragEnd = plDragEnd;
window.plDragOver = plDragOver;
window.plDragLeave = plDragLeave;
window.plDrop = plDrop;
window.portableSelectScope = portableSelectScope;
window.portableExport = portableExport;
window.portableOnFileChange = portableOnFileChange;
window.portableChooseFile = portableChooseFile;
window.portableValidate = portableValidate;
window.portableImport = portableImport;
window.i18nMissing = i18nMissing;

// ─── Boot ───────────────────────────────────────────────────────────────────

init();
})();

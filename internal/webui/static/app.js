// GoAl WebUI - Full featured JavaScript
'use strict';

(function() {
    // State
    let csrfToken = '';
    let sessionToken = '';
    let eventSource = null;
    let currentLogFilter = '';
    let logSearchTerm = '';
    let profilesData = [];
    let runtimesData = [];
    let modelsData = [];
    let isAuthenticated = false;

    // ========== CSRF ==========
    function getCSRFToken() {
        const match = document.cookie.match(/(?:^|;\s*)goal_csrf_token=([^;]+)/);
        return match ? match[1] : '';
    }

    function updateCSRF() {
        csrfToken = getCSRFToken();
    }

    // ========== Auth ==========
    window.handleLogin = async function(event) {
        event.preventDefault();
        const username = document.getElementById('username').value;
        const password = document.getElementById('password').value;
        const errorEl = document.getElementById('login-error');

        try {
            const response = await fetch('/api/v1/auth/login', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken
                },
                credentials: 'same-origin',
                body: JSON.stringify({ username, password })
            });

            if (response.ok) {
                const data = await response.json();
                csrfToken = data.csrf_token || data.csrf || '';
                isAuthenticated = true;
                document.getElementById('login-modal').style.display = 'none';
                document.getElementById('user-info').style.display = 'flex';
                document.getElementById('username-display').textContent = username;
                reloadAllData();
            } else {
                const data = await response.json().catch(() => ({}));
                errorEl.textContent = data.error || 'Authentication failed';
                errorEl.style.display = 'block';
            }
        } catch (err) {
            errorEl.textContent = 'Login request failed: ' + err.message;
            errorEl.style.display = 'block';
        }
        return false;
    };

    window.handleLogout = async function() {
        await fetch('/api/v1/auth/logout', {
            method: 'POST',
            headers: { 'X-CSRF-Token': csrfToken },
            credentials: 'same-origin'
        });
        isAuthenticated = false;
        document.getElementById('user-info').style.display = 'none';
        document.getElementById('login-modal').style.display = 'flex';
    };

    async function checkAuth() {
        try {
            const response = await fetch('/api/v1/auth/session');
            const data = await response.json();
            if (data.authenticated === true || data.authenticated === 'true') {
                isAuthenticated = true;
                csrfToken = getCSRFToken();
                document.getElementById('user-info').style.display = 'flex';
            } else {
                document.getElementById('login-modal').style.display = 'flex';
            }
        } catch {
            document.getElementById('login-modal').style.display = 'flex';
        }
    }

    // ========== Data Loading ==========
    async function loadProfiles() {
        try {
            const response = await fetch('/api/v1/profiles', {
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                profilesData = await response.json();
                renderProfiles(profilesData);
            }
        } catch { /* ignore */ }
    }

    async function loadRuntimes() {
        try {
            const response = await fetch('/api/v1/runtimes', {
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                runtimesData = await response.json();
                renderRuntimes(runtimesData);
            }
        } catch { /* ignore */ }
    }

    async function loadModels() {
        try {
            const response = await fetch('/api/v1/models', {
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                modelsData = await response.json();
                renderModels(modelsData);
            }
        } catch { /* ignore */ }
    }

    async function reloadAllData() {
        await Promise.all([loadProfiles(), loadRuntimes(), loadModels(), loadInstances()]);
    }

    // ========== Status ==========
    async function refreshDashboard() {
        // Update dashboard with instance counts
        try {
            const resp = await fetch('/api/v1/metrics', {
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (resp.ok) {
                const data = await resp.json();
                const runningEl = document.getElementById('dashboard-running');
                const totalEl = document.getElementById('dashboard-total');
                const runtimesEl = document.getElementById('dashboard-runtimes');
                if (runningEl) runningEl.textContent = data.running || 0;
                if (totalEl) totalEl.textContent = data.total_instances || 0;
                if (runtimesEl) runtimesEl.textContent = runtimesData.length;
            }
        } catch { /* ignore */ }

        // Update version
        try {
            const resp = await fetch('/api/v1/version', {
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (resp.ok) {
                const data = await resp.json();
                const verEl = document.getElementById('dashboard-version');
                if (verEl) verEl.textContent = data.version || '-';
            }
        } catch { /* ignore */ }
    }

    // ========== Runtimes ==========
    function renderRuntimes(runtimes) {
        const tbody = document.querySelector('#runtimes tbody');
        if (!tbody) return;
		const emptyEl = document.querySelector('#runtimes .empty');

        if (runtimes.length === 0) {
            if (emptyEl) emptyEl.style.display = 'block';
            tbody.style.display = 'none';
			const countEl = document.querySelector('#runtimes .count');
			if (countEl) countEl.textContent = '(0)';
            return;
        }

        if (emptyEl) emptyEl.style.display = 'none';
        tbody.style.display = '';
		const countEl = document.querySelector('#runtimes .count');
		if (countEl) countEl.textContent = '(' + runtimes.length + ')';

        tbody.innerHTML = runtimes.map(r => `
            <tr>
                <td>${escapeHtml(r.name)}</td>
                <td><code>${escapeHtml(r.executable)}</code></td>
                <td><code>${escapeHtml(r.working_directory || '-')}</code></td>
                <td>${(r.default_args || []).map(a => '<span class="arg">' + escapeHtml(a) + '</span>').join('')}</td>
                <td><code>${escapeHtml(r.id)}</code></td>
                <td class="actions">
                    <button onclick="showEditRuntimeModal(${safeInlineArg(r.id)})" class="btn btn-secondary btn-sm">Edit</button>
                    <button onclick="deleteRuntime(${safeInlineArg(r.id)})" class="btn btn-danger btn-sm">Delete</button>
                </td>
            </tr>
        `).join('');
    }

    window.showCreateRuntimeModal = function() {
        const modal = document.getElementById('create-runtime-modal');
        if (modal) modal.style.display = 'flex';
        const err = document.getElementById('create-runtime-error');
        if (err) { err.style.display = 'none'; }
        const form = document.getElementById('create-runtime-form');
        if (form) form.reset();
    };

    window.closeCreateRuntimeModal = function() {
        const modal = document.getElementById('create-runtime-modal');
        if (modal) modal.style.display = 'none';
    };

    window.showEditRuntimeModal = function(id) {
        const modal = document.getElementById('edit-runtime-modal');
        if (!modal) return;
        modal.style.display = 'flex';
        const errEl = document.getElementById('edit-runtime-error');
        if (errEl) errEl.style.display = 'none';

        // Fetch current runtime data
        fetch('/api/v1/runtimes/' + encodeURIComponent(id), {
            headers: { 'X-CSRF-Token': csrfToken },
            credentials: 'same-origin'
        })
        .then(r => r.ok ? r.json() : Promise.reject('not found'))
        .then(rt => {
            document.getElementById('edit-runtime-id').value = rt.id;
            document.getElementById('edit-runtime-name').value = rt.name || '';
            document.getElementById('edit-runtime-executable').value = rt.executable || '';
            document.getElementById('edit-runtime-workdir').value = rt.working_directory || '';
            document.getElementById('edit-runtime-args').value = (rt.default_args || []).join(' ');
        })
        .catch(() => {
            const errEl = document.getElementById('edit-runtime-error');
            if (errEl) { errEl.textContent = 'Failed to load runtime data'; errEl.style.display = 'block'; }
            closeEditRuntimeModal();
        });
    };

    window.closeEditRuntimeModal = function() {
        const modal = document.getElementById('edit-runtime-modal');
        if (modal) modal.style.display = 'none';
    };

    window.handleEditRuntime = async function(event) {
        event.preventDefault();
        const id = document.getElementById('edit-runtime-id').value;
        const errEl = document.getElementById('edit-runtime-error');
        if (errEl) { errEl.style.display = 'none'; errEl.textContent = ''; }

        const name = document.getElementById('edit-runtime-name').value.trim();
        const executable = document.getElementById('edit-runtime-executable').value.trim();
        const workdir = document.getElementById('edit-runtime-workdir').value.trim();
        const argsText = document.getElementById('edit-runtime-args').value.trim();

        if (!name) {
            if (errEl) { errEl.textContent = 'Name is required'; errEl.style.display = 'block'; }
            return false;
        }
        if (!executable) {
            if (errEl) { errEl.textContent = 'Executable path is required'; errEl.style.display = 'block'; }
            return false;
        }

        const defaultArgs = argsText ? argsText.split(/\s+/).filter(a => a.length > 0) : [];

        const body = {
            id: id,
            name: name,
            executable: executable,
            working_directory: workdir,
            default_args: defaultArgs
        };

        try {
            const response = await fetch('/api/v1/runtimes/' + encodeURIComponent(id), {
                method: 'PUT',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken
                },
                credentials: 'same-origin',
                body: JSON.stringify(body)
            });

            if (response.ok) {
                closeEditRuntimeModal();
                await loadRuntimes();
                showToast('Runtime updated', 'success');
                return false;
            }

            let msg = 'Failed to update runtime';
            try {
                const data = await response.json();
                msg = data.error || msg;
            } catch (e2) {}

            if (errEl) { errEl.textContent = msg; errEl.style.display = 'block'; }
        } catch (err) {
            if (errEl) { errEl.textContent = 'Request failed: ' + err.message; errEl.style.display = 'block'; }
        }
        return false;
    };

    // Create Runtime submission
    window.handleCreateRuntime = async function(event) {
        event.preventDefault();
        const errEl = document.getElementById('create-runtime-error');
        if (errEl) { errEl.style.display = 'none'; errEl.textContent = ''; }

        const name = document.getElementById('runtime-name').value.trim();
        const executable = document.getElementById('runtime-executable').value.trim();
        const workdir = document.getElementById('runtime-workdir').value.trim();
        const argsText = document.getElementById('runtime-args').value.trim();

        if (!name) {
            if (errEl) { errEl.textContent = 'Name is required'; errEl.style.display = 'block'; }
            return false;
        }
        if (!executable) {
            if (errEl) { errEl.textContent = 'Executable path is required'; errEl.style.display = 'block'; }
            return false;
        }

        const defaultArgs = argsText ? argsText.split(/\s+/).filter(a => a.length > 0) : [];

        const body = {
            name: name,
            executable: executable,
            working_directory: workdir,
            default_args: defaultArgs
        };

        try {
            const response = await fetch('/api/v1/runtimes', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken
                },
                credentials: 'same-origin',
                body: JSON.stringify(body)
            });

            if (response.ok) {
                closeCreateRuntimeModal();
                await loadRuntimes();
                await refreshDashboard();
                return false;
            }

            let msg = 'Failed to create runtime';
            try {
                const data = await response.json();
                msg = data.error || msg;
            } catch (e2) {}

            if (errEl) { errEl.textContent = msg; errEl.style.display = 'block'; }
        } catch (err) {
            if (errEl) { errEl.textContent = 'Request failed: ' + err.message; errEl.style.display = 'block'; }
        }
        return false;
    };

    // ---------- Toast & Confirm infrastructure ----------
    function showToast(message, kind) {
        const container = document.getElementById('toast-container');
        if (!container) return;

        const toast = document.createElement('div');
        const bg = kind === 'success' ? '#2ecc71' :
                   kind === 'error' ? '#e74c3c' :
                   '#9a9ab0';
        toast.style.cssText = `background:${bg};color:white;padding:0.75rem 1rem;border-radius:8px;font-size:0.9rem;box-shadow:0 4px 12px rgba(0,0,0,0.3);opacity:1;transition:opacity 0.3s;pointer-events:auto;`;
        toast.textContent = message;
        container.appendChild(toast);
        setTimeout(() => { toast.style.opacity = '0'; setTimeout(() => toast.remove(), 300); }, 2500);
    }
    window.showToast = showToast;

    function showConfirmModal(message, onConfirm) {
        const modal = document.getElementById('confirm-modal');
        const msg = document.getElementById('confirm-message');
        if (!modal || !msg) { return onConfirm && onConfirm(); }
        msg.textContent = message;
        const yesBtn = document.getElementById('confirm-modal-yes');
        yesBtn.onclick = () => { closeConfirmModal(); onConfirm && onConfirm(); };
        modal.style.display = 'flex';
    }

    window.closeConfirmModal = function() {
        const modal = document.getElementById('confirm-modal');
        if (modal) modal.style.display = 'none';
    };

    // ========== Runtime CRUD ==========
    window.deleteRuntime = function(id) {
        showConfirmModal('Delete runtime "' + id + '"?', async function() {
            try {
                const response = await fetch('/api/v1/runtimes/' + encodeURIComponent(id), {
                    method: 'DELETE',
                    headers: { 'X-CSRF-Token': csrfToken },
                    credentials: 'same-origin'
                });
                if (response.ok) {
                    showToast('Runtime deleted', 'success');
                    await loadRuntimes();
                } else {
                    const text = await response.text();
                    showToast('Failed to delete runtime: ' + text, 'error');
                }
            } catch (err) {
                showToast('Delete failed: ' + err.message, 'error');
            }
        });
    };

    // ========== Models ==========
    function renderModels(models) {
        const tbody = document.querySelector('#models tbody');
        if (!tbody) return;
		const emptyEl = document.querySelector('#models .empty');

        if (models.length === 0) {
            if (emptyEl) emptyEl.style.display = 'block';
            tbody.style.display = 'none';
			const countEl = document.querySelector('#models .count');
			if (countEl) countEl.textContent = '(0)';
            return;
        }

        if (emptyEl) emptyEl.style.display = 'none';
        tbody.style.display = '';
		const countEl = document.querySelector('#models .count');
		if (countEl) countEl.textContent = '(' + models.length + ')';

        // Build runtime lookup for display
        const runtimeMap = {};
        runtimesData.forEach(r => { runtimeMap[r.id] = r.name; });

        tbody.innerHTML = models.map(m => `
            <tr>
                <td>${escapeHtml(m.name)}</td>
                <td>${escapeHtml(m.runtime_id ? (runtimeMap[m.runtime_id] || m.runtime_id) : '-')}</td>
                <td><code>${escapeHtml(m.path || '-')}</code></td>
                <td>${escapeHtml(m.mmproj || '-')}</td>
                <td>${escapeHtml(m.format || '-')}</td>
                <td><code>${escapeHtml(m.id)}</code></td>
                <td class="actions">
                    <button onclick="showEditModelModal(${safeInlineArg(m.id)})" class="btn btn-secondary btn-sm">Edit</button>
                    <button onclick="deleteModel(${safeInlineArg(m.id)})" class="btn btn-danger btn-sm">Delete</button>
                </td>
            </tr>
        `).join('');
    }

    window.showCreateModelModal = function() {
        const modal = document.getElementById('create-model-modal');
        if (modal) modal.style.display = 'flex';
        const err = document.getElementById('create-model-error');
        if (err) { err.style.display = 'none'; }
        const form = document.getElementById('create-model-form');
        if (form) form.reset();
        loadRuntimeSelectIntoModel();
        updateModelKindVisibility();
    };

    window.closeCreateModelModal = function() {
        const modal = document.getElementById('create-model-modal');
        if (modal) modal.style.display = 'none';
    };

    window.showEditModelModal = function(id) {
        const modal = document.getElementById('edit-model-modal');
        if (!modal) return;
        modal.style.display = 'flex';
        const errEl = document.getElementById('edit-model-error');
        if (errEl) errEl.style.display = 'none';
        const errPath = document.getElementById('edit-model-path-error');
        const errArgs = document.getElementById('edit-model-args-error');
        if (errPath) errPath.style.display = 'none';
        if (errArgs) errArgs.style.display = 'none';

        // Clear form
        document.getElementById('edit-model-id').value = '';
        document.getElementById('edit-model-name').value = '';
        document.getElementById('edit-model-kind').value = '';
        document.getElementById('edit-model-path').value = '';
        document.getElementById('edit-model-arguments').value = '';
        document.getElementById('edit-model-path-group').style.display = 'block';
        document.getElementById('edit-model-args-group').style.display = 'none';

        // Load runtime options
        const select = document.getElementById('edit-model-runtime');
        if (select) {
            select.innerHTML = '<option value="">Loading...</option>';
        }

        fetch('/api/v1/models/' + encodeURIComponent(id), {
            headers: { 'X-CSRF-Token': csrfToken },
            credentials: 'same-origin'
        })
        .then(r => r.ok ? r.json() : Promise.reject('not found'))
        .then(m => {
            document.getElementById('edit-model-id').value = m.id;
            document.getElementById('edit-model-name').value = m.name || '';
            document.getElementById('edit-model-kind').value = (m.path ? 'path' : 'arguments');
            document.getElementById('edit-model-path').value = m.path || '';
            document.getElementById('edit-model-arguments').value = (m.arguments || []).join(' ');
            updateEditModelKindVisibility();

            // Fill runtime select
            if (select) {
                fetch('/api/v1/runtimes', {
                    headers: { 'X-CSRF-Token': csrfToken },
                    credentials: 'same-origin'
                })
                .then(r => r.ok ? r.json() : [])
                .then(runtimes => {
                    select.innerHTML = '<option value="">Select a runtime...</option>' +
                        runtimes.map(r => '<option value="' + escapeHtml(r.id) + '" ' + (r.id === m.runtime_id ? 'selected' : '') + '>' + escapeHtml(r.name) + '</option>').join('');
                })
                .catch(() => { select.innerHTML = '<option value="">Error loading runtimes</option>'; });
            }
        })
        .catch(() => {
            const errEl = document.getElementById('edit-model-error');
            if (errEl) { errEl.textContent = 'Failed to load model data'; errEl.style.display = 'block'; }
            closeEditModelModal();
        });
    };

    window.closeEditModelModal = function() {
        const modal = document.getElementById('edit-model-modal');
        if (modal) modal.style.display = 'none';
    };

    function updateEditModelKindVisibility() {
        const kind = document.getElementById('edit-model-kind').value;
        const pathGroup = document.getElementById('edit-model-path-group');
        const argsGroup = document.getElementById('edit-model-args-group');
        if (kind === 'arguments') {
            if (argsGroup) argsGroup.style.display = 'block';
            if (pathGroup) pathGroup.style.display = 'none';
        } else {
            if (argsGroup) argsGroup.style.display = 'none';
            if (pathGroup) pathGroup.style.display = 'block';
        }
    }
	window.updateEditModelKindVisibility = updateEditModelKindVisibility;

    window.handleEditModel = async function(event) {
        event.preventDefault();
        const id = document.getElementById('edit-model-id').value;
        const errEl = document.getElementById('edit-model-error');
        if (errEl) { errEl.style.display = 'none'; errEl.textContent = ''; }

        const name = document.getElementById('edit-model-name').value.trim();
        const runtimeId = document.getElementById('edit-model-runtime').value.trim();
        const kindEl = document.getElementById('edit-model-kind');
        const kind = kindEl ? kindEl.value.trim() : '';

        if (!name) {
            if (errEl) { errEl.textContent = 'Name is required'; errEl.style.display = 'block'; }
            return false;
        }
        if (!runtimeId) {
            if (errEl) { errEl.textContent = 'Runtime is required'; errEl.style.display = 'block'; }
            return false;
        }

        let path = '';
        let modelArguments = [];
        if (kind === 'arguments') {
            const argsText = document.getElementById('edit-model-arguments').value.trim();
            modelArguments = argsText ? argsText.split(/\s+/).filter(a => a.length > 0) : [];
            if (modelArguments.length === 0) {
                const errArgs = document.getElementById('edit-model-args-error');
                if (errArgs) { errArgs.textContent = 'Arguments are required for kind=arguments'; errArgs.style.display = 'block'; }
                return false;
            }
        } else if (kind === 'path') {
            path = document.getElementById('edit-model-path').value.trim();
            if (!path) {
                const errPath = document.getElementById('edit-model-path-error');
                if (errPath) { errPath.textContent = 'Path is required for kind=path'; errPath.style.display = 'block'; }
                return false;
            }
        } else {
            if (errEl) { errEl.textContent = 'Select a model type (path or arguments)'; errEl.style.display = 'block'; }
            return false;
        }

        const body = {
            id: id,
            name: name,
            runtime_id: runtimeId,
            path: path,
            arguments: modelArguments
        };

        try {
            const response = await fetch('/api/v1/models/' + encodeURIComponent(id), {
                method: 'PUT',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken
                },
                credentials: 'same-origin',
                body: JSON.stringify(body)
            });

            if (response.ok) {
                closeEditModelModal();
                await loadModels();
                await loadRuntimes();
                showToast('Model updated', 'success');
                return false;
            }

            let msg = 'Failed to update model';
            try {
                const data = await response.json();
                msg = data.error || msg;
            } catch (e2) {}

            if (errEl) { errEl.textContent = msg; errEl.style.display = 'block'; }
        } catch (err) {
            if (errEl) { errEl.textContent = 'Request failed: ' + err.message; errEl.style.display = 'block'; }
        }
        return false;
    };

    async function loadRuntimeSelectIntoModel() {
        const select = document.getElementById('model-runtime');
        if (!select) return;
        try {
            const resp = await fetch('/api/v1/runtimes', {
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (resp.ok) {
                const runtimes = await resp.json();
                select.innerHTML = '<option value="">Select a runtime...</option>' +
                    runtimes.map(r => '<option value="' + escapeHtml(r.id) + '">' + escapeHtml(r.name) + '</option>').join('');
            }
        } catch { /* ignore */ }
    }

    function updateModelKindVisibility() {
        const kind = document.getElementById('model-kind').value;
        const pathGroup = document.getElementById('model-path-group');
        const argsGroup = document.getElementById('model-args-group');
        if (kind === 'arguments') {
            if (argsGroup) argsGroup.style.display = 'block';
            if (pathGroup) pathGroup.style.display = 'none';
        } else {
            if (argsGroup) argsGroup.style.display = 'none';
            if (pathGroup) pathGroup.style.display = 'block';
        }
        const errPath = document.getElementById('model-path-error');
        const errArgs = document.getElementById('model-args-error');
        if (errPath) { errPath.style.display = 'none'; }
        if (errArgs) { errArgs.style.display = 'none'; }
    }
	window.updateModelKindVisibility = updateModelKindVisibility;

    window.handleCreateModel = async function(event) {
        event.preventDefault();
        const errEl = document.getElementById('create-model-error');
        if (errEl) { errEl.style.display = 'none'; errEl.textContent = ''; }

        // Clear field-specific errors
        const errPath = document.getElementById('model-path-error');
        const errArgs = document.getElementById('model-args-error');
        if (errPath) { errPath.style.display = 'none'; }
        if (errArgs) { errArgs.style.display = 'none'; }

        const name = document.getElementById('model-name').value.trim();
        const runtimeId = document.getElementById('model-runtime').value.trim();
        const kindEl = document.getElementById('model-kind');
        const kind = kindEl ? kindEl.value.trim() : '';

        if (!name) {
            if (errEl) { errEl.textContent = 'Name is required'; errEl.style.display = 'block'; }
            return false;
        }
        if (!runtimeId) {
            if (errEl) { errEl.textContent = 'Runtime is required'; errEl.style.display = 'block'; }
            return false;
        }

        let path = '';
        let modelArguments = [];
        if (kind === 'arguments') {
            const argsText = document.getElementById('model-arguments').value.trim();
            modelArguments = argsText ? argsText.split(/\s+/).filter(a => a.length > 0) : [];
            if (modelArguments.length === 0) {
                if (errArgs) { errArgs.textContent = 'Arguments are required for kind=arguments'; errArgs.style.display = 'block'; }
                return false;
            }
        } else if (kind === 'path') {
            path = document.getElementById('model-path').value.trim();
            if (!path) {
                if (errPath) { errPath.textContent = 'Path is required for kind=path'; errPath.style.display = 'block'; }
                return false;
            }
        } else {
            if (errEl) { errEl.textContent = 'Select a model type (path or arguments)'; errEl.style.display = 'block'; }
            return false;
        }

        const body = {
            name: name,
            runtime_id: runtimeId,
            path: path,
            arguments: modelArguments
        };

        try {
            const response = await fetch('/api/v1/models', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken
                },
                credentials: 'same-origin',
                body: JSON.stringify(body)
            });

            if (response.ok) {
                closeCreateModelModal();
                await loadModels();
                await loadRuntimes(); // runtime select updated with latest models list
                return false;
            }

            let msg = 'Failed to create model';
            try {
                const data = await response.json();
                msg = data.error || msg;
            } catch (e2) {}

            if (errEl) { errEl.textContent = msg; errEl.style.display = 'block'; }
        } catch (err) {
            if (errEl) { errEl.textContent = 'Request failed: ' + err.message; errEl.style.display = 'block'; }
        }
        return false;
    };

    window.deleteModel = async function(id) {
        if (!confirm('Delete model ' + id + '?')) return;
        try {
            const response = await fetch('/api/v1/models/' + id, {
                method: 'DELETE',
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                await loadModels();
            } else {
                alert('Failed to delete model');
            }
        } catch (err) {
            alert('Delete failed: ' + err.message);
        }
    };

    // ========== Profiles ==========
    function renderProfiles(profiles) {
        const tbody = document.getElementById('profiles-body');
        if (!tbody) return;
		const emptyEl = document.querySelector('#profiles .empty');

        if (profiles.length === 0) {
            if (emptyEl) emptyEl.style.display = 'block';
			tbody.innerHTML = '';
			const countEl = document.querySelector('#profiles .count');
			if (countEl) countEl.textContent = '(0)';
            return;
        }

        if (emptyEl) emptyEl.style.display = 'none';
		const countEl = document.querySelector('#profiles .count');
		if (countEl) countEl.textContent = '(' + profiles.length + ')';

        // Build lookups for display names
        const rtMap = {};
        runtimesData.forEach(r => { rtMap[r.id] = r.name; });
        const mdMap = {};
        modelsData.forEach(m => { mdMap[m.id] = m.name; });

        tbody.innerHTML = profiles.map(p => {
            const rtName = p.runtime_id ? (rtMap[p.runtime_id] || p.runtime_id) : '-';
            const mdName = p.model_id ? (mdMap[p.model_id] || p.model_id) : '-';
            return `
            <tr id="profile-row-${escapeHtml(p.id)}">
                <td>${escapeHtml(p.name)}</td>
                <td>${escapeHtml(rtName)}</td>
                <td>${escapeHtml(mdName)}</td>
                <td>${escapeHtml(p.host || '')}${p.port ? ':' + p.port : ''}</td>
                <td>${(p.args || []).map(a => '<span class="arg">' + escapeHtml(a) + '</span>').join('')}</td>
                <td><span class="profile-status stopped">not started</span></td>
                <td class="actions">
                    <button onclick="showProfilePreview(${safeInlineArg(p.id)})" class="btn btn-secondary btn-sm" title="Show resolved command">Preview</button>
                    <button onclick="showEditProfileModal(${safeInlineArg(p.id)})" class="btn btn-secondary btn-sm">Edit</button>
                    <button onclick="startProfile(${safeInlineArg(p.id)})" class="btn btn-success btn-sm">Start</button>
                    <button onclick="stopProfile(${safeInlineArg(p.id)})" class="btn btn-danger btn-sm">Stop</button>
                    <button onclick="restartProfile(${safeInlineArg(p.id)})" class="btn btn-warning btn-sm">Restart</button>
                </td>
            </tr>
        `}).join('');
    }

    window.openCreateProfileModal = function() {
        document.getElementById('create-profile-modal').style.display = 'flex';
        loadRuntimeModelSelects();
    };

    window.closeCreateProfileModal = function() {
        document.getElementById('create-profile-modal').style.display = 'none';
        document.getElementById('create-profile-form').reset();
        document.getElementById('create-profile-error').style.display = 'none';
    };

    window.showEditProfileModal = function(id) {
        const modal = document.getElementById('edit-profile-modal');
        if (!modal) return;
        modal.style.display = 'flex';
        const errEl = document.getElementById('edit-profile-error');
        if (errEl) errEl.style.display = 'none';

        // Clear form
        document.getElementById('edit-profile-id').value = '';
        document.getElementById('edit-profile-name').value = '';
        document.getElementById('edit-profile-runtime').value = '';
        document.getElementById('edit-profile-model').value = '';
        document.getElementById('edit-profile-host').value = '127.0.0.1';
        document.getElementById('edit-profile-port').value = '';
        document.getElementById('edit-profile-args').value = '';

        // Load runtime/model options
        const rtSelect = document.getElementById('edit-profile-runtime');
        if (rtSelect) rtSelect.innerHTML = '<option value="">Loading...</option>';
        const mdSelect = document.getElementById('edit-profile-model');
        if (mdSelect) mdSelect.innerHTML = '<option value="">Loading...</option>';

        fetch('/api/v1/profiles/' + encodeURIComponent(id), {
            headers: { 'X-CSRF-Token': csrfToken },
            credentials: 'same-origin'
        })
        .then(r => r.ok ? r.json() : Promise.reject('not found'))
        .then(p => {
            document.getElementById('edit-profile-id').value = p.id;
            document.getElementById('edit-profile-name').value = p.name || '';
            document.getElementById('edit-profile-host').value = p.host || '127.0.0.1';
            document.getElementById('edit-profile-port').value = p.port || '';
            document.getElementById('edit-profile-args').value = (p.args || []).join(' ');

            // Load runtimes
            if (rtSelect) {
                fetch('/api/v1/runtimes', {
                    headers: { 'X-CSRF-Token': csrfToken },
                    credentials: 'same-origin'
                })
                .then(r => r.ok ? r.json() : [])
                .then(runtimes => {
                    rtSelect.innerHTML = '<option value="">Select a runtime...</option>' +
                        runtimes.map(r => '<option value="' + escapeHtml(r.id) + '" ' + (r.id === p.runtime_id ? 'selected' : '') + '>' + escapeHtml(r.name) + '</option>').join('');
                })
                .catch(() => { rtSelect.innerHTML = '<option value="">Error loading runtimes</option>'; });
            }

            // Load models
            if (mdSelect) {
                fetch('/api/v1/models', {
                    headers: { 'X-CSRF-Token': csrfToken },
                    credentials: 'same-origin'
                })
                .then(r => r.ok ? r.json() : [])
                .then(models => {
                    mdSelect.innerHTML = '<option value="">Select a model...</option>' +
                        models.map(m => '<option value="' + escapeHtml(m.id) + '" ' + (m.id === p.model_id ? 'selected' : '') + '>' + escapeHtml(m.name) + '</option>').join('');
                })
                .catch(() => { mdSelect.innerHTML = '<option value="">Error loading models</option>'; });
            }
        })
        .catch(() => {
            const errEl = document.getElementById('edit-profile-error');
            if (errEl) { errEl.textContent = 'Failed to load profile data'; errEl.style.display = 'block'; }
            closeEditProfileModal();
        });
    };

    window.closeEditProfileModal = function() {
        const modal = document.getElementById('edit-profile-modal');
        if (modal) modal.style.display = 'none';
    };

    window.handleEditProfile = async function(event) {
        event.preventDefault();
        const id = document.getElementById('edit-profile-id').value;
        const errEl = document.getElementById('edit-profile-error');
        if (errEl) { errEl.style.display = 'none'; errEl.textContent = ''; }

        const formData = new FormData(document.getElementById('edit-profile-form'));
        const envPairs = [];
        document.querySelectorAll('.env-row').forEach(row => {
            const key = row.querySelector('.env-key')?.value.trim();
            const value = row.querySelector('.env-value')?.value;
            if (key) envPairs.push([key, value]);
        });

        const argsText = document.getElementById('edit-profile-args').value.trim();
        const args = argsText ? argsText.split(/\s+/) : [];

        const body = {
            id: id,
            name: formData.get('name'),
            runtime_id: formData.get('runtime_id'),
            model_id: formData.get('model_id') || undefined,
            host: formData.get('host') || undefined,
            port: formData.get('port') ? parseInt(formData.get('port')) : undefined,
            args: args
        };

        try {
            const response = await fetch('/api/v1/profiles/' + encodeURIComponent(id), {
                method: 'PUT',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken
                },
                credentials: 'same-origin',
                body: JSON.stringify(body)
            });

            if (response.ok) {
                await loadProfiles();
                closeEditProfileModal();
                showToast('Profile updated', 'success');
            } else {
                const data = await response.json().catch(() => ({}));
                errEl.textContent = data.error || 'Failed to update profile';
                errEl.style.display = 'block';
            }
        } catch (err) {
            errEl.textContent = 'Edit failed: ' + err.message;
            errEl.style.display = 'block';
        }
        return false;
    };

    async function loadRuntimeModelSelects() {
        // Load runtimes
        const runtimeSelect = document.getElementById('profile-runtime');
        try {
            const resp = await fetch('/api/v1/runtimes', {
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (resp.ok) {
                const runtimes = await resp.json();
                runtimeSelect.innerHTML = '<option value="">Select a runtime...</option>' +
                    runtimes.map(r => '<option value="' + escapeHtml(r.id) + '">' + escapeHtml(r.name) + '</option>').join('');
            }
        } catch { /* ignore */ }

        // Load models
        const modelSelect = document.getElementById('profile-model');
        try {
            const resp = await fetch('/api/v1/models', {
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (resp.ok) {
                const models = await resp.json();
                modelSelect.innerHTML = '<option value="">Select a model...</option>' +
                    models.map(m => '<option value="' + escapeHtml(m.id) + '">' + escapeHtml(m.name) + '</option>').join('');
            }
        } catch { /* ignore */ }
    }

    window.updateProfileModelSelect = function() {
        // Could filter models by runtime, currently just a hook
    };

    window.addEnvRow = function() {
        const container = document.getElementById('env-variables-container');
        const row = document.createElement('div');
        row.className = 'env-row';
        row.innerHTML = `
            <input type="text" placeholder="VAR_NAME" class="env-key">
            <input type="text" placeholder="value" class="env-value">
            <button type="button" class="btn btn-danger btn-sm" onclick="removeEnvRow(this)">×</button>
        `;
        container.appendChild(row);
    };

    window.removeEnvRow = function(btn) {
        const container = document.getElementById('env-variables-container');
        if (container.children.length > 1) {
            btn.parentElement.remove();
        }
    };

    window.handleCreateProfile = async function(event) {
        event.preventDefault();
        const errorEl = document.getElementById('create-profile-error');

        const formData = new FormData(document.getElementById('create-profile-form'));
        const envPairs = [];
        document.querySelectorAll('.env-row').forEach(row => {
            const key = row.querySelector('.env-key').value.trim();
            const value = row.querySelector('.env-value').value;
            if (key) envPairs.push([key, value]);
        });

        // Parse args from textarea
        const argsText = document.getElementById('profile-args').value.trim();
        const args = argsText ? argsText.split(/\s+/) : [];

        const body = {
            name: formData.get('name'),
            runtime_id: formData.get('runtime_id'),
            model_id: formData.get('model_id'),
            host: formData.get('host') || undefined,
            port: formData.get('port') ? parseInt(formData.get('port')) : undefined,
            args: args,
            environment: Object.fromEntries(envPairs)
        };

        try {
            const response = await fetch('/api/v1/profiles', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken
                },
                credentials: 'same-origin',
                body: JSON.stringify(body)
            });

            if (response.ok) {
                await loadProfiles();
                closeCreateProfileModal();
            } else {
                const data = await response.json().catch(() => ({}));
                errorEl.textContent = data.error || 'Failed to create profile';
                errorEl.style.display = 'block';
            }
        } catch (err) {
            errorEl.textContent = 'Create failed: ' + err.message;
            errorEl.style.display = 'block';
        }
        return false;
    };

    window.startProfile = async function(id) {
        const btn = event.target;
        const orig = btn.textContent;
        btn.disabled = true;
        btn.textContent = 'Starting...';
        try {
            const response = await fetch('/api/v1/profiles/' + encodeURIComponent(id) + '/start', {
                method: 'POST',
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                await loadInstances();
				await refreshDashboard();
                await loadProfiles();
                showToast('Profile started', 'success');
            } else {
                const data = await response.json().catch(() => ({}));
                showToast('Start failed: ' + (data.error || 'Unknown error'), 'error');
            }
        } catch (err) {
            showToast('Start failed: ' + err.message, 'error');
        } finally {
            btn.disabled = false;
            btn.textContent = orig;
        }
    };

    window.stopProfile = async function(id) {
        const btn = event.target;
        const orig = btn.textContent;
        btn.disabled = true;
        btn.textContent = 'Stopping...';
        try {
            const response = await fetch('/api/v1/profiles/' + encodeURIComponent(id) + '/stop', {
                method: 'POST',
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                await loadInstances();
				await refreshDashboard();
                await loadProfiles();
                showToast('Profile stopped', 'success');
            } else {
                const data = await response.json().catch(() => ({}));
                showToast('Stop failed: ' + (data.error || 'Unknown error'), 'error');
            }
        } catch (err) {
            showToast('Stop failed: ' + err.message, 'error');
        } finally {
            btn.disabled = false;
            btn.textContent = orig;
        }
    };

    window.restartProfile = async function(id) {
        const btn = event.target;
        const orig = btn.textContent;
        btn.disabled = true;
        btn.textContent = 'Restarting...';
        try {
            const response = await fetch('/api/v1/profiles/' + encodeURIComponent(id) + '/restart', {
                method: 'POST',
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                await loadInstances();
				await refreshDashboard();
                await loadProfiles();
                showToast('Profile restarted', 'success');
            } else {
                const data = await response.json().catch(() => ({}));
                showToast('Restart failed: ' + (data.error || 'Unknown error'), 'error');
            }
        } catch (err) {
            showToast('Restart failed: ' + err.message, 'error');
        } finally {
            btn.disabled = false;
            btn.textContent = orig;
        }
    };

    window.showProfilePreview = async function(id) {
        try {
            const response = await fetch('/api/v1/profiles/' + encodeURIComponent(id) + '/resolve', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken
                },
                credentials: 'same-origin'
            });
            if (!response.ok) {
                const text = await response.text();
                alert('Resolve failed: ' + text);
                return;
            }
            const spec = await response.json();
            document.getElementById('preview-executable').textContent = spec.executable || '';
            document.getElementById('preview-args').textContent = (spec.args || []).join(' ');
            document.getElementById('preview-workdir').textContent = spec.workingDirectory || '';
            const envList = document.getElementById('preview-env');
            envList.innerHTML = '';
            (spec.environmentKeys || []).forEach(k => {
                const span = document.createElement('span');
                span.className = 'env-tag';
                span.textContent = k;
                envList.appendChild(span);
            });
            document.getElementById('profile-preview-modal').style.display = 'flex';
        } catch (err) {
            alert('Resolve request failed: ' + err.message);
        }
    };

    window.closeProfilePreviewModal = function() {
        document.getElementById('profile-preview-modal').style.display = 'none';
    };

    // ========== Logs ==========
    function connectLogs() {
        if (eventSource) {
            eventSource.close();
        }

        eventSource = new EventSource('/api/v1/logs/stream');

        eventSource.onmessage = function(event) {
            try {
                const data = JSON.parse(event.data);
                appendLog(data);
            } catch (e) {
                appendLog({ stream: 'system', message: 'Parse error: ' + e.message });
            }
        };

        eventSource.onerror = function() {
            appendLog({ stream: 'system', message: '[Connection lost. Reconnecting...]' });
        };
    }

    function appendLog(ev) {
        const container = document.getElementById('log-container');
        if (!container) return;

        // Apply filters
        if (ev.stream !== 'system') {
            if (currentLogFilter && ev.stream !== currentLogFilter) return;
        }
        if (logSearchTerm) {
            if (!ev.message.toLowerCase().includes(logSearchTerm.toLowerCase())) return;
        }

        const p = document.createElement('p');
        const time = ev.time ? new Date(ev.time).toLocaleTimeString() : new Date().toLocaleTimeString();
        const cls = ev.stream === 'stderr' ? 'log-stderr' :
                    ev.stream === 'system' ? 'log-system' : 'log-stdout';
        p.className = cls;
        p.textContent = `[${time}] [${ev.stream}] ${ev.message}`;
        container.appendChild(p);

        // Limit log entries
        while (container.children.length > 1000) {
            container.removeChild(container.firstChild);
        }

        container.scrollTop = container.scrollHeight;
    }

    window.clearLogs = function() {
        const container = document.getElementById('log-container');
        if (container) container.innerHTML = '';
    };

    window.updateLogFilter = function() {
        currentLogFilter = document.getElementById('log-filter-stream').value;
        const mode = document.getElementById('log-mode').value;
        if (mode === 'history') { loadHistoryLogs(); return; }
        connectLogs();
    };

    window.filterLogs = function() {
        logSearchTerm = document.getElementById('log-search').value;
        const mode = document.getElementById('log-mode').value;
        if (mode === 'history') { loadHistoryLogs(); return; }
        // Clear and reconnect to apply filter
        const container = document.getElementById('log-container');
        if (container) container.innerHTML = '';
        connectLogs();
    };

    window.updateLogMode = function() {
        const mode = document.getElementById('log-mode').value;
        const live = document.getElementById('log-container');
        const hist = document.getElementById('history-panel');
        if (mode === 'history') {
            if (live) live.style.display = 'none';
            if (hist) hist.style.display = 'block';
            loadHistoryLogs();
        } else {
            if (live) live.style.display = 'block';
            if (hist) hist.style.display = 'none';
            connectLogs();
        }
    };

    async function loadHistoryLogs() {
        const search = document.getElementById('log-search').value;
        const stream = document.getElementById('log-filter-stream').value;
        const url = new URL('/api/v1/logs', window.location.origin);
        if (search) url.searchParams.set('search', search);
        if (stream) url.searchParams.set('stream', stream);
        url.searchParams.set('page_size', '200');

        const resp = await fetch(url.toString(), {
            headers: { 'X-CSRF-Token': csrfToken },
            credentials: 'same-origin'
        });
        if (!resp.ok) {
            const txt = await resp.text().catch(() => 'load failed');
            alert('History load failed: ' + txt);
            return;
        }

        const data = await resp.json();
        const items = data.items || [];
        renderHistoryLogs(items);
    }

    function renderHistoryLogs(items) {
        const panel = document.getElementById('history-panel');
        if (!panel) return;
        const empty = document.getElementById('history-empty');
        if (empty) empty.style.display = items.length ? 'none' : 'block';

        panel.innerHTML = '';
        items.forEach(ev => {
            const p = document.createElement('p');
			const time = ev.time ? new Date(ev.time).toLocaleTimeString() : new Date().toLocaleTimeString();
            const cls = ev.stream === 'stderr' ? 'log-stderr' :
                        ev.stream === 'system' ? 'log-system' : 'log-stdout';
            p.className = cls;
            p.textContent = `[${time}] [${ev.stream}] ${ev.message}`;
            panel.appendChild(p);
        });
    }

    // ========== Escape HTML ==========
    // ========== Instances ==========
    function renderInstances(instances) {
        const tbody = document.getElementById('instances-body');
        if (!tbody) return;
        const table = document.getElementById('instances-table');
        const emptyEl = document.getElementById('instances-empty');
        if (!table || !emptyEl) return;

        if (instances.length === 0) {
            emptyEl.style.display = 'block';
            table.style.display = 'none';
            return;
        }

        emptyEl.style.display = 'none';
        table.style.display = '';

        // Build lookups
        const profileMap = {};
        profilesData.forEach(p => { profileMap[p.id] = p.name; });

        tbody.innerHTML = instances.map(inst => {
            const profileName = inst.profile_id ? (profileMap[inst.profile_id] || inst.profile_id) : '-';
            const stateClass = inst.state ? inst.state.toLowerCase() : 'unknown';
			const hasExitCode = inst.exit_code !== null && inst.exit_code !== undefined;
            const exitInfo = (inst.exit_class || hasExitCode)
                ? `${inst.exit_class || '-'} (${hasExitCode ? inst.exit_code : '-'})`
                : '-';
            const started = inst.started_at ? new Date(inst.started_at).toLocaleString() : '-';
			const stoppedAt = inst.stopped_at ? new Date(inst.stopped_at) : null;
            const stopped = stoppedAt && stoppedAt.getFullYear() > 1 ? stoppedAt.toLocaleString() : '-';

            let actions = '';
            if (inst.state === 'running' || inst.state === 'starting' || inst.state === 'stopping') {
                actions = `
                    <button onclick="stopInstanceInstance(${safeInlineArg(inst.id)})" class="btn btn-danger btn-sm">Stop</button>
                    <button onclick="restartInstanceInstance(${safeInlineArg(inst.id)})" class="btn btn-warning btn-sm">Restart</button>
                    <button onclick="showInstanceLogs(${safeInlineArg(inst.id)})" class="btn btn-secondary btn-sm">Logs</button>
                `;
            } else if (inst.state === 'exited' || inst.state === 'failed' || inst.state === 'stale') {
                actions = `
                    <button onclick="restartInstanceInstance(${safeInlineArg(inst.id)})" class="btn btn-warning btn-sm">Restart</button>
                    <button onclick="showInstanceLogs(${safeInlineArg(inst.id)})" class="btn btn-secondary btn-sm">Logs</button>
                `;
            } else {
                actions = `<button onclick="showInstanceLogs(${safeInlineArg(inst.id)})" class="btn btn-secondary btn-sm">Logs</button>`;
            }

            return `
            <tr>
                <td><code>${escapeHtml(inst.id)}</code></td>
                <td>${escapeHtml(profileName)}</td>
                <td><span class="profile-status ${stateClass}">${escapeHtml(inst.state || 'unknown')}</span></td>
                <td><code>${inst.pid || '-'}</code></td>
                <td>${escapeHtml(started)}</td>
                <td>${escapeHtml(stopped)}</td>
                <td>${escapeHtml(exitInfo)}</td>
                <td class="actions">${actions}</td>
            </tr>
        `}).join('');
    }

    async function loadInstances() {
        try {
            const response = await fetch('/api/v1/instances', {
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                const instances = await response.json();
                renderInstances(instances);
            }
        } catch { /* ignore */ }
    }

    window.stopInstanceInstance = async function(id) {
        const btn = event ? event.target : null;
        try {
            const response = await fetch('/api/v1/instances/' + encodeURIComponent(id) + '/stop', {
                method: 'POST',
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                await loadInstances();
                await loadProfiles();
                showToast('Instance stopped', 'success');
            } else {
                const data = await response.json().catch(() => ({}));
                showToast('Stop failed: ' + (data.error || 'Unknown error'), 'error');
            }
        } catch (err) {
            showToast('Stop failed: ' + err.message, 'error');
        }
    };

    window.restartInstanceInstance = async function(id) {
        const btn = event ? event.target : null;
        try {
            const response = await fetch('/api/v1/instances/' + encodeURIComponent(id) + '/restart', {
                method: 'POST',
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                await loadInstances();
                await loadProfiles();
                showToast('Instance restarted', 'success');
            } else {
                const data = await response.json().catch(() => ({}));
                showToast('Restart failed: ' + (data.error || 'Unknown error'), 'error');
            }
        } catch (err) {
            showToast('Restart failed: ' + err.message, 'error');
        }
    };

    // ========== Instance Logs Viewer ==========
    let currentInstanceLogStream = null;

    window.showInstanceLogs = function(instanceId) {
        // Close existing instance log stream if any
        if (currentInstanceLogStream) {
            currentInstanceLogStream.close();
            currentInstanceLogStream = null;
        }

        // Show the logs section and switch to instance-specific view
        const logContainer = document.getElementById('log-container');
        const logFilter = document.getElementById('log-filter-stream');
        const logMode = document.getElementById('log-mode');
        const historyPanel = document.getElementById('history-panel');
        const historyEmpty = document.getElementById('history-empty');

        if (logMode) {
            logMode.value = 'live';
        }
        if (logContainer) {
            logContainer.style.display = 'block';
            logContainer.innerHTML = '';
        }
        if (historyPanel) {
            historyPanel.style.display = 'none';
        }
        if (historyEmpty) historyEmpty.style.display = 'none';

        // Connect to instance-specific SSE stream
        const streamUrl = '/api/v1/instances/' + encodeURIComponent(instanceId) + '/logs/stream';
        currentInstanceLogStream = new EventSource(streamUrl);

        currentInstanceLogStream.onmessage = function(event) {
            try {
                const data = JSON.parse(event.data);
                appendInstanceLog(data, logContainer);
            } catch (e) {
                appendInstanceLog({ stream: 'system', message: 'Parse error: ' + e.message, time: new Date().toISOString() }, logContainer);
            }
        };

        currentInstanceLogStream.onerror = function() {
            appendInstanceLog({ stream: 'system', message: '[Connection lost. Reconnecting...]', time: new Date().toISOString() }, logContainer);
        };

        // Scroll to bottom
        if (logContainer) {
            logContainer.scrollTop = logContainer.scrollHeight;
        }

        // Add disconnect button
        const controls = document.querySelector('.log-controls');
        if (controls) {
            const existingBtn = document.getElementById('disconnect-logs');
            if (existingBtn) existingBtn.remove();
            const disconnectBtn = document.createElement('button');
            disconnectBtn.id = 'disconnect-logs';
            disconnectBtn.className = 'btn btn-danger btn-sm';
            disconnectBtn.textContent = 'Disconnect';
            disconnectBtn.onclick = function() {
                if (currentInstanceLogStream) {
                    currentInstanceLogStream.close();
                    currentInstanceLogStream = null;
                }
                disconnectBtn.remove();
                appendInstanceLog({ stream: 'system', message: '[Stream disconnected]', time: new Date().toISOString() }, logContainer);
            };
            controls.appendChild(disconnectBtn);
        }
    };

    function appendInstanceLog(ev, container) {
        if (!container) return;
        const p = document.createElement('p');
        const time = ev.time ? new Date(ev.time).toLocaleTimeString() : new Date().toLocaleTimeString();
        const cls = ev.stream === 'stderr' ? 'log-stderr' :
                    ev.stream === 'system' ? 'log-system' : 'log-stdout';
        p.className = cls;
        p.textContent = '[' + time + '] [' + (ev.stream || 'unknown') + '] ' + (ev.message || '');
        container.appendChild(p);

        while (container.children.length > 1000) {
            container.removeChild(container.firstChild);
        }
        container.scrollTop = container.scrollHeight;
    }

    function escapeHtml(str) {
        const div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

	function safeInlineArg(value) {
		return JSON.stringify(String(value))
			.replace(/&/g, '&amp;')
			.replace(/"/g, '&quot;')
			.replace(/</g, '&lt;')
			.replace(/>/g, '&gt;');
	}

    // ========== Init ==========
    async function init() {
        csrfToken = getCSRFToken();
        await checkAuth();
        await reloadAllData();
        await refreshDashboard();
        connectLogs();

        // Periodic refresh
        setInterval(async () => {
            await loadInstances();
            await refreshDashboard();
        }, 10000);
    }

    init();
})();

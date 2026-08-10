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
        const match = document.cookie.match(/csrf_token=([^;]+)/);
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
                csrfToken = data.csrf_token || '';
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
            if (data.authenticated === 'true') {
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
        await Promise.all([loadProfiles(), loadRuntimes(), loadModels()]);
    }

    // ========== Status ==========
    async function refreshStatus() {
        try {
            const response = await fetch('/api/v1/status', {
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                const status = await response.json();
                const indicator = document.getElementById('status-indicator');
                if (indicator) {
                    indicator.className = 'status-badge status-' + status.state.toLowerCase();
                    indicator.innerHTML = '<span class="status-dot"></span>' + status.state;
                }
                const stateEl = document.getElementById('detail-state');
                if (stateEl) stateEl.textContent = status.state;
            }
        } catch { /* ignore */ }
    }

    // ========== Runtimes ==========
    function renderRuntimes(runtimes) {
        const tbody = document.querySelector('#runtimes tbody');
        if (!tbody) return;

        if (runtimes.length === 0) {
            const emptyEl = document.querySelector('#runtimes .empty');
            if (emptyEl) emptyEl.style.display = 'block';
            tbody.style.display = 'none';
            return;
        }

        if (emptyEl) emptyEl.style.display = 'none';
        tbody.style.display = '';

        tbody.innerHTML = runtimes.map(r => `
            <tr>
                <td>${escapeHtml(r.name)}</td>
                <td><code>${escapeHtml(r.executable)}</code></td>
                <td><code>${escapeHtml(r.working_directory || '-')}</code></td>
                <td>${(r.default_args || []).map(a => '<span class="arg">' + escapeHtml(a) + '</span>').join('')}</td>
                <td><code>${escapeHtml(r.id)}</code></td>
                <td class="actions">
                    <button onclick="deleteRuntime('${r.id}')" class="btn btn-danger btn-sm">Delete</button>
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
                await refreshStatus();
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

    window.deleteRuntime = async function(id) {
        if (!confirm('Delete runtime ' + id + '?')) return;
        try {
            const response = await fetch('/api/v1/runtimes/' + id, {
                method: 'DELETE',
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                await loadRuntimes();
            } else {
                alert('Failed to delete runtime');
            }
        } catch (err) {
            alert('Delete failed: ' + err.message);
        }
    };

    // ========== Models ==========
    function renderModels(models) {
        const tbody = document.querySelector('#models tbody');
        if (!tbody) return;

        if (models.length === 0) {
            const emptyEl = document.querySelector('#models .empty');
            if (emptyEl) emptyEl.style.display = 'block';
            tbody.style.display = 'none';
            return;
        }

        if (emptyEl) emptyEl.style.display = 'none';
        tbody.style.display = '';

        tbody.innerHTML = models.map(m => `
            <tr>
                <td>${escapeHtml(m.name)}</td>
                <td><code>${escapeHtml(m.path)}</code></td>
                <td><code>${escapeHtml(m.mmproj || '-')}</code></td>
                <td>${escapeHtml(m.format || '-')}</td>
                <td><code>${escapeHtml(m.id)}</code></td>
                <td class="actions">
                    <button onclick="deleteModel('${m.id}')" class="btn btn-danger btn-sm">Delete</button>
                </td>
            </tr>
        `).join('');
    }

    window.showCreateModelModal = function() {
        alert('Create Model dialog - use API: POST /api/v1/models');
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

        if (profiles.length === 0) {
            const emptyEl = document.querySelector('#profiles .empty');
            if (emptyEl) emptyEl.style.display = 'block';
            return;
        }

        if (emptyEl) emptyEl.style.display = 'none';

        tbody.innerHTML = profiles.map(p => `
            <tr id="profile-row-${p.id}">
                <td>${escapeHtml(p.name)}</td>
                <td>${escapeHtml(p.runtime_id)}</td>
                <td>${escapeHtml(p.model_id || '-')}</td>
                <td>${p.host || ''}${p.port ? ':' + p.port : ''}</td>
                <td>${(p.args || []).map(a => '<span class="arg">' + escapeHtml(a) + '</span>').join('')}</td>
                <td><span class="profile-status stopped">not started</span></td>
                <td class="actions">
                    <button onclick="startProfile('${p.id}')" class="btn btn-success btn-sm">Start</button>
                    <button onclick="stopProfile('${p.id}')" class="btn btn-danger btn-sm">Stop</button>
                </td>
            </tr>
        `).join('');
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
                    runtimes.map(r => '<option value="' + r.id + '">' + escapeHtml(r.name) + '</option>').join('');
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
                    models.map(m => '<option value="' + m.id + '">' + escapeHtml(m.name) + '</option>').join('');
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
        try {
            const response = await fetch('/api/v1/profiles/' + id + '/start', {
                method: 'POST',
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                await refreshStatus();
                await loadProfiles();
            } else {
                const data = await response.json().catch(() => ({}));
                alert('Start failed: ' + (data.error || 'Unknown error'));
            }
        } catch (err) {
            alert('Start failed: ' + err.message);
        }
    };

    window.stopProfile = async function(id) {
        try {
            const response = await fetch('/api/v1/profiles/' + id + '/stop', {
                method: 'POST',
                headers: { 'X-CSRF-Token': csrfToken },
                credentials: 'same-origin'
            });
            if (response.ok) {
                await refreshStatus();
                await loadProfiles();
            } else {
                const data = await response.json().catch(() => ({}));
                alert('Stop failed: ' + (data.error || 'Unknown error'));
            }
        } catch (err) {
            alert('Stop failed: ' + err.message);
        }
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
        const time = new Date(ev.time).toLocaleTimeString();
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
        // Reconnect SSE to get all logs (client-side filtering)
        connectLogs();
    };

    window.filterLogs = function() {
        logSearchTerm = document.getElementById('log-search').value;
        // Clear and reconnect to apply filter
        const container = document.getElementById('log-container');
        if (container) container.innerHTML = '';
        connectLogs();
    };

    // ========== Escape HTML ==========
    function escapeHtml(str) {
        const div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    // ========== Init ==========
    async function init() {
        csrfToken = getCSRFToken();
        await checkAuth();
        await reloadAllData();
        await refreshStatus();
        connectLogs();

        // Periodic status refresh
        setInterval(refreshStatus, 5000);
    }

    init();
})();
// ============ Log SSE ============
const logsEl = document.getElementById('logs');
if (logsEl) {
  const es = new EventSource('/api/v1/logs/stream');
  es.onmessage = e => {
    const x = JSON.parse(e.data);
    logsEl.textContent += `[${x.stream}] ${x.message}\n`;
    logsEl.scrollTop = logsEl.scrollHeight;
  };
}

// ============ Tab switching ============
document.querySelectorAll('.tab').forEach(tab => {
  tab.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
    tab.classList.add('active');
    document.getElementById('tab-' + tab.dataset.tab).classList.add('active');
  });
});

// ============ Modal ============
function showModal(id) { document.getElementById(id).classList.add('active'); }
function hideModal(id) { document.getElementById(id).classList.remove('active'); }

// ============ Status ============
async function fetchStatus() {
  try {
    const r = await fetch('/api/v1/status');
    const s = await r.json();
    const badge = document.getElementById('statusBadge');
    badge.textContent = s.Status.charAt(0).toUpperCase() + s.Status.slice(1).toLowerCase();
    badge.className = 'status-badge ' + s.Status;
  } catch(e) {}
}

// ============ Data ============
let profiles = [], runtimes = [], models = [];

async function loadAll() {
  try {
    const [pr, ru, mo] = await Promise.all([
      fetch('/api/v1/profiles').then(r => r.json()),
      fetch('/api/v1/runtimes').then(r => r.json()),
      fetch('/api/v1/models').then(r => r.json()),
    ]);
    profiles = pr || [];
    runtimes = ru || [];
    models = mo || [];
    renderProfiles();
    renderRuntimes();
    renderModels();
    populateSelects();
  } catch(e) { console.error(e); }
  fetchStatus();
}

function populateSelects() {
  const rtSel = document.getElementById('profileRuntime');
  const mdSel = document.getElementById('profileModel');
  rtSel.innerHTML = '<option value="">(select)</option>' +
    runtimes.map(r => `<option value="${r.id}">${esc(r.name)}</option>`).join('');
  mdSel.innerHTML = '<option value="">(none)</option>' +
    models.map(m => `<option value="${m.id}">${esc(m.name)}</option>`).join('');
}

// ============ Profiles ============
function renderProfiles() {
  const body = document.getElementById('profilesBody');
  const empty = document.getElementById('profilesEmpty');
  if (profiles.length === 0) {
    body.innerHTML = '';
    empty.style.display = 'block';
    return;
  }
  empty.style.display = 'none';
  body.innerHTML = profiles.map(p => {
    const rt = runtimes.find(r => r.id === p.runtime_id);
    const md = models.find(m => m.id === p.model_id);
    const activeBadge = p.active
      ? '<span style="color:#28a745;font-weight:600;font-size:12px;">● Active</span>'
      : '<span style="color:#6c757d;font-size:12px;">○ Inactive</span>';
    return `<tr>
      <td><strong>${esc(p.name)}</strong><br>${activeBadge}</td>
      <td>${esc(rt ? rt.name : 'N/A')}</td>
      <td>${esc(md ? md.name : 'N/A')}</td>
      <td>${p.host || '-'}:${p.port || '-'}</td>
      <td>
        <div class="btn-group">
          <button class="btn btn-success btn-sm" onclick="actionProfile('${p.id}','start')" title="Start">▶ Start</button>
          <button class="btn btn-warning btn-sm" onclick="actionProfile('${p.id}','restart')" title="Restart">↻ Restart</button>
          <button class="btn btn-danger btn-sm" onclick="actionProfile('${p.id}','stop')" title="Stop">■ Stop</button>
          ${p.active
            ? `<button class="btn btn-sm" style="background:#6c757d;color:#fff;" onclick="deactivateProfile('${p.id}')" title="Deactivate">⏻ Deactivate</button>`
            : `<button class="btn btn-sm" style="background:#17a2b8;color:#fff;" onclick="activateProfile('${p.id}')" title="Activate">✓ Activate</button>`}
          <button class="btn btn-sm" onclick="editProfile('${p.id}')" title="Edit">✎</button>
          <button class="btn btn-danger btn-sm" onclick="deleteProfile('${p.id}')" title="Delete">🗑</button>
        </div>
      </td>
    </tr>`;
  }).join('');
}

async function actionProfile(id, action) {
  try {
    const p = profiles.find(x => x.id === id);
    if (!p) return;
    const body = action === 'start' ? JSON.stringify({ runtime_id: p.runtime_id, model_id: p.model_id, host: p.host, port: p.port }) : undefined;
    const opts = body ? { method: 'POST', headers: {'Content-Type':'application/json'}, body } : undefined;
    await fetch(`/api/v1/profiles/${id}/action/${action}`, opts);
    loadAll();
  } catch(e) { console.error(e); }
}

async function activateProfile(id) {
  try {
    await fetch(`/api/v1/profiles/${id}/activate`, { method: 'POST' });
    loadAll();
  } catch(e) { console.error(e); }
}

async function deactivateProfile(id) {
  try {
    await fetch(`/api/v1/profiles/${id}/deactivate`, { method: 'POST' });
    loadAll();
  } catch(e) { console.error(e); }
}

async function saveProfile(e) {
  e.preventDefault();
  const id = document.getElementById('profileId').value;
  const name = document.getElementById('profileName').value;
  const runtimeId = document.getElementById('profileRuntime').value;
  const modelId = document.getElementById('profileModel').value;
  const host = document.getElementById('profileHost').value;
  const port = parseInt(document.getElementById('profilePort').value) || 0;
  const argsRaw = document.getElementById('profileArgs').value.trim();
  const envRaw = document.getElementById('profileEnv').value.trim();
  const args = argsRaw ? argsRaw.split('\n').map(l => l.trim()).filter(Boolean) : [];
  const env = {};
  if (envRaw) {
    envRaw.split('\n').forEach(l => {
      const idx = l.indexOf('=');
      if (idx > 0) env[l.substring(0, idx).trim()] = l.substring(idx + 1).trim();
    });
  }
  const url = id ? `/api/v1/profiles/${id}` : '/api/v1/profiles';
  const method = id ? 'PUT' : 'POST';
  const body = JSON.stringify({ name, runtime_id: runtimeId, model_id: modelId, host, port, args, environment: env });
  await fetch(url, { method, headers: {'Content-Type':'application/json'}, body });
  hideModal('profileModal');
  loadAll();
}

function editProfile(id) {
  const p = profiles.find(x => x.id === id);
  if (!p) return;
  document.getElementById('profileId').value = p.id;
  document.getElementById('profileName').value = p.name;
  document.getElementById('profileRuntime').value = p.runtime_id || '';
  document.getElementById('profileModel').value = p.model_id || '';
  document.getElementById('profileHost').value = p.host || '';
  document.getElementById('profilePort').value = p.port || '';
  document.getElementById('profileArgs').value = (p.args || []).join('\n');
  document.getElementById('profileEnv').value = Object.entries(p.environment || {}).map(([k,v]) => k+'='+v).join('\n');
  document.getElementById('profileModalTitle').textContent = 'Edit Profile';
  showModal('profileModal');
}

async function deleteProfile(id) {
  if (!confirm('Delete this profile?')) return;
  await fetch('/api/v1/profiles/' + id, { method: 'DELETE' });
  loadAll();
}

// ============ Runtimes ============
async function saveRuntime(e) {
  e.preventDefault();
  const id = document.getElementById('runtimeId').value;
  const name = document.getElementById('runtimeName').value;
  const exe = document.getElementById('runtimeExe').value;
  const workDir = document.getElementById('runtimeWorkDir').value;
  const defArgsRaw = document.getElementById('runtimeDefaultArgs').value.trim();
  const envRaw = document.getElementById('runtimeEnv').value.trim();
  const args = defArgsRaw ? defArgsRaw.split('\n').map(l => l.trim()).filter(Boolean) : [];
  const env = {};
  if (envRaw) {
    envRaw.split('\n').forEach(l => {
      const idx = l.indexOf('=');
      if (idx > 0) env[l.substring(0, idx).trim()] = l.substring(idx + 1).trim();
    });
  }
  const url = id ? `/api/v1/runtimes/${id}` : '/api/v1/runtimes';
  const method = id ? 'PUT' : 'POST';
  const body = JSON.stringify({ name, executable: exe, working_directory: workDir, default_args: args, environment: env });
  await fetch(url, { method, headers: {'Content-Type':'application/json'}, body });
  hideModal('runtimeModal');
  loadAll();
}

function editRuntime(id) {
  const r = runtimes.find(x => x.id === id);
  if (!r) return;
  document.getElementById('runtimeId').value = r.id;
  document.getElementById('runtimeName').value = r.name;
  document.getElementById('runtimeExe').value = r.executable;
  document.getElementById('runtimeWorkDir').value = r.working_directory || '';
  document.getElementById('runtimeDefaultArgs').value = (r.default_args || []).join('\n');
  document.getElementById('runtimeEnv').value = Object.entries(r.environment || {}).map(([k,v]) => k+'='+v).join('\n');
  document.getElementById('runtimeModalTitle').textContent = 'Edit Runtime';
  showModal('runtimeModal');
}

async function deleteRuntime(id) {
  if (!confirm('Delete this runtime?')) return;
  await fetch('/api/v1/runtimes/' + id, { method: 'DELETE' });
  loadAll();
}

// ============ Models ============
async function saveModel(e) {
  e.preventDefault();
  const id = document.getElementById('modelId').value;
  const name = document.getElementById('modelName').value;
  const path = document.getElementById('modelPath').value;
  const mmproj = document.getElementById('modelMMProj').value;
  const format = document.getElementById('modelFormat').value;
  const url = id ? `/api/v1/models/${id}` : '/api/v1/models';
  const method = id ? 'PUT' : 'POST';
  const body = JSON.stringify({ name, path, mmproj, format });
  await fetch(url, { method, headers: {'Content-Type':'application/json'}, body });
  hideModal('modelModal');
  loadAll();
}

async function deleteModel(id) {
  if (!confirm('Delete this model?')) return;
  await fetch('/api/v1/models/' + id, { method: 'DELETE' });
  loadAll();
}

// ============ Logs ============
const logViewer = document.getElementById('logViewer');
let evtSource = null;

function connectLogs() {
  if (typeof EventSource !== 'undefined') {
    evtSource = new EventSource('/api/v1/logs/stream');
    evtSource.onmessage = (ev) => {
      try {
        const data = JSON.parse(ev.data);
        const msg = `[${new Date().toISOString()}] ${data.level || 'INFO'}: ${data.message || JSON.stringify(data)}`;
        logViewer.textContent += msg + '\n';
        logViewer.scrollTop = logViewer.scrollHeight;
      } catch(e) {}
    };
    evtSource.onerror = () => { evtSource.close(); };
  }
}

function clearLogs() { logViewer.textContent = ''; }

// ============ Escape HTML ============
function esc(s) {
  const d = document.createElement('div');
  d.textContent = s || '';
  return d.innerHTML;
}

// ============ Init ============
loadAll();
connectLogs();
setInterval(loadAll, 5000);
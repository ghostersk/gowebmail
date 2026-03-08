// GoWebMail Admin SPA

const adminRoutes = {
  '/admin':           renderUsers,
  '/admin/settings':  renderSettings,
  '/admin/audit':     renderAudit,
  '/admin/security':  renderSecurity,
};

function navigate(path) {
  history.pushState({}, '', path);
  document.querySelectorAll('.admin-nav a').forEach(a => a.classList.toggle('active', a.getAttribute('href') === path));
  const fn = adminRoutes[path];
  if (fn) fn();
}

window.addEventListener('popstate', () => {
  const fn = adminRoutes[location.pathname];
  if (fn) fn();
});

// ============================================================
// Users
// ============================================================
async function renderUsers() {
  const el = document.getElementById('admin-content');
  el.innerHTML = `
    <div class="admin-page-header">
      <h1>Users</h1>
      <p>Manage GoWebMail accounts and permissions.</p>
    </div>
    <div class="admin-card">
      <div style="display:flex;justify-content:flex-end;margin-bottom:16px">
        <button class="btn-primary" onclick="openCreateUser()">+ New User</button>
      </div>
      <div id="users-table"><div class="spinner"></div></div>
    </div>
    <div class="modal-overlay" id="user-modal">
      <div class="modal">
        <h2 id="user-modal-title">New User</h2>
        <input type="hidden" id="user-id">
        <div class="modal-field"><label>Username</label><input type="text" id="user-username"></div>
        <div class="modal-field"><label>Email</label><input type="email" id="user-email"></div>
        <div class="modal-field"><label id="user-pw-label">Password</label><input type="password" id="user-password" placeholder="Min. 8 characters"></div>
        <div class="modal-field">
          <label>Role</label>
          <select id="user-role">
            <option value="user">User</option>
            <option value="admin">Admin</option>
          </select>
        </div>
        <div class="modal-field" id="user-active-field">
          <label>Active</label>
          <select id="user-active"><option value="1">Active</option><option value="0">Disabled</option></select>
        </div>
        <div class="modal-actions">
          <button class="modal-cancel" onclick="closeModal('user-modal')">Cancel</button>
          <button class="modal-submit" onclick="saveUser()">Save</button>
        </div>
      </div>
    </div>`;
  loadUsersTable();
}

async function loadUsersTable() {
  const r = await api('GET', '/admin/users');
  const el = document.getElementById('users-table');
  if (!r) { el.innerHTML = '<p class="alert error">Failed to load users</p>'; return; }
  if (!r.length) { el.innerHTML = '<p style="color:var(--muted);font-size:13px">No users yet.</p>'; return; }
  el.innerHTML = `<table class="data-table">
    <thead><tr><th>Username</th><th>Email</th><th>Role</th><th>Status</th><th>MFA</th><th>Last Login</th><th></th></tr></thead>
    <tbody>${r.map(u => `
      <tr>
        <td style="font-weight:500">${esc(u.username)}</td>
        <td style="color:var(--muted)">${esc(u.email)}</td>
        <td><span class="badge ${u.role==='admin'?'blue':'amber'}">${u.role}</span></td>
        <td><span class="badge ${u.is_active?'green':'red'}">${u.is_active?'Active':'Disabled'}</span></td>
        <td><span class="badge ${u.mfa_enabled?'blue':'amber'}">${u.mfa_enabled?'On':'Off'}</span></td>
        <td style="color:var(--muted);font-size:12px">${u.last_login_at ? new Date(u.last_login_at).toLocaleDateString() : 'Never'}</td>
        <td style="display:flex;gap:4px;justify-content:flex-end;flex-wrap:wrap">
          <button class="btn-secondary" style="padding:4px 10px;font-size:12px" onclick="openEditUser(${u.id})">Edit</button>
          <button class="btn-secondary" style="padding:4px 10px;font-size:12px" onclick="openResetPassword(${u.id},'${esc(u.username)}')">🔑 Reset PW</button>
          ${u.mfa_enabled?`<button class="btn-secondary" style="padding:4px 10px;font-size:12px;color:var(--warning,#f90)" onclick="disableMFA(${u.id},'${esc(u.username)}')">🔒 Disable MFA</button>`:''}
          <button class="btn-danger" style="padding:4px 10px;font-size:12px" onclick="deleteUser(${u.id})">Delete</button>
        </td>
      </tr>`).join('')}
    </tbody></table>`;
}

function openCreateUser() {
  document.getElementById('user-modal-title').textContent = 'New User';
  document.getElementById('user-id').value = '';
  document.getElementById('user-username').value = '';
  document.getElementById('user-email').value = '';
  document.getElementById('user-password').value = '';
  document.getElementById('user-role').value = 'user';
  document.getElementById('user-pw-label').textContent = 'Password';
  document.getElementById('user-active-field').style.display = 'none';
  openModal('user-modal');
}

async function openEditUser(userId) {
  const r = await api('GET', '/admin/users');
  if (!r) return;
  const user = r.find(u => u.id === userId);
  if (!user) return;
  document.getElementById('user-modal-title').textContent = 'Edit User';
  document.getElementById('user-id').value = userId;
  document.getElementById('user-username').value = user.username;
  document.getElementById('user-email').value = user.email;
  document.getElementById('user-password').value = '';
  document.getElementById('user-role').value = user.role;
  document.getElementById('user-active').value = user.is_active ? '1' : '0';
  document.getElementById('user-pw-label').textContent = 'New Password (leave blank to keep)';
  document.getElementById('user-active-field').style.display = 'block';
  openModal('user-modal');
}

async function saveUser() {
  const userId = document.getElementById('user-id').value;
  const body = {
    username: document.getElementById('user-username').value.trim(),
    email:    document.getElementById('user-email').value.trim(),
    role:     document.getElementById('user-role').value,
    is_active: document.getElementById('user-active').value === '1',
  };
  const pw = document.getElementById('user-password').value;
  if (pw) body.password = pw;
  else if (!userId) { toast('Password required for new users', 'error'); return; }

  const r = userId
    ? await api('PUT', '/admin/users/' + userId, body)
    : await api('POST', '/admin/users', { ...body, password: pw });

  if (r && r.ok) { toast(userId ? 'User updated' : 'User created', 'success'); closeModal('user-modal'); loadUsersTable(); }
  else toast((r && r.error) || 'Save failed', 'error');
}

async function deleteUser(userId) {
  if (!confirm('Delete this user? All their accounts and messages will be deleted.')) return;
  const r = await api('DELETE', '/admin/users/' + userId);
  if (r && r.ok) { toast('User deleted', 'success'); loadUsersTable(); }
  else toast((r && r.error) || 'Delete failed', 'error');
}

async function disableMFA(userId, username) {
  if (!confirm(`Disable MFA for "${username}"? They will be able to log in without a TOTP code until they re-enable it.`)) return;
  const r = await api('PUT', '/admin/users/' + userId, { disable_mfa: true });
  if (r && r.ok) { toast('MFA disabled for ' + username, 'success'); loadUsersTable(); }
  else toast((r && r.error) || 'Failed to disable MFA', 'error');
}

function openResetPassword(userId, username) {
  const pw = prompt(`Reset password for "${username}"\n\nEnter new password (min. 8 characters):`);
  if (!pw) return;
  if (pw.length < 8) { toast('Password must be at least 8 characters', 'error'); return; }
  api('PUT', '/admin/users/' + userId, { password: pw }).then(r => {
    if (r && r.ok) toast('Password reset for ' + username, 'success');
    else toast((r && r.error) || 'Failed to reset password', 'error');
  });
}

// ============================================================
// Settings
// ============================================================
const SETTINGS_META = [
  {
    group: 'Server',
    fields: [
      { key: 'HOSTNAME',    label: 'Hostname',     desc: 'Public hostname (no protocol or port). e.g. mail.example.com', type: 'text' },
      { key: 'LISTEN_ADDR', label: 'Listen Address', desc: 'Bind address e.g. :8080 or 0.0.0.0:8080', type: 'text' },
      { key: 'BASE_URL',    label: 'Base URL',      desc: 'Leave blank to auto-build from hostname + port', type: 'text' },
    ]
  },
  {
    group: 'Security',
    fields: [
      { key: 'SECURE_COOKIE',   label: 'Secure Cookies', desc: 'Set true when serving over HTTPS', type: 'select', options: ['false','true'] },
      { key: 'TRUSTED_PROXIES', label: 'Trusted Proxies', desc: 'Comma-separated IPs/CIDRs allowed to set X-Forwarded-For', type: 'text' },
      { key: 'SESSION_MAX_AGE', label: 'Session Max Age', desc: 'Session lifetime in seconds (default 604800 = 7 days)', type: 'number' },
    ]
  },
  {
    group: 'Gmail OAuth',
    fields: [
      { key: 'GOOGLE_CLIENT_ID',     label: 'Google Client ID',     type: 'text' },
      { key: 'GOOGLE_CLIENT_SECRET', label: 'Google Client Secret', type: 'password' },
      { key: 'GOOGLE_REDIRECT_URL',  label: 'Google Redirect URL',  desc: 'Leave blank to auto-derive from Base URL', type: 'text' },
    ]
  },
  {
    group: 'Outlook OAuth',
    fields: [
      { key: 'MICROSOFT_CLIENT_ID',     label: 'Microsoft Client ID',     type: 'text' },
      { key: 'MICROSOFT_CLIENT_SECRET', label: 'Microsoft Client Secret', type: 'password' },
      { key: 'MICROSOFT_TENANT_ID',     label: 'Microsoft Tenant ID',     desc: 'Use "common" for multi-tenant', type: 'text' },
      { key: 'MICROSOFT_REDIRECT_URL',  label: 'Microsoft Redirect URL',  desc: 'Leave blank to auto-derive from Base URL', type: 'text' },
    ]
  },
  {
    group: 'Database',
    fields: [
      { key: 'DB_PATH', label: 'Database Path', desc: 'Path to SQLite file, relative to working directory', type: 'text' },
    ]
  },
  {
    group: 'Security Notifications',
    fields: [
      { key: 'NOTIFY_ENABLED',   label: 'Enabled',      desc: 'Send email to users when brute-force attack is detected on their account', type: 'select', options: ['true','false'] },
      { key: 'NOTIFY_SMTP_HOST', label: 'SMTP Host',    desc: 'SMTP server for sending alerts. Example: smtp.example.com', type: 'text' },
      { key: 'NOTIFY_SMTP_PORT', label: 'SMTP Port',    desc: '587 = STARTTLS, 465 = TLS, 25 = plain relay', type: 'number' },
      { key: 'NOTIFY_FROM',      label: 'From Address', desc: 'Sender email. Example: security@example.com', type: 'text' },
      { key: 'NOTIFY_USER',      label: 'SMTP Username', desc: 'Leave blank for unauthenticated relay', type: 'text' },
      { key: 'NOTIFY_PASS',      label: 'SMTP Password', desc: 'Leave blank for unauthenticated relay', type: 'password' },
    ]
  },
  {
    group: 'Brute Force Protection',
    fields: [
      { key: 'BRUTE_ENABLED',        label: 'Enabled',         desc: 'Auto-block IPs after repeated failed logins', type: 'select', options: ['true','false'] },
      { key: 'BRUTE_MAX_ATTEMPTS',   label: 'Max Attempts',    desc: 'Failed logins before ban', type: 'number' },
      { key: 'BRUTE_WINDOW_MINUTES', label: 'Window (minutes)',desc: 'Time window for counting failures', type: 'number' },
      { key: 'BRUTE_BAN_HOURS',      label: 'Ban Duration (hours)', desc: '0 = permanent ban (admin must unban)', type: 'number' },
      { key: 'BRUTE_WHITELIST_IPS',  label: 'Whitelist IPs',   desc: 'Comma-separated IPs that are never blocked', type: 'text' },
    ]
  },
  {
    group: 'Geo Blocking',
    fields: [
      { key: 'GEO_BLOCK_COUNTRIES', label: 'Block Countries', desc: 'Comma-separated ISO codes to DENY (e.g. CN,RU,KP). Takes precedence over Allow list.', type: 'text' },
      { key: 'GEO_ALLOW_COUNTRIES', label: 'Allow Countries', desc: 'Comma-separated ISO codes to ALLOW exclusively (e.g. SK,CZ,DE). Leave blank to allow all.', type: 'text' },
    ]
  },
];

async function renderSettings() {
  const el = document.getElementById('admin-content');
  el.innerHTML = '<div class="spinner" style="margin-top:80px"></div>';
  const r = await api('GET', '/admin/settings');
  if (!r) { el.innerHTML = '<p class="alert error">Failed to load settings</p>'; return; }

  const groups = SETTINGS_META.map(g => `
    <div class="settings-group">
      <div class="settings-group-title">${g.group}</div>
      ${g.fields.map(f => {
        const val = esc(r[f.key] || '');
        const control = f.type === 'select'
          ? `<select id="cfg-${f.key}">${f.options.map(o => `<option value="${o}" ${r[f.key]===o?'selected':''}>${o}</option>`).join('')}</select>`
          : `<input type="${f.type}" id="cfg-${f.key}" value="${val}" placeholder="${f.desc||''}">`;
        return `
          <div class="setting-row">
            <div><div class="setting-label">${f.label}</div>${f.desc?`<div class="setting-desc">${f.desc}</div>`:''}</div>
            <div class="setting-control">${control}</div>
          </div>`;
      }).join('')}
    </div>`).join('');

  el.innerHTML = `
    <div class="admin-page-header">
      <h1>Application Settings</h1>
      <p>Changes are saved to <code style="font-family:monospace;background:var(--surface3);padding:2px 6px;border-radius:4px">data/gowebmail.conf</code> and take effect immediately for most settings. A restart is required for LISTEN_ADDR changes.</p>
    </div>
    <div id="settings-alert" style="display:none"></div>
    <div class="admin-card">
      ${groups}
      <div style="display:flex;justify-content:flex-end;gap:10px;margin-top:20px">
        <button class="btn-secondary" onclick="loadSettingsValues()">Reset</button>
        <button class="btn-primary" onclick="saveSettings()">Save Settings</button>
      </div>
    </div>`;
}

async function loadSettingsValues() {
  const r = await api('GET', '/admin/settings');
  if (!r) return;
  SETTINGS_META.forEach(g => g.fields.forEach(f => {
    const el = document.getElementById('cfg-' + f.key);
    if (el) el.value = r[f.key] || '';
  }));
}

async function saveSettings() {
  const body = {};
  SETTINGS_META.forEach(g => g.fields.forEach(f => {
    const el = document.getElementById('cfg-' + f.key);
    if (el) body[f.key] = el.value.trim();
  }));
  const r = await api('PUT', '/admin/settings', body);
  const alertEl = document.getElementById('settings-alert');
  if (r && r.ok) {
    toast('Settings saved', 'success');
    alertEl.className = 'alert success';
    alertEl.textContent = 'Settings saved. LISTEN_ADDR changes require a restart.';
    alertEl.style.display = 'block';
    setTimeout(() => alertEl.style.display = 'none', 5000);
  } else {
    alertEl.className = 'alert error';
    alertEl.textContent = (r && r.error) || 'Save failed';
    alertEl.style.display = 'block';
  }
}

// ============================================================
// Audit Log
// ============================================================
async function renderAudit(page) {
  page = page || 1;
  const el = document.getElementById('admin-content');
  if (page === 1) el.innerHTML = '<div class="spinner" style="margin-top:80px"></div>';

  const r = await api('GET', '/admin/audit?page=' + page + '&page_size=50');
  if (!r) { el.innerHTML = '<p class="alert error">Failed to load audit log</p>'; return; }

  const rows = (r.logs || []).map(l => `
    <tr>
      <td style="font-family:monospace;font-size:11px;color:var(--muted)">${new Date(l.created_at).toLocaleString()}</td>
      <td style="font-weight:500">${esc(l.user_email || 'system')}</td>
      <td><span class="badge ${eventBadge(l.event)}">${esc(l.event)}</span></td>
      <td style="color:var(--muted);font-size:12px">${esc(l.detail)}</td>
      <td style="font-family:monospace;font-size:11px;color:var(--muted)">${esc(l.ip_address)}</td>
    </tr>`).join('');

  el.innerHTML = `
    <div class="admin-page-header">
      <h1>Audit Log</h1>
      <p>Security and administrative activity log.</p>
    </div>
    <div class="admin-card" style="padding:0;overflow:hidden">
      <table class="data-table">
        <thead><tr><th>Time</th><th>User</th><th>Event</th><th>Detail</th><th>IP</th></tr></thead>
        <tbody>${rows || '<tr><td colspan="5" style="text-align:center;color:var(--muted);padding:30px">No events</td></tr>'}</tbody>
      </table>
      ${r.has_more ? `<div style="padding:12px;text-align:center"><button class="load-more-btn" onclick="renderAudit(${page+1})">Load more</button></div>` : ''}
    </div>`;
}

function eventBadge(evt) {
  if (!evt) return 'amber';
  if (evt.includes('login') || evt.includes('auth')) return 'blue';
  if (evt.includes('error') || evt.includes('fail')) return 'red';
  if (evt.includes('delete') || evt.includes('remove')) return 'red';
  if (evt.includes('create') || evt.includes('add')) return 'green';
  return 'amber';
}

// Boot: detect current page from URL
(function() {
  const path = location.pathname;
  document.querySelectorAll('.admin-nav a').forEach(a => a.classList.toggle('active', a.getAttribute('href') === path));
  const fn = adminRoutes[path];
  if (fn) fn();
  else renderUsers();

  document.querySelectorAll('.admin-nav a').forEach(a => {
    a.addEventListener('click', e => {
      e.preventDefault();
      navigate(a.getAttribute('href'));
    });
  });
})();
// ============================================================
// Security — IP Blocks & Login Attempts
// ============================================================
async function renderSecurity() {
  const el = document.getElementById('admin-content');
  el.innerHTML = `
    <div class="admin-page-header">
      <h1>Security</h1>
      <p>Monitor login attempts, manage IP blocks, and control access by country.</p>
    </div>

    <div class="admin-card" style="margin-bottom:24px">
      <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">
        <h2 style="margin:0;font-size:16px">Blocked IPs</h2>
        <button class="btn-primary" onclick="openAddBlock()">+ Block IP</button>
      </div>
      <div id="blocks-table"><div class="spinner"></div></div>
    </div>

    <div class="admin-card">
      <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">
        <h2 style="margin:0;font-size:16px">Login Attempts (last 72h)</h2>
        <button class="btn-secondary" onclick="loadLoginAttempts()">↻ Refresh</button>
      </div>
      <div id="attempts-table"><div class="spinner"></div></div>
    </div>

    <div class="modal-overlay" id="add-block-modal">
      <div class="modal" style="max-width:420px">
        <h2>Block IP Address</h2>
        <div class="modal-field"><label>IP Address</label><input type="text" id="block-ip" placeholder="e.g. 192.168.1.100"></div>
        <div class="modal-field"><label>Reason</label><input type="text" id="block-reason" placeholder="Manual admin block"></div>
        <div class="modal-field"><label>Ban Hours (0 = permanent)</label><input type="number" id="block-hours" value="24" min="0"></div>
        <div class="modal-actions">
          <button class="btn-secondary" onclick="closeModal('add-block-modal')">Cancel</button>
          <button class="btn-primary" onclick="submitAddBlock()">Block IP</button>
        </div>
      </div>
    </div>`;

  loadIPBlocks();
  loadLoginAttempts();
}

async function loadIPBlocks() {
  const el = document.getElementById('blocks-table');
  if (!el) return;
  const r = await api('GET', '/admin/ip-blocks');
  const blocks = r?.blocks || [];
  if (!blocks.length) {
    el.innerHTML = '<p style="color:var(--muted);padding:8px 0">No blocked IPs.</p>';
    return;
  }
  el.innerHTML = `<table class="admin-table" style="width:100%">
    <thead><tr>
      <th>IP</th><th>Country</th><th>Reason</th><th>Attempts</th><th>Blocked At</th><th>Expires</th><th></th>
    </tr></thead>
    <tbody>
    ${blocks.map(b => `<tr>
      <td><code>${esc(b.ip)}</code></td>
      <td>${b.country_code ? `<span title="${esc(b.country)}">${esc(b.country_code)}</span>` : '—'}</td>
      <td>${esc(b.reason)}</td>
      <td>${b.attempts||0}</td>
      <td style="font-size:11px">${fmtDate(b.blocked_at)}</td>
      <td style="font-size:11px;color:var(--muted)">${b.is_permanent ? '♾ Permanent' : b.expires_at ? fmtDate(b.expires_at) : '—'}</td>
      <td><button class="action-btn danger" onclick="unblockIP('${esc(b.ip)}')">Unblock</button></td>
    </tr>`).join('')}
    </tbody>
  </table>`;
}

async function loadLoginAttempts() {
  const el = document.getElementById('attempts-table');
  if (!el) return;
  const r = await api('GET', '/admin/login-attempts');
  const attempts = r?.attempts || [];
  if (!attempts.length) {
    el.innerHTML = '<p style="color:var(--muted);padding:8px 0">No login attempts recorded in the last 72 hours.</p>';
    return;
  }
  el.innerHTML = `<table class="admin-table" style="width:100%">
    <thead><tr>
      <th>IP</th><th>Country</th><th>Total</th><th>Failures</th><th>Last Seen</th><th></th>
    </tr></thead>
    <tbody>
    ${attempts.map(a => `<tr ${a.failures>3?'style="background:rgba(255,80,80,.07)"':''}>
      <td><code>${esc(a.ip)}</code></td>
      <td>${a.country_code ? `<span title="${esc(a.country)}">${esc(a.country_code)} ${esc(a.country)}</span>` : '—'}</td>
      <td>${a.total}</td>
      <td style="${a.failures>3?'color:#f87;font-weight:600':''}">${a.failures}</td>
      <td style="font-size:11px">${a.last_seen||'—'}</td>
      <td><button class="action-btn danger" onclick="blockFromAttempt('${esc(a.ip)}')">Block</button></td>
    </tr>`).join('')}
    </tbody>
  </table>`;
}

function openAddBlock() { openModal('add-block-modal'); }

async function submitAddBlock() {
  const ip = document.getElementById('block-ip').value.trim();
  const reason = document.getElementById('block-reason').value.trim() || 'Manual admin block';
  const hours = parseInt(document.getElementById('block-hours').value) || 0;
  if (!ip) { toast('IP address required', 'error'); return; }
  const r = await api('POST', '/admin/ip-blocks', { ip, reason, ban_hours: hours });
  if (r?.ok) { toast('IP blocked', 'success'); closeModal('add-block-modal'); loadIPBlocks(); }
  else toast(r?.error || 'Failed', 'error');
}

async function unblockIP(ip) {
  const r = await fetch('/api/admin/ip-blocks/' + encodeURIComponent(ip), { method: 'DELETE' });
  const data = await r.json();
  if (data?.ok) { toast('IP unblocked', 'success'); loadIPBlocks(); }
  else toast(data?.error || 'Failed', 'error');
}

function blockFromAttempt(ip) {
  document.getElementById('block-ip').value = ip;
  document.getElementById('block-reason').value = 'Manual block from login attempts';
  openModal('add-block-modal');
}

function fmtDate(s) {
  if (!s) return '—';
  try { return new Date(s).toLocaleString(); } catch(e) { return s; }
}

function esc(s) {
  if (!s) return '';
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

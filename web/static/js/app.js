// GoWebMail app.js — full client

// ── State ──────────────────────────────────────────────────────────────────
const S = {
  me: null, accounts: [], providers: {gmail:false,outlook:false}, signatures: [],
  folders: [], messages: [], totalMessages: 0, labels: [],
  currentPage: 1, currentFolder: 'unified', currentFolderName: 'Unified Inbox',
  currentMessage: null, selectedMessageId: null,
  searchQuery: '', composeMode: 'new', composeReplyToId: null,
  filterUnread: false, filterAttachment: false,
  sortOrder: 'date-desc', // 'date-desc' | 'date-asc' | 'size-desc'
  uiPrefs: {}, // server-persisted UI preferences (collapsed accounts/folders etc.)
  // Advanced search filters (Filter icon next to the search box) — cleared by clearSearchFilters().
  searchFilters: { scope:'', hasAttachment:'', dateFrom:'', dateTo:'', minSizeKB:'', maxSizeKB:'', accountId:'', folderId:'' },
};
function hasActiveSearchFilters() {
  const f = S.searchFilters;
  return !!(f.scope || f.hasAttachment !== '' || f.dateFrom || f.dateTo || f.minSizeKB || f.maxSizeKB || f.accountId || f.folderId);
}

// ── UI Preferences (server-persisted, cross-device) ─────────────────────────
let _uiPrefsSaveTimer = null;
function uiPrefsGet(key, def) { return (key in S.uiPrefs) ? S.uiPrefs[key] : def; }
function uiPrefsSet(key, val) {
  S.uiPrefs[key] = val;
  clearTimeout(_uiPrefsSaveTimer);
  _uiPrefsSaveTimer = setTimeout(() => {
    api('PUT', '/ui-prefs', S.uiPrefs);
  }, 600); // debounce 600ms
}
function isAccountCollapsed(accId) { return uiPrefsGet('ac_'+accId, false); }
function setAccountCollapsed(accId, v) { uiPrefsSet('ac_'+accId, v); }

// ── View settings (density / reading pane position / sidebar mode) ──────────
function applyViewPrefs() {
  const density = uiPrefsGet('density', 'compact');
  const pane = uiPrefsGet('readingPane', 'right');
  const sidebarMode = uiPrefsGet('sidebarMode', 'expanded');
  const conv = uiPrefsGet('conversations', 'on');
  [['on','Conversations'],['off','Individual emails']].forEach(([v,label]) => {
    const el = document.getElementById('vopt-conv-'+v);
    if (el) el.textContent = (v===conv?'✓ ':'○ ') + label;
  });
  const root = document.getElementById('app-root');
  if (root) { root.dataset.density = density; root.dataset.readingPane = pane; root.dataset.sidebar = sidebarMode; }
  [['compact','Compact'],['comfortable','Comfortable']].forEach(([v,label]) => {
    const el = document.getElementById('vopt-density-'+v);
    if (el) el.textContent = (v===density?'✓ ':'○ ') + label;
  });
  [['right','Right'],['bottom','Bottom']].forEach(([v,label]) => {
    const el = document.getElementById('vopt-pane-'+v);
    if (el) el.textContent = (v===pane?'✓ ':'○ ') + label;
  });
  [['expanded','Pinned (always visible)'],['collapsed','Minimized'],['auto','Auto-hide (peek on hover)']].forEach(([v,label]) => {
    const el = document.getElementById('vopt-sidebar-'+v);
    if (el) el.textContent = (v===sidebarMode?'✓ ':'○ ') + label;
  });
  const listPanel = document.querySelector('.message-list-panel');
  if (listPanel) {
    const widthPx = uiPrefsGet('panelWidthPx', null);
    const heightPct = uiPrefsGet('panelHeightPct', null);
    if (widthPx) listPanel.style.width = widthPx + 'px';
    if (heightPct) listPanel.style.height = heightPct + '%';
  }
}

// ── Draggable list/reading-pane divider (desktop only) ───────────────────────
// Persisted via uiPrefs (server-side, cross-device) rather than a cookie.
function initPanelResize() {
  const handle = document.getElementById('panel-resize-handle');
  const listPanel = document.querySelector('.message-list-panel');
  if (!handle || !listPanel) return;
  let dragging = false;
  handle.addEventListener('mousedown', e => {
    dragging = true;
    handle.classList.add('dragging');
    document.body.style.userSelect = 'none';
    e.preventDefault();
  });
  document.addEventListener('mousemove', e => {
    if (!dragging) return;
    const mailView = document.getElementById('mail-view');
    const r = mailView.getBoundingClientRect();
    if (document.getElementById('app-root').dataset.readingPane === 'bottom') {
      const pct = Math.min(80, Math.max(15, ((e.clientY - r.top) / r.height) * 100));
      listPanel.style.height = pct + '%';
    } else {
      const w = Math.min(680, Math.max(220, e.clientX - r.left));
      listPanel.style.width = w + 'px';
    }
  });
  document.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    handle.classList.remove('dragging');
    document.body.style.userSelect = '';
    if (document.getElementById('app-root').dataset.readingPane === 'bottom') {
      uiPrefsSet('panelHeightPct', parseFloat(listPanel.style.height));
    } else {
      uiPrefsSet('panelWidthPx', parseInt(listPanel.style.width));
    }
  });
}
function setViewPref(key, val) {
  uiPrefsSet(key, val);
  applyViewPrefs();
  if (key === 'conversations') {
    if (val === 'off') threadSiblingsById = {}; // no grouped detail view while flat
    renderMessageList();
  }
  closeTopMenus(); // closes the View dropdown (items stopPropagation, so outside-click can't)
}
// Quick manual toggle (header button + edge tab): flip between fully visible
// and hidden. Doesn't touch 'auto' as a saved preference — pick that from the
// View menu; this button just pins/unpins whatever's currently hidden.
function toggleSidebarCollapse() {
  const cur = uiPrefsGet('sidebarMode', 'expanded');
  setViewPref('sidebarMode', cur === 'expanded' ? 'collapsed' : 'expanded');
}
// Top-row menus (File/View/Help/Profile) share one outside-click listener; a
// stale close handler must never swallow the click that opens the next menu.
const TOP_MENUS = { help: ['help-menu', 'help-btn'], profile: ['profile-menu', 'profile-btn'] };
let _tbClose = null;
function closeTopMenus() {
  let anyOpen = false;
  Object.keys(TOP_MENUS).forEach(k => {
    const m = document.getElementById(TOP_MENUS[k][0]);
    if (m?.style.display === 'block') anyOpen = true;
    if (m) m.style.display = 'none';
    document.getElementById(TOP_MENUS[k][1])?.setAttribute('aria-expanded', 'false');
  });
  const v = document.getElementById('view-dropdown-menu');
  if (v) { if (v.style.display !== 'none') anyOpen = true; v.style.display = 'none'; }
  document.getElementById('view-dropdown-btn')?.setAttribute('aria-expanded', 'false');
  if (_tbClose) { document.removeEventListener('click', _tbClose); _tbClose = null; }
  return anyOpen;
}
function armOutsideClose() {
  if (_tbClose) document.removeEventListener('click', _tbClose);
  _tbClose = () => closeTopMenus();
  setTimeout(() => document.addEventListener('click', _tbClose), 0);
}
function toggleViewDropdown(e) {
  e.stopPropagation();
  const menu = document.getElementById('view-dropdown-menu');
  if (!menu) return;
  const isOpen = menu.style.display !== 'none';
  closeTopMenus();
  if (!isOpen) {
    menu.style.display = 'block';
    document.getElementById('view-dropdown-btn').setAttribute('aria-expanded', 'true');
    armOutsideClose();
  }
}
function topMenuOpen(kind) {
  const [mid, bid] = TOP_MENUS[kind];
  const menu = document.getElementById(mid);
  if (!menu) return;
  const wasOpen = menu.style.display === 'block';
  closeTopMenus();
  if (!wasOpen) {
    menu.style.display = 'block';
    document.getElementById(bid).setAttribute('aria-expanded', 'true');
    armOutsideClose();
  }
}

// ── Boot ───────────────────────────────────────────────────────────────────
async function init() {
  const [me, providers, wl, uiPrefsRaw] = await Promise.all([
    api('GET','/me'), api('GET','/providers'), api('GET','/remote-content-whitelist'),
    api('GET','/ui-prefs'),
  ]);
  if (me) {
    S.me = me;
    const nm = me.username || me.email;
    const pe = document.getElementById('profile-email');
    if (pe) pe.textContent = nm;
    const pi = document.getElementById('profile-initials');
    if (pi) pi.textContent = (nm || '?').split(/\s+/).slice(0, 2).map(w => w[0]).join('').toUpperCase() || '?';
    if (me.role === 'admin') document.getElementById('admin-link').style.display = 'block';
    document.title = 'Email - ' + nm + ' - GoWebMail';
  }
  if (providers) { S.providers = providers; updateProviderButtons(); }
  if (wl?.whitelist) S.remoteWhitelist = new Set(wl.whitelist);
  if (uiPrefsRaw && typeof uiPrefsRaw === 'object') S.uiPrefs = uiPrefsRaw;
  applyViewPrefs();
  ensureContactsCache(); // warms the cache isRemoteContentAllowed()'s "Only from Contacts" check reads synchronously

  await loadAccounts();
  await loadFolders();
  await loadLabels();
  await loadMessages();
  // Seed the poller from the same query /api/poll itself uses for "newest inbox message id"
  // (not from whatever happens to be in the currently-loaded, paginated/date-sorted message
  // list — that view can under-represent the true max id, which previously made every page
  // load fire a false "new mail" notification for mail that had already been seen).
  const seedPoll = await api('GET', '/poll?since=0');
  if (seedPoll) POLLER.lastKnownID = seedPoll.newest_id || 0;

  const p = new URLSearchParams(location.search);
  if (p.get('connected')) {
    toast('Account connected! Loading…', 'success');
    history.replaceState({},'','/');
    await loadAccounts();
    pollForNewFolders();
  }
  if (p.get('error')) { toast('Connection failed: '+p.get('error'), 'error'); history.replaceState({},'','/'); }

  document.addEventListener('keydown', e => {
    if (e.key === 'Escape' && closeTopMenus()) return; // top-row menus close first, even while typing
    if (['INPUT','TEXTAREA','SELECT'].includes(e.target.tagName)) return;
    if (e.target.contentEditable === 'true') return;
    if ((e.metaKey||e.ctrlKey) && e.key==='n') { e.preventDefault(); openCompose(); return; }
    if ((e.metaKey||e.ctrlKey) && e.key==='k') { e.preventDefault(); document.getElementById('search-input').focus(); return; }
    if (e.metaKey||e.ctrlKey||e.altKey) return; // don't shadow browser/OS shortcuts below

    if (e.key==='j' || e.key==='k') {
      e.preventDefault();
      const msgs = getFilteredSortedMsgs();
      if (!msgs.length) return;
      let idx = msgs.findIndex(m=>m.id===S.selectedMessageId);
      idx = e.key==='j' ? Math.min(idx+1, msgs.length-1) : Math.max(idx-1, 0);
      openMessage(msgs[idx].id);
    } else if (e.key==='r' && S.currentMessage) {
      e.preventDefault(); openReplyTo(S.currentMessage.id);
    } else if (e.key==='f' && S.currentMessage) {
      e.preventDefault(); openForward();
    } else if ((e.key==='#'||e.key==='Delete') && S.currentMessage) {
      e.preventDefault(); deleteMessage(S.currentMessage.id);
    } else if (e.key==='Escape') {
      if (S.composeVisible) { e.preventDefault(); closeCompose(); }
      else if (S.currentMessage) { e.preventDefault(); resetDetail(); renderMessageList(); mobBack(); }
    }
  });

  initComposeDragResize();
  initPanelResize();
  startPoller();
  mobSetView('list'); // initialise mobile view state
}

// ── Providers ──────────────────────────────────────────────────────────────
function updateProviderButtons() {
  ['gmail','outlook','outlook_personal'].forEach(p => {
    const btn = document.getElementById('btn-'+p);
    if (!btn) return;
    if (!S.providers[p]) { btn.disabled=true; btn.classList.add('unavailable'); btn.title='Not configured'; }
  });
}

// ── Settings: connected-accounts list ────────────────────────────────────────
function renderAccountsSettingsList() {
  const el = document.getElementById('settings-accounts-list');
  if (!el) return; // Settings modal not open yet — nothing to render into
  if (!S.accounts.length) {
    el.innerHTML = '<div style="font-size:12px;color:var(--muted);padding:8px 0">No accounts connected.</div>';
    return;
  }
  el.innerHTML = S.accounts.map(a => {
    const hasWarning = a.last_error || a.token_expired;
    const warningTitle = a.token_expired ? 'OAuth token expired — click Manage to reconnect' : (a.last_error ? '⚠ '+a.last_error : '');
    return `
    <div class="acct-row" title="${esc(a.email_address)}${hasWarning?' — '+warningTitle:''}">
      <span class="account-dot" style="background:${a.color};flex-shrink:0"></span>
      <div style="flex:1;min-width:0">
        <div style="font-size:13px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(a.display_name||a.email_address)}</div>
        <div style="font-size:11px;color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(a.email_address)}</div>
      </div>
      ${a.token_expired?'<span style="color:var(--danger);font-size:11px" title="OAuth token expired">🔑</span>':
        a.last_error?'<span style="color:var(--danger);font-size:11px" title="'+esc(a.last_error)+'">⚠</span>':''}
      <div style="display:flex;gap:4px;flex-shrink:0">
        <button class="icon-btn" title="Sync now" onclick="syncNow(${a.id},event)">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M12 4V1L8 5l4 4V6c3.31 0 6 2.69 6 6 0 1.01-.25 1.97-.7 2.8l1.46 1.46C19.54 15.03 20 13.57 20 12c0-4.42-3.58-8-8-8zm0 14c-3.31 0-6-2.69-6-6 0-1.01.25-1.97.7-2.8L5.24 7.74C4.46 8.97 4 10.43 4 12c0 4.42 3.58 8 8 8v3l4-4-4-4v3z"/></svg>
        </button>
        <button class="btn-secondary" style="font-size:11px;padding:3px 10px" onclick="openEditAccount(${a.id})">Manage</button>
        <button class="icon-btn" title="Remove" onclick="deleteAccount(${a.id})" style="color:var(--danger)">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>
        </button>
      </div>
    </div>`;
  }).join('');
}

// ── Accounts ───────────────────────────────────────────────────────────────
async function loadAccounts() {
  const data = await api('GET','/accounts');
  if (!data) return;
  S.accounts = data;
  renderAccountsSettingsList();
  populateComposeFrom();
  loadSignatures();
}

// ── Signatures (compose prefill) ────────────────────────────────────────────
async function loadSignatures() {
  const data = await api('GET','/signatures');
  if (data) S.signatures = data;
}

// Returns the HTML for an account's default-for-new or default-for-reply signature, or ''.
function getSignatureHTML(accountId, forReply) {
  const acc = S.accounts.find(a => a.id === accountId);
  if (!acc) return '';
  const sigId = forReply ? acc.default_signature_reply_id : acc.default_signature_new_id;
  if (!sigId) return '';
  const sig = S.signatures.find(s => s.id === sigId);
  return sig ? sig.content_html : '';
}

// Renders the (possibly empty) signature block with a stable id so it can be swapped
// live if the From-account changes mid-compose, without touching the rest of the body.
function signatureBlockHTML(accountId, forReply) {
  const html = getSignatureHTML(accountId, forReply);
  // A block-level "<div><br></div>" renders as exactly one blank line in every browser;
  // a bare <br><br> doesn't — its rendered height depends on what precedes/follows it
  // (e.g. it collapses to nothing right before another block element), which is exactly
  // the inconsistency this was written to avoid.
  return `<div id="sig-block">${html ? '<div><br></div>' + html : ''}</div>`;
}

// Bound to #compose-from's change event — swaps just the signature block.
function onComposeFromChange() {
  const accountId = parseInt(document.getElementById('compose-from')?.value || 0);
  const forReply = S.composeMode !== 'new';
  const block = document.getElementById('sig-block');
  if (block) block.outerHTML = signatureBlockHTML(accountId, forReply);
}

function connectOAuth(p) {
  if (p === 'outlook_personal') {
    location.href = '/auth/outlook-personal/connect';
  } else {
    location.href = '/auth/' + p + '/connect';
  }
}

function openAddAccountModal() {
  ['imap-email','imap-name','imap-password','imap-host','smtp-host','imap-caldav-url','imap-carddav-url'].forEach(id=>{ const el=document.getElementById(id); if(el) el.value=''; });
  document.getElementById('imap-port').value='993';
  document.getElementById('smtp-port').value='587';
  document.getElementById('use-jmap').checked=false;
  toggleJMAPFields();
  const r=document.getElementById('test-result'); if(r){r.style.display='none';r.className='test-result';}
  openModal('add-account-modal');
}

// Toggles the Add Account modal between IMAP/SMTP fields and a single JMAP
// server URL field (JMAP has no separate SMTP host/port or IMAP port — one
// base URL covers both mail access and sending).
function toggleJMAPFields() {
  const jmap=document.getElementById('use-jmap').checked;
  document.getElementById('imap-port-field').style.display=jmap?'none':'';
  document.getElementById('smtp-fields').style.display=jmap?'none':'';
  document.getElementById('imap-hint').style.display=jmap?'none':'';
  const label=document.getElementById('imap-host-label'), host=document.getElementById('imap-host');
  label.textContent=jmap?'JMAP Server URL':'IMAP Host';
  host.placeholder=jmap?'https://mail.example.com:8443':'imap.example.com';
}

async function testNewConnection() {
  const btn=document.getElementById('test-btn'), result=document.getElementById('test-result');
  const jmap=document.getElementById('use-jmap').checked;
  const body={email:document.getElementById('imap-email').value.trim(),password:document.getElementById('imap-password').value,
    imap_host:document.getElementById('imap-host').value.trim()};
  if (jmap) { body.provider='jmap'; }
  else {
    body.imap_port=parseInt(document.getElementById('imap-port').value)||993;
    body.smtp_host=document.getElementById('smtp-host').value.trim();
    body.smtp_port=parseInt(document.getElementById('smtp-port').value)||587;
  }
  if (!body.email||!body.password||!body.imap_host){result.textContent=(jmap?'Email, password and JMAP server URL required.':'Email, password and IMAP host required.');result.className='test-result err';result.style.display='block';return;}
  btn.innerHTML='<span class="spinner-inline"></span>Testing...';btn.disabled=true;
  const r=await api('POST','/accounts/test',body,20000);
  btn.textContent='Test Connection';btn.disabled=false;
  result.textContent=(r?.ok)?'✓ Connection successful!':((r?.error?.message||r?.error)||'Connection failed');
  result.className='test-result '+((r?.ok)?'ok':'err'); result.style.display='block';
}

async function addIMAPAccount() {
  const btn=document.getElementById('save-acct-btn');
  const jmap=document.getElementById('use-jmap').checked;
  const body={email:document.getElementById('imap-email').value.trim(),display_name:document.getElementById('imap-name').value.trim(),
    password:document.getElementById('imap-password').value,imap_host:document.getElementById('imap-host').value.trim()};
  if (jmap) { body.provider='jmap'; }
  else {
    body.imap_port=parseInt(document.getElementById('imap-port').value)||993;
    body.smtp_host=document.getElementById('smtp-host').value.trim();
    body.smtp_port=parseInt(document.getElementById('smtp-port').value)||587;
  }
  body.caldav_url=document.getElementById('imap-caldav-url').value.trim();
  body.carddav_url=document.getElementById('imap-carddav-url').value.trim();
  if (!body.email||!body.password||!body.imap_host){toast((jmap?'Email, password and JMAP server URL required':'Email, password and IMAP host required'),'error');return;}
  btn.disabled=true;btn.textContent='Connecting...';
  const r=await api('POST','/accounts',body);
  btn.disabled=false;btn.textContent='Connect';
  if (r?.ok){
    toast('Account added — syncing…','success');
    closeModal('add-account-modal');
    await loadAccounts();
    pollForNewFolders();
  } else toast(r?.error||'Failed to add account','error');
}

// Repeatedly reloads folders/accounts until the newly-added account's folders show up
// (initial IMAP sync takes a few seconds), instead of guessing a fixed delay. Also
// refreshes messages/unread counts so the sidebar updates without a manual page reload.
function pollForNewFolders() {
  let tries = 0;
  const poll = setInterval(async () => {
    tries++;
    await loadAccounts();
    await loadFolders();
    await loadMessages();
    const hasFolders = S.accounts.some(a => S.folders.some(f => f.account_id === a.id));
    if (hasFolders || tries >= 12) {
      clearInterval(poll);
      if (hasFolders) toast('Account ready!', 'success');
    }
  }, 2500);
}

async function detectMailSettings() {
  const email=document.getElementById('imap-email').value.trim();
  if (!email||!email.includes('@')){toast('Enter your email address first','error');return;}
  const btn=document.getElementById('detect-btn');
  btn.innerHTML='<span class="spinner-inline"></span>Detecting…';btn.disabled=true;
  const r=await api('POST','/accounts/detect',{email});
  btn.textContent='Auto-detect';btn.disabled=false;
  if (!r){toast('Detection failed','error');return;}
  document.getElementById('imap-host').value=r.imap_host||'';
  document.getElementById('imap-port').value=r.imap_port||993;
  document.getElementById('smtp-host').value=r.smtp_host||'';
  document.getElementById('smtp-port').value=r.smtp_port||587;
  if(r.detected) toast(`Detected ${r.imap_host} / ${r.smtp_host}`,'success');
  else toast('No servers found — filled with defaults based on domain','info');
}

async function syncNow(id, e) {
  if (e) e.stopPropagation();
  toast('Syncing…','info');
  const r = await api('POST','/accounts/'+id+'/sync');
  if (r?.ok) { toast('Synced '+(r.synced||0)+' messages','success'); loadAccounts(); loadFolders(); loadMessages(); }
  else toast(r?.error||'Sync failed','error');
}

// ── Edit Account modal ─────────────────────────────────────────────────────
// Opened from the Settings modal's Accounts tab; stacks above it (see
// #edit-account-modal z-index in CSS) rather than closing Settings, so
// Cancel/Save land the user back in Settings automatically.
async function openEditAccount(id) {
  const r=await api('GET','/accounts/'+id);
  if (!r) return;
  document.getElementById('edit-account-id').value=id;
  document.getElementById('edit-account-email').textContent=r.email_address;
  document.getElementById('edit-name').value=r.display_name||'';

  const isOAuth = r.provider==='gmail' || r.provider==='outlook' || r.provider==='outlook_personal';

  // Show/hide credential section and test button based on provider type
  document.getElementById('edit-creds-section').style.display = isOAuth ? 'none' : '';
  document.getElementById('edit-test-btn').style.display = isOAuth ? 'none' : '';
  const oauthSection = document.getElementById('edit-oauth-section');
  if (oauthSection) oauthSection.style.display = isOAuth ? '' : 'none';
  if (isOAuth) {
    const providerLabel = r.provider==='gmail' ? 'Google' : r.provider==='outlook_personal' ? 'Microsoft (Personal)' : 'Microsoft';
    const lbl = document.getElementById('edit-oauth-provider-label');
    const lblBtn = document.getElementById('edit-oauth-provider-label-btn');
    const expWarn = document.getElementById('edit-oauth-expired-warning');
    if (lbl) lbl.textContent = providerLabel;
    if (lblBtn) lblBtn.textContent = providerLabel;
    if (expWarn) expWarn.style.display = r.token_expired ? '' : 'none';
    const reconnectBtn = document.getElementById('edit-oauth-reconnect-btn');
    if (reconnectBtn) reconnectBtn.onclick = () => {
      closeModal('edit-account-modal');
      connectOAuth(r.provider);
    };
  }

  const isJMAP = r.provider==='jmap';
  if (!isOAuth) {
    document.getElementById('edit-password').value='';
    document.getElementById('edit-imap-host').value=r.imap_host||'';
    document.getElementById('edit-imap-port').value=r.imap_port||993;
    document.getElementById('edit-smtp-host').value=r.smtp_host||'';
    document.getElementById('edit-smtp-port').value=r.smtp_port||587;
    document.getElementById('edit-imap-port-field').style.display=isJMAP?'none':'';
    document.getElementById('edit-smtp-fields').style.display=isJMAP?'none':'';
    document.getElementById('edit-imap-host-label').textContent=isJMAP?'JMAP Server URL':'IMAP Host';
  }

  document.getElementById('edit-caldav-url').value=r.caldav_url||'';
  document.getElementById('edit-carddav-url').value=r.carddav_url||'';

  document.getElementById('edit-sync-days').value=r.sync_days||30;
  const sel = document.getElementById('edit-sync-mode');
  if (r.sync_mode==='all' || !r.sync_days) {
    sel.value='all';
  } else {
    const presetMap={30:'preset-30',90:'preset-90',180:'preset-180',365:'preset-365',730:'preset-730',1825:'preset-1825'};
    sel.value = presetMap[r.sync_days] || 'days';
  }
  toggleSyncDaysField();

  const errEl=document.getElementById('edit-last-error'), connEl=document.getElementById('edit-conn-result');
  connEl.style.display='none';
  errEl.style.display=r.last_error?'block':'none';
  if (r.last_error) errEl.textContent='Last sync error: '+r.last_error;

  const hiddenEl = document.getElementById('edit-hidden-folders');
  const hidden = S.folders.filter(f=>f.account_id===id && f.is_hidden);
  if (!hidden.length) {
    hiddenEl.innerHTML='<span style="color:var(--muted);font-size:12px">No hidden folders.</span>';
  } else {
    hiddenEl.innerHTML = hidden.map(f=>`
      <div style="display:flex;align-items:center;justify-content:space-between;padding:5px 0;border-bottom:1px solid var(--border)">
        <span style="font-size:13px">${esc(f.name)}</span>
        <button class="btn-secondary" style="font-size:11px;padding:3px 10px" onclick="unhideFolder(${f.id})">Unhide</button>
      </div>`).join('');
  }

  openModal('edit-account-modal');
}

async function unhideFolder(folderId) {
  const f = S.folders.find(f=>f.id===folderId);
  if (!f) return;
  const r = await api('PUT','/folders/'+folderId+'/visibility',{is_hidden:false, sync_enabled:true});
  if (r?.ok) {
    toast('Folder restored to sidebar','success');
    await loadFolders();
    // Refresh hidden list in modal
    const accId = parseInt(document.getElementById('edit-account-id').value);
    if (accId) {
      const hiddenEl = document.getElementById('edit-hidden-folders');
      const hidden = S.folders.filter(f=>f.account_id===accId && f.is_hidden);
      if (!hidden.length) hiddenEl.innerHTML='<span style="color:var(--muted);font-size:12px">No hidden folders.</span>';
      else hiddenEl.innerHTML = hidden.map(f=>`
        <div style="display:flex;align-items:center;justify-content:space-between;padding:5px 0;border-bottom:1px solid var(--border)">
          <span style="font-size:13px">${esc(f.name)}</span>
          <button class="btn-secondary" style="font-size:11px;padding:3px 10px" onclick="unhideFolder(${f.id})">Unhide</button>
        </div>`).join('');
    }
  } else toast('Failed to unhide folder','error');
}

function toggleSyncDaysField() {
  const mode=document.getElementById('edit-sync-mode')?.value;
  const row=document.getElementById('edit-sync-days-row');
  if (row) row.style.display=(mode==='days')?'flex':'none';
}

async function testEditConnection() {
  // Only relevant for IMAP/SMTP accounts — OAuth accounts reconnect via the button
  if (document.getElementById('edit-creds-section').style.display === 'none') {
    return;
  }
  const btn=document.getElementById('edit-test-btn'), connEl=document.getElementById('edit-conn-result');
  const pw=document.getElementById('edit-password').value, email=document.getElementById('edit-account-email').textContent.trim();
  if (!pw){connEl.textContent='Enter new password to test.';connEl.className='test-result err';connEl.style.display='block';return;}
  const isJMAP=document.getElementById('edit-imap-port-field').style.display==='none';
  const testBody={email,password:pw,imap_host:document.getElementById('edit-imap-host').value.trim()};
  if (isJMAP) { testBody.provider='jmap'; }
  else {
    testBody.imap_port=parseInt(document.getElementById('edit-imap-port').value)||993;
    testBody.smtp_host=document.getElementById('edit-smtp-host').value.trim();
    testBody.smtp_port=parseInt(document.getElementById('edit-smtp-port').value)||587;
  }
  btn.innerHTML='<span class="spinner-inline"></span>Testing...';btn.disabled=true;
  const r=await api('POST','/accounts/test',testBody,20000);
  btn.textContent='Test Connection';btn.disabled=false;
  connEl.textContent=(r?.ok)?'✓ Successful!':((r?.error?.message||r?.error)||'Failed');
  connEl.className='test-result '+((r?.ok)?'ok':'err'); connEl.style.display='block';
}

async function saveAccountEdit() {
  const id=document.getElementById('edit-account-id').value;
  const isOAuth = document.getElementById('edit-creds-section').style.display === 'none';
  const body={display_name:document.getElementById('edit-name').value.trim()};
  if (!isOAuth) {
    body.imap_host=document.getElementById('edit-imap-host').value.trim();
    body.imap_port=parseInt(document.getElementById('edit-imap-port').value)||993;
    body.smtp_host=document.getElementById('edit-smtp-host').value.trim();
    body.smtp_port=parseInt(document.getElementById('edit-smtp-port').value)||587;
    const pw=document.getElementById('edit-password').value;
    if (pw) body.password=pw;
  }
  body.caldav_url=document.getElementById('edit-caldav-url').value.trim();
  body.carddav_url=document.getElementById('edit-carddav-url').value.trim();
  const modeVal = document.getElementById('edit-sync-mode').value;
  let syncMode='all', syncDays=0;
  if (modeVal==='days') {
    syncMode='days'; syncDays=parseInt(document.getElementById('edit-sync-days').value)||30;
  } else if (modeVal.startsWith('preset-')) {
    syncMode='days'; syncDays=parseInt(modeVal.replace('preset-',''));
  } // else 'all': syncMode='all', syncDays=0
  const [r1, r2] = await Promise.all([
    api('PUT','/accounts/'+id, body),
    api('PUT','/accounts/'+id+'/sync-settings',{
      sync_mode: syncMode,
      sync_days: syncDays,
    }),
  ]);
  if (r1?.ok){toast('Account updated','success');closeModal('edit-account-modal');loadAccounts();}
  else toast(r1?.error||'Update failed','error');
}

async function deleteAccount(id) {
  const a=S.accounts.find(a=>a.id===id);
  inlineConfirm(
    'Remove '+(a?a.email_address:'this account')+'? All synced messages will be deleted.',
    async () => {
      const r=await api('DELETE','/accounts/'+id);
      if (r?.ok){toast('Account removed','success');loadAccounts();loadFolders();loadMessages();}
      else toast('Remove failed','error');
    }
  );
}

// ── Inline confirm (replaces browser confirm()) ────────────────────────────
function inlineConfirm(message, onOk, onCancel) {
  const el   = document.getElementById('inline-confirm');
  const msg  = document.getElementById('inline-confirm-msg');
  const ok   = document.getElementById('inline-confirm-ok');
  const cancel = document.getElementById('inline-confirm-cancel');
  msg.textContent = message;
  el.classList.add('open');
  const cleanup = () => { el.classList.remove('open'); ok.onclick=null; cancel.onclick=null; };
  ok.onclick     = () => { cleanup(); onOk && onOk(); };
  cancel.onclick = () => { cleanup(); onCancel && onCancel(); };
}

// ── Undo toast: run onCommit after `duration` unless the user clicks Undo first ──
function undoToast(msg, {onUndo, onCommit, duration=5000}) {
  let container = document.getElementById('toast-container');
  if (!container) {
    container = document.createElement('div');
    container.id = 'toast-container';
    container.className = 'toast-container';
    document.body.appendChild(container);
  }
  const el = document.createElement('div');
  el.className = 'toast toast-undo';
  const span = document.createElement('span');
  span.textContent = msg;
  const btn = document.createElement('button');
  btn.type = 'button'; btn.className = 'toast-undo-btn'; btn.textContent = 'Undo';
  el.appendChild(span); el.appendChild(btn);
  container.appendChild(el);
  let done = false;
  const dismiss = () => { el.style.opacity='0'; el.style.transition='opacity .3s'; setTimeout(()=>el.remove(),300); };
  const timer = setTimeout(() => { if(done) return; done=true; dismiss(); onCommit && onCommit(); }, duration);
  btn.onclick = () => { if(done) return; done=true; clearTimeout(timer); dismiss(); onUndo && onUndo(); };
}

// ── Inline prompt (replaces browser prompt()) ──────────────────────────────
function inlinePrompt(message, onOk, defaultValue) {
  const el     = document.getElementById('inline-prompt');
  const msg    = document.getElementById('inline-prompt-msg');
  const input  = document.getElementById('inline-prompt-input');
  const ok     = document.getElementById('inline-prompt-ok');
  const cancel = document.getElementById('inline-prompt-cancel');
  msg.textContent = message;
  input.value = defaultValue || '';
  el.classList.add('open');
  setTimeout(() => { input.focus(); input.select(); }, 50);
  const cleanup = () => { el.classList.remove('open'); ok.onclick=null; cancel.onclick=null; };
  ok.onclick     = () => { const v=input.value.trim(); if(!v) return; cleanup(); onOk && onOk(v); };
  cancel.onclick = () => { cleanup(); };
}

// ── Inline date/time prompt (snooze / send later) ──────────────────────────
function inlineDateTimePrompt(message, onOk, okLabel) {
  const el       = document.getElementById('inline-datetime');
  const msg      = document.getElementById('inline-datetime-msg');
  const input    = document.getElementById('inline-datetime-input');
  const presetsEl= document.getElementById('inline-datetime-presets');
  const ok       = document.getElementById('inline-datetime-ok');
  const cancel   = document.getElementById('inline-datetime-cancel');
  msg.textContent = message;
  ok.textContent = okLabel || 'Set';

  const pad = n => String(n).padStart(2,'0');
  const toLocalInput = d => `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  const now = new Date();
  input.min = toLocalInput(new Date(now.getTime()+60000));

  const presets = [
    ['Later today', () => { const d=new Date(); d.setHours(18,0,0,0); if(d<=now) d.setDate(d.getDate()+1); return d; }],
    ['Tomorrow 9am', () => { const d=new Date(); d.setDate(d.getDate()+1); d.setHours(9,0,0,0); return d; }],
    ['Next week', () => { const d=new Date(); d.setDate(d.getDate()+(((8-d.getDay())%7)||7)); d.setHours(9,0,0,0); return d; }],
  ];
  presetsEl.innerHTML = presets.map(([label],i)=>`<button type="button" class="datetime-preset-btn" data-i="${i}">${label}</button>`).join('');
  presetsEl.querySelectorAll('.datetime-preset-btn').forEach((btn,i)=>{
    btn.onclick = () => { input.value = toLocalInput(presets[i][1]()); };
  });
  input.value = toLocalInput(presets[1][1]());

  el.classList.add('open');
  setTimeout(() => input.focus(), 50);
  const cleanup = () => { el.classList.remove('open'); ok.onclick=null; cancel.onclick=null; };
  ok.onclick = () => {
    if (!input.value) return;
    const d = new Date(input.value);
    if (isNaN(d.getTime()) || d <= new Date()) { toast('Pick a time in the future','error'); return; }
    cleanup();
    onOk(d.toISOString());
  };
  cancel.onclick = () => cleanup();
}

// ── Folders ────────────────────────────────────────────────────────────────
async function loadFolders() {
  const data=await api('GET','/folders');
  if (!data) return;
  S.folders=data||[];
  renderFolders();
  updateUnreadBadge();
}

const FOLDER_ICONS = {
  inbox:'<path d="M20 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z"/>',
  sent:'<path d="M2.01 21L23 12 2.01 3 2 10l15 2-15 2z"/>',
  drafts:'<path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04c.39-.39.39-1.02 0-1.41l-2.34-2.34c-.39-.39-1.02-.39-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z"/>',
  trash:'<path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/>',
  spam:'<path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 15h-2v-2h2v2zm0-4h-2V7h2v6z"/>',
  archive:'<path d="M20.54 5.23l-1.39-1.68C18.88 3.21 18.47 3 18 3H6c-.47 0-.88.21-1.16.55L3.46 5.23C3.17 5.57 3 6.02 3 6.5V19c0 1.1.9 2 2 2h14c1.1 0 2-.9 2-2V6.5c0-.48-.17-.93-.46-1.27zM12 17.5L6.5 12H10v-2h4v2h3.5L12 17.5zM5.12 5l.81-1h12l.94 1H5.12z"/>',
  custom:'<path d="M20 6h-2.18c.07-.44.18-.86.18-1 0-2.21-1.79-4-4-4s-4 1.79-4 4c0 .14.11.56.18 1H8c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2z"/>',
};

// Favourites: the virtual views (Unified Inbox / Snoozed / Scheduled) are pinned at the
// top; uiPrefs.favourites (ordered folder ids) adds user-picked folders below them.
// Drag user rows to reorder; same-name folders across accounts get " - <mailbox>" + dot.
function renderFavourites() {
  const el = document.getElementById('favourites-list');
  if (!el) return;
  const accMap = {}; S.accounts.forEach(a => accMap[a.id] = a);
  const pinned = [
    { id:'nav-unified',  icon:FOLDER_ICONS.inbox, label:'Unified Inbox', act:`selectFolder('unified','Unified Inbox')`,
      badge:'<span class="unread-badge" id="unread-total" style="display:none"></span>' },
    { id:'nav-snoozed',  icon:'<path d="M12 20c4.42 0 8-3.58 8-8s-3.58-8-8-8-8 3.58-8 8 3.58 8 8 8zm.5-13H11v6l5.25 3.15.75-1.23-4.5-2.67V7z"/>',
      label:'Snoozed', act:`selectFolder('snoozed','Snoozed')` },
    { id:'nav-scheduled', icon:'<path d="M20 3h-1V1h-2v2H7V1H5v2H4c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V5c0-1.1-.9-2-2-2zm0 18H4V8h16v13zm-8-9h5v5h-5z"/>',
      label:'Scheduled', act:'showScheduledSends()' },
  ];
  const visible = S.folders.filter(f=>!f.is_hidden);
  const favs = (uiPrefsGet('favourites', [])||[])
    .map(id => visible.find(f=>f.id===id)).filter(Boolean);
  const userRows = favs.map(f => {
    const acc = accMap[f.account_id];
    const dup = visible.some(x => x.id!==f.id && x.name.toLowerCase()===f.name.toLowerCase());
    const label = dup
      ? `${esc(f.name)} - <span style="color:${acc?.color||'var(--muted)'}">●</span> ${esc(acc?.display_name||acc?.email_address||'')}`
      : esc(f.name);
    return `
      <div class="nav-item fav-drag" id="fav-f${f.id}" data-fid="${f.id}" draggable="true"
           onclick="selectFolder(${f.id},'${esc(f.name)}')"
           ondragstart="favDragStart(event,${f.id})" ondragend="favDragEnd(event)">
        <svg viewBox="0 0 24 24" fill="currentColor">${FOLDER_ICONS[f.folder_type]||FOLDER_ICONS.custom}</svg>
        ${label}
        ${f.folder_type==='inbox'&&f.unread_count>0?`<span class="unread-badge">${f.unread_count}</span>`:''}
      </div>`;
  }).join('');
  el.innerHTML = pinned.map(p => `
      <div class="nav-item" id="${p.id}" onclick="${p.act}">
        <svg viewBox="0 0 24 24" fill="currentColor">${p.icon}</svg>
        ${p.label}${p.badge||''}
      </div>`).join('') + userRows;
  updateUnreadBadge(); // the #unread-total span was rebuilt above
}

let _favDragId = null;
function favDragStart(e, id) {
  _favDragId = id;
  e.dataTransfer.effectAllowed = 'move';
  e.dataTransfer.setData('text/plain', String(id));
}
// Live reorder: insert the dragged row before the first user row whose midpoint is
// below the cursor (append at the end if none). Pinned rows aren't .fav-drag, so they stay on top.
function favDragOver(e) {
  if (_favDragId == null) return;
  e.preventDefault();
  const list = document.getElementById('favourites-list');
  const dragging = list?.querySelector(`.fav-drag[data-fid="${_favDragId}"]`);
  if (!dragging) return;
  const after = [...list.querySelectorAll('.fav-drag')]
    .find(el => el !== dragging && e.clientY < el.getBoundingClientRect().top + el.getBoundingClientRect().height/2);
  if (after) list.insertBefore(dragging, after); else list.appendChild(dragging);
}
function favDragEnd() {
  if (_favDragId == null) return;
  _favDragId = null;
  const list = document.getElementById('favourites-list');
  const ids = [...(list?.querySelectorAll('.fav-drag')||[])].map(el => parseInt(el.dataset.fid));
  uiPrefsSet('favourites', ids);
  renderFavourites();
}

function toggleFavourite(id) {
  const favs = uiPrefsGet('favourites', []) || [];
  const i = favs.indexOf(id);
  if (i >= 0) favs.splice(i, 1); else favs.push(id);
  uiPrefsSet('favourites', favs);
  renderFavourites();
}

function renderFolders() {
  renderFavourites();
  const el = document.getElementById('folders-by-account');
  const accMap = {}; S.accounts.forEach(a => accMap[a.id] = a);
  const byAcc = {};
  S.folders.filter(f => !f.is_hidden).forEach(f => {
    (byAcc[f.account_id] = byAcc[f.account_id] || []).push(f);
  });
  const prio = ['inbox','sent','drafts','trash','spam','archive'];
  const orderedAccounts = [...S.accounts].sort((a,b) => (a.sort_order||0) - (b.sort_order||0));

  el.innerHTML = orderedAccounts.map(acc => {
    const folders = byAcc[acc.id];
    // Show account even if no folders yet — it was just added and syncer hasn't run
    if (!folders?.length) {
      const statusHtml = acc.last_error
        ? `<div style="padding:6px 10px 8px;font-size:11px;color:var(--danger);background:rgba(239,68,68,.08);border-radius:0 0 6px 6px;line-height:1.4">
             ⚠ ${esc(acc.last_error)}
           </div>`
        : `<div style="padding:6px 12px 8px;font-size:11px;color:var(--muted)">⏳ Syncing folders…</div>`;
      return `<div class="nav-account-group" data-acc-id="${acc.id}">
        <div class="nav-folder-header" style="cursor:default">
          <span class="acc-drag-handle">&#8942;</span>
          <span style="width:7px;height:7px;border-radius:50%;background:${acc.color};display:inline-block;flex-shrink:0"></span>
          <span style="flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"
                title="${esc(acc.email_address)}">${esc(acc.display_name||acc.email_address)}</span>
          <button class="icon-sync-btn" title="Retry sync" onclick="syncNow(${acc.id},event)" style="flex-shrink:0">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor"><path d="M12 4V1L8 5l4 4V6c3.31 0 6 2.69 6 6 0 1.01-.25 1.97-.7 2.8l1.46 1.46C19.54 15.03 20 13.57 20 12c0-4.42-3.58-8-8-8zm0 14c-3.31 0-6-2.69-6-6 0-1.01.25-1.97.7-2.8L5.24 7.74C4.46 8.97 4 10.43 4 12c0 4.42 3.58 8 8 8v3l4-4-4-4v3z"/></svg>
          </button>
        </div>
        ${statusHtml}
      </div>`;
    }
    const accId = acc.id;
    const collapsed = isAccountCollapsed(accId);
    const sorted = [
      ...prio.map(t => folders.find(f => f.folder_type===t)).filter(Boolean),
      ...folders.filter(f => f.folder_type==='custom')
    ];
    const totalUnread = folders.reduce((s,f) => s+(f.unread_count||0), 0);
    const chevron = collapsed
      ? '<svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z"/></svg>'
      : '<svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor"><path d="M7 10l5 5 5-5z"/></svg>';

    const folderRows = collapsed ? '' : sorted.map(f => `
      <div class="nav-item${f.sync_enabled?'':' folder-nosync'}" id="nav-f${f.id}"
           data-fid="${f.id}" onclick="selectFolder(${f.id},'${esc(f.name)}')"
           oncontextmenu="showFolderMenu(event,${f.id})">
        <svg viewBox="0 0 24 24" fill="currentColor">${FOLDER_ICONS[f.folder_type]||FOLDER_ICONS.custom}</svg>
        ${esc(f.name)}
        <span class="folder-count-group">
          ${f.unread_count>0?`<span class="unread-badge">${f.unread_count}</span>`:''}
          ${f.total_count>0?`<span class="folder-total-count">${f.unread_count>0?'/':''}${f.total_count}</span>`:''}
        </span>
        ${!f.sync_enabled?'<span style="font-size:9px;color:var(--muted)" title="Sync disabled">\u29b8</span>':''}
      </div>`).join('');

    return `<div class="nav-account-group" data-acc-id="${accId}"
                 draggable="true"
                 ondragstart="accDragStart(event,${accId})"
                 ondragover="accDragOver(event)"
                 ondragleave="accDragLeave(event)"
                 ondrop="accDrop(event,${accId})">
      <div class="nav-folder-header" onclick="toggleAccountCollapse(${accId})"
           oncontextmenu="showAccountMenu(event,${accId})">
        <span class="acc-drag-handle" title="Drag to reorder" onclick="event.stopPropagation()">&#8942;</span>
        <span style="width:7px;height:7px;border-radius:50%;background:${acc.color};display:inline-block;flex-shrink:0"></span>
        <span style="flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"
              title="${esc(acc.email_address)}">${esc(acc.display_name||acc.email_address)}</span>
        ${totalUnread>0&&collapsed?`<span class="unread-badge" style="margin-left:auto">${totalUnread}</span>`:''}
        <button class="icon-sync-btn" title="Sync account" onclick="syncNow(${accId},event)" style="flex-shrink:0">
          <svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor"><path d="M12 4V1L8 5l4 4V6c3.31 0 6 2.69 6 6 0 1.01-.25 1.97-.7 2.8l1.46 1.46C19.54 15.03 20 13.57 20 12c0-4.42-3.58-8-8-8zm0 14c-3.31 0-6-2.69-6-6 0-1.01.25-1.97.7-2.8L5.24 7.74C4.46 8.97 4 10.43 4 12c0 4.42 3.58 8 8 8v3l4-4-4-4v3z"/></svg>
        </button>
        <span class="acc-chevron">${chevron}</span>
      </div>
      ${folderRows}
    </div>`;
  }).join('');

  // Re-wire drag-drop onto folder rows for message-to-folder moves
  el.querySelectorAll('.nav-item[data-fid]').forEach(item => {
    item.ondragover = e => { e.preventDefault(); item.classList.add('drag-over'); };
    item.ondragleave = () => item.classList.remove('drag-over');
    item.ondrop = e => {
      e.preventDefault(); item.classList.remove('drag-over');
      const fid = parseInt(item.dataset.fid);
      const mid = parseInt(e.dataTransfer.getData('text/plain'));
      if (mid && fid) moveMessage(mid, fid);
    };
  });
}

function toggleAccountCollapse(accId) {
  setAccountCollapsed(accId, !isAccountCollapsed(accId));
  renderFolders();
}

// ── Account drag-to-reorder ─────────────────────────────────────────────────
let _dragSrcAccId = null;

function accDragStart(e, accId) {
  _dragSrcAccId = accId;
  e.dataTransfer.effectAllowed = 'move';
  e.dataTransfer.setData('text/plain', String(accId));
  setTimeout(() => e.currentTarget?.classList.add('acc-dragging'), 0);
}

function accDragOver(e) {
  e.preventDefault();
  e.dataTransfer.dropEffect = 'move';
  const g = e.currentTarget;
  if (g && parseInt(g.dataset.accId) !== _dragSrcAccId) g.classList.add('acc-drag-target');
}

function accDragLeave(e) { e.currentTarget?.classList.remove('acc-drag-target'); }

async function accDrop(e, targetAccId) {
  e.preventDefault();
  e.currentTarget?.classList.remove('acc-drag-target');
  document.querySelectorAll('.acc-dragging').forEach(el => el.classList.remove('acc-dragging'));
  if (_dragSrcAccId === null || _dragSrcAccId === targetAccId) { _dragSrcAccId = null; return; }

  const ordered = [...S.accounts].sort((a,b) => (a.sort_order||0)-(b.sort_order||0));
  const srcIdx = ordered.findIndex(a => a.id === _dragSrcAccId);
  const dstIdx = ordered.findIndex(a => a.id === targetAccId);
  if (srcIdx === -1 || dstIdx === -1) { _dragSrcAccId = null; return; }

  const [moved] = ordered.splice(srcIdx, 1);
  ordered.splice(dstIdx, 0, moved);
  ordered.forEach((a, i) => { a.sort_order = i; });
  S.accounts = ordered;
  _dragSrcAccId = null;

  renderFolders();
  await api('PUT', '/accounts/sort-order', { order: ordered.map(a => a.id) });
}

function showFolderMenu(e, folderId) {
  e.preventDefault(); e.stopPropagation();
  const f = S.folders.find(f=>f.id===folderId);
  if (!f) return;
  const syncLabel = f.sync_enabled ? '⊘ Disable sync' : '↻ Enable sync';
  const otherFolders = S.folders.filter(x=>x.id!==folderId&&x.account_id===f.account_id&&!x.is_hidden).slice(0,16);
  const moveItems = otherFolders.map(x=>
    `<div class="ctx-item ctx-sub-item" onclick="moveFolderContents(${folderId},${x.id});closeMenu()">${esc(x.name)}</div>`
  ).join('');
  const moveEntry = otherFolders.length ? `
    <div class="ctx-item ctx-has-sub">📂 Move messages to
      <span class="ctx-sub-arrow">›</span>
      <div class="ctx-submenu">${moveItems}</div>
    </div>` : '';
  const isTrashOrSpam = f.folder_type==='trash' || f.folder_type==='spam';
  const emptyEntry = isTrashOrSpam
    ? `<div class="ctx-item danger" onclick="confirmEmptyFolder(${folderId});closeMenu()">🗑 Empty ${f.name}</div>` : '';
  const disabledCount = S.folders.filter(x=>x.account_id===f.account_id&&!x.sync_enabled).length;
  const enableAllEntry = disabledCount > 0
    ? `<div class="ctx-item" onclick="enableAllFolderSync(${f.account_id});closeMenu()">↻ Enable sync for all folders (${disabledCount})</div>` : '';
  // Default folders (inbox/sent/drafts/trash/spam) are provider-managed — deleting them
  // would break sync. Only user-created "custom" folders can be deleted.
  const deleteEntry = f.folder_type==='custom'
    ? `<div class="ctx-item danger" onclick="confirmDeleteFolder(${folderId});closeMenu()">🗑 Delete folder</div>` : '';
  const isFav = (uiPrefsGet('favourites', [])||[]).includes(folderId);
  const favEntry = `<div class="ctx-item" onclick="toggleFavourite(${folderId});closeMenu()">${isFav?'★ Remove from favourites':'☆ Add to favourites'}</div>`;
  showCtxMenu(e, `
    ${favEntry}
    <div class="ctx-item" onclick="createFolderPrompt(${f.account_id});closeMenu()">📁 New folder</div>
    <div class="ctx-item" onclick="syncFolderNow(${folderId});closeMenu()">↻ Sync this folder</div>
    <div class="ctx-item" onclick="toggleFolderSync(${folderId});closeMenu()">${syncLabel}</div>
    ${enableAllEntry}
    <div class="ctx-item" onclick="markFolderAllRead(${folderId});closeMenu()">✓ Mark all as read</div>
    <div class="ctx-item ctx-has-sub">⬇ Export folder
      <span class="ctx-sub-arrow">›</span>
      <div class="ctx-submenu">
        <div class="ctx-item ctx-sub-item" onclick="exportFolder(${folderId},'zip');closeMenu()">as .zip (.eml files)</div>
        <div class="ctx-item ctx-sub-item" onclick="exportFolder(${folderId},'mbox');closeMenu()">as .mbox</div>
      </div>
    </div>
    <div class="ctx-sep"></div>
    ${moveEntry}
    ${emptyEntry}
    <div class="ctx-item" onclick="confirmHideFolder(${folderId});closeMenu()">👁 Hide from sidebar</div>
    ${deleteEntry}`);
}

function showAccountMenu(e, accountId) {
  e.preventDefault(); e.stopPropagation();
  showCtxMenu(e, `
    <div class="ctx-item" onclick="createFolderPrompt(${accountId});closeMenu()">📁 New folder</div>`);
}

function createFolderPrompt(accountId) {
  inlinePrompt('New folder name:', async (name) => {
    const r = await api('POST', '/accounts/'+accountId+'/folders', { name });
    if (r?.ok) {
      S.folders.push(r.folder);
      toast('Folder created', 'success');
      renderFolders();
    } else toast(r?.error || 'Create failed', 'error');
  });
}

async function syncFolderNow(folderId) {
  toast('Syncing folder…','info');
  const r=await api('POST','/folders/'+folderId+'/sync');
  if (r?.ok) { toast('Synced '+(r.synced||0)+' messages','success'); loadFolders(); loadMessages(); }
  else toast(r?.error||'Sync failed','error');
}

function exportFolder(folderId, format) {
  window.open('/api/folders/'+folderId+'/export?format='+format, '_blank');
}

async function markFolderAllRead(folderId) {
  const r=await api('POST','/folders/'+folderId+'/mark-all-read');
  if(r?.ok){
    toast(`Marked ${r.marked||0} message(s) as read`,'success');
    loadFolders();
    loadMessages();
  } else toast(r?.error||'Failed','error');
}

async function toggleFolderSync(folderId) {
  const f = S.folders.find(f=>f.id===folderId);
  if (!f) return;
  const newSync = !f.sync_enabled;
  const r = await api('PUT','/folders/'+folderId+'/visibility',{is_hidden:f.is_hidden, sync_enabled:newSync});
  if (r?.ok) {
    f.sync_enabled = newSync;
    toast(newSync?'Folder sync enabled':'Folder sync disabled', 'success');
    renderFolders();
  } else toast('Update failed','error');
}

async function enableAllFolderSync(accountId) {
  const r = await api('POST','/accounts/'+accountId+'/enable-all-sync');
  if (r?.ok) {
    // Update local state
    S.folders.forEach(f=>{ if(f.account_id===accountId) f.sync_enabled=true; });
    toast(`Sync enabled for ${r.enabled||0} folder${r.enabled===1?'':'s'}`, 'success');
    renderFolders();
  } else toast('Failed to enable sync', 'error');
}

async function confirmEmptyFolder(folderId) {
  const f = S.folders.find(f=>f.id===folderId);
  if (!f) return;
  const label = f.folder_type==='trash' ? 'Trash' : 'Spam';
  inlineConfirm(
    `Permanently delete all messages in ${label}? This cannot be undone.`,
    async () => {
      const r = await api('POST','/folders/'+folderId+'/empty');
      if (r?.ok) {
        toast(`Emptied ${label} (${r.deleted||0} messages)`, 'success');
        // Remove locally
        S.messages = S.messages.filter(m=>m.folder_id!==folderId);
        if (S.currentMessage && S.currentFolder===folderId) resetDetail();
        await loadFolders();
        if (S.currentFolder===folderId) renderMessageList();
      } else toast('Failed to empty folder','error');
    }
  );
}

async function confirmHideFolder(folderId) {
  const f = S.folders.find(f=>f.id===folderId);
  if (!f) return;
  inlineConfirm(
    `Hide "${f.name}" from sidebar? You can unhide it from account settings.`,
    async () => {
      const r = await api('PUT','/folders/'+folderId+'/visibility',{is_hidden:true, sync_enabled:false});
      if (r?.ok) { toast('Folder hidden','success'); await loadFolders(); }
      else toast('Update failed','error');
    }
  );
}

async function confirmDeleteFolder(folderId) {
  const f = S.folders.find(f=>f.id===folderId);
  if (!f) return;
  const countRes = await api('GET','/folders/'+folderId+'/count');
  const count = countRes?.count ?? '?';
  inlineConfirm(
    `Delete folder "${f.name}"? This will permanently delete all ${count} message${count===1?'':'s'} inside it. This cannot be undone.`,
    async () => {
      const r = await api('DELETE','/folders/'+folderId);
      if (r?.ok) {
        toast('Folder deleted','success');
        S.folders = S.folders.filter(x=>x.id!==folderId);
        if (S.currentFolder===folderId) selectFolder('unified','Unified Inbox');
        renderFolders(); loadMessages();
      } else toast(r?.error||'Delete failed','error');
    }
  );
}

async function moveFolderContents(fromId, toId) {
  const from = S.folders.find(f=>f.id===fromId);
  const to   = S.folders.find(f=>f.id===toId);
  if (!from||!to) return;
  inlineConfirm(
    `Move all messages from "${from.name}" into "${to.name}"?`,
    async () => {
      const r = await api('POST','/folders/'+fromId+'/move-to/'+toId);
      if (r?.ok) { toast(`Moved ${r.moved||0} messages`,'success'); loadFolders(); loadMessages(); }
      else toast(r?.error||'Move failed','error');
    }
  );
}

function updateUnreadBadge() {
  const total=S.folders.filter(f=>f.folder_type==='inbox').reduce((s,f)=>s+(f.unread_count||0),0);
  const badge=document.getElementById('unread-total');
  badge.textContent=total; badge.style.display=total>0?'':'none';
  const bell=document.getElementById('bell-badge');
  if(bell){ bell.textContent=total>99?'99+':total; bell.style.display=total>0?'':'none'; }
}

// ── Labels ─────────────────────────────────────────────────────────────────
// Local to gowebmail only — not synced to Gmail/Outlook/IMAP (see server-side comment on
// models.Label for why a reliable cross-provider equivalent doesn't exist).
const LABEL_PALETTE = ['#e5484d','#f5a623','#f7ce46','#3dd68c','#12b886','#2bb3d6',
  '#5b8def','#7c5cfc','#c25ee0','#ec4899','#8a8f98','#64748b'];

async function loadLabels() {
  const data = await api('GET','/labels');
  S.labels = data || [];
  renderLabelsDropdown();
}

// Renders the Labels dropdown (panel-header, next to Filter): click a label to view its
// messages, or use the inline ✎/🗑 to rename/recolor or delete right from the list.
function renderLabelsDropdown() {
  const el = document.getElementById('labels-dropdown-menu');
  if (!el) return;
  const rows = S.labels.map(l => `
    <div class="label-dropdown-row">
      <span class="nav-label-dot" style="background:${l.color};cursor:pointer" onclick="selectLabel(${l.id},'${esc(l.name)}')"></span>
      <span class="label-dropdown-name" onclick="selectLabel(${l.id},'${esc(l.name)}')">${esc(l.name)}</span>
      <span class="label-dropdown-actions">
        <button onclick="event.stopPropagation();closeLabelsDropdown();openLabelEditor(${l.id})" title="Rename / recolor">✎</button>
        <button onclick="event.stopPropagation();closeLabelsDropdown();deleteLabelConfirm(${l.id})" title="Delete">🗑</button>
      </span>
    </div>`).join('');
  el.innerHTML = rows + `
    <div class="filter-sep-line"></div>
    <div class="label-dropdown-new" onclick="closeLabelsDropdown();openLabelEditor()">+ New label</div>`;
}

function toggleLabelsDropdown(e) {
  e.stopPropagation();
  const menu = document.getElementById('labels-dropdown-menu');
  if (!menu) return;
  const isOpen = menu.style.display !== 'none';
  menu.style.display = isOpen ? 'none' : 'block';
  document.getElementById('labels-dropdown-btn')?.setAttribute('aria-expanded', String(!isOpen));
  if (!isOpen) setTimeout(() => document.addEventListener('click', closeLabelsDropdown, { once: true }), 0);
}
function closeLabelsDropdown() {
  const menu = document.getElementById('labels-dropdown-menu');
  if (menu) menu.style.display = 'none';
  document.getElementById('labels-dropdown-btn')?.setAttribute('aria-expanded', 'false');
}

function toggleThreadPanel(e) {
  if (e) e.stopPropagation();
  const panel = document.getElementById('thread-panel');
  if (!panel) return;
  const isOpen = panel.style.display !== 'none';
  panel.style.display = isOpen ? 'none' : 'block';
  if (!isOpen) setTimeout(() => document.addEventListener('click', closeThreadPanel, { once: true }), 0);
}
function closeThreadPanel() {
  const panel = document.getElementById('thread-panel');
  if (panel) panel.style.display = 'none';
}

function selectLabel(labelId, name) {
  closeLabelsDropdown();
  selectFolder('label:'+labelId, name);
}

function openLabelEditor(labelId) {
  const label = labelId ? S.labels.find(l=>l.id===labelId) : null;
  document.getElementById('label-editor-title').textContent = label ? 'Edit Label' : 'New Label';
  document.getElementById('label-editor-id').value = labelId || '';
  document.getElementById('label-editor-name').value = label ? label.name : '';
  const chosen = label ? label.color : LABEL_PALETTE[0];
  document.getElementById('label-editor-swatches').innerHTML = LABEL_PALETTE.map(c =>
    `<span class="label-swatch${c===chosen?' selected':''}" style="background:${c}" data-color="${c}" onclick="pickLabelColor('${c}')"></span>`
  ).join('');
  document.getElementById('label-editor-custom-color').value = chosen;
  openModal('label-editor-modal');
  setTimeout(()=>document.getElementById('label-editor-name').focus(), 50);
}

function pickLabelColor(color) {
  document.getElementById('label-editor-custom-color').value = color;
  document.querySelectorAll('#label-editor-swatches .label-swatch').forEach(s =>
    s.classList.toggle('selected', s.dataset.color.toLowerCase() === color.toLowerCase()));
}

async function saveLabelEditor() {
  const id = document.getElementById('label-editor-id').value;
  const name = document.getElementById('label-editor-name').value.trim();
  const color = document.getElementById('label-editor-custom-color').value;
  if (!name) { toast('Label name required','error'); return; }
  const r = id
    ? await api('PUT','/labels/'+id,{name,color})
    : await api('POST','/labels',{name,color});
  if (r?.ok || r?.id) {
    closeModal('label-editor-modal');
    toast(id?'Label updated':'Label created','success');
    await loadLabels();
    await loadMessages(); // refresh label chips/dots on visible messages
  } else toast(r?.error || 'Failed to save label','error');
}

function deleteLabelConfirm(labelId) {
  const label = S.labels.find(l=>l.id===labelId);
  inlineConfirm(`Delete label "${label?.name||''}"? It will be removed from all messages.`, async () => {
    const r = await api('DELETE','/labels/'+labelId);
    if (r?.ok) {
      toast('Label deleted','success');
      if (S.currentFolder === 'label:'+labelId) selectFolder('unified','Unified Inbox');
      await loadLabels();
      await loadMessages();
    } else toast('Delete failed','error');
  });
}

// Assigns/unassigns a label on a message and updates local state (list row + open detail)
// without a full reload.
async function toggleMessageLabel(msgId, labelId) {
  const msg = S.messages.find(m=>m.id===msgId) || (S.currentMessage?.id===msgId ? S.currentMessage : null);
  const has = msg?.labels?.some(l=>l.id===labelId);
  const r = has
    ? await api('DELETE','/messages/'+msgId+'/labels/'+labelId)
    : await api('POST','/messages/'+msgId+'/labels/'+labelId);
  if (!r?.ok) { toast('Failed to update label','error'); return; }
  const label = S.labels.find(l=>l.id===labelId);
  [S.messages.find(m=>m.id===msgId), S.currentMessage?.id===msgId?S.currentMessage:null].forEach(m => {
    if (!m) return;
    m.labels = m.labels || [];
    if (has) m.labels = m.labels.filter(l=>l.id!==labelId);
    else if (label) m.labels.push(label);
  });
  renderMessageList();
  if (S.currentMessage?.id===msgId) renderMessageDetail(S.currentMessage,false);
}

function toggleLabelPicker(e, msgId) {
  e.stopPropagation();
  const msg = S.currentMessage?.id===msgId ? S.currentMessage : S.messages.find(m=>m.id===msgId);
  const items = S.labels.map(l => {
    const on = msg?.labels?.some(x=>x.id===l.id);
    return `<div class="ctx-item label-picker-item" onclick="toggleMessageLabel(${msgId},${l.id});closeMenu()">
      <span class="nav-label-dot" style="background:${l.color}"></span>${on?'✓ ':'○ '}${esc(l.name)}
    </div>`;
  }).join('') || '<div class="ctx-item" style="color:var(--muted)">No labels yet</div>';
  showCtxMenu(e, items);
}

// ── Messages ───────────────────────────────────────────────────────────────
function selectFolder(folderId, folderName) {
  S.currentFolder=folderId; S.currentFolderName=folderName||S.currentFolderName;
  S.currentPage=1; S.messages=[]; S.searchQuery='';
  document.getElementById('search-input').value='';
  if (hasActiveSearchFilters()) clearSearchFiltersQuiet();
  const sfp=document.getElementById('search-filters-panel'); if (sfp) sfp.style.display='none';
  document.getElementById('panel-title').textContent=folderName||S.currentFolderName;
  document.querySelectorAll('.nav-item').forEach(n=>n.classList.remove('active'));
  const navEl=folderId==='unified'?document.getElementById('nav-unified')
    :folderId==='starred'?document.getElementById('nav-starred')
    :folderId==='snoozed'?document.getElementById('nav-snoozed')
    :document.getElementById('nav-f'+folderId);
  if (navEl) navEl.classList.add('active');
  document.getElementById('fav-f'+folderId)?.classList.add('active');
  mobCloseNav();
  mobSetView('list');
  updateRibbonState();
  loadMessages();
}

const handleSearch=debounce(q=>{
  S.searchQuery=q.trim(); S.currentPage=1;
  document.getElementById('panel-title').textContent=q.trim()?'Search: '+q.trim():S.currentFolderName;
  loadMessages();
},350);

// ── Advanced search filters (scope, attachment, date range, size) ───────────
// Lives at the end of <body> (position:fixed), same reasoning as #compose-dropdown —
// its old spot inside .message-list-panel got clipped/overflowed in reading-pane
// "bottom" mode (fixed-height list panel). Positioned via JS under the search bar.
function toggleSearchFilters(e) {
  if (e) e.stopPropagation();
  const panel = document.getElementById('search-filters-panel');
  // Anchor to the *visible* search bar: .tb-search on desktop, .search-bar on
  // mobile. The old code always used .search-bar — display:none on desktop,
  // zero rect, panel glued to the left edge.
  const bars = [document.querySelector('.tb-search'), document.querySelector('.search-bar')];
  const bar = bars.find(b => b && b.getBoundingClientRect().width > 0);
  if (!panel || !bar) return;
  const isOpen = panel.style.display !== 'none';
  document.removeEventListener('click', closeSearchFiltersOutside);
  if (isOpen) { panel.style.display = 'none'; return; }
  populateMailboxScopeOptions();
  const r = bar.getBoundingClientRect();
  panel.style.display = 'block';
  panel.style.width = Math.min(r.width, 460) + 'px';
  panel.style.left = r.left + 'px';
  panel.style.top = Math.min(r.bottom + 4, window.innerHeight - 60) + 'px';
  setTimeout(() => document.addEventListener('click', closeSearchFiltersOutside), 0);
}
function closeSearchFiltersOutside(e) {
  const panel = document.getElementById('search-filters-panel');
  const btn = document.getElementById('search-filters-btn');
  if (!panel || panel.contains(e.target) || (btn && btn.contains(e.target))) return;
  panel.style.display = 'none';
  document.removeEventListener('click', closeSearchFiltersOutside);
}
// Offers "this account" / "this folder" only when a real folder is currently selected
// (not Unified Inbox / Starred, which don't map to a single account or folder).
function populateMailboxScopeOptions() {
  const sel = document.getElementById('sf-mailbox-scope');
  if (!sel) return;
  const prev = sel.value;
  sel.innerHTML = '<option value="">All mailboxes</option>';
  const cur = S.folders.find(f => String(f.id) === String(S.currentFolder));
  if (cur) {
    const acc = S.accounts.find(a => a.id === cur.account_id);
    const accLabel = acc ? (acc.display_name || acc.email_address) : 'this account';
    sel.innerHTML += `<option value="account:${cur.account_id}">This account: ${esc(accLabel)}</option>`;
    sel.innerHTML += `<option value="folder:${cur.id}">This folder: ${esc(cur.name)}</option>`;
  }
  if ([...sel.options].some(o => o.value === prev)) sel.value = prev;
}
function applySearchFilters() {
  const f = S.searchFilters;
  f.scope = document.getElementById('sf-scope').value;
  f.hasAttachment = document.getElementById('sf-attachment').value;
  f.dateFrom = document.getElementById('sf-date-from').value;
  f.dateTo = document.getElementById('sf-date-to').value;
  const olderDays = document.getElementById('sf-older-days').value;
  if (olderDays) {
    const cutoff = new Date(Date.now() - olderDays * 86400000);
    f.dateTo = cutoff.toISOString().slice(0, 10);
    document.getElementById('sf-date-to').value = f.dateTo;
  }
  f.minSizeKB = document.getElementById('sf-min-size').value;
  f.maxSizeKB = document.getElementById('sf-max-size').value;
  const mb = document.getElementById('sf-mailbox-scope').value; // "" | "account:<id>" | "folder:<id>"
  f.accountId = mb.startsWith('account:') ? mb.slice(8) : '';
  f.folderId = mb.startsWith('folder:') ? mb.slice(7) : '';
  document.getElementById('search-filters-btn').classList.toggle('active', hasActiveSearchFilters());
  S.currentPage = 1;
  loadMessages();
}
// Resets filter state/UI without triggering a reload — for callers (like selectFolder)
// that are about to call loadMessages() themselves right after.
function clearSearchFiltersQuiet() {
  S.searchFilters = { scope:'', hasAttachment:'', dateFrom:'', dateTo:'', minSizeKB:'', maxSizeKB:'', accountId:'', folderId:'' };
  ['sf-attachment','sf-date-from','sf-date-to','sf-older-days','sf-min-size','sf-max-size','sf-mailbox-scope']
    .forEach(id => { const el=document.getElementById(id); if (el) el.value=''; });
  const scopeEl=document.getElementById('sf-scope'); if (scopeEl) scopeEl.value='all'; // "" isn't a valid <option> here
  const btn=document.getElementById('search-filters-btn'); if (btn) btn.classList.remove('active');
}
function clearSearchFilters() {
  clearSearchFiltersQuiet();
  S.currentPage = 1;
  loadMessages();
}

async function loadMessages(append) {
  const list=document.getElementById('message-list');
  if (!append) list.innerHTML='<div class="spinner" style="margin-top:60px"></div>';
  let result;
  if (S.searchQuery || hasActiveSearchFilters()) {
    const f = S.searchFilters;
    let url = `/search?q=${encodeURIComponent(S.searchQuery)}&page=${S.currentPage}&page_size=50`;
    if (f.scope) url += `&scope=${f.scope}`;
    if (f.hasAttachment !== '') url += `&has_attachment=${f.hasAttachment}`;
    if (f.dateFrom) url += `&date_from=${f.dateFrom}`;
    if (f.dateTo) url += `&date_to=${f.dateTo}`;
    if (f.minSizeKB) url += `&min_size_kb=${f.minSizeKB}`;
    if (f.maxSizeKB) url += `&max_size_kb=${f.maxSizeKB}`;
    if (f.accountId) url += `&account_id=${f.accountId}`;
    if (f.folderId) url += `&folder_id=${f.folderId}`;
    result=await api('GET',url);
  }
  else if (S.currentFolder==='unified') result=await api('GET',`/messages/unified?page=${S.currentPage}&page_size=50`);
  else if (S.currentFolder==='starred') result=await api('GET',`/messages/starred?page=${S.currentPage}&page_size=50`);
  else if (S.currentFolder==='snoozed') result=await api('GET',`/messages/snoozed?page=${S.currentPage}&page_size=50`);
  else if (String(S.currentFolder).startsWith('label:')) result=await api('GET',`/messages/by-label/${String(S.currentFolder).slice(6)}?page=${S.currentPage}&page_size=50`);
  else result=await api('GET',`/messages?folder_id=${S.currentFolder}&page=${S.currentPage}&page_size=50`);
  if (!result){list.innerHTML='<div class="empty-state"><p>Failed to load</p></div>';return;}
  S.totalMessages=result.total||(result.messages||[]).length;
  if (append) S.messages.push(...(result.messages||[]));
  else S.messages=result.messages||[];
  renderMessageList();
  document.getElementById('panel-count').textContent=S.totalMessages>0?S.totalMessages+' messages':'';
}

function setFilter(mode) {
  S.filterUnread = (mode === 'unread');
  S.filterAttachment = (mode === 'attachment');
  S.sortOrder = (mode === 'unread' || mode === 'default' || mode === 'attachment') ? 'date-desc' : mode;

  // Update checkmarks
  ['default','unread','attachment','date-desc','date-asc','size-desc'].forEach(k => {
    const el = document.getElementById('fopt-'+k);
    if (el) el.textContent = (k === mode ? '✓ ' : '○ ') + el.textContent.slice(2);
  });

  // Update button label
  const labels = {
    'default':'Filter', 'unread':'Unread', 'attachment':'📎 Has Attachment',
    'date-desc':'↓ Date', 'date-asc':'↑ Date', 'size-desc':'↓ Size'
  };
  const labelEl = document.getElementById('filter-label');
  if (labelEl) {
    labelEl.textContent = labels[mode] || 'Filter';
    labelEl.style.color = mode !== 'default' ? 'var(--accent)' : '';
  }
  const menuEl = document.getElementById('filter-dropdown-menu');
  if (menuEl) menuEl.style.display = 'none';
  renderMessageList();
}

// Keep old names as aliases so nothing else breaks
function toggleFilterUnread() { setFilter(S.filterUnread ? 'default' : 'unread'); }
function setSortOrder(order) { setFilter(order); }

// ── Multi-select state ────────────────────────────────────────
if (!window.SEL) window.SEL = { ids: new Set(), lastIdx: -1 };

// ── Conversation grouping ─────────────────────────────────────────────────
// No per-provider thread id is populated anywhere in the sync engine (Graph/JMAP have one
// natively, plain IMAP doesn't), so this groups by normalized subject within the same
// account — good enough for "N messages" grouping without touching sync or schema. Only
// grouped within whatever page(s) are currently loaded, not across the whole mailbox.
function normalizeSubject(s) {
  let t = (s||'').trim(), prev;
  do { prev = t; t = t.replace(/^(re|fwd?|fw)\s*:\s*/i, '').trim(); } while (t !== prev);
  return t.toLowerCase();
}
// messageId -> sibling summaries {id,from,date,is_read} for every message that's part of a
// multi-message thread — populated by groupThreads(), read by renderMessageDetail().
let threadSiblingsById = {};
function groupThreads(msgs) {
  const groups = new Map(); // key -> messages[], insertion order preserved (already sorted)
  for (const m of msgs) {
    const subj = normalizeSubject(m.subject);
    // Blank/no-subject messages never group together — that'd lump unrelated emails.
    const key = subj ? (m.account_id||0)+'|'+subj : Symbol(m.id);
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(m);
  }
  threadSiblingsById = {};
  const out = [];
  for (const group of groups.values()) {
    if (group.length > 1) {
      const siblings = group.map(g=>({id:g.id, from:g.from_name||g.from_email, date:g.date, is_read:g.is_read}));
      for (const g of group) threadSiblingsById[g.id] = siblings;
    }
    const rep = group[0]; // groups are built from an already date-sorted list
    out.push(group.length > 1 ? {...rep, _threadCount: group.length} : rep);
  }
  return out;
}

function renderMessageList() {
  const list=document.getElementById('message-list');
  const msgs = getFilteredSortedMsgs();

  if (!msgs.length){
    const emptyMsg = S.filterUnread ? 'No unread messages' : S.filterAttachment ? 'No messages with attachments' : 'No messages';
    list.innerHTML=`<div class="empty-state"><svg viewBox="0 0 24 24"><path d="M20 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z"/></svg><p>${emptyMsg}</p></div>`;
    updateRibbonState();
    return;
  }

  // Update bulk action bar
  updateBulkBar();

  const sentView = isSentFolderView();

  list.innerHTML=msgs.map((m,i)=>{
    const acctLabel = m.account_name || m.account_email || '';
    const fromLine = sentView
      ? `To: ${m.to_list || '(no recipient)'}`
      : (m.from_name ? `${m.from_name} - ${m.from_email}` : (m.from_email||''));
    return `
    <div class="message-item ${m.id===S.selectedMessageId&&!SEL.ids.size?'active':''} ${!m.is_read?'unread':''} ${SEL.ids.has(m.id)?'selected':''}"
         data-id="${m.id}" data-idx="${i}"
         draggable="true"
         onclick="handleMsgClick(event,${m.id},${i})"
         oncontextmenu="showMessageMenu(event,${m.id})"
         ondragstart="handleMsgDragStart(event,${m.id})">
      <div class="msg-top">
        <span class="msg-unread-dot" title="${m.is_read?'':'Unread'}"></span>
        <span class="msg-dot" style="background:${m.account_color}" title="${esc(m.account_email||'')}"></span>
        <span class="msg-account-name" title="${esc(m.account_email||'')}">${esc(acctLabel)}</span>
        <span class="msg-from" title="${esc(fromLine)}">${esc(fromLine)}</span>
        <span class="msg-date">${formatDate(m.date)}</span>
      </div>
      <div class="msg-line2">
        <span class="msg-text" title="${esc(m.subject||'(no subject)')}">
          <span class="msg-subject">${esc(m.subject||'(no subject)')}</span>${m._threadCount?` <span class="msg-thread-count">${m._threadCount}</span>`:''}${m.preview?` — <span class="msg-preview">${esc(m.preview)}</span>`:''}
        </span>
        <span class="msg-icons">
          ${m.size?`<span class="msg-size">${formatSize(m.size)}</span>`:''}
          ${m.has_attachment?'<svg width="11" height="11" viewBox="0 0 24 24" fill="var(--muted)"><path d="M16.5 6v11.5c0 2.21-1.79 4-4 4s-4-1.79-4-4V5c0-1.38 1.12-2.5 2.5-2.5s2.5 1.12 2.5 2.5v10.5c0 .55-.45 1-1 1s-1-.45-1-1V6H10v9.5c0 1.38 1.12 2.5 2.5 2.5s2.5-1.12 2.5-2.5V5c0-2.21-1.79-4-4-4S7 2.79 7 5v12.5c0 3.04 2.46 5.5 5.5 5.5s5.5-2.46 5.5-5.5V6h-1.5z"/></svg>':''}
          ${m.labels?.length?`<span class="msg-label-dots">${m.labels.map(l=>`<span class="msg-label-dot" style="background:${l.color}" title="${esc(l.name)}"></span>`).join('')}</span>`:''}
          <span class="msg-star ${m.is_starred?'on':''}" onclick="toggleStar(${m.id},event)">${m.is_starred?'★':'☆'}</span>
        </span>
      </div>
    </div>`;
  }).join('')+(S.messages.length<S.totalMessages
    ?`<div class="load-more"><button class="load-more-btn" onclick="loadMoreMessages()">Load more</button></div>`:'');

  // Enable drag-drop onto folder nav items
  document.querySelectorAll('.nav-item[data-fid]').forEach(el=>{
    el.ondragover=e=>{e.preventDefault();el.classList.add('drag-over');};
    el.ondragleave=()=>el.classList.remove('drag-over');
    el.ondrop=e=>{
      e.preventDefault(); el.classList.remove('drag-over');
      const fid=parseInt(el.dataset.fid);
      if (!fid) return;
      const ids = SEL.ids.size ? [...SEL.ids] : [parseInt(e.dataTransfer.getData('text/plain'))];
      ids.forEach(id=>moveMessage(id, fid, true));
      SEL.ids.clear(); updateBulkBar(); renderMessageList();
    };
  });
}

function handleMsgClick(e, id, idx) {
  if (e.ctrlKey || e.metaKey) {
    // Toggle selection
    SEL.ids.has(id) ? SEL.ids.delete(id) : SEL.ids.add(id);
    SEL.lastIdx = idx;
    renderMessageList(); return;
  }
  if (e.shiftKey && SEL.lastIdx >= 0) {
    // Range select
    const msgs = getFilteredSortedMsgs();
    const lo=Math.min(SEL.lastIdx,idx), hi=Math.max(SEL.lastIdx,idx);
    for (let i=lo;i<=hi;i++) SEL.ids.add(msgs[i].id);
    renderMessageList(); return;
  }
  SEL.ids.clear(); SEL.lastIdx=idx;
  openMessage(id);
}

function getFilteredSortedMsgs() {
  let msgs=[...S.messages];
  if (S.filterUnread) msgs=msgs.filter(m=>!m.is_read);
  if (S.filterAttachment) msgs=msgs.filter(m=>m.has_attachment);
  if (S.sortOrder==='date-asc') msgs.sort((a,b)=>new Date(a.date)-new Date(b.date));
  else if (S.sortOrder==='size-desc') msgs.sort((a,b)=>(b.size||0)-(a.size||0));
  else msgs.sort((a,b)=>new Date(b.date)-new Date(a.date));
  // View-setting toggle: when grouping is off, show every message as its own row.
  return uiPrefsGet('conversations','on') === 'on' ? groupThreads(msgs) : msgs;
}

function handleMsgDragStart(e, id) {
  if (!SEL.ids.has(id)) { SEL.ids.clear(); SEL.ids.add(id); }
  e.dataTransfer.setData('text/plain', id);
  e.dataTransfer.effectAllowed='move';
}

function updateBulkBar() {
  let bar = document.getElementById('bulk-action-bar');
  if (!bar) {
    bar = document.createElement('div');
    bar.id='bulk-action-bar';
    bar.style.cssText='display:none;position:sticky;top:0;z-index:10;background:var(--accent);color:#fff;padding:6px 12px;font-size:12px;display:flex;align-items:center;gap:8px';
    bar.innerHTML=`<span id="bulk-count"></span>
      <button onclick="bulkMarkRead(true)" style="font-size:11px;padding:2px 8px;background:rgba(255,255,255,.2);border:none;border-radius:4px;color:#fff;cursor:pointer">Mark read</button>
      <button onclick="bulkMarkRead(false)" style="font-size:11px;padding:2px 8px;background:rgba(255,255,255,.2);border:none;border-radius:4px;color:#fff;cursor:pointer">Mark unread</button>
      <button onclick="bulkForwardAsAttachment()" style="font-size:11px;padding:2px 8px;background:rgba(255,255,255,.2);border:none;border-radius:4px;color:#fff;cursor:pointer">✉️ Forward as attachment</button>
      <button onclick="bulkDelete()" style="font-size:11px;padding:2px 8px;background:rgba(255,255,255,.2);border:none;border-radius:4px;color:#fff;cursor:pointer">Delete</button>
      <button onclick="SEL.ids.clear();renderMessageList()" style="margin-left:auto;font-size:11px;padding:2px 8px;background:rgba(255,255,255,.2);border:none;border-radius:4px;color:#fff;cursor:pointer">✕ Clear</button>`;
    document.getElementById('message-list').before(bar);
  }
  if (SEL.ids.size) {
    bar.style.display='flex';
    document.getElementById('bulk-count').textContent=SEL.ids.size+' selected';
  } else {
    bar.style.display='none';
  }
  updateRibbonState();
}

async function bulkMarkRead(read) {
  await Promise.all([...SEL.ids].map(id=>api('PUT','/messages/'+id+'/read',{read})));
  SEL.ids.forEach(id=>{const m=S.messages.find(m=>m.id===id);if(m)m.is_read=read;});
  SEL.ids.clear(); renderMessageList(); loadFolders();
}

// Opens one new compose with every selected message attached as .eml.
function bulkForwardAsAttachment() {
  const ids = [...SEL.ids];
  if (!ids.length) return;
  openCompose({ mode:'forward', title:'Forward as Attachment',
    subject: ids.length===1 ? 'Fwd: '+(S.messages.find(m=>m.id===ids[0])?.subject||'') : `Fwd: ${ids.length} messages`,
    body:'' });
  ids.forEach(id=>attachMessageAsEML(id));
  SEL.ids.clear(); renderMessageList();
}

function bulkDelete() {
  const ids = [...SEL.ids];
  if (!ids.length) return;
  const removed = S.messages.filter(m=>ids.includes(m.id));
  S.messages = S.messages.filter(m=>!ids.includes(m.id));
  if (S.currentMessage && ids.includes(S.currentMessage.id)) resetDetail();
  SEL.ids.clear(); renderMessageList();
  undoToast(`${ids.length} message${ids.length===1?'':'s'} deleted`, {
    onUndo: () => { S.messages = S.messages.concat(removed); renderMessageList(); },
    onCommit: async () => {
      const results = await Promise.all(ids.map(id=>api('DELETE','/messages/'+id)));
      if (results.some(r=>!r?.ok)) toast('Some messages failed to delete','error');
      loadFolders();
    },
  });
}

function loadMoreMessages(){ S.currentPage++; loadMessages(true); }

async function openMessage(id) {
  S.selectedMessageId=id; renderMessageList();
  mobSetView('detail');
  const detail=document.getElementById('message-detail');
  detail.innerHTML='<div class="spinner" style="margin-top:100px"></div>';
  const msg=await api('GET','/messages/'+id);
  if (!msg){detail.innerHTML='<div class="no-message"><p>Failed to load</p></div>';return;}
  if (isDraftFolder(msg.folder_id)) { resumeDraft(msg); return; }
  S.currentMessage=msg;
  renderMessageDetail(msg, false);
  const li=S.messages.find(m=>m.id===id);
  if (li&&!li.is_read){
    li.is_read=true; renderMessageList();
    // Sync read status to server (enqueues IMAP op via backend)
    api('PUT','/messages/'+id+'/read',{read:true});
  }
}

// Opening a message that lives in a Drafts folder resumes editing it (in the compose modal,
// pre-filled) rather than showing it read-only — otherwise a saved draft is a dead end, which
// defeats the point of "save it for later". S.draftId is seeded from the message's remote_uid
// (the same IMAP UID / Graph id / JMAP id SaveDraft/DiscardDraft already key off of), so the
// next autosave replaces this exact draft in place instead of creating a duplicate.
function isDraftFolder(folderId) {
  return S.folders?.find(f=>f.id===folderId)?.folder_type==='drafts';
}
// Whether the currently open folder is a Sent folder — used by renderMessageList to show
// "To: <recipient>" instead of the from-line, since a Sent row's "from" is always just your
// own account and tells you nothing about who the message actually went to.
function isSentFolderView() {
  return S.folders?.find(f=>f.id===S.currentFolder)?.folder_type==='sent';
}
function isSpamFolderView() {
  return S.folders?.find(f=>f.id===S.currentFolder)?.folder_type==='spam';
}

// Moves the message to its account's Spam folder and adds the sender to the Settings >
// Security > Spam Block list, so future mail from them is auto-filed to Spam at sync time
// too (see IsSpamBlocked call sites in the syncer) — not just this one message.
async function markAsSpam(msgId) {
  const msg = (S.currentMessage?.id===msgId) ? S.currentMessage : S.messages.find(m=>m.id===msgId);
  if (!msg) return;
  const spamFolder = S.folders.find(f=>f.account_id===msg.account_id && f.folder_type==='spam');
  if (!spamFolder) { toast('No Spam folder found for this account','error'); return; }
  if (msg.from_email) await api('POST','/spam-block',{sender:msg.from_email});
  await moveMessage(msgId, spamFolder.id, true);
  toast('Marked as spam — future mail from '+(msg.from_email||'this sender')+' will be blocked too','success');
}
function resumeDraft(msg) {
  const toList=(msg.to||'').split(',').map(s=>s.trim()).filter(Boolean);
  const ccList=(msg.cc||'').split(',').map(s=>s.trim()).filter(Boolean);
  const bccList=(msg.bcc||'').split(',').map(s=>s.trim()).filter(Boolean);
  openCompose({
    mode:'new', title:'Edit Draft', subject:msg.subject||'',
    accountId:msg.account_id, skipSignature:true, body:quotedBodyHTML(msg),
  });
  toList.forEach(a=>addTag('compose-to', a));
  if (ccList.length) { showCCRow(); ccList.forEach(a=>addTag('compose-cc-tags', a)); }
  if (bccList.length) { showBCCRow(); bccList.forEach(a=>addTag('compose-bcc-tags', a)); }
  S.draftId=msg.remote_uid||'';
  S.draftDirty=false;
}

// ── External link navigation whitelist ───────────────────────────────────────
// Persisted in sessionStorage so it resets on tab close (safety default).
const _extNavOk = new Set(JSON.parse(sessionStorage.getItem('extNavOk')||'[]'));
function _saveExtNavOk(){ sessionStorage.setItem('extNavOk', JSON.stringify([..._extNavOk])); }

function confirmExternalNav(url) {
  const origin = (() => { try { return new URL(url).origin; } catch(e){ return url; } })();
  if (_extNavOk.has(origin)) { window.open(url,'_blank','noopener,noreferrer'); return; }
  const overlay = document.createElement('div');
  overlay.className = 'modal-overlay open';
  overlay.innerHTML = `<div class="modal" style="max-width:480px">
    <h2 style="margin:0 0 12px">Open external link?</h2>
    <div style="word-break:break-all;background:var(--bg);border:1px solid var(--border);border-radius:6px;padding:10px;font-size:12px;font-family:monospace;margin-bottom:16px;color:var(--text2)">${esc(url)}</div>
    <p style="margin:0 0 20px;font-size:13px;color:var(--text2)">This link was in a received email. Opening it will take you to an external website.</p>
    <div style="display:flex;gap:8px;flex-wrap:wrap">
      <button class="btn-primary" id="enav-once">Open once</button>
      <button class="btn-primary" id="enav-always" style="background:var(--accent2,#2a7)">Always allow ${esc(origin)}</button>
      <button class="action-btn" id="enav-cancel">Cancel</button>
    </div>
  </div>`;
  document.body.appendChild(overlay);
  overlay.querySelector('#enav-once').onclick = () => { overlay.remove(); window.open(url,'_blank','noopener,noreferrer'); };
  overlay.querySelector('#enav-always').onclick = () => { _extNavOk.add(origin); _saveExtNavOk(); overlay.remove(); window.open(url,'_blank','noopener,noreferrer'); };
  overlay.querySelector('#enav-cancel').onclick = () => overlay.remove();
  overlay.onclick = e => { if(e.target===overlay) overlay.remove(); };
}

// ── HTML body sanitizing helpers (shared by the reading pane and reply/forward quoting) ──
function stripUnresolvedCID(h){ return h.replace(/src\s*=\s*(['"])cid:[^'"]*\1/gi,'src=""').replace(/src\s*=\s*cid:\S+/gi,'src=""'); }
function stripEmbeddedFrames(h){ return h.replace(/<iframe[\s\S]*?<\/iframe>/gi,'').replace(/<iframe[^>]*>/gi,''); }
function stripRemoteImages(h){
  return h.replace(/<img(\s[^>]*?)src\s*=\s*(['"])(https?:\/\/[^'"]+)\2/gi,'<img$1src="" data-blocked-src="$3"')
          .replace(/url\s*\(\s*(['"]?)https?:\/\/[^)'"]+\1\s*\)/gi,'url()')
          .replace(/<link[^>]*>/gi,'').replace(/<script[\s\S]*?<\/script>/gi,'');
}

// True if fromEmail appears in the cached contacts list (loaded once, warmed at boot — see
// ensureContactsCache). Used only by the "Only from Contacts" remote-image policy, so a
// not-yet-loaded cache fails closed (not a contact) rather than open.
function isContactEmail(fromEmail) {
  if (!fromEmail || !contactsCache) return false;
  const e = fromEmail.toLowerCase();
  return contactsCache.some(c => (c.email||'').toLowerCase() === e);
}

// Settings → Profile → Remote Images policy, plus the per-sender whitelist. "always"/"never"
// ignore the whitelist entirely (a blanket policy shouldn't be second-guessed by leftover
// per-sender entries from a time the policy was different); "contacts" and "manual" both
// still honor it, since it's how a sender gets permanently trusted under either.
function isRemoteContentAllowed(fromEmail) {
  const policy = uiPrefsGet('remoteImagePolicy', 'manual');
  if (policy === 'always') return true;
  if (policy === 'never') return false;
  if (policy === 'contacts') return isContactEmail(fromEmail) || S.remoteWhitelist.has(fromEmail);
  return S.remoteWhitelist.has(fromEmail); // manual (default)
}

// Body HTML for a quoted reply/forward: same treatment as the reading pane — cid: refs and
// iframes stripped always, remote images blocked unless allowed by policy. The underlying
// content isn't lost (stripRemoteImages keeps the real URL in data-blocked-src), it just
// can't trigger a network fetch the user never approved by ending up in a live, unsandboxed
// contenteditable (unlike the reading pane, which renders in a sandboxed iframe).
function quotedBodyHTML(msg) {
  if (!msg.body_html) return '<pre>'+esc(msg.body_text||'')+'</pre>';
  let html = stripUnresolvedCID(stripEmbeddedFrames(msg.body_html));
  if (!isRemoteContentAllowed(msg.from_email)) html = stripRemoteImages(html);
  return html;
}

// Inverse of stripRemoteImages: the recipient of a sent reply/forward — or the account itself,
// reopening a saved draft — should see the quoted images normally, so the real URL goes out
// even though it never got fetched in our own browser while composing.
function restoreBlockedImages(html) {
  return html.replace(/src=""\s+data-blocked-src="([^"]*)"/gi, 'src="$1"');
}

function renderMessageDetail(msg, showRemoteContent) {
  const detail=document.getElementById('message-detail');
  const allowed=showRemoteContent||isRemoteContentAllowed(msg.from_email);

  const cssReset = `<style>html,body{background:#ffffff!important;color:#1a1a1a!important;` +
    `font-family:Arial,sans-serif;font-size:14px;line-height:1.5;margin:8px}a{color:#1a5fb4}` +
    `img{max-width:100%;height:auto}iframe{display:none!important}</style>`;

  // Injected into srcdoc: reports height + intercepts all link clicks → postMessage to parent.
  //
  // _reportH() measures the bottom of the lowest leaf element that actually carries visible
  // text or an image, not document.documentElement.scrollHeight — email templates routinely
  // end in structural filler (an empty spacer cell, Outlook-only conditional markup real
  // browsers don't fully discard, trailing table rows) that scrollHeight faithfully counts as
  // "content" even though nothing is there to read, which is exactly the wall of dead space
  // this is fixing. Falls back to scrollHeight only if no such element is found at all.
  //
  // scrollHeight can also genuinely read 0 on the first call or two — the nested srcdoc
  // document's own layout settles a beat after DOMContentLoaded/load fire, and if nothing
  // changes the DOM afterward, the MutationObserver never gets a second chance to correct it.
  // ResizeObserver catches the moment layout actually changes; the backoff poll is a plain
  // safety net on top of both.
  const heightScript = `<script>
    function _reportH(){
      try{
        var maxBottom=0;
        var all=document.body?document.body.getElementsByTagName('*'):[];
        for(var i=0;i<all.length;i++){
          var el=all[i];
          if(el.children.length>0) continue; // only leaf elements carry real visible pixels
          var cs=getComputedStyle(el);
          if(cs.display==='none'||cs.visibility==='hidden'||parseFloat(cs.opacity||'1')===0) continue;
          var hasText=(el.textContent||'').replace(/[\\s\\u00A0]/g,'').length>0;
          if(!hasText && el.tagName!=='IMG') continue;
          var r=el.getBoundingClientRect();
          if(r.bottom>maxBottom) maxBottom=r.bottom;
        }
        var h=maxBottom>0?maxBottom:document.documentElement.scrollHeight;
        parent.postMessage({type:'gomail-frame-h',h:h},'*');
      }catch(ex){parent.postMessage({type:'gomail-frame-h',h:0},'*');}
    }
    document.addEventListener('DOMContentLoaded',_reportH);
    window.addEventListener('load',_reportH);
    new MutationObserver(_reportH).observe(document.documentElement,{subtree:true,childList:true,attributes:true});
    if(window.ResizeObserver) new ResizeObserver(_reportH).observe(document.documentElement);
    [50,150,400,900,1800,3000].forEach(function(ms){ setTimeout(_reportH, ms); });
    document.addEventListener('click',function(e){
      var el=e.target; while(el&&el.tagName!=='A') el=el.parentElement;
      if(!el) return;
      var href=el.getAttribute('href');
      if(!href||href.startsWith('#')||href.startsWith('mailto:')) return;
      e.preventDefault(); e.stopPropagation();
      parent.postMessage({type:'gomail-open-url',url:href},'*');
    },true);
  <\/script>`;

  const sandboxAttr = 'allow-scripts allow-popups allow-popups-to-escape-sandbox';

  let bodyHtml='';
  if (msg.body_html) {
    let html = stripUnresolvedCID(stripEmbeddedFrames(msg.body_html));
    if (allowed) {
      const srcdoc = cssReset + heightScript + html;
      bodyHtml=`<iframe id="msg-frame" sandbox="${sandboxAttr}"
        style="width:100%;border:none;min-height:200px;display:block"
        srcdoc="${srcdoc.replace(/"/g,'&quot;')}"></iframe>`;
    } else {
      const stripped = stripRemoteImages(html);
      // "Always allow" permanently whitelists the sender — doesn't make sense to offer under
      // a blanket "never" policy, so only the one-time "Load images" escape hatch shows there.
      const policy = uiPrefsGet('remoteImagePolicy', 'manual');
      const alwaysAllowBtn = policy === 'never' ? '' :
        `<button class="rcb-btn" onclick="whitelistSender('${esc(msg.from_email)}')">Always allow from ${esc(msg.from_email)}</button>`;
      bodyHtml=`<div class="remote-content-banner">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor"><path d="M21 19V5c0-1.1-.9-2-2-2H5c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2zM8.5 13.5l2.5 3.01L14.5 12l4.5 6H5l3.5-4.5z"/></svg>
        Remote images blocked.
        <button class="rcb-btn" onclick="renderMessageDetail(S.currentMessage,true)">Load images</button>
        ${alwaysAllowBtn}
      </div>
      <iframe id="msg-frame" title="Message content" sandbox="${sandboxAttr}"
        style="width:100%;border:none;min-height:200px;display:block"
        srcdoc="${(cssReset + heightScript + stripped).replace(/"/g,'&quot;')}"></iframe>`;
    }
  } else {
    bodyHtml=`<div class="detail-body-text">${esc(msg.body_text||'(empty)')}</div>`;
  }

  let attachHtml='';
  if (msg.attachments?.length) {
    const chips = msg.attachments.map(a=>{
      const url=`/api/messages/${msg.id}/attachments/${a.id}`;
      const ct=a.content_type||'';
      const viewable=/^(image\/|text\/|application\/pdf$|video\/|audio\/)/.test(ct);
      const icon=ct.startsWith('image/')?'🖼':ct==='application/pdf'?'📄':ct.startsWith('video/')?'🎬':ct.startsWith('audio/')?'🎵':'📎';
      if(viewable){
        return `<a class="attachment-chip" href="${url}" target="_blank" rel="noopener" title="Open ${esc(a.filename)}">${icon} <span>${esc(a.filename)}</span><span style="color:var(--muted);font-size:10px"> ${formatSize(a.size)}</span></a>`;
      }
      return `<a class="attachment-chip" href="${url}" download="${esc(a.filename)}" title="Download ${esc(a.filename)}">${icon} <span>${esc(a.filename)}</span><span style="color:var(--muted);font-size:10px"> ${formatSize(a.size)}</span></a>`;
    }).join('');
    const dlAll=`<button class="attachment-chip" onclick="downloadAllAttachments(${msg.id})" style="cursor:pointer;border:1px solid var(--border)">⬇ <span>Download all</span></button>`;
    attachHtml=`<div class="attachments-bar">${dlAll}${chips}</div>`;
  }

  // Other messages in this thread (same normalized subject, same account) — grouped
  // client-side by renderMessageList()'s groupThreads(), so only reflects loaded pages.
  // Rendered as a collapsed dropdown off an action button, not inline — inline was pushing
  // the actual message body down and shrinking the reading area.
  let threadBtnHtml='';
  const siblings=threadSiblingsById[msg.id];
  if (siblings?.length>1) {
    const rows=siblings.map(s=>`
      <div class="thread-sibling-row${s.id===msg.id?' active':''}${s.is_read?'':' unread'}" onclick="openMessage(${s.id})">
        <span class="msg-unread-dot" title="${s.is_read?'':'Unread'}"></span>
        <span class="thread-sibling-from">${esc(s.from||'')}</span>
        <span class="thread-sibling-date">${formatDate(s.date)}</span>
      </div>`).join('');
    threadBtnHtml=`
      <div class="thread-btn-wrap">
        <button class="action-btn" onclick="toggleThreadPanel(event)">🧵 Thread (${siblings.length})</button>
        <div class="thread-dropdown" id="thread-panel" style="display:none">
          <div class="thread-siblings-title">${siblings.length} messages in this thread</div>
          ${rows}
        </div>
      </div>`;
  }


  detail.innerHTML=`
    <div class="detail-header">
      <div class="detail-subject">${esc(msg.subject||'(no subject)')}</div>
      <div class="detail-meta">
        <div class="detail-from">
          <strong>${esc(msg.from_name||msg.from_email)}</strong>
          ${msg.from_name?`<span style="color:var(--muted);font-size:12px"> &lt;${esc(msg.from_email)}&gt;</span>`:''}
          ${msg.to?`<div style="font-size:12px;color:var(--muted);margin-top:2px">To: ${esc(msg.to)}</div>`:''}
          ${msg.cc?`<div style="font-size:12px;color:var(--muted)">CC: ${esc(msg.cc)}</div>`:''}
        </div>
        <div class="detail-date">${formatFullDate(msg.date)}</div>
      </div>
      <div class="detail-labels">
        ${(msg.labels||[]).map(l=>`<span class="label-chip" style="background:${l.color}22;color:${l.color};border-color:${l.color}55">
          <span class="label-chip-dot" style="background:${l.color}"></span>${esc(l.name)}
          <button onclick="toggleMessageLabel(${msg.id},${l.id})" title="Remove label">×</button>
        </span>`).join('')}
        <button class="label-add-btn" onclick="toggleLabelPicker(event,${msg.id})">+ Label</button>
      </div>
    </div>
    <div class="detail-actions">
      <button class="action-btn" onclick="openReply()">↩ Reply</button>
      <button class="action-btn" onclick="openForward()">↪ Forward</button>
      <button class="action-btn" onclick="openForwardAsAttachment()" title="Forward the original message as an .eml file attachment">↪ Fwd as Attachment</button>
      ${threadBtnHtml}
      <button class="action-btn" onclick="toggleStar(${msg.id})">${msg.is_starred?'★ Unstar':'☆ Star'}</button>
      <button class="action-btn" onclick="${S.currentFolder==='snoozed'?'unsnoozeMessage':'snoozeMessage'}(${msg.id})">⏰ ${S.currentFolder==='snoozed'?'Unsnooze':'Snooze'}</button>
      <button class="action-btn" onclick="showMessageHeaders(${msg.id})">⋮ Headers</button>
      <button class="action-btn" onclick="downloadEML(${msg.id})">⬇ Download</button>
      ${(isSentFolderView()||isSpamFolderView())?'':`<button class="action-btn" onclick="markAsSpam(${msg.id})" title="Move to Spam and block this sender">🚫 Mark as Spam</button>`}
      <button class="action-btn danger" onclick="deleteMessage(${msg.id})">🗑 Delete</button>
    </div>
    ${attachHtml}
    <div class="detail-body">${bodyHtml}</div>`;

  // Auto-size iframe via postMessage from injected height-reporting script.
  // We cannot use contentDocument (null without allow-same-origin in sandbox).
  if (msg.body_html) {
    const frame = document.getElementById('msg-frame');
    if (frame) {
      // Clean up any previous listener/fallback timer
      if (window._frameMsgHandler) window.removeEventListener('message', window._frameMsgHandler);
      if (window._frameFallbackTimer) clearTimeout(window._frameFallbackTimer);
      let lastH = 0, gotValidHeight = false;
      window._frameMsgHandler = (e) => {
        if (e.data?.type === 'gomail-frame-h' && e.data.h > 50) {
          gotValidHeight = true;
          const h = e.data.h + 24;
          if (Math.abs(h - lastH) > 4) {
            lastH = h;
            frame.style.height = h + 'px';
          }
        } else if (e.data?.type === 'gomail-open-url' && e.data.url) {
          confirmExternalNav(e.data.url);
        }
      };
      window.addEventListener('message', window._frameMsgHandler);
      // Every retry in heightScript reported 0/invalid — rather than leave the reader stuck
      // at the 200px minimum with real content scrolling inside a cramped box, fall back to a
      // generous height so the message is actually readable without the auto-fit ever working.
      window._frameFallbackTimer = setTimeout(() => {
        if (!gotValidHeight) frame.style.height = '600px';
      }, 3500);
    }
  }
}

// Download all attachments for a message sequentially
async function downloadAllAttachments(msgId) {
  const msg = S.currentMessage;
  if (!msg?.attachments?.length) return;
  for (const a of msg.attachments) {
    const url = `/api/messages/${msgId}/attachments/${a.id}`;
    try {
      const resp = await fetch(url);
      const blob = await resp.blob();
      const tmp = document.createElement('a');
      tmp.href = URL.createObjectURL(blob);
      tmp.download = a.filename || 'attachment';
      tmp.click();
      URL.revokeObjectURL(tmp.href);
      // Small delay to avoid browser throttling sequential downloads
      await new Promise(r => setTimeout(r, 400));
    } catch(e) { toast('Failed to download '+esc(a.filename),'error'); }
  }
}

async function whitelistSender(sender) {
  const r=await api('POST','/remote-content-whitelist',{sender});
  if (r?.ok){S.remoteWhitelist.add(sender);toast('Always allowing content from '+sender,'success');if(S.currentMessage)renderMessageDetail(S.currentMessage,false);}
}

async function showMessageHeaders(id) {
  const r=await api('GET','/messages/'+id+'/headers');
  if (!r?.headers) return;
  const rows=Object.entries(r.headers).filter(([,v])=>v)
    .map(([k,v])=>`<tr><td style="color:var(--muted);padding:4px 12px 4px 0;font-size:12px;white-space:nowrap;vertical-align:top">${esc(k)}</td><td style="font-size:12px;word-break:break-all">${esc(v)}</td></tr>`).join('');
  const rawText = r.raw||'';
  const overlay=document.createElement('div');
  overlay.className='modal-overlay open';
  overlay.innerHTML=`<div class="modal" style="width:660px;max-height:85vh;display:flex;flex-direction:column">
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">
      <h2 style="margin:0">Message Headers</h2>
      <button class="icon-btn" onclick="this.closest('.modal-overlay').remove()"><svg viewBox="0 0 24 24"><path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/></svg></button>
    </div>
    <div style="overflow-y:auto;flex:1">
      <table style="width:100%;margin-bottom:16px"><tbody>${rows}</tbody></table>
      <div style="font-size:11px;color:var(--muted);text-transform:uppercase;letter-spacing:.7px;margin-bottom:6px">Raw Headers</div>
      <div style="position:relative">
        <textarea id="raw-headers-ta" readonly style="width:100%;box-sizing:border-box;height:180px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text2);font-family:monospace;font-size:11px;padding:10px;resize:vertical;outline:none">${esc(rawText)}</textarea>
        <button onclick="navigator.clipboard.writeText(document.getElementById('raw-headers-ta').value).then(()=>toast('Copied','success'))"
          style="position:absolute;top:6px;right:8px;font-size:11px;padding:3px 10px;background:var(--surface2);border:1px solid var(--border);border-radius:4px;color:var(--text2);cursor:pointer">Copy</button>
      </div>
    </div>
  </div>`;
  overlay.addEventListener('click',e=>{if(e.target===overlay)overlay.remove();});
  document.body.appendChild(overlay);
}

function downloadEML(id) {
  window.open('/api/messages/'+id+'/download.eml','_blank');
}

function showMessageMenu(e, id) {
  e.preventDefault(); e.stopPropagation();
  const msg = S.messages.find(m=>m.id===id);
  const otherFolders = S.folders.filter(f=>!f.is_hidden&&String(f.id)!==String(S.currentFolder)).slice(0,16);
  const moveItems = otherFolders.map(f=>`<div class="ctx-item ctx-sub-item" onclick="moveMessage(${id},${f.id});closeMenu()">${esc(f.name)}</div>`).join('');
  const moveSub = otherFolders.length ? `
    <div class="ctx-item ctx-has-sub">📂 Move to
      <span class="ctx-sub-arrow">›</span>
      <div class="ctx-submenu">${moveItems}</div>
    </div>` : '';
  const labelItems = S.labels.map(l => {
    const on = msg?.labels?.some(x=>x.id===l.id);
    return `<div class="ctx-item ctx-sub-item label-picker-item" onclick="toggleMessageLabel(${id},${l.id});closeMenu()">
      <span class="nav-label-dot" style="background:${l.color}"></span>${on?'✓ ':'○ '}${esc(l.name)}
    </div>`;
  }).join('');
  const labelSub = S.labels.length ? `
    <div class="ctx-item ctx-has-sub">🏷 Label as
      <span class="ctx-sub-arrow">›</span>
      <div class="ctx-submenu">${labelItems}</div>
    </div>` : '';
  const attachEntry = S.composeVisible
    ? `<div class="ctx-item" onclick="attachMessageAsEML(${id});closeMenu()">✉️ Attach as .eml to open message</div>` : '';
  const snoozeEntry = S.currentFolder==='snoozed'
    ? `<div class="ctx-item" onclick="unsnoozeMessage(${id});closeMenu()">⏰ Unsnooze</div>`
    : `<div class="ctx-item" onclick="snoozeMessage(${id});closeMenu()">⏰ Snooze</div>`;
  const newTabUrl = isDraftFolder(msg?.folder_id) ? '/compose?edit_draft_id='+id : '/message/'+id;
  showCtxMenu(e,`
    <div class="ctx-item" onclick="window.open('${newTabUrl}','_blank');closeMenu()">↗ Open in new tab</div>
    <div class="ctx-sep"></div>
    <div class="ctx-item" onclick="openReplyTo(${id});closeMenu()">↩ Reply</div>
    <div class="ctx-item" onclick="toggleStar(${id});closeMenu()">${msg?.is_starred?'★ Unstar':'☆ Star'}</div>
    <div class="ctx-item" onclick="markRead(${id},${msg?.is_read?'false':'true'});closeMenu()">${msg?.is_read?'Mark unread':'Mark read'}</div>
    ${snoozeEntry}
    <div class="ctx-sep"></div>
    ${moveSub}
    ${labelSub}
    ${attachEntry}
    <div class="ctx-item" onclick="showMessageHeaders(${id});closeMenu()">⋮ View headers</div>
    <div class="ctx-item" onclick="downloadEML(${id});closeMenu()">⬇ Download .eml</div>
    <div class="ctx-sep"></div>
    ${(isSentFolderView()||isSpamFolderView())?'':`<div class="ctx-item" onclick="markAsSpam(${id});closeMenu()">🚫 Mark as spam</div>`}
    <div class="ctx-item danger" onclick="deleteMessage(${id});closeMenu()">🗑 Delete</div>`);
}

async function toggleStar(id, e) {
  if(e) e.stopPropagation();
  const r=await api('PUT','/messages/'+id+'/star');
  if (r){const m=S.messages.find(m=>m.id===id);if(m)m.is_starred=r.starred;renderMessageList();
    if(S.currentMessage?.id===id){S.currentMessage.is_starred=r.starred;renderMessageDetail(S.currentMessage,false);}}
}

async function markRead(id, read) {
  await api('PUT','/messages/'+id+'/read',{read});
  const m=S.messages.find(m=>m.id===id);if(m){m.is_read=read;renderMessageList();}
  loadFolders();
}

async function moveMessage(msgId, folderId, silent=false) {
  const folder = S.folders.find(f=>f.id===folderId);
  const doMove = async () => {
    const r=await api('PUT','/messages/'+msgId+'/move',{folder_id:folderId});
    if(r?.ok){if(!silent)toast('Moved','success');S.messages=S.messages.filter(m=>m.id!==msgId);
      if(S.currentMessage?.id===msgId)resetDetail();loadFolders();}
    else if(!silent) toast('Move failed','error');
  };
  if (silent) { doMove(); return; }
  inlineConfirm(`Move this message to "${folder?.name||'selected folder'}"?`, doMove);
}

function deleteMessage(id) {
  const idx = S.messages.findIndex(m=>m.id===id);
  if (idx===-1) return;
  const [msg] = S.messages.splice(idx,1);
  const wasCurrent = S.currentMessage?.id===id;
  renderMessageList();
  if (wasCurrent) resetDetail();
  undoToast('Message deleted', {
    onUndo: () => { S.messages.splice(idx,0,msg); renderMessageList(); },
    onCommit: async () => {
      const r=await api('DELETE','/messages/'+id);
      if(r?.ok){loadFolders();}
      else { toast('Delete failed','error'); S.messages.splice(idx,0,msg); renderMessageList(); }
    },
  });
}

function snoozeMessage(id) {
  inlineDateTimePrompt('Snooze until:', async (iso) => {
    const r = await api('PUT','/messages/'+id+'/snooze', {until: iso});
    if (r?.ok) {
      toast('Message snoozed','success');
      S.messages = S.messages.filter(m=>m.id!==id);
      renderMessageList();
      if (S.currentMessage?.id===id) resetDetail();
      loadFolders();
    } else toast('Snooze failed','error');
  }, 'Snooze');
}

async function unsnoozeMessage(id) {
  const r = await api('DELETE','/messages/'+id+'/snooze');
  if (r?.ok) {
    toast('Unsnoozed','success');
    if (S.currentFolder==='snoozed') { S.messages=S.messages.filter(m=>m.id!==id); renderMessageList(); if(S.currentMessage?.id===id) resetDetail(); }
    loadFolders();
  } else toast('Failed','error');
}

function resetDetail() {
  S.currentMessage=null;S.selectedMessageId=null;
  document.getElementById('message-detail').innerHTML=`<div class="no-message">
    <svg viewBox="0 0 24 24"><path d="M20 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z"/></svg>
    <h3>Select a message</h3><p>Choose a message to read it</p></div>`;
  updateRibbonState();
}

// ── Home ribbon ─────────────────────────────────────────────────────────────
// Actions act on the multi-selection (SEL.ids) when present, otherwise on the
// open message (S.selectedMessageId). Move reuses the shared context menu
// (#ctx-menu), positioned at the click point by showCtxMenu().
function ribbonAct(action) {
  const sel = SEL.ids.size ? [...SEL.ids] : (S.selectedMessageId ? [S.selectedMessageId] : []);
  switch (action) {
    case 'delete': SEL.ids.size ? bulkDelete() : deleteMessage(S.selectedMessageId); break;
    case 'archive': {
      const arc = S.folders.find(f=>f.folder_type==='archive'&&!f.is_hidden);
      if (!arc) { toast('No Archive folder','error'); break; }
      sel.forEach(id=>moveMessage(id, arc.id, true));
      break;
    }
    case 'reply': openReplyTo(S.selectedMessageId); break;
    case 'replyall': openReplyAll(S.selectedMessageId); break;
    case 'flag': sel.forEach(id=>toggleStar(id)); break;
    case 'snooze': snoozeMessage(S.selectedMessageId); break;
    case 'markallread': markFolderAllRead(S.currentFolder); break;
    case 'print': window.print(); break;
    case 'rules': openSettings(); showSettingsTab('rules'); break;
  }
}

// "Move" target list — same folder set as the message context menu's "Move to".
function ribbonMoveMenu(e) {
  e.stopPropagation();
  const otherFolders = S.folders.filter(f=>!f.is_hidden&&String(f.id)!==String(S.currentFolder)).slice(0,16);
  if (!otherFolders.length) return;
  const moveItems = otherFolders.map(f=>
    `<div class="ctx-item ctx-sub-item" onclick="ribbonMoveTo(${f.id});closeMenu()">${esc(f.name)}</div>`).join('');
  showCtxMenu(e, `
    <div class="ctx-item ctx-has-sub">📂 Move to
      <span class="ctx-sub-arrow">›</span>
      <div class="ctx-submenu">${moveItems}</div>
    </div>`);
}

function ribbonMoveTo(folderId) {
  const ids = SEL.ids.size ? [...SEL.ids] : [S.selectedMessageId];
  ids.forEach(id=>moveMessage(id, folderId, true));
}

// Reply-all CC = original's full recipient set (to + cc), deduped, minus the
// signed-in account's own address so the sender isn't cc'd on their own reply.
async function openReplyAll(msgId) {
  let msg=(S.currentMessage?.id===msgId)?S.currentMessage:S.messages.find(m=>m.id===msgId);
  if (!msg) return;
  if (msg.body_html===undefined && msg.body_text===undefined) {
    // Same fetch-as-need as openReplyTo: list summaries have no body fields.
    const full = await api('GET', '/messages/'+msgId);
    if (!full) return;
    msg = full;
  }
  openCompose({
    mode:'reply', replyId:msgId, title:'Reply All',
    accountId: msg.account_id||null,
    subject:msg.subject&&!msg.subject.startsWith('Re:')?'Re: '+msg.subject:(msg.subject||''),
    quoteHtml:`<div><br></div><div class="quote-divider">—— Original message ——</div><blockquote>${quotedBodyHTML(msg)}</blockquote>`,
  });
  addTag('compose-to', msg.from_email||'');
  const acc = S.accounts.find(a=>a.id===(msg.account_id||0));
  const own = acc ? acc.email_address.toLowerCase() : '';
  const cc=[]; const seen=new Set();
  for (const raw of [...(msg.to||'').split(','), ...(msg.cc||'').split(',')]) {
    const a=raw.trim();
    if (!a) continue;
    const k=a.toLowerCase();
    if (k===own || seen.has(k)) continue;
    seen.add(k); cc.push(a);
  }
  if (cc.length) { showCCRow(); cc.forEach(a=>addTag('compose-cc-tags', a)); }
}

function updateRibbonState() {
  const ribbon=document.getElementById('ribbon');
  if (!ribbon) return;
  const hasMsg = SEL.ids.size || S.selectedMessageId;
  const single = !SEL.ids.size && !!S.selectedMessageId; // reply/replyall/snooze/print need exactly one open message
  const set = (id, dis) => { const el=ribbon.querySelector('#'+id); if (el) el.disabled = dis; };
  set('rb-delete', !hasMsg);
  set('rb-archive', !hasMsg);
  set('rb-reply', !single);
  set('rb-replyall', !single);
  set('rb-move', !hasMsg);
  set('rb-flag', !hasMsg);
  set('rb-snooze', !single);
  set('rb-print', !single);
  set('rb-markallread', !/^\d+$/.test(String(S.currentFolder)));
}

function formatSize(b){if(!b)return'';if(b<1024)return b+' B';if(b<1048576)return Math.round(b/1024)+' KB';return(b/1048576).toFixed(1)+' MB';}

// ── Compose ────────────────────────────────────────────────────────────────
let composeAttachments=[];

function populateComposeFrom(preferAccountId) {
  const sel=document.getElementById('compose-from');
  if(!sel) return;
  sel.innerHTML=S.accounts.map(a=>`<option value="${a.id}">${esc(a.display_name||a.email_address)} &lt;${esc(a.email_address)}&gt;</option>`).join('');
  // Default to the account of the currently viewed folder, or explicitly passed account
  if (preferAccountId) {
    sel.value = String(preferAccountId);
  } else if (S.currentFolder && S.currentFolder !== 'unified' && S.currentFolder !== 'starred') {
    const folder = S.folders.find(f => f.id === S.currentFolder);
    if (folder) sel.value = String(folder.account_id);
  }
}

function openCompose(opts={}) {
  S.composeMode=opts.mode||'new'; S.composeReplyToId=opts.replyId||null;
  composeAttachments=[];
  document.getElementById('compose-title').textContent=opts.title||'New Message';
  document.getElementById('compose-minimised-label').textContent=opts.title||'New Message';
  // Clear tag containers and re-init
  ['compose-to','compose-cc-tags','compose-bcc-tags'].forEach(id=>{
    const c=document.getElementById(id);
    if(c){ c.innerHTML=''; initTagField(id); }
  });
  document.getElementById('compose-subject').value=opts.subject||'';
  document.getElementById('cc-row').style.display='none';
  document.getElementById('bcc-row').style.display='none';
  document.getElementById('cc-toggle-btn').style.display='';
  document.getElementById('bcc-toggle-btn').style.display='';
  populateComposeFrom(opts.accountId||null);
  const editor=document.getElementById('compose-editor');
  const fromAccountId=parseInt(document.getElementById('compose-from')?.value||0);
  const forReply=S.composeMode!=='new';
  const sig=opts.skipSignature?'':signatureBlockHTML(fromAccountId, forReply);
  // Signature goes between the typing area and the quoted original message (opts.quoteHtml),
  // not after it — otherwise it'd end up below everything the sender quoted back.
  editor.innerHTML=(opts.body||'') + sig + (opts.quoteHtml||'');
  S.draftDirty=false;
  S.draftId=''; // this compose session's autosaved draft id, once one exists (provider-specific)
  updateAttachList();
  showCompose();
  setTimeout(()=>{ const inp=document.querySelector('#compose-to .tag-input'); if(inp) inp.focus(); },80);
  startDraftAutosave();
}

function showCompose() {
  const d=document.getElementById('compose-dialog');
  const m=document.getElementById('compose-minimised');
  d.style.display='flex';
  m.style.display='none';
  S.composeVisible=true; S.composeMinimised=false;
  initComposeDragDrop();
}

function minimizeCompose() {
  document.getElementById('compose-dialog').style.display='none';
  document.getElementById('compose-minimised').style.display='flex';
  S.composeMinimised=true;
}

function restoreCompose() {
  showCompose();
}

function closeCompose(skipCheck) {
  // Only interrupt closing when there's something to decide about: unsaved edits, or a
  // draft that autosave already wrote to the server (which "keep editing" should leave
  // in place, but a bare close-without-asking would silently orphan).
  if (!skipCheck && (S.draftDirty || S.draftId)) {
    const el = document.getElementById('draft-close-confirm');
    const cancelBtn = document.getElementById('draft-close-cancel');
    const deleteBtn = document.getElementById('draft-close-delete');
    const saveBtn   = document.getElementById('draft-close-save');
    el.classList.add('open');
    const cleanup = () => { el.classList.remove('open'); cancelBtn.onclick=null; deleteBtn.onclick=null; saveBtn.onclick=null; };
    cancelBtn.onclick = () => { cleanup(); }; // keep editing — compose panel stays open
    deleteBtn.onclick = async () => { cleanup(); await discardDraft(); _closeCompose(); };
    saveBtn.onclick   = async () => { cleanup(); await saveDraft(); _closeCompose(); };
    return;
  }
  _closeCompose();
}

function _closeCompose() {
  document.getElementById('compose-dialog').style.display='none';
  document.getElementById('compose-minimised').style.display='none';
  clearDraftAutosave();
  S.composeVisible=false; S.composeMinimised=false; S.draftDirty=false;
}

function showCCRow()  { document.getElementById('cc-row').style.display='flex'; document.getElementById('cc-toggle-btn').style.display='none'; }
function showBCCRow() { document.getElementById('bcc-row').style.display='flex'; document.getElementById('bcc-toggle-btn').style.display='none'; }

function openReply() { if (S.currentMessage) openReplyTo(S.currentMessage.id); }

async function openReplyTo(msgId) {
  let msg=(S.currentMessage?.id===msgId)?S.currentMessage:S.messages.find(m=>m.id===msgId);
  if (!msg) return;
  if (msg.body_html===undefined && msg.body_text===undefined) {
    // Came from the message-list summary (MessageSummary has no body fields at all) — e.g.
    // the context-menu "Reply" on a message that was never opened. Fetch the real thing so
    // the quote isn't blank.
    const full = await api('GET', '/messages/'+msgId);
    if (!full) return;
    msg = full;
  }
  openCompose({
    mode:'reply', replyId:msgId, title:'Reply',
    accountId: msg.account_id||null,
    subject:msg.subject&&!msg.subject.startsWith('Re:')?'Re: '+msg.subject:(msg.subject||''),
    quoteHtml:`<div><br></div><div class="quote-divider">—— Original message ——</div><blockquote>${quotedBodyHTML(msg)}</blockquote>`,
  });
  addTag('compose-to', msg.from_email||'');
}

function openForward() {
  if (!S.currentMessage) return;
  const msg=S.currentMessage;
  openCompose({
    mode:'forward', forwardId:msg.id, title:'Forward',
    accountId: msg.account_id||null,
    subject:'Fwd: '+(msg.subject||''),
    quoteHtml:`<div><br></div><div class="quote-divider">—— Forwarded message ——<br>From: ${esc(msg.from_email||'')}</div><blockquote>${quotedBodyHTML(msg)}</blockquote>`,
  });
}

// Opens a fresh compose with the current message attached as .eml. Once open, more emails
// can be attached the same way — via drag-and-drop from the list, or the message context
// menu's "Attach as .eml" entry — since attaching isn't tied to a dedicated compose mode.
function openForwardAsAttachment() {
  if (!S.currentMessage) return;
  const msg=S.currentMessage;
  openCompose({
    mode:'forward', title:'Forward as Attachment',
    accountId: msg.account_id||null,
    subject:'Fwd: '+(msg.subject||''),
    body:'',
  });
  attachMessageAsEML(msg.id);
}

function sanitizeSubject(s){return s.replace(/[/\\:*?"<>|]/g,'_').slice(0,60)||'message';}

// ── Contact autocomplete cache (shared across all tag fields) ──────────────
let contactsCache=null, contactsCachePromise=null;
function ensureContactsCache() {
  if (contactsCache) return Promise.resolve(contactsCache);
  if (!contactsCachePromise) {
    contactsCachePromise = api('GET','/contacts').then(d=>contactsCache=d||[]).catch(()=>contactsCache=[]);
  }
  return contactsCachePromise;
}

// ── Email Tag Input ────────────────────────────────────────────────────────
function initTagField(containerId) {
  const container=document.getElementById(containerId);
  if (!container) return;
  // Remove any existing input first
  const old=container.querySelector('.tag-input');
  if(old) old.remove();

  const inp=document.createElement('input');
  inp.type='text';
  inp.className='tag-input';
  inp.placeholder=containerId==='compose-to'?'recipient@example.com':'';
  inp.setAttribute('autocomplete','off');
  inp.setAttribute('spellcheck','false');
  container.appendChild(inp);

  let suggestBox=null, suggestItems=[], suggestIndex=-1;
  const closeSuggest = () => { if(suggestBox){suggestBox.remove(); suggestBox=null;} suggestItems=[]; suggestIndex=-1; };
  const pickSuggest = (c) => { addTag(containerId, c.email); inp.value=''; closeSuggest(); };
  const renderSuggest = () => {
    if(suggestBox) suggestBox.remove();
    if(!suggestItems.length) { suggestBox=null; return; }
    suggestBox=document.createElement('div');
    suggestBox.className='contact-suggest';
    suggestItems.forEach((c,i)=>{
      const row=document.createElement('div');
      row.className='contact-suggest-row'+(i===suggestIndex?' active':'');
      row.innerHTML=`<span class="contact-suggest-name">${esc(c.display_name||c.email)}</span><span class="contact-suggest-email">${esc(c.email)}</span>`;
      row.onmousedown=e=>{ e.preventDefault(); pickSuggest(c); };
      suggestBox.appendChild(row);
    });
    container.parentElement.appendChild(suggestBox);
  };

  const commit = () => {
    const v=inp.value.trim().replace(/[,;\s]+$/,'');
    if(v){ addTag(containerId,v); inp.value=''; }
    closeSuggest();
  };

  inp.addEventListener('input', async ()=>{
    S.draftDirty=true;
    const v=inp.value.trim().toLowerCase();
    if(!v){ closeSuggest(); return; }
    const contacts=await ensureContactsCache();
    const existing=new Set(getTagValues(containerId).map(e=>e.toLowerCase()));
    suggestItems=contacts.filter(c=>c.email && !existing.has(c.email.toLowerCase()) &&
      (c.email.toLowerCase().includes(v) || (c.display_name||'').toLowerCase().includes(v))).slice(0,6);
    suggestIndex=-1;
    renderSuggest();
  });

  inp.addEventListener('keydown', e=>{
    if(suggestBox && suggestItems.length && (e.key==='ArrowDown'||e.key==='ArrowUp')) {
      e.preventDefault();
      suggestIndex = e.key==='ArrowDown' ? Math.min(suggestIndex+1, suggestItems.length-1) : Math.max(suggestIndex-1, -1);
      renderSuggest();
      return;
    }
    if(e.key==='Escape' && suggestBox) { e.preventDefault(); closeSuggest(); return; }
    if(e.key==='Enter'||e.key===','||e.key===';') {
      e.preventDefault();
      if(suggestBox && suggestIndex>=0) pickSuggest(suggestItems[suggestIndex]);
      else commit();
    } else if(e.key===' ') {
      // Space commits only if value looks like an email
      const v=inp.value.trim();
      if(v && /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v)) { e.preventDefault(); commit(); }
    } else if(e.key==='Backspace'&&!inp.value) {
      const tags=container.querySelectorAll('.email-tag');
      if(tags.length) tags[tags.length-1].remove();
    }
    S.draftDirty=true;
  });
  inp.addEventListener('blur', commit);
  container.addEventListener('click', e=>{ if(e.target===container||e.target.tagName==='LABEL') inp.focus(); else if(!e.target.closest('.email-tag')) inp.focus(); });
}

function addTag(containerId, value) {
  if (!value) return;
  const container=document.getElementById(containerId);
  if (!container) return;
  const isValid=/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value);
  const tag=document.createElement('span');
  tag.className='email-tag'+(isValid?'':' invalid');
  tag.dataset.email=value;
  const label=document.createElement('span');
  label.textContent=value;
  const remove=document.createElement('button');
  remove.innerHTML='×'; remove.className='tag-remove'; remove.type='button';
  remove.onclick=e=>{e.stopPropagation();tag.remove();S.draftDirty=true;};
  tag.appendChild(label); tag.appendChild(remove);
  const inp=container.querySelector('.tag-input');
  container.insertBefore(tag, inp||null);
  S.draftDirty=true;
}

function getTagValues(containerId) {
  return Array.from(document.querySelectorAll('#'+containerId+' .email-tag'))
    .map(t=>t.dataset.email||t.querySelector('span')?.textContent||'').filter(Boolean);
}

// ── Draft autosave ─────────────────────────────────────────────────────────
function startDraftAutosave() {
  clearDraftAutosave();
  S.draftTimer=setInterval(()=>{ if(S.draftDirty) saveDraft(true); }, 60000);
  const editor=document.getElementById('compose-editor');
  if(editor) editor.oninput=()=>S.draftDirty=true;
}

function clearDraftAutosave() {
  if(S.draftTimer){ clearInterval(S.draftTimer); S.draftTimer=null; }
}

async function saveDraft(silent) {
  S.draftDirty=false;
  const accountId=parseInt(document.getElementById('compose-from')?.value||0);
  if(!accountId){ if(!silent) toast('Draft saved locally','success'); return; }
  const editor=document.getElementById('compose-editor');
  const meta={
    account_id:accountId,
    to:getTagValues('compose-to'),
    cc:getTagValues('compose-cc-tags'),
    bcc:getTagValues('compose-bcc-tags'),
    subject:document.getElementById('compose-subject').value,
    body_html:restoreBlockedImages(editor.innerHTML.trim()),
    body_text:editor.innerText.trim(),
    draft_id:S.draftId||'',
  };
  const r=await api('POST','/draft',meta);
  if(r?.ok){ S.draftId=r.draft_id||S.draftId; if(!silent) toast('Draft saved','success'); else toast('Draft auto-saved to server','success'); loadFolders(); }
  else if(!silent) toast(r?.error||'Draft save failed','error');
}

// Deletes the draft that autosave already wrote to the server for this compose session.
// No-op if nothing was ever saved.
async function discardDraft() {
  if (!S.draftId) return;
  const accountId=parseInt(document.getElementById('compose-from')?.value||0);
  if (!accountId) return;
  await api('POST','/draft/discard',{account_id:accountId, draft_id:S.draftId});
  S.draftId='';
  loadFolders();
}

// ── Compose formatting ─────────────────────────────────────────────────────
function execFmt(cmd,val) { document.getElementById('compose-editor').focus(); document.execCommand(cmd,false,val||null); }

// Opening the native <select> dropdown steals focus and clears the editor's text
// selection before onchange fires, so fontName silently no-ops — save the range on
// mousedown (before the dropdown opens) and restore it before applying the font.
let savedComposeRange = null;
function saveEditorRange() {
  const sel = window.getSelection();
  savedComposeRange = sel.rangeCount ? sel.getRangeAt(0).cloneRange() : null;
}
function applyFontFromSelect(select) {
  const editor = document.getElementById('compose-editor');
  const sel = window.getSelection();
  editor.focus();
  if (savedComposeRange) { sel.removeAllRanges(); sel.addRange(savedComposeRange); }
  if (select.value) document.execCommand('fontName', false, select.value);
}
function triggerAttach() { document.getElementById('compose-attach-input').click(); }
function handleAttachFiles(input) { for(const file of input.files) composeAttachments.push({file,name:file.name,size:file.size}); input.value=''; updateAttachList(); S.draftDirty=true; }
function removeAttachment(i) {
  composeAttachments.splice(i,1); updateAttachList();
}
function updateAttachList() {
  const el=document.getElementById('compose-attach-list');
  if(!composeAttachments.length){el.innerHTML='';return;}
  el.innerHTML=composeAttachments.map((a,i)=>`<div class="attachment-chip">
    ${a.isForward?'✉️':'📎'} <span>${esc(a.name)}</span>
    <span style="color:var(--muted);font-size:10px">${a.size?formatSize(a.size):''}</span>
    <button onclick="removeAttachment(${i})" class="tag-remove" type="button">×</button>
  </div>`).join('');
}

// Attaches an existing email as a .eml file to whichever compose is currently open — used
// by "Forward as Attachment", the message context menu, and dragging a row onto compose.
// Multiple messages can be attached this way to the same compose session.
function attachMessageAsEML(msgId) {
  if (!S.composeVisible) { toast('Open a message to compose first','error'); return; }
  if (composeAttachments.some(a=>a.isForward && a.msgId===msgId)) { toast('Already attached','info'); return; }
  const msg = S.messages.find(m=>m.id===msgId) || (S.currentMessage?.id===msgId?S.currentMessage:null);
  composeAttachments.push({name: sanitizeSubject(msg?.subject||'message')+'.eml', size:0, isForward:true, msgId});
  updateAttachList();
  S.draftDirty=true;
  toast('Email attached','success');
}

// ── Compose drag-and-drop attachments ──────────────────────────────────────
function initComposeDragDrop() {
  const dialog=document.getElementById('compose-dialog');
  if(!dialog) return;
  dialog.addEventListener('dragover', e=>{
    e.preventDefault(); e.stopPropagation();
    dialog.classList.add('drag-over');
  });
  dialog.addEventListener('dragleave', e=>{
    if(!dialog.contains(e.relatedTarget)) dialog.classList.remove('drag-over');
  });
  dialog.addEventListener('drop', e=>{
    e.preventDefault(); e.stopPropagation();
    dialog.classList.remove('drag-over');
    if(e.dataTransfer?.files?.length){
      for(const file of e.dataTransfer.files) composeAttachments.push({file,name:file.name,size:file.size});
      updateAttachList(); S.draftDirty=true;
      toast(`${e.dataTransfer.files.length} file(s) attached`,'success');
      return;
    }
    // A message row dragged from the list (handleMsgDragStart sets its id as text/plain).
    const msgId=parseInt(e.dataTransfer?.getData('text/plain'));
    if (msgId && (S.messages.some(m=>m.id===msgId) || S.currentMessage?.id===msgId)) {
      attachMessageAsEML(msgId);
    }
  });
}

async function sendMessage() {
  const accountId=parseInt(document.getElementById('compose-from')?.value||0);
  const to=getTagValues('compose-to');
  if(!accountId||!to.length){toast('From account and To address required','error');return;}
  const editor=document.getElementById('compose-editor');
  const bodyHTML=restoreBlockedImages(editor.innerHTML.trim()), bodyText=editor.innerText.trim();
  const btn=document.getElementById('send-btn');
  btn.disabled=true; btn.textContent='Sending…';

  const endpoint=S.composeMode==='reply'?'/reply'
    :S.composeMode==='forward'?'/forward'
    :'/send';

  const meta={
    account_id:accountId, to,
    cc:getTagValues('compose-cc-tags'),
    bcc:getTagValues('compose-bcc-tags'),
    subject:document.getElementById('compose-subject').value,
    body_text:bodyText, body_html:bodyHTML,
    in_reply_to_id:S.composeMode==='reply'?S.composeReplyToId:0,
    forward_from_ids:composeAttachments.filter(a=>a.isForward).map(a=>a.msgId),
  };

  let r;
  const hasRealFiles = composeAttachments.some(a => a.file instanceof Blob);
  const needsFormData = hasRealFiles;
  if(needsFormData){
    const fd=new FormData();
    fd.append('meta', JSON.stringify(meta));
    for(const a of composeAttachments){
      if(a.file instanceof Blob){        // only append real File/Blob objects
        fd.append('file', a.file, a.name);
      }
      // isForward placeholders are intentionally skipped — the EML is fetched server-side
    }
    try{
      const resp=await fetch('/api'+endpoint,{method:'POST',body:fd});
      r=await resp.json();
    }catch(e){ r={error:String(e)}; }
  } else {
    r=await api('POST',endpoint,meta);
  }

  btn.disabled=false; btn.textContent='Send';
  if(r?.ok){
    toast('Message sent!','success');
    clearDraftAutosave();
    await discardDraft(); // remove the autosaved Drafts-folder copy now that it's actually sent
    _closeCompose();
    // Refresh after a short delay so the syncer has time to pick up the sent message
    setTimeout(async () => { await loadFolders(); await loadMessages(); }, 2500);
  }
  else toast(r?.error||'Send failed','error');
}

function openSendLater() {
  const accountId=parseInt(document.getElementById('compose-from')?.value||0);
  const to=getTagValues('compose-to');
  if(!accountId||!to.length){toast('From account and To address required','error');return;}
  if (composeAttachments.some(a => a.file instanceof Blob)) {
    toast("Send later doesn't support file attachments yet — forwarded messages are fine",'error');
    return;
  }
  const editor=document.getElementById('compose-editor');
  const meta={
    account_id:accountId, to,
    cc:getTagValues('compose-cc-tags'),
    bcc:getTagValues('compose-bcc-tags'),
    subject:document.getElementById('compose-subject').value,
    body_text:editor.innerText.trim(), body_html:restoreBlockedImages(editor.innerHTML.trim()),
    forward_from_ids:composeAttachments.filter(a=>a.isForward).map(a=>a.msgId),
  };
  inlineDateTimePrompt('Send at:', async (iso) => {
    meta.send_at = iso;
    const r = await api('POST','/send-later', meta);
    if (r?.ok) {
      toast('Message scheduled','success');
      clearDraftAutosave();
      await discardDraft();
      _closeCompose();
    } else toast(r?.error||'Failed to schedule','error');
  }, 'Schedule');
}

async function showScheduledSends() {
  openModal('scheduled-sends-modal');
  const list = document.getElementById('scheduled-sends-list');
  list.innerHTML = '<div class="spinner" style="margin:30px auto"></div>';
  const items = await api('GET','/scheduled-sends');
  if (!items || !items.length) {
    list.innerHTML = '<p style="color:var(--muted);font-size:13px;text-align:center;padding:20px 0">No scheduled sends.</p>';
    return;
  }
  list.innerHTML = items.map(s => `
    <div class="acct-row" style="align-items:flex-start">
      <div style="flex:1;min-width:0">
        <div style="font-size:13px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${s.paused ? '<span style="color:var(--muted);font-style:italic">Paused — </span>' : ''}${esc(s.subject||'(no subject)')}</div>
        <div style="font-size:11px;color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap">To: ${esc((s.to||[]).join(', '))}</div>
        <div style="font-size:11px;color:var(--accent)">Sends ${formatDate(s.send_at)}</div>
      </div>
      <div style="display:flex;gap:6px;flex-shrink:0">
        <button class="btn-secondary" style="font-size:11px" onclick="rescheduleSend(${s.id})">${s.paused ? 'Resume' : 'Pause'}</button>
        <button class="btn-secondary" style="font-size:11px" onclick="editSendTime(${s.id})">Edit time</button>
        <button class="btn-secondary" style="font-size:11px" onclick="cancelScheduledSend(${s.id})">Cancel</button>
      </div>
    </div>`).join('');
}

function editSendTime(id) {
  inlineDateTimePrompt('New send time:', async (iso) => {
    const r = await api('PUT','/scheduled-sends/'+id, {send_at: iso});
    if (r?.ok) { toast('Send time updated','success'); showScheduledSends(); }
    else toast(r?.error||'Failed to update send time','error');
  }, 'Set');
}

async function rescheduleSend(id) {
  const items = await api('GET','/scheduled-sends');
  const s = (items||[]).find(x => x.id === id);
  const r = await api('PUT','/scheduled-sends/'+id, {paused: !s?.paused});
  if (r?.ok) { toast(s?.paused ? 'Send resumed' : 'Send paused','success'); showScheduledSends(); }
  else toast(r?.error||'Failed','error');
}

async function cancelScheduledSend(id) {
  const r = await api('DELETE','/scheduled-sends/'+id);
  if (r?.ok) { toast('Scheduled send cancelled','success'); showScheduledSends(); }
  else toast('Cancel failed','error');
}

// ── Compose drag + all-edge resize ─────────────────────────────────────────
function saveComposeGeometry(dlg) {
  const r = dlg.getBoundingClientRect();
  document.cookie = `compose_geo=${JSON.stringify({l:Math.round(r.left),t:Math.round(r.top),w:Math.round(r.width),h:Math.round(r.height)})};path=/;max-age=31536000`;
}

function loadComposeGeometry(dlg) {
  try {
    const m = document.cookie.match(/compose_geo=([^;]+)/);
    if (!m) return false;
    const g = JSON.parse(decodeURIComponent(m[1]));
    if (!g.w||!g.h) return false;
    const maxL = window.innerWidth  - Math.max(360, g.w);
    const maxT = window.innerHeight - Math.max(280, g.h);
    dlg.style.left   = Math.max(0, Math.min(g.l, maxL)) + 'px';
    dlg.style.top    = Math.max(0, Math.min(g.t, maxT)) + 'px';
    dlg.style.width  = Math.max(360, g.w) + 'px';
    dlg.style.height = Math.max(280, g.h) + 'px';
    dlg.style.right  = 'auto'; dlg.style.bottom = 'auto';
    const editor = document.getElementById('compose-editor');
    if (editor) editor.style.height = (Math.max(280,g.h) - 242) + 'px';
    return true;
  } catch(e) { return false; }
}

function initComposeDragResize() {
  const dlg=document.getElementById('compose-dialog');
  if(!dlg) return;

  // Restore saved position/size, or fall back to default bottom-right
  if (!loadComposeGeometry(dlg)) {
    dlg.style.right='24px'; dlg.style.bottom='20px';
    dlg.style.left='auto';  dlg.style.top='auto';
  }

  // Drag by header
  const header=document.getElementById('compose-drag-handle');
  if(header) {
    let ox,oy,startL,startT;
    header.addEventListener('mousedown', e=>{
      if(e.target.closest('button')) return;
      const r=dlg.getBoundingClientRect();
      ox=e.clientX; oy=e.clientY; startL=r.left; startT=r.top;
      dlg.style.left=startL+'px'; dlg.style.top=startT+'px';
      dlg.style.right='auto'; dlg.style.bottom='auto';
      const mm=ev=>{
        dlg.style.left=Math.max(0,Math.min(window.innerWidth-dlg.offsetWidth, startL+(ev.clientX-ox)))+'px';
        dlg.style.top= Math.max(0,Math.min(window.innerHeight-30,         startT+(ev.clientY-oy)))+'px';
      };
      const mu=()=>{ document.removeEventListener('mousemove',mm); document.removeEventListener('mouseup',mu); saveComposeGeometry(dlg); };
      document.addEventListener('mousemove',mm);
      document.addEventListener('mouseup',mu);
      e.preventDefault();
    });
  }

  // Resize handles
  dlg.querySelectorAll('.compose-resize').forEach(handle=>{
    const dir=handle.dataset.dir;
    handle.addEventListener('mousedown', e=>{
      const rect=dlg.getBoundingClientRect();
      const startX=e.clientX,startY=e.clientY;
      const startW=rect.width,startH=rect.height,startL=rect.left,startT=rect.top;
      const mm=ev=>{
        let w=startW,h=startH,l=startL,t=startT;
        const dx=ev.clientX-startX, dy=ev.clientY-startY;
        if(dir.includes('e')) w=Math.max(360,startW+dx);
        if(dir.includes('w')){ w=Math.max(360,startW-dx); l=startL+startW-w; }
        if(dir.includes('s')) h=Math.max(280,startH+dy);
        if(dir.includes('n')){ h=Math.max(280,startH-dy); t=startT+startH-h; }
        dlg.style.width=w+'px'; dlg.style.height=h+'px';
        dlg.style.left=l+'px';  dlg.style.top=t+'px';
        dlg.style.right='auto'; dlg.style.bottom='auto';
        const editor=document.getElementById('compose-editor');
        if(editor) editor.style.height=(h-242)+'px';
      };
      const mu=()=>{ document.removeEventListener('mousemove',mm); document.removeEventListener('mouseup',mu); saveComposeGeometry(dlg); };
      document.addEventListener('mousemove',mm);
      document.addEventListener('mouseup',mu);
      e.preventDefault();
    });
  });
}

// ── Settings ───────────────────────────────────────────────────────────────
async function openSettings() {
  openModal('settings-modal');
  showSettingsTab('accounts');
  loadSyncInterval();
  loadRemoteImagePolicy();
  loadRemoteWhitelist();
  loadNotificationSetting();
  renderMFAPanel();
  loadIPRules();
  populateSettingsAccountSelects();
  // Pre-fill profile fields with current values
  const me = await api('GET', '/me');
  if (me) {
    document.getElementById('profile-username').placeholder = me.username || 'New username';
    document.getElementById('profile-email').placeholder = me.email || 'New email';
  }
}

// ── Settings: tabs ───────────────────────────────────────────────────────
let _settingsTabsLoaded = {};

function showSettingsTab(tab) {
  document.querySelectorAll('.settings-nav button').forEach(b => {
    const active = b.dataset.tab === tab;
    b.classList.toggle('active', active);
    b.setAttribute('aria-selected', String(active));
  });
  document.querySelectorAll('.settings-panel').forEach(p => p.classList.toggle('active', p.dataset.tab === tab));
  if (_settingsTabsLoaded[tab]) return;
  _settingsTabsLoaded[tab] = true;
  if (tab === 'accounts') { renderAccountsSettingsList(); }
  else if (tab === 'rules') { addRuleConditionRow(); loadRules(); }
  else if (tab === 'signatures') { renderSignaturesList(); renderSignatureDefaultsForm(); }
  else if (tab === 'certs') { loadCerts(); }
}

function accountOptionsHTML() {
  return S.accounts.map(a => `<option value="${a.id}">${esc(a.display_name||a.email_address)} &lt;${esc(a.email_address)}&gt;</option>`).join('');
}

function populateSettingsAccountSelects() {
  ['rules-account-select', 'certs-account-select', 'sig-defaults-account-select'].forEach(id => {
    const sel = document.getElementById(id);
    if (sel && !sel.options.length) sel.innerHTML = accountOptionsHTML();
  });
}

// ── Settings: Rules ──────────────────────────────────────────────────────
function addRuleConditionRow() {
  const wrap = document.getElementById('rule-conditions');
  const row = document.createElement('div');
  row.className = 'rule-condition-row';
  row.style.cssText = 'display:flex;gap:6px;margin-bottom:8px;align-items:center';
  row.innerHTML = `
    <select class="rc-field" style="flex:1">
      <option value="from">From</option><option value="to">To</option><option value="subject">Subject</option>
      <option value="body">Body</option><option value="has_attachment">Has attachment</option><option value="recipient_type">Recipient type</option>
    </select>
    <select class="rc-op" style="flex:1"><option value="contains">contains</option><option value="equals">equals</option><option value="starts_with">starts with</option></select>
    <input class="rc-value" type="text" placeholder="value" style="flex:1">
    <button class="icon-btn" onclick="this.closest('.rule-condition-row').remove()" title="Remove">&times;</button>`;
  wrap.appendChild(row);
}

function updateRuleActionFields() {
  const action = document.getElementById('rule-action').value;
  const valField = document.getElementById('rule-action-value-field');
  const valLabel = valField.querySelector('label');
  const valInput = document.getElementById('rule-action-value');
  const bodyField = document.getElementById('rule-autoreply-body-field');
  bodyField.style.display = action === 'auto_reply' ? 'block' : 'none';
  if (action === 'move_to_folder') { valField.style.display = ''; valLabel.textContent = 'Folder name'; valInput.placeholder = 'folder name'; }
  else if (action === 'forward') { valField.style.display = ''; valLabel.textContent = 'Forward to'; valInput.placeholder = 'someone@example.com'; }
  else if (action === 'auto_reply') { valField.style.display = ''; valLabel.textContent = 'Reply subject'; valInput.placeholder = 'Out of office'; }
  else { valField.style.display = 'none'; }
}

let _rulesCache = [];

async function loadRules() {
  const accountId = parseInt(document.getElementById('rules-account-select')?.value || 0);
  const el = document.getElementById('rules-list');
  if (!accountId) { el.innerHTML = '<p style="color:var(--muted);font-size:13px">Connect an account first.</p>'; return; }
  const rules = await api('GET', '/rules?account_id=' + accountId);
  _rulesCache = rules || [];
  if (!rules || !rules.length) { el.innerHTML = '<p style="color:var(--muted);font-size:13px">No rules yet.</p>'; return; }
  el.innerHTML = `<table class="data-table">
    <thead><tr><th>Name</th><th>Priority</th><th>Action</th><th>Active</th><th></th></tr></thead>
    <tbody>${rules.map(r => `
      <tr>
        <td style="font-weight:500">${esc(r.name)}</td>
        <td style="color:var(--muted)">${r.priority}</td>
        <td style="color:var(--muted)">${esc(r.action)}${r.action_value?' → '+esc(r.action_value):''}</td>
        <td><input type="checkbox" ${r.is_active?'checked':''} onchange="toggleRuleActive(${r.id},${accountId},this.checked)"></td>
        <td><button class="btn-danger" style="padding:4px 10px;font-size:12px" onclick="deleteRule(${r.id},${accountId})">Delete</button></td>
      </tr>`).join('')}
    </tbody></table>`;
}

async function saveRule() {
  const accountId = parseInt(document.getElementById('rules-account-select')?.value || 0);
  if (!accountId) { toast('Connect an account first', 'err'); return; }
  const conditions = [...document.querySelectorAll('#rule-conditions .rule-condition-row')].map(row => ({
    field: row.querySelector('.rc-field').value, op: row.querySelector('.rc-op').value, value: row.querySelector('.rc-value').value.trim(),
  })).filter(c => c.value);
  if (!conditions.length) { toast('Add at least one condition', 'err'); return; }
  const action = document.getElementById('rule-action').value;
  const body = {
    account_id: accountId,
    name: document.getElementById('rule-name').value.trim() || 'Untitled rule',
    priority: parseInt(document.getElementById('rule-priority').value) || 0,
    match_type: document.getElementById('rule-match-type').value,
    conditions, action,
    action_value: document.getElementById('rule-action-value').value.trim(),
    action_options: action === 'auto_reply' ? { body: document.getElementById('rule-autoreply-body').value } : {},
    is_active: true,
  };
  const r = await api('POST', '/rules', body);
  if (r && r.ok) { toast('Rule added'); document.getElementById('rule-name').value=''; document.getElementById('rule-conditions').innerHTML=''; addRuleConditionRow(); loadRules(); }
  else toast(r?.error || 'Failed to save rule', 'err');
}

async function toggleRuleActive(id, accountId, checked) {
  const rule = _rulesCache.find(r => r.id === id);
  if (!rule) return;
  rule.is_active = checked;
  await api('PUT', '/rules/' + id, rule);
}

async function deleteRule(id, accountId) {
  await api('DELETE', '/rules/' + id + '?account_id=' + accountId);
  loadRules();
}

// ── Settings: Signatures ─────────────────────────────────────────────────
let editingSignatureId = null;

function renderSignaturesList() {
  const el = document.getElementById('signatures-list');
  if (!S.signatures.length) { el.innerHTML = '<p style="color:var(--muted);font-size:13px">No signatures yet.</p>'; return; }
  el.innerHTML = S.signatures.map(s => `
    <div class="settings-group" style="padding-bottom:12px;margin-bottom:12px">
      <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
        <b>${esc(s.name)}</b>
        <div style="display:flex;gap:6px">
          <button class="btn-secondary" style="padding:4px 10px;font-size:12px" onclick="editSignature(${s.id})">Edit</button>
          <button class="btn-danger" style="padding:4px 10px;font-size:12px" onclick="deleteSignature(${s.id})">Delete</button>
        </div>
      </div>
      <div style="font-size:13px;color:var(--text2);background:var(--surface2);padding:8px;border-radius:6px">${s.content_html}</div>
    </div>`).join('');
}

function execSigFmt(cmd, val) { document.getElementById('sig-content').focus(); document.execCommand(cmd, false, val || null); }

function insertSigImage(input) {
  const file = input.files[0];
  if (!file) return;
  if (file.size > 1024 * 1024) { toast('Image too large — keep signature images under 1MB', 'error'); input.value = ''; return; }
  const editor = document.getElementById('sig-content');
  const sel = window.getSelection();
  const range = sel.rangeCount ? sel.getRangeAt(0).cloneRange() : null;
  const reader = new FileReader();
  reader.onload = () => {
    editor.focus();
    if (range) { sel.removeAllRanges(); sel.addRange(range); }
    document.execCommand('insertHTML', false, `<img src="${reader.result}" style="max-width:100%">`);
    input.value = '';
  };
  reader.readAsDataURL(file);
}

function editSignature(id) {
  const sig = S.signatures.find(s => s.id === id);
  if (!sig) return;
  editingSignatureId = id;
  document.getElementById('sig-name').value = sig.name;
  document.getElementById('sig-content').innerHTML = sig.content_html;
  document.getElementById('sig-form-title').textContent = 'Edit Signature';
  document.getElementById('sig-save-btn').textContent = 'Save Changes';
  document.getElementById('sig-cancel-btn').style.display = '';
  document.getElementById('sig-name').scrollIntoView({ block: 'center', behavior: 'smooth' });
}

function cancelSignatureEdit() {
  editingSignatureId = null;
  document.getElementById('sig-name').value = '';
  document.getElementById('sig-content').innerHTML = '';
  document.getElementById('sig-form-title').textContent = 'Add Signature';
  document.getElementById('sig-save-btn').textContent = 'Add Signature';
  document.getElementById('sig-cancel-btn').style.display = 'none';
}

async function saveSignature() {
  const name = document.getElementById('sig-name').value.trim();
  if (!name) { toast('Name required', 'error'); return; }
  const content_html = document.getElementById('sig-content').innerHTML.trim();
  const r = editingSignatureId
    ? await api('PUT', '/signatures/' + editingSignatureId, { name, content_html })
    : await api('POST', '/signatures', { name, content_html });
  if (r && r.ok) {
    toast(editingSignatureId ? 'Signature updated' : 'Signature added', 'success');
    cancelSignatureEdit();
    await loadSignatures(); renderSignaturesList(); renderSignatureDefaultsForm();
  } else toast(r?.error || 'Failed to save', 'error');
}

async function deleteSignature(id) {
  if (editingSignatureId === id) cancelSignatureEdit();
  await api('DELETE', '/signatures/' + id);
  await loadSignatures(); renderSignaturesList(); renderSignatureDefaultsForm();
}

function renderSignatureDefaultsForm() {
  const accountId = parseInt(document.getElementById('sig-defaults-account-select')?.value || 0);
  const el = document.getElementById('sig-defaults-form');
  if (!el) return;
  if (!accountId) { el.innerHTML = '<p style="color:var(--muted);font-size:13px">Connect an account first.</p>'; return; }
  const acc = S.accounts.find(a => a.id === accountId) || {};
  const opts = '<option value="0">None</option>' + S.signatures.map(s => `<option value="${s.id}">${esc(s.name)}</option>`).join('');
  el.innerHTML = `
    <div class="modal-field"><label>Default for new messages</label>
      <select id="sig-default-new">${opts}</select></div>
    <div class="modal-field"><label>Default for replies/forwards</label>
      <select id="sig-default-reply">${opts}</select></div>
    <button class="btn-primary" onclick="saveSignatureDefaults()">Save</button>`;
  document.getElementById('sig-default-new').value = acc.default_signature_new_id || 0;
  document.getElementById('sig-default-reply').value = acc.default_signature_reply_id || 0;
}

async function saveSignatureDefaults() {
  const accountId = parseInt(document.getElementById('sig-defaults-account-select')?.value || 0);
  if (!accountId) return;
  const body = {
    default_new_id: parseInt(document.getElementById('sig-default-new').value) || 0,
    default_reply_id: parseInt(document.getElementById('sig-default-reply').value) || 0,
  };
  const r = await api('PUT', '/accounts/' + accountId + '/signature-defaults', body);
  if (r && r.ok) { toast('Defaults saved'); await loadAccounts(); }
  else toast(r?.error || 'Failed to save', 'err');
}

// ── Settings: Certificates (S/MIME + PGP) ───────────────────────────────
async function loadCerts() {
  const accountId = parseInt(document.getElementById('certs-account-select')?.value || 0);
  if (!accountId) return;
  const [smimeIds, smimeContacts, pgpIds, pgpContacts] = await Promise.all([
    api('GET', '/smime/identity?account_id=' + accountId),
    api('GET', '/smime/contacts'),
    api('GET', '/pgp/identity?account_id=' + accountId),
    api('GET', '/pgp/contacts'),
  ]);
  renderSMIMEIdentities(smimeIds || [], accountId);
  renderSMIMEContacts(smimeContacts || []);
  renderPGPIdentities(pgpIds || [], accountId);
  renderPGPContacts(pgpContacts || []);
}

function renderSMIMEIdentities(ids, accountId) {
  const el = document.getElementById('smime-identity-list');
  if (!ids.length) { el.innerHTML = '<p style="color:var(--muted);font-size:13px">No S/MIME certificates yet. Generate a free self-signed certificate, or import one you already have.</p>'; return; }
  el.innerHTML = ids.map(i => `
    <div class="setting-row">
      <div><div class="setting-label">Certificate</div><div style="font-size:12px;color:var(--muted)">Expires ${new Date(i.not_after).toLocaleDateString()}</div></div>
      <button class="btn-danger" style="padding:4px 10px;font-size:12px" onclick="smimeRemoveIdentity(${i.id},${accountId})">Delete</button>
    </div>`).join('');
}

async function smimeGenerate() {
  const accountId = parseInt(document.getElementById('certs-account-select')?.value || 0);
  if (!accountId) { toast('Connect an account first', 'err'); return; }
  const r = await api('POST', '/smime/identity', { account_id: accountId });
  if (r && r.ok) { toast('Certificate generated'); loadCerts(); }
  else toast(r?.error || 'Failed to generate', 'err');
}

async function smimeImport() {
  const accountId = parseInt(document.getElementById('certs-account-select')?.value || 0);
  const fileInput = document.getElementById('smime-import-file');
  if (!accountId || !fileInput.files.length) { toast('Choose a .p12 file first', 'err'); return; }
  const fd = new FormData();
  fd.append('account_id', accountId);
  fd.append('p12_file', fileInput.files[0]);
  fd.append('p12_password', document.getElementById('smime-import-password').value);
  const resp = await fetch('/api/smime/identity/import?account_id=' + accountId, { method: 'POST', body: fd });
  const r = await resp.json().catch(() => null);
  if (r && r.ok) { toast('Certificate imported'); fileInput.value = ''; loadCerts(); }
  else toast(r?.error || 'Import failed', 'err');
}

async function smimeRemoveIdentity(id, accountId) {
  await api('DELETE', '/smime/identity/' + id + '?account_id=' + accountId);
  loadCerts();
}

function renderSMIMEContacts(contacts) {
  const el = document.getElementById('smime-contacts-list');
  if (!contacts.length) { el.innerHTML = ''; return; }
  el.innerHTML = `<table class="data-table"><thead><tr><th>Email</th><th></th></tr></thead><tbody>${contacts.map(c => `
    <tr><td>${esc(c.email)}</td><td><button class="btn-danger" style="padding:4px 10px;font-size:12px" onclick="smimeRemoveContact(${c.id})">Delete</button></td></tr>`).join('')}</tbody></table>`;
}

async function smimeAddContact() {
  const email = document.getElementById('smime-contact-email').value.trim();
  const fileInput = document.getElementById('smime-contact-file');
  if (!email || !fileInput.files.length) { toast('Email and certificate file required', 'err'); return; }
  const fd = new FormData();
  fd.append('email', email);
  fd.append('cert_file', fileInput.files[0]);
  const resp = await fetch('/api/smime/contacts', { method: 'POST', body: fd });
  const r = await resp.json().catch(() => null);
  if (r && r.ok) { toast('Contact added'); document.getElementById('smime-contact-email').value=''; fileInput.value=''; loadCerts(); }
  else toast(r?.error || 'Failed to add contact', 'err');
}

async function smimeRemoveContact(id) {
  await api('DELETE', '/smime/contacts/' + id);
  loadCerts();
}

function renderPGPIdentities(ids, accountId) {
  const el = document.getElementById('pgp-identity-list');
  if (!ids.length) { el.innerHTML = '<p style="color:var(--muted);font-size:13px">No PGP keys yet. Generate a new keypair, or import one you already have.</p>'; return; }
  el.innerHTML = ids.map(i => `
    <div class="setting-row">
      <div><div class="setting-label">${esc(i.label || i.email)}</div><div style="font-size:11px;color:var(--muted);font-family:monospace">${esc(i.fingerprint)}</div></div>
      <div style="display:flex;gap:6px;align-items:center">
        <input type="password" id="pgp-unlock-pass-${i.id}" placeholder="passphrase" style="width:120px;display:none;padding:4px 8px;font-size:12px;background:var(--bg);border:1px solid var(--border);border-radius:6px;color:var(--text)">
        <button class="btn-secondary" style="padding:4px 10px;font-size:12px" id="pgp-unlock-btn-${i.id}" onclick="pgpUnlockToggle(${i.id},${accountId})">Unlock</button>
        <button class="btn-danger" style="padding:4px 10px;font-size:12px" onclick="pgpRemoveIdentity(${i.id},${accountId})">Delete</button>
      </div>
    </div>`).join('');
}

async function pgpGenerate() {
  const accountId = parseInt(document.getElementById('certs-account-select')?.value || 0);
  if (!accountId) { toast('Connect an account first', 'err'); return; }
  const pass = document.getElementById('pgp-gen-pass').value;
  const pass2 = document.getElementById('pgp-gen-pass2').value;
  if (pass.length < 8) { toast('Passphrase must be at least 8 characters', 'err'); return; }
  if (pass !== pass2) { toast('Passphrases do not match', 'err'); return; }
  const body = { account_id: accountId, label: document.getElementById('pgp-gen-label').value.trim(), passphrase: pass, passphrase_confirm: pass2 };
  const r = await api('POST', '/pgp/identity', body);
  if (r && r.ok) { toast('PGP key generated'); document.getElementById('pgp-gen-pass').value=''; document.getElementById('pgp-gen-pass2').value=''; loadCerts(); }
  else toast(r?.error || 'Failed to generate', 'err');
}

async function pgpImport() {
  const accountId = parseInt(document.getElementById('certs-account-select')?.value || 0);
  const fileInput = document.getElementById('pgp-import-file');
  if (!accountId || !fileInput.files.length) { toast('Choose a key file first', 'err'); return; }
  const fd = new FormData();
  fd.append('account_id', accountId);
  fd.append('key_file', fileInput.files[0]);
  fd.append('passphrase', document.getElementById('pgp-import-pass').value);
  const resp = await fetch('/api/pgp/identity/import?account_id=' + accountId, { method: 'POST', body: fd });
  const r = await resp.json().catch(() => null);
  if (r && r.ok) { toast('PGP key imported'); fileInput.value = ''; loadCerts(); }
  else toast(r?.error || 'Import failed', 'err');
}

async function pgpRemoveIdentity(id, accountId) {
  await api('DELETE', '/pgp/identity/' + id + '?account_id=' + accountId);
  loadCerts();
}

function pgpUnlockToggle(id, accountId) {
  const input = document.getElementById('pgp-unlock-pass-' + id);
  const btn = document.getElementById('pgp-unlock-btn-' + id);
  if (!input) return;
  if (input.style.display === 'none') {
    input.style.display = '';
    input.focus();
    btn.textContent = 'Confirm';
    return;
  }
  pgpUnlock(id, accountId, input.value);
}

async function pgpUnlock(id, accountId, passphrase) {
  if (!passphrase) { toast('Enter a passphrase', 'err'); return; }
  const r = await api('POST', '/pgp/unlock?account_id=' + accountId, { identity_id: id, passphrase });
  toast(r && r.ok ? 'Key unlocked for this session' : (r?.error || 'Incorrect passphrase'), r && r.ok ? '' : 'err');
  if (r && r.ok) loadCerts();
}

function renderPGPContacts(contacts) {
  const el = document.getElementById('pgp-contacts-list');
  if (!contacts.length) { el.innerHTML = ''; return; }
  el.innerHTML = `<table class="data-table"><thead><tr><th>Email</th><th>Fingerprint</th><th></th></tr></thead><tbody>${contacts.map(c => `
    <tr><td>${esc(c.email)}</td><td style="font-family:monospace;font-size:11px">${esc(c.fingerprint)}</td>
    <td><button class="btn-danger" style="padding:4px 10px;font-size:12px" onclick="pgpRemoveContact(${c.id})">Delete</button></td></tr>`).join('')}</tbody></table>`;
}

async function pgpAddContact() {
  const email = document.getElementById('pgp-contact-email').value.trim();
  const fileInput = document.getElementById('pgp-contact-file');
  if (!email || !fileInput.files.length) { toast('Email and public key file required', 'err'); return; }
  const fd = new FormData();
  fd.append('email', email);
  fd.append('label', document.getElementById('pgp-contact-label').value.trim());
  fd.append('key_file', fileInput.files[0]);
  const resp = await fetch('/api/pgp/contacts', { method: 'POST', body: fd });
  const r = await resp.json().catch(() => null);
  if (r && r.ok) { toast('Contact added'); document.getElementById('pgp-contact-email').value=''; document.getElementById('pgp-contact-label').value=''; fileInput.value=''; loadCerts(); }
  else toast(r?.error || 'Failed to add contact', 'err');
}

async function pgpRemoveContact(id) {
  await api('DELETE', '/pgp/contacts/' + id);
  loadCerts();
}

async function updateProfile(field) {
  const value = document.getElementById('profile-' + field).value.trim();
  const password = document.getElementById('profile-confirm-pw').value;
  if (!value) { toast('Please enter a new ' + field, 'error'); return; }
  if (!password) { toast('Current password required to confirm changes', 'error'); return; }
  const r = await api('PUT', '/profile', { field, value, password });
  if (r?.ok) {
    toast(field.charAt(0).toUpperCase() + field.slice(1) + ' updated', 'success');
    document.getElementById('profile-' + field).value = '';
    document.getElementById('profile-confirm-pw').value = '';
  } else {
    toast(r?.error || 'Update failed', 'error');
  }
}

async function loadSyncInterval() {
  const r=await api('GET','/sync-interval');
  if(r) document.getElementById('sync-interval-select').value=String(r.sync_interval||15);
}

// ── Settings: Remote image policy ───────────────────────────────────────
function loadRemoteImagePolicy() {
  document.getElementById('remote-image-policy-select').value = uiPrefsGet('remoteImagePolicy', 'manual');
}

function saveRemoteImagePolicy() {
  const val = document.getElementById('remote-image-policy-select').value;
  uiPrefsSet('remoteImagePolicy', val);
  toast('Remote image policy saved', 'success');
  if (S.currentMessage) renderMessageDetail(S.currentMessage, false);
}

// ── Notifications (Settings > General) ──────────────────────────────────────
function loadNotificationSetting() {
  const cb = document.getElementById('notifications-toggle');
  if (cb) cb.checked = uiPrefsGet('notificationsEnabled', false);
  updateNotificationStatus();
}

function updateNotificationStatus() {
  const el = document.getElementById('notifications-status');
  if (!el) return;
  if (!('Notification' in window)) { el.textContent = 'Not supported in this browser.'; return; }
  if (Notification.permission === 'denied') { el.textContent = 'Blocked for this site in your browser settings — allow it there, then try again.'; return; }
  el.textContent = '';
}

// Turning this on is the ONLY place the browser's notification-permission prompt gets
// triggered (see startPoller) — never on page load unasked. Turning it off can't revoke a
// permission the browser already granted, so it just stops the poller from using it.
async function toggleNotifications(enabled) {
  const cb = document.getElementById('notifications-toggle');
  if (!enabled) {
    uiPrefsSet('notificationsEnabled', false);
    POLLER.notifGranted = false;
    updateNotificationStatus();
    await unsubscribePush();
    return;
  }
  if (!('Notification' in window)) {
    toast('Notifications are not supported in this browser', 'error');
    if (cb) cb.checked = false;
    return;
  }
  if (Notification.permission === 'denied') {
    toast('Notifications are blocked for this site in your browser settings', 'error');
    if (cb) cb.checked = false;
    updateNotificationStatus();
    return;
  }
  const perm = Notification.permission === 'default' ? await Notification.requestPermission() : Notification.permission;
  if (perm === 'granted') {
    uiPrefsSet('notificationsEnabled', true);
    POLLER.notifGranted = true;
    toast('Notifications enabled', 'success');
    await subscribePush(); // background push while the app isn't open/focused
  } else {
    if (cb) cb.checked = false;
    uiPrefsSet('notificationsEnabled', false);
    toast('Notification permission was not granted', 'error');
  }
  updateNotificationStatus();
}

// ---- Web Push subscription (background delivery — the in-page POLLER only covers
// foreground/open-tab delivery) ----
function urlBase64ToUint8Array(base64) {
  const padding = '='.repeat((4 - base64.length % 4) % 4);
  const raw = atob((base64 + padding).replace(/-/g, '+').replace(/_/g, '/'));
  return Uint8Array.from([...raw].map(c => c.charCodeAt(0)));
}

async function subscribePush() {
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) return;
  try {
    const reg = await navigator.serviceWorker.ready;
    let sub = await reg.pushManager.getSubscription();
    if (!sub) {
      const { public_key } = await api('GET', '/push/vapid-public-key') || {};
      if (!public_key) return;
      sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(public_key),
      });
    }
    await api('POST', '/push/subscribe', sub.toJSON());
  } catch (e) {
    console.error('Push subscribe failed:', e);
  }
}

async function unsubscribePush() {
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) return;
  try {
    const reg = await navigator.serviceWorker.ready;
    const sub = await reg.pushManager.getSubscription();
    if (!sub) return;
    await api('POST', '/push/unsubscribe', { endpoint: sub.endpoint });
    await sub.unsubscribe();
  } catch (e) {
    console.error('Push unsubscribe failed:', e);
  }
}

async function loadRemoteWhitelist() {
  const el = document.getElementById('remote-whitelist-list');
  const r = await api('GET', '/remote-content-whitelist');
  const list = r?.whitelist || [];
  if (!list.length) { el.innerHTML = '<p style="color:var(--muted);font-size:12px;margin:0">No senders allowed yet.</p>'; return; }
  el.innerHTML = `<div style="display:flex;flex-wrap:wrap;gap:6px">` + list.map(sender => `
    <span class="email-tag">${esc(sender)}<button class="tag-remove" data-sender="${esc(sender)}" onclick="removeFromRemoteWhitelist(this.dataset.sender)" title="Remove">×</button></span>
  `).join('') + `</div>`;
}

async function removeFromRemoteWhitelist(sender) {
  const r = await api('DELETE', '/remote-content-whitelist?sender=' + encodeURIComponent(sender));
  if (r?.ok) {
    S.remoteWhitelist.delete(sender);
    toast('Removed from allowed senders', 'success');
    loadRemoteWhitelist();
  } else toast('Failed to remove', 'error');
}

async function saveSyncInterval() {
  const val=parseInt(document.getElementById('sync-interval-select').value)||0;
  const r=await api('PUT','/sync-interval',{sync_interval:val});
  if(r?.ok) toast('Sync interval saved','success'); else toast('Failed','error');
}

async function changePassword() {
  const cur=document.getElementById('cur-pw').value, nw=document.getElementById('new-pw').value;
  if(!cur||!nw){toast('Both fields required','error');return;}
  const r=await api('POST','/change-password',{current_password:cur,new_password:nw});
  if(r?.ok){toast('Password updated','success');document.getElementById('cur-pw').value='';document.getElementById('new-pw').value='';}
  else toast(r?.error||'Failed','error');
}

async function renderMFAPanel() {
  const me=await api('GET','/me'); if(!me) return;
  const badge=document.getElementById('mfa-badge'), panel=document.getElementById('mfa-panel');
  if(me.mfa_enabled) {
    badge.innerHTML='<span class="badge green">Enabled</span>';
    panel.innerHTML=`<p style="font-size:13px;color:var(--muted);margin-bottom:12px">TOTP active. Enter code to disable.</p>
      <div class="modal-field"><label>Code</label><input type="text" id="mfa-code" placeholder="000000" maxlength="6" inputmode="numeric"></div>
      <button class="btn-danger" onclick="disableMFA()">Disable MFA</button>`;
  } else {
    badge.innerHTML='<span class="badge red">Disabled</span>';
    panel.innerHTML='<button class="btn-primary" onclick="beginMFASetup()">Set up Authenticator App</button>';
  }
}

async function beginMFASetup() {
  const r=await api('POST','/mfa/setup'); if(!r) return;
  document.getElementById('mfa-panel').innerHTML=`
    <p style="font-size:13px;color:var(--muted);margin-bottom:12px">Scan with your authenticator app.</p>
    <div style="text-align:center;margin-bottom:14px"><img src="${r.qr_url}" style="border-radius:8px;background:white;padding:8px"></div>
    <p style="font-size:11px;color:var(--muted);margin-bottom:12px;word-break:break-all">Key: <strong>${r.secret}</strong></p>
    <div class="modal-field"><label>Confirm code</label><input type="text" id="mfa-code" placeholder="000000" maxlength="6" inputmode="numeric"></div>
    <button class="btn-primary" onclick="confirmMFASetup()">Activate MFA</button>`;
}
async function confirmMFASetup() {
  const r=await api('POST','/mfa/confirm',{code:document.getElementById('mfa-code').value});
  if(r?.ok){toast('MFA enabled','success');renderMFAPanel();}else toast(r?.error||'Invalid code','error');
}
async function disableMFA() {
  const r=await api('POST','/mfa/disable',{code:document.getElementById('mfa-code').value});
  if(r?.ok){toast('MFA disabled','success');renderMFAPanel();}else toast(r?.error||'Invalid code','error');
}

async function loadIPRules() {
  const r = await api('GET', '/ip-rules');
  if (!r) return;
  document.getElementById('ip-rule-mode').value = r.mode || 'disabled';
  document.getElementById('ip-rule-list').value = r.ip_list || '';
  toggleIPRuleHelp();
}

function toggleIPRuleHelp() {
  const mode = document.getElementById('ip-rule-mode').value;
  const helpEl = document.getElementById('ip-rule-help');
  const listField = document.getElementById('ip-rule-list-field');
  const helps = {
    disabled: '',
    brute_skip: 'IPs in the list below will never be locked out of your account, even after many failed attempts. All other IPs are subject to global brute-force protection.',
    allow_only: '⚠ Only IPs in the list below will be able to log into your account. All other IPs will see an "Access not authorized" error. Make sure to include your current IP before saving.',
  };
  helpEl.textContent = helps[mode] || '';
  helpEl.style.display = mode !== 'disabled' ? 'block' : 'none';
  listField.style.display = mode !== 'disabled' ? 'block' : 'none';
}

async function saveIPRules() {
  const mode = document.getElementById('ip-rule-mode').value;
  const ip_list = document.getElementById('ip-rule-list').value.trim();
  if (mode !== 'disabled' && !ip_list) {
    toast('Please enter at least one IP address', 'error'); return;
  }
  const r = await api('PUT', '/ip-rules', { mode, ip_list });
  if (r?.ok) toast('IP rules saved', 'success');
  else toast(r?.error || 'Save failed', 'error');
}

// ── Login History (Settings > Security) ─────────────────────────────────────
// Server-side filtered/sorted/paginated — always scoped to the logged-in user by the
// GET /api/login-history handler itself (never a param this code could tamper with).
const LH = { page: 1, hasMore: false };

function openLoginHistory() {
  openModal('login-history-modal');
  loadLoginHistory(1);
}

async function loadLoginHistory(page) {
  LH.page = page;
  const params = new URLSearchParams({ page, page_size: 25 });
  const dateFrom = document.getElementById('lh-date-from').value;
  const dateTo = document.getElementById('lh-date-to').value;
  const status = document.getElementById('lh-status').value;
  const ip = document.getElementById('lh-ip').value.trim();
  const sort = document.getElementById('lh-sort').value;
  if (dateFrom) params.set('date_from', dateFrom);
  if (dateTo) params.set('date_to', dateTo);
  if (status) params.set('success', status);
  if (ip) params.set('ip', ip);
  if (sort) params.set('sort', sort);

  const tbody = document.getElementById('lh-table-body');
  tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;padding:24px"><span class="spinner-inline"></span></td></tr>';
  const r = await api('GET', '/login-history?' + params.toString());
  if (!r) {
    tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;color:var(--danger);padding:20px">Failed to load login history.</td></tr>';
    return;
  }

  LH.hasMore = !!r.has_more;
  const logs = r.logs || [];
  tbody.innerHTML = logs.length ? logs.map(l => `
    <tr>
      <td style="font-family:monospace;font-size:11px;color:var(--muted)">${new Date(l.created_at).toLocaleString()}</td>
      <td><span class="badge ${l.event==='login'?'green':'red'}">${l.event==='login'?'Success':'Failed'}</span></td>
      <td style="font-family:monospace;font-size:11px">${esc(l.ip_address||'—')}</td>
      <td style="color:var(--muted);font-size:12px">${esc(l.detail||'')}</td>
    </tr>`).join('') : '<tr><td colspan="4" style="text-align:center;color:var(--muted);padding:24px">No login attempts found.</td></tr>';

  document.getElementById('lh-page-info').textContent = `Page ${page} · ${r.total||0} total`;
  document.getElementById('lh-prev-btn').disabled = page <= 1;
  document.getElementById('lh-next-btn').disabled = !LH.hasMore;
}

function loginHistoryPrevPage() { if (LH.page > 1) loadLoginHistory(LH.page - 1); }
function loginHistoryNextPage() { if (LH.hasMore) loadLoginHistory(LH.page + 1); }
const debouncedLoadLoginHistory = debounce(() => loadLoginHistory(1), 400);

// ── Spam Block (Settings > Security) ────────────────────────────────────────
function openSpamBlock() {
  openModal('spam-block-modal');
  loadSpamBlock();
}

async function loadSpamBlock() {
  const tbody = document.getElementById('sb-table-body');
  tbody.innerHTML = '<tr><td colspan="3" style="text-align:center;padding:24px"><span class="spinner-inline"></span></td></tr>';
  const r = await api('GET', '/spam-block');
  const entries = r?.entries || [];
  tbody.innerHTML = entries.length ? entries.map(e => `
    <tr>
      <td style="font-family:monospace;font-size:12px">${esc(e.sender)}</td>
      <td style="font-family:monospace;font-size:11px;color:var(--muted)">${new Date(e.created_at).toLocaleString()}</td>
      <td><button class="btn-secondary" style="font-size:11px;padding:3px 8px" onclick="removeSpamBlockEntry('${esc(e.sender)}')">Remove</button></td>
    </tr>`).join('') : '<tr><td colspan="3" style="text-align:center;color:var(--muted);padding:24px">No blocked senders yet.</td></tr>';
}

async function addSpamBlockEntry() {
  const input = document.getElementById('sb-add-input');
  const sender = input.value.trim();
  if (!sender) return;
  const r = await api('POST', '/spam-block', { sender });
  if (r?.ok) { input.value = ''; toast('Sender blocked', 'success'); loadSpamBlock(); }
  else toast(r?.error || 'Failed to block sender', 'error');
}

async function removeSpamBlockEntry(sender) {
  const r = await api('DELETE', '/spam-block?sender=' + encodeURIComponent(sender));
  if (r?.ok) { toast('Sender unblocked', 'success'); loadSpamBlock(); }
  else toast('Failed to remove', 'error');
}

async function doLogout() { await fetch('/auth/logout',{method:'POST'}); location.href='/auth/login'; }

// ── Context menu helper ────────────────────────────────────────────────────
function showCtxMenu(e, html) {
  const menu=document.getElementById('ctx-menu');
  menu.innerHTML=html; menu.classList.add('open');
  menu.setAttribute('role','menu');
  menu.querySelectorAll('.ctx-item').forEach(item=>item.setAttribute('role','menuitem'));
  menu.querySelectorAll('.ctx-sep').forEach(sep=>sep.setAttribute('role','separator'));
  requestAnimationFrame(()=>{
    menu.style.left=Math.min(e.clientX,window.innerWidth-menu.offsetWidth-8)+'px';
    menu.style.top=Math.min(e.clientY,window.innerHeight-menu.offsetHeight-8)+'px';

    // Submenus ("Move to", "Label as") default to opening right/below their parent item.
    // Near a screen edge that pushes them off-view, so measure each one against the
    // viewport and flip it to the opposite side when it wouldn't fit.
    menu.querySelectorAll('.ctx-has-sub').forEach(parent=>{
      const sub=parent.querySelector('.ctx-submenu');
      if (!sub) return;
      sub.style.left=''; sub.style.right=''; sub.style.top=''; sub.style.bottom='';
      sub.style.visibility='hidden'; sub.style.display='block';
      const pr=parent.getBoundingClientRect(), sw=sub.offsetWidth, sh=sub.offsetHeight;
      sub.style.display=''; sub.style.visibility='';
      if (pr.right+sw>window.innerWidth) { sub.style.left='auto'; sub.style.right='100%'; }
      if (pr.top+sh>window.innerHeight) { sub.style.top='auto'; sub.style.bottom='-4px'; }
    });
  });
}

// ── Init tag fields and filter dropdown ───────────────────────────────────
// app.js loads at the bottom of <body> so the DOM is already ready here —
// we must NOT wrap in DOMContentLoaded (that event has already fired).
function _bootApp() {
  initTagField('compose-to');
  initTagField('compose-cc-tags');
  initTagField('compose-bcc-tags');

  // Filter dropdown
  const dropBtn  = document.getElementById('filter-dropdown-btn');
  const dropMenu = document.getElementById('filter-dropdown-menu');
  if (dropBtn && dropMenu) {
    dropBtn.addEventListener('click', e => {
      e.stopPropagation();
      const isOpen = dropMenu.classList.contains('open');
      dropMenu.classList.toggle('open', !isOpen);
      if (!isOpen) {
        document.addEventListener('click', () => dropMenu.classList.remove('open'), {once:true});
      }
    });
    ['default','unread','date-desc','date-asc','size-desc'].forEach(mode => {
      const el = document.getElementById('fopt-'+mode);
      if (el) el.addEventListener('click', e => { e.stopPropagation(); setFilter(mode); });
    });
  }

  init();
}

// Run immediately — DOM is ready since this script is at end of <body>
_bootApp();

// ── Real-time poller + notifications ────────────────────────────────────────
// Polls /api/poll every 20s for unread count changes and new message detection.
// When new messages arrive: updates badge instantly, shows corner toast,
// and fires a browser OS notification if permission granted.

const POLLER = {
  lastKnownID: 0,      // highest message ID we've seen
  timer: null,
  active: false,
  notifGranted: false,
};

async function startPoller() {
  // Notification permission is only ever requested from the explicit Settings > General
  // toggle (toggleNotifications) — never on load — so this just reflects whatever the user
  // already opted into and the browser already granted.
  POLLER.notifGranted = uiPrefsGet('notificationsEnabled', false)
    && 'Notification' in window && Notification.permission === 'granted';

  POLLER.active = true;
  schedulePoll();
}

function schedulePoll() {
  if (!POLLER.active) return;
  POLLER.timer = setTimeout(runPoll, 10000); // 10 second interval
}

async function runPoll() {
  if (!POLLER.active) return;
  try {
    const data = await api('GET', '/poll?since=' + POLLER.lastKnownID);
    if (!data) { schedulePoll(); return; }

    // Update badge immediately without full loadFolders()
    updateUnreadBadgeFromPoll(data.inbox_unread);

    // New messages arrived
    if (data.has_new && data.newest_id > POLLER.lastKnownID) {
      const prevID = POLLER.lastKnownID;
      POLLER.lastKnownID = data.newest_id;

      // Fetch new message details for notifications
      const newData = await api('GET', '/new-messages?since=' + prevID);
      const newMsgs = newData?.messages || [];

      if (newMsgs.length > 0) {
        showNewMailToast(newMsgs);
        sendOSNotification(newMsgs);
      }

      // Always refresh the message list and folder counts when new mail arrives
      await loadFolders();
      await loadMessages();
    }
  } catch(e) {
    // Network error — silent, retry next cycle
  }
  schedulePoll();
}

// Update the unread badge in the sidebar and browser tab title
// without triggering a full folder reload
function updateUnreadBadgeFromPoll(inboxUnread) {
  const badge = document.getElementById('unread-total');
  if (!badge) return;
  if (inboxUnread > 0) {
    badge.textContent = inboxUnread > 99 ? '99+' : inboxUnread;
    badge.style.display = '';
  } else {
    badge.style.display = 'none';
  }
  const bell = document.getElementById('bell-badge');
  if (bell) {
    bell.textContent = inboxUnread > 99 ? '99+' : inboxUnread;
    bell.style.display = inboxUnread > 0 ? '' : 'none';
  }
  // Update browser tab title
  const base = S.me ? 'Email - ' + (S.me.username || S.me.email) + ' - GoWebMail' : 'GoWebMail';
  document.title = inboxUnread > 0 ? `(${inboxUnread}) ${base}` : base;
}

// Corner toast notification for new mail
function showNewMailToast(msgs) {
  const existing = document.getElementById('newmail-toast');
  if (existing) existing.remove();

  const count = msgs.length;
  const first = msgs[0];
  const fromLabel = first.from_name || first.from_email || 'Unknown';
  const subject = first.subject || '(no subject)';

  const text = count === 1
    ? `<strong>${escHtml(fromLabel)}</strong><br><span>${escHtml(subject)}</span>`
    : `<strong>${count} new messages</strong><br><span>${escHtml(fromLabel)}: ${escHtml(subject)}</span>`;

  const el = document.createElement('div');
  el.id = 'newmail-toast';
  el.className = 'newmail-toast';
  el.innerHTML = `
    <div class="newmail-toast-icon">✉</div>
    <div class="newmail-toast-body">${text}</div>
    <button class="newmail-toast-close" onclick="this.parentElement.remove()">✕</button>`;

  // Click to open the message
  el.addEventListener('click', (e) => {
    if (e.target.classList.contains('newmail-toast-close')) return;
    el.remove();
    if (count === 1) {
      selectFolder(
        S.folders.find(f=>f.folder_type==='inbox')?.id || 'unified',
        'Inbox'
      );
      setTimeout(()=>openMessage(first.id), 400);
    } else {
      selectFolder('unified', 'Unified Inbox');
    }
  });

  document.body.appendChild(el);

  // Auto-dismiss after 6s
  setTimeout(() => { if (el.parentElement) el.remove(); }, 6000);
}

function escHtml(s) {
  return String(s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// OS / browser notification
function sendOSNotification(msgs) {
  if (!POLLER.notifGranted || !('Notification' in window)) return;
  const count = msgs.length;
  const first = msgs[0];
  const title = count === 1
    ? (first.from_name || first.from_email || 'New message')
    : `${count} new messages in GoWebMail`;
  const body = count === 1
    ? (first.subject || '(no subject)')
    : `${first.from_name || first.from_email}: ${first.subject || '(no subject)'}`;

  try {
    const n = new Notification(title, {
      body,
      icon: '/static/icons/icon-192.png', // use if you have one, else falls back gracefully
      tag: 'gowebmail-new',   // replaces previous if still visible
    });
    n.onclick = () => {
      window.focus();
      n.close();
      if (count === 1) {
        selectFolder(S.folders.find(f=>f.folder_type==='inbox')?.id||'unified','Inbox');
        setTimeout(()=>openMessage(first.id), 400);
      }
    };
    // Auto-close OS notification after 8s
    setTimeout(()=>n.close(), 8000);
  } catch(e) {
    // Some browsers block even with granted permission in certain contexts
  }
}

// ── Mobile navigation ────────────────────────────────────────────────────────
function isMobile() { return window.innerWidth <= 700; }

function mobSetView(view) {
  if (!isMobile()) return;
  const app = document.getElementById('app-root');
  if (!app) return;
  app.dataset.mobView = view;
  const navBtn  = document.getElementById('mob-nav-btn');
  const backBtn = document.getElementById('mob-back-btn');
  const titleEl = document.getElementById('mob-title');
  if (view === 'detail') {
    if (navBtn)  navBtn.style.display  = 'none';
    if (backBtn) backBtn.style.display = 'flex';
    if (titleEl) titleEl.textContent = S.currentMessage?.subject || 'Message';
  } else {
    if (navBtn)  navBtn.style.display  = 'flex';
    if (backBtn) backBtn.style.display = 'none';
    if (titleEl) titleEl.textContent = S.currentFolderName || 'GoWebMail';
  }
}

function mobBack() {
  if (!isMobile()) return;
  const app = document.getElementById('app-root');
  if (!app) return;
  if (app.dataset.mobView === 'detail') {
    mobSetView('list');
  }
}

function mobShowNav() {
  document.querySelector('.sidebar')?.classList.add('mob-open');
  document.getElementById('mob-sidebar-backdrop')?.classList.add('mob-open');
}

function mobCloseNav() {
  document.querySelector('.sidebar')?.classList.remove('mob-open');
  document.getElementById('mob-sidebar-backdrop')?.classList.remove('mob-open');
}

// Update mob title when folder changes
const _origSelectFolder = selectFolder;
// (selectFolder already calls mobSetView/mobCloseNav inline)

// On resize between mobile/desktop, reset any leftover mobile state
window.addEventListener('resize', () => {
  if (!isMobile()) {
    const app = document.getElementById('app-root');
    if (app) app.dataset.mobView = 'list';
    document.querySelector('.sidebar')?.classList.remove('mob-open');
    document.getElementById('mob-sidebar-backdrop')?.classList.remove('mob-open');
  }
});

// ── Compose dropdown ────────────────────────────────────────────────────────
// Lives at the end of <body> (position:fixed) rather than inside .sidebar-header,
// because .sidebar has overflow:hidden — an absolutely-positioned child there gets
// clipped by that ancestor regardless of z-index. Position it via JS instead,
// same idea as positionMenu() for the context menu.
function toggleComposeDropdown(e) {
  e.stopPropagation();
  const dd = document.getElementById('compose-dropdown');
  if (!dd) return;
  const isOpen = dd.style.display !== 'none';
  if (isOpen) { dd.style.display = 'none'; return; }
  const r = e.currentTarget.getBoundingClientRect();
  dd.style.display = 'block';
  dd.style.left = Math.min(r.left, window.innerWidth - dd.offsetWidth - 8) + 'px';
  dd.style.top = (r.bottom + 4) + 'px';
  setTimeout(() => document.addEventListener('click', closeComposeDropdown, { once: true }), 0);
}

function closeComposeDropdown() {
  const dd = document.getElementById('compose-dropdown');
  if (dd) dd.style.display = 'none';
}

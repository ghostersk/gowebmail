// GoWebMail app.js — full client

// ── State ──────────────────────────────────────────────────────────────────
const S = {
  me: null, accounts: [], providers: {gmail:false,outlook:false},
  folders: [], messages: [], totalMessages: 0,
  currentPage: 1, currentFolder: 'unified', currentFolderName: 'Unified Inbox',
  currentMessage: null, selectedMessageId: null,
  searchQuery: '', composeMode: 'new', composeReplyToId: null, composeForwardFromId: null,
  filterUnread: false, filterAttachment: false,
  sortOrder: 'date-desc', // 'date-desc' | 'date-asc' | 'size-desc'
};

// ── Boot ───────────────────────────────────────────────────────────────────
async function init() {
  const [me, providers, wl] = await Promise.all([
    api('GET','/me'), api('GET','/providers'), api('GET','/remote-content-whitelist'),
  ]);
  if (me) {
    S.me = me;
    document.getElementById('user-display').textContent = me.username || me.email;
    if (me.role === 'admin') document.getElementById('admin-link').style.display = 'block';
  }
  if (providers) { S.providers = providers; updateProviderButtons(); }
  if (wl?.whitelist) S.remoteWhitelist = new Set(wl.whitelist);

  await loadAccounts();
  await loadFolders();
  await loadMessages();
  // Seed poller ID so we don't notify on initial load
  if (S.messages.length > 0) {
    POLLER.lastKnownID = Math.max(...S.messages.map(m=>m.id));
  }

  const p = new URLSearchParams(location.search);
  if (p.get('connected')) { toast('Account connected!', 'success'); history.replaceState({},'','/'); }
  if (p.get('error'))     { toast('Connection failed: '+p.get('error'), 'error'); history.replaceState({},'','/'); }

  document.addEventListener('keydown', e => {
    if (['INPUT','TEXTAREA','SELECT'].includes(e.target.tagName)) return;
    if (e.target.contentEditable === 'true') return;
    if ((e.metaKey||e.ctrlKey) && e.key==='n') { e.preventDefault(); openCompose(); }
    if ((e.metaKey||e.ctrlKey) && e.key==='k') { e.preventDefault(); document.getElementById('search-input').focus(); }
  });

  initComposeDragResize();
  startPoller();
}

// ── Providers ──────────────────────────────────────────────────────────────
function updateProviderButtons() {
  ['gmail','outlook'].forEach(p => {
    const btn = document.getElementById('btn-'+p);
    if (!btn) return;
    if (!S.providers[p]) { btn.disabled=true; btn.classList.add('unavailable'); btn.title='Not configured'; }
  });
}

// ── Accounts popup ─────────────────────────────────────────────────────────
function toggleAccountsMenu(e) {
  e.stopPropagation();
  const popup = document.getElementById('accounts-popup');
  const backdrop = document.getElementById('accounts-popup-backdrop');
  if (popup.classList.contains('open')) {
    closeAccountsMenu(); return;
  }
  renderAccountsPopup();
  popup.classList.add('open');
  backdrop.classList.add('open');
}
function closeAccountsMenu() {
  document.getElementById('accounts-popup').classList.remove('open');
  document.getElementById('accounts-popup-backdrop').classList.remove('open');
}

function renderAccountsPopup() {
  const el = document.getElementById('accounts-popup-list');
  if (!S.accounts.length) {
    el.innerHTML = '<div style="font-size:12px;color:var(--muted);padding:8px 0">No accounts connected.</div>';
    return;
  }
  el.innerHTML = S.accounts.map(a => `
    <div class="acct-popup-item" title="${esc(a.email_address)}${a.last_error?' ⚠ '+esc(a.last_error):''}">
      <div style="display:flex;align-items:center;gap:8px;flex:1;min-width:0">
        <span class="account-dot" style="background:${a.color};flex-shrink:0"></span>
        <span style="font-size:13px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(a.display_name||a.email_address)}</span>
        ${a.last_error?'<span style="color:var(--danger);font-size:11px">⚠</span>':''}
      </div>
      <div style="display:flex;gap:4px;flex-shrink:0">
        <button class="icon-btn" title="Sync now" onclick="syncNow(${a.id},event)">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M12 4V1L8 5l4 4V6c3.31 0 6 2.69 6 6 0 1.01-.25 1.97-.7 2.8l1.46 1.46C19.54 15.03 20 13.57 20 12c0-4.42-3.58-8-8-8zm0 14c-3.31 0-6-2.69-6-6 0-1.01.25-1.97.7-2.8L5.24 7.74C4.46 8.97 4 10.43 4 12c0 4.42 3.58 8 8 8v3l4-4-4-4v3z"/></svg>
        </button>
        <button class="icon-btn" title="Settings" onclick="openEditAccount(${a.id})">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04c.39-.39.39-1.02 0-1.41l-2.34-2.34c-.39-.39-1.02-.39-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z"/></svg>
        </button>
        <button class="icon-btn" title="Remove" onclick="deleteAccount(${a.id})" style="color:var(--danger)">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor"><path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>
        </button>
      </div>
    </div>`).join('');
}

// ── Accounts ───────────────────────────────────────────────────────────────
async function loadAccounts() {
  const data = await api('GET','/accounts');
  if (!data) return;
  S.accounts = data;
  renderAccountsPopup();
  populateComposeFrom();
}

function connectOAuth(p) { location.href='/auth/'+p+'/connect'; }

function openAddAccountModal() {
  ['imap-email','imap-name','imap-password','imap-host','smtp-host'].forEach(id=>{ const el=document.getElementById(id); if(el) el.value=''; });
  document.getElementById('imap-port').value='993';
  document.getElementById('smtp-port').value='587';
  const r=document.getElementById('test-result'); if(r){r.style.display='none';r.className='test-result';}
  closeAccountsMenu();
  openModal('add-account-modal');
}

async function testNewConnection() {
  const btn=document.getElementById('test-btn'), result=document.getElementById('test-result');
  const body={email:document.getElementById('imap-email').value.trim(),password:document.getElementById('imap-password').value,
    imap_host:document.getElementById('imap-host').value.trim(),imap_port:parseInt(document.getElementById('imap-port').value)||993,
    smtp_host:document.getElementById('smtp-host').value.trim(),smtp_port:parseInt(document.getElementById('smtp-port').value)||587};
  if (!body.email||!body.password||!body.imap_host){result.textContent='Email, password and IMAP host required.';result.className='test-result err';result.style.display='block';return;}
  btn.innerHTML='<span class="spinner-inline"></span>Testing...';btn.disabled=true;
  const r=await api('POST','/accounts/test',body);
  btn.textContent='Test Connection';btn.disabled=false;
  result.textContent=(r?.ok)?'✓ Connection successful!':((r?.error)||'Connection failed');
  result.className='test-result '+((r?.ok)?'ok':'err'); result.style.display='block';
}

async function addIMAPAccount() {
  const btn=document.getElementById('save-acct-btn');
  const body={email:document.getElementById('imap-email').value.trim(),display_name:document.getElementById('imap-name').value.trim(),
    password:document.getElementById('imap-password').value,imap_host:document.getElementById('imap-host').value.trim(),
    imap_port:parseInt(document.getElementById('imap-port').value)||993,smtp_host:document.getElementById('smtp-host').value.trim(),
    smtp_port:parseInt(document.getElementById('smtp-port').value)||587};
  if (!body.email||!body.password||!body.imap_host){toast('Email, password and IMAP host required','error');return;}
  btn.disabled=true;btn.textContent='Connecting...';
  const r=await api('POST','/accounts',body);
  btn.disabled=false;btn.textContent='Connect';
  if (r?.ok){
    toast('Account added — syncing…','success');
    closeModal('add-account-modal');
    await loadAccounts();
    // Background sync takes a moment — reload folders/messages after a short wait
    setTimeout(async ()=>{ await loadFolders(); await loadMessages(); toast('Sync complete','success'); }, 3000);
  } else toast(r?.error||'Failed to add account','error');
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
async function openEditAccount(id) {
  closeAccountsMenu();
  const r=await api('GET','/accounts/'+id);
  if (!r) return;
  document.getElementById('edit-account-id').value=id;
  document.getElementById('edit-account-email').textContent=r.email_address;
  document.getElementById('edit-name').value=r.display_name||'';
  document.getElementById('edit-password').value='';
  document.getElementById('edit-imap-host').value=r.imap_host||'';
  document.getElementById('edit-imap-port').value=r.imap_port||993;
  document.getElementById('edit-smtp-host').value=r.smtp_host||'';
  document.getElementById('edit-smtp-port').value=r.smtp_port||587;
  document.getElementById('edit-sync-days').value=r.sync_days||30;
  // Restore sync mode select: map stored days/mode back to a preset option
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

  // Load hidden folders for this account
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
  const btn=document.getElementById('edit-test-btn'), connEl=document.getElementById('edit-conn-result');
  const pw=document.getElementById('edit-password').value, email=document.getElementById('edit-account-email').textContent.trim();
  if (!pw){connEl.textContent='Enter new password to test.';connEl.className='test-result err';connEl.style.display='block';return;}
  btn.innerHTML='<span class="spinner-inline"></span>Testing...';btn.disabled=true;
  const r=await api('POST','/accounts/test',{email,password:pw,
    imap_host:document.getElementById('edit-imap-host').value.trim(),imap_port:parseInt(document.getElementById('edit-imap-port').value)||993,
    smtp_host:document.getElementById('edit-smtp-host').value.trim(),smtp_port:parseInt(document.getElementById('edit-smtp-port').value)||587});
  btn.textContent='Test Connection';btn.disabled=false;
  connEl.textContent=(r?.ok)?'✓ Successful!':((r?.error)||'Failed');
  connEl.className='test-result '+((r?.ok)?'ok':'err'); connEl.style.display='block';
}

async function saveAccountEdit() {
  const id=document.getElementById('edit-account-id').value;
  const body={display_name:document.getElementById('edit-name').value.trim(),
    imap_host:document.getElementById('edit-imap-host').value.trim(),imap_port:parseInt(document.getElementById('edit-imap-port').value)||993,
    smtp_host:document.getElementById('edit-smtp-host').value.trim(),smtp_port:parseInt(document.getElementById('edit-smtp-port').value)||587};
  const pw=document.getElementById('edit-password').value;
  if (pw) body.password=pw;
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
      if (r?.ok){toast('Account removed','success');closeAccountsMenu();loadAccounts();loadFolders();loadMessages();}
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

function renderFolders() {
  const el=document.getElementById('folders-by-account');
  const accMap={}; S.accounts.forEach(a=>accMap[a.id]=a);
  const byAcc={};
  S.folders.filter(f=>!f.is_hidden).forEach(f=>{(byAcc[f.account_id]=byAcc[f.account_id]||[]).push(f);});
  const prio=['inbox','sent','drafts','trash','spam','archive'];
  el.innerHTML=Object.entries(byAcc).map(([accId,folders])=>{
    const acc=accMap[parseInt(accId)];
    const accColor = acc?.color || '#888';
    const accEmail = acc?.email_address || 'Account '+accId;
    if(!folders?.length) return '';
    const sorted=[...prio.map(t=>folders.find(f=>f.folder_type===t)).filter(Boolean),...folders.filter(f=>f.folder_type==='custom')];
    return `<div class="nav-folder-header">
        <span style="width:6px;height:6px;border-radius:50%;background:${accColor};display:inline-block;flex-shrink:0"></span>
        <span style="flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(accEmail)}</span>
        <button class="icon-sync-btn" title="Sync account" onclick="syncNow(${parseInt(accId)},event)" style="margin-left:4px">
          <svg width="11" height="11" viewBox="0 0 24 24" fill="currentColor"><path d="M12 4V1L8 5l4 4V6c3.31 0 6 2.69 6 6 0 1.01-.25 1.97-.7 2.8l1.46 1.46C19.54 15.03 20 13.57 20 12c0-4.42-3.58-8-8-8zm0 14c-3.31 0-6-2.69-6-6 0-1.01.25-1.97.7-2.8L5.24 7.74C4.46 8.97 4 10.43 4 12c0 4.42 3.58 8 8 8v3l4-4-4-4v3z"/></svg>
        </button>
      </div>`+sorted.map(f=>`
      <div class="nav-item${f.sync_enabled?'':' folder-nosync'}" id="nav-f${f.id}" data-fid="${f.id}" onclick="selectFolder(${f.id},'${esc(f.name)}')"
           oncontextmenu="showFolderMenu(event,${f.id})">
        <svg viewBox="0 0 24 24" fill="currentColor">${FOLDER_ICONS[f.folder_type]||FOLDER_ICONS.custom}</svg>
        ${esc(f.name)}
        ${f.unread_count>0?`<span class="unread-badge">${f.unread_count}</span>`:''}
        ${!f.sync_enabled?'<span style="font-size:9px;color:var(--muted);margin-left:auto" title="Sync disabled">⊘</span>':''}
      </div>`).join('');
  }).join('');
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
  showCtxMenu(e, `
    <div class="ctx-item" onclick="syncFolderNow(${folderId});closeMenu()">↻ Sync this folder</div>
    <div class="ctx-item" onclick="toggleFolderSync(${folderId});closeMenu()">${syncLabel}</div>
    ${enableAllEntry}
    <div class="ctx-item" onclick="markFolderAllRead(${folderId});closeMenu()">✓ Mark all as read</div>
    <div class="ctx-sep"></div>
    ${moveEntry}
    ${emptyEntry}
    <div class="ctx-item" onclick="confirmHideFolder(${folderId});closeMenu()">👁 Hide from sidebar</div>
    <div class="ctx-item danger" onclick="confirmDeleteFolder(${folderId});closeMenu()">🗑 Delete folder</div>`);
}

async function syncFolderNow(folderId) {
  toast('Syncing folder…','info');
  const r=await api('POST','/folders/'+folderId+'/sync');
  if (r?.ok) { toast('Synced '+(r.synced||0)+' messages','success'); loadFolders(); loadMessages(); }
  else toast(r?.error||'Sync failed','error');
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
}

// ── Messages ───────────────────────────────────────────────────────────────
function selectFolder(folderId, folderName) {
  S.currentFolder=folderId; S.currentFolderName=folderName||S.currentFolderName;
  S.currentPage=1; S.messages=[]; S.searchQuery='';
  document.getElementById('search-input').value='';
  document.getElementById('panel-title').textContent=folderName||S.currentFolderName;
  document.querySelectorAll('.nav-item').forEach(n=>n.classList.remove('active'));
  const navEl=folderId==='unified'?document.getElementById('nav-unified')
    :folderId==='starred'?document.getElementById('nav-starred')
    :document.getElementById('nav-f'+folderId);
  if (navEl) navEl.classList.add('active');
  loadMessages();
}

const handleSearch=debounce(q=>{
  S.searchQuery=q.trim(); S.currentPage=1;
  document.getElementById('panel-title').textContent=q.trim()?'Search: '+q.trim():S.currentFolderName;
  loadMessages();
},350);

async function loadMessages(append) {
  const list=document.getElementById('message-list');
  if (!append) list.innerHTML='<div class="spinner" style="margin-top:60px"></div>';
  let result;
  if (S.searchQuery) result=await api('GET',`/search?q=${encodeURIComponent(S.searchQuery)}&page=${S.currentPage}&page_size=50`);
  else if (S.currentFolder==='unified') result=await api('GET',`/messages/unified?page=${S.currentPage}&page_size=50`);
  else if (S.currentFolder==='starred') result=await api('GET',`/messages/starred?page=${S.currentPage}&page_size=50`);
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

function renderMessageList() {
  const list=document.getElementById('message-list');
  let msgs = [...S.messages];

  // Filter
  if (S.filterUnread) msgs = msgs.filter(m => !m.is_read);
  if (S.filterAttachment) msgs = msgs.filter(m => m.has_attachment);

  // Sort
  if (S.sortOrder === 'date-asc') msgs.sort((a,b) => new Date(a.date)-new Date(b.date));
  else if (S.sortOrder === 'size-desc') msgs.sort((a,b) => (b.size||0)-(a.size||0));
  else msgs.sort((a,b) => new Date(b.date)-new Date(a.date));

  if (!msgs.length){
    const emptyMsg = S.filterUnread ? 'No unread messages' : S.filterAttachment ? 'No messages with attachments' : 'No messages';
    list.innerHTML=`<div class="empty-state"><svg viewBox="0 0 24 24"><path d="M20 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z"/></svg><p>${emptyMsg}</p></div>`;
    return;
  }

  // Update bulk action bar
  updateBulkBar();

  list.innerHTML=msgs.map((m,i)=>`
    <div class="message-item ${m.id===S.selectedMessageId&&!SEL.ids.size?'active':''} ${!m.is_read?'unread':''} ${SEL.ids.has(m.id)?'selected':''}"
         data-id="${m.id}" data-idx="${i}"
         draggable="true"
         onclick="handleMsgClick(event,${m.id},${i})"
         oncontextmenu="showMessageMenu(event,${m.id})"
         ondragstart="handleMsgDragStart(event,${m.id})">
      <div class="msg-top">
        <span class="msg-from">${esc(m.from_name||m.from_email)}</span>
        <span class="msg-date">${formatDate(m.date)}</span>
      </div>
      <div class="msg-subject">${esc(m.subject||'(no subject)')}</div>
      <div class="msg-preview">${esc(m.preview||'')}</div>
      <div class="msg-meta">
        <span class="msg-dot" style="background:${m.account_color}"></span>
        <span class="msg-acct">${esc(m.account_email||'')}</span>
        ${m.size?`<span style="font-size:10px;color:var(--muted);margin-left:4px">${formatSize(m.size)}</span>`:''}
        ${m.has_attachment?'<svg width="11" height="11" viewBox="0 0 24 24" fill="var(--muted)" style="margin-left:4px"><path d="M16.5 6v11.5c0 2.21-1.79 4-4 4s-4-1.79-4-4V5c0-1.38 1.12-2.5 2.5-2.5s2.5 1.12 2.5 2.5v10.5c0 .55-.45 1-1 1s-1-.45-1-1V6H10v9.5c0 1.38 1.12 2.5 2.5 2.5s2.5-1.12 2.5-2.5V5c0-2.21-1.79-4-4-4S7 2.79 7 5v12.5c0 3.04 2.46 5.5 5.5 5.5s5.5-2.46 5.5-5.5V6h-1.5z"/></svg>':''}
        <span class="msg-star ${m.is_starred?'on':''}" onclick="toggleStar(${m.id},event)">${m.is_starred?'★':'☆'}</span>
      </div>
    </div>`).join('')+(S.messages.length<S.totalMessages
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
  return msgs;
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
}

async function bulkMarkRead(read) {
  await Promise.all([...SEL.ids].map(id=>api('PUT','/messages/'+id+'/read',{read})));
  SEL.ids.forEach(id=>{const m=S.messages.find(m=>m.id===id);if(m)m.is_read=read;});
  SEL.ids.clear(); renderMessageList(); loadFolders();
}

async function bulkDelete() {
  const count = SEL.ids.size;
  inlineConfirm(
    `Delete ${count} message${count===1?'':'s'}? This cannot be undone.`,
    async () => {
      const ids = [...SEL.ids];
      await Promise.all(ids.map(id=>api('DELETE','/messages/'+id)));
      ids.forEach(id=>{S.messages=S.messages.filter(m=>m.id!==id);});
      SEL.ids.clear(); renderMessageList(); loadFolders();
    }
  );
}

function loadMoreMessages(){ S.currentPage++; loadMessages(true); }

async function openMessage(id) {
  S.selectedMessageId=id; renderMessageList();
  const detail=document.getElementById('message-detail');
  detail.innerHTML='<div class="spinner" style="margin-top:100px"></div>';
  const msg=await api('GET','/messages/'+id);
  if (!msg){detail.innerHTML='<div class="no-message"><p>Failed to load</p></div>';return;}
  S.currentMessage=msg;
  renderMessageDetail(msg, false);
  const li=S.messages.find(m=>m.id===id);
  if (li&&!li.is_read){
    li.is_read=true; renderMessageList();
    // Sync read status to server (enqueues IMAP op via backend)
    api('PUT','/messages/'+id+'/read',{read:true});
  }
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

function renderMessageDetail(msg, showRemoteContent) {
  const detail=document.getElementById('message-detail');
  const allowed=showRemoteContent||S.remoteWhitelist.has(msg.from_email);

  const cssReset = `<style>html,body{background:#ffffff!important;color:#1a1a1a!important;` +
    `font-family:Arial,sans-serif;font-size:14px;line-height:1.5;margin:8px}a{color:#1a5fb4}` +
    `img{max-width:100%;height:auto}iframe{display:none!important}</style>`;

  // Injected into srcdoc: reports height + intercepts all link clicks → postMessage to parent
  const heightScript = `<script>
    function _reportH(){parent.postMessage({type:'gomail-frame-h',h:document.documentElement.scrollHeight},'*');}
    document.addEventListener('DOMContentLoaded',_reportH);
    window.addEventListener('load',_reportH);
    new MutationObserver(_reportH).observe(document.documentElement,{subtree:true,childList:true,attributes:true});
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

  function stripUnresolvedCID(h){ return h.replace(/src\s*=\s*(['"])cid:[^'"]*\1/gi,'src=""').replace(/src\s*=\s*cid:\S+/gi,'src=""'); }
  function stripEmbeddedFrames(h){ return h.replace(/<iframe[\s\S]*?<\/iframe>/gi,'').replace(/<iframe[^>]*>/gi,''); }
  function stripRemoteImages(h){
    return h.replace(/<img(\s[^>]*?)src\s*=\s*(['"])(https?:\/\/[^'"]+)\2/gi,'<img$1src="" data-blocked-src="$3"')
            .replace(/url\s*\(\s*(['"]?)https?:\/\/[^)'"]+\1\s*\)/gi,'url()')
            .replace(/<link[^>]*>/gi,'').replace(/<script[\s\S]*?<\/script>/gi,'');
  }

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
      bodyHtml=`<div class="remote-content-banner">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor"><path d="M21 19V5c0-1.1-.9-2-2-2H5c-1.1 0-2 .9-2 2v14c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2zM8.5 13.5l2.5 3.01L14.5 12l4.5 6H5l3.5-4.5z"/></svg>
        Remote images blocked.
        <button class="rcb-btn" onclick="renderMessageDetail(S.currentMessage,true)">Load images</button>
        <button class="rcb-btn" onclick="whitelistSender('${esc(msg.from_email)}')">Always allow from ${esc(msg.from_email)}</button>
      </div>
      <iframe id="msg-frame" sandbox="${sandboxAttr}"
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
    </div>
    <div class="detail-actions">
      <button class="action-btn" onclick="openReply()">↩ Reply</button>
      <button class="action-btn" onclick="openForward()">↪ Forward</button>
      <button class="action-btn" onclick="openForwardAsAttachment()" title="Forward the original message as an .eml file attachment">↪ Fwd as Attachment</button>
      <button class="action-btn" onclick="toggleStar(${msg.id})">${msg.is_starred?'★ Unstar':'☆ Star'}</button>
      <button class="action-btn" onclick="markRead(${msg.id},${!msg.is_read})">${msg.is_read?'Mark unread':'Mark read'}</button>
      <button class="action-btn" onclick="showMessageHeaders(${msg.id})">⋮ Headers</button>
      <button class="action-btn" onclick="downloadEML(${msg.id})">⬇ Download</button>
      <button class="action-btn danger" onclick="deleteMessage(${msg.id})">🗑 Delete</button>
    </div>
    ${attachHtml}
    <div class="detail-body">${bodyHtml}</div>`;

  // Auto-size iframe via postMessage from injected height-reporting script.
  // We cannot use contentDocument (null without allow-same-origin in sandbox).
  if (msg.body_html) {
    const frame = document.getElementById('msg-frame');
    if (frame) {
      // Clean up any previous listener
      if (window._frameMsgHandler) window.removeEventListener('message', window._frameMsgHandler);
      let lastH = 0;
      window._frameMsgHandler = (e) => {
        if (e.data?.type === 'gomail-frame-h' && e.data.h > 50) {
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
  const otherFolders = S.folders.filter(f=>!f.is_hidden&&f.id!==S.currentFolder).slice(0,16);
  const moveItems = otherFolders.map(f=>`<div class="ctx-item ctx-sub-item" onclick="moveMessage(${id},${f.id});closeMenu()">${esc(f.name)}</div>`).join('');
  const moveSub = otherFolders.length ? `
    <div class="ctx-item ctx-has-sub">📂 Move to
      <span class="ctx-sub-arrow">›</span>
      <div class="ctx-submenu">${moveItems}</div>
    </div>` : '';
  showCtxMenu(e,`
    <div class="ctx-item" onclick="openReplyTo(${id});closeMenu()">↩ Reply</div>
    <div class="ctx-item" onclick="toggleStar(${id});closeMenu()">${msg?.is_starred?'★ Unstar':'☆ Star'}</div>
    <div class="ctx-item" onclick="markRead(${id},${msg?.is_read?'false':'true'});closeMenu()">${msg?.is_read?'Mark unread':'Mark read'}</div>
    <div class="ctx-sep"></div>
    ${moveSub}
    <div class="ctx-item" onclick="showMessageHeaders(${id});closeMenu()">⋮ View headers</div>
    <div class="ctx-item" onclick="downloadEML(${id});closeMenu()">⬇ Download .eml</div>
    <div class="ctx-sep"></div>
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

async function deleteMessage(id) {
  inlineConfirm('Delete this message?', async () => {
    const r=await api('DELETE','/messages/'+id);
    if(r?.ok){toast('Deleted','success');S.messages=S.messages.filter(m=>m.id!==id);renderMessageList();
      if(S.currentMessage?.id===id)resetDetail();loadFolders();}
    else toast('Delete failed','error');
  });
}

function resetDetail() {
  S.currentMessage=null;S.selectedMessageId=null;
  document.getElementById('message-detail').innerHTML=`<div class="no-message">
    <svg viewBox="0 0 24 24"><path d="M20 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z"/></svg>
    <h3>Select a message</h3><p>Choose a message to read it</p></div>`;
}

function formatSize(b){if(!b)return'';if(b<1024)return b+' B';if(b<1048576)return Math.round(b/1024)+' KB';return(b/1048576).toFixed(1)+' MB';}

// ── Compose ────────────────────────────────────────────────────────────────
let composeAttachments=[];

function populateComposeFrom() {
  const sel=document.getElementById('compose-from');
  if(!sel) return;
  sel.innerHTML=S.accounts.map(a=>`<option value="${a.id}">${esc(a.display_name||a.email_address)} &lt;${esc(a.email_address)}&gt;</option>`).join('');
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
  const editor=document.getElementById('compose-editor');
  editor.innerHTML=opts.body||'';
  S.draftDirty=false;
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
  if (!skipCheck && S.draftDirty) {
    inlineConfirm('Save draft before closing?',
      ()=>{ saveDraft(); _closeCompose(); },
      ()=>{ _closeCompose(); }
    );
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

function showCCRow()  { document.getElementById('cc-row').style.display='flex'; }
function showBCCRow() { document.getElementById('bcc-row').style.display='flex'; }

function openReply() { if (S.currentMessage) openReplyTo(S.currentMessage.id); }

function openReplyTo(msgId) {
  const msg=(S.currentMessage?.id===msgId)?S.currentMessage:S.messages.find(m=>m.id===msgId);
  if (!msg) return;
  openCompose({
    mode:'reply', replyId:msgId, title:'Reply',
    subject:msg.subject&&!msg.subject.startsWith('Re:')?'Re: '+msg.subject:(msg.subject||''),
    body:`<br><br><div class="quote-divider">—— Original message ——</div><blockquote>${msg.body_html||('<pre>'+esc(msg.body_text||'')+'</pre>')}</blockquote>`,
  });
  addTag('compose-to', msg.from_email||'');
}

function openForward() {
  if (!S.currentMessage) return;
  const msg=S.currentMessage;
  S.composeForwardFromId=msg.id;
  openCompose({
    mode:'forward', forwardId:msg.id, title:'Forward',
    subject:'Fwd: '+(msg.subject||''),
    body:`<br><br><div class="quote-divider">—— Forwarded message ——<br>From: ${esc(msg.from_email||'')}</div><blockquote>${msg.body_html||('<pre>'+esc(msg.body_text||'')+'</pre>')}</blockquote>`,
  });
}

function openForwardAsAttachment() {
  if (!S.currentMessage) return;
  const msg=S.currentMessage;
  S.composeForwardFromId=msg.id;
  openCompose({
    mode:'forward-attachment', forwardId:msg.id, title:'Forward as Attachment',
    subject:'Fwd: '+(msg.subject||''),
    body:'',
  });
  // Add a visual placeholder chip (the actual EML is fetched server-side)
  composeAttachments=[{name: sanitizeSubject(msg.subject||'message')+'.eml', size:0, isForward:true}];
  updateAttachList();
}

function sanitizeSubject(s){return s.replace(/[/\\:*?"<>|]/g,'_').slice(0,60)||'message';}

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

  const commit = () => {
    const v=inp.value.trim().replace(/[,;\s]+$/,'');
    if(v){ addTag(containerId,v); inp.value=''; }
  };

  inp.addEventListener('keydown', e=>{
    if(e.key==='Enter'||e.key===','||e.key===';') { e.preventDefault(); commit(); }
    else if(e.key===' ') {
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
    subject:document.getElementById('compose-subject').value,
    body_html:editor.innerHTML.trim(),
    body_text:editor.innerText.trim(),
  };
  const r=await api('POST','/draft',meta);
  if(!silent) toast('Draft saved','success');
  else if(r?.ok) toast('Draft auto-saved to server','success');
}

// ── Compose formatting ─────────────────────────────────────────────────────
function execFmt(cmd,val) { document.getElementById('compose-editor').focus(); document.execCommand(cmd,false,val||null); }
function triggerAttach() { document.getElementById('compose-attach-input').click(); }
function handleAttachFiles(input) { for(const file of input.files) composeAttachments.push({file,name:file.name,size:file.size}); input.value=''; updateAttachList(); S.draftDirty=true; }
function removeAttachment(i) {
  // Don't remove EML forward placeholder (isForward) from UI; it's handled server-side
  if(composeAttachments[i]?.isForward && S.composeMode==='forward-attachment'){
    toast('The original message will be attached when sent','info'); return;
  }
  composeAttachments.splice(i,1); updateAttachList();
}
function updateAttachList() {
  const el=document.getElementById('compose-attach-list');
  if(!composeAttachments.length){el.innerHTML='';return;}
  el.innerHTML=composeAttachments.map((a,i)=>`<div class="attachment-chip">
    📎 <span>${esc(a.name)}</span>
    <span style="color:var(--muted);font-size:10px">${a.size?formatSize(a.size):''}</span>
    <button onclick="removeAttachment(${i})" class="tag-remove" type="button">×</button>
  </div>`).join('');
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
    }
  });
}

async function sendMessage() {
  const accountId=parseInt(document.getElementById('compose-from')?.value||0);
  const to=getTagValues('compose-to');
  if(!accountId||!to.length){toast('From account and To address required','error');return;}
  const editor=document.getElementById('compose-editor');
  const bodyHTML=editor.innerHTML.trim(), bodyText=editor.innerText.trim();
  const btn=document.getElementById('send-btn');
  btn.disabled=true; btn.textContent='Sending…';

  const endpoint=S.composeMode==='reply'?'/reply'
    :S.composeMode==='forward'?'/forward'
    :S.composeMode==='forward-attachment'?'/forward-attachment'
    :'/send';

  const meta={
    account_id:accountId, to,
    cc:getTagValues('compose-cc-tags'),
    bcc:getTagValues('compose-bcc-tags'),
    subject:document.getElementById('compose-subject').value,
    body_text:bodyText, body_html:bodyHTML,
    in_reply_to_id:S.composeMode==='reply'?S.composeReplyToId:0,
    forward_from_id:(S.composeMode==='forward'||S.composeMode==='forward-attachment')?S.composeForwardFromId:0,
  };

  let r;
  // Use FormData when there are real file attachments, OR when forwarding as attachment
  // (server needs multipart so it can read forward_from_id from meta and fetch the EML itself)
  const hasRealFiles = composeAttachments.some(a => a.file instanceof Blob);
  const needsFormData = hasRealFiles || S.composeMode === 'forward-attachment';
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
  if(r?.ok){ toast('Message sent!','success'); clearDraftAutosave(); _closeCompose(); }
  else toast(r?.error||'Send failed','error');
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
  loadSyncInterval();
  renderMFAPanel();
}

async function loadSyncInterval() {
  const r=await api('GET','/sync-interval');
  if(r) document.getElementById('sync-interval-select').value=String(r.sync_interval||15);
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

async function doLogout() { await fetch('/auth/logout',{method:'POST'}); location.href='/auth/login'; }

// ── Context menu helper ────────────────────────────────────────────────────
function showCtxMenu(e, html) {
  const menu=document.getElementById('ctx-menu');
  menu.innerHTML=html; menu.classList.add('open');
  requestAnimationFrame(()=>{
    menu.style.left=Math.min(e.clientX,window.innerWidth-menu.offsetWidth-8)+'px';
    menu.style.top=Math.min(e.clientY,window.innerHeight-menu.offsetHeight-8)+'px';
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
  // Request browser notification permission (non-blocking)
  if ('Notification' in window && Notification.permission === 'default') {
    Notification.requestPermission().then(p => {
      POLLER.notifGranted = p === 'granted';
    });
  } else if ('Notification' in window) {
    POLLER.notifGranted = Notification.permission === 'granted';
  }

  POLLER.active = true;
  schedulePoll();
}

function schedulePoll() {
  if (!POLLER.active) return;
  POLLER.timer = setTimeout(runPoll, 20000); // 20 second interval
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

      // Refresh current view if we're looking at inbox/unified
      const isInboxView = S.currentFolder === 'unified' ||
        S.folders.find(f => f.id === S.currentFolder && f.folder_type === 'inbox');
      if (isInboxView) {
        await loadMessages();
        await loadFolders();
      } else {
        await loadFolders(); // update counts in sidebar
      }
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
  // Update browser tab title
  const base = 'GoWebMail';
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

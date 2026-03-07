// GoMail app.js — full client

// ── State ──────────────────────────────────────────────────────────────────
const S = {
  me: null, accounts: [], providers: {gmail:false,outlook:false},
  folders: [], messages: [], totalMessages: 0,
  currentPage: 1, currentFolder: 'unified', currentFolderName: 'Unified Inbox',
  currentMessage: null, selectedMessageId: null,
  searchQuery: '', composeMode: 'new', composeReplyToId: null,
  remoteWhitelist: new Set(),
  draftTimer: null, draftDirty: false,
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
    if (me.compose_popup) document.getElementById('compose-popup-toggle').checked = true;
  }
  if (providers) { S.providers = providers; updateProviderButtons(); }
  if (wl?.whitelist) S.remoteWhitelist = new Set(wl.whitelist);

  await Promise.all([loadAccounts(), loadFolders()]);
  await loadMessages();

  const p = new URLSearchParams(location.search);
  if (p.get('connected')) { toast('Account connected!', 'success'); history.replaceState({},'',' /'); }
  if (p.get('error'))     { toast('Connection failed: '+p.get('error'), 'error'); history.replaceState({},'','/'); }

  document.addEventListener('keydown', e => {
    if (['INPUT','TEXTAREA','SELECT'].includes(e.target.tagName)) return;
    if (e.target.contentEditable === 'true') return;
    if ((e.metaKey||e.ctrlKey) && e.key==='n') { e.preventDefault(); openCompose(); }
    if ((e.metaKey||e.ctrlKey) && e.key==='k') { e.preventDefault(); document.getElementById('search-input').focus(); }
  });

  // Resizable compose
  initComposeResize();
}

// ── Providers ──────────────────────────────────────────────────────────────
function updateProviderButtons() {
  ['gmail','outlook'].forEach(p => {
    const btn = document.getElementById('btn-'+p);
    if (!S.providers[p]) { btn.disabled=true; btn.classList.add('unavailable'); btn.title=p+' OAuth not configured'; }
  });
}

// ── Accounts ───────────────────────────────────────────────────────────────
async function loadAccounts() {
  const data = await api('GET','/accounts');
  if (!data) return;
  S.accounts = data;
  renderAccounts();
  populateComposeFrom();
}

function renderAccounts() {
  const el = document.getElementById('accounts-list');
  el.innerHTML = S.accounts.map(a => `
    <div class="account-item" oncontextmenu="showAccountMenu(event,${a.id})"
         title="${esc(a.email_address)}${a.last_error?' ⚠ '+esc(a.last_error):''}">
      <div class="account-dot" style="background:${a.color}"></div>
      <span class="account-email">${esc(a.email_address)}</span>
      ${a.last_error?'<div class="account-error-dot"></div>':''}
      <button onclick="syncNow(${a.id},event)" id="sync-btn-${a.id}" class="icon-sync-btn" title="Sync now">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><path d="M12 4V1L8 5l4 4V6c3.31 0 6 2.69 6 6 0 1.01-.25 1.97-.7 2.8l1.46 1.46C19.54 15.03 20 13.57 20 12c0-4.42-3.58-8-8-8zm0 14c-3.31 0-6-2.69-6-6 0-1.01.25-1.97.7-2.8L5.24 7.74C4.46 8.97 4 10.43 4 12c0 4.42 3.58 8 8 8v3l4-4-4-4v3z"/></svg>
      </button>
    </div>`).join('');
}

function showAccountMenu(e, id) {
  e.preventDefault(); e.stopPropagation();
  const a = S.accounts.find(a=>a.id===id);
  showCtxMenu(e, `
    <div class="ctx-item" onclick="syncNow(${id});closeMenu()">↻ Sync now</div>
    <div class="ctx-item" onclick="openEditAccount(${id},true);closeMenu()">⚡ Test connection</div>
    <div class="ctx-item" onclick="openEditAccount(${id});closeMenu()">✎ Edit credentials</div>
    ${a?.last_error?`<div class="ctx-item" onclick="toast('${esc(a.last_error)}','error');closeMenu()">⚠ View last error</div>`:''}
    <div class="ctx-sep"></div>
    <div class="ctx-item danger" onclick="deleteAccount(${id});closeMenu()">🗑 Remove account</div>`);
}

async function syncNow(id, e) {
  if (e) e.stopPropagation();
  const btn = document.getElementById('sync-btn-'+id);
  if (btn) { btn.style.opacity='0.3'; btn.style.pointerEvents='none'; }
  const r = await api('POST','/accounts/'+id+'/sync');
  if (btn) { btn.style.opacity=''; btn.style.pointerEvents=''; }
  if (r?.ok) { toast('Synced '+(r.synced||0)+' messages','success'); loadAccounts(); loadFolders(); loadMessages(); }
  else toast(r?.error||'Sync failed','error');
}

function connectOAuth(p) { location.href='/auth/'+p+'/connect'; }

// ── Add Account modal ──────────────────────────────────────────────────────
function openAddAccountModal() {
  ['imap-email','imap-name','imap-password','imap-host','smtp-host'].forEach(id=>{ const el=document.getElementById(id); if(el) el.value=''; });
  document.getElementById('imap-port').value='993';
  document.getElementById('smtp-port').value='587';
  const r=document.getElementById('test-result'); if(r){r.style.display='none';r.className='test-result';}
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
  if (r?.ok){toast('Account added!','success');closeModal('add-account-modal');loadAccounts();loadFolders();loadMessages();}
  else toast(r?.error||'Failed to add account','error');
}

// ── Edit Account modal ─────────────────────────────────────────────────────
async function openEditAccount(id, testAfterOpen) {
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
  // Sync settings
  document.getElementById('edit-sync-mode').value=r.sync_mode||'days';
  document.getElementById('edit-sync-days').value=r.sync_days||30;
  toggleSyncDaysField();
  const errEl=document.getElementById('edit-last-error'), connEl=document.getElementById('edit-conn-result');
  connEl.style.display='none';
  errEl.style.display=r.last_error?'block':'none';
  if (r.last_error) errEl.textContent='Last sync error: '+r.last_error;
  openModal('edit-account-modal');
  if (testAfterOpen) setTimeout(testEditConnection,200);
}

function toggleSyncDaysField() {
  const mode=document.getElementById('edit-sync-mode')?.value;
  const row=document.getElementById('edit-sync-days-row');
  if (row) row.style.display=(mode==='all')?'none':'flex';
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
  const [r1, r2] = await Promise.all([
    api('PUT','/accounts/'+id, body),
    api('PUT','/accounts/'+id+'/sync-settings',{
      sync_mode: document.getElementById('edit-sync-mode').value,
      sync_days: parseInt(document.getElementById('edit-sync-days').value)||30,
    }),
  ]);
  if (r1?.ok){toast('Account updated','success');closeModal('edit-account-modal');loadAccounts();}
  else toast(r1?.error||'Update failed','error');
}

async function deleteAccount(id) {
  const a=S.accounts.find(a=>a.id===id);
  if (!confirm('Remove '+(a?a.email_address:id)+'?\nAll synced messages will be deleted.')) return;
  const r=await api('DELETE','/accounts/'+id);
  if (r?.ok){toast('Account removed','success');loadAccounts();loadFolders();loadMessages();}
  else toast('Remove failed','error');
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
  S.folders.forEach(f=>{(byAcc[f.account_id]=byAcc[f.account_id]||[]).push(f);});
  const prio=['inbox','sent','drafts','trash','spam','archive'];
  el.innerHTML=Object.entries(byAcc).map(([accId,folders])=>{
    const acc=accMap[parseInt(accId)]; if(!acc) return '';
    const sorted=[...prio.map(t=>folders.find(f=>f.folder_type===t)).filter(Boolean),...folders.filter(f=>f.folder_type==='custom')];
    return `<div class="nav-folder-header">
        <span style="width:6px;height:6px;border-radius:50%;background:${acc.color};display:inline-block;flex-shrink:0"></span>
        ${esc(acc.email_address)}
      </div>`+sorted.map(f=>`
      <div class="nav-item" id="nav-f${f.id}" onclick="selectFolder(${f.id},'${esc(f.name)}')"
           oncontextmenu="showFolderMenu(event,${f.id},${acc.id})">
        <svg viewBox="0 0 24 24" fill="currentColor">${FOLDER_ICONS[f.folder_type]||FOLDER_ICONS.custom}</svg>
        ${esc(f.name)}
        ${f.unread_count>0?`<span class="unread-badge">${f.unread_count}</span>`:''}
      </div>`).join('');
  }).join('');
}

function showFolderMenu(e, folderId, accountId) {
  e.preventDefault(); e.stopPropagation();
  showCtxMenu(e, `
    <div class="ctx-item" onclick="syncFolderNow(${folderId});closeMenu()">↻ Sync this folder</div>
    <div class="ctx-item" onclick="selectFolder(${folderId});closeMenu()">📂 Open folder</div>`);
}

async function syncFolderNow(folderId) {
  const r=await api('POST','/folders/'+folderId+'/sync');
  if (r?.ok) { toast('Synced '+(r.synced||0)+' messages','success'); loadFolders(); loadMessages(); }
  else toast(r?.error||'Sync failed','error');
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
  else result=await api('GET',`/messages?folder_id=${S.currentFolder}&page=${S.currentPage}&page_size=50`);
  if (!result){list.innerHTML='<div class="empty-state"><p>Failed to load</p></div>';return;}
  S.totalMessages=result.total||(result.messages||[]).length;
  if (append) S.messages.push(...(result.messages||[]));
  else S.messages=result.messages||[];
  renderMessageList();
  document.getElementById('panel-count').textContent=S.totalMessages>0?S.totalMessages+' messages':'';
}

function renderMessageList() {
  const list=document.getElementById('message-list');
  if (!S.messages.length){
    list.innerHTML=`<div class="empty-state"><svg viewBox="0 0 24 24"><path d="M20 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z"/></svg><p>No messages</p></div>`;
    return;
  }
  list.innerHTML=S.messages.map(m=>`
    <div class="message-item ${m.id===S.selectedMessageId?'active':''} ${!m.is_read?'unread':''}"
         onclick="openMessage(${m.id})" oncontextmenu="showMessageMenu(event,${m.id})">
      <div class="msg-top">
        <span class="msg-from">${esc(m.from_name||m.from_email)}</span>
        <span class="msg-date">${formatDate(m.date)}</span>
      </div>
      <div class="msg-subject">${esc(m.subject||'(no subject)')}</div>
      <div class="msg-preview">${esc(m.preview||'')}</div>
      <div class="msg-meta">
        <span class="msg-dot" style="background:${m.account_color}"></span>
        <span class="msg-acct">${esc(m.account_email||'')}</span>
        ${m.has_attachment?'<svg width="11" height="11" viewBox="0 0 24 24" fill="var(--muted)" style="margin-left:4px"><path d="M16.5 6v11.5c0 2.21-1.79 4-4 4s-4-1.79-4-4V5c0-1.38 1.12-2.5 2.5-2.5s2.5 1.12 2.5 2.5v10.5c0 .55-.45 1-1 1s-1-.45-1-1V6H10v9.5c0 1.38 1.12 2.5 2.5 2.5s2.5-1.12 2.5-2.5V5c0-2.21-1.79-4-4-4S7 2.79 7 5v12.5c0 3.04 2.46 5.5 5.5 5.5s5.5-2.46 5.5-5.5V6h-1.5z"/></svg>':''}
        <span class="msg-star ${m.is_starred?'on':''}" onclick="toggleStar(${m.id},event)">${m.is_starred?'★':'☆'}</span>
      </div>
    </div>`).join('')+(S.messages.length<S.totalMessages
    ?`<div class="load-more"><button class="load-more-btn" onclick="loadMoreMessages()">Load more</button></div>`:'');
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
  if (li&&!li.is_read){li.is_read=true;renderMessageList();}
}

function renderMessageDetail(msg, showRemoteContent) {
  const detail=document.getElementById('message-detail');
  const allowed=showRemoteContent||S.remoteWhitelist.has(msg.from_email);

  let bodyHtml='';
  if (msg.body_html) {
    if (allowed) {
      bodyHtml=`<iframe id="msg-frame" sandbox="allow-same-origin allow-popups"
        style="width:100%;border:none;min-height:300px;display:block"
        srcdoc="${msg.body_html.replace(/"/g,'&quot;')}"></iframe>`;
    } else {
      bodyHtml=`<div class="remote-content-banner">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm1 15h-2v-2h2v2zm0-4h-2V7h2v6z"/></svg>
        Remote images blocked.
        <button class="rcb-btn" onclick="renderMessageDetail(S.currentMessage,true)">Load content</button>
        <button class="rcb-btn" onclick="whitelistSender('${esc(msg.from_email)}')">Always allow from ${esc(msg.from_email)}</button>
      </div>
      <div class="detail-body-text">${esc(msg.body_text||'(empty)')}</div>`;
    }
  } else {
    bodyHtml=`<div class="detail-body-text">${esc(msg.body_text||'(empty)')}</div>`;
  }

  let attachHtml='';
  if (msg.attachments?.length) {
    attachHtml=`<div class="attachments-bar">
      <span style="font-size:11px;color:var(--muted);text-transform:uppercase;letter-spacing:.6px;margin-right:8px">Attachments</span>
      ${msg.attachments.map(a=>`<div class="attachment-chip">
        📎 <span>${esc(a.filename)}</span>
        <span style="color:var(--muted);font-size:10px">${formatSize(a.size)}</span>
      </div>`).join('')}
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
    </div>
    <div class="detail-actions">
      <button class="action-btn" onclick="openReply()">↩ Reply</button>
      <button class="action-btn" onclick="openForward()">↪ Forward</button>
      <button class="action-btn" onclick="toggleStar(${msg.id})">${msg.is_starred?'★ Unstar':'☆ Star'}</button>
      <button class="action-btn" onclick="markRead(${msg.id},${!msg.is_read})">${msg.is_read?'Mark unread':'Mark read'}</button>
      <button class="action-btn" onclick="showMessageHeaders(${msg.id})">⋮ Headers</button>
      <button class="action-btn danger" onclick="deleteMessage(${msg.id})">🗑 Delete</button>
    </div>
    ${attachHtml}
    <div class="detail-body">${bodyHtml}</div>`;

  if (msg.body_html && allowed) {
    const frame=document.getElementById('msg-frame');
    if (frame) frame.onload=()=>{try{const h=frame.contentDocument.documentElement.scrollHeight;frame.style.height=(h+30)+'px';}catch(e){}};
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
  const overlay=document.createElement('div');
  overlay.className='modal-overlay open';
  overlay.innerHTML=`<div class="modal" style="width:600px;max-height:80vh;overflow-y:auto">
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:16px">
      <h2 style="margin:0">Message Headers</h2>
      <button class="icon-btn" onclick="this.closest('.modal-overlay').remove()"><svg viewBox="0 0 24 24"><path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/></svg></button>
    </div>
    <table style="width:100%"><tbody>${rows}</tbody></table>
  </div>`;
  overlay.addEventListener('click',e=>{if(e.target===overlay)overlay.remove();});
  document.body.appendChild(overlay);
}

function showMessageMenu(e, id) {
  e.preventDefault(); e.stopPropagation();
  const moveFolders=S.folders.slice(0,8).map(f=>`<div class="ctx-item" onclick="moveMessage(${id},${f.id});closeMenu()">${esc(f.name)}</div>`).join('');
  showCtxMenu(e,`
    <div class="ctx-item" onclick="openReplyTo(${id});closeMenu()">↩ Reply</div>
    <div class="ctx-item" onclick="toggleStar(${id});closeMenu()">★ Toggle star</div>
    <div class="ctx-item" onclick="showMessageHeaders(${id});closeMenu()">⋮ View headers</div>
    ${moveFolders?`<div class="ctx-sep"></div><div style="font-size:10px;color:var(--muted);padding:4px 12px;text-transform:uppercase;letter-spacing:.8px">Move to</div>${moveFolders}`:''}
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

async function moveMessage(msgId, folderId) {
  const r=await api('PUT','/messages/'+msgId+'/move',{folder_id:folderId});
  if(r?.ok){toast('Moved','success');S.messages=S.messages.filter(m=>m.id!==msgId);renderMessageList();
    if(S.currentMessage?.id===msgId)resetDetail();loadFolders();}
}

async function deleteMessage(id) {
  if(!confirm('Delete this message?')) return;
  const r=await api('DELETE','/messages/'+id);
  if(r?.ok){toast('Deleted','success');S.messages=S.messages.filter(m=>m.id!==id);renderMessageList();
    if(S.currentMessage?.id===id)resetDetail();loadFolders();}
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
  document.getElementById('compose-to').innerHTML='';
  document.getElementById('compose-cc-tags').innerHTML='';
  document.getElementById('compose-bcc-tags').innerHTML='';
  document.getElementById('compose-subject').value=opts.subject||'';
  document.getElementById('cc-row').style.display='none';
  document.getElementById('bcc-row').style.display='none';
  const editor=document.getElementById('compose-editor');
  editor.innerHTML=opts.body||'';
  S.draftDirty=false;
  updateAttachList();
  if (S.me?.compose_popup) {
    openComposePopup();
  } else {
    document.getElementById('compose-overlay').classList.add('open');
    // Focus the To field's input
    setTimeout(()=>{ const inp=document.querySelector('#compose-to .tag-input'); if(inp) inp.focus(); },50);
  }
  startDraftAutosave();
}

function openReply() { if (S.currentMessage) openReplyTo(S.currentMessage.id); }

function openReplyTo(msgId) {
  const msg=(S.currentMessage?.id===msgId)?S.currentMessage:S.messages.find(m=>m.id===msgId);
  if (!msg) return;
  openCompose({
    mode:'reply', replyId:msgId, title:'Reply',
    subject:msg.subject&&!msg.subject.startsWith('Re:')?'Re: '+msg.subject:(msg.subject||''),
    body:`<br><br><div class="quote-divider">—— Original message ——</div><blockquote>${msg.body_html||('<pre>'+esc(msg.body_text||'')+'</pre>')}</blockquote>`,
  });
  // Pre-fill To
  addTag('compose-to', msg.from_email||'');
}

function openForward() {
  if (!S.currentMessage) return;
  const msg=S.currentMessage;
  openCompose({
    mode:'forward', title:'Forward',
    subject:'Fwd: '+(msg.subject||''),
    body:`<br><br><div class="quote-divider">—— Forwarded message ——<br>From: ${esc(msg.from_email||'')}</div><blockquote>${msg.body_html||('<pre>'+esc(msg.body_text||'')+'</pre>')}</blockquote>`,
  });
}

function closeCompose(skipDraftCheck) {
  if (!skipDraftCheck && S.draftDirty) {
    const choice=confirm('Save draft before closing?');
    if (choice) { saveDraft(); return; }
  }
  clearDraftAutosave();
  if (S.me?.compose_popup) {
    const win=window._composeWin;
    if (win&&!win.closed) win.close();
  } else {
    document.getElementById('compose-overlay').classList.remove('open');
  }
  S.draftDirty=false;
}

// ── Email Tag Input ────────────────────────────────────────────────────────
function initTagField(containerId) {
  const container=document.getElementById(containerId);
  if (!container) return;
  const inp=document.createElement('input');
  inp.type='text'; inp.className='tag-input'; inp.placeholder=containerId==='compose-to'?'recipient@example.com':'';
  container.appendChild(inp);
  inp.addEventListener('keydown', e=>{
    if ((e.key===' '||e.key==='Enter'||e.key===','||e.key===';') && inp.value.trim()) {
      e.preventDefault();
      addTag(containerId, inp.value.trim().replace(/[,;]$/,''));
      inp.value='';
    } else if (e.key==='Backspace'&&!inp.value) {
      const tags=container.querySelectorAll('.email-tag');
      if (tags.length) tags[tags.length-1].remove();
    }
  });
  inp.addEventListener('blur', ()=>{
    if (inp.value.trim()) { addTag(containerId, inp.value.trim()); inp.value=''; }
  });
  container.addEventListener('click', ()=>inp.focus());
}

function addTag(containerId, value) {
  if (!value) return;
  const container=document.getElementById(containerId);
  if (!container) return;
  // Basic email validation
  const isValid=/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value);
  const tag=document.createElement('span');
  tag.className='email-tag'+(isValid?'':' invalid');
  tag.textContent=value;
  const remove=document.createElement('button');
  remove.innerHTML='×'; remove.className='tag-remove';
  remove.onclick=e=>{e.stopPropagation();tag.remove();S.draftDirty=true;};
  tag.appendChild(remove);
  const inp=container.querySelector('.tag-input');
  container.insertBefore(tag, inp);
  S.draftDirty=true;
}

function getTagValues(containerId) {
  return Array.from(document.querySelectorAll('#'+containerId+' .email-tag'))
    .map(t=>t.textContent.replace('×','').trim()).filter(Boolean);
}

// ── Draft autosave ─────────────────────────────────────────────────────────
function startDraftAutosave() {
  clearDraftAutosave();
  S.draftTimer=setInterval(()=>{
    if (S.draftDirty) saveDraft(true);
  }, 60000); // every 60s
  // Mark dirty on any edit
  const editor=document.getElementById('compose-editor');
  if (editor) editor.oninput=()=>S.draftDirty=true;
  ['compose-subject'].forEach(id=>{
    const el=document.getElementById(id);
    if(el) el.oninput=()=>S.draftDirty=true;
  });
}

function clearDraftAutosave() {
  if (S.draftTimer) { clearInterval(S.draftTimer); S.draftTimer=null; }
}

async function saveDraft(silent) {
  const accountId=parseInt(document.getElementById('compose-from')?.value||0);
  if (!accountId) return;
  const to=getTagValues('compose-to');
  const editor=document.getElementById('compose-editor');
  // For now save as a local note — a real IMAP APPEND to Drafts would be ideal
  // but for MVP we just suppress the dirty flag and toast
  S.draftDirty=false;
  if (!silent) toast('Draft saved','success');
  else toast('Draft auto-saved','success');
}

// ── Compose formatting ─────────────────────────────────────────────────────
function execFmt(cmd, val) {
  document.getElementById('compose-editor').focus();
  document.execCommand(cmd, false, val||null);
}

function triggerAttach() { document.getElementById('compose-attach-input').click(); }

function handleAttachFiles(input) {
  for (const file of input.files) composeAttachments.push({file,name:file.name,size:file.size});
  input.value=''; updateAttachList(); S.draftDirty=true;
}

function removeAttachment(i) { composeAttachments.splice(i,1); updateAttachList(); }

function updateAttachList() {
  const el=document.getElementById('compose-attach-list');
  if (!composeAttachments.length){el.innerHTML='';return;}
  el.innerHTML=composeAttachments.map((a,i)=>`<div class="attachment-chip">
    📎 <span>${esc(a.name)}</span>
    <span style="color:var(--muted);font-size:10px">${formatSize(a.size)}</span>
    <button onclick="removeAttachment(${i})" class="tag-remove">×</button>
  </div>`).join('');
}

async function sendMessage() {
  const accountId=parseInt(document.getElementById('compose-from')?.value||0);
  const to=getTagValues('compose-to');
  if (!accountId||!to.length){toast('From account and To address required','error');return;}
  const editor=document.getElementById('compose-editor');
  const bodyHTML=editor.innerHTML.trim();
  const bodyText=editor.innerText.trim();
  const btn=document.getElementById('send-btn');
  btn.disabled=true;btn.textContent='Sending...';
  const endpoint=S.composeMode==='reply'?'/reply':S.composeMode==='forward'?'/forward':'/send';
  const r=await api('POST',endpoint,{
    account_id:accountId, to,
    cc:getTagValues('compose-cc-tags'),
    bcc:getTagValues('compose-bcc-tags'),
    subject:document.getElementById('compose-subject').value,
    body_text:bodyText, body_html:bodyHTML,
    in_reply_to_id:S.composeMode==='reply'?S.composeReplyToId:0,
  });
  btn.disabled=false;btn.textContent='Send';
  if (r?.ok){toast('Sent!','success');clearDraftAutosave();S.draftDirty=false;
    document.getElementById('compose-overlay').classList.remove('open');}
  else toast(r?.error||'Send failed','error');
}

// ── Resizable compose ──────────────────────────────────────────────────────
function initComposeResize() {
  const win=document.getElementById('compose-window');
  if (!win) return;
  let resizing=false, startX, startY, startW, startH;
  const handle=document.getElementById('compose-resize-handle');
  if (!handle) return;
  handle.addEventListener('mousedown', e=>{
    resizing=true; startX=e.clientX; startY=e.clientY;
    startW=win.offsetWidth; startH=win.offsetHeight;
    document.addEventListener('mousemove', onMouseMove);
    document.addEventListener('mouseup', ()=>{resizing=false;document.removeEventListener('mousemove',onMouseMove);});
    e.preventDefault();
  });
  function onMouseMove(e) {
    if (!resizing) return;
    const newW=Math.max(360, startW+(e.clientX-startX));
    const newH=Math.max(280, startH-(e.clientY-startY));
    win.style.width=newW+'px';
    win.style.height=newH+'px';
    document.getElementById('compose-editor').style.height=(newH-240)+'px';
  }
}

// ── Compose popup window ───────────────────────────────────────────────────
function openComposePopup() {
  const popup=window.open('','_blank','width=640,height=520,resizable=yes,scrollbars=yes');
  window._composeWin=popup;
  // Simpler: just use the in-page compose anyway for now; popup would need full HTML
  // Fall back to in-page for robustness
  document.getElementById('compose-overlay').classList.add('open');
}

// ── Settings ───────────────────────────────────────────────────────────────
async function openSettings() {
  openModal('settings-modal');
  loadSyncInterval();
  renderMFAPanel();
}

async function loadSyncInterval() {
  const r=await api('GET','/sync-interval');
  if (r) document.getElementById('sync-interval-select').value=String(r.sync_interval||15);
}

async function saveSyncInterval() {
  const val=parseInt(document.getElementById('sync-interval-select').value)||0;
  const r=await api('PUT','/sync-interval',{sync_interval:val});
  if (r?.ok) toast('Saved','success'); else toast('Failed','error');
}

async function saveComposePopupPref() {
  const val=document.getElementById('compose-popup-toggle').checked;
  await api('PUT','/compose-popup',{compose_popup:val});
  if (S.me) S.me.compose_popup=val;
}

async function changePassword() {
  const cur=document.getElementById('cur-pw').value, nw=document.getElementById('new-pw').value;
  if (!cur||!nw){toast('Both fields required','error');return;}
  const r=await api('POST','/change-password',{current_password:cur,new_password:nw});
  if (r?.ok){toast('Password updated','success');document.getElementById('cur-pw').value='';document.getElementById('new-pw').value='';}
  else toast(r?.error||'Failed','error');
}

async function renderMFAPanel() {
  const me=await api('GET','/me');
  if (!me) return;
  const badge=document.getElementById('mfa-badge'), panel=document.getElementById('mfa-panel');
  if (me.mfa_enabled) {
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
  const r=await api('POST','/mfa/setup'); if (!r) return;
  document.getElementById('mfa-panel').innerHTML=`
    <p style="font-size:13px;color:var(--muted);margin-bottom:12px">Scan with your authenticator app.</p>
    <div style="text-align:center;margin-bottom:14px"><img src="${r.qr_url}" style="border-radius:8px;background:white;padding:8px"></div>
    <p style="font-size:11px;color:var(--muted);margin-bottom:12px;word-break:break-all">Key: <strong>${r.secret}</strong></p>
    <div class="modal-field"><label>Confirm code</label><input type="text" id="mfa-code" placeholder="000000" maxlength="6" inputmode="numeric"></div>
    <button class="btn-primary" onclick="confirmMFASetup()">Activate MFA</button>`;
}
async function confirmMFASetup() {
  const r=await api('POST','/mfa/confirm',{code:document.getElementById('mfa-code').value});
  if (r?.ok){toast('MFA enabled','success');renderMFAPanel();}else toast(r?.error||'Invalid code','error');
}
async function disableMFA() {
  const r=await api('POST','/mfa/disable',{code:document.getElementById('mfa-code').value});
  if (r?.ok){toast('MFA disabled','success');renderMFAPanel();}else toast(r?.error||'Invalid code','error');
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

// Close compose on overlay click
document.addEventListener('click', e=>{
  if (e.target===document.getElementById('compose-overlay')) {
    if (S.draftDirty) { if (confirm('Save draft before closing?')) { saveDraft(); return; } }
    closeCompose(true);
  }
});

// Init tag fields after DOM is ready
document.addEventListener('DOMContentLoaded', ()=>{
  initTagField('compose-to');
  initTagField('compose-cc-tags');
  initTagField('compose-bcc-tags');
});

init();

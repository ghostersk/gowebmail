// ── Contacts & Calendar ─────────────────────────────────────────────────────

let _currentView = 'mail';

// ======== VIEW SWITCHING ========
// Uses data-view attribute on #app-root to switch panels via CSS,
// avoiding direct style manipulation of elements that may not exist.

function _setView(view) {
  _currentView = view;
  // Update nav item active states
  ['nav-unified','nav-starred','nav-contacts','nav-calendar'].forEach(id => {
    document.getElementById(id)?.classList.remove('active');
  });
  // Show/hide panels
  const mail1 = document.getElementById('message-list-panel');
  const mail2 = document.getElementById('message-detail');
  const contacts = document.getElementById('contacts-panel');
  const calendar = document.getElementById('calendar-panel');
  if (mail1) mail1.style.display = view === 'mail' ? '' : 'none';
  if (mail2) mail2.style.display = view === 'mail' ? '' : 'none';
  if (contacts) contacts.style.display = view === 'contacts' ? 'flex' : 'none';
  if (calendar) calendar.style.display = view === 'calendar' ? 'flex' : 'none';
}

function showMail() {
  _setView('mail');
  document.getElementById('nav-unified')?.classList.add('active');
}

function showContacts() {
  _setView('contacts');
  document.getElementById('nav-contacts')?.classList.add('active');
  if (typeof mobCloseNav === 'function') { mobCloseNav(); mobSetView('list'); }
  loadContacts();
}

function showCalendar() {
  _setView('calendar');
  document.getElementById('nav-calendar')?.classList.add('active');
  if (typeof mobCloseNav === 'function') { mobCloseNav(); mobSetView('list'); }
  calRender();
}

// Patch selectFolder — called from app.js sidebar click handlers.
// When a mail folder is clicked while contacts/calendar is showing, switch back to mail first.
// Avoids infinite recursion by checking _currentView before doing anything.
(function() {
  const _orig = window.selectFolder;
  window.selectFolder = function(folderId, folderName) {
    if (_currentView !== 'mail') {
      showMail();
      // Give the DOM a tick to re-show the mail panels before loading
      setTimeout(function() {
        _orig && _orig(folderId, folderName);
      }, 10);
      return;
    }
    _orig && _orig(folderId, folderName);
  };
})();

// ======== CONTACTS ========

let _contacts = [];
let _editingContactId = null;

async function loadContacts() {
  const data = await api('GET', '/contacts');
  _contacts = data || [];
  renderContacts(_contacts);
}

function renderContacts(list) {
  const el = document.getElementById('contacts-list');
  if (!el) return;
  if (!list || list.length === 0) {
    el.innerHTML = `<div style="text-align:center;padding:60px 20px;color:var(--muted)">
      <svg viewBox="0 0 24 24" width="48" height="48" fill="currentColor" style="opacity:.25;margin-bottom:12px;display:block;margin:0 auto 12px"><path d="M20 0H4v2h16V0zM0 4v18h24V4H0zm22 16H2V6h20v14zM12 11c1.66 0 3-1.34 3-3s-1.34-3-3-3-3 1.34-3 3 1.34 3 3 3zm-6 6c0-2.21 2.69-4 6-4s6 1.79 6 4H6z"/></svg>
      <p>No contacts yet. Click "+ New Contact" to add one.</p>
    </div>`;
    return;
  }
  el.innerHTML = list.map(c => {
    const initials = (c.display_name || c.email || '?').split(' ').map(w => w[0]).join('').substring(0,2).toUpperCase();
    const color = c.avatar_color || '#6b7280';
    const meta = [c.email, c.company].filter(Boolean).join(' · ');
    return `<div class="contact-card" onclick="openContactForm(${c.id})">
      <div class="contact-avatar" style="background:${esc(color)}">${esc(initials)}</div>
      <div class="contact-info">
        <div class="contact-name">${esc(c.display_name || c.email)}</div>
        <div class="contact-meta">${esc(meta)}</div>
      </div>
      <button class="btn-secondary" style="font-size:11px;padding:4px 8px" onclick="event.stopPropagation();composeToContact('${esc(c.email)}')">Mail</button>
    </div>`;
  }).join('');
}

function filterContacts(q) {
  if (!q) { renderContacts(_contacts); return; }
  const lower = q.toLowerCase();
  renderContacts(_contacts.filter(c =>
    (c.display_name||'').toLowerCase().includes(lower) ||
    (c.email||'').toLowerCase().includes(lower) ||
    (c.company||'').toLowerCase().includes(lower)
  ));
}

function composeToContact(email) {
  showMail();
  setTimeout(() => {
    if (typeof openCompose === 'function') openCompose();
    setTimeout(() => { if (typeof addTag === 'function') addTag('compose-to', email); }, 100);
  }, 50);
}

function openContactForm(id) {
  _editingContactId = id || null;
  const delBtn = document.getElementById('cf-delete-btn');
  if (id) {
    document.getElementById('contact-modal-title').textContent = 'Edit Contact';
    if (delBtn) delBtn.style.display = '';
    const c = _contacts.find(x => x.id === id);
    if (c) {
      document.getElementById('cf-name').value = c.display_name || '';
      document.getElementById('cf-email').value = c.email || '';
      document.getElementById('cf-phone').value = c.phone || '';
      document.getElementById('cf-company').value = c.company || '';
      document.getElementById('cf-notes').value = c.notes || '';
    }
  } else {
    document.getElementById('contact-modal-title').textContent = 'New Contact';
    if (delBtn) delBtn.style.display = 'none';
    ['cf-name','cf-email','cf-phone','cf-company','cf-notes'].forEach(id => {
      const el = document.getElementById(id); if (el) el.value = '';
    });
  }
  openModal('contact-modal');
}

async function saveContact() {
  const body = {
    display_name: document.getElementById('cf-name').value.trim(),
    email: document.getElementById('cf-email').value.trim(),
    phone: document.getElementById('cf-phone').value.trim(),
    company: document.getElementById('cf-company').value.trim(),
    notes: document.getElementById('cf-notes').value.trim(),
  };
  if (!body.display_name && !body.email) { toast('Name or email is required','error'); return; }
  if (_editingContactId) {
    await api('PUT', `/contacts/${_editingContactId}`, body);
  } else {
    await api('POST', '/contacts', body);
  }
  closeModal('contact-modal');
  await loadContacts();
  toast(_editingContactId ? 'Contact updated' : 'Contact saved', 'success');
}

async function deleteContact() {
  if (!_editingContactId) return;
  if (!confirm('Delete this contact?')) return;
  await api('DELETE', `/contacts/${_editingContactId}`);
  closeModal('contact-modal');
  await loadContacts();
  toast('Contact deleted', 'success');
}

// ======== CALENDAR ========

const CAL = {
  view: 'month',
  cursor: new Date(),
  events: [],
};

function calSetView(v) {
  CAL.view = v;
  document.getElementById('cal-btn-month')?.classList.toggle('active', v === 'month');
  document.getElementById('cal-btn-week')?.classList.toggle('active', v === 'week');
  calRender();
}

function calNav(dir) {
  if (CAL.view === 'month') {
    CAL.cursor = new Date(CAL.cursor.getFullYear(), CAL.cursor.getMonth() + dir, 1);
  } else {
    CAL.cursor = new Date(CAL.cursor.getTime() + dir * 7 * 86400000);
  }
  calRender();
}

function calGoToday() { CAL.cursor = new Date(); calRender(); }

async function calRender() {
  const gridEl = document.getElementById('cal-grid');
  if (!gridEl) return;

  let from, to;
  if (CAL.view === 'month') {
    from = new Date(CAL.cursor.getFullYear(), CAL.cursor.getMonth(), 1);
    to   = new Date(CAL.cursor.getFullYear(), CAL.cursor.getMonth() + 1, 0);
    from = new Date(from.getTime() - from.getDay() * 86400000);
    to   = new Date(to.getTime() + (6 - to.getDay()) * 86400000);
  } else {
    const dow = CAL.cursor.getDay();
    from = new Date(CAL.cursor.getTime() - dow * 86400000);
    to   = new Date(from.getTime() + 6 * 86400000);
  }

  const fmt = d => d.toISOString().split('T')[0];
  const data = await api('GET', `/calendar/events?from=${fmt(from)}&to=${fmt(to)}`);
  CAL.events = data || [];

  const months = ['January','February','March','April','May','June','July','August','September','October','November','December'];
  const titleEl = document.getElementById('cal-title');
  if (CAL.view === 'month') {
    if (titleEl) titleEl.textContent = `${months[CAL.cursor.getMonth()]} ${CAL.cursor.getFullYear()}`;
    calRenderMonth(from, to);
  } else {
    if (titleEl) titleEl.textContent = `${months[from.getMonth()]} ${from.getDate()} – ${months[to.getMonth()]} ${to.getDate()}, ${to.getFullYear()}`;
    calRenderWeek(from);
  }
}

function calRenderMonth(from, to) {
  const days = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat'];
  const today = new Date(); today.setHours(0,0,0,0);
  let html = `<div class="cal-grid-month">`;
  days.forEach(d => html += `<div class="cal-day-header">${d}</div>`);
  const cur = new Date(from);
  const curMonth = CAL.cursor.getMonth();
  while (cur <= to) {
    const dateStr = cur.toISOString().split('T')[0];
    const isToday = cur.getTime() === today.getTime();
    const isOther = cur.getMonth() !== curMonth;
    const dayEvents = CAL.events.filter(e => e.start_time && e.start_time.startsWith(dateStr));
    const shown = dayEvents.slice(0, 3);
    const more = dayEvents.length - 3;
    html += `<div class="cal-day${isToday?' today':''}${isOther?' other-month':''}" data-date="${dateStr}">
      <div class="cal-day-num" onclick="openEventForm(null,'${dateStr}T09:00')">${cur.getDate()}</div>
      ${shown.map(ev=>`<div class="cal-event" style="background:${ev.color||'#0078D4'}"
        onclick="openEventForm(${ev.id})" title="${esc(ev.title)}">${esc(ev.title)}</div>`).join('')}
      ${more>0?`<div class="cal-more" onclick="openEventForm(null,'${dateStr}T09:00')">+${more} more</div>`:''}
    </div>`;
    cur.setDate(cur.getDate() + 1);
  }
  html += `</div>`;
  document.getElementById('cal-grid').innerHTML = html;
}

function calRenderWeek(weekStart) {
  const days = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat'];
  const today = new Date(); today.setHours(0,0,0,0);
  let html = `<div class="cal-week-grid">`;
  html += `<div class="cal-week-header" style="background:var(--surface)"></div>`;
  for (let i=0;i<7;i++) {
    const d = new Date(weekStart.getTime()+i*86400000);
    const isT = d.getTime()===today.getTime();
    html += `<div class="cal-week-header${isT?' today-col':''}">${days[d.getDay()]} ${d.getDate()}</div>`;
  }
  for (let h=0;h<24;h++) {
    const label = h===0?'12am':h<12?`${h}am`:h===12?'12pm':`${h-12}pm`;
    html += `<div class="cal-time-col">${label}</div>`;
    for (let i=0;i<7;i++) {
      const d = new Date(weekStart.getTime()+i*86400000);
      const dateStr = d.toISOString().split('T')[0];
      const slotEvs = CAL.events.filter(ev => {
        if (!ev.start_time) return false;
        return ev.start_time.startsWith(dateStr) &&
          parseInt((ev.start_time.split('T')[1]||'').split(':')[0]||'0') === h;
      });
      const isT = d.getTime()===today.getTime();
      html += `<div class="cal-week-cell${isT?' today':''}"
        onclick="openEventForm(null,'${dateStr}T${String(h).padStart(2,'0')}:00')">
        ${slotEvs.map(ev=>`<div class="cal-event" style="background:${ev.color||'#0078D4'};font-size:10px;position:absolute;left:2px;right:2px;z-index:1"
          onclick="event.stopPropagation();openEventForm(${ev.id})">${esc(ev.title)}</div>`).join('')}
      </div>`;
    }
  }
  html += `</div>`;
  document.getElementById('cal-grid').innerHTML = html;
}

// ======== EVENT FORM ========

let _editingEventId = null;
let _selectedEvColor = '#0078D4';

function selectEvColor(el) {
  _selectedEvColor = el.dataset.color;
  document.querySelectorAll('#ev-colors span').forEach(s => s.style.borderColor = 'transparent');
  el.style.borderColor = 'white';
}

function openEventForm(id, defaultStart) {
  _editingEventId = id || null;
  const delBtn = document.getElementById('ev-delete-btn');
  _selectedEvColor = '#0078D4';
  document.querySelectorAll('#ev-colors span').forEach((s,i) => s.style.borderColor = i===0?'white':'transparent');
  if (id) {
    document.getElementById('event-modal-title').textContent = 'Edit Event';
    if (delBtn) delBtn.style.display = '';
    const ev = CAL.events.find(e => e.id === id);
    if (ev) {
      document.getElementById('ev-title').value = ev.title||'';
      document.getElementById('ev-start').value = (ev.start_time||'').replace(' ','T').substring(0,16);
      document.getElementById('ev-end').value   = (ev.end_time||'').replace(' ','T').substring(0,16);
      document.getElementById('ev-allday').checked = !!ev.all_day;
      document.getElementById('ev-location').value = ev.location||'';
      document.getElementById('ev-desc').value = ev.description||'';
      _selectedEvColor = ev.color||'#0078D4';
      document.querySelectorAll('#ev-colors span').forEach(s => {
        s.style.borderColor = s.dataset.color===_selectedEvColor ? 'white' : 'transparent';
      });
    }
  } else {
    document.getElementById('event-modal-title').textContent = 'New Event';
    if (delBtn) delBtn.style.display = 'none';
    document.getElementById('ev-title').value = '';
    const start = defaultStart || new Date().toISOString().substring(0,16);
    document.getElementById('ev-start').value = start;
    const endDate = new Date(start); endDate.setHours(endDate.getHours()+1);
    document.getElementById('ev-end').value = endDate.toISOString().substring(0,16);
    document.getElementById('ev-allday').checked = false;
    document.getElementById('ev-location').value = '';
    document.getElementById('ev-desc').value = '';
  }
  openModal('event-modal');
}

async function saveEvent() {
  const title = document.getElementById('ev-title').value.trim();
  if (!title) { toast('Title is required','error'); return; }
  const body = {
    title,
    start_time: document.getElementById('ev-start').value.replace('T',' '),
    end_time:   document.getElementById('ev-end').value.replace('T',' '),
    all_day:    document.getElementById('ev-allday').checked,
    location:   document.getElementById('ev-location').value.trim(),
    description:document.getElementById('ev-desc').value.trim(),
    color:      _selectedEvColor,
    status:     'confirmed',
  };
  if (_editingEventId) {
    await api('PUT', `/calendar/events/${_editingEventId}`, body);
  } else {
    await api('POST', '/calendar/events', body);
  }
  closeModal('event-modal');
  await calRender();
  toast(_editingEventId ? 'Event updated' : 'Event created', 'success');
}

async function deleteEvent() {
  if (!_editingEventId) return;
  if (!confirm('Delete this event?')) return;
  await api('DELETE', `/calendar/events/${_editingEventId}`);
  closeModal('event-modal');
  await calRender();
  toast('Event deleted', 'success');
}

// ======== CALDAV ========

async function showCalDAVSettings() {
  openModal('caldav-modal');
  await loadCalDAVTokens();
}

async function loadCalDAVTokens() {
  const tokens = await api('GET', '/caldav/tokens') || [];
  const el = document.getElementById('caldav-tokens-list');
  if (!el) return;
  if (!tokens.length) {
    el.innerHTML = '<p style="font-size:13px;color:var(--muted)">No tokens yet.</p>';
    return;
  }
  el.innerHTML = tokens.map(t => {
    const url = `${location.origin}/caldav/${t.token}/calendar.ics`;
    return `<div class="caldav-token-row">
      <div style="flex:1;min-width:0">
        <div style="font-size:13px;font-weight:500">${esc(t.label)}</div>
        <div class="caldav-token-url" onclick="copyCalDAVUrl('${url}')" title="Click to copy">${url}</div>
        <div style="font-size:11px;color:var(--muted)">Created: ${t.created_at}${t.last_used?' · Last used: '+t.last_used:''}</div>
      </div>
      <button class="icon-btn" onclick="revokeCalDAVToken(${t.id})" title="Revoke" style="color:var(--danger);flex-shrink:0">
        <svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>
      </button>
    </div>`;
  }).join('');
}

async function createCalDAVToken() {
  const label = document.getElementById('caldav-label').value.trim() || 'CalDAV token';
  await api('POST', '/caldav/tokens', { label });
  document.getElementById('caldav-label').value = '';
  await loadCalDAVTokens();
  toast('Token created', 'success');
}

async function revokeCalDAVToken(id) {
  if (!confirm('Revoke this token?')) return;
  await api('DELETE', `/caldav/tokens/${id}`);
  await loadCalDAVTokens();
  toast('Token revoked', 'success');
}

function copyCalDAVUrl(url) {
  navigator.clipboard.writeText(url).then(() => toast('URL copied','success'));
}

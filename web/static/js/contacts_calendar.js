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
  // App rail active state (nav-contacts/nav-calendar no longer exist — rail replaced them)
  const railFor = { mail: 'rail-mail', contacts: 'rail-people', calendar: 'rail-cal' }[view];
  ['rail-mail', 'rail-cal', 'rail-people'].forEach(id => {
    document.getElementById(id)?.classList.toggle('on', id === railFor);
  });
  // Home ribbon is mail-only (Outlook shows it in the mail view)
  const ribbon = document.getElementById('ribbon');
  if (ribbon) ribbon.style.display = (view === 'mail') ? '' : 'none';
  // Show/hide panels — mail-view wraps the message list + reading pane together
  // so they hide/show as one unit rather than two separately-toggled panels.
  const mailView = document.getElementById('mail-view');
  const contacts = document.getElementById('contacts-panel');
  const calendar = document.getElementById('calendar-panel');
  if (mailView) mailView.style.display = view === 'mail' ? '' : 'none';
  if (contacts) contacts.style.display = view === 'contacts' ? 'flex' : 'none';
  if (calendar) calendar.style.display = view === 'calendar' ? 'flex' : 'none';
}

function showMail() {
  _setView('mail');
  document.getElementById('nav-unified')?.classList.add('active');
  if (typeof layoutRibbonOverflow === 'function') layoutRibbonOverflow();
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
  calendars: [],
  hidden: JSON.parse(localStorage.getItem('calVisible.v1') || '[]'),
};

function calVisibleIds() {
  return CAL.calendars.filter(c => !CAL.hidden.includes(c.id)).map(c => c.id);
}

function calSetView(v) {
  CAL.view = v;
  document.getElementById('cal-btn-day')?.classList.toggle('active', v === 'day');
  document.getElementById('cal-btn-month')?.classList.toggle('active', v === 'month');
  document.getElementById('cal-btn-week')?.classList.toggle('active', v === 'week');
  calRender();
}

function calNav(dir) {
  if (CAL.view === 'month') {
    CAL.cursor = new Date(CAL.cursor.getFullYear(), CAL.cursor.getMonth() + dir, 1);
  } else if (CAL.view === 'day') {
    // setDate handles 25h/23h DST days; ms-arithmetic would land on the same day.
    CAL.cursor = new Date(CAL.cursor.getFullYear(), CAL.cursor.getMonth(), CAL.cursor.getDate() + dir);
  } else {
    CAL.cursor = new Date(CAL.cursor.getTime() + dir * 7 * 86400000);
  }
  calRender();
}

function calGoToday() { CAL.cursor = new Date(); calRender(); }

// ======== CALENDARS PANEL ========

const CAL_COLORS = ['#0078D4','#EA4335','#34A853','#FBBC04','#9C27B0','#FF6D00'];
let _calEditId = 0;
let _calFormColor = '#0078D4';

async function loadCalendars() {
  try {
    CAL.calendars = (await api('GET', '/calendars')) || [];
  } catch (e) {
    CAL.calendars = [];
  }
  const ex = document.getElementById('cal-export-btn');
  if (ex) {
    const vis = calVisibleIds();
    ex.href = '/api/calendar/export.ics' +
      (CAL.calendars.length && vis.length < CAL.calendars.length ? '?calendar_ids=' + vis.join(',') : '');
  }
  renderCalPanel();
}

function renderCalPanel() {
  const panel = document.getElementById('cal-cal-panel');
  if (!panel) return;
  if (!CAL.calendars.length) { panel.style.display = 'none'; return; }
  panel.style.display = '';
  const accEmail = id => {
    const a = (S.accounts || []).find(x => x.id === id);
    return a ? a.email_address : '';
  };
  panel.innerHTML = CAL.calendars.map(c => {
    const email = c.account_id ? ` <span style="color:var(--muted);font-size:11px">${esc(accEmail(c.account_id))}</span>` : '';
    return `<div class="cal-cal-row">
      <input type="checkbox" ${CAL.hidden.includes(c.id) ? '' : 'checked'} style="width:auto" onchange="calToggleVisible(${c.id}, this.checked)">
      <span style="width:10px;height:10px;border-radius:50%;background:${esc(c.color)};flex-shrink:0"></span>
      <span style="flex:1;font-size:13px">${esc(c.name)}${email}</span>
      <button class="icon-btn" style="width:22px;height:22px;font-size:12px" title="Edit calendar" onclick="calEdit(${c.id})">✎</button>
      <button class="icon-btn" style="width:22px;height:22px;font-size:12px" title="Delete calendar" onclick="calDeleteCal(${c.id})">✕</button>
    </div>`;
  }).join('') +
  `<div class="cal-cal-row" style="border-bottom:none"><span style="cursor:pointer;color:var(--accent);font-size:13px" onclick="calAddCal()">+ Add calendar</span></div>`;
}

function calCalForm(c) {
  c = c || {};
  const opts = ['<option value="">None</option>'].concat(
    (S.accounts || []).map(a =>
      `<option value="${a.id}" ${c.account_id === a.id ? 'selected' : ''}>${esc(a.email_address)}</option>`)).join('');
  return `<div class="cal-cal-row" style="flex-wrap:wrap">
    <input id="cal-name" type="text" placeholder="Name" value="${esc(c.name || '')}" style="width:130px;padding:4px 8px;font-size:13px;background:var(--bg);border:1px solid var(--border2);border-radius:6px;color:var(--text)">
    <select id="cal-acc" style="padding:4px 6px;font-size:12px;background:var(--bg);border:1px solid var(--border2);border-radius:6px;color:var(--text)">${opts}</select>
    ${CAL_COLORS.map(cl => `<span data-color="${cl}" onclick="calPickColor(this)" title="${cl}" style="width:18px;height:18px;border-radius:50%;background:${cl};cursor:pointer;border:2px solid ${c.color === cl ? 'white' : 'transparent'}"></span>`).join('')}
    <button class="btn-secondary" style="font-size:11px;padding:2px 10px" onclick="calSaveCal(${c.id || 0})">Save</button>
    <button class="btn-secondary" style="font-size:11px;padding:2px 10px" onclick="renderCalPanel()">Cancel</button>
  </div>`;
}

function calPickColor(el) {
  _calFormColor = el.dataset.color;
  document.querySelectorAll('#cal-cal-panel [data-color]').forEach(s => s.style.borderColor = 'transparent');
  el.style.borderColor = 'white';
}

function calAddCal() {
  _calEditId = 0;
  _calFormColor = CAL_COLORS[0];
  const panel = document.getElementById('cal-cal-panel');
  panel.style.display = '';
  panel.innerHTML = calCalForm(null);
  document.getElementById('cal-name').focus();
}

function calEdit(id) {
  const c = CAL.calendars.find(x => x.id === id);
  if (!c) return;
  _calEditId = id;
  _calFormColor = c.color || CAL_COLORS[0];
  const panel = document.getElementById('cal-cal-panel');
  panel.style.display = '';
  panel.innerHTML = calCalForm(c);
}

async function calSaveCal(id) {
  const name = document.getElementById('cal-name').value.trim();
  if (!name) { toast('Calendar name is required', 'error'); return; }
  const body = { name, color: _calFormColor, account_id: Number(document.getElementById('cal-acc').value) || null };
  if (id) await api('PUT', `/calendars/${id}`, body);
  else await api('POST', '/calendars', body);
  await calRender();
  toast(id ? 'Calendar updated' : 'Calendar added', 'success');
}

async function calDeleteCal(id) {
  const c = CAL.calendars.find(x => x.id === id);
  if (!c) return;
  if (!c.account_id && c.name === 'General') return; // General can't be deleted (server enforces too)
  if (!confirm(`Delete "${c.name}"? Its events will move to General.`)) return;
  await api('DELETE', `/calendars/${id}`);
  CAL.hidden = CAL.hidden.filter(h => h !== id);
  localStorage.setItem('calVisible.v1', JSON.stringify(CAL.hidden));
  await calRender();
  toast('Calendar deleted', 'success');
}

async function calToggleVisible(id, checked) {
  if (checked) CAL.hidden = CAL.hidden.filter(h => h !== id);
  else if (!CAL.hidden.includes(id)) CAL.hidden.push(id);
  localStorage.setItem('calVisible.v1', JSON.stringify(CAL.hidden));
  await calRender();
}

async function calImportFile(input) {
  const f = input.files && input.files[0];
  input.value = '';
  if (!f) return;
  const text = await f.text();
  const r = await api('POST', '/calendar/import', { text, calendar_id: 0 });
  if (r && r.imported !== undefined) {
    toast(`Imported ${r.imported} events`, 'success');
    await calRender();
  } else {
    toast('Import failed', 'error');
  }
}

async function calRender() {
  const gridEl = document.getElementById('cal-grid');
  if (!gridEl) return;

  let from, to;
  if (CAL.view === 'month') {
    from = new Date(CAL.cursor.getFullYear(), CAL.cursor.getMonth(), 1);
    to   = new Date(CAL.cursor.getFullYear(), CAL.cursor.getMonth() + 1, 0);
    from = new Date(from.getTime() - from.getDay() * 86400000);
    to   = new Date(to.getTime() + (6 - to.getDay()) * 86400000);
  } else if (CAL.view === 'day') {
    from = new Date(CAL.cursor.getFullYear(), CAL.cursor.getMonth(), CAL.cursor.getDate());
    to   = new Date(from.getTime() + 86400000);
  } else {
    const dow = CAL.cursor.getDay();
    from = new Date(CAL.cursor.getTime() - dow * 86400000);
    to   = new Date(from.getTime() + 6 * 86400000);
  }

  const fmt = d => d.toISOString().split('T')[0];
  await loadCalendars();
  const vis = calVisibleIds();
  // No filter when nothing is hidden (or the calendar list is unavailable);
  // an empty visible set means "show nothing".
  let data;
  if (CAL.calendars.length && vis.length === 0) {
    data = [];
  } else {
    const q = (CAL.calendars.length && vis.length < CAL.calendars.length) ? `&calendar_ids=${vis.join(',')}` : '';
    data = await api('GET', `/calendar/events?from=${fmt(from)}&to=${fmt(to)}${q}`);
  }
  CAL.events = data || [];
  const evs = expandRecurrences(CAL.events, fmt(from), fmt(to));

  const months = ['January','February','March','April','May','June','July','August','September','October','November','December'];
  const titleEl = document.getElementById('cal-title');
  if (CAL.view === 'month') {
    if (titleEl) titleEl.textContent = `${months[CAL.cursor.getMonth()]} ${CAL.cursor.getFullYear()}`;
    calRenderMonth(from, to, evs);
  } else if (CAL.view === 'day') {
    const wd = ['Sunday','Monday','Tuesday','Wednesday','Thursday','Friday','Saturday'][CAL.cursor.getDay()];
    if (titleEl) titleEl.textContent = `${wd}, ${months[CAL.cursor.getMonth()]} ${CAL.cursor.getDate()}, ${CAL.cursor.getFullYear()}`;
    calRenderDay(CAL.cursor, evs);
  } else {
    if (titleEl) titleEl.textContent = `${months[from.getMonth()]} ${from.getDate()} – ${months[to.getMonth()]} ${to.getDate()}, ${to.getFullYear()}`;
    calRenderWeek(from, evs);
  }
}

// Date-string helpers — work on the stored "YYYY-MM-DD" prefix so no timezone
// conversion can shift an event into the wrong day.
function calDate(s){ return (s||'').slice(0,10); }
function calHM(s){ return (s||'').slice(11,16) || '00:00'; }
function calMin(s){ const h = calHM(s); return parseInt(h.slice(0,2),10)*60 + parseInt(h.slice(3,5),10); }
function calTime(hm){
  const h = parseInt(hm.slice(0,2),10), m = hm.slice(3,5);
  return `${h%12||12}:${m} ${h>=12?'pm':'am'}`;
}
function dInc(ds, days){
  const [y,mo,d] = ds.split('-').map(Number);
  return new Date(Date.UTC(y, mo-1, d+days)).toISOString().slice(0,10);
}
function mInc(ds, months){
  const [y,mo,d] = ds.split('-').map(Number);
  return new Date(Date.UTC(y, mo-1+months, d)).toISOString().slice(0,10);
}
// A timed event covers its end day; an all-day one with a midnight end
// (iCal DTEND is exclusive) does not. shortcut: form all-day events should be
// saved with a non-midnight end to span multiple days.
function evCovers(ev, D){
  const s = calDate(ev.start_time), e = calDate(ev.end_time);
  const exclEnd = ev.all_day && calHM(ev.end_time) === '00:00' && s < e;
  return D >= s && (D < e || (D === e && !exclEnd));
}
function evLastDay(ev){
  const e = calDate(ev.end_time);
  return (ev.all_day && calHM(ev.end_time) === '00:00' && calDate(ev.start_time) < e) ? dInc(e, -1) : e;
}
// Recurrence instances for the visible range. shortcut: DAILY/WEEKLY/MONTHLY
// only, no BYDAY — upgrade if a user imports a fancier RRULE.
function expandRecurrences(events, f, t){
  const out = [];
  events.forEach(ev => {
    if (!ev.recurrence_rule || !ev.start_time) { out.push(ev); return; }
    const m = ev.recurrence_rule.match(/FREQ=(DAILY|WEEKLY|MONTHLY)(;INTERVAL=(\d+))?(;UNTIL=[A-Z0-9TZ-]+)?(;COUNT=(\d+))?/i);
    if (!m) { out.push(ev); return; }
    const freq = m[1].toUpperCase();
    const step = Math.max(1, parseInt(m[3] || '1'));
    const until = m[4] ? m[4].replace(/UNTIL=/,'').slice(0,8) : null; // YYYYMMDD
    const count = m[5] ? parseInt(m[5]) : null;
    const sDay = calDate(ev.start_time), eDay = calDate(ev.end_time);
    const sHM = calHM(ev.start_time), eHM = calHM(ev.end_time);
    const span = Math.round((Date.parse(eDay) - Date.parse(sDay)) / 86400000);
    let d = sDay, i = 0;
    while (d <= t && i < 1000) {
      if (d >= f) {
        out.push({ ...ev, id: ev.id + '~r' + i, rbase: ev.id,
          start_time: d + 'T' + sHM,
          end_time: dInc(d, span) + 'T' + eHM });
      }
      i++;
      if (count && i >= count) break;
      d = freq === 'DAILY' ? dInc(d, step) : freq === 'WEEKLY' ? dInc(d, 7*step) : mInc(d, step);
      if (until && d.replace(/-/g,'') > until) break;
    }
  });
  return out;
}

function calRenderMonth(from, to, evs) {
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
    const dayEvents = evs.filter(e => e.start_time && evCovers(e, dateStr));
    const shown = dayEvents.slice(0, 3);
    const more = dayEvents.length - 3;
    html += `<div class="cal-day${isToday?' today':''}${isOther?' other-month':''}" data-date="${dateStr}">
      <div class="cal-day-num" onclick="openEventForm(null,'${dateStr}T09:00')">${cur.getDate()}</div>
      ${shown.map(ev => {
        const isS = calDate(ev.start_time) === dateStr;
        const isE = evLastDay(ev) === dateStr;
        let cls = 'cal-event', label;
        if (isS) label = (ev.all_day ? 'All day ' : calTime(calHM(ev.start_time)) + ' ') + esc(ev.title);
        else if (isE) { cls += ' cal-evt-end'; label = esc(ev.title); }
        else { cls += ' cal-evt-mid'; label = '→ ' + esc(ev.title); }
        return `<div class="${cls}" style="background:${ev.color||'#0078D4'}"
          onclick="openEventForm(${ev.rbase||ev.id})" title="${esc(ev.title)}">${label}</div>`;
      }).join('')}
      ${more>0?`<div class="cal-more" onclick="openEventForm(null,'${dateStr}T09:00')">+${more} more</div>`:''}
    </div>`;
    cur.setDate(cur.getDate() + 1);
  }
  html += `</div>`;
  document.getElementById('cal-grid').innerHTML = html;
}

function calRenderWeek(weekStart, evs) {
  const days = ['Sun','Mon','Tue','Wed','Thu','Fri','Sat'];
  const today = new Date(); today.setHours(0,0,0,0);
  const dateStrs = [];
  for (let i=0;i<7;i++) {
    const d = new Date(weekStart.getTime()+i*86400000);
    dateStrs.push(d.toISOString().split('T')[0]);
  }
  let html = `<div class="cal-week-grid">`;
  html += `<div class="cal-week-header"></div>`;
  for (let i=0;i<7;i++) {
    const d = new Date(weekStart.getTime()+i*86400000);
    const isT = d.getTime()===today.getTime();
    html += `<div class="cal-week-header${isT?' today-col':''}">${days[d.getDay()]} ${d.getDate()}</div>`;
  }
  html += `<div class="cal-allday-strip"></div>`;
  for (let i=0;i<7;i++) {
    const dateStr = dateStrs[i];
    const allday = evs.filter(ev => ev.all_day && evCovers(ev, dateStr));
    const isT = new Date(dateStr).getTime() === today.getTime();
    html += `<div class="cal-allday-cell${isT?' today-col':''}">
      ${allday.map(ev=>`<div class="cal-event" style="background:${ev.color||'#0078D4'}"
        onclick="openEventForm(${ev.rbase||ev.id})" title="${esc(ev.title)}">${esc(ev.title)}</div>`).join('')}
    </div>`;
  }
  let timeCol = '';
  for (let h=0;h<24;h++) {
    const label = h===0?'':h<12?`${h}am`:h===12?'12pm':`${h-12}pm`;
    timeCol += `<div class="cal-time-col">${label}</div>`;
  }
  html += `<div class="cal-time-col-stack">${timeCol}</div>`;
  for (let i=0;i<7;i++) {
    const dateStr = dateStrs[i];
    const d = new Date(weekStart.getTime()+i*86400000);
    const isT = d.getTime()===today.getTime();
    const timed = evs.filter(ev => !ev.all_day && ev.start_time && evCovers(ev, dateStr));
    html += `<div class="cal-week-col${isT?' today':''}" onclick="calColClick('${dateStr}',event)">
      ${timed.map((ev,ix)=>{
        const isS = calDate(ev.start_time) === dateStr;
        const isE = evLastDay(ev) === dateStr;
        const sMin = isS ? calMin(ev.start_time) : 0;
        const eMin = isE ? calMin(ev.end_time) : 24*60;
        const top = sMin/1440*100;
        const hgt = Math.max(1.6, (eMin - sMin)/1440*100);
        const cls = 'cal-evt-abs' + (isS?' ev-start':'') + (isE?' ev-end':'');
        const label = isS ? `${calTime(calHM(ev.start_time))} ${esc(ev.title)}` : esc(ev.title);
        return `<div class="${cls}" style="background:${ev.color||'#0078D4'};top:${top}%;height:${hgt}%;left:${2+(ix%4)*2}px"
          onclick="event.stopPropagation();openEventForm(${ev.rbase||ev.id})" title="${esc(ev.title)}">${label}</div>`;
      }).join('')}
    </div>`;
  }
  html += `</div>`;
  document.getElementById('cal-grid').innerHTML = html;
}

function calColClick(dateStr, e) {
  const h = Math.min(23, Math.max(0, Math.floor(e.offsetY / 48)));
  openEventForm(null, `${dateStr}T${String(h).padStart(2,'0')}:00`);
}

function calRenderDay(day, evs) {
  const dateStr = day.toISOString().split('T')[0];
  const allday = evs.filter(ev => ev.all_day && evCovers(ev, dateStr));
  const timed = evs.filter(ev => !ev.all_day && ev.start_time && evCovers(ev, dateStr));
  let timeCol = '';
  for (let h=0;h<24;h++) {
    const label = h===0?'':h<12?`${h}am`:h===12?'12pm':`${h-12}pm`;
    timeCol += `<div class="cal-time-col">${label}</div>`;
  }
  let html = `<div class="cal-dayview">
    <div class="cal-dayview-grid">
      <div class="cal-allday-strip"></div>
      <div class="cal-allday-cell">
        ${allday.map(ev=>`<div class="cal-event" style="background:${ev.color||'#0078D4'}"
          onclick="openEventForm(${ev.rbase||ev.id})" title="${esc(ev.title)}">${esc(ev.title)}</div>`).join('')}
      </div>
      <div class="cal-time-col-stack">${timeCol}</div>
      <div class="cal-week-col" onclick="calColClick('${dateStr}',event)">
        ${timed.map((ev,ix)=>{
          const isS = calDate(ev.start_time) === dateStr;
          const isE = evLastDay(ev) === dateStr;
          const sMin = isS ? calMin(ev.start_time) : 0;
          const eMin = isE ? calMin(ev.end_time) : 24*60;
          const cls = 'cal-evt-abs' + (isS?' ev-start':'') + (isE?' ev-end':'');
          const label = isS ? `${calTime(calHM(ev.start_time))} ${esc(ev.title)}` : esc(ev.title);
          return `<div class="${cls}" style="background:${ev.color||'#0078D4'};top:${sMin/1440*100}%;height:${Math.max(1.6,(eMin-sMin)/1440*100)}%;left:${2+(ix%4)*2}px"
            onclick="event.stopPropagation();openEventForm(${ev.rbase||ev.id})" title="${esc(ev.title)}">${label}</div>`;
        }).join('')}
      </div>
    </div>
    <div class="cal-day-agenda">
      ${timed.concat(allday).sort((a,b)=>calMin(a.start_time)-calMin(b.start_time)).map(ev=>{
        const isS = calDate(ev.start_time) === dateStr;
        const isE = evLastDay(ev) === dateStr;
        let range;
        if (ev.all_day) range = 'All day';
        else if (isS && isE) range = `${calTime(calHM(ev.start_time))} – ${calTime(calHM(ev.end_time))}`;
        else if (isS) range = calTime(calHM(ev.start_time)) + ' →';
        else range = '→ ' + (isE ? calTime(calHM(ev.end_time)) : '');
        const loc = ev.location ? ` · ${esc(ev.location)}` : '';
        const att = ev.attendees && ev.attendees.trim() ? ` · 👥${ev.attendees.split(',').filter(x=>x.trim()).length}` : '';
        return `<div class="cal-agenda-item" onclick="openEventForm(${ev.rbase||ev.id})">
          <div class="cal-agenda-time">${range}</div>
          <div class="cal-agenda-title">${esc(ev.title)}</div>
          ${loc||att?`<div class="cal-agenda-meta">${loc}${att}</div>`:''}
          ${ev.description?`<div class="cal-agenda-desc">${esc(ev.description)}</div>`:''}
        </div>`;
      }).join('') || '<div class="cal-agenda-empty">No events</div>'}
    </div>
  </div>`;
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

// Fill the #ev-calendar picker (visible calendars first) and select the
// event's calendar, or the first visible one for new events.
function fillEvCalendar(preferId) {
  const sel = document.getElementById('ev-calendar');
  if (!sel) return;
  const cals = [...CAL.calendars].sort((a, b) =>
    (CAL.hidden.includes(b.id) ? 1 : 0) - (CAL.hidden.includes(a.id) ? 1 : 0));
  sel.innerHTML = cals.map(c => `<option value="${c.id}">${esc(c.name)}</option>`).join('');
  let pick = cals.find(c => c.id === preferId) || cals.find(c => !CAL.hidden.includes(c.id)) || cals[0];
  if (pick) sel.value = String(pick.id);
}

async function openEventForm(id, defaultStart) {
  _editingEventId = id || null;
  const delBtn = document.getElementById('ev-delete-btn');
  let ev = id ? CAL.events.find(e => e.id === id) : null;
  if (id && !ev) {
    // Recurrence instances are emitted for the view range even when their base starts
    // before it — fetch the base event so "Edit" can't save blanks over it.
    ev = await api('GET', '/calendar/events/' + id);
    if (!ev || !ev.id) { toast('Could not load event', 'error'); _editingEventId = null; return; }
  }
  fillEvCalendar(ev ? ev.calendar_id : 0);
  _selectedEvColor = '#0078D4';
  document.querySelectorAll('#ev-colors span').forEach((s,i) => s.style.borderColor = i===0?'white':'transparent');
  if (id) {
    document.getElementById('event-modal-title').textContent = 'Edit Event';
    if (delBtn) delBtn.style.display = '';
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
  const att = document.getElementById('ev-attendees');
  att.innerHTML = '';
  initTagField('ev-attendees');
  ((ev && ev.attendees) || '').split(',').map(x => x.trim()).filter(Boolean).forEach(a => addTag('ev-attendees', a));
  const inviteBtn = document.getElementById('ev-invite-btn');
  if (inviteBtn) inviteBtn.style.display = (id && ev && ev.account_id) ? '' : 'none';
  openModal('event-modal');
}

async function saveEvent() {
  const title = document.getElementById('ev-title').value.trim();
  if (!title) { toast('Title is required','error'); return; }
  const evCalId = Number(document.getElementById('ev-calendar').value) || 0;
  const evCal = CAL.calendars.find(x => x.id === evCalId);
  const body = {
    title,
    start_time: document.getElementById('ev-start').value.replace('T',' '),
    end_time:   document.getElementById('ev-end').value.replace('T',' '),
    all_day:    document.getElementById('ev-allday').checked,
    location:   document.getElementById('ev-location').value.trim(),
    description:document.getElementById('ev-desc').value.trim(),
    color:      _selectedEvColor,
    status:     'confirmed',
    calendar_id: evCalId,
    account_id:  evCal ? evCal.account_id : null,
    attendees:   getTagValues('ev-attendees').join(','),
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

async function inviteEvent() {
  if (!_editingEventId) return;
  const ev = CAL.events.find(e => e.id === _editingEventId);
  const n = ev ? ev.attendees.split(',').filter(x => x.trim()).length : 0;
  if (!n) { toast('Add attendees and save first', 'error'); return; }
  const res = await api('POST', `/calendar/events/${_editingEventId}/invite`);
  if (res && res.ok) toast(`Invitation sent to ${n} attendee${n > 1 ? 's' : ''}`, 'success');
  else toast('Invite failed' + (res && res.error ? ': ' + res.error : ''), 'error');
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
      <button class="icon-btn" onclick="revokeCalDAVToken(${t.id})" title="Revoke" aria-label="Revoke this CalDAV token" style="color:var(--danger);flex-shrink:0">
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

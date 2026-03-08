// GoMail shared utilities - loaded on every page

// ---- API helper ----
async function api(method, path, body) {
  const opts = { method, headers: { 'Content-Type': 'application/json' } };
  if (body !== undefined) opts.body = JSON.stringify(body);
  try {
    const r = await fetch('/api' + path, opts);
    if (r.status === 401) { location.href = '/auth/login'; return null; }
    return r.json().catch(() => null);
  } catch (e) {
    console.error('API error:', path, e);
    return null;
  }
}

// ---- Toast notifications ----
function toast(msg, type) {
  let container = document.getElementById('toast-container');
  if (!container) {
    container = document.createElement('div');
    container.id = 'toast-container';
    container.className = 'toast-container';
    document.body.appendChild(container);
  }
  const el = document.createElement('div');
  el.className = 'toast' + (type ? ' ' + type : '');
  el.textContent = msg;
  container.appendChild(el);
  setTimeout(() => { el.style.opacity = '0'; el.style.transition = 'opacity .3s'; }, 3200);
  setTimeout(() => el.remove(), 3500);
}

// ---- HTML escaping ----
function esc(s) {
  return String(s || '')
    .replace(/&/g,'&amp;')
    .replace(/</g,'&lt;')
    .replace(/>/g,'&gt;')
    .replace(/"/g,'&quot;');
}

// ---- Date formatting ----
function formatDate(d) {
  if (!d) return '';
  const date = new Date(d), now = new Date(), diff = now - date;
  if (diff < 86400000 && date.getDate() === now.getDate())
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  if (diff < 7 * 86400000)
    return date.toLocaleDateString([], { weekday: 'short' });
  return date.toLocaleDateString([], { month: 'short', day: 'numeric' });
}

function formatFullDate(d) {
  return d ? new Date(d).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }) : '';
}

// ---- Context menu helpers ----
function closeMenu() {
  const m = document.getElementById('ctx-menu');
  if (m) m.classList.remove('open');
}

function positionMenu(menu, x, y) {
  menu.style.left = Math.min(x, window.innerWidth  - menu.offsetWidth  - 8) + 'px';
  menu.style.top  = Math.min(y, window.innerHeight - menu.offsetHeight - 8) + 'px';
}

// ---- Debounce ----
function debounce(fn, ms) {
  let t;
  return (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms); };
}

// ---- Modal helpers ----
function openModal(id) {
  const el = document.getElementById(id);
  if (el) el.classList.add('open');
}
function closeModal(id) {
  const el = document.getElementById(id);
  if (el) el.classList.remove('open');
}

// Close modals on overlay click
document.addEventListener('click', e => {
  if (e.target.classList.contains('modal-overlay')) {
    e.target.classList.remove('open');
  }
});

// Close context menu on any click
document.addEventListener('click', closeMenu);

// Keyboard shortcuts
document.addEventListener('keydown', e => {
  if (e.key === 'Escape') {
    document.querySelectorAll('.modal-overlay.open').forEach(m => m.classList.remove('open'));
    closeMenu();
  }
});

// ---- Rich text compose helpers ----
function insertLink() {
  const url = prompt('Enter URL:');
  if (!url) return;
  const text = window.getSelection().toString() || url;
  document.getElementById('compose-editor').focus();
  document.execCommand('createLink', false, url);
}

// ── Filter dropdown (stubs — real logic in app.js, but onclick needs global scope) ──
function goMailToggleFilter(e) {
  e.stopPropagation();
  const menu = document.getElementById('filter-dropdown-menu');
  if (!menu) return;
  const isOpen = menu.classList.contains('open');
  menu.classList.toggle('open', !isOpen);
  if (!isOpen) {
    document.addEventListener('click', function closeFilter() {
      menu.classList.remove('open');
      document.removeEventListener('click', closeFilter);
    });
  }
}

function goMailSetFilter(mode) {
  var menu = document.getElementById('filter-dropdown-menu');
  if (menu) menu.style.display = 'none';
  if (typeof setFilter === 'function') setFilter(mode);
}

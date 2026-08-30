// GoWebMail service worker — background Web Push delivery only.
// No fetch/cache handling: this app is always online-driven, so an offline shell would
// just serve stale mail; we deliberately don't add one.

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));

self.addEventListener('push', (event) => {
  let data = {};
  try { data = event.data ? event.data.json() : {}; } catch (e) { /* non-JSON push, ignore */ }

  const title = data.title || 'GoWebMail';
  const body = data.body || 'New mail';
  const tag = data.tag || 'gowebmail-new';

  event.waitUntil((async () => {
    // Skip if a GoWebMail window is already open and focused — the in-page POLLER toast
    // already covers that case, so this avoids a duplicate notification.
    const clientList = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    if (clientList.some(c => c.focused)) return;

    return self.registration.showNotification(title, {
      body,
      tag,
      icon: '/static/icons/icon-192.png',
      badge: '/static/icons/icon-192.png',
      data: { url: '/' },
    });
  })());
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const url = (event.notification.data && event.notification.data.url) || '/';
  event.waitUntil((async () => {
    const clientList = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    for (const c of clientList) {
      if ('focus' in c) return c.focus();
    }
    if (self.clients.openWindow) return self.clients.openWindow(url);
  })());
});

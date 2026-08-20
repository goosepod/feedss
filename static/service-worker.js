const CACHE_NAME = 'feedss-shell-v13';
const SHELL_ASSETS = [
  '/static/offline.html',
	'/static/styles.css?v=20260820-13',
	'/static/app.js?v=20260820-13',
  '/static/favicon.svg',
  '/static/icon-192.png',
  '/static/icon-512.png',
  '/static/manifest.webmanifest',
];

self.addEventListener('install', event => {
  event.waitUntil(caches.open(CACHE_NAME).then(cache => cache.addAll(SHELL_ASSETS)));
  self.skipWaiting();
});

self.addEventListener('activate', event => {
  event.waitUntil(
    caches.keys().then(names => Promise.all(
      names.filter(name => name !== CACHE_NAME).map(name => caches.delete(name)),
    )),
  );
  self.clients.claim();
});

self.addEventListener('fetch', event => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== 'GET' || url.origin !== self.location.origin || url.pathname.startsWith('/api/')) return;

  if (request.mode === 'navigate') {
    event.respondWith(fetch(request).catch(() => caches.match('/static/offline.html')));
    return;
  }

  if (url.pathname.startsWith('/static/')) {
    event.respondWith(caches.match(request).then(cached => cached || fetch(request)));
  }
});

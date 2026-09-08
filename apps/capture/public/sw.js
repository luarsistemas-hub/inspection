const CACHE = "inspection-capture-shell-v1";
const ASSETS = ["/manifest.webmanifest", "/icon.svg"];
self.addEventListener("install", (event) => event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(ASSETS))));
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));
self.addEventListener("fetch", (event) => {
  const request = event.request; const url = new URL(request.url);
  // Never cache pages, GraphQL, credentials, evidence, signed URLs, or any dynamic response.
  if (request.method !== "GET" || url.origin !== self.location.origin || !url.pathname.startsWith("/_next/static/") && !ASSETS.includes(url.pathname)) return;
  event.respondWith(caches.match(request).then((cached) => cached || fetch(request).then((response) => { if (!response.ok) return response; const copy = response.clone(); void caches.open(CACHE).then((cache) => cache.put(request, copy)); return response; })));
});

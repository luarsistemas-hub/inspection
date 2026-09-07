const STATIC_CACHE = "inspection-static-v1";
// Never pre-cache HTML routes: the dashboard is a protected surface and the
// capture route can contain invitation-scoped state. Only public shell assets
// are eligible for offline reuse.
const STATIC_ASSETS = ["/manifest.webmanifest", "/icon.svg"];
function isAllowedStaticAsset(url) {
  return url.origin === self.location.origin &&
    (url.pathname === "/manifest.webmanifest" || url.pathname === "/icon.svg" || url.pathname.startsWith("/_next/static/"));
}
self.addEventListener("install", (event) => event.waitUntil(caches.open(STATIC_CACHE).then((cache) => cache.addAll(STATIC_ASSETS))));
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));
self.addEventListener("fetch", (event) => {
  const request = event.request;
  // Dynamic routes, GraphQL, media and signed URLs are never cacheable.
  if (request.method !== "GET" || !["script", "style", "image", "font"].includes(request.destination)) return;
  const url = new URL(request.url);
  if (!isAllowedStaticAsset(url)) return;
  event.respondWith(caches.match(request).then((cached) => cached || fetch(request).then((response) => {
    const copy = response.clone(); void caches.open(STATIC_CACHE).then((cache) => cache.put(request, copy)); return response;
  })));
});

// Service worker. Served from /sw.js by the Go server, which injects the
// asset version and precache list below. A new asset version means a new
// cache; old caches are removed on activate.
//
// STATIC FILES ONLY. Directory pages, photos and the API are never cached
// here: people's details must not stay on a shared tablet after sign-out.
// Only /static/ files (and the one generic /offline page, which holds
// nothing about anybody) are ever stored.
const VERSION = "__VERSION__"
const STATIC_CACHE = `md-static-${VERSION}`
const PRECACHE = __PRECACHE__

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(STATIC_CACHE).then((cache) => cache.addAll(PRECACHE)).then(() => self.skipWaiting())
  )
})

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(
        // Also drops "md-pages", should a page cache ever have existed.
        keys.filter((key) => key !== STATIC_CACHE && key.startsWith("md-")).map((key) => caches.delete(key))
      ))
      .then(() => self.clients.claim())
  )
})

self.addEventListener("fetch", (event) => {
  const request = event.request
  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return
  if (request.method !== "GET") return

  // Only static files are answered from the cache. Everything else (pages,
  // /photos/, /api/, sign-in) goes straight to the network and is left to
  // the browser.
  if (url.pathname.startsWith("/static/")) {
    event.respondWith(cacheFirst(request))
  } else if (request.mode === "navigate") {
    event.respondWith(pageOrOffline(request))
  }
})

async function cacheFirst(request) {
  const cache = await caches.open(STATIC_CACHE)
  const cached = await cache.match(request)
  if (cached) return cached
  const response = await fetch(request)
  if (response.ok) cache.put(request, response.clone())
  return response
}

// Pages come from the network and are never stored. With no connection,
// the generic offline page (precached with the static files) is shown.
async function pageOrOffline(request) {
  try {
    return await fetch(request)
  } catch (error) {
    return (
      (await caches.match("/offline")) ||
      new Response("You are offline.", { status: 503, headers: { "Content-Type": "text/plain" } })
    )
  }
}

/*
 * FundZim service worker (FRONTEND.md §3; ADR-033 nonce CSP). Deliberately minimal and safe:
 *  - Caches ONLY content-hashed build assets under /_next/static/ (cache-first; they never change) and the
 *    static offline page (/offline.html + /offline.css).
 *  - NEVER caches or intercepts: API responses (/api/*), documents and pages (campaign, auth, dashboard,
 *    admin, settings), media, or anything cross-origin. Pages always come from the network, with their own
 *    per-request CSP nonce, so a cached page can never serve stale campaign state or another user's data.
 *  - When the browser reports it is offline, a navigation gets the static offline page (no scripts).
 *  - Never queues anything for later replay.
 */
var VERSION = "v1";
var STATIC_CACHE = "fundzim-static-" + VERSION;
var OFFLINE_CACHE = "fundzim-offline-" + VERSION;
var OFFLINE_URLS = ["/offline.html", "/offline.css"];
var MAX_STATIC_ENTRIES = 150;
var OFFLINE_CSP = "default-src 'none'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

/** Only same-origin GETs of hashed build assets are ever cached. */
function isCacheableStatic(url, origin, method) {
  if (method !== "GET") return false;
  var u = new URL(url);
  return u.origin === origin && u.pathname.indexOf("/_next/static/") === 0 && u.search === "";
}

function trim(cache) {
  return cache.keys().then(function (keys) {
    var excess = keys.length - MAX_STATIC_ENTRIES;
    return Promise.all(keys.slice(0, Math.max(0, excess)).map(function (k) { return cache.delete(k); }));
  });
}

function offlineResponse() {
  return caches.open(OFFLINE_CACHE).then(function (cache) {
    return cache.match("/offline.html").then(function (res) {
      if (!res) return Response.error();
      return res.text().then(function (body) {
        return new Response(body, { status: 503, headers: { "Content-Type": "text/html; charset=utf-8", "Content-Security-Policy": OFFLINE_CSP, "Cache-Control": "no-store" } });
      });
    });
  });
}

if (typeof self !== "undefined" && typeof self.addEventListener === "function" && self.registration) {
  self.addEventListener("install", function (event) {
    event.waitUntil(caches.open(OFFLINE_CACHE).then(function (cache) { return cache.addAll(OFFLINE_URLS); }).then(function () { return self.skipWaiting(); }));
  });

  self.addEventListener("activate", function (event) {
    event.waitUntil(
      caches.keys().then(function (names) {
        return Promise.all(names.filter(function (n) { return n.indexOf("fundzim-") === 0 && n !== STATIC_CACHE && n !== OFFLINE_CACHE; }).map(function (n) { return caches.delete(n); }));
      }).then(function () { return self.clients.claim(); })
    );
  });

  self.addEventListener("fetch", function (event) {
    var req = event.request;
    if (req.mode === "navigate" && req.method === "GET") {
      // Online: not intercepted at all (the network answers with a fresh, nonced page).
      if (self.navigator && self.navigator.onLine === false) event.respondWith(offlineResponse());
      return;
    }
    if (!isCacheableStatic(req.url, self.location.origin, req.method)) return;
    event.respondWith(
      caches.open(STATIC_CACHE).then(function (cache) {
        return cache.match(req).then(function (hit) {
          if (hit) return hit;
          return fetch(req).then(function (res) {
            if (res.ok && res.type === "basic") cache.put(req, res.clone()).then(function () { return trim(cache); });
            return res;
          });
        });
      })
    );
  });
}

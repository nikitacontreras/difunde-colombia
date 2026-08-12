"use strict";
/* Service worker ultra-liviano.
   Precachea SOLO recursos críticos de la sonda (/). NO el mapa.
   Los probes (/p, /probe/*) se dejan pasar sin cache para no falsear mediciones. */
var CORE = ["/", "/app.css", "/app.js", "/manifest.webmanifest"];
var CACHE = "cdfd-core-v1";

self.addEventListener("install", function (e) {
  e.waitUntil(
    caches.open(CACHE).then(function (c) { return c.addAll(CORE); }).then(function () {
      return self.skipWaiting();
    })
  );
});

self.addEventListener("activate", function (e) {
  e.waitUntil(
    caches.keys().then(function (keys) {
      return Promise.all(keys.filter(function (k) { return k !== CACHE; }).map(function (k) { return caches.delete(k); }));
    }).then(function () { return self.clients.claim(); })
  );
});

self.addEventListener("fetch", function (e) {
  if (e.request.method !== "GET") return;
  var u = new URL(e.request.url);
  if (u.origin !== self.location.origin) return;
  if (u.pathname === "/p" || u.pathname.indexOf("/probe/") === 0) return;

  if (e.request.mode === "navigate") {
    e.respondWith(
      fetch(e.request).then(function (r) {
        var cp = r.clone();
        caches.open(CACHE).then(function (c) { c.put("/", cp); });
        return r;
      }).catch(function () { return caches.match("/"); })
    );
    return;
  }

  e.respondWith(
    caches.match(e.request).then(function (hit) {
      if (hit) return hit;
      return fetch(e.request).then(function (r) {
        if (r.ok && (u.pathname === "/app.js" || u.pathname === "/app.css")) {
          var cp = r.clone();
          caches.open(CACHE).then(function (c) { c.put(e.request, cp); });
        }
        return r;
      });
    })
  );
});

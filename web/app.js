"use strict";
/* Colombia Difunde - sonda de conectividad.
   Wire format compacto (POST /o):
   x lat, y lon, a accuracy(m), r rtt mediana(ms), j jitter(ms),
   n muestras, ok exitosos, f fallidos, q success ratio,
   e effectiveType, br browser rtt(ms), bd downlink(Mbps), sd saveData,
   c señal llamadas (yes/no/unknown), op operador (corroboración),
   k1 duracion /probe/1k(ms), k4 duracion /probe/4k(ms),
   t epoch(s) observado, u=1 solicitar id de seguimiento. */

var $ = function (id) { return document.getElementById(id); };
var DB = null;

var P_TIMEOUT = 5000;      // ms por probe /p
var PROBE_TIMEOUT = 8000;  // ms por probe /probe
var K1_THRESHOLD = 2000;   // si 1k tarda más, STOP
var K4_THRESHOLD = 3000;   // si 4k tarda más, STOP
var MIN_RATIO = 0.4;       // ratio mínimo para continuar con 1k/4k

var obsId = null;
var lastPayload = null;

function setStatus(msg, cls) {
  var el = $("status");
  el.textContent = msg;
  el.className = "status" + (cls ? " " + cls : "");
}

function getPosition() {
  return new Promise(function (res) {
    if (!("geolocation" in navigator)) { res({ ok: false, code: "unsupported" }); return; }
    var opts = { enableHighAccuracy: true, timeout: 12000, maximumAge: 300000 };
    navigator.geolocation.getCurrentPosition(
      function (p) {
        res({ ok: true, lat: p.coords.latitude, lon: p.coords.longitude, acc: p.coords.accuracy });
      },
      function (e) {
        var code = e.code === e.PERMISSION_DENIED ? "denied" : "unavailable";
        res({ ok: false, code: code, msg: e.message });
      },
      opts
    );
  });
}

function probeOnce(path, timeout) {
  return new Promise(function (res) {
    var t0 = performance.now();
    var ac = new AbortController();
    var timer = setTimeout(function () { ac.abort(); }, timeout || P_TIMEOUT);
    fetch(path, { cache: "no-store", signal: ac.signal })
      .then(function (r) {
        clearTimeout(timer);
        res(performance.now() - t0);
      })
      .catch(function () { clearTimeout(timer); res(-1); });
  });
}

async function runProbes() {
  var samples = [], ok = 0, fail = 0, i;
  for (i = 0; i < 4; i++) {
    var d = await probeOnce("/p", P_TIMEOUT);
    if (d >= 0) { samples.push(d); ok++; } else { fail++; }
  }
  var k1 = -1, k4 = -1;
  var ratio = ok / Math.max(1, ok + fail);
  if (ratio >= MIN_RATIO) {
    k1 = await probeOnce("/probe/1k", PROBE_TIMEOUT);
    if (k1 >= 0 && k1 < K1_THRESHOLD) {
      k4 = await probeOnce("/probe/4k", PROBE_TIMEOUT);
    }
  }
  return { samples: samples, ok: ok, fail: fail, k1: k1, k4: k4 };
}

function median(a) {
  if (!a.length) return 0;
  var b = a.slice().sort(function (x, y) { return x - y; });
  var m = Math.floor(b.length / 2);
  return b.length % 2 ? b[m] : (b[m - 1] + b[m]) / 2;
}

function jitter(a) {
  if (a.length < 2) return 0;
  var s = 0;
  for (var i = 1; i < a.length; i++) s += Math.abs(a[i] - a[i - 1]);
  return s / (a.length - 1);
}

function connection() {
  var c = navigator.connection || navigator.mozConnection || navigator.webkitConnection;
  if (!c) return null;
  return {
    e: c.effectiveType || "",
    br: typeof c.rtt === "number" && c.rtt >= 0 ? c.rtt : -1,
    bd: typeof c.downlink === "number" && c.downlink >= 0 ? c.downlink : -1,
    sd: c.saveData ? 1 : 0
  };
}

function buildPayload(gps, net, probes) {
  var p = {
    x: gps.lat, y: gps.lon, a: Math.round(gps.acc || 0),
    r: Math.round(median(probes.samples)),
    j: Math.round(jitter(probes.samples)),
    n: probes.samples.length,
    ok: probes.ok,
    f: probes.fail,
    q: probes.ok / Math.max(1, probes.ok + probes.fail),
    k1: Math.round(probes.k1),
    k4: Math.round(probes.k4),
    t: Math.floor(Date.now() / 1000),
    u: 1
  };
  if (net) {
    if (net.e) p.e = net.e;
    if (net.br >= 0) p.br = net.br;
    if (net.bd >= 0) p.bd = net.bd;
    p.sd = net.sd;
  }
  return p;
}

function send(payload) {
  return fetch("/o", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(payload),
    signal: AbortSignal.timeout(15000)
  }).then(function (r) {
    var id = r.headers.get("X-Obs-ID");
    if (r.status === 201 || r.status === 200) {
      return { ok: true, id: id ? parseInt(id, 10) : null, op: r.headers.get("X-Obs-Op") };
    }
    return { ok: false, http: r.status };
  }).catch(function () { return { ok: false, offline: true }; });
}

function openDB() {
  if (DB) return Promise.resolve(DB);
  return new Promise(function (res, rej) {
    var req = indexedDB.open("cdfd", 1);
    req.onupgradeneeded = function () { req.result.createObjectStore("pending", { keyPath: "id" }); };
    req.onsuccess = function () { DB = req.result; res(DB); };
    req.onerror = function () { rej(req.error); };
  });
}

function queueOffline(payload) {
  return openDB().then(function (db) {
    var id = ("crypto" in window && window.crypto.randomUUID) ? crypto.randomUUID() : String(Date.now());
    return new Promise(function (res, rej) {
      var tx = db.transaction("pending", "readwrite");
      tx.objectStore("pending").put({ id: id, p: payload, ts: Date.now() });
      tx.oncomplete = res; tx.onerror = function () { rej(tx.error); };
    });
  });
}

function updateQueued(payload) {
  return openDB().then(function (db) {
    return new Promise(function (res, rej) {
      var q = db.transaction("pending").objectStore("pending").getAll();
      q.onsuccess = function () {
        var items = q.result;
        if (!items.length) { res(); return; }
        var tx = db.transaction("pending", "readwrite");
        var st = tx.objectStore("pending");
        for (var i = 0; i < items.length; i++) {
          if (items[i].p.t === payload.t) st.put({ id: items[i].id, p: payload, ts: items[i].ts });
        }
        tx.oncomplete = res; tx.onerror = function () { rej(tx.error); };
      };
      q.onerror = function () { rej(q.error); };
    });
  });
}

async function syncPending() {
  var db = await openDB();
  var items = await new Promise(function (res, rej) {
    var q = db.transaction("pending").objectStore("pending").getAll();
    q.onsuccess = function () { res(q.result || []); };
    q.onerror = function () { rej(q.error); };
  });
  if (!items.length) return;
  var body = items.map(function (o) { return o.p; });
  try {
    var r = await fetch("/sync", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(30000)
    });
    if (r.ok || r.status === 204) {
      var tx = db.transaction("pending", "readwrite");
      var st = tx.objectStore("pending");
      items.forEach(function (o) { st.delete(o.id); });
    }
  } catch (e) { /* se reintentará en el próximo "online" */ }
}

function sendUpdate(payload) {
  if (obsId) {
    fetch("/o/update", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload),
      signal: AbortSignal.timeout(8000)
    }).catch(function () {});
  } else {
    updateQueued(lastPayload).catch(function () {});
  }
}

async function start() {
  $("share").disabled = true;
  setStatus("Obteniendo ubicación…");
  var gps = await getPosition();
  if (!gps.ok) {
    if (gps.code === "denied") setStatus("Ubicación rechazada. Sin ubicación no podemos registrar la observación.", "err");
    else if (gps.code === "unsupported") setStatus("Este navegador no soporta geolocalización.", "err");
    else setStatus("Ubicación no disponible. Reintenta o revisa el GPS (puede tardar en lugares cerrados).", "err");
    $("share").disabled = false;
    return;
  }
  if (gps.acc > 5000) setStatus("Precisión GPS baja (" + Math.round(gps.acc) + " m). Se intenta de todos modos.", "err");

  setStatus("Midiendo tu conexión… (puede tardar unos segundos)");
  var net = connection();
  var probes = await runProbes();
  lastPayload = buildPayload(gps, net, probes);

  setStatus("Enviando observación…");
  var res = await send(lastPayload);
  if (res.ok) {
    obsId = res.id;
    setStatus("¡Gracias! Observación registrada." + summary(gps, probes), "ok");
    $("followup").classList.remove("hidden");
    $("report-panel").classList.remove("hidden");
    if (res.op && res.op !== "desconocido") {
      $("opdet").querySelector("span").textContent = res.op.charAt(0).toUpperCase() + res.op.slice(1);
      $("opdet").classList.remove("hidden");
    } else {
      setStatus("¡Gracias! Observación registrada." + summary(gps, probes) + "\nOperador no identificado por ASN.", "ok");
    }
  } else if (res.offline) {
    await queueOffline(lastPayload).catch(function () {});
    setStatus("Sin conexión. La observación se guardó y se enviará cuando vuelva Internet.", "err");
    $("followup").classList.remove("hidden");
    $("report-panel").classList.remove("hidden");
    return;
  } else {
    setStatus("Error del servidor (" + res.http + "). Se guardará para reintentar.", "err");
    await queueOffline(lastPayload).catch(function () {});
  }
}

function summary(gps, probes) {
  var txt = "\nPrecisión: " + Math.round(gps.acc) + " m";
  txt += "\nProbes HTTP: " + probes.ok + "/" + (probes.ok + probes.fail) + " ok";
  if (probes.samples.length) txt += " · RTT medio: " + Math.round(median(probes.samples)) + " ms";
  return txt;
}

function bindFollowup() {
  document.querySelectorAll("[data-call]").forEach(function (b) {
    b.addEventListener("click", function () {
      lastPayload.c = b.getAttribute("data-call");
      sendUpdate({ id: obsId, c: lastPayload.c });
      var group = b.closest(".row");
      group.querySelectorAll("button").forEach(function (x) { x.disabled = true; });
      b.classList.add("sel");
      setStatus("¡Gracias! Señal para llamadas registrada (" + b.textContent.trim() + ").", "ok");
    });
  });
}

// Generación local de Proof of Work (PoW) con SHA-256 nativo
async function solvePoW(kind, name) {
  var prefix = "00";
  var nonce = 0;
  while (true) {
    var str = kind + name + nonce;
    var msgBuffer = new TextEncoder().encode(str);
    var hashBuffer = await crypto.subtle.digest("SHA-256", msgBuffer);
    var hashArray = Array.from(new Uint8Array(hashBuffer));
    var hashHex = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
    if (hashHex.startsWith(prefix)) {
      return String(nonce);
    }
    nonce++;
  }
}

function bindReportForm() {
  $("report-form").addEventListener("submit", async function (e) {
    e.preventDefault();
    if (!lastPayload || !lastPayload.x) {
      setStatus("Primero debes compartir tu ubicación.", "err");
      return;
    }
    $("rep-submit").disabled = true;
    var kind = $("rep-kind").value;
    var name = $("rep-name").value;
    var addr = $("rep-address").value;
    var phone = $("rep-phone").value;
    
    setStatus("Generando prueba de trabajo anti-spam local...");
    var nonce = await solvePoW(kind, name);

    setStatus("Enviando reporte de ayuda...");
    var payload = {
      kind: kind, name: name, address: addr, phone: phone,
      lat: lastPayload.x, lon: lastPayload.y, details: {}, nonce: nonce
    };

    fetch("/report", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload),
      signal: AbortSignal.timeout(15000)
    }).then(function (r) {
      if (r.ok) {
        setStatus("¡Reporte enviado exitosamente! Queda en verificación por el administrador.", "ok");
        $("report-form").reset();
      } else {
        setStatus("Error al enviar reporte (" + r.status + ").", "err");
      }
      $("rep-submit").disabled = false;
    }).catch(function () {
      setStatus("Error de conexión al enviar reporte.", "err");
      $("rep-submit").disabled = false;
    });
  });
}

$("share").addEventListener("click", start);
bindFollowup();
bindReportForm();
window.addEventListener("online", function () { syncPending(); });
if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch(function () {});
}

"use strict";

function clientFingerprintSeed() {
  var nav = typeof navigator !== "undefined" ? navigator : {};
  var conn = nav.connection || nav.mozConnection || nav.webkitConnection || null;
  var screenPart = "";
  if (typeof screen !== "undefined") {
    screenPart = [screen.width || 0, screen.height || 0, screen.colorDepth || 0].join("x");
  }
  var tz = "";
  try {
    tz = Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  } catch (e) {}
  return [
    nav.userAgent || "",
    nav.language || "",
    nav.platform || "",
    tz,
    screenPart,
    String(nav.hardwareConcurrency || 0),
    String(nav.deviceMemory || 0),
    conn ? (conn.effectiveType || "") : "",
    conn ? String(conn.saveData ? 1 : 0) : "0"
  ].join("|");
}

function fnv1a32(str) {
  var h = 0x811c9dc5;
  for (var i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i);
    h = (h + ((h << 1) >>> 0) + ((h << 4) >>> 0) + ((h << 7) >>> 0) + ((h << 8) >>> 0) + ((h << 24) >>> 0)) >>> 0;
  }
  return ("00000000" + h.toString(16)).slice(-8);
}

var CLIENT_FINGERPRINT = fnv1a32(clientFingerprintSeed());

function apiFetch(input, init) {
  init = init || {};
  var headers = new Headers(init.headers || {});
  headers.set("X-Client-Fingerprint", CLIENT_FINGERPRINT);
  init.headers = headers;
  return fetch(input, init);
}

(function () {
  var map = L.map("map", { zoomControl: false }).setView([4.57, -74.07], 6);
  L.control.zoom({ position: "topright" }).addTo(map);
  map.createPane("coveragePane");
  map.getPane("coveragePane").style.zIndex = 260;
  map.getPane("coveragePane").style.pointerEvents = "none";

  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: "&copy; OpenStreetMap"
  }).addTo(map);

  function toast(msg) {
    var t = document.getElementById("toast");
    t.textContent = msg;
    t.style.display = "block";
    setTimeout(function () { t.style.display = "none"; }, 4000);
  }

  function coverageProviderById(id) {
    if (!coverageCatalog || !coverageCatalog.providers) return null;
    id = String(id || "").toLowerCase();
    for (var i = 0; i < coverageCatalog.providers.length; i++) {
      var provider = coverageCatalog.providers[i];
      if ((provider.id || "").toLowerCase() === id) return provider;
    }
    return null;
  }

  function coverageTechnologyById(provider, id) {
    if (!provider || !provider.technologies) return null;
    id = String(id || "").toLowerCase();
    for (var i = 0; i < provider.technologies.length; i++) {
      var tech = provider.technologies[i];
      if ((tech.id || "").toLowerCase() === id) return tech;
    }
    return null;
  }

  function coverageStatsLine(provider) {
    if (!provider || !provider.stats) return "";
    var bits = [];
    if (provider.stats.departments) bits.push(provider.stats.departments + " departamentos");
    if (provider.stats.municipalities) bits.push(provider.stats.municipalities + " municipios");
    if (provider.stats.cities) bits.push(provider.stats.cities + " ciudades");
    if (provider.stats.localities) bits.push(provider.stats.localities + " localidades");
    if (provider.stats.admins) bits.push(provider.stats.admins + " admins");
    if (provider.stats.layers) bits.push(provider.stats.layers + " capas");
    if (provider.stats.tile_matrix_sets) bits.push(provider.stats.tile_matrix_sets + " tile matrix sets");
    if (provider.stats.overlays) bits.push(provider.stats.overlays + " overlays");
    return bits.join(" · ");
  }

  function clearCoverageLayer() {
    if (coverageAbortController) {
      coverageAbortController.abort();
      coverageAbortController = null;
    }
    if (coverageTileLayer) {
      map.removeLayer(coverageTileLayer);
      coverageTileLayer = null;
    }
    coverageOverlayGroup.clearLayers();
    coverageLoadedBounds = null;
    coverageActiveKey = "";
  }

  function coverageSyncUI(provider, tech) {
    if (!coverageSummaryEl || !coverageInfoEl || !coverageWarningEl) return;
    if (!provider || !tech) {
      coverageSummaryEl.textContent = "Selecciona un proveedor para ver la cobertura oficial.";
      coverageInfoEl.textContent = "";
      coverageWarningEl.style.display = "none";
      return;
    }
    var summary = provider.name + " · " + tech.name;
    if (tech.render_type) {
      summary += " · " + tech.render_type.toUpperCase();
    }
    coverageSummaryEl.textContent = summary;

    var lines = [];
    if (provider.updated_at) lines.push("Actualizado: " + provider.updated_at);
    var statsLine = coverageStatsLine(provider);
    if (statsLine) lines.push(statsLine);
    if (tech.overlay_count) lines.push(tech.overlay_count + " overlays georreferenciados");
    if (tech.tile_url_templates && tech.tile_url_templates.length) {
      lines.push(tech.tile_url_templates.length + " template(s) de tiles");
    }
    if (tech.source_urls && tech.source_urls.length) {
      lines.push(tech.source_urls.length + " URL(s) fuente");
    }
    if (tech.notes && tech.notes.length) {
      lines.push(tech.notes[0]);
    } else if (provider.notes && provider.notes.length) {
      lines.push(provider.notes[0]);
    }
    coverageInfoEl.innerHTML = lines.map(function (line) {
      return "<div style='margin-bottom:4px;'>" + line + "</div>";
    }).join("");

    var sourceHref = provider.public_page || provider.map_page || "#";
    coverageSourceLink.href = sourceHref;
    coverageSourceLink.style.pointerEvents = sourceHref === "#" ? "none" : "auto";
    coverageSourceLink.style.opacity = sourceHref === "#" ? "0.5" : "1";

    var mapHref = provider.map_page || provider.public_page || "#";
    coverageMapLink.href = mapHref;
    coverageMapLink.style.pointerEvents = mapHref === "#" ? "none" : "auto";
    coverageMapLink.style.opacity = mapHref === "#" ? "0.5" : "1";

    if (tech.renderable) {
      coverageWarningEl.style.display = "none";
      coverageWarningEl.textContent = "";
      coverageEnabledToggle.disabled = false;
    } else {
      coverageWarningEl.style.display = "block";
      coverageWarningEl.textContent = "Esta fuente publica por WMTS y por ahora queda catalogada, no superpuesta automáticamente.";
      coverageEnabledToggle.checked = false;
      coverageEnabledToggle.disabled = true;
      coverageEnabled = false;
      clearCoverageLayer();
    }
  }

  function rebuildCoverageProviderOptions(selectedProviderId, selectedTechId) {
    if (!coverageProviderSelect || !coverageTechnologySelect) return;
    coverageProviderSelect.innerHTML = "";
    if (!coverageCatalog || !coverageCatalog.providers || !coverageCatalog.providers.length) {
      var emptyOpt = document.createElement("option");
      emptyOpt.value = "";
      emptyOpt.textContent = "Sin catálogo disponible";
      coverageProviderSelect.appendChild(emptyOpt);
      coverageTechnologySelect.innerHTML = "";
      var emptyTech = document.createElement("option");
      emptyTech.value = "";
      emptyTech.textContent = "Sin capas disponibles";
      coverageTechnologySelect.appendChild(emptyTech);
      coverageSyncUI(null, null);
      return;
    }

    for (var i = 0; i < coverageCatalog.providers.length; i++) {
      var provider = coverageCatalog.providers[i];
      var opt = document.createElement("option");
      opt.value = provider.id;
      opt.textContent = provider.name;
      coverageProviderSelect.appendChild(opt);
    }

    var provider = coverageProviderById(selectedProviderId) || coverageCatalog.providers[0];
    coverageProviderSelect.value = provider.id;
    rebuildCoverageTechnologyOptions(provider, selectedTechId);
    coverageSyncUI(provider, coverageTechnologyById(provider, coverageTechnologySelect.value));
  }

  function rebuildCoverageTechnologyOptions(provider, selectedTechId) {
    if (!coverageTechnologySelect) return;
    coverageTechnologySelect.innerHTML = "";
    var technologies = (provider && provider.technologies) || [];
    if (!technologies.length) {
      var empty = document.createElement("option");
      empty.value = "";
      empty.textContent = "Sin tecnologías";
      coverageTechnologySelect.appendChild(empty);
      return;
    }

    for (var i = 0; i < technologies.length; i++) {
      var tech = technologies[i];
      var opt = document.createElement("option");
      opt.value = tech.id;
      opt.textContent = tech.name + (tech.renderable ? "" : " · WMTS");
      coverageTechnologySelect.appendChild(opt);
    }

    var tech = coverageTechnologyById(provider, selectedTechId) || technologies[0];
    coverageTechnologySelect.value = tech.id;
  }

  function coverageCurrentTileTemplate(provider, tech) {
    if (!provider || !tech || !tech.tile_url_templates || !tech.tile_url_templates.length) return "";
    if ((provider.id || "").toLowerCase() === "tigo" && tech.tile_url_templates.length > 1) {
      return map.getZoom() >= 12 ? tech.tile_url_templates[1] : tech.tile_url_templates[0];
    }
    return tech.tile_url_templates[0];
  }

  function applyCoverageTileLayer(provider, tech) {
    var template = coverageCurrentTileTemplate(provider, tech);
    if (!template) {
      clearCoverageLayer();
      coverageSyncUI(provider, tech);
      return;
    }
    var key = provider.id + ":" + tech.id + ":" + template;
    if (coverageActiveKey === key && coverageTileLayer) {
      coverageSyncUI(provider, tech);
      return;
    }
    clearCoverageLayer();
    coverageTileLayer = L.tileLayer(template, {
      pane: "coveragePane",
      opacity: 0.52,
      maxZoom: 19,
      minZoom: 3,
      updateWhenIdle: true,
      keepBuffer: 2,
      crossOrigin: true,
      attribution: provider.name + " cobertura oficial"
    }).addTo(map);
    coverageActiveKey = key;
    coverageSyncUI(provider, tech);
  }

  function applyCoverageMovistar(provider, tech, force) {
    var zoom = map.getZoom();
    if (zoom < 7) {
      clearCoverageLayer();
      coverageSyncUI(provider, tech);
      coverageInfoEl.innerHTML += "<div style='margin-top:6px;color:#fbbf24;'>Haz zoom para cargar overlays de Movistar.</div>";
      return;
    }

    var bounds = map.getBounds();
    var boundsKey = [
      bounds.getWest().toFixed(4),
      bounds.getSouth().toFixed(4),
      bounds.getEast().toFixed(4),
      bounds.getNorth().toFixed(4)
    ].join(",");

    if (!force && coverageLoadedBounds && coverageLoadedBounds.contains(bounds) && coverageActiveKey === provider.id + ":" + tech.id + ":" + boundsKey) {
      coverageSyncUI(provider, tech);
      return;
    }

    if (coverageAbortController) {
      coverageAbortController.abort();
    }
    var requestController = new AbortController();
    coverageAbortController = requestController;

    var url = "/coverage/overlays?provider=" + encodeURIComponent(provider.id) +
      "&technology=" + encodeURIComponent(tech.id) +
      "&bbox=" + encodeURIComponent([
        bounds.getWest(), bounds.getSouth(), bounds.getEast(), bounds.getNorth()
      ].join(","));

    coverageSummaryEl.textContent = provider.name + " · " + tech.name + " · cargando overlays...";
    coverageInfoEl.innerHTML = "";

    apiFetch(url, { cache: "no-store", signal: requestController.signal })
      .then(function (r) {
        if (!r.ok) throw new Error("bad_response");
        return r.json();
      })
      .then(function (payload) {
        if (coverageAbortController !== requestController) return;
        if (!payload || !payload.overlays) return;
        clearCoverageLayer();
        coverageOverlayGroup.clearLayers();
        payload.overlays.forEach(function (ov) {
          if (!ov || !ov.bbox || !ov.url) return;
          var overlay = L.imageOverlay(ov.url, [
            [ov.bbox.south, ov.bbox.west],
            [ov.bbox.north, ov.bbox.east]
          ], {
            pane: "coveragePane",
            opacity: 0.54,
            interactive: false,
            crossOrigin: true
          });
          coverageOverlayGroup.addLayer(overlay);
        });
        coverageLoadedBounds = bounds;
        coverageActiveKey = provider.id + ":" + tech.id + ":" + boundsKey;
        coverageSyncUI(provider, tech);
        if (payload.count === 0) {
          coverageWarningEl.style.display = "block";
          coverageWarningEl.textContent = "No hay overlays de Movistar dentro del viewport actual.";
        }
      })
      .catch(function () {
        if (coverageAbortController !== requestController || requestController.signal.aborted) return;
        coverageWarningEl.style.display = "block";
        coverageWarningEl.textContent = "No se pudieron cargar los overlays de Movistar para este viewport.";
      });
  }

  function syncCoverageLayer(force) {
    if (!coverageCatalog) return;
    var provider = coverageProviderById(coverageProviderId) || coverageCatalog.providers[0];
    if (!provider) {
      clearCoverageLayer();
      coverageSyncUI(null, null);
      return;
    }
    var tech = coverageTechnologyById(provider, coverageTechnologyId) || provider.technologies[0];
    if (!tech) {
      clearCoverageLayer();
      coverageSyncUI(provider, null);
      return;
    }

    coverageProviderId = provider.id;
    coverageTechnologyId = tech.id;

    if (coverageProviderSelect && coverageProviderSelect.value !== provider.id) {
      coverageProviderSelect.value = provider.id;
    }
    if (coverageTechnologySelect && coverageTechnologySelect.value !== tech.id) {
      coverageTechnologySelect.value = tech.id;
    }

    if (!coverageEnabled || !tech.renderable) {
      clearCoverageLayer();
      coverageSyncUI(provider, tech);
      return;
    }

    if ((provider.id || "").toLowerCase() === "movistar" || tech.render_type === "image-overlays") {
      applyCoverageMovistar(provider, tech, !!force);
      return;
    }
    if (tech.render_type === "xyz-tiles") {
      applyCoverageTileLayer(provider, tech);
      return;
    }
    clearCoverageLayer();
    coverageSyncUI(provider, tech);
  }

  function loadCoverageCatalog() {
    if (!coverageSummaryEl) return;
    coverageSummaryEl.textContent = "Cargando catálogo de cobertura...";
    apiFetch("/coverage/providers", { cache: "no-store" })
      .then(function (r) {
        if (!r.ok) throw new Error("bad_response");
        return r.json();
      })
      .then(function (catalog) {
        coverageCatalog = catalog || { providers: [] };
        var providerId = coverageCatalog.providers && coverageCatalog.providers[0] ? coverageCatalog.providers[0].id : "";
        rebuildCoverageProviderOptions(providerId, "");
        coverageEnabledToggle.disabled = false;
        syncCoverageLayer(true);
      })
      .catch(function () {
        coverageSummaryEl.textContent = "No se pudo cargar el catálogo oficial de cobertura.";
        if (coverageInfoEl) coverageInfoEl.textContent = "";
        if (coverageWarningEl) {
          coverageWarningEl.style.display = "block";
          coverageWarningEl.textContent = "La API de cobertura no respondió.";
        }
        if (coverageEnabledToggle) coverageEnabledToggle.disabled = true;
      });
  }

  var COLOR = { "OPERATIVO": "#2ecc71", "DEGRADADO": "#f1c40f", "AFECTACION_PROBABLE": "#e74c3c" };
  var RADIUS = { "OPERATIVO": 9, "DEGRADADO": 12, "AFECTACION_PROBABLE": 16 };

  var userLatLng = null;
  var allResources = [];
  var currentTab = "all";
  var searchQuery = "";
  var selectedResource = null;
  var isPickingLocation = false;
  var pickedLatLng = null;

  // Últimos bounds cargados por loader (para no re-pedir si el viewport
  // sigue dentro de lo ya cargado) y debounce de refresco al mover el mapa.
  var loadedBounds = {};
  var refreshTimer = null;

  var coverageCatalog = null;
  var coverageEnabled = false;
  var coverageProviderId = "";
  var coverageTechnologyId = "";
  var coverageTileLayer = null;
  var coverageOverlayGroup = L.layerGroup().addTo(map);
  var coverageAbortController = null;
  var coverageLoadedBounds = null;
  var coverageActiveKey = "";
  var coverageRefreshTimer = null;

  var coverageProviderSelect = document.getElementById("coverage-provider");
  var coverageTechnologySelect = document.getElementById("coverage-tech");
  var coverageEnabledToggle = document.getElementById("coverage-enabled");
  var coverageSummaryEl = document.getElementById("coverage-summary");
  var coverageInfoEl = document.getElementById("coverage-info");
  var coverageWarningEl = document.getElementById("coverage-warning");
  var coverageSourceLink = document.getElementById("coverage-source-link");
  var coverageMapLink = document.getElementById("coverage-map-link");

  // Connectivity Layers
  var cellLayer = L.geoJSON(null, {
    pointToLayer: function (f, latlng) {
      var s = f.properties.s;
      return L.circleMarker(latlng, {
        radius: RADIUS[s] || 6,
        color: "#fff", weight: 1,
        fillColor: COLOR[s] || "#888", fillOpacity: 0.55
      });
    },
    onEachFeature: function (f, layer) {
      var p = f.properties;
      var text = "n=" + p.n + " · rtt=" + Math.round(p.r) + " ms · ok=" + Math.round(p.q * 100) + "% · " + (p.o || "desconocido");
      layer.bindTooltip(text, { direction: "top" });
      
      var statusLabels = { "OPERATIVO": "Operativo (Normal)", "DEGRADADO": "Conectividad Degradada", "AFECTACION_PROBABLE": "Afectación Probable de Infraestructura" };
      var html = "<b>Celda de Conectividad</b><br>" +
                 "Estado: <b>" + (statusLabels[p.s] || p.s) + "</b><br>" +
                 "Muestras: " + p.n + "<br>" +
                 "RTT HTTP Mediano: " + Math.round(p.r) + " ms<br>" +
                 "Éxito de peticiones: " + Math.round(p.q * 100) + "%<br>" +
                 "Operador principal: " + (p.o ? p.o.toUpperCase() : "Desconocido") + "<br>" +
                 "Antenas oficiales en celda: " + (p.p || 0);
      layer.bindPopup(html);
    }
  }).addTo(map);

  var sitesClusterGroup = L.markerClusterGroup({
    disableClusteringAtZoom: 16,
    maxClusterRadius: 50
  }).addTo(map);

  var sitesLayer = L.geoJSON(null, {
    pointToLayer: function (f, latlng) {
      var icon = L.divIcon({
        className: 'pulsating-dot-container',
        html: '<div class="pulsating-dot"></div>',
        iconSize: [16, 16],
        iconAnchor: [8, 8]
      });
      return L.marker(latlng, { icon: icon });
    },
    onEachFeature: function (f, layer) {
      var p = f.properties;
      var op = p.o ? (p.o.charAt(0).toUpperCase() + p.o.slice(1)) : "Desconocido";
      var html = "<b>Torre de Telefonía Móvil</b><br>Operador: " + op;
      if (p.nd) html += "<br>Barrio: " + p.nd;
      if (p.ad) html += "<br>Dirección: " + p.ad;
      layer.bindPopup(html);
    }
  });

  // Custom Markers for Resources
  var resourceMarkersGroup = L.layerGroup().addTo(map);

  // Pulsing sismo marker at the SGC epicenter of the 2026-08-10 earthquake (San José del Palmar)
  var sismoLatLng = [4.99, -76.29];
  var sismoIcon = L.divIcon({
    className: 'pulsating-sismo-container',
    html: '<div class="pulsating-sismo-dot"></div>',
    iconSize: [24, 24],
    iconAnchor: [12, 12]
  });

  var sismoMarker = L.marker(sismoLatLng, { icon: sismoIcon }).addTo(map);

  sismoMarker.bindPopup(function () {
    var container = document.createElement("div");
    container.style.width = "260px";
    container.style.maxHeight = "300px";
    container.style.overflowY = "auto";
    container.innerHTML = "<b>Cargando historial de sismos...</b>";

    fetch("/api/sismos?lat=4.99&lon=-76.29&rad=90&days=15")
      .then(function (r) { return r.json(); })
      .then(function (data) {
        var sismosList = (data.events || []).slice(0).sort(function (a, b) {
          var m = (b.mag || 0) - (a.mag || 0);
          if (m !== 0) return m;
          return (b.local_time || "").localeCompare(a.local_time || "");
        }).slice(0, 15);

        if (sismosList.length === 0) {
          container.innerHTML = "<b>Epicentro: San José del Palmar, Chocó</b><br>No se encontraron réplicas recientes registradas.";
          return;
        }

        var html = "<b>Epicentro: San José del Palmar (Terremoto y Réplicas)</b><br><span style='font-size:11px;color:var(--text-secondary);'>" + (data.source || "Datos oficiales del Servicio Geológico Colombiano") + "</span><hr style='border:0;border-top:1px solid var(--border-color);margin:8px 0;'>";
        html += "<div style='display:flex;flex-direction:column;gap:6px;'>";

        sismosList.forEach(function (e) {
          var mag = e.mag != null ? e.mag.toFixed(1) : "?";
          var magType = e.mag_type || "ML";
          var prof = e.depth != null ? Math.round(e.depth) : "?";
          var dist = e.dist_km != null ? " a " + e.dist_km + " km del epicentro" : "";
          var dateStr = e.local_time || "Fecha desconocida";

          var color = "#16a34a"; // green
          if (parseFloat(mag) >= 5.0) color = "#dc2626"; // red
          else if (parseFloat(mag) >= 4.0) color = "#d97706"; // orange

          html += "<div style='background:rgba(255,255,255,0.05);padding:6px 8px;border-radius:6px;border-left:4px solid " + color + ";'>";
          html += "<strong>Magnitud: " + mag + "</strong> <small>(" + magType + ")</small><br>";
          html += "<span style='font-size:11px;color:var(--text-secondary);'>Profundidad: " + prof + " km" + dist + "</span><br>";
          html += "<span style='font-size:11px;color:var(--text-secondary);'>" + dateStr + (e.place ? " · " + e.place : "") + "</span>";
          html += "</div>";
        });
        html += "</div>";
        container.innerHTML = html;
      })
      .catch(function () {
        container.innerHTML = "<b>San José del Palmar, Chocó</b><br>Error al consultar el catálogo de sismos del SGC.";
      });

    return container;
  });

  // Helper: Haversine distance
  function getDistance(lat1, lon1, lat2, lon2) {
    var R = 6371e3; // meters
    var phi1 = lat1 * Math.PI/180;
    var phi2 = lat2 * Math.PI/180;
    var deltaPhi = (lat2-lat1) * Math.PI/180;
    var deltaLambda = (lon2-lon1) * Math.PI/180;
    var a = Math.sin(deltaPhi/2) * Math.sin(deltaPhi/2) +
            Math.cos(phi1) * Math.cos(phi2) *
            Math.sin(deltaLambda/2) * Math.sin(deltaLambda/2);
    var c = 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1-a));
    return R * c; // in meters
  }

  // Format distance
  function formatDistance(m) {
    if (m === null || m === undefined) return "";
    if (m < 1000) return "A " + Math.round(m) + " M";
    return "A " + (m / 1000).toFixed(1) + " KM";
  }

  // Time formatter
  function formatRelativeTime(ts) {
    var diffMs = Date.now() - new Date(ts).getTime();
    var diffMins = Math.round(diffMs / 60000);
    if (diffMins < 1) return "ahora";
    if (diffMins < 60) return "hace " + diffMins + " min";
    var diffHrs = Math.round(diffMins / 60);
    if (diffHrs < 24) return "hace " + diffHrs + " h";
    return new Date(ts).toLocaleDateString();
  }

  function getResourceUrgency(r) {
    if (r.Details && r.Details.urgency) {
      return r.Details.urgency;
    }
    return "normal";
  }

  function getResourceScope(r) {
    return String(r.LocationScope || "point").toLowerCase();
  }

  function getResourceIntent(r) {
    return String((r.Details && r.Details.intent) || "request").toLowerCase();
  }

  function resourceLocationLabel(r) {
    if (getResourceScope(r) === "city") {
      return [r.Municipality, r.Department].filter(Boolean).join(", ") || "Cobertura por ciudad";
    }
    return r.Address || "Ubicación puntual";
  }

  function escapeHTML(value) {
    return String(value == null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#039;");
  }

  // Render Sidebar List
  function renderReportList() {
    var filtered = allResources.filter(function (r) {
      if (window.IS_ADMIN) {
        if (currentTab === "pending") {
          if (r.Status !== "pending" && r.Status !== "rejected") return false;
        } else {
          if (r.Status !== "approved") return false;
        }
      } else {
        if (r.Status !== "approved") return false;
      }

      // Tab filters
      if (currentTab === "acopios" && r.Kind !== "centro_acopio") return false;
      if (currentTab === "necesidades" && (r.Kind === "centro_acopio" || r.Kind === "refugio" || r.Kind === "olla_comunitaria" || r.Kind === "logistica")) return false;
      if (currentTab === "ollas" && r.Kind !== "olla_comunitaria") return false;
      if (currentTab === "refugios" && r.Kind !== "refugio") return false;
      if (currentTab === "logistica" && r.Kind !== "logistica") return false;

      // Search keyword
      if (searchQuery) {
        var query = searchQuery.toLowerCase();
        var nameMatch = (r.Name || "").toLowerCase().indexOf(query) !== -1;
        var addrMatch = (r.Address || "").toLowerCase().indexOf(query) !== -1;
        var cityMatch = (r.Municipality || "").toLowerCase().indexOf(query) !== -1 ||
          (r.Department || "").toLowerCase().indexOf(query) !== -1;
        var needsMatch = false;
        if (r.Details && Array.isArray(r.Details.needs)) {
          needsMatch = r.Details.needs.some(function (n) {
            return n.toLowerCase().indexOf(query) !== -1;
          });
        }
        if (!nameMatch && !addrMatch && !cityMatch && !needsMatch) return false;
      }
      return true;
    });

    // Sort by relative distance if user location is known, else by date
    filtered.sort(function (a, b) {
      if (userLatLng) {
        var aCity = getResourceScope(a) === "city";
        var bCity = getResourceScope(b) === "city";
        if (aCity || bCity) {
          if (aCity !== bCity) return aCity ? -1 : 1;
          return new Date(b.ReportedAt).getTime() - new Date(a.ReportedAt).getTime();
        }
        var distA = getDistance(userLatLng.lat, userLatLng.lng, a.Lat, a.Lon);
        var distB = getDistance(userLatLng.lat, userLatLng.lng, b.Lat, b.Lon);
        return distA - distB;
      }
      return new Date(b.ReportedAt).getTime() - new Date(a.ReportedAt).getTime();
    });

    // Update Counts
    var approvedList = allResources.filter(function (r) { return r.Status === "approved"; });
    document.getElementById("count-all").textContent = approvedList.length;
    document.getElementById("count-acopios").textContent = approvedList.filter(r => r.Kind === "centro_acopio").length;
    document.getElementById("count-necesidades").textContent = approvedList.filter(r => r.Kind !== "centro_acopio" && r.Kind !== "refugio" && r.Kind !== "olla_comunitaria" && r.Kind !== "logistica").length;
    document.getElementById("count-ollas").textContent = approvedList.filter(r => r.Kind === "olla_comunitaria").length;
    document.getElementById("count-refugios").textContent = approvedList.filter(r => r.Kind === "refugio").length;
    document.getElementById("count-logistica").textContent = approvedList.filter(r => r.Kind === "logistica").length;
    if (window.IS_ADMIN && document.getElementById("count-pending")) {
      document.getElementById("count-pending").textContent = allResources.filter(r => r.Status !== "approved").length;
    }
    
    var headerCountEl = document.getElementById("header-active-count");
    if (headerCountEl) {
      headerCountEl.textContent = approvedList.length;
    }

    var container = document.getElementById("report-items-container");
    container.innerHTML = "";

    if (filtered.length === 0) {
      container.innerHTML = "<div style='padding:20px; opacity:0.6; text-align:center; font-size:13px;'>No se encontraron reportes.</div>";
      return;
    }

    filtered.forEach(function (r) {
      var cityScope = getResourceScope(r) === "city";
      var distStr = userLatLng && !cityScope ? formatDistance(getDistance(userLatLng.lat, userLatLng.lng, r.Lat, r.Lon)) : "";
      var timeStr = formatRelativeTime(r.ReportedAt);
      
      var urgency = getResourceUrgency(r);
      var needed = (r.Details && r.Details.needed) || 0;
      var helping = (r.Details && r.Details.helping) || 0;
      
      var badgesHtml = "";
      if (urgency === "urgente") {
        badgesHtml += '<span class="badge badge-urgent">URGENTE</span>';
      }
      if (needed > 0) {
        badgesHtml += '<span class="badge badge-warning">FALTAN ' + needed + ' - VE</span>';
      } else if (helping > 0) {
        badgesHtml += '<span class="badge badge-info">NO ACUDIR - YA HAY ' + helping + '</span>';
      }
      if (distStr) {
        badgesHtml += '<span class="badge badge-distance">' + distStr + '</span>';
      }
      if (cityScope) {
        badgesHtml += '<span class="badge badge-distance">COBERTURA: ' + escapeHTML(r.Municipality || "CIUDAD") + '</span>';
      }
      if (getResourceIntent(r) === "offer") {
        badgesHtml += '<span class="badge badge-info">OFRECIMIENTO</span>';
      }

      var needsListStr = "";
      if (r.Details && Array.isArray(r.Details.needs) && r.Details.needs.length > 0) {
        needsListStr = (getResourceIntent(r) === "offer" ? "Disponible: " : "Se necesita: ") + r.Details.needs.join(", ");
      } else if (r.Details && r.Details.description) {
        needsListStr = r.Details.description;
      } else {
        needsListStr = getResourceIntent(r) === "offer" ? "Oferta disponible para coordinación." : "Sin requerimientos específicos reportados.";
      }

      var div = document.createElement("div");
      div.className = "report-item";
      div.innerHTML = `
        <div class="report-item-header">
          <h4 class="report-item-title">${escapeHTML(r.Name || "Reporte sin título")}</h4>
          <span class="report-item-time">${escapeHTML(timeStr)}</span>
        </div>
        <div class="badge-row">${badgesHtml}</div>
        <p class="report-item-desc">${escapeHTML(needsListStr)}</p>
      `;

      div.addEventListener("click", function () {
        selectResource(r);
      });
      container.appendChild(div);
    });
  }

  // Draw markers on map
  function drawResourceMarkers() {
    resourceMarkersGroup.clearLayers();
    allResources.forEach(function (r) {
      if (getResourceScope(r) === "city") return;
      var urgency = getResourceUrgency(r);
      var needed = (r.Details && r.Details.needed) || 0;
      var helping = (r.Details && r.Details.helping) || 0;
      
      var color = "#ff9800"; // default orange
      if (r.Kind === "logistica") color = "#009688";
      else if (urgency === "urgente") color = "#c62828";
      else if (needed > 0) color = "#ef6c00";
      else if (helping > 0) color = "#1565c0";

      var textVal = needed > 0 ? needed : (helping > 0 ? helping : "");

      var icon = L.divIcon({
        className: 'custom-resource-marker',
        html: `<div class="marker-pin-custom" style="background: ${color}">${textVal}</div>`,
        iconSize: [26, 26],
        iconAnchor: [13, 13]
      });

      var marker = L.marker([r.Lat, r.Lon], { icon: icon });
      marker.on("click", function () {
        selectResource(r);
      });
      resourceMarkersGroup.addLayer(marker);
    });
  }

  // Select a Report and view details
  function selectResource(r) {
    selectedResource = r;
    
    document.getElementById("detail-card-title").textContent = r.Name;
    var descriptionParts = [resourceLocationLabel(r)];
    if (r.Phone) descriptionParts.push("Tel: " + r.Phone);
    if (r.Details && r.Details.description) descriptionParts.push(r.Details.description);
    document.getElementById("detail-card-desc").textContent = descriptionParts.join(" · ");
    
    // Set counters
    document.getElementById("val-helping").textContent = (r.Details && r.Details.helping) || 0;
    document.getElementById("val-needed").textContent = (r.Details && r.Details.needed) || 0;

    // Badges
    var urgency = getResourceUrgency(r);
    var badgHtml = `<span class="badge ${urgency === 'urgente' ? 'badge-urgent' : 'badge-info'}">${escapeHTML(urgency.toUpperCase())}</span>`;
    badgHtml += `<span class="badge badge-warning">${escapeHTML(r.Kind.replace("_", " ").toUpperCase())}</span>`;
    if (getResourceScope(r) === "city") badgHtml += `<span class="badge badge-distance">TODA ${escapeHTML(r.Municipality || "LA CIUDAD").toUpperCase()}</span>`;
    if (getResourceIntent(r) === "offer") badgHtml += `<span class="badge badge-info">OFRECIMIENTO</span>`;
    document.getElementById("detail-card-badges").innerHTML = badgHtml;
    document.getElementById("detail-counters-title").textContent = getResourceScope(r) === "city" ? "Personas coordinando" : "Gente en el punto";

    // Votes / confirmations
    var confirms = (r.Details && r.Details.confirms) || 0;
    var dismisses = (r.Details && r.Details.dismisses) || 0;
    document.getElementById("detail-card-votes").textContent = confirms + " confirman · " + dismisses + " desmienten";

    // Google Maps link
    var mapsQuery = getResourceScope(r) === "city" ? resourceLocationLabel(r) : r.Lat + "," + r.Lon;
    document.getElementById("btn-gmaps").href = "https://www.google.com/maps/search/?api=1&query=" + encodeURIComponent(mapsQuery);

    // Render Needs
    renderNeedsTags(r);

    // Check if user has already voted for this resource
    var hasVoted = localStorage.getItem("voted_" + r.ID);
    var btnConfirm = document.getElementById("btn-vote-confirm");
    var btnDisprove = document.getElementById("btn-vote-disprove");
    if (hasVoted) {
      btnConfirm.disabled = true;
      btnConfirm.style.opacity = 0.5;
      btnDisprove.disabled = true;
      btnDisprove.style.opacity = 0.5;
    } else {
      btnConfirm.disabled = false;
      btnConfirm.style.opacity = 1;
      btnDisprove.disabled = false;
      btnDisprove.style.opacity = 1;
    }

    // Admin Moderation Panel
    var adminPanel = document.getElementById("admin-moderation-panel");
    if (adminPanel) {
      if (window.IS_ADMIN) {
        adminPanel.style.display = "flex";
        var statusEl = document.getElementById("admin-resource-status");
        statusEl.textContent = r.Status || "PENDIENTE";
        if (r.Status === "approved") {
          statusEl.style.color = "#10b981";
        } else if (r.Status === "rejected") {
          statusEl.style.color = "#ef4444";
        } else {
          statusEl.style.color = "#fbbf24";
        }
      } else {
        adminPanel.style.display = "none";
      }
    }

    // Open detail panel
    document.getElementById("detail-panel").classList.add("open");

    // Center map on selected resource
    if (getResourceScope(r) !== "city") {
      map.setView([r.Lat, r.Lon], 16);
    }
  }

  function renderNeedsTags(r) {
    var container = document.getElementById("detail-card-needs");
    container.innerHTML = "";
    var needs = (r.Details && r.Details.needs) || [];
    if (needs.length === 0) {
      container.innerHTML = "<div style='font-size:12px; opacity:0.6;'>No se han especificado necesidades.</div>";
      return;
    }
    needs.forEach(function (n, index) {
      var span = document.createElement("span");
      span.className = "need-tag";
      span.appendChild(document.createTextNode(String(n) + " "));
      var remove = document.createElement("span");
      remove.className = "need-remove";
      remove.setAttribute("data-index", String(index));
      remove.textContent = "×";
      remove.addEventListener("click", function (e) {
        e.stopPropagation();
        removeNeed(index);
      });
      span.appendChild(remove);
      container.appendChild(span);
    });
  }

  // Update details in DB
  function pushDetailsUpdate(updatedDetails, successMsg) {
    if (!selectedResource) return;
    apiFetch("/resources/update-details", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        id: selectedResource.ID,
        details: updatedDetails
      })
    }).then(function (res) {
      if (res.ok) {
        if (successMsg) toast(successMsg);
        // Update local copy
        if (!selectedResource.Details) selectedResource.Details = {};
        for (var k in updatedDetails) {
          selectedResource.Details[k] = updatedDetails[k];
        }
        renderReportList();
        drawResourceMarkers();
        selectResource(selectedResource);
      } else if (res.status === 409) {
        res.text().then(function(txt) {
          toast(txt || "Ya has registrado una validación para este punto.");
        });
      } else {
        toast("Error al actualizar información.");
      }
    }).catch(function () {
      toast("Error de red.");
    });
  }

  // Counter button clicks
  document.querySelectorAll(".btn-counter").forEach(function (btn) {
    btn.addEventListener("click", function () {
      if (!selectedResource) return;
      var counterType = btn.getAttribute("data-counter"); // "helping" or "needed"
      var action = btn.getAttribute("data-action"); // "inc" or "dec"
      
      var currentVal = (selectedResource.Details && selectedResource.Details[counterType]) || 0;
      var newVal = action === "inc" ? currentVal + 1 : Math.max(0, currentVal - 1);
      
      var update = {};
      update[counterType] = newVal;
      pushDetailsUpdate(update, "Contador actualizado.");
    });
  });

  // Adding needs
  document.getElementById("btn-add-need").addEventListener("click", function () {
    if (!selectedResource) return;
    var val = document.getElementById("new-need-input").value.trim();
    if (!val) return;
    var needs = (selectedResource.Details && selectedResource.Details.needs) || [];
    needs.push(val);
    pushDetailsUpdate({ needs: needs }, "Necesidad añadida.");
    document.getElementById("new-need-input").value = "";
  });

  function removeNeed(index) {
    if (!selectedResource) return;
    var needs = (selectedResource.Details && selectedResource.Details.needs) || [];
    needs.splice(index, 1);
    pushDetailsUpdate({ needs: needs }, "Necesidad eliminada.");
  }

  // Voting
  document.getElementById("btn-vote-confirm").addEventListener("click", function () {
    if (!selectedResource || localStorage.getItem("voted_" + selectedResource.ID)) return;
    var confirms = (selectedResource.Details && selectedResource.Details.confirms) || 0;
    localStorage.setItem("voted_" + selectedResource.ID, "confirm");
    
    var btnConfirm = document.getElementById("btn-vote-confirm");
    var btnDisprove = document.getElementById("btn-vote-disprove");
    btnConfirm.disabled = true;
    btnConfirm.style.opacity = 0.5;
    btnDisprove.disabled = true;
    btnDisprove.style.opacity = 0.5;
    
    pushDetailsUpdate({ confirms: confirms + 1 }, "¡Confirmación registrada!");
  });

  document.getElementById("btn-vote-disprove").addEventListener("click", function () {
    if (!selectedResource || localStorage.getItem("voted_" + selectedResource.ID)) return;
    var dismisses = (selectedResource.Details && selectedResource.Details.dismisses) || 0;
    localStorage.setItem("voted_" + selectedResource.ID, "disprove");
    
    var btnConfirm = document.getElementById("btn-vote-confirm");
    var btnDisprove = document.getElementById("btn-vote-disprove");
    btnConfirm.disabled = true;
    btnConfirm.style.opacity = 0.5;
    btnDisprove.disabled = true;
    btnDisprove.style.opacity = 0.5;
    
    pushDetailsUpdate({ dismisses: dismisses + 1 }, "¡Reporte de información incorrecta registrado!");
  });

  // Back button in details
  document.getElementById("btn-detail-back").addEventListener("click", function () {
    document.getElementById("detail-panel").classList.remove("open");
    selectedResource = null;
  });

  // Share button
  document.getElementById("btn-share").addEventListener("click", function () {
    if (!selectedResource) return;
    if (navigator.share) {
      navigator.share({
        title: selectedResource.Name,
        text: resourceLocationLabel(selectedResource) + (getResourceIntent(selectedResource) === "offer" ? " - Ofrecimiento de ayuda disponible." : " - Solicitud de ayuda vigente."),
        url: window.location.href
      });
    } else {
      navigator.clipboard.writeText(window.location.href);
      toast("Enlace copiado al portapapeles.");
    }
  });

  // Search & Tab interactions
  document.getElementById("search-box").addEventListener("input", function (e) {
    searchQuery = e.target.value;
    renderReportList();
  });

  document.querySelectorAll(".tab-btn").forEach(function (tab) {
    tab.addEventListener("click", function () {
      document.querySelectorAll(".tab-btn").forEach(t => t.classList.remove("active"));
      tab.classList.add("active");
      currentTab = tab.getAttribute("data-tab");
      renderReportList();
    });
  });

  // Drawer options
  document.getElementById("btn-toggle-connectivity").addEventListener("click", function () {
    document.getElementById("connectivity-drawer").classList.add("open");
  });

  document.getElementById("btn-close-connectivity").addEventListener("click", function () {
    document.getElementById("connectivity-drawer").classList.remove("open");
  });

  function reportScope() {
    var checked = document.querySelector('input[name="modal-scope"]:checked');
    return checked ? checked.value : "city";
  }

  function updateReportScope() {
    var city = reportScope() === "city";
    document.getElementById("modal-city-fields").hidden = !city;
    document.getElementById("modal-point-fields").hidden = city;
    document.getElementById("modal-municipality").required = city;
    document.getElementById("picked-location-status").textContent = pickedLatLng
      ? "Punto seleccionado: " + pickedLatLng.lat.toFixed(5) + ", " + pickedLatLng.lng.toFixed(5)
      : "Aún no has seleccionado un punto.";
  }

  document.querySelectorAll('input[name="modal-scope"]').forEach(function (radio) {
    radio.addEventListener("change", updateReportScope);
  });

  // El formulario decide primero entre cobertura por ciudad o punto exacto.
  document.getElementById("btn-trigger-add").addEventListener("click", function () {
    document.getElementById("add-point-modal").classList.add("open");
    updateReportScope();
  });

  document.getElementById("btn-pick-report-location").addEventListener("click", function () {
    isPickingLocation = true;
    document.getElementById("add-point-modal").classList.remove("open");
    document.getElementById("map-pick-indicator").style.display = "block";
    toast("Haz click en el mapa para ubicar el punto");
  });

  map.on("click", function (e) {
    if (isPickingLocation) {
      isPickingLocation = false;
      document.getElementById("map-pick-indicator").style.display = "none";
      pickedLatLng = e.latlng;
      updateReportScope();
      document.getElementById("add-point-modal").classList.add("open");
    } else {
      queryCoverageAt(e.latlng);
    }
  });

  // ---- Cobertura por datos (municipio + punto) ----
  var municipios = null;
  var synthesisAbort = null;

  function loadMunicipios() {
    if (municipios) return;
    fetch("/cobertura_municipios.geojson")
      .then(function (r) { return r.json(); })
      .then(function (data) { municipios = data; })
      .catch(function () {});
  }

  function pointInRing(pt, ring) {
    var inside = false;
    for (var i = 0, j = ring.length - 1; i < ring.length; j = i++) {
      var xi = ring[i][0], yi = ring[i][1];
      var xj = ring[j][0], yj = ring[j][1];
      var intersect = ((yi > pt[1]) !== (yj > pt[1])) &&
        (pt[0] < (xj - xi) * (pt[1] - yi) / (yj - yi) + xi);
      if (intersect) inside = !inside;
    }
    return inside;
  }

  function pointInGeometry(pt, geom) {
    if (!geom || !geom.coordinates) return false;
    if (geom.type === "Polygon") {
      if (!pointInRing(pt, geom.coordinates[0])) return false;
      for (var k = 1; k < geom.coordinates.length; k++) {
        if (pointInRing(pt, geom.coordinates[k])) return false;
      }
      return true;
    }
    if (geom.type === "MultiPolygon") {
      for (var p = 0; p < geom.coordinates.length; p++) {
        var poly = geom.coordinates[p];
        if (!pointInRing(pt, poly[0])) continue;
        var hole = false;
        for (var h = 1; h < poly.length; h++) {
          if (pointInRing(pt, poly[h])) { hole = true; break; }
        }
        if (!hole) return true;
      }
      return false;
    }
    return false;
  }

  function findMunicipality(latlng) {
    if (!municipios || !municipios.features) return null;
    var pt = [latlng.lng, latlng.lat];
    for (var i = 0; i < municipios.features.length; i++) {
      var f = municipios.features[i];
      if (pointInGeometry(pt, f.geometry)) return f.properties;
    }
    return null;
  }

  function opName(op) {
    var names = { claro: "Claro", movistar: "Movistar", tigo: "Tigo", wom: "WOM" };
    return names[String(op || "").toLowerCase()] || op || "";
  }

  function pct(v) {
    var n = Number(v);
    if (!isFinite(n)) return "n/d";
    return Math.round(n * 100) + "%";
  }

  function operatorBars(op, items) {
    var label = opName(op);
    if (!items || !items.length) {
      return "<div style='display:flex; align-items:center; gap:8px;'><span style='width:64px; font-size:11px; color:var(--text-secondary);'>" + label + "</span><span style='font-size:11px; color:#8a94a6;'>sin datos</span></div>";
    }
    var bars = items.map(function (r) {
      var v = Math.max(0, Math.min(1, Number(r.covered_ratio) || 0));
      var color = v > 0.6 ? "#2ecc71" : v > 0.3 ? "#f1c40f" : "#e74c3c";
      return "<div style='display:flex; align-items:center; gap:6px;'>" +
        "<span style='width:26px; font-size:10px; color:var(--text-secondary); text-align:right;'>" + r.technology + "</span>" +
        "<span style='flex:1; height:7px; background:rgba(255,255,255,0.12); border-radius:4px; overflow:hidden;'>" +
        "<span style='display:block; height:100%; width:" + Math.round(v * 100) + "%; background:" + color + "; border-radius:4px;'></span></span>" +
        "<span style='width:38px; font-size:10px; color:var(--text-primary); text-align:right;'>" + pct(r.covered_ratio) + "</span></div>";
    }).join("");
    return "<div style='display:flex; flex-direction:column; gap:3px;'><div style='font-size:11px; font-weight:700; color:var(--text-primary);'>" + label + "</div>" + bars + "</div>";
  }

  function renderSynthesis(props, rows, point) {
    var panel = document.getElementById("synthesis-panel");
    var nameEl = document.getElementById("synthesis-municipio");
    var listEl = document.getElementById("synthesis-list");
    var pointEl = document.getElementById("synthesis-point");
    if (!panel) return;
    nameEl.textContent = (props.municipio || "Sin municipio") +
      (props.departamento && props.departamento !== props.municipio ? " · " + props.departamento : "");
    var groups = {};
    (rows || []).forEach(function (r) {
      var key = String(r.operator || "").toLowerCase();
      (groups[key] = groups[key] || []).push(r);
    });
    var html = "";
    ["claro", "movistar", "tigo", "wom"].forEach(function (op) {
      html += operatorBars(op, groups[op]);
    });
    Object.keys(groups).forEach(function (op) {
      if (["claro", "movistar", "tigo", "wom"].indexOf(op) === -1) html += operatorBars(op, groups[op]);
    });
    listEl.innerHTML = html || "<div style='font-size:11px; color:var(--text-secondary);'>Sin datos de cobertura para este municipio.</div>";

    var plist = Array.isArray(point) ? point : [];
    if (plist.length) {
      pointEl.innerHTML = "En este punto: <strong style='color:var(--text-primary);'>" +
        plist.map(function (p) { return opName(p.operator) + " " + (p.technology || ""); }).join(" · ") + "</strong>";
      pointEl.style.display = "block";
    } else {
      pointEl.textContent = "Sin cobertura de operadores en este punto.";
      pointEl.style.display = "block";
    }
    panel.style.display = "block";
  }

  function queryCoverageAt(latlng) {
    if (synthesisAbort) synthesisAbort.abort();
    synthesisAbort = new AbortController();
    var signal = synthesisAbort.signal;
    fetch("/coverage/point?lat=" + latlng.lat.toFixed(5) + "&lon=" + latlng.lng.toFixed(5), { signal: signal })
      .then(function (r) { return r.json(); })
      .then(function (point) {
        var props = findMunicipality(latlng);
        if (props) {
          return fetch("/coverage/synthesis?dane=" + encodeURIComponent(props.dane), { signal: signal })
            .then(function (r2) { return r2.json(); })
            .then(function (rows) {
              renderSynthesis(props, rows, point);
              document.getElementById("connectivity-drawer").classList.add("open");
            });
        }
        renderSynthesis({ municipio: "Fuera de municipios", departamento: "" }, [], point);
        document.getElementById("connectivity-drawer").classList.add("open");
      })
      .catch(function () {});
  }

  document.getElementById("btn-close-synthesis").addEventListener("click", function () {
    document.getElementById("synthesis-panel").style.display = "none";
  });

  loadMunicipios();

  document.getElementById("btn-modal-close").addEventListener("click", function () {
    document.getElementById("add-point-modal").classList.remove("open");
    pickedLatLng = null;
    isPickingLocation = false;
    document.getElementById("map-pick-indicator").style.display = "none";
    updateReportScope();
  });

  // Proof of work helper (native anti-spam)
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

  // Handle new resource submit
  document.getElementById("add-point-form").addEventListener("submit", async function (e) {
    e.preventDefault();

    var scope = reportScope();
    if (scope === "point" && !pickedLatLng) {
      toast("Selecciona primero el punto exacto en el mapa.");
      return;
    }
    var kind = document.getElementById("modal-kind").value;
    var name = document.getElementById("modal-name").value;
    var intent = document.getElementById("modal-intent").value;
    var address = document.getElementById("modal-address").value;
    var phone = document.getElementById("modal-phone").value;
    var municipality = document.getElementById("modal-municipality").value;
    var department = document.getElementById("modal-department").value;
    var urgency = document.getElementById("modal-urgency").value;
    var description = document.getElementById("modal-description").value;
    var rawNeeds = document.getElementById("modal-needs").value;
    
    var needsArr = rawNeeds.split(",").map(s => s.trim()).filter(Boolean);

    toast("Generando prueba de trabajo anti-spam...");
    var nonce = await solvePoW(kind, name);

    var payload = {
      kind: kind,
      name: name,
      address: address,
      phone: phone,
      location_scope: scope,
      municipality: scope === "city" ? municipality : "",
      department: scope === "city" ? department : "",
      nonce: nonce,
      details: {
        intent: intent,
        urgency: urgency,
        description: description,
        needs: needsArr,
        helping: 0,
        needed: 0,
        confirms: 1,
        dismisses: 0
      }
    };
    if (scope === "point") {
      payload.lat = pickedLatLng.lat;
      payload.lon = pickedLatLng.lng;
    }

    apiFetch("/report", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload)
    }).then(function (res) {
      if (res.ok) {
        toast("Reporte enviado exitosamente");
        document.getElementById("add-point-modal").classList.remove("open");
        document.getElementById("add-point-form").reset();
        pickedLatLng = null;
        updateReportScope();
        refresh(true);
      } else {
        toast("Error al registrar el reporte.");
      }
    }).catch(function () {
      toast("Error de red.");
    });
  });

  updateReportScope();

  // Load connectivity operators rankings
  function updateRankings(cells) {
    var ops = {};
    cells.forEach(function (c) {
      var op = c.o || "desconocido";
      if (!ops[op]) {
        ops[op] = { name: op, n: 0, rtts: [], success: [], bestCell: null };
      }
      ops[op].n += c.n;
      ops[op].rtts.push(c.r);
      ops[op].success.push(c.q);
      
      if (!ops[op].bestCell || (c.q > ops[op].bestCell.q) || (c.q === ops[op].bestCell.q && c.r < ops[op].bestCell.r)) {
        ops[op].bestCell = c;
      }
    });

    var list = [];
    for (var k in ops) {
      var o = ops[k];
      var medRtt = o.rtts.reduce(function (a, b) { return a + b; }, 0) / o.rtts.length;
      var medSuccess = o.success.reduce(function (a, b) { return a + b; }, 0) / o.success.length;
      list.push({
        name: o.name.toUpperCase(),
        rtt: Math.round(medRtt),
        success: Math.round(medSuccess * 100),
        samples: o.n,
        bestCell: o.bestCell
      });
    }

    list.sort(function (a, b) {
      if (b.success !== a.success) return b.success - a.success;
      return a.rtt - b.rtt;
    });

    var container = document.getElementById("rankings-list");
    container.innerHTML = "";
    if (list.length === 0) {
      container.innerHTML = "<div style='opacity:0.7'>Sin observaciones en el área visible.</div>";
      return;
    }

    list.forEach(function (r, index) {
      var row = document.createElement("div");
      row.className = "operator-ranking-row";
      row.title = "Click para enfocar la mejor zona de este operador";
      row.innerHTML = "<div>" +
                        "<span class='name'>" + (index + 1) + ". " + r.name + "</span>" +
                        "<span class='samples'>(" + r.samples + " obs)</span>" +
                      "</div>" +
                      "<span class='stats'>" + r.success + "% / " + r.rtt + "ms</span>";
      
      row.addEventListener("click", function () {
        if (r.bestCell) {
          map.setView([r.bestCell.y, r.bestCell.x], 15);
          cellLayer.eachLayer(function (layer) {
            var coords = layer.getLatLng();
            if (Math.abs(coords.lat - r.bestCell.y) < 0.0001 && Math.abs(coords.lng - r.bestCell.x) < 0.0001) {
              layer.openPopup();
            }
          });
        }
      });
      container.appendChild(row);
    });
  }

  // Geolocation
  if ("geolocation" in navigator) {
    navigator.geolocation.getCurrentPosition(function (pos) {
      userLatLng = { lat: pos.coords.latitude, lng: pos.coords.longitude };
      map.setView([userLatLng.lat, userLatLng.lng], 14);
      L.marker([userLatLng.lat, userLatLng.lng]).addTo(map).bindPopup("Tu ubicación actual").openPopup();
      refresh(true);
    }, function () {
      toast("No se pudo obtener ubicación para centrar el mapa.");
    }, { enableHighAccuracy: true, timeout: 8000 });
  }

  function loadCells(force) {
    var b = map.getBounds();
    if (!force && loadedBounds.cells && loadedBounds.cells.contains(b)) return;
    var bbox = [b.getWest(), b.getSouth(), b.getEast(), b.getNorth()].join(",");
    var op = document.getElementById("op").value;
    var win = document.getElementById("win").value;
    var u = "/cells?bbox=" + encodeURIComponent(bbox) + "&window=" + win + "&operator=" + encodeURIComponent(op);
    apiFetch(u, { cache: "no-store" }).then(function (r) { if (!r.ok) throw 0; return r.json(); }).then(function (cells) {
      loadedBounds.cells = b;
      var fc = { type: "FeatureCollection", features: [] };
      cells.forEach(function (c) {
        if (!COLOR[c.s]) return;
        fc.features.push({ type: "Feature", geometry: { type: "Point", coordinates: [c.x, c.y] },
          properties: { s: c.s, n: c.n, r: c.r, q: c.q, o: c.o, p: c.p, t: c.t } });
      });
      cellLayer.clearLayers();
      cellLayer.addData(fc);
      updateRankings(cells);
    }).catch(function () {});
  }

  function loadSites(force) {
    if (!document.getElementById("sites").checked) {
      sitesClusterGroup.clearLayers();
      sitesLayer.clearLayers();
      delete loadedBounds.sites;
      return;
    }
    if (map.getZoom() < 12) {
      sitesClusterGroup.clearLayers();
      sitesLayer.clearLayers();
      delete loadedBounds.sites;
      return;
    }
    var b = map.getBounds();
    if (!force && loadedBounds.sites && loadedBounds.sites.contains(b)) return;
    var bbox = [b.getWest(), b.getSouth(), b.getEast(), b.getNorth()].join(",");
    apiFetch("/sites?bbox=" + encodeURIComponent(bbox), { cache: "no-store" }).then(function (r) { if (!r.ok) throw 0; return r.json(); }).then(function (sites) {
      loadedBounds.sites = b;
      sitesClusterGroup.clearLayers();
      var markers = [];
      sites.forEach(function (s) {
        var latlng = L.latLng(s.y, s.x);
        var icon = L.divIcon({
          className: 'pulsating-dot-container',
          html: '<div class="pulsating-dot"></div>',
          iconSize: [16, 16],
          iconAnchor: [8, 8]
        });
        var marker = L.marker(latlng, { icon: icon });
        var op = s.o ? (s.o.charAt(0).toUpperCase() + s.o.slice(1)) : "Desconocido";
        var html = "<b>Torre de Telefonía Móvil</b><br>Operador: " + op;
        if (s.nd) html += "<br>Barrio: " + s.nd;
        if (s.ad) html += "<br>Dirección: " + s.ad;
        marker.bindPopup(html);
        markers.push(marker);
      });
      sitesClusterGroup.addLayers(markers);
    }).catch(function () {});
  }

  function loadResources(force) {
    var b = map.getBounds();
    if (!force && loadedBounds.resources && loadedBounds.resources.contains(b)) return;
    var bbox = [b.getWest(), b.getSouth(), b.getEast(), b.getNorth()].join(",");
    var url = "/resources?bbox=" + encodeURIComponent(bbox);
    apiFetch(url, { cache: "no-store" }).then(function (r) { if (!r.ok) throw 0; return r.json(); }).then(function (resList) {
      loadedBounds.resources = b;
      allResources = resList.filter(function (r) { return r.Status === "approved"; });
      
      // Update UI elements
      renderReportList();
      drawResourceMarkers();
    }).catch(function () {});
  }

  // Connectivity Test Card Logic inside Map
  var testObsId = null;
  var testPayload = null;

  document.getElementById("btn-close-test-card").addEventListener("click", function() {
    document.getElementById("connectivity-test-card").style.display = "none";
  });

  async function runTestProbes() {
    var samples = [], ok = 0, fail = 0;
    for (var i = 0; i < 4; i++) {
      var d = await probeOnce("/p", 5000);
      if (d >= 0) { samples.push(d); ok++; } else { fail++; }
    }
    var k1 = -1, k4 = -1;
    var ratio = ok / Math.max(1, ok + fail);
    if (ratio >= 0.4) {
      k1 = await probeOnce("/probe/1k", 8000);
      if (k1 >= 0 && k1 < 2000) {
        k4 = await probeOnce("/probe/4k", 8000);
      }
    }
    return { samples: samples, ok: ok, fail: fail, k1: k1, k4: k4 };
  }

  function probeOnce(path, timeout) {
    return new Promise(function (res) {
      var t0 = performance.now();
      var ac = new AbortController();
      var timer = setTimeout(function () { ac.abort(); }, timeout);
      apiFetch(path, { cache: "no-store", signal: ac.signal })
        .then(function (r) {
          clearTimeout(timer);
          res(performance.now() - t0);
        })
        .catch(function () { clearTimeout(timer); res(-1); });
    });
  }

  function median(a) {
    if (!a.length) return 0;
    var b = a.slice().sort(function (x, y) { return x - y; });
    var m = Math.floor(b.length / 2);
    return b.length % 2 ? b[m] : (b[m - 1] + b[m]) / 2;
  }

  // Jitter
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

  function periodicRefreshDelayMs() {
    var c = connection();
    if (!c) return 60000;
    if (c.sd || /2g/.test(c.e)) return 300000;
    if (c.e === "3g") return 120000;
    return 60000;
  }

  function shouldForcePeriodicRefresh() {
    var c = connection();
    if (!c) return true;
    return !(c.sd || /2g/.test(c.e));
  }

  var periodicRefreshTimer = null;

  function schedulePeriodicRefresh() {
    clearTimeout(periodicRefreshTimer);
    periodicRefreshTimer = setTimeout(function () {
      refresh(shouldForcePeriodicRefresh());
      schedulePeriodicRefresh();
    }, periodicRefreshDelayMs());
  }

  document.getElementById("btn-run-test").addEventListener("click", async function() {
    var btn = document.getElementById("btn-run-test");
    var statusEl = document.getElementById("test-status");
    var followupEl = document.getElementById("test-followup");
    
    btn.disabled = true;
    btn.textContent = "MIDIENDO...";
    statusEl.style.display = "block";
    statusEl.textContent = "Obteniendo ubicación...";
    followupEl.style.display = "none";

    var gps = await new Promise(function(res) {
      if (!("geolocation" in navigator)) { res({ ok: false }); return; }
      navigator.geolocation.getCurrentPosition(
        function(p) { res({ ok: true, lat: p.coords.latitude, lon: p.coords.longitude, acc: p.coords.accuracy }); },
        function() { res({ ok: false }); },
        { enableHighAccuracy: true, timeout: 8000 }
      );
    });

    if (!gps.ok) {
      statusEl.textContent = "Error: se requiere ubicación GPS activa.";
      btn.disabled = false;
      btn.textContent = "MEDIR RED";
      return;
    }

    statusEl.textContent = "Midiendo latencia de red...";
    var net = connection();
    var probes = await runTestProbes();

    statusEl.textContent = "Enviando resultados...";
    testPayload = {
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
      if (net.e) testPayload.e = net.e;
      if (net.br >= 0) testPayload.br = net.br;
      if (net.bd >= 0) testPayload.bd = net.bd;
      testPayload.sd = net.sd;
    }

    apiFetch("/o", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(testPayload)
    }).then(function(r) {
      var id = r.headers.get("X-Obs-ID");
      if (r.ok || r.status === 201) {
        testObsId = id ? parseInt(id, 10) : null;
        var rttVal = Math.round(median(probes.samples));
        statusEl.textContent = "¡Medición completada! RTT medio: " + rttVal + " ms";
        followupEl.style.display = "flex";
        refresh(true);
      } else {
        statusEl.textContent = "Error al registrar medición.";
      }
      btn.disabled = false;
      btn.textContent = "MEDIR RED";
    }).catch(function() {
      statusEl.textContent = "Error de red al enviar medición.";
      btn.disabled = false;
      btn.textContent = "MEDIR RED";
    });
  });

  document.querySelectorAll(".btn-test-call").forEach(function(btn) {
    btn.addEventListener("click", function() {
      if (!testPayload) return;
      var val = btn.getAttribute("data-val");
      testPayload.c = val;
      
      if (testObsId) {
        apiFetch("/o/update", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ id: testObsId, c: val })
        }).catch(function(){});
      }
      
      document.querySelectorAll(".btn-test-call").forEach(b => b.classList.remove("sel"));
      btn.classList.add("sel");
      toast("¡Gracias! Reporte de señal registrado.");
    });
  });

  async function runAutomaticConnectivityTest() {
    var statusEl = document.getElementById("test-status");
    var followupEl = document.getElementById("test-followup");
    var btn = document.getElementById("btn-run-test");
    
    if (btn) {
      btn.disabled = true;
      btn.textContent = "MIDIENDO...";
    }
    statusEl.style.display = "block";
    statusEl.textContent = "Obteniendo ubicación...";
    followupEl.style.display = "none";

    var gps = await new Promise(function(res) {
      if (!("geolocation" in navigator)) { res({ ok: false }); return; }
      navigator.geolocation.getCurrentPosition(
        function(p) { res({ ok: true, lat: p.coords.latitude, lon: p.coords.longitude, acc: p.coords.accuracy }); },
        function() { res({ ok: false }); },
        { enableHighAccuracy: true, timeout: 8000 }
      );
    });

    if (!gps.ok) {
      statusEl.textContent = "Medición omitida: se requiere ubicación GPS.";
      if (btn) {
        btn.disabled = false;
        btn.textContent = "MEDIR RED";
      }
      return;
    }

    statusEl.textContent = "Midiendo latencia de red...";
    var net = connection();
    var probes = await runTestProbes();

    statusEl.textContent = "Enviando resultados...";
    testPayload = {
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
      if (net.e) testPayload.e = net.e;
      if (net.br >= 0) testPayload.br = net.br;
      if (net.bd >= 0) testPayload.bd = net.bd;
      testPayload.sd = net.sd;
    }

    apiFetch("/o", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(testPayload)
    }).then(function(r) {
      var id = r.headers.get("X-Obs-ID");
      if (r.ok || r.status === 201) {
        testObsId = id ? parseInt(id, 10) : null;
        var rttVal = Math.round(median(probes.samples));
        statusEl.textContent = "¡Medición completada! RTT medio: " + rttVal + " ms";
        followupEl.style.display = "flex";
        refresh(true);
      } else {
        statusEl.textContent = "Error al registrar medición.";
      }
      if (btn) {
        btn.disabled = false;
        btn.textContent = "MEDIR RED";
      }
    }).catch(function() {
      statusEl.textContent = "Error de red al enviar medición.";
      if (btn) {
        btn.disabled = false;
        btn.textContent = "MEDIR RED";
      }
    });
  }

  // force=true ignora el skip por viewport (acciones explícitas y refresco periódico).
  function refresh(force) { loadCells(force); loadSites(force); loadResources(force); }

  // Al mover/zoom el mapa, debounce 300ms y el skip por bounds ya cargado
  // evitan disparar requests por cada nivel de zoom.
  function scheduleRefresh() {
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(function () {
      refreshTimer = null;
      refresh();
      syncCoverageLayer(false);
    }, 300);
  }

  refresh();
  loadCoverageCatalog();
  map.on("moveend", scheduleRefresh);

  // Disclaimer and auto-run logic
  var disclaimerAccepted = localStorage.getItem("disclaimer_accepted");
  if (!disclaimerAccepted) {
    var modal = document.getElementById("disclaimer-modal");
    if (modal) {
      modal.style.display = "flex";
      document.getElementById("btn-accept-disclaimer").addEventListener("click", function() {
        modal.style.display = "none";
        localStorage.setItem("disclaimer_accepted", "true");
        runAutomaticConnectivityTest();
      });
    }
  } else {
    runAutomaticConnectivityTest();
  }
  
  // Wire filters
  document.getElementById("op").addEventListener("change", function () { refresh(true); });
  document.getElementById("win").addEventListener("change", function () { refresh(true); });
  document.getElementById("sites").addEventListener("change", function () {
    if (this.checked && map.getZoom() < 12) {
      toast("Haz zoom para visualizar las antenas móviles");
    }
    loadSites();
  });
  document.getElementById("show-resources").addEventListener("change", function () {
    if (document.getElementById("show-resources").checked) {
      resourceMarkersGroup.addTo(map);
    } else {
      map.removeLayer(resourceMarkersGroup);
    }
  });
  coverageProviderSelect.addEventListener("change", function () {
    if (!coverageCatalog) return;
    coverageProviderId = this.value;
    var provider = coverageProviderById(coverageProviderId);
    coverageTechnologyId = "";
    rebuildCoverageTechnologyOptions(provider, "");
    coverageTechnologyId = coverageTechnologySelect.value;
    syncCoverageLayer(true);
  });
  coverageTechnologySelect.addEventListener("change", function () {
    coverageTechnologyId = this.value;
    syncCoverageLayer(true);
  });
  coverageEnabledToggle.addEventListener("change", function () {
    coverageEnabled = this.checked;
    syncCoverageLayer(true);
  });

  schedulePeriodicRefresh();
  setTimeout(function() { map.invalidateSize(); }, 200);

  function initSismoAlerts() {
    var card = document.getElementById("sismos-alert-card");
    var btn = document.getElementById("btn-subscribe-sismos");
    var status = document.getElementById("sismos-alert-status");
    if (!card || !btn) return;

    document.getElementById("btn-close-sismos-card").addEventListener("click", function() {
      card.style.display = "none";
    });

    function setStatus(msg) {
      status.textContent = msg;
      status.style.display = "block";
    }
    function setActive(on) {
      if (on) {
        card.style.display = "none";
      } else {
        card.style.display = "flex";
        btn.textContent = "ACTIVAR NOTIFICACIONES";
        btn.style.background = "var(--accent-color)";
      }
    }

    if (!("serviceWorker" in navigator) || !("PushManager" in window)) {
      btn.disabled = true;
      setStatus("Tu navegador no soporta notificaciones push.");
      return;
    }

    function base64ToUint8Array(b64) {
      var raw = atob(b64.replace(/_/g, "/").replace(/-/g, "+"));
      var arr = new Uint8Array(raw.length);
      for (var i = 0; i < raw.length; i++) arr[i] = raw.charCodeAt(i);
      return arr;
    }

    function b64Key(key) {
      return btoa(String.fromCharCode.apply(null, new Uint8Array(key)));
    }

    function unsubscribe(sub) {
      sub.unsubscribe().then(function() {
        fetch("/api/push/unsubscribe", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ endpoint: sub.endpoint })
        }).catch(function() {});
        setActive(false);
        setStatus("Notificaciones desactivadas.");
      }).catch(function() {
        setStatus("No se pudieron desactivar las notificaciones.");
      });
    }

    btn.addEventListener("click", function() {
      btn.disabled = true;
      navigator.serviceWorker.ready.then(function(reg) {
        return reg.pushManager.getSubscription().then(function(existing) {
          if (existing) { unsubscribe(existing); return null; }
          return fetch("/api/push/vapid").then(function(r) { return r.json(); })
            .then(function(v) {
              if (!v.public_key) throw { code: "no_vapid" };
              return reg.pushManager.subscribe({
                userVisibleOnly: true,
                applicationServerKey: base64ToUint8Array(v.public_key)
              });
            });
        });
      }).then(function(sub) {
        if (!sub) return;
        return fetch("/api/push/subscribe", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            endpoint: sub.endpoint,
            keys: {
              p256dh: b64Key(sub.getKey("p256dh")),
              auth: b64Key(sub.getKey("auth"))
            },
            device: (navigator.userAgent.match(/Mobile/i) ? "Móvil" : "Escritorio") + " " + navigator.userAgent
          })
        }).then(function(r) {
          if (!r.ok) throw { code: "server" };
          setActive(true);
        });
      }).catch(function(e) {
        if (e && e.code === "no_vapid") {
          setStatus("El servidor aún no ofrece notificaciones.");
        } else if (e && (e.name === "NotAllowedError" || e.code === 20)) {
          setStatus("Permiso denegado. Habilítalo en la configuración del navegador.");
        } else if (e && e.code === "server") {
          setStatus("Error al guardar la suscripción. Intenta de nuevo.");
        } else {
          setStatus("Error al activar notificaciones. Intenta de nuevo.");
        }
      }).then(function() {
        btn.disabled = false;
      });
    });

    navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(function() {});
    navigator.serviceWorker.ready.then(function(reg) {
      return reg.pushManager.getSubscription();
    }).then(setActive).catch(function() {});
  }

  initSismoAlerts();
})();

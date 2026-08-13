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

  var COLOR = { "OPERATIVO": "#2ecc71", "DEGRADADO": "#f1c40f", "AFECTACION_PROBABLE": "#e74c3c" };
  var RADIUS = { "OPERATIVO": 9, "DEGRADADO": 12, "AFECTACION_PROBABLE": 16 };

  var userLatLng = null;
  var allResources = [];
  var currentTab = "all";
  var searchQuery = "";
  var selectedResource = null;
  var isPickingLocation = false;
  var pickedLatLng = null;

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
        var needsMatch = false;
        if (r.Details && Array.isArray(r.Details.needs)) {
          needsMatch = r.Details.needs.some(function (n) {
            return n.toLowerCase().indexOf(query) !== -1;
          });
        }
        if (!nameMatch && !addrMatch && !needsMatch) return false;
      }
      return true;
    });

    // Sort by relative distance if user location is known, else by date
    filtered.sort(function (a, b) {
      if (userLatLng) {
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
      var distStr = userLatLng ? formatDistance(getDistance(userLatLng.lat, userLatLng.lng, r.Lat, r.Lon)) : "";
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

      var needsListStr = "";
      if (r.Details && Array.isArray(r.Details.needs) && r.Details.needs.length > 0) {
        needsListStr = "Se necesita: " + r.Details.needs.join(", ");
      } else {
        needsListStr = "Sin requerimientos específicos reportados.";
      }

      var div = document.createElement("div");
      div.className = "report-item";
      div.innerHTML = `
        <div class="report-item-header">
          <h4 class="report-item-title">${r.Name || "Reporte sin título"}</h4>
          <span class="report-item-time">${timeStr}</span>
        </div>
        <div class="badge-row">${badgesHtml}</div>
        <p class="report-item-desc">${needsListStr}</p>
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
    document.getElementById("detail-card-desc").textContent = r.Address + (r.Phone ? " · Tel: " + r.Phone : "");
    
    // Set counters
    document.getElementById("val-helping").textContent = (r.Details && r.Details.helping) || 0;
    document.getElementById("val-needed").textContent = (r.Details && r.Details.needed) || 0;

    // Badges
    var urgency = getResourceUrgency(r);
    var badgHtml = `<span class="badge ${urgency === 'urgente' ? 'badge-urgent' : 'badge-info'}">${urgency.toUpperCase()}</span>`;
    badgHtml += `<span class="badge badge-warning">${r.Kind.replace("_", " ").toUpperCase()}</span>`;
    document.getElementById("detail-card-badges").innerHTML = badgHtml;

    // Votes / confirmations
    var confirms = (r.Details && r.Details.confirms) || 0;
    var dismisses = (r.Details && r.Details.dismisses) || 0;
    document.getElementById("detail-card-votes").textContent = confirms + " confirman · " + dismisses + " desmienten";

    // Google Maps link
    document.getElementById("btn-gmaps").href = "https://www.google.com/maps/search/?api=1&query=" + r.Lat + "," + r.Lon;

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
    map.setView([r.Lat, r.Lon], 16);
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
      span.innerHTML = `${n} <span class="need-remove" data-index="${index}">×</span>`;
      span.querySelector(".need-remove").addEventListener("click", function (e) {
        e.stopPropagation();
        removeNeed(index);
      });
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
        text: selectedResource.Address + " - Necesita ayuda urgente.",
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

  // Add Point Trigger
  document.getElementById("btn-trigger-add").addEventListener("click", function () {
    isPickingLocation = true;
    document.getElementById("map-pick-indicator").style.display = "block";
    toast("Haz click en el mapa para ubicar el punto");
  });

  map.on("click", function (e) {
    if (isPickingLocation) {
      isPickingLocation = false;
      document.getElementById("map-pick-indicator").style.display = "none";
      pickedLatLng = e.latlng;
      document.getElementById("add-point-modal").classList.add("open");
    }
  });

  document.getElementById("btn-modal-close").addEventListener("click", function () {
    document.getElementById("add-point-modal").classList.remove("open");
    pickedLatLng = null;
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

  // Handle new point submit
  document.getElementById("add-point-form").addEventListener("submit", async function (e) {
    e.preventDefault();
    if (!pickedLatLng) return;

    var kind = document.getElementById("modal-kind").value;
    var name = document.getElementById("modal-name").value;
    var address = document.getElementById("modal-address").value;
    var phone = document.getElementById("modal-phone").value;
    var urgency = document.getElementById("modal-urgency").value;
    var rawNeeds = document.getElementById("modal-needs").value;
    
    var needsArr = rawNeeds.split(",").map(s => s.trim()).filter(Boolean);

    toast("Generando prueba de trabajo anti-spam...");
    var nonce = await solvePoW(kind, name);

    var payload = {
      kind: kind,
      name: name,
      address: address,
      phone: phone,
      lat: pickedLatLng.lat,
      lon: pickedLatLng.lng,
      nonce: nonce,
      details: {
        urgency: urgency,
        needs: needsArr,
        helping: 0,
        needed: 0,
        confirms: 1,
        dismisses: 0
      }
    };

    apiFetch("/report", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload)
    }).then(function (res) {
      if (res.ok) {
        toast("Reporte enviado exitosamente");
        document.getElementById("add-point-modal").classList.remove("open");
        document.getElementById("add-point-form").reset();
        refresh();
      } else {
        toast("Error al registrar el reporte.");
      }
    }).catch(function () {
      toast("Error de red.");
    });
  });

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
      refresh();
    }, function () {
      toast("No se pudo obtener ubicación para centrar el mapa.");
    }, { enableHighAccuracy: true, timeout: 8000 });
  }

  function loadCells() {
    var b = map.getBounds();
    var bbox = [b.getWest(), b.getSouth(), b.getEast(), b.getNorth()].join(",");
    var op = document.getElementById("op").value;
    var win = document.getElementById("win").value;
    var u = "/cells?bbox=" + encodeURIComponent(bbox) + "&window=" + win + "&operator=" + encodeURIComponent(op);
    apiFetch(u, { cache: "no-store" }).then(function (r) { if (!r.ok) throw 0; return r.json(); }).then(function (cells) {
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

  function loadSites() {
    if (!document.getElementById("sites").checked) {
      sitesClusterGroup.clearLayers();
      sitesLayer.clearLayers();
      return;
    }
    if (map.getZoom() < 12) {
      sitesClusterGroup.clearLayers();
      sitesLayer.clearLayers();
      return;
    }
    var b = map.getBounds();
    var bbox = [b.getWest(), b.getSouth(), b.getEast(), b.getNorth()].join(",");
    apiFetch("/sites?bbox=" + encodeURIComponent(bbox), { cache: "no-store" }).then(function (r) { if (!r.ok) throw 0; return r.json(); }).then(function (sites) {
      var fc = { type: "FeatureCollection", features: sites.map(function (s) {
        return { type: "Feature", geometry: { type: "Point", coordinates: [s.x, s.y] },
          properties: { o: s.o, nd: s.nd, ad: s.ad } };
      })};
      sitesClusterGroup.clearLayers();
      sitesLayer.clearLayers();
      sitesLayer.addData(fc);
      sitesClusterGroup.addLayer(sitesLayer);
    }).catch(function () {});
  }

  function loadResources() {
    var b = map.getBounds();
    var bbox = [b.getWest(), b.getSouth(), b.getEast(), b.getNorth()].join(",");
    var url = "/resources?bbox=" + encodeURIComponent(bbox);
    apiFetch(url, { cache: "no-store" }).then(function (r) { if (!r.ok) throw 0; return r.json(); }).then(function (resList) {
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
        refresh();
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

  // Admin key initialization and events
  var urlParams = new URLSearchParams(window.location.search);
  var adminKey = urlParams.get("adminkey");
  if (adminKey) {
    window.IS_ADMIN = true;
    window.ADMIN_KEY = adminKey;
    
    // Add PENDIENTES tab button dynamically
    var tabsDiv = document.querySelector(".tabs-container");
    if (tabsDiv) {
      var btn = document.createElement("button");
      btn.className = "tab-btn";
      btn.setAttribute("data-tab", "pending");
      btn.innerHTML = "PENDIENTES <span id='count-pending'>0</span>";
      tabsDiv.appendChild(btn);
      
      btn.addEventListener("click", function() {
        document.querySelectorAll(".tab-btn").forEach(function (b) { b.classList.remove("active"); });
        btn.classList.add("active");
        currentTab = "pending";
        renderReportList();
      });
    }

    document.getElementById("btn-admin-approve").addEventListener("click", function() {
      if (!selectedResource) return;
      apiFetch("/o/moderate", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ id: selectedResource.ID, status: "approved" })
      }).then(function(r) {
        if (r.ok) {
          toast("Reporte aprobado con éxito.");
          document.getElementById("admin-resource-status").textContent = "approved";
          document.getElementById("admin-resource-status").style.color = "#10b981";
          selectedResource.Status = "approved";
          refresh();
        } else {
          toast("Error al moderar el reporte.");
        }
      }).catch(function() {
        toast("Error de red al moderar el reporte.");
      });
    });

    document.getElementById("btn-admin-reject").addEventListener("click", function() {
      if (!selectedResource) return;
      apiFetch("/o/moderate", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ id: selectedResource.ID, status: "rejected" })
      }).then(function(r) {
        if (r.ok) {
          toast("Reporte rechazado con éxito.");
          document.getElementById("admin-resource-status").textContent = "rejected";
          document.getElementById("admin-resource-status").style.color = "#ef4444";
          selectedResource.Status = "rejected";
          refresh();
        } else {
          toast("Error al moderar el reporte.");
        }
      }).catch(function() {
        toast("Error de red al moderar el reporte.");
      });
    });
  }

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
        refresh();
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

  function refresh() { loadCells(); loadSites(); loadResources(); }
  refresh();
  map.on("moveend", refresh);

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
  document.getElementById("op").addEventListener("change", refresh);
  document.getElementById("win").addEventListener("change", refresh);
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

  setInterval(refresh, 60000);
  setTimeout(function() { map.invalidateSize(); }, 200);
})();

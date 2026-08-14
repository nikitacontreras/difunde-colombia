"use strict";

var state = {
  authed: false,
  tab: "observations",
  observations: {
    items: [],
    total: 0,
    limit: 50,
    offset: 0,
    query: "",
    operator: ""
  },
  resources: [],
  observationsLoaded: false,
  resourcesLoaded: false,
  overview: null,
  selected: null,
  selectedType: null,
  loading: false,
  filterTimer: null
};

var els = {
  authGate: document.getElementById("auth-gate"),
  dashboard: document.getElementById("dashboard"),
  sessionPill: document.getElementById("session-pill"),
  authError: document.getElementById("auth-error"),
  loginForm: document.getElementById("admin-login-form"),
  adminKey: document.getElementById("admin-key"),
  refresh: document.getElementById("btn-refresh"),
  logout: document.getElementById("btn-logout"),
  tabs: Array.prototype.slice.call(document.querySelectorAll("[data-tab]")),
  search: document.getElementById("search-input"),
  operator: document.getElementById("operator-input"),
  limit: document.getElementById("limit-select"),
  observationsBody: document.getElementById("observations-tbody"),
  observationsMeta: document.getElementById("observations-meta"),
  observationsPagination: document.getElementById("observations-pagination"),
  prevPage: document.getElementById("btn-prev-page"),
  nextPage: document.getElementById("btn-next-page"),
  resourcesGrid: document.getElementById("resources-grid"),
  resourcesMeta: document.getElementById("resources-meta"),
  inspectorKind: document.getElementById("inspector-kind"),
  inspectorEmpty: document.getElementById("inspector-empty"),
  inspectorContent: document.getElementById("inspector-content"),
  statObservations: document.getElementById("stat-observations"),
  stat24h: document.getElementById("stat-24h"),
  statResources: document.getElementById("stat-resources"),
  statOperators: document.getElementById("stat-operators"),
  statUpdated: document.getElementById("stat-updated"),
  statObservationsSub: document.getElementById("stat-observations-sub"),
  statResourcesSub: document.getElementById("stat-resources-sub")
};

function api(path, init) {
  init = init || {};
  var headers = new Headers(init.headers || {});
  init.headers = headers;
  init.credentials = "same-origin";
  return fetch(path, init);
}

function escapeText(value) {
  return String(value == null ? "" : value);
}

function formatDate(value) {
  if (!value) return "N/D";
  var date = new Date(value);
  if (isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat("es-CO", {
    dateStyle: "medium",
    timeStyle: "short"
  }).format(date);
}

function formatNumber(value, digits) {
  if (value === null || value === undefined || value === "") return "N/D";
  var num = Number(value);
  if (Number.isNaN(num)) return "N/D";
  return digits != null ? num.toFixed(digits) : String(num);
}

function formatRatio(value) {
  if (value === null || value === undefined || value === "") return "N/D";
  var num = Number(value);
  if (Number.isNaN(num)) return "N/D";
  return Math.round(num * 100) + "%";
}

function setAuthState(on) {
  state.authed = on;
  els.dashboard.hidden = !on;
  els.authGate.hidden = on;
  els.sessionPill.textContent = on ? "sesión activa" : "sesión inactiva";
  els.sessionPill.style.color = on ? "#c7ebff" : "";
}

function setError(msg) {
  els.authError.textContent = msg || "";
}

function handleUnauthorized(result) {
  if (result && result.unauthorized) {
    logoutAdmin();
    return true;
  }
  return false;
}

function clearInspector() {
  state.selected = null;
  state.selectedType = null;
  els.inspectorKind.textContent = "ninguno";
  els.inspectorEmpty.hidden = false;
  els.inspectorContent.hidden = true;
  els.inspectorContent.innerHTML = "";
}

function addDetailRow(container, label, value) {
  var row = document.createElement("div");
  row.className = "detail-row";

  var title = document.createElement("span");
  title.textContent = label;

  var body = document.createElement("strong");
  body.textContent = value == null || value === "" ? "N/D" : String(value);

  row.appendChild(title);
  row.appendChild(body);
  container.appendChild(row);
}

function addButton(container, label, handler) {
  var btn = document.createElement("button");
  btn.type = "button";
  btn.className = "ghost-button compact";
  btn.textContent = label;
  btn.addEventListener("click", handler);
  container.appendChild(btn);
  return btn;
}

function showInspector(kind, label, bodyBuilder) {
  state.selectedType = kind;
  els.inspectorKind.textContent = label;
  els.inspectorEmpty.hidden = true;
  els.inspectorContent.hidden = false;
  els.inspectorContent.innerHTML = "";
  var content = document.createElement("div");
  content.className = "detail-list";
  bodyBuilder(content);
  els.inspectorContent.appendChild(content);
}

function observationBadge(row) {
  if (row.call_signal === "yes") return { label: "señal", cls: "ok", value: "sí" };
  if (row.call_signal === "no") return { label: "señal", cls: "bad", value: "no" };
  return { label: "señal", cls: "warn", value: row.call_signal || "desconocida" };
}

function resourceBadge(resource) {
  var status = String(resource.Status || "pending").toLowerCase();
  if (status === "approved") return { label: "estado", cls: "ok", value: "aprobado" };
  if (status === "rejected") return { label: "estado", cls: "bad", value: "rechazado" };
  return { label: "estado", cls: "warn", value: "pendiente" };
}

function selectTab(name) {
  state.tab = name;
  els.tabs.forEach(function (btn) {
    btn.classList.toggle("active", btn.getAttribute("data-tab") === name);
  });
  if ((name === "observations" && state.selectedType !== "observation") ||
      (name === "resources" && state.selectedType !== "resource")) {
    clearInspector();
  }
  document.getElementById("panel-observations").classList.toggle("active", name === "observations");
  document.getElementById("panel-resources").classList.toggle("active", name === "resources");
  if (name === "resources") {
    if (state.authed && !state.resourcesLoaded) {
      els.resourcesGrid.innerHTML = "";
      var loading = document.createElement("div");
      loading.className = "empty-state";
      loading.textContent = "Cargando recursos...";
      els.resourcesGrid.appendChild(loading);
      loadResources().then(function (result) {
        if (handleUnauthorized(result)) return;
      }).catch(function () {
        setError("No se pudo cargar los recursos.");
      });
    } else {
      renderResources();
    }
  } else if (name === "observations") {
    if (state.authed && !state.observationsLoaded) {
      loadObservations().then(function (result) {
        if (handleUnauthorized(result)) return;
      }).catch(function () {
        setError("No se pudo cargar el histórico.");
      });
    } else {
      renderObservationsTable();
    }
  }
}

function setLoading(on) {
  state.loading = on;
  els.refresh.disabled = on;
  els.logout.disabled = on;
  els.tabs.forEach(function (btn) {
    btn.disabled = on;
  });
  els.search.disabled = on;
  els.operator.disabled = on;
  els.limit.disabled = on;
  els.prevPage.disabled = on || state.observations.offset <= 0;
  els.nextPage.disabled = on || state.observations.offset + state.observations.limit >= state.observations.total;
}

async function loginAdmin() {
  setError("");
  var key = els.adminKey.value.trim();
  if (!key) {
    setError("Ingresa el header X-Admin-Key.");
    return;
  }
  setLoading(true);
  try {
    var response = await api("/admin/session", {
      method: "POST",
      headers: { "X-Admin-Key": key }
    });
    if (!response.ok) {
      if (response.status === 401) {
        setError("La clave no es válida.");
      } else {
        setError("No se pudo abrir la sesión admin.");
      }
      return;
    }
    els.adminKey.value = "";
    await bootstrap();
  } catch (err) {
    setError("Error de red al validar el acceso.");
  } finally {
    setLoading(false);
  }
}

async function logoutAdmin() {
  setLoading(true);
  try {
    await api("/admin/logout", { method: "POST" });
  } catch (err) {}
  state.overview = null;
  state.resources = [];
  state.resourcesLoaded = false;
  state.observationsLoaded = false;
  clearTimeout(state.filterTimer);
  state.filterTimer = null;
  state.observations = {
    items: [],
    total: 0,
    limit: Number(els.limit.value) || 50,
    offset: 0,
    query: "",
    operator: ""
  };
  clearInspector();
  setAuthState(false);
  renderEmptyStates();
  setLoading(false);
}

function renderEmptyStates() {
  els.observationsBody.innerHTML = "";
  var tr = document.createElement("tr");
  var td = document.createElement("td");
  td.colSpan = 8;
  td.className = "empty-cell";
  td.textContent = state.authed ? "Cargando histórico..." : "Acceso restringido.";
  tr.appendChild(td);
  els.observationsBody.appendChild(tr);

  els.resourcesGrid.innerHTML = "";
  var empty = document.createElement("div");
  empty.className = "empty-state";
  empty.textContent = state.authed ? "Cargando recursos..." : "Acceso restringido.";
  els.resourcesGrid.appendChild(empty);
}

async function loadOverview() {
  var response = await api("/admin/api/overview");
  if (response.status === 401) return { unauthorized: true };
  if (!response.ok) throw new Error("overview");
  var data = await response.json();
  state.overview = data;
  els.statObservations.textContent = String(data.observations_total || 0);
  els.stat24h.textContent = String(data.observations_24h || 0);
  els.statResources.textContent = String(data.resources_total || 0);
  els.statOperators.textContent = String(data.active_operators_count || 0);
  els.statUpdated.textContent = data.latest_observation_at ? "Última observación: " + formatDate(data.latest_observation_at) : "Sin observaciones aún";
  els.statObservationsSub.textContent = "histórico total";
  els.statResourcesSub.textContent = [
    String(data.resources_approved || 0) + " aprobados",
    String(data.resources_pending || 0) + " pendientes",
    String(data.resources_rejected || 0) + " rechazados"
  ].join(" · ");
  return data;
}

function observationRowClass(row) {
  var signal = row.call_signal;
  if (signal === "yes") return "ok";
  if (signal === "no") return "bad";
  return "warn";
}

function renderObservationsTable() {
  els.observationsBody.innerHTML = "";
  if (!state.observations.items.length) {
    var emptyTr = document.createElement("tr");
    var emptyTd = document.createElement("td");
    emptyTd.colSpan = 8;
    emptyTd.className = "empty-cell";
    emptyTd.textContent = "No hay observaciones para esos filtros.";
    emptyTr.appendChild(emptyTd);
    els.observationsBody.appendChild(emptyTr);
  }

  state.observations.items.forEach(function (row) {
    var tr = document.createElement("tr");
    tr.className = state.selectedType === "observation" && state.selected && state.selected.id === row.id ? "selected" : "";

    function cell(text, extraClass) {
      var td = document.createElement("td");
      if (extraClass) td.className = extraClass;
      td.textContent = text;
      tr.appendChild(td);
    }

    cell(String(row.id), "row-title");
    cell(formatDate(row.observed_at));
    cell(row.operator || "desconocido");

    var rtt = row.http_rtt_median != null ? Math.round(row.http_rtt_median) + " ms" : "N/D";
    cell(rtt);

    cell(formatRatio(row.success_ratio));

    var signal = observationBadge(row);
    cell(signal.value);

    cell(row.h3_cell || "N/D");
    cell(row.client_ip || "N/D");

    tr.addEventListener("click", function () {
      selectObservation(row);
    });
    els.observationsBody.appendChild(tr);
  });

  var start = state.observations.total === 0 ? 0 : state.observations.offset + 1;
  var end = Math.min(state.observations.offset + state.observations.limit, state.observations.total);
  els.observationsMeta.textContent = [
    state.observations.total + " filas",
    state.observations.total ? "mostrando " + start + "–" + end : "sin resultados"
  ].join(" · ");
  els.prevPage.disabled = state.observations.offset <= 0;
  els.nextPage.disabled = state.observations.offset + state.observations.limit >= state.observations.total;

  els.observationsPagination.innerHTML = "";
  var summary = document.createElement("div");
  summary.textContent = state.observations.total
    ? "Página " + Math.floor(state.observations.offset / state.observations.limit + 1) +
      " de " + Math.max(1, Math.ceil(state.observations.total / state.observations.limit))
    : "Sin páginas";
  els.observationsPagination.appendChild(summary);

  var actions = document.createElement("div");
  actions.className = "pager-actions";
  var prev = document.createElement("button");
  prev.type = "button";
  prev.className = "ghost-button compact";
  prev.textContent = "Anterior";
  prev.disabled = state.observations.offset <= 0;
  prev.addEventListener("click", function () {
    if (state.observations.offset <= 0) return;
    state.observations.offset = Math.max(0, state.observations.offset - state.observations.limit);
    loadObservations();
  });
  var next = document.createElement("button");
  next.type = "button";
  next.className = "ghost-button compact";
  next.textContent = "Siguiente";
  next.disabled = state.observations.offset + state.observations.limit >= state.observations.total;
  next.addEventListener("click", function () {
    if (state.observations.offset + state.observations.limit >= state.observations.total) return;
    state.observations.offset += state.observations.limit;
    loadObservations();
  });
  actions.appendChild(prev);
  actions.appendChild(next);
  els.observationsPagination.appendChild(actions);
}

async function loadObservations() {
  var params = new URLSearchParams();
  params.set("limit", String(state.observations.limit));
  params.set("offset", String(state.observations.offset));
  if (state.observations.query) params.set("q", state.observations.query);
  if (state.observations.operator) params.set("operator", state.observations.operator);

  var response = await api("/admin/api/observations?" + params.toString());
  if (response.status === 401) return { unauthorized: true };
  if (!response.ok) throw new Error("observations");

  var page = await response.json();
  state.observations.items = page.items || [];
  state.observations.total = page.total || 0;
  state.observations.limit = page.limit || state.observations.limit;
  state.observations.offset = page.offset || 0;
  renderObservationsTable();
  if (state.selectedType === "observation" && state.selected) {
    var match = state.observations.items.find(function (item) { return item.id === state.selected.id; });
    if (match) {
      selectObservation(match);
    }
  }
  state.observationsLoaded = true;
  return page;
}

function selectObservation(row) {
  state.selected = row;
  state.selectedType = "observation";
  els.tabs.forEach(function (btn) {
    btn.classList.remove("active");
  });
  document.querySelector('[data-tab="observations"]').classList.add("active");
  document.getElementById("panel-observations").classList.add("active");
  document.getElementById("panel-resources").classList.remove("active");

  renderObservationsTable();
  showInspector("observation", "observación", function (container) {
    addDetailRow(container, "ID", row.id);
    addDetailRow(container, "Observado", formatDate(row.observed_at));
    addDetailRow(container, "Recibido", formatDate(row.received_at));
    addDetailRow(container, "Operador", row.operator || "desconocido");
    addDetailRow(container, "ASN", row.asn != null ? row.asn : "N/D");
    addDetailRow(container, "Celda H3", row.h3_cell || "N/D");
    addDetailRow(container, "Latitud", formatNumber(row.latitude, 6));
    addDetailRow(container, "Longitud", formatNumber(row.longitude, 6));
    addDetailRow(container, "Precisión", formatNumber(row.accuracy, 0) + " m");
    addDetailRow(container, "RTT mínimo", formatNumber(row.http_rtt_min, 0) + " ms");
    addDetailRow(container, "RTT mediano", formatNumber(row.http_rtt_median, 0) + " ms");
    addDetailRow(container, "Jitter", formatNumber(row.jitter, 0) + " ms");
    addDetailRow(container, "Éxito", formatRatio(row.success_ratio));
    addDetailRow(container, "Muestras", row.samples);
    addDetailRow(container, "Fallos", row.failed_requests);
    addDetailRow(container, "Tipo de conexión", row.effective_type || "N/D");
    addDetailRow(container, "RTT navegador", formatNumber(row.browser_rtt, 0) + " ms");
    addDetailRow(container, "Downlink", formatNumber(row.browser_downlink, 2) + " Mbps");
    addDetailRow(container, "Save-Data", row.save_data ? "sí" : "no");
    addDetailRow(container, "Señal de llamada", row.call_signal || "N/D");
    addDetailRow(container, "Operador usuario", row.operator_user || "N/D");
    addDetailRow(container, "Probe 1k", formatNumber(row.probe_1k_ms, 0) + " ms");
    addDetailRow(container, "Probe 4k", formatNumber(row.probe_4k_ms, 0) + " ms");
    addDetailRow(container, "Estimación", formatNumber(row.transfer_estimate_bps, 0) + " bps");
    addDetailRow(container, "Client IP", row.client_ip || "N/D");

    var actions = document.createElement("div");
    actions.className = "detail-actions";
    addButton(actions, "Abrir en Maps", function () {
      window.open("https://www.google.com/maps/search/?api=1&query=" + encodeURIComponent(row.latitude + "," + row.longitude), "_blank", "noopener");
    });
    addButton(actions, "Copiar coordenadas", function () {
      navigator.clipboard.writeText(row.latitude + ", " + row.longitude).catch(function () {});
    });
    addButton(actions, "Ir al mapa", function () {
      window.location.href = "/map";
    });
    container.appendChild(actions);
  });
}

function renderResources() {
  var query = state.observations.query.toLowerCase();
  var op = state.observations.operator.toLowerCase();
  var filtered = state.resources.filter(function (resource) {
    if (op) {
      var status = String(resource.Status || "").toLowerCase();
      if (String(resource.Kind || "").toLowerCase().indexOf(op) === -1 &&
          String(resource.Name || "").toLowerCase().indexOf(op) === -1 &&
          status.indexOf(op) === -1) {
        return false;
      }
    }
    if (query) {
      var haystack = [
        resource.Kind,
        resource.Name,
        resource.Address,
        resource.Phone,
        resource.Status,
        resource.ReportedAt
      ].join(" ").toLowerCase();
      if (haystack.indexOf(query) === -1) return false;
    }
    return true;
  });

  els.resourcesGrid.innerHTML = "";
  if (!filtered.length) {
    var empty = document.createElement("div");
    empty.className = "empty-state";
    empty.textContent = "No hay recursos para esos filtros.";
    els.resourcesGrid.appendChild(empty);
    els.resourcesMeta.textContent = "0 recursos visibles";
    return;
  }

  els.resourcesMeta.textContent = filtered.length + " recursos cargados";
  filtered.forEach(function (resource) {
    var card = document.createElement("article");
    card.className = "resource-card" + (state.selectedType === "resource" && state.selected && state.selected.ID === resource.ID ? " selected" : "");

    var head = document.createElement("div");
    head.className = "resource-head";

    var copy = document.createElement("div");
    var kind = document.createElement("span");
    kind.className = "mini-pill";
    kind.textContent = resource.Kind || "sin tipo";
    var title = document.createElement("h3");
    title.textContent = resource.Name || "Sin título";
    var desc = document.createElement("p");
    desc.textContent = resource.Address || "Sin dirección";
    copy.appendChild(kind);
    copy.appendChild(title);
    copy.appendChild(desc);

    var badgeWrap = document.createElement("div");
    var badge = resourceBadge(resource);
    var status = document.createElement("span");
    status.className = "badge " + badge.cls;
    status.textContent = badge.value;
    badgeWrap.appendChild(status);

    head.appendChild(copy);
    head.appendChild(badgeWrap);
    card.appendChild(head);

    var meta = document.createElement("div");
    meta.className = "resource-meta";
    var reported = document.createElement("span");
    reported.className = "mini-pill";
    reported.textContent = formatDate(resource.ReportedAt);
    meta.appendChild(reported);
    if (resource.Phone) {
      var phone = document.createElement("span");
      phone.className = "mini-pill";
      phone.textContent = resource.Phone;
      meta.appendChild(phone);
    }
    card.appendChild(meta);

    var actions = document.createElement("div");
    actions.className = "mini-actions";
    addButton(actions, "Ver detalle", function () {
      selectResource(resource);
    });
    addButton(actions, "Aprobar", function (e) {
      e.stopPropagation();
      moderateResource(resource.ID, "approved");
    });
    addButton(actions, "Rechazar", function (e) {
      e.stopPropagation();
      moderateResource(resource.ID, "rejected");
    });
    card.appendChild(actions);

    card.addEventListener("click", function () {
      selectResource(resource);
    });
    els.resourcesGrid.appendChild(card);
  });
}

function selectResource(resource) {
  state.selected = resource;
  state.selectedType = "resource";
  renderResources();
  showInspector("resource", "recurso", function (container) {
    addDetailRow(container, "ID", resource.ID);
    addDetailRow(container, "Tipo", resource.Kind || "N/D");
    addDetailRow(container, "Nombre", resource.Name || "N/D");
    addDetailRow(container, "Dirección", resource.Address || "N/D");
    addDetailRow(container, "Teléfono", resource.Phone || "N/D");
    addDetailRow(container, "Estado", resource.Status || "pending");
    addDetailRow(container, "Latitud", formatNumber(resource.Lat, 6));
    addDetailRow(container, "Longitud", formatNumber(resource.Lon, 6));
    addDetailRow(container, "Reportado", formatDate(resource.ReportedAt));
    addDetailRow(container, "Detalles", JSON.stringify(resource.Details || {}, null, 2));

    var actions = document.createElement("div");
    actions.className = "detail-actions";
    addButton(actions, "Aprobar", function () {
      moderateResource(resource.ID, "approved");
    });
    addButton(actions, "Rechazar", function () {
      moderateResource(resource.ID, "rejected");
    });
    if (resource.Lat != null && resource.Lon != null) {
      addButton(actions, "Abrir en Maps", function () {
        window.open("https://www.google.com/maps/search/?api=1&query=" + encodeURIComponent(resource.Lat + "," + resource.Lon), "_blank", "noopener");
      });
    }
    container.appendChild(actions);
  });
}

async function moderateResource(id, status) {
  var response = await api("/resources/moderate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: id, status: status })
  });
  if (!response.ok) {
    if (response.status === 401) {
      setAuthState(false);
    }
    return;
  }
  await refreshCurrentView();
}

async function loadResources() {
  var response = await api("/resources");
  if (response.status === 401) return { unauthorized: true };
  if (!response.ok) throw new Error("resources");
  state.resources = await response.json();
  renderResources();
  if (state.selectedType === "resource" && state.selected) {
    var match = state.resources.find(function (resource) {
      return resource.ID === state.selected.ID;
    });
    if (match) {
      selectResource(match);
    }
  }
  state.resourcesLoaded = true;
  return state.resources;
}

async function refreshCurrentView() {
  if (!state.authed) return;
  setLoading(true);
  try {
    var results = await Promise.all([
      loadOverview(),
      state.tab === "resources" ? loadResources() : loadObservations()
    ]);
    if (results.some(function (result) {
      return result && result.unauthorized;
    })) {
      await logoutAdmin();
      return;
    }
    if (state.tab === "resources") {
      renderResources();
    }
  } catch (err) {
    setError("No se pudo refrescar el panel.");
  } finally {
    setLoading(false);
  }
}

async function bootstrap() {
  setError("");
  setLoading(true);
  try {
    var overview = await loadOverview();
    if (overview && overview.unauthorized) {
      setAuthState(false);
      renderEmptyStates();
      return;
    }
    setAuthState(true);
    state.observations.limit = Number(els.limit.value) || 50;
    var results = await Promise.all([
      state.tab === "resources" ? loadResources() : loadObservations()
    ]);
    if (results.some(function (result) {
      return result && result.unauthorized;
    })) {
      await logoutAdmin();
      return;
    }
    clearInspector();
    selectTab(state.tab);
  } catch (err) {
    setAuthState(false);
    renderEmptyStates();
    setError("No se pudo cargar el panel. Verifica tu acceso.");
  } finally {
    setLoading(false);
  }
}

function queueFilterReload() {
  clearTimeout(state.filterTimer);
  state.filterTimer = setTimeout(function () {
    state.observations.offset = 0;
    if (!state.authed) return;
    if (state.tab === "observations") {
      loadObservations().then(function (result) {
        if (handleUnauthorized(result)) return;
      }).catch(function () {
        setError("No se pudo cargar el histórico.");
      });
    } else if (state.resourcesLoaded) {
      renderResources();
    }
  }, 250);
}

els.loginForm.addEventListener("submit", function (event) {
  event.preventDefault();
  loginAdmin();
});

els.refresh.addEventListener("click", function () {
  refreshCurrentView();
});

els.logout.addEventListener("click", function () {
  logoutAdmin();
});

els.tabs.forEach(function (btn) {
  btn.addEventListener("click", function () {
    selectTab(btn.getAttribute("data-tab"));
  });
});

els.search.addEventListener("input", function () {
  state.observations.query = els.search.value.trim();
  queueFilterReload();
});

els.operator.addEventListener("input", function () {
  state.observations.operator = els.operator.value.trim();
  queueFilterReload();
});

els.limit.addEventListener("change", function () {
  state.observations.limit = Number(els.limit.value) || 50;
  state.observations.offset = 0;
  if (state.authed && state.tab === "observations") {
    loadObservations().then(function (result) {
      if (handleUnauthorized(result)) return;
    }).catch(function () {
      setError("No se pudo actualizar el límite.");
    });
  }
});

els.prevPage.addEventListener("click", function () {
  if (state.observations.offset <= 0) return;
  state.observations.offset = Math.max(0, state.observations.offset - state.observations.limit);
  loadObservations().then(function (result) {
    if (handleUnauthorized(result)) return;
  }).catch(function () {
    setError("No se pudo cargar la página anterior.");
  });
});

els.nextPage.addEventListener("click", function () {
  if (state.observations.offset + state.observations.limit >= state.observations.total) return;
  state.observations.offset += state.observations.limit;
  loadObservations().then(function (result) {
    if (handleUnauthorized(result)) return;
  }).catch(function () {
    setError("No se pudo cargar la página siguiente.");
  });
});

window.addEventListener("online", function () {
  if (state.authed) {
    refreshCurrentView();
  }
});

bootstrap();

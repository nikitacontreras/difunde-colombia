"use strict";

var state = {
  tab: "observations",
  loading: false,
  observationsLoaded: false,
  resourcesLoaded: false,
  overview: null,
  observations: { items: [], total: 0, limit: 50, offset: 0 },
  resources: [],
  selected: null,
  selectedType: null,
  editingDetails: null,
  filterTimer: null,
  toastTimer: null
};

var els = {
  refresh: document.getElementById("btn-refresh"),
  network: document.getElementById("network-state"),
  message: document.getElementById("panel-message"),
  tabs: Array.prototype.slice.call(document.querySelectorAll("[data-tab]")),
  panels: Array.prototype.slice.call(document.querySelectorAll("[data-panel]")),
  workspace: document.querySelector(".workspace"),
  inspector: document.getElementById("inspector"),
  inspectorKind: document.getElementById("inspector-kind"),
  inspectorEmpty: document.getElementById("inspector-empty"),
  inspectorContent: document.getElementById("inspector-content"),
  search: document.getElementById("search-input"),
  operator: document.getElementById("operator-input"),
  from: document.getElementById("from-input"),
  to: document.getElementById("to-input"),
  limit: document.getElementById("limit-select"),
  observationsBody: document.getElementById("observations-tbody"),
  observationsMeta: document.getElementById("observations-meta"),
  observationsPagination: document.getElementById("observations-pagination"),
  prevPage: document.getElementById("btn-prev-page"),
  nextPage: document.getElementById("btn-next-page"),
  export: document.getElementById("btn-export"),
  resourcesGrid: document.getElementById("resources-grid"),
  resourcesMeta: document.getElementById("resources-meta"),
  resourceSearch: document.getElementById("resource-search"),
  resourceStatusFilter: document.getElementById("resource-status-filter"),
  resourceKindFilter: document.getElementById("resource-kind-filter"),
  resourceScopeFilter: document.getElementById("resource-scope-filter"),
  resourceIntentFilter: document.getElementById("resource-intent-filter"),
  newResource: document.getElementById("btn-new-resource"),
  pendingNavCount: document.getElementById("pending-nav-count"),
  queuePending: document.getElementById("queue-pending"),
  queueApproved: document.getElementById("queue-approved"),
  queueCity: document.getElementById("queue-city"),
  queueLogistics: document.getElementById("queue-logistics"),
  statObservations: document.getElementById("stat-observations"),
  stat24h: document.getElementById("stat-24h"),
  stat7d: document.getElementById("stat-7d"),
  statRisk: document.getElementById("stat-risk"),
  statResources: document.getElementById("stat-resources"),
  statResourcesSub: document.getElementById("stat-resources-sub"),
  statCityOffers: document.getElementById("stat-city-offers"),
  statCityOffersSub: document.getElementById("stat-city-offers-sub"),
  statOperators: document.getElementById("stat-operators"),
  statSaveData: document.getElementById("stat-save-data"),
  statUpdated: document.getElementById("stat-updated"),
  form: document.getElementById("resource-form"),
  formID: document.getElementById("resource-id"),
  formTitle: document.getElementById("editor-title"),
  formSubtitle: document.getElementById("editor-subtitle"),
  formIntent: document.getElementById("resource-intent"),
  formKind: document.getElementById("resource-kind"),
  formName: document.getElementById("resource-name"),
  formPhone: document.getElementById("resource-phone"),
  formStatus: document.getElementById("resource-status"),
  formMunicipality: document.getElementById("resource-municipality"),
  formDepartment: document.getElementById("resource-department"),
  formLat: document.getElementById("resource-lat"),
  formLon: document.getElementById("resource-lon"),
  formAddress: document.getElementById("resource-address"),
  formDescription: document.getElementById("resource-description"),
  formAvailability: document.getElementById("resource-availability"),
  formUrgency: document.getElementById("resource-urgency"),
  formNeeds: document.getElementById("resource-needs"),
  cityFields: document.getElementById("city-fields"),
  pointFields: document.getElementById("point-fields"),
  saveResource: document.getElementById("btn-save-resource"),
  cancelEdit: document.getElementById("btn-cancel-edit"),
  templateCityLogistics: document.getElementById("template-city-logistics"),
  templatePointHelp: document.getElementById("template-point-help"),
  toast: document.getElementById("toast")
};

var kindLabels = {
  logistica: "Logística",
  centro_acopio: "Centro de acopio",
  olla_comunitaria: "Olla comunitaria",
  hospital: "Salud",
  refugio: "Refugio",
  agua: "Agua",
  energia: "Energía",
  internet: "Internet",
  via_bloqueada: "Vía bloqueada",
  afectacion_estructural: "Afectación estructural"
};

function api(path, init) {
  init = init || {};
  init.credentials = "same-origin";
  return fetch(path, init).then(function (response) {
    if (response.status === 404) {
      var error = new Error("admin not found");
      error.code = "admin-not-found";
      throw error;
    }
    return response;
  });
}

function setMessage(message, success) {
  els.message.textContent = message || "";
  els.message.classList.toggle("success", Boolean(success));
}

function handleError(error, fallback) {
  if (error && error.code === "admin-not-found") {
    setMessage("El header X-Admin-Key no llegó a la petición o dejó de ser válido.", false);
    return;
  }
  setMessage(fallback || "No se pudo completar la operación.", false);
}

function showToast(message) {
  clearTimeout(state.toastTimer);
  els.toast.textContent = message;
  els.toast.hidden = false;
  state.toastTimer = setTimeout(function () { els.toast.hidden = true; }, 3200);
}

function formatDate(value) {
  if (!value) return "N/D";
  var date = new Date(value);
  if (isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat("es-CO", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

function formatNumber(value, digits) {
  if (value === null || value === undefined || value === "") return "N/D";
  var number = Number(value);
  if (!isFinite(number)) return "N/D";
  return digits === undefined ? String(number) : number.toFixed(digits);
}

function formatRatio(value) {
  if (value === null || value === undefined || value === "") return "N/D";
  var number = Number(value);
  return isFinite(number) ? Math.round(number * 100) + "%" : "N/D";
}

function resourceScope(resource) {
  return String(resource.LocationScope || "point").toLowerCase();
}

function resourceIntent(resource) {
  return String((resource.Details && resource.Details.intent) || "request").toLowerCase();
}

function resourceStatus(resource) {
  return String(resource.Status || "pending").toLowerCase();
}

function resourceLocation(resource) {
  if (resourceScope(resource) === "city") {
    return [resource.Municipality, resource.Department].filter(Boolean).join(", ") || "Ciudad sin especificar";
  }
  return resource.Address || formatNumber(resource.Lat, 5) + ", " + formatNumber(resource.Lon, 5);
}

function setLoading(on) {
  state.loading = on;
  els.refresh.disabled = on;
  els.saveResource.disabled = on;
  els.prevPage.disabled = on || state.observations.offset <= 0;
  els.nextPage.disabled = on || state.observations.offset + state.observations.limit >= state.observations.total;
  els.refresh.textContent = on ? "Actualizando..." : "Actualizar datos";
}

function clearInspector() {
  state.selected = null;
  state.selectedType = null;
  els.inspectorKind.textContent = "ninguno";
  els.inspectorEmpty.hidden = false;
  els.inspectorContent.hidden = true;
  els.inspectorContent.textContent = "";
}

function addDetailRow(container, label, value) {
  var row = document.createElement("div");
  row.className = "detail-row";
  var key = document.createElement("span");
  key.textContent = label;
  var body = document.createElement("strong");
  body.textContent = value === null || value === undefined || value === "" ? "N/D" : String(value);
  row.appendChild(key);
  row.appendChild(body);
  container.appendChild(row);
}

function addAction(container, label, handler) {
  var button = document.createElement("button");
  button.type = "button";
  button.className = "button button-quiet";
  button.textContent = label;
  button.addEventListener("click", handler);
  container.appendChild(button);
}

function showInspector(kind, builder) {
  els.inspectorKind.textContent = kind;
  els.inspectorEmpty.hidden = true;
  els.inspectorContent.hidden = false;
  els.inspectorContent.textContent = "";
  var list = document.createElement("div");
  list.className = "detail-list";
  builder(list);
  els.inspectorContent.appendChild(list);
}

function selectTab(name) {
  state.tab = name;
  els.tabs.forEach(function (button) {
    var active = button.getAttribute("data-tab") === name;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", active ? "true" : "false");
  });
  els.panels.forEach(function (panel) {
    panel.classList.toggle("active", panel.getAttribute("data-panel") === name);
  });
  var editing = name === "editor";
  els.inspector.hidden = editing;
  els.workspace.classList.toggle("editor-mode", editing);
  if (editing) clearInspector();
  if (name === "resources" && !state.resourcesLoaded) {
    loadResources().catch(function (error) { handleError(error, "No se pudieron cargar los recursos."); });
  }
  if (name === "observations" && !state.observationsLoaded) {
    loadObservations().catch(function (error) { handleError(error, "No se pudo cargar el histórico."); });
  }
}

function renderOverview(data) {
  state.overview = data;
  els.statObservations.textContent = String(data.observations_total || 0);
  els.stat24h.textContent = String(data.observations_24h || 0);
  els.stat7d.textContent = String(data.observations_7d || 0);
  els.statRisk.textContent = String(data.observations_risk_24h || 0);
  els.statResources.textContent = String(data.resources_total || 0);
  els.statResourcesSub.textContent = String(data.resources_pending || 0) + " pendientes · " + String(data.resources_approved || 0) + " publicados";
  els.statCityOffers.textContent = String(data.resources_city_scope || 0);
  els.statCityOffersSub.textContent = String(data.resources_offers || 0) + " ofertas · " + String(data.resources_logistics || 0) + " logística";
  els.statOperators.textContent = String(data.active_operators_count || 0);
  els.statSaveData.textContent = String(data.observations_save_data_24h || 0) + " usuarios con ahorro de datos";
  els.pendingNavCount.textContent = String(data.resources_pending || 0);
  els.queuePending.textContent = String(data.resources_pending || 0);
  els.queueApproved.textContent = String(data.resources_approved || 0);
  els.queueCity.textContent = String(data.resources_city_scope || 0);
  els.queueLogistics.textContent = String(data.resources_logistics || 0);
  var observationUpdate = data.latest_observation_at ? "Última observación: " + formatDate(data.latest_observation_at) : "Sin observaciones todavía";
  var resourceUpdate = data.latest_resource_at ? " · Último recurso: " + formatDate(data.latest_resource_at) : "";
  els.statUpdated.textContent = observationUpdate + resourceUpdate;
}

async function loadOverview() {
  var response = await api("/admin/api/overview");
  if (!response.ok) throw new Error("overview");
  var data = await response.json();
  renderOverview(data);
  return data;
}

function observationSignalClass(row) {
  if (row.call_signal === "yes") return "ok";
  if (row.call_signal === "no") return "bad";
  return "warn";
}

function appendCell(row, value, className) {
  var cell = document.createElement("td");
  if (className) cell.className = className;
  cell.textContent = value;
  row.appendChild(cell);
  return cell;
}

function renderObservations() {
  els.observationsBody.textContent = "";
  if (!state.observations.items.length) {
    var emptyRow = document.createElement("tr");
    var emptyCell = appendCell(emptyRow, "No hay observaciones para estos filtros.", "empty-cell");
    emptyCell.colSpan = 8;
    els.observationsBody.appendChild(emptyRow);
  }
  state.observations.items.forEach(function (observation) {
    var row = document.createElement("tr");
    if (state.selectedType === "observation" && state.selected && state.selected.id === observation.id) row.className = "selected";
    appendCell(row, String(observation.id), "mono");
    appendCell(row, formatDate(observation.observed_at));
    appendCell(row, observation.operator || "desconocido");
    appendCell(row, observation.effective_type || (observation.mobile ? "móvil" : "N/D"));
    appendCell(row, formatNumber(observation.http_rtt_median, 0) + " ms", "mono");
    appendCell(row, formatRatio(observation.success_ratio), "mono");
    var signal = appendCell(row, observation.call_signal || "desconocida", "status-dot " + observationSignalClass(observation));
    signal.setAttribute("aria-label", "Señal de llamadas: " + (observation.call_signal || "desconocida"));
    appendCell(row, observation.h3_cell || "N/D", "mono");
    row.addEventListener("click", function () { selectObservation(observation); });
    els.observationsBody.appendChild(row);
  });

  var total = state.observations.total;
  var start = total ? state.observations.offset + 1 : 0;
  var end = Math.min(state.observations.offset + state.observations.limit, total);
  els.observationsMeta.textContent = total + " registros · mostrando " + start + " a " + end;
  var page = total ? Math.floor(state.observations.offset / state.observations.limit) + 1 : 0;
  var pages = total ? Math.ceil(total / state.observations.limit) : 0;
  els.observationsPagination.textContent = "Página " + page + " de " + pages;
  els.prevPage.disabled = state.loading || state.observations.offset <= 0;
  els.nextPage.disabled = state.loading || state.observations.offset + state.observations.limit >= total;
}

function dateParam(value, endOfDay) {
  if (!value) return "";
  var suffix = endOfDay ? "T23:59:59.999" : "T00:00:00.000";
  var date = new Date(value + suffix);
  return isNaN(date.getTime()) ? "" : date.toISOString();
}

async function loadObservations() {
  var params = new URLSearchParams();
  params.set("limit", String(state.observations.limit));
  params.set("offset", String(state.observations.offset));
  if (els.search.value.trim()) params.set("q", els.search.value.trim());
  if (els.operator.value.trim()) params.set("operator", els.operator.value.trim());
  if (dateParam(els.from.value, false)) params.set("from", dateParam(els.from.value, false));
  if (dateParam(els.to.value, true)) params.set("to", dateParam(els.to.value, true));
  var response = await api("/admin/api/observations?" + params.toString());
  if (!response.ok) throw new Error("observations");
  var page = await response.json();
  state.observations.items = page.items || [];
  state.observations.total = page.total || 0;
  state.observations.limit = page.limit || state.observations.limit;
  state.observations.offset = page.offset || 0;
  state.observationsLoaded = true;
  renderObservations();
  return page;
}

function selectObservation(observation) {
  state.selected = observation;
  state.selectedType = "observation";
  renderObservations();
  showInspector("observación", function (container) {
    addDetailRow(container, "ID", observation.id);
    addDetailRow(container, "Observado", formatDate(observation.observed_at));
    addDetailRow(container, "Recibido", formatDate(observation.received_at));
    addDetailRow(container, "Operador resuelto", observation.operator || "desconocido");
    addDetailRow(container, "Operador reportado", observation.operator_user || "N/D");
    addDetailRow(container, "ASN", observation.asn == null ? "N/D" : observation.asn);
    addDetailRow(container, "Tipo de conexión", observation.effective_type || "N/D");
    addDetailRow(container, "Móvil", observation.mobile ? "sí" : "no");
    addDetailRow(container, "RTT mediano", formatNumber(observation.http_rtt_median, 0) + " ms");
    addDetailRow(container, "RTT mínimo", formatNumber(observation.http_rtt_min, 0) + " ms");
    addDetailRow(container, "Jitter", formatNumber(observation.jitter, 0) + " ms");
    addDetailRow(container, "Peticiones exitosas", formatRatio(observation.success_ratio));
    addDetailRow(container, "Muestras / fallos", observation.samples + " / " + observation.failed_requests);
    addDetailRow(container, "Señal para llamadas", observation.call_signal || "N/D");
    addDetailRow(container, "Ahorro de datos", observation.save_data ? "activo" : "inactivo");
    addDetailRow(container, "Downlink navegador", formatNumber(observation.browser_downlink, 2) + " Mbps");
    addDetailRow(container, "Probe 1k / 4k", formatNumber(observation.probe_1k_ms, 0) + " / " + formatNumber(observation.probe_4k_ms, 0) + " ms");
    addDetailRow(container, "Transferencia estimada", formatNumber(observation.transfer_estimate_bps, 0) + " bps");
    addDetailRow(container, "Precisión", formatNumber(observation.accuracy, 0) + " m");
    addDetailRow(container, "Celda H3", observation.h3_cell || "N/D");
    addDetailRow(container, "IP cliente", observation.client_ip || "N/D");
    var actions = document.createElement("div");
    actions.className = "detail-actions";
    addAction(actions, "Abrir ubicación", function () {
      window.open("https://www.google.com/maps/search/?api=1&query=" + encodeURIComponent(observation.latitude + "," + observation.longitude), "_blank", "noopener");
    });
    addAction(actions, "Copiar coordenadas", function () {
      navigator.clipboard.writeText(observation.latitude + ", " + observation.longitude).then(function () { showToast("Coordenadas copiadas."); }).catch(function () {});
    });
    container.appendChild(actions);
  });
}

function csvCell(value) {
  var text = String(value == null ? "" : value);
  return '"' + text.replace(/"/g, '""') + '"';
}

function exportObservations() {
  if (!state.observations.items.length) {
    showToast("No hay filas en esta página para exportar.");
    return;
  }
  var keys = ["id", "observed_at", "received_at", "operator", "operator_user", "asn", "effective_type", "http_rtt_median", "jitter", "success_ratio", "call_signal", "h3_cell", "latitude", "longitude", "accuracy", "client_ip"];
  var lines = [keys.map(csvCell).join(",")];
  state.observations.items.forEach(function (row) {
    lines.push(keys.map(function (key) { return csvCell(row[key]); }).join(","));
  });
  var blob = new Blob(["\ufeff" + lines.join("\n")], { type: "text/csv;charset=utf-8" });
  var link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = "observaciones-" + new Date().toISOString().slice(0, 10) + ".csv";
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(link.href);
}

function statusBadge(status) {
  if (status === "approved") return { label: "aprobado", className: "ok" };
  if (status === "rejected") return { label: "rechazado", className: "bad" };
  return { label: "pendiente", className: "warn" };
}

function renderResources() {
  var query = els.resourceSearch.value.trim().toLowerCase();
  var statusFilter = els.resourceStatusFilter.value;
  var kindFilter = els.resourceKindFilter.value;
  var scopeFilter = els.resourceScopeFilter.value;
  var intentFilter = els.resourceIntentFilter.value;
  var filtered = state.resources.filter(function (resource) {
    if (statusFilter && resourceStatus(resource) !== statusFilter) return false;
    if (kindFilter && resource.Kind !== kindFilter) return false;
    if (scopeFilter && resourceScope(resource) !== scopeFilter) return false;
    if (intentFilter && resourceIntent(resource) !== intentFilter) return false;
    if (query) {
      var haystack = [resource.ID, resource.Kind, resource.Name, resource.Address, resource.Phone, resource.Municipality, resource.Department, resource.Status, JSON.stringify(resource.Details || {})].join(" ").toLowerCase();
      if (haystack.indexOf(query) === -1) return false;
    }
    return true;
  });

  els.resourcesGrid.textContent = "";
  els.resourcesMeta.textContent = filtered.length + " visibles de " + state.resources.length + " recursos";
  if (!filtered.length) {
    var empty = document.createElement("div");
    empty.className = "empty-state";
    empty.textContent = "No hay recursos para estos filtros.";
    els.resourcesGrid.appendChild(empty);
    return;
  }

  filtered.forEach(function (resource) {
    var card = document.createElement("article");
    card.className = "resource-card";
    if (state.selectedType === "resource" && state.selected && state.selected.ID === resource.ID) card.classList.add("selected");

    var title = document.createElement("div");
    title.className = "resource-title";
    var kicker = document.createElement("span");
    kicker.className = "resource-kicker";
    kicker.textContent = (resourceIntent(resource) === "offer" ? "Ofrece · " : "Solicita · ") + (kindLabels[resource.Kind] || resource.Kind);
    var heading = document.createElement("h3");
    heading.textContent = resource.Name || "Sin título";
    title.appendChild(kicker);
    title.appendChild(heading);

    var location = document.createElement("div");
    location.className = "resource-location";
    var locationStrong = document.createElement("strong");
    locationStrong.textContent = resourceScope(resource) === "city" ? "Cobertura por ciudad" : "Punto exacto";
    var locationText = document.createElement("span");
    locationText.textContent = resourceLocation(resource);
    location.appendChild(locationStrong);
    location.appendChild(locationText);

    var contact = document.createElement("div");
    contact.className = "resource-contact";
    contact.textContent = resource.Phone || "Sin teléfono";
    var reported = document.createElement("div");
    reported.textContent = formatDate(resource.ReportedAt);
    contact.appendChild(document.createElement("br"));
    contact.appendChild(reported);

    var meta = document.createElement("div");
    meta.className = "resource-meta";
    var status = statusBadge(resourceStatus(resource));
    var statusEl = document.createElement("span");
    statusEl.className = "badge " + status.className;
    statusEl.textContent = status.label;
    var scopeEl = document.createElement("span");
    scopeEl.className = "badge";
    scopeEl.textContent = resourceScope(resource) === "city" ? "ciudad" : "punto";
    meta.appendChild(statusEl);
    meta.appendChild(scopeEl);

    var actions = document.createElement("div");
    actions.className = "resource-actions";
    function actionButton(label, symbol, className, handler) {
      var button = document.createElement("button");
      button.type = "button";
      button.className = "icon-button " + className;
      button.textContent = symbol;
      button.title = label;
      button.setAttribute("aria-label", label + ": " + resource.Name);
      button.addEventListener("click", function (event) { event.stopPropagation(); handler(); });
      actions.appendChild(button);
    }
    actionButton("Editar", "↗", "edit", function () { editResource(resource); });
    actionButton("Aprobar", "✓", "approve", function () { moderateResource(resource, "approved"); });
    actionButton("Rechazar", "×", "reject", function () { moderateResource(resource, "rejected"); });

    card.appendChild(title);
    card.appendChild(location);
    card.appendChild(contact);
    card.appendChild(meta);
    card.appendChild(actions);
    card.addEventListener("click", function () { selectResource(resource); });
    els.resourcesGrid.appendChild(card);
  });
}

async function loadResources() {
  var response = await api("/admin/api/resources");
  if (!response.ok) throw new Error("resources");
  state.resources = await response.json();
  state.resourcesLoaded = true;
  renderResources();
  return state.resources;
}

function selectResource(resource) {
  state.selected = resource;
  state.selectedType = "resource";
  renderResources();
  showInspector("recurso", function (container) {
    addDetailRow(container, "ID", resource.ID);
    addDetailRow(container, "Publicación", resourceIntent(resource) === "offer" ? "Ofrecimiento" : "Solicitud / necesidad");
    addDetailRow(container, "Categoría", kindLabels[resource.Kind] || resource.Kind);
    addDetailRow(container, "Nombre", resource.Name);
    addDetailRow(container, "Estado", resourceStatus(resource));
    addDetailRow(container, "Alcance", resourceScope(resource) === "city" ? "Toda la ciudad" : "Punto exacto");
    addDetailRow(container, "Ubicación", resourceLocation(resource));
    addDetailRow(container, "Dirección / zona", resource.Address || "N/D");
    addDetailRow(container, "Teléfono", resource.Phone || "N/D");
    if (resourceScope(resource) === "point") addDetailRow(container, "Coordenadas", formatNumber(resource.Lat, 6) + ", " + formatNumber(resource.Lon, 6));
    addDetailRow(container, "Reportado", formatDate(resource.ReportedAt));
    addDetailRow(container, "Descripción", resource.Details && resource.Details.description || "N/D");
    addDetailRow(container, "Disponibilidad", resource.Details && resource.Details.availability || "N/D");
    addDetailRow(container, "Urgencia", resource.Details && resource.Details.urgency || "normal");
    addDetailRow(container, "Etiquetas / necesidades", resource.Details && Array.isArray(resource.Details.needs) ? resource.Details.needs.join(", ") : "N/D");
    addDetailRow(container, "Confirmaciones / reportes", String(resource.Details && resource.Details.confirms || 0) + " / " + String(resource.Details && resource.Details.dismisses || 0));
    var actions = document.createElement("div");
    actions.className = "detail-actions";
    addAction(actions, "Editar", function () { editResource(resource); });
    addAction(actions, "Aprobar", function () { moderateResource(resource, "approved"); });
    addAction(actions, "Rechazar", function () { moderateResource(resource, "rejected"); });
    if (resourceScope(resource) === "point") {
      addAction(actions, "Abrir ubicación", function () {
        window.open("https://www.google.com/maps/search/?api=1&query=" + encodeURIComponent(resource.Lat + "," + resource.Lon), "_blank", "noopener");
      });
    }
    container.appendChild(actions);
  });
}

async function moderateResource(resource, status) {
  try {
    var response = await api("/resources/moderate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: resource.ID, status: status })
    });
    if (!response.ok) throw new Error("moderate");
    resource.Status = status;
    renderResources();
    selectResource(resource);
    await loadOverview();
    showToast(status === "approved" ? "Recurso publicado." : "Recurso rechazado.");
  } catch (error) {
    handleError(error, "No se pudo cambiar el estado del recurso.");
  }
}

function currentScope() {
  var checked = document.querySelector('input[name="resource-scope"]:checked');
  return checked ? checked.value : "city";
}

function setScope(scope) {
  var radio = document.querySelector('input[name="resource-scope"][value="' + scope + '"]');
  if (radio) radio.checked = true;
  toggleScopeFields();
}

function toggleScopeFields() {
  var city = currentScope() === "city";
  els.cityFields.hidden = !city;
  els.pointFields.hidden = city;
  els.formMunicipality.required = city;
  els.formLat.required = !city;
  els.formLon.required = !city;
}

function resetResourceForm(template) {
  els.form.reset();
  els.formID.value = "";
  state.editingDetails = null;
  els.formTitle.textContent = "Agregar información";
  els.formSubtitle.textContent = "Publica un recurso puntual o una oferta que cubre toda una ciudad.";
  els.cancelEdit.hidden = true;
  els.saveResource.textContent = "Publicar recurso";
  els.formStatus.value = "approved";
  els.formUrgency.value = "normal";
  if (template === "point") {
    els.formIntent.value = "request";
    els.formKind.value = "centro_acopio";
    setScope("point");
  } else {
    els.formIntent.value = "offer";
    els.formKind.value = "logistica";
    setScope("city");
  }
}

function editResource(resource) {
  resetResourceForm(resourceScope(resource));
  state.editingDetails = Object.assign({}, resource.Details || {});
  els.formID.value = String(resource.ID);
  els.formTitle.textContent = "Editar recurso #" + resource.ID;
  els.formSubtitle.textContent = "Actualiza ubicación, alcance, datos de contacto o estado de publicación.";
  els.cancelEdit.hidden = false;
  els.saveResource.textContent = "Guardar cambios";
  els.formIntent.value = resourceIntent(resource);
  els.formKind.value = resource.Kind;
  els.formName.value = resource.Name || "";
  els.formPhone.value = resource.Phone || "";
  els.formStatus.value = resourceStatus(resource);
  setScope(resourceScope(resource));
  els.formMunicipality.value = resource.Municipality || "";
  els.formDepartment.value = resource.Department || "";
  els.formLat.value = resourceScope(resource) === "point" ? resource.Lat : "";
  els.formLon.value = resourceScope(resource) === "point" ? resource.Lon : "";
  els.formAddress.value = resource.Address || "";
  els.formDescription.value = resource.Details && resource.Details.description || "";
  els.formAvailability.value = resource.Details && resource.Details.availability || "";
  els.formUrgency.value = resource.Details && resource.Details.urgency || "normal";
  els.formNeeds.value = resource.Details && Array.isArray(resource.Details.needs) ? resource.Details.needs.join(", ") : "";
  selectTab("editor");
  window.scrollTo({ top: 0, behavior: "smooth" });
}

function formPayload() {
  var details = Object.assign({}, state.editingDetails || {});
  details.intent = els.formIntent.value;
  details.urgency = els.formUrgency.value;
  details.description = els.formDescription.value.trim();
  details.availability = els.formAvailability.value.trim();
  details.needs = els.formNeeds.value.split(",").map(function (item) { return item.trim(); }).filter(Boolean);
  if (details.helping == null) details.helping = 0;
  if (details.needed == null) details.needed = 0;
  if (details.confirms == null) details.confirms = 1;
  if (details.dismisses == null) details.dismisses = 0;
  var scope = currentScope();
  var payload = {
    kind: els.formKind.value,
    name: els.formName.value.trim(),
    address: els.formAddress.value.trim(),
    phone: els.formPhone.value.trim(),
    location_scope: scope,
    municipality: scope === "city" ? els.formMunicipality.value.trim() : "",
    department: scope === "city" ? els.formDepartment.value.trim() : "",
    status: els.formStatus.value,
    details: details
  };
  if (scope === "point") {
    payload.lat = Number(els.formLat.value);
    payload.lon = Number(els.formLon.value);
  }
  return payload;
}

async function saveResource(event) {
  event.preventDefault();
  if (!els.form.reportValidity()) return;
  var id = Number(els.formID.value || 0);
  setLoading(true);
  setMessage("");
  try {
    var response = await api(id ? "/admin/api/resources/" + id : "/admin/api/resources", {
      method: id ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(formPayload())
    });
    if (!response.ok) {
      var reason = await response.text();
      throw new Error(reason || "resource save");
    }
    resetResourceForm("city");
    state.resourcesLoaded = false;
    await Promise.all([loadOverview(), loadResources()]);
    selectTab("resources");
    setMessage(id ? "Cambios guardados." : "Recurso creado y disponible para moderación.", true);
    showToast(id ? "Recurso actualizado." : "Recurso creado.");
  } catch (error) {
    handleError(error, error && error.message && error.message !== "resource save" ? error.message : "No se pudo guardar el recurso.");
  } finally {
    setLoading(false);
  }
}

function queueObservationFilter() {
  clearTimeout(state.filterTimer);
  state.filterTimer = setTimeout(function () {
    state.observations.offset = 0;
    loadObservations().catch(function (error) { handleError(error, "No se pudo actualizar el histórico."); });
  }, 260);
}

async function refreshCurrent() {
  setLoading(true);
  setMessage("");
  try {
    var loaders = [loadOverview()];
    if (state.tab === "resources") loaders.push(loadResources());
    if (state.tab === "observations") loaders.push(loadObservations());
    await Promise.all(loaders);
    setMessage("Datos actualizados " + new Intl.DateTimeFormat("es-CO", { timeStyle: "short" }).format(new Date()) + ".", true);
  } catch (error) {
    handleError(error, "No se pudo actualizar el panel.");
  } finally {
    setLoading(false);
  }
}

function updateNetworkState() {
  var online = navigator.onLine;
  els.network.textContent = online ? "En línea" : "Sin conexión";
  els.network.style.color = online ? "" : "#ffc9bf";
}

els.tabs.forEach(function (button) {
  button.addEventListener("click", function () {
    if (button.getAttribute("data-tab") === "editor" && !els.formID.value) resetResourceForm("city");
    selectTab(button.getAttribute("data-tab"));
  });
});
els.refresh.addEventListener("click", refreshCurrent);
els.export.addEventListener("click", exportObservations);
els.newResource.addEventListener("click", function () { resetResourceForm("city"); selectTab("editor"); });
els.templateCityLogistics.addEventListener("click", function () { resetResourceForm("city"); els.formName.focus(); });
els.templatePointHelp.addEventListener("click", function () { resetResourceForm("point"); els.formName.focus(); });
els.cancelEdit.addEventListener("click", function () { resetResourceForm("city"); selectTab("resources"); });
els.form.addEventListener("submit", saveResource);
Array.prototype.slice.call(document.querySelectorAll('input[name="resource-scope"]')).forEach(function (radio) {
  radio.addEventListener("change", toggleScopeFields);
});

[els.search, els.operator].forEach(function (input) { input.addEventListener("input", queueObservationFilter); });
[els.from, els.to].forEach(function (input) { input.addEventListener("change", queueObservationFilter); });
els.limit.addEventListener("change", function () {
  state.observations.limit = Number(els.limit.value) || 50;
  state.observations.offset = 0;
  loadObservations().catch(function (error) { handleError(error, "No se pudo cambiar el número de filas."); });
});
els.prevPage.addEventListener("click", function () {
  state.observations.offset = Math.max(0, state.observations.offset - state.observations.limit);
  loadObservations().catch(function (error) { handleError(error, "No se pudo cargar la página anterior."); });
});
els.nextPage.addEventListener("click", function () {
  if (state.observations.offset + state.observations.limit >= state.observations.total) return;
  state.observations.offset += state.observations.limit;
  loadObservations().catch(function (error) { handleError(error, "No se pudo cargar la página siguiente."); });
});

[els.resourceSearch, els.resourceStatusFilter, els.resourceKindFilter, els.resourceScopeFilter, els.resourceIntentFilter].forEach(function (input) {
  input.addEventListener(input.tagName === "INPUT" ? "input" : "change", renderResources);
});
window.addEventListener("online", function () { updateNetworkState(); refreshCurrent(); });
window.addEventListener("offline", updateNetworkState);

async function bootstrap() {
  updateNetworkState();
  resetResourceForm("city");
  setLoading(true);
  try {
    await Promise.all([loadOverview(), loadObservations()]);
    selectTab("observations");
  } catch (error) {
    handleError(error, "No se pudo iniciar el centro de operaciones.");
  } finally {
    setLoading(false);
  }
}

bootstrap();

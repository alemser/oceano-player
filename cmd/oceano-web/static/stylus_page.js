"use strict";

const stylusPageState = {
  catalog: [],
  stylus: null,
  metrics: null,
  listenersBound: false,
  canEdit: false,
};

function toast(msg, isError) {
  const el = document.getElementById("toast");
  if (!el) return;
  el.textContent = msg;
  el.className = isError ? "toast-error" : "toast-ok";
  el.style.opacity = "1";
  clearTimeout(el._t);
  el._t = setTimeout(() => { el.style.opacity = "0"; }, 3500);
}

function stylusNum(v, digits = 2) {
  const n = Number(v || 0);
  return n.toFixed(digits).replace(/\.00$/, "");
}

function setStateBadge(state) {
  const el = document.getElementById("stylus-state-badge");
  if (!el) return;
  const raw = String(state || "healthy").toLowerCase();
  const map = { healthy: "HEALTHY", plan: "PLAN", soon: "SOON", overdue: "OVERDUE" };
  el.textContent = map[raw] || "HEALTHY";
  let color = "var(--fg)";
  if (raw === "plan") color = "#f6c945";
  if (raw === "soon") color = "#f39b47";
  if (raw === "overdue") color = "#e65c5c";
  el.style.color = color;
  el.style.borderColor = color;
}

function applyAvailability(canEdit, reason) {
  const ids = [
    "stylus-enabled",
    "stylus-mode",
    "stylus-catalog-select",
    "stylus-custom-brand",
    "stylus-custom-model",
    "stylus-custom-profile",
    "stylus-custom-lifetime",
    "stylus-is-new",
    "stylus-initial-hours",
    "stylus-save-btn",
    "stylus-replace-btn",
  ];
  ids.forEach((id) => {
    const el = document.getElementById(id);
    if (!el) return;
    el.disabled = !canEdit;
  });
  const msg = document.getElementById("stylus-disabled-msg");
  if (msg) {
    msg.style.display = canEdit ? "none" : "";
    msg.textContent = reason || "Stylus tracking is available when vinyl topology is configured.";
  }
}

function syncModeFields() {
  const mode = document.getElementById("stylus-mode")?.value || "catalog";
  const showCatalog = mode === "catalog";
  const catalogField = document.getElementById("stylus-catalog-field");
  if (catalogField) catalogField.style.display = showCatalog ? "" : "none";

  const customIds = [
    "stylus-custom-brand-field",
    "stylus-custom-model-field",
    "stylus-custom-profile-field",
    "stylus-custom-lifetime-field",
  ];
  customIds.forEach((id) => {
    const el = document.getElementById(id);
    if (el) el.style.display = showCatalog ? "none" : "";
  });
}

function syncInitialHoursField() {
  const isNew = document.getElementById("stylus-is-new")?.checked;
  const field = document.getElementById("stylus-initial-hours-field");
  if (!field) return;
  field.style.display = isNew ? "none" : "";
}

function bindListenersOnce() {
  if (stylusPageState.listenersBound) return;
  stylusPageState.listenersBound = true;
  document.getElementById("stylus-mode")?.addEventListener("change", syncModeFields);
  document.getElementById("stylus-is-new")?.addEventListener("change", syncInitialHoursField);
  document.getElementById("stylus-save-btn")?.addEventListener("click", saveStylusSettings);
  document.getElementById("stylus-replace-btn")?.addEventListener("click", replaceStylusNow);
}

function renderCatalog(items) {
  const sel = document.getElementById("stylus-catalog-select");
  if (!sel) return;
  sel.innerHTML = "";
  for (const it of items || []) {
    const opt = document.createElement("option");
    opt.value = String(it.id);
    const hours = Number(it.recommended_hours || 0);
    opt.textContent = `${it.brand} ${it.model} (${it.stylus_profile}, ${hours}h)`;
    sel.appendChild(opt);
  }
}

function renderMetrics(metrics) {
  const m = metrics || {};
  const setText = (id, value) => {
    const el = document.getElementById(id);
    if (el) el.textContent = value;
  };
  setText("stylus-m-vinyl-hours", `${stylusNum(m.vinyl_hours_since_install)} h`);
  setText("stylus-m-total-hours", `${stylusNum(m.stylus_hours_total)} h`);
  setText("stylus-m-remaining-hours", `${stylusNum(m.remaining_hours)} h`);
  setText("stylus-m-wear", `${stylusNum(m.wear_percent)}%`);
  setStateBadge(m.state || "healthy");

  const fill = document.getElementById("stylus-progress-fill");
  if (fill) {
    const pct = Math.max(0, Math.min(100, Number(m.wear_percent || 0)));
    fill.style.width = `${pct}%`;
  }
}

function setValue(id, value) {
  const el = document.getElementById(id);
  if (el) el.value = value ?? "";
}

function getValue(id) {
  return (document.getElementById(id)?.value || "").trim();
}

function loadFormFromState(resp) {
  const enabled = !!resp?.enabled;
  const stylus = resp?.stylus || null;
  const metrics = resp?.metrics || null;

  stylusPageState.stylus = stylus;
  stylusPageState.metrics = metrics;

  const enabledEl = document.getElementById("stylus-enabled");
  if (enabledEl) enabledEl.checked = enabled;

  const modeEl = document.getElementById("stylus-mode");
  if (modeEl) modeEl.value = stylus?.catalog_id ? "catalog" : "custom";

  const catalogEl = document.getElementById("stylus-catalog-select");
  if (catalogEl && stylus?.catalog_id) catalogEl.value = String(stylus.catalog_id);

  setValue("stylus-custom-brand", stylus?.brand || "");
  setValue("stylus-custom-model", stylus?.model || "");
  setValue("stylus-custom-profile", stylus?.stylus_profile || "");
  setValue("stylus-custom-lifetime", stylus?.lifetime_hours || "");

  const isNew = Number(stylus?.initial_used_hours || 0) <= 0;
  const isNewEl = document.getElementById("stylus-is-new");
  if (isNewEl) isNewEl.checked = isNew;
  setValue("stylus-initial-hours", stylus?.initial_used_hours || 0);

  syncModeFields();
  syncInitialHoursField();
  renderMetrics(metrics);
}

function buildRequestPayload() {
  const enabled = !!document.getElementById("stylus-enabled")?.checked;
  const mode = document.getElementById("stylus-mode")?.value || "catalog";
  const isNew = !!document.getElementById("stylus-is-new")?.checked;
  const payload = { enabled, is_new: isNew };

  if (!enabled) return payload;

  if (mode === "catalog") {
    const id = parseInt(document.getElementById("stylus-catalog-select")?.value || "0", 10);
    if (!Number.isFinite(id) || id <= 0) {
      throw new Error("Choose a catalog model.");
    }
    payload.catalog_id = id;
  } else {
    const lifetime = parseInt(document.getElementById("stylus-custom-lifetime")?.value || "0", 10);
    payload.brand = getValue("stylus-custom-brand");
    payload.model = getValue("stylus-custom-model");
    payload.stylus_profile = getValue("stylus-custom-profile");
    payload.lifetime_hours = Number.isFinite(lifetime) ? lifetime : 0;
    if (!payload.brand || !payload.model || !payload.stylus_profile || payload.lifetime_hours <= 0) {
      throw new Error("Fill all custom stylus fields and set lifetime hours > 0.");
    }
  }

  if (!isNew) {
    const v = document.getElementById("stylus-initial-hours")?.value;
    if (String(v || "").trim() !== "") {
      const n = Number(v);
      if (!Number.isFinite(n) || n < 0) throw new Error("Initial used hours must be >= 0.");
      payload.initial_used_hours = n;
    }
  }
  return payload;
}

async function loadStylusPage() {
  bindListenersOnce();
  let setupStatus = null;
  try {
    const sr = await fetch("/api/setup-status", { cache: "no-store" });
    if (sr.ok) setupStatus = await sr.json();
  } catch {}

  const vinylTopologyPresent = !!(setupStatus?.vinyl_topology_present);
  stylusPageState.canEdit = vinylTopologyPresent;
  applyAvailability(
    stylusPageState.canEdit,
    vinylTopologyPresent ? "" : "Configure at least one vinyl physical-media device to enable stylus tracking setup."
  );

  try {
    const [catalogRes, stylusRes] = await Promise.all([
      fetch("/api/stylus/catalog"),
      fetch("/api/stylus"),
    ]);
    if (!catalogRes.ok || !stylusRes.ok) {
      throw new Error("load failed");
    }
    const catalogPayload = await catalogRes.json();
    const stylusPayload = await stylusRes.json();
    stylusPageState.catalog = catalogPayload.items || [];
    renderCatalog(stylusPageState.catalog);
    loadFormFromState(stylusPayload);
  } catch {
    toast("Failed to load stylus settings.", true);
  }
}

async function saveStylusSettings() {
  if (!stylusPageState.canEdit) {
    toast("Stylus tracking is available when vinyl topology is configured.", true);
    return;
  }
  let payload;
  try {
    payload = buildRequestPayload();
  } catch (err) {
    toast(err.message || "Invalid stylus settings.", true);
    return;
  }

  try {
    const res = await fetch("/api/stylus", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      toast(body.error || "Failed to save stylus settings.", true);
      return;
    }
    loadFormFromState(body);
    toast("Stylus settings saved.", false);
  } catch {
    toast("Failed to save stylus settings.", true);
  }
}

async function replaceStylusNow() {
  if (!stylusPageState.canEdit) {
    toast("Stylus tracking is available when vinyl topology is configured.", true);
    return;
  }
  const mode = document.getElementById("stylus-mode")?.value || "catalog";
  const isNew = !!document.getElementById("stylus-is-new")?.checked;
  const payload = { is_new: isNew };

  try {
    if (mode === "catalog") {
      const id = parseInt(document.getElementById("stylus-catalog-select")?.value || "0", 10);
      if (Number.isFinite(id) && id > 0) payload.catalog_id = id;
    } else {
      const lifetime = parseInt(document.getElementById("stylus-custom-lifetime")?.value || "0", 10);
      payload.brand = getValue("stylus-custom-brand");
      payload.model = getValue("stylus-custom-model");
      payload.stylus_profile = getValue("stylus-custom-profile");
      if (Number.isFinite(lifetime) && lifetime > 0) payload.lifetime_hours = lifetime;
    }

    if (!isNew) {
      const v = document.getElementById("stylus-initial-hours")?.value;
      if (String(v || "").trim() !== "") {
        const n = Number(v);
        if (!Number.isFinite(n) || n < 0) {
          toast("Initial used hours must be >= 0.", true);
          return;
        }
        payload.initial_used_hours = n;
      }
    }

    const res = await fetch("/api/stylus/replace", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      toast(body.error || "Failed to replace stylus.", true);
      return;
    }
    loadFormFromState(body);
    toast("Stylus replaced and hours reset.", false);
  } catch {
    toast("Failed to replace stylus.", true);
  }
}

document.addEventListener("DOMContentLoaded", loadStylusPage);

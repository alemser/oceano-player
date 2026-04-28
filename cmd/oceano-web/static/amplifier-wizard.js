"use strict";

const WIZARD_ACTIVE_STEP_KEY = "oceano.onboarding.amplifier_wizard.active_step";

const WIZ_STEPS = [
  {
    id: "topology",
    title: "Identity and topology",
    desc: "Set amplifier maker/model, input map and connected devices.",
    href: "/topology",
    state(status) {
      if (!status) return { label: "Loading", tone: "" };
      return status.amplifier_topology_complete
        ? { label: "Done", tone: "done" }
        : { label: "Pending", tone: "warn" };
    },
  },
  {
    id: "pairing",
    title: "Broadlink pairing (IR path)",
    desc: "Required before learning IR commands when IR is enabled.",
    href: "/ir-setup",
    state(status) {
      if (!status) return { label: "Loading", tone: "" };
      if (!status.amplifier_ir_enabled) return { label: "Optional", tone: "" };
      return status.broadlink_paired
        ? { label: "Done", tone: "done" }
        : { label: "Pair required", tone: "warn" };
    },
  },
  {
    id: "ir",
    title: "IR learning",
    desc: "Learn power, volume and input commands only after pairing.",
    href: "/ir-setup",
    state(status) {
      if (!status) return { label: "Loading", tone: "" };
      if (!status.amplifier_ir_enabled) return { label: "Skipped (IR off)", tone: "" };
      return status.broadlink_paired
        ? { label: "Ready", tone: "done" }
        : { label: "Blocked by pairing", tone: "warn" };
    },
  },
  {
    id: "calibration",
    title: "Physical calibration",
    desc: "Run calibration only for physical-media inputs.",
    href: "/recognition.html",
    state(status) {
      if (!status) return { label: "Loading", tone: "" };
      if (!status.calibration_physical_recommended) return { label: "Not required", tone: "done" };
      return status.calibration_physical_complete
        ? { label: "Done", tone: "done" }
        : { label: "Recommended", tone: "warn" };
    },
  },
  {
    id: "stylus",
    title: "Stylus tracking (vinyl path)",
    desc: "Configure profile and rated life when vinyl topology exists.",
    href: "/stylus",
    state(status) {
      if (!status) return { label: "Loading", tone: "" };
      if (!status.vinyl_topology_present) return { label: "Not applicable", tone: "" };
      return status.stylus_profile_configured
        ? { label: "Done", tone: "done" }
        : { label: "Recommended", tone: "warn" };
    },
  },
];

const summaryEl = document.getElementById("wiz-summary");
const stepsEl = document.getElementById("wiz-steps");
const errEl = document.getElementById("wiz-error");
const refreshBtn = document.getElementById("wiz-refresh");
const currentTitleEl = document.getElementById("wiz-current-title");
const currentDescEl = document.getElementById("wiz-current-desc");
const currentStateEl = document.getElementById("wiz-current-state");
const currentGateEl = document.getElementById("wiz-current-gate");
const openStepEl = document.getElementById("wiz-open-step");
const prevBtn = document.getElementById("wiz-prev");
const nextBtn = document.getElementById("wiz-next");
const roleFormatEl = document.getElementById("wiz-role-format");
const roleFormatListEl = document.getElementById("wiz-role-format-list");
const roleFormatErrEl = document.getElementById("wiz-role-format-error");
const roleFormatSaveBtn = document.getElementById("wiz-role-format-save");
const stylusEl = document.getElementById("wiz-stylus");
const stylusSummaryEl = document.getElementById("wiz-stylus-summary");
const stylusCatalogEl = document.getElementById("wiz-stylus-catalog");
const stylusResetHoursEl = document.getElementById("wiz-stylus-reset-hours");
const stylusErrEl = document.getElementById("wiz-stylus-error");
const stylusSaveBtn = document.getElementById("wiz-stylus-save");

let _currentStepIdx = 0;
let _lastStatus = null;
let _wizardConfig = null;
let _stylusState = null;
let _stylusCatalog = [];

function escapeHTML(v) {
  return String(v)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function stepById(id) {
  return WIZ_STEPS.findIndex((s) => s.id === id);
}

function loadStepFromQuery() {
  const params = new URLSearchParams(window.location.search || "");
  const id = params.get("step");
  if (!id) return false;
  const idx = stepById(id);
  if (idx < 0) return false;
  _currentStepIdx = idx;
  saveCurrentStep();
  return true;
}

function loadSavedStep() {
  const id = localStorage.getItem(WIZARD_ACTIVE_STEP_KEY);
  if (!id) return;
  const idx = stepById(id);
  if (idx >= 0) _currentStepIdx = idx;
}

function saveCurrentStep() {
  const s = WIZ_STEPS[_currentStepIdx];
  if (s) localStorage.setItem(WIZARD_ACTIVE_STEP_KEY, s.id);
}

function isIRBlocked(status) {
  return !!status && !!status.amplifier_ir_enabled && !status.broadlink_paired;
}

function isStepBlocked(step, status) {
  return step.id === "ir" && isIRBlocked(status);
}

function renderCurrentStep(status) {
  const step = WIZ_STEPS[_currentStepIdx];
  if (!step) return;
  const s = step.state(status);
  const blocked = isStepBlocked(step, status);
  currentTitleEl.textContent = `Step ${_currentStepIdx + 1}: ${step.title}`;
  currentDescEl.textContent = step.desc;
  currentStateEl.textContent = s.label;
  currentStateEl.className = `wiz-step-state ${s.tone || ""}`;
  openStepEl.href = blocked ? "/pair.html" : step.href;
  openStepEl.textContent = blocked ? "Open Broadlink pairing first" : "Open current step";
  currentGateEl.hidden = !blocked;
  currentGateEl.textContent = blocked
    ? "IR learning is blocked until Broadlink pairing is completed."
    : "";
  prevBtn.disabled = _currentStepIdx <= 0;
  const nextStep = WIZ_STEPS[_currentStepIdx + 1];
  const nextBlocked = nextStep ? isStepBlocked(nextStep, status) : false;
  nextBtn.disabled = _currentStepIdx >= WIZ_STEPS.length - 1 || nextBlocked;
  if (nextBlocked) {
    nextBtn.title = "Complete Broadlink pairing before moving to IR learning.";
  } else {
    nextBtn.removeAttribute("title");
  }
}

function inputNameByID(inputs, id) {
  return (inputs || []).find((inp) => String(inp.id) === String(id))?.logical_name || String(id);
}

function renderRoleFormatEditor() {
  const step = WIZ_STEPS[_currentStepIdx];
  const isTopology = step && step.id === "topology";
  roleFormatEl.hidden = !isTopology;
  if (!isTopology) return;
  if (!_wizardConfig || !_wizardConfig.amplifier) {
    roleFormatListEl.innerHTML = `<div class="wiz-step-desc">Loading devices...</div>`;
    return;
  }
  const amp = _wizardConfig.amplifier || {};
  const devices = Array.isArray(amp.connected_devices) ? amp.connected_devices : [];
  if (!devices.length) {
    roleFormatListEl.innerHTML = `<div class="wiz-step-desc">No connected devices yet. Add devices in amplifier configuration first.</div>`;
    return;
  }
  roleFormatListEl.innerHTML = devices.map((dev, idx) => {
    const ids = Array.isArray(dev.input_ids) ? dev.input_ids : [];
    const inputsLabel = ids.length ? ids.map((id) => inputNameByID(amp.inputs || [], id)).join(", ") : "No input mapping";
    const role = String(dev.role || "physical_media");
    const format = String(dev.physical_format || "unspecified");
    return `
      <div class="wiz-role-row">
        <div class="wiz-role-name">
          ${escapeHTML(dev.name || "Unnamed device")}
          <small>${escapeHTML(inputsLabel)}</small>
        </div>
        <select class="wiz-role-select" data-role-idx="${idx}">
          <option value="physical_media" ${role === "physical_media" ? "selected" : ""}>physical_media</option>
          <option value="streaming" ${role === "streaming" ? "selected" : ""}>streaming</option>
          <option value="other" ${role === "other" ? "selected" : ""}>other</option>
        </select>
        <select class="wiz-role-select" data-format-idx="${idx}" ${role === "physical_media" ? "" : "disabled"}>
          <option value="unspecified" ${format === "unspecified" ? "selected" : ""}>unspecified</option>
          <option value="vinyl" ${format === "vinyl" ? "selected" : ""}>vinyl</option>
          <option value="cd" ${format === "cd" ? "selected" : ""}>cd</option>
          <option value="tape" ${format === "tape" ? "selected" : ""}>tape</option>
          <option value="mixed" ${format === "mixed" ? "selected" : ""}>mixed</option>
        </select>
      </div>
    `;
  }).join("");
  roleFormatListEl.querySelectorAll("[data-role-idx]").forEach((el) => {
    el.addEventListener("change", () => {
      const idx = Number(el.getAttribute("data-role-idx"));
      const dev = _wizardConfig?.amplifier?.connected_devices?.[idx];
      if (!dev) return;
      dev.role = el.value || "physical_media";
      if (dev.role !== "physical_media") dev.physical_format = "unspecified";
      renderRoleFormatEditor();
    });
  });
  roleFormatListEl.querySelectorAll("[data-format-idx]").forEach((el) => {
    el.addEventListener("change", () => {
      const idx = Number(el.getAttribute("data-format-idx"));
      const dev = _wizardConfig?.amplifier?.connected_devices?.[idx];
      if (!dev) return;
      dev.physical_format = el.value || "unspecified";
      if (dev.physical_format === "vinyl") dev.role = "physical_media";
      renderRoleFormatEditor();
    });
  });
}

async function loadWizardConfig() {
  const r = await fetch("/api/config", { cache: "no-store" });
  if (!r.ok) throw new Error(`config HTTP ${r.status}`);
  _wizardConfig = await r.json();
}

async function loadStylusState() {
  const [catalogResp, stateResp] = await Promise.all([
    fetch("/api/stylus/catalog", { cache: "no-store" }),
    fetch("/api/stylus", { cache: "no-store" }),
  ]);
  if (!catalogResp.ok) throw new Error(`stylus catalog HTTP ${catalogResp.status}`);
  if (!stateResp.ok) throw new Error(`stylus state HTTP ${stateResp.status}`);
  const catalog = await catalogResp.json();
  const state = await stateResp.json();
  _stylusCatalog = Array.isArray(catalog.items) ? catalog.items : [];
  _stylusState = state || null;
}

function renderStylusEditor(status) {
  const step = WIZ_STEPS[_currentStepIdx];
  const isStylusStep = step && step.id === "stylus";
  const available = !!status?.vinyl_topology_present;
  stylusEl.hidden = !(isStylusStep && available);
  if (!isStylusStep || !available) return;
  if (!_stylusState) {
    stylusSummaryEl.textContent = "Loading stylus state...";
    return;
  }
  const s = _stylusState.stylus;
  const m = _stylusState.metrics || {};
  const summaryParts = [];
  if (s) summaryParts.push(`${s.brand} ${s.model} (${s.stylus_profile})`);
  summaryParts.push(`Wear ${Number(m.wear_percent || 0).toFixed(1)}%`);
  summaryParts.push(`Remaining ${Number(m.remaining_hours || 0).toFixed(1)}h`);
  stylusSummaryEl.textContent = summaryParts.join(" · ");
  stylusCatalogEl.innerHTML = _stylusCatalog.map((it) => {
    const selected = s && Number(s.catalog_id || 0) === Number(it.id) ? "selected" : "";
    return `<option value="${it.id}" ${selected}>${escapeHTML(it.brand)} ${escapeHTML(it.model)} (${escapeHTML(it.stylus_profile)}, ${it.recommended_hours}h)</option>`;
  }).join("");
  stylusSaveBtn.disabled = _stylusCatalog.length === 0;
}

function render(status) {
  let doneCount = 0;
  stepsEl.innerHTML = WIZ_STEPS.map((step, idx) => {
    const s = step.state(status);
    const done = s.tone === "done";
    const blocked = isStepBlocked(step, status);
    const active = idx === _currentStepIdx;
    if (done) doneCount += 1;
    return `
      <article class="wiz-step ${done ? "done" : ""} ${active ? "active" : ""}">
        <span class="wiz-step-index">${idx + 1}</span>
        <div>
          <div class="wiz-step-title">${escapeHTML(step.title)}</div>
          <div class="wiz-step-desc">${escapeHTML(step.desc)}</div>
          <div class="wiz-step-actions">
            <a class="wiz-step-link" href="${escapeHTML(blocked ? "/pair.html" : step.href)}">${blocked ? "Open pairing (required)" : "Open step"}</a>
            <a class="wiz-step-link" href="#" data-step-index="${idx}">Set current</a>
          </div>
        </div>
        <span class="wiz-step-state ${escapeHTML(s.tone)}">${escapeHTML(s.label)}</span>
      </article>
    `;
  }).join("");

  summaryEl.textContent = `${doneCount}/${WIZ_STEPS.length} steps currently satisfied`;
  stepsEl.querySelectorAll("[data-step-index]").forEach((el) => {
    el.addEventListener("click", (ev) => {
      ev.preventDefault();
      const idx = Number(el.getAttribute("data-step-index"));
      if (Number.isNaN(idx) || idx < 0 || idx >= WIZ_STEPS.length) return;
      _currentStepIdx = idx;
      saveCurrentStep();
      render(_lastStatus);
    });
  });
  renderCurrentStep(status);
  renderRoleFormatEditor();
  renderStylusEditor(status);
}

async function loadStatus() {
  try {
    const [statusResp] = await Promise.all([
      fetch("/api/setup-status", { cache: "no-store" }),
      loadWizardConfig(),
      loadStylusState(),
    ]);
    if (!statusResp.ok) throw new Error(`HTTP ${statusResp.status}`);
    const status = await statusResp.json();
    errEl.hidden = true;
    _lastStatus = status;
    render(status);
  } catch (err) {
    _lastStatus = null;
    render(null);
    errEl.hidden = false;
    errEl.textContent = `Failed to load setup status: ${err.message}`;
  }
}

refreshBtn.addEventListener("click", loadStatus);
roleFormatSaveBtn.addEventListener("click", async () => {
  if (!_wizardConfig) return;
  roleFormatSaveBtn.disabled = true;
  roleFormatErrEl.hidden = true;
  try {
    const r = await fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(_wizardConfig),
    });
    const body = await r.json().catch(() => ({}));
    if (!r.ok || body.ok === false) throw new Error(body.error || body.results?.join(" · ") || `HTTP ${r.status}`);
    await loadStatus();
  } catch (err) {
    roleFormatErrEl.hidden = false;
    roleFormatErrEl.textContent = `Failed to save role/format: ${err.message}`;
  } finally {
    roleFormatSaveBtn.disabled = false;
  }
});
stylusSaveBtn.addEventListener("click", async () => {
  if (!_stylusState) return;
  stylusSaveBtn.disabled = true;
  stylusErrEl.hidden = true;
  try {
    const selectedCatalogID = Number(stylusCatalogEl.value || 0);
    if (!selectedCatalogID) throw new Error("Choose a stylus model.");
    const payload = {
      enabled: true,
      catalog_id: selectedCatalogID,
    };
    if (stylusResetHoursEl.checked) {
      payload.is_new = true;
    } else if (_stylusState?.stylus) {
      payload.is_new = false;
      payload.initial_used_hours = Number(_stylusState.stylus.initial_used_hours || 0);
    }
    const r = await fetch("/api/stylus", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const body = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`);
    await loadStatus();
    stylusResetHoursEl.checked = false;
  } catch (err) {
    stylusErrEl.hidden = false;
    stylusErrEl.textContent = `Failed to save stylus setup: ${err.message}`;
  } finally {
    stylusSaveBtn.disabled = false;
  }
});
prevBtn.addEventListener("click", () => {
  if (_currentStepIdx <= 0) return;
  _currentStepIdx -= 1;
  saveCurrentStep();
  render(_lastStatus);
});
nextBtn.addEventListener("click", () => {
  if (_currentStepIdx >= WIZ_STEPS.length - 1) return;
  const idx = _currentStepIdx + 1;
  const step = WIZ_STEPS[idx];
  if (isStepBlocked(step, _lastStatus)) return;
  _currentStepIdx = idx;
  saveCurrentStep();
  render(_lastStatus);
});
if (!loadStepFromQuery()) loadSavedStep();
render(null);
loadStatus();

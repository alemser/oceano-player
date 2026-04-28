"use strict";

const WIZARD_ACTIVE_STEP_KEY = "oceano.onboarding.amplifier_wizard.active_step";

const WIZ_STEPS = [
  {
    id: "topology",
    title: "Identity and topology",
    desc: "Set amplifier maker/model, input map and connected devices.",
    href: "/amplifier.html",
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
    href: "/pair.html",
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
    href: "/amplifier.html#amp-ir-section",
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
    href: "/amplifier.html#stylus-section",
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

let _currentStepIdx = 0;
let _lastStatus = null;

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
}

async function loadStatus() {
  try {
    const r = await fetch("/api/setup-status", { cache: "no-store" });
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    const status = await r.json();
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

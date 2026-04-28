"use strict";

const HUB_POLL_MS = 30000;

const HUB_WIZARDS = [
  {
    id: "amp",
    title: "Amplifier",
    detail: "Maker/model · Inputs map · Connected devices · Broadlink · IR codes",
    href: "/topology",
    steps: [
      "Define amplifier identity",
      "Map inputs and connected devices",
      "Optionally configure Broadlink and IR",
    ],
    status(s) {
      if (!s) return { cls: "neutral", text: "Loading…" };
      if (!s.amplifier_topology_complete) return { cls: "warn", text: "Not started" };
      if (s.amplifier_ir_enabled && !s.broadlink_paired) return { cls: "warn", text: "IR pending" };
      if (s.amplifier_ir_enabled) return { cls: "ok", text: "Done · IR paired" };
      return { cls: "ok", text: "Done" };
    },
  },
  {
    id: "physical",
    title: "Physical media",
    detail: "Capture · ACRCloud · Input calibration",
    href: "/recognition.html?from=hub",
    steps: [
      "Set capture device and threshold",
      "Add ACRCloud credentials",
      "Validate physical input calibration",
    ],
    status(s) {
      if (!s) return { cls: "neutral", text: "Loading…" };
      const svc = s.services_healthy || {};
      if (svc.oceano_state_manager === false) return { cls: "error", text: "Manager offline" };
      if (!s.capture_configured) return { cls: "warn", text: "Capture pending" };
      if (!s.recognition_credentials_set) return { cls: "warn", text: "Credentials pending" };
      if (s.calibration_physical_recommended) return { cls: "warn", text: "Calibration pending" };
      return { cls: "ok", text: "Done" };
    },
  },
  {
    id: "stylus",
    title: "Stylus tracking",
    detail: "Cartridge profile · Initial hours · Replacement flow",
    href: "/stylus?from=hub",
    steps: [
      "Confirm vinyl topology",
      "Configure stylus profile",
      "Set initial hours and save",
    ],
    status(s) {
      if (!s) return { cls: "neutral", text: "Loading…" };
      if (!s.vinyl_topology_present) return { cls: "neutral", text: "Blocked (no vinyl)" };
      if (s.stylus_profile_configured) return { cls: "ok", text: "Done" };
      return { cls: "warn", text: "Not started" };
    },
  },
  {
    id: "streaming",
    title: "Streaming",
    detail: "AirPlay · Bluetooth health",
    href: "/streaming.html?from=hub",
    steps: [
      "Confirm service health",
      "Validate AirPlay and Bluetooth visibility",
    ],
    status(s) {
      if (!s) return { cls: "neutral", text: "Loading…" };
      const svc = s.services_healthy || {};
      const allUp = Object.keys(svc).length === 0 || Object.values(svc).every((v) => v !== false);
      if (!allUp) return { cls: "warn", text: "Service check needed" };
      return { cls: "ok", text: "Done" };
    },
  },
  {
    id: "display",
    title: "Now playing & display",
    detail: "Now playing page · Kiosk and local panel behavior",
    href: "/display.html?from=hub",
    steps: [
      "Open now playing display settings",
      "Validate kiosk behavior if using HDMI/DSI",
    ],
    status(s) {
      if (!s) return { cls: "neutral", text: "Loading…" };
      const svc = s.services_healthy || {};
      if (svc.oceano_state_manager === false) return { cls: "warn", text: "State feed missing" };
      return { cls: "neutral", text: "Optional" };
    },
  },
];

// ── Helpers ─────────────────────────────────────────────────────────────────

function esc(v) {
  const shared = window.OceanoSetupShared;
  if (shared && typeof shared.escapeHTML === "function") return shared.escapeHTML(v);
  return String(v)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;")
    .replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

function actionLabelFromChip(chip) {
  if (chip.cls === "ok") return "Review";
  if (chip.cls === "warn") return "Continue setup";
  if (chip.cls === "error") return "Fix now";
  return "Open";
}

function renderWizard(wizard, status) {
  const chip = wizard.status(status);
  const expanded = expandedWizardID === wizard.id;
  return `<article class="hub-wizard ${expanded ? "is-open" : ""}">
    <button class="hub-wizard-head" type="button" data-toggle-id="${esc(wizard.id)}" aria-expanded="${expanded ? "true" : "false"}">
      <span class="hub-wizard-title">${esc(wizard.title)}</span>
      <span class="status-chip ${esc(chip.cls)}"><span class="dot"></span>${esc(chip.text)}</span>
      <span class="hub-wizard-chevron" aria-hidden="true">›</span>
    </button>
    <div class="hub-wizard-panel" ${expanded ? "" : "hidden"}>
      <p class="hub-wizard-detail">${esc(wizard.detail)}</p>
      <ul class="hub-wizard-steps">
        ${wizard.steps.map((step) => `<li>${esc(step)}</li>`).join("")}
      </ul>
      <div class="hub-wizard-actions">
        <a class="btn-wizard" href="${esc(wizard.href)}">${esc(actionLabelFromChip(chip))}</a>
      </div>
    </div>
  </article>`;
}

let expandedWizardID = "";

function renderWizards(status) {
  wizardListEl.innerHTML = HUB_WIZARDS.map((w) => renderWizard(w, status)).join("");
  wizardListEl.querySelectorAll("[data-toggle-id]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = btn.getAttribute("data-toggle-id");
      expandedWizardID = expandedWizardID === id ? "" : id;
      renderWizards(lastStatus);
    });
  });
}

// ── Checklist ────────────────────────────────────────────────────────────────

const shared = window.OceanoSetupShared || {};
const ITEMS = typeof shared.checklistItems === "function" ? shared.checklistItems() : [];

function renderChecklist(status) {
  const skips =
    typeof shared.getChecklistSkips === "function" ? shared.getChecklistSkips() : {};

  let doneCount = 0;
  checklistList.innerHTML = ITEMS.map((item) => {
    const done = item.isDone(status);
    const skipped = !done && !!skips[item.id];
    if (done || skipped) doneCount++;
    const chipCls = done ? "done" : skipped ? "skip" : "";
    const chipTxt = done ? "Done" : skipped ? "Skip" : "Pending";
    const skipBtn = item.skippable
      ? `<button class="btn-skip" data-skip-id="${esc(item.id)}">${skipped ? "Undo" : "Skip"}</button>`
      : "";
    return `<li class="hub-cl-item ${done ? "done" : ""}">
      ${esc(item.label)}
      <span class="hub-cl-chip ${chipCls}">${chipTxt}</span>
      <span class="hub-cl-actions">
        <a class="btn-skip" href="${esc(item.href || "/config.html")}">Open</a>
        ${skipBtn}
      </span>
    </li>`;
  }).join("");

  progressEl.textContent = `${doneCount} / ${ITEMS.length} done`;

  checklistList.querySelectorAll("[data-skip-id]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = btn.getAttribute("data-skip-id");
      const s = typeof shared.getChecklistSkips === "function"
        ? shared.getChecklistSkips() : {};
      if (s[id]) delete s[id]; else s[id] = true;
      if (typeof shared.setChecklistSkips === "function") shared.setChecklistSkips(s);
      renderChecklist(lastStatus);
    });
  });
}

// ── Fetch & render ───────────────────────────────────────────────────────────

let lastStatus = null;

async function loadStatus() {
  try {
    const res = await fetch("/api/setup-status", { cache: "no-store" });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    lastStatus = await res.json();
    errorBanner.hidden = true;
    renderWizards(lastStatus);
    renderChecklist(lastStatus);
  } catch (err) {
    errorBanner.hidden = false;
    errorBanner.textContent = `Could not load setup status: ${err.message}`;
    renderWizards(null);
    renderChecklist(null);
  }
}

// ── DOM refs ─────────────────────────────────────────────────────────────────

const wizardListEl    = document.getElementById("hub-wizard-list");
const errorBanner     = document.getElementById("hub-error");
const checklistSection= document.getElementById("onboarding-checklist");
const checklistList   = document.getElementById("checklist-list");
const progressEl      = document.getElementById("checklist-progress");
const refreshBtn      = document.getElementById("refresh-btn");

refreshBtn.addEventListener("click", loadStatus);

// ── Init ─────────────────────────────────────────────────────────────────────

renderWizards(null);
renderChecklist(null);
loadStatus();
setInterval(loadStatus, HUB_POLL_MS);

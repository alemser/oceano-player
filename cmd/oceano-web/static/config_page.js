"use strict";

const HUB_POLL_MS = 5000;
const CHECKLIST_DISMISSED_KEY = "oceano.config.checklist.dismissed";
const CHECKLIST_SKIPS_KEY = "oceano.config.checklist.skips";

const HUB_ICONS = {
  amplifier:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<rect x="3" y="7.5" width="18" height="11" rx="2.3"/>' +
    '<circle cx="8" cy="13" r="1.8"/>' +
    '<path d="M13 11h5M13 14h5"/>' +
    "</svg>",
  calibration:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M4 17V7M12 20V4M20 15V9"/>' +
    '<circle cx="4" cy="10" r="2"/>' +
    '<circle cx="12" cy="14" r="2"/>' +
    '<circle cx="20" cy="12" r="2"/>' +
    "</svg>",
  stylus:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<circle cx="9" cy="13" r="5.2"/>' +
    '<circle cx="9" cy="13" r="1.5" fill="currentColor" stroke="none"/>' +
    '<path d="M14.7 5.8l4 4-5.1 5.1"/>' +
    '<path d="M18.7 9.8l.9 1.9"/>' +
    "</svg>",
  advanced:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M10.3 3.8a1 1 0 0 1 1.4-.2l.6.5a1 1 0 0 0 .9.2l.7-.2a1 1 0 0 1 1.2.8l.1.8a1 1 0 0 0 .6.8l.7.3a1 1 0 0 1 .5 1.3l-.3.7a1 1 0 0 0 .1.9l.5.6a1 1 0 0 1-.1 1.4l-.6.5a1 1 0 0 0-.3.9l.1.8a1 1 0 0 1-.9 1.1l-.8.1a1 1 0 0 0-.8.6l-.3.7a1 1 0 0 1-1.3.4l-.7-.3a1 1 0 0 0-.9.1l-.6.4a1 1 0 0 1-1.4-.2l-.4-.6a1 1 0 0 0-.8-.4h-.8a1 1 0 0 1-1-.9l-.1-.8a1 1 0 0 0-.6-.8l-.7-.3a1 1 0 0 1-.4-1.3l.3-.7a1 1 0 0 0-.1-.9l-.5-.6a1 1 0 0 1 .2-1.4l.6-.4a1 1 0 0 0 .4-.9l-.1-.8a1 1 0 0 1 .9-1.1l.8-.1a1 1 0 0 0 .8-.6l.3-.7z"/>' +
    '<circle cx="12" cy="12" r="2.3"/>' +
    "</svg>",
};

const HUB_CARDS = [
  {
    id: "physical",
    title: "Physical media",
    href: "/recognition.html",
    iconHTML: (window.SOURCE_ICONS && (window.SOURCE_ICONS.Vinyl || window.SOURCE_ICONS.Physical)) || "",
    compute(status) {
      if (!status) return { text: "Loading...", tone: "" };
      if (!status.capture_configured) return { text: "Capture not configured", tone: "warn" };
      if (!status.recognition_credentials_set) return { text: "ACRCloud credentials missing", tone: "warn" };
      return { text: "Capture and recognition ready", tone: "ok" };
    },
  },
  {
    id: "amp",
    title: "Amplifier & IR",
    href: "/amplifier-wizard.html",
    iconHTML: HUB_ICONS.amplifier,
    compute(status) {
      if (!status) return { text: "Loading...", tone: "" };
      if (!status.amplifier_topology_complete) return { text: "Topology not configured", tone: "warn" };
      if (status.amplifier_ir_enabled && !status.broadlink_paired) return { text: "IR enabled but Broadlink not paired", tone: "warn" };
      if (status.amplifier_ir_enabled) return { text: "Topology ready, IR paired", tone: "ok" };
      return { text: "Topology ready (IR optional)", tone: "ok" };
    },
  },
  {
    id: "calibration",
    title: "Recognition & calibration",
    href: "/recognition.html",
    iconHTML: HUB_ICONS.calibration,
    compute(status) {
      if (!status) return { text: "Loading...", tone: "" };
      if (status.calibration_physical_recommended && !status.calibration_physical_complete) {
        return { text: "Calibration recommended for physical inputs", tone: "warn" };
      }
      if (status.calibration_physical_complete) return { text: "Calibration complete", tone: "ok" };
      return { text: "No physical calibration pending", tone: "ok" };
    },
  },
  {
    id: "stylus",
    title: "Stylus tracking",
    href: "/amplifier.html#stylus-section",
    iconHTML: HUB_ICONS.stylus,
    compute(status) {
      if (!status) return { text: "Loading...", tone: "" };
      if (!status.vinyl_topology_present) return { text: "No vinyl topology configured", tone: "" };
      if (status.stylus_profile_configured) return { text: "Stylus profile configured", tone: "ok" };
      if (status.stylus_tracking_recommended) return { text: "Configure stylus profile and rated life", tone: "warn" };
      return { text: "Stylus setup optional", tone: "" };
    },
  },
  {
    id: "streaming",
    title: "Streaming basics",
    href: "/index.html",
    iconHTML: (window.SOURCE_ICONS && window.SOURCE_ICONS.AirPlay) || "🔊",
    compute(status) {
      if (!status) return { text: "Loading...", tone: "" };
      const healthy = status.services_healthy || {};
      const detector = healthy.oceano_source_detector !== false;
      const manager = healthy.oceano_state_manager !== false;
      const web = healthy.oceano_web !== false;
      if (detector && manager && web) return { text: "Core services healthy", tone: "ok" };
      return { text: "One or more services unhealthy", tone: "warn" };
    },
  },
  {
    id: "advanced",
    title: "Advanced",
    href: "/advanced.html",
    iconHTML: HUB_ICONS.advanced,
    compute(status) {
      if (!status) return { text: "Loading...", tone: "" };
      return { text: `Schema v${status.schema_version || 1} · Live setup status API`, tone: "" };
    },
  },
];

const gridEl = document.getElementById("hub-grid");
const errorEl = document.getElementById("hub-error");
const refreshBtn = document.getElementById("refresh-btn");
const checklistEl = document.getElementById("onboarding-checklist");
const checklistListEl = document.getElementById("checklist-list");
const checklistProgressEl = document.getElementById("checklist-progress");
const dismissChecklistBtn = document.getElementById("checklist-dismiss-btn");
const restoreChecklistBtn = document.getElementById("checklist-restore-btn");

const CHECKLIST_ITEMS = [
  {
    id: "foundation",
    label: "Run oceano-setup on the Pi (foundation)",
    href: "/index.html",
    isDone: (s) => !!s && !!s.oceano_setup_acknowledged,
    skippable: true,
  },
  {
    id: "capture",
    label: "Capture device configured",
    href: "/index.html",
    isDone: (s) => !!s && !!s.capture_configured,
  },
  {
    id: "recognition",
    label: "ACRCloud credentials configured",
    href: "/recognition.html",
    isDone: (s) => !!s && !!s.recognition_credentials_set,
  },
  {
    id: "amp",
    label: "Amplifier topology configured (IR optional)",
    href: "/amplifier-wizard.html?step=topology",
    isDone: (s) => !!s && !!s.amplifier_topology_complete,
    skippable: true,
  },
  {
    id: "calibration",
    label: "Physical input calibration complete",
    href: "/amplifier-wizard.html?step=calibration",
    isDone: (s) => !!s && (!!s.calibration_physical_complete || !s.calibration_physical_recommended),
    skippable: true,
  },
  {
    id: "stylus",
    label: "Stylus tracking configured (vinyl path)",
    href: "/amplifier-wizard.html?step=stylus",
    isDone: (s) => !!s && (!!s.stylus_profile_configured || !s.stylus_tracking_recommended),
    skippable: true,
  },
];

function escapeHTML(v) {
  return String(v)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function getChecklistSkips() {
  try {
    const raw = localStorage.getItem(CHECKLIST_SKIPS_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function setChecklistSkips(skips) {
  localStorage.setItem(CHECKLIST_SKIPS_KEY, JSON.stringify(skips));
}

function isChecklistDismissed() {
  return localStorage.getItem(CHECKLIST_DISMISSED_KEY) === "1";
}

function setChecklistDismissed(v) {
  if (v) localStorage.setItem(CHECKLIST_DISMISSED_KEY, "1");
  else localStorage.removeItem(CHECKLIST_DISMISSED_KEY);
}

function renderCards(status) {
  gridEl.innerHTML = HUB_CARDS.map((card) => {
    const s = card.compute(status);
    const icon = card.iconHTML.trim().startsWith("<svg")
      ? `<span class="hub-card-icon" aria-hidden="true">${card.iconHTML}</span>`
      : `<span class="hub-card-icon" aria-hidden="true">${escapeHTML(card.iconHTML)}</span>`;
    return `
      <article class="hub-card">
        ${icon}
        <div class="hub-card-title">${escapeHTML(card.title)}</div>
        <div class="hub-card-status ${escapeHTML(s.tone)}">${escapeHTML(s.text)}</div>
        <div class="hub-card-actions">
          <a class="hub-card-link" href="${escapeHTML(card.href)}">Open</a>
        </div>
      </article>
    `;
  }).join("");
}

function renderChecklist(status) {
  const dismissed = isChecklistDismissed();
  const skips = getChecklistSkips();
  if (dismissed) {
    checklistListEl.innerHTML = "";
    checklistProgressEl.textContent = "Checklist hidden";
    dismissChecklistBtn.hidden = true;
    restoreChecklistBtn.hidden = false;
    checklistEl.classList.add("is-dismissed");
    return;
  }

  dismissChecklistBtn.hidden = false;
  restoreChecklistBtn.hidden = true;
  checklistEl.classList.remove("is-dismissed");

  let doneCount = 0;
  checklistListEl.innerHTML = CHECKLIST_ITEMS.map((item) => {
    const done = item.isDone(status);
    const skipped = !!skips[item.id];
    if (done || skipped) doneCount += 1;
    const stateClass = done ? "done" : "";
    const chipClass = done ? "done" : skipped ? "skip" : "";
    const chipText = done ? "Done" : skipped ? "Skip" : "Pending";
    const skipBtn = item.skippable
      ? `<button class="checklist-skip-btn" type="button" data-skip-id="${escapeHTML(item.id)}">${skipped ? "Undo skip" : "Skip"}</button>`
      : "";
    return `
      <li class="checklist-item ${stateClass}">
        <span class="checklist-item-label">
          <span>${escapeHTML(item.label)}</span>
          <span class="checklist-chip ${chipClass}">${chipText}</span>
        </span>
        <a class="hub-card-link" href="${escapeHTML(item.href || "/config.html")}">Open</a>
        ${skipBtn}
      </li>
    `;
  }).join("");

  checklistProgressEl.textContent = `${doneCount}/${CHECKLIST_ITEMS.length} steps marked done`;
  checklistListEl.querySelectorAll("[data-skip-id]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const id = btn.getAttribute("data-skip-id");
      const next = getChecklistSkips();
      next[id] = !next[id];
      if (!next[id]) delete next[id];
      setChecklistSkips(next);
      renderChecklist(status);
    });
  });
}

async function loadSetupStatus() {
  try {
    const res = await fetch("/api/setup-status", { cache: "no-store" });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const status = await res.json();
    errorEl.hidden = true;
    errorEl.textContent = "";
    renderCards(status);
    renderChecklist(status);
  } catch (err) {
    renderCards(null);
    renderChecklist(null);
    errorEl.hidden = false;
    errorEl.textContent = `Failed to load setup status: ${err.message}`;
  }
}

refreshBtn.addEventListener("click", loadSetupStatus);
dismissChecklistBtn.addEventListener("click", () => {
  setChecklistDismissed(true);
  renderChecklist(null);
});
restoreChecklistBtn.addEventListener("click", () => {
  setChecklistDismissed(false);
  loadSetupStatus();
});
renderCards(null);
renderChecklist(null);
loadSetupStatus();
setInterval(loadSetupStatus, HUB_POLL_MS);

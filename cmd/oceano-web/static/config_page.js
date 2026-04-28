"use strict";

const HUB_POLL_MS = 5000;
const CHECKLIST_DISMISSED_KEY = "oceano.config.checklist.dismissed";
const CHECKLIST_SKIPS_KEY = "oceano.config.checklist.skips";

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
    href: "/amplifier.html",
    iconHTML: "🎛️",
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
    iconHTML: "📊",
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
    iconHTML: "💿",
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
    iconHTML: "⚙️",
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
    isDone: (s) => !!s && !!s.oceano_setup_acknowledged,
    skippable: true,
  },
  {
    id: "capture",
    label: "Capture device configured",
    isDone: (s) => !!s && !!s.capture_configured,
  },
  {
    id: "recognition",
    label: "ACRCloud credentials configured",
    isDone: (s) => !!s && !!s.recognition_credentials_set,
  },
  {
    id: "amp",
    label: "Amplifier topology configured (IR optional)",
    isDone: (s) => !!s && !!s.amplifier_topology_complete,
    skippable: true,
  },
  {
    id: "calibration",
    label: "Physical input calibration complete",
    isDone: (s) => !!s && (!!s.calibration_physical_complete || !s.calibration_physical_recommended),
    skippable: true,
  },
  {
    id: "stylus",
    label: "Stylus tracking configured (vinyl path)",
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

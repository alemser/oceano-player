"use strict";

const HUB_POLL_MS = 30000;

// ── Card definitions ────────────────────────────────────────────────────────
// icon()   → HTML string from SOURCE_ICONS / HUB_ICONS (loaded by icons.js)
// chip(s)  → { cls: 'ok'|'warn'|'error'|'neutral', text: string }
// detail   → static sub-title shown below the chip

const HUB_CARDS = [
  {
    id: "physical",
    title: "Physical media",
    detail: "Capture · ACRCloud · Calibration · Stylus",
    href: "/recognition.html",
    icon() { return (window.SOURCE_ICONS || {}).Vinyl || (window.HUB_ICONS || {}).Advanced || ""; },
    chip(s) {
      if (!s) return { cls: "neutral", text: "Loading…" };
      const svc = s.services_healthy || {};
      if (svc.oceano_state_manager === false)
        return { cls: "error", text: "State manager offline" };
      if (!s.capture_configured)
        return { cls: "warn", text: "Capture not configured" };
      if (!s.recognition_credentials_set)
        return { cls: "warn", text: "ACRCloud credentials missing" };
      if (s.calibration_physical_recommended)
        return { cls: "warn", text: "Calibration incomplete" };
      if (s.stylus_tracking_recommended)
        return { cls: "warn", text: "Stylus not configured" };
      return { cls: "ok", text: "Ready" };
    },
  },
  {
    id: "amp",
    title: "Amplifier & IR",
    detail: "Inputs · Broadlink · IR codes",
    href: "/amplifier.html",
    icon() { return (window.HUB_ICONS || {}).Amplifier || ""; },
    chip(s) {
      if (!s) return { cls: "neutral", text: "Loading…" };
      if (!s.amplifier_topology_complete)
        return { cls: "warn", text: "Not configured" };
      if (s.amplifier_ir_enabled && !s.broadlink_paired)
        return { cls: "warn", text: "Broadlink not paired" };
      if (s.amplifier_ir_enabled)
        return { cls: "ok", text: "Ready · IR paired" };
      return { cls: "ok", text: "Ready" };
    },
  },
  {
    id: "stylus",
    title: "Stylus tracking",
    detail: "Hours · Cartridge life",
    href: "/amplifier.html",
    icon() { return (window.HUB_ICONS || {}).Stylus || ""; },
    chip(s) {
      if (!s) return { cls: "neutral", text: "Loading…" };
      if (!s.vinyl_topology_present)
        return { cls: "neutral", text: "No vinyl topology" };
      if (s.stylus_profile_configured)
        return { cls: "ok", text: "Configured" };
      return { cls: "warn", text: "Not configured" };
    },
  },
  {
    id: "streaming",
    title: "Streaming",
    detail: "AirPlay · Bluetooth",
    href: "/index.html",
    icon() { return (window.SOURCE_ICONS || {}).AirPlay || ""; },
    chip(s) {
      if (!s) return { cls: "neutral", text: "Loading…" };
      const svc = s.services_healthy || {};
      const allUp = Object.keys(svc).length === 0 ||
        Object.values(svc).every((v) => v !== false);
      if (!allUp) return { cls: "warn", text: "Service offline" };
      return { cls: "ok", text: "Ready" };
    },
  },
  {
    id: "display",
    title: "Now playing & display",
    detail: "Kiosk · Weather · Idle screen",
    href: "/display.html",
    icon() { return (window.HUB_ICONS || {}).Display || ""; },
    chip(s) {
      if (!s) return { cls: "neutral", text: "Loading…" };
      const svc = s.services_healthy || {};
      if (svc.oceano_state_manager === false)
        return { cls: "warn", text: "No state feed" };
      return { cls: "neutral", text: "Optional" };
    },
  },
  {
    id: "advanced",
    title: "Advanced",
    detail: "Sockets · Paths · Library DB",
    href: "/advanced.html",
    icon() { return (window.HUB_ICONS || {}).Advanced || ""; },
    chip() { return { cls: "neutral", text: "Optional" }; },
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

function renderCard(card, status) {
  const chip = card.chip(status);
  const iconHtml = card.icon();
  const borderCls =
    chip.cls === "warn" ? "border-warn" :
    chip.cls === "error" ? "border-error" : "";
  return `<a class="hub-card ${borderCls}" href="${esc(card.href)}">
    <div class="hub-card-top">
      <span class="hub-card-icon" aria-hidden="true">${iconHtml}</span>
      <span class="hub-card-arrow" aria-hidden="true">→</span>
    </div>
    <h2 class="hub-card-title">${esc(card.title)}</h2>
    <span class="status-chip ${esc(chip.cls)}"><span class="dot"></span>${esc(chip.text)}</span>
    <p class="hub-card-detail">${esc(card.detail)}</p>
  </a>`;
}

// ── Checklist ────────────────────────────────────────────────────────────────

const shared = window.OceanoSetupShared || {};
const ITEMS = typeof shared.checklistItems === "function" ? shared.checklistItems() : [];

function renderChecklist(status) {
  const dismissed =
    typeof shared.isChecklistDismissed === "function" && shared.isChecklistDismissed();

  checklistSection.hidden = dismissed;
  restoreRow.hidden = !dismissed;
  if (dismissed) return;

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
    gridEl.innerHTML = HUB_CARDS.map((c) => renderCard(c, lastStatus)).join("");
    renderChecklist(lastStatus);
  } catch (err) {
    errorBanner.hidden = false;
    errorBanner.textContent = `Could not load setup status: ${err.message}`;
    gridEl.innerHTML = HUB_CARDS.map((c) => renderCard(c, null)).join("");
    renderChecklist(null);
  }
}

// ── DOM refs ─────────────────────────────────────────────────────────────────

const gridEl          = document.getElementById("hub-grid");
const errorBanner     = document.getElementById("hub-error");
const checklistSection= document.getElementById("onboarding-checklist");
const checklistList   = document.getElementById("checklist-list");
const progressEl      = document.getElementById("checklist-progress");
const dismissBtn      = document.getElementById("checklist-dismiss-btn");
const restoreRow      = document.getElementById("checklist-restore-row");
const restoreBtn      = document.getElementById("checklist-restore-btn");
const refreshBtn      = document.getElementById("refresh-btn");

dismissBtn.addEventListener("click", () => {
  if (typeof shared.setChecklistDismissed === "function") shared.setChecklistDismissed(true);
  renderChecklist(lastStatus);
});

restoreBtn.addEventListener("click", () => {
  if (typeof shared.setChecklistDismissed === "function") shared.setChecklistDismissed(false);
  renderChecklist(lastStatus);
});

refreshBtn.addEventListener("click", loadStatus);

// ── Init ─────────────────────────────────────────────────────────────────────

gridEl.innerHTML = HUB_CARDS.map((c) => renderCard(c, null)).join("");
renderChecklist(null);
loadStatus();
setInterval(loadStatus, HUB_POLL_MS);

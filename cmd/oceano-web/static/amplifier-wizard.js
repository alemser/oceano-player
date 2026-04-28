"use strict";

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

function escapeHTML(v) {
  return String(v)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function render(status) {
  let doneCount = 0;
  stepsEl.innerHTML = WIZ_STEPS.map((step, idx) => {
    const s = step.state(status);
    const done = s.tone === "done";
    if (done) doneCount += 1;
    return `
      <article class="wiz-step ${done ? "done" : ""}">
        <span class="wiz-step-index">${idx + 1}</span>
        <div>
          <div class="wiz-step-title">${escapeHTML(step.title)}</div>
          <div class="wiz-step-desc">${escapeHTML(step.desc)}</div>
          <div class="wiz-step-actions"><a class="wiz-step-link" href="${escapeHTML(step.href)}">Open step</a></div>
        </div>
        <span class="wiz-step-state ${escapeHTML(s.tone)}">${escapeHTML(s.label)}</span>
      </article>
    `;
  }).join("");

  summaryEl.textContent = `${doneCount}/${WIZ_STEPS.length} steps currently satisfied`;
}

async function loadStatus() {
  try {
    const r = await fetch("/api/setup-status", { cache: "no-store" });
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    const status = await r.json();
    errEl.hidden = true;
    render(status);
  } catch (err) {
    render(null);
    errEl.hidden = false;
    errEl.textContent = `Failed to load setup status: ${err.message}`;
  }
}

refreshBtn.addEventListener("click", loadStatus);
render(null);
loadStatus();

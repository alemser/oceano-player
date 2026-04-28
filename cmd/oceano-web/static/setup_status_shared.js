"use strict";

(function setupStatusSharedScope() {
  const CHECKLIST_DISMISSED_KEY = "oceano.config.checklist.dismissed";
  const CHECKLIST_SKIPS_KEY = "oceano.config.checklist.skips";

  function checklistItems() {
    return [
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
        href: "/recognition.html?from=hub",
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
        href: "/stylus?from=hub",
        isDone: (s) => !!s && (!!s.stylus_profile_configured || !s.stylus_tracking_recommended),
        skippable: true,
      },
    ];
  }

  function bridgeItems(status) {
    const items = checklistItems();
    const wantedIDs = ["capture", "recognition", "amp", "calibration", "stylus"];
    return wantedIDs.map((id) => {
      const item = items.find((entry) => entry.id === id);
      return {
        id: item.id,
        label: item.label,
        done: item.isDone(status),
      };
    });
  }

  function escapeHTML(value) {
    return String(value)
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

  function setChecklistDismissed(value) {
    if (value) localStorage.setItem(CHECKLIST_DISMISSED_KEY, "1");
    else localStorage.removeItem(CHECKLIST_DISMISSED_KEY);
  }

  window.OceanoSetupShared = {
    bridgeItems,
    checklistItems,
    escapeHTML,
    getChecklistSkips,
    isChecklistDismissed,
    setChecklistDismissed,
    setChecklistSkips,
  };
})();

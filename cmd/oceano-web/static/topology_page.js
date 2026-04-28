"use strict";

function toast(msg, isError) {
  const el = document.getElementById("toast");
  if (!el) return;
  el.textContent = msg;
  el.className = isError ? "toast-error" : "toast-ok";
  el.style.opacity = "1";
  clearTimeout(el._t);
  el._t = setTimeout(() => { el.style.opacity = "0"; }, 3500);
}

function setField(id, value) {
  const el = document.getElementById(id);
  if (!el) return;
  el.value = value ?? "";
}

function fieldValue(id) {
  return (document.getElementById(id)?.value || "").trim();
}

function applyTopologyContext() {
  const params = new URLSearchParams(window.location.search || "");
  const fromWizard = params.get("from") === "wizard";
  const titleEl = document.getElementById("topology-context-title");
  const copyEl = document.getElementById("topology-context-copy");
  const backEl = document.getElementById("topology-back-link");
  const irEl = document.getElementById("topology-ir-link");
  const nextEl = document.getElementById("topology-next-step-link");
  const profileSectionEl = document.getElementById("topology-profile-section");

  if (fromWizard) {
    if (titleEl) titleEl.textContent = "Topology for onboarding";
    if (copyEl) copyEl.textContent = "Set maker/model, input map, and connected devices before moving to IR and calibration steps.";
    if (backEl) {
      backEl.href = "/amplifier-wizard?step=topology";
      backEl.innerHTML = backEl.innerHTML.replace("Back to wizard", "Back to wizard");
    }
    if (irEl) irEl.href = "/ir-setup?from=wizard";
    if (nextEl) {
      nextEl.style.display = "";
      nextEl.href = "/amplifier-wizard?step=pairing";
    }
    if (profileSectionEl) profileSectionEl.style.display = "none";
    return;
  }

  if (titleEl) titleEl.textContent = "Topology configuration";
  if (copyEl) copyEl.textContent = "Configure amplifier identity, input map, and connected device classification.";
  if (backEl) {
    backEl.href = "/config";
    backEl.innerHTML = backEl.innerHTML.replace("Back to wizard", "Back");
  }
  if (irEl) irEl.href = "/ir-setup";
  if (nextEl) nextEl.style.display = "none";
  if (profileSectionEl) profileSectionEl.style.display = "";
}

function updateTopologyProfileSummary() {
  const hintEl = document.getElementById("topology-profile-current");
  const selectEl = document.getElementById("amp-profile-select");
  if (!hintEl || !selectEl) return;
  const selectedText = selectEl.options?.[selectEl.selectedIndex]?.textContent?.trim();
  hintEl.textContent = selectedText ? `Current selection: ${selectedText}` : "No profile selected.";
}

async function loadTopologyProfiles(cfg) {
  if (typeof loadAmplifierProfiles !== "function") return;
  await loadAmplifierProfiles(cfg);
  if (typeof _refreshCloneBuiltInButton === "function") _refreshCloneBuiltInButton();
  updateTopologyProfileSummary();
}

async function topologyActivateSelectedProfile() {
  const select = document.getElementById("amp-profile-select");
  const profileID = select?.value || "";
  if (!profileID) {
    toast("Select a profile first.", true);
    return;
  }

  const btn = document.getElementById("topology-profile-activate-btn");
  if (btn) { btn.disabled = true; btn.textContent = "Activating…"; }

  try {
    const r = await fetch("/api/amplifier/profiles/activate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ profile_id: profileID }),
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) {
      toast(data?.error || "Failed to activate profile.", true);
      return;
    }
    toast("Profile activated.");
    await loadTopologyPage();
  } catch {
    toast("Failed to activate profile.", true);
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = "Activate selected profile"; }
  }
}

async function loadTopologyPage() {
  let cfg;
  try {
    const r = await fetch("/api/config", { cache: "no-store" });
    if (!r.ok) {
      toast("Failed to load configuration.", true);
      return;
    }
    cfg = await r.json();
  } catch {
    toast("Failed to load configuration.", true);
    return;
  }

  const amp = cfg.amplifier ?? {};
  _ampConfig = amp;
  _ampLastKnownInputID = String(cfg.amplifier_runtime?.last_known_input_id ?? "");

  setField("amp-maker", amp.maker ?? "");
  setField("amp-model", amp.model ?? "");
  setField("amp-input-mode", amp.input_mode ?? "cycle");

  setAmplifierInputsModel(amp.inputs ?? []);
  setConnectedDevicesModel(amp.connected_devices ?? []);
  await loadTopologyProfiles(cfg);
}

async function saveTopologyPage() {
  const btn = document.getElementById("btn-topology-save");
  if (btn) { btn.disabled = true; btn.textContent = "Saving…"; }

  let fullCfg;
  try {
    const r = await fetch("/api/config", { cache: "no-store" });
    if (!r.ok) throw new Error("load failed");
    fullCfg = await r.json();
  } catch {
    toast("Failed to load current config before saving.", true);
    if (btn) { btn.disabled = false; btn.textContent = "Save & Restart Services"; }
    return;
  }

  const inputs = (typeof collectAmplifierInputsFromUI === "function")
    ? collectAmplifierInputsFromUI()
    : (_ampConfig.inputs ?? []);
  const connectedDevices = (typeof collectConnectedDevicesFromUI === "function")
    ? collectConnectedDevicesFromUI()
    : (_ampConfig.connected_devices ?? []);

  fullCfg.amplifier = {
    ...(_ampConfig),
    enabled: _ampConfig.enabled ?? fullCfg.amplifier?.enabled ?? false,
    profile_id: _ampConfig.profile_id || fullCfg.amplifier?.profile_id || "",
    input_mode: fieldValue("amp-input-mode") || _ampConfig.input_mode || "cycle",
    maker: fieldValue("amp-maker") || _ampConfig.maker || "",
    model: fieldValue("amp-model") || _ampConfig.model || "",
    inputs,
    connected_devices: connectedDevices,
    usb_reset: _ampConfig.usb_reset ?? {},
    broadlink: _ampConfig.broadlink ?? {},
    ir_codes: _ampConfig.ir_codes ?? {},
  };

  try {
    const r = await fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(fullCfg),
    });
    const res = await r.json().catch(() => ({}));
    const isError = !r.ok || res.ok === false;
    toast(res.results?.join(" · ") || (isError ? "Save failed" : "Saved & services restarted"), isError);
    if (!isError) _ampConfig = fullCfg.amplifier;
  } catch (err) {
    toast("Save failed: " + err.message, true);
  }

  if (btn) { btn.disabled = false; btn.textContent = "Save & Restart Services"; }
}

document.addEventListener("DOMContentLoaded", () => {
  applyTopologyContext();
  document.getElementById("btn-topology-save")?.addEventListener("click", saveTopologyPage);
  document.getElementById("topology-profile-activate-btn")?.addEventListener("click", topologyActivateSelectedProfile);
  document.getElementById("amp-profile-select")?.addEventListener("change", updateTopologyProfileSummary);
  loadTopologyPage();
});

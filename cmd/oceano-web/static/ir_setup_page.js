"use strict";

const irSetupState = {
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

function setField(id, value) {
  const el = document.getElementById(id);
  if (!el) return;
  el.value = value ?? "";
}

function fieldValue(id) {
  return (document.getElementById(id)?.value || "").trim();
}

function refreshDirectIRWarning() {
  const warning = document.getElementById("amp-direct-warning");
  if (!warning) return;
  const mode = document.getElementById("amp-input-mode")?.value || _ampConfig.input_mode || "cycle";
  if (mode !== "direct") {
    warning.style.display = "none";
    warning.textContent = "";
    return;
  }
  const irCodes = _ampConfig.ir_codes || {};
  const missing = _ampInputsModel
    .filter((input) => !irCodes[_ampInputCommandID(input.id)])
    .map((input, idx) => (input.logical_name || `Input ${idx + 1}`).trim());
  if (missing.length === 0) {
    warning.style.display = "none";
    warning.textContent = "";
    return;
  }
  warning.style.display = "";
  warning.textContent = `Direct mode is active. Missing IR mapping for ${missing.length} input(s): ${missing.join(", ")}.`;
}

function renderConnectedRemoteIR() {
  const wrap = document.getElementById("ir-device-list");
  if (!wrap) return;
  wrap.innerHTML = "";

  const remotes = (_ampConnectedDevices || []).filter((d) => !!d.has_remote);
  if (!remotes.length) {
    wrap.innerHTML = '<p class="hint" style="margin:0">No remote-enabled devices. Mark "Has remote" in topology.</p>';
    return;
  }

  for (const dev of remotes) {
    const block = document.createElement("div");
    block.className = "ir-device-block";
    block.innerHTML = `
      <div class="ir-device-title">${dev.name || "Unnamed device"}</div>
      <div class="ir-device-sub">${(dev.input_ids || []).length ? `${dev.input_ids.length} mapped input(s)` : "No mapped inputs"}</div>
      <div id="device-ir-table-${dev.id}"></div>
    `;
    wrap.appendChild(block);
    renderIRTable(`device-ir-table-${dev.id}`, DEVICE_REMOTE_COMMANDS, `device-${dev.id}`, dev.ir_codes ?? {});
  }
}

function applyAvailability(canEdit, reason) {
  document.getElementById("btn-ir-save").disabled = !canEdit;
  document.getElementById("amp-broadlink-host").disabled = !canEdit;
  const msg = document.getElementById("ir-disabled-msg");
  if (msg) {
    msg.style.display = canEdit ? "none" : "";
    msg.textContent = reason || "Finish topology first to configure IR.";
  }
}

async function loadIRSetupPage() {
  let cfg;
  let setupStatus;
  try {
    const [cfgRes, statusRes] = await Promise.all([
      fetch("/api/config", { cache: "no-store" }),
      fetch("/api/setup-status", { cache: "no-store" }),
    ]);
    if (!cfgRes.ok) throw new Error("config");
    cfg = await cfgRes.json();
    setupStatus = statusRes.ok ? await statusRes.json() : null;
  } catch {
    toast("Failed to load IR setup.", true);
    return;
  }

  const amp = cfg.amplifier ?? {};
  _ampConfig = amp;
  setAmplifierInputsModel(amp.inputs ?? []);
  setConnectedDevicesModel(amp.connected_devices ?? []);
  setField("amp-broadlink-host", amp.broadlink?.host ?? "");
  setField("amp-token", amp.broadlink?.token ?? "");
  setField("amp-input-mode", amp.input_mode ?? "cycle");
  updateAmpIRSummary(amp.ir_codes ?? {});
  refreshDirectIRWarning();
  renderConnectedRemoteIR();

  const canEdit = !!setupStatus?.amplifier_topology_complete;
  irSetupState.canEdit = canEdit;
  applyAvailability(
    canEdit,
    "Complete topology (maker/model + inputs) before configuring Broadlink and IR commands."
  );
}

async function saveIRSetupPage() {
  if (!irSetupState.canEdit) {
    toast("Complete topology first.", true);
    return;
  }

  const btn = document.getElementById("btn-ir-save");
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

  fullCfg.amplifier = {
    ...(_ampConfig),
    enabled: _ampConfig.enabled ?? fullCfg.amplifier?.enabled ?? false,
    maker: _ampConfig.maker ?? fullCfg.amplifier?.maker ?? "",
    model: _ampConfig.model ?? fullCfg.amplifier?.model ?? "",
    input_mode: _ampConfig.input_mode ?? fullCfg.amplifier?.input_mode ?? "cycle",
    inputs: _ampConfig.inputs ?? fullCfg.amplifier?.inputs ?? [],
    connected_devices: collectConnectedDevicesFromUI(),
    ir_codes: _ampConfig.ir_codes ?? fullCfg.amplifier?.ir_codes ?? {},
    broadlink: {
      ...(_ampConfig.broadlink ?? {}),
      host: fieldValue("amp-broadlink-host"),
      token: _ampConfig.broadlink?.token ?? fullCfg.amplifier?.broadlink?.token ?? "",
    },
    usb_reset: _ampConfig.usb_reset ?? fullCfg.amplifier?.usb_reset ?? {},
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
  document.getElementById("btn-ir-save")?.addEventListener("click", saveIRSetupPage);
  loadIRSetupPage();
});

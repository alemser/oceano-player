// amplifier_page.js — Bootstrap for /amplifier.html standalone page.
// index.amplifier.js is loaded before this and declares all shared globals
// (_ampConfig, _ampProfiles, _ampInputsModel) and all UI functions.

// ── Toast ─────────────────────────────────────────────────────────────────────

function toast(msg, isError) {
  const el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.className = isError ? 'toast-error' : 'toast-ok';
  el.style.opacity = '1';
  clearTimeout(el._t);
  el._t = setTimeout(() => { el.style.opacity = '0'; }, 3500);
}

// ── Field helpers ─────────────────────────────────────────────────────────────

function _ampPageSet(id, value) {
  const el = document.getElementById(id);
  if (!el) return;
  if (el.type === 'checkbox') { el.checked = !!value; }
  else { el.value = (value ?? ''); }
}

function _ampPageVal(id) {
  return (document.getElementById(id)?.value || '').trim();
}

function _ampPageIntOr(id, fallback) {
  const n = parseInt(_ampPageVal(id), 10);
  return Number.isNaN(n) ? fallback : n;
}

// ── Load ──────────────────────────────────────────────────────────────────────

async function loadAmplifierPage() {
  let cfg;
  try {
    const r = await fetch('/api/config');
    if (!r.ok) { toast('Failed to load configuration.', true); return; }
    cfg = await r.json();
  } catch {
    toast('Failed to load configuration.', true);
    return;
  }

  const amp = cfg.amplifier ?? {};

  _ampConfig = amp;
  _ampLastKnownInputID = String(cfg.amplifier_runtime?.last_known_input_id ?? '');

  // Amplifier fields
  _ampPageSet('amp-maker',  amp.maker  ?? '');
  _ampPageSet('amp-model',  amp.model  ?? '');
  _ampPageSet('amp-input-mode', amp.input_mode ?? 'cycle');
  _ampPageSet('amp-usb-reset-max-attempts',  amp.usb_reset?.max_attempts       ?? 13);
  _ampPageSet('amp-usb-reset-first-step-ms', amp.usb_reset?.first_step_settle_ms ?? 150);
  _ampPageSet('amp-usb-reset-step-wait-ms',  amp.usb_reset?.step_wait_ms       ?? 2400);
  _ampPageSet('amp-broadlink-host', amp.broadlink?.host  ?? '');
  _ampPageSet('amp-token',          amp.broadlink?.token ?? '');

  // Inputs model and IR tables (functions from index.amplifier.js)
  setAmplifierInputsModel(amp.inputs ?? []);
  setConnectedDevicesModel(amp.connected_devices ?? []);
  updateAmpIRSummary(amp.ir_codes ?? {});
  _refreshDirectIRWarning();

  // Profiles
  await loadAmplifierProfiles(cfg);
}

// ── Save ──────────────────────────────────────────────────────────────────────

async function saveAmplifierPage() {
  const btn = document.getElementById('btn-amp-page-save');
  if (btn) { btn.disabled = true; btn.textContent = 'Saving…'; }

  // Re-load the full config first so we don't clobber unrelated sections.
  let fullCfg;
  try {
    const r = await fetch('/api/config');
    if (!r.ok) throw new Error('load failed');
    fullCfg = await r.json();
  } catch {
    toast('Failed to load current config before saving.', true);
    if (btn) { btn.disabled = false; btn.textContent = 'Save & Restart Services'; }
    return;
  }

  const inputs = (typeof collectAmplifierInputsFromUI === 'function')
    ? collectAmplifierInputsFromUI()
    : (_ampConfig.inputs ?? []);

  const connectedDevices = (typeof collectConnectedDevicesFromUI === 'function')
    ? collectConnectedDevicesFromUI()
    : (_ampConfig.connected_devices ?? []);

  fullCfg.amplifier = {
    ...(_ampConfig),
    enabled:    _ampConfig.enabled ?? fullCfg.amplifier?.enabled ?? false,
    profile_id: _ampPageVal('amp-profile-select') || _ampConfig.profile_id || '',
    input_mode: _ampPageVal('amp-input-mode')     || _ampConfig.input_mode || 'cycle',
    maker:      _ampPageVal('amp-maker')           || _ampConfig.maker     || '',
    model:      _ampPageVal('amp-model')           || _ampConfig.model     || '',
    inputs,
    connected_devices: connectedDevices,
    usb_reset: {
      ...(_ampConfig.usb_reset ?? {}),
      max_attempts:         _ampPageIntOr('amp-usb-reset-max-attempts',  13),
      first_step_settle_ms: _ampPageIntOr('amp-usb-reset-first-step-ms', 150),
      step_wait_ms:         _ampPageIntOr('amp-usb-reset-step-wait-ms',  2400),
    },
    broadlink: {
      ...(_ampConfig.broadlink ?? {}),
      host: _ampPageVal('amp-broadlink-host') || _ampConfig.broadlink?.host || '',
    },
  };

  try {
    const r = await fetch('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(fullCfg),
    });
    const res = await r.json().catch(() => ({}));
    const isError = !r.ok || res.ok === false;
    toast(res.results?.join(' · ') || (isError ? 'Save failed' : 'Saved & services restarted'), isError);
    if (!isError) {
      // Refresh in-memory config from the saved state
      _ampConfig = fullCfg.amplifier;
    }
  } catch (err) {
    toast('Save failed: ' + err.message, true);
  }

  if (btn) { btn.disabled = false; btn.textContent = 'Save & Restart Services'; }
}

// ── Init ──────────────────────────────────────────────────────────────────────

document.addEventListener('DOMContentLoaded', loadAmplifierPage);

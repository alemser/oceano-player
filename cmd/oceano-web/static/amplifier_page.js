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

const _stylusPageState = {
  catalog: [],
  stylus: null,
  enabled: false,
  metrics: null,
  listenersBound: false,
};

function _stylusNum(v, digits = 2) {
  const n = Number(v || 0);
  return n.toFixed(digits).replace(/\.00$/, '');
}

function _stylusSetStateBadge(state) {
  const el = document.getElementById('stylus-state-badge');
  if (!el) return;
  const raw = String(state || 'healthy').toLowerCase();
  const textMap = { healthy: 'HEALTHY', plan: 'PLAN', soon: 'SOON', overdue: 'OVERDUE' };
  el.textContent = textMap[raw] || 'HEALTHY';
  let color = 'var(--fg)';
  if (raw === 'plan') color = '#f6c945';
  if (raw === 'soon') color = '#f39b47';
  if (raw === 'overdue') color = '#e65c5c';
  el.style.color = color;
  el.style.borderColor = color;
}

function _stylusApplyAmplifierDependency(isAmplifierEnabled) {
  const ids = [
    'stylus-enabled',
    'stylus-mode',
    'stylus-catalog-select',
    'stylus-custom-brand',
    'stylus-custom-model',
    'stylus-custom-profile',
    'stylus-custom-lifetime',
    'stylus-is-new',
    'stylus-initial-hours',
  ];
  ids.forEach((id) => {
    const el = document.getElementById(id);
    if (!el) return;
    el.disabled = !isAmplifierEnabled;
  });
  const msg = document.getElementById('stylus-disabled-msg');
  if (msg) msg.style.display = isAmplifierEnabled ? 'none' : '';
}

function _stylusSyncModeFields() {
  const mode = document.getElementById('stylus-mode')?.value || 'catalog';
  const showCatalog = mode === 'catalog';
  const catalogField = document.getElementById('stylus-catalog-field');
  if (catalogField) catalogField.style.display = showCatalog ? '' : 'none';

  const customIds = [
    'stylus-custom-brand-field',
    'stylus-custom-model-field',
    'stylus-custom-profile-field',
    'stylus-custom-lifetime-field',
  ];
  customIds.forEach((id) => {
    const el = document.getElementById(id);
    if (el) el.style.display = showCatalog ? 'none' : '';
  });
}

function _stylusSyncInitialHoursField() {
  const isNew = document.getElementById('stylus-is-new')?.checked;
  const field = document.getElementById('stylus-initial-hours-field');
  if (!field) return;
  field.style.display = isNew ? 'none' : '';
}

function _stylusBindListenersOnce() {
  if (_stylusPageState.listenersBound) return;
  _stylusPageState.listenersBound = true;

  document.getElementById('stylus-mode')?.addEventListener('change', _stylusSyncModeFields);
  document.getElementById('stylus-is-new')?.addEventListener('change', _stylusSyncInitialHoursField);
}

function _stylusRenderCatalog(items) {
  const sel = document.getElementById('stylus-catalog-select');
  if (!sel) return;
  sel.innerHTML = '';
  for (const it of items || []) {
    const opt = document.createElement('option');
    opt.value = String(it.id);
    const hours = Number(it.recommended_hours || 0);
    opt.textContent = `${it.brand} ${it.model} (${it.stylus_profile}, ${hours}h)`;
    sel.appendChild(opt);
  }
}

function _stylusRenderMetrics(metrics) {
  const m = metrics || {};
  const setText = (id, value) => {
    const el = document.getElementById(id);
    if (el) el.textContent = value;
  };
  setText('stylus-m-vinyl-hours', `${_stylusNum(m.vinyl_hours_since_install)} h`);
  setText('stylus-m-total-hours', `${_stylusNum(m.stylus_hours_total)} h`);
  setText('stylus-m-remaining-hours', `${_stylusNum(m.remaining_hours)} h`);
  setText('stylus-m-wear', `${_stylusNum(m.wear_percent)}%`);
  _stylusSetStateBadge(m.state || 'healthy');

  const fill = document.getElementById('stylus-progress-fill');
  if (fill) {
    const pct = Math.max(0, Math.min(100, Number(m.wear_percent || 0)));
    fill.style.width = `${pct}%`;
  }
}

function _stylusLoadFormFromState(resp) {
  const enabled = !!resp?.enabled;
  const stylus = resp?.stylus || null;
  const metrics = resp?.metrics || null;

  _stylusPageState.enabled = enabled;
  _stylusPageState.stylus = stylus;
  _stylusPageState.metrics = metrics;

  const enabledEl = document.getElementById('stylus-enabled');
  if (enabledEl) enabledEl.checked = enabled;

  const modeEl = document.getElementById('stylus-mode');
  if (modeEl) modeEl.value = stylus?.catalog_id ? 'catalog' : 'custom';

  const catalogEl = document.getElementById('stylus-catalog-select');
  if (catalogEl && stylus?.catalog_id) catalogEl.value = String(stylus.catalog_id);

  _ampPageSet('stylus-custom-brand', stylus?.brand || '');
  _ampPageSet('stylus-custom-model', stylus?.model || '');
  _ampPageSet('stylus-custom-profile', stylus?.stylus_profile || '');
  _ampPageSet('stylus-custom-lifetime', stylus?.lifetime_hours || '');

  const isNew = Number(stylus?.initial_used_hours || 0) <= 0;
  const isNewEl = document.getElementById('stylus-is-new');
  if (isNewEl) isNewEl.checked = isNew;
  _ampPageSet('stylus-initial-hours', stylus?.initial_used_hours || 0);

  _stylusSyncModeFields();
  _stylusSyncInitialHoursField();
  _stylusRenderMetrics(metrics);
}

function _stylusBuildRequestPayload() {
  const enabled = !!document.getElementById('stylus-enabled')?.checked;
  const mode = document.getElementById('stylus-mode')?.value || 'catalog';
  const isNew = !!document.getElementById('stylus-is-new')?.checked;
  const payload = { enabled, is_new: isNew };

  if (!enabled) return payload;

  if (mode === 'catalog') {
    const id = parseInt(document.getElementById('stylus-catalog-select')?.value || '0', 10);
    if (!Number.isFinite(id) || id <= 0) {
      throw new Error('Choose a catalog model.');
    }
    payload.catalog_id = id;
  } else {
    const lifetime = parseInt(document.getElementById('stylus-custom-lifetime')?.value || '0', 10);
    payload.brand = _ampPageVal('stylus-custom-brand');
    payload.model = _ampPageVal('stylus-custom-model');
    payload.stylus_profile = _ampPageVal('stylus-custom-profile');
    payload.lifetime_hours = Number.isFinite(lifetime) ? lifetime : 0;
    if (!payload.brand || !payload.model || !payload.stylus_profile || payload.lifetime_hours <= 0) {
      throw new Error('Fill all custom stylus fields and set lifetime hours > 0.');
    }
  }

  if (!isNew) {
    const v = document.getElementById('stylus-initial-hours')?.value;
    if (String(v || '').trim() !== '') {
      const n = Number(v);
      if (!Number.isFinite(n) || n < 0) throw new Error('Initial used hours must be >= 0.');
      payload.initial_used_hours = n;
    }
  }
  return payload;
}

async function loadStylusSection() {
  _stylusBindListenersOnce();
  try {
    const [catalogRes, stylusRes] = await Promise.all([
      fetch('/api/stylus/catalog'),
      fetch('/api/stylus'),
    ]);
    if (!catalogRes.ok || !stylusRes.ok) {
      throw new Error('load failed');
    }
    const catalogPayload = await catalogRes.json();
    const stylusPayload = await stylusRes.json();
    _stylusPageState.catalog = catalogPayload.items || [];
    _stylusRenderCatalog(_stylusPageState.catalog);
    _stylusLoadFormFromState(stylusPayload);
  } catch {
    toast('Failed to load stylus settings.', true);
  }
}

async function saveStylusSettings() {
  if (!(_ampConfig?.enabled)) {
    toast('Enable amplifier before stylus tracking.', true);
    return;
  }

  let payload;
  try {
    payload = _stylusBuildRequestPayload();
  } catch (err) {
    toast(err.message || 'Invalid stylus settings.', true);
    return;
  }

  try {
    const res = await fetch('/api/stylus', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      toast(body.error || 'Failed to save stylus settings.', true);
      return;
    }
    _stylusLoadFormFromState(body);
    toast('Stylus settings saved.', false);
  } catch {
    toast('Failed to save stylus settings.', true);
  }
}

async function replaceStylusNow() {
  if (!(_ampConfig?.enabled)) {
    toast('Enable amplifier before replacing stylus profile.', true);
    return;
  }

  const mode = document.getElementById('stylus-mode')?.value || 'catalog';
  const isNew = !!document.getElementById('stylus-is-new')?.checked;
  const payload = { is_new: isNew };

  try {
    if (mode === 'catalog') {
      const id = parseInt(document.getElementById('stylus-catalog-select')?.value || '0', 10);
      if (Number.isFinite(id) && id > 0) payload.catalog_id = id;
    } else {
      const lifetime = parseInt(document.getElementById('stylus-custom-lifetime')?.value || '0', 10);
      payload.brand = _ampPageVal('stylus-custom-brand');
      payload.model = _ampPageVal('stylus-custom-model');
      payload.stylus_profile = _ampPageVal('stylus-custom-profile');
      if (Number.isFinite(lifetime) && lifetime > 0) payload.lifetime_hours = lifetime;
    }

    if (!isNew) {
      const v = document.getElementById('stylus-initial-hours')?.value;
      if (String(v || '').trim() !== '') {
        const n = Number(v);
        if (!Number.isFinite(n) || n < 0) {
          toast('Initial used hours must be >= 0.', true);
          return;
        }
        payload.initial_used_hours = n;
      }
    }

    const res = await fetch('/api/stylus/replace', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      toast(body.error || 'Failed to replace stylus.', true);
      return;
    }
    _stylusLoadFormFromState(body);
    toast('Stylus replaced and hours reset.', false);
  } catch {
    toast('Failed to replace stylus.', true);
  }
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

  // Stylus section depends on amplifier availability.
  _stylusApplyAmplifierDependency(!!amp.enabled);
  await loadStylusSection();
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

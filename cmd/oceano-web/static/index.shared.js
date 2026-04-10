// ── Load config ──────────────────────────────────────────────────────────────
// Advanced values are not shown in the UI but are kept here and sent back on
// save so they are never accidentally reset to defaults.
let _advancedConfig = {};
let _recognitionConfig = {};

async function loadConfig() {
  const r = await fetch('/api/config');
  const cfg = await r.json();

  await loadNowPlayingDisplayCapabilities();

  set('inp-device',        cfg.audio_input?.device ?? '');
  set('inp-device-match',  cfg.audio_input?.device_match ?? '');
  set('inp-silence',       cfg.audio_input?.silence_threshold ?? 0.025);
  set('inp-debounce',      cfg.audio_input?.debounce_windows ?? 10);

  set('out-airplay-name',  cfg.audio_output?.airplay_name ?? '');
  set('out-device',        cfg.audio_output?.device ?? '');
  set('out-device-match',  cfg.audio_output?.device_match ?? '');

  set('rec-host',          cfg.recognition?.acrcloud_host ?? '');
  set('rec-access-key',    cfg.recognition?.acrcloud_access_key ?? '');
  set('rec-secret-key',    cfg.recognition?.acrcloud_secret_key ?? '');
  set('rec-chain',         cfg.recognition?.recognizer_chain ?? 'acrcloud_first');
  set('rec-duration',      cfg.recognition?.capture_duration_secs ?? 7);
  set('rec-interval',      cfg.recognition?.max_interval_secs ?? 300);
  set('rec-no-match-backoff', cfg.recognition?.no_match_backoff_secs ?? 15);
  set('rec-fp-boundary-skip', cfg.recognition?.fingerprint_boundary_lead_skip_secs ?? 2);
  set('rec-confirm-delay', cfg.recognition?.confirmation_delay_secs ?? 0);
  set('rec-confirm-duration', cfg.recognition?.confirmation_capture_duration_secs ?? 4);
  set('rec-confirm-bypass', cfg.recognition?.confirmation_bypass_score ?? 95);
  _recognitionConfig = cfg.recognition ?? {};

  set('disp-preset',          cfg.display?.ui_preset ?? 'high_contrast_rotate');
  set('disp-cycle-time',      cfg.display?.cycle_time ?? 30);
  set('disp-standby-timeout', cfg.display?.standby_timeout ?? 600);
  const artworkEl = document.getElementById('disp-external-artwork');
  if (artworkEl) artworkEl.checked = cfg.display?.external_artwork_enabled ?? true;

  const weatherEnabledEl = document.getElementById('weather-enabled');
  if (weatherEnabledEl) weatherEnabledEl.checked = cfg.weather?.enabled ?? true;
  set('weather-label',   cfg.weather?.location_label ?? 'Dublin');
  set('weather-lat',     cfg.weather?.latitude ?? 53.3498);
  set('weather-lon',     cfg.weather?.longitude ?? -6.2603);
  set('weather-refresh', cfg.weather?.refresh_mins ?? 10);

  // Amplifier / CD player config
  const ampEl = document.getElementById('amp-enabled');
  if (ampEl) ampEl.checked = cfg.amplifier?.enabled ?? false;
  set('amp-maker',          cfg.amplifier?.maker ?? '');
  set('amp-model',          cfg.amplifier?.model ?? '');
  set('amp-broadlink-host', cfg.amplifier?.broadlink?.host ?? '');
  set('amp-token',          cfg.amplifier?.broadlink?.token ?? '');
  updateAmpIRSummary(cfg.amplifier?.ir_codes ?? {});
  _ampConfig = cfg.amplifier ?? {};

  const cdEl = document.getElementById('cd-enabled');
  if (cdEl) cdEl.checked = cfg.cd_player?.enabled ?? false;
  set('cd-maker',  cfg.cd_player?.maker ?? '');
  set('cd-model',  cfg.cd_player?.model ?? '');
  updateCDIRSummary(cfg.cd_player?.ir_codes ?? {});
  _cdConfig = cfg.cd_player ?? {};

  updateAmpPanel();

  // Preserve advanced values as-is from server.
  _advancedConfig = cfg.advanced ?? {};
  updateRecognitionUI();
}

async function loadNowPlayingDisplayCapabilities() {
  const section = document.getElementById('nowplaying-section');
  const hint = document.getElementById('nowplaying-display-hint');
  if (!section) return;

  try {
    const r = await fetch('/api/display-detected');
    if (!r.ok) {
      section.style.display = 'none';
      return;
    }
    const d = await r.json();
    if (d?.connected) {
      section.style.display = '';
      if (hint) {
        const names = Array.isArray(d.connectors) ? d.connectors.join(', ') : '';
        hint.textContent = names
          ? `Detected display connectors: ${names}. Options below apply to the HDMI/DSI now playing page.`
          : 'Options for the HDMI/DSI now playing page.';
      }
    } else {
      section.style.display = 'none';
    }
  } catch {
    section.style.display = 'none';
  }
}

function set(id, val) {
  const el = document.getElementById(id);
  if (el) el.value = val;
}

function updateRecognitionUI() {
  const chain = val('rec-chain') || 'acrcloud_first';
  const usesACRCloud = chain === 'acrcloud_first' || chain === 'shazam_first' || chain === 'acrcloud_only';
  const group = document.getElementById('acrcloud-config-group');
  const hint = document.getElementById('acrcloud-config-hint');
  const ids = ['rec-host', 'rec-access-key', 'rec-secret-key'];

  if (group) {
    group.classList.toggle('field-group-muted', !usesACRCloud);
  }
  for (const id of ids) {
    const el = document.getElementById(id);
    if (el) {
      el.disabled = !usesACRCloud;
    }
  }
  if (hint) {
    hint.textContent = usesACRCloud
      ? 'Stored and used whenever the selected chain includes ACRCloud.'
      : 'Stored but currently inactive because the selected chain does not use ACRCloud.';
  }
}

// ── Status bar ───────────────────────────────────────────────────────────────
async function loadStatus() {
  try {
    const r = await fetch('/api/status');
    if (!r.ok) { setStatus(null); return; }
    const s = await r.json();
    setStatus(s);
  } catch { setStatus(null); }
  setTimeout(loadStatus, 3000);
}

let _lastArtworkPath = null;

function setStatus(s) {
  const bar      = document.getElementById('status-bar');
  const titleEl  = document.getElementById('status-title');
  const subEl    = document.getElementById('status-subtitle');
  const artImg   = document.getElementById('status-artwork');
  const badgeEl  = document.getElementById('status-badge');

  if (!s || s.state !== 'playing') {
    bar.className = '';
    titleEl.textContent = s ? (s.source === 'None' ? 'Not playing' : `${s.source} — stopped`) : 'Backend unreachable';
    subEl.textContent = '';
    artImg.classList.remove('loaded');
    badgeEl.style.display = 'none';
    return;
  }

  bar.className = 'playing';
  const t = s.track;
  titleEl.textContent = t ? (t.title || t.artist || s.source) : s.source;
  subEl.textContent   = t ? [t.artist, t.album].filter(Boolean).join(' · ') : '';

  const src = (s.source || '').toLowerCase();
  badgeEl.textContent = s.source;
  badgeEl.className   = `source-badge ${src}`;
  badgeEl.style.display = '';

  const artPath = t?.artwork_path || null;
  if (artPath !== _lastArtworkPath) {
    _lastArtworkPath = artPath;
    if (artPath) {
      artImg.onload  = () => artImg.classList.add('loaded');
      artImg.onerror = () => artImg.classList.remove('loaded');
      artImg.src = `/api/artwork?t=${Date.now()}`;
    } else {
      artImg.classList.remove('loaded');
      artImg.src = '';
    }
  }
}

function esc(s) {
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

// ── Device detection ─────────────────────────────────────────────────────────
let cachedDevices = null;

async function detectDevices(prefix) {
  if (!cachedDevices) {
    const r = await fetch('/api/devices');
    cachedDevices = await r.json();
  }
  showPicker(prefix, cachedDevices);
}

function showPicker(prefix, devices) {
  // Close any open picker
  document.querySelectorAll('.device-picker.open').forEach(p => p.classList.remove('open'));

  const picker = document.getElementById('picker-' + prefix);
  picker.innerHTML = '';
  (devices || []).forEach(d => {
    const el = document.createElement('div');
    el.className = 'device-picker-item';
    el.innerHTML = `${esc(d.name)} <span class="card-num">plughw:${d.card},0 — ${esc(d.desc)}</span>`;
    el.onclick = () => {
      document.getElementById(prefix + '-device').value = `plughw:${d.card},0`;
      picker.classList.remove('open');
    };
    picker.appendChild(el);
  });
  picker.classList.add('open');
}

// Close pickers on outside click
document.addEventListener('click', e => {
  if (!e.target.closest('.device-picker-wrap')) {
    document.querySelectorAll('.device-picker.open').forEach(p => p.classList.remove('open'));
  }
});

// ── Save config ──────────────────────────────────────────────────────────────
const cfgForm = document.getElementById('cfg-form');
if (cfgForm) cfgForm.addEventListener('submit', async e => {
  e.preventDefault();
  const btn = document.getElementById('btn-save');
  if (btn) {
    btn.disabled = true;
    btn.textContent = 'Saving…';
  }

  const cfg = {
    audio_input: {
      device:             val('inp-device'),
      device_match:       val('inp-device-match'),
      silence_threshold:  parseFloat(val('inp-silence')) || 0.025,
      debounce_windows:   parseInt(val('inp-debounce'))  || 10,
    },
    audio_output: {
      airplay_name:  val('out-airplay-name'),
      device:        val('out-device'),
      device_match:  val('out-device-match'),
    },
    recognition: {
      ..._recognitionConfig,
      acrcloud_host:        val('rec-host'),
      acrcloud_access_key:  val('rec-access-key'),
      acrcloud_secret_key:  val('rec-secret-key'),
        recognizer_chain:     val('rec-chain') || 'acrcloud_first',
      capture_duration_secs: intOr('rec-duration', 7),
      max_interval_secs:     intOr('rec-interval', 300),
      no_match_backoff_secs: intOr('rec-no-match-backoff', 15),
      fingerprint_boundary_lead_skip_secs: intOr('rec-fp-boundary-skip', 2),
      confirmation_delay_secs: intOr('rec-confirm-delay', 0),
      confirmation_capture_duration_secs: intOr('rec-confirm-duration', 4),
      confirmation_bypass_score: intOr('rec-confirm-bypass', 95),
    },
    advanced: _advancedConfig,
    display: {
      ui_preset:                val('disp-preset'),
      cycle_time:               parseInt(val('disp-cycle-time')) || 30,
      standby_timeout:          parseInt(val('disp-standby-timeout')) || 600,
      external_artwork_enabled: document.getElementById('disp-external-artwork')?.checked ?? true,
    },
    weather: {
      enabled:        document.getElementById('weather-enabled')?.checked ?? true,
      location_label: val('weather-label') || 'Dublin',
      latitude:       floatOr('weather-lat', 53.3498),
      longitude:      floatOr('weather-lon', -6.2603),
      refresh_mins:   intOr('weather-refresh', 10),
    },
    amplifier: {
      ..._ampConfig,
      enabled:                   document.getElementById('amp-enabled')?.checked ?? false,
      maker:                     val('amp-maker'),
      model:                     val('amp-model'),
      broadlink: { ...(_ampConfig.broadlink ?? {}), host: val('amp-broadlink-host') },
    },
    cd_player: {
      ..._cdConfig,
      enabled: document.getElementById('cd-enabled')?.checked ?? false,
      maker:   val('cd-maker'),
      model:   val('cd-model'),
    },
  };

  try {
    const r = await fetch('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(cfg),
    });
    const res = await r.json().catch(() => ({}));
    const isError = !r.ok || res.ok === false;
    toast(res.results?.join(' · ') || (isError ? 'Save failed' : 'Saved'), isError);
    if (!isError && window.history?.replaceState) {
      window.history.replaceState({}, document.title, window.location.pathname);
    }
  } catch (err) {
    toast('Save failed: ' + err.message, true);
  }

  if (btn) {
    btn.disabled = false;
    btn.textContent = 'Save & Restart Services';
  }
});

function val(id) { return (document.getElementById(id)?.value || '').trim(); }
function intOr(id, fallback) {
  const n = parseInt(val(id), 10);
  return Number.isNaN(n) ? fallback : n;
}

function floatOr(id, fallback) {
  const n = parseFloat(val(id));
  return Number.isNaN(n) ? fallback : n;
}

function setFieldValue(id, value) {
  const el = document.getElementById(id);
  if (!el) return;
  el.value = value;
}

async function reverseGeocodeLabel(lat, lon) {
  const query = new URLSearchParams({
    latitude: String(lat),
    longitude: String(lon),
    language: 'en',
    count: '1',
  });

  const resp = await fetch('https://geocoding-api.open-meteo.com/v1/reverse?' + query.toString(), {
    cache: 'no-store',
  });
  if (!resp.ok) {
    throw new Error('reverse geocoding failed');
  }

  const data = await resp.json();
  const r = Array.isArray(data?.results) ? data.results[0] : null;
  if (!r) return '';

  const city = (r.city || r.name || '').trim();
  const country = (r.country || '').trim();
  if (!city && !country) return '';
  if (city && country) return `${city}, ${country}`;
  return city || country;
}

function detectCurrentLocationWeather() {
  if (!('geolocation' in navigator)) {
    toast('Geolocation is not supported in this browser.', true);
    return;
  }

  toast('Requesting current location…');
  navigator.geolocation.getCurrentPosition(
    async (pos) => {
      const lat = pos.coords.latitude;
      const lon = pos.coords.longitude;
      setFieldValue('weather-lat', lat.toFixed(5));
      setFieldValue('weather-lon', lon.toFixed(5));

      // Always overwrite stale static labels (e.g. Lisbon) when using live
      // geolocation; prefer a resolved city/country name when available.
      setFieldValue('weather-label', 'Current location');
      try {
        const label = await reverseGeocodeLabel(lat, lon);
        if (label) {
          setFieldValue('weather-label', label);
        }
      } catch (_) {
        // Keep generic label if reverse geocoding is unavailable.
      }

      toast(`Location captured: ${lat.toFixed(5)}, ${lon.toFixed(5)}`);
    },
    (err) => {
      let msg = 'Unable to read current location.';
      if (err && err.code === 1) msg = 'Location permission denied.';
      if (err && err.code === 2) msg = 'Location unavailable.';
      if (err && err.code === 3) msg = 'Location request timed out.';
      toast(msg, true);
    },
    {
      enableHighAccuracy: true,
      timeout: 12000,
      maximumAge: 5 * 60 * 1000,
    }
  );
}

document.getElementById('rec-chain')?.addEventListener('change', updateRecognitionUI);

function toast(msg, isError = false) {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = isError ? 'error show' : 'show';
  clearTimeout(el._t);
  el._t = setTimeout(() => el.className = isError ? 'error' : '', 3500);
}


// ── Config drawer ─────────────────────────────────────────────────────────────
function openConfig() {
  document.getElementById('config-drawer').classList.add('open');
  document.getElementById('config-overlay').classList.add('open');
}
function closeConfig() {
  document.getElementById('config-drawer').classList.remove('open');
  document.getElementById('config-overlay').classList.remove('open');
}


// ── Power dialog ──────────────────────────────────────────────────────────────
function openPowerDialog() {
  document.getElementById('power-dialog')?.classList.add('open');
}

function closePowerDialog() {
  document.getElementById('power-dialog')?.classList.remove('open');
}

function showPowerActionToast(message) {
  toast(message, true);
}

async function sendPowerAction(action) {
  closePowerDialog();
  try {
    const response = await fetch('/api/power', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    });
    if (!response.ok) {
      const errorText = (await response.text()).trim();
      throw new Error(errorText || `Power action failed (${response.status})`);
    }
  } catch (error) {
    showPowerActionToast(error?.message || 'Failed to send power action');
  }
}

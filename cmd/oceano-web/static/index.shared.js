// ── Load config ──────────────────────────────────────────────────────────────
// Advanced values are not shown in the UI but are kept here and sent back on
// save so they are never accidentally reset to defaults.
let _advancedConfig = {};
let _recognitionConfig = {};
let _audioInputConfig = {};

async function loadConfig() {
  const r = await fetch('/api/config');
  const cfg = await r.json();

  await loadSPIDisplayCapabilities();
  await loadNowPlayingDisplayCapabilities();

  _audioInputConfig = cfg.audio_input ?? {};
  set('inp-device',        cfg.audio_input?.device ?? '');
  set('inp-device-match',  cfg.audio_input?.device_match ?? '');

  set('out-airplay-name',  cfg.audio_output?.airplay_name ?? '');
  set('out-device',        cfg.audio_output?.device ?? '');
  set('out-device-match',  cfg.audio_output?.device_match ?? '');

  const btEnabledEl = document.getElementById('bt-enabled');
  if (btEnabledEl) btEnabledEl.checked = cfg.bluetooth?.enabled ?? false;
  set('bt-name', cfg.bluetooth?.name ?? '');
  loadBluetoothDevices();

  set('rec-host',             cfg.recognition?.acrcloud_host ?? '');
  set('rec-access-key',       cfg.recognition?.acrcloud_access_key ?? '');
  set('rec-secret-key',       cfg.recognition?.acrcloud_secret_key ?? '');
  set('rec-chain',            cfg.recognition?.recognizer_chain ?? 'acrcloud_first');
  set('rec-shazam-python',    cfg.recognition?.shazam_python_bin ?? '');
  set('rec-duration',         cfg.recognition?.capture_duration_secs ?? 7);
  set('rec-interval',         cfg.recognition?.max_interval_secs ?? 300);
  set('rec-refresh-interval', cfg.recognition?.refresh_interval_secs ?? 120);
  set('rec-no-match-backoff', cfg.recognition?.no_match_backoff_secs ?? 15);
  set('rec-confirm-delay',    cfg.recognition?.confirmation_delay_secs ?? 0);
  set('rec-confirm-duration', cfg.recognition?.confirmation_capture_duration_secs ?? 4);
  set('rec-confirm-bypass',   cfg.recognition?.confirmation_bypass_score ?? 95);
  set('rec-continuity-interval', cfg.recognition?.shazam_continuity_interval_secs ?? 8);
  set('rec-continuity-capture',  cfg.recognition?.shazam_continuity_capture_duration_secs ?? 4);
  _recognitionConfig = cfg.recognition ?? {};

  set('adv-library-db', cfg.advanced?.library_db ?? '');
  set('adv-vu-socket',  cfg.advanced?.vu_socket  ?? '');
  set('adv-pcm-socket',     cfg.advanced?.pcm_socket ?? '');
  set('adv-source-file',    cfg.advanced?.source_file ?? '');
  set('adv-state-file',     cfg.advanced?.state_file ?? '');
  set('adv-artwork-dir',    cfg.advanced?.artwork_dir ?? '');
  set('adv-metadata-pipe',  cfg.advanced?.metadata_pipe ?? '');

  set('disp-preset',          cfg.display?.ui_preset ?? 'high_contrast_rotate');
  set('disp-cycle-time',      cfg.display?.cycle_time ?? 30);
  set('disp-standby-timeout', cfg.display?.standby_timeout ?? 600);
  const artworkEl = document.getElementById('disp-external-artwork');
  if (artworkEl) artworkEl.checked = cfg.display?.external_artwork_enabled ?? true;

  const npAmbientEl = document.getElementById('np-ambient-color');
  if (npAmbientEl) npAmbientEl.checked = cfg.now_playing?.ambient_color_enabled ?? true;
  const npIdleThemeEl = document.getElementById('np-idle-screen-theme');
  if (npIdleThemeEl) {
    const t = (cfg.now_playing?.idle_screen_theme || 'classic').toLowerCase();
    npIdleThemeEl.value = t === 'colourful' || t === 'colorful' ? 'colourful' : 'classic';
  }
  set('np-idle-delay', cfg.advanced?.idle_delay_secs ?? 3);

  const weatherEnabledEl = document.getElementById('weather-enabled');
  if (weatherEnabledEl) weatherEnabledEl.checked = cfg.weather?.enabled ?? true;
  set('weather-label',   cfg.weather?.location_label ?? 'Dublin');
  set('weather-lat',     cfg.weather?.latitude ?? 53.3498);
  set('weather-lon',     cfg.weather?.longitude ?? -6.2603);
  set('weather-refresh', cfg.weather?.refresh_mins ?? 10);

  // Amplifier config
  const ampEl = document.getElementById('amp-enabled');
  if (ampEl) ampEl.checked = cfg.amplifier?.enabled ?? false;
  set('amp-maker',          cfg.amplifier?.maker ?? '');
  set('amp-model',          cfg.amplifier?.model ?? '');
  set('amp-input-mode',     cfg.amplifier?.input_mode ?? 'cycle');
  set('amp-usb-reset-max-attempts',   cfg.amplifier?.usb_reset?.max_attempts ?? 13);
  set('amp-usb-reset-first-step-ms',  cfg.amplifier?.usb_reset?.first_step_settle_ms ?? 150);
  set('amp-usb-reset-step-wait-ms',   cfg.amplifier?.usb_reset?.step_wait_ms ?? 2400);
  set('amp-broadlink-host', cfg.amplifier?.broadlink?.host ?? '');
  set('amp-token',          cfg.amplifier?.broadlink?.token ?? '');
  _ampConfig = cfg.amplifier ?? {};
  _ampLastKnownInputID = String(cfg.amplifier_runtime?.last_known_input_id ?? '');
  if (typeof setAmplifierInputsModel === 'function') {
    setAmplifierInputsModel(cfg.amplifier?.inputs ?? []);
  }
  if (typeof setConnectedDevicesModel === 'function') {
    setConnectedDevicesModel(cfg.amplifier?.connected_devices ?? []);
  }
  updateAmpIRSummary(cfg.amplifier?.ir_codes ?? {});
  if (typeof _refreshDirectIRWarning === 'function') {
    _refreshDirectIRWarning();
  }

  updateAmpPanel();
  if (typeof loadAmplifierProfiles === 'function') {
    await loadAmplifierProfiles(cfg);
  }

  // Preserve advanced values as-is from server.
  _advancedConfig = cfg.advanced ?? {};
  updateRecognitionUI();
  loadRecognitionStats();
}

async function loadRecognitionStats() {
  const container = document.getElementById('rec-stats-container');
  if (!container) return;

  try {
    const r = await fetch('/api/recognition/stats');
    const stats = await r.json();

    if (Object.keys(stats).length === 0) {
      container.innerHTML = '<div class="hint">No statistics available yet. Recognition needs to run at least once.</div>';
      return;
    }

    container.innerHTML = '';
    // Sort providers: Trigger first, then others alphabetically.
    const providers = Object.keys(stats).sort((a, b) => {
      if (a === 'Trigger') return -1;
      if (b === 'Trigger') return 1;
      return a.localeCompare(b);
    });

    for (const p of providers) {
      const evs = stats[p];
      const card = document.createElement('div');
      card.className = 'stat-card';

      let html;
      if (p === 'Trigger') {
        const boundary = evs.boundary || 0;
        const fallback = evs.fallback_timer || 0;
        const total = boundary + fallback;
        const boundaryRate = total > 0 ? Math.round((boundary / total) * 100) : 0;
        html = `<div class="stat-provider">TRIGGER</div>`;
        html += `<div class="stat-row"><span class="label">Boundary</span><span class="value">${boundary}</span></div>`;
        html += `<div class="stat-row"><span class="label">Fallback timer</span><span class="value">${fallback}</span></div>`;
        html += `<div class="stat-row"><span class="label">Total</span><span class="value">${total}</span></div>`;
        if (total > 0) {
          html += `<div class="stat-success-rate">
            <span>Boundary rate</span>
            <span class="rate-ok">${boundaryRate}%</span>
          </div>`;
        } else {
          html += `<div class="stat-success-rate">
            <span>Boundary rate</span>
            <span class="rate-none">—</span>
          </div>`;
        }
      } else {
        const attempts = evs.attempt || 0;
        const successes = evs.success || 0;
        const rate = attempts > 0 ? Math.round((successes / attempts) * 100) : 0;

        html = `<div class="stat-provider">${p}</div>`;
        html += `<div class="stat-row"><span class="label">Attempts</span><span class="value">${attempts}</span></div>`;
        html += `<div class="stat-row"><span class="label">Matches</span><span class="value">${successes}</span></div>`;

        if (evs.no_match) {
          html += `<div class="stat-row"><span class="label">No match</span><span class="value">${evs.no_match}</span></div>`;
        }
        if (evs.error) {
          html += `<div class="stat-row"><span class="label">Errors</span><span class="value">${evs.error}</span></div>`;
        }

        if (attempts > 0) {
          html += `<div class="stat-success-rate">
            <span>Success rate</span>
            <span class="rate-ok">${rate}%</span>
          </div>`;
        } else {
          html += `<div class="stat-success-rate">
            <span>Success rate</span>
            <span class="rate-none">—</span>
          </div>`;
        }
      }

      card.innerHTML = html;
      container.appendChild(card);
    }
  } catch (e) {
    container.innerHTML = `<div class="hint" style="color:var(--warn-text)">Failed to load statistics: ${e.message}</div>`;
  }
}

async function loadSPIDisplayCapabilities() {
  const section = document.getElementById('spi-section');
  if (!section) return;
  try {
    const r = await fetch('/api/spi-display-installed');
    if (!r.ok) { section.style.display = 'none'; return; }
    const d = await r.json();
    section.style.display = d?.installed ? '' : 'none';
  } catch {
    section.style.display = 'none';
  }
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

async function restartHDMIDisplayService() {
  const btn = document.getElementById('restart-display-btn');
  if (btn) btn.disabled = true;

  try {
    const response = await fetch('/api/display/restart', { method: 'POST' });
    if (!response.ok) {
      const errorText = (await response.text()).trim();
      throw new Error(errorText || `Display restart failed (${response.status})`);
    }
    toast('HDMI/DSI display service restarted.');
  } catch (error) {
    toast(error?.message || 'Failed to restart HDMI/DSI display service.', true);
  } finally {
    if (btn) btn.disabled = false;
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
window._lastBackendStatus = null;

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
  window._lastBackendStatus = s || null;

  const bar      = document.getElementById('status-bar');
  const titleEl  = document.getElementById('status-title');
  const subEl    = document.getElementById('status-subtitle');
  const artImg   = document.getElementById('status-artwork');
  const badgeEl  = document.getElementById('status-badge');

  const recBtn = document.getElementById('status-recognize-btn');
  if (!s || s.state !== 'playing') {
    bar.className = '';
    titleEl.textContent = s ? (s.source === 'None' ? 'Not playing' : `${s.source} — stopped`) : 'Backend unreachable';
    subEl.textContent = '';
    artImg.classList.remove('loaded');
    badgeEl.style.display = 'none';
    if (recBtn) recBtn.style.display = 'none';
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

  const isPhysicalSource = ['physical','cd','vinyl'].includes(src);
  if (recBtn) recBtn.style.display = isPhysicalSource ? '' : 'none';

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
      ..._audioInputConfig,
      device:        val('inp-device'),
      device_match:  val('inp-device-match'),
    },
    audio_output: {
      airplay_name:  val('out-airplay-name'),
      device:        val('out-device'),
      device_match:  val('out-device-match'),
    },
    bluetooth: {
      enabled: document.getElementById('bt-enabled')?.checked ?? false,
      name:    val('bt-name'),
    },
    recognition: { ..._recognitionConfig },
    display: {
      ui_preset:                val('disp-preset'),
      cycle_time:               parseInt(val('disp-cycle-time')) || 30,
      standby_timeout:          parseInt(val('disp-standby-timeout')) || 600,
      external_artwork_enabled: document.getElementById('disp-external-artwork')?.checked ?? true,
    },
    now_playing: {
      ambient_color_enabled: document.getElementById('np-ambient-color')?.checked ?? true,
      idle_screen_theme:     document.getElementById('np-idle-screen-theme')?.value === 'colourful' ? 'colourful' : 'classic',
    },
    advanced: {
      ..._advancedConfig,
      idle_delay_secs: intOr('np-idle-delay', 3),
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
      enabled:    document.getElementById('amp-enabled')?.checked ?? false,
      profile_id: val('amp-profile-select') || _ampConfig.profile_id || '',
      input_mode: _ampConfig.input_mode || 'cycle',
      maker:      _ampConfig.maker  || '',
      model:      _ampConfig.model  || '',
      inputs:     _ampConfig.inputs ?? [],
      usb_reset:  _ampConfig.usb_reset ?? {},
      broadlink:  _ampConfig.broadlink ?? {},
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

// ── Weather city search ───────────────────────────────────────────────────────

let _citySearchTimer = null;
let _cityResults     = [];
let _cityFocusIdx    = -1;

function onWeatherCityInput() {
  clearTimeout(_citySearchTimer);
  const q = (document.getElementById('weather-city-search')?.value || '').trim();
  if (q.length < 2) { _hideCityResults(); return; }
  _citySearchTimer = setTimeout(() => _fetchCityResults(q), 350);
}

function onWeatherCityKeydown(e) {
  const box = document.getElementById('weather-city-results');
  if (!box || box.style.display === 'none') return;
  if (e.key === 'ArrowDown') { e.preventDefault(); _moveFocus(1); }
  else if (e.key === 'ArrowUp') { e.preventDefault(); _moveFocus(-1); }
  else if (e.key === 'Enter')  { e.preventDefault(); if (_cityFocusIdx >= 0) _selectCity(_cityResults[_cityFocusIdx]); }
  else if (e.key === 'Escape') { _hideCityResults(); }
}

async function _fetchCityResults(q) {
  try {
    const url = `https://geocoding-api.open-meteo.com/v1/search?name=${encodeURIComponent(q)}&count=6&language=en&format=json`;
    const r = await fetch(url, { cache: 'no-store' });
    if (!r.ok) return;
    const data = await r.json();
    _cityResults = Array.isArray(data.results) ? data.results : [];
    _renderCityResults();
  } catch (_) {}
}

function _renderCityResults() {
  const box = document.getElementById('weather-city-results');
  if (!box) return;
  if (!_cityResults.length) { _hideCityResults(); return; }
  _cityFocusIdx = -1;
  box.innerHTML = _cityResults.map((c, i) => {
    const name    = [c.name, c.admin1, c.country].filter(Boolean).join(', ');
    const sub     = c.country_code ? `${c.latitude?.toFixed(2)}, ${c.longitude?.toFixed(2)}` : '';
    return `<div class="weather-city-opt" data-idx="${i}"
              style="padding:9px 13px;cursor:pointer;font-size:0.88rem;border-bottom:1px solid var(--border);
                     display:flex;flex-direction:column;gap:1px;transition:background 0.1s"
              onmousedown="event.preventDefault();_selectCity(_cityResults[${i}])"
              onmouseenter="_moveFocusTo(${i})">
              <span style="color:var(--text);font-weight:500">${_escHtml(name)}</span>
              <span style="color:var(--text-dim);font-size:0.76rem">${_escHtml(sub)}</span>
            </div>`;
  }).join('');
  box.style.display = '';
}

function _moveFocus(delta) {
  _moveFocusTo(Math.max(-1, Math.min(_cityResults.length - 1, _cityFocusIdx + delta)));
}

function _moveFocusTo(idx) {
  _cityFocusIdx = idx;
  document.querySelectorAll('.weather-city-opt').forEach((el, i) => {
    el.style.background = i === idx ? 'var(--hover)' : '';
  });
}

function _selectCity(c) {
  if (!c) return;
  const label = [c.name, c.admin1, c.country].filter(Boolean).join(', ');
  setFieldValue('weather-label', c.name || label);
  setFieldValue('weather-lat',   Number(c.latitude).toFixed(5));
  setFieldValue('weather-lon',   Number(c.longitude).toFixed(5));
  const inp = document.getElementById('weather-city-search');
  if (inp) inp.value = label;
  _hideCityResults();
  toast(`Location set: ${label}`);
}

function _hideCityResults() {
  const box = document.getElementById('weather-city-results');
  if (box) box.style.display = 'none';
  _cityResults = [];
  _cityFocusIdx = -1;
}

function _escHtml(s) {
  return String(s || '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

document.addEventListener('click', (e) => {
  if (!e.target.closest('#weather-city-search') && !e.target.closest('#weather-city-results')) {
    _hideCityResults();
  }
});

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
  loadBackups();
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

// ── Bluetooth paired devices ──────────────────────────────────────────────────

async function loadBluetoothDevices() {
  const container = document.getElementById('bt-devices-list');
  if (!container) return;

  try {
    const r = await fetch('/api/bluetooth/devices');
    const devices = await r.json();

    container.textContent = '';

    if (!devices || devices.length === 0) {
      const empty = document.createElement('span');
      empty.style.cssText = 'color:var(--muted);font-size:13px';
      empty.textContent = 'No paired devices.';
      container.appendChild(empty);
      return;
    }

    for (const dev of devices) {
      const row = document.createElement('div');
      row.style.cssText = 'display:flex;align-items:center;justify-content:space-between;gap:12px;padding:8px 10px;background:var(--surface2,#1e1e1e);border-radius:6px;border:1px solid var(--border,#333)';

      const label = document.createElement('span');
      label.style.cssText = 'font-size:13px;flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap';
      label.textContent = dev.name + ' · ' + dev.mac;
      row.appendChild(label);

      const btn = document.createElement('button');
      btn.textContent = 'Remove';
      btn.style.cssText = 'flex-shrink:0;padding:4px 10px;font-size:12px;background:transparent;border:1px solid var(--danger,#7a1a1a);color:var(--danger-text,#f5b5b5);border-radius:4px;cursor:pointer';
      btn.onclick = async () => {
        btn.disabled = true;
        btn.textContent = 'Removing…';
        try {
          const res = await fetch('/api/bluetooth/devices?mac=' + encodeURIComponent(dev.mac), { method: 'DELETE' });
          if (!res.ok) throw new Error(await res.text());
          row.remove();
          if (container.children.length === 0) loadBluetoothDevices();
        } catch (e) {
          btn.disabled = false;
          btn.textContent = 'Remove';
          toast(e.message || 'Failed to remove device', true);
        }
      };
      row.appendChild(btn);
      container.appendChild(row);
    }
  } catch {
    // API not available (e.g. development) — hide the field silently.
  }
}

// ── Force recognition ────────────────────────────────────────────────────────

async function forceRecognize(btn) {
  if (btn) btn.disabled = true;
  try {
    await fetch('/api/recognize', { method: 'POST' });
  } catch { /* ignore — state manager handles it */ }
  // Re-enable after 15 s to avoid flooding recognition attempts.
  setTimeout(() => { if (btn) btn.disabled = false; }, 15_000);
}


'use strict';

const _calibrationState = {
  off: null,
  on: null,
  vinylTransition: null,
  cfg: null,
  byInput: {},
};

function _normalizeCalibrationSample(raw) {
  if (!raw || typeof raw !== 'object') return null;
  const avg = Number(raw.avg_rms);
  const min = Number(raw.min_rms);
  const max = Number(raw.max_rms);
  const samples = Number(raw.samples);
  if (!Number.isFinite(avg) || !Number.isFinite(min) || !Number.isFinite(max) || !Number.isFinite(samples)) {
    return null;
  }
  return {
    avg_rms: avg,
    min_rms: min,
    max_rms: max,
    samples,
  };
}

function _normalizeCalibrationProfiles(raw) {
  const out = {};
  if (!raw || typeof raw !== 'object') return out;
  for (const [key, profile] of Object.entries(raw)) {
    if (!key || key === '__manual__') continue;
    if (!profile || typeof profile !== 'object') continue;
    out[key] = {
      off: _normalizeCalibrationSample(profile.off),
      on: _normalizeCalibrationSample(profile.on),
      vinyl_transition: _normalizeVinylTransition(profile.vinyl_transition),
    };
  }
  return out;
}

function _normalizeVinylTransition(raw) {
  if (!raw || typeof raw !== 'object') return null;
  const tail = Number(raw.tail_avg_rms);
  const gap = Number(raw.gap_avg_rms);
  const attack = Number(raw.attack_avg_rms);
  const gapSecs = Number(raw.gap_duration_secs);
  const sps = Number(raw.samples_per_sec);
  const samples = Number(raw.samples);
  if (!Number.isFinite(tail) || !Number.isFinite(gap) || !Number.isFinite(attack) || !Number.isFinite(gapSecs) || !Number.isFinite(sps) || !Number.isFinite(samples)) {
    return null;
  }
  return {
    tail_avg_rms: tail,
    gap_avg_rms: gap,
    attack_avg_rms: attack,
    gap_duration_secs: gapSecs,
    samples_per_sec: sps,
    samples,
  };
}

function _calibrationSelectedInputKey() {
  const raw = _rval('cal-input-select');
  return raw || '__manual__';
}

function _syncCalibrationContextFromSelection() {
  const key = _calibrationSelectedInputKey();
  const slot = _calibrationState.byInput[key] || { off: null, on: null, vinyl_transition: null };
  _calibrationState.off = slot.off;
  _calibrationState.on = slot.on;
  _calibrationState.vinylTransition = slot.vinyl_transition || null;
  renderCalibrationResult('off', _calibrationState.off);
  renderCalibrationResult('on', _calibrationState.on);
  renderVinylTransitionResult(_calibrationState.vinylTransition);
  renderVinylTransitionVisibility();
  renderCalibrationRecommendation();
  _renderCurrentValuesHint();
}

function _selectedInputLabel() {
  const el = document.getElementById('cal-input-select');
  if (!el) return '';
  const idx = el.selectedIndex;
  if (idx < 0) return '';
  return (el.options[idx]?.textContent || '').trim();
}

function _isVinylInputSelected() {
  const key = _calibrationSelectedInputKey();
  const label = _selectedInputLabel().toLowerCase();
  return key === '10' || label.includes('phono') || label.includes('vinyl') || label.includes('vinil');
}

function _renderCurrentValuesHint() {
  const el = document.getElementById('cal-current-values-hint');
  if (!el) return;

  const key = _calibrationSelectedInputKey();
  const slot = _calibrationState.byInput[key];
  if (!slot || (!slot.off && !slot.on && !slot.vinyl_transition)) {
    el.textContent = 'No calibration saved for this input yet.';
    return;
  }

  const rec = calibrationRecommendation();
  if (!rec || !rec.ok) {
    el.textContent = 'Calibration data exists for this input, but recommendation is incomplete. Capture OFF and ON again.';
    return;
  }

  const parts = [];
  if (rec.detectorThreshold != null) {
    parts.push(`source=${rec.detectorThreshold.toFixed(4)}`);
  }
  if (rec.vuThreshold != null) {
    parts.push(`vu=${rec.vuThreshold.toFixed(4)}`);
  }
  if (Number.isFinite(rec.gap)) {
    parts.push(`off/on-gap=${rec.gap.toFixed(4)}`);
  }
  if (_calibrationState.vinylTransition && Number.isFinite(Number(_calibrationState.vinylTransition.gap_duration_secs))) {
    parts.push(`vinyl-gap=${Number(_calibrationState.vinylTransition.gap_duration_secs).toFixed(2)}s`);
  }
  el.textContent = `Current calibrated values for this input: ${parts.join(' | ')}.`;
}

function renderVinylTransitionVisibility() {
  const box = document.getElementById('cal-vinyl-step');
  if (!box) return;
  box.style.display = _isVinylInputSelected() ? '' : 'none';
}

function _rval(id) {
  return (document.getElementById(id)?.value ?? '').trim();
}
function _rset(id, v) {
  const el = document.getElementById(id);
  if (el) el.value = v ?? '';
}
function _rint(id, fallback) {
  const n = parseInt(_rval(id), 10);
  return Number.isNaN(n) ? fallback : n;
}
function _rfloat(id, fallback) {
  const n = parseFloat(_rval(id));
  return Number.isNaN(n) ? fallback : n;
}

function _cfgInt(v, fallback) {
  const n = Number.parseInt(v, 10);
  return Number.isNaN(n) ? fallback : n;
}

function _cfgFloat(v, fallback) {
  const n = Number.parseFloat(v);
  return Number.isNaN(n) ? fallback : n;
}

function updateRecognitionUI() {
  const chain = _rval('rec-chain') || 'acrcloud_first';
  const usesACR = chain !== 'shazam_only';
  const group = document.getElementById('acrcloud-config-group');
  const hint  = document.getElementById('acrcloud-config-hint');
  if (group) group.style.display = usesACR ? '' : 'none';
  if (hint)  hint.style.display  = usesACR ? '' : 'none';
}

function applyTuningPreset() {
  const preset = _rval('rec-tuning-preset') || 'custom';
  if (preset === 'custom') {
    return;
  }

  // Conservative profile for vinyl playback with manual needle repositioning.
  if (preset === 'vinyl_safe') {
    _rset('rec-continuity-interval', 8);
    _rset('rec-continuity-grace', 45);
    _rset('rec-continuity-mismatch-window', 180);
    _rset('rec-continuity-sightings-cal', 2);
    _rset('rec-continuity-sightings-uncal', 3);
    _rset('rec-early-check-margin', 20);
    _rset('rec-duration-guard-bypass', 20);
    _rset('rec-duration-pessimism', 0.75);
    _rset('rec-boundary-restore-min-seek', 60);
    toast('Preset applied: Vinyl Safe');
    return;
  }

  if (preset === 'balanced') {
    _rset('rec-continuity-interval', 7);
    _rset('rec-continuity-grace', 35);
    _rset('rec-continuity-mismatch-window', 180);
    _rset('rec-continuity-sightings-cal', 2);
    _rset('rec-continuity-sightings-uncal', 3);
    _rset('rec-early-check-margin', 25);
    _rset('rec-duration-guard-bypass', 20);
    _rset('rec-duration-pessimism', 0.75);
    _rset('rec-boundary-restore-min-seek', 55);
    toast('Preset applied: Balanced');
    return;
  }

  if (preset === 'gapless_aggressive') {
    _rset('rec-continuity-interval', 6);
    _rset('rec-continuity-grace', 25);
    _rset('rec-continuity-mismatch-window', 150);
    _rset('rec-continuity-sightings-cal', 1);
    _rset('rec-continuity-sightings-uncal', 2);
    _rset('rec-early-check-margin', 35);
    _rset('rec-duration-guard-bypass', 15);
    _rset('rec-duration-pessimism', 0.70);
    _rset('rec-boundary-restore-min-seek', 45);
    toast('Preset applied: Gapless Aggressive');
  }
}

async function loadRecognitionPage() {
  let cfg;
  try {
    const r = await fetch('/api/config');
    if (!r.ok) { toast('Failed to load configuration.', true); return; }
    cfg = await r.json();
  } catch {
    toast('Failed to load configuration.', true);
    return;
  }

  _rset('inp-silence',  cfg.audio_input?.silence_threshold ?? 0.025);
  _rset('inp-debounce', cfg.audio_input?.debounce_windows  ?? 10);
  _rset('rec-vu-silence-threshold', cfg.advanced?.vu_silence_threshold ?? 0.0095);
  _rset('rec-session-gap',  cfg.advanced?.session_gap_threshold_secs ?? 45);
  _rset('rec-idle-delay',   cfg.advanced?.idle_delay_secs ?? 10);

  _rset('rec-chain',            cfg.recognition?.recognizer_chain        ?? 'acrcloud_first');
  _rset('rec-host',             cfg.recognition?.acrcloud_host           ?? '');
  _rset('rec-access-key',       cfg.recognition?.acrcloud_access_key     ?? '');
  _rset('rec-secret-key',       cfg.recognition?.acrcloud_secret_key     ?? '');
  _rset('rec-shazam-python',    cfg.recognition?.shazam_python_bin       ?? '');
  _rset('rec-duration',         cfg.recognition?.capture_duration_secs);
  _rset('rec-interval',         cfg.recognition?.max_interval_secs);
  _rset('rec-refresh-interval', cfg.recognition?.refresh_interval_secs);
  _rset('rec-no-match-backoff', cfg.recognition?.no_match_backoff_secs);
  _rset('rec-confirm-delay',    cfg.recognition?.confirmation_delay_secs);
  _rset('rec-confirm-duration', cfg.recognition?.confirmation_capture_duration_secs);
  _rset('rec-confirm-bypass',   cfg.recognition?.confirmation_bypass_score);
  _rset('rec-continuity-interval', cfg.recognition?.shazam_continuity_interval_secs);
  _rset('rec-continuity-capture',  cfg.recognition?.shazam_continuity_capture_duration_secs);
  _rset('rec-continuity-grace', cfg.recognition?.continuity_calibration_grace_secs);
  _rset('rec-continuity-mismatch-window', cfg.recognition?.continuity_mismatch_confirm_window_secs);
  _rset('rec-continuity-sightings-cal', cfg.recognition?.continuity_required_sightings_calibrated);
  _rset('rec-continuity-sightings-uncal', cfg.recognition?.continuity_required_sightings_uncalibrated);
  _rset('rec-early-check-margin', cfg.recognition?.early_check_margin_secs);
  _rset('rec-duration-guard-bypass', cfg.recognition?.duration_guard_bypass_window_secs);
  _rset('rec-duration-pessimism', cfg.recognition?.duration_pessimism);
  _rset('rec-boundary-restore-min-seek', cfg.recognition?.boundary_restore_min_seek_secs);

  _calibrationState.cfg = cfg;
  _calibrationState.byInput = _normalizeCalibrationProfiles(cfg.advanced?.calibration_profiles);
  fillCalibrationInputOptions(cfg);
  _renderCurrentValuesHint();

  updateRecognitionUI();
  loadRecognitionStats();
}

function fillCalibrationInputOptions(cfg) {
  const select = document.getElementById('cal-input-select');
  if (!select) return;
  select.innerHTML = '';

  const inputs = Array.isArray(cfg?.amplifier?.inputs) ? cfg.amplifier.inputs : [];
  const visible = inputs.filter((i) => i && i.visible !== false);

  if (!visible.length) {
    const opt = document.createElement('option');
    opt.value = '';
    opt.textContent = 'Manual selection on amplifier';
    select.appendChild(opt);
    _syncCalibrationContextFromSelection();
    return;
  }

  for (const input of visible) {
    const opt = document.createElement('option');
    opt.value = String(input.id || '');
    opt.textContent = input.logical_name || `Input ${input.id}`;
    select.appendChild(opt);
  }

  _syncCalibrationContextFromSelection();
}

function renderCalibrationResult(kind, data) {
  const target = document.getElementById(kind === 'off' ? 'cal-off-result' : 'cal-on-result');
  if (!target) return;
  if (!data) {
    target.textContent = '';
    return;
  }
  const label = kind === 'off' ? 'OFF sample' : 'ON sample';
  target.textContent = `${label}: avg=${data.avg_rms.toFixed(4)} min=${data.min_rms.toFixed(4)} max=${data.max_rms.toFixed(4)} samples=${data.samples}`;
}

function calibrationRecommendation() {
  const off = _calibrationState.off;
  const on = _calibrationState.on;
  const vinyl = _calibrationState.vinylTransition;
  let detectorThreshold = null;
  let vuThreshold = null;
  let gap = null;
  let offRMS = null;
  let onRMS = null;

  if (off && on) {
    offRMS = Number(off.avg_rms || 0);
    onRMS = Number(on.avg_rms || 0);
    if (!(onRMS > offRMS)) {
      return {
        ok: false,
        message: 'ON RMS is not above OFF RMS. Repeat captures with stable volume and no playback.',
      };
    }
    gap = onRMS - offRMS;
    detectorThreshold = offRMS + gap * 0.65;
    vuThreshold = offRMS + gap * 0.50;
  }

  if (vinyl) {
    const tail = Number(vinyl.tail_avg_rms || 0);
    const gapRMS = Number(vinyl.gap_avg_rms || 0);
    const attack = Number(vinyl.attack_avg_rms || 0);
    const minMusic = Math.min(tail, attack);
    if (Number.isFinite(minMusic) && Number.isFinite(gapRMS) && minMusic > gapRMS) {
      const vinylVuThreshold = gapRMS + (minMusic - gapRMS) * 0.35;
      vuThreshold = vuThreshold == null ? vinylVuThreshold : Math.min(vuThreshold, vinylVuThreshold);
    }
  }

  if (detectorThreshold == null && vuThreshold == null) {
    return null;
  }

  const parts = [];
  if (detectorThreshold != null) {
    parts.push(`source silence threshold ${detectorThreshold.toFixed(4)}`);
  }
  if (vuThreshold != null) {
    parts.push(`VU silence threshold ${vuThreshold.toFixed(4)}`);
  }
  if (vinyl && Number.isFinite(Number(vinyl.gap_duration_secs))) {
    parts.push(`vinyl gap ~${Number(vinyl.gap_duration_secs).toFixed(2)}s`);
  }

  return {
    ok: true,
    detectorThreshold,
    vuThreshold,
    offRMS,
    onRMS,
    gap,
    message: `Recommended: ${parts.join(', ')}.`,
  };
}

function renderCalibrationRecommendation() {
  const el = document.getElementById('cal-recommendation');
  if (!el) return;
  const rec = calibrationRecommendation();
  if (!rec) {
    el.textContent = 'Capture OFF/ON samples, and for Phono optionally capture a vinyl transition sample, to compute recommendations.';
    return;
  }
  if (!rec.ok) {
    el.textContent = rec.message;
    return;
  }
  el.textContent = rec.message;
}

async function captureCalibrationSample(kind) {
  const status = document.getElementById('cal-wizard-status');
  const secs = _rint('cal-duration-secs', 6);
  const captureSecs = Math.max(2, Math.min(20, secs));
  const inputKey = _calibrationSelectedInputKey();
  if (status) status.textContent = `Capturing ${kind.toUpperCase()} sample for ${captureSecs}s...`;

  try {
    const res = await fetch('/api/calibration/vu-sample', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ seconds: captureSecs }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      if (status) status.textContent = body.error || 'Calibration capture failed.';
      toast(body.error || 'Calibration capture failed.', true);
      return;
    }
    if (!_calibrationState.byInput[inputKey]) {
      _calibrationState.byInput[inputKey] = { off: null, on: null, vinyl_transition: null };
    }
    _calibrationState.byInput[inputKey][kind] = body;
    _calibrationState[kind] = body;
    renderCalibrationResult(kind, body);
    renderCalibrationRecommendation();
    _renderCurrentValuesHint();
    if (status) status.textContent = `${kind.toUpperCase()} sample captured.`;
    toast(`${kind.toUpperCase()} sample captured.`, false);
  } catch {
    if (status) status.textContent = 'Calibration capture failed.';
    toast('Calibration capture failed.', true);
  }
}

function _meanRange(values, start, end) {
  const s = Math.max(0, Math.min(values.length, start));
  const e = Math.max(s + 1, Math.min(values.length, end));
  let sum = 0;
  for (let i = s; i < e; i++) {
    sum += Number(values[i]) || 0;
  }
  return sum / (e - s);
}

function analyzeVinylTransitionSequence(body) {
  const rms = Array.isArray(body?.rms) ? body.rms.map((v) => Number(v) || 0) : [];
  if (rms.length < 12) {
    return null;
  }

  const sps = Number(body.samples_per_sec) > 0 ? Number(body.samples_per_sec) : (rms.length / Math.max(1, Number(body.seconds) || 1));
  let minIdx = 0;
  for (let i = 1; i < rms.length; i++) {
    if (rms[i] < rms[minIdx]) minIdx = i;
  }

  const tailSpan = Math.max(3, Math.round(sps * 2.5));
  const gapSpan = Math.max(3, Math.round(sps * 0.8));
  const attackSpan = Math.max(3, Math.round(sps * 2.0));

  const tailStart = Math.max(0, minIdx - tailSpan);
  const tailEnd = Math.max(tailStart + 1, minIdx - Math.max(1, Math.round(sps * 0.2)));
  const gapStart = Math.max(0, minIdx - Math.floor(gapSpan / 2));
  const gapEnd = Math.min(rms.length, gapStart + gapSpan);
  const attackStart = Math.min(rms.length - 1, minIdx + Math.max(1, Math.round(sps * 0.4)));
  const attackEnd = Math.min(rms.length, attackStart + attackSpan);

  const tailAvg = _meanRange(rms, tailStart, tailEnd);
  const gapAvg = _meanRange(rms, gapStart, gapEnd);
  const attackAvg = _meanRange(rms, attackStart, attackEnd);

  const nearGap = gapAvg * 1.2;
  let gapCount = 0;
  for (const v of rms) {
    if (v <= nearGap) gapCount++;
  }
  const gapDurationSecs = sps > 0 ? (gapCount / sps) : 0;

  return {
    tail_avg_rms: tailAvg,
    gap_avg_rms: gapAvg,
    attack_avg_rms: attackAvg,
    gap_duration_secs: gapDurationSecs,
    samples_per_sec: sps,
    samples: rms.length,
  };
}

function renderVinylTransitionResult(data) {
  const result = document.getElementById('cal-vinyl-result');
  const hint = document.getElementById('cal-vinyl-recommendation');
  if (result) {
    if (!data) {
      result.textContent = '';
    } else {
      result.textContent = `Vinyl transition: tail=${data.tail_avg_rms.toFixed(4)} gap=${data.gap_avg_rms.toFixed(4)} attack=${data.attack_avg_rms.toFixed(4)} gapDur=${data.gap_duration_secs.toFixed(2)}s samples=${data.samples}`;
    }
  }
  if (hint) {
    if (!data) {
      hint.textContent = '';
    } else {
      hint.textContent = 'Transition profile saved for this input and used to make VU threshold recommendation more conservative.';
    }
  }
}

async function captureVinylTransitionSample() {
  const status = document.getElementById('cal-wizard-status');
  if (!_isVinylInputSelected()) {
    toast('Vinyl transition capture is only available for Phono inputs.', true);
    return;
  }
  const secs = _rint('cal-duration-secs', 6);
  const captureSecs = Math.max(6, Math.min(30, secs * 3));
  const inputKey = _calibrationSelectedInputKey();
  if (status) status.textContent = `Capturing vinyl transition sequence for ${captureSecs}s...`;

  try {
    const res = await fetch('/api/calibration/vu-sequence', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ seconds: captureSecs }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      if (status) status.textContent = body.error || 'Vinyl transition capture failed.';
      toast(body.error || 'Vinyl transition capture failed.', true);
      return;
    }

    const transition = analyzeVinylTransitionSequence(body);
    if (!transition) {
      if (status) status.textContent = 'Vinyl transition sample too short. Repeat with a longer transition.';
      toast('Vinyl transition sample too short. Repeat capture.', true);
      return;
    }

    if (!_calibrationState.byInput[inputKey]) {
      _calibrationState.byInput[inputKey] = { off: null, on: null, vinyl_transition: null };
    }
    _calibrationState.byInput[inputKey].vinyl_transition = transition;
    _calibrationState.vinylTransition = transition;
    renderVinylTransitionResult(transition);
    renderCalibrationRecommendation();
    _renderCurrentValuesHint();
    if (status) status.textContent = 'Vinyl transition sample captured.';
    toast('Vinyl transition sample captured.', false);
  } catch {
    if (status) status.textContent = 'Vinyl transition capture failed.';
    toast('Vinyl transition capture failed.', true);
  }
}

function applyCalibrationRecommendations() {
  const rec = calibrationRecommendation();
  if (!rec || !rec.ok) {
    toast('Capture calibration samples first.', true);
    return;
  }
  if (rec.detectorThreshold != null) {
    _rset('inp-silence', rec.detectorThreshold.toFixed(4));
  }
  if (rec.vuThreshold != null) {
    _rset('rec-vu-silence-threshold', rec.vuThreshold.toFixed(4));
  }
  _renderCurrentValuesHint();
  toast('Calibration recommendations applied to fields. Save to persist.', false);
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
        html += `<div class="stat-success-rate"><span>Boundary rate</span><span class="${total > 0 ? 'rate-ok' : 'rate-none'}">${total > 0 ? boundaryRate + '%' : '—'}</span></div>`;
      } else {
        const attempts = evs.attempt || 0;
        const successes = evs.success || 0;
        const rate = attempts > 0 ? Math.round((successes / attempts) * 100) : 0;
        html = `<div class="stat-provider">${p}</div>`;
        html += `<div class="stat-row"><span class="label">Attempts</span><span class="value">${attempts}</span></div>`;
        html += `<div class="stat-row"><span class="label">Matches</span><span class="value">${successes}</span></div>`;
        if (evs.no_match) html += `<div class="stat-row"><span class="label">No match</span><span class="value">${evs.no_match}</span></div>`;
        if (evs.error)    html += `<div class="stat-row"><span class="label">Errors</span><span class="value">${evs.error}</span></div>`;
        html += `<div class="stat-success-rate"><span>Success rate</span><span class="${attempts > 0 ? 'rate-ok' : 'rate-none'}">${attempts > 0 ? rate + '%' : '—'}</span></div>`;
      }
      card.innerHTML = html;
      container.appendChild(card);
    }
  } catch (e) {
    container.innerHTML = `<div class="hint" style="color:var(--warn-text)">Failed to load statistics: ${e.message}</div>`;
  }
}

async function saveRecognitionPage() {
  const btn = document.getElementById('btn-rec-page-save');
  if (btn) { btn.disabled = true; btn.textContent = 'Saving…'; }

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

  fullCfg.audio_input = {
    ...(fullCfg.audio_input ?? {}),
    silence_threshold: _rfloat('inp-silence', _cfgFloat(fullCfg.audio_input?.silence_threshold, 0.025)),
    debounce_windows:  _rint('inp-debounce', _cfgInt(fullCfg.audio_input?.debounce_windows, 10)),
  };

  fullCfg.advanced = {
    ...(fullCfg.advanced ?? {}),
    vu_silence_threshold:       _rfloat('rec-vu-silence-threshold', _cfgFloat(fullCfg.advanced?.vu_silence_threshold, 0.0095)),
    session_gap_threshold_secs: _rint('rec-session-gap',  _cfgInt(fullCfg.advanced?.session_gap_threshold_secs, 45)),
    idle_delay_secs:            _rint('rec-idle-delay',   _cfgInt(fullCfg.advanced?.idle_delay_secs, 10)),
    calibration_profiles:       _normalizeCalibrationProfiles(_calibrationState.byInput),
  };

  const recCurrent = fullCfg.recognition ?? {};

  fullCfg.recognition = {
    ...recCurrent,
    recognizer_chain:                     _rval('rec-chain') || 'acrcloud_first',
    acrcloud_host:                        _rval('rec-host'),
    acrcloud_access_key:                  _rval('rec-access-key'),
    acrcloud_secret_key:                  _rval('rec-secret-key'),
    shazam_python_bin:                    _rval('rec-shazam-python'),
    capture_duration_secs:                _rint('rec-duration', _cfgInt(recCurrent.capture_duration_secs, 7)),
    max_interval_secs:                    _rint('rec-interval', _cfgInt(recCurrent.max_interval_secs, 300)),
    refresh_interval_secs:                _rint('rec-refresh-interval', _cfgInt(recCurrent.refresh_interval_secs, 120)),
    no_match_backoff_secs:                _rint('rec-no-match-backoff', _cfgInt(recCurrent.no_match_backoff_secs, 15)),
    confirmation_delay_secs:              _rint('rec-confirm-delay', _cfgInt(recCurrent.confirmation_delay_secs, 0)),
    confirmation_capture_duration_secs:   _rint('rec-confirm-duration', _cfgInt(recCurrent.confirmation_capture_duration_secs, 4)),
    confirmation_bypass_score:            _rint('rec-confirm-bypass', _cfgInt(recCurrent.confirmation_bypass_score, 95)),
    shazam_continuity_interval_secs:           _rint('rec-continuity-interval', _cfgInt(recCurrent.shazam_continuity_interval_secs, 8)),
    shazam_continuity_capture_duration_secs:   _rint('rec-continuity-capture', _cfgInt(recCurrent.shazam_continuity_capture_duration_secs, 4)),
    continuity_calibration_grace_secs:          _rint('rec-continuity-grace', _cfgInt(recCurrent.continuity_calibration_grace_secs, 45)),
    continuity_mismatch_confirm_window_secs:    _rint('rec-continuity-mismatch-window', _cfgInt(recCurrent.continuity_mismatch_confirm_window_secs, 180)),
    continuity_required_sightings_calibrated:   _rint('rec-continuity-sightings-cal', _cfgInt(recCurrent.continuity_required_sightings_calibrated, 2)),
    continuity_required_sightings_uncalibrated: _rint('rec-continuity-sightings-uncal', _cfgInt(recCurrent.continuity_required_sightings_uncalibrated, 3)),
    early_check_margin_secs:                    _rint('rec-early-check-margin', _cfgInt(recCurrent.early_check_margin_secs, 20)),
    duration_guard_bypass_window_secs:         _rint('rec-duration-guard-bypass', _cfgInt(recCurrent.duration_guard_bypass_window_secs, 20)),
    duration_pessimism:                        _rfloat('rec-duration-pessimism', _cfgFloat(recCurrent.duration_pessimism, 0.75)),
    boundary_restore_min_seek_secs:            _rint('rec-boundary-restore-min-seek', _cfgInt(recCurrent.boundary_restore_min_seek_secs, 60)),
  };

  try {
    const r = await fetch('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(fullCfg),
    });
    const res = await r.json().catch(() => ({}));
    if (!r.ok) {
      toast(res.error || 'Save failed.', true);
    } else {
      toast('Saved — services restarting…');
    }
  } catch {
    toast('Save failed.', true);
  }

  if (btn) { btn.disabled = false; btn.textContent = 'Save & Restart Services'; }
}

function toast(msg, isError = false) {
  const el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.className = isError ? 'error show' : 'show';
  clearTimeout(el._t);
  el._t = setTimeout(() => el.className = '', 3500);
}

document.getElementById('rec-chain')?.addEventListener('change', updateRecognitionUI);
document.addEventListener('DOMContentLoaded', () => {
  document.getElementById('cal-input-select')?.addEventListener('change', _syncCalibrationContextFromSelection);
  loadRecognitionPage();
});

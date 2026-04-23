'use strict';

// ── Shared calibration state ───────────────────────────────────────────────────

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
  return { avg_rms: avg, min_rms: min, max_rms: max, samples };
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
  if (!Number.isFinite(tail) || !Number.isFinite(gap) || !Number.isFinite(attack) ||
      !Number.isFinite(gapSecs) || !Number.isFinite(sps) || !Number.isFinite(samples)) {
    return null;
  }
  return { tail_avg_rms: tail, gap_avg_rms: gap, attack_avg_rms: attack, gap_duration_secs: gapSecs, samples_per_sec: sps, samples };
}

function _isVinylLabel(label, key) {
  const l = (label || '').toLowerCase();
  return key === '10' || l.includes('phono') || l.includes('vinyl') || l.includes('vinil');
}

// ── Recommendation engine ──────────────────────────────────────────────────────
// Accepts explicit off/on/vinyl, or falls back to _calibrationState when called with no args.

function calibrationRecommendation(off, on, vinyl) {
  if (off === undefined) off = _calibrationState.off;
  if (on === undefined)  on  = _calibrationState.on;
  if (vinyl === undefined) vinyl = _calibrationState.vinylTransition;

  let detectorThreshold = null;
  let vuThreshold = null;
  let gap = null;
  let offRMS = null;
  let onRMS = null;

  if (off && on) {
    offRMS = Number(off.avg_rms || 0);
    onRMS  = Number(on.avg_rms || 0);
    if (!(onRMS > offRMS)) {
      return { ok: false, message: 'ON RMS is not above OFF RMS. Repeat captures with stable volume and no playback.' };
    }
    gap = onRMS - offRMS;
    detectorThreshold = offRMS + gap * 0.65;
    vuThreshold       = offRMS + gap * 0.50;
  }

  if (vinyl) {
    const tail    = Number(vinyl.tail_avg_rms || 0);
    const gapRMS  = Number(vinyl.gap_avg_rms || 0);
    const attack  = Number(vinyl.attack_avg_rms || 0);
    const minMusic = Math.min(tail, attack);
    if (Number.isFinite(minMusic) && Number.isFinite(gapRMS) && minMusic > gapRMS) {
      const vinylVu = gapRMS + (minMusic - gapRMS) * 0.35;
      vuThreshold = vuThreshold == null ? vinylVu : Math.min(vuThreshold, vinylVu);
    }
  }

  if (detectorThreshold == null && vuThreshold == null) return null;

  const parts = [];
  if (detectorThreshold != null) parts.push(`source silence threshold ${detectorThreshold.toFixed(4)}`);
  if (vuThreshold != null)        parts.push(`VU silence threshold ${vuThreshold.toFixed(4)}`);
  if (vinyl && Number.isFinite(Number(vinyl.gap_duration_secs))) {
    parts.push(`vinyl gap ~${Number(vinyl.gap_duration_secs).toFixed(2)}s`);
  }

  return { ok: true, detectorThreshold, vuThreshold, offRMS, onRMS, gap, message: `Recommended: ${parts.join(', ')}.` };
}

// ── UI helpers ─────────────────────────────────────────────────────────────────

function _rval(id)             { return (document.getElementById(id)?.value ?? '').trim(); }
function _rset(id, v)          { const el = document.getElementById(id); if (el) el.value = v ?? ''; }
function _rint(id, fallback)   { const n = parseInt(_rval(id), 10); return Number.isNaN(n) ? fallback : n; }
function _rfloat(id, fallback) { const n = parseFloat(_rval(id)); return Number.isNaN(n) ? fallback : n; }
function _cfgInt(v, fallback)  { const n = Number.parseInt(v, 10); return Number.isNaN(n) ? fallback : n; }
function _cfgFloat(v, fallback){ const n = Number.parseFloat(v); return Number.isNaN(n) ? fallback : n; }

function _esc(s) {
  return String(s || '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// ── Calibration summary (main page) ───────────────────────────────────────────

function renderCalibrationSummary() {
  const container = document.getElementById('cal-summary-grid');
  if (!container) return;

  const cfg = _calibrationState.cfg;
  const allInputs = Array.isArray(cfg?.amplifier?.inputs)
    ? cfg.amplifier.inputs.filter(i => i && i.visible !== false)
    : [];

  const profiles = _calibrationState.byInput;
  const shown = new Set();
  const items = [];

  for (const [key, slot] of Object.entries(profiles)) {
    if (!slot || (!slot.off && !slot.on && !slot.vinyl_transition)) continue;
    const ampInp = allInputs.find(i => String(i.id) === key);
    shown.add(key);
    items.push({ key, label: ampInp?.logical_name || key, slot, measured: true });
  }

  for (const inp of allInputs) {
    const key = String(inp.id || '');
    if (shown.has(key)) continue;
    items.push({ key, label: inp.logical_name || `Input ${inp.id}`, slot: null, measured: false });
  }

  if (items.length === 0) {
    container.innerHTML = '<div class="hint" style="padding:4px 0 2px">No amplifier inputs configured. Run the wizard after setting up inputs in the Amplifier section.</div>';
    return;
  }

  container.innerHTML = items.map(item => {
    const { label, slot, measured, key } = item;
    const isPhono = _isVinylLabel(label, key);

    const badges = [];
    if (measured) badges.push(`<span class="cal-sc-badge measured">Measured</span>`);
    else          badges.push(`<span class="cal-sc-badge defaults">Defaults</span>`);
    if (isPhono)  badges.push(`<span class="cal-sc-badge phono">Phono</span>`);

    let valsHtml = '';
    if (measured && slot) {
      const rec = calibrationRecommendation(slot.off, slot.on, slot.vinyl_transition || null);
      if (rec && rec.ok) {
        valsHtml += `<div class="cal-sc-val"><span class="lbl">Source</span><span class="val">${rec.detectorThreshold.toFixed(4)}</span></div>`;
        valsHtml += `<div class="cal-sc-val"><span class="lbl">VU</span><span class="val">${rec.vuThreshold.toFixed(4)}</span></div>`;
        if (rec.gap != null) valsHtml += `<div class="cal-sc-val"><span class="lbl">OFF/ON gap</span><span class="val">${rec.gap.toFixed(4)}</span></div>`;
      } else {
        valsHtml += `<span class="hint" style="align-self:center">Incomplete — run wizard again to capture OFF and ON.</span>`;
      }
      if (slot.vinyl_transition && Number.isFinite(slot.vinyl_transition.gap_duration_secs)) {
        valsHtml += `<div class="cal-sc-val"><span class="lbl">Vinyl gap</span><span class="val">${slot.vinyl_transition.gap_duration_secs.toFixed(2)}s</span></div>`;
      }
    } else {
      const vu  = _rfloat('rec-vu-silence-threshold', 0.0095);
      const sil = _rfloat('inp-silence', 0.025);
      valsHtml  = `<div class="cal-sc-val"><span class="lbl">Source</span><span class="val">${sil.toFixed(4)}</span></div>`;
      valsHtml += `<div class="cal-sc-val"><span class="lbl">VU</span><span class="val">${vu.toFixed(4)}</span></div>`;
    }

    const defNote = !measured ? `<div class="cal-sc-defnote">Global defaults — not yet calibrated for this input</div>` : '';

    return `<div class="cal-sc-card${measured ? '' : ' is-default'}">
      <div class="cal-sc-head"><span class="cal-sc-name">${_esc(label)}</span>${badges.join('')}</div>
      <div class="cal-sc-values">${valsHtml}</div>
      ${defNote}
    </div>`;
  }).join('');
}

// ── Recognition UI ─────────────────────────────────────────────────────────────

function updateRecognitionUI() {
  const chain = _rval('rec-chain') || 'acrcloud_first';
  const usesACR = chain !== 'shazam_only';
  const group = document.getElementById('acrcloud-config-group');
  const hint  = document.getElementById('acrcloud-config-hint');
  if (group) group.style.display = usesACR ? '' : 'none';
  if (hint)  hint.style.display  = usesACR ? '' : 'none';
}

function _tuningPresetValues(preset) {
  switch (preset) {
    case 'standard':
      // Safe defaults — no calibration required.
      return { interval: 8, grace: 45, window: 180, sigCal: 2, sigUncal: 3, earlyCheck: 20, guardBypass: 20, pessimism: 0.75, restoreSeek: 60 };
    case 'calibrated':
      // For setups with active calibration profiles. Exits learning mode faster,
      // more proactive near track end. Requires calibration wizard to have run.
      return { interval: 8, grace: 25, window: 180, sigCal: 2, sigUncal: 3, earlyCheck: 35, guardBypass: 20, pessimism: 0.75, restoreSeek: 60 };
    case 'balanced':
      // Middle ground — works well without calibration on mixed vinyl/CD collections.
      return { interval: 7, grace: 35, window: 180, sigCal: 2, sigUncal: 3, earlyCheck: 25, guardBypass: 20, pessimism: 0.75, restoreSeek: 55 };
    case 'gapless':
      // For CD-heavy or gapless collections. Faster detection, more API calls.
      return { interval: 6, grace: 25, window: 150, sigCal: 1, sigUncal: 2, earlyCheck: 35, guardBypass: 15, pessimism: 0.70, restoreSeek: 45 };
    default:
      return null;
  }
}

function detectTuningPreset() {
  const v = {
    interval:    parseInt(_rval('rec-continuity-interval'))        || 0,
    grace:       parseInt(_rval('rec-continuity-grace'))           || 0,
    window:      parseInt(_rval('rec-continuity-mismatch-window')) || 0,
    sigCal:      parseInt(_rval('rec-continuity-sightings-cal'))   || 0,
    sigUncal:    parseInt(_rval('rec-continuity-sightings-uncal')) || 0,
    earlyCheck:  parseInt(_rval('rec-early-check-margin'))         || 0,
    guardBypass: parseInt(_rval('rec-duration-guard-bypass'))      || 0,
    pessimism:   parseFloat(_rval('rec-duration-pessimism'))       || 0,
    restoreSeek: parseInt(_rval('rec-boundary-restore-min-seek'))  || 0,
  };
  for (const name of ['standard', 'calibrated', 'balanced', 'gapless']) {
    const p = _tuningPresetValues(name);
    if (v.interval === p.interval && v.grace === p.grace && v.sigCal === p.sigCal &&
        v.sigUncal === p.sigUncal && v.earlyCheck === p.earlyCheck &&
        Math.abs(v.pessimism - p.pessimism) < 0.001 && v.restoreSeek === p.restoreSeek) {
      return name;
    }
  }
  return 'custom';
}

function applyTuningPreset() {
  const preset = _rval('rec-tuning-preset') || 'custom';
  if (preset === 'custom') return;
  const p = _tuningPresetValues(preset);
  if (!p) return;
  _rset('rec-continuity-interval',        p.interval);
  _rset('rec-continuity-grace',           p.grace);
  _rset('rec-continuity-mismatch-window', p.window);
  _rset('rec-continuity-sightings-cal',   p.sigCal);
  _rset('rec-continuity-sightings-uncal', p.sigUncal);
  _rset('rec-early-check-margin',         p.earlyCheck);
  _rset('rec-duration-guard-bypass',      p.guardBypass);
  _rset('rec-duration-pessimism',         p.pessimism);
  _rset('rec-boundary-restore-min-seek',  p.restoreSeek);
  const label = { standard: 'Standard', calibrated: 'Calibrated', balanced: 'Balanced', gapless: 'Gapless' }[preset] || preset;
  toast(`Preset applied: ${label}`);
}

// ── Page load / save ───────────────────────────────────────────────────────────

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
  _rset('rec-continuity-grace',    cfg.recognition?.continuity_calibration_grace_secs);
  _rset('rec-continuity-mismatch-window', cfg.recognition?.continuity_mismatch_confirm_window_secs);
  _rset('rec-continuity-sightings-cal',   cfg.recognition?.continuity_required_sightings_calibrated);
  _rset('rec-continuity-sightings-uncal', cfg.recognition?.continuity_required_sightings_uncalibrated);
  _rset('rec-early-check-margin',      cfg.recognition?.early_check_margin_secs);
  _rset('rec-duration-guard-bypass',   cfg.recognition?.duration_guard_bypass_window_secs);
  _rset('rec-duration-pessimism',      cfg.recognition?.duration_pessimism);
  _rset('rec-boundary-restore-min-seek', cfg.recognition?.boundary_restore_min_seek_secs);

  _calibrationState.cfg      = cfg;
  _calibrationState.byInput  = _normalizeCalibrationProfiles(cfg.advanced?.calibration_profiles);

  renderCalibrationSummary();
  updateRecognitionUI();

  const sel = document.getElementById('rec-tuning-preset');
  if (sel) sel.value = detectTuningPreset();
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
    recognizer_chain:                             _rval('rec-chain') || 'acrcloud_first',
    acrcloud_host:                                _rval('rec-host'),
    acrcloud_access_key:                          _rval('rec-access-key'),
    acrcloud_secret_key:                          _rval('rec-secret-key'),
    shazam_python_bin:                            _rval('rec-shazam-python'),
    capture_duration_secs:                        _rint('rec-duration', _cfgInt(recCurrent.capture_duration_secs, 7)),
    max_interval_secs:                            _rint('rec-interval', _cfgInt(recCurrent.max_interval_secs, 300)),
    refresh_interval_secs:                        _rint('rec-refresh-interval', _cfgInt(recCurrent.refresh_interval_secs, 120)),
    no_match_backoff_secs:                        _rint('rec-no-match-backoff', _cfgInt(recCurrent.no_match_backoff_secs, 15)),
    confirmation_delay_secs:                      _rint('rec-confirm-delay', _cfgInt(recCurrent.confirmation_delay_secs, 0)),
    confirmation_capture_duration_secs:           _rint('rec-confirm-duration', _cfgInt(recCurrent.confirmation_capture_duration_secs, 4)),
    confirmation_bypass_score:                    _rint('rec-confirm-bypass', _cfgInt(recCurrent.confirmation_bypass_score, 95)),
    shazam_continuity_interval_secs:              _rint('rec-continuity-interval', _cfgInt(recCurrent.shazam_continuity_interval_secs, 8)),
    shazam_continuity_capture_duration_secs:      _rint('rec-continuity-capture', _cfgInt(recCurrent.shazam_continuity_capture_duration_secs, 4)),
    continuity_calibration_grace_secs:            _rint('rec-continuity-grace', _cfgInt(recCurrent.continuity_calibration_grace_secs, 45)),
    continuity_mismatch_confirm_window_secs:      _rint('rec-continuity-mismatch-window', _cfgInt(recCurrent.continuity_mismatch_confirm_window_secs, 180)),
    continuity_required_sightings_calibrated:     _rint('rec-continuity-sightings-cal', _cfgInt(recCurrent.continuity_required_sightings_calibrated, 2)),
    continuity_required_sightings_uncalibrated:   _rint('rec-continuity-sightings-uncal', _cfgInt(recCurrent.continuity_required_sightings_uncalibrated, 3)),
    early_check_margin_secs:                      _rint('rec-early-check-margin', _cfgInt(recCurrent.early_check_margin_secs, 20)),
    duration_guard_bypass_window_secs:            _rint('rec-duration-guard-bypass', _cfgInt(recCurrent.duration_guard_bypass_window_secs, 20)),
    duration_pessimism:                           _rfloat('rec-duration-pessimism', _cfgFloat(recCurrent.duration_pessimism, 0.75)),
    boundary_restore_min_seek_secs:               _rint('rec-boundary-restore-min-seek', _cfgInt(recCurrent.boundary_restore_min_seek_secs, 60)),
  };

  try {
    const r = await fetch('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(fullCfg),
    });
    const res = await r.json().catch(() => ({}));
    if (!r.ok) { toast(res.error || 'Save failed.', true); }
    else        { toast('Saved — services restarting…'); }
  } catch {
    toast('Save failed.', true);
  }

  if (btn) { btn.disabled = false; btn.textContent = 'Save & Restart Services'; }
}

// ── Vinyl sequence analysis ────────────────────────────────────────────────────

function _meanRange(values, start, end) {
  const s = Math.max(0, Math.min(values.length, start));
  const e = Math.max(s + 1, Math.min(values.length, end));
  let sum = 0;
  for (let i = s; i < e; i++) sum += Number(values[i]) || 0;
  return sum / (e - s);
}

function analyzeVinylTransitionSequence(body) {
  const rms = Array.isArray(body?.rms) ? body.rms.map(v => Number(v) || 0) : [];
  if (rms.length < 12) return null;

  const sps = Number(body.samples_per_sec) > 0
    ? Number(body.samples_per_sec)
    : (rms.length / Math.max(1, Number(body.seconds) || 1));

  let minIdx = 0;
  for (let i = 1; i < rms.length; i++) {
    if (rms[i] < rms[minIdx]) minIdx = i;
  }

  const tailSpan   = Math.max(3, Math.round(sps * 2.5));
  const gapSpan    = Math.max(3, Math.round(sps * 0.8));
  const attackSpan = Math.max(3, Math.round(sps * 2.0));

  const tailStart   = Math.max(0, minIdx - tailSpan);
  const tailEnd     = Math.max(tailStart + 1, minIdx - Math.max(1, Math.round(sps * 0.2)));
  const gapStart    = Math.max(0, minIdx - Math.floor(gapSpan / 2));
  const gapEnd      = Math.min(rms.length, gapStart + gapSpan);
  const attackStart = Math.min(rms.length - 1, minIdx + Math.max(1, Math.round(sps * 0.4)));
  const attackEnd   = Math.min(rms.length, attackStart + attackSpan);

  const tailAvg   = _meanRange(rms, tailStart, tailEnd);
  const gapAvg    = _meanRange(rms, gapStart, gapEnd);
  const attackAvg = _meanRange(rms, attackStart, attackEnd);

  const nearGap = gapAvg * 1.2;
  let gapCount = 0;
  for (const v of rms) { if (v <= nearGap) gapCount++; }
  const gapDurationSecs = sps > 0 ? (gapCount / sps) : 0;

  return { tail_avg_rms: tailAvg, gap_avg_rms: gapAvg, attack_avg_rms: attackAvg, gap_duration_secs: gapDurationSecs, samples_per_sec: sps, samples: rms.length };
}

// ── Toast ──────────────────────────────────────────────────────────────────────

function toast(msg, isError = false) {
  const el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.className = isError ? 'error show' : 'show';
  clearTimeout(el._t);
  el._t = setTimeout(() => el.className = '', 3500);
}

// ═══════════════════════════════════════════════════════════════════════════════
// CALIBRATION WIZARD
// ═══════════════════════════════════════════════════════════════════════════════

const WIZ_STEPS = { SELECT: 1, OFF: 2, ON: 3, VINYL: 4, ANOTHER: 5, SUMMARY: 6 };

const _wiz = {
  step: 0,
  inputKey: '__manual__',
  inputLabel: 'Manual',
  isPhono: false,
  off: null,
  on: null,
  vinyl: null,
  capturing: false,
  captureDuration: 6,
};

// ── SVG illustrations ──────────────────────────────────────────────────────────

const _SVG_AMP = `<svg viewBox="0 0 110 60" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round">
  <rect x="2" y="10" width="106" height="40" rx="5" stroke-width="1.5"/>
  <circle cx="13" cy="22" r="3.5" fill="currentColor" opacity="0.45" stroke="none"/>
  <rect x="24" y="16" width="34" height="20" rx="3" stroke-width="1.3"/>
  <rect x="28" y="27" width="3" height="6" rx="1" fill="currentColor" opacity="0.3" stroke="none"/>
  <rect x="33" y="24" width="3" height="9" rx="1" fill="currentColor" opacity="0.5" stroke="none"/>
  <rect x="38" y="26" width="3" height="7" rx="1" fill="currentColor" opacity="0.4" stroke="none"/>
  <rect x="43" y="23" width="3" height="10" rx="1" fill="currentColor" opacity="0.6" stroke="none"/>
  <rect x="48" y="25" width="3" height="8" rx="1" fill="currentColor" opacity="0.35" stroke="none"/>
  <circle cx="74" cy="26" r="7" stroke-width="1.5"/>
  <line x1="74" y1="26" x2="74" y2="20" stroke-width="1.5"/>
  <circle cx="92" cy="26" r="6" stroke-width="1.5"/>
  <line x1="92" y1="26" x2="96" y2="21" stroke-width="1.5"/>
  <circle cx="74" cy="40" r="3.5" stroke-width="1.3"/>
  <circle cx="84" cy="40" r="3.5" stroke-width="1.3"/>
  <circle cx="94" cy="40" r="3.5" stroke-width="1.3"/>
  <line x1="14" y1="37" x2="14" y2="43" stroke-width="1"/>
  <line x1="19" y1="37" x2="19" y2="43" stroke-width="1"/>
</svg>`;

const _SVG_POWER_OFF = `<svg viewBox="0 0 64 64" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round">
  <circle cx="32" cy="38" r="18" stroke-width="1.8"/>
  <path d="M32 22 L32 38" stroke-width="3"/>
  <path d="M22 26 A16 16 0 1 0 42 26" stroke-width="2"/>
  <circle cx="32" cy="58" r="2.5" fill="currentColor" opacity="0.25" stroke="none"/>
</svg>`;

const _SVG_RCA_DISC = `<svg viewBox="0 0 100 50" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round">
  <line x1="4" y1="14" x2="22" y2="14" stroke-width="2"/>
  <line x1="4" y1="36" x2="22" y2="36" stroke-width="2"/>
  <rect x="22" y="8" width="14" height="12" rx="3" stroke-width="1.5"/>
  <rect x="22" y="30" width="14" height="12" rx="3" stroke-width="1.5"/>
  <line x1="40" y1="14" x2="58" y2="14" stroke-width="1.5" stroke-dasharray="3 3" opacity="0.45"/>
  <line x1="40" y1="36" x2="58" y2="36" stroke-width="1.5" stroke-dasharray="3 3" opacity="0.45"/>
  <rect x="58" y="8" width="14" height="12" rx="3" stroke-width="1.5" opacity="0.4"/>
  <rect x="58" y="30" width="14" height="12" rx="3" stroke-width="1.5" opacity="0.4"/>
  <line x1="72" y1="14" x2="96" y2="14" stroke-width="2" opacity="0.4"/>
  <line x1="72" y1="36" x2="96" y2="36" stroke-width="2" opacity="0.4"/>
  <line x1="49" y1="8" x2="49" y2="42" stroke-width="1.5" stroke-dasharray="1 4" opacity="0.6"/>
</svg>`;

const _SVG_POWER_ON = `<svg viewBox="0 0 64 64" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round">
  <circle cx="32" cy="38" r="18" stroke-width="1.8"/>
  <path d="M32 22 L32 38" stroke-width="3"/>
  <path d="M22 26 A16 16 0 1 0 42 26" stroke-width="2"/>
  <circle cx="32" cy="58" r="2.5" fill="currentColor" opacity="0.85" stroke="none"/>
</svg>`;

const _SVG_VINYL = `<svg viewBox="0 0 64 64" fill="none" stroke="currentColor">
  <circle cx="32" cy="32" r="29" stroke-width="1.5"/>
  <circle cx="32" cy="32" r="23" stroke-width="0.8" opacity="0.5"/>
  <circle cx="32" cy="32" r="17" stroke-width="0.8" opacity="0.4"/>
  <circle cx="32" cy="32" r="11" stroke-width="0.8" opacity="0.3"/>
  <circle cx="32" cy="32" r="5"  fill="currentColor" opacity="0.18" stroke="none"/>
  <circle cx="32" cy="32" r="2.5" stroke-width="1.5"/>
</svg>`;

const _SVG_CHECK = `<svg viewBox="0 0 64 64" fill="none" stroke="currentColor">
  <circle cx="32" cy="32" r="28" stroke-width="1.5"/>
  <polyline points="20 32 28 41 45 23" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`;

const _ICO_MIC = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2a3 3 0 0 0-3 3v6a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3z"/><path d="M19 10v1a7 7 0 0 1-14 0v-1"/><line x1="12" y1="19" x2="12" y2="23"/><line x1="8" y1="23" x2="16" y2="23"/></svg>`;

const _ICO_CHECK = `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>`;

// ── Wizard open / close ────────────────────────────────────────────────────────

function openCalibrationWizard() {
  _wiz.step = WIZ_STEPS.SELECT;
  _wiz.capturing = false;
  _wiz.captureDuration = 6;

  // Pre-fill from current amplifier inputs (use first visible input or manual)
  const cfg = _calibrationState.cfg;
  const allInputs = Array.isArray(cfg?.amplifier?.inputs)
    ? cfg.amplifier.inputs.filter(i => i && i.visible !== false)
    : [];

  if (allInputs.length > 0) {
    _wiz.inputKey   = String(allInputs[0].id || '');
    _wiz.inputLabel = allInputs[0].logical_name || `Input ${allInputs[0].id}`;
  } else {
    _wiz.inputKey   = '__manual__';
    _wiz.inputLabel = 'Manual';
  }

  _wiz.isPhono = _isVinylLabel(_wiz.inputLabel, _wiz.inputKey);
  _wizLoadInputState();

  const overlay = document.getElementById('cal-wiz-overlay');
  if (overlay) overlay.classList.add('open');
  _wizRender();
}

function closeCalibrationWizard() {
  const overlay = document.getElementById('cal-wiz-overlay');
  if (overlay) overlay.classList.remove('open');
  _wiz.step = 0;
  renderCalibrationSummary();
}

function _wizLoadInputState() {
  const slot = _calibrationState.byInput[_wiz.inputKey];
  _wiz.off   = slot?.off   || null;
  _wiz.on    = slot?.on    || null;
  _wiz.vinyl = slot?.vinyl_transition || null;
}

// ── Wizard navigation ──────────────────────────────────────────────────────────

function _wizNextStep() {
  switch (_wiz.step) {
    case WIZ_STEPS.SELECT:  return WIZ_STEPS.OFF;
    case WIZ_STEPS.OFF:     return WIZ_STEPS.ON;
    case WIZ_STEPS.ON:      return _wiz.isPhono ? WIZ_STEPS.VINYL : WIZ_STEPS.ANOTHER;
    case WIZ_STEPS.VINYL:   return WIZ_STEPS.ANOTHER;
    default:                return WIZ_STEPS.SUMMARY;
  }
}

function _wizPrevStep() {
  switch (_wiz.step) {
    case WIZ_STEPS.OFF:     return WIZ_STEPS.SELECT;
    case WIZ_STEPS.ON:      return WIZ_STEPS.OFF;
    case WIZ_STEPS.VINYL:   return WIZ_STEPS.ON;
    case WIZ_STEPS.ANOTHER: return _wiz.isPhono ? WIZ_STEPS.VINYL : WIZ_STEPS.ON;
    case WIZ_STEPS.SUMMARY: return WIZ_STEPS.ANOTHER;
    default:                return WIZ_STEPS.SELECT;
  }
}

function _wizCanNext() {
  if (_wiz.capturing) return false;
  switch (_wiz.step) {
    case WIZ_STEPS.OFF:  return _wiz.off !== null;
    case WIZ_STEPS.ON:   return _wiz.on  !== null;
    default:             return true;
  }
}

function wizNext() {
  if (_wiz.capturing) return;

  if (_wiz.step === WIZ_STEPS.SELECT) {
    const sel = document.getElementById('wiz-input-select');
    const cb  = document.getElementById('wiz-phono-cb');
    if (sel) {
      _wiz.inputKey   = sel.value || '__manual__';
      _wiz.inputLabel = (sel.options[sel.selectedIndex]?.textContent || '').trim() || _wiz.inputKey;
    }
    if (cb) _wiz.isPhono = cb.checked;
    _wizLoadInputState();
  }

  if (_wiz.step === WIZ_STEPS.ON || _wiz.step === WIZ_STEPS.VINYL) {
    _wizCommit();
  }

  _wiz.step = _wizNextStep();
  _wizRender();
}

function wizBack() {
  if (_wiz.capturing) return;
  if (_wiz.step === WIZ_STEPS.SELECT) { closeCalibrationWizard(); return; }
  _wiz.step = _wizPrevStep();
  _wizRender();
}

function _wizCalibrateAnother() {
  _wiz.step     = WIZ_STEPS.SELECT;
  _wiz.off      = null;
  _wiz.on       = null;
  _wiz.vinyl    = null;
  _wiz.capturing = false;

  const cfg = _calibrationState.cfg;
  const allInputs = Array.isArray(cfg?.amplifier?.inputs)
    ? cfg.amplifier.inputs.filter(i => i && i.visible !== false)
    : [];
  if (allInputs.length > 0) {
    _wiz.inputKey   = String(allInputs[0].id || '');
    _wiz.inputLabel = allInputs[0].logical_name || `Input ${allInputs[0].id}`;
    _wiz.isPhono    = _isVinylLabel(_wiz.inputLabel, _wiz.inputKey);
  }

  _wizRender();
}

function _wizGoSummary() {
  _wiz.step = WIZ_STEPS.SUMMARY;
  _wizRender();
}

function _wizCommit() {
  const key = _wiz.inputKey;
  if (!_calibrationState.byInput[key]) {
    _calibrationState.byInput[key] = { off: null, on: null, vinyl_transition: null };
  }
  _calibrationState.byInput[key].off              = _wiz.off;
  _calibrationState.byInput[key].on               = _wiz.on;
  _calibrationState.byInput[key].vinyl_transition  = _wiz.vinyl;

  _calibrationState.off             = _wiz.off;
  _calibrationState.on              = _wiz.on;
  _calibrationState.vinylTransition = _wiz.vinyl;

  const rec = calibrationRecommendation(_wiz.off, _wiz.on, _wiz.vinyl);
  if (rec && rec.ok) {
    if (rec.detectorThreshold != null) _rset('inp-silence', rec.detectorThreshold.toFixed(4));
    if (rec.vuThreshold != null)       _rset('rec-vu-silence-threshold', rec.vuThreshold.toFixed(4));
  }
}

function _wizSaveAndClose() {
  closeCalibrationWizard();
  toast('Calibration updated. Click "Save & Restart Services" to persist.', false);
}

// ── Wizard input/checkbox change handlers ──────────────────────────────────────

function _wizInputChanged() {
  const sel = document.getElementById('wiz-input-select');
  const cb  = document.getElementById('wiz-phono-cb');
  if (!sel || !cb) return;
  const key   = sel.value || '__manual__';
  const label = (sel.options[sel.selectedIndex]?.textContent || '').trim();
  cb.checked = _isVinylLabel(label, key);
  _wiz.isPhono = cb.checked;
  _wizRenderHeader();
}

function _wizPhonoCbChange(checkbox) {
  _wiz.isPhono = checkbox.checked;
  _wizRenderHeader();
}

// ── Capture functions ──────────────────────────────────────────────────────────

async function _wizCapture(kind) {
  if (_wiz.capturing) return;
  _wiz.capturing = true;
  _wizRenderBodyContent();
  _wizRenderFooter();

  const capSecs = _wiz.captureDuration;
  _wizStartProgress(capSecs);

  try {
    const res = await fetch('/api/calibration/vu-sample', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ seconds: capSecs }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      toast(body.error || 'Capture failed.', true);
    } else {
      const sample = _normalizeCalibrationSample(body);
      _wiz[kind] = sample;
      if (!_calibrationState.byInput[_wiz.inputKey]) {
        _calibrationState.byInput[_wiz.inputKey] = { off: null, on: null, vinyl_transition: null };
      }
      _calibrationState.byInput[_wiz.inputKey][kind] = sample;
      _calibrationState[kind] = sample;
    }
  } catch {
    toast('Capture failed.', true);
  }

  _wiz.capturing = false;
  _wizRenderBodyContent();
  _wizRenderFooter();
}

async function _wizCaptureVinyl() {
  if (_wiz.capturing) return;
  _wiz.capturing = true;
  _wizRenderBodyContent();
  _wizRenderFooter();

  const capSecs = Math.max(12, _wiz.captureDuration * 3);
  _wizStartProgress(capSecs);

  try {
    const res = await fetch('/api/calibration/vu-sequence', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ seconds: capSecs }),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) {
      toast(body.error || 'Capture failed.', true);
    } else {
      const transition = analyzeVinylTransitionSequence(body);
      if (!transition) {
        toast('Sample too short or no clear transition found. Try again.', true);
      } else {
        _wiz.vinyl = transition;
        if (!_calibrationState.byInput[_wiz.inputKey]) {
          _calibrationState.byInput[_wiz.inputKey] = { off: null, on: null, vinyl_transition: null };
        }
        _calibrationState.byInput[_wiz.inputKey].vinyl_transition = transition;
        _calibrationState.vinylTransition = transition;
      }
    }
  } catch {
    toast('Capture failed.', true);
  }

  _wiz.capturing = false;
  _wizRenderBodyContent();
  _wizRenderFooter();
}

function _wizStartProgress(capSecs) {
  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      const bar = document.getElementById('wiz-prog-bar');
      if (bar) {
        bar.style.transition = `width ${capSecs}s linear`;
        bar.style.width = '100%';
      }
    });
  });
}

// ── Wizard render ──────────────────────────────────────────────────────────────

function _wizRender() {
  _wizRenderHeader();
  _wizRenderBodyContent();
  _wizRenderFooter();
}

function _wizRenderHeader() {
  const el = document.getElementById('cal-wiz-step-indicator');
  if (!el) return;

  const stepNums = [WIZ_STEPS.SELECT, WIZ_STEPS.OFF, WIZ_STEPS.ON];
  if (_wiz.isPhono) stepNums.push(WIZ_STEPS.VINYL);
  stepNums.push(WIZ_STEPS.SUMMARY);

  const labels = {
    [WIZ_STEPS.SELECT]:  'Input',
    [WIZ_STEPS.OFF]:     'OFF',
    [WIZ_STEPS.ON]:      'ON',
    [WIZ_STEPS.VINYL]:   'Vinyl',
    [WIZ_STEPS.SUMMARY]: 'Summary',
  };

  const cur = _wiz.step;
  let html = '';

  for (let i = 0; i < stepNums.length; i++) {
    const s = stepNums[i];
    const isDone   = cur > s || cur === WIZ_STEPS.ANOTHER;
    const isActive = cur === s;
    const cls = isDone ? 'done' : isActive ? 'active' : '';

    if (i > 0) {
      const lineDone = cur > stepNums[i - 1] || cur === WIZ_STEPS.ANOTHER;
      html += `<div class="cal-wiz-line${lineDone ? ' done' : ''}"></div>`;
    }

    html += `<div class="cal-wiz-dot ${cls}" title="${labels[s]}">`;
    if (isDone) html += _ICO_CHECK;
    else        html += String(i + 1);
    html += `</div>`;
  }

  el.innerHTML = html;
}

function _wizRenderBodyContent() {
  const el = document.getElementById('cal-wiz-body');
  if (!el) return;
  switch (_wiz.step) {
    case WIZ_STEPS.SELECT:  el.innerHTML = _wizStep1(); break;
    case WIZ_STEPS.OFF:     el.innerHTML = _wizStep2(); break;
    case WIZ_STEPS.ON:      el.innerHTML = _wizStep3(); break;
    case WIZ_STEPS.VINYL:   el.innerHTML = _wizStep4(); break;
    case WIZ_STEPS.ANOTHER: el.innerHTML = _wizStep5(); break;
    case WIZ_STEPS.SUMMARY: el.innerHTML = _wizStep6(); break;
  }
}

function _wizRenderFooter() {
  const el = document.getElementById('cal-wiz-footer');
  if (!el) return;

  const dis = _wiz.capturing ? ' disabled' : '';
  const s   = _wiz.step;

  if (s === WIZ_STEPS.ANOTHER) {
    el.innerHTML = `<button class="btn-secondary"${dis} onclick="wizBack()">Back</button><div></div>`;
    return;
  }

  if (s === WIZ_STEPS.SUMMARY) {
    el.innerHTML = `<button class="btn-secondary"${dis} onclick="wizBack()">Back</button><button class="btn-save" onclick="_wizSaveAndClose()" style="padding:9px 22px">Save &amp; Close</button>`;
    return;
  }

  const canNext  = _wizCanNext();
  const nextDis  = (canNext && !_wiz.capturing) ? '' : ' disabled';
  const nextLabel = s === WIZ_STEPS.VINYL ? 'Skip / Continue' : 'Continue';

  el.innerHTML = `
    <button class="btn-secondary"${dis} onclick="wizBack()">${s === WIZ_STEPS.SELECT ? 'Cancel' : 'Back'}</button>
    <button class="btn-secondary" style="background:var(--accent-dim);border-color:var(--accent);color:var(--accent)"${nextDis} onclick="wizNext()">${nextLabel}</button>
  `;
}

// ── Wizard step content ────────────────────────────────────────────────────────

function _wizStep1() {
  const cfg = _calibrationState.cfg;
  const allInputs = Array.isArray(cfg?.amplifier?.inputs)
    ? cfg.amplifier.inputs.filter(i => i && i.visible !== false)
    : [];

  let selectHtml;
  if (allInputs.length > 0) {
    selectHtml = `<select id="wiz-input-select" class="cal-wiz-select" onchange="_wizInputChanged()">`;
    for (const inp of allInputs) {
      const key = String(inp.id || '');
      const label = inp.logical_name || `Input ${inp.id}`;
      const sel = key === _wiz.inputKey ? ' selected' : '';
      selectHtml += `<option value="${_esc(key)}"${sel}>${_esc(label)}</option>`;
    }
    selectHtml += `</select>`;
  } else {
    selectHtml = `<div class="hint" style="padding:8px 10px;border:1px solid var(--border);border-radius:5px">No inputs configured in the Amplifier section. Data will be stored as "manual".</div>`;
  }

  const phonoChecked = _wiz.isPhono ? ' checked' : '';

  return `
    <div class="cal-wiz-illus">${_SVG_AMP}</div>
    <div class="cal-wiz-title">Choose an input to calibrate</div>
    <div class="cal-wiz-desc">Calibration measures the noise floor of each input at rest — the difference between the REC OUT/LINE OUT signal when the source is off versus on but idle. These measurements let the system detect source switches precisely and avoid false track identifications.</div>
    <div class="cal-wiz-field">
      <label class="cal-wiz-field-label"><span>Amplifier input</span></label>
      ${selectHtml}
    </div>
    <label class="cal-wiz-cb-row">
      <input type="checkbox" id="wiz-phono-cb"${phonoChecked} onchange="_wizPhonoCbChange(this)">
      <div class="cal-wiz-cb-text">
        <div class="cb-label">This is a phono stage / turntable input</div>
        <div class="cb-hint">Enables a step to capture vinyl track transitions for precise inter-track gap detection.</div>
      </div>
    </label>
  `;
}

function _wizStep2() {
  const isPhono   = _wiz.isPhono;
  const capturing = _wiz.capturing;
  const illus     = isPhono ? _SVG_RCA_DISC : _SVG_POWER_OFF;

  const instrHtml = isPhono
    ? `Switch your amplifier to <b>${_esc(_wiz.inputLabel)}</b>. Unplug the RCA cables from the phono stage (or switch it off if available). Make sure the turntable motor is stopped.`
    : `Switch your amplifier to <b>${_esc(_wiz.inputLabel)}</b>. <b>Power off the source device</b> — put the CD player in standby or unplug it. Make sure no audio is playing.`;

  const resultHtml = _wiz.off
    ? `<div class="cal-wiz-result-ok"><span class="r-icon">${_ICO_CHECK}</span><span class="r-text">avg ${_wiz.off.avg_rms.toFixed(4)} · min ${_wiz.off.min_rms.toFixed(4)} · max ${_wiz.off.max_rms.toFixed(4)} · ${_wiz.off.samples} samples</span></div>`
    : '';

  const btnLabel = _wiz.off ? 'Re-capture' : 'Start measurement';
  const btnDis   = capturing ? ' disabled' : '';
  const progHtml = capturing ? `<div class="cal-wiz-prog"><div class="cal-wiz-prog-bar" id="wiz-prog-bar"></div></div><div class="cal-wiz-cap-hint">Measuring noise floor for ${_wiz.captureDuration}s…</div>` : '';

  return `
    <div class="cal-wiz-illus">${illus}</div>
    <div class="cal-wiz-title">Silence — source ${isPhono ? 'disconnected' : 'off'}</div>
    <div class="cal-wiz-desc">Captures the baseline noise floor when the source is inactive. This is the quietest state this input can be in.</div>
    <div class="cal-wiz-instr">${instrHtml}</div>
    <button class="cal-wiz-cap-btn"${btnDis} onclick="_wizCapture('off')">${_ICO_MIC} ${capturing ? 'Measuring…' : btnLabel}</button>
    ${progHtml}
    ${resultHtml}
  `;
}

function _wizStep3() {
  const isPhono   = _wiz.isPhono;
  const capturing = _wiz.capturing;

  const instrHtml = isPhono
    ? `Now <b>connect the RCA cables</b> back. The phono stage should be active but the turntable not playing anything.`
    : `Now <b>power on the source device</b> but do not play anything. Wait a few seconds for it to settle, then start the measurement.`;

  let resultHtml = '';
  if (_wiz.on) {
    resultHtml = `<div class="cal-wiz-result-ok"><span class="r-icon">${_ICO_CHECK}</span><span class="r-text">avg ${_wiz.on.avg_rms.toFixed(4)} · min ${_wiz.on.min_rms.toFixed(4)} · max ${_wiz.on.max_rms.toFixed(4)} · ${_wiz.on.samples} samples</span></div>`;
    if (_wiz.off) {
      const rec = calibrationRecommendation(_wiz.off, _wiz.on, _wiz.vinyl);
      if (rec && rec.ok) {
        resultHtml += `<div class="cal-wiz-rec-box"><b>Recommended thresholds</b><br>Source silence: <b>${rec.detectorThreshold.toFixed(4)}</b> &nbsp;·&nbsp; VU silence: <b>${rec.vuThreshold.toFixed(4)}</b>${rec.gap != null ? `<br><span style="color:var(--muted)">OFF/ON gap: ${rec.gap.toFixed(4)}</span>` : ''}</div>`;
      } else if (rec && !rec.ok) {
        resultHtml += `<div class="cal-wiz-warn-box">${_esc(rec.message)}</div>`;
      }
    }
  }

  const btnLabel = _wiz.on ? 'Re-capture' : 'Start measurement';
  const btnDis   = capturing ? ' disabled' : '';
  const progHtml = capturing ? `<div class="cal-wiz-prog"><div class="cal-wiz-prog-bar" id="wiz-prog-bar"></div></div><div class="cal-wiz-cap-hint">Measuring idle noise floor for ${_wiz.captureDuration}s…</div>` : '';

  return `
    <div class="cal-wiz-illus">${_SVG_POWER_ON}</div>
    <div class="cal-wiz-title">Silence — source on, no music</div>
    <div class="cal-wiz-desc">Captures the idle noise floor of the active source. The system uses the gap between OFF and ON to place the silence threshold precisely.</div>
    <div class="cal-wiz-instr">${instrHtml}</div>
    <button class="cal-wiz-cap-btn"${btnDis} onclick="_wizCapture('on')">${_ICO_MIC} ${capturing ? 'Measuring…' : btnLabel}</button>
    ${progHtml}
    ${resultHtml}
  `;
}

function _wizStep4() {
  const capturing = _wiz.capturing;
  const capSecs   = Math.max(12, _wiz.captureDuration * 3);

  let resultHtml = '';
  if (_wiz.vinyl) {
    const v = _wiz.vinyl;
    resultHtml = `<div class="cal-wiz-result-ok"><span class="r-icon">${_ICO_CHECK}</span><span class="r-text">gap ${v.gap_duration_secs.toFixed(2)}s · tail ${v.tail_avg_rms.toFixed(4)} · gap RMS ${v.gap_avg_rms.toFixed(4)} · attack ${v.attack_avg_rms.toFixed(4)}</span></div>`;
  }

  const btnLabel = _wiz.vinyl ? 'Re-capture' : `Start capture (~${capSecs}s)`;
  const btnDis   = capturing ? ' disabled' : '';
  const progHtml = capturing ? `<div class="cal-wiz-prog"><div class="cal-wiz-prog-bar" id="wiz-prog-bar"></div></div><div class="cal-wiz-cap-hint">Capturing vinyl transition for ${capSecs}s — let the track end naturally…</div>` : '';

  return `
    <div class="cal-wiz-illus">${_SVG_VINYL}</div>
    <div class="cal-wiz-title">Vinyl track transition <span style="font-weight:400;font-size:0.82rem;color:var(--muted)">(optional)</span></div>
    <div class="cal-wiz-desc">Captures the exact silence gap between vinyl tracks, enabling more precise inter-track detection. Skip this step if you are not calibrating a turntable or prefer the default.</div>
    <div class="cal-wiz-instr">Place the needle <b>~10–15 seconds before the end</b> of a track. Click <em>Start capture</em> then wait as the track finishes and the next one begins. The system detects the gap automatically.</div>
    <button class="cal-wiz-cap-btn"${btnDis} onclick="_wizCaptureVinyl()">${_ICO_MIC} ${capturing ? 'Measuring…' : btnLabel}</button>
    ${progHtml}
    ${resultHtml}
  `;
}

function _wizStep5() {
  return `
    <div class="cal-wiz-illus">${_SVG_CHECK}</div>
    <div class="cal-wiz-title">${_esc(_wiz.inputLabel)} calibrated</div>
    <div class="cal-wiz-desc">Calibration data for this input has been captured. Would you like to calibrate another input, or review the full summary?</div>
    <div class="cal-wiz-big-btns">
      <button class="cal-wiz-big-btn accent" onclick="_wizCalibrateAnother()">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
        Calibrate another input
      </button>
      <button class="cal-wiz-big-btn" onclick="_wizGoSummary()">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>
        View summary
      </button>
    </div>
  `;
}

function _wizStep6() {
  const cfg = _calibrationState.cfg;
  const allInputs = Array.isArray(cfg?.amplifier?.inputs)
    ? cfg.amplifier.inputs.filter(i => i && i.visible !== false)
    : [];

  const profiles = _calibrationState.byInput;
  const shown = new Set();
  const items = [];

  for (const [key, slot] of Object.entries(profiles)) {
    if (!slot || (!slot.off && !slot.on && !slot.vinyl_transition)) continue;
    const ampInp = allInputs.find(i => String(i.id) === key);
    shown.add(key);
    items.push({ key, label: ampInp?.logical_name || key, slot, measured: true });
  }
  for (const inp of allInputs) {
    const key = String(inp.id || '');
    if (shown.has(key)) continue;
    items.push({ key, label: inp.logical_name || `Input ${inp.id}`, slot: null, measured: false });
  }

  if (items.length === 0) {
    return `
      <div class="cal-wiz-illus">${_SVG_CHECK}</div>
      <div class="cal-wiz-title">Calibration complete</div>
      <div class="cal-wiz-desc">No amplifier inputs are configured. The system will use the global silence thresholds as fallback.</div>
      <div class="cal-wiz-save-note">Click <b>Save &amp; Close</b> then <b>Save &amp; Restart Services</b> on the main page to persist.</div>
    `;
  }

  const cardsHtml = items.map(item => {
    const { label, slot, measured, key } = item;
    const isPhono = _isVinylLabel(label, key);

    const badges = [];
    if (measured) badges.push(`<span class="cal-sc-badge measured">Measured</span>`);
    else          badges.push(`<span class="cal-sc-badge defaults">Defaults</span>`);
    if (isPhono)  badges.push(`<span class="cal-sc-badge phono">Phono</span>`);

    let valsHtml = '';
    if (measured && slot) {
      const rec = calibrationRecommendation(slot.off, slot.on, slot.vinyl_transition || null);
      if (rec && rec.ok) {
        valsHtml += `<div class="cal-wiz-sum-val"><span class="lbl">Source</span><span class="val">${rec.detectorThreshold.toFixed(4)}</span></div>`;
        valsHtml += `<div class="cal-wiz-sum-val"><span class="lbl">VU</span><span class="val">${rec.vuThreshold.toFixed(4)}</span></div>`;
        if (rec.gap != null) valsHtml += `<div class="cal-wiz-sum-val"><span class="lbl">OFF/ON gap</span><span class="val">${rec.gap.toFixed(4)}</span></div>`;
      } else {
        valsHtml = `<span class="hint">Incomplete — OFF and ON samples needed.</span>`;
      }
      if (slot.vinyl_transition && Number.isFinite(slot.vinyl_transition.gap_duration_secs)) {
        valsHtml += `<div class="cal-wiz-sum-val"><span class="lbl">Vinyl gap</span><span class="val">${slot.vinyl_transition.gap_duration_secs.toFixed(2)}s</span></div>`;
      }
    } else {
      const vu  = _rfloat('rec-vu-silence-threshold', 0.0095);
      const sil = _rfloat('inp-silence', 0.025);
      valsHtml  = `<div class="cal-wiz-sum-val"><span class="lbl">Source</span><span class="val">${sil.toFixed(4)}</span></div>`;
      valsHtml += `<div class="cal-wiz-sum-val"><span class="lbl">VU</span><span class="val">${vu.toFixed(4)}</span></div>`;
    }

    return `<div class="cal-wiz-sum-card${measured ? '' : ' is-default'}">
      <div class="cal-wiz-sum-head"><span class="cal-wiz-sum-name">${_esc(label)}</span>${badges.join('')}</div>
      <div class="cal-wiz-sum-vals">${valsHtml}</div>
    </div>`;
  }).join('');

  const hasDefaults = items.some(i => !i.measured);
  const noteHtml = hasDefaults
    ? `<p class="cal-wiz-sum-note">Inputs marked "Defaults" have not been calibrated yet. The system will use the global VU threshold as fallback for those inputs.</p>`
    : '';

  return `
    <div class="cal-wiz-title">Calibration summary</div>
    <div class="cal-wiz-desc">Review the thresholds for each input. These will be applied when you save.</div>
    <div class="cal-wiz-sum-grid">${cardsHtml}</div>
    ${noteHtml}
    <div class="cal-wiz-save-note">Click <b>Save &amp; Close</b>, then <b>Save &amp; Restart Services</b> on the main page to persist changes.</div>
  `;
}

// ── Event wiring ───────────────────────────────────────────────────────────────

document.getElementById('rec-chain')?.addEventListener('change', updateRecognitionUI);
document.addEventListener('DOMContentLoaded', () => {
  loadRecognitionPage();
});

// ── Mic Gain Wizard ────────────────────────────────────────────────────────────
// 4 steps: Device info → Play music prompt → Live RMS + gain adjust → Confirm

const MIC_STEPS = ['Device', 'Play Music', 'Adjust Gain', 'Confirm'];

let _mic = {
  step: 0,
  info: null,       // micGainInfoResponse from /api/mic-gain/info
  vuTimer: null,    // interval ID for live RMS polling
  vuSocket: '',     // filled from config
};

function openMicGainWizard() {
  _mic.step = 0;
  _mic.info = null;
  _stopMicVU();
  document.getElementById('mic-gain-overlay').classList.add('open');
  _micRenderStep();
}

function closeMicGainWizard() {
  _stopMicVU();
  document.getElementById('mic-gain-overlay').classList.remove('open');
}

function _stopMicVU() {
  if (_mic.vuTimer) {
    clearInterval(_mic.vuTimer);
    _mic.vuTimer = null;
  }
}

function _micStepIndicator() {
  const ind = document.getElementById('mic-gain-step-indicator');
  if (!ind) return;
  ind.innerHTML = MIC_STEPS.map((label, i) => {
    const cls = i < _mic.step ? 'cal-wiz-step done' : i === _mic.step ? 'cal-wiz-step active' : 'cal-wiz-step';
    return `<span class="${cls}">${i + 1}. ${label}</span>`;
  }).join('');
}

function _micRenderStep() {
  _micStepIndicator();
  const body   = document.getElementById('mic-gain-body');
  const footer = document.getElementById('mic-gain-footer');
  if (!body || !footer) return;

  switch (_mic.step) {
    case 0: _micStep0(body, footer); break;
    case 1: _micStep1(body, footer); break;
    case 2: _micStep2(body, footer); break;
    case 3: _micStep3(body, footer); break;
  }
}

// Step 0: load device info and display
function _micStep0(body, footer) {
  body.innerHTML = `<div class="cal-wiz-title">Capture Device</div>
    <div class="cal-wiz-desc">Loading device information…</div>`;
  footer.innerHTML = '';

  fetch('/api/mic-gain/info')
    .then(r => r.json())
    .then(info => {
      _mic.info = info;
      if (info.error && !info.card_num && info.card_num !== 0) {
        body.innerHTML = `<div class="cal-wiz-title">Capture Device</div>
          <div class="cal-wiz-desc" style="color:var(--error,#e05)">
            <b>Could not resolve capture device:</b><br>${_esc(info.error)}
          </div>
          <p class="cal-wiz-desc" style="margin-top:12px">
            Check your Audio Input configuration and ensure the USB sound card is connected.
          </p>`;
        footer.innerHTML = `<button class="btn-secondary" onclick="closeMicGainWizard()">Close</button>`;
        return;
      }
      body.innerHTML = `<div class="cal-wiz-title">Capture Device</div>
        <div class="cal-wiz-desc">This wizard will help you set the right input gain for track recognition. Only the <b>capture</b> (REC OUT) card is adjusted here.</div>
        <div class="cal-wiz-sum-grid" style="margin-top:16px">
          <div class="cal-wiz-sum-card">
            <div class="cal-wiz-sum-head"><span class="cal-wiz-sum-name">Device</span></div>
            <div class="cal-wiz-sum-vals">
              <div class="cal-wiz-sum-val"><span class="lbl">Name</span><span class="val">${_esc(info.device_name || info.device)}</span></div>
              <div class="cal-wiz-sum-val"><span class="lbl">ALSA</span><span class="val">${_esc(info.device)}</span></div>
              <div class="cal-wiz-sum-val"><span class="lbl">Control</span><span class="val">${_esc(info.control || '—')}</span></div>
              <div class="cal-wiz-sum-val"><span class="lbl">Current gain</span><span class="val" id="mic-cur-gain">${info.gain_pct !== undefined ? info.gain_pct + '%' : '—'}</span></div>
            </div>
          </div>
        </div>
        ${info.error ? `<p class="hint" style="color:var(--warn,#f90);margin-top:10px">Warning: ${_esc(info.error)}</p>` : ''}`;
      footer.innerHTML = `
        <button class="btn-secondary" onclick="closeMicGainWizard()">Cancel</button>
        <button class="btn-primary" onclick="_micNext()">Next →</button>`;
    })
    .catch(e => {
      body.innerHTML = `<div class="cal-wiz-title">Capture Device</div>
        <div class="cal-wiz-desc" style="color:var(--error,#e05)">Error: ${_esc(e.message)}</div>`;
      footer.innerHTML = `<button class="btn-secondary" onclick="closeMicGainWizard()">Close</button>`;
    });
}

// Step 1: instruct user to play music
function _micStep1(body, footer) {
  body.innerHTML = `<div class="cal-wiz-title">Play Music</div>
    <div class="cal-wiz-desc">
      Put on a record or play a CD at a <b>typical listening volume</b>. Make sure the amplifier is set to the physical input (Phono or CD).<br><br>
      When music is playing at normal volume, click <b>Next</b> to start monitoring the signal level.
    </div>`;
  footer.innerHTML = `
    <button class="btn-secondary" onclick="_micPrev()">← Back</button>
    <button class="btn-primary" onclick="_micNext()">Next →</button>`;
}

// Step 2: live RMS meter + gain +/- buttons
function _micStep2(body, footer) {
  _stopMicVU();
  body.innerHTML = `<div class="cal-wiz-title">Adjust Gain</div>
    <div class="cal-wiz-desc">
      Target RMS: <b>0.05 – 0.25</b>. Use the buttons to raise or lower the capture gain until the meter is in the green zone during music playback.
    </div>
    <div style="margin-top:20px;text-align:center">
      <div style="font-size:11px;color:var(--text-2,#888);margin-bottom:4px">Live RMS</div>
      <div id="mic-rms-bar-wrap" style="width:100%;height:18px;background:var(--surface-2,#222);border-radius:4px;overflow:hidden;margin-bottom:8px">
        <div id="mic-rms-bar" style="height:100%;width:0%;background:#4caf50;transition:width 0.15s"></div>
      </div>
      <div id="mic-rms-val" style="font-size:22px;font-weight:700;letter-spacing:0.02em;margin-bottom:16px">—</div>
      <div style="display:flex;gap:10px;justify-content:center;align-items:center">
        <button class="btn-secondary" onclick="_micAdjust('down')" style="font-size:18px;padding:6px 18px" title="Decrease gain by 5%">−</button>
        <span id="mic-gain-val" style="min-width:60px;text-align:center;font-size:16px;font-weight:600">${_mic.info ? _mic.info.gain_pct + '%' : '—'}</span>
        <button class="btn-secondary" onclick="_micAdjust('up')" style="font-size:18px;padding:6px 18px" title="Increase gain by 5%">+</button>
      </div>
      <div class="hint" style="margin-top:10px">Gain control: <b>${_mic.info ? _esc(_mic.info.control) : '—'}</b></div>
    </div>`;
  footer.innerHTML = `
    <button class="btn-secondary" onclick="_micPrev()">← Back</button>
    <button class="btn-primary" onclick="_micNext()">Next →</button>`;

  // Start live VU polling via existing calibration endpoint
  _mic.vuTimer = setInterval(() => {
    fetch('/api/calibration/vu-sample', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({seconds: 1}),
    })
      .then(r => r.json())
      .then(d => {
        const rms = d.avg_rms ?? 0;
        const valEl  = document.getElementById('mic-rms-val');
        const barEl  = document.getElementById('mic-rms-bar');
        if (!valEl) { _stopMicVU(); return; }
        valEl.textContent = rms.toFixed(4);
        // Map 0–0.4 to bar width; colour based on target zone
        const pct = Math.min(rms / 0.4 * 100, 100);
        barEl.style.width = pct + '%';
        if (rms < 0.05)       barEl.style.background = '#888';
        else if (rms <= 0.25) barEl.style.background = '#4caf50';
        else if (rms <= 0.35) barEl.style.background = '#f90';
        else                  barEl.style.background = '#e05';
      })
      .catch(() => {});
  }, 1200);
}

function _micAdjust(dir) {
  fetch('/api/mic-gain/adjust', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({direction: dir}),
  })
    .then(r => r.json())
    .then(d => {
      if (d.error) { toast('Error: ' + d.error); return; }
      const el = document.getElementById('mic-gain-val');
      if (el) el.textContent = d.gain_pct + '%';
      if (_mic.info) _mic.info.gain_pct = d.gain_pct;
    })
    .catch(e => toast('Error: ' + e.message));
}

// Step 3: confirm and persist
function _micStep3(body, footer) {
  _stopMicVU();
  const gain = _mic.info ? _mic.info.gain_pct : '—';
  body.innerHTML = `<div class="cal-wiz-title">Save Settings</div>
    <div class="cal-wiz-desc">
      The capture gain is currently set to <b>${gain}%</b>.<br><br>
      Click <b>Save &amp; Close</b> to persist this setting with <code>alsactl store</code> so it survives reboots.
    </div>`;
  footer.innerHTML = `
    <button class="btn-secondary" onclick="_micPrev()">← Back</button>
    <button class="btn-primary" id="mic-save-btn" onclick="_micSave()">Save &amp; Close</button>`;
}

function _micSave() {
  const btn = document.getElementById('mic-save-btn');
  if (btn) { btn.disabled = true; btn.textContent = 'Saving…'; }
  fetch('/api/mic-gain/store', {method: 'POST'})
    .then(r => r.json())
    .then(d => {
      if (d.error) { toast('Error: ' + d.error); if (btn) { btn.disabled = false; btn.textContent = 'Save & Close'; } return; }
      toast('Gain settings saved.');
      closeMicGainWizard();
    })
    .catch(e => { toast('Error: ' + e.message); if (btn) { btn.disabled = false; btn.textContent = 'Save & Close'; } });
}

function _micNext() {
  _stopMicVU();
  if (_mic.step < MIC_STEPS.length - 1) { _mic.step++; _micRenderStep(); }
}

function _micPrev() {
  _stopMicVU();
  if (_mic.step > 0) { _mic.step--; _micRenderStep(); }
}

'use strict';

// ── Shared calibration state ───────────────────────────────────────────────────

const _calibrationState = {
  off: null,
  on: null,
  vinylTransition: null,
  cfg: null,
  byInput: {},
};

// ── Data model helpers ─────────────────────────────────────────────────────────

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

function _phonoInputsFromConfig(cfg) {
  const allInputs = Array.isArray(cfg?.amplifier?.inputs)
    ? cfg.amplifier.inputs.filter(i => i && i.visible !== false)
    : [];
  const turntableInputIDs = new Set();
  const devices = Array.isArray(cfg?.amplifier?.connected_devices) ? cfg.amplifier.connected_devices : [];
  for (const dev of devices) {
    if (!dev || !dev.is_turntable) continue;
    const ids = Array.isArray(dev.input_ids) ? dev.input_ids : [];
    for (const id of ids) turntableInputIDs.add(String(id));
  }
  if (turntableInputIDs.size > 0) {
    const mapped = allInputs.filter(i => turntableInputIDs.has(String(i.id || '')));
    if (mapped.length > 0) return mapped;
  }
  return allInputs.filter(i => _isVinylLabel(i.logical_name || '', String(i.id || '')));
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

  const cfg = _calibrationState.cfg;
  const phonoInputs = _phonoInputsFromConfig(cfg);

  if (phonoInputs.length > 0) {
    _wiz.inputKey   = String(phonoInputs[0].id || '');
    _wiz.inputLabel = phonoInputs[0].logical_name || `Input ${phonoInputs[0].id}`;
  } else {
    _wiz.inputKey   = '__manual__';
    _wiz.inputLabel = 'Phono';
  }

  _wiz.isPhono = true;
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
    if (sel) {
      _wiz.inputKey   = sel.value || '__manual__';
      _wiz.inputLabel = (sel.options[sel.selectedIndex]?.textContent || '').trim() || _wiz.inputKey;
    }
    _wiz.isPhono = true;
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
  const phonoInputs = _phonoInputsFromConfig(cfg);
  if (phonoInputs.length > 0) {
    _wiz.inputKey   = String(phonoInputs[0].id || '');
    _wiz.inputLabel = phonoInputs[0].logical_name || `Input ${phonoInputs[0].id}`;
    _wiz.isPhono    = true;
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

async function _wizSaveAndClose() {
  closeCalibrationWizard();
  try {
    const r = await fetch('/api/config');
    if (!r.ok) throw new Error('load failed');
    const fullCfg = await r.json();
    fullCfg.advanced = {
      ...(fullCfg.advanced ?? {}),
      calibration_profiles: _normalizeCalibrationProfiles(_calibrationState.byInput),
    };
    const sr = await fetch('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(fullCfg),
    });
    const sd = await sr.json().catch(() => ({}));
    if (!sr.ok) {
      toast('Calibration updated — save failed: ' + (sd.error || sr.status), true);
    } else {
      toast('Calibration saved. Recommended thresholds will apply after "Save & Restart Services".');
    }
  } catch {
    toast('Calibration updated. Click "Save & Restart Services" to persist.', false);
  }
}

// ── Wizard input/checkbox change handlers ──────────────────────────────────────

function _wizInputChanged() {
  _wiz.isPhono = true;
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
  const phonoInputs = _phonoInputsFromConfig(cfg);

  let selectHtml;
  if (phonoInputs.length > 0) {
    selectHtml = `<select id="wiz-input-select" class="cal-wiz-select" onchange="_wizInputChanged()">`;
    for (const inp of phonoInputs) {
      const key = String(inp.id || '');
      const label = inp.logical_name || `Input ${inp.id}`;
      const sel = key === _wiz.inputKey ? ' selected' : '';
      selectHtml += `<option value="${_esc(key)}"${sel}>${_esc(label)}</option>`;
    }
    selectHtml += `</select>`;
  } else {
    selectHtml = `<div class="hint" style="padding:8px 10px;border:1px solid var(--border);border-radius:5px">No Phono input found in the Amplifier section. Add/rename one input as Phono to use vinyl calibration.</div>`;
  }

  return `
    <div class="cal-wiz-illus">${_SVG_AMP}</div>
    <div class="cal-wiz-title">Choose a Phono input</div>
    <div class="cal-wiz-desc">This wizard is dedicated to vinyl calibration. It measures OFF/ON noise floor and optional vinyl track transitions for more precise inter-track detection.</div>
    <div class="cal-wiz-field">
      <label class="cal-wiz-field-label"><span>Phono input</span></label>
      ${selectHtml}
    </div>
  `;
}

function _wizStep2() {
  const capturing = _wiz.capturing;
  const instrHtml = `Switch your amplifier to <b>${_esc(_wiz.inputLabel)}</b>. Unplug the RCA cables from the phono stage (or switch it off if available). Make sure the turntable motor is stopped.`;

  const resultHtml = _wiz.off
    ? `<div class="cal-wiz-result-ok"><span class="r-icon">${_ICO_CHECK}</span><span class="r-text">avg ${_wiz.off.avg_rms.toFixed(4)} · min ${_wiz.off.min_rms.toFixed(4)} · max ${_wiz.off.max_rms.toFixed(4)} · ${_wiz.off.samples} samples</span></div>`
    : '';

  const btnLabel = _wiz.off ? 'Re-capture' : 'Start measurement';
  const btnDis   = capturing ? ' disabled' : '';
  const progHtml = capturing ? `<div class="cal-wiz-prog"><div class="cal-wiz-prog-bar" id="wiz-prog-bar"></div></div><div class="cal-wiz-cap-hint">Measuring noise floor for ${_wiz.captureDuration}s…</div>` : '';

  return `
    <div class="cal-wiz-illus">${_SVG_RCA_DISC}</div>
    <div class="cal-wiz-title">Silence — phono disconnected/off</div>
    <div class="cal-wiz-desc">Captures the baseline noise floor when the source is inactive. This is the quietest state this input can be in.</div>
    <div class="cal-wiz-instr">${instrHtml}</div>
    <button class="cal-wiz-cap-btn"${btnDis} onclick="_wizCapture('off')">${_ICO_MIC} ${capturing ? 'Measuring…' : btnLabel}</button>
    ${progHtml}
    ${resultHtml}
  `;
}

function _wizStep3() {
  const capturing = _wiz.capturing;

  const instrHtml = `Now <b>connect the RCA cables</b> back. The phono stage should be active but the turntable not playing anything.`;

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
  const phonoInputs = _phonoInputsFromConfig(cfg);

  const profiles = _calibrationState.byInput;
  const shown = new Set();
  const items = [];

  for (const [key, slot] of Object.entries(profiles)) {
    if (!slot || (!slot.off && !slot.on && !slot.vinyl_transition)) continue;
    const ampInp = phonoInputs.find(i => String(i.id) === key);
    if (!ampInp) continue;
    shown.add(key);
    items.push({ key, label: ampInp?.logical_name || key, slot, measured: true });
  }
  for (const inp of phonoInputs) {
    const key = String(inp.id || '');
    if (shown.has(key)) continue;
    items.push({ key, label: inp.logical_name || `Input ${inp.id}`, slot: null, measured: false });
  }

  if (items.length === 0) {
    return `
      <div class="cal-wiz-illus">${_SVG_CHECK}</div>
      <div class="cal-wiz-title">Calibration complete</div>
      <div class="cal-wiz-desc">No Phono input is configured. The system will keep using global silence thresholds as fallback.</div>
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
      if (slot.vinyl_transition) {
        if (Number.isFinite(slot.vinyl_transition.gap_avg_rms) && slot.vinyl_transition.gap_avg_rms > 0) {
          valsHtml += `<div class="cal-wiz-sum-val"><span class="lbl">Groove noise</span><span class="val">${slot.vinyl_transition.gap_avg_rms.toFixed(5)}</span></div>`;
        }
        if (Number.isFinite(slot.vinyl_transition.gap_duration_secs)) {
          valsHtml += `<div class="cal-wiz-sum-val"><span class="lbl">Vinyl gap</span><span class="val">${slot.vinyl_transition.gap_duration_secs.toFixed(2)}s</span></div>`;
        }
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

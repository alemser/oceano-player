'use strict';

// ── Calibration summary (main page) ───────────────────────────────────────────

let _effectiveInputRecognitionPolicyMap = new Map();

async function loadEffectiveInputRecognitionPolicies() {
  _effectiveInputRecognitionPolicyMap = new Map();
  try {
    const r = await fetch('/api/amplifier/input-recognition-policies');
    if (!r.ok) return;
    const body = await r.json();
    const items = Array.isArray(body?.items) ? body.items : [];
    for (const it of items) {
      const id = String(it?.input_id || '').trim();
      if (!id) continue;
      const pol = String(it?.effective_policy || '').trim().toLowerCase();
      if (pol === 'library' || pol === 'display_only' || pol === 'off') {
        _effectiveInputRecognitionPolicyMap.set(id, pol);
      }
    }
  } catch {
    // Best effort only: calibration page still renders without policy badges.
  }
}

function renderCalibrationSummary() {
  const container = document.getElementById('cal-summary-grid');
  if (!container) return;

  const cfg = _calibrationState.cfg;
  const allInputs = Array.isArray(cfg?.amplifier?.inputs)
    ? cfg.amplifier.inputs.filter(i => i && i.visible !== false)
    : [];
  const phonoInputs = (typeof _phonoInputsFromConfig === 'function')
    ? _phonoInputsFromConfig(cfg)
    : allInputs.filter(i => _isVinylLabel(i.logical_name || '', String(i.id || '')));

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
    container.innerHTML = '<div class="hint" style="padding:4px 0 2px">No Phono input configured. Set one in Amplifier settings to run vinyl calibration.</div>';
    return;
  }

  container.innerHTML = items.map(item => {
    const { label, slot, measured, key } = item;
    const isPhono = phonoInputs.some(i => String(i.id) === String(key));
    const policy = _effectiveInputRecognitionPolicyMap.get(String(key)) || 'off';

    const badges = [];
    if (measured) badges.push(`<span class="cal-sc-badge measured">Measured</span>`);
    else          badges.push(`<span class="cal-sc-badge defaults">Defaults</span>`);
    if (isPhono)  badges.push(`<span class="cal-sc-badge phono">Phono</span>`);
    if (policy === 'library') badges.push(`<span class="cal-sc-badge measured">Recognition: Library</span>`);
    else if (policy === 'display_only') badges.push(`<span class="cal-sc-badge defaults">Recognition: Display only</span>`);
    else badges.push(`<span class="cal-sc-badge defaults">Recognition: Off</span>`);

    let valsHtml = '';
    if (measured && slot) {
      const rec = calibrationRecommendation(slot.off, slot.on, null);
      if (rec && rec.ok) {
        valsHtml += `<div class="cal-sc-val"><span class="lbl">Source</span><span class="val">${rec.detectorThreshold.toFixed(4)}</span></div>`;
        valsHtml += `<div class="cal-sc-val"><span class="lbl">VU</span><span class="val">${rec.vuThreshold.toFixed(4)}</span></div>`;
        if (rec.gap != null) valsHtml += `<div class="cal-sc-val"><span class="lbl">OFF/ON gap</span><span class="val">${rec.gap.toFixed(4)}</span></div>`;
      } else {
        valsHtml += `<span class="hint" style="align-self:center">Incomplete — run wizard again to capture OFF and ON.</span>`;
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
  const usesACR = chain !== 'shazam_only' && chain !== 'audd_only';
  const usesAudD = chain === 'acrcloud_first' || chain === 'shazam_first' ||
    chain === 'audd_first' || chain === 'audd_only';
  const group = document.getElementById('acrcloud-config-group');
  const hint  = document.getElementById('acrcloud-config-hint');
  if (group) group.style.display = usesACR ? '' : 'none';
  if (hint)  hint.style.display  = usesACR ? '' : 'none';

  const auddG = document.getElementById('audd-config-group');
  const auddInp = document.getElementById('rec-audd-token');
  const auddHint = document.getElementById('audd-config-hint');
  if (auddG) auddG.style.display = usesAudD ? '' : 'none';
  if (auddInp) auddInp.disabled = !usesAudD;
  if (auddHint) {
    auddHint.textContent = usesAudD
      ? 'Optional. Official music recognition API — docs.audd.io. Included in the chain when non-empty and the selected order allows AudD.'
      : 'AudD is not used with the selected chain.';
  }
}

function _tuningPresetValues(preset) {
  switch (preset) {
    case 'standard':
      return { interval: 8, grace: 45, window: 180, sigCal: 2, sigUncal: 3, earlyCheck: 20, guardBypass: 20, pessimism: 0.75, restoreSeek: 60 };
    case 'calibrated':
      return { interval: 8, grace: 25, window: 180, sigCal: 2, sigUncal: 3, earlyCheck: 35, guardBypass: 20, pessimism: 0.75, restoreSeek: 60 };
    case 'balanced':
      return { interval: 7, grace: 35, window: 180, sigCal: 2, sigUncal: 3, earlyCheck: 25, guardBypass: 20, pessimism: 0.75, restoreSeek: 55 };
    case 'gapless':
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

async function importRMSBaseline(overwrite) {
  const r = await fetch('/api/recognition/rms-learning/import-default', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ overwrite: !!overwrite }),
  });
  const body = await r.json().catch(() => ({}));
  if (!r.ok) {
    const msg = body?.error || `Import failed (HTTP ${r.status})`;
    const err = new Error(msg);
    err.status = r.status;
    throw err;
  }
  return body;
}

async function importBaselineAndEnableAutonomy() {
  const first = window.confirm(
    'Import starter baseline for RMS learning?\n\n' +
    'This is recommended for a new/empty setup. The system will still auto-calibrate based on your local playback.'
  );
  if (!first) return;

  const btn = document.getElementById('rec-import-rms-baseline-btn');
  if (btn) { btn.disabled = true; btn.textContent = 'Importing…'; }
  try {
    try {
      await importRMSBaseline(false);
    } catch (err) {
      if (err?.status !== 409) throw err;
      const overwrite = window.confirm(
        'Existing RMS learning data was found.\n\n' +
        'Do you want to overwrite it with the starter baseline?'
      );
      if (!overwrite) {
        toast('Import cancelled (existing data kept).');
        return;
      }
      await importRMSBaseline(true);
    }

    const autoBox = document.getElementById('rec-autonomous-calibration-enabled');
    const telemBox = document.getElementById('rec-telemetry-nudges-enabled');
    const rmsEn = document.getElementById('rec-rms-learning-enabled');
    const rmsApply = document.getElementById('rec-rms-learning-apply');
    if (autoBox) autoBox.checked = true;
    if (telemBox) telemBox.checked = true;
    if (rmsEn) rmsEn.checked = true;
    if (rmsApply) rmsApply.checked = true;
    if (!_rval('rec-rms-min-silence')) _rset('rec-rms-min-silence', 400);
    if (!_rval('rec-rms-min-music')) _rset('rec-rms-min-music', 400);
    if (!_rval('rec-rms-persist-secs')) _rset('rec-rms-persist-secs', 120);

    toast('Baseline imported. Saving autonomous settings…');
    await saveRecognitionPage();
    await loadRecognitionPage();
    if (typeof refreshRMSLearningSnapshot === 'function') {
      await refreshRMSLearningSnapshot('rec-rms-learning-summary');
    }
  } catch (err) {
    toast(err?.message || 'Failed to import starter baseline.', true);
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = 'Import starter baseline (recommended for new setup)'; }
  }
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
  _rset('rec-audd-token',       cfg.recognition?.audd_api_token          ?? '');
  const shazamEn = document.getElementById('rec-shazam-enabled');
  if (shazamEn) shazamEn.checked = (cfg.recognition?.shazam_recognizer_enabled !== false);
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
  const autonomous = cfg.advanced?.autonomous_calibration;
  const autoBox = document.getElementById('rec-autonomous-calibration-enabled');
  if (autoBox) autoBox.checked = (autonomous == null || autonomous.enabled == null) ? true : !!autonomous.enabled;
  const telemetryNudges = cfg.advanced?.r3_telemetry_nudges;
  const telemetryBox = document.getElementById('rec-telemetry-nudges-enabled');
  if (telemetryBox) telemetryBox.checked = !!telemetryNudges?.enabled;
  _rset('rec-telemetry-lookback', telemetryNudges?.lookback_days ?? '');
  _rset('rec-telemetry-min-pairs', telemetryNudges?.min_followup_pairs ?? '');
  _rset('rec-telemetry-baseline-fp', telemetryNudges?.baseline_false_positive_ratio ?? '');
  _rset('rec-telemetry-max-silence', telemetryNudges?.max_silence_threshold_delta ?? '');
  _rset('rec-telemetry-max-pess', telemetryNudges?.max_duration_pessimism_delta ?? '');
  const rms = cfg.advanced?.rms_percentile_learning;
  const rmsEn = document.getElementById('rec-rms-learning-enabled');
  if (rmsEn) rmsEn.checked = (rms == null || rms.enabled == null) ? true : !!rms.enabled;
  const rmsAp = document.getElementById('rec-rms-learning-apply');
  if (rmsAp) rmsAp.checked = !!rms?.autonomous_apply;
  _rset('rec-rms-min-silence', rms?.min_silence_samples ?? '');
  _rset('rec-rms-min-music', rms?.min_music_samples ?? '');
  _rset('rec-rms-persist-secs', rms?.persist_interval_secs ?? '');

  _calibrationState.cfg      = cfg;
  _calibrationState.byInput  = _normalizeCalibrationProfiles(cfg.advanced?.calibration_profiles);
  await loadEffectiveInputRecognitionPolicies();

  renderCalibrationSummary();
  updateRecognitionUI();

  if (typeof refreshRMSLearningSnapshot === 'function') {
    refreshRMSLearningSnapshot('rec-rms-learning-summary');
  }

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
  const previousTelemetryNudges = fullCfg.advanced?.r3_telemetry_nudges ?? {};
  const previousAutonomous = fullCfg.advanced?.autonomous_calibration ?? {};
  const previousRMS = fullCfg.advanced?.rms_percentile_learning ?? {};
  function _recFloatOptional(id) {
    const s = _rval(id);
    if (s === '') return undefined;
    const x = parseFloat(s);
    return Number.isFinite(x) ? x : undefined;
  }
  const telemetryOut = {
    ...previousTelemetryNudges,
    enabled: document.getElementById('rec-telemetry-nudges-enabled')?.checked ?? false,
  };
  const lb = _rint('rec-telemetry-lookback', 0);
  if (lb > 0) telemetryOut.lookback_days = lb;
  const mp = _rint('rec-telemetry-min-pairs', 0);
  if (mp > 0) telemetryOut.min_followup_pairs = mp;
  const bfp = _recFloatOptional('rec-telemetry-baseline-fp');
  if (bfp !== undefined) telemetryOut.baseline_false_positive_ratio = bfp;
  const ms = _recFloatOptional('rec-telemetry-max-silence');
  if (ms !== undefined) telemetryOut.max_silence_threshold_delta = ms;
  const mpess = _recFloatOptional('rec-telemetry-max-pess');
  if (mpess !== undefined) telemetryOut.max_duration_pessimism_delta = mpess;
  const rmsOut = {
    ...previousRMS,
    enabled: document.getElementById('rec-rms-learning-enabled')?.checked ?? false,
    autonomous_apply: document.getElementById('rec-rms-learning-apply')?.checked ?? false,
  };
  const rmsSil = _rint('rec-rms-min-silence', 0);
  if (rmsSil > 0) rmsOut.min_silence_samples = rmsSil;
  const rmsMus = _rint('rec-rms-min-music', 0);
  if (rmsMus > 0) rmsOut.min_music_samples = rmsMus;
  const rmsPer = _rint('rec-rms-persist-secs', 0);
  if (rmsPer > 0) rmsOut.persist_interval_secs = rmsPer;
  fullCfg.advanced.autonomous_calibration = {
    ...previousAutonomous,
    enabled: document.getElementById('rec-autonomous-calibration-enabled')?.checked ?? false,
  };
  fullCfg.advanced.r3_telemetry_nudges = telemetryOut;
  fullCfg.advanced.rms_percentile_learning = rmsOut;

  const recCurrent = fullCfg.recognition ?? {};
  fullCfg.recognition = {
    ...recCurrent,
    recognizer_chain:                             _rval('rec-chain') || 'acrcloud_first',
    acrcloud_host:                                _rval('rec-host'),
    acrcloud_access_key:                          _rval('rec-access-key'),
    acrcloud_secret_key:                          _rval('rec-secret-key'),
    audd_api_token:                               _rval('rec-audd-token'),
    shazam_recognizer_enabled:                    document.getElementById('rec-shazam-enabled')?.checked ?? (recCurrent.shazam_recognizer_enabled !== false),
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
    else {
      toast('Saved — services restarting…');
      if (typeof refreshRMSLearningSnapshot === 'function') {
        refreshRMSLearningSnapshot('rec-rms-learning-summary');
      }
    }
  } catch {
    toast('Save failed.', true);
  }

  if (btn) { btn.disabled = false; btn.textContent = 'Save & Restart Services'; }
}

// ── Event wiring ───────────────────────────────────────────────────────────────

document.getElementById('rec-chain')?.addEventListener('change', updateRecognitionUI);
document.addEventListener('DOMContentLoaded', () => {
  document.getElementById('rec-import-rms-baseline-btn')?.addEventListener('click', importBaselineAndEnableAutonomy);
  loadRecognitionPage();
});

'use strict';

function _aval(id) {
  return (document.getElementById(id)?.value ?? '').trim();
}
function _aset(id, v) {
  const el = document.getElementById(id);
  if (el) el.value = v ?? '';
}
function _aint(id, fallback) {
  const n = parseInt(_aval(id), 10);
  return Number.isNaN(n) ? fallback : n;
}

async function loadAdvancedPage() {
  let cfg;
  try {
    const r = await fetch('/api/config');
    if (!r.ok) { toast('Failed to load configuration.', true); return; }
    cfg = await r.json();
  } catch {
    toast('Failed to load configuration.', true);
    return;
  }

  _aset('adv-library-db',  cfg.advanced?.library_db ?? '');
  _aset('adv-vu-socket',   cfg.advanced?.vu_socket ?? '');
  _aset('adv-pcm-socket',  cfg.advanced?.pcm_socket ?? '');
  _aset('adv-source-file', cfg.advanced?.source_file ?? '');
  _aset('adv-state-file',  cfg.advanced?.state_file ?? '');
  _aset('adv-artwork-dir', cfg.advanced?.artwork_dir ?? '');
  _aset('adv-metadata-pipe', cfg.advanced?.metadata_pipe ?? '');
  const autonomous = cfg.advanced?.autonomous_calibration;
  const autoBox = document.getElementById('adv-autonomous-calibration-enabled');
  if (autoBox) autoBox.checked = !!autonomous?.enabled;
  const telemetryNudges = cfg.advanced?.r3_telemetry_nudges;
  const telemetryBox = document.getElementById('adv-telemetry-nudges-enabled');
  if (telemetryBox) telemetryBox.checked = !!telemetryNudges?.enabled;
  _aset('adv-telemetry-lookback', telemetryNudges?.lookback_days ?? '');
  _aset('adv-telemetry-min-pairs', telemetryNudges?.min_followup_pairs ?? '');
  _aset('adv-telemetry-baseline-fp', telemetryNudges?.baseline_false_positive_ratio ?? '');
  _aset('adv-telemetry-max-silence', telemetryNudges?.max_silence_threshold_delta ?? '');
  _aset('adv-telemetry-max-pess', telemetryNudges?.max_duration_pessimism_delta ?? '');
  const rms = cfg.advanced?.rms_percentile_learning;
  const rmsEn = document.getElementById('adv-rms-learning-enabled');
  if (rmsEn) rmsEn.checked = !!rms?.enabled;
  const rmsAp = document.getElementById('adv-rms-learning-apply');
  if (rmsAp) rmsAp.checked = !!rms?.autonomous_apply;
  _aset('adv-rms-min-silence', rms?.min_silence_samples ?? '');
  _aset('adv-rms-min-music', rms?.min_music_samples ?? '');
  _aset('adv-rms-persist-secs', rms?.persist_interval_secs ?? '');

  if (typeof refreshRMSLearningSnapshot === 'function') {
    refreshRMSLearningSnapshot('adv-rms-learning-summary');
  }
}

async function saveAdvancedPage() {
  const btn = document.getElementById('btn-adv-page-save');
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

  const previousTelemetryNudges = fullCfg.advanced?.r3_telemetry_nudges ?? {};
  const previousAutonomous = fullCfg.advanced?.autonomous_calibration ?? {};
  const previousRMS = fullCfg.advanced?.rms_percentile_learning ?? {};
  function _advFloat(id) {
    const s = _aval(id);
    if (s === '') return undefined;
    const x = parseFloat(s);
    return Number.isFinite(x) ? x : undefined;
  }
  const autonomousEnabled = document.getElementById('adv-autonomous-calibration-enabled')?.checked ?? false;
  const telemetryEnabled = document.getElementById('adv-telemetry-nudges-enabled')?.checked ?? false;
  const telemetryOut = {
    ...previousTelemetryNudges,
    enabled: telemetryEnabled,
  };
  const lb = _aint('adv-telemetry-lookback', 0);
  if (lb > 0) telemetryOut.lookback_days = lb;
  const mp = _aint('adv-telemetry-min-pairs', 0);
  if (mp > 0) telemetryOut.min_followup_pairs = mp;
  const bfp = _advFloat('adv-telemetry-baseline-fp');
  if (bfp !== undefined) telemetryOut.baseline_false_positive_ratio = bfp;
  const ms = _advFloat('adv-telemetry-max-silence');
  if (ms !== undefined) telemetryOut.max_silence_threshold_delta = ms;
  const mpess = _advFloat('adv-telemetry-max-pess');
  if (mpess !== undefined) telemetryOut.max_duration_pessimism_delta = mpess;

  const rmsOut = {
    ...previousRMS,
    enabled: document.getElementById('adv-rms-learning-enabled')?.checked ?? false,
    autonomous_apply: document.getElementById('adv-rms-learning-apply')?.checked ?? false,
  };
  const rmsSil = _aint('adv-rms-min-silence', 0);
  if (rmsSil > 0) rmsOut.min_silence_samples = rmsSil;
  const rmsMus = _aint('adv-rms-min-music', 0);
  if (rmsMus > 0) rmsOut.min_music_samples = rmsMus;
  const rmsPer = _aint('adv-rms-persist-secs', 0);
  if (rmsPer > 0) rmsOut.persist_interval_secs = rmsPer;

  fullCfg.advanced = {
    ...(fullCfg.advanced ?? {}),
    library_db: _aval('adv-library-db') || fullCfg.advanced?.library_db || '',
    vu_socket:  _aval('adv-vu-socket')  || fullCfg.advanced?.vu_socket  || '',
    pcm_socket:     _aval('adv-pcm-socket')   || fullCfg.advanced?.pcm_socket   || '',
    source_file:    _aval('adv-source-file')  || fullCfg.advanced?.source_file  || '',
    state_file:     _aval('adv-state-file')   || fullCfg.advanced?.state_file   || '',
    artwork_dir:    _aval('adv-artwork-dir')  || fullCfg.advanced?.artwork_dir  || '',
    metadata_pipe:  _aval('adv-metadata-pipe')|| fullCfg.advanced?.metadata_pipe|| '',
    autonomous_calibration: { ...previousAutonomous, enabled: autonomousEnabled },
    rms_percentile_learning: rmsOut,
    r3_telemetry_nudges: telemetryOut,
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
      if (typeof refreshRMSLearningSnapshot === 'function') {
        refreshRMSLearningSnapshot('adv-rms-learning-summary');
      }
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

document.addEventListener('DOMContentLoaded', loadAdvancedPage);

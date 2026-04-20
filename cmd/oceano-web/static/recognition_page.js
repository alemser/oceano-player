'use strict';

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
document.addEventListener('DOMContentLoaded', loadRecognitionPage);

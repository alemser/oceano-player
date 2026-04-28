'use strict';

function _rmsSnapEsc(s) {
  return String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function _rmsRelativeTime(isoStr) {
  if (!isoStr) return '—';
  const d = new Date(isoStr);
  if (isNaN(d)) return isoStr.slice(0, 19).replace('T', ' ');
  const secs = Math.round((Date.now() - d) / 1000);
  if (secs < 60) return 'just now';
  if (secs < 3600) return Math.floor(secs / 60) + ' min ago';
  if (secs < 86400) return Math.floor(secs / 3600) + 'h ago';
  return Math.floor(secs / 86400) + 'd ago';
}

function _rmsStatusLabel(level) {
  switch (level) {
    case 'ready':      return 'Ready';
    case 'separating': return 'Learning';
    case 'collecting': return 'Collecting';
    default:           return level || '—';
  }
}

function _rmsFormatLabel(key) {
  switch (key) {
    case 'vinyl':    return 'Vinyl';
    case 'cd':       return 'CD';
    case 'physical': return 'Physical';
    default:         return key;
  }
}

function _isRMSFormatVisibleInUI(key) {
  return key === 'cd' || key === 'vinyl';
}

function _rmsProgressBar(pct, label, count, min) {
  const w = Math.max(0, Math.min(100, pct));
  const countStr = count >= min ? `${count}` : `${count} / ${min}`;
  return (
    `<div class="rms-snap-bar-row">` +
    `<span class="rms-snap-bar-lbl">${_rmsSnapEsc(label)}</span>` +
    `<div class="rms-snap-bar-wrap"><div class="rms-snap-bar" style="width:${w}%"></div></div>` +
    `<span class="rms-snap-bar-count">${_rmsSnapEsc(countStr)}</span>` +
    `</div>`
  );
}

function _rmsCard(row, minSil, minMus, autoApply) {
  const level = row.readiness_level || 'collecting';
  const fmt = _rmsFormatLabel(row.format_key);
  const statusLabel = _rmsStatusLabel(level);
  const statusDesc = level === 'ready'
    ? (autoApply ? 'Thresholds are derived and being applied automatically.' : 'Thresholds derived — enable Autonomous Apply to use them.')
    : level === 'separating'
    ? 'Enough samples, but silence and music distributions overlap too much to derive a threshold.'
    : 'Still collecting baseline samples of silence and music.';

  const silBar = _rmsProgressBar(row.silence_pct ?? 0, 'Silence', row.silence_total ?? 0, minSil);
  const musBar = _rmsProgressBar(row.music_pct ?? 0, 'Music', row.music_total ?? 0, minMus);

  let threshLine = '';
  if (row.derived_enter != null && row.derived_exit != null) {
    threshLine =
      `<div class="rms-snap-thresholds">` +
      `<span>Enter&nbsp;<strong>${Number(row.derived_enter).toFixed(4)}</strong></span>` +
      `<span>Exit&nbsp;<strong>${Number(row.derived_exit).toFixed(4)}</strong></span>` +
      `</div>`;
  }

  return (
    `<div class="rms-snap-card rms-status-${_rmsSnapEsc(level)}">` +
    `<div class="rms-snap-card-header">` +
    `<span class="rms-snap-format">${_rmsSnapEsc(fmt)}</span>` +
    `<span class="rms-snap-chip rms-chip-${_rmsSnapEsc(level)}">${_rmsSnapEsc(statusLabel)}</span>` +
    `</div>` +
    `<p class="rms-snap-desc">${_rmsSnapEsc(statusDesc)}</p>` +
    silBar + musBar +
    threshLine +
    `<div class="rms-snap-updated">Updated ${_rmsSnapEsc(_rmsRelativeTime(row.updated_at))}</div>` +
    `</div>`
  );
}

/** Fills #containerId with visual cards from GET /api/recognition/rms-learning */
async function refreshRMSLearningSnapshot(containerId) {
  const el = document.getElementById(containerId);
  if (!el) return;
  el.innerHTML = '<span class="hint">Loading…</span>';
  try {
    const res = await fetch('/api/recognition/rms-learning', { cache: 'no-store' });
    if (!res.ok) {
      el.innerHTML = '<span class="hint">Snapshot unavailable.</span>';
      return;
    }
    const data = await res.json();
    const rows = Array.isArray(data.rows) ? data.rows : [];
    const visibleRows = rows.filter(r => _isRMSFormatVisibleInUI(String(r?.format_key || '').toLowerCase()));
    const minSil = Number(data.min_silence_samples || 400);
    const minMus = Number(data.min_music_samples || 400);
    const autoApply = !!data.autonomous_apply;

    if (rows.length === 0) {
      el.innerHTML = '<span class="hint">No histogram data yet — enable RMS learning and play physical media.</span>';
      return;
    }
    if (visibleRows.length === 0) {
      el.innerHTML = '<span class="hint">Collecting in background. CD/Vinyl cards appear once those formats receive samples.</span>';
      return;
    }
    el.innerHTML = visibleRows.map(r => _rmsCard(r, minSil, minMus, autoApply)).join('');
  } catch {
    el.innerHTML = '<span class="hint">Could not load snapshot.</span>';
  }
}

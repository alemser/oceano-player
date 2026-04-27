'use strict';

function _rmsSnapEsc(s) {
  return String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function _rmsFmtDerived(x) {
  if (x === null || x === undefined) return '—';
  const n = typeof x === 'number' ? x : parseFloat(x);
  if (!Number.isFinite(n)) return '—';
  return n.toFixed(4);
}

/** Fills #containerId with a compact table from GET /api/recognition/rms-learning */
async function refreshRMSLearningSnapshot(containerId) {
  const el = document.getElementById(containerId);
  if (!el) return;
  el.innerHTML = '<span class="hint">Loading snapshot…</span>';
  try {
    const res = await fetch('/api/recognition/rms-learning', { cache: 'no-store' });
    if (!res.ok) {
      el.innerHTML = '<span class="hint">Snapshot unavailable.</span>';
      return;
    }
    const data = await res.json();
    const rows = Array.isArray(data.rows) ? data.rows : [];
    if (rows.length === 0) {
      el.innerHTML = '<span class="hint">No histogram rows yet — enable RMS learning and play physical media.</span>';
      return;
    }
    let html = '<table class="rms-snap-table"><thead><tr>';
    html += '<th>Format</th><th>Silence</th><th>Music</th><th>Enter</th><th>Exit</th><th>Updated</th>';
    html += '</tr></thead><tbody>';
    for (const row of rows) {
      const raw = row.updated_at != null ? String(row.updated_at) : '';
      const u = raw.length >= 19 ? raw.slice(0, 19).replace('T', ' ') : (raw || '—');
      html += '<tr>';
      html += `<td><code>${_rmsSnapEsc(row.format_key)}</code></td>`;
      html += `<td>${_rmsSnapEsc(String(row.silence_total ?? 0))}</td>`;
      html += `<td>${_rmsSnapEsc(String(row.music_total ?? 0))}</td>`;
      html += `<td>${_rmsSnapEsc(_rmsFmtDerived(row.derived_enter))}</td>`;
      html += `<td>${_rmsSnapEsc(_rmsFmtDerived(row.derived_exit))}</td>`;
      html += `<td class="rms-snap-updated">${_rmsSnapEsc(u)}</td>`;
      html += '</tr>';
    }
    html += '</tbody></table>';
    el.innerHTML = html;
  } catch {
    el.innerHTML = '<span class="hint">Could not load snapshot.</span>';
  }
}

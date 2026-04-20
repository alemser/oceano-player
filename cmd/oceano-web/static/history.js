'use strict';

// ─── DOM refs ─────────────────────────────────────────────────────────────────
const $statPlays   = document.getElementById('stat-plays');
const $statHours   = document.getElementById('stat-hours');
const $statArtist  = document.getElementById('stat-top-artist');
const $statSources = document.getElementById('stat-sources');

// ─── Boot ─────────────────────────────────────────────────────────────────────
async function init() {
  await loadStats();
}

// ─── API ──────────────────────────────────────────────────────────────────────
async function loadStats() {
  try {
    const res = await fetch('/api/history/stats', { cache: 'no-store' });
    if (!res.ok) return;
    const s = await res.json();
    renderStats(s);
  } catch (_) {}
}

// ─── Stats ────────────────────────────────────────────────────────────────────
function renderStats(s) {
  $statPlays.textContent   = s.total_plays ?? '—';
  $statHours.textContent   = s.total_listened_hours != null
    ? s.total_listened_hours.toFixed(1) + ' h'
    : '—';
  $statArtist.textContent  = s.top_artists?.length ? s.top_artists[0].artist : '—';

  const src = s.plays_by_source || {};
  const parts = Object.entries(src)
    .sort((a, b) => b[1] - a[1])
    .map(([k, v]) => {
      const label = k === 'Physical' ? 'Physical (Unknown)' : k;
      return `<span class="src-count">${esc(String(v))}</span> ${esc(label)}`;
    })
    .join('<br>');
  $statSources.innerHTML = parts || '—';

  const hrs = s.hours_by_source || {};
  const hrParts = Object.entries(hrs)
    .filter(([, h]) => h >= 0.05)
    .sort((a, b) => b[1] - a[1])
    .map(([k, h]) => `${k} ${h.toFixed(1)}h`)
    .join(' · ');
  const $hrDetail = document.getElementById('stat-hours-detail');
  if ($hrDetail) $hrDetail.textContent = hrParts;
}

// ─── Helpers ──────────────────────────────────────────────────────────────────
function esc(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

init();

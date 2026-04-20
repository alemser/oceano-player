'use strict';

// ─── DOM refs ─────────────────────────────────────────────────────────────────
const $statPlays   = document.getElementById('stat-plays');
const $statHours   = document.getElementById('stat-hours');
const $statArtist  = document.getElementById('stat-top-artist');
const $statSources = document.getElementById('stat-sources');
const $stylusTitle = document.getElementById('stat-stylus-title');
const $stylusModel = document.getElementById('stat-stylus-model');
const $stylusState = document.getElementById('stat-stylus-state');
const $stylusWear = document.getElementById('stat-stylus-wear');
const $stylusProgress = document.getElementById('stat-stylus-progress');
const $stylusRemaining = document.getElementById('stat-stylus-remaining');

// ─── Boot ─────────────────────────────────────────────────────────────────────
async function init() {
  await Promise.all([loadStats(), loadStylusSummary()]);
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

async function loadStylusSummary() {
  try {
    const res = await fetch('/api/stylus', { cache: 'no-store' });
    if (!res.ok) {
      renderStylusSummary(null);
      return;
    }
    const s = await res.json();
    renderStylusSummary(s);
  } catch (_) {
    renderStylusSummary(null);
  }
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

function stylusStateVisual(state) {
  const val = String(state || 'healthy').toLowerCase();
  if (val === 'overdue') return { label: 'OVERDUE', color: '#e65c5c' };
  if (val === 'soon') return { label: 'SOON', color: '#f39b47' };
  if (val === 'plan') return { label: 'PLAN', color: '#f6c945' };
  return { label: 'HEALTHY', color: '#3cb371' };
}

function renderStylusSummary(payload) {
  if (!$stylusTitle || !$stylusModel || !$stylusState || !$stylusWear || !$stylusProgress || !$stylusRemaining) {
    return;
  }

  const disabled = !payload || payload.enabled !== true;
  if (disabled) {
    $stylusTitle.textContent = 'Stylus tracking';
    $stylusModel.textContent = 'Disabled in Amplifier settings';
    $stylusState.textContent = 'OFF';
    $stylusState.style.color = 'var(--text-dim)';
    $stylusState.style.borderColor = 'var(--border)';
    $stylusWear.textContent = '—';
    $stylusProgress.style.width = '0%';
    $stylusRemaining.textContent = 'Enable to monitor wear from vinyl hours';
    return;
  }

  const stylus = payload.stylus || {};
  const metrics = payload.metrics || {};
  const model = [stylus.brand, stylus.model].filter(Boolean).join(' ').trim();

  $stylusTitle.textContent = model || 'Custom stylus';
  $stylusModel.textContent = stylus.stylus_profile
    ? `${stylus.stylus_profile} · ${Number(stylus.lifetime_hours || 0)}h lifetime`
    : 'Configured stylus';

  const visual = stylusStateVisual(metrics.state);
  $stylusState.textContent = visual.label;
  $stylusState.style.color = visual.color;
  $stylusState.style.borderColor = visual.color;

  const wear = Number(metrics.wear_percent || 0);
  $stylusWear.textContent = `${wear.toFixed(1)}% wear`;

  const pct = Math.max(0, Math.min(100, wear));
  $stylusProgress.style.width = `${pct}%`;

  const remaining = Number(metrics.remaining_hours || 0);
  const total = Number(metrics.stylus_hours_total || 0);
  $stylusRemaining.textContent = `${remaining.toFixed(1)}h remaining · ${total.toFixed(1)}h total`;
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

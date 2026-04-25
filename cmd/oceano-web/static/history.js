'use strict';

// ─── DOM refs ─────────────────────────────────────────────────────────────────
const $statPlays         = document.getElementById('stat-plays');
const $statPlaysLabel    = document.getElementById('stat-plays-label');
const $statHours         = document.getElementById('stat-hours');
const $statHoursLabel    = document.getElementById('stat-hours-label');
const $statHoursDetail   = document.getElementById('stat-hours-detail');
const $statArtist        = document.getElementById('stat-top-artist');
const $statArtistsLabel  = document.getElementById('stat-artists-label');
const $statSourcesChart  = document.getElementById('stat-sources-chart');
const $stylusTitle       = document.getElementById('stat-stylus-title');
const $stylusModel       = document.getElementById('stat-stylus-model');
const $stylusState       = document.getElementById('stat-stylus-state');
const $stylusWear        = document.getElementById('stat-stylus-wear');
const $stylusProgress    = document.getElementById('stat-stylus-progress');
const $stylusRemaining   = document.getElementById('stat-stylus-remaining');

// ─── Period state ─────────────────────────────────────────────────────────────
let _periodDays = 30;
let _statsRequestSeq = 0;

function setPeriod(days) {
  _periodDays = days;
  document.querySelectorAll('.period-btn').forEach(b => {
    b.classList.toggle('active', Number(b.dataset.days) === days);
  });
  loadStats();
}

function periodLabel() {
  if (_periodDays === 7)  return 'This week';
  if (_periodDays === 30) return 'Last 30 days';
  return 'All time';
}

// ─── Boot ─────────────────────────────────────────────────────────────────────
async function init() {
  setPeriod(30);
  await Promise.all([loadStylusSummary(), loadRecognitionStats()]);
}

// ─── API ──────────────────────────────────────────────────────────────────────
async function loadStats() {
  const reqSeq = ++_statsRequestSeq;
  try {
    const url = `/api/history/stats?days=${_periodDays}`;
    const res = await fetch(url, { cache: 'no-store' });
    if (!res.ok) return;
    const s = await res.json();
    if (reqSeq !== _statsRequestSeq) return;
    renderStats(s);
    await loadBoundaryStats(reqSeq);
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
  const label = periodLabel();

  $statPlays.textContent = s.total_plays ?? '—';
  $statPlaysLabel.textContent = `Plays · ${label}`;

  $statHours.textContent = s.total_listened_hours != null
    ? s.total_listened_hours.toFixed(1) + ' h'
    : '—';
  $statHoursLabel.textContent = `Hours · ${label}`;

  renderHoursDetail(s.hours_by_source || {});
  renderTopArtists(s.top_artists || [], label);
  renderSourcesChart(s.plays_by_source || {});
}

function renderHoursDetail(hrs) {
  const entries = Object.entries(hrs)
    .filter(([, h]) => h >= 0.05)
    .sort((a, b) => b[1] - a[1]);

  if (!entries.length) {
    $statHoursDetail.innerHTML = '';
    return;
  }

  const total = entries.reduce((s, [, h]) => s + h, 0) || 1;

  const segs = entries.map(([k, h]) => {
    const pct = (h / total * 100).toFixed(1);
    const cls = sourceSegClass(k);
    return `<div class="hours-seg ${cls}" style="width:${pct}%" title="${sourceDisplayLabel(k)}: ${h.toFixed(1)}h"></div>`;
  }).join('');

  const legend = entries.map(([k, h]) => {
    const cls = sourceSegClass(k);
    return `<span class="hours-leg-item"><span class="hours-leg-dot ${cls}"></span>${esc(sourceDisplayLabel(k))} <strong>${h.toFixed(1)}h</strong></span>`;
  }).join('');

  $statHoursDetail.innerHTML =
    `<div class="hours-stack-bar">${segs}</div>` +
    `<div class="hours-stack-legend">${legend}</div>`;
}

function sourceSegClass(key) {
  if (key === 'Vinyl')  return 'seg-vinyl';
  if (key === 'CD')     return 'seg-cd';
  if (key === 'AirPlay') return 'seg-airplay';
  return 'seg-other';
}

function renderTopArtists(topArtists, periodLabel) {
  $statArtistsLabel.textContent = `Top artists · ${periodLabel}`;
  if (!topArtists.length) {
    $statArtist.innerHTML = '<span class="top-artist-empty">No data yet</span>';
    return;
  }
  const rankClass = ['rank-1', 'rank-2', 'rank-3', 'rank-4', 'rank-5'];
  $statArtist.innerHTML = topArtists
    .map((item, idx) =>
      `<span class="top-artist-line ${rankClass[idx] || 'rank-5'}">` +
        `<span class="rank-num">#${idx + 1}</span>${esc(item.artist || '—')}` +
      `</span>`
    )
    .join('');
}

function renderSourcesChart(playsBySource) {
  const entries = Object.entries(playsBySource)
    .sort((a, b) => b[1] - a[1]);

  if (!entries.length) {
    $statSourcesChart.innerHTML = '<span class="src-empty">No data yet</span>';
    return;
  }

  const max = entries[0][1] || 1;
  $statSourcesChart.innerHTML = entries.map(([k, v]) => {
    const pct = Math.round((v / max) * 100);
    const label = sourceDisplayLabel(k);
    const cls = sourceSegClass(k);
    return `<div class="src-row">
      <span class="src-label">${esc(label)}</span>
      <div class="src-bar-wrap"><div class="src-bar ${cls}" style="width:${pct}%"></div></div>
      <span class="src-count-num">${v}</span>
    </div>`;
  }).join('');
}

function sourceDisplayLabel(key) {
  if (key === 'Physical') return 'Unidentified';
  return key;
}

// ─── Stylus ───────────────────────────────────────────────────────────────────
function stylusStateVisual(state) {
  const val = String(state || 'healthy').toLowerCase();
  if (val === 'overdue') return { label: 'OVERDUE', color: '#e65c5c' };
  if (val === 'soon')    return { label: 'SOON',    color: '#f39b47' };
  if (val === 'plan')    return { label: 'PLAN',    color: '#f6c945' };
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

  const stylus  = payload.stylus  || {};
  const metrics = payload.metrics || {};
  const model   = [stylus.brand, stylus.model].filter(Boolean).join(' ').trim();

  $stylusTitle.textContent = model || 'Custom stylus';
  $stylusModel.textContent = stylus.stylus_profile
    ? `${stylus.stylus_profile} · ${Number(stylus.lifetime_hours || 0)}h lifetime`
    : 'Configured stylus';

  const visual = stylusStateVisual(metrics.state);
  $stylusState.textContent    = visual.label;
  $stylusState.style.color       = visual.color;
  $stylusState.style.borderColor = visual.color;

  const wear = Number(metrics.wear_percent || 0);
  $stylusWear.textContent = `${wear.toFixed(1)}% wear`;

  const pct = Math.max(0, Math.min(100, wear));
  $stylusProgress.style.width = `${pct}%`;

  const remaining = Number(metrics.remaining_hours || 0);
  const used      = Number(metrics.stylus_hours_total || 0);
  $stylusRemaining.textContent = `${remaining.toFixed(1)}h remaining · ${used.toFixed(1)}h used`;
}

// ─── Boundary telemetry (VU / duration-boundary path) ─────────────────────────
const _boundaryOutcomeLabels = {
  fired: 'Fired (recognition triggered)',
  suppressed_duration_guard: 'Suppressed (duration guard)',
  ignored_mature_progress: 'Ignored (mature progress)',
  suppressed_not_physical: 'Suppressed (not Physical)',
  trigger_channel_full: 'Trigger channel full',
  energy_change_cooldown: 'Energy dip (cooldown)',
};

async function loadBoundaryStats(statsReqSeq) {
  const container = document.getElementById('boundary-stats-grid');
  if (!container) return;
  const seq = statsReqSeq != null ? statsReqSeq : _statsRequestSeq;
  try {
    const r = await fetch(`/api/recognition/boundary-stats?days=${_periodDays}`, { cache: 'no-store' });
    if (!r.ok) {
      container.innerHTML = '<div class="rec-stats-placeholder">Unavailable</div>';
      return;
    }
    const payload = await r.json();
    if (seq !== _statsRequestSeq) return;
    renderBoundaryStats(container, payload);
  } catch {
    if (container) container.innerHTML = '<div class="rec-stats-placeholder">Failed to load.</div>';
  }
}

function renderBoundaryStats(container, payload) {
  const total = Number(payload.total || 0);
  const by = payload.by_outcome || {};
  const actionable = Number(payload.actionable_total || 0);
  const fr = payload.fire_rate;

  if (total === 0) {
    container.innerHTML = '<div class="rec-stats-placeholder">No boundary events in this period yet.</div>';
    return;
  }

  const order = [
    'fired',
    'suppressed_duration_guard',
    'ignored_mature_progress',
    'suppressed_not_physical',
    'trigger_channel_full',
    'energy_change_cooldown',
  ];
  let rows = '';
  for (const k of order) {
    const n = Number(by[k] || 0);
    const label = _boundaryOutcomeLabels[k] || k;
    rows += `<div class="rec-prov-row"><span class="lbl">${esc(label)}</span><span class="val">${n}</span></div>`;
  }
  for (const k of Object.keys(by).sort()) {
    if (order.includes(k)) continue;
    rows += `<div class="rec-prov-row"><span class="lbl">${esc(k)}</span><span class="val">${by[k]}</span></div>`;
  }

  const rateLabel = actionable > 0 && typeof fr === 'number' && fr >= 0
    ? `${Math.round(fr * 100)}%`
    : '—';
  const rateHint = actionable > 0
    ? 'Share of actionable boundary decisions that triggered recognition (excludes energy-dip cooldown rows).'
    : '';
  const rateTitle = rateHint ? ` title="${esc(rateHint)}"` : '';

  container.innerHTML =
    `<div class="rec-prov-card boundary-stats-card">` +
    `<div class="rec-prov-name">Summary</div>` +
    `<div class="rec-prov-row"><span class="lbl">Total events</span><span class="val">${total}</span></div>` +
    `<div class="rec-prov-row"><span class="lbl">Actionable decisions</span><span class="val">${actionable}</span></div>` +
    `<div class="rec-prov-rate"><span class="lbl">Fire rate</span><span class="${rateLabel !== '—' ? 'val-ok' : 'val-none'}"${rateTitle}>${rateLabel}</span></div>` +
    `<div class="boundary-outcome-block">${rows}</div>` +
    `</div>`;
}

// ─── Recognition provider stats ───────────────────────────────────────────────
async function loadRecognitionStats() {
  const container = document.getElementById('rec-stats-grid');
  if (!container) return;
  try {
    const r = await fetch('/api/recognition/stats', { cache: 'no-store' });
    if (!r.ok) { container.innerHTML = '<div class="rec-stats-placeholder">Unavailable</div>'; return; }
    const stats = await r.json();
    if (Object.keys(stats).length === 0) {
      container.innerHTML = '<div class="rec-stats-placeholder">No data yet — recognition needs to run at least once.</div>';
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
      card.className = 'rec-prov-card';
      let html = `<div class="rec-prov-name">${esc(p)}</div>`;
      if (p === 'Trigger') {
        const boundary = evs.boundary || 0;
        const fallback = evs.fallback_timer || 0;
        const total    = boundary + fallback;
        const rate     = total > 0 ? Math.round((boundary / total) * 100) : null;
        html += row('Boundary', boundary) + row('Fallback timer', fallback) + row('Total', total);
        html += `<div class="rec-prov-rate"><span class="lbl">Boundary rate</span><span class="${rate != null ? 'val-ok' : 'val-none'}">${rate != null ? rate + '%' : '—'}</span></div>`;
      } else {
        const attempts  = evs.attempt || 0;
        const successes = evs.success  || 0;
        const rate      = attempts > 0 ? Math.round((successes / attempts) * 100) : null;
        html += row('Attempts', attempts) + row('Matches', successes);
        if (evs.no_match) html += row('No match', evs.no_match);
        if (evs.error)    html += row('Errors',   evs.error);
        html += `<div class="rec-prov-rate"><span class="lbl">Success rate</span><span class="${rate != null ? 'val-ok' : 'val-none'}">${rate != null ? rate + '%' : '—'}</span></div>`;
      }
      card.innerHTML = html;
      container.appendChild(card);
    }
  } catch {
    if (container) container.innerHTML = '<div class="rec-stats-placeholder">Failed to load.</div>';
  }
}

function row(label, value) {
  return `<div class="rec-prov-row"><span class="lbl">${esc(label)}</span><span class="val">${value}</span></div>`;
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

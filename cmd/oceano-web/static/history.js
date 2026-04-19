'use strict';

// ─── State ────────────────────────────────────────────────────────────────────
let _allPlays   = [];
let _offset     = 0;
let _total      = 0;
const LIMIT     = 50;
let _source     = '';

// ─── DOM refs ─────────────────────────────────────────────────────────────────
const $statPlays     = document.getElementById('stat-plays');
const $statHours     = document.getElementById('stat-hours');
const $statArtist    = document.getElementById('stat-top-artist');
const $statSources   = document.getElementById('stat-sources');
const $heatGrid      = document.getElementById('heatmap-grid');
const $heatMonths    = document.getElementById('heatmap-months');
const $timeline      = document.getElementById('timeline');
const $sourceFilter  = document.getElementById('source-filter');
const $loadMoreBtn   = document.getElementById('load-more-btn');
const $loadMoreWrap  = document.getElementById('load-more-wrap');

// ─── Boot ─────────────────────────────────────────────────────────────────────
async function init() {
  await Promise.all([loadStats(), loadPlays(true)]);
  $sourceFilter.addEventListener('change', () => {
    _source = $sourceFilter.value;
    _allPlays = [];
    _offset = 0;
    renderTimeline();
    loadPlays(true);
  });
  $loadMoreBtn.addEventListener('click', () => loadPlays(false));
}

// ─── API ──────────────────────────────────────────────────────────────────────
async function loadStats() {
  try {
    const res = await fetch('/api/history/stats', { cache: 'no-store' });
    if (!res.ok) return;
    const s = await res.json();
    renderStats(s);
    renderHeatmap(s.heatmap || {});
  } catch (_) {}
}

async function loadPlays(reset) {
  if (reset) { _allPlays = []; _offset = 0; _total = 0; }
  const params = new URLSearchParams({ limit: LIMIT, offset: _offset });
  if (_source) params.set('source', _source);
  try {
    const res = await fetch('/api/history?' + params, { cache: 'no-store' });
    if (!res.ok) return;
    const data = await res.json();
    _total = data.total || 0;
    const plays = (data.plays || []).filter(p => !_source || p.source === _source);
    _allPlays = reset ? plays : [..._allPlays, ...plays];
    _offset += plays.length;
    renderTimeline();
    $loadMoreBtn.hidden = _offset >= _total;
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
    .map(([k, v]) => `${k} ${v}`)
    .join(' · ');
  $statSources.textContent = parts || '—';
}

// ─── Heatmap ──────────────────────────────────────────────────────────────────
function renderHeatmap(heatmap) {
  // Build a 53-week grid ending today, 7 rows (Sun–Sat).
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  // Align to the start of the week (Sunday).
  const startDay = new Date(today);
  startDay.setDate(today.getDate() - today.getDay()); // last Sunday
  startDay.setDate(startDay.getDate() - 52 * 7);      // go back 52 weeks

  const COLS = 53;
  const ROWS = 7;
  const cells = []; // [col][row] = date string or null

  for (let col = 0; col < COLS; col++) {
    cells[col] = [];
    for (let row = 0; row < ROWS; row++) {
      const d = new Date(startDay);
      d.setDate(startDay.getDate() + col * 7 + row);
      cells[col][row] = d > today ? null : fmtDate(d);
    }
  }

  // Max count for level scaling.
  const maxCount = Math.max(1, ...Object.values(heatmap));

  // Build 7 row elements (one per weekday).
  $heatGrid.innerHTML = '';
  for (let row = 0; row < ROWS; row++) {
    const rowEl = document.createElement('div');
    rowEl.className = 'heatmap-row';
    for (let col = 0; col < COLS; col++) {
      const dateStr = cells[col][row];
      const cell = document.createElement('div');
      if (!dateStr) {
        cell.className = 'heatmap-cell future';
      } else {
        const count = heatmap[dateStr] || 0;
        const level = count === 0 ? 0 : Math.min(4, Math.ceil((count / maxCount) * 4));
        cell.className = 'heatmap-cell';
        cell.dataset.level = level;
        cell.title = `${dateStr}: ${count} play${count !== 1 ? 's' : ''}`;
      }
      rowEl.appendChild(cell);
    }
    $heatGrid.appendChild(rowEl);
  }

  // Month labels.
  $heatMonths.innerHTML = '';
  let prevMonth = -1;
  for (let col = 0; col < COLS; col++) {
    const d = new Date(startDay);
    d.setDate(startDay.getDate() + col * 7);
    const month = d.getMonth();
    const span = document.createElement('span');
    span.className = 'heatmap-month-label';
    span.style.width = '14px'; // cell(12) + gap(2)
    span.style.display = 'inline-block';
    if (month !== prevMonth) {
      span.textContent = d.toLocaleString('en', { month: 'short' });
      prevMonth = month;
    }
    $heatMonths.appendChild(span);
  }
}

function fmtDate(d) {
  return d.toISOString().slice(0, 10);
}

// ─── Timeline ─────────────────────────────────────────────────────────────────
function renderTimeline() {
  if (!_allPlays.length) {
    $timeline.innerHTML = '<div id="timeline-empty">No plays recorded yet.</div>';
    return;
  }

  // Group by local date.
  const groups = {};
  const order  = [];
  for (const p of _allPlays) {
    const dayKey = p.started_at.slice(0, 10); // UTC date; good enough
    if (!groups[dayKey]) { groups[dayKey] = []; order.push(dayKey); }
    groups[dayKey].push(p);
  }

  $timeline.innerHTML = '';
  for (const day of order) {
    const label = formatDayLabel(day);
    const group = document.createElement('div');
    group.className = 'day-group';
    group.innerHTML = `<div class="day-label">${label}</div>`;
    const playsEl = document.createElement('div');
    playsEl.className = 'day-plays';
    for (const p of groups[day]) {
      playsEl.appendChild(makePlayRow(p));
    }
    group.appendChild(playsEl);
    $timeline.appendChild(group);
  }
}

function makePlayRow(p) {
  const row = document.createElement('div');
  row.className = 'play-row';
  row.setAttribute('role', 'listitem');

  // Artwork
  const artEl = document.createElement('div');
  artEl.className = 'play-art';
  if (p.artwork_path) {
    const img = document.createElement('img');
    img.src = p.collection_id
      ? '/api/library/' + encodeURIComponent(p.collection_id) + '/artwork'
      : '/api/history/artwork/' + encodeURIComponent(p.id);
    img.alt = '';
    img.loading = 'lazy';
    img.onerror = () => { artEl.innerHTML = musicNoteSVG(); };
    artEl.appendChild(img);
  } else {
    artEl.innerHTML = musicNoteSVG();
  }

  // Metadata
  const metaEl = document.createElement('div');
  metaEl.className = 'play-meta';
  const title = p.title || 'Unknown track';
  const sub = [p.artist, p.album].filter(Boolean).join(' · ');
  metaEl.innerHTML = `
    <div class="play-title">${esc(title)}</div>
    ${sub ? `<div class="play-artist-album">${esc(sub)}</div>` : ''}
  `;

  // Right column: time, duration, source badge
  const rightEl = document.createElement('div');
  rightEl.className = 'play-right';

  const timeStr = fmtTime(p.started_at);
  const durStr  = p.listened_seconds > 0 ? fmtDuration(p.listened_seconds) : '';

  rightEl.innerHTML = `
    <span class="play-time">${timeStr}</span>
    ${durStr ? `<span class="play-duration">${durStr}</span>` : ''}
    ${sourceBadge(p)}
  `;

  row.appendChild(artEl);
  row.appendChild(metaEl);
  row.appendChild(rightEl);
  return row;
}

function sourceBadge(p) {
  let cls = 'physical';
  let label = p.source || 'Unknown';
  if (p.source === 'AirPlay')   { cls = 'airplay'; }
  if (p.source === 'Bluetooth') { cls = 'bluetooth'; }
  if (p.source === 'Physical') {
    if (p.media_format === 'Vinyl') { cls = 'vinyl'; label = p.vinyl_side ? `Vinyl ${p.vinyl_side}` : 'Vinyl'; }
    else if (p.media_format === 'CD') { cls = 'cd'; label = 'CD'; }
  }
  return `<span class="source-badge ${cls}">${esc(label)}</span>`;
}

// ─── Helpers ──────────────────────────────────────────────────────────────────
function formatDayLabel(dateStr) {
  const d    = new Date(dateStr + 'T12:00:00');
  const now  = new Date();
  const diff = Math.round((now - d) / 86400000);
  if (diff === 0) return 'Today';
  if (diff === 1) return 'Yesterday';
  return d.toLocaleDateString('en', { weekday: 'long', day: 'numeric', month: 'long' });
}

function fmtTime(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  return d.toLocaleTimeString('en', { hour: '2-digit', minute: '2-digit' });
}

function fmtDuration(secs) {
  if (secs <= 0) return '';
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return m > 0 ? `${m}m ${s.toString().padStart(2, '0')}s` : `${s}s`;
}

function esc(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function musicNoteSVG() {
  return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>`;
}

init();

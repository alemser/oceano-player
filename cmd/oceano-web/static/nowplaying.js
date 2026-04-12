"use strict";

const SOURCE_LABELS = {
  AirPlay:  'AirPlay',
  Bluetooth:'Bluetooth',
  UPnP:     'UPnP',
  Vinyl:    'Vinyl',
  CD:       'CD',
  Physical: 'Physical',
  None:     '—',
};

const STREAMING_SOURCES = new Set(['AirPlay', 'Bluetooth', 'UPnP']);

// ─── DOM refs ────────────────────────────────────────────────────────────────

const $idle       = document.getElementById('idle-screen');
const $sourceBar = document.getElementById('source-bar');
const $sourceIcon = document.getElementById('source-icon');
const $sourceLabel  = document.getElementById('source-label');
const $metaSourceIcon = document.getElementById('meta-source-icon');
const $metaSourceLabel = document.getElementById('meta-source-label');
const $metaPlaybackBadge = document.getElementById('meta-playback-badge');
const $badge      = document.getElementById('playback-badge');
const $artImg     = document.getElementById('artwork-img');
const $artDefault = document.getElementById('artwork-default');
const $title      = document.getElementById('track-title');
const $artist     = document.getElementById('track-artist');
const $album      = document.getElementById('track-album');
const $chips      = document.getElementById('meta-chips');
const $streamProgress = document.getElementById('stream-progress');
const $streamFill = document.getElementById('stream-progress-fill');
const $streamElapsed = document.getElementById('stream-elapsed');
const $streamTotal = document.getElementById('stream-total');
const $identifying= document.getElementById('identifying-label');

function openPowerDialog() {
  document.getElementById('power-dialog')?.classList.add('open');
}

function closePowerDialog() {
  document.getElementById('power-dialog')?.classList.remove('open');
}

function showPowerActionToast(message) {
  const toast = document.createElement('div');
  toast.textContent = message;
  toast.setAttribute('role', 'alert');
  toast.style.position = 'fixed';
  toast.style.right = '16px';
  toast.style.bottom = '16px';
  toast.style.zIndex = '9999';
  toast.style.maxWidth = '320px';
  toast.style.padding = '10px 14px';
  toast.style.border = '1px solid #7a1a1a';
  toast.style.borderRadius = '8px';
  toast.style.background = '#2a1010';
  toast.style.color = '#f5b5b5';
  toast.style.boxShadow = '0 8px 24px rgba(0, 0, 0, 0.35)';
  toast.style.fontSize = '13px';
  toast.style.lineHeight = '1.4';
  document.body.appendChild(toast);
  setTimeout(() => toast.remove(), 4500);
}

async function sendPowerAction(action) {
  closePowerDialog();
  try {
    const response = await fetch('/api/power', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    });
    if (!response.ok) {
      const errorText = (await response.text()).trim();
      throw new Error(errorText || `Power action failed (${response.status})`);
    }
  } catch (error) {
    showPowerActionToast(error?.message || 'Failed to send power action');
  }
}

// ─── Artwork helpers ─────────────────────────────────────────────────────────

let _lastArtworkPath = null;

function updateArtwork(artworkPath) {
  if (artworkPath === _lastArtworkPath) return;
  _lastArtworkPath = artworkPath;

  if (artworkPath) {
    const img = new Image();
    img.onload = () => {
      $artImg.src = img.src;
      $artImg.style.opacity = '1';
      $artDefault.style.opacity = '0';
    };
    img.onerror = () => showDefaultArtwork();
    // Bust cache between track changes with a timestamp parameter.
    // The server reads the artwork path directly from the state file;
    // the ?t parameter is ignored by the server but forces browser cache refresh.
    img.src = '/api/artwork?t=' + Date.now();
  } else {
    showDefaultArtwork();
  }
}

function showDefaultArtwork() {
  $artImg.style.opacity = '0';
  $artDefault.style.opacity = '1';
  _lastArtworkPath = null;
}

// ─── Chip builders ───────────────────────────────────────────────────────────

function chipSVG(pathD) {
  return `<svg viewBox="0 0 12 12" fill="none" stroke="currentColor"
               stroke-width="1.5" stroke-linecap="round" aria-hidden="true">
    <path d="${pathD}"/>
  </svg>`;
}

function makeChip(icon, label) {
  const el = document.createElement('span');
  el.className = 'chip';

  const iconWrap = document.createElement('span');
  iconWrap.innerHTML = icon;
  while (iconWrap.firstChild) {
    el.appendChild(iconWrap.firstChild);
  }

  el.appendChild(document.createTextNode(String(label)));
  return el;
}

const nowPlayingHelpers = window.NowPlayingHelpers || {};
const parseVinylTrackRef = nowPlayingHelpers.parseVinylTrackRef || (() => null);
const formatMS = nowPlayingHelpers.formatMS || ((ms) => String(ms));
const computeElapsedMS = nowPlayingHelpers.computeElapsedMS || ((track) => Number(track?.seek_ms || 0));

function isStreamingSource(source) {
  return STREAMING_SOURCES.has(source);
}

function updateStreamingProgress() {
  if (!_lastState) {
    $streamProgress.classList.remove('visible');
    return;
  }

  const source = _lastState.source || 'None';
  const playing = _lastState.state === 'playing';
  const track = _lastState.track || null;
  const durationMS = Number(track?.duration_ms || 0);

  if (!playing || !track || !isStreamingSource(source) || durationMS <= 0) {
    $streamProgress.classList.remove('visible');
    return;
  }

  const elapsedRaw = computeElapsedMS(track, playing);
  const elapsed = Math.min(Math.max(0, elapsedRaw), durationMS);
  const pct = Math.max(0, Math.min(100, (elapsed / durationMS) * 100));

  $streamProgress.classList.add('visible');
  $streamFill.style.width = pct.toFixed(2) + '%';
  $streamElapsed.textContent = formatMS(elapsed);
  $streamTotal.textContent = formatMS(durationMS);
}

// ─── Main UI update ──────────────────────────────────────────────────────────

let _lastState = null;

function applyState(state) {
  const source  = state.source  || 'None';
  const playing = state.state === 'playing';
  const track   = state.track   || null;

  const isIdle = !playing || source === 'None';
  $idle.classList.toggle('visible', isIdle);

  // Source icon + label
  $sourceIcon.innerHTML  = SOURCE_ICONS[source] || SOURCE_ICONS.None;
  $sourceLabel.textContent = SOURCE_LABELS[source] || source;
  $metaSourceIcon.innerHTML = SOURCE_ICONS[source] || SOURCE_ICONS.None;
  $metaSourceLabel.textContent = SOURCE_LABELS[source] || source;

  // Option 1 UX: when there is active playback, keep source context in the
  // in-content badge and hide the separate top bar.
  $sourceBar.classList.toggle('hidden', playing && source !== 'None');

  // Playback badge
  $badge.textContent = playing ? 'Playing' : 'Stopped';
  $badge.classList.toggle('playing', playing);
  $metaPlaybackBadge.textContent = playing ? 'Playing' : 'Stopped';
  $metaPlaybackBadge.classList.toggle('playing', playing);

  // Track metadata
  const hasTrack = track && (track.title || track.artist);
  $identifying.className = '';
  $identifying.textContent = '';

  if (hasTrack) {
    $title.textContent  = track.title  || '—';
    $artist.textContent = track.artist || '';
    $album.textContent  = track.album  || '';
    updateArtwork(track.artwork_path || null);
  } else if (playing && source !== 'None') {
    // Playing but not yet identified (physical source recognizing)
    $title.textContent  = 'Identifying…';
    $artist.textContent = '';
    $album.textContent  = '';
    $identifying.className = 'pulsing';
    $identifying.textContent = 'Listening for a match';
    showDefaultArtwork();
  } else {
    $title.textContent  = '—';
    $artist.textContent = '';
    $album.textContent  = '';
    showDefaultArtwork();
  }

  // Supplemental chips (format-specific metadata)
  $chips.textContent = '';

  if (track) {
    // Streaming: sample rate + bit depth
    if (track.samplerate) {
      $chips.appendChild(makeChip(
        chipSVG('M1 6 Q3 2 5 6 Q7 10 9 6 Q11 2 11 6'),
        track.samplerate
      ));
    }
    if (track.bitdepth) {
      $chips.appendChild(makeChip(
        chipSVG('M2 9 L2 3 M2 6 L6 3 M6 3 L6 9 M6 6 L10 3 M10 3 L10 9'),
        track.bitdepth
      ));
    }

    // Bluetooth codec chip (SBC, AAC, LDAC, AptX, Opus, …)
    if (track.codec) {
      $chips.appendChild(makeChip(
        chipSVG('M6 2 L10 6 L6 10 L6 2 M6 6 L2 2 M6 6 L2 10'),
        track.codec
      ));
    }

    // Track/side chips: supports CD and Vinyl representations.
    if (track.track_number) {
      const trackRef = String(track.track_number).trim();
      const vinylRef = parseVinylTrackRef(trackRef);

      if (source === 'Vinyl') {
        if (vinylRef) {
          $chips.appendChild(makeChip(
            chipSVG('M6 1 A5 5 0 1 1 6 11 A5 5 0 1 1 6 1 M6 1 V11'),
            'Side ' + vinylRef.side
          ));
          $chips.appendChild(makeChip(
            chipSVG('M2 6 Q2 2 6 2 Q10 2 10 6 Q10 10 6 10 Q2 10 2 6'),
            'Track ' + vinylRef.track
          ));
        } else {
          $chips.appendChild(makeChip(
            chipSVG('M2 6 Q2 2 6 2 Q10 2 10 6 Q10 10 6 10 Q2 10 2 6'),
            'Track ' + trackRef
          ));
        }
      } else if (source === 'CD' || source === 'Physical') {
        $chips.appendChild(makeChip(
          chipSVG('M2 6 Q2 2 6 2 Q10 2 10 6 Q10 10 6 10 Q2 10 2 6'),
          'Track ' + trackRef
        ));
      }
    }

    // Physical match chip: shown when a streaming track exists in the local library
    if (track.physical_match && track.physical_match.format) {
      const pm = track.physical_match;
      const fmt = pm.format; // "Vinyl" or "CD"
      const isVinyl = fmt === 'Vinyl';
      // Vinyl icon: disc with groove lines; CD icon: disc with centre hole
      const iconPath = isVinyl
        ? 'M6 1 A5 5 0 1 1 6 11 A5 5 0 1 1 6 1 M6 3 A3 3 0 1 1 6 9 A3 3 0 1 1 6 3'
        : 'M6 1 A5 5 0 1 1 6 11 A5 5 0 1 1 6 1 M6 4.5 A1.5 1.5 0 1 1 6 7.5 A1.5 1.5 0 1 1 6 4.5';
      let label = 'In collection · ' + fmt;
      if (pm.track_number) {
        const vinylRef = isVinyl ? parseVinylTrackRef(pm.track_number) : null;
        if (vinylRef) {
          label += ' · Side ' + vinylRef.side + ' · ' + vinylRef.track;
        } else {
          label += ' · Track ' + pm.track_number;
        }
      }
      const chip = makeChip(chipSVG(iconPath), label);
      chip.classList.add('chip-physical-match');
      $chips.appendChild(chip);
    }
  }

  // Store for change detection
  _lastState = state;
  updateStreamingProgress();
}

// Update streaming elapsed/progress every second using seek_ms +
// (now - seek_updated_at), matching the SPI behavior.
setInterval(updateStreamingProgress, 1000);

// ─── SSE connection ──────────────────────────────────────────────────────────

let _es = null;
let _reconnectTimer = null;

function connect() {
  if (_es) { _es.close(); _es = null; }

  _es = new EventSource('/api/stream');

  _es.onopen = () => {
    clearTimeout(_reconnectTimer);
  };

  _es.onmessage = (e) => {
    try {
      const state = JSON.parse(e.data);
      applyState(state);
    } catch (err) {
      console.warn('nowplaying: bad state payload', err);
    }
  };

  _es.onerror = () => {
    _es.close();
    _es = null;
    // Fixed reconnect delay: retry after 3 s, then the browser will handle reconnects.
    clearTimeout(_reconnectTimer);
    _reconnectTimer = setTimeout(connect, 3000);
  };
}

// Kick off initial connection.
connect();

// ─── Amplifier power state indicator ────────────────────────────────────────
// Single indicator that appears above all screens. Z-index ensures visibility.

async function loadAmpPowerState() {
  try {
    const r = await fetch('/api/amplifier/state');
    if (!r.ok) return; // 404 → amp not configured; keep indicator hidden
    const s = await r.json();
    const maker = String(s.maker || '').trim();
    const model = String(s.model || '').trim();
    const ampName = [maker, model].filter(Boolean).join(' ') || 'Amplifier';

    const el = document.getElementById('amp-indicator');
    const labelEl = document.getElementById('amp-label');
    if (!el) return;

    el.style.display = 'flex';
    if (labelEl) labelEl.textContent = ampName;
    el.title = ampName;
    el.setAttribute('aria-label', ampName);
  } catch { /* network error or amp not available — leave indicator hidden */ }
}

loadAmpPowerState();
setInterval(loadAmpPowerState, 30_000);
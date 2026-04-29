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
const PHYSICAL_IDLE_HOLD_MS = 5000;
const IDENTIFYING_ARTWORK_HOLD_MS = 15000;

// ─── DOM refs ────────────────────────────────────────────────────────────────

const $idle       = document.getElementById('idle-screen');
const $sourceBar = document.getElementById('source-bar');
const $sourceIcon = document.getElementById('source-icon');
const $sourceLabel  = document.getElementById('source-label');
const $metaSourceIcon = document.getElementById('meta-source-icon');
const $metaSourceLabel = document.getElementById('meta-source-label');
const $sourcePlayDot = document.getElementById('source-play-dot');
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

// ─── Ambient colour ──────────────────────────────────────────────────────────

let _ambientEnabled = true;
// After /api/config loads: false when no ALSA capture device (device or device_match) is set.
let _captureInputKnown = false;
let _captureInputConfigured = true;

async function loadAmbientConfig() {
  try {
    const r = await fetch('/api/config', { cache: 'no-store' });
    if (!r.ok) return;
    const cfg = await r.json();
    _ambientEnabled = cfg.now_playing?.ambient_color_enabled ?? true;
    const ai = cfg.audio_input || {};
    const dev = String(ai.device || '').trim();
    const match = String(ai.device_match || '').trim();
    _captureInputConfigured = Boolean(dev || match);
    _captureInputKnown = true;
    if (_lastState) applyState(_lastState);
  } catch { /* network error — keep default */ }
}

function applyAmbientArtwork(url) {
  if (!_ambientEnabled) return;
  const el = document.getElementById('meta-ambient');
  if (!el) return;
  el.style.backgroundImage = `url(${url})`;
  document.getElementById('app')?.classList.add('has-ambient');
}

function clearAmbientColor() {
  const el = document.getElementById('meta-ambient');
  if (el) el.style.backgroundImage = '';
  document.getElementById('app')?.classList.remove('has-ambient');
}

// ─── Artwork helpers ─────────────────────────────────────────────────────────

let _lastArtworkPath = null;
let _keepArtworkUntilMs = 0;

function updateArtwork(artworkPath) {
  if (artworkPath === _lastArtworkPath) return;
  _lastArtworkPath = artworkPath;

  if (artworkPath) {
    const img = new Image();
    img.onload = () => {
      $artImg.src = img.src;
      $artImg.style.opacity = '1';
      $artDefault.style.opacity = '0';
      applyAmbientArtwork(img.src);
      _keepArtworkUntilMs = Date.now() + IDENTIFYING_ARTWORK_HOLD_MS;
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
  if (Date.now() < _keepArtworkUntilMs && $artImg.src && $artImg.style.opacity === '1') {
    return;
  }
  $artImg.style.opacity = '0';
  $artDefault.style.opacity = '1';
  _lastArtworkPath = null;
  clearAmbientColor();
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

  if (!playing || !track || durationMS <= 0) {
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
let _isIdle = true;
let _physicalGapHoldUntilMs = 0;
let _physicalGapHoldTimer = null;
let _wasPhysicalGapIdle = false;

function isPhysicalPlaybackSource(source) {
  return source === 'Physical' || source === 'CD' || source === 'Vinyl';
}

function clearPhysicalGapHold() {
  _physicalGapHoldUntilMs = 0;
  if (_physicalGapHoldTimer) {
    clearTimeout(_physicalGapHoldTimer);
    _physicalGapHoldTimer = null;
  }
}

function schedulePhysicalGapHoldRepaint(waitMs) {
  if (_physicalGapHoldTimer || waitMs <= 0) return;
  _physicalGapHoldTimer = setTimeout(() => {
    _physicalGapHoldTimer = null;
    if (_lastState) applyState(_lastState);
  }, waitMs);
}

function applyState(state) {
  const source  = state.source  || 'None';
  const stateFormat = String(state.format || '').trim();
  const playing = state.state === 'playing';
  const track   = state.track   || null;
  // True only while the source-detector file says Physical. False during the
  // idle-delay tail (amp already off physical) — avoids stuck "Identifying…"
  // when state.state is still "playing" from VU noise on an inactive REC line.
  // Missing field: older state-manager → treat as true for backward compatibility.
  const physicalDetectorOn =
    state.physical_detector_active === true || state.physical_detector_active === undefined;

  const physicalGapIdle = isPhysicalPlaybackSource(source) && state.state === 'idle';
  const nowMs = Date.now();
  if (physicalGapIdle && !_wasPhysicalGapIdle) {
    _physicalGapHoldUntilMs = nowMs + PHYSICAL_IDLE_HOLD_MS;
  } else if (!physicalGapIdle) {
    clearPhysicalGapHold();
  }
  _wasPhysicalGapIdle = physicalGapIdle;

  const holdActive = physicalGapIdle && nowMs < _physicalGapHoldUntilMs;
  if (holdActive) {
    schedulePhysicalGapHoldRepaint(_physicalGapHoldUntilMs - nowMs + 16);
  }

  const effectivePlaying = holdActive ? true : playing;
  const effectiveTrack = holdActive && !track ? (_lastState?.track || null) : track;
  const isIdle = (!effectivePlaying || source === 'None');
  _isIdle = isIdle;
  $idle.classList.toggle('visible', isIdle);
  const $ampInd = document.getElementById('amp-indicator');
  if ($ampInd && isIdle) $ampInd.style.display = 'none';

  // Source icon + label
  $sourceIcon.innerHTML  = SOURCE_ICONS[source] || SOURCE_ICONS.None;
  $sourceLabel.textContent = SOURCE_LABELS[source] || source;
  $metaSourceIcon.innerHTML = SOURCE_ICONS[source] || SOURCE_ICONS.None;
  $metaSourceLabel.textContent = SOURCE_LABELS[source] || source;

  // Option 1 UX: when there is active playback, keep source context in the
  // in-content badge and hide the separate top bar.
  $sourceBar.classList.toggle('hidden', effectivePlaying && source !== 'None');

  // Playback badge
  $badge.textContent = effectivePlaying ? 'Playing' : 'Stopped';
  $badge.classList.toggle('playing', effectivePlaying);
  $sourcePlayDot.classList.toggle('playing', effectivePlaying);
  document.getElementById('app')?.classList.toggle('source-playing', effectivePlaying);

  // Track metadata
  const hasTrack = isRecognizedTrack(effectiveTrack);
  const recognition = state.recognition || null;
  $identifying.className = '';
  $identifying.textContent = '';

  if (hasTrack) {
    $title.textContent  = effectiveTrack.title  || '—';
    $artist.textContent = effectiveTrack.artist || '';
    $album.textContent  = effectiveTrack.album  || '';
    updateArtwork(effectiveTrack.artwork_path || null);
  } else if (effectivePlaying && physicalDetectorOn && (source === 'Physical' || source === 'CD' || source === 'Vinyl')) {
    // Without a configured capture path, the recognizer cannot run — do not show "Identifying…".
    if (_captureInputKnown && !_captureInputConfigured) {
      $title.textContent  = 'Recognition unavailable';
      $artist.textContent = '';
      $album.textContent  = '';
      $identifying.className = '';
      $identifying.textContent = 'Configure audio input in settings';
      // Keep previous artwork during short inter-track/identification transitions.
      showDefaultArtwork();
    } else {
      $title.textContent  = 'Identifying…';
      $artist.textContent = '';
      $album.textContent  = '';
      $identifying.className = 'pulsing';
      if (recognition && recognition.phase === 'off') {
        $identifying.className = '';
        $identifying.textContent = 'Recognition disabled for this input';
      } else if (recognition && recognition.phase === 'no_match') {
        $identifying.textContent = 'Listening for the next match';
      } else {
        $identifying.textContent = 'Listening for a match';
      }
      showDefaultArtwork();
    }
  } else if (effectivePlaying && source !== 'None') {
    // Streaming source playing without metadata (e.g. Bluetooth without AVRCP).
    $title.textContent  = '—';
    $artist.textContent = '';
    $album.textContent  = '';
    showDefaultArtwork();
  } else {
    $title.textContent  = '—';
    $artist.textContent = '';
    $album.textContent  = '';
    showDefaultArtwork();
  }

  // Supplemental chips (format-specific metadata)
  $chips.textContent = '';

  if (hasTrack) {
    const normalizedSource = String(source || '').trim();
    const normalizedFormat = stateFormat.toLowerCase();
    const sourceLooksVinyl = normalizedSource.toLowerCase() === 'vinyl';
    const sourceLooksCD = normalizedSource.toLowerCase() === 'cd';
    const physicalWithVinylFormat = normalizedSource.toLowerCase() === 'physical' && normalizedFormat === 'vinyl';
    const physicalWithCDFormat = normalizedSource.toLowerCase() === 'physical' && normalizedFormat === 'cd';

    // Streaming: sample rate + bit depth merged into one chip
    if (effectiveTrack.samplerate || effectiveTrack.bitdepth) {
      const fmtLabel = [effectiveTrack.samplerate, effectiveTrack.bitdepth].filter(Boolean).join(' · ');
      $chips.appendChild(makeChip(
        chipSVG('M1 6 Q3 2 5 6 Q7 10 9 6 Q11 2 11 6'),
        fmtLabel
      ));
    }

    // Bluetooth codec chip (SBC, AAC, LDAC, AptX, Opus, …)
    if (effectiveTrack.codec) {
      $chips.appendChild(makeChip(
        chipSVG('M6 2 L10 6 L6 10 L6 2 M6 6 L2 2 M6 6 L2 10'),
        effectiveTrack.codec
      ));
    }

    // Track/side chips: supports CD and Vinyl representations.
    if (effectiveTrack.track_number) {
      const trackRef = String(effectiveTrack.track_number).trim();
      const vinylRef = parseVinylTrackRef(trackRef);
      const shouldRenderVinyl = sourceLooksVinyl || physicalWithVinylFormat || (!!vinylRef && normalizedSource.toLowerCase() === 'physical');
      const shouldRenderCD = sourceLooksCD || physicalWithCDFormat;

      if (shouldRenderVinyl) {
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
      } else if (shouldRenderCD || normalizedSource.toLowerCase() === 'physical') {
        $chips.appendChild(makeChip(
          chipSVG('M2 6 Q2 2 6 2 Q10 2 10 6 Q10 10 6 10 Q2 10 2 6'),
          'Track ' + trackRef
        ));
      }
    }

    // Physical match chip: shown when a streaming track exists in the local library
    if (effectiveTrack.physical_match && effectiveTrack.physical_match.format) {
      const pm = effectiveTrack.physical_match;
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

function isRecognizedTrack(track) {
  if (!track) return false;
  const title = String(track.title || '').trim().toLowerCase();
  const artist = String(track.artist || '').trim().toLowerCase();
  const invalidTitles = new Set(['unrecognized', 'unknown', 'identifying…', 'identifying...']);
  if (invalidTitles.has(title)) return false;
  return Boolean(title || artist);
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

// Load ambient colour setting from config.
loadAmbientConfig();

// ─── Amplifier line (read-only chip) ───────────────────────────────────────────
// Matches amplifier/model.js renderAmpInputSelect: when a connected device names
// a single input, that device name replaces the logical input (e.g. Phono) label.

/**
 * @param {{ id?: string, logical_name?: string, visible?: boolean }|null|undefined} input
 * @param {Array<{ name?: string, input_ids?: string[] }>} connectedDevices
 * @returns {string}
 */
function resolveAmpInputDropdownLabel(input, connectedDevices) {
  if (!input || !input.id) return '';
  const inputId = String(input.id);
  const inputToDevice = new Map();
  (connectedDevices || []).forEach((dev) => {
    const name = String(dev?.name || '').trim();
    if (!name) return;
    (dev.input_ids || []).forEach((id) => {
      inputToDevice.set(String(id), name);
    });
  });
  const devName = inputToDevice.get(inputId);
  if (devName) {
    const dev = (connectedDevices || []).find((d) =>
      (d.input_ids || []).map(String).includes(inputId)
    );
    const multiInput = !!(dev && (dev.input_ids || []).length > 1);
    const logical = String(input.logical_name || '').trim();
    return multiInput ? `${devName} — ${logical || 'Input'}` : devName;
  }
  return String(input.logical_name || '').trim() || inputId;
}

/**
 * @param {Array<{ id?: string, logical_name?: string, visible?: boolean }>} inputs
 * @param {string} lastKnownId
 */
function pickAmplifierInputForLine(inputs, lastKnownId) {
  const list = (inputs || []).filter((it) => it && it.id && it.visible);
  if (!list.length) return null;
  const id = String(lastKnownId || '').trim();
  if (id) {
    const found = list.find((it) => String(it.id) === id);
    if (found) return found;
  }
  return list[0];
}

async function loadAmpPowerState() {
  const el = document.getElementById('amp-indicator');
  const labelEl = document.getElementById('amp-label');
  const sepEl = document.getElementById('amp-indicator-sep');
  const inputLineEl = document.getElementById('amp-input-line');
  if (!el || !labelEl) return;

  let cfg = null;
  try {
    const rc = await fetch('/api/config', { cache: 'no-store' });
    if (rc.ok) cfg = await rc.json();
  } catch { /* ignore — still show amp name from state */ }

  try {
    const r = await fetch('/api/amplifier/state');
    if (!r.ok) {
      el.style.display = 'none';
      return;
    }
    const s = await r.json();
    const maker = String(s.maker || '').trim();
    const model = String(s.model || '').trim();
    const ampName = [maker, model].filter(Boolean).join(' ') || 'Amplifier';

    el.style.display = _isIdle ? 'none' : 'flex';
    labelEl.textContent = ampName;

    const ps = String(s.power_state || '').toLowerCase();
    el.classList.toggle('ps-on', ps === 'on' || ps === 'warming_up');
    el.classList.toggle('ps-off', ps === 'off' || ps === 'standby');

    const amp = cfg?.amplifier;
    const inputs = Array.isArray(amp?.inputs) ? amp.inputs : [];
    const connected = Array.isArray(amp?.connected_devices) ? amp.connected_devices : [];
    const lastId = String(cfg?.amplifier_runtime?.last_known_input_id ?? '').trim();
    const input = pickAmplifierInputForLine(inputs, lastId);
    const second = input ? resolveAmpInputDropdownLabel(input, connected) : '';

    if (sepEl && inputLineEl) {
      if (second) {
        sepEl.style.display = '';
        inputLineEl.style.display = '';
        inputLineEl.textContent = second;
      } else {
        sepEl.style.display = 'none';
        inputLineEl.style.display = 'none';
        inputLineEl.textContent = '';
      }
    }

    const fullTitle = second ? `${ampName} · ${second}` : ampName;
    el.title = fullTitle;
    el.setAttribute('aria-label', fullTitle);
  } catch {
    el.style.display = 'none';
  }
}

loadAmpPowerState();
setInterval(loadAmpPowerState, 30_000);
(function (root, factory) {
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
    return;
  }
  root.NowPlayingHelpers = factory();
}(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  function parseVinylTrackRef(value) {
    const raw = String(value || '').trim().toUpperCase();
    if (!raw) return null;

    // Accept A1, B2, C-3, D.4
    let m = /^([A-D])\s*[-.]?\s*(\d{1,2})$/.exec(raw);
    if (m) {
      return { side: m[1], track: String(parseInt(m[2], 10)) };
    }

    // Accept 1A, 2-B, 3.C
    m = /^(\d{1,2})\s*[-.]?\s*([A-D])$/.exec(raw);
    if (m) {
      return { side: m[2], track: String(parseInt(m[1], 10)) };
    }

    return null;
  }

  function formatMS(ms) {
    const totalSec = Math.max(0, Math.floor(ms / 1000));
    const h = Math.floor(totalSec / 3600);
    const m = Math.floor((totalSec % 3600) / 60);
    const s = totalSec % 60;
    if (h > 0) {
      return String(h) + ':' + String(m).padStart(2, '0') + ':' + String(s).padStart(2, '0');
    }
    return String(m) + ':' + String(s).padStart(2, '0');
  }

  function computeElapsedMS(track, playing, nowMS) {
    const seekMS = Number(track && track.seek_ms || 0);
    if (!playing) return seekMS;

    const updatedAt = track && track.seek_updated_at ? Date.parse(track.seek_updated_at) : NaN;
    if (Number.isNaN(updatedAt)) return seekMS;

    const now = typeof nowMS === 'number' ? nowMS : Date.now();
    const drift = now - updatedAt;
    return seekMS + Math.max(0, drift);
  }

  return {
    parseVinylTrackRef,
    formatMS,
    computeElapsedMS,
  };
}));

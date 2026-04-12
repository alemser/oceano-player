"use strict";

// SOURCE_ICONS maps source and amplifier input names to SVG strings.
//
// Source names (title-case): AirPlay, Bluetooth, UPnP, Vinyl, CD, Physical, None,
//                             Spotify, Tidal, AppleMusic
// Input names (lowercase):   dvd, tape, fm, dab, line, aux, tv, optical, coax, usb, default
//
// To add a new streaming provider: add an entry here and load icons.js in the
// relevant HTML page — no other file needs to change.
const SOURCE_ICONS = {
  // ── Playback sources ─────────────────────────────────────────────────────
  AirPlay:    `<svg viewBox="0 0 32 32" fill="currentColor" aria-hidden="true"><path d="M16 22 L8 30 L24 30 Z"/><path d="M16 6C21.5 6 26 10.5 26 16L24 16C24 11.6 20.4 8 16 8C11.6 8 8 11.6 8 16L6 16C6 10.5 10.5 6 16 6Z"/><path d="M16 11C19.3 11 22 13.7 22 17L20 17C20 14.8 18.2 13 16 13C13.8 13 12 14.8 12 17L10 17C10 13.7 12.7 11 16 11Z"/><path d="M16 16C17.7 16 19 17.3 19 19L17 19C17 18.4 16.6 18 16 18C15.4 18 15 18.4 15 19L13 19C13 17.3 14.3 16 16 16Z"/></svg>`,
  Bluetooth:  `<svg viewBox="0 0 32 32" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="10,22 22,10 16,4 16,28 22,22 10,10"/></svg>`,
  UPnP:       `<svg viewBox="0 0 32 32" fill="currentColor" aria-hidden="true"><circle cx="16" cy="8" r="3"/><circle cx="6" cy="24" r="3"/><circle cx="26" cy="24" r="3"/><line x1="16" y1="11" x2="6" y2="21" stroke="currentColor" stroke-width="1.5"/><line x1="16" y1="11" x2="26" y2="21" stroke="currentColor" stroke-width="1.5"/><line x1="8" y1="24" x2="24" y2="24" stroke="currentColor" stroke-width="1.5"/></svg>`,
  Vinyl:      `<svg viewBox="0 0 32 32" fill="none" aria-hidden="true"><circle cx="16" cy="16" r="13" stroke="currentColor" stroke-width="1.5"/><circle cx="16" cy="16" r="9.5" stroke="currentColor" stroke-width="0.8" opacity="0.5"/><circle cx="16" cy="16" r="6" stroke="currentColor" stroke-width="0.8" opacity="0.35"/><circle cx="16" cy="16" r="2.5" fill="currentColor" opacity="0.6"/><path d="M23 6 L28 11" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" opacity="0.9"/><path d="M28 11 L22.3 16.7" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" opacity="0.9"/><circle cx="22.1" cy="16.9" r="1.1" fill="currentColor" opacity="0.95"/></svg>`,
  CD:         `<svg viewBox="0 0 32 32" fill="none" aria-hidden="true"><circle cx="16" cy="16" r="13" stroke="currentColor" stroke-width="1.5"/><circle cx="16" cy="16" r="8" stroke="currentColor" stroke-width="0.8" opacity="0.35"/><circle cx="16" cy="16" r="3" stroke="currentColor" stroke-width="1.5"/><path d="M16 3 A13 13 0 0 1 29 16" stroke="currentColor" stroke-width="3" stroke-linecap="round" opacity="0.5"/></svg>`,
  Physical:   `<svg viewBox="0 0 32 32" fill="none" aria-hidden="true"><circle cx="16" cy="16" r="13" stroke="currentColor" stroke-width="1.5"/><circle cx="16" cy="16" r="8" stroke="currentColor" stroke-width="0.8" opacity="0.4"/><circle cx="16" cy="16" r="3.5" fill="currentColor" opacity="0.5"/></svg>`,
  None:       `<svg viewBox="0 0 32 32" fill="currentColor" aria-hidden="true"><rect x="2" y="14" width="6" height="10" rx="3" opacity="0.25"/><rect x="10" y="9" width="6" height="15" rx="3" opacity="0.25"/><rect x="18" y="5" width="6" height="19" rx="3" opacity="0.25"/><rect x="26" y="12" width="6" height="12" rx="3" opacity="0.25"/></svg>`,

  // ── Streaming providers ───────────────────────────────────────────────────
  // Three horizontal arcs — characteristic Spotify wave pattern.
  Spotify:    `<svg viewBox="0 0 32 32" fill="none" aria-hidden="true"><circle cx="16" cy="16" r="13" stroke="currentColor" stroke-width="1.5"/><path d="M9 14c3.5-2 9-2 14 0" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/><path d="M10 18.5c3-1.5 9-1.5 12 0" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/><path d="M11 23c2.5-1.2 7.5-1.2 10 0" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/></svg>`,
  // Interlocking diamond motif from Tidal's logo.
  Tidal:      `<svg viewBox="0 0 32 32" fill="currentColor" aria-hidden="true"><path d="M16 4L23 13L16 22L9 13Z"/><path d="M9 13L16 22L9 28L2 22Z" opacity="0.6"/><path d="M23 13L30 22L23 28L16 22Z" opacity="0.6"/></svg>`,
  // Rounded square with a music note — evokes the Apple Music icon shape.
  AppleMusic: `<svg viewBox="0 0 32 32" fill="none" aria-hidden="true"><rect x="3" y="3" width="26" height="26" rx="5" stroke="currentColor" stroke-width="1.5"/><path d="M20 9v11M14 11v9" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/><path d="M14 11l6-2" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/><circle cx="14" cy="20" r="2.5" fill="currentColor"/><circle cx="20" cy="18" r="2.5" fill="currentColor"/></svg>`,

  // ── Amplifier inputs (lowercase keys) ────────────────────────────────────
  dvd:     `<svg viewBox="0 0 32 32" fill="none" aria-hidden="true"><ellipse cx="16" cy="16" rx="13" ry="10" stroke="currentColor" stroke-width="1.5"/><ellipse cx="16" cy="16" rx="7" ry="4.5" stroke="currentColor" stroke-width="1.2" opacity="0.5"/><circle cx="16" cy="16" r="2.2" fill="currentColor" opacity="0.7"/></svg>`,
  tape:    `<svg viewBox="0 0 32 32" fill="none" aria-hidden="true"><rect x="5" y="10" width="22" height="12" rx="4" stroke="currentColor" stroke-width="1.5"/><circle cx="11" cy="16" r="2.2" stroke="currentColor" stroke-width="1.2"/><circle cx="21" cy="16" r="2.2" stroke="currentColor" stroke-width="1.2"/><rect x="13.5" y="14.5" width="5" height="3" rx="1.2" stroke="currentColor" stroke-width="1.1"/></svg>`,
  line:    `<svg viewBox="0 0 32 32" fill="none" aria-hidden="true"><rect x="7" y="13" width="18" height="6" rx="2.5" stroke="currentColor" stroke-width="1.5"/><rect x="13" y="10" width="6" height="12" rx="2" stroke="currentColor" stroke-width="1.2" opacity="0.5"/></svg>`,
  fm:      `<svg viewBox="0 0 32 32" fill="none" aria-hidden="true"><rect x="6" y="18" width="20" height="6" rx="2.5" stroke="currentColor" stroke-width="1.5"/><path d="M16 18V8" stroke="currentColor" stroke-width="1.5"/><circle cx="16" cy="8" r="2" fill="currentColor"/></svg>`,
  dab:     `<svg viewBox="0 0 32 32" fill="none" aria-hidden="true"><rect x="6" y="18" width="20" height="6" rx="2.5" stroke="currentColor" stroke-width="1.5"/><rect x="10" y="10" width="12" height="6" rx="2" stroke="currentColor" stroke-width="1.2" opacity="0.5"/></svg>`,
  aux:     `<svg viewBox="0 0 32 32" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><circle cx="16" cy="22" r="3"/><path d="M16 19V10"/><path d="M12 14L16 10L20 14"/></svg>`,
  tv:      `<svg viewBox="0 0 32 32" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="4" y="8" width="24" height="16" rx="2"/><path d="M10 8L16 3L22 8"/></svg>`,
  optical: `<svg viewBox="0 0 32 32" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M16 4v24"/><path d="M7 10q9 5 18 0"/><path d="M7 22q9-5 18 0"/></svg>`,
  coax:    `<svg viewBox="0 0 32 32" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><circle cx="16" cy="16" r="3.5"/><circle cx="16" cy="16" r="8" stroke-dasharray="3.5 3"/><path d="M16 5V3M16 29V27M5 16H3M29 16H27"/></svg>`,
  usb:     `<svg viewBox="0 0 32 32" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M16 4v16"/><path d="M12 17l4 4 4-4"/><path d="M10 9v3H7V9h3"/><path d="M22 8l3 3-3 3V8"/></svg>`,
  default: `<svg viewBox="0 0 32 32" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="5" y="13" width="22" height="12" rx="2"/><path d="M9 13V9a7 7 0 0 1 14 0v4"/></svg>`,
};

/**
 * Returns the SVG markup for a playback source name.
 * Falls back to the "None" icon for unknown names.
 * @param {string} name  Source name, e.g. "AirPlay", "Spotify", "Vinyl"
 * @returns {string}     SVG string ready to assign to element.innerHTML
 */
function sourceIcon(name) {
  return SOURCE_ICONS[name] || SOURCE_ICONS.None;
}

/**
 * Returns the SVG markup for an amplifier input name.
 * The lookup is case-insensitive; trailing numbers are stripped
 * so "Aux 1" and "Aux 2" both resolve to the "aux" icon.
 * Falls back to the "default" icon for unknown input names.
 * @param {string} name  Input name, e.g. "FM", "Aux 1", "USB"
 * @returns {string}     SVG string ready to assign to element.innerHTML
 */
function inputIcon(name) {
  const key = (name || '').toLowerCase().replace(/\s+\d+$/, '');
  return SOURCE_ICONS[key] || SOURCE_ICONS.default;
}

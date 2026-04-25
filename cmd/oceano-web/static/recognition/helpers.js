'use strict';

// ── DOM / form helpers ─────────────────────────────────────────────────────────

function _rval(id)             { return (document.getElementById(id)?.value ?? '').trim(); }
function _rset(id, v)          { const el = document.getElementById(id); if (el) el.value = v ?? ''; }
function _rint(id, fallback)   { const n = parseInt(_rval(id), 10); return Number.isNaN(n) ? fallback : n; }
function _rfloat(id, fallback) { const n = parseFloat(_rval(id)); return Number.isNaN(n) ? fallback : n; }
function _cfgInt(v, fallback)  { const n = Number.parseInt(v, 10); return Number.isNaN(n) ? fallback : n; }
function _cfgFloat(v, fallback){ const n = Number.parseFloat(v); return Number.isNaN(n) ? fallback : n; }

function _esc(s) {
  return String(s || '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// ── Toast ──────────────────────────────────────────────────────────────────────

function toast(msg, isError = false) {
  const el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.className = isError ? 'error show' : 'show';
  clearTimeout(el._t);
  el._t = setTimeout(() => el.className = '', 3500);
}

'use strict';

function _aval(id) {
  return (document.getElementById(id)?.value ?? '').trim();
}
function _aset(id, v) {
  const el = document.getElementById(id);
  if (el) el.value = v ?? '';
}
function _aint(id, fallback) {
  const n = parseInt(_aval(id), 10);
  return Number.isNaN(n) ? fallback : n;
}

async function loadAdvancedPage() {
  let cfg;
  try {
    const r = await fetch('/api/config');
    if (!r.ok) { toast('Failed to load configuration.', true); return; }
    cfg = await r.json();
  } catch {
    toast('Failed to load configuration.', true);
    return;
  }

  const guard = document.getElementById('adv-streaming-usb-guard-enabled');
  if (guard) guard.checked = cfg.advanced?.streaming_usb_guard_enabled ?? true;

  _aset('adv-library-db',  cfg.advanced?.library_db ?? '');
  _aset('adv-idle-delay',  cfg.advanced?.idle_delay_secs ?? 10);
  _aset('adv-session-gap', cfg.advanced?.session_gap_threshold_secs ?? 45);
  _aset('adv-vu-socket',   cfg.advanced?.vu_socket ?? '');
  _aset('adv-pcm-socket',  cfg.advanced?.pcm_socket ?? '');
  _aset('adv-source-file', cfg.advanced?.source_file ?? '');
  _aset('adv-state-file',  cfg.advanced?.state_file ?? '');
  _aset('adv-artwork-dir', cfg.advanced?.artwork_dir ?? '');
  _aset('adv-metadata-pipe', cfg.advanced?.metadata_pipe ?? '');
}

async function saveAdvancedPage() {
  const btn = document.getElementById('btn-adv-page-save');
  if (btn) { btn.disabled = true; btn.textContent = 'Saving…'; }

  let fullCfg;
  try {
    const r = await fetch('/api/config');
    if (!r.ok) throw new Error('load failed');
    fullCfg = await r.json();
  } catch {
    toast('Failed to load current config before saving.', true);
    if (btn) { btn.disabled = false; btn.textContent = 'Save & Restart Services'; }
    return;
  }

  const guard = document.getElementById('adv-streaming-usb-guard-enabled');
  fullCfg.advanced = {
    ...(fullCfg.advanced ?? {}),
    streaming_usb_guard_enabled: guard?.checked ?? (fullCfg.advanced?.streaming_usb_guard_enabled ?? true),
    library_db:     _aval('adv-library-db')   || fullCfg.advanced?.library_db   || '',
    idle_delay_secs:            _aint('adv-idle-delay', 10),
    session_gap_threshold_secs: _aint('adv-session-gap', 45),
    vu_socket:      _aval('adv-vu-socket')    || fullCfg.advanced?.vu_socket    || '',
    pcm_socket:     _aval('adv-pcm-socket')   || fullCfg.advanced?.pcm_socket   || '',
    source_file:    _aval('adv-source-file')  || fullCfg.advanced?.source_file  || '',
    state_file:     _aval('adv-state-file')   || fullCfg.advanced?.state_file   || '',
    artwork_dir:    _aval('adv-artwork-dir')  || fullCfg.advanced?.artwork_dir  || '',
    metadata_pipe:  _aval('adv-metadata-pipe')|| fullCfg.advanced?.metadata_pipe|| '',
  };

  try {
    const r = await fetch('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(fullCfg),
    });
    const res = await r.json().catch(() => ({}));
    if (!r.ok) {
      toast(res.error || 'Save failed.', true);
    } else {
      toast('Saved — services restarting…');
    }
  } catch {
    toast('Save failed.', true);
  }

  if (btn) { btn.disabled = false; btn.textContent = 'Save & Restart Services'; }
}

function toast(msg, isError = false) {
  const el = document.getElementById('toast');
  if (!el) return;
  el.textContent = msg;
  el.className = isError ? 'error show' : 'show';
  clearTimeout(el._t);
  el._t = setTimeout(() => el.className = '', 3500);
}

document.addEventListener('DOMContentLoaded', loadAdvancedPage);

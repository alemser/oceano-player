// ── Amplifier / CD player control ────────────────────────────────────────────
let _ampConfig = {};
let _cdConfig  = {};

// ── IR Learning ───────────────────────────────────────────────────────────────

const AMP_COMMANDS = [
  { id: 'power_on',    label: 'Power On' },
  { id: 'power_off',   label: 'Power Off' },
  { id: 'volume_up',   label: 'Volume +' },
  { id: 'volume_down', label: 'Volume −' },
  { id: 'next_input',  label: 'Input ▲ (next)' },
  { id: 'prev_input',  label: 'Input ▼ (prev)' },
];

const CD_COMMANDS = [
  { id: 'power_on',  label: 'Power On' },
  { id: 'power_off', label: 'Power Off' },
  { id: 'play',      label: 'Play' },
  { id: 'pause',     label: 'Pause' },
  { id: 'stop',      label: 'Stop' },
  { id: 'next',      label: 'Next Track' },
  { id: 'previous',  label: 'Prev Track' },
  { id: 'eject',     label: 'Eject' },
];

let _learnPoll = null; // setInterval handle

function renderIRTable(tableId, commands, device, irCodes) {
  const el = document.getElementById(tableId);
  if (!el) return;
  el.innerHTML = '';
  commands.forEach(cmd => {
    const configured = !!(irCodes && irCodes[cmd.id]);
    const row = document.createElement('div');
    row.className = 'ir-row';
    row.id = `ir-row-${device}-${cmd.id}`;
    row.innerHTML = `
      <span class="ir-label">${cmd.label}</span>
      <span class="ir-status ${configured ? 'ir-ok' : 'ir-missing'}" id="ir-status-${device}-${cmd.id}">
        ${configured ? '✓' : '—'}
      </span>
      <button type="button" class="ir-learn-btn" id="ir-btn-${device}-${cmd.id}"
              onclick="learnCommand('${cmd.id}','${device}')">Learn</button>`;
    el.appendChild(row);
  });
}

async function learnCommand(command, device) {
  const btn = document.getElementById(`ir-btn-${device}-${command}`);
  const statusEl = document.getElementById(`ir-status-${device}-${command}`);
  if (!btn) return;

  // Cancel any previous poll
  if (_learnPoll) { clearInterval(_learnPoll); _learnPoll = null; }

  btn.disabled = true;
  btn.textContent = 'Listening…';
  btn.classList.add('ir-learning');
  if (statusEl) { statusEl.textContent = '…'; statusEl.className = 'ir-status ir-listening'; }

  try {
    const r = await fetch('/api/broadlink/learn-start', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ command, device }),
    });
    if (!r.ok) {
      const err = await r.json();
      setLearnResult(btn, statusEl, 'error', err.error || 'Failed to start');
      return;
    }
  } catch (e) {
    setLearnResult(btn, statusEl, 'error', 'Network error');
    return;
  }

  // Poll for result
  _learnPoll = setInterval(async () => {
    try {
      const r = await fetch('/api/broadlink/learn-status');
      if (!r.ok) return;
      const s = await r.json();
      if (s.status === 'listening') return; // still waiting

      clearInterval(_learnPoll);
      _learnPoll = null;

      if (s.status === 'captured') {
        // Keep local config in sync so Save & Restart includes the new code.
        if (device === 'amplifier') {
          if (!_ampConfig.ir_codes) _ampConfig.ir_codes = {};
          _ampConfig.ir_codes[command] = s.code;
        } else {
          if (!_cdConfig.ir_codes) _cdConfig.ir_codes = {};
          _cdConfig.ir_codes[command] = s.code;
        }
        setLearnResult(btn, statusEl, 'ok', null);
      } else {
        setLearnResult(btn, statusEl, 'error', s.message || s.status);
      }
    } catch { /* network blip — keep polling */ }
  }, 600);

  // Safety timeout: stop polling after 35 s
  setTimeout(() => {
    if (_learnPoll) {
      clearInterval(_learnPoll);
      _learnPoll = null;
      setLearnResult(btn, statusEl, 'error', 'No response');
    }
  }, 35000);
}

function setLearnResult(btn, statusEl, result, msg) {
  btn.disabled = false;
  btn.classList.remove('ir-learning');
  if (result === 'ok') {
    btn.textContent = 'Learn';
    if (statusEl) { statusEl.textContent = '✓'; statusEl.className = 'ir-status ir-ok'; }
  } else {
    btn.textContent = 'Retry';
    if (statusEl) { statusEl.textContent = '✗'; statusEl.className = 'ir-status ir-error'; }
    if (msg) showToast(msg, 'error');
  }
}

// ── Inputs editor ─────────────────────────────────────────────────────────────

// labelToId derives a stable ID from a human label.
// "USB Audio" → "USB_AUDIO", "Phono" → "PHONO"
function labelToId(label) {
  return (label ?? '').trim().toUpperCase()
    .replace(/[^A-Z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '') || 'INPUT';
}

function renderAmpInputsList(inputs) {
  const list = document.getElementById('amp-inputs-list');
  if (!list) return;
  const showHidden = document.getElementById('amp-show-hidden')?.checked ?? true;
  list.innerHTML = '';
  inputs.forEach((inp, i) => {
    const row = buildInputRow(inp, i);
    if (!inp.visible && !showHidden) row.style.display = 'none';
    list.appendChild(row);
  });
}

function buildInputRow(inp, idx) {
  const row = document.createElement('div');
  row.className = 'amp-input-row';
  row.draggable = true;
  row.dataset.idx = idx;
  // Store the original id so edits to the label don't silently change the id.
  // On new rows (empty id) the id will be derived from the label at save time.
  row.dataset.inputId = inp.id ?? '';

  row.innerHTML = `
    <span class="amp-input-drag" title="Drag to reorder">⠿</span>
    <input class="amp-input-label" type="text" placeholder="Name (e.g. USB Audio)"
           value="${esc(inp.label ?? '')}" oninput="refreshDefaultInputDropdown()">
    <button type="button" class="btn-input-visible ${inp.visible ? 'is-visible' : ''}"
            title="${inp.visible ? 'Visible in UI' : 'Hidden in UI'}"
            onclick="toggleInputVisible(this)">
      ${inp.visible ? '👁' : '○'}
    </button>
    <button type="button" class="btn-input-remove" title="Remove" onclick="removeInputRow(this)">✕</button>`;

  row.addEventListener('dragstart', e => {
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', row.dataset.idx);
    row.style.opacity = '0.4';
  });
  row.addEventListener('dragend', () => { row.style.opacity = ''; });
  row.addEventListener('dragover', e => { e.preventDefault(); row.classList.add('drag-over'); });
  row.addEventListener('dragleave', () => row.classList.remove('drag-over'));
  row.addEventListener('drop', e => {
    e.preventDefault();
    row.classList.remove('drag-over');
    const fromIdx = parseInt(e.dataTransfer.getData('text/plain'), 10);
    const toIdx   = parseInt(row.dataset.idx, 10);
    if (fromIdx === toIdx) return;
    const inputs = collectAmpInputs();
    const [moved] = inputs.splice(fromIdx, 1);
    inputs.splice(toIdx, 0, moved);
    renderAmpInputsList(inputs);
    refreshDefaultInputDropdown();
  });

  return row;
}

function ampAddInput() {
  const inputs = collectAmpInputs();
  inputs.push({ label: '', id: '', visible: true });
  // Ensure show-hidden is on so the new row is visible
  const cb = document.getElementById('amp-show-hidden');
  if (cb) cb.checked = true;
  renderAmpInputsList(inputs);
  refreshDefaultInputDropdown();
  const rows = document.querySelectorAll('.amp-input-row');
  if (rows.length) rows[rows.length - 1].querySelector('.amp-input-label')?.focus();
}

function removeInputRow(btn) {
  btn.closest('.amp-input-row').remove();
  refreshDefaultInputDropdown();
}

function toggleInputVisible(btn) {
  const isNowVisible = !btn.classList.contains('is-visible');
  btn.classList.toggle('is-visible', isNowVisible);
  btn.title = isNowVisible ? 'Visible in UI' : 'Hidden in UI';
  btn.textContent = isNowVisible ? '👁' : '○';
  // If hiding and show-hidden is off, remove the row from view
  if (!isNowVisible) {
    const showHidden = document.getElementById('amp-show-hidden')?.checked ?? true;
    if (!showHidden) btn.closest('.amp-input-row').style.display = 'none';
  }
}

function toggleShowHidden() {
  const inputs = collectAmpInputs();
  renderAmpInputsList(inputs);
}

// collectAmpInputs reads current row state from the DOM (visible or hidden rows).
function collectAmpInputs() {
  return Array.from(document.querySelectorAll('.amp-input-row')).map(row => {
    const label = row.querySelector('.amp-input-label')?.value ?? '';
    // Use stored id if available (data attr set on load), else derive from label.
    const id = row.dataset.inputId || labelToId(label);
    return {
      label,
      id,
      visible: row.querySelector('.btn-input-visible')?.classList.contains('is-visible') ?? false,
    };
  });
}

// refreshDefaultInputDropdown rebuilds the dropdown from current input rows
// and re-selects the previously chosen value (or the passed value on load).
function refreshDefaultInputDropdown(selectValue) {
  const sel = document.getElementById('amp-default-input');
  if (!sel) return;
  const prev = selectValue ?? sel.value;
  const inputs = collectAmpInputs();
  sel.innerHTML = '<option value="">— select —</option>' +
    inputs.filter(i => i.id).map(i =>
      `<option value="${esc(i.id)}"${i.id === prev ? ' selected' : ''}>${esc(i.label || i.id)}</option>`
    ).join('');
}

function updateAmpPanel() {
  const ampEnabled = document.getElementById('amp-enabled')?.checked;
  const cdEnabled  = document.getElementById('cd-enabled')?.checked;
  const panel    = document.getElementById('amp-panel');
  const cdWidget = document.getElementById('cd-widget');
  if (panel)    panel.style.display    = ampEnabled ? '' : 'none';
  if (cdWidget) cdWidget.style.display = (ampEnabled && cdEnabled) ? '' : 'none';
}

function updateAmpIRSummary(irCodes) {
  renderIRTable('amp-ir-table', AMP_COMMANDS, 'amplifier', irCodes);
}

function updateCDIRSummary(irCodes) {
  renderIRTable('cd-ir-table', CD_COMMANDS, 'cdplayer', irCodes);
}

async function loadAmplifierState() {
  try {
    const r = await fetch('/api/amplifier/state');
    if (!r.ok) return;
    renderAmpWidget(await r.json());
  } catch { /* not configured or offline — widget stays hidden */ }

  try {
    const r = await fetch('/api/cdplayer/state');
    const ampEnabled = document.getElementById('amp-enabled')?.checked;
    const cdEnabled = document.getElementById('cd-enabled')?.checked;
    if (!r.ok || !ampEnabled || !cdEnabled) { document.getElementById('cd-widget').style.display = 'none'; return; }
    const state = await r.json();
    const title = document.getElementById('cd-widget-title');
    if (title) title.textContent = `${state.maker} ${state.model}`;
    document.getElementById('cd-widget').style.display = '';
  } catch { /* not configured */ }
}

function handleAmpHeaderKey(event, widgetId) {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    toggleAmpWidget(widgetId);
  }
}

function toggleAmpWidget(widgetId) {
  const widget = document.getElementById(widgetId);
  if (!widget) return;
  const isExpanded = widget.classList.toggle('expanded');
  const header = widget.querySelector('.amp-header');
  if (header) header.setAttribute('aria-expanded', isExpanded);
}

function renderAmpWidget(state) {
  const panel = document.getElementById('amp-panel');
  if (!state || !panel) return;

  const ampEnabled = document.getElementById('amp-enabled')?.checked;
  if (ampEnabled === false) return;
  panel.style.display = '';

  const title = document.getElementById('amp-widget-title');
  if (title) title.textContent = `${state.maker} ${state.model}`;

  // Hardware-detected power label ⏻ ON/OFF/? (always visible in collapsed header)
  const pwrLabel = document.getElementById('amp-power-label');
  const pwrText  = document.getElementById('amp-power-label-text');
  if (pwrLabel && pwrText) {
    const ps    = state.detected_power_state ?? 'unknown';
    const model = state.model || state.maker || 'Amp';
    pwrLabel.className = `amp-power-label${ps === 'on' ? ' ps-on' : ps === 'off' ? ' ps-off' : ''}`;
    pwrText.textContent = ps === 'on' ? 'ON' : ps === 'off' ? 'OFF' : '?';
    pwrLabel.title = `${model} — Detected: ${ps === 'on' ? 'On' : ps === 'off' ? 'Off' : '—'}`;
  }

  // Power button state
  _ampPowerOn = state.power_on ?? false;
  const pwrBtn = document.getElementById('btn-amp-power');
  const pwrBtnLabel = document.getElementById('btn-amp-power-label');
  if (pwrBtn) {
    pwrBtn.classList.toggle('pwr-on',  _ampPowerOn);
    pwrBtn.classList.toggle('pwr-off', !_ampPowerOn);
    pwrBtn.title = _ampPowerOn ? 'Power off' : 'Power on';
  }
  if (pwrBtnLabel) pwrBtnLabel.textContent = _ampPowerOn ? 'Power Off' : 'Power On';

  // Software ready state chip (visible next to pill)
  const dot   = document.getElementById('amp-ready-dot');
  const label = document.getElementById('amp-ready-label');
  if (dot && label) {
    if (!state.power_on) {
      dot.className = 'amp-ready-dot';
      label.textContent = '';
    } else if (state.audio_ready) {
      dot.className = 'amp-ready-dot ready';
      label.textContent = state.current_input?.label ?? 'Ready';
    } else {
      dot.className = 'amp-ready-dot';
      label.textContent = state.audio_ready_at ? 'Warming up…' : 'Switching…';
    }
  }

  // Input label
  const inputLabel = document.getElementById('amp-input-label');
  if (inputLabel) {
    inputLabel.textContent = state.current_input?.label ?? '—';
  }

  // Keep sync panel input list up to date
  if (Array.isArray(state.input_list)) {
    _syncInputList = state.input_list;
    const syncPanel = document.getElementById('amp-sync-panel');
    if (syncPanel && syncPanel.style.display !== 'none') _renderSyncPanel();
  }

  // Warn user to sync when the assumed input may not match the physical amp
  const syncBtn = document.getElementById('btn-amp-sync');
  if (syncBtn) {
    const needsSync = !state.input_synced;
    syncBtn.classList.toggle('needs-sync', needsSync);
    syncBtn.title = needsSync
      ? 'Sync required — set the actual input the amp is on'
      : 'Sync — set assumed input without IR';
  }
}

// ── Power toggle ──────────────────────────────────────────────────────────────

let _ampPowerOn = false;

async function ampTogglePower() {
  await ampPower(_ampPowerOn ? 'off' : 'on');
}

async function ampPower(action) {
  await fetch('/api/amplifier/power', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({action}),
  });
  setTimeout(loadAmplifierState, 300);
}

// ── Volume hold-to-repeat ─────────────────────────────────────────────────────

let _repeatTimer  = null;
let _repeatActive = false;

function startRepeat(type, direction) {
  stopRepeat();
  _repeatActive = true;
  _doRepeat(type, direction); // fire immediately
  const delay   = 300;
  const cadence = 150;
  _repeatTimer = setTimeout(() => {
    if (!_repeatActive) return;
    _repeatTimer = setInterval(() => {
      if (!_repeatActive) { stopRepeat(); return; }
      _doRepeat(type, direction);
    }, cadence);
  }, delay);
}

function stopRepeat() {
  _repeatActive = false;
  if (_repeatTimer !== null) { clearTimeout(_repeatTimer); clearInterval(_repeatTimer); _repeatTimer = null; }
}

function _doRepeat(type, direction) {
  if (type === 'volume') ampVolume(direction);
}

async function ampVolume(direction) {
  await fetch('/api/amplifier/volume', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({direction}),
  });
}

// ── Input navigation buttons ──────────────────────────────────────────────────

async function ampNextInput() {
  await fetch('/api/amplifier/next-input', { method: 'POST' });
  setTimeout(loadAmplifierState, 300);
}

async function ampPrevInput() {
  await fetch('/api/amplifier/prev-input', { method: 'POST' });
  setTimeout(loadAmplifierState, 300);
}

// ── Input sync panel ──────────────────────────────────────────────────────────

let _syncInputList = [];

function toggleInputSync() {
  const panel = document.getElementById('amp-sync-panel');
  const btn   = document.getElementById('btn-amp-sync');
  if (!panel) return;
  const visible = panel.style.display !== 'none';
  if (visible) {
    panel.style.display = 'none';
    if (btn) btn.classList.remove('active');
  } else {
    _renderSyncPanel();
    panel.style.display = '';
    if (btn) btn.classList.add('active');
  }
}

function _renderSyncPanel() {
  const panel = document.getElementById('amp-sync-panel');
  if (!panel || !_syncInputList.length) return;
  panel.innerHTML = '<span class="sync-label">Amp is on:</span>' +
    _syncInputList.map(inp =>
      `<button class="btn-sync-input" onclick="applyInputSync('${esc(inp.id)}')">${esc(inp.label)}</button>`
    ).join('');
}

async function applyInputSync(id) {
  await fetch('/api/amplifier/sync-input', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({id}),
  });
  // Hide panel and refresh state
  const panel = document.getElementById('amp-sync-panel');
  const btn   = document.getElementById('btn-amp-sync');
  if (panel) panel.style.display = 'none';
  if (btn) btn.classList.remove('active');
  setTimeout(loadAmplifierState, 150);
}

async function cdTransport(action) {
  await fetch('/api/cdplayer/transport', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({action}),
  });
}


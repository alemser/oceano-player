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
}

// ── Power toggle ──────────────────────────────────────────────────────────────

async function ampPower() {
  await fetch('/api/amplifier/power', { method: 'POST' });
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
}

async function ampPrevInput() {
  await fetch('/api/amplifier/prev-input', { method: 'POST' });
}

async function cdTransport(action) {
  await fetch('/api/cdplayer/transport', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({action}),
  });
}

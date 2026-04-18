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
    startPowerStatePolling();
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

  if (state.power_state) applyPowerState(state.power_state);
}

const _powerStateLabels = {
  on:          'On',
  warming_up:  'Warming up',
  standby:     'Standby',
  off:         'Off',
  unknown:     '?',
};

function applyPowerState(ps) {
  const badge = document.getElementById('amp-power-badge');
  if (badge) {
    badge.dataset.state = ps;
    badge.textContent   = _powerStateLabels[ps] ?? ps;
    badge.title         = `Power state: ${ps.replace('_', ' ')}`;
  }

  const btnOn  = document.getElementById('btn-amp-on');
  const btnOff = document.getElementById('btn-amp-off');
  if (btnOn)  btnOn.classList.toggle('pwr-active',  ps === 'on' || ps === 'warming_up');
  if (btnOff) btnOff.classList.toggle('pwr-active', ps === 'off' || ps === 'standby');
}

let _powerStatePoll = null;

function startPowerStatePolling() {
  if (_powerStatePoll) return;
  _pollPowerState();
  _powerStatePoll = setInterval(_pollPowerState, 30000);
}

async function _pollPowerState() {
  try {
    const r = await fetch('/api/amplifier/power-state');
    if (!r.ok) return;
    const data = await r.json();
    applyPowerState(data.power_state);
  } catch { /* offline — keep last state */ }
}

// ── Power ON / OFF ────────────────────────────────────────────────────────────

async function ampPowerOn() {
  applyPowerState('warming_up'); // optimistic UI
  await fetch('/api/amplifier/power-on', { method: 'POST' });
}

async function ampPowerOff() {
  applyPowerState('off'); // optimistic UI
  await fetch('/api/amplifier/power-off', { method: 'POST' });
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

async function ampResetUSBInput() {
  const btn = document.getElementById('btn-amp-reset-usb');
  if (btn) btn.disabled = true;

  try {
    const r = await fetch('/api/amplifier/reset-usb-input', { method: 'POST' });
    if (!r.ok) throw new Error('request failed');

    const res = await r.json();
    switch (res?.status) {
      case 'already_usb':
        toast('USB input is already active.');
        break;
      case 'found_usb':
        toast(`USB input found after ${res.attempts} input jump(s).`);
        break;
      case 'usb_not_found':
        toast(`USB input not found after ${res?.attempts ?? 13} input jump(s).`, true);
        break;
      default:
        toast('USB input reset finished.');
        break;
    }
  } catch {
    toast('Failed to reset input to USB.', true);
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function cdTransport(action) {
  await fetch('/api/cdplayer/transport', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({action}),
  });
}

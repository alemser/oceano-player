function updateAmpPanel() {
  const ampEnabled = document.getElementById('amp-enabled')?.checked;
  const panel = document.getElementById('amp-panel');
  if (panel) panel.style.display = ampEnabled ? '' : 'none';
  renderCurrentInputDeviceWidget();
}

function updateAmpIRSummary(irCodes) {
  renderIRTable('amp-ir-table', _buildAmplifierIRCommands(), 'amplifier', irCodes);
}

async function loadAmplifierState() {
  try {
    const r = await fetch('/api/amplifier/state');
    if (!r.ok) return;
    renderAmpWidget(await r.json());
    startPowerStatePolling();
  } catch { /* not configured or offline — widget stays hidden */ }

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

let _ampProcessingCount = 0;
const AMP_SELECTION_ACTIVE_WINDOW_MS = 1200;
let _ampLastNavPressAtMs = 0;

function _ampSelectionIsActive() {
  if (_ampLastNavPressAtMs <= 0) return false;
  return (Date.now() - _ampLastNavPressAtMs) <= AMP_SELECTION_ACTIVE_WINDOW_MS;
}

function setAmpProcessing(active) {
  if (active) {
    _ampProcessingCount += 1;
  } else {
    _ampProcessingCount = Math.max(0, _ampProcessingCount - 1);
  }

  const busy = _ampProcessingCount > 0;
  const indicator = document.getElementById('amp-processing-indicator');
  if (indicator) {
    indicator.dataset.active = busy ? 'true' : 'false';
    indicator.title = busy ? 'Processing...' : 'Idle';
  }

  const select = document.getElementById('amp-input-select');
  if (select) select.disabled = busy;
  const btnReset = document.getElementById('btn-amp-reset-usb');
  if (btnReset) btnReset.disabled = busy;
}

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
  const wasActive = _ampSelectionIsActive();
  const r = await fetch('/api/amplifier/next-input', { method: 'POST' });
  if (!r.ok) return;
  _ampLastNavPressAtMs = Date.now();
  // In cycle mode, first click after inactivity only selects/highlights current input.
  if (!wasActive) return;
  const total = _ampInputsModel.length;
  if (total > 0) {
    _ampCurrentInputIdx = (_ampCurrentInputIdx + 1 + total) % total;
    renderAmpInputSelect();
    await persistKnownInputByFullIdx(_ampCurrentInputIdx);
  }
}

async function ampPrevInput() {
  const wasActive = _ampSelectionIsActive();
  const r = await fetch('/api/amplifier/prev-input', { method: 'POST' });
  if (!r.ok) return;
  _ampLastNavPressAtMs = Date.now();
  // In cycle mode, first click after inactivity only selects/highlights current input.
  if (!wasActive) return;
  const total = _ampInputsModel.length;
  if (total > 0) {
    _ampCurrentInputIdx = (_ampCurrentInputIdx - 1 + total) % total;
    renderAmpInputSelect();
    await persistKnownInputByFullIdx(_ampCurrentInputIdx);
  }
}

// Navigate to a visible input identified by its index in the full _ampInputsModel.
// Uses all inputs (including hidden) so backend can compute cycle distances correctly.
// In cycle mode, backend chooses the shortest path (next/prev) and still handles
// selector arming and timing windows.
async function ampSelectInputByFullIdx(targetFullIdx) {
  if (_ampProcessingCount > 0) return;
  const total = _ampInputsModel.length;
  if (total === 0 || targetFullIdx < 0) return;

  // Keep legacy forward-step payload for compatibility with older backends.
  const current = _ampCurrentInputIdx < 0 ? 0 : _ampCurrentInputIdx;
  const steps = (targetFullIdx - current + total) % total;
  const targetInput = _ampInputsModel[targetFullIdx];
  const currentInput = _ampCurrentInputIdx >= 0 ? _ampInputsModel[_ampCurrentInputIdx] : null;

  setAmpProcessing(true);
  try {
    const r = await fetch('/api/amplifier/select-input', {
      method: 'POST',
      headers: {'Content-Type':'application/json'},
      body: JSON.stringify({
        steps,
        target_input_id: targetInput?.id || '',
        current_input_id: currentInput?.id || '',
      }),
    });
    if (!r.ok) {
      let msg = 'Failed to change input.';
      try {
        const err = await r.json();
        if (err?.error) msg = err.error;
      } catch {
        // Keep fallback message when backend does not return JSON.
      }
      toast(msg, true);
      renderAmpInputSelect();
      return;
    }

    _ampCurrentInputIdx = targetFullIdx;
    renderAmpInputSelect();
    await persistKnownInputByFullIdx(_ampCurrentInputIdx);
  } finally {
    setAmpProcessing(false);
  }
}

async function ampResetUSBInput() {
  if (_ampProcessingCount > 0) return;
  setAmpProcessing(true);

  try {
    const r = await fetch('/api/amplifier/reset-usb-input', { method: 'POST' });
    if (!r.ok) throw new Error('request failed');

    const res = await r.json();
    switch (res?.status) {
      case 'already_usb':
        toast('USB input is already active.');
        _syncSelectToUSBInput();
        break;
      case 'found_usb':
        toast(`USB input found after ${res.attempts} input jump(s).`);
        _syncSelectToUSBInput();
        break;
      case 'usb_not_found':
        toast(`USB input not found after ${res?.attempts ?? 13} input jump(s).`, true);
        break;
      default:
        toast('USB input reset finished.');
        _syncSelectToUSBInput();
        break;
    }
  } catch {
    toast('Failed to reset input to USB.', true);
  } finally {
    setAmpProcessing(false);
  }
}

// After a successful USB reset, find the USB input in the model and reflect it in the select.
function _syncSelectToUSBInput() {
  const idx = _ampInputsModel.findIndex(
    (inp) => inp.logical_name.toLowerCase().includes('usb')
  );
  if (idx >= 0) {
    _ampCurrentInputIdx = idx;
    renderAmpInputSelect();
  }
}

async function deviceTransport(action) {
  const widget = document.getElementById('amp-device-widget');
  const deviceID = widget?.dataset?.deviceId || '';
  if (!deviceID) return;

  await fetch('/api/amplifier/device-action', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({ device_id: deviceID, action }),
  });
}


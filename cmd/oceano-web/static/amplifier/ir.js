// ── IR Learning ───────────────────────────────────────────────────────────────

const DEVICE_REMOTE_COMMANDS = [
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
          _refreshDirectIRWarning();
        } else if (device.startsWith('device-')) {
          const devID = device.slice('device-'.length);
          const devEntry = _ampConnectedDevices.find((d) => d.id === devID);
          if (devEntry) {
            if (!devEntry.ir_codes) devEntry.ir_codes = {};
            devEntry.ir_codes[command] = s.code;
          }
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
    if (msg) toast(msg, true);
  }
}

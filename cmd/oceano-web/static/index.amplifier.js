// ── Amplifier control ────────────────────────────────────────────────────────
let _ampConfig = {};
let _ampProfiles = [];
let _ampInputsModel = [];
let _ampConnectedDevices = [];
let _ampLastKnownInputID = '';

// Tracks which index in the full _ampInputsModel is currently active.
// -1 means unknown (e.g. just after page load before any navigation).
let _ampCurrentInputIdx = -1;

function _newInputID() {
  return String(Date.now()) + String(Math.floor(Math.random() * 1000));
}

function setAmplifierInputsModel(inputs) {
  const inArr = Array.isArray(inputs) ? inputs : [];
  _ampInputsModel = inArr.map((it) => ({
    id: String(it?.id ?? '').trim(),
    logical_name: String(it?.logical_name ?? '').trim(),
    visible: !!it?.visible,
  })).filter((it) => it.id !== '');

  if (_ampInputsModel.length === 0) {
    _ampInputsModel = [{ id: _newInputID(), logical_name: 'USB Audio', visible: true }];
  }
  const knownIdx = _ampInputsModel.findIndex((it) => it.id === String(_ampLastKnownInputID || ''));
  _ampCurrentInputIdx = knownIdx >= 0 ? knownIdx : -1;
  renderAmplifierInputsTable();
  renderConnectedDevicesTable();
  renderAmpInputSelect();
}

function refreshAmplifierInputViews() {
  let trackedInputID = String(_ampLastKnownInputID || '');
  if (_ampCurrentInputIdx >= 0 && _ampCurrentInputIdx < _ampInputsModel.length) {
    trackedInputID = String(_ampInputsModel[_ampCurrentInputIdx]?.id || trackedInputID);
  }
  const trackedIdx = _ampInputsModel.findIndex((it) => String(it.id) === trackedInputID);
  _ampCurrentInputIdx = trackedIdx >= 0 ? trackedIdx : -1;

  renderAmplifierInputsTable();
  renderAmpInputSelect();
}

async function persistKnownInputByFullIdx(fullIdx) {
  const input = _ampInputsModel[fullIdx];
  if (!input) return;
  _ampLastKnownInputID = String(input.id);
  await fetch('/api/amplifier/last-known-input', {
    method: 'POST',
    headers: {'Content-Type':'application/json'},
    body: JSON.stringify({ input_id: input.id }),
  });
}

function collectAmplifierInputsFromUI() {
  return _ampInputsModel.map((it) => ({
    id: it.id,
    logical_name: (it.logical_name || '').trim() || it.id,
    visible: !!it.visible,
  }));
}

function _currentInputRemoteDevice() {
  if (_ampCurrentInputIdx >= 0) {
    const input = _ampInputsModel[_ampCurrentInputIdx];
    if (input) {
      const inputID = String(input.id);
      const byInput = _ampConnectedDevices.find((d) => d.has_remote && (d.input_ids || []).includes(inputID));
      if (byInput) return byInput;
    }
  }
  // On first page load the current input may be unknown; show the first
  // remote-enabled device so transport controls are still available.
  return _ampConnectedDevices.find((d) => d.has_remote) || null;
}

function renderCurrentInputDeviceWidget() {
  const widget = document.getElementById('amp-device-widget');
  const title = document.getElementById('amp-device-widget-title');
  if (!widget || !title) return;

  const dev = _currentInputRemoteDevice();
  if (!dev) {
    widget.style.display = 'none';
    widget.dataset.deviceId = '';
    return;
  }

  widget.style.display = '';
  widget.dataset.deviceId = dev.id;
  title.textContent = dev.name || 'Connected Device';
}

// Populates the #amp-input-select dropdown with visible inputs.
// Uses full-model indices as option values so navigation can count all inputs.
// Input label replacement only uses connected devices with IR enabled.
function renderAmpInputSelect() {
  const sel = document.getElementById('amp-input-select');
  if (!sel) return;

  // Build a map: input ID -> device name, from all connected devices.
  // A non-remote device (e.g. turntable) should still rename the input label.
  const inputToDevice = new Map();
  _ampConnectedDevices.forEach((dev) => {
    if (!dev.name) return;
    (dev.input_ids || []).forEach((id) => inputToDevice.set(String(id), dev.name));
  });

  sel.innerHTML = '';
  _ampInputsModel.forEach((input, fullIdx) => {
    if (!input.visible) return;
    const opt = document.createElement('option');
    opt.value = fullIdx;
    const devName = inputToDevice.get(String(input.id));
    if (devName) {
      // Determine if this device covers multiple inputs (to append input name)
      const dev = _ampConnectedDevices.find((d) => (d.input_ids || []).includes(String(input.id)));
      const multiInput = !!dev && (dev.input_ids || []).length > 1;
      opt.textContent = multiInput
        ? `${devName} — ${input.logical_name || `Input ${fullIdx + 1}`}`
        : devName;
    } else {
      opt.textContent = input.logical_name || `Input ${fullIdx + 1}`;
    }
    if (fullIdx === _ampCurrentInputIdx) opt.selected = true;
    sel.appendChild(opt);
  });
  // If no option matches the current index, show a blank placeholder
  if (_ampCurrentInputIdx < 0 || !sel.querySelector(`option[value="${_ampCurrentInputIdx}"]`)) {
    const placeholder = document.createElement('option');
    placeholder.value = -1;
    placeholder.textContent = '—';
    placeholder.disabled = true;
    placeholder.selected = true;
    sel.insertBefore(placeholder, sel.firstChild);
  }

  renderCurrentInputDeviceWidget();
}

// ── Connected Devices ────────────────────────────────────────────────────────

function setConnectedDevicesModel(devices) {
  const arr = Array.isArray(devices) ? devices : [];
  _ampConnectedDevices = arr.map((d) => ({
    id:         String(d?.id        ?? '').trim(),
    name:       String(d?.name      ?? '').trim(),
    input_ids:  (d?.input_ids ?? []).map(String),
    has_remote: !!d?.has_remote,
    ir_codes:   d?.ir_codes ?? {},
  })).filter((d) => d.id !== '');
  renderConnectedDevicesTable();
  renderAmpInputSelect();
}

function collectConnectedDevicesFromUI() {
  return _ampConnectedDevices.map((d) => ({ ...d }));
}

function addConnectedDevice() {
  _ampConnectedDevices.push({ id: _newInputID(), name: '', input_ids: [], has_remote: false, ir_codes: {} });
  renderConnectedDevicesTable();
}

function removeConnectedDevice(idx) {
  _ampConnectedDevices.splice(idx, 1);
  renderConnectedDevicesTable();
  renderAmpInputSelect();
}

function renderConnectedDevicesTable() {
  const container = document.getElementById('amp-devices-list');
  if (!container) return;

  container.innerHTML = '';

  if (_ampConnectedDevices.length === 0) {
    container.innerHTML = '<p class="hint" style="margin:0">No devices configured. Click "Add Device" to register a connected device.</p>';
    return;
  }

  _ampConnectedDevices.forEach((dev, idx) => {
    const row = document.createElement('div');
    row.className = 'amp-device-row';

    // ── Top bar: name + remote toggle + remove ──────────────────────────────
    const topBar = document.createElement('div');
    topBar.className = 'amp-device-topbar';

    const nameInput = document.createElement('input');
    nameInput.type = 'text';
    nameInput.className = 'amp-device-name';
    nameInput.value = dev.name;
    nameInput.placeholder = 'Device name (e.g. Yamaha CD-S300)';
    nameInput.oninput = () => {
      _ampConnectedDevices[idx].name = nameInput.value;
      renderAmpInputSelect();
    };

    const remoteLabel = document.createElement('label');
    remoteLabel.className = 'amp-device-remote-toggle';
    remoteLabel.title = 'This device has a remote control — enable to configure IR codes';
    const remoteCb = document.createElement('input');
    remoteCb.type = 'checkbox';
    remoteCb.checked = dev.has_remote;
    remoteCb.style.cssText = 'width:auto;accent-color:var(--accent);';
    const remoteSpan = document.createElement('span');
    remoteSpan.textContent = 'Has remote';
    remoteLabel.appendChild(remoteCb);
    remoteLabel.appendChild(remoteSpan);
    remoteCb.onchange = () => {
      _ampConnectedDevices[idx].has_remote = remoteCb.checked;
      renderConnectedDevicesTable();
    };

    const removeBtn = document.createElement('button');
    removeBtn.type = 'button';
    removeBtn.className = 'btn-input-remove';
    removeBtn.title = 'Remove device';
    removeBtn.textContent = '✕';
    removeBtn.onclick = () => removeConnectedDevice(idx);

    topBar.appendChild(nameInput);
    topBar.appendChild(remoteLabel);
    topBar.appendChild(removeBtn);

    // ── Inputs checkboxes ──────────────────────────────────────────────────
    const inputsWrap = document.createElement('div');
    inputsWrap.className = 'amp-device-inputs';
    const inputsLabel = document.createElement('span');
    inputsLabel.className = 'amp-device-inputs-label';
    inputsLabel.textContent = 'Inputs:';
    inputsWrap.appendChild(inputsLabel);

    _ampInputsModel.forEach((inp) => {
      const cbLabel = document.createElement('label');
      cbLabel.className = 'amp-device-input-cb';
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.checked = dev.input_ids.includes(String(inp.id));
      cb.onchange = () => {
        const s = new Set(_ampConnectedDevices[idx].input_ids);
        cb.checked ? s.add(String(inp.id)) : s.delete(String(inp.id));
        _ampConnectedDevices[idx].input_ids = Array.from(s);
        renderAmpInputSelect();
      };
      const cbSpan = document.createElement('span');
      cbSpan.textContent = inp.logical_name || inp.id;
      cbLabel.appendChild(cb);
      cbLabel.appendChild(cbSpan);
      inputsWrap.appendChild(cbLabel);
    });

    if (_ampInputsModel.length === 0) {
      const hint = document.createElement('span');
      hint.className = 'hint';
      hint.textContent = 'No inputs configured yet.';
      inputsWrap.appendChild(hint);
    }

    row.appendChild(topBar);
    row.appendChild(inputsWrap);

    // ── IR codes (only when has_remote) ────────────────────────────────────
    if (dev.has_remote) {
      const irSection = document.createElement('div');
      irSection.className = 'amp-device-ir-section';
      const irLabel = document.createElement('div');
      irLabel.className = 'amp-device-inputs-label';
      irLabel.style.cssText = 'margin-bottom:6px;';
      irLabel.textContent = 'IR Codes';
      irSection.appendChild(irLabel);
      const irTable = document.createElement('div');
      irTable.id = `device-ir-table-${dev.id}`;
      irSection.appendChild(irTable);
      row.appendChild(irSection);
    }

    container.appendChild(row);

    if (dev.has_remote) {
      renderIRTable(`device-ir-table-${dev.id}`, DEVICE_REMOTE_COMMANDS, `device-${dev.id}`, dev.ir_codes ?? {});
    }
  });
}

function _ampInputCommandID(inputID) {
  return `input_${String(inputID).trim()}`;
}

function _currentAmpMode() {
  return document.getElementById('amp-input-mode')?.value || _ampConfig.input_mode || 'cycle';
}

function _buildAmplifierIRCommands() {
  const common = [
    { id: 'power_on', label: 'Power On' },
    { id: 'power_off', label: 'Power Off' },
    { id: 'volume_up', label: 'Volume +' },
    { id: 'volume_down', label: 'Volume −' },
  ];

  if (_currentAmpMode() === 'cycle') {
    return common.concat([
      { id: 'next_input', label: 'Input ▲ (next)' },
      { id: 'prev_input', label: 'Input ▼ (prev)' },
    ]);
  }

  const perInput = _ampInputsModel.map((input, idx) => ({
    id: _ampInputCommandID(input.id),
    label: `Select Input: ${(input.logical_name || `Input ${idx + 1}`).trim()}`,
  }));
  return common.concat(perInput);
}

function _refreshDirectIRWarning() {
  const warning = document.getElementById('amp-direct-warning');
  if (!warning) return;

  if (_currentAmpMode() !== 'direct') {
    warning.style.display = 'none';
    warning.textContent = '';
    return;
  }

  const irCodes = _ampConfig.ir_codes || {};
  const missing = _ampInputsModel
    .filter((input) => !irCodes[_ampInputCommandID(input.id)])
    .map((input, idx) => (input.logical_name || `Input ${idx + 1}`).trim());

  if (missing.length === 0) {
    warning.style.display = 'none';
    warning.textContent = '';
    return;
  }

  warning.style.display = '';
  warning.textContent = `Direct mode is active. Missing IR mapping for ${missing.length} input(s): ${missing.join(', ')}.`;
}

function _refreshAmplifierIRUI() {
  updateAmpIRSummary(_ampConfig.ir_codes || {});
  _refreshDirectIRWarning();
}

function renderAmplifierInputsTable() {
  const el = document.getElementById('amp-inputs-table');
  if (!el) return;
  el.innerHTML = '';

  _ampInputsModel.forEach((input, idx) => {
    const row = document.createElement('div');
    row.className = 'field';
    row.style.display = 'grid';
    row.style.gridTemplateColumns = 'minmax(180px,1fr) auto auto auto auto';
    row.style.gap = '8px';
    row.style.alignItems = 'center';

    const name = document.createElement('input');
    name.type = 'text';
    name.value = input.logical_name || '';
    name.placeholder = `Input ${idx + 1}`;
    name.oninput = () => {
      _ampInputsModel[idx].logical_name = name.value;
      renderAmpInputSelect();
    };

    const visibleWrap = document.createElement('label');
    visibleWrap.style.display = 'inline-flex';
    visibleWrap.style.alignItems = 'center';
    visibleWrap.style.gap = '6px';
    visibleWrap.style.whiteSpace = 'nowrap';
    const visible = document.createElement('input');
    visible.type = 'checkbox';
    visible.checked = !!input.visible;
    visible.onchange = () => {
      _ampInputsModel[idx].visible = visible.checked;
      renderAmpInputSelect();
    };
    const vText = document.createElement('span');
    vText.textContent = 'Visible';
    visibleWrap.appendChild(visible);
    visibleWrap.appendChild(vText);

    const defaultTag = document.createElement('span');
    defaultTag.className = 'hint';
    defaultTag.style.whiteSpace = 'nowrap';
    defaultTag.textContent = idx === 0 ? 'Default' : '';

    const up = document.createElement('button');
    up.type = 'button';
    up.className = 'detect-btn';
    up.textContent = 'Move Up';
    up.disabled = idx === 0;
    up.onclick = () => {
      if (idx === 0) return;
      const tmp = _ampInputsModel[idx - 1];
      _ampInputsModel[idx - 1] = _ampInputsModel[idx];
      _ampInputsModel[idx] = tmp;
      refreshAmplifierInputViews();
    };

    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'detect-btn';
    del.textContent = 'Remove';
    del.onclick = () => {
      if (_ampInputsModel.length <= 1) {
        toast('At least one input is required.', true);
        return;
      }
      _ampInputsModel.splice(idx, 1);
      refreshAmplifierInputViews();
    };

    row.appendChild(name);
    row.appendChild(visibleWrap);
    row.appendChild(defaultTag);
    row.appendChild(up);
    row.appendChild(del);
    el.appendChild(row);
  });

  _refreshAmplifierIRUI();
}

function addAmplifierInputRow() {
  _ampInputsModel.push({ id: _newInputID(), logical_name: '', visible: true });
  refreshAmplifierInputViews();
}

function onAmpInputModeChanged() {
  _refreshAmplifierIRUI();
}

function _refreshCloneBuiltInButton() {
  const btn = document.getElementById('amp-clone-profile-btn');
  const select = document.getElementById('amp-profile-select');
  if (!btn || !select) return;
  const selected = _ampProfiles.find((p) => p?.id === select.value);
  const isBuiltin = selected?.origin === 'builtin';
  btn.disabled = !isBuiltin;
  btn.title = isBuiltin ? 'Clone selected built-in profile as editable custom profile' : 'Select a built-in profile to clone';
}

async function loadAmplifierProfiles(cfg) {
  const select = document.getElementById('amp-profile-select');
  if (!select) return;

  try {
    const r = await fetch('/api/amplifier/profiles');
    if (!r.ok) return;
    const data = await r.json();
    _ampProfiles = Array.isArray(data?.profiles) ? data.profiles : [];
    const activeId = data?.active_profile_id || cfg?.amplifier?.profile_id || _ampConfig.profile_id || '';

    select.innerHTML = '';
    if (_ampProfiles.length === 0) {
      const opt = document.createElement('option');
      opt.value = '';
      opt.textContent = 'No profiles available';
      select.appendChild(opt);
      _refreshCloneBuiltInButton();
      return;
    }

    _ampProfiles.forEach(p => {
      const id = p?.id || '';
      const name = p?.name || id;
      const origin = p?.origin || 'custom';
      const opt = document.createElement('option');
      opt.value = id;
      opt.textContent = `${name} (${origin})`;
      if (id === activeId) opt.selected = true;
      select.appendChild(opt);
    });

    _refreshCloneBuiltInButton();
  } catch {
    // Keep current UI state if profile list cannot be fetched.
  }
}

function _slugifyProfileID(text) {
  return String(text || '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '') || 'custom_profile';
}

function _nextClonedProfileID(baseID) {
  const ids = new Set((_ampProfiles || []).map((p) => String(p?.id || '').toLowerCase()));
  let candidate = `${_slugifyProfileID(baseID)}_custom`;
  if (!ids.has(candidate.toLowerCase())) return candidate;
  let n = 2;
  while (ids.has(`${candidate}_${n}`.toLowerCase())) n += 1;
  return `${candidate}_${n}`;
}

async function cloneSelectedBuiltInProfile() {
  const select = document.getElementById('amp-profile-select');
  const profileID = select?.value || '';
  if (!profileID) {
    toast('Select a built-in profile first.', true);
    return;
  }

  const selected = _ampProfiles.find((p) => p?.id === profileID);
  if (!selected || selected.origin !== 'builtin') {
    toast('Clone Built-in works only for built-in profiles.', true);
    return;
  }

  const cloneID = _nextClonedProfileID(selected.id || selected.name || 'profile');
  const cloneName = `${selected.name || selected.id} Custom`;
  const baseConfig = selected.config || {};

  const payload = {
    id: cloneID,
    name: cloneName,
    origin: 'custom',
    config: {
      ...baseConfig,
      profile_id: cloneID,
      inputs: Array.isArray(baseConfig.inputs) ? baseConfig.inputs : [],
      ir_codes: { ...(baseConfig.ir_codes || {}) },
    },
  };

  try {
    const r = await fetch('/api/amplifier/profiles', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) {
      toast(data?.error || 'Failed to clone profile.', true);
      return;
    }

    toast(`Cloned built-in profile as ${cloneName}.`);
    await loadAmplifierProfiles();
    const sel = document.getElementById('amp-profile-select');
    if (sel) sel.value = cloneID;
    _refreshCloneBuiltInButton();
  } catch {
    toast('Failed to clone profile.', true);
  }
}

async function saveCurrentAsCustomProfile() {
  const id = prompt('Custom profile ID (example: my_amp_profile)');
  if (!id) return;
  const profileID = id.trim();
  if (!profileID) return;
  const name = prompt('Profile name', profileID) || profileID;

  const inputMode = _currentAmpMode();
  const inputs = collectAmplifierInputsFromUI();
  const irCodes = { ...(_ampConfig.ir_codes || {}) };

  if (inputMode === 'direct') {
    const missing = [];
    inputs.forEach((input, idx) => {
      const key = _ampInputCommandID(input.id);
      if (!String(irCodes[key] || '').trim()) {
        missing.push((input.logical_name || `Input ${idx + 1}`).trim());
      }
    });
    if (missing.length > 0) {
      toast(`Direct mode requires IR code per input. Missing: ${missing.join(', ')}.`, true);
      return;
    }
  }

  const payload = {
    id: profileID,
    name,
    origin: 'custom',
    config: {
      ..._ampConfig,
      profile_id: profileID,
      input_mode: inputMode,
      maker: document.getElementById('amp-maker')?.value || _ampConfig.maker || '',
      model: document.getElementById('amp-model')?.value || _ampConfig.model || '',
      inputs,
      ir_codes: irCodes,
      broadlink: { ...(_ampConfig.broadlink || {}), host: document.getElementById('amp-broadlink-host')?.value || '' },
    },
  };

  try {
    const r = await fetch('/api/amplifier/profiles', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) {
      toast(data?.error || 'Failed to save custom profile.', true);
      return;
    }
    toast('Custom profile saved.');
    await loadAmplifierProfiles();
    const select = document.getElementById('amp-profile-select');
    if (select) select.value = profileID;
    _refreshCloneBuiltInButton();
  } catch {
    toast('Failed to save custom profile.', true);
  }
}

async function deleteSelectedCustomProfile() {
  const select = document.getElementById('amp-profile-select');
  const profileID = select?.value || '';
  if (!profileID) {
    toast('Select a profile first.', true);
    return;
  }
  const selected = _ampProfiles.find(p => p?.id === profileID);
  if (selected?.origin === 'builtin') {
    toast('Built-in profiles cannot be deleted.', true);
    return;
  }
  if (!confirm(`Delete profile ${profileID}?`)) return;

  try {
    const r = await fetch(`/api/amplifier/profiles?profile_id=${encodeURIComponent(profileID)}`, {
      method: 'DELETE',
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) {
      toast(data?.error || 'Failed to delete profile.', true);
      return;
    }
    toast('Profile deleted.');
    await loadAmplifierProfiles();
  } catch {
    toast('Failed to delete profile.', true);
  }
}

async function activateSelectedAmplifierProfile() {
  const select = document.getElementById('amp-profile-select');
  const profileID = select?.value || '';
  if (!profileID) {
    toast('Select a profile first.', true);
    return;
  }

  try {
    const r = await fetch('/api/amplifier/profiles/activate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ profile_id: profileID }),
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) {
      toast(data?.error || 'Failed to activate profile.', true);
      return;
    }
    toast('Profile activated.');
    if (typeof loadAmplifierPage === 'function') {
      await loadAmplifierPage();
    } else if (typeof loadConfig === 'function') {
      await loadConfig();
    }
  } catch {
    toast('Failed to activate profile.', true);
  }
}

async function exportSelectedAmplifierProfile() {
  const select = document.getElementById('amp-profile-select');
  const profileID = select?.value || '';
  if (!profileID) {
    toast('Select a profile first.', true);
    return;
  }

  try {
    const r = await fetch(`/api/amplifier/profiles/export?profile_id=${encodeURIComponent(profileID)}&mode=safe`);
    const doc = await r.json().catch(() => ({}));
    if (!r.ok) {
      toast(doc?.error || 'Failed to export profile.', true);
      return;
    }

    const blob = new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `amplifier-profile-${profileID}.json`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
    toast('Profile exported.');
  } catch {
    toast('Failed to export profile.', true);
  }
}

async function importAmplifierProfileFile(input) {
  const file = input?.files?.[0];
  if (!file) return;

  try {
    const text = await file.text();
    let body;
    try {
      body = JSON.parse(text);
    } catch {
      toast('Invalid JSON file.', true);
      return;
    }

    const r = await fetch('/api/amplifier/profiles/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const data = await r.json().catch(() => ({}));
    if (!r.ok) {
      toast(data?.error || 'Failed to import profile.', true);
      return;
    }

    toast('Profile imported.');
    await loadAmplifierProfiles();
    if (data?.profile_id) {
      const select = document.getElementById('amp-profile-select');
      if (select) select.value = data.profile_id;
      _refreshCloneBuiltInButton();
    }
  } catch {
    toast('Failed to import profile.', true);
  } finally {
    if (input) input.value = '';
  }
}

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
  const r = await fetch('/api/amplifier/next-input', { method: 'POST' });
  if (!r.ok) return;
  const total = _ampInputsModel.length;
  if (total > 0) {
    _ampCurrentInputIdx = (_ampCurrentInputIdx + 1 + total) % total;
    renderAmpInputSelect();
    await persistKnownInputByFullIdx(_ampCurrentInputIdx);
  }
}

async function ampPrevInput() {
  const r = await fetch('/api/amplifier/prev-input', { method: 'POST' });
  if (!r.ok) return;
  const total = _ampInputsModel.length;
  if (total > 0) {
    _ampCurrentInputIdx = (_ampCurrentInputIdx - 1 + total) % total;
    renderAmpInputSelect();
    await persistKnownInputByFullIdx(_ampCurrentInputIdx);
  }
}

// Navigate to a visible input identified by its index in the full _ampInputsModel.
// Always navigates forward (ascending index order), wrapping around if needed.
// Uses all inputs (including hidden) to count the IR presses correctly.
// In cycle mode, the backend handles the first IR press as selector activation,
// then performs one additional press per requested forward step.
async function ampSelectInputByFullIdx(targetFullIdx) {
  if (_ampProcessingCount > 0) return;
  const total = _ampInputsModel.length;
  if (total === 0 || targetFullIdx < 0) return;

  // If current position is unknown, reset tracking to 0 before computing distance
  const current = _ampCurrentInputIdx < 0 ? 0 : _ampCurrentInputIdx;
  const steps = (targetFullIdx - current + total) % total;

  setAmpProcessing(true);
  try {
    const r = await fetch('/api/amplifier/select-input', {
      method: 'POST',
      headers: {'Content-Type':'application/json'},
      body: JSON.stringify({ steps }),
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


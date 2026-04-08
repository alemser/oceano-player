// ── Amplifier / CD player control ────────────────────────────────────────────
let _ampConfig = {};
let _cdConfig  = {};

// ── Inputs editor ─────────────────────────────────────────────────────────────

function renderAmpInputsList(inputs) {
  const list = document.getElementById('amp-inputs-list');
  if (!list) return;
  list.innerHTML = '';
  inputs.forEach((inp, i) => list.appendChild(buildInputRow(inp, i)));
}

function buildInputRow(inp, idx) {
  const row = document.createElement('div');
  row.className = 'amp-input-row';
  row.draggable = true;
  row.dataset.idx = idx;

  row.innerHTML = `
    <span class="amp-input-drag" title="Drag to reorder">⠿</span>
    <input class="amp-input-label" type="text" placeholder="Label (e.g. USB Audio)"
           value="${esc(inp.label ?? '')}" oninput="refreshDefaultInputDropdown()">
    <span class="amp-input-sep"></span>
    <input class="amp-input-id" type="text" placeholder="ID (e.g. USB)"
           value="${esc(inp.id ?? '')}" oninput="refreshDefaultInputDropdown()">
    <span class="amp-input-sep"></span>
    <button type="button" class="btn-input-visible ${inp.visible ? 'is-visible' : ''}"
            title="${inp.visible ? 'Visible in UI' : 'Hidden in UI'}"
            onclick="toggleInputVisible(this)">
      ${inp.visible ? '👁' : '○'}
    </button>
    <button type="button" class="btn-input-remove" title="Remove" onclick="removeInputRow(this)">✕</button>`;

  // Drag-and-drop reordering
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
  renderAmpInputsList(inputs);
  refreshDefaultInputDropdown();
  // Focus the label field of the new row
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
  btn.title   = isNowVisible ? 'Visible in UI' : 'Hidden in UI';
  btn.textContent = isNowVisible ? '👁' : '○';
}

// collectAmpInputs reads current row state from the DOM.
function collectAmpInputs() {
  return Array.from(document.querySelectorAll('.amp-input-row')).map(row => ({
    label:   row.querySelector('.amp-input-label')?.value ?? '',
    id:      row.querySelector('.amp-input-id')?.value ?? '',
    visible: row.querySelector('.btn-input-visible')?.classList.contains('is-visible') ?? false,
  }));
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
  const el = document.getElementById('amp-ir-summary');
  if (!el) return;
  const configured = Object.entries(irCodes).filter(([,v]) => v).map(([k]) => k);
  el.textContent = configured.length
    ? `${configured.length} configured: ${configured.join(', ')}`
    : 'No codes configured.';
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

  // Hardware-detected power label ⏻ Model (always visible in collapsed header)
  const pwrLabel = document.getElementById('amp-power-label');
  const pwrText  = document.getElementById('amp-power-label-text');
  if (pwrLabel && pwrText) {
    const ps    = state.detected_power_state ?? 'unknown';
    const model = state.model || state.maker || 'Amp';
    pwrLabel.className = `amp-power-label${ps === 'on' ? ' ps-on' : ps === 'off' ? ' ps-off' : ''}`;
    pwrText.textContent = model;
    pwrLabel.title = `Detected: ${ps === 'on' ? 'On' : ps === 'off' ? 'Off' : '—'}`;
  }

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

  const sel = document.getElementById('amp-input-select');
  if (sel && Array.isArray(state.input_list)) {
    const curId = state.current_input?.id ?? '';
    sel.innerHTML = state.input_list.map(inp =>
      `<option value="${esc(inp.id)}"${inp.id === curId ? ' selected' : ''}>${esc(inp.label)}</option>`
    ).join('');
  }
}

async function ampPower(action) {
  await fetch('/api/amplifier/power', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({action}),
  });
  setTimeout(loadAmplifierState, 300);
}

async function ampVolume(direction) {
  await fetch('/api/amplifier/volume', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({direction}),
  });
}

async function ampSetInput(id) {
  if (!id) return;
  await fetch('/api/amplifier/input', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({id}),
  });
  setTimeout(loadAmplifierState, 300);
}

async function cdTransport(action) {
  await fetch('/api/cdplayer/transport', {
    method: 'POST', headers: {'Content-Type':'application/json'},
    body: JSON.stringify({action}),
  });
}


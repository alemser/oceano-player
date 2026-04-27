'use strict';

// ── Mic Gain Wizard ────────────────────────────────────────────────────────────
// Steps: Select Device → Play Music → Adjust Gain → Save

const MIC_STEPS = 4;

let _mic = {
  step: 0,
  info: null,
  vuTimer: null,
  devices: [],
  selectedCard: null,
  autoTuneRunning: false,
  autoTuneAbort: false,
};

function openMicGainWizard() {
  _mic.step = 0;
  _mic.info = null;
  _mic.devices = [];
  _mic.selectedCard = null;
  _mic.autoTuneRunning = false;
  _mic.autoTuneAbort = false;
  _stopMicVU();
  document.getElementById('mic-gain-overlay').classList.add('open');
  _micRenderStep();
}

function closeMicGainWizard() {
  _mic.autoTuneAbort = true;
  _stopMicVU();
  document.getElementById('mic-gain-overlay').classList.remove('open');
}

function _stopMicVU() {
  if (_mic.vuTimer) { clearInterval(_mic.vuTimer); _mic.vuTimer = null; }
}

function _micStepIndicator() {
  const ind = document.getElementById('mic-gain-step-indicator');
  if (!ind) return;
  const labels = ['Device', 'Play Music', 'Adjust Gain', 'Save'];
  let html = '';
  for (let i = 0; i < MIC_STEPS; i++) {
    const dotCls = i < _mic.step ? 'cal-wiz-dot done' : i === _mic.step ? 'cal-wiz-dot active' : 'cal-wiz-dot';
    const icon = i < _mic.step
      ? `<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>`
      : `${i + 1}`;
    html += `<div class="${dotCls}">${icon}</div>`;
    if (i < MIC_STEPS - 1) {
      html += `<div class="cal-wiz-line${i < _mic.step ? ' done' : ''}"></div>`;
    }
  }
  ind.innerHTML = html;
}

function _micRenderStep() {
  _micStepIndicator();
  const body   = document.getElementById('mic-gain-body');
  const footer = document.getElementById('mic-gain-footer');
  if (!body || !footer) return;
  switch (_mic.step) {
    case 0: _micStep0(body, footer); break;
    case 1: _micStep1(body, footer); break;
    case 2: _micStep2(body, footer); break;
    case 3: _micStep3(body, footer); break;
  }
}

function _micStep0(body, footer) {
  body.innerHTML = `
    <div class="cal-wiz-illus">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
        <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/>
        <path d="M19 10v2a7 7 0 0 1-14 0v-2"/>
        <line x1="12" y1="19" x2="12" y2="23"/>
        <line x1="8" y1="23" x2="16" y2="23"/>
      </svg>
    </div>
    <div class="cal-wiz-title">Select Capture Device</div>
    <div class="cal-wiz-desc">Choose the USB sound card connected to the amplifier's REC OUT. This wizard will help you set the right input gain for track recognition.</div>
    <div id="mic-dev-list" style="margin-top:4px"><div class="cal-wiz-desc" style="opacity:0.5">Loading devices…</div></div>`;
  footer.innerHTML = `
    <button class="btn-secondary" onclick="closeMicGainWizard()">Cancel</button>
    <button class="btn-secondary" style="background:var(--accent-dim);border-color:var(--accent);color:var(--accent)" id="mic-next-0" onclick="_micStep0Next()" disabled>Next →</button>`;

  Promise.all([
    fetch('/api/devices').then(r => r.json()),
    fetch('/api/mic-gain/info').then(r => r.json()).catch(() => null),
  ]).then(([devs, cfgInfo]) => {
    _mic.devices = devs || [];
    // /api/mic-gain/info may set "error" when amixer fails (no mixer, permissions)
    // but still return card_num + device from config. Do not treat that as "no config"
    // and fall back to the first /api/devices card (HDMI=0 on Pi).
    let configuredCard = null;
    if (cfgInfo) {
      if (cfgInfo.device) {
        configuredCard = cfgInfo.card_num;
      } else if (!cfgInfo.error) {
        configuredCard = cfgInfo.card_num;
      }
    }
    _mic.selectedCard = (configuredCard != null && configuredCard !== undefined)
      ? configuredCard
      : (devs.length ? devs[0].card : null);

    if (!devs.length) {
      document.getElementById('mic-dev-list').innerHTML =
        `<div class="cal-wiz-warn-box">No ALSA sound cards detected. Connect the USB capture card and try again.</div>`;
      return;
    }

    const devCardNums = devs.map(d => d.card);
    if (configuredCard != null && !devCardNums.includes(configuredCard)) {
      const firstUSB = devs.find(d => /usb|uac|generalplus|audio device/i.test((d.desc || '') + (d.name || '')));
      if (firstUSB) {
        _mic.selectedCard = firstUSB.card;
      } else {
        _mic.selectedCard = devs[0].card;
      }
    }

    const configStaleWarning =
      (cfgInfo && cfgInfo.device && !devCardNums.includes(cfgInfo.card_num))
        ? `<div class="cal-wiz-warn-box" style="margin-bottom:10px">The saved <code>audio_input.device</code> points to card <b>${_esc(String(cfgInfo.card_num))}</b>, which is not in the system list (USB re-plug or reboot can renumber cards). Re-save the capture device in the config editor or run <code>sudo oceano-setup</code> — the selection below is a best guess.</div>`
        : (cfgInfo && cfgInfo.error && _mic.selectedCard != null && (cfgInfo.device || ''))
          ? `<div class="cal-wiz-warn-box" style="margin-bottom:10px;opacity:0.95">${_esc(cfgInfo.error)} (gain controls may be unavailable; check USB card and <code>amixer -c N</code> on the Pi.)</div>`
          : '';

    const inDevList = (c) => devCardNums.includes(c);
    const rows = configStaleWarning + devs.map(d => {
      const isConf = cfgInfo && inDevList(cfgInfo.card_num) && d.card === cfgInfo.card_num;
      const checked = d.card === _mic.selectedCard ? 'checked' : '';
      return `<label class="cal-wiz-cb-row" style="cursor:pointer" onclick="_micSelectCard(${d.card})">
        <input type="radio" name="mic-card" value="${d.card}" ${checked} style="width:15px;height:15px;accent-color:var(--accent);flex-shrink:0;margin-top:2px;cursor:pointer">
        <div class="cal-wiz-cb-text">
          <div class="cb-label">card ${d.card} — ${_esc(d.desc || d.name)}
            ${isConf ? `<span class="cal-sc-badge measured" style="margin-left:6px;font-size:0.58rem">configured</span>` : ''}
          </div>
          <div class="cb-hint">${_esc(d.name)}</div>
        </div>
      </label>`;
    }).join('');

    document.getElementById('mic-dev-list').innerHTML = rows;
    const btn = document.getElementById('mic-next-0');
    if (btn) btn.disabled = false;
  }).catch(e => {
    document.getElementById('mic-dev-list').innerHTML =
      `<div class="cal-wiz-warn-box">Error loading devices: ${_esc(e.message)}</div>`;
  });
}

function _micSelectCard(card) {
  _mic.selectedCard = card;
  document.querySelectorAll('input[name="mic-card"]').forEach(r => {
    r.checked = parseInt(r.value) === card;
  });
  const btn = document.getElementById('mic-next-0');
  if (btn) btn.disabled = false;
}

function _micStep0Next() {
  if (_mic.selectedCard === null) return;
  fetch(`/api/mic-gain/info?card=${_mic.selectedCard}`)
    .then(r => r.json())
    .then(info => {
      _mic.info = info;
      _mic.step = 1;
      _micRenderStep();
    })
    .catch(e => toast('Error: ' + e.message));
}

function _micStep1(body, footer) {
  const devName = _mic.info ? (_mic.info.device_name || _mic.info.device) : '—';
  body.innerHTML = `
    <div class="cal-wiz-illus">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="12" cy="12" r="10"/>
        <circle cx="12" cy="12" r="3"/>
        <line x1="12" y1="2" x2="12" y2="5"/>
        <line x1="12" y1="19" x2="12" y2="22"/>
      </svg>
    </div>
    <div class="cal-wiz-title">Play Music</div>
    <div class="cal-wiz-desc">Put on a record or play a CD at a typical listening volume, with the amplifier set to that physical input.</div>
    <div class="cal-wiz-instr">
      <b>Selected device:</b> card ${_mic.selectedCard ?? '?'} — ${_esc(devName)}<br>
      <b>Gain control:</b> ${_esc(_mic.info?.control || '—')}<br>
      <b>Current gain:</b> ${_mic.info?.gain_pct ?? '—'}%
    </div>
    <div class="cal-wiz-desc" style="margin-top:12px;margin-bottom:0">
      When music is playing at normal volume, click <b>Next</b> to start monitoring the signal level.
    </div>`;
  footer.innerHTML = `
    <button class="btn-secondary" onclick="_micPrev()">← Back</button>
    <button class="btn-secondary" style="background:var(--accent-dim);border-color:var(--accent);color:var(--accent)" onclick="_micNext()">Next →</button>`;
}

function _micStep2(body, footer) {
  _stopMicVU();
  const gain    = _mic.info?.gain_pct ?? '—';
  const control = _mic.info?.control  ?? '—';

  let peakRMS      = 0;
  let peakTimer    = null;
  let sessionMin   = Infinity;
  let sessionMax   = 0;
  let sessionSum   = 0;
  let sessionCount = 0;

  const silenceThreshold = _rfloat('rec-vu-silence-threshold', 0);

  body.innerHTML = `
    <div class="cal-wiz-title">Adjust Gain</div>
    <div class="cal-wiz-desc">Aim for peaks in the <b>green zone (0.05–0.25)</b>. Let a loud passage play to check the peak doesn't clip.</div>

    <div style="margin:16px 0 4px">
      <div style="position:relative;width:100%;height:12px;background:rgba(255,255,255,0.06);border-radius:6px;overflow:visible">
        <div id="mic-rms-bar" style="position:absolute;left:0;top:0;height:100%;width:0%;background:var(--muted);border-radius:6px;transition:width 0.18s,background 0.18s"></div>
        <div style="position:absolute;top:0;left:12.5%;width:50%;height:100%;background:rgba(126,207,126,0.08);pointer-events:none"></div>
        <div id="mic-threshold-marker" style="position:absolute;top:-4px;width:2px;height:20px;background:rgba(74,158,255,0.85);border-radius:1px;left:0%;display:none" title="Current silence threshold"></div>
        <div id="mic-peak-marker" style="position:absolute;top:-3px;width:3px;height:18px;background:rgba(240,192,96,0.9);border-radius:2px;left:0%;transition:left 0.1s;display:none"></div>
      </div>
      <div style="display:flex;justify-content:space-between;margin-top:3px">
        <span style="font-size:0.62rem;color:var(--muted)">0</span>
        <span style="font-size:0.62rem;color:var(--ok-text)">▲ 0.05</span>
        <span style="font-size:0.62rem;color:var(--ok-text)">0.25 ▲</span>
        <span style="font-size:0.62rem;color:var(--muted)">0.40+</span>
      </div>
    </div>
    <div id="mic-threshold-info" style="font-size:0.7rem;color:rgba(74,158,255,0.85);margin-bottom:4px;display:none">
      <span style="display:inline-block;width:10px;height:2px;background:rgba(74,158,255,0.85);vertical-align:middle;margin-right:4px;border-radius:1px"></span>
      Silence threshold: <span id="mic-threshold-val">—</span> — signal must stay above this for recognition to trigger
    </div>

    <div style="display:flex;gap:6px;margin:10px 0 14px">
      <div class="cal-wiz-sum-val" style="flex:1;text-align:center"><span class="lbl">Avg</span><span class="val" id="mic-rms-avg">—</span></div>
      <div class="cal-wiz-sum-val" style="flex:1;text-align:center"><span class="lbl">Peak</span><span class="val" id="mic-rms-peak" style="color:rgba(240,192,96,0.9)">—</span></div>
      <div class="cal-wiz-sum-val" style="flex:1;text-align:center"><span class="lbl">Min seen</span><span class="val" id="mic-rms-min">—</span></div>
      <div class="cal-wiz-sum-val" style="flex:1;text-align:center"><span class="lbl">Max seen</span><span class="val" id="mic-rms-max">—</span></div>
    </div>

    <div id="mic-rms-status" class="cal-wiz-rec-box" style="margin-bottom:16px">Waiting for signal…</div>

    <div style="display:flex;align-items:center;gap:12px;justify-content:center;flex-wrap:wrap">
      <button type="button" class="cal-wiz-cap-btn mic-gain-step-btn" onclick="_micAdjust('down')" title="Decrease gain one step" style="min-width:52px;padding:10px 14px">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="5" y1="12" x2="19" y2="12"/></svg>
      </button>
      <div style="text-align:center;min-width:72px">
        <div style="font-size:0.65rem;color:var(--muted);text-transform:uppercase;letter-spacing:0.06em;margin-bottom:2px">Gain</div>
        <div id="mic-gain-val" style="font-size:1.4rem;font-weight:700;font-family:monospace">${gain}%</div>
        <div id="mic-gain-raw" style="font-size:0.68rem;color:var(--muted);margin-top:1px;font-family:monospace">${_mic.info?.gain_raw != null ? `${_mic.info.gain_raw}/${_mic.info.gain_max}` : ''}</div>
        <div style="font-size:0.62rem;color:var(--muted);margin-top:1px">${_esc(control)}</div>
      </div>
      <button type="button" class="cal-wiz-cap-btn mic-gain-step-btn" onclick="_micAdjust('up')" title="Increase gain one step" style="min-width:52px;padding:10px 14px">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
      </button>
    </div>
    <div style="display:flex;flex-direction:column;align-items:center;gap:6px;margin-top:14px">
      <button type="button" class="btn-secondary" id="mic-auto-tune-btn" onclick="_micStartAutoTune()" style="font-size:0.82rem">Auto-adjust gain</button>
      <span class="hint" style="text-align:center;max-width:420px;margin:0">Uses the current music level to move gain toward the green zone (≈0.07–0.24 RMS avg, peaks under ~0.34). You can always override with <b>−</b> / <b>+</b>.</span>
    </div>`;

  footer.innerHTML = `
    <button class="btn-secondary" onclick="_micPrev()">← Back</button>
    <button class="btn-secondary" style="background:var(--accent-dim);border-color:var(--accent);color:var(--accent)" onclick="_micNext()">Next →</button>`;

  if (silenceThreshold > 0) {
    const tPct = Math.min(silenceThreshold / 0.40 * 100, 100);
    const tMarker = document.getElementById('mic-threshold-marker');
    const tInfo   = document.getElementById('mic-threshold-info');
    const tVal    = document.getElementById('mic-threshold-val');
    if (tMarker) { tMarker.style.left = tPct + '%'; tMarker.style.display = 'block'; }
    if (tInfo)   { tInfo.style.display = 'block'; }
    if (tVal)    { tVal.textContent = silenceThreshold.toFixed(4); }
  }

  _mic.vuTimer = setInterval(() => {
    fetch('/api/calibration/vu-sample', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({seconds: 1}),
    })
      .then(r => r.json())
      .then(d => {
        const avg  = d.avg_rms ?? 0;
        const sMax = d.max_rms ?? avg;

        const barEl     = document.getElementById('mic-rms-bar');
        const peakEl    = document.getElementById('mic-peak-marker');
        const avgEl     = document.getElementById('mic-rms-avg');
        const peakValEl = document.getElementById('mic-rms-peak');
        const minEl     = document.getElementById('mic-rms-min');
        const maxEl     = document.getElementById('mic-rms-max');
        const statusEl  = document.getElementById('mic-rms-status');
        if (!barEl) { _stopMicVU(); return; }

        sessionSum += avg;
        sessionCount++;
        if (avg > 0.01) sessionMin = Math.min(sessionMin, avg);
        sessionMax = Math.max(sessionMax, sMax);

        if (sMax > peakRMS) {
          peakRMS = sMax;
          clearTimeout(peakTimer);
          peakTimer = setTimeout(() => {
            peakRMS = 0;
            if (peakEl) { peakEl.style.display = 'none'; }
            if (peakValEl) peakValEl.textContent = '—';
          }, 4000);
        }

        const avgPct  = Math.min(avg     / 0.40 * 100, 100);
        const peakPct = Math.min(peakRMS / 0.40 * 100, 100);

        barEl.style.width = avgPct + '%';

        if (peakRMS > 0.005 && peakEl) {
          peakEl.style.display = 'block';
          peakEl.style.left = peakPct + '%';
          peakEl.style.background = peakRMS > 0.38 ? '#e05577' : peakRMS > 0.25 ? 'rgba(240,192,96,0.9)' : 'rgba(126,207,126,0.85)';
        }

        if (avg < 0.01)       barEl.style.background = 'var(--muted)';
        else if (avg < 0.05)  barEl.style.background = 'var(--warn-text,#f0c060)';
        else if (avg <= 0.25) barEl.style.background = 'var(--ok-text,#7ecf7e)';
        else if (avg <= 0.35) barEl.style.background = 'var(--warn-text,#f0c060)';
        else                  barEl.style.background = '#e05577';

        if (avgEl)     avgEl.textContent     = avg.toFixed(4);
        if (peakValEl) peakValEl.textContent = peakRMS > 0 ? peakRMS.toFixed(4) : '—';
        if (minEl)     minEl.textContent     = sessionMin < Infinity ? sessionMin.toFixed(4) : '—';
        if (maxEl)     maxEl.textContent     = sessionMax > 0 ? sessionMax.toFixed(4) : '—';

        if (avg < 0.01) {
          statusEl.className = 'cal-wiz-rec-box';
          if (silenceThreshold > 0 && avg > silenceThreshold) {
            statusEl.className = 'cal-wiz-warn-box';
            statusEl.innerHTML = `Noise floor (${avg.toFixed(4)}) is above the calibrated silence threshold (${silenceThreshold.toFixed(4)}) — the system will always detect "Physical" even with no music playing. Reduce gain or re-run noise floor calibration.`;
          } else {
            statusEl.innerHTML = 'No signal detected — make sure music is playing.';
          }
        } else if (avg < 0.05) {
          statusEl.className = 'cal-wiz-warn-box';
          statusEl.innerHTML = 'Signal too low — increase gain with <b>+</b>.';
        } else if (avg <= 0.25) {
          const okSvg = `<span class="r-icon"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="20 6 9 17 4 12"/></svg></span>`;
          statusEl.className = 'cal-wiz-result-ok';
          if (peakRMS > 0.38) {
            statusEl.className = 'cal-wiz-warn-box';
            statusEl.innerHTML = `Average is good (${avg.toFixed(4)}) but peaks are near clipping (${peakRMS.toFixed(4)}) — consider reducing gain slightly with <b>−</b>.`;
          } else if (peakRMS > 0.25) {
            statusEl.innerHTML = `${okSvg}<span class="r-text">Good level. Occasional peaks at ${peakRMS.toFixed(4)} are normal for dynamic content — recognition will work well.</span>`;
          } else {
            statusEl.innerHTML = `${okSvg}<span class="r-text">Good level. Peak ${peakRMS > 0 ? peakRMS.toFixed(4) : avg.toFixed(4)} — recognition will capture a clean signal.</span>`;
          }
        } else if (avg <= 0.35) {
          statusEl.className = 'cal-wiz-warn-box';
          statusEl.innerHTML = `Average high (${avg.toFixed(4)}) — consider reducing gain with <b>−</b>.`;
        } else {
          statusEl.className = 'cal-wiz-warn-box';
          statusEl.innerHTML = 'Clipping — reduce gain with <b>−</b> to avoid distortion in recognition.';
        }

        if (sessionCount >= 5 && sessionMin < Infinity && sessionMax > 0) {
          const ratio = sessionMax / Math.max(sessionMin, 0.001);
          if (ratio > 8 && statusEl.className === 'cal-wiz-result-ok') {
            statusEl.innerHTML += `<br><span style="font-size:0.72rem;opacity:0.75">Wide dynamic range detected (${ratio.toFixed(0)}×). Gain is optimised for loud passages — quiet passages may not trigger recognition.</span>`;
          }
        }
      })
      .catch(() => {});
  }, 1200);
}

function _micApplyGainResponse(d) {
  if (!d || d.error) return;
  const el = document.getElementById('mic-gain-val');
  if (el) el.textContent = d.gain_pct + '%';
  const rawEl = document.getElementById('mic-gain-raw');
  if (rawEl && d.gain_raw != null) rawEl.textContent = `${d.gain_raw}/${d.gain_max}`;
  if (_mic.info) {
    _mic.info.gain_pct = d.gain_pct;
    _mic.info.gain_raw = d.gain_raw;
    _mic.info.gain_max = d.gain_max;
  }
}

function _micAdjust(dir) {
  fetch('/api/mic-gain/adjust', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      direction: dir,
      step: 1,
      raw_step: true,
      card_num: _mic.selectedCard,
    }),
  })
    .then(r => r.json())
    .then(d => {
      if (d.error) { toast('Error: ' + d.error); return; }
      _micApplyGainResponse(d);
    })
    .catch(e => toast('Error: ' + e.message));
}

function _micAdjustAsync(dir) {
  return fetch('/api/mic-gain/adjust', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      direction: dir,
      step: 1,
      raw_step: true,
      card_num: _mic.selectedCard,
    }),
  })
    .then(r => r.json())
    .then(d => {
      if (d.error) throw new Error(d.error);
      _micApplyGainResponse(d);
      return d;
    });
}

async function _micVuSampleOnce() {
  const r = await fetch('/api/calibration/vu-sample', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({ seconds: 1 }),
  });
  if (!r.ok) throw new Error('VU sample failed');
  return r.json();
}

function _micSetGainStepButtonsDisabled(disabled) {
  document.querySelectorAll('.mic-gain-step-btn').forEach(b => {
    b.disabled = disabled;
  });
}

async function _micStartAutoTune() {
  if (_mic.autoTuneRunning) return;
  const btnAuto = document.getElementById('mic-auto-tune-btn');
  const statusEl = document.getElementById('mic-rms-status');

  _mic.autoTuneRunning = true;
  _mic.autoTuneAbort = false;
  if (btnAuto) {
    btnAuto.disabled = true;
    btnAuto.textContent = 'Adjusting…';
  }
  _micSetGainStepButtonsDisabled(true);

  const sleep = (ms) => new Promise(res => setTimeout(res, ms));
  let noSignalTries = 0;
  let success = false;

  try {
    for (let i = 0; i < 52 && !_mic.autoTuneAbort; i++) {
      let sm;
      try {
        sm = await _micVuSampleOnce();
      } catch {
        await sleep(600);
        continue;
      }
      const avg = sm.avg_rms ?? 0;
      const peak = sm.max_rms ?? avg;

      if (avg < 0.012) {
        noSignalTries++;
        if (statusEl) {
          statusEl.className = 'cal-wiz-warn-box';
          statusEl.innerHTML = 'Play music at normal volume — waiting for signal…';
        }
        if (noSignalTries >= 18) {
          toast('No steady signal — start playback, then try Auto-adjust again.', true);
          break;
        }
        await sleep(700);
        continue;
      }
      noSignalTries = 0;

      const okAvg = avg >= 0.07 && avg <= 0.24;
      const okPeak = peak <= 0.34;
      if (okAvg && okPeak) {
        success = true;
        toast('Auto-adjust: gain is in the recommended range. Fine-tune with − / + if you like.');
        break;
      }

      const rawBefore = _mic.info?.gain_raw;
      let dir = null;
      if (peak > 0.38 || avg > 0.32) {
        dir = 'down';
      } else if (avg < 0.07) {
        dir = 'up';
      } else if (avg > 0.24) {
        dir = 'down';
      } else if (peak > 0.34) {
        dir = 'down';
      } else {
        break;
      }

      await _micAdjustAsync(dir);
      await sleep(800);

      const rawAfter = _mic.info?.gain_raw;
      if (rawBefore != null && rawAfter === rawBefore) {
        toast('Mixer limit reached — use − / + manually if the level is still off.', true);
        break;
      }
    }
  } catch (e) {
    toast(e.message || String(e), true);
  } finally {
    _mic.autoTuneRunning = false;
    if (btnAuto) {
      btnAuto.disabled = false;
      btnAuto.textContent = 'Auto-adjust gain';
    }
    _micSetGainStepButtonsDisabled(false);
  }
}

function _micStep3(body, footer) {
  _stopMicVU();
  const gain    = _mic.info?.gain_pct ?? '—';
  const control = _mic.info?.control  ?? '—';
  const devName = _mic.info ? (_mic.info.device_name || _mic.info.device) : '—';

  body.innerHTML = `
    <div class="cal-wiz-illus">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
        <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/>
        <polyline points="22 4 12 14.01 9 11.01"/>
      </svg>
    </div>
    <div class="cal-wiz-title">Save Settings</div>
    <div class="cal-wiz-desc">Review the settings below and click <b>Save &amp; Close</b> to persist them across reboots.</div>
    <div class="cal-wiz-result-ok" style="margin-bottom:14px">
      <span class="r-icon">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="20 6 9 17 4 12"/></svg>
      </span>
      <span class="r-text">
        card ${_mic.selectedCard ?? '?'} — ${_esc(devName)}<br>
        control: ${_esc(control)} &nbsp;·&nbsp; gain: <b>${gain}%</b>
      </span>
    </div>
    <div class="cal-wiz-hint-box">
      The system will automatically learn the new noise floor from the first few minutes of silence after saving.
    </div>`;

  footer.innerHTML = `
    <button class="btn-secondary" onclick="_micPrev()">← Back</button>
    <button class="btn-save" id="mic-save-btn" onclick="_micSave()" style="margin-left:auto">Save &amp; Close</button>`;
}

function _micSave() {
  const btn = document.getElementById('mic-save-btn');
  if (btn) btn.disabled = true;

  const card = _mic.info?.card_num != null ? `?card=${_mic.info.card_num}` : '';
  fetch(`/api/mic-gain/store${card}`, {method: 'POST'})
    .then(r => r.json())
    .then(d => {
      if (d.error) {
        toast('Error: ' + d.error);
        if (btn) btn.disabled = false;
        return;
      }
      if (_mic.info) { _calibrationState.gainInfo = _mic.info; }
      closeMicGainWizard();
      toast('Gain saved.');
    })
    .catch(e => {
      toast('Error: ' + e.message);
      if (btn) btn.disabled = false;
    });
}

function _micNext() {
  _mic.autoTuneAbort = true;
  _stopMicVU();
  if (_mic.step < MIC_STEPS - 1) { _mic.step++; _micRenderStep(); }
}

function _micPrev() {
  _mic.autoTuneAbort = true;
  _stopMicVU();
  if (_mic.step > 0) { _mic.step--; _micRenderStep(); }
}

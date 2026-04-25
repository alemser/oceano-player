'use strict';

// ── Live RMS Monitor ──────────────────────────────────────────────────────────

const RMS_MON_MAX  = 60;   // points × 2.5 s ≈ 2.5 min of history
const RMS_MON_TOP  = 0.40; // RMS value that maps to full bar / top of canvas

let _rmsM = {
  active:  false,
  timer:   null,
  history: [],
  sMin:    Infinity,
  sMax:    0,
};

function toggleRMSMonitor() {
  _rmsM.active ? _rmsMonStop() : _rmsMonStart();
}

function _rmsMonStart() {
  _rmsM.active  = true;
  _rmsM.history = [];
  _rmsM.sMin    = Infinity;
  _rmsM.sMax    = 0;

  document.getElementById('rms-mon-panel').style.display = 'block';
  document.getElementById('rms-mon-toggle').textContent  = '◼ Stop';

  const thresh = _rfloat('rec-vu-silence-threshold', 0);
  const tLine  = document.getElementById('rms-mon-thresh-line');
  if (tLine && thresh > 0) {
    tLine.style.left    = (thresh / RMS_MON_TOP * 100) + '%';
    tLine.style.display = 'block';
  }

  _rmsMonTick();
  _rmsM.timer = setInterval(_rmsMonTick, 2500);
}

function _rmsMonStop() {
  _rmsM.active = false;
  clearInterval(_rmsM.timer);
  _rmsM.timer  = null;
  document.getElementById('rms-mon-toggle').textContent = '▶ Start';
  _rmsMonSetStatus('Stopped.', 'var(--muted)');
}

function _rmsMonTick() {
  fetch('/api/calibration/vu-sample', {
    method:  'POST',
    headers: {'Content-Type': 'application/json'},
    body:    JSON.stringify({seconds: 2}),
  })
    .then(r => r.json())
    .then(d => {
      const avg  = d.avg_rms ?? 0;
      const minV = d.min_rms ?? avg;
      const maxV = d.max_rms ?? avg;

      _rmsM.history.push(avg);
      if (_rmsM.history.length > RMS_MON_MAX) _rmsM.history.shift();
      if (avg < _rmsM.sMin) _rmsM.sMin = avg;
      if (avg > _rmsM.sMax) _rmsM.sMax = avg;

      const barEl = document.getElementById('rms-mon-bar');
      if (barEl) {
        barEl.style.width      = Math.min(avg / RMS_MON_TOP * 100, 100) + '%';
        barEl.style.background = _rmsMonColor(avg);
      }

      _rmsMonSet('rms-mon-avg',  avg.toFixed(5));
      _rmsMonSet('rms-mon-min',  minV.toFixed(5));
      _rmsMonSet('rms-mon-max',  maxV.toFixed(5));
      _rmsMonSet('rms-mon-smin', _rmsM.sMin < Infinity ? _rmsM.sMin.toFixed(5) : '—');
      _rmsMonSet('rms-mon-smax', _rmsM.sMax > 0 ? _rmsM.sMax.toFixed(5) : '—');

      const secs = _rmsM.history.length * 2.5;
      _rmsMonSet('rms-mon-history-label', secs < 60 ? `${Math.round(secs)}s ago ←` : `${(secs / 60).toFixed(1)} min ago ←`);

      const thresh = _rfloat('rec-vu-silence-threshold', 0);
      if (avg < 0.005) {
        if (thresh > 0 && avg > thresh) {
          _rmsMonSetStatus(`Noise floor ${avg.toFixed(5)} above threshold ${thresh.toFixed(5)} — will always detect Physical`, '#e05577');
        } else {
          _rmsMonSetStatus('Silence — no signal or device not active', 'var(--muted)');
        }
      } else if (avg < 0.05) {
        _rmsMonSetStatus('Signal low — check amplifier input or increase capture gain', 'var(--warn-text, #f0c060)');
      } else if (avg <= 0.25) {
        _rmsMonSetStatus('Good level — recognition will work well', 'var(--ok-text, #7ecf7e)');
      } else if (avg <= 0.35) {
        _rmsMonSetStatus('Level high — consider reducing capture gain', 'var(--warn-text, #f0c060)');
      } else {
        _rmsMonSetStatus('Clipping — reduce capture gain to avoid recognition failure', '#e05577');
      }

      _rmsMonDraw();
    })
    .catch(() => _rmsMonSetStatus('Error: VU socket not available', '#e05577'));
}

function _rmsMonColor(v) {
  if (v < 0.005)  return 'rgba(255,255,255,0.15)';
  if (v < 0.05)   return 'var(--warn-text, #f0c060)';
  if (v <= 0.25)  return 'var(--ok-text, #7ecf7e)';
  if (v <= 0.35)  return 'var(--warn-text, #f0c060)';
  return '#e05577';
}

function _rmsMonSet(id, text) {
  const el = document.getElementById(id);
  if (el) el.textContent = text;
}

function _rmsMonSetStatus(msg, color) {
  const el = document.getElementById('rms-mon-status');
  if (!el) return;
  el.textContent  = msg;
  el.style.color  = color;
}

function _rmsMonDraw() {
  const canvas = document.getElementById('rms-mon-canvas');
  if (!canvas) return;
  const W = canvas.offsetWidth;
  const H = canvas.height;
  canvas.width = W;

  const ctx    = canvas.getContext('2d');
  const pts    = _rmsM.history;
  const thresh = _rfloat('rec-vu-silence-threshold', 0);

  ctx.clearRect(0, 0, W, H);
  if (pts.length < 2) return;

  const xStep = W / (RMS_MON_MAX - 1);
  const xOf   = i => (RMS_MON_MAX - pts.length + i) * xStep;
  const yOf   = v => H - (Math.min(v, RMS_MON_TOP) / RMS_MON_TOP) * H;

  const y25 = yOf(0.25);
  const y05 = yOf(0.05);
  const yTh = thresh > 0 ? yOf(thresh) : H;

  if (thresh > 0) {
    ctx.fillStyle = 'rgba(74,158,255,0.04)';
    ctx.fillRect(0, yTh, W, H - yTh);
  }
  ctx.fillStyle = 'rgba(126,207,126,0.05)';
  ctx.fillRect(0, y25, W, y05 - y25);

  ctx.setLineDash([3, 4]);
  ctx.lineWidth = 1;
  if (thresh > 0) {
    ctx.strokeStyle = 'rgba(74,158,255,0.3)';
    ctx.beginPath(); ctx.moveTo(0, yTh); ctx.lineTo(W, yTh); ctx.stroke();
  }
  ctx.strokeStyle = 'rgba(126,207,126,0.2)';
  ctx.beginPath(); ctx.moveTo(0, y25); ctx.lineTo(W, y25); ctx.stroke();
  ctx.setLineDash([]);

  ctx.beginPath();
  ctx.moveTo(xOf(0), H);
  for (let i = 0; i < pts.length; i++) ctx.lineTo(xOf(i), yOf(pts[i]));
  ctx.lineTo(xOf(pts.length - 1), H);
  ctx.closePath();
  const last = pts[pts.length - 1];
  ctx.fillStyle = last < 0.005  ? 'rgba(74,158,255,0.08)' :
                  last <= 0.25  ? 'rgba(126,207,126,0.14)' :
                  last <= 0.35  ? 'rgba(240,192,96,0.14)'  : 'rgba(224,85,119,0.14)';
  ctx.fill();

  ctx.beginPath();
  for (let i = 0; i < pts.length; i++) {
    i === 0 ? ctx.moveTo(xOf(i), yOf(pts[i])) : ctx.lineTo(xOf(i), yOf(pts[i]));
  }
  ctx.strokeStyle = last < 0.005  ? 'rgba(74,158,255,0.5)'   :
                    last <= 0.25  ? 'rgba(126,207,126,0.85)' :
                    last <= 0.35  ? 'rgba(240,192,96,0.85)'  : 'rgba(224,85,119,0.85)';
  ctx.lineWidth = 1.5;
  ctx.stroke();

  ctx.beginPath();
  ctx.arc(xOf(pts.length - 1), yOf(last), 3, 0, Math.PI * 2);
  ctx.fillStyle = ctx.strokeStyle;
  ctx.fill();
}

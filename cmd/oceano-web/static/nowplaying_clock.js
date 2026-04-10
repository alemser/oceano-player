// ─── DOM refs for clock ──────────────────────────────────────────────────────
const $idleTime = document.getElementById('idle-time');
const $idleDate = document.getElementById('idle-date');

const DAYS   = ['Sunday','Monday','Tuesday','Wednesday','Thursday','Friday','Saturday'];
const MONTHS = ['January','February','March','April','May','June','July','August','September','October','November','December'];

// ─── Idle clock logic ────────────────────────────────────────────────────────
function tickClock() {
  const now = new Date();
  const h = String(now.getHours()).padStart(2, '0');
  const m = String(now.getMinutes()).padStart(2, '0');
  if ($idleTime) $idleTime.textContent = h + ':' + m;
  if ($idleDate) $idleDate.textContent = DAYS[now.getDay()] + ', ' + now.getDate() + ' ' + MONTHS[now.getMonth()];
}

tickClock();
setInterval(tickClock, 10_000);
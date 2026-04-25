// ─── DOM refs for clock ──────────────────────────────────────────────────────
const $idleTime    = document.getElementById('idle-time');
const $idleSeconds = document.getElementById('idle-seconds');
const $idleDate    = document.getElementById('idle-date');

const DAYS   = ['Sunday','Monday','Tuesday','Wednesday','Thursday','Friday','Saturday'];
const MONTHS = ['January','February','March','April','May','June','July','August','September','October','November','December'];

// ─── Tick: called every second ───────────────────────────────────────────────
function tickClock() {
  const now = new Date();
  const h = now.getHours();
  const m = now.getMinutes();
  const s = now.getSeconds();

  if ($idleTime) {
    $idleTime.textContent =
      String(h).padStart(2, '0') + ':' + String(m).padStart(2, '0');
  }
  if ($idleSeconds) {
    $idleSeconds.textContent = String(s).padStart(2, '0');
  }
  if ($idleDate) {
    $idleDate.textContent =
      DAYS[now.getDay()] + ', ' + now.getDate() + ' ' + MONTHS[now.getMonth()];
  }

}

// ─── Precise scheduling: sync to wall-clock seconds ──────────────────────────
function scheduleNextTick() {
  const msUntilNextSecond = 1000 - (Date.now() % 1000);
  setTimeout(() => {
    tickClock();
    setInterval(tickClock, 1000);
  }, msUntilNextSecond);
}

tickClock();
scheduleNextTick();

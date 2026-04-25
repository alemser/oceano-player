// ─── DOM refs for clock ──────────────────────────────────────────────────────
const $idleTime    = document.getElementById('idle-time');
const $idleSeconds = document.getElementById('idle-seconds');
const $idleDate    = document.getElementById('idle-date');
const $idleHeroH   = document.getElementById('idle-hero-h');
const $idleHeroM   = document.getElementById('idle-hero-m');
const $idleHeroSec = document.getElementById('idle-hero-sec');
const $idleHeroDow = document.getElementById('idle-hero-dow');
const $idleHeroDmy = document.getElementById('idle-hero-dmy');

const DAYS   = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

// Reference-style date: "Tuesday, May 5"
function fmtDateLong(now) {
  return `${DAYS[now.getDay()]}, ${MONTHS[now.getMonth()]} ${now.getDate()}`;
}

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
    $idleDate.textContent = fmtDateLong(now).toUpperCase();
  }
  // Colourful hero: 24h — HH, :MM, :SS (same system as main clock; no AM/PM)
  if ($idleHeroH)  $idleHeroH.textContent = String(h).padStart(2, '0');
  if ($idleHeroM)  $idleHeroM.textContent = ':' + String(m).padStart(2, '0');
  if ($idleHeroSec) $idleHeroSec.textContent = ':' + String(s).padStart(2, '0');
  if ($idleHeroDow)  $idleHeroDow.textContent  = DAYS[now.getDay()].toUpperCase();
  if ($idleHeroDmy)  $idleHeroDmy.textContent  = fmtDateLong(now);
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

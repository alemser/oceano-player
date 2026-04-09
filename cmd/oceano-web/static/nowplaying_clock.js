// ─── DOM ref for clock ───────────────────────────────────────────────────────
const $idleTime = document.getElementById('idle-time');

// ─── Idle clock logic ────────────────────────────────────────────────────────
function tickClock() {
  const now = new Date();
  
  // Get current hours and minutes, padding with a leading zero if needed (e.g., "09:05")
  const h = String(now.getHours()).padStart(2, '0');
  const m = String(now.getMinutes()).padStart(2, '0');
  
  // Update the HTML text content
  if ($idleTime) {
    $idleTime.textContent = h + ':' + m;
  }
}

// Run immediately to set the initial time on load
tickClock();

// Update the clock every 10 seconds (10,000 milliseconds)
setInterval(tickClock, 10_000);
// ── Init ─────────────────────────────────────────────────────────────────────
loadConfig();
loadStatus();
loadLibrary();
loadAmplifierState();
startLibraryAutoRefresh();
setInterval(loadAmplifierState, 5000);

if (new URLSearchParams(location.search).get('drawer') === '1') {
  openConfig();
  history.replaceState({}, document.title, location.pathname);
}


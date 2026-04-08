// ── Init ─────────────────────────────────────────────────────────────────────
loadConfig();
loadStatus();
loadLibrary();
loadAmplifierState();
startLibraryAutoRefresh();
setInterval(loadAmplifierState, 5000);


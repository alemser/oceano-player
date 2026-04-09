// ─── DOM refs for weather ──────────────────────────────────────────────────
const $idleWeather = document.getElementById('idle-weather');
const $idleWeatherIcon = document.getElementById('idle-weather-icon');
const $idleWeatherTemp = document.getElementById('idle-weather-temp');
const $idleWeatherCond = document.getElementById('idle-weather-cond');

// ─── Idle weather (live, fixed location for now) ───────────────────────────

const WEATHER_DEFAULT = {
  latitude: 53.3498,
  longitude: -6.2603,
  locationLabel: 'Dublin',
  enabled: true,
  refreshMS: 10 * 60 * 1000,
};
let WEATHER_CONFIG = { ...WEATHER_DEFAULT };
let _weatherTimer = null;

const WEATHER_CODE_MAP = {
  0: 'Clear',
  1: 'Mostly clear',
  2: 'Partly cloudy',
  3: 'Overcast',
  45: 'Fog',
  48: 'Rime fog',
  51: 'Light drizzle',
  53: 'Drizzle',
  55: 'Heavy drizzle',
  56: 'Freezing drizzle',
  57: 'Heavy freezing drizzle',
  61: 'Light rain',
  63: 'Rain',
  65: 'Heavy rain',
  66: 'Freezing rain',
  67: 'Heavy freezing rain',
  71: 'Light snow',
  73: 'Snow',
  75: 'Heavy snow',
  77: 'Snow grains',
  80: 'Light showers',
  81: 'Showers',
  82: 'Heavy showers',
  85: 'Snow showers',
  86: 'Heavy snow showers',
  95: 'Thunderstorm',
  96: 'Storm + hail',
  99: 'Severe storm + hail',
};

function weatherIconSVG(kind) {
  switch (kind) {
    case 'cloud':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 18h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 18Z"/></svg>`;
    case 'rain':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 14h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 14Z"/><path d="M9 17v3M13 17v3M17 17v3"/></svg>`;
    case 'storm':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 14h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 14Z"/><path d="m12 15-2 4h2l-1 3 4-6h-2l1-3"/></svg>`;
    case 'snow':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M6 14h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 14Z"/><path d="M9 18h0M13 19h0M17 18h0"/></svg>`;
    case 'fog':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M5 11h12"/><path d="M3 15h15"/><path d="M6 19h10"/></svg>`;
    default:
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="3.6"/><path d="M12 2.2V5"/><path d="M12 19V21.8"/><path d="M2.2 12H5"/><path d="M19 12h2.8"/><path d="M5 5L7 7"/><path d="M17 17L19 19"/><path d="M17 7L19 5"/><path d="M5 19L7 17"/></svg>`;
  }
}

function weatherIconKind(code) {
  const n = Number(code);
  if ([45, 48].includes(n)) return 'fog';
  if ([95, 96, 99].includes(n)) return 'storm';
  if ([71, 73, 75, 77, 85, 86].includes(n)) return 'snow';
  if ([51, 53, 55, 56, 57, 61, 63, 65, 66, 67, 80, 81, 82].includes(n)) return 'rain';
  if ([1, 2, 3].includes(n)) return 'cloud';
  return 'sun';
}

function weatherLabelFromCode(code) {
  return WEATHER_CODE_MAP[Number(code)] || 'Conditions unavailable';
}

function renderWeather(tempC, condition, code) {
  const rounded = Number.isFinite(tempC) ? Math.round(tempC) + '°C' : '--°C';
  if ($idleWeatherIcon) {
    $idleWeatherIcon.innerHTML = weatherIconSVG(weatherIconKind(code));
  }
  $idleWeatherTemp.textContent = rounded;
  $idleWeatherCond.textContent = WEATHER_CONFIG.locationLabel + ' · ' + condition;
}

function applyWeatherVisibility() {
  if (!$idleWeather) return;
  $idleWeather.style.display = WEATHER_CONFIG.enabled ? 'inline-flex' : 'none';
}

function normalizeRefreshMS(minutes) {
  const mins = Number(minutes);
  if (!Number.isFinite(mins)) return WEATHER_DEFAULT.refreshMS;
  return Math.max(2, Math.min(120, Math.round(mins))) * 60 * 1000;
}

async function loadWeatherConfig() {
  try {
    const resp = await fetch('/api/config', { cache: 'no-store' });
    if (!resp.ok) return;
    const cfg = await resp.json();
    const weather = cfg && cfg.weather ? cfg.weather : null;
    if (!weather) return;

    WEATHER_CONFIG = {
      latitude: Number.isFinite(Number(weather.latitude)) ? Number(weather.latitude) : WEATHER_DEFAULT.latitude,
      longitude: Number.isFinite(Number(weather.longitude)) ? Number(weather.longitude) : WEATHER_DEFAULT.longitude,
      locationLabel: String(weather.location_label || WEATHER_DEFAULT.locationLabel).trim() || WEATHER_DEFAULT.locationLabel,
      enabled: weather.enabled !== false,
      refreshMS: normalizeRefreshMS(weather.refresh_mins),
    };
  } catch (_err) {
    // Keep defaults when config endpoint is unavailable.
  }
}

async function refreshWeather() {
  if (!WEATHER_CONFIG.enabled) return;

  const query = new URLSearchParams({
    latitude: String(WEATHER_CONFIG.latitude),
    longitude: String(WEATHER_CONFIG.longitude),
    current: 'temperature_2m,weather_code',
    timezone: 'auto',
  });

  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 8000);

  try {
    const resp = await fetch('https://api.open-meteo.com/v1/forecast?' + query.toString(), {
      signal: controller.signal,
      cache: 'no-store',
    });
    if (!resp.ok) {
      throw new Error('weather request failed: ' + resp.status);
    }
    const data = await resp.json();
    const current = data && data.current ? data.current : null;
    const temp = current ? Number(current.temperature_2m) : NaN;
    const code = current ? current.weather_code : null;
    renderWeather(temp, weatherLabelFromCode(code), code);
  } catch (_err) {
    // Keep UI stable offline: retain last value or show graceful fallback.
    if (!$idleWeatherTemp.textContent) {
      renderWeather(NaN, 'Offline', null);
    }
  } finally {
    clearTimeout(timeout);
  }
}

async function startWeatherLoop() {
  await loadWeatherConfig();
  applyWeatherVisibility();

  if (!WEATHER_CONFIG.enabled) {
    return;
  }

  // Start with deterministic placeholder, then replace with live data.
  renderWeather(NaN, 'Loading…', null);
  await refreshWeather();

  if (_weatherTimer) {
    clearInterval(_weatherTimer);
  }
  _weatherTimer = setInterval(refreshWeather, WEATHER_CONFIG.refreshMS);
}

startWeatherLoop();
// ─── DOM refs ─────────────────────────────────────────────────────────────────
const $idleWeather      = document.getElementById('idle-weather-icon-wrap'); // used for visibility
const $idleWeatherIcon  = document.getElementById('idle-weather-icon');
const $idleTempGroup    = document.getElementById('idle-temp-group');
const $idleWeatherTemp  = document.getElementById('idle-weather-temp');
const $idleWeatherFeels = document.getElementById('idle-weather-feels');
const $idleWeatherCond  = document.getElementById('idle-weather-cond');
const $idleLocationLbl  = document.getElementById('idle-location-label');
const $idleSunrise      = document.getElementById('idle-sunrise');
const $idleSunset       = document.getElementById('idle-sunset');
const $idlePressure     = document.getElementById('idle-pressure');
const $idleForecast     = document.getElementById('idle-forecast');
const $idleMetaStats    = document.getElementById('idle-meta-stats');
const $idleLocationCond = document.getElementById('idle-location-cond');

// ─── Config ────────────────────────────────────────────────────────────────────
const WEATHER_DEFAULT = {
  latitude:      53.3498,
  longitude:     -6.2603,
  locationLabel: 'Dublin',
  enabled:       true,
  refreshMS:     10 * 60 * 1000,
};
let WEATHER_CONFIG = { ...WEATHER_DEFAULT };
let _weatherTimer  = null;

// ─── WMO condition map ─────────────────────────────────────────────────────────
const WEATHER_CODE_MAP = {
  0: 'Clear', 1: 'Mostly clear', 2: 'Partly cloudy', 3: 'Overcast',
  45: 'Fog', 48: 'Rime fog',
  51: 'Light drizzle', 53: 'Drizzle', 55: 'Heavy drizzle',
  56: 'Freezing drizzle', 57: 'Heavy freezing drizzle',
  61: 'Light rain', 63: 'Rain', 65: 'Heavy rain',
  66: 'Freezing rain', 67: 'Heavy freezing rain',
  71: 'Light snow', 73: 'Snow', 75: 'Heavy snow', 77: 'Snow grains',
  80: 'Light showers', 81: 'Showers', 82: 'Heavy showers',
  85: 'Snow showers', 86: 'Heavy snow showers',
  95: 'Thunderstorm', 96: 'Storm + hail', 99: 'Severe storm',
};

const FC_DAYS = ['SUN', 'MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT'];

// ─── SVG icons ─────────────────────────────────────────────────────────────────
function weatherIconSVG(kind) {
  switch (kind) {
    case 'cloud':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M6 18h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 18Z"/></svg>`;
    case 'rain':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M6 14h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 14Z"/><path d="M9 17v3M13 17v3M17 17v3"/></svg>`;
    case 'storm':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M6 14h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 14Z"/><path d="m12 15-2 4h2l-1 3 4-6h-2l1-3"/></svg>`;
    case 'snow':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M6 14h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 14Z"/><path d="M9 18h0M13 19h0M17 18h0"/></svg>`;
    case 'fog':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M5 11h12"/><path d="M3 15h15"/><path d="M6 19h10"/></svg>`;
    case 'moon':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z"/></svg>`;
    default: // sun
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="3.8"/><path d="M12 2.2V5M12 19V21.8M2.2 12H5M19 12h2.8M5 5L7 7M17 17L19 19M17 7L19 5M5 19L7 17"/></svg>`;
  }
}

function weatherIconKind(code, isDay = true) {
  const n = Number(code);
  if ([45, 48].includes(n)) return 'fog';
  if ([95, 96, 99].includes(n)) return 'storm';
  if ([71, 73, 75, 77, 85, 86].includes(n)) return 'snow';
  if ([51, 53, 55, 56, 57, 61, 63, 65, 66, 67, 80, 81, 82].includes(n)) return 'rain';
  if ([1, 2, 3].includes(n)) return 'cloud';
  return isDay ? 'sun' : 'moon';
}

function weatherLabelFromCode(code) {
  return WEATHER_CODE_MAP[Number(code)] || 'Unknown';
}

// ─── Helpers ───────────────────────────────────────────────────────────────────
function fmtTime(iso) {
  // "2024-04-11T06:15" → "06:15"
  if (!iso) return '—';
  const parts = String(iso).split('T');
  return parts.length > 1 ? parts[1].slice(0, 5) : '—';
}

function fmtTemp(t) {
  return Number.isFinite(Number(t)) ? Math.round(Number(t)) + '°' : '—°';
}

// ─── Render ────────────────────────────────────────────────────────────────────
function renderWeather(data) {
  const current = data && data.current ? data.current : null;
  const daily   = data && data.daily   ? data.daily   : null;

  // Current temp + condition + icon
  const temp        = current ? Number(current.temperature_2m) : NaN;
  const feelsLike   = current ? Number(current.apparent_temperature) : NaN;
  const code        = current ? current.weather_code : null;
  const isDay       = current ? current.is_day !== 0 : true;
  const pressure    = current ? Math.round(Number(current.surface_pressure)) : null;

  if ($idleWeatherIcon) {
    $idleWeatherIcon.innerHTML = weatherIconSVG(weatherIconKind(code, isDay));
  }
  if ($idleWeatherTemp) {
    $idleWeatherTemp.textContent = Number.isFinite(temp) ? Math.round(temp) + '°C' : '—°C';
  }
  if ($idleWeatherFeels) {
    $idleWeatherFeels.textContent = Number.isFinite(feelsLike) ? 'Feels like ' + Math.round(feelsLike) + '°' : '';
  }
  if ($idleWeatherCond) {
    $idleWeatherCond.textContent = weatherLabelFromCode(code);
  }
  if ($idleLocationLbl) {
    $idleLocationLbl.textContent = WEATHER_CONFIG.locationLabel;
  }

  // Stats: sunrise / sunset (take today = index 0)
  if ($idleSunrise && daily && daily.sunrise) {
    $idleSunrise.textContent = fmtTime(daily.sunrise[0]);
  }
  if ($idleSunset && daily && daily.sunset) {
    $idleSunset.textContent = fmtTime(daily.sunset[0]);
  }
  if ($idlePressure) {
    $idlePressure.textContent = pressure ? pressure + ' hPa' : '—';
  }

  // 7-day forecast strip
  if ($idleForecast && daily && daily.time) {
    const _d = new Date();
    const todayStr = `${_d.getFullYear()}-${String(_d.getMonth()+1).padStart(2,'0')}-${String(_d.getDate()).padStart(2,'0')}`;
    $idleForecast.innerHTML = daily.time.slice(0, 7).map((dateStr, i) => {
      const dayObj  = new Date(dateStr + 'T12:00:00');
      const dayName = FC_DAYS[dayObj.getDay()];
      const isToday = dateStr === todayStr;
      const maxT    = fmtTemp(daily.temperature_2m_max ? daily.temperature_2m_max[i] : null);
      const minT    = fmtTemp(daily.temperature_2m_min ? daily.temperature_2m_min[i] : null);
      const icon    = weatherIconSVG(weatherIconKind(daily.weather_code ? daily.weather_code[i] : null));
      const precip  = daily.precipitation_probability_max ? Number(daily.precipitation_probability_max[i]) : 0;
      const precipHTML = precip >= 20 ? `<span class="idle-fc-precip">${Math.round(precip)}%</span>` : '';

      return `<div class="idle-fc-day">
        <span class="idle-fc-name${isToday ? ' today' : ''}">${dayName}</span>
        <span class="idle-fc-icon">${icon}</span>
        <span class="idle-fc-max">${maxT}</span>
        <span class="idle-fc-min">${minT}</span>
        ${precipHTML}
      </div>`;
    }).join('');
  }
}

function renderWeatherOffline() {
  if ($idleWeatherCond && !$idleWeatherCond.textContent) {
    if ($idleWeatherIcon)  $idleWeatherIcon.innerHTML = weatherIconSVG('sun');
    if ($idleWeatherTemp)  $idleWeatherTemp.textContent = '—°C';
    if ($idleWeatherCond)  $idleWeatherCond.textContent = 'Offline';
    if ($idleLocationLbl)  $idleLocationLbl.textContent = WEATHER_CONFIG.locationLabel;
  }
}

// ─── Visibility ────────────────────────────────────────────────────────────────
function applyWeatherVisibility() {
  const show = WEATHER_CONFIG.enabled;
  [$idleWeather, $idleTempGroup, $idleMetaStats, $idleLocationCond, $idleForecast].forEach(el => {
    if (el) el.style.display = show ? '' : 'none';
  });
}

// ─── Config ────────────────────────────────────────────────────────────────────
function normalizeRefreshMS(minutes) {
  const mins = Number(minutes);
  if (!Number.isFinite(mins)) return WEATHER_DEFAULT.refreshMS;
  return Math.max(2, Math.min(120, Math.round(mins))) * 60 * 1000;
}

async function loadWeatherConfig() {
  try {
    const resp = await fetch('/api/config', { cache: 'no-store' });
    if (!resp.ok) return;
    const cfg     = await resp.json();
    const weather = cfg && cfg.weather ? cfg.weather : null;
    if (!weather) return;

    WEATHER_CONFIG = {
      latitude:      Number.isFinite(Number(weather.latitude))   ? Number(weather.latitude)   : WEATHER_DEFAULT.latitude,
      longitude:     Number.isFinite(Number(weather.longitude))  ? Number(weather.longitude)  : WEATHER_DEFAULT.longitude,
      locationLabel: String(weather.location_label || WEATHER_DEFAULT.locationLabel).trim() || WEATHER_DEFAULT.locationLabel,
      enabled:       weather.enabled !== false,
      refreshMS:     normalizeRefreshMS(weather.refresh_mins),
    };
  } catch (_) {
    // Keep defaults when config endpoint is unavailable.
  }
}

// ─── Fetch ─────────────────────────────────────────────────────────────────────
async function refreshWeather() {
  if (!WEATHER_CONFIG.enabled) return;

  const query = new URLSearchParams({
    latitude:     String(WEATHER_CONFIG.latitude),
    longitude:    String(WEATHER_CONFIG.longitude),
    current:      'temperature_2m,apparent_temperature,weather_code,surface_pressure,is_day',
    daily:        'weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max,sunrise,sunset',
    forecast_days: '7',
    timezone:     'auto',
    models:       'icon_seamless',
  });

  const controller = new AbortController();
  const timeout    = setTimeout(() => controller.abort(), 10000);

  try {
    const resp = await fetch('https://api.open-meteo.com/v1/forecast?' + query.toString(), {
      signal: controller.signal,
      cache: 'no-store',
    });
    if (!resp.ok) throw new Error('weather ' + resp.status);
    const data = await resp.json();
    renderWeather(data);
  } catch (_) {
    renderWeatherOffline();
  } finally {
    clearTimeout(timeout);
  }
}

// ─── Boot ──────────────────────────────────────────────────────────────────────
async function startWeatherLoop() {
  await loadWeatherConfig();
  applyWeatherVisibility();
  if (!WEATHER_CONFIG.enabled) return;

  // Placeholder until first fetch
  if ($idleWeatherTemp)  $idleWeatherTemp.textContent = '—°C';
  if ($idleWeatherCond)  $idleWeatherCond.textContent = '…';
  if ($idleLocationLbl)  $idleLocationLbl.textContent = WEATHER_CONFIG.locationLabel;

  await refreshWeather();

  if (_weatherTimer) clearInterval(_weatherTimer);
  _weatherTimer = setInterval(refreshWeather, WEATHER_CONFIG.refreshMS);
}

startWeatherLoop();

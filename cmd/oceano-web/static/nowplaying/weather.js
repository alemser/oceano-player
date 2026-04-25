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
const $idleLocationCond  = document.getElementById('idle-location-cond');
const $idleLocationHero  = document.getElementById('idle-location-hero');
const $idleCondHero      = document.getElementById('idle-weather-cond-hero');
const $idleFeelsHero     = document.getElementById('idle-weather-feels-hero');
const $idleTempHero      = document.getElementById('idle-weather-temp-hero');
const $idleMainHero      = document.getElementById('idle-main-hero');

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

// Reusable cloud path (WMO 3+ composite icons).
const PATH_CLOUD = 'M6 18h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 18Z';

// ─── SVG icons ─────────────────────────────────────────────────────────────────
function weatherIconSVG(kind) {
  switch (kind) {
    case 'cloud':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="${PATH_CLOUD}"/></svg>`;
    // WMO 1: sun/moon with a small cloud (most sky clear).
    case 'mostly_clear_day':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true">` +
        `<g transform="translate(0,1)"><circle cx="7" cy="7" r="2.2"/>` +
        `<path d="M7 1.3v.9M7 13.3v.9M1.2 6.8H2M12.3 6.8h.9M2.1 2.1l.7.7M11.1 10.1l.7.7M11.1 2.2L10.4 3M2.4 10.1l.7.7"/>` +
        `</g><g transform="translate(8,9) scale(0.48)"><path d="${PATH_CLOUD}"/></g></svg>`;
    case 'mostly_clear_night':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true">` +
        `<g transform="translate(1,1) scale(0.42)"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z"/></g>` +
        `<g transform="translate(8,9) scale(0.48)"><path d="${PATH_CLOUD}"/></g></svg>`;
    // WMO 2: larger cloud, sun/moon more obscured.
    case 'partly_day':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true">` +
        `<g transform="translate(0,1)"><circle cx="6" cy="5.5" r="1.8"/>` +
        `<path d="M6 1.2v.7M6 9.8v.7M0.8 5.5H1.6M10.4 5.5h.7M1.1 1.1l.6.6M10.3 9.9l.6.6M10.1 1.2L9.5 1.8M1.5 9.3l.6.6"/></g>` +
        `<g transform="translate(0,4)"><path d="${PATH_CLOUD}"/></g></svg>`;
    case 'partly_night':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true">` +
        `<g transform="translate(0,0) scale(0.4)"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z"/></g>` +
        `<g transform="translate(0,4)"><path d="${PATH_CLOUD}"/></g></svg>`;
    case 'rain':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M6 14h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 14Z"/><path d="M9 17v3M13 17v3M17 17v3"/></svg>`;
    case 'storm':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M6 14h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 14Z"/><path d="m12 15-2 4h2l-1 3 4-6h-2l1-3"/></svg>`;
    case 'snow':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M6 14h10a4 4 0 0 0 0-8 5.5 5.5 0 0 0-10.3-1.6A3.5 3.5 0 0 0 6 14Z"/>` +
        `<path d="M9 20l.8-.6M8.2 20l.8.6M10 20l-1-1.2"/><path d="M12.5 19.5l.6-.4M12 20l.6.4M13.2 20l-1-1.2"/>` +
        `<path d="M16.5 20.2l.7-.5M15.6 20.2l.7.5M17.4 20.2l-1-1.1"/></svg>`;
    case 'fog':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M5 11h12"/><path d="M3 15h15"/><path d="M6 19h10"/></svg>`;
    case 'moon':
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z"/></svg>`;
    case 'sun':
    default:
      return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="3.8"/><path d="M12 2.2V5M12 19V21.8M2.2 12H5M19 12h2.8M5 5L7 7M17 17L19 19M17 7L19 5M5 19L7 17"/></svg>`;
  }
}

function weatherIconKind(code, isDay = true) {
  const n = Number(code);
  if (Number.isNaN(n)) return isDay ? 'sun' : 'moon';
  if (n === 0) return isDay ? 'sun' : 'moon';
  if (n === 1) return isDay ? 'mostly_clear_day' : 'mostly_clear_night';
  if (n === 2) return isDay ? 'partly_day' : 'partly_night';
  if (n === 3) return 'cloud';
  if ([45, 48].includes(n)) return 'fog';
  if ([95, 96, 99].includes(n)) return 'storm';
  if ([71, 73, 75, 77, 85, 86].includes(n)) return 'snow';
  if ([51, 53, 55, 56, 57, 61, 63, 65, 66, 67, 80, 81, 82].includes(n)) return 'rain';
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

// ─── Colourful sky: sun/moon arc from sunrise/sunset (Open-Meteo local ISO times) ─
let _lastDailySnapshot = null;
let _orbTimer = null;

function applyHeroSkyOrbPosition() {
  const orb  = document.getElementById('idle-hero-orb');
  const main = document.getElementById('idle-main-hero');
  if (!orb) return;
  const now = new Date();
  const t0    = now.getTime();
  const daily = _lastDailySnapshot;
  let x   = 72;
  let y   = 28;
  let day = true;

  if (daily && daily.sunrise && daily.sunset && daily.sunrise[0] && daily.sunset[0]) {
    const rise = new Date(daily.sunrise[0]);
    const setT = new Date(daily.sunset[0]);
    if (t0 >= rise.getTime() && t0 <= setT.getTime()) {
      const u = (t0 - rise.getTime()) / (setT.getTime() - rise.getTime());
      x = 5 + u * 86;
      y = 12 + 50 * (1 - Math.sin(u * Math.PI));
      day = true;
    } else {
      day = false;
      const phase = (t0 / 3600000) % 6.18;
      x = 55 + 28 * Math.sin(phase);
      y = 12 + 18 * Math.sin(phase * 0.7);
    }
  } else {
    const h = now.getHours() + now.getMinutes() / 60;
    if (h >= 6 && h < 20) {
      const u = (h - 6) / 14;
      x = 5 + u * 86;
      y = 12 + 50 * (1 - Math.sin(u * Math.PI));
      day = true;
    } else {
      day = false;
      x = 68;
      y = 16;
    }
  }
  orb.style.left = x + '%';
  orb.style.top  = y + '%';
  const disc = document.getElementById('idle-hero-sun-disc');
  if (disc) {
    if (day) {
      disc.classList.remove('idle-hero-moon');
    } else {
      disc.classList.add('idle-hero-moon');
    }
  }
  if (main) {
    main.setAttribute('data-orb-day', day ? '1' : '0');
  }
}

function updateHeroCelestialFromDaily(daily) {
  _lastDailySnapshot = daily || null;
  applyHeroSkyOrbPosition();
}
window.applyHeroSkyOrbPosition = applyHeroSkyOrbPosition;

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
  if ($idleLocationHero) {
    $idleLocationHero.textContent = WEATHER_CONFIG.locationLabel;
  }
  if ($idleCondHero) {
    $idleCondHero.textContent = weatherLabelFromCode(code);
  }
  if ($idleFeelsHero) {
    $idleFeelsHero.textContent = Number.isFinite(feelsLike) ? 'Feels like ' + Math.round(feelsLike) + '°' : '';
  }
  if ($idleTempHero) {
    $idleTempHero.textContent = Number.isFinite(temp) ? Math.round(temp) + '°' : '—';
  }
  if ($idleMainHero) {
    $idleMainHero.setAttribute('data-daytime', isDay ? '1' : '0');
    if (code !== null && code !== undefined && !Number.isNaN(Number(code))) {
      $idleMainHero.setAttribute('data-wmo', String(code));
    } else {
      $idleMainHero.removeAttribute('data-wmo');
    }
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
  if (window.idleAtmosphere) {
    window.idleAtmosphere.onWeatherData(code, isDay, temp);
  }

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
  updateHeroCelestialFromDaily(daily);
}

function renderWeatherOffline() {
  if (window.idleAtmosphere) {
    window.idleAtmosphere.onOffline();
  }
  if ($idleWeatherCond && !$idleWeatherCond.textContent) {
    if ($idleWeatherIcon)  $idleWeatherIcon.innerHTML = weatherIconSVG('sun');
    if ($idleWeatherTemp)  $idleWeatherTemp.textContent = '—°C';
    if ($idleWeatherCond)  $idleWeatherCond.textContent = 'Offline';
    if ($idleLocationLbl)  $idleLocationLbl.textContent = WEATHER_CONFIG.locationLabel;
  }
  if ($idleCondHero)  $idleCondHero.textContent = 'Offline';
  if ($idleTempHero)  $idleTempHero.textContent = '—';
  if ($idleLocationHero)  $idleLocationHero.textContent = WEATHER_CONFIG.locationLabel;
  if ($idleMainHero)   $idleMainHero.removeAttribute('data-wmo');
  _lastDailySnapshot = null;
  applyHeroSkyOrbPosition();
}

// ─── Visibility ────────────────────────────────────────────────────────────────
function applyWeatherVisibility() {
  const show = WEATHER_CONFIG.enabled;
  [
    $idleWeather,
    $idleTempGroup,
    $idleMetaStats,
    $idleLocationCond,
    $idleForecast,
  ].forEach(el => {
    if (el) el.style.display = show ? '' : 'none';
  });
  const hRight = document.getElementById('idle-hero-right');
  if (hRight) hRight.style.display = show ? '' : 'none';
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
    if (window.idleAtmosphere) {
      window.idleAtmosphere.onConfigLoaded(cfg);
    }
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
  if (!WEATHER_CONFIG.enabled) {
    if (window.idleAtmosphere) {
      window.idleAtmosphere.onWeatherDisabledOrWaiting();
    }
    return;
  }

  // Placeholder until first fetch
  if ($idleWeatherTemp)  $idleWeatherTemp.textContent = '—°C';
  if ($idleWeatherCond)  $idleWeatherCond.textContent = '…';
  if ($idleLocationLbl)  $idleLocationLbl.textContent = WEATHER_CONFIG.locationLabel;
  if ($idleLocationHero) $idleLocationHero.textContent = WEATHER_CONFIG.locationLabel;
  if ($idleCondHero)     $idleCondHero.textContent = '…';
  if ($idleTempHero)     $idleTempHero.textContent = '—';

  await refreshWeather();

  if (_weatherTimer) clearInterval(_weatherTimer);
  _weatherTimer = setInterval(refreshWeather, WEATHER_CONFIG.refreshMS);
  if (_orbTimer) clearInterval(_orbTimer);
  _orbTimer = setInterval(applyHeroSkyOrbPosition, 60 * 1000);
  applyHeroSkyOrbPosition();
}

startWeatherLoop();

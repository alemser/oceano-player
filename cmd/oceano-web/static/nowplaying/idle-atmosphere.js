// Idle screen “colourful” mode: iOS/Weather-like gradients and text tints (classic mode unchanged).
(function () {
  'use strict';

  const $idle = document.getElementById('idle-screen');
  if (!$idle) return;

  const IDLE_PROPS = [
    '--idle-sky', '--idle-orb', '--idle-orb-c',
    '--idle-t', '--idle-sec', '--idle-date',
    '--idle-loc', '--idle-cond', '--idle-sep',
    '--idle-stat-l', '--idle-stat-v', '--idle-temp', '--idle-feels', '--idle-icon',
    '--idle-logo', '--idle-logot', '--idle-bar',
    '--idle-fc-n', '--idle-fc-td', '--idle-fc-mx', '--idle-fc-mn', '--idle-fc-ic', '--idle-fc-pr', '--idle-fc-br',
    '--idle-hero-loc', '--idle-hero-h', '--idle-hero-sub', '--idle-hero-sec', '--idle-hero-date', '--idle-hero-cond', '--idle-hero-feels',
  ];

  let _theme = 'classic';
  let _timeTimer = null;

  function themeFromConfig(cfg) {
    if (!cfg || !cfg.now_playing) return 'classic';
    const t = String(cfg.now_playing.idle_screen_theme || 'classic').toLowerCase();
    if (t === 'colourful' || t === 'colorful') return 'colourful';
    return 'classic';
  }

  function clearTimeTimer() {
    if (_timeTimer) {
      clearInterval(_timeTimer);
      _timeTimer = null;
    }
  }

  function clearIdleVars() {
    IDLE_PROPS.forEach(p => {
      $idle.style.removeProperty(p);
    });
    $idle.classList.remove('idle-theme-colourful');
  }

  function setVars(obj) {
    for (const k of Object.keys(obj)) {
      $idle.style.setProperty(k, obj[k]);
    }
  }

  function withHeroAliases(p) {
    if (!p) return p;
    return {
      ...p,
      '--idle-hero-loc':   p['--idle-hero-loc']   || p['--idle-loc']  || 'rgba(255,255,255,0.95)',
      '--idle-hero-h':     p['--idle-hero-h']     || p['--idle-t']    || 'rgba(255,255,255,0.95)',
      '--idle-hero-sub':   p['--idle-hero-sub']   || p['--idle-sec']  || 'rgba(255,255,255,0.8)',
      '--idle-hero-sec':   p['--idle-hero-sec']   || p['--idle-sec']  || 'rgba(255,255,255,0.5)',
      '--idle-hero-date':  p['--idle-hero-date']  || p['--idle-date'] || 'rgba(255,255,255,0.8)',
      '--idle-hero-cond':  p['--idle-hero-cond']  || p['--idle-cond'] || 'rgba(255,255,255,0.75)',
      '--idle-hero-feels': p['--idle-hero-feels'] || p['--idle-feels'] || 'rgba(255,255,255,0.5)',
    };
  }

  function nudgeWarmCold(isDay, base, tC) {
    if (!isDay) return base;
    if (!Number.isFinite(tC) || tC < 30) return base;
    const orb0 = parseFloat(String(base['--idle-orb'] != null ? base['--idle-orb'] : '0'), 10) || 0;
    return { ...base, '--idle-orb': String(Math.min(0.64, orb0 + 0.1)) };
  }

  function wmoToPalette(n, isDay, tempC) {
    const t = Math.round(n);
    const d = isDay;
    let p;

    if ([95, 96, 99].includes(t)) {
      p = {
        '--idle-sky': 'linear-gradient(170deg, #1e1530 0%, #2a1c3a 40%, #100818 100%)',
        '--idle-orb': '0.2',
        '--idle-orb-c': 'rgba(200, 120, 255, 0.35)',
        '--idle-t': 'rgba(245, 240, 255, 0.95)',
        '--idle-sec': 'rgba(200, 180, 230, 0.7)',
        '--idle-date': 'rgba(200, 190, 220, 0.65)',
        '--idle-loc': 'rgba(220, 210, 255, 0.75)',
        '--idle-cond': 'rgba(200, 190, 240, 0.85)',
        '--idle-sep': 'rgba(160, 140, 200, 0.35)',
        '--idle-stat-l': 'rgba(180, 160, 210, 0.5)',
        '--idle-stat-v': 'rgba(220, 210, 255, 0.85)',
        '--idle-temp': 'rgba(230, 220, 255, 0.9)',
        '--idle-feels': 'rgba(200, 195, 235, 0.6)',
        '--idle-icon': 'rgba(200, 185, 240, 0.85)',
        '--idle-logo': 'rgba(180, 150, 230, 0.5)',
        '--idle-logot': 'rgba(210, 195, 255, 0.55)',
        '--idle-bar': 'rgba(255, 255, 255, 0.08)',
        '--idle-fc-n': 'rgba(160, 150, 200, 0.55)',
        '--idle-fc-td': 'rgba(200, 170, 255, 0.8)',
        '--idle-fc-mx': 'rgba(230, 220, 255, 0.8)',
        '--idle-fc-mn': 'rgba(160, 150, 200, 0.55)',
        '--idle-fc-ic': 'rgba(200, 185, 240, 0.7)',
        '--idle-fc-pr': 'rgba(200, 170, 255, 0.85)',
        '--idle-fc-br': 'rgba(255, 255, 255, 0.1)',
      };
    } else if ([71, 73, 75, 77, 85, 86].includes(t)) {
      p = {
        '--idle-sky': 'linear-gradient(180deg, #9ebfe8 0%, #c4daf2 45%, #a8c0dc 100%)',
        '--idle-orb': '0.18',
        '--idle-orb-c': 'rgba(255, 255, 255, 0.55)',
        '--idle-t': 'rgba(18, 32, 55, 0.92)',
        '--idle-sec': 'rgba(32, 52, 80, 0.65)',
        '--idle-date': 'rgba(32, 48, 70, 0.55)',
        '--idle-loc': 'rgba(28, 45, 68, 0.6)',
        '--idle-cond': 'rgba(22, 40, 62, 0.7)',
        '--idle-sep': 'rgba(40, 60, 90, 0.25)',
        '--idle-stat-l': 'rgba(28, 48, 72, 0.4)',
        '--idle-stat-v': 'rgba(20, 38, 60, 0.65)',
        '--idle-temp': 'rgba(15, 35, 60, 0.88)',
        '--idle-feels': 'rgba(32, 55, 82, 0.5)',
        '--idle-icon': 'rgba(32, 55, 90, 0.75)',
        '--idle-logo': 'rgba(40, 80, 140, 0.4)',
        '--idle-logot': 'rgba(25, 50, 85, 0.45)',
        '--idle-bar': 'rgba(30, 50, 80, 0.1)',
        '--idle-fc-n': 'rgba(32, 52, 80, 0.45)',
        '--idle-fc-td': 'rgba(20, 45, 120, 0.7)',
        '--idle-fc-mx': 'rgba(18, 40, 65, 0.8)',
        '--idle-fc-mn': 'rgba(40, 60, 90, 0.45)',
        '--idle-fc-ic': 'rgba(32, 60, 100, 0.6)',
        '--idle-fc-pr': 'rgba(20, 45, 120, 0.6)',
        '--idle-fc-br': 'rgba(32, 55, 80, 0.12)',
      };
    } else if ([45, 48].includes(t)) {
      p = {
        '--idle-sky': 'linear-gradient(180deg, #c4ccd4 0%, #aeb8c4 50%, #98a2ae 100%)',
        '--idle-orb': '0',
        '--idle-orb-c': 'rgba(200, 210, 220, 0.3)',
        '--idle-t': 'rgba(22, 32, 44, 0.9)',
        '--idle-sec': 'rgba(40, 52, 64, 0.55)',
        '--idle-date': 'rgba(38, 50, 62, 0.5)',
        '--idle-loc': 'rgba(35, 45, 58, 0.55)',
        '--idle-cond': 'rgba(30, 40, 52, 0.7)',
        '--idle-sep': 'rgba(50, 60, 72, 0.2)',
        '--idle-stat-l': 'rgba(40, 50, 64, 0.38)',
        '--idle-stat-v': 'rgba(32, 44, 58, 0.65)',
        '--idle-temp': 'rgba(20, 35, 50, 0.85)',
        '--idle-feels': 'rgba(35, 48, 60, 0.5)',
        '--idle-icon': 'rgba(38, 52, 70, 0.65)',
        '--idle-logo': 'rgba(30, 45, 60, 0.35)',
        '--idle-logot': 'rgba(32, 48, 64, 0.4)',
        '--idle-bar': 'rgba(40, 50, 62, 0.1)',
        '--idle-fc-n': 'rgba(40, 52, 64, 0.45)',
        '--idle-fc-td': 'rgba(20, 45, 100, 0.65)',
        '--idle-fc-mx': 'rgba(20, 38, 55, 0.78)',
        '--idle-fc-mn': 'rgba(50, 62, 75, 0.45)',
        '--idle-fc-ic': 'rgba(40, 55, 70, 0.55)',
        '--idle-fc-pr': 'rgba(20, 50, 110, 0.5)',
        '--idle-fc-br': 'rgba(30, 42, 55, 0.12)',
      };
    } else if ([51, 53, 55, 56, 57].includes(t)) {
      p = {
        '--idle-sky': 'linear-gradient(180deg, #5a6a7a 0%, #3d4c5c 50%, #2a3442 100%)',
        '--idle-orb': '0',
        '--idle-orb-c': 'rgba(0,0,0,0)',
        '--idle-t': 'rgba(240, 245, 255, 0.94)',
        '--idle-sec': 'rgba(200, 210, 230, 0.55)',
        '--idle-date': 'rgba(190, 200, 220, 0.5)',
        '--idle-loc': 'rgba(200, 210, 230, 0.6)',
        '--idle-cond': 'rgba(210, 220, 240, 0.7)',
        '--idle-sep': 'rgba(160, 175, 200, 0.3)',
        '--idle-stat-l': 'rgba(150, 165, 195, 0.45)',
        '--idle-stat-v': 'rgba(220, 230, 250, 0.8)',
        '--idle-temp': 'rgba(230, 240, 255, 0.9)',
        '--idle-feels': 'rgba(180, 195, 220, 0.6)',
        '--idle-icon': 'rgba(200, 215, 240, 0.75)',
        '--idle-logo': 'rgba(160, 180, 210, 0.45)',
        '--idle-logot': 'rgba(200, 215, 240, 0.5)',
        '--idle-bar': 'rgba(255, 255, 255, 0.08)',
        '--idle-fc-n': 'rgba(160, 180, 210, 0.5)',
        '--idle-fc-td': 'rgba(190, 210, 255, 0.85)',
        '--idle-fc-mx': 'rgba(220, 235, 255, 0.85)',
        '--idle-fc-mn': 'rgba(150, 170, 200, 0.5)',
        '--idle-fc-ic': 'rgba(200, 215, 240, 0.65)',
        '--idle-fc-pr': 'rgba(160, 195, 255, 0.75)',
        '--idle-fc-br': 'rgba(255, 255, 255, 0.1)',
      };
    } else if ([61, 63, 65, 66, 67, 80, 81, 82].includes(t)) {
      p = {
        '--idle-sky': 'linear-gradient(180deg, #3d5268 0%, #2a3a4c 50%, #1a2432 100%)',
        '--idle-orb': '0',
        '--idle-orb-c': 'rgba(100, 140, 200, 0.2)',
        '--idle-t': 'rgba(230, 238, 255, 0.95)',
        '--idle-sec': 'rgba(150, 175, 210, 0.55)',
        '--idle-date': 'rgba(160, 180, 210, 0.5)',
        '--idle-loc': 'rgba(170, 195, 225, 0.65)',
        '--idle-cond': 'rgba(200, 215, 240, 0.75)',
        '--idle-sep': 'rgba(100, 130, 180, 0.35)',
        '--idle-stat-l': 'rgba(130, 155, 195, 0.45)',
        '--idle-stat-v': 'rgba(210, 222, 245, 0.9)',
        '--idle-temp': 'rgba(220, 234, 255, 0.95)',
        '--idle-feels': 'rgba(160, 180, 210, 0.6)',
        '--idle-icon': 'rgba(200, 215, 240, 0.8)',
        '--idle-logo': 'rgba(140, 170, 210, 0.4)',
        '--idle-logot': 'rgba(180, 200, 235, 0.5)',
        '--idle-bar': 'rgba(200, 220, 255, 0.1)',
        '--idle-fc-n': 'rgba(150, 175, 210, 0.5)',
        '--idle-fc-td': 'rgba(150, 195, 255, 0.85)',
        '--idle-fc-mx': 'rgba(200, 225, 255, 0.88)',
        '--idle-fc-mn': 'rgba(120, 150, 195, 0.55)',
        '--idle-fc-ic': 'rgba(180, 200, 235, 0.7)',
        '--idle-fc-pr': 'rgba(150, 190, 255, 0.8)',
        '--idle-fc-br': 'rgba(255, 255, 255, 0.1)',
      };
    } else if (t === 0 && d) {
      p = nudgeWarmCold(true, {
        '--idle-sky': 'linear-gradient(165deg, #4ab0ff 0%, #5ec4ff 38%, #3d8ed4 100%)',
        '--idle-orb': '0.5',
        '--idle-orb-c': 'rgba(255, 235, 200, 0.55)',
        '--idle-t': 'rgba(12, 32, 58, 0.9)',
        '--idle-sec': 'rgba(18, 50, 88, 0.5)',
        '--idle-date': 'rgba(20, 48, 78, 0.5)',
        '--idle-loc': 'rgba(18, 50, 82, 0.6)',
        '--idle-cond': 'rgba(15, 40, 68, 0.68)',
        '--idle-sep': 'rgba(30, 60, 100, 0.2)',
        '--idle-stat-l': 'rgba(20, 48, 80, 0.38)',
        '--idle-stat-v': 'rgba(15, 42, 70, 0.65)',
        '--idle-temp': 'rgba(8, 38, 70, 0.88)',
        '--idle-feels': 'rgba(20, 52, 90, 0.48)',
        '--idle-icon': 'rgba(10, 45, 90, 0.72)',
        '--idle-logo': 'rgba(15, 60, 120, 0.4)',
        '--idle-logot': 'rgba(12, 50, 100, 0.5)',
        '--idle-bar': 'rgba(8, 40, 80, 0.12)',
        '--idle-fc-n': 'rgba(18, 50, 85, 0.4)',
        '--idle-fc-td': 'rgba(12, 60, 140, 0.75)',
        '--idle-fc-mx': 'rgba(5, 35, 70, 0.82)',
        '--idle-fc-mn': 'rgba(30, 55, 90, 0.4)',
        '--idle-fc-ic': 'rgba(12, 50, 95, 0.6)',
        '--idle-fc-pr': 'rgba(8, 55, 150, 0.65)',
        '--idle-fc-br': 'rgba(10, 45, 80, 0.12)',
      }, tempC);
    } else if (t === 0 && !d) {
      p = {
        '--idle-sky': 'linear-gradient(180deg, #0c1018 0%, #121a2a 45%, #080c18 100%)',
        '--idle-orb': '0.2',
        '--idle-orb-c': 'rgba(100, 140, 255, 0.25)',
        '--idle-t': 'rgba(230, 240, 255, 0.92)',
        '--idle-sec': 'rgba(130, 155, 200, 0.55)',
        '--idle-date': 'rgba(140, 165, 200, 0.45)',
        '--idle-loc': 'rgba(150, 180, 220, 0.5)',
        '--idle-cond': 'rgba(170, 200, 240, 0.65)',
        '--idle-sep': 'rgba(60, 90, 130, 0.35)',
        '--idle-stat-l': 'rgba(100, 130, 175, 0.45)',
        '--idle-stat-v': 'rgba(200, 220, 250, 0.8)',
        '--idle-temp': 'rgba(210, 230, 255, 0.9)',
        '--idle-feels': 'rgba(140, 170, 210, 0.55)',
        '--idle-icon': 'rgba(180, 210, 250, 0.75)',
        '--idle-logo': 'rgba(80, 120, 200, 0.35)',
        '--idle-logot': 'rgba(150, 190, 250, 0.4)',
        '--idle-bar': 'rgba(200, 220, 255, 0.08)',
        '--idle-fc-n': 'rgba(120, 150, 195, 0.4)',
        '--idle-fc-td': 'rgba(160, 200, 255, 0.7)',
        '--idle-fc-mx': 'rgba(200, 230, 255, 0.9)',
        '--idle-fc-mn': 'rgba(90, 120, 160, 0.45)',
        '--idle-fc-ic': 'rgba(150, 185, 240, 0.65)',
        '--idle-fc-pr': 'rgba(140, 180, 255, 0.7)',
        '--idle-fc-br': 'rgba(200, 220, 255, 0.08)',
      };
    } else if ((t === 1 || t === 2) && d) {
      p = nudgeWarmCold(true, {
        '--idle-sky': 'linear-gradient(165deg, #6ab8f8 0%, #7ec5ff 42%, #4a8cd4 100%)',
        '--idle-orb': '0.4',
        '--idle-orb-c': 'rgba(255, 230, 180, 0.45)',
        '--idle-t': 'rgba(14, 36, 60, 0.9)',
        '--idle-sec': 'rgba(20, 52, 90, 0.48)',
        '--idle-date': 'rgba(22, 50, 82, 0.5)',
        '--idle-loc': 'rgba(18, 50, 82, 0.6)',
        '--idle-cond': 'rgba(15, 40, 68, 0.7)',
        '--idle-sep': 'rgba(30, 60, 100, 0.2)',
        '--idle-stat-l': 'rgba(20, 48, 80, 0.38)',
        '--idle-stat-v': 'rgba(15, 45, 75, 0.64)',
        '--idle-temp': 'rgba(8, 40, 72, 0.86)',
        '--idle-feels': 'rgba(22, 55, 92, 0.46)',
        '--idle-icon': 'rgba(10, 48, 88, 0.7)',
        '--idle-logo': 'rgba(15, 60, 115, 0.4)',
        '--idle-logot': 'rgba(12, 50, 100, 0.5)',
        '--idle-bar': 'rgba(8, 40, 80, 0.12)',
        '--idle-fc-n': 'rgba(18, 50, 85, 0.4)',
        '--idle-fc-td': 'rgba(12, 60, 140, 0.75)',
        '--idle-fc-mx': 'rgba(5, 35, 70, 0.8)',
        '--idle-fc-mn': 'rgba(30, 60, 95, 0.4)',
        '--idle-fc-ic': 'rgba(12, 50, 95, 0.6)',
        '--idle-fc-pr': 'rgba(8, 55, 150, 0.65)',
        '--idle-fc-br': 'rgba(10, 45, 80, 0.12)',
      }, tempC);
    } else if ((t === 1 || t === 2) && !d) {
      p = {
        '--idle-sky': 'linear-gradient(175deg, #1a2640 0%, #1e2f48 50%, #121c2e 100%)',
        '--idle-orb': '0.14',
        '--idle-orb-c': 'rgba(160, 190, 255, 0.22)',
        '--idle-t': 'rgba(225, 235, 255, 0.88)',
        '--idle-sec': 'rgba(120, 150, 195, 0.5)',
        '--idle-date': 'rgba(130, 160, 200, 0.45)',
        '--idle-loc': 'rgba(150, 180, 220, 0.55)',
        '--idle-cond': 'rgba(160, 195, 240, 0.65)',
        '--idle-sep': 'rgba(60, 90, 130, 0.32)',
        '--idle-stat-l': 'rgba(100, 135, 180, 0.42)',
        '--idle-stat-v': 'rgba(190, 215, 250, 0.8)',
        '--idle-temp': 'rgba(200, 230, 255, 0.9)',
        '--idle-feels': 'rgba(130, 170, 215, 0.5)',
        '--idle-icon': 'rgba(160, 200, 255, 0.72)',
        '--idle-logo': 'rgba(70, 110, 200, 0.35)',
        '--idle-logot': 'rgba(150, 190, 255, 0.42)',
        '--idle-bar': 'rgba(200, 220, 255, 0.08)',
        '--idle-fc-n': 'rgba(100, 140, 195, 0.4)',
        '--idle-fc-td': 'rgba(150, 195, 255, 0.75)',
        '--idle-fc-mx': 'rgba(200, 230, 255, 0.88)',
        '--idle-fc-mn': 'rgba(80, 120, 170, 0.45)',
        '--idle-fc-ic': 'rgba(120, 170, 240, 0.62)',
        '--idle-fc-pr': 'rgba(130, 180, 255, 0.7)',
        '--idle-fc-br': 'rgba(200, 220, 255, 0.08)',
      };
    } else if (t === 3) {
      p = {
        '--idle-sky': d
          ? 'linear-gradient(180deg, #8a9db0 0%, #6a7c90 45%, #4a5868 100%)'
          : 'linear-gradient(180deg, #2a3040 0%, #1e242e 100%)',
        '--idle-orb': '0',
        '--idle-orb-c': 'rgba(0,0,0,0)',
        '--idle-t': d ? 'rgba(255, 255, 255, 0.94)' : 'rgba(230, 235, 245, 0.9)',
        '--idle-sec': d ? 'rgba(30, 40, 55, 0.45)' : 'rgba(140, 160, 190, 0.5)',
        '--idle-date': d ? 'rgba(20, 28, 40, 0.48)' : 'rgba(150, 170, 200, 0.45)',
        '--idle-loc': d ? 'rgba(15, 25, 40, 0.55)' : 'rgba(160, 180, 210, 0.5)',
        '--idle-cond': d ? 'rgba(15, 24, 38, 0.6)' : 'rgba(180, 200, 230, 0.65)',
        '--idle-sep': d ? 'rgba(25, 40, 60, 0.25)' : 'rgba(100, 120, 150, 0.35)',
        '--idle-stat-l': d ? 'rgba(20, 32, 50, 0.38)' : 'rgba(120, 140, 170, 0.4)',
        '--idle-stat-v': d ? 'rgba(8, 18, 32, 0.65)' : 'rgba(200, 215, 240, 0.8)',
        '--idle-temp': d ? 'rgba(5, 20, 45, 0.82)' : 'rgba(210, 230, 255, 0.9)',
        '--idle-feels': d ? 'rgba(15, 35, 60, 0.48)' : 'rgba(140, 170, 210, 0.5)',
        '--idle-icon': d ? 'rgba(5, 25, 60, 0.65)' : 'rgba(170, 200, 240, 0.7)',
        '--idle-logo': d ? 'rgba(5, 25, 50, 0.35)' : 'rgba(100, 150, 220, 0.4)',
        '--idle-logot': d ? 'rgba(8, 32, 65, 0.4)' : 'rgba(150, 190, 255, 0.45)',
        '--idle-bar': d ? 'rgba(5, 15, 30, 0.12)' : 'rgba(200, 220, 255, 0.1)',
        '--idle-fc-n': d ? 'rgba(20, 35, 50, 0.4)' : 'rgba(120, 150, 190, 0.4)',
        '--idle-fc-td': d ? 'rgba(8, 50, 130, 0.7)' : 'rgba(150, 200, 255, 0.75)',
        '--idle-fc-mx': d ? 'rgba(0, 18, 45, 0.8)' : 'rgba(200, 230, 255, 0.88)',
        '--idle-fc-mn': d ? 'rgba(30, 50, 70, 0.45)' : 'rgba(90, 120, 160, 0.5)',
        '--idle-fc-ic': d ? 'rgba(5, 32, 75, 0.58)' : 'rgba(150, 190, 255, 0.65)',
        '--idle-fc-pr': d ? 'rgba(5, 50, 140, 0.6)' : 'rgba(120, 180, 255, 0.75)',
        '--idle-fc-br': d ? 'rgba(0, 10, 25, 0.1)' : 'rgba(200, 220, 255, 0.08)',
      };
    } else {
      p = wmoToPalette(0, d, tempC);
    }

    return p;
  }

  function hourPalette(h) {
    if (h >= 5 && h < 7) {
      return {
        '--idle-sky': 'linear-gradient(180deg, #2a1a3a 0%, #3d4a6a 35%, #1a1e30 100%)',
        '--idle-orb': '0.5',
        '--idle-orb-c': 'rgba(255, 140, 80, 0.45)',
        '--idle-t': 'rgba(255, 250, 245, 0.9)',
        '--idle-sec': 'rgba(200, 150, 120, 0.4)',
        '--idle-date': 'rgba(200, 180, 200, 0.45)',
        '--idle-loc': 'rgba(220, 210, 240, 0.55)',
        '--idle-cond': 'rgba(220, 215, 255, 0.65)',
        '--idle-sep': 'rgba(200, 120, 100, 0.25)',
        '--idle-stat-l': 'rgba(180, 150, 160, 0.4)',
        '--idle-stat-v': 'rgba(255, 245, 250, 0.8)',
        '--idle-temp': 'rgba(255, 255, 255, 0.9)',
        '--idle-feels': 'rgba(220, 200, 200, 0.55)',
        '--idle-icon': 'rgba(255, 230, 200, 0.75)',
        '--idle-logo': 'rgba(200, 120, 100, 0.35)',
        '--idle-logot': 'rgba(255, 200, 180, 0.45)',
        '--idle-bar': 'rgba(255, 200, 160, 0.12)',
        '--idle-fc-n': 'rgba(200, 150, 150, 0.45)',
        '--idle-fc-td': 'rgba(255, 220, 200, 0.85)',
        '--idle-fc-mx': 'rgba(255, 255, 255, 0.9)',
        '--idle-fc-mn': 'rgba(180, 150, 130, 0.45)',
        '--idle-fc-ic': 'rgba(255, 210, 180, 0.6)',
        '--idle-fc-pr': 'rgba(200, 160, 255, 0.5)',
        '--idle-fc-br': 'rgba(255, 200, 150, 0.1)',
      };
    }
    if (h >= 7 && h < 10) {
      return wmoToPalette(0, true, 15);
    }
    if (h >= 10 && h < 17) {
      return wmoToPalette(0, true, 18);
    }
    if (h >= 17 && h < 20) {
      return {
        '--idle-sky': 'linear-gradient(180deg, #1a0e28 0%, #2d1a40 30%, #4a2a50 50%, #1a2035 100%)',
        '--idle-orb': '0.4',
        '--idle-orb-c': 'rgba(255, 120, 60, 0.45)',
        '--idle-t': 'rgba(255, 245, 240, 0.92)',
        '--idle-sec': 'rgba(200, 160, 140, 0.5)',
        '--idle-date': 'rgba(200, 170, 190, 0.5)',
        '--idle-loc': 'rgba(220, 190, 220, 0.55)',
        '--idle-cond': 'rgba(230, 200, 230, 0.7)',
        '--idle-sep': 'rgba(180, 100, 120, 0.3)',
        '--idle-stat-l': 'rgba(200, 140, 150, 0.45)',
        '--idle-stat-v': 'rgba(255, 240, 250, 0.88)',
        '--idle-temp': 'rgba(255, 255, 255, 0.9)',
        '--idle-feels': 'rgba(220, 180, 200, 0.6)',
        '--idle-icon': 'rgba(255, 200, 180, 0.75)',
        '--idle-logo': 'rgba(200, 100, 80, 0.35)',
        '--idle-logot': 'rgba(255, 180, 150, 0.45)',
        '--idle-bar': 'rgba(255, 150, 120, 0.1)',
        '--idle-fc-n': 'rgba(200, 150, 170, 0.5)',
        '--idle-fc-td': 'rgba(255, 200, 220, 0.8)',
        '--idle-fc-mx': 'rgba(255, 255, 255, 0.9)',
        '--idle-fc-mn': 'rgba(160, 120, 150, 0.5)',
        '--idle-fc-ic': 'rgba(255, 180, 200, 0.65)',
        '--idle-fc-pr': 'rgba(255, 150, 200, 0.6)',
        '--idle-fc-br': 'rgba(255, 180, 200, 0.1)',
      };
    }
    return wmoToPalette(0, false, 10);
  }

  function offlinePalette() {
    return {
      '--idle-sky': 'linear-gradient(180deg, #1a1d24 0%, #141820 100%)',
      '--idle-orb': '0',
      '--idle-orb-c': 'rgba(0,0,0,0)',
      '--idle-t': 'rgba(200, 210, 230, 0.85)',
      '--idle-sec': 'rgba(100, 115, 140, 0.5)',
      '--idle-date': 'rgba(120, 135, 160, 0.45)',
      '--idle-loc': 'rgba(130, 145, 170, 0.5)',
      '--idle-cond': 'rgba(150, 165, 195, 0.55)',
      '--idle-sep': 'rgba(80, 100, 130, 0.3)',
      '--idle-stat-l': 'rgba(100, 115, 140, 0.4)',
      '--idle-stat-v': 'rgba(180, 200, 230, 0.7)',
      '--idle-temp': 'rgba(190, 210, 240, 0.8)',
      '--idle-feels': 'rgba(120, 140, 170, 0.5)',
      '--idle-icon': 'rgba(150, 170, 200, 0.65)',
      '--idle-logo': 'rgba(60, 90, 130, 0.35)',
      '--idle-logot': 'rgba(100, 130, 180, 0.4)',
      '--idle-bar': 'rgba(255, 255, 255, 0.06)',
      '--idle-fc-n': 'rgba(90, 110, 140, 0.45)',
      '--idle-fc-td': 'rgba(120, 160, 220, 0.6)',
      '--idle-fc-mx': 'rgba(180, 200, 230, 0.8)',
      '--idle-fc-mn': 'rgba(80, 100, 130, 0.5)',
      '--idle-fc-ic': 'rgba(120, 150, 190, 0.6)',
      '--idle-fc-pr': 'rgba(100, 140, 200, 0.5)',
      '--idle-fc-br': 'rgba(80, 100, 120, 0.12)',
    };
  }

  function applyPalette(p) {
    clearIdleVars();
    if (_theme !== 'colourful' || !p) return;
    $idle.classList.add('idle-theme-colourful');
    setVars(withHeroAliases(p));
  }

  function applyTimeOnly() {
    if (_theme !== 'colourful') return;
    const h = new Date().getHours();
    applyPalette(hourPalette(h));
  }

  function onConfigLoaded(cfg) {
    _theme = themeFromConfig(cfg);
    clearTimeTimer();
    clearIdleVars();
    if (_theme !== 'colourful') {
      return;
    }
    const weatherOn = !cfg || !cfg.weather || cfg.weather.enabled !== false;
    if (!weatherOn) {
      applyTimeOnly();
      _timeTimer = setInterval(applyTimeOnly, 60 * 1000);
    } else {
      applyTimeOnly();
    }
    if (window.applyHeroSkyOrbPosition) {
      window.applyHeroSkyOrbPosition();
    }
  }

  function onWeatherData(wmo, isDay, tempC) {
    if (_theme !== 'colourful') return;
    if (_timeTimer) {
      clearTimeTimer();
    }
    if (wmo === null || wmo === undefined || wmo === '') {
      applyTimeOnly();
      return;
    }
    const n = Number(wmo);
    if (Number.isNaN(n)) {
      applyTimeOnly();
      return;
    }
    const pal = wmoToPalette(n, isDay, tempC);
    applyPalette(pal);
  }

  function onWeatherDisabledOrWaiting() {
    if (_theme !== 'colourful') return;
    applyTimeOnly();
    if (!_timeTimer) {
      _timeTimer = setInterval(applyTimeOnly, 60 * 1000);
    }
  }

  function onOffline() {
    if (_theme !== 'colourful') return;
    if (_timeTimer) {
      clearTimeTimer();
    }
    applyPalette(offlinePalette());
  }

  window.idleAtmosphere = {
    onConfigLoaded: onConfigLoaded,
    onWeatherData:  onWeatherData,
    onWeatherDisabledOrWaiting: onWeatherDisabledOrWaiting,
    onOffline:     onOffline,
  };
})();

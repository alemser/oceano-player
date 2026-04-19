// ── Library ──────────────────────────────────────────────────────────────────
let _library = [];
let _libLoadedAt = 0;
let _editingId = null;
let _libraryAutoTimer = null;
let _librarySignature = '';

function librarySignature(items) {
  return (items || []).map(e => [
    e.id,
    e.title,
    e.artist,
    e.album || '',
    e.label || '',
    e.released || '',
    e.format || '',
    e.track_number || '',
    e.artwork_path || '',
    e.play_count,
    e.last_played,
  ].join('|')).join('\n');
}

function startLibraryAutoRefresh() {
  stopLibraryAutoRefresh();
  _libraryAutoTimer = setInterval(() => {
    // Avoid re-rendering the grid while editing a modal entry.
    if (_editingId !== null) return;
    loadLibrary();
  }, 5000);
}

function stopLibraryAutoRefresh() {
  if (_libraryAutoTimer) {
    clearInterval(_libraryAutoTimer);
    _libraryAutoTimer = null;
  }
}

async function loadLibrary() {
  try {
    const r = await fetch('/api/library');
    const items = await r.json();
    const sig = librarySignature(items);
    if (sig === _librarySignature) return;
    _library = items;
    _librarySignature = sig;
    _libLoadedAt = Date.now();
  } catch(e) {
    const sig = '';
    if (_librarySignature === sig) return;
    _library = [];
    _librarySignature = sig;
  }
  renderLibrary();
}

function renderLibrary() {
  const search = (document.getElementById('lib-search')?.value || '').toLowerCase();
  const fmt    = document.getElementById('lib-format-filter')?.value || '';
  const grid   = document.getElementById('lib-grid');
  const count  = document.getElementById('lib-count');
  if (!grid) return;

  const filtered = _library.filter(e => {
    const matchSearch = !search ||
      e.title.toLowerCase().includes(search) ||
      e.artist.toLowerCase().includes(search) ||
      (e.album||'').toLowerCase().includes(search);
    const matchFmt = !fmt || e.format === fmt;
    return matchSearch && matchFmt;
  });

  count.textContent = filtered.length + ' / ' + _library.length + ' tracks';

  if (filtered.length === 0) {
    grid.innerHTML = `<div class="lib-empty" style="grid-column:1/-1">
      ${_library.length === 0 ? 'No tracks yet — play a record to start building your collection.' : 'No tracks match your search.'}
    </div>`;
    return;
  }

  grid.innerHTML = filtered.map(e => {
    const fmtClass = (e.format||'unknown').toLowerCase();
    const artUrl   = e.artwork_path ? `/api/library/${e.id}/artwork?t=${_libLoadedAt}` : '';
    const title = e.title || '(Untitled)';
    const artist = e.artist || '';
    return `<div class="lib-card" onclick="openModal(${e.id})">
      <div class="lib-card-art">
        <svg width="32" height="32" viewBox="0 0 24 24" fill="none">
          <path d="M9 18V5l12-2v13M9 18c0 1.657-1.343 3-3 3s-3-1.343-3-3 1.343-3 3-3 3 1.343 3 3zm12-2c0 1.657-1.343 3-3 3s-3-1.343-3-3 1.343-3 3-3 3 1.343 3 3z"
                stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
        ${artUrl ? `<img src="${artUrl}" alt="" onload="this.classList.add('loaded')">` : ''}
      </div>
      <div class="lib-card-body">
        <div class="lib-card-title">${esc(title)}</div>
        <div class="lib-card-artist">${esc(artist)}</div>
        <div class="lib-card-meta">
          <span class="lib-format-badge ${fmtClass}">${e.format||'?'}</span>
        </div>
      </div>
    </div>`;
  }).join('');
}

function openModal(id) {
  const e = _library.find(x => x.id === id);
  if (!e) return;
  _editingId = id;

  document.getElementById('modal-heading').textContent = e.title || 'Edit track';
  document.getElementById('modal-plays').textContent   =
    'Last played: ' + new Date(e.last_played).toLocaleDateString();
  document.getElementById('modal-title').value    = e.title;
  document.getElementById('modal-artist').value   = e.artist;
  document.getElementById('modal-album').value    = e.album || '';
  document.getElementById('modal-format').value   = e.format || 'Unknown';
  document.getElementById('modal-label').value        = e.label || '';
  document.getElementById('modal-released').value     = e.released || '';
  document.getElementById('modal-track-number').value = e.track_number || '';

  const img = document.getElementById('modal-art-img');
  img.classList.remove('loaded');
  if (e.artwork_path) {
    img.src = `/api/library/${id}/artwork?t=${Date.now()}`;
    img.onload = () => img.classList.add('loaded');
  } else {
    img.src = '';
  }

  document.getElementById('lib-modal').classList.add('open');
  loadArtworkPicker(id);
  const idx = _library.findIndex(x => x.id === id);
  document.getElementById('btn-copy-prev').disabled = (idx + 1 >= _library.length);
}

function closeModal() {
  document.getElementById('lib-modal').classList.remove('open');
  document.getElementById('modal-heading').textContent = '';
  document.getElementById('modal-title').value = '';
  document.getElementById('modal-artist').value = '';
  document.getElementById('modal-album').value = '';
  document.getElementById('modal-format').value = 'Unknown';
  document.getElementById('modal-label').value = '';
  document.getElementById('modal-released').value = '';
  document.getElementById('modal-track-number').value = '';
  const img = document.getElementById('modal-art-img');
  img.classList.remove('loaded');
  img.src = '';
  _editingId = null;
}

async function saveEntry() {
  if (!_editingId) return;
  const body = {
    title:    document.getElementById('modal-title').value.trim(),
    artist:   document.getElementById('modal-artist').value.trim(),
    album:    document.getElementById('modal-album').value.trim(),
    format:   document.getElementById('modal-format').value,
    label:    document.getElementById('modal-label').value.trim(),
    released: document.getElementById('modal-released').value.trim(),
    track_number: document.getElementById('modal-track-number').value.trim(),
    artwork_path: (_library.find(x => x.id === _editingId)||{}).artwork_path || '',
  };
  if (!body.title || !body.artist) { toast('Title and artist are required', true); return; }
  const r = await fetch(`/api/library/${_editingId}`, { method:'PUT', headers:{'Content-Type':'application/json'}, body: JSON.stringify(body) });
  if (!r.ok) { toast('Save failed', true); return; }
  toast('Saved');
  closeModal();
  await loadLibrary();
}

async function deleteEntry() {
  if (!_editingId) return;
  if (!confirm('Remove this track from your collection?')) return;
  const r = await fetch(`/api/library/${_editingId}`, { method:'DELETE' });
  if (!r.ok) { toast('Delete failed', true); return; }
  toast('Removed');
  closeModal();
  await loadLibrary();
}

function triggerArtworkUpload() {
  document.getElementById('artwork-file-input').click();
}

async function loadArtworkPicker(excludeId) {
  const wrap    = document.getElementById('artwork-picker-wrap');
  const picker  = document.getElementById('artwork-picker');
  try {
    const r       = await fetch('/api/library/artworks');
    const artworks = await r.json();
    // Exclude the entry being edited
    const others  = artworks.filter(a => a.id !== excludeId);
    if (others.length === 0) { wrap.style.display = 'none'; return; }
    wrap.style.display = 'block';
    picker.innerHTML = others.map(a => `
      <div class="artwork-picker-thumb" onclick="copyArtworkFrom(${a.id})" title="${esc(a.artist)} — ${esc(a.title)}">
        <img src="/api/library/${a.id}/artwork?t=${Date.now()}" alt="">
        <div class="thumb-label">${esc(a.artist)}</div>
      </div>`).join('');
  } catch(e) {
    wrap.style.display = 'none';
  }
}

async function copyArtworkFrom(sourceId) {
  if (!_editingId) return;
  const source = _library.find(x => x.id === sourceId);
  if (!source?.artwork_path) return;

  // Update DB directly: set artwork_path to the source entry's path.
  const entry  = _library.find(x => x.id === _editingId);
  if (!entry) return;
  const body = {
    title:        document.getElementById('modal-title').value.trim()   || entry.title,
    artist:       document.getElementById('modal-artist').value.trim()  || entry.artist,
    album:        document.getElementById('modal-album').value.trim(),
    format:       document.getElementById('modal-format').value,
    label:        document.getElementById('modal-label').value.trim(),
    released:     document.getElementById('modal-released').value.trim(),
    track_number: document.getElementById('modal-track-number').value.trim(),
    artwork_path: source.artwork_path,
  };
  const r = await fetch(`/api/library/${_editingId}`, {
    method: 'PUT', headers: {'Content-Type':'application/json'}, body: JSON.stringify(body)
  });
  if (!r.ok) { toast('Copy failed', true); return; }

  // Update local state and modal preview.
  entry.artwork_path = source.artwork_path;
  const img = document.getElementById('modal-art-img');
  img.classList.remove('loaded');
  img.src = `/api/library/${_editingId}/artwork?t=${Date.now()}`;
  img.onload = () => img.classList.add('loaded');
  toast('Artwork copied');
  await loadLibrary();
}

function fillFromPrevious() {
  if (!_editingId) return;
  const idx = _library.findIndex(x => x.id === _editingId);
  // _library is sorted by last_played DESC — the previous track is at idx+1
  const prev = _library[idx + 1];
  if (!prev) { toast('No previous track found', true); return; }

  document.getElementById('modal-artist').value      = prev.artist      || '';
  document.getElementById('modal-album').value       = prev.album       || '';
  document.getElementById('modal-label').value       = prev.label       || '';
  document.getElementById('modal-released').value    = prev.released    || '';
  document.getElementById('modal-format').value      = prev.format      || 'Unknown';

  // Update artwork preview and local cache so Save picks it up
  const entry = _library.find(x => x.id === _editingId);
  if (entry) entry.artwork_path = prev.artwork_path || '';
  const img = document.getElementById('modal-art-img');
  if (prev.artwork_path) {
    img.classList.remove('loaded');
    img.src = `/api/library/${prev.id}/artwork?t=${Date.now()}`;
    img.onload = () => img.classList.add('loaded');
  } else {
    img.src = '';
  }
  toast(`Copied from "${prev.title}"`);
}

async function uploadArtwork(input) {
  if (!input.files.length || !_editingId) return;
  const form = new FormData();
  form.append('artwork', input.files[0]);
  const r = await fetch(`/api/library/${_editingId}/artwork`, { method:'POST', body: form });
  if (!r.ok) { toast('Upload failed', true); return; }
  const data = await r.json();
  // Update local cache so save() picks up the new path
  const entry = _library.find(x => x.id === _editingId);
  if (entry) entry.artwork_path = data.artwork_path;
  const img = document.getElementById('modal-art-img');
  img.classList.remove('loaded');
  img.src = `/api/library/${_editingId}/artwork?t=${Date.now()}`;
  img.onload = () => img.classList.add('loaded');
  toast('Artwork updated');
  input.value = '';
}

function esc(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// ── Backups ───────────────────────────────────────────────────────────────────

async function loadBackups() {
  const el = document.getElementById('backup-list');
  if (!el) return;
  try {
    const r = await fetch('/api/backups');
    const backups = await r.json();
    renderBackups(backups);
  } catch {
    el.innerHTML = '<div class="backup-empty">Could not load backup list.</div>';
  }
}

function renderBackups(backups) {
  const el = document.getElementById('backup-list');
  if (!el) return;
  if (!backups || backups.length === 0) {
    el.innerHTML = '<div class="backup-empty">No backups yet — the first automatic backup runs shortly after startup.</div>';
    return;
  }
  el.innerHTML = backups.map((b, i) => {
    const date = new Date(b.created_at).toLocaleString();
    const size = formatBytes(b.size_bytes);
    const isLatest = i === 0;
    return `<div class="backup-row">
      <div class="backup-row-info">
        <span class="backup-date">${date}</span>
        ${isLatest ? '<span class="backup-badge">Latest</span>' : ''}
        <span class="backup-size">${size}</span>
        <div class="backup-row-actions">
          <a href="/api/backups/download?file=${encodeURIComponent(b.file)}" download
             class="backup-btn-dl" title="Download">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
            Download
          </a>
          <button type="button" class="backup-btn-restore" onclick="toggleRestoreOptions(this)" title="Restore options">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>
            Restore
          </button>
        </div>
      </div>
      <div class="backup-restore-opts" style="display:none">
        <span class="backup-restore-label">Restore from this backup:</span>
        <div class="backup-restore-btns">
          <button type="button" class="backup-btn-scope" onclick="doRestore('${esc(b.file)}', 'library', this)">Library only</button>
          <button type="button" class="backup-btn-scope" onclick="doRestore('${esc(b.file)}', 'config', this)">Config only</button>
          <button type="button" class="backup-btn-scope backup-btn-scope-both" onclick="doRestore('${esc(b.file)}', 'both', this)">Both</button>
          <button type="button" class="backup-btn-cancel-restore" onclick="toggleRestoreOptions(this.closest('.backup-row').querySelector('.backup-btn-restore'))">Cancel</button>
        </div>
      </div>
    </div>`;
  }).join('');
}

function toggleRestoreOptions(triggerBtn) {
  const row = triggerBtn.closest('.backup-row');
  if (!row) return;
  const opts = row.querySelector('.backup-restore-opts');
  if (!opts) return;
  const willOpen = opts.style.display === 'none';
  // Close all other open restore panels first.
  document.querySelectorAll('.backup-restore-opts').forEach(o => { o.style.display = 'none'; });
  opts.style.display = willOpen ? 'block' : 'none';
}

async function doRestore(file, scope, triggerEl) {
  const labels = { library: 'library database and artwork', config: 'configuration', both: 'library and configuration' };
  if (!confirm(`Restore ${labels[scope] || scope} from this backup?\n\nCurrent data will be replaced.`)) return;

  if (triggerEl) triggerEl.disabled = true;
  try {
    const r = await fetch('/api/backups/restore', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ file, scope }),
    });
    const data = await r.json();
    if (!r.ok || !data.ok) {
      toast((data.results || []).join('; ') || 'Restore failed', true);
      return;
    }
    toast((data.results || ['Restored successfully']).join(' · '));
    // Close restore panel.
    document.querySelectorAll('.backup-restore-opts').forEach(o => { o.style.display = 'none'; });
    // Reload affected data.
    if (scope === 'library' || scope === 'both') {
      _librarySignature = '';
      await loadLibrary();
    }
    if (scope === 'config' || scope === 'both') {
      await loadConfig();
    }
  } catch {
    toast('Restore failed', true);
  } finally {
    if (triggerEl) triggerEl.disabled = false;
  }
}

async function runBackupNow() {
  const btn = document.getElementById('btn-backup-now');
  if (btn) { btn.disabled = true; btn.textContent = 'Backing up…'; }
  try {
    const r = await fetch('/api/backups/run', { method: 'POST' });
    const data = await r.json();
    if (!r.ok || !data.ok) {
      toast('Backup failed', true);
    } else {
      toast('Backup created');
      renderBackups(data.backups);
    }
  } catch {
    toast('Backup failed', true);
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = 'Back Up Now'; }
  }
}

function formatBytes(bytes) {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
}

// ── Upload & Restore ──────────────────────────────────────────────────────────

function onBackupFileSelected(input) {
  const label = document.getElementById('backup-upload-label');
  const btn   = document.getElementById('btn-upload-restore');
  const file  = input.files[0];
  if (file) {
    label.textContent = file.name;
    label.classList.add('has-file');
    if (btn) btn.disabled = false;
  } else {
    label.textContent = 'Choose file…';
    label.classList.remove('has-file');
    if (btn) btn.disabled = true;
  }
}

async function uploadAndRestore() {
  const input = document.getElementById('backup-upload-input');
  const scope = document.getElementById('backup-upload-scope')?.value || 'both';
  const btn   = document.getElementById('btn-upload-restore');
  if (!input?.files?.length) { toast('Select a backup file first', true); return; }

  const labels = { library: 'library database and artwork', config: 'configuration', both: 'library and configuration' };
  if (!confirm(`Restore ${labels[scope] || scope} from the selected file?\n\nCurrent data will be replaced.`)) return;

  if (btn) { btn.disabled = true; btn.textContent = 'Restoring…'; }
  try {
    const form = new FormData();
    form.append('backup', input.files[0]);
    form.append('scope', scope);

    const r = await fetch('/api/backups/upload-restore', { method: 'POST', body: form });
    const data = await r.json();
    if (!r.ok || !data.ok) {
      toast((data.results || []).join('; ') || 'Restore failed', true);
      return;
    }
    toast((data.results || ['Restored successfully']).join(' · '));
    // Reset the file input.
    input.value = '';
    onBackupFileSelected(input);
    // Reload affected data.
    if (scope === 'library' || scope === 'both') {
      _librarySignature = '';
      await loadLibrary();
    }
    if (scope === 'config' || scope === 'both') {
      await loadConfig();
    }
  } catch {
    toast('Restore failed', true);
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = 'Restore'; }
  }
}


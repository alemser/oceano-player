// ── Library ──────────────────────────────────────────────────────────────────
let _library = [];
let _libLoadedAt = 0;
let _editingId = null;
let _libraryAutoTimer = null;
let _librarySignature = '';
let _resolveSearchTimer = null;
let _resolveSearchRequestId = 0;

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
    const title = e.title || (e.is_fingerprint_stub ? `Unresolved fingerprint #${e.id}` : '(Untitled)');
    const artist = e.artist || (e.is_fingerprint_stub ? 'No provider match' : '');
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

  document.getElementById('modal-heading').textContent = e.title || (e.is_fingerprint_stub ? `Unresolved fingerprint #${e.id}` : 'Edit track');
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
  const resolveBtn = document.getElementById('btn-resolve-stub');
  const resolveWrap = document.getElementById('resolve-wrap');
  if (resolveBtn && resolveWrap) {
    const canResolve = !!e.is_fingerprint_stub;
    resolveBtn.style.display = canResolve ? '' : 'none';
    resolveWrap.style.display = 'none';
    document.getElementById('resolve-search').value = '';
    document.getElementById('resolve-results').innerHTML = '';
  }
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
  const resolveBtn = document.getElementById('btn-resolve-stub');
  const resolveWrap = document.getElementById('resolve-wrap');
  if (resolveBtn) resolveBtn.style.display = 'none';
  if (resolveWrap) resolveWrap.style.display = 'none';
  const results = document.getElementById('resolve-results');
  if (results) results.innerHTML = '';
  const search = document.getElementById('resolve-search');
  if (search) search.value = '';
  _editingId = null;
}

function toggleResolvePanel() {
  const wrap = document.getElementById('resolve-wrap');
  if (!wrap) return;
  const willOpen = wrap.style.display === 'none' || !wrap.style.display;
  wrap.style.display = willOpen ? 'block' : 'none';
  if (willOpen) {
    document.getElementById('resolve-search').focus();
    searchResolveTargets();
  }
}

function searchResolveTargets() {
  if (!_editingId) return;
  const q = document.getElementById('resolve-search')?.value?.trim() || '';
  const out = document.getElementById('resolve-results');
  if (!out) return;
  if (_resolveSearchTimer) clearTimeout(_resolveSearchTimer);
  _resolveSearchTimer = setTimeout(async () => {
    const requestId = ++_resolveSearchRequestId;
    if (q.length < 2) {
      if (requestId !== _resolveSearchRequestId) return;
      out.innerHTML = '<div class="resolve-empty">Type at least 2 characters to search.</div>';
      return;
    }
    try {
      const r = await fetch(`/api/library/search?q=${encodeURIComponent(q)}&limit=12`);
      if (requestId !== _resolveSearchRequestId) return;
      if (!r.ok) {
        out.innerHTML = '<div class="resolve-empty">Search failed.</div>';
        return;
      }
      const rows = await r.json();
      if (requestId !== _resolveSearchRequestId) return;
      if (!Array.isArray(rows) || rows.length === 0) {
        out.innerHTML = '<div class="resolve-empty">No matches found.</div>';
        return;
      }
      out.innerHTML = rows.map(row => `
        <div class="resolve-item">
          <div class="resolve-item-main">
            <div class="resolve-item-title">${esc(row.title || '')}</div>
            <div class="resolve-item-sub">${esc(row.artist || '')}${row.album ? ` · ${esc(row.album)}` : ''}</div>
          </div>
          <button type="button" class="resolve-item-btn" onclick="resolveStubTo(${row.id})">Use</button>
        </div>
      `).join('');
    } catch {
      if (requestId !== _resolveSearchRequestId) return;
      out.innerHTML = '<div class="resolve-empty">Search failed.</div>';
    }
  }, 220);
}

async function resolveStubTo(targetId) {
  if (!_editingId || !targetId) return;
  if (!confirm('Resolve this fingerprint stub to the selected track?')) return;

  try {
    const r = await fetch(`/api/library/${_editingId}/resolve`, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({ target_id: targetId })
    });
    if (!r.ok) {
      const msg = (await r.text()) || 'Resolve failed';
      toast(msg, true);
      return;
    }
    toast('Fingerprint stub resolved');
    closeModal();
    await loadLibrary();
  } catch {
    toast('Resolve failed', true);
  }
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


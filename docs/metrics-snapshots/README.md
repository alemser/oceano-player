# Metrics snapshots

This directory holds **operator-local** JSON snapshots of listening / recognition counters (for example before resetting a subset of `recognition_summary` rows).

## API

- `GET /api/recognition/stats` — provider counters and `Trigger.boundary` / `Trigger.fallback_timer` (library SQLite `recognition_summary`).

## Minimal `recognition.providers` after upgrade (jq example)

If physical recognition stopped after an upgrade because `providers` was never written, set at least ACRCloud as primary (backup first):

```bash
sudo cp -a /etc/oceano/config.json "/etc/oceano/config.json.bak.$(date +%Y%m%d%H%M%S)"
tmp="$(mktemp)"
sudo jq '.recognition.providers = [{id:"acrcloud",enabled:true,roles:["primary"]}] | .recognition.merge_policy = (.recognition.merge_policy // "first_success")' /etc/oceano/config.json >"$tmp"
sudo mv "$tmp" /etc/oceano/config.json
sudo systemctl restart oceano-web oceano-state-manager
```

Prefer **Save** from **`oceano-player-ios`** when available so ordering matches the app.

## Clearing Shazamio-only summary rows (on the Pi)

After saving a snapshot, optional reset of **counters only** (does not remove collection entries or play history):

```bash
sudo sqlite3 /var/lib/oceano/library.db \
  "DELETE FROM recognition_summary WHERE provider IN ('Shazamio','ShazamioContinuity','Shazam','ShazamContinuity');"
```

Default library path is `/var/lib/oceano/library.db` unless overridden in `/etc/oceano/config.json`.

## Privacy

Snapshots may reflect personal usage. Add patterns to `.gitignore` if you prefer not to commit them.

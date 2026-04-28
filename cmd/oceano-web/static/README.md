# Static UI Modules (oceano-web)

This folder uses split assets for the configuration/library UI.

## Main files

- `index.html`: page markup only
- `index.css`: styles for config + library + amplifier widgets
- `index.shared.js`: shared utilities, config load/save, status bar, device picker, drawer + power dialog
- `index.library.js`: library grid, modal editing, artwork copy/upload, stub resolve
- `amplifier/model.js`: amplifier inputs/devices models, topology editors, profile actions
- `amplifier/ir.js`: IR learning table, learn polling, result application
- `amplifier/runtime.js`: runtime controls/state polling (power, input nav, transport)
- `index.boot.js`: startup sequence (`loadConfig`, `loadStatus`, `loadLibrary`, `loadAmplifierState`)

## Script load order

Keep this order in `index.html`:

1. `index.shared.js`
2. `index.library.js`
3. `amplifier/model.js`
4. `amplifier/ir.js`
5. `amplifier/runtime.js`
6. `index.boot.js`

`index.boot.js` must stay last because it calls functions defined by the other modules.

## Maintenance rules

- Keep cross-module globals minimal and intentional.
- Put new config/status helpers in `index.shared.js`.
- Put library-only UI logic in `index.library.js`.
- Keep amplifier editor/model logic in `amplifier/model.js`.
- Keep IR-learning transport in `amplifier/ir.js`.
- Keep runtime control/state behavior in `amplifier/runtime.js`.
- Avoid reintroducing inline `<style>` or `<script>` blocks in `index.html`.
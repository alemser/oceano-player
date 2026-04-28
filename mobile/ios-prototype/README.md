# Oceano iOS Prototype (SwiftUI)

This folder contains a lightweight iOS prototype scaffold for an Oceano companion app.

## Goals in this prototype

- Discover Oceano on LAN using Bonjour/mDNS (`_http._tcp`) with API validation.
- Auto-suggest a base URL from discovered services.
- Allow manual host entry fallback.
- Persist selected base URL locally.
- Poll `/api/status` to render current source/track summary.

## What's included

- `OceanoMobilePrototypeApp.swift` (app entry point)
- `ContentView.swift` (host selection + now playing summary UI)
- `Discovery/BonjourDiscovery.swift` (NetServiceBrowser based discovery)
- `Networking/OceanoStateClient.swift` (simple polling client)
- `Models/PlayerState.swift` (JSON model for `/api/status`)
- `Support/HostStore.swift` (persist base URL in `UserDefaults`)

## Create and run in Xcode

1. In Xcode: **File -> New -> Project -> iOS App**.
2. Product name: `OceanoMobilePrototype`, interface `SwiftUI`, language `Swift`.
3. Replace generated files with the files in `mobile/ios-prototype/OceanoMobilePrototype/`.
4. In the app target `Info`:
   - Add `NSLocalNetworkUsageDescription` = `Discover Oceano Player on your local network.`
   - Add `NSBonjourServices` item `_http._tcp`.
5. Build and run on iPhone.

## Behavior notes

- Discovery considers HTTP Bonjour services and validates candidates via `/api/status`.
- If Bonjour discovery does not resolve in time, the UI offers a quick connect path to `http://oceano.local:8080`.
- Manual URL fallback is always available (`http://<ip>:8080`).

## Next steps

- Replace polling with SSE (`/api/stream`).
- Add explicit pairing screen and validation feedback.
- Add quick actions for amplifier input/power.

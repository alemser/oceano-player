# Documentation — Oceano Player

Developer documentation for the Oceano Player backend.

## How the docs are organized

| Directory | Purpose |
|-----------|---------|
| **[reference/](reference/)** | Living technical references — architecture deep-dives, subsystem internals, constant tables. Updated as code changes. |
| **[plans/](plans/)** | Product roadmaps and implementation plans — phased deliverables, acceptance criteria, iOS follow-ups. |
| **[reviews/](reviews/)** | One-time review artifacts — code review findings, audit results. Archival, not updated. |

## Start here

| If you need to… | Read |
|-----------------|------|
| Understand the product vision and long-term architecture | [reference/architecture-vision.md](reference/architecture-vision.md) |
| Understand how physical track recognition works end-to-end | [reference/recognition.md](reference/recognition.md) (quick reference) + [reference/recognition-architecture.md](reference/recognition-architecture.md) (narrative deep-dive) |
| Understand state detection, source priority, and how NowPlaying is driven | [reference/state-detection.md](reference/state-detection.md) |
| Understand amplifier IR control, power detection, profiles | [reference/amplifier-control.md](reference/amplifier-control.md) |
| Know the engineering standards and agent policies | [standards.md](standards.md) |
| After editing explicit recognition providers (`recognition.providers` / `merge_policy`) | [reference/recognition.md](reference/recognition.md#explicit-provider-list-mandatory-verification) + `.cursor/skills/pi-recognition-explicit-providers-smoke/SKILL.md` |
| Make a backend change and evaluate iOS impact | [cross-repo-sync.md](cross-repo-sync.md) |
| See the recognition enhancement roadmap (R1–R10) | [plans/recognition-enhancement.md](plans/recognition-enhancement.md) |
| Improve recognition provider chain (new providers, UX, parallel) | [plans/recognition-provider-chain-improvement.md](plans/recognition-provider-chain-improvement.md) |
| See AirPlay DACP transport plan and status | [plans/airplay-dacp-transport.md](plans/airplay-dacp-transport.md) |


## Quick reference: project layout

```
cmd/
  oceano-source-detector/   Physical/None detector + VU + PCM relay (systemd)
  oceano-state-manager/     Unified state aggregator + recognition + BT monitor (systemd)
  oceano-web/               Config UI + /api/stream SSE + /nowplaying.html
  oceano-setup/             Interactive wizard (AirPlay/BT/devices/display)

internal/
  recognition/              Provider interfaces + ACRCloud + Shazam + chain
  library/                  SQLite collection, artwork, play history, RMS learning
  amplifier/                IR control, power detection, profiles, Broadlink bridge
```

## Conventions

- All code and documentation in **English**.
- This repo is the **backend contract owner** — `oceano-player-ios` is a strict downstream consumer.
- For any API/config/state change, run the [cross-repo sync checklist](cross-repo-sync.md).
- Engineering standards are enforced: see [standards.md](standards.md).

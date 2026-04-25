# Arquitectura de Reconhecimento de Música — Oceano Player

> Documento atualizado em 2026-04-20. Reflecte o estado do código no commit `52ae32a` (branch `main`).

---

## Índice

1. [Visão geral](#1-visão-geral)
2. [Fontes de trigger](#2-fontes-de-trigger)
3. [Ciclo de vida completo — fase a fase](#3-ciclo-de-vida-completo--fase-a-fase)
4. [Captura de PCM](#4-captura-de-pcm)
5. [Cadeia de provedores](#5-cadeia-de-provedores)
6. [Política de confirmação](#6-política-de-confirmação)
7. [Monitor de continuidade Shazam](#7-monitor-de-continuidade-shazam)
8. [Supressão de boundary por duração](#8-supressão-de-boundary-por-duração)
9. [Restauração pre-boundary](#9-restauração-pre-boundary)
10. [Cálculo de seek](#10-cálculo-de-seek)
11. [Persistência de estado](#11-persistência-de-estado)
12. [Tratamento de erros e backoff](#12-tratamento-de-erros-e-backoff)
13. [Constantes de temporização](#13-constantes-de-temporização)
14. [Exemplo completo — Dark Side of the Moon, Lado A](#14-exemplo-completo--dark-side-of-the-moon-lado-a)
15. [Avaliação e trade-offs](#15-avaliação-e-trade-offs)

---

## 1. Visão geral

```
Eventos externos (VU monitor, source poller, fallback timer, SIGUSR1)
         │
         ▼
recognitionCoordinator.run()       ← orquestração e política
         │
         ▼
ChainRecognizer.Recognize()        ← execução de cadeia de provedores
         │
    ┌────┴────┐
    ▼         ▼
 ACRCloud   Shazam                 ← provedores isolados (internal/recognition)
         │
         ▼
applyRecognizedResult()            ← persistência, library, state output
```

Paralelamente, o **Shazam Continuity Monitor** corre numa goroutine independente para detectar transições gapless sem depender de silêncio no áudio.

A interface pública entre camadas é mínima:

```go
type Recognizer interface {
    Name() string
    Recognize(ctx context.Context, wavPath string) (*Result, error)
}
```

O coordinator nunca conhece ACRCloud ou Shazam directamente. `ErrRateLimit` é o único erro semântico exposto — permite backoff diferenciado sem depender de strings de erro.

---

## 2. Fontes de trigger

O coordinator aguarda num `select` sobre duas fontes:

```
m.recognizeTrigger  (chan recognizeTrigger, buffer=1)
fallbackTimer
```

O canal tem **buffer 1** — triggers em excesso são descartados silenciosamente. Só o mais recente importa. Cada trigger carrega dois flags:

```go
type recognizeTrigger struct {
    isBoundary     bool  // verdade para VU, manual, continuity
    isHardBoundary bool  // verdade apenas para silêncio→áudio (não energy-change)
}
```

### Tabela de emissores

| Emissor | Ficheiro | `isBoundary` | `isHard` | Condição |
|---|---|---|---|---|
| VU Monitor — silêncio→áudio | `source_vu_monitor.go` | `true` | `true` | ≥22 frames (~1s) silêncio + ≥11 frames (~0.5s) áudio |
| VU Monitor — energy-change | `source_vu_monitor.go` | `true` | `false` | Dip EMA sustentado ≥32 frames (~1.5s) + recuperação |
| Source poller | `source_vu_monitor.go` | `false` | `false` | Nova sessão Physical ou retoma após idle |
| Shazam Continuity Monitor | goroutine | `true` | `false` | Mismatch confirmado N vezes dentro de 3 min |
| SIGUSR1 (manual) | `main.go` | `true` | `true` | Sinal Unix enviado via systemctl |
| Fallback timer | `recognition_coordinator.go` | `false` | `false` | `RecognizerMaxInterval` (sem resultado) ou `RecognizerRefreshInterval` (com resultado) |

### VU Monitor — detalhes de detecção

O VU monitor lê frames do socket `/tmp/oceano-vu.sock` a ~21.5 Hz (float32 L+R).

**Silêncio→Áudio (hard boundary):**
- Requer `silenceFrames=22` consecutivos abaixo de `silenceThreshold=0.01` RMS
- Seguido de `activeFrames=11` consecutivos acima do threshold
- Gera `isHardBoundary=true` — indica pausa real (levantar a agulha, mudar de lado, parar o CD)

**Energy-change (soft boundary):**
- Dois EMAs paralelos: lento (`alpha=0.005`, τ≈9s) e rápido (`alpha=0.15`, τ≈0.3s)
- Dip detectado quando `fastEMA < slowEMA × 0.45` sustentado ≥32 frames (~1.5s)
- Recuperação quando `fastEMA > slowEMA × 0.75`
- Cooldown de 30s entre triggers — evita rafagas em faixas com passagens silenciosas
- Gera `isHardBoundary=false` — indica transição gapless ou fade between tracks
- Warmup: `energyWarmupFrames=200` (~9s) após início; detector inativo durante warmup

---

## 3. Ciclo de vida completo — fase a fase

### Fase 0 — Detecção de fonte (antes de qualquer reconhecimento)

**O que acontece:**
- `pollSourceFile` lê `/tmp/oceano-source.json` a cada ~2s
- Compara o `source` e o timestamp da última leitura

**Classificação da retoma:**

| Gap desde último áudio | Classificação | Acção |
|---|---|---|
| `> SessionGapThreshold` (45s) | Nova sessão | Limpa resultado, reset de seek, trigger periódico |
| `> IdleDelay` (10s) | Retoma após idle | Reset de seek anchor, trigger periódico |
| `>= physicalResumeRecognitionGap` (2s) | Retoma dentro de sessão | Reset de seek anchor |
| `< 2s` | Continuação normal | Nenhuma acção |

---

### Fase 1 — Início de faixa (boundary trigger)

**Trigger:** hard boundary (silêncio→áudio) ou energy-change

**Sequência:**

```
1. Coordinator recebe trigger{isBoundary:true, isHardBoundary:true/false}
2. Guards pré-captura:
   a. Backoff activo? → se não for boundary, aguarda; boundary bypassa (excepto rate-limit)
   b. Fonte é Physical? → AirPlay/Bluetooth sobrepõem-se; abort se não for Physical
3. Snapshot pre-boundary guardado internamente:
   - preBoundaryResult, preBoundarySeekMS, preBoundaryArtworkPath, preBoundaryLibraryEntryID
4. recognitionResult ← nil  →  UI mostra "Identifying..." (spinner)
5. shazamContinuityReady ← false, shazamContinuityAbandoned ← false
6. Skip de 2s de PCM (descarta buffering da faixa anterior)
7. Captura 7s por defeito (`RecognizerCaptureDuration`; ver `recognition.capture_duration_secs`)
8. Guard pós-captura: fonte ainda é Physical?
9. ChainRecognizer.Recognize(wavPath)
10. Apagar WAV temporário
```

**Possíveis saídas:**

| Resultado | Acção |
|---|---|
| Match (nova faixa) | → Fase 2: applyRecognizedResult |
| Match (mesma faixa) | → Fase 1b: restauração pre-boundary |
| No match | → handleNoMatch: limpa resultado, backoff 15s, retry |
| Rate limit | → handleError: backoff 5 min (strict, não bypassável) |
| Erro genérico | → handleError: backoff 30s, bypassável por boundary |

---

### Fase 1b — Mesmo track re-confirmado após boundary

**Função:** `shouldRestorePreBoundaryResult`

**Condições para restaurar:**
- `!isHardBoundary` — se foi hard (silêncio real), não restaurar
- `preBoundarySeekMS >= BoundaryRestoreMinSeek` (60s) — faixa estava em andamento há tempo suficiente

**Se restaurar:**
- `physicalSeekMS = preBoundarySeekMS + (agora - preBoundarySeekUpdatedAt)` — seek continua monotonicamente
- Artwork e library entry preservados
- Play count não incrementado novamente
- IDs do novo resultado (ex: ShazamID se ACR já tinha) são mesclados via `mergeMissingProviderIDs`

**Se não restaurar:**
- `applyRecognizedResult` chamado normalmente — mesmo sendo "a mesma faixa", seek reset e play count incrementado

---

### Fase 2 — Match identificado (applyRecognizedResult)

**Sequência:**

```
1. Library lookup por ACRID + ShazamID
   - Se encontrado: usa metadata guardada (preserva edições do utilizador)
   - Se não encontrado: usa metadata do provedor
2. Fetch de artwork se não existe:
   - MusicBrainz Cover Art Archive
   - Grava em /var/lib/oceano/artwork/
3. lib.RecordPlay(result, artworkPath) → entryID + timestamp de início (back-dated por seekMS)
4. Releitura da entry final (incorpora merges da library)
5. Cálculo de seekMS (ver Fase 10)
6. tryEnableShazamContinuity() → goroutine separada (fire-and-forget):
   - Captura 4s de PCM
   - Chama Shazam
   - Se concordam: shazamContinuityReady ← true, merge ShazamID
7. Actualiza state sob mutex:
   - recognitionResult, lastRecognizedAt
   - physicalSeekMS, physicalSeekUpdatedAt
   - physicalStartedAt (âncora de sessão)
   - physicalArtworkPath, physicalLibraryEntryID
   - shazamContinuityReady (baseado em isShazamFallback, shazamMatchedACR, ou ShazamID presente)
8. markDirty() → state JSON escrito assincronamente
```

---

### Fase 3 — Meio da faixa (steady state)

**O que corre em paralelo durante a reprodução:**

| Goroutine | Frequência | Papel |
|---|---|---|
| `runShazamContinuityMonitor` | A cada 8s (`ShazamContinuityInterval`) | Detecta transições gapless |
| `runLibrarySync` | A cada 3s | Actualiza metadata da library sem novo reconhecimento |
| `fallbackTimer` | 2 min após último resultado (`RecognizerRefreshInterval`) | Re-check de segurança |
| State writer | On `markDirty()` + tick 5s | Escreve `/tmp/oceano-state.json` |

**Shazam Continuity Monitor (durante steady state):**
- Se `shazamContinuityReady=true` e `durationMs > 0`:
  - **Salta o poll** se `elapsed < durationMs - EarlyCheckMargin (20s)`
  - Ex: faixa de 4 min → monitor inactivo dos 0s até ~3min40s
  - Objectivo: poupar chamadas Shazam durante o corpo da faixa
- Se `shazamContinuityReady=false`:
  - Corre a cada 8s a partir de `ContinuityCalibrationGrace` (45s) após recognition
  - Modo mais conservador: requer 3 sightings (`ContinuityRequiredSightingsUncalibrated`) para trigger

---

### Fase 4 — Aproximação ao fim da faixa (~75% da duração)

**O que muda:**

**Supressão de boundary por duração** (`shouldSuppressBoundary`):
- Quando `elapsed < durationMs × DurationPessimism (0.75)` → suprime o trigger
- **Acima de 75%** da duração estimada: supressão desactivada; boundaries voltam a disparar normalmente

**Shazam Continuity Monitor:**
- Com `durationMs` conhecido: começa a verificar quando `elapsed >= durationMs - 20s`
- Entra em modo activo para detectar a transição iminente

**Fallback timer:**
- `RecognizerRefreshInterval` (2 min) pode disparar se não houve reconhecimento recente
- Com resultado existente, não limpa o UI — apenas re-confirma

---

### Fase 5 — Transição entre faixas (gapless ou com silêncio)

#### 5a — Com silêncio (hard boundary)

```
VU monitor detecta ≥22 frames silêncio + ≥11 frames áudio
→ trigger{isBoundary:true, isHardBoundary:true}
→ Volta à Fase 1
```

#### 5b — Gapless (sem silêncio)

```
VU energy-change OU Shazam Continuity:

Energy-change:
  fastEMA < slowEMA × 0.45 durante ≥1.5s → trigger{isBoundary:true, isHardBoundary:false}

Shazam Continuity:
  Poll retorna faixa diferente → sighting registado
  N sightings dentro de 3 min → trigger{isBoundary:true, isHardBoundary:false}
  (N=2 se calibrado, N=3 se não calibrado)
```

**Diferença chave entre os dois mecanismos:**

| | Energy-change | Shazam Continuity |
|---|---|---|
| Latência | ~1.5–2s após dip | 8–16s após transição |
| Fiabilidade | Alta se há dip de energia | Alta independente de energia |
| Falsos positivos | Passagens silenciosas dentro da faixa | Metadados inconsistentes |
| Complementaridade | Detecta a maioria das transições | Detecta o que o VU perdeu |

---

### Fase 6 — Fim do tempo total (RecognizerMaxInterval)

**Quando dispara:** 5 min (`RecognizerMaxInterval`) sem resultado nenhum (após múltiplos no-match ou logo após início de sessão com Physical).

**Comportamento:**
- `isBoundary=false` — não limpa o UI; se houver resultado anterior, mantém-no
- Captura duração configurada (por defeito 7s) sem skip
- Re-tenta chain (ACRCloud → Shazam)
- Se match: `applyRecognizedResult` com cálculo de seek "timer mode"
- Se no-match: backoff 15s, próximo retry fica no próximo `RecognizerMaxInterval`

**Distinção do fallback de refresh (`RecognizerRefreshInterval` = 2 min):**
- `RefreshInterval` dispara quando **há resultado** — é uma re-confirmação de segurança (gapless que o monitor não detectou)
- `MaxInterval` dispara quando **não há resultado** — é o mecanismo de last-resort

---

## 4. Captura de PCM

**Fonte:** socket Unix `/tmp/oceano-pcm.sock` (exposto pelo `oceano-source-detector`)
**Formato:** S16_LE, 2 canais, 44100 Hz
**Output:** WAV temporário em `/tmp/oceano-rec-<nanosecond>.wav`

O coordinator nunca abre o dispositivo ALSA directamente.

| Contexto | Duração | Skip | Timeout total |
|---|---|---|---|
| Boundary (principal) | 7s (`RecognizerCaptureDuration`, alinhado com `config.json`) | 2s (flush buffer anterior) | ≈19s (`skip+duration+10s` timeout) |
| Timer/manual | 7s | 0s | ≈17s |
| Confirmação | 4s (`ConfirmationCaptureDuration`) | 0s | 14s |
| Shazam alignment | 4s | 0s | 14s |
| Shazam continuity | 4s (`ShazamContinuityCaptureDuration`) | 0s | 14s |

**Skip de 2s nos boundary triggers:** o socket PCM tem um buffer circular interno do `oceano-source-detector`. Nos primeiros 2s após uma transição silêncio→áudio, esse buffer ainda contém amostras da faixa anterior (crackle de agulha em vinyl, click de CD). O skip descarta esses dados.

**Fonte de verdade da duração:** `recognition.capture_duration_secs` em `/etc/oceano/config.json`. Ao gravar na UI web, `oceano-web` reescreve `oceano-state-manager.service` com `--recognizer-capture-duration` igual a esse valor. O default do binário (`defaultConfig` em Go) e o default do JSON gerado pela UI usam o **mesmo** número (7s) para evitar divergência quando alguém corre o state-manager sem unit file.

---

## 5. Cadeia de provedores

`buildRecognitionComponents` constrói **três roles** a partir dos mesmos dois provedores físicos:

| Role | Quem é | Usado por |
|---|---|---|
| `chain` | `ChainRecognizer` (ACR → Shazam ou outro) | Identificação principal |
| `confirmer` | Segundo provider da chain, ou `nil` | Segunda chamada de confirmação |
| `continuity` | Sempre Shazam (`wrapWithStatsAs(..., "ShazamContinuity")`) | Monitor gapless |

O objecto Shazam físico é criado uma vez e partilhado. Cada wrapper tem nome de stats independente (`"Shazam"` vs `"ShazamContinuity"`).

### Políticas configuráveis (`-recognizer-chain`)

| Valor | Primary | Fallback |
|---|---|---|
| `acrcloud_first` (default) | ACRCloud | Shazam |
| `shazam_first` | Shazam | ACRCloud |
| `acrcloud_only` | ACRCloud | — |
| `shazam_only` | Shazam | — |

### ACRCloud

- HMAC-SHA1 sobre multipart HTTP POST
- Timeout: 25s
- Códigos de rate limit: status 4001 ou 4003 → `ErrRateLimit`
- Score: 0–100 (qualidade do match)

### Shazam

- Daemon Python persistente (evita cold-start de 1–3s por chamada)
- Protocolo: escreve path WAV em stdin, lê JSON do stdout
- Timeout hard: 45s por reconhecimento
- Sem score numérico nativo nos resultados

---

## 6. Política de confirmação

**Activada quando:** `ConfirmationDelay > 0` (default: 0 — **desactivada por omissão**) e resultado é faixa nova.

**Bypasses (confirmação não ocorre):**
- Score ≥ `ConfirmationBypassScore` (95) — ACRCloud muito confiante
- É boundary trigger — urgência sobrepõe-se à prudência

**Fluxo quando activa:**
1. Sleep `ConfirmationDelay`
2. Segunda captura de 4s
3. Se dois providers disponíveis: chamadas paralelas em goroutines
4. Lógica de acordo: mesmo ACRID, ou `tracksEquivalent(title, artist)` (normalização de strings)
5. Concordância → aceita; desacordo → mantém candidato original (fail open, registado em logs)

---

## 7. Monitor de continuidade Shazam

Goroutine independente que detecta transições **gapless**.

### Estados

```
UNCALIBRATED ──────────────────────────────────► CALIBRATED
     │                                                │
     │  Aguarda ContinuityCalibrationGrace (45s)      │  Optimização: salta polls se
     │  antes de começar polls                        │  elapsed < durationMs - 20s
     │                                                │
     │  Polls a cada 8s                               │  Polls a cada 8s (só perto do fim)
     │  Requer 3 sightings para trigger               │  Requer 2 sightings para trigger
```

### Calibração

`tryEnableShazamContinuity` corre logo após ACRCloud identificar uma faixa:
- Captura 4s + chama Shazam
- Se concordam com a faixa actual → `shazamContinuityReady=true`
- Os polls normais também calibram oportunisticamente

Se a calibração falhar (Shazam não reconhece a faixa), o monitor fica `UNCALIBRATED` e opera de forma conservadora (N=3).

### Confirmação de mismatch

O monitor regista pares `(fromKey, toKey)` onde a chave é:
1. `acrid:<ACRID>` se disponível
2. `shazam:<ShazamID>` se disponível
3. `meta:<título_normalizado>|<artista_normalizado>` como fallback

| Estado | Sightings necessários | Janela de confirmação |
|---|---|---|
| Calibrado | 2 | 3 min |
| Não calibrado | 3 | 3 min |

Quando confirmado: `triggerBoundaryRecognition(isHardBoundary=false)`.

### Guard contra captura simultânea

`recognizerBusyUntil` impede o monitor de correr enquanto o coordinator está a capturar. Sem isto, duas leituras simultâneas do mesmo socket PCM corromperiam ambas as capturas.

---

## 8. Supressão de boundary por duração

**Função:** `shouldSuppressBoundary`

**Propósito:** evitar falsos positivos em passagens silenciosas dentro de uma faixa (fade, pausa entre movimentos, etc.)

**Lógica:**

```
elapsed = physicalSeekMS + (agora - physicalSeekUpdatedAt)
suppressUntil = durationMs × DurationPessimism (0.75)

Suprime se elapsed < suppressUntil
```

Ou seja: **até 75% da duração estimada, boundaries são ignorados**.

**Grace window após falso positivo:**
- Quando `seekResetFrames=5` (~250ms silêncio) detectados sem trigger real
- `durationGuardBypassWindow=20s` — janela onde a supressão é **desactivada** temporariamente
- Permite que a próxima boundary seja processada imediatamente
- Lógica: o sistema "notou" que há silêncio mas ainda não tem certeza se é transição; abre temporariamente a janela

---

## 9. Restauração pre-boundary

**Problema a resolver:** A agulha passa por uma passagem silenciosa (ex: pausa entre movimentos de uma sinfonia). O VU detecta silêncio→áudio e dispara um boundary. O reconhecimento confirma que é a mesma faixa. Sem restauração, o seek seria resetado e o play count incrementado erroneamente.

**Snapshot guardado antes de limpar o UI:**
- `preBoundaryResult` — resultado completo
- `preBoundarySeekMS` / `preBoundarySeekUpdatedAt` — posição de seek estimada
- `preBoundaryLibraryEntryID` — FK para library
- `preBoundaryArtworkPath` — path de artwork

**Condições de restauração (`shouldRestorePreBoundaryResult`):**

| Condição | Razão |
|---|---|
| `!isHardBoundary` | Boundary hard = pausa real; não restaurar |
| `preBoundarySeekMS >= 60s` | Faixa estava a meio; seek recente < 60s pode estar errado |

**Se restaurar:**
- Seek continua: `preBoundarySeekMS + (agora - preBoundarySeekUpdatedAt)`
- Artwork e entryID preservados
- IDs em falta do resultado novo são mesclados (`mergeMissingProviderIDs`)
- Play não contado novamente

---

## 10. Cálculo de seek

**Função:** `computeRecognizedSeekMS`

O seek é **estimado**, não medido — não há player real com time code.

| Contexto | Cálculo | Reset de âncora |
|---|---|---|
| Boundary trigger | `max(agora - captureStartedAt, agora - lastBoundaryForSeek)` | Sim (novo lastBoundaryForSeek) |
| Timer/manual, mesma faixa, âncora existe | `max(agora - captureStartedAt, agora - physicalStartedAt)` | Não |
| Timer/manual, faixa diferente | `agora - captureStartedAt` | Sim |

O `lastBoundaryForSeek` regista o momento exacto em que o boundary ocorreu, **antes** da captura. Como a captura + reconhecimento demora ~12–15s, sem esta âncora o seek seria sempre 12–15s por baixo.

---

## 11. Persistência de estado

**Ficheiro:** `/tmp/oceano-state.json` (escrita atómica: temp file + rename)

**Schema:**

```json
{
  "source": "Physical|CD|Vinyl|AirPlay|Bluetooth|None",
  "state": "playing|stopped",
  "track": {
    "title": "...",
    "artist": "...",
    "album": "...",
    "track_number": "A2",
    "duration_ms": 180000,
    "seek_ms": 45000,
    "seek_updated_at": "2026-04-20T10:32:00Z",
    "artwork_path": "/var/lib/oceano/artwork/...",
    "samplerate": "44.1 kHz",
    "bitdepth": "16 bit"
  },
  "updated_at": "2026-04-20T10:32:00Z"
}
```

**`runLibrarySync` (tick 3s):**
- Source Physical: refaz lookup na library por ACRID+ShazamID e actualiza metadata sem novo reconhecimento
- Source AirPlay/Bluetooth: procura faixa streaming na library física → `streamingPhysicalMatch`

---

## 12. Tratamento de erros e backoff

| Situação | Backoff | Flag `backoffRateLimited` | Bypassável por boundary? |
|---|---|---|---|
| ACRCloud 4001/4003 | 5 min | `true` | **Não** |
| `ErrRateLimit` genérico | 5 min | `true` | **Não** |
| No match | 15s (`NoMatchBackoff`) | `false` | **Sim** |
| Erro de rede/captura | 30s | `false` | **Sim** |

A lógica de bypass:
```
shouldBypassBackoff = isBoundaryTrigger && !backoffRateLimited
```

Rate limit não é bypassável para **não queimar quota** — se ACRCloud está a limitar, disparar reconhecimentos extras é contraproducente.

**Daemon Shazam:**
- Erro em stdin (processo morreu): restart automático one-shot; retorna erro nesta chamada; próxima chamada usa daemon novo
- Timeout de stdout: process killed; retorna erro

---

## 13. Constantes de temporização

### Coordinator e providers

| Constante | Default | Propósito |
|---|---|---|
| `RecognizerCaptureDuration` | 7s | Amostra de áudio por tentativa (igual ao default em `config.json` / UI) |
| `RecognizerMaxInterval` | 5 min | Fallback sem resultado |
| `RecognizerRefreshInterval` | 2 min | Re-check com resultado existente |
| `NoMatchBackoff` | 15s | Espera após no-match |
| `ConfirmationDelay` | 0s | Delay antes de segunda captura (**desactivado**) |
| `ConfirmationCaptureDuration` | 4s | Amostra para confirmação |
| `ConfirmationBypassScore` | 95 | Skip confirmação se score ACRCloud ≥ este valor |
| `BoundaryRestoreMinSeek` | 60s | Seek mínimo para restaurar resultado pre-boundary |

### Continuity monitor

| Constante | Default | Propósito |
|---|---|---|
| `ShazamContinuityInterval` | 8s | Frequência do monitor |
| `ShazamContinuityCaptureDuration` | 4s | Amostra para continuity check |
| `ContinuityCalibrationGrace` | 45s | Grace period após recognition antes de começar polls |
| `ContinuityMismatchConfirmWindow` | 3 min | Janela para contar sightings de mismatch |
| `ContinuityRequiredSightingsCalibrated` | 2 | Sightings para trigger (calibrado) |
| `ContinuityRequiredSightingsUncalibrated` | 3 | Sightings para trigger (não calibrado) |
| `EarlyCheckMargin` | 20s | Antecipação do check antes do fim estimado da faixa |

### Boundary suppression

| Constante | Default | Propósito |
|---|---|---|
| `DurationPessimism` | 0.75 | Suprime boundaries antes de 75% da duração |
| `DurationGuardBypassWindow` | 20s | Janela de bypass após detecção de falso positivo |

### Source / session

| Constante | Default | Propósito |
|---|---|---|
| `IdleDelaySecs` | 10s | Tempo para manter faixa no UI após silêncio |
| `SessionGapThresholdSecs` | 45s | Gap que reseta a sessão (vs pausa inter-faixa) |

### VU monitor (hardcoded)

| Constante | Valor | Propósito |
|---|---|---|
| `silenceThreshold` | 0.01 RMS | Threshold para detectar silêncio |
| `silenceFrames` | 22 (~1s) | Silêncio contínuo necessário |
| `activeFrames` | 11 (~0.5s) | Áudio contínuo após silêncio |
| `hardSilenceFrames` | 40 (~1.8s) | Distingue pausa real de glitch |
| `seekResetFrames` | 5 (~250ms) | Silêncio breve para armar durationGuardBypass |
| `energyDipRatio` | 0.45 | Dip quando fastEMA < slowEMA × 0.45 |
| `energyRecoverRatio` | 0.75 | Recuperação quando fastEMA > slowEMA × 0.75 |
| `energyDipMinFrames` | 32 (~1.5s) | Duração mínima do dip |
| `energySlowAlpha` | 0.005 (τ≈9s) | EMA lento |
| `energyFastAlpha` | 0.15 (τ≈0.3s) | EMA rápido |
| `energyWarmupFrames` | 200 (~9s) | Warmup antes de energy detection |
| `energyChangeCooldown` | 30s | Mínimo entre triggers de energy-change |

---

## 14. Exemplo completo — Dark Side of the Moon, Lado A

```
t=00:00  Disco colocado no gira-discos. Silêncio.

t=00:00  pollSourceFile: Physical detectado.
         gap > SessionGapThreshold → nova sessão.
         trigger{boundary=false} enviado.

t=00:00  coordinator: sem skip (timer trigger), captura 7s de "Speak to Me"
t=00:07  ACRCloud: no match (faixa é percussão pura sem melodia identificável)
         backoff 15s.

t=00:25  coordinator: retry → no match → backoff 15s

t=00:40  coordinator: captura apanha transição Speak to Me→Breathe
         ACRCloud: "Breathe (In the Air)" — Pink Floyd — score 88
         → applyRecognizedResult
         → seekMS ≈ 37s (captura de 7s + tempo decorrido)
         → artwork fetched do MusicBrainz
         → tryEnableShazamContinuity: goroutine → captura 4s → Shazam: "Breathe" ✓
         → shazamContinuityReady = true (CALIBRADO)

--- Breathe dura 2m43s (163s). durationMs=163000. ---

t=00:40  continuity monitor: elapsed=0s < 163s - 20s = 143s → SALTAR POLLS

t=02:23  continuity monitor: elapsed=103s < 143s → ainda a saltar

t=02:43  continuity monitor: elapsed=123s < 143s → ainda a saltar

t=03:03  continuity monitor: elapsed=143s = threshold → COMEÇA A VERIFICAR
         Poll: Shazam: "Breathe" → match (calibração confirmada)

t=03:11  continuity monitor: poll → Shazam: "On the Run"
         sighting #1/2 registado (par: "breathe|pink floyd" → "on the run|pink floyd")

t=03:19  continuity monitor: poll → Shazam: "On the Run"
         sighting #2/2 → MISMATCH CONFIRMADO (calibrado, N=2)
         → trigger{isBoundary:true, isHardBoundary:false}

t=03:19  coordinator: skip=2s, captura 7s
         Guard pós-captura: Physical ✓
         ACRCloud: "On the Run" — Pink Floyd
         → applyRecognizedResult
         → seekMS ≈ 2s (skip + capture; boundary era recente)
         → tryEnableShazamContinuity → calibrado para "On the Run"

         [Atraso total desde início de "On the Run": ~19s]
         [Sem o continuity monitor (só VU), o atraso dependia de haver silêncio]

--- Vinil, lado A, 21 min total. ---

t=10:00  VU energy-change detectado (transição para "Time")
         fastEMA dip > 32 frames sustentado → trigger{isBoundary:true, isHardBoundary:false}
         → reconhecimento e calibração de "Time"

t=16:00  "The Great Gig in the Sky" — transição gapless sem dip de energia
         continuity monitor detecta via Shazam após 2 sightings (~16s atraso)

t=20:30  DurationPessimism activo para "Great Gig" (dura ~4min40s = 280s)
         elapsed=0.75×280=210s → boundary suppression activa até ~t=23:20

t=20:45  Lado A termina. Silêncio real.
         VU: 22 frames abaixo de threshold → hard boundary detectado
         trigger{isBoundary:true, isHardBoundary:true}
         Skip=2s (descarta click do rim do disco)
         Captura: silêncio → ACRCloud: no match → resultado limpo → UI: "Identifying..."
         backoff 15s → retry → no match → RecognizerMaxInterval (5 min)
```

---

## 15. Avaliação e trade-offs

### Pontos fortes

**Separação de concerns.** `Recognizer` é puro. `ChainRecognizer` é puro. `statsRecognizer` é decorator. `recognitionCoordinator` é puro policy. Cada camada testável isoladamente.

**Canal buffer=1 como flow control.** Nunca acumula fila de reconhecimentos; só o mais recente importa.

**Guard layers antes de side effects.** Três verificações de fonte (antes do skip, após skip, após captura) cobrem a janela de ~12–15s de captura durante a qual a fonte pode mudar.

**Pre-boundary snapshot.** Restauração de seek e artwork evita regressões de UI em passagens silenciosas sem reiniciar o play count.

**Continuity monitor adaptativo.** Salta polls durante o corpo da faixa quando duração é conhecida — poupa chamadas Shazam sem comprometer detecção de transições.

### Tensões e trade-offs

**`run()` como god method (~300 linhas).** Gere triggers, guards, captura, backoff, same-track, confirmação e persistência. Estado local (`preBoundary*`) é explícito mas a função seria difícil de testar unitariamente sem refactoring.

**Seek é estimado.** Calculado a partir de timestamps. Para faixas longas com boundary no meio, pode divergir por vários segundos. Sem player real com time code não é corrigível.

**Shazam como daemon Python.** Cold start de 1–3s se o processo morrer. Sem score numérico. Restart silencioso pode mascarar falhas. Ver `RECOGNITION_FINDINGS.md` para avaliação de alternativas.

**Continuity monitor pode operar sem calibração.** Após `ContinuityCalibrationGrace` (45s), o monitor corre mesmo que Shazam nunca tenha concordado com a faixa actual. Com N=3 sightings é conservador, mas Shazam pode sistematicamente mis-identificar a mesma faixa, gerando falsos positivos periódicos a cada ~3 min.

**`mgr` como estado partilhado implícito.** Coordinator e continuity monitor escrevem nos mesmos campos via mutex sem API formal. Thread-safe, mas acoplamento implícito — mudança no significado de `shazamContinuityReady` requer ler dois ficheiros.

# Arquitectura de Controlo do Amplificador e Dispositivos — Oceano Player

> Documento gerado em 2026-04-20. Reflecte o estado do código no commit `329f0e5` (branch `bug-fixes-general`).

---

## Visão geral

O sistema de controlo do amplificador está distribuído por **três camadas** com responsabilidades bem delimitadas:

```
HTTP API (/api/amplifier/*)
         │
         ▼
BroadlinkAmplifier / BroadlinkCDPlayer   ← IR command dispatch + power detection
         │
    ┌────┴─────┐
    ▼          ▼
PythonBroadlinkClient    PowerStateMonitor
(subprocess bridge)      (polling + inference)
         │
         ▼
broadlink_bridge.py → python-broadlink SDK → RM4 Mini → IR
```

Paralelamente, a detecção de energia lê o socket VU do `oceano-source-detector` e usa `aplay -l` para detectar a presença do DAC USB — sem abrir directamente o dispositivo ALSA.

---

## Camada 1 — Interfaces (`internal/amplifier/interfaces.go`)

```go
type RemoteDevice interface {
    Maker() string / Model() string
    VolumeUp() error / VolumeDown() error
    Play() error / Pause() error / Stop() error
    Next() error / Previous() error
    PowerOn() error / PowerOff() error
}

type Amplifier interface {
    RemoteDevice
    NextInput() error / PrevInput() error
    DetectPowerState(ctx context.Context) (PowerState, error)
}

type InputCycler interface {
    ProbeWithInputCycling(ctx context.Context) (PowerState, error)
}

type CDPlayer interface {
    RemoteDevice
    CurrentTrack() (int, error)      // stub — IR não expõe este dado
    TotalTracks() (int, error)       // stub
    IsPlaying() (bool, error)        // stub
    CurrentTimeSeconds() (int, error) // stub
    TotalTimeSeconds() (int, error)   // stub
    Eject() error
}
```

**Princípio aplicado: Interface Segregation.**
`RemoteDevice` é a base comum. `Amplifier` e `CDPlayer` estendem-na com capacidades específicas. `InputCycler` é opcional — verificado via type assertion para não forçar todos os amplificadores a implementar cycling.

`ErrNotSupported` é o único sentinel semântico — permite ao caller distinguir "falhou" de "este dispositivo não tem esta função".

**`PowerState`** — cinco estados possíveis:

| Estado | Significado |
|---|---|
| `PowerStateOn` | DAC USB detectado ou RMS ≥ 0.012 |
| `PowerStateOff` | Nunca retornado directamente (impossível de confirmar sem feedback) |
| `PowerStateWarmingUp` | Power-on enviado recentemente, dentro do `WarmUp` window |
| `PowerStateStandby` | Reservado — não usado ainda |
| `PowerStateUnknown` | Nenhuma evidência conclusiva |

A escolha de nunca retornar `PowerStateOff` é deliberada: o ruído de fundo de um phono stage pode manter o RMS acima do threshold mesmo com o amplificador desligado. "Desconhecido" é honesto; "desligado" seria um falso positivo.

---

## Camada 2 — Implementações IR (`internal/amplifier/`)

### BroadlinkAmplifier (`broadlink_amplifier.go`)

Responsabilidades:
1. Dispatch de comandos IR via `BroadlinkClient`
2. Detecção passiva de estado de energia
3. Detecção activa via input cycling (quando configurado)

**Lookup de IR codes:**

```go
func (a *BroadlinkAmplifier) sendIR(name string) error {
    a.mu.RLock()
    code, ok := a.settings.IRCodes[name]
    a.mu.RUnlock()
    if !ok { return fmt.Errorf("IR code %q not found", name) }
    return a.client.SendIRCode(code)
}
```

Comandos suportados: `power_on`, `power_off`, `volume_up`, `volume_down`, `next_input`, `prev_input`.
Comandos de transporte (`Play`, `Pause`, `Stop`, `Next`, `Previous`) retornam `ErrNotSupported` — o amplificador não tem transporte próprio.

**DetectPowerState** — cascata de dois checks:

```
Check 1: USB DAC discovery (aplay -l, substring match, timeout 2s)
    → Se DAC presente: PowerStateOn imediato

Check 2: Noise floor via VU socket (lê frames float32 durante 3s)
    → rms >= 0.012 → PowerStateOn
    → rms <  0.012 → PowerStateUnknown
```

O threshold `0.012` foi calibrado empiricamente contra o Magnat MR 780: "ligado e em silêncio" produz RMS ≈ 0.0145 — margem suficiente para evitar falsos negativos causados por variância de captura.

**ProbeWithInputCycling** (implementa `InputCycler`):

Último recurso quando ambos os checks passivos falham. Envia comandos IR de navegação de input e verifica a presença do DAC USB após cada passo. Só activa se `elapsed_silence >= MinSilence` — não interrompe playback em curso.

```
Para cada ciclo (até MaxCycles):
  1. Enviar IR next/prev_input
  2. Aguardar StepWait
  3. Probe USB DAC
  4. Se encontrado → PowerStateOn, guardar input actual
```

### BroadlinkCDPlayer (`broadlink_cdplayer.go`)

Implementa os comandos de transporte do Yamaha CD-S300. Comandos de query (`CurrentTrack`, `IsPlaying`, etc.) são stubs — IR não expõe estado; seria necessário protocolo serial RS-232 por modelo.

### BroadlinkClient (`broadlink_client.go`)

Abstracção do canal de comunicação com o RM4 Mini:

| Implementação | Uso |
|---|---|
| `PythonBroadlinkClient` | Produção — subprocess `broadlink_bridge.py` (protocolo JSON-line) |
| `MockBroadlinkClient` | Testes — regista códigos enviados, injeta erros |
| `NotImplementedBroadlinkClient` | Placeholder quando pairing incompleto |

O bridge Python é invocado como subprocess isolado. Se crashar, não afecta o processo Go. Os comandos são: `pair`, `learn`, `send_ir`.

Paths de pesquisa do bridge (por ordem):
1. `/usr/local/lib/oceano/broadlink_bridge.py`
2. Directório do binário
3. `./scripts/broadlink_bridge.py`

---

## Camada 3 — PowerStateMonitor (`internal/amplifier/power_monitor.go`)

Goroutine de polling independente que combina detecção passiva com inferência por histórico de comandos.

### Lógica de detecção (`detect(ctx)`)

```
1. amp.DetectPowerState(ctx)
   → On? → retorna imediatamente

2. lastCommand == "on" && dentro do WarmUp window?
   → retorna PowerStateWarmingUp

3. InputCyclingEnabled && silence >= MinSilence?
   → cycler.ProbeWithInputCycling(ctx)
   → On? → retorna PowerStateOn
   → Senão → retorna PowerStateUnknown

4. Retorna PowerStateUnknown
```

### Notificações

`NotifyPowerOn()` — chamado pela API após enviar IR de power-on. Regista timestamp e emite imediatamente `PowerStateWarmingUp` (se `WarmUp > 0`) — o utilizador vê feedback antes de o hardware confirmar.

`NotifyPowerOff()` — regista o último comando como "off" para orientar a inferência.

### Padrão Subscriber

```go
ch := monitor.Subscribe()           // canal buffered
defer monitor.Unsubscribe(ch)
for state := range ch { ... }
```

Canais buffered — se um subscriber ficar lento, não bloqueia os outros. O monitor nunca bloqueia no envio.

---

## Detecção de Dispositivos ALSA

### `/api/devices` — scanner ALSA

Lê `/proc/asound/cards` e parseia o formato:

```
 N [ShortName       ]: Driver - Long Name
```

Devolve `[]ALSADevice{Card, Name, Desc}`. Usado pela UI para o device picker — o utilizador selecciona por nome legível sem precisar de saber o número do card.

### USB DAC probe (`power_detection.go`)

`checkUSBDACWithContext` corre `aplay -l` e faz substring match case-insensitive contra `DACMatchString` (por omissão: `Model` do amplificador). Timeout de 2s. Usado em três contextos:

1. `DetectPowerState` — check primário
2. `ProbeWithInputCycling` — a cada passo do ciclo
3. `resetUSBInput` HTTP handler — confirma que o USB foi encontrado após reset

---

## Sistema de Configuração

### Estrutura de config (`cmd/oceano-web/config.go`)

```
AmplifierConfig
  ├── Enabled / Maker / Model
  ├── InputMode: "cycle" | "direct"
  ├── Inputs: []AmplifierInputConfig {ID, LogicalName, Visible}
  ├── Broadlink: {Host, Port, Token, DeviceID}
  ├── IRCodes: map[string]string (comando → código base64)
  ├── WarmUpSecs / StandbyTimeoutMins
  ├── InputCycling: {Enabled, Direction, MaxCycles, StepWait, MinSilence}
  ├── USBReset: {MaxAttempts, FirstStepSettleMS, StepWaitMS}
  └── ConnectedDevices: []ConnectedDeviceConfig {ID, Name, InputIDs, HasRemote, IRCodes}
```

Config é o source of truth único — não há base de dados. O ficheiro `/etc/oceano/config.json` é reescrito atomicamente pela UI (write tmp → rename).

### Sistema de Perfis

Perfis são snapshots reutilizáveis de `AmplifierConfig`. Permitem trocar de modelo de amplificador sem re-aprender todos os códigos IR.

**Activação de perfil:**

```
1. Carregar baseline (built-in ou stored)
2. Preservar: Broadlink credentials + IR codes aprendidos + ConnectedDevices do utilizador
3. Sobrescrever tudo o resto com os defaults do perfil
4. Guardar em config.json
```

**Perfil built-in incluído: Magnat MR 780**

| Campo | Valor |
|---|---|
| Inputs | Phono (oculto), CD, Aux, USB Audio |
| Input mode | Cycle |
| Warm-up | 30s |
| Standby timeout | 20 min |
| Input cycling | Desactivado por omissão |
| Direction | `prev` |

**Export/Import**: Export em JSON (safe mode remove credenciais; full mode inclui-as). Import valida e persiste como custom profile. Built-ins não podem ser apagados.

---

## API HTTP (`cmd/oceano-web/amplifier_api.go`)

| Endpoint | Método | Função |
|---|---|---|
| `/api/amplifier/state` | GET | Identity + power state |
| `/api/amplifier/power-on` | POST | IR power-on + notify monitor |
| `/api/amplifier/power-off` | POST | IR power-off + notify monitor |
| `/api/amplifier/volume` | POST | `{"direction":"up"\|"down"}` |
| `/api/amplifier/next-input` | POST | IR next input (guarded) |
| `/api/amplifier/select-input` | POST | `{"steps":N}` — jump N inputs |
| `/api/amplifier/reset-usb-input` | POST | Hunt USB input via cycling |
| `/api/amplifier/last-known-input` | POST | Persist selected input ID |
| `/api/amplifier/device-action` | POST | CD player: play/pause/stop/next/prev/eject |
| `/api/amplifier/profiles` | GET/POST/DELETE | Profile CRUD |
| `/api/amplifier/profiles/activate` | POST | Switch active profile |
| `/api/amplifier/profiles/export` | GET | JSON export |
| `/api/amplifier/profiles/import` | POST | JSON import |
| `/api/broadlink/learn-start` | POST | Iniciar aprendizagem IR |
| `/api/broadlink/learn-status` | GET | Poll código captado |
| `/api/amplifier/pair-start` | POST | Iniciar pairing RM4 Mini |
| `/api/amplifier/pair-status` | GET | Poll progresso de pairing |

### Streaming USB Guard

`streaming_usb_guard.go` bloqueia `next-input` e `prev-input` enquanto AirPlay ou Bluetooth estão activos. Lê `/tmp/oceano-state.json` para determinar a source actual. Evita que o utilizador quebre acidentalmente o stream em curso ao tentar mudar de input.

### USB Reset Flow

Algoritmo de hunting do input USB para cycle-mode:

```
1. DAC presente? → retorna imediatamente
2. Escolher direcção (shortest path de lastKnownInputID ao USB input)
3. Para cada tentativa (até MaxAttempts):
   a. Enviar IR cycle
   b. Aguardar FirstStepSettleMS (selector do amplificador a iluminar)
   c. Probe DAC → encontrado? → guardar input, retornar
   d. Enviar IR cycle (passo efectivo)
   e. Aguardar StepWaitMS
   f. Probe DAC → encontrado? → guardar input, retornar
4. Retornar "not found" com contagem de tentativas
```

### Aprendizagem IR

```
1. POST /api/broadlink/learn-start {command, device}
2. Server: goroutine → broadlink_bridge.py learn (RM4 Mini entra em modo learning, 30s timeout)
3. Client: poll /api/broadlink/learn-status
4. Sucesso: código persistido em config.json + espelhado no perfil activo
```

---

## Fluxo completo — exemplo: ligar amplificador e seleccionar USB

```
UI: POST /api/amplifier/power-on
  → amp.PowerOn()  → sendIR("power_on") → bridge.py → RM4 Mini → IR
  → monitor.NotifyPowerOn()
  → PowerStateMonitor emite PowerStateWarmingUp imediatamente

[WarmUp window = 30s]

PowerStateMonitor ticker (30s):
  → amp.DetectPowerState()
  → Check 1: aplay -l → DAC encontrado → PowerStateOn
  → monitor broadcast: PowerStateOn → subscribers

UI: POST /api/amplifier/select-input {"steps": 2}
  → StreamingUSBGuard: source == "None" → permitido
  → selectInputForward(2):
      Se selector não activo → enviar IR + aguardar 1.2s
      Enviar 2x IR next_input com inter-step wait
      Guardar último input ID

UI: POST /api/amplifier/reset-usb-input
  → resetUSBInput():
      Probe DAC → não encontrado
      Cycling: enviar IR next_input, aguardar, probe...
      DAC encontrado → guardar "usb_audio" como lastKnownInputID
```

---

## Avaliação de Software Engineering

### Pontos fortes

**Interface Segregation bem aplicada.** `RemoteDevice` é a base mínima; `Amplifier`, `CDPlayer`, `InputCycler` estendem-na sem forçar implementações desnecessárias. Type assertions (`cycler, ok := amp.(InputCycler)`) para capacidades opcionais — evita métodos no-op ou panic em implementações parciais.

**Detecção de energia em dois níveis.** A cascata USB-DAC → noise-floor → input-cycling cobre os casos reais: amplificador ligado e a tocar (DAC), ligado e em silêncio (RMS), e estado incerto (cycling). A decisão de nunca retornar `PowerStateOff` é conservadora e correcta — evita falsos positivos de desligado que levariam a enviar comandos de power-on desnecessários.

**Subprocess bridge Python isolado.** O SDK `python-broadlink` corre num processo separado. Se o bridge crashar, o servidor Go não cai. O protocolo JSON-line é simples e testável independentemente do Go.

**Sistema de perfis com preservação de dados do utilizador.** A activação de um perfil substitui os defaults mas preserva o que o utilizador configurou (Broadlink credentials, IR codes aprendidos, dispositivos ligados). Evita perda acidental de configuração ao trocar de modelo de amplificador.

**Cobertura de testes expressiva.** Três ficheiros de testes com 65+ casos cobrem: noise floor classification, VU socket I/O, input cycling step counting, monitor state machine, profile activation e merge.

### Tensões e trade-offs

**Input mode `direct` não implementado.** A UI expõe a opção, a config suporta-a, mas `selectInputForward` não tem lógica directa — recai em cycle behavior. Um utilizador que configure "direct" terá o mesmo comportamento de "cycle" sem feedback de erro. Deveria ou ser removido da UI até implementado, ou retornar um erro claro.

**CDPlayer query methods são stubs sem fallback.** `CurrentTrack()`, `IsPlaying()`, etc. retornam erro. Se a UI tentar exibir posição de track do CD, vai falhar silenciosamente. A interface é generosa; a implementação não acompanha. Uma solução pragmática seria não incluir estes métodos na interface até existir uma implementação real.

**Sem queue de comandos IR.** Múltiplos cliques rápidos em volume ou input enviam comandos em paralelo para o RM4 Mini sem rate limiting. Para uso de UI humano o risco é baixo; para automação (e.g., programmatic input cycling) pode overwhelm o bridge.

**`StandbyTimeout` existe na config mas `PowerStateStandby` nunca é retornado.** O monitor infere `Unknown` após silêncio prolongado, nunca `Standby`. O campo da config existe, é validado, mas não tem efeito. Cria expectativas falsas.

**Acoplamento implícito ao VU socket path.** `AmplifierSettings.VUSocketPath` é passado directamente de `cfg.Advanced.VUSocket`. Se o path mudar na config (e.g., em desenvolvimento), o monitor de energia reflete automaticamente — mas não há abstracção de "fonte de RMS": está sempre ligado ao socket do `oceano-source-detector`. Para testes de integração, é necessário um servidor de socket mock ou o detector a correr.

---

## Configuração relevante (defaults actuais)

| Parâmetro | Default | Efeito |
|---|---|---|
| `WarmUpSecs` | 30 | Grace period após power-on antes de re-verificar |
| `StandbyTimeoutMins` | 20 | Threshold de inferência de standby (sem efeito actual) |
| `InputCycling.MaxCycles` | 6 | Tentativas máximas de cycling para encontrar input |
| `InputCycling.StepWait` | 800ms | Espera entre passos de cycling |
| `InputCycling.MinSilence` | 30s | Silêncio mínimo antes de tentar cycling |
| `USBReset.MaxAttempts` | 8 | Tentativas de hunting do input USB |
| `USBReset.FirstStepSettleMS` | 1200 | Espera para selector do amplificador iluminar |
| `USBReset.StepWaitMS` | 800 | Espera entre passos de USB reset |
| `noiseFloorOnThreshold` | 0.012 | RMS mínimo para classificar como ligado |
| `Monitor poll interval` | 30s | Frequência de polling de estado de energia |

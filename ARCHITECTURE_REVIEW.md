# Oceano Player — Vision & Architecture

## Proposta de valor

O Oceano Player não é um player de música.

É um **orquestrador de identidade musical contínua** — um sistema que sabe que a faixa tocando no Tidal é a mesma que você tem em vinil, e que o CD que está rodando agora já foi reconhecido antes. A experiência é seamless: independente de onde o áudio vem, você vê o que está tocando, de onde vem, e o que você possui daquilo.

Nenhum produto disponível (moOde, Volumio, Roon) faz essa combinação de mídia física com reconhecimento automático e biblioteca local. Esse é o diferencial real.

---

## O que está construído hoje

### Separação de camadas atual

```
internal/recognition/       → interfaces Recognizer, Fingerprinter (ACRCloud, Shazam, local)
internal/amplifier/         → interfaces Amplifier, CDPlayer, RemoteDevice (IR control)
internal/library/           → SQLite: coleção física de vinil/CD

cmd/oceano-source-detector/ → detecta Physical/None + publica PCM e VU via sockets
cmd/oceano-state-manager/   → agrega todas as fontes em PlayerState unificado → /tmp/oceano-state.json
cmd/oceano-web/             → config UI + SSE stream + library web UI
```

### O que está bem feito
- `internal/recognition` tem interface `Recognizer` plugável — trocar ou adicionar providers não exige mudança no core
- `internal/amplifier` tem interfaces limpas separadas das implementações Broadlink
- O contrato de saída (`PlayerState`) já é o estado normalizado que a UI consome, independente da fonte
- Separação real entre captura de áudio (detector) e lógica de negócio (state-manager)

### Acoplamento que existe hoje
- `main.go` do state-manager (574 linhas) faz wiring de config, goroutines, shairport, VU e recognition em um lugar só — funcional, mas denso
- Não existe um conceito explícito de "source adapter" — AirPlay e Physical são casos especiais no main, não implementações de uma interface comum

---

## A abstração correta para novas fontes

Uma interface `PlaybackEngine` com `play()`, `pause()`, `stop()` não faz sentido aqui porque **nenhuma fonte é controlada pelo oceano**:

- **AirPlay**: o iOS/Mac envia para shairport-sync — oceano só lê metadata
- **Tidal Connect**: o app Tidal controla — oceano observa
- **Bluetooth**: o dispositivo fonte controla — oceano lê via DBUS
- **Physical (Vinyl/CD)**: oceano detecta áudio, não controla

A abstração correta é **`SourceAdapter`** — observa e reporta estado:

```go
type SourceAdapter interface {
    ID()   string
    Type() string // "airplay" | "bluetooth" | "physical" | "tidal"

    Start() error
    Stop() error

    Observe() <-chan SourceEvent
}

type SourceEvent struct {
    Type      string   // "play" | "stop" | "metadata_update"
    Source    string   // "airplay" | "bluetooth" | "physical" | "tidal"
    Track     *Track
    DeviceID  string
    Timestamp int64
}
```

O state-manager vira um **Graph Aggregator**: recebe eventos de todos os adapters, resolve a identidade musical e produz `PlayerState`.

---

## Unified Music Graph (UMG)

### Ideia central

O sistema não rastreia "qual fonte está tocando". Rastreia **qual entidade musical está ativa** — e onde ela existe.

```
Track: "Kid A — Radiohead"
   ├── Source: TIDAL (tocando agora)
   ├── PhysicalMatch: Vinyl — lado A, faixa 1
   └── RecognitionEvent: ACRCloud ACRID xyz, score 95
```

### Entidades

**Track** — a entidade musical canônica:
```go
type Track struct {
    ID      string
    Title   string
    Artists []string
    Album   string
    ISRC    string // chave de matching cross-source quando disponível
}
```

**UnifiedNode** — track com todos os contextos resolvidos:
```go
type UnifiedNode struct {
    Track         *Track
    ActiveSources []SourceInstance   // fontes atualmente ativas
    PhysicalMatch *Release           // vinil/CD correspondente na biblioteca local
}
```

**PlayerState** (saída para UI) continua o mesmo contrato JSON atual, mas agora é derivado do `UnifiedNode`:
```json
{
  "source": "AirPlay",
  "track": { "title": "Kid A", "artist": "Radiohead", "album": "Kid A" },
  "physical_match": { "format": "Vinyl", "side": "A", "track_number": "1" },
  "state": "playing"
}
```

### Matching cross-source

Prioridade de matching ao receber um evento:

1. **ISRC** — quando disponível no Tidal/AirPlay metadata, match exato e confiável
2. **ACR ID / Shazam ID** — para Physical, após reconhecimento
3. **Fuzzy** — título + artista normalizado (já existe em `track_helpers.go`)

O SQLite de `internal/library` já é o "node store" — só precisa de campos adicionais para ISRC e cross-references.

---

## Avaliação de cada feature desejada

### Bluetooth
Tecnicamente direto no Pi: `bluetoothd` + `bluealsa` ou PipeWire. O state-manager precisa de um `BluetoothAdapter` que leia metadata de DBUS (perfil A2DP). A infraestrutura já suporta; é mais um adapter novo.

### Tidal Connect
Restrição real: não existe SDK ou API oficial para controle externo. Opções:

| Abordagem | Viabilidade | Status |
|---|---|---|
| Tidal app → AirPlay → shairport-sync | **Já funciona hoje** | Produção |
| Container `tidal-connect` unofficial | Viável, requer teste | Documentado em `TIDAL_CONNECT_PLAN.md` |
| Detecção via mDNS + estado otimista | Possível, metadata limitado | Futuro |

O caminho mais pragmático hoje: **Tidal app → AirPlay**. Já funciona, sem nenhum esforço adicional.

### Biblioteca unificada (física + streaming)
O diferencial mais forte e único do produto. `internal/library` já tem a coleção física. Implementar o matching significa:
- Quando AirPlay/Tidal/BT tocam algo → buscar por ISRC ou título+artista na library local
- Se encontrar → enriquecer o `PlayerState` com `physical_match`
- UI pode mostrar: *"Você tem este álbum em Vinyl — lado B, faixa 3"*

---

## Roadmap sugerido

### Princípio de execução

O sistema já é 80% arquitetura funcional. O risco real não é falta de estrutura — é refatorar por esporte, criando abstrações antes de ter valor funcional. Cada fase só começa quando a anterior entrega algo observável em produção.

### O que NÃO fazer primeiro
- Não refatorar tudo para `SourceAdapter` de uma vez — gera rewrite sem ganho imediato
- Não modelar o graph completo — ISRC + foreign keys no SQLite já bastam
- Não tocar em Bluetooth ou Tidal Connect até Physical Match estar funcionando

### Fase 1 — SourceEvent canônico (mudança mínima, alto impacto)

O menor diff possível no `state-manager` para introduzir a ideia sem quebrar nada:

- Definir `SourceEvent` como tipo interno em `main.go` (um struct, não um pacote novo)
- Physical e AirPlay continuam como estão, mas passam a emitir `SourceEvent` em vez de atualizar estado diretamente
- O aggregator consome o canal de eventos e produz `PlayerState`
- `main.go` fica só com wiring — o resto não muda

Entregável: state-manager refatorado internamente, comportamento externo idêntico.

### Fase 2 — Physical Match (o diferencial real)

Esse é o valor único do produto. Implementar o match entre o que está tocando e a biblioteca local:

- Adicionar campo `ISRC` na tabela de tracks do SQLite
- Match engine na ordem: ISRC → ACR ID → fuzzy (título + artista normalizado)
- Enriquecer `PlayerState` com `physical_match` quando encontrado
- Now-playing UI exibe: *"Você tem este álbum em Vinyl — lado B, faixa 3"*

Entregável: funcionalidade visível para o usuário final, zero mudança de infra.

### Fase 3 — Bluetooth

Com o `SourceEvent` já em uso, adicionar uma nova fonte vira apenas criar um novo emissor:

- `BluetoothAdapter` lendo metadata via DBUS (perfil A2DP)
- Emite `SourceEvent` igual aos outros
- Nova fonte no `PlayerState`

Entregável: Bluetooth reconhecido e exibido na now-playing UI como AirPlay já é.

### Fase 4 — Tidal Connect nativo

Só atacar depois do Physical Match estável:

- Testar container unofficial (Fase 1 do `TIDAL_CONNECT_PLAN.md`)
- `TidalAdapter` lendo estado do container
- Metadata via container API ou detecção mDNS

### Fase 5 — Library web unificada

- Web UI mostrando biblioteca física + histórico de plays via streaming
- Cruzamento: "este álbum que você ouviu no Tidal existe na sua coleção física"

---

## O que não fazer

- Não recriar moOde, Volumio ou qualquer audio stack — isso é infra commodity
- Não implementar `PlaybackEngine` com controle de playback — nenhuma fonte é controlável pelo oceano
- Não tentar SDK ou reverse engineering do Tidal — não existe suporte oficial
- Não introduzir graph database — SQLite com foreign keys resolve o modelo de entidades
- Não fazer refatoração arquitetural completa antes de ter nova funcionalidade entregue

---

## Conclusão

O Oceano Player já tem as fundações certas. O sistema não precisa de arquitetura mais bonita — precisa de mais fontes alimentando o mesmo estado e do cruzamento com a biblioteca física.

A evolução é incremental:

1. `SourceEvent` canônico no state-manager (estrutural, invisível ao usuário)
2. Physical Match (visível, diferenciador, único no mercado)
3. Novas fontes (Bluetooth, Tidal) como emissores de eventos

Esse é o produto que não existe: consistência de identidade musical entre mundos físicos e digitais.

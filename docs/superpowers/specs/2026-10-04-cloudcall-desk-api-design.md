# CloudCall Desk API — design

Spec da API Go deste repositório. Aprovada em 2026-10-04 a partir de `NFON-Projeto-e-Tutorial.md` e do brainstorm em `docs/superpowers/brainstorm/2026-10-03-cloudcall-desk-api.md`.

A implementação continua incremental, pelo contrato pedagógico da especificação original. Este documento fecha o desenho; não autoriza entregar o backend de uma vez.

## 1. Objetivo e recorte

CloudCall Desk é um painel local de comunicação empresarial, com chamadas simuladas. Não há áudio, SIP, WebRTC, cobrança nem números reais.

Este repositório contém o servidor: API Go, PostgreSQL, migrações, seed, Docker Compose, testes Go e o contrato OpenAPI. Angular, NgRx, interface, debounce/switchMap, DI do EventSource, acessibilidade e Playwright ficam no outro repositório.

O MVP opera com uma empresa e um usuário de demonstração escolhidos pelo servidor. Sem autenticação. A API declara `demoMode: true`. Não é um produto pronto para exposição pública.

PostgreSQL é decisão deste projeto de estudo. Não afirmar que é o banco da NFON.

## 2. Fora de escopo

- Frontend Angular e E2E de tela.
- Imagem Docker da API.
- Proxy de desenvolvimento. O `ng serve` e o processo Go usam portas distintas.
- Autenticação, CSRF, multi-tenant completo, múltiplas instâncias, Redis, broker, ORM, framework HTTP além de `net/http`.
- Timer em background para timeout de toque. `no_answer` e `ring_timeout` entram só pelo endpoint de falha simulada.
- Geração de TypeScript neste repositório. O outro repositório gera tipos a partir de `api/openapi.yaml`.
- Cumprir metas de latência sem medi-las. p95 e 10 clientes SSE são laboratório posterior, com hardware e números registrados.

## 3. Arquitetura

Um processo Go (`cmd/api`) e um PostgreSQL no Docker Compose. A API corre no host (`go run ./cmd/api`).

Monólito modular por domínio. Handlers começam no pacote do domínio. Serviço e store aparecem quando há regra ou SQL real. Interface só em `internal/event`.

No startup o servidor escolhe a empresa e o usuário demo a partir do seed. Nenhum handler aceita `organizationId` ou `userId` do cliente como autorização. O SQL de recurso filtra pela empresa demo. Isolamento por tenant fica preparado na query; autorização completa fica para uma extensão.

| Pacote | Função | Depende de |
|---|---|---|
| `cmd/api` | Liga config, banco, rotas, hub e shutdown | pacotes abaixo |
| `internal/platform/config` | Lê ambiente e falha se faltar valor obrigatório | nada de domínio |
| `internal/platform/httpx` | `net/http`, CORS, `requestId`, log, limite de body, formato de erro | config |
| `internal/platform/db` | Pool `database/sql` + pgx, migrações e seed | config |
| `internal/contact` | Validação, CRUD e busca paginada | `*sql.DB` |
| `internal/user` | Colegas, presença e versão do usuário | `*sql.DB` |
| `internal/call` | Máquina de estados, comandos, histórico, notas | `*sql.DB`, publicador de eventos, store de usuário |
| `internal/dashboard` | Agregados do dia no fuso da empresa | `*sql.DB` |
| `internal/event` | Hub SSE em memória, heartbeat, fila por cliente | `Publisher` e `SnapshotLoader` |

`event` não importa `call` nem `user`. O `main` injeta o loader que lê chamadas ativas, presença e tombstones.

O contrato com o Angular é `api/openapi.yaml`, escrito à mão neste repositório.

## 4. Dados e persistência

PostgreSQL é a fonte da verdade. Timestamps em `timestamptz` (UTC). O dia do dashboard usa o fuso IANA da empresa. IDs são UUID gerados pela aplicação.

| Tabela | Extra além da especificação original | Restrições |
|---|---|---|
| `organizations` | — | uma linha no seed demo; `timezone` IANA; `brand_color` `#RRGGBB` |
| `users` | `presence_before_busy` | presença `available`, `busy` ou `offline`; ramal único na empresa; `version` inteira a partir de 1 |
| `contacts` | — | nome obrigatório; telefone E.164 (`+` e 8–15 dígitos, primeiro ≠ 0); e-mail opcional; telefone único por empresa |
| `calls` | — | `contact_id` nulo se o contato for apagado; snapshots de nome e telefone permanecem; `version` inteira a partir de 1 |
| `call_transitions` | — | append-only; `from_status` nulo só na criação |
| `call_notes` | — | um texto por chamada, no máximo 2.000 caracteres |

`presence_before_busy` guarda a presença anterior quando uma chamada não terminal força `busy`. Ao terminar, a presença volta e a coluna fica nula. Enquanto essa chamada existir, `PATCH /me/presence` responde `409` / `presence_locked`. `busy` manual, sem chamada, é permitido e é o valor restaurado se uma chamada começar depois.

Índice único parcial em `calls (owner_user_id)` onde o estado é `dialing`, `ringing` ou `active`. Transição valida estado, direção e `expectedVersion` na mesma transação da timeline. Nota só em chamada terminal, na transação que trava a linha da chamada.

Excluir contato: `ON DELETE SET NULL` em `calls.contact_id`. Chamadas, transições e notas não são apagadas.

Migrações: SQL numerado, só para frente, embutido no binário, aplicado na subida, registrado em `schema_migrations`. Seed demo idempotente com UUIDs fixos: 1 empresa, 1 usuário demo, alguns colegas e poucos contatos. Volume de laboratório (1.000 contatos e 10.000 chamadas históricas) só por `go run ./cmd/seedload`.

Antes de aceitar tráfego, chamadas não terminais de um processo anterior fecham como `failed` / `simulation_interrupted` e a presença é restaurada. `/health/live` não toca o banco. `/health/ready` só depois do ping, das migrações, do seed e dessa varredura.

## 5. Contrato HTTP

Recurso cujo UUID não existe, ou não pertence à empresa demo, responde `404`. O cliente nunca envia `organizationId` nem `userId`.

`GET /health/live` e `GET /health/ready` ficam fora de `/api/v1`. O restante usa prefixo `/api/v1` e JSON, exceto `GET /api/v1/events` (SSE).

Corpo máximo 16 KiB. Corpos JSON exigem `Content-Type: application/json`. Paginação: `{ items, total, page, pageSize }`. `page` começa em 1. `pageSize` padrão 20, máximo 100. Desempate por `id`.

### 5.1 Identidade e presença

- `GET /api/v1/me` — `200`. Usuário demo, empresa (`name`, `brandColor`, `timezone`) e `demoMode: true`.
- `GET /api/v1/users` — `200`. Colegas da empresa, incluindo o usuário demo: ramal, presença, `version`.
- `PATCH /api/v1/me/presence` — `200`. Corpo: `{ presence, expectedVersion }`. Versão velha: `409` / `version_mismatch`. Com chamada não terminal: `409` / `presence_locked`.

### 5.2 Contatos

- `GET /api/v1/contacts` — `200`. Query: `q` (nome ou telefone), `page`, `pageSize`, `sort=name`. Ordem: nome, depois `id`.
- `POST /api/v1/contacts` — `201`. Corpo: `name`, `phone`, `email` opcional.
- `PATCH /api/v1/contacts/{id}` — `200`. Parcial: campo omitido permanece; `email` nulo apaga o e-mail. Telefone ou e-mail inválido: `400` com `fieldErrors`. Telefone duplicado na empresa: `409` / `conflict`.
- `DELETE /api/v1/contacts/{id}` — `204`. Não apaga histórico.

### 5.3 Chamadas e dashboard

- `POST /api/v1/calls` — `201`. Corpo: `contactId` ou `phone`, nunca os dois. Sem contato, snapshot usa o número e o nome `Unknown`. Já existir não terminal: `409` / `active_call_exists`.
- `GET /api/v1/calls` — `200`. Query: `direction` (`inbound`|`outbound`), `status` (um estado do domínio), `terminal` (`true`|`false`), `from` e `to` em ISO-8601 UTC sobre `createdAt`. `status` e `terminal` se combinam com E; combinação impossível devolve lista vazia. Sem esses filtros, a lista é o histórico completo paginado. Ordem: `createdAt` descendente, depois `id`.
- `GET /api/v1/calls/{id}` — `200`. Chamada, timeline e nota se houver.
- `POST /api/v1/calls/{id}/actions` — `200`. Corpo: `{ action, expectedVersion }` com `answer`, `reject` ou `end`.
- `PUT /api/v1/calls/{id}/note` — `200`. Corpo: `{ text }`, só em chamada terminal; senão `409` / `invalid_transition`.
- `GET /api/v1/dashboard` — `200`. Dia civil no fuso da empresa: totais do dia, recebidas, realizadas, perdidas e ativas agora.

### 5.4 Demo

Rotas só registradas com `DEMO_MODE=true`:

- `POST /api/v1/demo/incoming-call` — `201`. Mesmo corpo que `POST /calls`.
- `POST /api/v1/demo/calls/{id}/connect` — `200`. Corpo: `{ expectedVersion }`.
- `POST /api/v1/demo/calls/{id}/fail` — `200`. Corpo: `{ expectedVersion, reason }`.

### 5.5 Erros e CORS

Envelope `{ code, message, fieldErrors?, requestId }`.

| HTTP | `code` | Quando |
|---|---|---|
| 400 | `validation_error` | entrada inválida, inclusive `reason` incompatível com a direção |
| 404 | `not_found` | recurso inexistente ou de outra empresa |
| 409 | `conflict` | telefone duplicado e demais conflitos sem código mais específico |
| 409 | `invalid_transition` | comando ilegal no estado/direção atuais |
| 409 | `version_mismatch` | `expectedVersion` diferente da versão persistida |
| 409 | `active_call_exists` | já há chamada não terminal do usuário demo |
| 409 | `presence_locked` | tentativa de mudar presença durante chamada não terminal |
| 500 | `internal_error` | falha inesperada, sem detalhe interno |

CORS só para `CORS_ORIGIN` (padrão `http://localhost:4200`): métodos usados, headers `Content-Type` e `X-Request-Id`. Sem cookies. Preflight `OPTIONS` nas rotas JSON. SSE é `GET` sem header customizado, com os mesmos headers CORS.

`X-Request-Id` válido (UUID) é reutilizado; senão a API gera um e devolve no response.

## 6. Estados e comandos de chamada

O backend aplica transição, timestamp e versão. Cada comando válido grava chamada e timeline na mesma transação, incrementa `version` em 1 e só então publica `call.updated` (e `presence.updated` se a presença mudar). Não existe endpoint para gravar status arbitrário.

`POST /calls` cria realizada em `dialing`. `POST /demo/incoming-call` cria recebida em `ringing`. Nos dois, presença vai para `busy` e a anterior fica em `presence_before_busy`.

`connect` só na realizada: `dialing → ringing`, depois `ringing → active`. Na recebida, quem atende é o usuário com `answer`.

| De (realizada) | answer / reject | end | connect | fail |
|---|---|---|---|---|
| dialing | inválido | ended | ringing | failed |
| ringing | inválido | ended | active | failed |
| active | inválido | ended | inválido | failed |

| De (recebida) | answer | reject | end | connect | fail |
|---|---|---|---|---|---|
| ringing | active | rejected | inválido | inválido | missed se `reason=ring_timeout`, senão failed |
| active | inválido | inválido | ended | inválido | failed |

`rejected` e `missed` só na recebida. Realizada sem resposta termina em `failed` / `no_answer`.

Motivos que o cliente pode enviar, e onde valem:

- `no_answer` — realizada em `dialing` ou `ringing` → `failed`
- `ring_timeout` — recebida em `ringing` → `missed`
- `network_error` — qualquer não terminal → `failed`
- `simulation_interrupted` — só a varredura de startup; no endpoint é `400` / `validation_error`

Qualquer outro par estado/direção/motivo é `400` / `validation_error`.

`startedAt` entra ao chegar em `active`. `endedAt` entra em qualquer terminal. Duração só existe se houve `active`.

Versão divergente: `409` / `version_mismatch`. Dois `answer` simultâneos: um commita; o outro perde na versão ou no índice único parcial.

Ao entrar em terminal, a presença volta ao valor guardado e `presence_before_busy` fica nulo. A varredura de startup usa a mesma regra, com motivo `simulation_interrupted`.

## 7. Tempo real (SSE)

Uma conexão `GET /api/v1/events`. Comandos continuam no REST. O hub vive em memória neste processo. Várias instâncias exigiriam outro fan-out; isso fica só documentado. Sem garantia de exactly-once e sem replay persistente.

Envelope: `{ eventId, type, occurredAt, entityId?, entityVersion?, payload }`. Tipos: `snapshot`, `call.updated`, `presence.updated`, `heartbeat`. `call.updated` e `presence.updated` levam a entidade inteira. `eventId` é único neste processo. Versão é monotônica por entidade, não ordem global.

Ordem na conexão: registrar o consumidor, ler o snapshot, enviar o `snapshot`, depois a fila acumulada. O snapshot substitui a coleção de chamadas ativas e a presença. Conexão substituída: o handler antigo é cancelado e seus envios são ignorados.

Payload do snapshot: `calls` (não terminais), `users` (presença completa) e `tombstones` `{ id, version, status }` das chamadas do usuário demo que terminaram nos últimos 15 minutos, no máximo 100, unidas a qualquer id com evento na fila daquela leitura. O cliente descarta evento cuja versão seja menor que a do snapshot ou do tombstone. Chamada terminal no stream sai da lista ativa.

Cada consumidor tem canal de 32 eventos. Publish não bloqueia os outros: fila cheia desconecta aquele cliente, que reconecta por snapshot. O `context` do request cancela a assinatura.

Heartbeat a cada 15 s. A regra de 45 s sem evento é do cliente Angular. `WriteTimeout` global curto não se aplica a esta rota; o flush tem deadline por evento. Headers: `text/event-stream`, sem cache, CORS da origem configurada.

Shutdown: parar de aceitar requests, fechar o hub, cancelar consumidores e esperar os handlers com timeout. Sem goroutine órfã do heartbeat nem do stream.

## 8. Ambiente e operação

Compose sobe só o PostgreSQL, com volume nomeado e healthcheck. A API corre no host. Versões exatas de Go, PostgreSQL e pgx entram na primeira etapa de implementação, consultando a documentação oficial e o README.

Configuração só por ambiente. Subida falha se faltar valor obrigatório. `.env.example` documenta o conjunto, sem segredos reais.

| Variável | Padrão | Função |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Bind da API |
| `DATABASE_URL` | — (obrigatória) | Postgres |
| `CORS_ORIGIN` | `http://localhost:4200` | Origem permitida, JSON e SSE |
| `DEMO_MODE` | `true` | Neste MVP precisa ser `true`; senão a subida falha |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

Timeouts HTTP: `ReadHeaderTimeout` cerca de 5 s. `ReadTimeout` e `WriteTimeout` globais não cobrem o SSE. Shutdown em `SIGINT`/`SIGTERM`: para de aceitar, fecha o hub, cancela streams, espera no máximo 10 s, fecha o pool.

Logs JSON no stdout: `requestId`, método, rota, status, duração. Sem telefone, e-mail, corpo de nota ou `DATABASE_URL`.

## 9. Testes

Apenas testes Go, com `testing` da stdlib. Sem framework de assert. Integração usa PostgreSQL real. `go test -race` no hub e nos caminhos concorrentes.

**Domínio (sem banco).** Tabela de transições por direção, motivos aceitos e rejeitados, incremento de versão, recusa de `simulation_interrupted` no endpoint.

**Integração SQL/HTTP.**

- Validação de contato (E.164, e-mail, telefone duplicado → `409`).
- Excluir contato deixa `contact_id` nulo e preserva snapshots e nota.
- Duas criações de chamada do mesmo usuário: uma `201`, outra `409` / `active_call_exists`.
- Dois `answer` na mesma recebida: uma transição, um atendimento.
- `end` com `expectedVersion` velha: `409` / `version_mismatch`; estado inalterado.
- Nota em não terminal recusada; em terminal, upsert.
- Restart: `ringing` vira `failed` / `simulation_interrupted`; `/ready` só depois.
- Dashboard: dia no fuso da empresa; timestamps UTC no banco.

**Hub.** Registrar, publicar durante a leitura do snapshot, enviar snapshot e depois a fila. Tombstone impede ressuscitar chamada terminal. Fila de 32 desliga o cliente lento. Cancelar o `context` remove o consumidor. Heartbeat não vaza goroutine no shutdown.

## 10. Entrega pedagógica

Quando a implementação começar, executar só a etapa pedida, com no máximo dois conceitos centrais por etapa. Não antecipar infra ou features de etapas futuras. Explicações em português; código, identificadores e mensagens da aplicação em inglês. Atualizar `docs/progress.md` e ADRs curtos conforme a especificação original.

Etapas de backend na sequência original, a adaptar a este repositório (sem Angular): 01 (versões), 07 (primeiro servidor), 08 (handlers), 09 (PostgreSQL), 10 (migrations/seed), 11 (contatos leitura), 12 (OpenAPI), 16 (escritas), 17–21 (chamadas, histórico, dashboard), 22–23 e 26 (SSE), 27 (integração), 31 (shutdown). A numeração original permanece para cruzar com o tutorial do outro repositório.

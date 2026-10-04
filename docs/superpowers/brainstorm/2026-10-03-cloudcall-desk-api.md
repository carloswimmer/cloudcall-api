# Brainstorm em pausa: API CloudCall Desk

Checkpoint de 2026-10-03. O desenho não está fechado. Não escrever a spec final, não implementar e não abrir a próxima seção até o usuário retomar e responder o que ficou pendente.

## Objetivo

Criar neste repositório a API do CloudCall Desk em Go, seguindo `NFON-Projeto-e-Tutorial.md`.

Este repositório fica com tudo que é servidor: API, PostgreSQL, migrações, seed, Docker e o contrato OpenAPI. Angular, NgRx, interface, E2E de tela e acessibilidade ficam em outro repositório.

O produto é um painel local de comunicação empresarial, com chamadas simuladas. Não há áudio, SIP, WebRTC, cobrança nem números reais. O aluno é sênior em React/TypeScript e iniciante em Angular e Go. A implementação, quando começar, continua incremental pelo contrato pedagógico do documento. Este brainstorm desenha a API inteira antes disso.

## Requisitos discutidos

Recorte pedido pelo usuário:

- Backend completo dos marcos que cabem aqui: contatos, presença, chamadas, histórico, notas, dashboard, SSE, testes Go, Docker/PostgreSQL e OpenAPI publicado para o outro repositório.
- Sem proxy de desenvolvimento. O `ng serve` e o processo Go ficam em portas diferentes. CORS só para a origem explícita do Angular, inclusive no SSE.
- PostgreSQL por decisão do projeto de estudo, sem afirmar que é o banco da NFON.

Herdado da especificação e aceito como base deste recorte:

- Prefixo `/api/v1`. Identidade e empresa demo escolhidas pelo servidor.
- Sem autenticação no MVP. Modo demonstração explícito.
- Go, `net/http`, `encoding/json`, `database/sql` e driver pgx. Sem ORM, sem framework HTTP extra, sem Redis, broker, microserviços ou Kubernetes.
- Uma instância. Hub SSE em memória. PostgreSQL é a fonte da verdade.
- Erros `{ code, message, fieldErrors?, requestId }`, com 400, 404, 409 e 500 sem detalhe interno.
- Paginação `{ items, total, page, pageSize }`, limite de 100, desempate por ID.
- SSE: uma conexão por instância da aplicação cliente; envelope com `eventId`, `type`, `occurredAt`, `entityId?`, `entityVersion?`, `payload`; tipos `snapshot`, `call.updated`, `presence.updated`, `heartbeat`.

## Decisões tomadas

O usuário escolheu o recorte completo da API (opção A) e CORS na origem explícita, sem proxy (opção B).

Abordagem aprovada: monólito modular por domínio. Camadas handler/serviço/repositório desde o primeiro endpoint e um pacote único `internal/api` foram descartados.

Transições de chamada no MVP são só por request: ações do usuário e rotas demo. Sem timer em background. `no_answer` e timeout de toque entram como motivo explícito no endpoint de falha simulada.

### Arquitetura aprovada

Um processo Go (`cmd/api`) e PostgreSQL no Docker Compose. A API roda no host com `go run`. Imagem da API fora deste desenho.

No startup o servidor escolhe a empresa e o usuário demo a partir do seed. Nenhum handler aceita `organizationId` ou `userId` do cliente como autorização. O SQL de recurso filtra pela empresa demo. Multi-tenant completo fica fora do MVP; a restrição fica explícita para uma extensão futura.

| Pacote | Função | Depende de |
|---|---|---|
| `cmd/api` | Liga config, banco, rotas, hub e shutdown | pacotes abaixo |
| `internal/platform/config` | Lê ambiente e falha cedo se faltar valor obrigatório | nada de domínio |
| `internal/platform/httpx` | `net/http`, CORS, `requestId`, log, limite de body, formato de erro | config |
| `internal/platform/db` | Pool `database/sql` + pgx, migrações e seed | config |
| `internal/contact` | Validação, CRUD e busca paginada | `*sql.DB` |
| `internal/user` | Colegas, presença e versão do usuário | `*sql.DB` |
| `internal/call` | Máquina de estados, comandos, histórico, notas | `*sql.DB`, publicador de eventos, store de usuário |
| `internal/dashboard` | Agregados do dia no fuso da empresa | `*sql.DB` |
| `internal/event` | Hub SSE em memória, heartbeat, fila por cliente | loader de snapshot injetado |

Handlers começam no pacote do domínio. Serviço e store aparecem quando há regra ou SQL real. Interface só em `internal/event`: `Publisher` e `SnapshotLoader`. O `main` injeta a leitura de chamadas e presença. `event` não importa `call` nem `user`.

`CORS_ORIGIN` tem padrão `http://localhost:4200` e vale para JSON e SSE. Modo demo é configuração obrigatória neste MVP. Rotas `/api/v1/demo/...` só são registradas com esse modo ligado.

Contrato com o Angular: `api/openapi.yaml`, escrito à mão neste repositório. Tipos TypeScript são gerados no outro repositório.

## Seção apresentada e ainda sem aprovação

Dados e persistência. Não avançar para HTTP, máquina de estados, SSE, shutdown ou testes antes do usuário confirmar ou ajustar esta seção.

PostgreSQL é a fonte da verdade. Timestamps em `timestamptz` (UTC). O dia do dashboard usa o fuso IANA gravado na empresa. IDs são UUID gerados pela aplicação.

| Tabela | Além da especificação | Restrições |
|---|---|---|
| `organizations` | — | uma linha no seed demo; `timezone` válido; `brand_color` em `#RRGGBB` |
| `users` | `presence_before_busy` | presença `available`, `busy` ou `offline`; ramal único na empresa; `version` inteira a partir de 1 |
| `contacts` | — | nome obrigatório; telefone E.164; e-mail opcional; telefone único por empresa |
| `calls` | — | `contact_id` nulo se o contato for apagado; snapshots permanecem; `version` inteira |
| `call_transitions` | — | append-only; `from_status` nulo só na criação |
| `call_notes` | — | um texto por chamada, no máximo 2.000 caracteres |

`presence_before_busy` guarda a presença anterior quando uma chamada não terminal força `busy`. Ao terminar, a presença volta e a coluna fica nula. Enquanto essa chamada existir, `PATCH` de presença responde `409`. `busy` manual, sem chamada, continua permitido e é o valor restaurado se uma chamada começar depois.

Índice único parcial em `calls (owner_user_id)` onde o estado é `dialing`, `ringing` ou `active`. A transição valida estado, direção e `expectedVersion` na mesma transação da timeline. Nota só em chamada terminal, na transação que trava a linha da chamada.

Excluir contato faz `ON DELETE SET NULL` em `calls.contact_id`. Chamadas e notas não são apagadas.

Migrações: SQL numerado, só para frente, embutido no binário, aplicado na subida, registrado em `schema_migrations`. Seed demo idempotente com UUIDs fixos: 1 empresa, 1 usuário demo, alguns colegas e poucos contatos. Volume de laboratório (1.000 contatos e 10.000 chamadas históricas) só por comando explícito, fora da subida normal.

Antes de aceitar tráfego, chamadas não terminais de um processo anterior fecham como `failed` / `simulation_interrupted` e a presença é restaurada. `/health/live` não toca o banco. `/health/ready` responde pronto depois do ping, das migrações, do seed e dessa varredura.

## Dúvidas em aberto

Nenhuma pergunta foi deixada sem resposta pelo usuário. O que falta é desenho ainda não apresentado:

- Contrato HTTP: rotas, filtros, paginação, corpos, CORS detalhado e erros.
- Máquina de estados por direção, motivos de falha e comandos demo.
- Fluxo SSE: snapshot, versões, tombstone, heartbeat, cliente lento e shutdown.
- Ambiente: variáveis, logs, timeouts e encerramento gracioso.
- Testes Go deste repositório e o que fica no repositório Angular.
- Versões exatas de Go, PostgreSQL e pgx. Ficam para a etapa de ambiente, consultando a documentação oficial.
- O repositório ainda não tem git. O commit da spec final só depois da aprovação do desenho completo.

Companion visual ainda não foi oferecido. Só oferecer se uma seção futura for mais clara vista do que lida.

## Próximo passo

Retomar perguntando se a seção **Dados e persistência** está certa. Só depois da resposta apresentar a seção seguinte. Não escrever `docs/superpowers/specs/` nem partir para o plano de implementação neste momento.

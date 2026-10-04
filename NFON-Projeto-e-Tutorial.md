# Projeto de estudo: CloudCall Desk

## 1. Objetivo e contexto

Construir um painel de comunicação empresarial para praticar Angular e Go e preparar uma entrevista de Frontend Engineer na NFON.

O aluno é desenvolvedor sênior em React e TypeScript, mas iniciante em Angular e Go. O tutorial deve aproveitar seu conhecimento de arquitetura, APIs e testes, explicando os mecanismos específicos dessas duas tecnologias.

A descrição da vaga fornecida menciona TypeScript, Angular, NgRx ou tecnologias comparáveis, arquitetura, testes, confiabilidade, desenvolvimento assistido por IA e possibilidade de backend em Go. Ela não identifica o banco de dados. Portanto, usar PostgreSQL por decisão deste projeto, sem afirmar que é o banco utilizado pela NFON.

Os anexos sobre Trading 212 não são requisitos deste projeto. O domínio é inspirado na comunicação empresarial em nuvem da NFON; não reproduz sua arquitetura interna nem integra APIs privadas da empresa.

## 2. Produto e limites

CloudCall Desk é um painel usado por colaboradores de uma empresa para consultar colegas e contatos, controlar chamadas simuladas, acompanhar presença e visualizar histórico.

As chamadas são simulações de estados e eventos geradas pelo backend. Não há áudio, números reais, cobrança, SIP ou WebRTC no escopo obrigatório. Essa decisão mantém o foco no aprendizado de frontend, backend e comunicação em tempo real.

O MVP funciona localmente com uma empresa e um usuário de demonstração predefinidos no servidor. Não implementar autenticação nesse estágio. A aplicação deve informar que opera em modo de demonstração; não deve ser apresentada como pronta para exposição pública.

### Marcos

1. **Fundação:** Angular, API Go, PostgreSQL e contatos persistidos.
2. **MVP funcional:** chamadas simuladas, histórico e presença via REST.
3. **Tempo real:** SSE, estado compartilhado, reconexão e concorrência.
4. **Preparação para entrevista:** testes, desempenho, decisões e exercícios comparativos.

Extensões opcionais são feitas somente depois desses marcos, mediante pedido do aluno.

## 3. Stack e regras de atualização

| Camada | Escolha | Finalidade |
|---|---|---|
| Frontend | Angular e TypeScript strict | Aplicação principal |
| UI | Componentes standalone, templates modernos, CSS simples | Aprender Angular sem construir um design system completo |
| Rotas e HTTP | Angular Router e HttpClient | Navegação e integração |
| Estado local | Signals e computed | UI e valores derivados |
| Fluxos assíncronos | RxJS | Busca, cancelamento e eventos |
| Estado compartilhado | NgRx SignalStore | Chamadas ativas e presença |
| Formulários | Reactive Forms tipados e estáveis | Cadastro e edição de contatos |
| Backend | Go, net/http, encoding/json | API e streaming |
| Banco | PostgreSQL, database/sql e driver pgx compatível | Persistência e SQL explícito |
| Contrato | OpenAPI e tipos TypeScript gerados | Contrato entre linguagens |
| Ambiente | Docker Compose para PostgreSQL | Execução local reproduzível |
| Testes | Runner suportado pelo Angular CLI escolhido, Go testing e Playwright | Unidade, integração e E2E |

Na primeira etapa, consultar documentação oficial, selecionar versões estáveis e suportadas, verificar compatibilidade Angular/Node/TypeScript/RxJS/NgRx e registrar versões exatas e comandos. Fixar dependências e lockfile. Não usar versões RC ou experimentais como fundamento.

Usar APIs públicas estáveis: standalone, inject, signals, computed, input/output e controle de fluxo @if/@for quando compatíveis com as versões escolhidas. Usar a configuração de change detection recomendada pela versão escolhida e estudar OnPush explicitamente. Não adicionar Zone.js automaticamente se o scaffold já utilizar execução zoneless.

NgModules, decorators de inputs tradicionais e aplicações baseadas em Zone.js podem aparecer numa explicação de código legado, sem serem impostos ao projeto. Não declarar que toda API antiga está deprecated: verificar individualmente.

Evitar ORM, microserviços, Kubernetes, Redis, broker e framework HTTP adicional no MVP. Não implementar frontend React completo: as comparações são exemplos curtos ou exercícios isolados.

## 4. Requisitos funcionais

### RF01 — Shell e navegação

- Rotas: `/dashboard`, `/contacts`, `/calls`, `/calls/:id` e `/settings`.
- Menu com rota ativa, página não encontrada e carregamento de features sob demanda.
- Exibir empresa, usuário demo e estado da conexão de eventos.
- Estados de carregamento, vazio e erro devem ser distintos.

**Aceite:** navegar entre telas preserva chamadas ativas e não abre conexões SSE adicionais.

### RF02 — Contatos

- Listar contatos com busca por nome ou telefone, paginação e ordenação estável.
- Criar, editar e excluir contatos com confirmação de exclusão.
- Campos: nome obrigatório, telefone obrigatório e email opcional.
- Validar no frontend e novamente no backend. Para o projeto, telefone segue E.164: `+` e entre 8 e 15 dígitos, primeiro dígito diferente de zero.
- Telefone único dentro da empresa. Exibir erros de campo retornados pela API.
- Busca com debounce de aproximadamente 300 ms e cancelamento de solicitações substituídas.
- Filtro/página ficam na URL para permitir reload e compartilhamento do estado de navegação.

**Aceite:** buscar A e logo depois B nunca permite que uma resposta atrasada de A substitua B. Exclusão não apaga os registros históricos de chamadas.

### RF03 — Colegas e presença

- Mostrar colegas com ramal e presença `available`, `busy` ou `offline`.
- Disponibilizar controles de simulação identificados como ferramentas demo.
- Presença é informação do domínio controlada pelo backend, não o mesmo que conexão SSE do navegador.
- Uma chamada ativa torna o usuário demo `busy`; ao finalizar, sua presença anterior é restaurada.

**Aceite:** alterar presença no simulador atualiza dois navegadores sem reload na etapa de tempo real.

### RF04 — Chamadas simuladas

- Discar para um contato ou telefone informado.
- Simular chamada recebida de um contato conhecido ou número desconhecido.
- Atender, rejeitar e encerrar, conforme estado atual.
- Mostrar direção, interlocutor, estado e duração.
- Há no máximo uma chamada não terminal por usuário demo. Uma nova tentativa nesse período retorna `409 Conflict`.
- O backend decide transições e timestamps. O frontend apresenta ações permitidas, mas não substitui a validação do servidor.
- Uma chamada perdida ou falha deve ter causa explícita, como timeout de toque ou falha simulada.
- Temporizador visual usa `startedAt` do servidor; não grava nem transmite um evento por segundo.

**Aceite:** dois clientes tentando atender a mesma chamada simultaneamente não produzem duas transições ou dois registros de atendimento.

### RF05 — Histórico e detalhe

- Listar chamadas com filtros por direção, estado terminal e período, com paginação.
- Exibir duração apenas quando a chamada chegou a `active`.
- Detalhe com timeline de transições, timestamps e notas.
- Notas são texto simples, máximo 2.000 caracteres, e podem ser salvas somente em chamadas terminais.
- Preservar nome/número do interlocutor no histórico mesmo após edição ou exclusão do contato.

**Aceite:** reload e reinício do backend preservam contatos, notas e histórico.

### RF06 — Dashboard

- Mostrar chamadas do dia, recebidas, realizadas, perdidas e chamadas ativas.
- Definir o dia pelo fuso configurado da empresa; persistir timestamps em UTC.
- Métricas históricas são calculadas pelo backend. Após evento relevante, revalidar métricas com agrupamento de atualizações para evitar um request por evento.

**Aceite:** uma chamada encerrada aparece no histórico e atualiza os indicadores sem duplicação.

### RF07 — Tempo real e recuperação

- Comandos usam REST; eventos usam uma única conexão SSE por instância da aplicação.
- Mostrar `connecting`, `connected`, `reconnecting` e `offline` separadamente.
- EventSource pode reconectar automaticamente; a aplicação ainda precisa detectar ausência de heartbeat, sinalizar problema e reconciliar estado.
- Heartbeat aproximadamente a cada 15 s; após 45 s sem eventos/heartbeat, marcar conexão como degradada e reiniciar a conexão com espera limitada.
- Em cada conexão/reconexão, receber snapshot de chamadas não terminais e presença. Este projeto não exige replay persistente de todos os eventos.
- Ignorar versões antigas e eventos duplicados. Chamadas terminais recebidas devem sair da lista ativa.
- Durante perda de conexão, exibir que o estado pode estar desatualizado; a API continua validando comandos.

**Aceite:** desligar o backend, fazer mudanças após reiniciá-lo e reconectar recupera o estado atual sem manter chamada fantasma ou duplicar assinatura.

### RF08 — Configuração por empresa

- Exibir nome, cor de marca e timezone vindos da API.
- Aplicar marca via configuração e CSS variables, mantendo componentes compartilhados.
- Não construir um sistema multi-tenant completo no MVP. Registrar como autorização e isolamento seriam acrescentados depois.

**Aceite:** trocar configuração demo altera o shell sem duplicar componentes.

## 5. Modelo de domínio

| Entidade | Campos essenciais |
|---|---|
| Organization | id, name, brandColor, timezone |
| User | id, organizationId, name, extension, presence, version |
| Contact | id, organizationId, name, phone, email, createdAt, updatedAt |
| Call | id, organizationId, ownerUserId, contactId opcional, peerNameSnapshot, peerPhoneSnapshot, direction, status, version, createdAt, startedAt opcional, endedAt opcional, failureReason opcional |
| CallTransition | id, callId, fromStatus opcional, toStatus, occurredAt, reason opcional |
| CallNote | callId único, text, updatedAt |

Usar UUIDs, chaves estrangeiras, restrições, timestamps com timezone e índices orientados às consultas. Ao excluir contato, permitir `contactId = NULL`; preservar snapshots no registro da chamada.

### Estados e transições

| Estado | Próximos estados permitidos | Contexto |
|---|---|---|
| dialing | ringing, failed, ended | Chamada realizada aguardando conexão |
| ringing | active, rejected, missed, failed, ended | Recebida pode ser atendida/rejeitada; realizada pode ser conectada/cancelada |
| active | ended, failed | Chamada atendida |
| ended, rejected, missed, failed | nenhum | Estados terminais |

Regras adicionais dependem da direção: `rejected` e `missed` são resultados da chamada recebida no MVP. Chamada realizada sem resposta termina em `failed` com motivo `no_answer`.

Separar comandos do usuário de eventos do simulador. Por exemplo, usuário atende uma chamada recebida, enquanto o simulador conecta a chamada realizada. Não expor um endpoint que permita definir qualquer status arbitrariamente.

Validar estado atual, direção e versão em uma transação. Gravar chamada e timeline atomicamente; publicar evento apenas depois do commit. Aplicar exclusão concorrente por usuário, usando transação e restrição parcial de unicidade para chamadas não terminais.

No restart, chamadas não terminais de uma simulação anterior são finalizadas como `failed`, motivo `simulation_interrupted`, antes de aceitar tráfego. Essa simplificação é documentada; não representa recuperação de telefonia real.

## 6. API e contratos

Prefixo `/api/v1`. Identidade e empresa demo são escolhidas pelo servidor; não confiar em IDs arbitrários do cliente como autorização.

| Método e rota | Responsabilidade |
|---|---|
| GET /health/live | Processo está vivo |
| GET /health/ready | Banco e inicialização estão prontos |
| GET /api/v1/me | Usuário demo e configuração da empresa |
| GET /api/v1/contacts | Busca e paginação |
| POST /api/v1/contacts | Criar contato |
| PATCH /api/v1/contacts/{id} | Editar contato |
| DELETE /api/v1/contacts/{id} | Excluir contato |
| GET /api/v1/users | Colegas e presença |
| PATCH /api/v1/me/presence | Alterar presença demo permitida |
| POST /api/v1/calls | Iniciar chamada realizada |
| GET /api/v1/calls | Histórico filtrado/paginado; filtro de não terminais |
| GET /api/v1/calls/{id} | Chamada e timeline |
| POST /api/v1/calls/{id}/actions | Comando `answer`, `reject` ou `end`, com expectedVersion |
| PUT /api/v1/calls/{id}/note | Salvar nota |
| GET /api/v1/dashboard | Indicadores |
| GET /api/v1/events | Stream SSE com snapshot inicial |
| POST /api/v1/demo/incoming-call | Simular recebida |
| POST /api/v1/demo/calls/{id}/connect | Simular atendimento remoto |
| POST /api/v1/demo/calls/{id}/fail | Simular falha |

Rotas demo ficam habilitadas apenas em modo demo. Usar paginação `{ items, total, page, pageSize }`, limite de 100 itens e ordenação com desempate por ID. Documentar filtros e limites no OpenAPI.

Erros seguem `{ code, message, fieldErrors?, requestId }`. Usar 400 para entrada inválida, 404 para recurso inexistente, 409 para conflito de domínio/versão e 500 para falha inesperada sem revelar detalhes internos.

Gerar tipos TypeScript a partir do OpenAPI antes da integração real. Explicar que tipos gerados não validam JSON em runtime: validar payloads de eventos e respostas críticas na fronteira. Não usar `ts-rest` como se fornecesse contratos executáveis compartilhados entre Go e TypeScript.

### Protocolo SSE

Envelope: `{ eventId, type, occurredAt, entityId?, entityVersion?, payload }`.

Tipos: `snapshot`, `call.updated`, `presence.updated`, `heartbeat`.

`call.updated` e `presence.updated` carregam o estado completo da entidade, não patches dependentes de eventos anteriores. Isso simplifica deduplicação e recuperação. A versão é monotônica por entidade; não representa ordenação global.

No servidor, registrar o consumidor antes de ler o snapshot e enfileirar eventos durante a leitura. Enviar snapshot primeiro e depois eventos; o cliente compara versões. O snapshot substitui a coleção de chamadas ativas e a presença. Ignorar callbacks de conexões já substituídas.

O snapshot deve incluir versões de chamadas terminais relevantes à reconciliação ou manter um watermark/tombstone equivalente, evitando que um evento antigo ressuscite uma chamada encerrada durante a leitura. Explicar e testar essa corrida explicitamente.

Manter fila limitada por consumidor. Se um cliente ficar lento e a fila encher, desconectá-lo para recuperar por snapshot; não bloquear todos os clientes nem criar filas ilimitadas. Cancelar assinatura quando o request terminar. Não anunciar garantia de exactly-once.

## 7. Arquitetura e aprendizado obrigatório

### Angular

Organizar por features: contacts, calls, dashboard e settings. Shared contém apenas UI efetivamente reutilizada. Core concentra configuração e transporte compartilhados.

| Responsabilidade | Local recomendado | Comparação a explicar |
|---|---|---|
| Modal aberto, filtros temporários | Signal local | useState |
| Duração e totais derivados | computed | Derivação em render/useMemo; execução diferente |
| Formulário em edição | Reactive Forms | Formulários controlados/libraries React |
| Busca cancelável | RxJS e HttpClient | AbortController e query keys |
| Dados REST paginados | Serviço/estado da feature | Não equivale automaticamente a TanStack Query |
| Chamadas ativas e presença | NgRx SignalStore | Store compartilhada como Zustand/Redux |
| Dependências e escopo | inject e providers | Context/composição; sem equivalência exata |
| Recursos externos | DestroyRef/takeUntilDestroyed | Cleanup de effects; timing diferente |

Não copiar dados do store para outro signal via effect. Não usar effect para calcular valores que poderiam ser computed. NgRx não fornece automaticamente política de cache, stale time ou deduplicação de requests equivalente a TanStack Query.

Exercitar providers de root e de componente em um laboratório pequeno: provar quando existem instâncias compartilhadas ou separadas e corrigir a criação acidental de dois serviços de eventos.

Observar constructor, ngOnChanges, ngOnInit, ngAfterViewInit, afterNextRender e destruição num componente temporário. Explicar que ngOnInit ocorre antes da inicialização do template, enquanto useEffect pertence ao processo de commit do React; não tratá-los como equivalentes exatos. Para DOM, escolher a API apropriada ao propósito.

Praticar switchMap para busca cancelável e exhaustMap para impedir comandos duplicados enquanto um comando está em andamento, sem depender disso para garantir integridade no servidor. Explicar concatMap e mergeMap com cenários de contraste. Capturar erros sem matar permanentemente o stream de buscas.

Após o MVP, implementar um exercício pequeno com NgRx Store clássico: actions, reducer, selector e effect para uma lista de presença isolada. Comparar com SignalStore sem manter duas fontes de verdade na aplicação final.

### Go

Monólito modular com `cmd/api` e pacotes internos orientados a domínio. Começar com handlers simples; extrair serviço e repositório quando houver necessidade concreta.

Explicar structs, métodos, ponteiros, zero values, slices, maps, interfaces implícitas, erros e composição, comparando com TypeScript. Não criar uma interface para cada struct automaticamente.

Usar context em queries e operações canceláveis, pool de conexões, SQL parametrizado e transações. Diferenciar goroutine de Promise e concorrência de paralelismo. Ensinar channels e sincronização a partir do fan-out de eventos, sem introduzi-los em todo request.

O hub SSE fica em memória em uma única instância do backend. PostgreSQL continua sendo a fonte de verdade. Documentar que várias instâncias exigiriam distribuição de eventos e outra estratégia operacional.

Configurar timeouts HTTP adequados, respeitando a natureza longa do SSE; um timeout global curto de escrita não pode derrubar todos os streams. Implementar shutdown gracioso e interrupção dos consumidores sem goroutine leaks.

## 8. Requisitos não funcionais

| ID | Requisito | Verificação |
|---|---|---|
| RNF01 | Inicialização reproduzível e dados sintéticos | README, env.example, migrations e seed idempotente |
| RNF02 | Código strict e APIs estáveis | Build, typecheck, lint, gofmt e go vet |
| RNF03 | Integridade sob concorrência | Teste de dois comandos competindo e restrições no banco |
| RNF04 | Recuperação de rede | Cenário de disconnect/reconnect e snapshot consistente |
| RNF05 | Cleanup | Navegar 20 vezes sem crescimento de conexões SSE ou listeners |
| RNF06 | Acessibilidade | Fluxos principais por teclado, labels, foco e mensagens acessíveis; presença não depende só de cor |
| RNF07 | Responsividade | Uso em viewport de 375 px e desktop sem bloquear ações principais |
| RNF08 | Validação e limites | Corpo HTTP limitado, SQL parametrizado, erros sanitizados e notas como texto |
| RNF09 | Observabilidade | Logs estruturados com requestId, rota, status e duração; sem dados sensíveis |
| RNF10 | Performance local mensurável | 1.000 contatos e 10.000 chamadas seed; GETs paginados p95 alvo <300 ms em teste local |
| RNF11 | Atualização em tempo real | Latência evento→UI alvo <1 s com 10 clientes locais e carga de 10 eventos/s |
| RNF12 | Regressão | Testes significativos de domínio, integração SQL/HTTP e E2E principais |

Metas de performance são objetivos do laboratório, não SLA de produção. Registrar hardware, build, concorrência, duração do teste e resultados. Não inventar cumprimento sem medir. Não prometer alta disponibilidade com um único processo.

Servir frontend e API pelo mesmo origin no desenvolvimento via proxy, incluindo SSE. Se CORS for necessário, configurar origens explícitas. Não adicionar credenciais ou tokens reais ao repositório.

## 9. Testes mínimos de comportamento

- Transições válidas e inválidas, incluindo diferenças entre recebida e realizada.
- Comandos concorrentes para a mesma chamada e criação concorrente de chamadas do mesmo usuário.
- Transação não deixa timeline sem atualização de chamada, ou vice-versa.
- Busca A/B com respostas invertidas e falha seguida de nova busca bem-sucedida.
- Evento duplicado, versão antiga e evento antigo após estado terminal.
- Atualização durante snapshot e recuperação após conexão perdida.
- Exclusão de contato preserva histórico.
- Validação frontend/backend e tratamento de 409.
- Navegação encerra recursos de features sem encerrar o transporte compartilhado indevidamente.
- E2E: criar contato → iniciar → conectar → encerrar → consultar histórico.
- E2E: recebida → atender ou rejeitar; segundo cliente acompanha alterações.
- `go test -race` para hub e caminhos concorrentes, com testes relevantes, não apenas execução sem cobertura desses caminhos.

## 10. Contrato pedagógico para o agente do cursor

Você deve gerar e executar um tutorial progressivo usando esta especificação. Não entregar o projeto inteiro nem executar todas as etapas de uma vez.

### Modo de trabalho

1. Primeiro, apresentar o plano numerado e as versões escolhidas. Aguardar o pedido do aluno para executar a etapa 01.
2. Executar apenas a etapa solicitada. Ela deve introduzir no máximo dois conceitos centrais e ter uma verificação observável.
3. Se a etapa precisar de mais de aproximadamente cinco arquivos relevantes ou 30–45 minutos de estudo, dividi-la em subetapas antes de implementar.
4. Antes de mudar código, explicar objetivo, pré-requisitos, arquivos envolvidos e decisão técnica.
5. Implementar mudanças pequenas; comentar o motivo das escolhas, sem narrar cada linha trivial.
6. Depois, mostrar como executar, resultado esperado, verificação realizada e uma comparação curta com React/TypeScript quando pertinente.
7. Propor um exercício pequeno para o aluno e duas perguntas de compreensão. Encerrar e aguardar o próximo pedido.
8. Não antecipar infra, bibliotecas ou features de etapas futuras. Não esconder grandes alterações em uma etapa de setup.
9. Explicações em português; código, identificadores e mensagens da aplicação em inglês.
10. Preservar código escrito pelo aluno. Antes de refatorar, explicar problema concreto e alternativa.
11. Usar documentação oficial para verificar APIs e deprecações. Explicitar limitações das analogias React/Angular e TS/Go.
12. Atualizar `docs/progress.md` com etapa, conceitos, comandos de validação, resultado e pendências. Manter README e registrar decisões relevantes em ADRs curtos.

### Formato de cada etapa

- Objetivo e resultado observável.
- Conceitos Angular e/ou Go.
- Comparação com conhecimentos do aluno.
- Alterações delimitadas.
- Comandos e verificação.
- Exercício do aluno.
- Duas perguntas de compreensão.
- Resumo de uma frase que poderia ser usado numa entrevista.

## 11. Sequência sugerida do tutorial

Cada linha é uma etapa executável separadamente. Dividir as linhas mais amplas em A/B se necessário.

| Etapa | Entrega | Conceitos e verificação |
|---|---|---|
| 01 | Versões e ambiente | Compatibilidade; ferramentas respondem com versões registradas |
| 02 | Primeiro componente Angular | Standalone/template vs função/JSX; página estática funciona |
| 03 | Shell e rotas | Router e lazy loading; navegação e 404 |
| 04 | Signals e componente de presença | input/output, computed; eventos alteram UI sem backend |
| 05 | Laboratório de lifecycle | Ordem dos hooks e cleanup; logs previsíveis ao montar/desmontar |
| 06 | Laboratório de DI | Escopos; comparar identidade de instâncias root/componente |
| 07 | Primeiro servidor Go | Structs, funções, JSON, erros; health responde |
| 08 | Handlers e validação | Requests, status e limites; entradas inválidas falham corretamente |
| 09 | PostgreSQL local | Compose, pool e readiness; banco indisponível afeta readiness |
| 10 | Migrations e seed | SQL e constraints; seed repetido não duplica dados |
| 11 | Consulta de contatos em Go | database/sql, scan e context; consulta paginada funciona |
| 12 | Contrato OpenAPI | Schemas e tipos gerados; frontend compila com contrato |
| 13 | Lista real no Angular | HttpClient e loading/empty/error; tratar erro e retry manual |
| 14 | Busca e URL | debounce/switchMap; resposta antiga não substitui filtro novo |
| 15 | Formulário de contato | Reactive Forms; feedback local e erros da API |
| 16 | Escritas no backend | SQL parametrizado e unicidade; criar/editar/excluir |
| 17 | Domínio de chamadas puro em Go | Métodos e erros; testes da tabela de transições |
| 18 | Persistência de chamadas | Transação, versionamento e restrição de uma chamada ativa |
| 19 | Comandos e simulador REST | UI da chamada; rejeitar ações inválidas e duplo clique |
| 20 | Histórico e notas | Consultas/joins e duração; reload preserva dados |
| 21 | Dashboard e marca | Agregação e timezone; métricas e configuração corretas |
| 22 | Primeiro SSE em Go | Flush/context; heartbeat chega e disconnect cancela trabalho |
| 23 | Hub e concorrência | Goroutines/channels/sincronização; clientes lentos são isolados |
| 24 | Transporte Angular compartilhado | EventSource e DI root; uma conexão por aplicação |
| 25 | NgRx SignalStore | Entidades normalizadas e computed; chamada/presença compartilhadas |
| 26 | Snapshot e reconexão | Versões, duplicação e corridas; dois clientes convergem |
| 27 | Testes de integração | SQL e HTTP reais; conflitos preservam invariantes |
| 28 | Testes Angular e E2E | Busca, formulários e dois clientes; cenários críticos passam |
| 29 | Cleanup e desempenho | Profiler, change detection e timer local; medir metas |
| 30 | Laboratório NgRx clássico | Actions/reducer/effect/selector; comparar com SignalStore |
| 31 | Shutdown e qualidade | Cancelamento, logs e pipeline; checks e encerramento limpo |
| 32 | Defesa arquitetural | ADRs, limites e demonstração de 5 minutos para entrevista |

## 12. Exercícios de entrevista e conclusão

Ao final, o aluno deve conseguir explicar, usando exemplos do código:

1. Por que Signals não são uma tradução direta de useState e por que computed não é igual a useMemo.
2. Diferenças entre hooks de lifecycle e effects React, incluindo timing e cleanup.
3. Como o escopo de um provider pode criar estado separado ou conexões duplicadas.
4. Por que a busca usa switchMap, comandos podem usar exhaustMap e o servidor continua responsável pela concorrência.
5. Por que estado local, formulário, estado remoto e chamadas compartilhadas têm responsáveis diferentes.
6. Como NgRx clássico e SignalStore diferem, e quando um store seria desnecessário.
7. Por que SSE atende ao fluxo REST+eventos e quando WebSocket/WebRTC seriam necessários.
8. Como Go utiliza interfaces implícitas, context, transações e goroutines neste projeto.
9. Como o cliente recupera estado sem presumir que uma conexão nunca perde eventos.
10. Quais mudanças seriam necessárias para autenticação, múltiplas empresas, múltiplas instâncias e telefonia real.

**Definition of Done:** marcos obrigatórios executados, aplicação local demonstrável, critérios funcionais verificados, testes críticos passando, medidas registradas, documentação reproduzível e limitações explícitas. O objetivo é entendimento demonstrável, não volume de código gerado.

## 13. Extensões opcionais

- Autenticação com sessão HttpOnly, proteção CSRF adequada e autorização no backend; integração segura do SSE com sessão.
- Duas empresas e testes de isolamento por tenant.
- Transferência de chamada, expandindo a máquina de estados somente após definir regras.
- Resumo assistido por IA a partir de uma transcrição sintética, com provider fake padrão, timeout, cancelamento e revisão humana. Provider real apenas com configuração explícita.
- Componente React isolado para repetir o mesmo fluxo e comparar implementações.
- WebRTC somente em um módulo posterior, com sinalização e infraestrutura apropriadas.

## 14. Fontes oficiais para o tutorial

Consultar versões atuais ao iniciar. Fontes fundamentam domínio e mecanismos; não confirmam o stack interno da NFON além da vaga fornecida.

- NFON: https://corporate.nfon.com/en/
- Angular versões e suporte: https://angular.dev/reference/releases
- Angular componentes: https://angular.dev/guide/components
- Angular lifecycle: https://angular.dev/guide/components/lifecycle
- Angular Signals: https://angular.dev/guide/signals
- Angular zoneless: https://angular.dev/guide/zoneless
- Angular cleanup RxJS: https://angular.dev/ecosystem/rxjs-interop/take-until-destroyed
- NgRx SignalStore: https://ngrx.io/guide/signals
- Go SQL: https://go.dev/doc/tutorial/database-access
- Go versões: https://go.dev/doc/devel/release
- PostgreSQL suporte: https://www.postgresql.org/support/versioning/

## 15. Prompt para iniciar o cursor/agente

> Use este documento como especificação de um projeto didático para um desenvolvedor sênior React/TypeScript que está aprendendo Angular e Go para uma entrevista da NFON. Gere um tutorial incremental seguindo o contrato pedagógico e os requisitos acima. Primeiro apresente apenas o plano, eventuais subdivisões e as versões estáveis compatíveis escolhidas. Não implemente o projeto inteiro. Quando eu pedir uma etapa, execute somente essa etapa, explique decisões e diferenças em relação a React/TypeScript, verifique o resultado e pare. Comece pela apresentação do plano.

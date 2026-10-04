# Progress

## Task 1 — versions
- Conceitos: módulos Go, versões suportadas de Go/PostgreSQL
- Comandos: `go version`, `go mod init cloudcall`
- Resultado: módulo criado, versões registradas
- Pendente: servidor HTTP

## Task 2 — live health
- Conceitos: `http.ServeMux` com padrões de método (`GET /health/live`), `httptest` para testes de integração HTTP
- Comandos: `go test ./internal/app`, `go run ./cmd/api`, `curl localhost:8080/health/live`
- Resultado: `GET /health/live` retorna `200` e `{"status":"live"}` via `app.NewLive().Handler()`
- Pendente: probe ready, middleware, construtor completo `New`

## Task 3 — config, CORS, erros, request ID, limite de corpo
- Conceitos: configuração por variáveis de ambiente com falha rápida na inicialização (`DATABASE_URL` obrigatório; `DEMO_MODE` só aceita `true`), middleware como `func(http.Handler) http.Handler`, `context.WithValue` para propagar o request ID, `http.MaxBytesReader` para limitar o corpo, CORS com origem única, `http.Server` com `ReadHeaderTimeout` (sem `WriteTimeout` global, por causa do SSE futuro)
- Comandos: `go test ./internal/platform/config -count=1`, `go test ./internal/platform/httpx -count=1`, `go get github.com/google/uuid`, `go test ./... && go vet ./...`
- Resultado: `config.Load`, `httpx.Middleware/WriteJSON/WriteError/RequestID/MaxBodyBytes` e `App.WithMiddleware` funcionando; `main` carrega a config e sobe o servidor com o middleware
- Pendente: construtor `New(ctx, cfg)`, PostgreSQL, `/health/ready`, rotas de domínio

## Task 4 — PostgreSQL Compose e probe ready
- Conceitos: `database/sql` com driver pgx (`pgx/v5/stdlib`), pool de conexões (`SetMaxOpenConns`, `SetMaxIdleConns`, `SetConnMaxLifetime`), `PingContext` para verificar o banco, construtor `New(ctx, cfg)` que abre o pool e `Shutdown` que o fecha, testes de integração que pulam sem `DATABASE_URL`
- Comandos: `go get github.com/jackc/pgx/v5/stdlib`, `docker compose up -d`, `docker compose ps`, `go test ./internal/app ./internal/platform/db -count=1`, `curl localhost:8080/health/ready`
- Resultado: `db.Open`, `app.New`, `App.Ready`, `App.Shutdown` e `GET /health/ready` (200 `{"status":"ready"}` ou 503 `database unavailable` sem vazar a URL); `main` usa `New`; pgx v5.11.0
- Pendente: migrações, seed, rotas de domínio, desligamento gracioso (Task 16)

## Task 5 — migrações e seed demo (organização + usuários)
- Conceitos: migrações SQL embutidas com `//go:embed`, uma transação por arquivo, tabela `schema_migrations`, `pg_advisory_xact_lock` para serializar migradores concorrentes, seed idempotente com `INSERT … ON CONFLICT (id) DO NOTHING` (não reseta presença existente), `New` falha antes de escutar se migrate/seed falhar
- Comandos: `go test ./internal/platform/db -count=1`, `DATABASE_URL=… go test ./...`, `go vet ./...`
- Resultado: `db.Migrate`, `db.Seed`, IDs exportados (`OrganizationID`, `DemoUserID`, `Colleague1ID`, `Colleague2ID`, `Contact1ID` reservado); org Northwind e 3 usuários (Alex Rivera 100, Jordan Lee 101, Sam Okafor 102)
- Pendente: rotas de usuários (Task 6), tabela de contatos (Task 7)

## Task 6 — eu, colegas e presença
- Conceitos: `PATCH` com concorrência otimista (`UPDATE … WHERE version = $n RETURNING`, zero linhas = conflito 409 `version_mismatch`), erros de domínio (`ErrVersionMismatch`, `ErrNotFound`) mapeados para HTTP no handler, `json:"-"` para não vazar campos internos, o servidor usa os IDs do demo (o cliente nunca envia `userId`/`organizationId`), validação com `fieldErrors`
- Comandos: `DATABASE_URL=… go test ./internal/user ./internal/app -count=1`, `go test ./... && go vet ./...`
- Resultado: `GET /api/v1/me`, `GET /api/v1/users` e `PATCH /api/v1/me/presence` montados em `App.New`; `busy` sem chamada é permitido até a Task 11
- Pendente: contatos (Task 7), chamadas, SSE, trava de presença durante chamada (Task 11)

## Task 7 — lista de contatos
- Conceitos: migração `002_contacts.sql` (UNIQUE `(organization_id, phone)` e índice por `(organization_id, name, id)`), validação E.164 (`+` e 8–15 dígitos, sem zero inicial), e-mail opcional, struct genérica `Page[T]` (generics), paginação com `LIMIT/OFFSET` e `COUNT(*)`, busca `ILIKE` por nome ou telefone, `sort` só aceita `name`, erros 400 com `fieldErrors`, `items` sempre `[]` (nunca `null`)
- Comandos: `go test ./internal/contact -count=1`, `DATABASE_URL=… go test ./... -count=1`, `go vet ./...`
- Resultado: `GET /api/v1/contacts` (`q`, `page`, `pageSize` padrão 20 e máximo 100, `sort=name`) montado em `App.New`; seed insere `Contact1ID` (Ada Lovelace); o cliente nunca envia `organizationId`
- Pendente: criar, editar e remover contatos (Task 9)

## Task 8 — OpenAPI da superfície atual
- Conceitos: OpenAPI 3.1 como contrato HTTP para geração de tipos no Angular, schemas reutilizáveis (`Error`, `Me`, `User`, `Organization`, `Contact`, `PageContacts`), envelope de erro `{ code, message, fieldErrors?, requestId }`, paginação `{ items, total, page, pageSize }`
- Comandos: `ruby -ryaml -e "YAML.load_file('api/openapi.yaml')"`
- Resultado: `api/openapi.yaml` documenta `GET /health/live`, `GET /health/ready`, `GET /api/v1/me`, `GET /api/v1/users`, `PATCH /api/v1/me/presence` e `GET /api/v1/contacts` (query `q`, `page`, `pageSize`, `sort`); rotas futuras (escrita de contatos, chamadas, dashboard, SSE, demo) citadas na descrição, sem paths inventados
- Pendente: mutações de contatos (Task 9) e extensão do OpenAPI nas tasks seguintes

## Task 9 — criar, editar e remover contatos
- Conceitos: `INSERT/UPDATE/DELETE … RETURNING` parametrizados e limitados por `organization_id`, `PATCH` parcial com `CASE WHEN $n THEN … ELSE coluna END`, diferença entre campo ausente e `null` em JSON (tipo com `UnmarshalJSON` que registra `Set`), violação de unicidade detectada com `errors.As` para `*pgconn.PgError` (código `23505`) e mapeada para erro de domínio `ErrPhoneTaken` → 409 `conflict`, `PathValue("id")` do `ServeMux`, 204 sem corpo
- Comandos: `DATABASE_URL=… go test ./internal/contact -count=1`, `go test ./... && go vet ./...`
- Resultado: `POST /api/v1/contacts` (201), `PATCH /api/v1/contacts/{id}` (200; `email: null` limpa o e-mail) e `DELETE /api/v1/contacts/{id}` (204); `id` desconhecido ou malformado → 404 `not_found`; OpenAPI atualizado
- Pendente: chamadas, dashboard e SSE

## Task 10 — máquina de estados de chamada (pura)
- Conceitos: tabela de transições em Go puro (sem SQL/HTTP) com `switch` explícito por direção, tipos string nomeados como enums (`Status`, `Action`, `Reason`), funções puras que devolvem nova struct por valor (a original não muda), sentinelas de erro comparadas com `errors.Is`, concorrência otimista via `ExpectedVersion`, ponteiros para campos opcionais (`StartedAt`, `EndedAt`, `FailureReason`), testes em tabela com `t.Run` (uma linha por célula das grades de entrada/saída + motivos inválidos + estados terminais)
- Comandos: `go test ./internal/call -count=1`, `go test ./... && go vet ./...`
- Resultado: `call.Next(c, cmd, now)` devolve a nova chamada (versão +1, `startedAt` ao entrar em `active`, `endedAt` ao entrar em estado terminal) e o `Transition`; erros `ErrVersionMismatch`, `ErrInvalidTransition`, `ErrValidation`
- Pendente: persistência (store), rotas HTTP de chamadas e trava de presença (Task 11)

## Task 11 — persistência de chamadas, uma ativa por dono, trava de presença e varredura
- Conceitos: migração `003_calls.sql` com índice único parcial (`WHERE status IN ('dialing','ringing','active')`) que garante uma chamada ativa por dono direto no banco, violação `23505` mapeada para `ErrActiveCallExists`, transações (`*sql.Tx`) compartilhadas entre `call.Store` e `user.Store.MarkBusy/RestorePresence`, `SELECT … FOR UPDATE` para serializar `Apply`, `presence_before_busy` com `COALESCE` para lembrar a presença anterior, interface pequena `CallLock` no pacote `user` para evitar import circular (`call` importa `user`), varredura de inicialização escrita no store (não via `Next`, que rejeita `simulation_interrupted`)
- Comandos: `DATABASE_URL=… go test ./... -count=1`, `go test -race ./internal/call`, `go vet ./...`
- Resultado: `call.Store` com `Create`, `Apply`, `Get`, `HasNonTerminal` e `SweepInterrupted`; `PATCH /api/v1/me/presence` devolve 409 `presence_locked` durante chamada; `app.New` varre chamadas interrompidas após o seed
- Pendente: rotas HTTP de chamadas (Task 12), notas (Task 13), SSE (Task 15)

## Task 12 — comandos de chamada e rotas do simulador demo
- Conceitos: handler HTTP com dependência por interface pequena (`ContactFinder`, satisfeita por `*contact.Store`), validação "exatamente um de" (`contactId` ou `phone`) antes de tocar o banco, verificação do contato na organização antes do `INSERT` para o cliente nunca ver erro bruto de chave estrangeira (`23503` ainda mapeado para 404 se o contato sumir entre a consulta e o insert), snapshot de nome e telefone na criação, DTO de resposta separado da struct de domínio (`json` tags só na borda), mapeamento de erros de domínio com `errors.Is` (`ErrActiveCallExists`/`ErrVersionMismatch`/`ErrInvalidTransition` → 409, `ErrValidation` → 400, `ErrNotFound` → 404), rotas do simulador registradas à parte (`RegisterDemo`) só quando `cfg.DemoMode`
- Comandos: `DATABASE_URL=… go test ./... -count=1`, `go vet ./...`, `ruby -ryaml -e "YAML.load_file('api/openapi.yaml')"`
- Resultado: `POST /api/v1/calls` (201 `dialing`), `POST /api/v1/calls/{id}/actions` (`answer|reject|end`), `POST /api/v1/demo/incoming-call` (201 `ringing`), `POST /api/v1/demo/calls/{id}/connect` e `/fail`; contato desconhecido → 404 `not_found`; `GET /calls`, notas, dashboard e SSE seguem fora; OpenAPI atualizado com `Call`, `CreateCall` e respostas reutilizáveis
- Pendente: histórico, detalhe e notas (Task 13), dashboard, SSE (Task 15)

## Task 13 — histórico, detalhe, notas e invariante de remoção de contato
- Conceitos: listagem com filtros opcionais combinados por `AND` em um único SQL (`$n::tipo IS NULL OR coluna = $n`, parâmetros `sql.Null*`), ordenação estável `created_at DESC, id DESC`, `from`/`to` em RFC 3339 normalizados para UTC e inclusivos, combinação impossível (`status=ended&terminal=false`) devolve página vazia e não 400, paginação reaproveitando `contact.ParsePage` (sem overflow de `OFFSET`), upsert de nota com `INSERT … SELECT … WHERE status terminal … ON CONFLICT DO UPDATE` (a checagem de estado e a escrita são um só comando), `ErrInvalidTransition` → 409 para nota em chamada não terminal, limite de 2000 caracteres contado em runes (igual ao `char_length` do banco), `durationSeconds` calculado na borda HTTP com `EndedAt.Sub(*StartedAt) / time.Second` e `omitempty` em ponteiro, `ON DELETE SET NULL` mantém o histórico: apagar o contato zera `contactId` mas preserva `peerName`/`peerPhone`
- Comandos: `DATABASE_URL=… go test ./... -count=1`, `go vet ./...`, `ruby -ryaml -e "YAML.load_file('api/openapi.yaml')"`
- Resultado: `GET /api/v1/calls` (filtros `direction`, `status`, `terminal`, `from`, `to`, `page`, `pageSize`), `GET /api/v1/calls/{id}` com `transitions` e `note`, `PUT /api/v1/calls/{id}/note`; OpenAPI atualizado
- Pendente: dashboard (Task 14), SSE (Task 15)

## Task 14 — dashboard do dia no fuso da organização
- Conceitos: uma única consulta SQL que une a organização (fuso `timezone` lido da própria linha) a `calls` com `LEFT JOIN LATERAL`; "hoje" é decidido no banco com `(created_at AT TIME ZONE tz)::date = (now() AT TIME ZONE tz)::date`, então relógio e instantes gravados concordam sobre o dia civil (e não em UTC); agregação com `count(...) FILTER (WHERE ...)` em vez de várias consultas; `active` usa outro filtro na mesma passada (dono demo, status não terminal) e por isso não depende do dia; `LEFT JOIN` garante zeros quando não há chamadas; organização inexistente não devolve linha e vira 500 `internal_error` sem vazar o erro bruto; `to_char(..., 'YYYY-MM-DD')` evita depender do `DateStyle` da sessão
- Testes: organização isolada com fuso `UTC` (e `Pacific/Kiritimati`, `Pacific/Pago_Pago`, `Europe/Berlin` no teste de fronteira da meia-noite local) em vez de alterar a organização seed, porque pacotes de teste rodam em paralelo e compartilham o banco; teste de fumaça separado na organização seed confere o fuso `Europe/Berlin`
- Comandos: `DATABASE_URL=… go test ./... -count=1`, `go vet ./...`, `ruby -ryaml -e "YAML.load_file('api/openapi.yaml')"`
- Resultado: `GET /api/v1/dashboard` com `date`, `timezone`, `total`, `inbound`, `outbound`, `missed`, `active`; OpenAPI atualizado com o schema `Dashboard`
- Pendente: SSE (Task 15)

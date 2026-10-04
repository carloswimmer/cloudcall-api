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

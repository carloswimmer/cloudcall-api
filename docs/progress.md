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

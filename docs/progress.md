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

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

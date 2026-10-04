# CloudCall Desk API (demo only)

API Go para a demonstração CloudCall Desk. O repositório Angular fala com esta API via CORS, sem proxy de desenvolvimento.

## Ferramentas

Toolchain Go local registrado:

```
go version go1.26.8 darwin/arm64
```

## Banco de dados

PostgreSQL em Docker com a imagem `postgres:18` (`docker-compose.yml`). Driver [pgx](https://github.com/jackc/pgx) `v5.11.0` (versão resolvida em `go.mod`), usado via `database/sql` (`pgx/v5/stdlib`).

Nota: a imagem `postgres:18` exige o volume montado em `/var/lib/postgresql` (e não em `/var/lib/postgresql/data`), senão o contêiner encerra na inicialização.

Probe de prontidão: `GET /health/ready` retorna `200 {"status":"ready"}` quando o banco responde ao ping, ou `503` caso contrário.

## Configuração

Copie `.env.example` para `.env` e ajuste os valores conforme necessário.

## Comandos

```bash
docker compose up -d
go run ./cmd/api
go test ./...
```

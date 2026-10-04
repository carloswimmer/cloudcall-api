# CloudCall Desk API (demo only)

API Go para a demonstração CloudCall Desk. O repositório Angular fala com esta API via CORS, sem proxy de desenvolvimento.

## Ferramentas

Toolchain Go local registrado:

```
go version go1.26.8 darwin/arm64
```

## Banco de dados

PostgreSQL em Docker com a imagem `postgres:18`. O driver [pgx](https://github.com/jackc/pgx) será adicionado na Task 5.

## Configuração

Copie `.env.example` para `.env` e ajuste os valores conforme necessário.

## Comandos

```bash
docker compose up -d
go run ./cmd/api
go test ./...
```

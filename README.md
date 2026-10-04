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

Copie `.env.example` para `.env` e ajuste os valores conforme necessário. Na subida, a API carrega `.env` do diretório de trabalho (opcional); variáveis já definidas no shell ou na plataforma (AWS, systemd, etc.) não são sobrescritas. Use `ENV_FILE` para apontar outro arquivo.

## Comandos

```bash
docker compose up -d
go run ./cmd/api
go test ./...
```

## Encerramento gracioso

`SIGINT` (Ctrl+C) e `SIGTERM` encerram a API em até 10 segundos: o servidor HTTP para de aceitar conexões, os streams SSE (`GET /api/v1/events`) são encerrados, e então o hub e o pool do banco são fechados. Veja a decisão em [`docs/adr/001-single-process-sse.md`](docs/adr/001-single-process-sse.md).

## Carga de laboratório (`cmd/seedload`)

Para testar paginação, filtros e dashboard com volume, o comando abaixo insere 1.000 contatos e 10.000 chamadas históricas **terminais** (`ended`, `missed`, `rejected`, `failed`) na organização demo:

```bash
DATABASE_URL=postgres://cloudcall:cloudcall@localhost:5432/cloudcall?sslmode=disable go run ./cmd/seedload
```

- Os dados inseridos usam telefones `+1555…`; ao rodar de novo, o comando apaga primeiro as linhas `+1555…` da organização e insere tudo de novo (o resultado é sempre o mesmo). Contatos e chamadas com outros telefones não são tocados.
- **Não rode isto na inicialização da API**: é uma ferramenta manual de laboratório. A API só executa o seed demo pequeno e idempotente.
- As chamadas inseridas não têm linhas em `call_transitions` (servem para listas e dashboard, não para o detalhe).

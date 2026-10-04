# CloudCall Desk API (demo only)

Go API for the CloudCall Desk demo. The Angular frontend talks to this service directly via CORS (no dev proxy).

## Toolchain

Recorded local Go toolchain:

```
go version go1.26.8 darwin/arm64
```

## Database

PostgreSQL runs via Docker using image `postgres:18`. The [pgx](https://github.com/jackc/pgx) driver will be added in Task 5.

## Configuration

Copy `.env.example` to `.env` and adjust values as needed.

## Commands

```bash
docker compose up -d
go run ./cmd/api
go test ./...
```

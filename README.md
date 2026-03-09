# ComBox Backend

[![Code Quality](https://github.com/combox/combox-backend/actions/workflows/security.yml/badge.svg)](https://github.com/combox/combox-backend/actions/workflows/security.yml)

[English](./README.md) | [Русский](./README.ru.md)

Go backend for ComBox. This repo contains private/public HTTP APIs, WebSocket realtime transport, auth/session flows, chats/channels/messages, media storage integration, search, and bot-related services.

## Requirements

- Go 1.24+
- PostgreSQL
- Valkey
- MinIO or S3-compatible storage

## Common Commands

```bash
make tidy
make fmt
make test
make build
make run
```

Main targets:

- `make tidy` - `go mod tidy`
- `make fmt` - `go fmt ./...`
- `make test` - `go test ./...`
- `make build` - build `cmd/api`
- `make run` - run backend from local `.env`
- `make docker-build` - build local Docker image
- `make docker-run` - run local Docker image
- `make commit branch=feature/name message="..."` - branch-first commit + push flow

## Environment

See [.env.example](./.env.example).

Core areas:

- app/http: `HTTP_ADDRESS`, `APP_ENV`, `DEFAULT_LOCALE`
- database/cache: `POSTGRES_DSN`, `VALKEY_ADDR`, `VALKEY_PASSWORD`, `VALKEY_DB`
- auth: `AUTH_ACCESS_SECRET`, `AUTH_REFRESH_SECRET`, `AUTH_ACCESS_TTL`, `AUTH_REFRESH_TTL`
- storage: `MINIO_API_INTERNAL`, `MINIO_PUBLIC_BASE_URL`, `MINIO_BUCKET`, `MINIO_ROOT_USER`, `MINIO_ROOT_PASSWORD`

## Project Layout

- `cmd/api/` - backend entrypoint
- `internal/app/` - dependency wiring
- `internal/config/` - config loading
- `internal/transport/http/` - HTTP and WS transport
- `internal/service/` - application logic
- `internal/repository/` - postgres, valkey, minio repositories
- `internal/domain/` - domain rules
- `migrations/` - SQL migrations
- `strings/` - localized response texts
- `tests/e2e/` - end-to-end tests

## CI And PR Flow

- GitHub Actions validates formatting, tests, build, and vet checks
- security workflows run dependency review, CodeQL, and `govulncheck`
- Dependabot updates Go modules and GitHub Actions
- CodeRabbit can review pull requests after the GitHub App is installed
- merge to `main` should happen only through PR after green checks and review

Important:

- required approvals and required checks must be enforced through GitHub branch protection / rulesets
- the `make commit` target helps avoid direct work on `main`, but GitHub rules should still enforce it server-side

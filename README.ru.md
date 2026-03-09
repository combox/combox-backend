# ComBox Backend

[![Code Quality](https://github.com/combox/combox-backend/actions/workflows/security.yml/badge.svg)](https://github.com/combox/combox-backend/actions/workflows/security.yml)

[English](./README.md) | [Русский](./README.ru.md)

Go backend для ComBox. В репозитории лежат приватный/публичный HTTP API, WebSocket realtime transport, auth/session flows, чаты/каналы/сообщения, интеграция с media storage, поиск и bot-related сервисы.

## Требования

- Go 1.24+
- PostgreSQL
- Valkey
- MinIO или S3-compatible storage

## Основные команды

```bash
make tidy
make fmt
make test
make build
make run
```

Основные цели:

- `make tidy` - `go mod tidy`
- `make fmt` - `go fmt ./...`
- `make test` - `go test ./...`
- `make build` - сборка `cmd/api`
- `make run` - запуск backend из локального `.env`
- `make docker-build` - сборка локального Docker image
- `make docker-run` - запуск локального Docker image
- `make commit branch=feature/name message="..."` - branch-first commit + push flow

## Переменные окружения

Смотри [.env.example](./.env.example).

Основные зоны:

- app/http: `HTTP_ADDRESS`, `APP_ENV`, `DEFAULT_LOCALE`
- database/cache: `POSTGRES_DSN`, `VALKEY_ADDR`, `VALKEY_PASSWORD`, `VALKEY_DB`
- auth: `AUTH_ACCESS_SECRET`, `AUTH_REFRESH_SECRET`, `AUTH_ACCESS_TTL`, `AUTH_REFRESH_TTL`
- storage: `MINIO_API_INTERNAL`, `MINIO_PUBLIC_BASE_URL`, `MINIO_BUCKET`, `MINIO_ROOT_USER`, `MINIO_ROOT_PASSWORD`

## Структура проекта

- `cmd/api/` - backend entrypoint
- `internal/app/` - wiring зависимостей
- `internal/config/` - загрузка конфигов
- `internal/transport/http/` - HTTP и WS transport
- `internal/service/` - application logic
- `internal/repository/` - postgres, valkey, minio repositories
- `internal/domain/` - доменные правила
- `migrations/` - SQL migrations
- `strings/` - локализованные response texts
- `tests/e2e/` - end-to-end tests

## CI И PR Flow

- GitHub Actions проверяет форматирование, тесты, сборку и `go vet`
- security workflows запускают dependency review, CodeQL и `govulncheck`
- Dependabot обновляет Go modules и GitHub Actions
- CodeRabbit сможет ревьюить PR после установки GitHub App
- merge в `main` должен идти только через PR после зелёных проверок и review

Важно:

- required approvals и required checks включаются через GitHub branch protection / rulesets
- `make commit` помогает не работать напрямую в `main`, но серверные правила всё равно нужно включить в GitHub

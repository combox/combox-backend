# ComBox Backend

![banner](.github/assets/banner.png)

[![CodeQuality](https://github.com/combox/combox-backend/actions/workflows/security.yml/badge.svg)](https://github.com/combox/combox-backend/actions/workflows/security.yml)

[English](./README.md) | [Русский](./README.ru.md)

Бэкенд-сервис для ComBox. Он отдаёт приватный и публичный HTTP API, WebSocket realtime-транспорт, auth/session flows, логику чатов и каналов, интеграцию с медиа-хранилищем, поиск, bot integrations и готов для edge-развёртывания с mTLS.

## Технологии

[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-4169E1?style=for-the-badge&logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Valkey](https://img.shields.io/badge/Valkey-DC382D?style=for-the-badge&logo=valkey&logoColor=white)](https://valkey.io)
[![MinIO](https://img.shields.io/badge/MinIO-C72E49?style=for-the-badge&logo=minio&logoColor=white)](https://min.io)
[![Docker](https://img.shields.io/badge/Docker-2496ED?style=for-the-badge&logo=docker&logoColor=white)](https://www.docker.com)

## Архитектура (высокоуровнево)

```text
 Клиенты (Web / App / Bots)
            |
            v
   /api/private/*   /api/public/*   /ws/*
            |
            v
      [ ComBox Backend ]
            |
      +-----+-------------------+------------------+
      |                         |                  |
      v                         v                  v
  PostgreSQL                Valkey             MinIO / S3
  users, chats,            cache, pubsub,      attachments,
  messages, bots,          presence,            previews, media
  sessions, attachments    user settings
```

## Что умеет

- Auth: register, login, refresh, logout, profile, password, email change
- Realtime: WebSocket transport с fanout через Valkey
- Messaging: чаты, группы, каналы/топики, реакции, update/delete сообщений
- Media: метаданные вложений + MinIO/S3 storage
- Search: люди и публичные чаты
- Bots: публичная bot info, bot tokens, bot webhooks, system bot notifications
- Presence и пользовательские настройки: last seen visibility, recent GIFs, mute/unread counters

## Поверхность API

Приватный HTTP:

- `/api/private/v1/auth/*`
- `/api/private/v1/profile`
- `/api/private/v1/profile/password`
- `/api/private/v1/profile/email/change/*`
- `/api/private/v1/profile/settings`
- `/api/private/v1/chats`
- `/api/private/v1/chats/{chat_id}/messages`
- `/api/private/v1/messages/{message_id}`
- `/api/private/v1/messages/{message_id}/read`
- `/api/private/v1/messages/{message_id}/reactions`
- `/api/private/v1/media/*`
- `/api/private/v1/search`
- `/api/private/v1/gifs/*`
- `/api/private/v1/presence`

Публичный HTTP:

- `/api/public/v1/bots/{bot_id}/info`
- `/api/public/v1/bots/{bot_id}/webhooks`

Realtime:

- `/api/private/v1/ws`

Health:

- `/healthz`
- `/readyz`

## Realtime-модель

WebSocket-клиенты подключаются с тем же user context, что и HTTP.

- backend подписывается на user/device/presence channels в Valkey
- chat events расходятся между backend-инстансами
- приватные клиенты могут запрашивать чаты, сообщения, отправлять сообщения, отмечать reads и переключать реакции

Это позволяет масштабировать backend горизонтально без sticky state в памяти процесса.

## Auth и сессии

- access tokens — короткоживущие JWT
- refresh-сессии хранятся в PostgreSQL
- `users.session_idle_ttl_seconds` управляет sliding lifetime refresh-сессии
- каждый успешный refresh продлевает сессию
- если пользователь был неактивен дольше заданного idle TTL, нужен повторный логин

## Медиа-модель

ComBox использует MinIO локально и любой S3-compatible storage в production.

- метаданные лежат в PostgreSQL
- бинарные объекты лежат в MinIO/S3
- backend отдаёт attachment records и media lookup endpoints
- стратегия публичных URL настраивается через env вроде `MINIO_PUBLIC_BASE_URL`

## Режимы развёртывания

### Локальная разработка

```bash
cp .env.example .env
# отредактировать .env

make run
```

### Локальный Docker-образ

```bash
make docker-build
make docker-run
```

### Edge mode

Запуск backend внутри общей edge-сети без публикации host ports:

```bash
make edge-up
```

Используется [docker-compose.edge.yml](./docker-compose.edge.yml) и внешняя сеть `combox-edge-core`.

### Multi-machine behind edge with mTLS

Для развёртывания за `combox-edge` с взаимным TLS:

```bash
TLS_ENABLED=true
TLS_CERT_FILE=/etc/combox/mtls/server.crt
TLS_KEY_FILE=/etc/combox/mtls/server.key
TLS_CLIENT_CA_FILE=/etc/combox/mtls/ca.crt
HTTP_ADDRESS=:8443
```

Дальше edge-прослойка форвардит HTTPS+mTLS трафик на backend-инстансы.

## Переменные окружения

Основные переменные:

- `APP_ENV`
- `HTTP_ADDRESS`
- `DEFAULT_LOCALE`
- `STRINGS_PATH`
- `POSTGRES_DSN`
- `VALKEY_ADDR`
- `VALKEY_PASSWORD`
- `VALKEY_DB`
- `AUTH_ACCESS_SECRET`
- `AUTH_REFRESH_SECRET`
- `AUTH_ACCESS_TTL`
- `AUTH_REFRESH_TTL`
- `BOT_TOKEN_PEPPER`

Email / auth flow:

- `AUTH_EMAIL_VERIFY_ENABLED`
- `AUTH_EMAIL_CODE_TTL`
- `AUTH_EMAIL_CODE_MAX_ATTEMPTS`
- `RESEND_API_KEY`
- `RESEND_FROM`
- `RESEND_BASE_URL`

Storage:

- `MINIO_API_INTERNAL`
- `MINIO_PUBLIC_BASE_URL`
- `MINIO_BUCKET`
- `MINIO_ROOT_USER`
- `MINIO_ROOT_PASSWORD`
- `MINIO_SECURE`
- `MINIO_REGION`
- `MINIO_SSE_MODE`

Прочее:

- `GIPHY_API_KEY`
- `MIGRATIONS_ENABLED`
- `MIGRATIONS_PATH`
- `READY_TIMEOUT`

Смотри [.env.example](./.env.example) для значений по умолчанию и локальных примеров.

## Миграции и схема

- миграции лежат в [migrations/](./migrations)
- backend может применять их автоматически при старте, если `MIGRATIONS_ENABLED=true`
- применённые миграции отслеживаются в `schema_migrations`

Основные домены хранения:

- users и sessions
- chats, channels, memberships
- messages и reactions
- attachments/media sessions
- bots, bot tokens, bot webhooks

## Тестирование

Юнит-тесты:

```bash
go test ./...
```

E2E-тесты:

```bash
docker compose -f docker-compose.e2e.yml up -d
export E2E_POSTGRES_DSN='postgres://combox:combox_local_password@127.0.0.1:15432/combox?sslmode=disable'
export E2E_VALKEY_ADDR='127.0.0.1:16379'
go test -tags=e2e ./tests/e2e -count=1
```

## Сборка и операции

Основные цели:

- `make tidy`
- `make fmt`
- `make build`
- `make run`
- `make test`
- `make docker-build`
- `make docker-run`
- `make edge-up`
- `make edge-down`
- `make edge-logs`

## Структура репозитория

- `cmd/api/` - entrypoint приложения
- `internal/app/` - wiring и dependency setup
- `internal/config/` - env/config loading
- `internal/transport/http/` - HTTP + WS handlers
- `internal/service/` - business logic
- `internal/repository/postgres/` - PostgreSQL repositories
- `internal/repository/valkey/` - Valkey repositories
- `internal/integration/` - external/system integrations
- `migrations/` - database migrations
- `strings/` - локализованные response texts
- `tests/e2e/` - end-to-end tests

## Заметки

- user-facing response texts должны жить в `strings/`, а не в хардкоде transport-логики
- edge deployment assumptions синхронизированы с `combox-edge`
- system-bot и bot-token слой уже есть; новые moderation/collectible flows нужно строить поверх этих примитивов, а не дублировать их

## Лицензия

<a href="./LICENSE">
  <img src=".github/assets/mit-badge.png" width="70" alt="MIT License">
</a>

## Автор

[Ernela](https://github.com/Ernous) - Разработчица;
[D7TUN6](https://github.com/D7TUN6) - Идея, разработчик

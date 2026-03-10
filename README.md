# ComBox Backend

![banner](.github/assets/banner.png)

[![CodeQuality](https://github.com/combox/combox-backend/actions/workflows/security.yml/badge.svg)](https://github.com/combox/combox-backend/actions/workflows/security.yml)

[English](./README.md) | [Русский](./README.ru.md)

Backend service for ComBox. It exposes private/public HTTP APIs, WebSocket realtime transport, auth/session flows, chat and channel logic, media storage integration, search, bot integrations, and edge-ready deployment with mTLS.

## Powered by

[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-4169E1?style=for-the-badge&logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Valkey](https://img.shields.io/badge/Valkey-DC382D?style=for-the-badge&logo=valkey&logoColor=white)](https://valkey.io)
[![MinIO](https://img.shields.io/badge/MinIO-C72E49?style=for-the-badge&logo=minio&logoColor=white)](https://min.io)
[![Docker](https://img.shields.io/badge/Docker-2496ED?style=for-the-badge&logo=docker&logoColor=white)](https://www.docker.com)

## Architecture (high level)

```text
 Clients (Web / App / Bots)
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

## What it does

- Auth: register, login, refresh, logout, profile, password, email change
- Realtime: WebSocket transport with Valkey-backed fanout
- Messaging: chats, groups, channels/topics, reactions, message updates/deletes
- Media: attachment metadata + MinIO/S3-backed object access
- Search: people and public chats
- Bots: public bot info, bot tokens, bot webhooks, system bot notifications
- Presence and per-user settings: last seen visibility, GIF recents, mute/unread counters

## API surface

Private HTTP:

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

Public HTTP:

- `/api/public/v1/bots/{bot_id}/info`
- `/api/public/v1/bots/{bot_id}/webhooks`

Realtime:

- `/api/private/v1/ws`

Health:

- `/healthz`
- `/readyz`

## Realtime model

WebSocket clients connect with the same authenticated user context as HTTP.

- backend subscribes to user/device/presence channels in Valkey
- chat events are faned out across backend instances
- private clients can request chats, messages, send messages, mark reads, toggle reactions, and subscribe to presence streams

This keeps the backend horizontally scalable without sticky state in process memory.

## Auth and session model

- access tokens are short-lived JWTs
- refresh sessions are stored in PostgreSQL
- `users.session_idle_ttl_seconds` controls sliding refresh-session lifetime
- each successful refresh extends the session
- if the user stays inactive beyond the configured idle TTL, login is required again

## Media model

ComBox uses MinIO locally and any S3-compatible storage in production.

- metadata lives in PostgreSQL
- binary objects live in MinIO/S3
- backend exposes attachment records and media lookup endpoints
- edge/public URL strategy is controlled through env vars such as `MINIO_PUBLIC_BASE_URL`

## Deployment modes

### Local development

```bash
cp .env.example .env
# edit .env

make run
```

### Local Docker image

```bash
make docker-build
make docker-run
```

### Edge mode

Run backend inside the shared edge Docker network, without publishing host ports:

```bash
make edge-up
```

This uses [docker-compose.edge.yml](./docker-compose.edge.yml) and joins external network `combox-edge-core`.

### Multi-machine behind edge with mTLS

For deployment behind `combox-edge`, enable TLS and client cert verification:

```bash
TLS_ENABLED=true
TLS_CERT_FILE=/etc/combox/mtls/server.crt
TLS_KEY_FILE=/etc/combox/mtls/server.key
TLS_CLIENT_CA_FILE=/etc/combox/mtls/ca.crt
HTTP_ADDRESS=:8443
```

The edge layer then forwards HTTPS+mTLS traffic to backend instances.

## Environment

Core variables:

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

Other:

- `GIPHY_API_KEY`
- `MIGRATIONS_ENABLED`
- `MIGRATIONS_PATH`
- `READY_TIMEOUT`

See [.env.example](./.env.example) for defaults and local examples.

## Migrations and schema

- migrations live in [migrations/](./migrations)
- startup can apply them automatically when `MIGRATIONS_ENABLED=true`
- applied migrations are tracked in `schema_migrations`

Core persisted domains:

- users and sessions
- chats, channels, memberships
- messages and reactions
- attachments/media sessions
- bots, bot tokens, bot webhooks

## Testing

Unit tests:

```bash
go test ./...
```

E2E tests:

```bash
docker compose -f docker-compose.e2e.yml up -d
export E2E_POSTGRES_DSN='postgres://combox:combox_local_password@127.0.0.1:15432/combox?sslmode=disable'
export E2E_VALKEY_ADDR='127.0.0.1:16379'
go test -tags=e2e ./tests/e2e -count=1
```

## Build and operations

Common targets:

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

## Repo layout

- `cmd/api/` - application entrypoint
- `internal/app/` - wiring and dependency setup
- `internal/config/` - env/config loading
- `internal/transport/http/` - HTTP + WS handlers
- `internal/service/` - business logic
- `internal/repository/postgres/` - PostgreSQL repositories
- `internal/repository/valkey/` - Valkey repositories
- `internal/integration/` - external/system integrations
- `migrations/` - database migrations
- `strings/` - localized response texts
- `tests/e2e/` - end-to-end tests

## Notes

- user-facing response texts belong in `strings/`, not hardcoded transport logic
- edge deployment assumptions are aligned with `combox-edge`
- system-bot and bot-token functionality already exist; new moderation/collectible flows should extend those primitives instead of duplicating them

## License

<a href="./LICENSE">
  <img src=".github/assets/mit-badge.png" width="70" alt="MIT License">
</a>

## Author

[Ernela](https://github.com/Ernous) - Developer;  
[D7TUN6](https://github.com/D7TUN6) - Idea, Developer

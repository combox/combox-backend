# ComBox Backend

![banner](.github/assets/banner.png)

[English](./README.md) | [Русский](./README.ru.md)

Backend service for ComBox platform. Provides REST API, WebSocket, and core business logic for chat, authentication, and media handling.

## Powered by

[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-4169E1?style=for-the-badge&logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Valkey](https://img.shields.io/badge/Valkey-DC382D?style=for-the-badge&logo=valkey&logoColor=white)](https://valkey.io)
[![Docker](https://img.shields.io/badge/Docker-2496ED?style=for-the-badge&logo=docker&logoColor=white)](https://www.docker.com)

## Architecture (high level)

```text
           Internet / LAN
                 |
                 v
            [ Nginx Edge ]  :443
                 |
                 v
            /api/private/*  -> ComBox Backend
                 |
                 v
          [ HTTP + WS Server ]
                 |
      +----------+----------+
      |          |          |
   Auth       Chat       Media
 Service     Service    Service
      |          |          |
      v          v          v
  PostgreSQL   Valkey    S3/MinIO
```

## API Overview

### Authentication (M2)

- `POST /api/private/v1/auth/register`
- `POST /api/private/v1/auth/login`
- `POST /api/private/v1/auth/refresh`
- `POST /api/private/v1/auth/logout`

All private endpoints require `Authorization: Bearer <access_token>` header.

### Chat (M3)

- `GET /api/private/v1/chats`
- `POST /api/private/v1/chats`
- `GET /api/private/v1/chats/{chat_id}/messages`
- `POST /api/private/v1/chats/{chat_id}/messages`
- `PATCH /api/private/v1/messages/{message_id}`
- `DELETE /api/private/v1/messages/{message_id}`
- `POST /api/private/v1/messages/{message_id}/read`

### Realtime (M4)

- WebSocket: `/api/private/v1/ws?access_token=<token>`
- Subscribe/unsubscribe to chat streams
- Fanout across instances via Valkey

### Media (M5)

- `POST /api/private/v1/media/upload-url` (presigned)
- `POST /api/private/v1/media/attachments`
- `GET /api/private/v1/media/attachments/{id}`

### Public API (M5)

- `GET /api/public/v1/bots/{bot_id}/info`
- `POST /api/public/v1/bots/{bot_id}/webhooks`

## Security

### Authentication

- JWT access tokens (short-lived) + refresh tokens (long-lived)
- Refresh sessions stored in PostgreSQL with revocation support
- WebSocket authentication via query parameter `access_token`

### Authorization

- Private API (`/api/private/*`) requires valid Bearer token
- User context extracted and validated via centralized middleware
- Public API (`/api/public/*`) uses bot token scopes

### Error responses

All errors return structured JSON envelope:

```json
{
  "code": "invalid_credentials",
  "message": "invalid credentials",
  "details": {},
  "request_id": "req_123456789"
}
```

## Deployment modes

### Local development

```bash
cp .env.example .env
# Edit .env for local PostgreSQL/Valkey
make run
```

### Edge gateway mode (recommended)

Backend runs without host ports, accessible only via edge nginx:

```bash
make edge-up
```

This uses `docker-compose.edge.yml` and joins external network `combox-edge-core`.

### Multi-machine with mTLS

For VPS deployment behind edge with mutual TLS:

```bash
TLS_ENABLED=true
TLS_CERT_FILE=/etc/combox/mtls/server.crt
TLS_KEY_FILE=/etc/combox/mtls/server.key
TLS_CLIENT_CA_FILE=/etc/combox/mtls/ca.crt
HTTP_ADDRESS=:8443
```

Place certs on the VPS and mount them into the container (see `docker-compose.edge.yml`).

## Environment

Required variables:

- `POSTGRES_DSN` - PostgreSQL connection string
- `VALKEY_ADDR` - Valkey/Redis address
- `DEFAULT_LOCALE` - Default response locale (e.g., `en`)
- `STRINGS_PATH` - Path to i18n strings directory
- `AUTH_ACCESS_SECRET` - JWT access token signing secret
- `AUTH_REFRESH_SECRET` - JWT refresh token signing secret

Optional variables have defaults in `.env.example`.

## Health checks

- `GET /healthz` - Liveness probe (always 200)
- `GET /readyz` - Readiness probe (checks Postgres + Valkey)

## Database

### Migrations

- Automatic on startup if `MIGRATIONS_ENABLED=true`
- Files in `MIGRATIONS_PATH` (default: `migrations`)
- Applied migrations tracked in `schema_migrations` table

### Schema

Core tables:

- `users` - User accounts
- `sessions` - Refresh token sessions
- `chats` - Chat metadata
- `messages` - Chat messages
- `attachments` - Media metadata

## Internationalization

- Response texts served from `strings/*.json`
- Request locale from `Accept-Language` header
- Fallback to `DEFAULT_LOCALE`
- Supports structured error messages per locale

## Testing

### Unit tests

```bash
go test ./...
```

### E2E tests

Requires real Postgres + Valkey:

```bash
docker compose -f docker-compose.e2e.yml up -d
export E2E_POSTGRES_DSN='postgres://combox:combox_local_password@127.0.0.1:15432/combox?sslmode=disable'
export E2E_VALKEY_ADDR='127.0.0.1:16379'
go test -tags=e2e ./tests/e2e -count=1
```

## Build and Docker

### Local build

```bash
make build
```

### Docker image

```bash
make docker-build
```

### Multi-platform build

```bash
make docker-build-multi
```

## Observability

### Logging

- Structured JSON logging
- Request IDs for tracing
- Log levels: `debug`, `info`, `warn`, `error`
- Configurable via `LOG_LEVEL`

### Metrics

TODO: Add Prometheus metrics endpoint.

## Development workflow

1. Create feature branch from `main`
2. Implement changes with tests
3. Run `make test` and `make lint`
4. Update documentation if needed
5. Submit pull request

## Repo layout

- `cmd/api/` - Application entrypoint
- `internal/` - Private application code
  - `app/` - Application setup and DI
  - `config/` - Configuration loading
  - `observability/` - Logging and metrics
  - `transport/` - HTTP/WebSocket handlers
  - `service/` - Business logic
  - `repository/` - Data access layer
- `migrations/` - Database migrations
- `strings/` - i18n message files
- `tests/e2e/` - End-to-end tests
- `docker-compose*.yml` - Development containers
- `Dockerfile` - Container build
- `Makefile` - Build automation

## MWP

- **M1** - Project bootstrap, health checks, migrations
- **M2** - Authentication service (register/login/refresh/logout)
- **M3** - Chat core (create/list chats, messages, pagination)
- **M4** - Realtime WebSocket with fanout
- **M5** - Media handling and public API
- **M6** - Hardening and performance

See `mwp.md` for detailed milestone specifications.

## License

<a href="./LICENSE">
  <img src=".github/assets/mit-badge.png" width="70" alt="MIT License">
</a>

## Author

[Ernela](https://github.com/Ernous) - Developer;
[D7TUN6](https://github.com/D7TUN6) - Idea, Developer

# ComBox Backend

![banner](.github/assets/banner.png)

[English](./README.md) | [Русский](./README.ru.md)

Бэкенд-сервис для платформы ComBox. Предоставляет REST API, WebSocket и основную бизнес-логику для чата, аутентификации и обработки медиа.

## Технологии

[![Go](https://img.shields.io/badge/Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-4169E1?style=for-the-badge&logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Valkey](https://img.shields.io/badge/Valkey-DC382D?style=for-the-badge&logo=valkey&logoColor=white)](https://valkey.io)
[![Docker](https://img.shields.io/badge/Docker-2496ED?style=for-the-badge&logo=docker&logoColor=white)](https://www.docker.com)

## Архитектура (высокоуровневая)

```text
           Интернет / LAN
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

## Обзор API

### Аутентификация (M2)

- `POST /api/private/v1/auth/register`
- `POST /api/private/v1/auth/login`
- `POST /api/private/v1/auth/refresh`
- `POST /api/private/v1/auth/logout`

Все приватные эндпоинты требуют заголовок `Authorization: Bearer <access_token>`.

### Чат (M3)

- `GET /api/private/v1/chats`
- `POST /api/private/v1/chats`
- `GET /api/private/v1/chats/{chat_id}/messages`
- `POST /api/private/v1/chats/{chat_id}/messages`
- `PATCH /api/private/v1/messages/{message_id}`
- `DELETE /api/private/v1/messages/{message_id}`
- `POST /api/private/v1/messages/{message_id}/read`

### Реальное время (M4)

- WebSocket: `/api/private/v1/ws?access_token=<token>`
- Подписка/отписка от потоков чата
- Фанаут между инстансами через Valkey

### Медиа (M5)

- `POST /api/private/v1/media/upload-url` (presigned)
- `POST /api/private/v1/media/attachments`
- `GET /api/private/v1/media/attachments/{id}`

### Публичный API (M5)

- `GET /api/public/v1/bots/{bot_id}/info`
- `POST /api/public/v1/bots/{bot_id}/webhooks`

## Безопасность

### Аутентификация

- JWT access токены (короткоживущие) + refresh токены (долгоживущие)
- Сессии refresh хранятся в PostgreSQL с поддержкой отзыва
- Аутентификация WebSocket через query параметр `access_token`

### Авторизация

- Приватный API (`/api/private/*`) требует валидный Bearer токен
- Контекст пользователя извлекается и проверяется через централизованное middleware
- Публичный API (`/api/public/*`) использует bot токены с областями доступа

### Обработка ошибок

Все ошибки возвращают структурированный JSON:

```json
{
  "code": "invalid_credentials",
  "message": "неверные учётные данные",
  "details": {},
  "request_id": "req_123456789"
}
```

## Режимы развёртывания

### Локальная разработка

```bash
cp .env.example .env
# Отредактировать .env для локальных PostgreSQL/Valkey
make run
```

### Edge gateway режим (рекомендуется)

Бэкенд работает без публикации портов, доступен только через edge nginx:

```bash
make edge-up
```

Использует `docker-compose.edge.yml` и подключается к внешней сети `combox-edge-core`.

### Multi-machine с mTLS

Для развёртывания на VPS за edge с взаимным TLS:

```bash
TLS_ENABLED=true
TLS_CERT_FILE=/etc/combox/mtls/server.crt
TLS_KEY_FILE=/etc/combox/mtls/server.key
TLS_CLIENT_CA_FILE=/etc/combox/mtls/ca.crt
HTTP_ADDRESS=:8443
```

Разместите сертификаты на VPS и смонтируйте их в контейнер (см. `docker-compose.edge.yml`).

## Переменные окружения

Обязательные переменные:

- `POSTGRES_DSN` - Строка подключения к PostgreSQL
- `VALKEY_ADDR` - Адрес Valkey/Redis
- `DEFAULT_LOCALE` - Локаль ответов по умолчанию (например, `ru`)
- `STRINGS_PATH` - Путь к директории i18n строк
- `AUTH_ACCESS_SECRET` - Секрет для подписи JWT access токенов
- `AUTH_REFRESH_SECRET` - Секрет для подписи JWT refresh токенов

Опциональные переменные имеют значения по умолчанию в `.env.example`.

## Health checks

- `GET /healthz` - Liveness probe (всегда 200)
- `GET /readyz` - Readiness probe (проверяет Postgres + Valkey)

## База данных

### Миграции

- Автоматические при запуске если `MIGRATIONS_ENABLED=true`
- Файлы в `MIGRATIONS_PATH` (по умолчанию: `migrations`)
- Применённые миграции отслеживаются в таблице `schema_migrations`

### Схема

Основные таблицы:

- `users` - Пользователи
- `sessions` - Сессии refresh токенов
- `chats` - Метаданные чатов
- `messages` - Сообщения чатов
- `attachments` - Метаданные медиа

## Интернационализация

- Тексты ответов из `strings/*.json`
- Локаль запроса из заголовка `Accept-Language`
- Fallback на `DEFAULT_LOCALE`
- Поддержка структурированных сообщений об ошибках по локали

## Тестирование

### Юнит-тесты

```bash
go test ./...
```

### E2E тесты

Требуют реальные Postgres + Valkey:

```bash
docker compose -f docker-compose.e2e.yml up -d
export E2E_POSTGRES_DSN='postgres://combox:combox_local_password@127.0.0.1:15432/combox?sslmode=disable'
export E2E_VALKEY_ADDR='127.0.0.1:16379'
go test -tags=e2e ./tests/e2e -count=1
```

## Сборка и Docker

### Локальная сборка

```bash
make build
```

### Docker образ

```bash
make docker-build
```

### Мультиплатформенная сборка

```bash
make docker-build-multi
```

## Observability

### Логирование

- Структурированный JSON лог
- Request ID для трейсинга
- Уровни логов: `debug`, `info`, `warn`, `error`
- Настраивается через `LOG_LEVEL`

### Метрики

TODO: Добавить endpoint Prometheus метрик.

## Процесс разработки

1. Создать feature ветку от `main`
2. Реализовать изменения с тестами
3. Запустить `make test` и `make lint`
4. Обновить документацию при необходимости
5. Отправить pull request

## Структура репозитория

- `cmd/api/` - Точка входа приложения
- `internal/` - Приватный код приложения
  - `app/` - Настройка и DI приложения
  - `config/` - Загрузка конфигурации
  - `observability/` - Логирование и метрики
  - `transport/` - HTTP/WebSocket обработчики
  - `service/` - Бизнес-логика
  - `repository/` - Слой доступа к данным
- `migrations/` - Миграции базы данных
- `strings/` - Файлы i18n сообщений
- `tests/e2e/` - End-to-end тесты
- `docker-compose*.yml` - Контейнеры для разработки
- `Dockerfile` - Сборка контейнера
- `Makefile` - Автоматизация сборки

## Майлстоуны

- **M1** - Bootstrap проекта, health checks, миграции
- **M2** - Сервис аутентификации (register/login/refresh/logout)
- **M3** - Ядро чата (create/list чаты, сообщения, пагинация)
- **M4** - WebSocket в реальном времени с фанаутом
- **M5** - Обработка медиа и публичный API
- **M6** - Укрепление безопасности и производительность

Подробные спецификации майлстоунов в `mwp.md`.

## Лицензия

<a href="./LICENSE">
  <img src=".github/assets/mit-badge.png" width="70" alt="MIT License">
</a>

## Автор

[Ernela](https://github.com/Ernous) - Разработчик;
[D7TUN6](https://github.com/D7TUN6) - Идея, разработчик

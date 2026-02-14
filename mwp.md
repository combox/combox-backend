# Chat Backend MWP

## 1. Goal

MWP (Minimum Working Product) for `chat-backend` is the smallest production-oriented backend that supports:

- real-time messaging for first-party clients
- persisted history in PostgreSQL
- media attachments via S3-compatible storage
- scoped bot access via public API

Target: one deployable modular monolith with clear boundaries and zero blocking architectural debt for v1.

## 2. System Boundaries

### In scope

- private REST API for clients: `/api/private/v1/*`
- public REST API for bots/integrations: `/api/public/v1/*`
- WebSocket gateway: `/ws/v1`
- auth/session lifecycle
- chat/message core flows
- read states and presence (basic)
- file upload presign flow
- observability baseline (logs/metrics/health)

### Out of scope

- end-to-end encryption
- calls/video/voice
- multi-region active-active
- full-text distributed search cluster
- advanced moderation/admin suite

## 3. Architecture Shape (MWP)

- Pattern: modular monolith
- Layers:
  - `internal/transport` (HTTP/WS + middleware)
  - `internal/service` (business use-cases)
  - `internal/repository` (postgres/valkey/s3)
  - `internal/domain` (entities/rules/errors)
- Async fanout: Valkey Pub/Sub
- Source of truth: PostgreSQL
- Ephemeral state: Valkey
- Object payloads: S3-compatible bucket
- Concurrency model: goroutine-first for all I/O and realtime flows

## 3.1 Concurrency Rule (Mandatory)

- Every network-bound path must be designed around goroutines and context cancellation.
- Background jobs (fanout, retries, cleanup, websocket writers) must run in managed goroutine workers.
- No blocking long-running operation is allowed on request goroutine without timeout/cancel path.
- Goroutine lifecycle must be explicit (start, stop, wait, drain).

## 3.2 Strings Rule (Mandatory)

- User-facing/runtime response text must come from `strings/*.json`.
- `en` is fallback locale, selected by config.
- New API response messages are not allowed without adding string keys first.

## 4. API Contract (MWP)

### Private API (`/api/private/v1`)

- `POST /auth/register`
- `POST /auth/login`
- `POST /auth/refresh`
- `POST /auth/logout`
- `GET /users/me`
- `GET /chats`
- `POST /chats`
- `GET /chats/{chat_id}/messages`
- `POST /chats/{chat_id}/messages`
- `PATCH /messages/{message_id}`
- `DELETE /messages/{message_id}`
- `POST /messages/{message_id}/read`
- `POST /media/presign-upload`

### Public API (`/api/public/v1`)

- `POST /bot/messages`
- `GET /bot/chats/{chat_id}/messages`
- `POST /bot/webhooks`

### WebSocket (`/ws/v1`)

- `chat.subscribe`
- `message.created`
- `message.updated`
- `message.deleted`
- `message.read`
- `typing.started` / `typing.stopped`

## 5. Data Model (Minimal)

- `users`
- `sessions`
- `chats`
- `chat_members`
- `messages`
- `message_reads`
- `attachments`
- `bot_tokens`

Constraints:

- all IDs are UUID
- all timestamps are UTC
- soft delete for messages
- idempotency key support for message send

## 6. Folder Structure (Start Point)

```text
chat-backend/
  cmd/
    api/
      main.go
  internal/
    app/
    config/
    transport/
      http/
      ws/
      middleware/
    service/
    repository/
      postgres/
      valkey/
      s3/
    domain/
    observability/
  migrations/
  api/
    openapi/
  tests/
    integration/
  rules.md
  mwp.md
```

## 7. Milestones

Mandatory rule for all milestones:

- Each milestone closes only with tests for all added functionality.
- At minimum: unit tests for business/transport logic + integration tests for external dependencies touched in milestone.
- Milestone is not complete if tests are missing or failing.

### M1: Foundation - done

Deliverables:

- bootstrap project and module structure
- config loader from env
- DB connectivity and migration runner
- base HTTP server with liveness/readiness
- structured logging + request ID middleware

Exit criteria:

- service starts with `chat-edge` dependencies
- readiness fails when postgres/valkey unavailable
- CI runs fmt/lint/unit baseline

### M2: Auth and Sessions - done

Deliverables:

- register/login/logout/refresh endpoints
- access token + refresh token flow
- session persistence and revocation

Exit criteria:

- happy-path auth e2e test passes
- expired access token returns contract error envelope

### M3: Chat Core (HTTP) - done

Deliverables:

- create/list chats
- message send/edit/delete/read endpoints
- history pagination by cursor

Exit criteria:

- two users exchange messages via REST
- pagination stable under concurrent writes

### M4: Realtime (WS) - done

Deliverables:

- authenticated websocket handshake
- subscribe/unsubscribe chat streams
- Valkey-backed fanout between instances

Exit criteria:

- message from instance A delivered to subscriber on instance B
- reconnect does not duplicate events for same client message ID

### M5: Media and Public API - in progress

Deliverables:

- presigned upload URL generation
- attachment metadata binding to message
- bot token auth with scopes

Exit criteria:

- upload + attach + read flow works end-to-end
- bot token cannot access out-of-scope chat

### M6: Hardening and RC

Deliverables:

- rate limiting and payload guards
- integration tests for core flows
- smoke load profile and p95 baseline

Exit criteria:

- no critical findings in lint/tests
- release candidate image built and bootable

## 8. Non-Functional Targets (MWP)

- API p95 (local baseline): <= 250ms for non-upload endpoints
- WS fanout p95 (local baseline): <= 150ms
- startup time: <= 10s (without migrations)
- zero plaintext secrets in logs

## 9. Definition of Ready per Task

- endpoint/event contract defined
- DB impact identified (migration yes/no)
- observability fields specified
- test approach listed (unit/integration)

## 10. Definition of Done per Task

- code merged with tests
- metrics/log fields present
- docs updated (`mwp.md` + contract)
- backward compatibility checked for v1

## 11. Immediate Backlog (Next Coding Step)

1. Implement `PATCH /messages/{message_id}` and `DELETE /messages/{message_id}`.
2. Implement `POST /messages/{message_id}/read`.
3. Add integration tests for M3 chat/message flows against PostgreSQL.
4. Add OpenAPI draft for M2+M3 private API endpoints and error envelope.

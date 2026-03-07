# Chat Backend Rules

## 1. Purpose

This file defines how we design, implement, test, and ship `chat-backend`.
Primary goals:

- predictable architecture
- stable APIs for clients and bots
- safe data handling
- fast delivery without breaking contracts

## 2. Core Stack

- Language: Go (stable current major)
- HTTP: `net/http` + explicit middlewares
- Realtime: WebSocket
- DB: PostgreSQL
- Cache/queue/pubsub: Valkey (Redis-compatible)
- Object storage: S3-compatible API (MinIO in local/dev)
- Edge/proxy: Nginx in `chat-edge`
- Deploy unit: container image

## 3. Architecture Principles

- Modular monolith first, microservices later only by measured need.
- Clear layers:
  - `transport` (HTTP/WS)
  - `service` (business logic)
  - `repository` (DB/Valkey/S3)
  - `domain` (entities, value objects, errors)
- Handlers do not contain business logic.
- Repositories do not contain HTTP semantics.
- All external side effects go through interfaces.

## 4. API Model

- Two API zones:
  - private API: first-party clients only (`/api/private/*`)
  - public API: bots/integrations (`/api/public/*`)
- API-first approach:
  - define contracts before implementation
  - no breaking changes without version bump
- Versioning:
  - path versioning (`/v1`)
- Request/response format:
  - JSON
  - UTC timestamps (ISO-8601)
  - stable error envelope (`code`, `message`, `details`, `request_id`)

## 5. WebSocket Rules

- WS endpoint uses authenticated token handshake.
- All events are typed and versioned.
- Idempotency for client retries (message/client request IDs).
- Per-connection and per-user limits (rate and payload size).
- Presence, typing, and fanout use Valkey pubsub/cache.

## 6. Data & Storage Rules

- PostgreSQL is source of truth.
- Valkey is cache/ephemeral state only.
- S3 stores media and attachments only; metadata stays in PostgreSQL.
- Never store plaintext secrets in DB.
- Use DB migrations only; no manual schema changes in production.

## 7. Security Rules

- JWT access + refresh strategy (short-lived access token).
- RBAC for admin/integration actions.
- Public API uses scoped API keys.
- Validate and sanitize all input at boundary.
- Limit upload types/sizes; scan uploads where applicable.
- PII and credentials never logged.

## 8. Code Quality Rules

- Always pass `context.Context` down call chain.
- No global mutable state except explicit safe singletons.
- Timeout for all network/DB calls.
- Structured logging only (JSON).
- Linting and tests must pass before merge.
- Any behavior change requires tests.
- Runtime response texts must be loaded from `strings/*.json` (no hardcoded user-facing text in handlers).
- Concurrency primitives must be goroutine + context based with explicit lifecycle handling.

## 9. Observability

- Logs: structured with `request_id`, `user_id` (if available), route, latency.
- Metrics:
  - request latency
  - WS connections
  - DB query latency
  - Valkey hit/miss
  - queue/fanout lag
- Health endpoints:
  - liveness
  - readiness (checks DB/Valkey dependencies)

## 10. Build & Release

- Build is container-first.
- CI pipeline:
  - fmt/lint
  - unit tests
  - integration tests (DB + Valkey + S3)
  - image build
- Release policy:
  - semantic versioning
  - changelog required
  - rollback-ready deploy

## 11. Definition of Done

A backend task is done only if:

- feature implemented by contract
- tests added/updated
- metrics/logging included
- docs updated (`rules.md`/README/API docs)
- migration and rollback path documented (if schema changed)

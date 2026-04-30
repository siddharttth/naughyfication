# Naughyfication

Reliable Notification Infrastructure for Modern Applications

Naughyfication is a production-grade notification backend for teams that need delivery reliability without rebuilding queues, retries, status tracking, and failure recovery from scratch.

## Why Naughyfication

Notifications fail more often than most systems admit. SMTP providers timeout, workers crash, retries get lost, and debugging delivery state usually turns into grep-driven archaeology.

Naughyfication solves that with a developer-first foundation:

- durable background delivery with PostgreSQL + Redis
- idempotent request handling
- exponential backoff retries
- DLQ support for exhausted jobs
- delivery visibility through logs, metrics, and webhook callbacks

## Features

- Idempotent `POST /v1/notify` requests with PostgreSQL-backed replay state
- Durable async processing with Redis and `asynq`
- Exponential backoff retry engine
- Dead letter queue with manual retry endpoints
- Webhook callbacks with retry and timeout handling
- Prometheus-style metrics endpoint
- Redis-backed per-user rate limiting
- API key authentication
- Dockerized local development
- OpenAPI spec and lightweight docs route
- Multi-channel-ready architecture for future SMS and webhook providers

## Quick Start

### Using Docker

```bash
docker compose up --build
```

The stack starts:

- app on `http://127.0.0.1:8080`
- PostgreSQL on `127.0.0.1:5432`
- Redis on `127.0.0.1:6379`

### Using Go Locally

```bash
cp .env.example .env
# export the values from your env file using your preferred loader
go run ./cmd/server
```

## Environment Configuration

Use [.env.example](/Users/siddharthshekhar/Developer/college-projects/naughtyfication/.env.example) as the starting point for local or hosted deployments.

The current app also supports these additional runtime settings:

```bash
REDIS_ADDRESS=127.0.0.1:6379
REDIS_PASSWORD=
REDIS_DB=0
RATE_LIMIT_RPS=10
RATE_LIMIT_BURST=20
WEBHOOK_TIMEOUT=5s
WEBHOOK_MAX_RETRIES=3
BOOTSTRAP_USER_EMAIL=dev@naughtyfication.local
LOG_LEVEL=info
```

## First Request

```bash
curl -X POST http://127.0.0.1:8080/v1/notify \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: dev-secret-key' \
  -H 'Idempotency-Key: welcome-email-1' \
  -d '{
    "type": "email",
    "to": "user@example.com",
    "subject": "Welcome to Naughyfication",
    "template_body": "Hi {{name}}, your OTP is {{otp}}.",
    "data": {
      "name": "Siddharth",
      "otp": "481902"
    }
  }'
```

Example response:

```json
{
  "id": "notif_xxx",
  "user_id": "user_bootstrap",
  "type": "email",
  "to": "user@example.com",
  "status": "pending"
}
```

## Core Endpoints

- `POST /v1/notify` submit a notification
- `GET /v1/notifications` list notifications
- `GET /v1/notifications/{id}` fetch notification status and logs
- `PUT /v1/webhook` register a delivery callback URL
- `GET /v1/dlq` inspect dead-lettered jobs
- `POST /v1/dlq/{id}/retry` manually retry a dead-lettered job
- `GET /metrics` expose Prometheus-style metrics
- `GET /docs` open lightweight API docs
- `GET /openapi.yaml` fetch the OpenAPI spec

## Architecture

```text
Client -> REST API -> Idempotency + Rate Limit -> PostgreSQL
                                     |
                                     -> Redis / asynq -> Worker -> Provider
                                                          |         |
                                                          |         -> Webhook callback
                                                          -> Retry / DLQ / Delivery logs
```

## Developer Experience

- [OpenAPI spec](/Users/siddharthshekhar/Developer/college-projects/naughtyfication/internal/httpapi/static/openapi.yaml)
- [Node.js example](/Users/siddharthshekhar/Developer/college-projects/naughtyfication/examples/node/send-notification.js)
- [Go example](/Users/siddharthshekhar/Developer/college-projects/naughtyfication/examples/go/send_notification.go)
- [Python example](/Users/siddharthshekhar/Developer/college-projects/naughtyfication/examples/python/send_notification.py)

## Testing

```bash
go test ./...
```

## Current Scope

Today the default production path is email delivery with a provider abstraction ready for SMS and additional channels. If `SMTP_HOST` is empty and `ALLOW_MOCK_DELIVERY=true`, sends are logged for local development instead of being delivered.

## Launch Positioning

Naughyfication is built for indie developers, startups, and SaaS backends that need a reliable notification backbone early, but do not want to keep re-learning the same delivery lessons in production.

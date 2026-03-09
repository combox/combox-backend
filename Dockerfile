FROM golang:1.25.8-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/combox-backend ./cmd/api

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ffmpeg ca-certificates

WORKDIR /app

COPY --from=builder /out/combox-backend /app/combox-backend
COPY migrations /app/migrations
COPY strings /app/strings

ENV APP_ENV=production \
    HTTP_ADDRESS=:8080 \
    DEFAULT_LOCALE=en \
    STRINGS_PATH=/app/strings \
    MIGRATIONS_ENABLED=true \
    MIGRATIONS_PATH=/app/migrations

EXPOSE 8080

ENTRYPOINT ["/app/combox-backend"]

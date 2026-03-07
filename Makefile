SHELL := /bin/bash

APP_NAME := combox-backend
GO ?= go
DOCKER ?= docker
IMAGE ?= combox-backend:dev
EDGE_DC := docker compose -f docker-compose.edge.yml

.PHONY: tidy fmt build run test docker-build docker-run edge-up edge-down edge-logs

tidy:
	$(GO) mod tidy

fmt:
	$(GO) fmt ./...

build:
	$(GO) build -o bin/$(APP_NAME) ./cmd/api

run:
	set -a; source .env; set +a; $(GO) run ./cmd/api

test:
	$(GO) test ./...

docker-build:
	$(DOCKER) build -t $(IMAGE) .

docker-run:
	$(DOCKER) run --rm --env-file .env -p 8080:8080 $(IMAGE)

edge-up:
	@chmod +x ../combox-edge/scripts/init-mtls.sh
	@../combox-edge/scripts/init-mtls.sh init >/dev/null 2>&1 || true
	@../combox-edge/scripts/init-mtls.sh issue-server combox-backend >/dev/null 2>&1 || true
	@if $(EDGE_DC) build --help 2>/dev/null | grep -q -- '--progress'; then \
		$(EDGE_DC) build --progress=plain; \
	else \
		$(EDGE_DC) build; \
	fi
	$(EDGE_DC) up -d --no-build

edge-down:
	$(EDGE_DC) down --remove-orphans

edge-logs:
	$(EDGE_DC) logs -f --tail=120 combox-backend

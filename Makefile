VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

DEV_PG_PORT ?= 55432
DEV_DATABASE_URL ?= postgres://observer:observer@127.0.0.1:$(DEV_PG_PORT)/observer?sslmode=disable
COMPOSE_DEV := docker compose -f deploy/compose.dev.yaml

.PHONY: dev dev-db dev-down test test-unit lint sqlc sqlc-check build dist clean

## dev: start Postgres and run the service on http://localhost:8080
dev: dev-db
	DATABASE_URL='$(DEV_DATABASE_URL)' BLOB_DIR=data/blob REGISTRATION_MODE=open LOG_FORMAT=text LOG_LEVEL=debug go run ./cmd/observer serve

## dev-db: start the development Postgres and wait until it is healthy
dev-db:
	DEV_PG_PORT=$(DEV_PG_PORT) $(COMPOSE_DEV) up -d --wait

## dev-down: stop the development Postgres (keeps its data volume)
dev-down:
	$(COMPOSE_DEV) down

## test: all tests, including the ones that need Postgres
test: dev-db
	TEST_DATABASE_URL='$(DEV_DATABASE_URL)' go test -race -count=1 ./...

## test-unit: tests that need no database (database tests are skipped)
test-unit:
	go test -race -count=1 ./...

lint:
	golangci-lint run

sqlc:
	sqlc generate

## sqlc-check: fail if the generated code is out of date
sqlc-check:
	sqlc diff

build:
	go build -ldflags "$(LDFLAGS)" -o bin/observer ./cmd/observer

# Static binaries for the VPS (no Go toolchain needed there).
dist:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/observer-linux-amd64 ./cmd/observer
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/observer-linux-arm64 ./cmd/observer

clean:
	rm -rf bin dist

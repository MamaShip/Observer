VERSION ?= $(shell git describe --tags --match 'v*' --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

DEV_PG_PORT ?= 55432
DEV_DATABASE_URL ?= postgres://observer:observer@127.0.0.1:$(DEV_PG_PORT)/observer?sslmode=disable
COMPOSE_DEV := docker compose -f deploy/compose.dev.yaml

.PHONY: dev dev-db dev-down test test-unit lint sqlc sqlc-check build dist clean web web-dev web-test web-fonts openapi openapi-check image

## dev: start Postgres and run the service on http://localhost:8080 (with the
## web app as last built by `make web`). Override HTTP_ADDR, BASE_URL and
## SNAP_BASE_URL in the environment to use other ports.
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

## web: build the web app into internal/webui/dist/app (embedded by `go build`)
web: web/node_modules
	cd web && npm run build

web/node_modules: web/package.json web/package-lock.json
	cd web && npm ci
	@touch $@

## web-dev: Vite dev server on http://localhost:5173, proxying /api to
## API_TARGET. Run the service with BASE_URL=http://localhost:5173.
API_TARGET ?= http://127.0.0.1:8080
web-dev: web/node_modules
	cd web && API_TARGET=$(API_TARGET) npm run dev

## web-test: type-check and unit-test the web app
web-test: web/node_modules
	cd web && npm run typecheck && npm test

## web-fonts: re-cut the display font to the interface text (needs fonttools and brotli)
web-fonts:
	python3 -I web/scripts/subset-font.py

## openapi: regenerate the web client's API types from the Go handlers
openapi: web/node_modules
	go run ./cmd/observer openapi > web/openapi.json
	cd web && npm run openapi

## openapi-check: fail if the web client's API types are out of date
openapi-check: openapi
	git diff --exit-code web/src/api/schema.d.ts

build: web
	go build -ldflags "$(LDFLAGS)" -o bin/observer ./cmd/observer

# Static binaries for the VPS (no Go toolchain needed there).
dist: web
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/observer-linux-amd64 ./cmd/observer
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/observer-linux-arm64 ./cmd/observer

## image: build the production image (observer:local), with this machine's GOPROXY
IMAGE ?= observer:local
image:
	docker build --build-arg VERSION=$(VERSION) --build-arg GOPROXY=$(shell go env GOPROXY) -t $(IMAGE) .

clean:
	rm -rf bin dist

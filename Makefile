VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint dist clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/observer ./cmd/observer

test:
	go test -race -count=1 ./...

lint:
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...

# Static binaries for the VPS (no Go toolchain needed there).
dist:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/observer-linux-amd64 ./cmd/observer
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/observer-linux-arm64 ./cmd/observer

clean:
	rm -rf bin dist

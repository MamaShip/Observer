# syntax=docker/dockerfile:1
# The production image: the observer binary with the web app embedded, and
# the PostgreSQL client tools for backups (plan.md §13.1, §13.4).

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN mkdir -p ../internal/webui/dist && npm run build

FROM golang:1.27-alpine AS build
# Builds in mainland China pass --build-arg GOPROXY=https://goproxy.cn,direct.
ARG GOPROXY=https://proxy.golang.org,direct
WORKDIR /src
COPY go.mod go.sum ./
RUN GOPROXY=${GOPROXY} go mod download
COPY . .
COPY --from=web /src/internal/webui/dist/app internal/webui/dist/app
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/observer ./cmd/observer

# pg_dump must match the server's major version (postgres:18 in compose).
FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata postgresql18-client \
 && adduser -D -H -u 10001 observer \
 && mkdir -p /cache && chown observer /cache
COPY --from=build /out/observer /usr/local/bin/observer
USER observer
ENV HTTP_ADDR=:8080 CACHE_DIR=/cache
EXPOSE 8080
ENTRYPOINT ["observer"]
CMD ["serve"]

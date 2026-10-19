# syntax=docker/dockerfile:1
# Three-stage build: React GUI bundle -> static Go server -> small Alpine
# runtime. The SQLite driver (modernc.org/sqlite) is pure Go, so CGO stays
# off and the runtime image carries nothing but the binary, the static
# bundle and a healthcheck client.
#
# Build from the repo root (the context needs cmd/, internal/, frontend/):
#   docker build -f deployments/docker/Dockerfile -t file-explorer:dev .
# or just: make docker-build

# ---- GUI bundle -----------------------------------------------------------
FROM node:22-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ---- server binary ---------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/server ./cmd/server

# ---- runtime ---------------------------------------------------------------
FROM alpine:3.21
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=web /src/frontend/dist /app/web
# everything mutable lives under /data: the file tree (under /data/root)
# and the SQLite sidecar. Mount a volume there; see docker-compose.yml.
RUN mkdir -p /data/root
ENV ADDR=":8080" \
    ROOT="/data/root" \
    DB_PATH="/data/perms.db" \
    ADMIN="system:admin:admin" \
    WEB_DIR="/app/web" \
    LOG_JSON="true"
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO /dev/null "http://127.0.0.1${ADDR}/" || exit 1
ENTRYPOINT ["/app/server"]

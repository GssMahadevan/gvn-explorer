# Docker deployment

Single Go binary + static React bundle, all state in one mounted directory.

## Quick start

```sh
# from the repo root
make docker-up          # build + start, app on http://localhost:8080
make docker-down
make docker-logs
```

If :8080 is taken on your machine, pick another host port:
`APP_PORT=8098 make docker-up` (and `APP_PORT=8098 make docker-test`).

Without make:

```sh
cd deployments/docker
docker compose up --build -d
```

First login: tenant `system`, user `admin`, password `admin` — set by the
`ADMIN` env var, and **only honored while the database has no users**.
Change it (or add tenants/users) in the Users panel, or override `ADMIN`
before the very first start.

## Image layout (built by Dockerfile, 3 stages)

| stage | base | produces |
|---|---|---|
| `web` | node:22-alpine | `npm ci && npm run build` → `frontend/dist` static bundle |
| `build` | golang:1.26-alpine | `CGO_ENABLED=0` server binary (SQLite is `modernc.org/sqlite`, pure Go) |
| runtime | alpine:3.21 | `/app/server` + `/app/web` (GUI bundle); healthcheck `wget /` |

The old vanilla `web/` fallback is intentionally **not** in the image; the
image always ships the React GUI.

## Volumes — what to mount

Everything mutable lives under `/data`; mount that and nothing else:

| container path | content | host (compose default) |
|---|---|---|
| `/data/root` | the file tree the explorer CRUDs (server `-root`) | `./data/root` |
| `/data` | `perms.db` (users/groups/ACLs/sessions/**config drafts**) at `DB_PATH` | `./data/db` |

`./data` is gitignored. Losing it = losing accounts and ACLs (the file
tree itself lives in `./data/root`). With plain `docker run`:

```sh
docker run -d --name fx -p 8080:8080 \
  -v "$PWD/my-root:/data/root" -v "$PWD/my-db:/data" \
  -e ADMIN='system:admin:change-me' file-explorer:dev
```

## Configuration (env → flags)

All flags have env defaults; `docker run -e` or compose `environment` is
enough (see `cmd/server/main.go` for the full list). Bad numeric env
values fail at startup instead of falling back silently.

| env | default in image | meaning |
|---|---|---|
| `ADDR` | `:8080` | listen address |
| `ROOT` | `/data/root` | sandbox root for the file API |
| `DB_PATH` | `/data/perms.db` | SQLite file |
| `ADMIN` | `system:admin:admin` | bootstrap super admin `tenant:user:pass`, first start only |
| `WEB_DIR` | `/app/web` | static GUI dir (`""` disables GUI) |
| `MAX_MB` | `10` | max file size for read/write/edit |
| `RPS` / `R_BURST` | `20` / `60` | per-principal API rate limit (`RPS=0` disables) |
| `AUTH_RPS` / `AUTH_BURST` | `3` / `10` | per-IP login rate limit (`AUTH_RPS=0` disables) |
| `LOG_LEVEL` / `LOG_JSON` | `info` / `true` | structured logging |

`HUB.md` in this directory is the README published with the image to
Docker Hub (end-user view: pull + run + configure); this file is the
repo-side view (build + develop + deploy locally). Keep the two config
tables in sync when adding flags.

## Postgres (future) and k8s

The compose file already defines a `postgres` service behind a profile —
start it with `docker compose --profile postgres up -d`. It becomes real
once the db layer gains a Postgres backend (`internal/db/migrations/
postgres/` already carries the 0001 + 0003 dialect — 0002 is not ported
yet, that is part of the wiring work); until then the server only speaks
SQLite. When wiring it up, add `DB_DRIVER`/`DATABASE_URL` handling in
`cmd/server/main.go` and an `app → postgres` dependency here.

A k8s deployment is the next step after that: the ConfigMap covers the
env vars, two PersistentVolumeClaims map to `/data/root` and `/data`,
and the Postgres service becomes a StatefulSet — see
`deployments/k8s/README.md`. The healthcheck endpoint is `GET /`
(readiness on `/` is fine; it only serves the static bundle).

## Smoke test after start

```sh
BASE_URL=http://localhost:8080 ADMIN_TENANT=system ADMIN_USER=admin ADMIN_PASS=admin \
  ./scripts/test_files.sh   # full 323-case API regression, safe to re-run
```

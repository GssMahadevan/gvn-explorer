# File explorer in Docker

One static Go binary + static React bundle, all state under one mounted
directory. This file is the repo-side guide: build the image, run it
locally, operate it. `HUB.md` next to it is the end-user README
published to Docker Hub (pull + run + configure); keep the two config
tables in sync when adding flags.

## What you get

- A fast, server-paged web file browser (works on phones): browse,
  search, mkdir, upload, rename, download.
- A file page that opens text in a real editor (CodeMirror; markdown
  gets a formatting toolbar and live preview), and opens `.yml`/`.yaml`
  in a structured editor that round-trips **comments and notes**
  unchanged. Edits save as drafts in the database and hit your disk
  only on **publish** (validated first). Binaries just download — the
  open-vs-download decision is made from the file name, GitHub-style.
- Real per-path permissions: read/write grants to users or groups with
  nearest-ancestor inheritance, deny by default, optional expiry,
  one-click suspension. Multi-tenant: several orgs share one tree;
  each admin manages only their own users/groups. API keys give
  headless devices the same permissions model.
- A forensic audit trail of every login, account change, grant and
  published file — browsable in the GUI as the **Events** tab or from
  the **event log** drawer in the flash bar (filter by action/time
  range, search, sort, per-row structured details). Structured JSON
  server logs with a request id on every line.

## Quick start

```sh
# from the repo root
make docker-up          # build + start, app on http://localhost:8080
make docker-logs
make docker-down
```

If :8080 is taken on your host (common — many stacks use it), pick
another host port: `APP_PORT=8098 make docker-up`
(and `APP_PORT=8098 make docker-test`).

Without make:

```sh
cd deployments/docker
docker compose up --build -d
```

First login: tenant `system`, user `admin`, password `admin` — set by
the `ADMIN` env var, and **only honored while the database has no
users**. Change it (or add tenants/users) in the Users panel, or
override `ADMIN` before the very first start.

## End-user guides

Once the container runs, the app itself is documented under
[`docs/`](docs/) — written for the people who *use* a deployment, not
the people who build it. Every example is plain `curl` against
`http://localhost:8080`, so it runs as-is against a running container:

| doc | covers |
|---|---|
| [docs/tenants-users-groups.md](docs/tenants-users-groups.md) | organizations (tenants), accounts, device users, groups; suspending vs expiring vs deleting |
| [docs/permissions.md](docs/permissions.md) | grants, inheritance rules, zero-grant subtree exclusion, expiring customer shares, checking effective access |
| [docs/api-keys.md](docs/api-keys.md) | devices & CI: key provisioning, labels/rotation, revocation, `last_used_at` fleet health, ETag/304 firmware polling, device writes/uploads |
| [docs/api.md](docs/api.md) | the REST API for non-browser clients: both auth modes, CSRF rules, error envelope, the shared list/paging/search/sort contract, full file & admin endpoint tables |

The GUI follows these same concepts 1:1 (Tenants / Users / Groups /
Permissions / Events panels), so the guides apply whether you click or
script. The exhaustive machine-verified contract remains the root
`README.md` plus the curl suite in `scripts/test_files.sh`.

## Image layout (built by Dockerfile, 3 stages)

| stage | base | produces |
|---|---|---|
| `web` | node:22-alpine | `npm ci && npm run build` → `frontend/dist` static bundle |
| `build` | golang:1.26-alpine | `CGO_ENABLED=0` server binary (SQLite is `modernc.org/sqlite`, pure Go) |
| runtime | alpine:3.21 | `/app/server` + `/app/web` (GUI bundle); healthcheck `wget /` |

Total image ~25 MB (~10 MB compressed). The old vanilla `web/`
fallback is intentionally **not** in the image; the image always ships
the React GUI. No build tools, package managers or shell extras in the
runtime layer.

## Volumes — what to mount

Everything mutable lives under `/data`; mount that and nothing else:

| container path | content | host (compose default) |
|---|---|---|
| `/data/root` | the file tree the explorer CRUDs (server `-root`) | `./data/root` |
| `/data` | `perms.db` (users/groups/ACLs/sessions/audit/**config drafts**) at `DB_PATH` | `./data/db` |

`./data` is gitignored. Backup = copy both directories (container
stopped, or `sqlite3 perms.db ".backup"` while running); restore =
mount them back. Losing `/data` = losing accounts, grants and audit
(the file tree itself lives in `./data/root` and survives). With plain
`docker run`:

```sh
docker run -d --name fx -p 8080:8080 \
  -v "$PWD/my-root:/data/root" -v "$PWD/my-db:/data" \
  -e ADMIN='system:admin:change-me' file-explorer:dev
```

## Configuration (env → flags)

All flags have env defaults; `docker run -e` or compose `environment`
is enough (see `cmd/server/main.go` for the full list). Bad numeric
env values fail at startup instead of falling back silently.

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

Everything else — tenants, users, groups, API keys, grants, expiries,
suspension — is *runtime data* in `perms.db`, managed through the GUI
or the API, never through env.

The server speaks plain HTTP (`SameSite=Lax` cookies), so terminate
TLS at your reverse proxy; give it `client_max_body_size` ≥ `MAX_MB`
so uploads pass through. Every request carries an `X-Request-Id` that
appears in both the API responses and the JSON logs.

## Upgrades

Migrations run automatically at startup and are **not reversed on
rollback**: before a version bump, stop the container and copy
`/data`. `GET /` is the health endpoint (used by the image's
HEALTHCHECK and fine for k8s readiness).

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
`deployments/k8s/README.md`.

## Smoke test after start

```sh
APP_PORT=8098 make docker-test
# or against any running instance:
BASE_URL=http://localhost:8098 ADMIN_TENANT=system ADMIN_USER=admin ADMIN_PASS=admin \
  ./scripts/test_files.sh   # full 323-case API regression, safe to re-run
```

NB: `test_files.sh` defaults to `http://localhost:8080` — if some other
stack owns that port on your machine, always pass `BASE_URL` (or
`APP_PORT` through the make target), or you will be testing that stack.

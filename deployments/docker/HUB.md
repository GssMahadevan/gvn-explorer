# file-explorer

A self-hosted web file explorer: browse, create, edit and organize files
under a root directory from any browser or phone — with per-path user and
group permissions, multi-tenant accounts, expiring access, API keys for
devices, an append-only audit log, and a structured YAML editor that
preserves comments. One static binary, one SQLite file, one container.

Built for the boring-but-real job of letting many people (and IoT boxes)
share a file tree safely: who may read or write which path, for how long,
and who changed what.

```
docker run -d --name file-explorer \
  -p 8080:8080 \
  -v "$PWD/my-files:/data/root" \
  -v "$PWD/state:/data" \
  -e ADMIN='acme:admin:change-me' \
  yourorg/file-explorer:1
```

Open http://localhost:8080 and sign in with tenant `acme`, user `admin`,
password `change-me`.

## Why run this

- **Files, in a browser** — fast server-paged listings, search, rename,
  move-free sandbox browsing, upload, download, mkdir; works on phones.
- **Real permissions** — grant read/write per path to users or groups;
  nearest-ancestor inheritance; deny by default; access can carry an
  expiry. Suspension is a one-click kill switch.
- **Multi-tenant** — several organizations share one file tree; each
  admin only manages their own principals. A super admin bridges tenants.
- **YAML done right** — config files (`.yml`/`.yaml`) open a structured
  tree editor with a draft/publish workflow; comments, notes and quoting
  survive the round trip. Other text files get a proper code editor with
  a markdown toolbar. Binaries just download — like GitHub, the decision
  is made from the name.
- **Audit** — every login, account change, grant and published file is
  appended to an event log (actor, target, path, structured details)
  and browsable in the UI: a full Events table with search, sort, and
  action/time-range filters, plus an event-log drawer reachable from
  anywhere. Server logs are structured JSON with a request id per line.
- **API-first** — the whole GUI is a thin client over a documented REST
  API; API keys give headless devices the same permissions model.
- **Built-in documentation** — the user guide (accounts, permissions,
  API keys, the REST API for scripts) ships inside the image: every
  user opens it from the app menu (Help) or browses the read-only
  `help/` folder in the file explorer.

## Docker Compose

```yaml
services:
  file-explorer:
    image: yourorg/file-explorer:1
    ports:
      - "8080:8080"
    volumes:
      - ./files:/data/root   # the file tree you expose
      - ./state:/data        # accounts, ACLs, audit, drafts  (back this up!)
    environment:
      ADMIN: "acme:admin:change-me" # first start only — pick a real password
      LOG_LEVEL: "info"
    restart: unless-stopped
```

## Configuration

Everything is configured by environment variables (command-line flags
also exist and win over env). There is no config file to edit inside
the image.

| env | default | meaning |
|---|---|---|
| `ADMIN` | `system:admin:admin` | bootstrap super admin `tenant:user:pass`; **applied only while the database has no users** |
| `ROOT` | `/data/root` | directory exposed to the file explorer (sandbox root) |
| `DB_PATH` | `/data/perms.db` | SQLite file for users/groups/ACLs/sessions/audit |
| `ADDR` | `:8080` | listen address (the image listens on 8080 by convention; map any host port) |
| `WEB_DIR` | `/app/web` | static GUI directory; set to `""` for API-only mode |
| `MAX_MB` | `10` | max file size accepted by read/write/edit endpoints |
| `RPS` | `20` | requests/sec per authenticated principal (0 disables) |
| `R_BURST` | `60` | burst allowance for `RPS` |
| `AUTH_RPS` | `3` | login requests/sec per source IP (0 disables) |
| `AUTH_BURST` | `10` | burst allowance for `AUTH_RPS` |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `LOG_JSON` | `true` | JSON log lines (`false` = human-readable) |

Bad values fail loudly at startup (`RPS: invalid number "ozone"`) rather
than being silently ignored.

Everything else — tenants, users, groups, API keys, grants, expiries,
suspension — is *runtime data*, managed through the GUI or the API, not
environment variables.

## First login

The `ADMIN` variable creates the first super admin **only when the
database is empty**. After the first start, change passwords and create
tenants/users in the Users panel; re-upping `ADMIN` afterwards does
nothing. (If you lost the password: stop the container, snapshot/remove
the state directory only if you accept losing accounts, or reseed against
a fresh state directory.)

## Volumes, backup, restore

Two mounts, that's the whole state:

| container path | content |
|---|---|
| `/data/root` | the file tree the app manages (any pre-existing tree works — the app never rewrites your layout) |
| `/data` | `perms.db`: accounts, ACLs, sessions, audit log, unpublished YAML drafts |

- **Backup** = copy both directories (do it with the container stopped,
  or back the SQLite file up with `sqlite3 ... ".backup"` while running).
- **Restore** = mount them back at the same paths.
- Losing `/data` = losing accounts/permissions/audit (files survive).
  Losing `/data/root` = the opposite.

## Upgrades

The image is versioned as `:major.minor.patch` and `:latest`. The server
applies its SQLite migrations automatically at startup, and migrations
are **not reversed on rollback** — before a `major`/`minor` bump, stop
the container and copy `/data`. Patch releases are drop-in. `GET /` is
the health endpoint (also used by the image's HEALTHCHECK).

## TLS and reverse proxies

The server speaks plain HTTP (cookie sessions with `SameSite=Lax`), so
terminate TLS at your reverse proxy:

```yaml
# nginx sketch
location / {
  proxy_pass http://127.0.0.1:8080;
  client_max_body_size 16m;   # >= MAX_MB, uploads go through as bodies
}
```

The API accepts large bodies up to `MAX_MB`; make sure the proxy allows
at least that. Requests carry an `X-Request-Id` end to end, which also
appears in the access logs — handy when debugging through a proxy.

## What it is not (yet)

- **Single instance** — one process, one SQLite file, one local mount.
  It is not a distributed FS; mount an NFS/SMB/CIFS tree at `/data/root`
  if the bytes must live elsewhere.
- **Postgres** is a planned backend (a `postgres` service profile ships
  with the repo), not wired into the server yet.
- **No built-in TLS/OIDC/LDAP** — plain-HTTP + local accounts behind
  your proxy and auth, if you have one.

## Image internals

Multi-stage build: Node builds the React (Ant Design) GUI into static
files, Go compiles a `CGO_ENABLED=0` static binary (pure-Go SQLite via
`modernc.org/sqlite`), and the runtime is Alpine with just `/app/server`
+ `/app/web` — ~25 MB. Source, API contract, and the 300-case curl
regression suite live at https://github.com/yourorg/file-explorer.

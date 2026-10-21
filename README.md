# gvn-explorer — file explorer (Go + React)

Multi-tenant web file explorer with a Go backend. Files live on the real
filesystem under one **root path**; SQLite stores the platform-independent
permission model (tenants, users, groups, per-path ACL grants, sessions) —
never the file contents.

Tenants namespace *principals* (usernames and group names are unique per
tenant); the file tree itself is shared, so isolation between tenants is
enforced purely by ACL grants.

> Schema note: the tenant schema is **not compatible** with pre-tenant
> databases. Delete the old `.db` file; the server recreates it.

**Just want to try it?** Grab a prebuilt archive from the
[Releases](https://github.com/GssMahadevan/gvn-explorer/releases) page
(linux/windows/macOS × amd64/arm64, ~5 MB each), unpack, run the binary,
open https://localhost:8080 (self-signed cert: accept the one-time
browser warning) and sign in as `system / admin / admin`.
No build tools, no Docker; the archive's own README explains the rest.
Got an old Android phone? The APK in the Releases wraps the same server
and turns the phone into the box it runs on — see [`android/README.md`](android/README.md).
Container? `ghcr.io/gssmahadevan/gvn-explorer:latest` (multi-arch) — pull
and run below under *Run from the published image*.

Prefer to watch first? Two narrated walkthroughs live in `docs/`:
[demo-standalone.mp4](docs/demo-standalone.mp4) (~45 s: download, run,
sign in — the whole standalone setup) and [demo-docker.mp4](docs/demo-docker.mp4)
(~3.5 min: the full feature tour — users + API keys, groups, tenants,
file create/edit, markdown, the YAML tree editor, per-path permission
grants, built-in help). They are real screencasts — the same binary and
image we ship, driven by `make demo`.

## Run

```sh
GO111MODULE=on go build -o bin/server ./cmd/server
./bin/server -addr :8080 -root ./data -db perms.db -admin system:admin:admin
```

Zero flags now means **HTTPS**: the server generates a self-signed cert
in memory and serves `https://localhost:8080` (browsers warn once — that's
inherent to self-signed). Plain HTTP is the explicit opt-out:
`-tls-self=false`. Full TLS story under *HTTPS* below.

Flags (env fallbacks in caps; flags win):

| flag    | env       | meaning                                              |
|---------|-----------|------------------------------------------------------|
| -root   | ROOT      | directory exposed to the explorer (created if absent) |
| -db     | DB_PATH   | SQLite file for tenants/users/groups/ACLs/sessions   |
| -admin  | ADMIN     | bootstrap `tenant:user:pass`, applied only when no users exist |
| -web    | WEB_DIR   | static GUI dir (empty string disables)               |
| -max-mb | —         | read/write/upload size cap (default 10)              |
| -rps    | —         | requests/sec per principal on API endpoints, 0 disables rate limiting (default 20) |
| -rburst | —         | burst allowance for -rps (default 60)                |
| -auth-rps | —       | login requests/sec per source IP, 0 disables (default 3) |
| -auth-burst | —     | burst allowance for -auth-rps (default 10)           |
| -auth-concurrency | — | max simultaneous password hashes (login + join); extras get an instant `503` + `Retry-After` (default 4) |
| -log-level | LOG_LEVEL | debug|info|warn|error (default info)               |
| -log-json  | LOG_JSON  | JSON log lines, one object per line (default true; false = human text) |
| -tls-cert / -tls-key | TLS_CERT / TLS_KEY | serve HTTPS from this cert/key pair instead of the default self-signed cert (must be set together) |
| -tls-self  | TLS_SELF  | in-memory self-signed cert — **default true**: HTTPS out of the box; `-tls-self=false` serves plain HTTP |
| -cookie-secure | COOKIE_SECURE | session cookie `Secure` flag: auto (on while serving TLS) / on / off |
| -hsts-days | HSTS_DAYS | `Strict-Transport-Security` max-age in days on TLS responses (0 disables) |

The bootstrap flag creates the tenant *and* a **platform super admin** inside
it. All paths are logical and rooted at `-root`: no `..`, absolute paths, or
symlink traversal can escape it.

## Run from the published image (ghcr.io)

The image is published multi-arch (linux/amd64 + linux/arm64) on GitHub
Container Registry — `latest` tracks the release tag. No registry login is
needed for a public package:

```sh
docker pull ghcr.io/gssmahadevan/gvn-explorer:latest
docker run -d --name gvn -p 8080:8080 \
  -v "$PWD/files:/data/root" \
  -v "$PWD/state:/data" \
  -e ADMIN='system:admin:change-me' \
  ghcr.io/gssmahadevan/gvn-explorer:latest
```

Then open **https://localhost:8080** — the image serves HTTPS with a
self-signed cert by default (accept the browser's one-time warning; add
`-e TLS_SELF=false` to the run command for plain HTTP). The two mounts
are the whole design:

| mount      | what lives there                                                    |
|------------|----------------------------------------------------------------------|
| `/data/root` | your **file tree** — plain files on the host, visible/editable outside the app |
| `/data`      | `perms.db`: tenants/users/groups/ACLs/sessions/API keys/audit/drafts — the **only state to back up** |

Image defaults are `ROOT=/data/root`, `DB_PATH=/data/perms.db`,
`ADMIN=system:admin:admin`, `LOG_JSON=true`, `TLS_SELF=true` (self-signed
HTTPS); the container health-checks
itself and logs JSON to stdout (`docker logs -f gvn`). Every server flag has
its uppercase env counterpart (same table as above) — e.g. `-e ROOT=/data/root`
is the default already, so pointing the app at another host directory is just
a different `-v`. `ADMIN` only applies while the database is empty (first
start), so set it before the very first run in production. Upgrade =
`docker pull` + recreate the container; your volumes carry everything.

A `docker-compose.yml` (same two volumes, plus a Postgres-ready shape) and
the full operator notes live in [`deployments/docker/README.md`](deployments/docker/README.md);
`make docker-up` builds the same image locally from source instead.

One database note: the server runs SQLite in WAL mode, so a *running*
database also has `perms.db-wal`/`perms.db-shm` beside it — with the
server stopped the `-wal` content is folded back in and `perms.db`
alone is a complete backup (a live copy should include the sidecars).

## HTTPS (self-signed or your own cert)

**HTTPS is the default.** With no flags at all the server generates a
self-signed certificate in memory and serves encrypted traffic; old
`http://` links still work via an instant redirect (see below). Plain
HTTP is the explicit opt-out:

```sh
./bin/server                    # → https on -addr, self-signed cert (the default)
./bin/server -tls-self=false    # → plain HTTP, exactly as in pre-TLS versions
```

The container behaves the same (`TLS_SELF=false` to opt out). Browsers show
a one-time warning for any self-signed cert; `curl -k` or `--cacert`
covers scripts.

For a *stable* cert — one that survives restarts and can be added to the
OS trust store so the warning disappears — generate one on disk and point
the server at it:

```sh
./scripts/gen-selfsigned.sh                              # → tls/server.crt + tls/server.key
./bin/server -tls-cert tls/server.crt -tls-key tls/server.key
```

The generated cert covers `localhost`, the machine hostname, `127.0.0.1`
and `::1` and is its own CA, so clients can verify it without switching
off checking: `curl --cacert tls/server.crt https://localhost:8080/healthz`.
(Serve behind a real cert the same way — `-tls-cert/-tls-key` take any
pair, the built-in TLS needs nothing else.)

Behaviour when TLS is on:

- **Plain HTTP keeps working on the same port.** The first byte of each
  connection decides: TLS records proceed, plaintext HTTP requests get an
  instant **308** to the `https://` URL of the same host:port:path. 308
  preserves method and body, so even an old `http://` bookmark with a POST
  re-sends correctly; `Location` reuses the request's `Host`, so port and
  hostname survive.
- The session cookie picks up **`Secure`** automatically
  (`-cookie-secure=on|off` overrides — set `on` behind a TLS-terminating
  proxy, which is still fully supported).
- `-hsts-days 365` opts into HSTS; off by default because a bare-IP
  self-signed setup pinned in the browser is awkward to undo.
- TLS 1.2 and up only.

Heads-up for existing deployments: because the default flipped, anything
that depended on plain HTTP (proxies, scripts, monitoring) should set
`TLS_SELF=false` explicitly — with it the server is byte-identical to the
pre-TLS listener. `make test-api` (HTTP) plus `make test-tls` (default
HTTPS, the opt-out, both cert paths, the 308, Secure cookies, security
headers) keep both sides honest. What the app does against the standard
*web attacks* — XSS, CSRF, clickjacking, path traversal, SQL injection,
MIME confusion — is summarized under *Web attack surface* further down.

## Auth

`POST /api/v1/auth/login {"tenant","username","password"}` sets an HttpOnly
session cookie (`sid`, 24h, stored in SQLite). `POST /api/v1/auth/logout`,
`GET /api/v1/auth/me`. Passwords are bcrypt hashes.

Two admin roles:

| role                 | grants                                             |
|----------------------|----------------------------------------------------|
| platform super admin | tenant CRUD, cross-tenant user/group/grant management, **bypasses all file ACLs** |
| tenant admin         | manages users/groups/grants **of its own tenant only**; no file bypass — file access follows grants |

The super admin is only ever created by the bootstrap flag; the API can set
`is_admin` (tenant admin) but never `is_super`.

CSRF hardening: state-changing requests must be same-origin (Origin header
checked) and must not use form-style content types — send
`-H 'Content-Type: application/json'` with every request that has a body
(`curl -d` alone defaults to form-urlencoded and is refused with 403 csrf).
Cookies are `SameSite=Lax`.

## Permission model

Grants are rows `(path, principal_type, principal, r/w/d bits)`. Four
principal types exist: `user` and `group` (a person or a named set of people,
always resolved inside one tenant, so the same name in two tenants is two
distinct grant subjects), and two **pseudo-principals**: `tenant` addresses
every member of one tenant at once, and `all` addresses every signed-in user
of the instance. Effective perms for a path = **nearest ancestor** with an
explicit grant for you: at each level `user` wins over `group` wins over
`tenant` wins over `all` (the more specific layer shadows the broader ones);
multiple group grants union; no grant anywhere = deny. Example:

```sh
curl -b jar -X PUT localhost:8080/api/v1/perms \
  -H 'Content-Type: application/json' \
  -d '{"path":"/docs","principal_type":"group","principal":"readers","tenant":"acme","read":true}'
```

gives `acme/readers` read on `/docs` and everything below, unless overridden
deeper. `tenant` defaults to the caller's tenant; tenant admins may only ever
target their own. A `tenant` grant is addressed by the tenant's name
(`{"principal_type":"tenant","principal":"acme"}`), and `all` takes no name
(`{"principal_type":"all","principal":"*"}`). `GET /api/v1/fs` hides entries
the caller cannot read. A **zero grant** (everything false) on a nearer path
intentionally *excludes* a subtree: inheritance stops at the nearest ancestor
with any grant row for the principal, so `{"path":"/secret","principal_type":"group","principal":"readers","read":false}`
removes `/secret` from readers' listings even when they hold read on `/` —
the same exclusion works against a `tenant` or `all` row for exactly one
user.

Group membership is always resolved inside the group's own tenant — adding
"alice" to `globex/staff` can never enroll `acme/alice`.

## Tenant home directories

Creating a tenant (`POST /api/v1/tenants`) provisions a home directory at
`/{tenant-name}` and a read grant for `principal_type:tenant` on it — an
ordinary, visible, revocable grant row, so the tenant's own admin can
upgrade it to `rw` or revoke it like any other. The home is then a hard
**partition**: only that tenant's members (and the super admin) can reach
anything inside it, whatever grant rows elsewhere say; to everyone else the
directory does not even appear in listings. Inside a home, every grant must
address that tenant's own members — grants for foreign users, foreign
tenants or `all` are refused with 422 there, even for the super admin.
Outside homes nothing is world-readable by default: shared zones open only
through explicit per-path `tenant`/`all` grants. A top-level directory that
already matches a tenant's name becomes that tenant's home at startup.
New tenant names must be able to back a directory: lowercase `a-z0-9-`,
starting alphanumerically, ≤ 32 chars, and `help` is reserved (422
otherwise); pre-existing tenants with other names keep working, their
top-level directories are simply ordinary shared directories.


## Expiring access (customer shares)

Both grants and whole accounts can carry an expiry — the poor-man's-Dropbox
flow: hand a customer read on `/deliverables` for 3 days, then everything
they touch goes dark with **no cron or extra infra**, because every request
re-evaluates expiry in SQL.

- `PUT /api/v1/perms` accepts `"expires_days": 3` (from now, fractions ok) or
  `"expires_at": "2026-10-22T00:00:00Z"` (exact cutoff, wins over days);
  omit both = never. `GET /api/v1/perms` still lists lapsed grants with
  `"expired": true` so admins can see and renew them (renew = re-PUT).
- `POST /api/v1/users` accepts the same two fields: a customer account with a
  lifespan. Once lapsed, login is rejected (indistinguishable from a wrong
  password) and **live sessions die on the next request**.
- An expired grant is treated as if absent entirely: it grants no bits and
  does **not** stop inheritance, so a customer with read on `/` keeps seeing
  the tree after a per-folder share lapses. For a hard cutoff, expire the
  account itself.

```sh
# customer gets 3 days:
curl -b jar -X POST localhost:8080/api/v1/users -H 'Content-Type: application/json' \
  -d '{"tenant":"acme","username":"cust42","password":"s3cret","expires_days":3}'
curl -b jar -X PUT localhost:8080/api/v1/perms -H 'Content-Type: application/json' \
  -d '{"path":"/deliverables","principal_type":"user","principal":"cust42","tenant":"acme","read":true,"expires_days":3}'
```

Databases created before this feature are migrated forward automatically at
startup (see *Schema & migrations*).

## Device access (API keys)

Non-interactive callers — IoT devices (ESP8266/ESP32 pulling firmware),
scripts, CI delivering customer artifacts (tgz bundles, docker-image tarballs
distributed via `docker load` instead of Docker Hub).

A device is just a **user with no password**: `POST /api/v1/users
{"tenant":"home1","username":"node7"}` stores an unknowable random hash, so
`/auth/login` structurally can never succeed for it. Access comes from
**auth factors** — one row per (user, method, label) in `auth_factors`:

| endpoint | meaning |
|----------|---------|
| `GET /api/v1/users/{id}/factors` | list factors (metadata only, never the secret) + `last_used_at` fleet health |
| `POST /api/v1/users/{id}/factors` `{"method":"apikey","label":"v1","expires_days":90}` | generate key; **plaintext returned exactly once**, DB keeps only sha256 |
| `DELETE /api/v1/users/{id}/factors/{fid}` | revoke — bites on the very next request (no session to expire) |

`label` lets an old and new key coexist while a firmware fleet rotates.
Factors accept the same `expires_days`/`expires_at` expiry as grants/accounts,
and an **expired account outranks a live key**. Tenant admins may manage
factors only for their own tenant's users.

Then the device calls any file endpoint with a bearer token — no login, no
cookie, and because browsers cannot attach an `Authorization` header
cross-site, these requests are CSRF-immune by construction:

```sh
curl -H "Authorization: Bearer $KEY" 'localhost:8080/api/v1/fs/raw?path=/firmware/modelx.bin'
```

ACLs apply exactly as for human users — grant the device `r` on just
`/firmware`, and nothing else in the tree even exists for it.

`/fs/raw` sends an `ETag` (size+modtime); firmware should poll with
`If-None-Match` and get a bodiless `304` on unchanged images — prefer this
over `If-Modified-Since` since ESP clocks are unreliable.

The factor table also **reserves `ed25519` and `oauth2`** methods (the `alg`
column names the key algorithm or the provider: google, entra, github...).
Provisioning either today returns a clean `422 not_implemented`; OAuth2
subjects will live as factors too, so social login won't need a schema change.

Rate limiting is built in (golang.org/x/time/rate token buckets, in-memory —
no extra infra): every authenticated principal gets `-rps`/`-rburst`
(default 20/60 req/s, so one compromised or runaway device cannot hammer the
download endpoint), and login is bucketed per source IP at `-auth-rps`
(default 3/s) against password brute force. Over the limit returns `429`
with `Retry-After`. Per-IP buckets stop one attacker, but N different IPs
can still each pay a bcrypt; on a router-class machine that aggregate is
bounded by `-auth-concurrency` (default 4 simultaneous hashes — login and
join share the slots; when they are all busy a new auth request is refused
with `503` + `Retry-After` instead of queuing). Buckets reset on restart,
which is fine at this scale.

## Device pairing (QR join)

The "enter the code on the TV" flow: an admin opens a short-lived,
single-use invite; the other device scans a QR code or types a 7-character
code at the public `/join` page — which **creates a plain account in the
inviting tenant and signs the browser in on the spot**. No account
sign-up dance on a small screen.

| endpoint | meaning |
|----------|---------|
| `POST /api/v1/pairings` `{"description","ttl_seconds?","username?","apikey?","expires_days?"}` | open an invite; the reply carries `token` + `code` **shown exactly once** |
| `GET /api/v1/pairings` | list invites (metadata only: pending/used/expired/revoked; same paging/search contract as every list) |
| `DELETE /api/v1/pairings/{id}` | revoke an unused invite — a stamp, not a delete; history survives |
| `POST /api/v1/join` `{"token"}` or `{"code"}` | public, rate-limited: creates the account (auto-named `dev-…` unless a username was preset), returns `{"user","password","apikey?"}` and sets the session cookie |

Only **sha256 hashes** of the token and the short code are stored; the QR
link puts the token in the URL `#fragment`, so it never reaches any
server log. Single-use is an atomic claim — concurrent joins cannot
double-redeem. Claim errors answer distinctly: unknown `404`, expired
`410`, used-or-revoked `409`, both secrets in one request `422`. TTL
defaults to 10 minutes (30 s … 24 h), the generated password and the
optional API key are shown once on the join screen, and the new account
deliberately carries **zero grants** until someone grants it paths.

## Suspension (`active` flag)

Tenants, users and groups can be **deactivated without deleting them** —
the kill switch for an offboarded customer, a paused account or a
quarantined device. Rows, grants, factors and history all survive; every
action in the system stops:

- user inactive → login refused (indistinguishable from a wrong password),
  live sessions die on the *next request*, bearer API keys are rejected, and
  admin actions are impossible even with a stolen cookie
- group inactive → every ACL grant *through* it is ignored, exactly like an
  expired grant; direct user grants are untouched
- tenant inactive → the whole tenant goes dark at once: no logins, live
  sessions and API keys of its principals stop working, but the rows (and
  every grant targeting them) are untouched and come back on reinstatement
- reinstatement (`{"active":true}`) restores everything instantly, because
  nothing was ever destroyed

```sh
curl -b jar -X POST localhost:8080/api/v1/users/7/active \
  -H 'Content-Type: application/json' -d '{"active":false}'
curl -b jar -X POST localhost:8080/api/v1/groups/3/active \
  -H 'Content-Type: application/json' -d '{"active":true}'
curl -b jar -X POST localhost:8080/api/v1/tenants/2/active \
  -H 'Content-Type: application/json' -d '{"active":false}'   # super admin only
```

Admin rights, tenant-scoped; tenant suspension is super-admin only. Guard
rails: nobody can suspend *their own* account, and a super admin cannot
suspend *their own tenant* (422) — lockouts are always recoverable. Lists
(`/api/v1/users`, `/api/v1/groups`, `/api/v1/tenants`, and `/api/v1/perms`
grant rows via `principal_active`) flag inactive principals, and the entity
lists accept `?active=true|false` so a suspended-accounts view is one
query.

Distinct from expiry: `expires_at` is a scheduled lapse the admin walks back;
`active=false` is an immediate suspension. Both block everything.

## Audit trail (events table)

Every authz/authn DML is appended to an `events` table — forensics first:
who changed what, to whom, from which IP, when. Recorded actions:
`tenant_create/update/delete/active`, `user_create/update/delete/password/active`,
`group_create/update/delete/active`, `member_add/remove`, `perm_set/delete`,
`factor_create/delete`, `login`, `login_failed`, `fs_commit` (published file writes),
`import_apply` (one per applied JSON document, see *Portable JSON documents*).

Design rules:

- **append-only, no FKs** — events outlive the deletion of their actors and
  targets; deleted users/grants are named in `target_name` snapshots
- **no secrets, ever** — password hashes, plaintext API keys and the key
  hashes themselves never enter the trail (only method/label/expiry)
- `tenant_id` = the *subject's* tenant, so a tenant admin querying events
  sees everything that touched their tenant — including actions performed by
  a super admin — and nothing else

```sh
curl -b jar 'localhost:8080/api/v1/events?limit=50&action=perm_set'
# filters: action, target_type, target (name), since (RFC3339),
#          plus the standard list params (q, col, sort, dir, limit, offset)
```

Timestamps carry two precisions: `ts` stays unix seconds (the original
contract) while `ts_ms` gives the exact instant, and `time` renders the
same moment as RFC3339 UTC with fractional seconds. Storage, ordering and
the `since=` filter (which may cut *inside* a second) work in
milliseconds — two events in one second, or a draft saved under a second
ago, are no longer aliases of each other.

Admin rights; super admins see all tenants. Newest first. A failed audit
write is logged loudly but never blocks the operation itself. The GUI
mirrors this API one-for-one as the **Events tab** (see Web GUI): all
columns, the same filters, and per-row expansion of the `details` JSON.

## Portable JSON documents (export / import)

Every entity can be stated as one JSON document and re-applied on any
instance. `GET /api/v1/export?path=/p` | `?user=t/u` | `?group=t/g` |
`?tenant=t` returns the canonical text — a path's grants, or a user
(fields + `member_of` + the grants naming them), a group (members +
grants), or a tenant. It speaks **names, never ids**, carries no
secrets, and grant lines are exactly the `PUT /api/v1/perms` input
shape — so one document is reviewable, diffable text you can keep in
git or in a `/templates/` folder (a convention, not a special place).

`POST /api/v1/import {"doc": …, "mode": "merge"|"replace"}` (admin)
writes one back:

- **merge** (the default) adds and updates; **replace** additionally
  deletes the rows belonging to the target that the document omits —
  inherited grants are never touched;
- validation happens **all at once**: one unresolved name 422s the
  whole call and lists every problem; nothing ever half-applies, and
  re-applying the same document is a no-op;
- the writes themselves run as **one transaction**: a large document
  commits once (a 2000-line apply is milliseconds even on a phone),
  and a rare mid-apply failure leaves no partial state behind;
- the only principal import creates is the document's **own subject**
  (a grant line may name it before it exists); every other name must
  already be there. Imported users are device users until given a
  password; an omitted `tenant` means the applying account's tenant;
- a `dir` document creates the whole directory chain first (parents
  included, shallowest first); a `tenant` document provisions the home
  exactly like `POST /tenants` does; `/help` stays read-only here too;
- every apply writes one `import_apply` event naming the target and
  the counts; a newly created subject also records the canonical
  `user_create`/`group_create`/`tenant_create` with `via: "document"`.

The GUI exports from any row's ⋮ menu and from the Permissions panel
(clipboard, or a downloaded `<kind>-<name>.json` template), and takes
text back under Permissions ▸ Apply JSON — pasted or picked from a
file — showing the exact difference first: it fetches the target's
*current* document from the same export endpoint, so the preview of
what merge adds and what replace deletes is the server's own truth,
not a guess.

## Web attack surface

What stands between this app and the standard browser-side attacks — each
line names the exact mechanism, most of it pinned by tests:

- **XSS** — everything rendered in the GUI goes through React, which
  escapes all text; the markdown viewer (`react-markdown`) **does not
  render raw HTML inside `.md` files at all** (no `rehype-raw`), and the
  codebase uses no `dangerouslySetInnerHTML`. A content-security-policy
  (`script-src 'self'`, sent on every response) means no script from any
  source the bundle didn't ship can execute.
- **CSRF** — three independent layers on every state-changing call: the
  `Origin` must match the `Host`, form content-types are refused (a
  cross-origin form can't accidentally post JSON-shaped bodies), and
  session cookies are `SameSite=Lax`. Bearer-token callers carry no
  ambient credential, so they are immune by construction.
- **Clickjacking** — `X-Frame-Options: SAMEORIGIN` + CSP `frame-ancestors
  'self'`: no other origin can frame the app to capture blind clicks;
  only the app itself frames its own bytes (the browser's built-in PDF
  viewer is a same-origin frame on `/fs/raw?dispo=inline`).
- **Confused-MPE / download XSS** — file content comes back as
  `Content-Disposition: attachment` unless the client explicitly asks for
  `?dispo=inline`, which the server honours only for a closed allowlist
  of formats browsers render natively (pdf/image/audio/video) — plus
  `X-Content-Type-Options: nosniff` either way; an uploaded
  `report.html` never runs as a page on your origin.
- **Path traversal / symlink escape** — enforced in `internal/fsx`, not in
  handlers: cleaned path, root-prefix check, and symlink components
  refused, so no `..`, absolute path, or planted symlink leaves the
  sandbox.
- **SQL injection** — every query is parameterized (including the dynamic
  `IN (?,?,…)` lists); sort columns come from per-endpoint whitelists and
  LIKE metacharacters in search terms are escaped, so user text is data
  everywhere.
- **Session theft / brute force** — bcrypt password hashing; API keys are
  stored as SHA-256 hashes and shown exactly once; session tokens come
  from `crypto/rand`, are rotated at every login and dropped at logout,
  and are `HttpOnly` (+ `Secure` automatically whenever TLS serves, see
  *HTTPS*); logins are rate-limited per source IP (3/s + burst by
  default) and every authenticated principal has its own request bucket;
  request bodies are capped (`-max-mb`).
- **Referrer leakage** — `Referrer-Policy: no-referrer` keeps logical
  paths (which carry file names) from leaking to third-party links.
- **Replay/forensics** — every authn/authz mutation lands in the
  append-only `events` table above; failed logins are recorded with
  source IP.

`make test-tls` asserts the header set, the Secure cookie under TLS, and
the HTTP→HTTPS redirect; the GUI walk runs with the CSP active and fails
on any console error.

## Logging

Structured logging via stdlib `log/slog`: one JSON object per line by
default (`-log-json=false` for readable text), level-filtered by
`-log-level`. Every request gets a correlation id (`X-Request-Id` response
header, generated or adopted from a well-formed incoming one) that appears
on the request line and panic traces — one client complaint maps to exactly
one server trace. Request lines carry method/path/query, status, duration,
and source IP; level by outcome (5xx → error, 429 → warn, rest info, and
`-log-level=debug` quiets GET noise while adding bearer-auth and rate-limit
diagnostics). Pipe through `jq` to slice:

```sh
./bin/server 2>&1 | jq 'select(.level=="ERROR")'
```

## Listing views: paging, search, sort

Every list endpoint — `/api/v1/fs`, `/api/v1/perms`, `/api/v1/users`,
`/api/v1/groups`, `/api/v1/tenants`, `/api/v1/events` — accepts the same
optional query parameters, so the GUI drives every panel identically:

| param    | meaning                                                            |
|----------|--------------------------------------------------------------------|
| `limit`  | rows per page (1..1000); omitted = all rows (script-friendly default) |
| `offset` | rows to skip (≥ 0)                                                  |
| `q`      | case-insensitive substring search (≤ 100 chars)                     |
| `col`    | restrict `q` to one column (see below); empty = all searchable ones  |
| `sort`   | sort key (see below)                                                |
| `dir`    | `asc` or `desc`                                                     |
| `active` | `true`/`false` (or `1`/`0`): only active or only suspended principals — users/groups/tenants lists only; a 400 anywhere else |
| `tenant` | exact tenant name — scopes users/groups lists (who is available in one tenant); a 400 on any list without that capability |

Anything outside the whitelists gets a 400, so typos surface loudly instead
of silently changing what a query means. **Every response carries an
`X-Total-Count` header**: how many rows match the filters regardless of the
page returned, which is all a caller needs to compute the last page.

| endpoint  | searchable `col`s           | `sort` keys                                   | default order         |
|-----------|-----------------------------|-----------------------------------------------|-----------------------|
| fs        | name                        | name, size, mtime                             | name asc, dirs first  |
| perms     | principal, tenant           | principal, type, expires                      | principal asc         |
| users     | username, tenant, description | username, tenant, created, active, description | username asc          |
| groups    | name, tenant, description   | name, tenant, active, description             | name asc              |
| tenants   | name, description           | name, created, active                         | name asc              |
| events    | actor, action, target, path | ts, actor, action, target, path               | ts desc (newest first)|

```sh
curl -b jar 'localhost:8080/api/v1/users?q=ali&col=username&sort=created&dir=desc&limit=25&offset=25'
curl -b jar 'localhost:8080/api/v1/users?active=false'   # suspended accounts only
curl -b jar 'localhost:8080/api/v1/users?tenant=acme'    # one tenant's roster
```

File listings live on disk, not in SQL, so fs search/sort/paging run
in-process over the caller-visible entries — same contract either way;
directories always sort first regardless of `dir`.

## File API (session cookie or `Authorization: Bearer <apikey>`)

| method | path                    | body / query                          | needs        |
|--------|-------------------------|---------------------------------------|--------------|
| GET    | /api/v1/fs?path=        | —                                     | r on path    |
| GET    | /api/v1/fs/content?path=| JSON text view                        | r            |
| GET    | /api/v1/fs/raw?path=    | download bytes (`&dispo=inline` for the browser's own pdf/image/audio/video viewer — honoured only for an explicit allowlist) | r            |
| POST   | /api/v1/fs/mkdir        | `{"path"}`                            | w on path    |
| POST   | /api/v1/fs/write        | `{"path","content"}` (+ optional `"base_mtime"`/`"base_mtime_ms"`) | w on path |
| POST   | /api/v1/fs/rename       | `{"from","to"}`                       | d on from, w on to |
| POST   | /api/v1/fs/upload       | multipart `path=<dir>, file=<f>`      | w on target  |
| DELETE | /api/v1/fs?path=        | file, or empty dir                    | d on path    |
| GET    | /api/v1/fs/draft?path=  | working copy, `draft:null` if none    | r on path    |
| PUT    | /api/v1/fs/draft        | `{"path","content"}` (upsert)         | w on path    |
| DELETE | /api/v1/fs/draft?path=  | discard the working copy              | w on path    |
| POST   | /api/v1/fs/draft/commit | `{"path"}` — publish                  | w on path    |

`/fs/write` accepts an optional `base_mtime` (the mtime `/fs/content`
reports): if the file has changed since, the write is refused with
`409 conflict` instead of clobbering newer bytes — the GUI uses this to
keep two writers from silently overwriting each other's plain-text edits.
Both precisions are published (`mtime`/`mtime_ms` on the content view,
`mod_time_ms` on listing entries, `updated_at_ms` on drafts), and
`base_mtime_ms` guards at the exact one — it wins if both forms are
supplied.

**Draft/publish** backs the YAML config editor: GUI edits autosave to a
per-path working copy, and the file on disk changes only when the draft
is *committed* — which re-validates the YAML server-side (parse-only,
multi-document aware), writes the file, drops the draft and records an
`fs_commit` audit event. A refused validation leaves both draft and file
untouched. One draft per path is the concurrency model: two admins
editing the same config share one draft, last writer wins. Reading a
draft needs `r`; saving, discarding and committing all need `w` —
drafting itself is a write action on the file. The working copy lives in
a `file_drafts` SQLite table: the **only** place the server ever stores
file content, a deliberate exception to the "permissions only" rule.

**Built-in help (`/help`).** The user guide under `docs/user/` is embedded
into the binary (same trick as the web bundle) and exposed through the
file API as the reserved read-only tree `/help`: every logged-in caller
may `GET /fs?path=/help`, `/fs/content`, `/fs/raw` — no grants needed,
and those paths always report bits `r--`. Any mutation (write, mkdir,
rename, delete, upload, draft, grant) on a `/help` path returns
`403 read_only`. The GUI surfaces it two ways: brand-menu **Help**
opens `/help/README.md` rendered in the file page (links between guides
navigate inside the app), and the Files panel pins a read-only `help/`
row at root — pinned client-side, so the actual `/fs` root listing is
byte-for-byte unchanged. A root directory literally named `help` is
shadowed by it; rename the directory if you need that name.

## Admin API

All three entity kinds carry a free-text `description` for operators
("customer acme vault", "build agents"): accepted on create, edited via
the PUT below, and searchable/sortable through the list contract.

- `GET/POST /api/v1/tenants`, `PUT /api/v1/tenants/{id} {"description"}`,
  `POST /api/v1/tenants/{id}/active {"active"}`, `DELETE /api/v1/tenants/{id}` —
  **super admin only**; creating a tenant also provisions its home directory
  `/{name}` (member read, see *Tenant home directories*); new names must be
  directory-safe (lowercase `a-z0-9-`, start alphanumeric, ≤32 chars, `help`
  reserved — 422 otherwise); deleting a tenant cascades to its users, groups,
  memberships and every ACL grant that targets them
- `GET/PUT /api/v1/perms`, `DELETE /api/v1/perms/{id}` — view grants + your
  effective bits (any user); mutate with admin rights, tenant-scoped for
  tenant admins; `principal_type` is `user`/`group` (named, in a tenant),
  `tenant` (every member of one tenant) or `all` (every signed-in user);
  grants may carry `expires_days`/`expires_at`
- `GET/POST /api/v1/users`, `PUT /api/v1/users/{id} {"description"}`,
  `DELETE /api/v1/users/{id}`, `POST /api/v1/users/{id}/password`,
  `POST /api/v1/users/{id}/active {"active"}` —
  tenant admins see and manage only their tenant; the `tenant` field on
  create is honored for super admins and pinned to own tenant otherwise;
  users may carry `expires_days`/`expires_at` for lapsed customer accounts;
  `active` suspends/reinstates (kill switch without deletion)
- `GET/POST /api/v1/users/{id}/factors`, `DELETE /api/v1/users/{id}/factors/{fid}` —
  provision/revoke API keys for device users (admin rights, tenant-scoped);
  `method` may be `apikey` now, `ed25519`/`oauth2` reserved
- `GET/POST /api/v1/groups`, `PUT /api/v1/groups/{id} {"description"}`,
  `DELETE /api/v1/groups/{id}`, `POST /api/v1/groups/{id}/active {"active"}`
- `POST /api/v1/groups/{id}/members {"username"}`, `DELETE /api/v1/groups/{id}/members/{userId}`
- `GET /api/v1/events` — forensic audit trail (see above); query params
  `limit`, `action`, `target_type`, `target`, `since`
- `GET /api/v1/export?path=… | ?user=t/u | ?group=t/g | ?tenant=t` and
  `POST /api/v1/import {"doc": …, "mode": "merge"|"replace"}` — portable
  JSON documents for one path/user/group/tenant (see *Portable JSON
  documents*); export is read-scoped like `GET /perms`, import needs admin

## Web GUI

The console lives in `frontend/` — **React 19 + Ant Design** (all MIT),
built with Vite. The Go server ships the built bundle as plain static
files and never runs any JS itself; every list panel drives the shared
paging/search/sort contract above, with antd's server-side table paging
(`X-Total-Count` → last page). `docs/gui-survey.md` records the framework
survey and the perf data behind this choice. A file or folder row's
**permissions** menu action points the Permissions panel at that exact
path, so granting access to one file is one click from the browser. That
panel also carries the document pair: **Export JSON** (this path as one
portable statement — clipboard or a `<kind>-<name>.json` template
download) and, for admins, **Apply JSON** (paste text or pick a saved
file; a live diff shows every row being added, updated or — in replace
mode — deleted before anything is written; see *Portable JSON
documents*).

Structure: all panels stay mounted under a tab header and swap instantly;
entity lists (users, groups, tenants, grants) show `active` and
`description` columns with an all/active/suspended filter that maps
straight onto `?active=` — suspended principals are one click away. Every
create/edit goes through a small per-entity modal (users, groups, tenants,
grants; edit mode only offers what the API's PUT accepts). The tenant and
principal fields of those modals are real pickers, not free text: the
grant modal and the group-members form suggest user/group names scoped to
the selected tenant via `?tenant=`, and the tenant control matches the
backend's rule — super admins search all tenants, tenant admins see their
own fixed. API errors and
confirmations land in a **flash center** under the header: the latest
error or success shows for 30 seconds, the full history stays behind the
badge and clears on demand — no modal dialogs standing between you and
the table. Confirmations name their subject instead of just the verb —
`grant saved: acme/alice on /reports/q3.md → r-w-`, `suspended user
acme/bob`, `renamed /a → /b` — so the session messages double as a "what
did I just change" trail. Every row's actions live behind one vertical three-dots menu
(`components/RowActions`): entity operations (edit, export JSON, set password,
new API key, suspend/activate, delete), file operations (permissions, export
JSON, rename, delete) and grant revoke all occupy the same one-column-wide
slot, so
tables stay usable at phone widths no matter how many actions an entity
has; destructive items pop a themed confirm dialog whose OK button
carries the action name.

The **Events tab** (admins only) shows the forensic audit trail as a
first-class table (`components/EventsTable`, embedded by the panel):
every stored column — id, time, actor (the
`tenant/username` snapshot, tagged `anonymous` for unauthenticated
attempts), tenant (super admins only, resolved from the id), action,
target, path and source IP. It rides the same server-side list contract
as every other panel (`q`/`col` across actor/action/target/path,
sort by ts/actor/action/target/path, newest first by default) plus the
audit-only filters the API accepts: a searchable `action=` select over
the full action vocabulary and a time-range select mapped to `since=`.
Events carrying structured `details` get a row expander rendering the
JSON payload. Every timestamp in the GUI — audit rows, file listing
mtimes, draft save times, pairing and account times — renders through
one component in the same shape: local wall clock to the millisecond
(`YYYY-MM-DD HH:MM:SS.mmm`), with the exact instant as ISO/UTC in the
hover title. The table re-fetches every time it becomes visible — tab
or drawer — the trail is append-only, so the freshest tail is what you
should see; the server pages it like every other list, so a trail of
100k events costs the same as one of 100. The same component also
stands as a side drawer behind the flash center's **`event log`**
button for admins: from anywhere — even while viewing a file — the full
audit table with columns, sort, filters and search is one click away,
with this session's local messages (the things that are not audit
events) in a separate small section below it, clearable on demand. For
plain users, who cannot query the events API, the badge stays a
`msg log` popover of session history.

Clicking a file either opens the **file page** or downloads, decided from
the **name** alone — GitHub-style policy in `filekind.ts`: `.yml`/`.yaml`
get the structured editor below, an allowlist of text extensions (plus
extension-less files like `LICENSE`/`Makefile`) get the document view,
images (`.png`/`.jpg`/`.gif`/`.webp`/`.avif`/`.svg`/…) are previewed
in-browser, movies (`.mp4`/`.m4v`/`.webm`/`.mov`/`.ogv`) play in a
native `<video controls>` player, audio (`.mp3`/`.m4a`/`.wav`/`.ogg`/
`.opus`/…) in a native `<audio controls>` one, and `.pdf` opens in the
browser's own pdf viewer via an `<iframe>` on `/fs/raw?dispo=inline` —
the server honours that optional param only for its own explicit
allowlist (the closed set of formats every mainstream browser decodes
by itself) and ignores it for anything else; no param, no change. The
rule is the same as for video: if every mainstream browser ships the
viewer, we frame it — no player or viewer library. An evaluation of
Video.js v10
/ Media Chrome / Plyr showed none of them pays for itself when every
video is a single progressive file from the same origin; formats no
browser decodes (`.mkv`, `.avi`, `.tiff`) or that could run scripts on
your origin (`.svg`, `.html`) deliberately stay downloads.
Everything else (binaries, `.zip`, unknown types) never opens a page:
the name itself links to `/fs/raw` (attachment disposition), and every
file's ⋮ menu carries `download` too. The file page
(`components/FilePane`) is a real pane, not a modal: it replaces the tab
area (the panels stay mounted behind it) and one component serves both
viewing and editing, like GitHub's file pages. The name click is always
the **viewer**: markdown files **render** (react-markdown + GFM, with a
Render/Code toggle — the source side is read-only **CodeMirror 6**),
other text files open read-only CodeMirror, YAML shows the structured
tree read-only, images render straight from `/fs/raw` and movies play
with native controls — media panes never fetch text content and never
offer an editor. Editing is an explicit step: every editable file's ⋮
menu carries `edit` (not media, not download-only files), and the viewer
header has an Edit button for the case where you already know you want
it. Text editing uses CodeMirror —
the editor family behind GitHub's in-browser file editing, chosen over
Monaco for a tenth the bundle size and real touch support; markdown
files additionally get a formatting **ribbon** above the editor (bold /
italic / strike / H1-H3 / quote / lists / link / code — GitHub-comment
style: the icons edit the markdown *source* around the selection, so the
file always stays plain lossless markdown). Its Save sends the
`base_mtime_ms` captured on open: if
the file changed underneath, the server refuses with 409 conflict and the
text stays in the pane to be copied out. **YAML config files** (`.yml`,
`.yaml`) instead open the structured editor (`components/YamlEditor`),
organized like drilling into folders. The **left panel is a flat path
stack**: one row per level — file (multi-doc files), document, key,
index — top item is the root, the current node at the bottom, each row
an icon + name + value hint. It is deliberately not an indented tree,
so deeply nested files never need horizontal scrolling at any width;
tapping any row jumps the editor back to that level, `↑ parent` pops
one level. The **right side** edits
the node the stack sits on: mappings as a **K / V / description** table
(description = the entry's inline YAML comment), arrays as a **value /
comment** table — both view-and-navigate, with container rows showing a
`▸` summary button; clicking any row **pushes** the stack one level
deeper, and scalars open a pushed full-width leaf editor where the note
above the line and multiline block scalars get real text areas. On
phone widths the path stack lives in a drawer that hides itself the
moment you pick a level, and the editor keeps the full width.
Multi-document files get a `file` level that lists every document,
anchors/aliases display but stay locked — raw text is never hand-edited.
Every change mutates the parsed AST (client-side `yaml`) and
re-serializes it, so leading notes, inline comments and quoting styles
survive the round trip — precisely the reason these process configs stay
YAML rather than JSON.
Edits autosave to the server-side draft (a banner names the last writer
and time); the file on disk changes only on **Publish**, where the server
re-validates and audits the draft, and a rejected draft surfaces its 422
inside the pane without losing anything. Drafting requires `w` — the
viewer and readers with plain `r` get the same tree read-only.

```sh
cd frontend
npm install
npm run dev            # vite dev server, proxies /api -> :8095
npm run build          # type-check + static bundle -> frontend/dist
```

Serve the bundle from the backend itself (or hand `dist/` to nginx — any
static host works as long as the API is same-origin):

```sh
./bin/server -web frontend/dist
```

The old hand-written vanilla-JS GUI (`web/`) has been removed: the
React console covers everything, from disk in development and from
inside the binary in release builds. (`docs/gui-survey.md` still
records the original survey if you want the history.)

## Schema & migrations

The schema is versioned migration files, not inline strings:

- `internal/db/migrations/<dialect>/NNNN_slug.sql`, embedded in the binary
  (`go:embed`) and applied in filename order, each exactly once in its own
  transaction; applied versions are recorded in a `schema_migrations` table
- databases created **before** the mechanism are baselined: if the `tenants`
  table exists but nothing is recorded, the first migration is marked applied
  without running it (its content *is* that schema) and the server upgrades
  from there — running yesterday's database with today's binary is the
  normal path, and the curl suite covers exactly that upgrade
- engine differences live in one place (`internal/db`'s `Dialect`): call
  sites write `?`-placeholder SQL once, and the `DB` wrapper rebinds to the
  engine (`$1, $2, …`) and retrieves inserted ids the way that engine
  returns them (`RETURNING id` vs `LastInsertId`)

**SQLite is the only engine wired up today.** The `postgres` groundwork is
in tree (dialect + `migrations/postgres/0001_init.sql`, deliberately
shape-identical to the sqlite baseline: BIGINT ids, text/unix timestamps,
0/1 booleans). Actually running on Postgres would additionally mean:

- importing a driver (`pgx/v5` stdlib) in `cmd/server` and passing it to
  `db.Open("postgres", dsn)`
- `migrate`'s own bookkeeping SQL uses `?` and probes `sqlite_master` for
  the baseline check — both need a dialect branch, and pgx accepts one
  statement per `Exec`, so multi-statement migration files must be split
- `q=` search relies on SQLite's ASCII case-insensitive `LIKE`; Postgres
  would need `ILIKE`, `LOWER()`, or `citext` columns

## Tests

```sh
GO111MODULE=on go test ./...                     # httptest API suite
BASE_URL=http://localhost:8095 ADMIN_TENANT=system ADMIN_PASS=secret \
  ./scripts/test_files.sh                        # curl regression suite
python3 scripts/test_matrix.py --base-url http://localhost:8095 \
  --admin-tenant system --admin-user admin --admin-pass admin
                                                 # cross-tenant/ACL/path matrix
./scripts/test-tls.sh                            # HTTPS/self-signed smoke
./scripts/test_gui.sh                            # headless-Chrome GUI walk
```

`test_matrix.py` is a dependency-free (stdlib-only) API matrix checker: it
provisions throwaway tenants/users/groups/files under a random run-id, then
verifies that principals, grants, groups, API keys, audit rows and JSON
documents never cross
tenant boundaries; that the r/w/d bits, nearest-ancestor inheritance, zero-grant
subtrees, user-overrides-group, expiry and suspension behave exactly as the
permission docs promise; and that hostile path inputs (`../`, `%2e%2e`, double
encodings, NUL, metacharacters, emoji, over-long names) are cleanly rejected
or cleanly handled — never a 5xx, never an escape above the file root. The
`Api` class inside is the single adapter between checks and wire format, so
pointing the suite at another project with the same contract is a one-class
port. `--only cross,acl,paths` runs a subset; `--insecure` covers self-signed
https; fixtures are namespaced so runs never collide.

`test_gui.sh` is self-contained: it builds `bin/server` and `frontend/dist`
when missing, boots a real server against a throwaway sandbox root with
seeded fixture tenants/users/files — including a real 8×8 PNG and a real
H.264 MP4 under `scripts/gui/fixtures/` — then drives `scripts/gui/walk.js` — a
puppeteer-core walk of the live UI (login, file table, the ⋮ row action
menus, modal forms, the file page: viewer-by-default flows — rendered
markdown + read-only Code source toggle, row-menu `edit` → markdown
ribbon → save, YAML structured-view → edit with drill-down +
draft-autosave → publish incl. comment/block-scalar preservation and the
390px-width drawer/scroll regression, image preview that really decodes +
native video player that really plays + media rows never offered `edit`,
download links for non-media files,
the active-filter wire format,
flash center, per-file grant flow, the JSON document pair (export modal
from the row menu and the Permissions panel, Apply JSON with the live
diff preview writing a real grant), the Events tab (all audit columns,
the `action=` filter wire format, search scoping, details expansion),
header menus, dark mode, sign-out)
with 117 assertions plus a zero-console-error guard (which also proves the
server's content-security-policy is compatible with the bundle). `SHOTS=1` additionally
writes light/dark screenshots to `$OUT_DIR` (default `/tmp/fx-gui`).
Needs a local Chrome; `CHROME_PATH` overrides the macOS default.

The root `Makefile` (split into `makefiles/{build,test,deploy}.mk`) wraps all of
this: `make build`, `make test` (go + a self-contained `test-api` that boots a
throwaway server on its own port + `test-tls` (HTTPS: the default
self-signed mode, the `-tls-self=false` opt-out, generated
certs, the HTTP→HTTPS 308, Secure cookies, security headers) +
`test-matrix` (the cross-tenant / permission / path-fuzz Python matrix above,
plus `test-matrix-purego` running it against the `-tags sqlite_purego`
MIPS build)
+ the GUI walk),
`make docker-*`.

## Deployment

Container image + compose live in `deployments/docker/` (see its README for
volume/env details):

```sh
make docker-up     # build + start; app on http://localhost:8080 (the compose file opts out of the TLS default)
make docker-test   # run the curl suite against the running stack
make docker-down
```

Three-stage Dockerfile (node GUI build → `CGO_ENABLED=0` Go binary — the
SQLite driver `modernc.org/sqlite` is pure Go → small Alpine runtime;
32-bit MIPS builds swap it for `ncruces/go-sqlite3` via
`-tags sqlite_purego`, see `internal/db/driver_purego.go`).
All state lives under `/data`: mount the managed file tree at `/data/root`
and the SQLite/drafts dir at `/data`; config is env-only (`ADDR`, `ROOT`,
`DB_PATH`, `ADMIN`, `WEB_DIR`, `LOG_*`). The compose file also carries a
profiled `postgres` service ready for the Postgres cutover. Placeholder
READMEs in `deployments/k8s/` and `deployments/swarm/` sketch the next
steps; nothing there is deployed yet.

## Release & publish

Source repo: `https://github.com/GssMahadevan/gvn-explorer` (origin;
the PAT in `~/.gitconfig`'s `insteadOf` rewrite authenticates pushes).

**Multi-arch container** — one tag, both architectures
(`linux/amd64` + `linux/arm64`; consumers pull their own). Both build
stages pin to `$BUILDPLATFORM`, so Node and Go run natively and only
cross-compile — no QEMU penalty:

```sh
docker login ghcr.io                      # PAT works for ghcr (needs write:packages)
make docker-push IMAGE=ghcr.io/gssmahadevan/gvn-explorer:latest
make docker-push IMAGE=ghcr.io/gssmahadevan/gvn-explorer:v0.1.0   # pin-able tag, same build
```

Publishing the *same commit* under two tags means the second push only
uploads the small manifest — and a pure retag of an image already in the
registry needs no build at all:
`docker buildx imagetools create -t …:vX.Y.Z …:latest`.

The first run creates a `docker-container`-driver buildx builder
(`gvn-builder`, overridable via `BUILDER=`); it keeps its own layer
cache, so repeat pushes are fast. Verify with
`docker buildx imagetools inspect <image>`.

**Bare binaries** — `make dist VERSION=v0.1.0` cross-builds all eight platforms
(linux/windows/darwin × amd64/arm64, plus linux/mips + linux/mipsle for
OpenWrt-class routers) with `-tags embedweb`, which bakes
the GUI bundle (see `frontend/webfs_embed.go`) *and* the `/help` docs
into the binary. Every artifact is `CGO_ENABLED=0`: fully static, runs on
musl (OpenWrt) as-is. The MIPS targets additionally get
`GOMIPS=softfloat` (no FPU on those SoCs) and `-tags sqlite_purego`
(modernc has no 32-bit-MIPS code; the ncruces driver is arch-independent
and passes the same 170-check matrix — `make test-matrix-purego` proves
it on this machine). Each `dist/bundles/` archive is just the binary +
`deployments/binary/README.md`. Genuinely one file: `make test-dist`
unpacks this machine's archive, starts it with zero flags and asserts
the GUI and `/help` come out of the binary (and that no `web/` dir
ships). Bare binaries also land flat in `dist/`. The plain build
(`go build`, dev, tests, the Docker image) never embeds — it serves
`-web` from disk exactly as before; embedding is purely the
release-binary mode. Serving precedence either way: an existing `-web`
directory wins, otherwise the embedded bundle.

**Android APK** — `make android-apk` builds the same embedded binary for
arm64-v8a/armeabi-v7a/x86_64, packages it inside a thin wrapper app
(`android/`: a Kotlin foreground service that exec's the binary and a
one-screen settings UI with a storage-volume picker for phone/SD/USB
roots), and drops the APK next to the other artifacts in `dist/`. The
app has no third-party dependencies; the server inside it is the
ordinary `CGO_ENABLED=0` build. `android/README.md` has the end-user
setup; it was smoke-tested end-to-end (install → boot autostart →
login → file write) on an Android-14 emulator via adb.

Published: **v0.1.0** is live on the Releases page with all six
archives attached. The flow for the next release (no `gh` CLI needed):

```sh
git tag vX.Y.Z && git push origin vX.Y.Z
make dist VERSION=vX.Y.Z && make test-dist
# create the release + upload assets with the same PAT (api.github.com
# /repos/.../releases, then the release upload_url per asset),
# or use `gh release create` wherever the gh CLI exists.
```

ghcr note: the container package keeps its own visibility setting —
if anonymous `docker pull` gives 401 even though the repo is public, flip
the package to Public at
<https://github.com/users/GssMahadevan/packages/container/gvn-explorer/settings>
(the `delete:packages` PAT scope is what the API path would need instead).

## Layout

```
Makefile           entry points; includes makefiles/ (same layout as gvndb sample)
makefiles/         build.mk (server+GUI + `dist`/`test-dist` cross-builds), test.mk (go/curl/GUI suites), deploy.mk (docker+compose + multi-arch `docker-push`)
deployments/       docker/ (Dockerfile, compose, README), binary/ (README shipped inside dist archives), k8s/ + swarm/ (planned, README sketches)
docs/              user guide embedded into the binary at build time (embed.go) and served read-only under /help; guide set in docs/user/
cmd/server/        main + middleware (JSON logging w/ request ids, panic recovery)
internal/db/       SQL layer: dialect-aware DB wrapper + embedded versioned migrations (tenants/users/groups/members/perms/auth_factors/sessions/events/file_drafts)
internal/auth/     tenants, users, groups, bcrypt, sessions, API-key factors, active flag
internal/acl/      grant upsert/list + Effective() inheritance engine
internal/audit/    append-only forensic event trail (authz/authn DML, fs_commit)
internal/drafts/   per-path working copies for the draft/publish edit model
internal/fsx/      sandboxed filesystem ops (list/read/write/mkdir/rename/delete)
internal/handlers/ REST handlers + route table, JSON error envelope, rate limiters
frontend/          React 19 + Ant Design GUI (Vite; list panels, per-entity form modals, row action menus, file page (FilePane): viewer default - rendered md / read-only CodeMirror / structured YAML / image preview / native <video> player, edit via row menu w/ markdown ribbon, YAML structured editor w/ draft+publish, download actions (filekind.ts policy), flash center; `npm run build` -> dist/ static bundle; webfs_disk.go/webfs_embed.go: GUI served from disk by default, embedded into release binaries under -tags embedweb)
scripts/           test suites: test_files.sh (curl API regression), test_matrix.py (stdlib cross-tenant/ACL/path-fuzz matrix), test_gui.sh + gui/ (puppeteer GUI walk + screenshots), test_dist.sh (release-bundle smoke: unpack-and-run, embedded GUI+/help)
```

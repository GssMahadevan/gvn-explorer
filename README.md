# file-explorer (llm-crud1)

Multi-tenant web file explorer with a Go backend. Files live on the real
filesystem under one **root path**; SQLite stores the platform-independent
permission model (tenants, users, groups, per-path ACL grants, sessions) —
never the file contents.

Tenants namespace *principals* (usernames and group names are unique per
tenant); the file tree itself is shared, so isolation between tenants is
enforced purely by ACL grants.

> Schema note: the tenant schema is **not compatible** with pre-tenant
> databases. Delete the old `.db` file; the server recreates it.

## Run

```sh
GO111MODULE=on go build -o bin/server ./cmd/server
./bin/server -addr :8080 -root ./data -db perms.db -admin system:admin:admin
```

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
| -log-level | LOG_LEVEL | debug|info|warn|error (default info)               |
| -log-json  | LOG_JSON  | JSON log lines, one object per line (default true; false = human text) |

The bootstrap flag creates the tenant *and* a **platform super admin** inside
it. All paths are logical and rooted at `-root`: no `..`, absolute paths, or
symlink traversal can escape it.

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

Grants are rows `(path, principal_type user|group, principal_id, r/w/d bits)`;
`principal_id` points at a users/groups row, which carries the tenant, so the
same name in two tenants is two distinct grant subjects. Effective perms for a
path = **nearest ancestor** with an explicit grant: user grants win over group
grants at the same level; multiple group grants union; no grant anywhere =
deny. Example:

```sh
curl -b jar -X PUT localhost:8080/api/v1/perms \
  -H 'Content-Type: application/json' \
  -d '{"path":"/docs","principal_type":"group","principal":"readers","tenant":"acme","read":true}'
```

gives `acme/readers` read on `/docs` and everything below, unless overridden
deeper. `tenant` defaults to the caller's tenant; tenant admins may only ever
target their own. `GET /api/v1/fs` hides entries the caller cannot read. A
**zero grant** (everything false) on a nearer path intentionally *excludes* a
subtree: inheritance stops at the nearest ancestor with any grant row for the
principal, so `{"path":"/secret","principal_type":"group","principal":"readers","read":false}`
removes `/secret` from readers' listings even when they hold read on `/`.

Group membership is always resolved inside the group's own tenant — adding
"alice" to `globex/staff` can never enroll `acme/alice`.

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

Databases created before this feature migrate automatically (two additive
`ALTER TABLE` statements run at startup).

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
with `Retry-After`. Buckets reset on restart, which is fine at this scale.

## Suspension (`active` flag)

Users and groups can be **deactivated without deleting them** — the kill
switch for an offboarded customer or a quarantined device. Rows, grants,
factors and history all survive; every action in the system stops:

- user inactive → login refused (indistinguishable from a wrong password),
  live sessions die on the *next request*, bearer API keys are rejected, and
  admin actions are impossible even with a stolen cookie
- group inactive → every ACL grant *through* it is ignored, exactly like an
  expired grant; direct user grants are untouched
- reinstatement (`{"active":true}`) restores everything instantly, because
  nothing was ever destroyed

```sh
curl -b jar -X POST localhost:8080/api/v1/users/7/active \
  -H 'Content-Type: application/json' -d '{"active":false}'
curl -b jar -X POST localhost:8080/api/v1/groups/3/active \
  -H 'Content-Type: application/json' -d '{"active":true}'
```

Admin rights, tenant-scoped. Guard rail: nobody can suspend *their own*
account (422) — lockouts are always recoverable by another admin. Lists
(`/api/v1/users`, `/api/v1/groups`, and `/api/v1/perms` grant rows via
`principal_active`) flag inactive principals so the state is visible.

Distinct from expiry: `expires_at` is a scheduled lapse the admin walks back;
`active=false` is an immediate suspension. Both block everything.

## Audit trail (events table)

Every authz/authn DML is appended to an `events` table — forensics first:
who changed what, to whom, from which IP, when. Recorded actions:
`tenant_create/delete`, `user_create/delete/password/active`,
`group_create/delete/active`, `member_add/remove`, `perm_set/delete`,
`factor_create/delete`, `login`, `login_failed`.

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

Admin rights; super admins see all tenants. Newest first. A failed audit
write is logged loudly but never blocks the operation itself.

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

Anything outside the whitelists gets a 400, so typos surface loudly instead
of silently changing what a query means. **Every response carries an
`X-Total-Count` header**: how many rows match the filters regardless of the
page returned, which is all a caller needs to compute the last page.

| endpoint  | searchable `col`s           | `sort` keys                         | default order         |
|-----------|-----------------------------|-------------------------------------|-----------------------|
| fs        | name                        | name, size, mtime                   | name asc, dirs first  |
| perms     | principal, tenant           | principal, type, expires            | principal asc         |
| users     | username, tenant            | username, tenant, created, active   | username asc          |
| groups    | name, tenant                | name, tenant, active                | name asc              |
| tenants   | name                        | name, created                       | name asc              |
| events    | actor, action, target, path | ts, actor, action, target, path     | ts desc (newest first)|

```sh
curl -b jar 'localhost:8080/api/v1/users?q=ali&col=username&sort=created&dir=desc&limit=25&offset=25'
```

File listings live on disk, not in SQL, so fs search/sort/paging run
in-process over the caller-visible entries — same contract either way;
directories always sort first regardless of `dir`.

## File API (session cookie or `Authorization: Bearer <apikey>`)

| method | path                    | body / query                          | needs        |
|--------|-------------------------|---------------------------------------|--------------|
| GET    | /api/v1/fs?path=        | —                                     | r on path    |
| GET    | /api/v1/fs/content?path=| JSON text view                        | r            |
| GET    | /api/v1/fs/raw?path=    | download bytes                        | r            |
| POST   | /api/v1/fs/mkdir        | `{"path"}`                            | w on path    |
| POST   | /api/v1/fs/write        | `{"path","content"}`                  | w on path    |
| POST   | /api/v1/fs/rename       | `{"from","to"}`                       | d on from, w on to |
| POST   | /api/v1/fs/upload       | multipart `path=<dir>, file=<f>`      | w on target  |
| DELETE | /api/v1/fs?path=        | file, or empty dir                    | d on path    |

## Admin API

- `GET/POST /api/v1/tenants`, `DELETE /api/v1/tenants/{id}` — **super admin only**;
  deleting a tenant cascades to its users, groups, memberships and every ACL
  grant that targets them
- `GET/PUT /api/v1/perms`, `DELETE /api/v1/perms/{id}` — view grants + your
  effective bits (any user); mutate with admin rights, tenant-scoped for
  tenant admins; grants may carry `expires_days`/`expires_at`
- `GET/POST /api/v1/users`, `DELETE /api/v1/users/{id}`, `POST /api/v1/users/{id}/password`,
  `POST /api/v1/users/{id}/active {"active"}` —
  tenant admins see and manage only their tenant; the `tenant` field on
  create is honored for super admins and pinned to own tenant otherwise;
  users may carry `expires_days`/`expires_at` for lapsed customer accounts;
  `active` suspends/reinstates (kill switch without deletion)
- `GET/POST /api/v1/users/{id}/factors`, `DELETE /api/v1/users/{id}/factors/{fid}` —
  provision/revoke API keys for device users (admin rights, tenant-scoped);
  `method` may be `apikey` now, `ed25519`/`oauth2` reserved
- `GET/POST /api/v1/groups`, `DELETE /api/v1/groups/{id}`, `POST /api/v1/groups/{id}/active {"active"}`
- `POST /api/v1/groups/{id}/members {"username"}`, `DELETE /api/v1/groups/{id}/members/{userId}`
- `GET /api/v1/events` — forensic audit trail (see above); query params
  `limit`, `action`, `target_type`, `target`, `since`

## Web GUI

The console lives in `frontend/` — **React 19 + Ant Design** (all MIT),
built with Vite. The Go server ships the built bundle as plain static
files and never runs any JS itself; every list panel drives the shared
paging/search/sort contract above, with antd's server-side table paging
(`X-Total-Count` → last page). `docs/gui-survey.md` records the framework
survey and the perf data behind this choice. A file or folder row's
**perms** action points the Permissions panel at that exact path, so
granting access to one file is one click from the browser.

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

`web/` — the hand-written vanilla-JS GUI, zero build toolchain — stays in
the tree as a fallback and as a compact reference of the API contract;
`-web web` serves it.

## Tests

```sh
GO111MODULE=on go test ./...                     # httptest API suite
BASE_URL=http://localhost:8095 ADMIN_TENANT=system ADMIN_PASS=secret \
  ./scripts/test_files.sh                        # curl regression suite
```

## Layout

```
cmd/server/        main + middleware (JSON logging w/ request ids, panic recovery)
internal/db/       SQLite open + schema (tenants/users/groups/members/perms/auth_factors/sessions/events)
internal/auth/     tenants, users, groups, bcrypt, sessions, API-key factors, active flag
internal/acl/      grant upsert/list + Effective() inheritance engine
internal/audit/    append-only forensic event trail (authz/authn DML)
internal/fsx/      sandboxed filesystem ops (list/read/write/mkdir/rename/delete)
internal/handlers/ REST handlers + route table, JSON error envelope, rate limiters
frontend/          React 19 + Ant Design GUI (Vite; `npm run build` -> dist/ static bundle)
web/               vanilla-JS GUI fallback (panels per view w/ paging+search+sort, editor, perms, admin)
```

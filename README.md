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

## File API (session cookie required)

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
- `GET/POST /api/v1/users`, `DELETE /api/v1/users/{id}`, `POST /api/v1/users/{id}/password` —
  tenant admins see and manage only their tenant; the `tenant` field on
  create is honored for super admins and pinned to own tenant otherwise;
  users may carry `expires_days`/`expires_at` for lapsed customer accounts
- `GET/POST /api/v1/groups`, `DELETE /api/v1/groups/{id}`
- `POST /api/v1/groups/{id}/members {"username"}`, `DELETE /api/v1/groups/{id}/members/{userId}`

## Tests

```sh
GO111MODULE=on go test ./...                     # httptest API suite
BASE_URL=http://localhost:8095 ADMIN_TENANT=system ADMIN_PASS=secret \
  ./scripts/test_files.sh                        # curl regression suite
```

## Layout

```
cmd/server/        main + middleware (logging, panic recovery)
internal/db/       SQLite open + schema (tenants/users/groups/members/perms/sessions)
internal/auth/     tenants, users, groups, bcrypt, sessions
internal/acl/      grant upsert/list + Effective() inheritance engine
internal/fsx/      sandboxed filesystem ops (list/read/write/mkdir/rename/delete)
internal/handlers/ REST handlers + route table, JSON error envelope
web/               vanilla-JS GUI (login w/ tenant, explorer, editor, perms, admin)
```

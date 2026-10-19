# REST API for non-browser clients

Everything the GUI does is a documented HTTP call — scripts, CI jobs,
devices and other services use the same contract, no HTML anywhere.
This is the practical reference — everything you need while reading it
here is in this guide. The exhaustive contract, plus the ~350-case curl
regression suite that tests it, lives in the project's source repo
(root `README.md` and `scripts/test_files.sh`).

Base URL: `http://<host>:8080/api/v1`. JSON in, JSON out. Every
response carries `X-Request-Id` (echo yours to correlate logs).

## Authentication

Two ways, interchangeable for every endpoint:

**Session (browsers, interactive scripts)**

```sh
curl -c jar -X POST localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"system","username":"admin","password":"admin"}'
# sets the sid cookie; the body identifies you (tenant, username, admin flags)
curl -b jar localhost:8080/api/v1/auth/me      # who am I (also your effective bits)
curl -b jar -X POST localhost:8080/api/v1/auth/logout
```

The `sid` cookie is HttpOnly, 24h, server-side sessions in SQLite.
CSRF hardening applies to cookie callers: requests **with a body**
must send `Content-Type: application/json` (plain `curl -d` defaults
to form-urlencoded and is refused with `403 csrf`) and must be
same-origin.

**Bearer (devices, CI)** — no login, no CSRF rules:

```sh
curl -H "Authorization: Bearer $KEY" 'localhost:8080/api/v1/fs?path=/firmware'
```

Keys are minted/revoked as described in [api-keys.md](api-keys.md).
Expired/suspended users and keys simply stop: `401`.

## Errors and limits

Errors are one envelope at every endpoint:

```json
{ "error": { "code": "exists", "message": "tenant name taken" } }
```

`400` bad/invalid parameters (unknown sort columns etc. fail loudly,
never silently change meaning) · `401` no/expired credentials ·
`403` authenticated but not allowed (also CSRF) · `404` gone ·
`409` conflict (rename onto existing name; `base_mtime` mismatch) ·
`422` semantically invalid (bad YAML on publish, suspending yourself) ·
`429` rate limited, with `Retry-After` — per principal (default
20 r/s, burst 60) and per login IP (3 r/s). File bodies up to
`MAX_MB` (default 10).

## Listing contract (shared by all list endpoints)

`/fs`, `/perms`, `/users`, `/groups`, `/tenants`, `/events` all take
the same optional parameters — one pattern for every collection:

| param | meaning |
|---|---|
| `limit` | rows per page (1..1000); omitted = all rows |
| `offset` | rows to skip |
| `q` | case-insensitive substring search |
| `col` | restrict `q` to one column (per-endpoint whitelist) |
| `sort` / `dir` | sort key + `asc`/`desc` |
| `active` | `true`/`false` — users/groups/tenants lists |
| `tenant` | exact tenant name — users/groups lists |

Every list response carries **`X-Total-Count`**: total matches
regardless of page, so you can compute pages. Examples:

```sh
curl -b jar 'localhost:8080/api/v1/users?q=ali&col=username&sort=created&dir=desc&limit=25&offset=25'
curl -b jar 'localhost:8080/api/v1/events?limit=50&action=perm_set'   # events also take action=, since=
```

## File API

| method | path | body / query | needs |
|--------|------|--------------|-------|
| GET | `/fs?path=` | directory listing (name, size, mtime, type) | `r` on path |
| GET | `/fs/content?path=` | JSON text view incl. `mtime` | `r` |
| GET | `/fs/raw?path=` | raw bytes; `ETag` + `If-None-Match`/304 | `r` |
| POST | `/fs/mkdir` | `{"path"}` | `w` |
| POST | `/fs/write` | `{"path","content"}`, optional `"base_mtime"` | `w` |
| POST | `/fs/rename` | `{"from","to"}` | `d` on from, `w` on to |
| POST | `/fs/upload` | multipart `path=<dir>, file=<f>` | `w` on target |
| DELETE | `/fs?path=` | file, or empty directory | `d` |

```sh
curl -H "Authorization: Bearer $KEY" -X POST localhost:8080/api/v1/fs/mkdir \
  -H 'Content-Type: application/json' -d '{"path":"/reports/2026"}'
curl -H "Authorization: Bearer $KEY" -X POST localhost:8080/api/v1/fs/write \
  -H 'Content-Type: application/json' \
  -d '{"path":"/reports/2026/q3.md","content":"# Q3\n...","base_mtime":1750000000}'
```

All paths are logical, rooted at the configured root: `..`, absolute
paths and symlinks can never escape the sandbox. `/fs/write` with
`base_mtime` (the mtime `/fs/content` reported) gives you optimistic
concurrency: mismatch → `409` instead of clobbering someone else's
newer bytes. Listings, search and paging are server-side — a 500k-file
directory is still one cheap page.

**Draft/publish** (backs the YAML editor; any client may use it):
`GET/PUT/DELETE /fs/draft` is a per-path working copy stored in the
database, and `POST /fs/draft/commit {"path"}` writes it to disk —
for `.yaml`/`.yml` the commit re-validates server-side and refuses
invalid YAML with `422` (draft stays, file untouched). Each commit
records an `fs_commit` audit event.

## Admin API

Permissions, users, groups, tenants, factors — details and examples in
[permissions.md](permissions.md) and
[tenants-users-groups.md](tenants-users-groups.md):

| area | endpoints |
|------|-----------|
| grants | `GET /perms?path=` (grants + your `effective` bits) · `PUT /perms` (upsert; `read/write/delete`, `expires_days`/`expires_at`) · `DELETE /perms/{id}` |
| tenants | `GET/POST /tenants`, `PUT /tenants/{id}` (description), `POST /tenants/{id}/active`, `DELETE /tenants/{id}` — **super admin only** |
| users | `GET/POST /users`, `PUT /users/{id}` (description), `POST /users/{id}/password`, `POST /users/{id}/active {"active"}`, `DELETE /users/{id}` |
| groups | `GET/POST /groups`, `PUT /groups/{id}`, `POST /groups/{id}/active`, `DELETE /groups/{id}`, `POST /groups/{id}/members {"username"}`, `DELETE /groups/{id}/members/{userId}` |
| keys | `GET/POST /users/{id}/factors`, `DELETE /users/{id}/factors/{fid}` |
| events | `GET /events` (admins; `action=`, `target_type=`, `target=`, `since=` + list contract; tenant admins see their tenant, super admins everything) |

Tenant admins are automatically scoped to their own tenant everywhere
(including the audit trail); super admins pass `{"tenant":...}` on
create and `?tenant=` on lists.

## The contract is tested, not folklore

The server's regression suite (in the source repo:
`scripts/test_files.sh`) runs ~350 curl assertions against a live
server — auth, expiry, suspension, CSRF, ACL inheritance, pagination,
rate limits. If you have the checkout, grepping that file is the
fastest way to see any call's exact request/response.

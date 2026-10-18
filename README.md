# file-explorer (llm-crud1)

Web file explorer with a Go backend. Files live on the real filesystem under
one **root path**; SQLite stores the platform-independent permission model
(users, groups, per-path ACL grants, sessions) — never the file contents.

## Run

```sh
GO111MODULE=on go build -o bin/server ./cmd/server
./bin/server -addr :8080 -root ./data -db perms.db -admin admin:admin
```

Flags (env fallbacks in caps; flags win):

| flag    | env       | meaning                                            |
|---------|-----------|----------------------------------------------------|
| -root   | ROOT      | directory exposed to the explorer (created if absent) |
| -db     | DB_PATH   | SQLite file for users/groups/ACLs/sessions         |
| -admin  | ADMIN     | bootstrap `user:pass`, applied only when no users exist |
| -web    | WEB_DIR   | static GUI dir (empty string disables)             |
| -max-mb | —         | read/write/upload size cap (default 10)            |

All paths are logical and rooted at `-root`: no `..`, absolute paths, or
symlink traversal can escape it.

## Auth

`POST /api/v1/auth/login {"username","password"}` sets an HttpOnly session
cookie (`sid`, 24h, stored in SQLite). `POST /api/v1/auth/logout`,
`GET /api/v1/auth/me`. Passwords are bcrypt hashes. `is_admin` bypasses ACLs.

CSRF hardening: state-changing requests must be same-origin (Origin header
checked) and must not use form-style content types — send
`-H 'Content-Type: application/json'` with every request that has a body
(`curl -d` alone defaults to form-urlencoded and is refused with 403 csrf).
Cookies are `SameSite=Lax`.

## Permission model

Grants are rows `(path, principal_type user|group, principal, r/w/d bits)`.
Effective perms for a path = **nearest ancestor** with an explicit grant:
user grants win over group grants at the same level; multiple group grants
union; no grant anywhere = deny. Example:

```sh
curl -b jar -X PUT localhost:8080/api/v1/perms \
  -d '{"path":"/docs","principal_type":"group","principal":"readers","read":true}'
```

gives readers read on `/docs` and everything below, unless overridden deeper.
`GET /api/v1/fs` hides entries the caller cannot read. A **zero grant**
(everything false) on a nearer path intentionally *excludes* a subtree:
inheritance stops at the nearest ancestor with any grant row for the principal,
so `{"path":"/secret","principal_type":"group","principal":"readers","read":false}`
removes `/secret` from readers' listings even when they hold read on `/`.

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

## Admin API (admin session)

- `GET/PUT /api/v1/perms`, `DELETE /api/v1/perms/{id}` — view/upsert/delete grants
- `GET/POST /api/v1/users`, `DELETE /api/v1/users/{id}`, `POST /api/v1/users/{id}/password`
- `GET/POST /api/v1/groups`, `DELETE /api/v1/groups/{id}`
- `POST /api/v1/groups/{id}/members {"username"}`, `DELETE /api/v1/groups/{id}/members/{userId}`

## Layout

```
cmd/server/        main + middleware (logging, panic recovery)
internal/db/       SQLite open + schema (users/groups/members/perms/sessions)
internal/auth/     users, groups, bcrypt, sessions
internal/acl/      grant upsert/list + Effective() inheritance engine
internal/fsx/      sandboxed filesystem ops (list/read/write/mkdir/rename/delete)
internal/handlers/ REST handlers + route table, JSON error envelope
web/               GUI (being rebuilt for this domain)
```

# Tenants, users and groups

Who may use the system, and who they belong to. Everything here is
managed in the GUI (Tenants / Users / Groups tabs) or through the same
API the GUI uses — see [api.md](api.md) for the raw calls.

The examples assume a local instance at `http://localhost:8080` and a
cookie jar from logging in. A default instance serves HTTPS with a
self-signed cert — use `https://localhost:8080` and add `-k` to every
curl below:

```sh
curl -c jar -X POST localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"system","username":"admin","password":"admin"}'
```

## The model in one paragraph

A **tenant** is an organization namespace: its users, groups and grants
are addressed as `tenant/name`. A **user** is a person *or* a device
(devices just use API keys, see [api-keys.md](api-keys.md)). A
**group** is a named set of users inside one tenant; granting a path to
a group covers all its members. Two roles exist: the **platform super
admin** (created only by the bootstrap, sees all tenants, bypasses file
permissions) and a **tenant admin** (`is_admin`, manages only its own
tenant's people and grants — and needs file grants like anyone else).
Every entity has a free-text `description` field for your own notes
("customer acme vault", "build agents").

## Tenants (super admin only)

Create `acme` in the Tenants tab, or:

```sh
curl -b jar -X POST localhost:8080/api/v1/tenants \
  -H 'Content-Type: application/json' \
  -d '{"name":"acme","description":"customer acme"}'
```

New tenant names must be able to back a directory: lowercase letters,
digits and `-`, starting alphanumeric, at most 32 characters, and
`help` (the documentation path) is reserved. A name that doesn't fit is
refused with 422. (Tenants created by older versions may have other
names; they work normally, their top-level directories just stay
ordinary shared space.)

**Every tenant automatically has a home directory** at `/{name}` —
creating the tenant creates `/acme` and one ordinary grant row giving all
acme members read on it. The home is private by construction: only acme
members (and super admins) can see or open anything inside, and to
everyone else `/acme` doesn't even appear in listings. Inside the home
all grants must address acme's own members; shared content across
communities lives outside homes, opened by explicit grants
([permissions.md](permissions.md)). If a top-level directory named
`acme` already existed when the tenant was created, it simply becomes
the home. The default member-read grant is a row like any other: acme's
own tenant admin can upgrade it (e.g. to read+write so members can
collaborate) or revoke it — revoking hides the home from members but
never touches the data.

**Suspending** a tenant is the kill switch for a whole customer: no
logins, live sessions and API keys of its users stop at the next
request — but rows, grants and history are untouched, and
reactivating restores everything. Deleting a tenant is permanent and
cascades to its users, groups, memberships and grants; its home
directory stays on disk as ordinary shared space until you remove it.
You cannot suspend your own tenant (no self-lockout).

## Users

Create a person (the initial password can be changed by an admin or
left to the user):

```sh
curl -b jar -X POST localhost:8080/api/v1/users \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"acme","username":"alice","password":"alice-pass","description":"ops lead"}'
```

- `is_admin: true` makes them a tenant admin of *their* tenant. Only
  the bootstrap ever creates the super admin.
- `"expires_days": 14` (or `"expires_at":"2026-11-01T00:00:00Z"`)
  gives the account a lifespan: after it lapses, login is rejected and
  **already-logged-in sessions die on their next request**. No cron,
  no restart — every request re-checks expiry. (Expiry is set at
  creation; to extend a customer, recreate the account — or better,
  keep the account permanent and put the expiry on the *grant*, which
  re-PUT simply renews. See [permissions.md](permissions.md).)
- Set a password later: `POST /api/v1/users/{id}/password`.
- Omit the password entirely and you get a **device user** — login by
  password is structurally impossible for it; give it an API key
  instead ([api-keys.md](api-keys.md)).

**Suspend instead of delete** for offboarding: `active:false` stops
everything instantly (logins, sessions, keys, admin actions) while
grants, keys and audit history survive; `active:true` restores them.
Deleting a user is final (the audit trail keeps their name as a
snapshot). Guard rails: nobody can suspend their own account.

The Users list supports the standard filters — `?q=ali`,
`?active=false` (suspended only), `?tenant=acme` (one tenant's
roster) — and every response's `X-Total-Count` header gives the full
match count.

## Groups

Groups are always tenant-local: adding "alice" to `globex/staff` can
never enroll `acme/alice`.

```sh
# group + one member
curl -b jar -X POST localhost:8080/api/v1/groups \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"acme","name":"staff","description":"acme staff"}'
curl -b jar -X POST localhost:8080/api/v1/groups/3/members \
  -H 'Content-Type: application/json' -d '{"username":"alice"}'
```

In the GUI the Groups panel shows member tags you can click to remove,
plus an "add member" form with tenant-scoped name suggestions.
Suspending a group ignores every permission that flows *through* it
while leaving direct user grants untouched — handy for pausing a whole
team at once.

Any tenant, user or group can also be stated as one JSON document and
re-applied on another instance ("export JSON" in the row menu, "Apply
JSON" in the Permissions panel) — see
[api.md → documents](api.md#documents-export--import).

## What to read next

- [permissions.md](permissions.md) — who can touch which path
- [pairing.md](pairing.md) — let a device create its own account by QR
- [api-keys.md](api-keys.md) — devices, keys, expiry, rotation
- [api.md](api.md) — the raw REST API

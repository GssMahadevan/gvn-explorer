# Permissions (grants, inheritance, expiring shares)

Who may read or write which paths. Managed in the GUI (a **Permissions**
tab for the browsed directory, or a file row's "permissions" action) or
via `PUT /api/v1/perms` — same upsert endpoint either way.

Setup assumed: instance at `http://localhost:8080`, logged-in cookie jar
`jar` (see [tenants-users-groups.md](tenants-users-groups.md)).

## The model in one paragraph

A grant is a row `(path, principal, bits)` — principal is a user or a
group (always `tenant/name`), bits are `read`, `write`, `delete`. Your
effective permission on a path comes from the **nearest ancestor** that
has an explicit grant for you (directly or via one of your groups):
a user grant **wins over** any group grant at the same level; several
group grants **union**; no grant anywhere on the way up = **deny**.
`GET /api/v1/fs` only ever lists what you can read, so "you don't see
it" is the same thing as "you may not read it". Super admins bypass
file ACLs; tenant admins do not.

## Everyday examples

Give the `acme/staff` group read+write on `/team` (everything below it,
inherited):

```sh
curl -b jar -X PUT localhost:8080/api/v1/perms \
  -H 'Content-Type: application/json' \
  -d '{"path":"/team","principal_type":"group","principal":"staff","tenant":"acme","read":true,"write":true}'
```

Give `acme/alice` the same path alone (user grants override group ones):

```sh
curl -b jar -X PUT localhost:8080/api/v1/perms \
  -H 'Content-Type: application/json' \
  -d '{"path":"/team","principal_type":"user","principal":"alice","tenant":"acme","read":true,"write":true,"delete":true}'
```

`PUT /api/v1/perms` is an **upsert**: PUT the same `(path, principal)`
again with different bits and the grant updates. Remove one with the
row's "revoke" action or `DELETE /api/v1/perms/{id}`. `tenant` defaults
to your own; tenant admins can only ever address their tenant.

## Excluding a subtree: the zero grant

Inheritance stops at the nearest ancestor with any grant row for that
principal — so a grant with **all bits false** deliberately carves a
hole out of a wider grant:

```sh
# staff reads everything under / ...
curl -b jar -X PUT localhost:8080/api/v1/perms -H 'Content-Type: application/json' \
  -d '{"path":"/","principal_type":"group","principal":"staff","tenant":"acme","read":true}'
# ... except /private — it will not even appear in their listings
curl -b jar -X PUT localhost:8080/api/v1/perms -H 'Content-Type: application/json' \
  -d '{"path":"/private","principal_type":"group","principal":"staff","tenant":"acme","read":false}'
```

## Expiring shares (the customer flow)

Hand someone access with a built-in sunset. `PUT /api/v1/perms` also
accepts:

- `"expires_days": 3` — from now, fractions ok, or
- `"expires_at": "2026-10-22T00:00:00Z"` — exact cutoff (wins over days)

```sh
# customer acme gets 3 days of read on /deliverables
curl -b jar -X POST localhost:8080/api/v1/users -H 'Content-Type: application/json' \
  -d '{"tenant":"acme","username":"cust42","password":"s3cret"}'
curl -b jar -X PUT localhost:8080/api/v1/perms -H 'Content-Type: application/json' \
  -d '{"path":"/deliverables","principal_type":"user","principal":"cust42","tenant":"acme","read":true,"expires_days":3}'
```

Day 4: the grant is dead — no cron, every request re-evaluates expiry.
The Permissions panel (and `GET /api/v1/perms?path=…`) still lists
lapsed grants marked `expired`, and renewing is just the same PUT with
a new expiry.

Two subtleties worth knowing:

- An **expired grant behaves as if absent**: it gives no bits but does
  *not* block inheritance — a customer who also has read on `/` keeps
  seeing the tree after a per-folder share lapses.
- For a **hard cutoff**, expire the account itself (`expires_days` on
  `POST /api/v1/users`) — that kills logins and live sessions too.

## Checking what someone can do

The Permissions panel shows the grant list for the current path *plus*
"effective for you" at the top. The API equivalent:

```sh
curl -b jar 'localhost:8080/api/v1/perms?path=/team/spec.md'
```

returns the grants on that exact path and an `effective` object with
your own bits — handy in scripts ("may this agent delete X?").

Every grant change lands in the audit trail (`perm_set` /
`perm_delete` events with the before/after bits) — see the Events tab
or the event-log drawer.

## What to read next

- [api-keys.md](api-keys.md) — giving devices the same permissions
- [api.md](api.md) — the raw REST API

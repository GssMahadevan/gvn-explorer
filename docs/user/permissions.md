# Permissions (grants, inheritance, expiring shares)

Who may read or write which paths. Managed in the GUI (a **Permissions**
tab for the browsed directory, or a file row's "permissions" action) or
via `PUT /api/v1/perms` — same upsert endpoint either way.

Setup assumed: instance at `http://localhost:8080`, logged-in cookie jar
`jar` (see [tenants-users-groups.md](tenants-users-groups.md)). A default
instance serves HTTPS with a self-signed cert — use `https://` and add
`-k` to every curl below.

## The model in one paragraph

A grant is a row `(path, principal, bits)` — principal is a user, a group,
**every member of a tenant** (`tenant`), or **every signed-in user** (`all`);
users and groups are always `tenant/name`, bits are `read`, `write`,
`delete`. Your effective permission on a path comes from the **nearest
ancestor** that has an explicit grant for you (directly, via one of your
groups, via your tenant's membership, or via a world grant): at the same
level a user grant **wins over** any group grant, groups **union**, and
both win over `tenant`, which wins over `all` — the more specific layer
shadows the broader ones; no grant anywhere on the way up = **deny**.
`GET /api/v1/fs` only ever lists what you can read, so "you don't see
it" is the same thing as "you may not read it". Super admins bypass
file ACLs; tenant admins do not.

## Granting to a whole tenant, or to everyone

One row can address a whole community instead of enumerating people:

```sh
# every member of acme reads /shared/specs
curl -b jar -X PUT localhost:8080/api/v1/perms -H 'Content-Type: application/json' \
  -d '{"path":"/shared/specs","principal_type":"tenant","principal":"acme","read":true}'
# anyone signed in at all can read the public announcement
curl -b jar -X PUT localhost:8080/api/v1/perms -H 'Content-Type: application/json' \
  -d '{"path":"/public/news.txt","principal_type":"all","principal":"*","read":true}'
```

The Permissions panel labels such rows "acme (all members)" and
"* all signed-in users", and they revoke like any other row. Only super
admins may address *other* tenants (or everyone); a tenant admin's `tenant`
grants naturally only ever reach their own. A zero grant works against
these too: an all-bits-false row for one user on a nearer path carves
that user out of a `tenant` or `all` grant without touching anyone else.

## Tenant home directories

Each tenant automatically has a home directory `/{tenant-name}` — for
`acme` that is `/acme`. It arrives with one ordinary grant row addressed
to `tenant: acme` (read-only), so **every member can read it by default**
and the tenant's own admin can upgrade that row to `rw` or revoke it like
any grant. The home is then a hard partition: only members of that tenant
(and super admins) can reach inside it — to everyone else the directory
does not even appear in listings, no matter what other grants exist. And
inside a home, every grant must address that tenant's own members:
granting to a foreign user, another tenant or `all` there is refused
(422), even for super admins — use the shared zones outside the homes for
content addressed that broadly. Nothing else is world-readable by default:
shared areas open only through explicit per-path grants like the ones
above. (See [tenants-users-groups.md](tenants-users-groups.md) for how
homes are created and what names are allowed.)

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

A path's whole grant list travels as one JSON document too: "Export
JSON" from the Permissions panel, and admins can apply such a document
back — merge mode adds, replace mode also removes rows the document
omits — with a live diff preview before anything is written
([api.md → documents](api.md#documents-export--import)).

## What to read next

- [api-keys.md](api-keys.md) — giving devices the same permissions
- [api.md](api.md) — the raw REST API

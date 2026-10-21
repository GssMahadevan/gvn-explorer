# API keys and devices

How non-browser callers use the same server: IoT devices (ESP32 pulling
firmware), scripts, CI jobs delivering artifacts. No sessions, no
cookies — one **bearer token** per device, subject to exactly the same
permissions model as a human.

## The model in one paragraph

A device is just a **user with no password**: create it with an empty
password and password-login becomes structurally impossible for it.
Access comes from **auth factors** — API keys attached to that user.
The plaintext key is shown **exactly once** at creation (the DB keeps
only a sha256 hash), it can carry its own expiry, and revocation bites
on the very next request because there is no session to age out. Keys
are labelled (`"label":"v2"`), so an old and a new key can coexist
while a fleet is rotated, and `GET .../factors` reports `last_used_at`
per key — your fleet-health view.

## Provisioning a device (as an admin)

GUI: Users tab → row menu → **new API key** (a dialog shows the
plaintext once) — or with curl:

```sh
# 1. the device user, no password
curl -b jar -X POST localhost:8080/api/v1/users -H 'Content-Type: application/json' \
  -d '{"tenant":"home1","username":"node7","description":"garage esp32"}'

# 2. a key that lapses in 90 days
curl -b jar -X POST localhost:8080/api/v1/users/7/factors -H 'Content-Type: application/json' \
  -d '{"method":"apikey","label":"v1","expires_days":90}'
# -> {"key":"...plain text, shown only once..."}

# 3. give it read on exactly one directory — nothing else exists for it
curl -b jar -X PUT localhost:8080/api/v1/perms -H 'Content-Type: application/json' \
  -d '{"path":"/firmware","principal_type":"user","principal":"node7","tenant":"home1","read":true}'
```

Now anything the device does is one header:

```sh
curl -H "Authorization: Bearer $KEY" 'localhost:8080/api/v1/fs/raw?path=/firmware/modelx.bin'
```

Bearer requests never need a CSRF header and never log in; rate
limiting still applies per principal (default 20 req/s, burst 60 — one
runaway device cannot melt the download endpoint; over the limit you
get `429` with `Retry-After`).

## Firmware pattern: poll cheaply with ETags

`/fs/raw` sends an `ETag` derived from size+mtime. Store it and poll
with `If-None-Match`; unchanged images come back as a bodiless `304`:

```sh
ETAG=$(curl -sI -H "Authorization: Bearer $KEY" \
  'localhost:8080/api/v1/fs/raw?path=/firmware/modelx.bin' | awk -F': ' '/^ETag/{print $2}')
curl -s -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer $KEY" -H "If-None-Match: $ETAG" \
  'localhost:8080/api/v1/fs/raw?path=/firmware/modelx.bin'   # 304 = stay on current
```

Prefer `If-None-Match` over `If-Modified-Since` — ESP clocks lie.

## Device writes / CI delivery

With `w` on a path a device can write files the same way the GUI does:

```sh
# text/JSON body
curl -X POST localhost:8080/api/v1/fs/write -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"path":"/telemetry/home1/node7.json","content":"{\"temp\":21.5}"}'

# multipart upload (any bytes, e.g. a CI artifact)
curl -X POST localhost:8080/api/v1/fs/upload -H "Authorization: Bearer $KEY" \
  -F path=/artifacts -F file=@build-bundle.tgz
```

`fs/write` optionally takes `"base_mtime"` — the mtime you last read —
and answers `409 conflict` instead of clobbering a newer file.

## Rotating and revoking

```sh
curl -b jar localhost:8080/api/v1/users/7/factors        # list, incl. last_used_at
curl -b jar -X DELETE localhost:8080/api/v1/users/7/factors/12   # revoke: dead at once
```

Rotation without downtime: issue `v2`, ship it in a firmware update,
delete `v1` once every node reports the new one. An **expired or
suspended account outranks a live key**: pause the device user and its
keys stop working instantly, keys and history intact.

Secrets never leak into the audit trail — `factor_create/delete`
events record method, label and expiry only, never the key or its hash.

(Tenant admins manage factors for their own tenant's users only. The
factor table also reserves `ed25519` and `oauth2` methods — asking for
one today returns a clean `422 not_implemented`.)

## What to read next

- [pairing.md](pairing.md) — invites that hand a device its first key
- [permissions.md](permissions.md) — what the key may touch
- [api.md](api.md) — the full REST reference

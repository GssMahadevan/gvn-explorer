# Cross-device clipboards

Getting a file — or a quick note — from one device to another without
emailing it to yourself. A clipboard here is a **named link feed shared
by a tenant**: it holds links to files, never copies of them. Your phone
shares a photo; your laptop opens the same bytes from the same path,
under the same permissions.

Setup assumed: instance at `http://localhost:8080`, logged-in cookie jar
`jar` (see [tenants-users-groups.md](tenants-users-groups.md)). A default
instance serves HTTPS with a self-signed cert — use `https://` and add
`-k` to every curl below.

## The model in one paragraph

A clipboard is a name (like `work-notes`) plus a list of links to real
files, visible to every member of your tenant. It appears as a folder in
your home directory — `/tenants/{your-tenant}/clipboards/` — and each
link shows up inside it as a normal file entry, named `{id}-{basename}`.
Opening that entry streams **the target file itself**, so there is one
copy of every byte and one set of permissions. The link is only ever as
strong as the file's own ACLs: **posting** requires you to read the
target at post time, and every later **open** re-checks read — a share
can never outlive or outrank what you may see. If the target is deleted,
the link stays listed but is flagged dead, and opening it says so.
Anyone in the tenant may create a feed and post to one; only the
creator or a tenant admin may delete one (its permission rows go with
it). New feeds arrive with one ordinary read grant addressed to the
whole tenant — visible in the Permissions panel, revocable like any row.

## In the GUI

Open any file's ⋮ row menu → **share to clipboard**: pick an existing
feed or name a new one and what kind of things it holds (text, images,
video, audio, PDFs — or anything). Then on your other device open
**Files → your home → clipboards → the feed** and click the link —
it previews exactly like the file itself. The open file page has a
**Copy** button that puts the text (or an image) into your device's
system clipboard, falling back to copying the file's link when the
browser won't hand over bytes. For notes with no file yet, the
**Clip text** button on any writable folder's toolbar saves what you
type as a real file there and shares it — no hidden storage outside the
permission model.

## With curl

```sh
# create a feed (name: lowercase, dashes; kind: */* text/plain image/*
# video/* audio/* application/pdf — the kind is only what the feed is
# *for*; the server never guesses beyond this allowlist)
curl -b jar -X POST localhost:8080/api/v1/clipboards \
  -H 'Content-Type: application/json' \
  -d '{"name":"work-notes","mime":"text/plain"}'
# -> {"id":1,...,"link_path":"/tenants/acme/clipboards/work-notes"}

# post a link (you must be able to read the target right now)
curl -b jar -X PUT localhost:8080/api/v1/clipboards/1/items \
  -H 'Content-Type: application/json' -d '{"path":"/reports/q3.pdf"}'
# -> {"id":1,...,"link_path":"/tenants/acme/clipboards/work-notes/1-q3.pdf"}

# the other device: read it through the link, same ACLs, same bytes
curl -b jar localhost:8080/api/v1/fs/raw?path=/tenants/acme/clipboards/work-notes/1-q3.pdf

# list feeds / list items (unreadable targets hidden, dead ones flagged)
curl -b jar localhost:8080/api/v1/clipboards
curl -b jar localhost:8080/api/v1/clipboards/1/items

# browse the tree with the file endpoints instead
curl -b jar 'localhost:8080/api/v1/fs?path=/tenants/acme/clipboards/work-notes'

# delete a feed (creator or tenant admin); its grants go with it
curl -b jar -X DELETE localhost:8080/api/v1/clipboards/1
```

The whole file API works inside the tree — listings, `/content`, `/raw`
with range requests, downloads. What does **not** work is writing to
it: it is rendered from the feed list, so mkdir/write/rename/delete in
there answer "read-only; manage feeds via /api/v1/clipboards". Other
tenants never see your feeds: their path into your home is sealed, and
a foreign feed id is a plain 404.

## Housekeeping

Feeds are bounded, not forever: each keeps at most `-clip-max-items`
live links (default 50), and links expire after `-clip-ttl` (default
24 h). Expired and dead-on-arrival links are cleaned up lazily when a
feed is read — there is no background sweeper. A deleted target stays
listed until the link itself times out, so "it says dead" is honest
information, not a stale ghost you have to chase.

## What to read next

- [Permissions](permissions.md) — why a link inherits nothing: the
  target's own grants decide every open.
- [Tenants, users and groups](tenants-users-groups.md) — feeds are
  tenant-scoped; homes are where they live.
- [API keys and devices](api-keys.md) — a device account posting to a
  feed is the same curl above with `Authorization: Bearer`.

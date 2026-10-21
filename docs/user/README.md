# File Explorer — user guide

You are reading the built-in help: this guide ships inside the server
binary and is readable by **every logged-in user** at `/help/…` — the
same read-only documentation tree you opened from the app menu
("Help"). Nothing here needs an admin grant, and nothing here is
writable.

What the system is: files under one root folder, shared in a browser,
with per-path permissions, tenant isolation, expiring customer shares
and API keys for devices. Every change is in the audit trail (the
Events tab). In one line: this is a media *mirror* — the library
lives here, and everyone with a grant sees the same copy — not a
WhatsApp restore source that chat apps re-import from.

Start here, by what you want to do:

| I want to… | Read |
|---|---|
| add a customer, a coworker or a device account | [Tenants, users and groups](tenants-users-groups.md) |
| give someone access to a folder, or cut it off later | [Permissions](permissions.md) |
| hand out time-limited access to a customer | [Permissions → expiring shares](permissions.md#expiring-shares-the-customer-flow) |
| pair a TV, console or walk-up device by QR code | [Pairing a device](pairing.md) |
| let a device / CI job talk to the server | [API keys and devices](api-keys.md) |
| script the whole thing with curl | [REST API for non-browser clients](api.md) |

Every guide's examples are plain `curl` against the running server, so
you can copy them as-is. The guides cross-reference each other; each
ends with "what to read next".

> Deployment notes (running the Docker image, volumes, ports, backups)
> live in the repository's `deployments/docker/README.md` — they are
> for whoever operates this instance, not for day-to-day use.

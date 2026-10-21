# Pairing a device (QR invite)

The "enter the code on the TV" flow. An admin opens a one-time invite;
the other device — a TV, a console, a tablet, a walk-up colleague —
scans a QR code or types a short code, and is signed in. No account
sign-up dance on a small screen, no password typed with a remote.

## How it works

1. On the **Pairings** tab (admins only) press **Pair device** and say
   what the device is ("living room tv"). Optionally make the invite
   long-lived, ask for an API key, or set the account to expire.
2. The dialog shows a **QR code** and a **7-character code** — the
   letters are chosen so nobody confuses them on a TV keyboard (no
   I/L/O/U). Both are shown exactly once.
3. On the device side the link opens `/join` — a public page with a
   **Join** button (a typed code also works). The server creates a
   plain account **in the tenant that opened the invite** and logs the
   browser in on the spot, with a session cookie.
4. The generated **password** — and the API key, if you asked for one —
   are displayed once, on the join screen, and offered straight to the
   browser's password manager. Copy them now or lose them.

The new account has **no grants**: it lands with an empty file list on
purpose. Grant it paths afterwards (Users tab → permissions, or
curl), exactly like any other account.

## What an invite is

- **Single-use.** Redeeming it burns it; the list then marks it *used*
  and names the account it created.
- **Expiring.** Default 10 minutes; an admin may set anything from
  30 seconds to 24 hours per invite. An unredeemed invite past its TTL
  reads *expired* and answers `410` on join.
- **Revocable.** *Revoke* in the row menu stamps it dead without
  deleting history — a redeem attempt then answers `409`, as does a
  spent invite.
- **Scoped.** Invites belong to a tenant; a tenant admin sees and
  revokes only their own. Everything is in the Events audit trail.

## curl

```sh
# open an invite (lives 10 minutes unless ttl_seconds says otherwise)
curl -b jar -X POST localhost:8080/api/v1/pairings -H 'Content-Type: application/json' \
  -d '{"description":"living room tv","apikey":true,"ttl_seconds":600}'
# -> {"pairing":{...},"token":"...","code":"8FK2SD3"} — token and code shown once

# on the device side: redeem the QR token *or* the typed code, never both
curl -c jar2 -s -X POST localhost:8080/api/v1/join -H 'Content-Type: application/json' \
  -d '{"code":"8FK2SD3"}'
# -> {"user":{...},"password":"shown once","apikey":"..."} + session cookie in jar2

# now grant it something to see
curl -b jar -X PUT localhost:8080/api/v1/perms -H 'Content-Type: application/json' \
  -d '{"path":"/media","principal_type":"user","principal":"dev-8fk2sd","tenant":"home1","read":true}'
```

A preset `"username"` in the create call names the account instead of
generating a `dev-…` name; if that name already exists the invite is
refused at create time (`409`), not discovered later at the TV.

Bad claims answer distinctly: unknown code or token `404`, expired
`410`, used-or-revoked `409`, both secrets in one request `422`.

## Why it is safe to put on a poster

The QR link carries its claim token in the **URL fragment**
(`https://host/join#<token>`), which browsers never send to servers —
it appears in no access log. The server stores only **hashes** of the
token and the short code, so a database leak exposes nothing
redeemable. `/join` is public but rate-limited like login, and the
same server-side cap on simultaneous password hashing covers it.

## What to read next

- [api-keys.md](api-keys.md) — the API key an invite can hand out, rotation, expiry
- [permissions.md](permissions.md) — what to grant the paired account
- [tenants-users-groups.md](tenants-users-groups.md) — expiring accounts, suspension

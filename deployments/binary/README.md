# File Explorer — self-contained build

This archive contains everything the server needs except your data:

- `gvn-explorer` (or `gvn-explorer.exe`) — one static server binary. The
  REST API, the built-in help (`/help`) and all accounts/permissions
  logic live inside it.
- `web/` — the browser GUI the server serves at `/`.
- `README.md` — this file.

## Run it

Unpack, `cd` into the folder, and start it — no flags needed:

```sh
./gvn-explorer-linux-amd64        # Windows: gvn-explorer-windows-amd64.exe
```

It creates `./data/` (the file tree it exposes) and `./perms.db`
(accounts, permissions, audit), then serves **http://localhost:8080**
with a fresh super admin `system / admin / admin` — open the URL, log
in, and change the admin password from the Users tab (or start with
`-admin system:admin:<your-pass>` on a fresh install, since that
bootstrap only runs while no users exist).

## Usual knobs

`./gvn-explorer -h` lists everything; the ones people actually use:

| flag / env var | default | meaning |
|---|---|---|
| `-addr` / `ADDR` | `:8080` | listen address |
| `-root` / `ROOT` | `data` | folder exposed to the explorer |
| `-db` / `DB_PATH` | `perms.db` | SQLite sidecar (never stores file contents) |
| `-admin` / `ADMIN` | `system:admin:admin` | bootstrap super admin (first start only) |
| `-web` / `WEB_DIR` | `web` | GUI bundle directory |

## Backup & move

Stop the server, copy `data/` + `perms.db` next to the new binary, start
there. Upgrading = swapping the binary; the schema migrates itself.

The built-in user guide is inside the app: brand-menu **Help**, or the
read-only `help/` folder in the file explorer. Operators: the Docker
image and its knobs are documented at
`deployments/docker/README.md` in the source repo.

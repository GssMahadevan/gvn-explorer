#!/usr/bin/env python3
"""Cross-tenant / permission / path-fuzz matrix checks for the file API.

Point it at any running instance and it builds throwaway tenants, users,
groups and files, then verifies the three things a server like this one
must never get wrong:

  cross  cross-tenant attacks: can a principal, grant, group, API key or
         audit row from tenant A leak into (or act inside) tenant B?
  acl    within-tenant read/write/delete matrix: deny-by-default, bit
         separation, nearest-ancestor inheritance, zero-grant subtrees,
         user-overrides-group, expiry, suspension, group membership.
  paths  hostile path inputs: "../", encoded traversals (%2e%2e), null
         bytes, double encodings, shell/URL metacharacters, emoji,
         over-long names — every input must be rejected cleanly or
         normalise back inside the sandbox; it must never 500 and never
         escape the file root.

Standard library only — no pip installs. Works over plain HTTP or over
https with a self-signed cert (--insecure).

Usage:
    python3 scripts/test_matrix.py --base-url http://127.0.0.1:8098 \
        [--admin-tenant system --admin-user admin --admin-pass admin] \
        [--insecure] [--only cross,acl] [--keep]

Fixtures are namespaced by a random run id, so repeated or concurrent
runs never collide with each other or with hand-made data; teardown
deletes everything it created (--keep to inspect it). Exit code: 0 all
green, 1 some check failed, 2 could not set up.

Reusing this file in another project: the Api class below is the only
adapter between the checks and the wire format. If the other project
exposes the same REST contract, the file runs as-is; if its contract
differs, only Api changes — never a check.
"""

import argparse
import json
import random
import ssl
import string
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from http.cookiejar import CookieJar


def enc(p):
    """Percent-encode a path for use as a query value (server decodes once)."""
    return urllib.parse.quote(p, safe="")


class Client:
    """One principal's HTTP session: gets a session cookie from login, or
    sends a bearer key instead."""

    def __init__(self, base, tls_ctx=None):
        self.base = base.rstrip("/")
        handlers = [urllib.request.HTTPCookieProcessor(CookieJar())]
        if tls_ctx is not None:
            handlers.append(urllib.request.HTTPSHandler(context=tls_ctx))
        self.opener = urllib.request.build_opener(*handlers)

    def call(self, method, path, body=None, bearer=None):
        """-> (status, parsed_json_or_None, raw_bytes). HTTP statuses are
        return values, never exceptions. 429 is quietly retried: the
        server rate limits logins (3rps) and principals (20rps), and this
        script calls fast."""
        url = self.base + path
        data = json.dumps(body).encode() if body is not None else None
        for _ in range(8):
            req = urllib.request.Request(url, method=method)
            if data is not None:
                req.add_header("Content-Type", "application/json")
            if bearer:
                req.add_header("Authorization", "Bearer " + bearer)
            try:
                with self.opener.open(req, data) as resp:
                    code, raw = resp.status, resp.read()
            except urllib.error.HTTPError as e:
                code, raw = e.code, e.read()
            if code == 429:
                time.sleep(0.7)
                continue
            try:
                obj = json.loads(raw.decode("utf-8"))
            except (ValueError, UnicodeDecodeError):
                obj = None
            return code, obj, raw
        return 429, None, b""


class Api:
    """The adapter layer. Every check in this file talks to the server only
    through these methods — this is the single place to port if another
    project's API differs; the checks themselves never touch URLs."""

    def __init__(self, client):
        self.c = client

    # --- session
    def login(self, tenant, user, password):
        return self.c.call("POST", "/api/v1/auth/login",
                           {"tenant": tenant, "username": user, "password": password})

    def me(self):
        return self.c.call("GET", "/api/v1/auth/me")

    # --- file verbs
    def ls(self, path):
        return self.c.call("GET", "/api/v1/fs?path=" + enc(path))

    def raw(self, path):
        return self.c.call("GET", "/api/v1/fs/raw?path=" + enc(path))

    def mkdir(self, path):
        return self.c.call("POST", "/api/v1/fs/mkdir", {"path": path})

    def write(self, path, content):
        return self.c.call("POST", "/api/v1/fs/write", {"path": path, "content": content})

    def rename(self, src, dst):
        return self.c.call("POST", "/api/v1/fs/rename", {"from": src, "to": dst})

    def rm(self, path):
        return self.c.call("DELETE", "/api/v1/fs?path=" + enc(path))

    def draft_get(self, path):
        return self.c.call("GET", "/api/v1/fs/draft?path=" + enc(path))

    def draft_put(self, path, content):
        return self.c.call("PUT", "/api/v1/fs/draft", {"path": path, "content": content})

    def draft_commit(self, path):
        return self.c.call("POST", "/api/v1/fs/draft/commit", {"path": path})

    # --- tenants (super admin)
    def list_tenants(self):
        return self.c.call("GET", "/api/v1/tenants?limit=500")

    def create_tenant(self, name):
        return self.c.call("POST", "/api/v1/tenants", {"name": name})

    def tenant_active(self, tid, active):
        return self.c.call("POST", "/api/v1/tenants/%d/active" % tid, {"active": active})

    def delete_tenant(self, tid):
        return self.c.call("DELETE", "/api/v1/tenants/%d" % tid)

    # --- users / groups / grants (admin)
    def users(self, **params):
        q = "&".join("%s=%s" % (k, v) for k, v in params.items())
        return self.c.call("GET", "/api/v1/users?limit=500" + ("&" + q if q else ""))

    def create_user(self, username, password, tenant=None, admin=False, expires_at=None):
        body = {"username": username, "password": password, "is_admin": admin}
        if tenant is not None:
            body["tenant"] = tenant
        if expires_at is not None:
            body["expires_at"] = expires_at
        return self.c.call("POST", "/api/v1/users", body)

    def delete_user(self, uid):
        return self.c.call("DELETE", "/api/v1/users/%d" % uid)

    def user_active(self, uid, active):
        return self.c.call("POST", "/api/v1/users/%d/active" % uid, {"active": active})

    def create_key(self, uid, label="matrix"):
        return self.c.call("POST", "/api/v1/users/%d/factors" % uid,
                           {"method": "apikey", "label": label})

    def delete_key(self, uid, fid):
        return self.c.call("DELETE", "/api/v1/users/%d/factors/%d" % (uid, fid))

    def groups(self):
        return self.c.call("GET", "/api/v1/groups?limit=500")

    def create_group(self, name, tenant=None):
        body = {"name": name}
        if tenant is not None:
            body["tenant"] = tenant
        return self.c.call("POST", "/api/v1/groups", body)

    def delete_group(self, gid):
        return self.c.call("DELETE", "/api/v1/groups/%d" % gid)

    def add_member(self, gid, username):
        return self.c.call("POST", "/api/v1/groups/%d/members" % gid, {"username": username})

    def remove_member(self, gid, uid):
        return self.c.call("DELETE", "/api/v1/groups/%d/members/%d" % (gid, uid))

    def group_active(self, gid, active):
        return self.c.call("POST", "/api/v1/groups/%d/active" % gid, {"active": active})

    def grant(self, path, principal_type, principal, read=False, write=False,
              delete=False, tenant=None, expires_at=None, expires_days=None):
        body = {"path": path, "principal_type": principal_type, "principal": principal,
                "read": read, "write": write, "delete": delete}
        if tenant is not None:
            body["tenant"] = tenant
        if expires_at is not None:
            body["expires_at"] = expires_at
        if expires_days is not None:
            body["expires_days"] = expires_days
        return self.c.call("PUT", "/api/v1/perms", body)

    def delete_grant(self, gid):
        return self.c.call("DELETE", "/api/v1/perms/%d" % gid)

    def events(self):
        return self.c.call("GET", "/api/v1/events?limit=200")


class Rep:
    """Prints one line per check, like the shell suites; collects failures."""

    def __init__(self):
        self.passed = 0
        self.failed = []

    def ck(self, desc, cond):
        if cond:
            self.passed += 1
            print("  PASS " + desc, flush=True)
        else:
            self.failed.append(desc)
            print("  FAIL " + desc, flush=True)

    def want(self, res, codes, desc):
        """Assert the HTTP status of a call. An unexpected 5xx always
        fails, which is what makes the fuzz suites meaningful."""
        code = res[0]
        allowed = {codes} if isinstance(codes, int) else set(codes)
        if code not in allowed:
            desc += " (got %d, want %s)" % (code, "/".join(map(str, sorted(allowed))))
        self.ck(desc, code in allowed)


def fresh(x):
    """A new unauthenticated session against the same server."""
    return Api(Client(x.base, x.tls_ctx))


def names(api, path):
    """Sorted entry names under path, or None if the listing failed."""
    st, obj, _ = api.ls(path)
    if st != 200 or not isinstance(obj, dict):
        return None
    return sorted(e["entry"]["name"] for e in obj.get("entries", []))


def paths_within_root(obj):
    """Every listed entry must carry a clean absolute path: this is what
    'never escapes the sandbox' looks like from the outside."""
    return all(e["entry"]["path"].startswith("/")
               and ".." not in e["entry"]["path"].split("/")
               for e in obj.get("entries", []))


class Fix:
    """All names created for one run, namespaced by a random id so runs
    never collide with each other or with hand-made data."""

    def __init__(self, args):
        self.base = args.base_url.rstrip("/")
        self.tls_ctx = ssl._create_unverified_context() if args.insecure else None
        rid = self.rid = "".join(random.choice(string.ascii_lowercase + string.digits)
                                 for _ in range(6))
        self.ta, self.tb = rid + "alpha", rid + "beta"
        self.alice, self.mallory, self.dev = "alice" + rid, "mallory" + rid, "dev" + rid
        self.bob, self.carol, self.dup = "bob" + rid, "carol" + rid, "dup" + rid
        self.crew, self.shared = "crew" + rid, "shared" + rid
        self.pw = "Matrix-" + rid + "!x1"
        self.mx, self.fx, self.ct = "/mx" + rid, "/fx" + rid, "/ct"
        self.uid, self.gid, self.tid, self.sess = {}, {}, {}, {}
        self.super = None


def die(msg):
    print("SETUP FAIL: " + msg, flush=True)
    sys.exit(2)


# The ACL fixture tree, built once by the super admin. mx is the sandbox
# the acl suite grants over; ct is the shared-tree file the cross suite
# grants across tenants.
MX_FILES = {"pub/notes.txt": "TOP-SECRET", "pub/sub/deep.txt": "DEEP-SECRET",
            "gate/inside.txt": "GATE-SECRET", "zero/hidden.txt": "ZERO-SECRET",
            "solo.txt": "SOLO-CONTENT"}


def setup(x, args):
    sup = x.super = Api(Client(x.base, x.tls_ctx))
    st, _, _ = sup.login(args.admin_tenant, args.admin_user, args.admin_pass)
    if st != 200:
        die("admin login returned %d" % st)

    for t in (x.ta, x.tb):
        st, obj, _ = sup.create_tenant(t)
        if st != 201:
            die("create tenant %s returned %d" % (t, st))
        x.tid[t] = obj["id"]

    for t, u, is_admin in [(x.ta, x.alice, True), (x.ta, x.mallory, False),
                           (x.ta, x.dev, False), (x.ta, x.dup, False),
                           (x.tb, x.dup, False), (x.tb, x.bob, True),
                           (x.tb, x.carol, False)]:
        st, obj, _ = sup.create_user(u, x.pw, tenant=t, admin=is_admin)
        if st != 201:
            die("create user %s@%s returned %d" % (u, t, st))
        x.uid[(t, u)] = obj["id"]

    for t, g in [(x.ta, x.crew), (x.ta, x.shared), (x.tb, x.shared)]:
        st, obj, _ = sup.create_group(g, tenant=t)
        if st != 201:
            die("create group %s@%s returned %d (shared names must be "
                "tenant-unique)" % (g, t, st))
        x.gid[(t, g)] = obj["id"]

    for d in [x.mx, x.mx + "/pub", x.mx + "/pub/sub", x.mx + "/gate",
              x.mx + "/zero", x.ct]:
        st, _, _ = sup.mkdir(d)
        if st not in (200, 201):
            die("mkdir %s returned %d" % (d, st))
    for rel, body in MX_FILES.items():
        st, _, _ = sup.write(x.mx + "/" + rel, body)
        if st != 200:
            die("write %s returned %d" % (rel, st))
    st, _, _ = sup.write(x.ct + "/secret.txt", "CT-SECRET")
    if st != 200:
        die("write %s/secret.txt returned %d" % (x.ct, st))

    for name, (t, u) in [("alice", (x.ta, x.alice)), ("mallory", (x.ta, x.mallory)),
                         ("bob", (x.tb, x.bob)), ("carol", (x.tb, x.carol))]:
        api = Api(Client(x.base, x.tls_ctx))
        st, _, _ = api.login(t, u, x.pw)
        if st != 200:
            die("login %s@%s returned %d" % (u, t, st))
        x.sess[name] = api


def teardown(x, keep):
    if keep:
        print("(--keep: fixtures left behind, run id " + x.rid + ")")
        return
    sup = x.super
    for gid in x.gid.values():
        sup.delete_group(gid)
    for uid in x.uid.values():
        sup.delete_user(uid)
    for tid in x.tid.values():
        sup.delete_tenant(tid)
    for p in (x.mx, x.fx, x.ct, "/up-" + x.rid, "/out-" + x.rid):
        sup.rm(p)  # best effort: the fuzz suite may not have created these


def suite_cross(x, rep):
    print("== cross: cross-tenant attacks", flush=True)
    a, m, b, c, sup = (x.sess["alice"], x.sess["mallory"], x.sess["bob"],
                       x.sess["carol"], x.super)
    rid = x.rid

    st, _, _ = sup.c.call("GET", "/healthz")
    rep.want((st,), (200,), "server answers /healthz")

    # --- identity is scoped by tenant
    rep.want(fresh(x).login(x.tb, x.alice, x.pw), (401,),
             "same credentials cannot log in to the other tenant")
    rep.want(fresh(x).login(x.ta, x.dup, x.pw), (200,),
             "colliding username logs in in tenant A")
    rep.want(fresh(x).login(x.tb, x.dup, x.pw), (200,),
             "...and independently in tenant B")

    # --- admin listings never cross
    st, rows, _ = a.users()
    rep.want((st,), (200,), "tenant admin can list users")
    rep.ck("A's user list contains only A's rows",
           st == 200 and rows and all(r["tenant"] == x.ta for r in rows)
           and x.bob not in [r["username"] for r in rows])
    st2, rows2, _ = a.users(tenant=x.tb)
    rep.ck("?tenant= of another tenant yields no foreign rows for a tenant admin",
           st2 == 200 and rows2 is not None
           and all(r["tenant"] == x.ta for r in rows2))
    st, grows, _ = a.groups()
    rep.ck("A's group list contains only A's rows",
           st == 200 and grows and all(g["tenant"] == x.ta for g in grows))

    # --- tenant management is super-only
    rep.want(a.list_tenants(), (403,), "tenant admin cannot list tenants")
    rep.want(a.create_tenant(rid + "nope"), (403,), "tenant admin cannot create tenants")
    rep.want(a.tenant_active(x.tid[x.tb], False), (403,),
             "tenant admin cannot suspend another tenant")

    # --- admin creates are pinned to their own tenant
    st, _, _ = a.create_user("newguy" + rid, x.pw, tenant=x.tb)
    rep.want((st,), (201,), "cross-tenant create request is accepted (pinned)")
    rep.want(fresh(x).login(x.ta, "newguy" + rid, x.pw), (200,),
             "...but the user lands in the caller's tenant")
    st, brows, _ = b.users()
    rep.ck("...and never surfaces in B's user list",
           st == 200 and "newguy" + rid not in [r["username"] for r in brows])

    # --- grants cannot reach across tenants
    rep.want(a.grant(x.ct, "user", x.bob, read=True, tenant=x.tb), (403,),
             "an A admin cannot grant to a B principal")
    rep.want(a.grant(x.ct, "user", x.bob, read=True), (404,),
             "...and a B name without tenant resolves to no user in A")
    st, gobj, _ = sup.grant(x.ct, "user", x.alice, read=True, tenant=x.ta)
    rep.want((st,), (200, 201), "super grants the shared tree to an A user")
    g1 = (gobj or {}).get("id")
    rep.want(b.delete_grant(g1 or 0), (403,), "a B admin cannot revoke an A grant")
    st, _, _ = a.ls(x.ct)
    rep.want((st,), (200,), "the A grant survived the foreign revoke attempt")

    # --- colliding group names are separate rows
    rep.ck("same group name exists independently in both tenants",
           x.gid[(x.ta, x.shared)] != x.gid[(x.tb, x.shared)])
    st, grows, _ = a.groups()
    mine = [g for g in (grows or []) if g["name"] == x.shared]
    rep.ck("A's group list shows only the A copy of the colliding name",
           len(mine) == 1 and mine[0]["tenant"] == x.ta)
    rep.want(b.add_member(x.gid[(x.tb, x.shared)], x.alice), (400, 403, 404),
             "a B group cannot enroll an A user")

    # --- super reaches everywhere
    st, _, _ = sup.create_user("sup" + rid, x.pw, tenant=x.tb)
    rep.want((st,), (201,), "super creates a user in B")
    st, _, _ = sup.grant(x.ct, "user", x.carol, read=True, tenant=x.tb)
    rep.want((st,), (200, 201), "super grants a B principal on the shared tree")
    st, k, _ = sup.create_key(x.uid[(x.tb, x.bob)])
    rep.want((st,), (201,), "super provisions a key for a B user")
    bob_key = (k or {}).get("key")
    st, _, body = c.raw(x.ct + "/secret.txt")
    rep.ck("a granted B user reads the file A created (shared tree is by design)",
           st == 200 and body == b"CT-SECRET")
    rep.want(m.ls(x.ct), (403,), "an ungranted A user still cannot see that file")

    # --- API keys are tenant-safe identities
    st, ka, _ = a.create_key(x.uid[(x.ta, x.alice)])
    rep.want((st,), (201,), "A admin provisions their own API key")
    alice_key, alice_fid = (ka or {}).get("key"), (ka or {}).get("factor", {}).get("id")
    st, who, _ = Client(x.base, x.tls_ctx).call("GET", "/api/v1/auth/me", bearer=alice_key)
    rep.ck("the key authenticates exactly its owner",
           st == 200 and who and who["username"] == x.alice and who["tenant"] == x.ta)
    _, kda, _ = sup.create_key(x.uid[(x.ta, x.dup)])
    _, kdb, _ = sup.create_key(x.uid[(x.tb, x.dup)])
    ida = Client(x.base, x.tls_ctx).call("GET", "/api/v1/auth/me", bearer=(kda or {}).get("key"))
    idb = Client(x.base, x.tls_ctx).call("GET", "/api/v1/auth/me", bearer=(kdb or {}).get("key"))
    rep.ck("colliding usernames: each key maps to its own tenant",
           ida[0] == 200 and idb[0] == 200 and ida[1]["tenant"] == x.ta
           and idb[1]["tenant"] == x.tb)
    rep.want(a.delete_key(x.uid[(x.ta, x.alice)], alice_fid or 0), (200, 204),
             "revoke the key")
    st, _, _ = Client(x.base, x.tls_ctx).call("GET", "/api/v1/auth/me", bearer=alice_key)
    rep.want((st,), (401,), "revoked key dies on the very next request")

    # --- kill switches act inside one tenant only
    rep.want(sup.tenant_active(x.tid[x.tb], False), (200, 204), "super suspends tenant B")
    rep.want(fresh(x).login(x.tb, x.bob, x.pw), (401, 403), "suspended tenant: login refused")
    st, _, _ = Client(x.base, x.tls_ctx).call("GET", "/api/v1/auth/me", bearer=bob_key)
    rep.want((st,), (401,), "suspended tenant: its API keys die too")
    st, _, _ = a.ls(x.ct)
    rep.want((st,), (200,), "tenant A is untouched by B's suspension")
    rep.want(sup.tenant_active(x.tid[x.tb], True), (200, 204), "super reactivates tenant B")
    rep.want(fresh(x).login(x.tb, x.bob, x.pw), (200,), "B login works again")

    idup = x.uid[(x.ta, x.dup)]
    rep.want(sup.user_active(idup, False), (200,), "suspend the A copy of the colliding name")
    rep.want(fresh(x).login(x.ta, x.dup, x.pw), (401, 403), "the suspended copy cannot log in")
    rep.want(fresh(x).login(x.tb, x.dup, x.pw), (200,), "the B copy of the same name is unaffected")
    rep.want(sup.user_active(idup, True), (200,), "unsuspend it")

    # --- expiry is per row, not per name
    st, _, _ = sup.create_user("exp" + rid, x.pw, tenant=x.ta, expires_at="2020-01-01T00:00:00Z")
    rep.want((st,), (201,), "create an already-expired user in A")
    rep.want(fresh(x).login(x.ta, "exp" + rid, x.pw), (401, 403), "expired user cannot log in")
    st, _, _ = sup.create_user("exp" + rid, x.pw, tenant=x.tb)
    rep.want((st,), (201,), "same username, no expiry, in B")
    rep.want(fresh(x).login(x.tb, "exp" + rid, x.pw), (200,), "the B copy logs in fine")

    # --- audit trail is tenant-scoped
    b.user_active(x.uid[(x.tb, x.carol)], False)
    sup.user_active(x.uid[(x.tb, x.carol)], True)  # audited actions inside B
    st, evs, _ = a.events()
    actors = [e.get("actor", "") for e in (evs or [])]
    rep.ck("A's audit trail never contains B's actors",
           st == 200 and not any(x.bob in s or s.startswith(x.tb + "/") for s in actors))
    st, evs, _ = b.events()
    rep.ck("B does see its own actions (scoping is not just emptiness)",
           st == 200 and any(e.get("actor") == x.tb + "/" + x.bob for e in evs or []))


def suite_acl(x, rep):
    print("== acl: within-tenant read/write/delete matrix", flush=True)
    a, m, sup = x.sess["alice"], x.sess["mallory"], x.super
    mx = x.mx

    # --- deny by default
    rep.want(m.ls(mx), (403,), "no grant: listing refused")
    rep.want(m.raw(mx + "/pub/notes.txt"), (403,), "no grant: read refused")
    rep.want(m.write(mx + "/solo.txt", "x"), (403,), "no grant: write refused")
    rep.want(m.rm(mx + "/solo.txt"), (403,), "no grant: delete refused")
    rep.want(m.mkdir(mx + "/newdir"), (403,), "no grant: mkdir refused")
    rep.want(m.rename(mx + "/solo.txt", mx + "/moved.txt"), (403,), "no grant: rename refused")
    rep.want(m.draft_get(mx + "/pub/notes.txt"), (403,), "no grant: draft refused")

    # --- r opens exactly reads, and inherits to any depth
    st, g, _ = a.grant(mx, "user", x.mallory, read=True)
    rep.want((st,), (200, 201), "grant r on " + mx)
    gmx = (g or {}).get("id")
    st, _, body = m.raw(mx + "/pub/notes.txt")
    rep.ck("r inherited two levels down", st == 200 and body == b"TOP-SECRET")
    st, obj, _ = m.ls(mx + "/pub/sub")
    rep.ck("inherited r makes nested listings work", st == 200)
    rep.ck("list responses carry clean absolute paths", st == 200 and paths_within_root(obj))
    rep.want(m.write(mx + "/solo.txt", "x"), (403,), "r alone never writes")
    rep.want(m.rm(mx + "/solo.txt"), (403,), "r alone never deletes")
    rep.want(m.mkdir(mx + "/newdir"), (403,), "r alone never creates dirs")
    rep.want(m.draft_put(mx + "/pub/notes.txt", "DRAFT"), (403,), "r alone cannot save a draft")
    rep.want(m.draft_get(mx + "/pub/notes.txt"), (200,), "reading a draft needs only r")

    # --- w/d bits are separate levers
    a.grant(mx, "user", x.mallory, read=True, write=True)
    st, _, _ = m.write(mx + "/created-by-w.txt", "W")
    rep.want((st,), (200, 201), "w creates new files")
    rep.want(m.mkdir(mx + "/made-dir"), (200, 201), "w creates directories")
    rep.want(m.rm(mx + "/created-by-w.txt"), (403,), "w without d cannot delete")
    rep.want(m.rename(mx + "/solo.txt", mx + "/moved.txt"), (403,),
             "rename needs d on the source: refused without it")
    a.grant(mx, "user", x.mallory, read=True, write=True, delete=True)
    rep.want(m.rename(mx + "/solo.txt", mx + "/moved.txt"), (200,),
             "rename (d on source + w on target) works with r+w+d")
    rep.want(m.rm(mx + "/moved.txt"), (200, 204), "d deletes")

    # --- an explicit zero grant carves a hole in an inherited grant
    st, gz, _ = a.grant(mx + "/zero", "user", x.mallory)
    rep.want((st,), (200, 201), "zero grant (all bits false) on " + mx + "/zero")
    gzero = (gz or {}).get("id")
    got = names(m, mx)
    rep.ck("carved-out subtree is hidden from the parent listing",
           got is not None and "zero" not in got and "pub" in got)
    rep.want(m.ls(mx + "/zero"), (403,), "zero-grant subtree: listing refused")
    rep.want(m.raw(mx + "/zero/hidden.txt"), (403,), "zero-grant subtree: read refused")
    st, _, _ = sup.raw(mx + "/zero/hidden.txt")
    rep.ck("super admin still sees the carved-out subtree (super bypasses ACLs)",
           st == 200)
    rep.want(a.delete_grant(gmx or 0), (200, 204), "remove the inherited mx grant")
    rep.want(a.delete_grant(gzero or 0), (200, 204), "remove the zero grant")
    rep.want(m.ls(mx), (403,), "back to deny-by-default after revoke")

    # --- group membership is the gate to group grants
    st, gg, _ = a.grant(mx + "/gate", "group", x.crew, read=True)
    rep.want((st,), (200, 201), "grant r on " + mx + "/gate to group " + x.crew)
    rep.want(m.raw(mx + "/gate/inside.txt"), (403,), "non-member does not get the group's grant")
    rep.want(a.add_member(x.gid[(x.ta, x.crew)], x.mallory), (200, 201, 204),
             "enroll the user")
    st, _, body = m.raw(mx + "/gate/inside.txt")
    rep.ck("membership grants access immediately", st == 200 and body == b"GATE-SECRET")
    rep.want(a.group_active(x.gid[(x.ta, x.crew)], False), (200,), "suspend the group")
    rep.want(m.raw(mx + "/gate/inside.txt"), (403,), "suspended group: its grants are inert")
    rep.want(a.group_active(x.gid[(x.ta, x.crew)], True), (200,), "reactivate the group")
    rep.want(m.raw(mx + "/gate/inside.txt"), (200,), "access returns with the group")

    # --- nearest grant wins: user beats group
    st, gu, _ = a.grant(mx + "/gate", "user", x.mallory)
    rep.want((st,), (200, 201), "explicit zero user grant on the group-granted path")
    ggate = (gu or {}).get("id")
    rep.want(m.raw(mx + "/gate/inside.txt"), (403,), "user grant overrides group grant (deny)")
    a.grant(mx + "/gate", "user", x.mallory, read=True)
    rep.want(m.raw(mx + "/gate/inside.txt"), (200,), "user grant overrides group grant (allow)")
    rep.want(a.delete_grant(ggate or 0), (200, 204), "delete the user-level grant again")
    rep.want(a.remove_member(x.gid[(x.ta, x.crew)], x.uid[(x.ta, x.mallory)]), (200, 204),
             "un-enroll the user")
    rep.want(m.raw(mx + "/gate/inside.txt"), (403,), "un-enrolled: group access gone")

    # --- grant expiry
    st, ge, _ = a.grant(mx, "user", x.mallory, read=True, expires_at="2020-01-01T00:00:00Z")
    rep.want((st,), (200, 201), "grant with expires_at in the past")
    gexp = (ge or {}).get("id")
    rep.want(m.raw(mx + "/pub/notes.txt"), (403,), "expired grant: read refused")
    a.grant(mx, "user", x.mallory, read=True, expires_days=1)
    rep.want(m.raw(mx + "/pub/notes.txt"), (200,), "same grant, expiring tomorrow: allowed")

    # --- user suspension is a kill switch over all access
    rep.want(a.user_active(x.uid[(x.ta, x.mallory)], False), (200,), "suspend the user")
    rep.want(m.ls(mx), (401,), "the live session dies on the next request")
    rep.want(fresh(x).login(x.ta, x.mallory, x.pw), (401, 403), "suspended user cannot log in")
    rep.want(a.user_active(x.uid[(x.ta, x.mallory)], True), (200,), "unsuspend")
    m2 = fresh(x)
    rep.want(m2.login(x.ta, x.mallory, x.pw), (200,), "login works again")
    st, _, body = m2.raw(mx + "/pub/notes.txt")
    rep.ck("grants resume exactly as they were", st == 200 and body == b"TOP-SECRET")
    m = m2  # keep using the live session

    # --- API keys go through exactly the same ACL engine
    st, k, _ = a.create_key(x.uid[(x.ta, x.dev)])
    dev_key = (k or {}).get("key")
    devc = Client(x.base, x.tls_ctx)
    dev_url = "/api/v1/fs/raw?path=" + enc(mx + "/pub/notes.txt")
    rep.want(devc.call("GET", dev_url, bearer=dev_key), (403,),
             "device user without a grant is refused")
    st, gd, _ = a.grant(mx, "user", x.dev, read=True)
    rep.want((st,), (200, 201), "grant r to the device user")
    gdev = (gd or {}).get("id")
    rep.want(devc.call("GET", dev_url, bearer=dev_key), (200,), "key honors its user's grants")
    rep.want(a.delete_grant(gdev or 0), (200, 204), "revoke the device grant")
    rep.want(devc.call("GET", dev_url, bearer=dev_key), (403,), "revoked on the very next call")

    # --- tenant admins are not supers
    rep.want(a.ls(mx + "/zero"), (403,), "tenant admin does not bypass file ACLs")

    # --- draft/publish needs w to touch, r to look
    a.grant(mx + "/pub/notes.txt", "user", x.mallory, read=True, write=True)
    rep.want(m.draft_get(mx + "/pub/notes.txt"), (200,), "r+w can open a draft")
    rep.want(m.draft_put(mx + "/pub/notes.txt", "EDITED-BY-DRAFT"), (200, 201, 204),
             "r+w can save a draft")
    rep.want(m.draft_commit(mx + "/pub/notes.txt"), (200,), "r+w can publish")
    st, _, body = m.raw(mx + "/pub/notes.txt")
    rep.ck("publish writes through to the file", st == 200 and body == b"EDITED-BY-DRAFT")
    a.grant(mx + "/pub/notes.txt", "user", x.mallory, read=True)
    rep.want(m.draft_put(mx + "/pub/notes.txt", "NOPE"), (403,),
             "after w is revoked, saving a draft is refused")
    rep.want(a.delete_grant(gexp or 0), (200, 204), "cleanup: revoke the mx grant")
    rep.want(m.raw(mx + "/pub/sub/deep.txt"), (403,), "after all revokes: deny by default again")


def suite_paths(x, rep):
    print("== paths: hostile path inputs", flush=True)
    sup = x.super
    fx, rid = x.fx, x.rid

    for d in (fx, fx + "/sub"):
        st, _, _ = sup.mkdir(d)
        if st not in (200, 201):
            die("setup: mkdir %s returned %d" % (d, st))

    # --- '..' is refused outright, in every verb: the strongest possible
    # traversal guard. It must be a clean 4xx, never a 5xx, and nothing
    # may be listed or created.
    for p in [fx + "/..", fx + "/sub/..", "/..", "//..//..", fx + "/a/../../b"]:
        st, _, _ = sup.ls(p)
        rep.ck("list %r refused outright (4xx, never 5xx)" % p, 400 <= st < 500)
    st, _, _ = sup.c.call("GET", "/api/v1/fs?path=" + fx + "/%2e%2e")
    rep.ck("percent-encoded traversal (%2e%2e) decodes to '..' and is refused too",
           400 <= st < 500)
    st, _, _ = sup.ls(fx + "/%2e%2e")  # fully encoded: server sees a literal name
    rep.want((st,), (404,),
             "double-encoded traversal (%252e%252e) is a literal name, cannot escape")

    # --- traversal via mkdir: refused, and creates nothing anywhere
    for p in [fx + "/a/../b", fx + "/../up-" + rid, fx + "/../../up2-" + rid]:
        st, _, _ = sup.mkdir(p)
        rep.ck("mkdir %r refused (4xx)" % p, 400 <= st < 500)
    root_got, fx_got = names(sup, "/"), names(sup, fx)
    rep.ck("refused traversals created nothing at the root or under it",
           root_got is not None and fx_got is not None
           and "up-" + rid not in root_got and "up2-" + rid not in root_got
           and "a" not in fx_got and "b" not in fx_got)
    rep.want(sup.mkdir(fx + "/./x1"), (200, 201), "'./' prefix is normalised away")
    got = names(sup, fx)
    rep.ck("'./x1' created as plain 'x1'", got is not None and "x1" in got)
    rep.want(sup.mkdir(fx + "/tail/"), (200, 201), "trailing slash is normalised away")
    got = names(sup, fx)
    rep.ck("'tail/' created as plain 'tail'", got is not None and "tail" in got)

    # --- metacharacter / unicode names: accept-and-list or
    # reject-and-absent, consistently — and never a 500.
    fuzz = ["a b", "a&b", "a=b", "a'b", 'a"b', "a;b", "a$b", "a`b", "a|b",
            "a*b", "a?b", "a#b", "a%b", "%zz", "a..b", "..;", ".hidden",
            "~t", "smile-\U0001f642", "a;b?c#d"]
    for nm in fuzz:
        st, _, _ = sup.mkdir(fx + "/" + nm)
        got = names(sup, fx)
        created = got is not None and nm in got
        ok = 500 > st >= 400 or (200 <= st < 300)  # clean rejection or success
        if ok:
            ok = created == (200 <= st < 300)
        rep.ck("mkdir %r: consistent create/reject, never 5xx" % nm, ok)

    # --- backslashes are separators (Windows-style input): a name never
    # contains a backslash, it nests instead. Pin that policy.
    rep.want(sup.mkdir(fx + "/bs"), (200, 201), "mkdir the backslash probe parent")
    rep.want(sup.mkdir(fx + "/bs/a"), (200, 201), "mkdir the nested half of the probe")
    rep.want(sup.mkdir(fx + "/bs/a\\b"), (200, 201), "mkdir 'a\\b' nests instead of escaping")
    got = names(sup, fx + "/bs")
    rep.ck("...created 'a' (backslash became a separator, not a literal)",
           got == ["a"])
    got = names(sup, fx + "/bs/a")
    rep.ck("...'b' sits nested under it", got == ["b"])

    # --- hard rejections: NUL, over-long names, missing parents must all
    # be clean 4xx (never 5xx) and create nothing.
    st, _, _ = sup.mkdir(fx + "/nul\x00txt")
    got = names(sup, fx)
    rep.ck("mkdir with an embedded NUL is rejected cleanly (4xx, no 5xx, not created)",
           400 <= st < 500 and (got is None or "nul\x00txt" not in got))
    st, _, _ = sup.mkdir(fx + "/" + "L" * 300)
    rep.ck("mkdir with an over-long name is rejected cleanly (4xx, not 5xx)",
           400 <= st < 500)
    st, _, _ = sup.write(fx + "/" + "L" * 300, "x")
    rep.ck("write with an over-long name is rejected cleanly (4xx, not 5xx)",
           400 <= st < 500)
    st, _, _ = sup.mkdir(fx + "/no-such-parent/kid")
    rep.want((st,), (409,), "mkdir under a missing parent: 409 parent_missing, not 500")

    # --- metachars survive a write+read round trip
    for nm, body in [("q?r=s&t&u", "META"), ("smile-\U0001f642.md", "EMO")]:
        st, _, _ = sup.write(fx + "/" + nm, body)
        rep.want((st,), (200, 201), "write a file named %r" % nm)
        st, _, raw = sup.raw(fx + "/" + nm)
        rep.ck("read it back byte-identical over URL-encoded paths",
               st == 200 and raw == body.encode())

    # --- rename: '..' refused like everywhere else; an absolute target
    # stays inside the sandbox root
    sup.write(fx + "/mv-src", "MV")
    rep.want(sup.rename(fx + "/mv-src", fx + "/sub/../mvd"), (400,),
             "rename via '..' refused (4xx)")
    rep.want(sup.rename(fx + "/mv-src", "/out-" + rid), (200,),
             "rename to an absolute path stays inside the sandbox root")
    got = names(sup, "/")
    rep.ck("...the moved file really appears at the sandbox root",
           got is not None and "out-" + rid in got)
    rep.want(sup.raw(fx + "/mv-src"), (404,), "no stale copy left behind at the source")

    # --- encoded slash in the query is decoded exactly once
    for d in [fx + "/d1", fx + "/d1/d2"]:
        rep.want(sup.mkdir(d), (200, 201), "mkdir for the encoded-slash test")
    st, obj, _ = sup.c.call("GET", "/api/v1/fs?path=" + fx + "%2fd1%2fd2")
    rep.ck("?path with %2F decodes once and resolves the nested dir",
           st == 200 and obj and obj.get("path") == fx + "/d1/d2")
    st, _, _ = sup.write(fx + "/d1/d2/deep.txt", "DEEP")
    st, _, raw = sup.raw(fx + "/d1/d2/deep.txt")
    rep.ck("deep nested write+read round trip", st == 200 and raw == b"DEEP")


SUITES = {"cross": suite_cross, "acl": suite_acl, "paths": suite_paths}


def main():
    ap = argparse.ArgumentParser(
        description="Run the cross-tenant / permission / path matrix checks "
                    "against a running instance (see module docstring).")
    ap.add_argument("--base-url", required=True,
                    help="e.g. http://127.0.0.1:8080 or https://host:8443")
    ap.add_argument("--admin-tenant", default="system")
    ap.add_argument("--admin-user", default="admin")
    ap.add_argument("--admin-pass", default="admin")
    ap.add_argument("--insecure", action="store_true",
                    help="skip TLS verification (for self-signed certs)")
    ap.add_argument("--only", default="cross,acl,paths",
                    help="comma-separated subset of: " + ",".join(SUITES))
    ap.add_argument("--keep", action="store_true",
                    help="leave all fixtures in place after the run")
    args = ap.parse_args()

    chosen = [s.strip() for s in args.only.split(",") if s.strip()]
    unknown = [s for s in chosen if s not in SUITES]
    if unknown:
        print("unknown suite(s): " + " ".join(unknown))
        sys.exit(2)

    x = Fix(args)
    print("== matrix run %s against %s" % (x.rid, x.base), flush=True)
    try:
        setup(x, args)
    except Exception as e:  # noqa: BLE001 - report any setup break as exit 2
        die("%r" % e)

    rep = Rep()
    try:
        for s in chosen:
            SUITES[s](x, rep)
    finally:
        teardown(x, args.keep)

    print("matrix: passed=%d failed=%d" % (rep.passed, len(rep.failed)))
    for f in rep.failed:
        print("  FAILED " + f)
    sys.exit(0 if not rep.failed else 1)


if __name__ == "__main__":
    main()

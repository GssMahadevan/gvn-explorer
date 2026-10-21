package handlers

// Tenant homes + the "tenant"/"all" pseudo-principals: every tenant owns
// /tenants/{name}, only its members (and super) may touch it regardless
// of grant rows, and the default member-browse grant is an ordinary
// revocable row. The home root also seals inheritance: the world row on
// /tenants is traversal only and never reaches inside a home.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// listedPaths returns the entry paths of a successful listing.
func listedPaths(t *testing.T, c *http.Client, url string) []string {
	t.Helper()
	out := must(t, c, "GET", url, "", 200)
	var paths []string
	for _, ev := range out["entries"].([]any) {
		paths = append(paths, ev.(map[string]any)["entry"].(map[string]any)["path"].(string))
	}
	return paths
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func TestTenantHomes(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	// A real server runs this at startup: the /tenants namespace plus
	// homes for pre-existing tenants (acme here). No blanket world row on
	// "/" — that would turn the whole shared zone world-readable through
	// inheritance (see the BootstrapHomes doc).
	if err := e.api.BootstrapHomes(context.Background()); err != nil {
		t.Fatalf("bootstrap homes: %v", err)
	}

	// globex joins through the API, so its home is provisioned at creation.
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"globex"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"globex","username":"wendy","password":"pw"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"globex","username":"gladmin","password":"pw","is_admin":true}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"alice","password":"pw"}`, 201) // acme/alice
	wendy := login(t, e.srv, "globex", "wendy", "pw")
	gladmin := login(t, e.srv, "globex", "gladmin", "pw")
	alice := login(t, e.srv, "acme", "alice", "pw")

	// --- the home namespace is enterable by every user (one world row,
	// provisioned by the bootstrap), and a new member reaches their home
	// through it by the default tenant grant. Nothing else is reachable:
	// deny-by-default is untouched.
	must(t, wendy, "GET", u+"/api/v1/fs?path=/tenants", "", 200)
	must(t, wendy, "GET", u+"/api/v1/fs?path=/tenants/globex", "", 200)
	must(t, wendy, "POST", u+"/api/v1/fs/write", `{"path":"/tenants/globex/note.txt","content":"x"}`, 403) // default is r only
	must(t, wendy, "GET", u+"/api/v1/fs?path=/", "", 403)

	// inside the namespace each member sees only their own tenant
	gl := listedPaths(t, wendy, u+"/api/v1/fs?path=/tenants")
	if !contains(gl, "/tenants/globex") {
		t.Errorf("own home missing from the namespace listing for its member: %v", gl)
	}
	if contains(gl, "/tenants/acme") {
		t.Errorf("foreign home visible to a non-member: %v", gl)
	}

	// hard partition: alice cannot touch globex's home whatever rows exist
	must(t, alice, "GET", u+"/api/v1/fs?path=/tenants/globex", "", 403)
	must(t, alice, "GET", u+"/api/v1/fs/content?path=/tenants/globex/whatever", "", 403)
	// super is exempt
	must(t, e.admin, "GET", u+"/api/v1/fs?path=/tenants/globex", "", 200)

	// once root is opened to a member, their listing shows the namespace,
	// and inside it their home; foreign homes are filtered out entirely.
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/","principal_type":"user","principal":"wendy","tenant":"globex","read":true}`, 200)
	root := listedPaths(t, wendy, u+"/api/v1/fs?path=/")
	if !contains(root, "/tenants") {
		t.Errorf("home namespace missing from a permitted root listing: %v", root)
	}
	// the namespace itself is reserved: no one may replace it, not even super
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/tenants"}`, 403)
	must(t, e.admin, "DELETE", u+"/api/v1/fs?path=/tenants", "", 403)
	must(t, e.admin, "POST", u+"/api/v1/fs/rename", `{"from":"/tenants","to":"/homes"}`, 400)

	// --- shared zone + the two new principals
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/pub"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/pub/f.txt","content":"hi"}`, 200)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/pub","principal_type":"tenant","principal":"globex","read":true}`, 200)
	// alice has no row anywhere: the globex tenant row does not match her
	// walk at all, so /pub stays denied for her
	must(t, alice, "GET", u+"/api/v1/fs/content?path=/pub/f.txt", "", 403)
	// a file deeper in the shared tree, probed by carol: she holds no
	// personal grants at all, so only tenant membership can reach it
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/pub/sub"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/pub/sub/g.txt","content":"deep"}`, 200)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"globex","username":"carol","password":"pw"}`, 201)
	carol := login(t, e.srv, "globex", "carol", "pw")
	must(t, carol, "GET", u+"/api/v1/fs/content?path=/pub/sub/g.txt", "", 200) // tenant row at /pub
	must(t, alice, "GET", u+"/api/v1/fs/content?path=/pub/sub/g.txt", "", 403)

	// world grant on shared paths, but a zero-grant row for one user still
	// beats it (per-layer shadowing is the exclusion mechanism)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/pub","principal_type":"all","read":true}`, 200)
	must(t, alice, "GET", u+"/api/v1/fs/content?path=/pub/f.txt", "", 200)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/pub","principal_type":"user","principal":"alice","read":false}`, 200)
	must(t, alice, "GET", u+"/api/v1/fs/content?path=/pub/f.txt", "", 403)
	must(t, carol, "GET", u+"/api/v1/fs/content?path=/pub/f.txt", "", 200)

	// inside a home every principal must be a member of that tenant — even from super
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/tenants/acme","principal_type":"tenant","principal":"globex","read":true}`, 422)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/tenants/globex/note.txt","principal_type":"user","principal":"alice","read":true}`, 422)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/tenants/globex","principal_type":"all","read":true}`, 422)
	// members of the home's own tenant remain grantable
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/tenants/globex","principal_type":"user","principal":"wendy","tenant":"globex","read":true,"write":true}`, 200)
	must(t, wendy, "POST", u+"/api/v1/fs/write", `{"path":"/tenants/globex/note.txt","content":"home"}`, 200)

	// cross-tenant pseudo-grants in the shared zone are super's prerogative
	must(t, gladmin, "PUT", u+"/api/v1/perms",
		`{"path":"/pub","principal_type":"tenant","principal":"acme","read":true}`, 403)
	// but a tenant's own admin may re-issue its default home grant
	must(t, gladmin, "PUT", u+"/api/v1/perms",
		`{"path":"/tenants/globex","principal_type":"tenant","principal":"globex","read":true}`, 200)

	// --- pseudo-grant rows stay visible and labeled for the UI
	grants := must(t, e.admin, "GET", u+"/api/v1/perms?path=/pub", "", 200)
	var sawAll, sawTenant bool
	for _, gv := range grants["grants"].([]any) {
		gm := gv.(map[string]any)
		switch gm["principal_type"] {
		case "all":
			sawAll = true
			if gm["principal_name"] != "*" || gm["principal_tenant"] != "*" {
				t.Errorf("world grant mislabeled: %v", gm)
			}
		case "tenant":
			sawTenant = true
			if gm["principal_name"] != "globex" || gm["principal_tenant"] != "globex" {
				t.Errorf("tenant grant mislabeled: %v", gm)
			}
		}
	}
	if !sawAll || !sawTenant {
		t.Errorf("pseudo-grants not listed as ordinary rows (all=%v tenant=%v)", sawAll, sawTenant)
	}

	// --- defaults are ordinary rows: revoke the home grant, and the member
	// loses their home entirely. This is also the sealing check: the world
	// row on /tenants makes the namespace enterable but never inherits
	// into the home subtree.
	hg := must(t, e.admin, "GET", u+"/api/v1/perms?path=/tenants/globex", "", 200)
	var homeGrantID int64
	for _, gv := range hg["grants"].([]any) {
		gm := gv.(map[string]any)
		if gm["principal_type"] == "tenant" {
			homeGrantID = jsonInt(gm["id"])
		}
	}
	if homeGrantID == 0 {
		t.Fatal("tenant creation did not provision the default member grant")
	}
	must(t, e.admin, "DELETE", fmt.Sprintf("%s/api/v1/perms/%d", u, homeGrantID), "", 204)
	// carol has only ever held the default row: she loses the home entirely
	must(t, carol, "GET", u+"/api/v1/fs?path=/tenants/globex", "", 403)
	must(t, carol, "GET", u+"/api/v1/fs?path=/tenants", "", 200) // traversal survives
	carolNS := listedPaths(t, carol, u+"/api/v1/fs?path=/tenants")
	if contains(carolNS, "/tenants/globex") {
		t.Errorf("home with revoked default still visible: %v", carolNS)
	}
	// wendy keeps it only through her explicit personal grant
	must(t, wendy, "GET", u+"/api/v1/fs?path=/tenants/globex", "", 200)
}

// A tenant home is a sealed scope: the world row on /tenants (and any
// other shared-zone row above it) never grants bits inside a home — only
// rows at the home root or below it can admit a member.
func TestHomeSealsInheritance(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	if err := e.api.BootstrapHomes(context.Background()); err != nil {
		t.Fatalf("bootstrap homes: %v", err)
	}
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"erin","password":"pw"}`, 201) // acme/erin
	erin := login(t, e.srv, "acme", "erin", "pw")

	// erin enters the namespace (world row) but acme's home default was
	// provisioned as a row at the home root: without it she gets nothing
	// inside, even though ancestors are readable.
	rev := must(t, e.admin, "GET", u+"/api/v1/perms?path=/tenants/acme", "", 200)
	for _, gv := range rev["grants"].([]any) {
		gm := gv.(map[string]any)
		if gm["principal_type"] == "tenant" {
			must(t, e.admin, "DELETE", fmt.Sprintf("%s/api/v1/perms/%d", u, jsonInt(gm["id"])), "", 204)
		}
	}
	must(t, erin, "GET", u+"/api/v1/fs?path=/tenants", "", 200)
	must(t, erin, "GET", u+"/api/v1/fs?path=/tenants/acme", "", 403)
	must(t, erin, "GET", u+"/api/v1/fs/content?path=/tenants/acme/any.txt", "", 403)
}

// Upgrading an instance created before homes lived under /tenants: the
// startup backfill moves the directory and re-keys everything stored by
// path (grants, drafts) — once; a second start changes nothing.
func TestHomeMigration(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	ctx := context.Background()

	// legacy state: acme exists with a /acme home, the default member
	// grant, an extra grant and a draft keyed to the old path (nothing
	// has run BootstrapHomes yet — this is the state an upgraded server
	// opens its database in).
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/acme"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/acme/old.txt","content":"legacy"}`, 200)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"acme","username":"dana","password":"pw"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/acme","principal_type":"tenant","principal":"acme","read":true}`, 200)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/acme/old.txt","principal_type":"user","principal":"dana","tenant":"acme","read":true}`, 200)
	if _, err := e.api.Drafts.Put(ctx, "/acme/old.txt", "draft-body", "acme/admin"); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	if err := e.api.BootstrapHomes(ctx); err != nil {
		t.Fatalf("bootstrap homes: %v", err)
	}

	// the directory moved; nothing remains at the legacy path
	must(t, e.admin, "GET", u+"/api/v1/fs?path=/acme", "", 404)
	must(t, e.admin, "GET", u+"/api/v1/fs/content?path=/acme/old.txt", "", 404)
	must(t, e.admin, "GET", u+"/api/v1/fs?path=/tenants/acme", "", 200)

	// the grant moved with it: dana reads only through the new path
	dana := login(t, e.srv, "acme", "dana", "pw")
	must(t, dana, "GET", u+"/api/v1/fs/content?path=/tenants/acme/old.txt", "", 200)
	must(t, dana, "GET", u+"/api/v1/fs/content?path=/acme/old.txt", "", 403) // the row moved: nothing authorizes the old path anymore

	// and the draft re-keyed to the new path
	if _, ok, err := e.api.Drafts.Get(ctx, "/tenants/acme/old.txt"); err != nil || !ok {
		t.Errorf("draft not rehomed: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := e.api.Drafts.Get(ctx, "/acme/old.txt"); ok {
		t.Errorf("draft still keyed to the legacy path")
	}

	// the default member grant moved with everything else, and a member
	// feels it right away through the new path
	dana2 := listedPaths(t, dana, u+"/api/v1/fs?path=/tenants")
	if !contains(dana2, "/tenants/acme") {
		t.Errorf("member cannot see the migrated home: %v", dana2)
	}
	gr := must(t, e.admin, "GET", u+"/api/v1/perms?path=/tenants/acme", "", 200)
	var sawDefault bool
	for _, gv := range gr["grants"].([]any) {
		if gm := gv.(map[string]any); gm["principal_type"] == "tenant" {
			sawDefault = true
		}
	}
	if !sawDefault {
		t.Errorf("migrated home did not get the default member grant")
	}

	// idempotent: a second start leaves everything as it is
	if err := e.api.BootstrapHomes(ctx); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	must(t, e.admin, "GET", u+"/api/v1/fs?path=/tenants/acme", "", 200)
	must(t, dana, "GET", u+"/api/v1/fs/content?path=/tenants/acme/old.txt", "", 200)
}

// Tenant names must be able to back a directory: creation validates, and
// the reserved documentation and namespace paths stay out of reach.
func TestTenantNameValidation(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	for _, bad := range []string{"Bad Name", "UPPER", "help", "tenants", "-x", "a/b", "..", "ünïcøde", "a_b"} {
		must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"`+bad+`"}`, 422)
	}
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"good-2"}`, 201)
}

package handlers

// Tenant homes + the "tenant"/"all" pseudo-principals: every tenant owns
// /{name}, only its members (and super) may touch it regardless of grant
// rows, and the default member-browse grant is an ordinary revocable row.

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
	// A real server runs this at startup: homes for pre-existing tenants
	// (acme here). No blanket world row on "/" — that would turn the
	// whole shared zone world-readable through inheritance (see the
	// BootstrapHomes doc).
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

	// --- a new member reaches their home directly (default tenant grant)
	// and nothing else: deny-by-default is untouched.
	must(t, wendy, "GET", u+"/api/v1/fs?path=/globex", "", 200)
	must(t, wendy, "POST", u+"/api/v1/fs/write", `{"path":"/globex/note.txt","content":"x"}`, 403) // default is r only
	must(t, wendy, "GET", u+"/api/v1/fs?path=/", "", 403)

	// hard partition: alice cannot touch globex's home whatever rows exist
	must(t, alice, "GET", u+"/api/v1/fs?path=/globex", "", 403)
	must(t, alice, "GET", u+"/api/v1/fs/content?path=/globex/whatever", "", 403)
	// super is exempt
	must(t, e.admin, "GET", u+"/api/v1/fs?path=/globex", "", 200)

	// once root is opened to a member, the entry filter shows their home
	// and hides foreign homes entirely (the partition denies them).
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/","principal_type":"user","principal":"wendy","tenant":"globex","read":true}`, 200)
	root := listedPaths(t, wendy, u+"/api/v1/fs?path=/")
	if !contains(root, "/globex") {
		t.Errorf("member's home missing from their root listing: %v", root)
	}
	if contains(root, "/acme") {
		t.Errorf("foreign home visible to a non-member: %v", root)
	}

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
		`{"path":"/acme","principal_type":"tenant","principal":"globex","read":true}`, 422)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/globex/note.txt","principal_type":"user","principal":"alice","read":true}`, 422)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/globex","principal_type":"all","read":true}`, 422)
	// members of the home's own tenant remain grantable
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/globex","principal_type":"user","principal":"wendy","tenant":"globex","read":true,"write":true}`, 200)
	must(t, wendy, "POST", u+"/api/v1/fs/write", `{"path":"/globex/note.txt","content":"home"}`, 200)

	// cross-tenant pseudo-grants in the shared zone are super's prerogative
	must(t, gladmin, "PUT", u+"/api/v1/perms",
		`{"path":"/pub","principal_type":"tenant","principal":"acme","read":true}`, 403)
	// but a tenant's own admin may re-issue its default home grant
	must(t, gladmin, "PUT", u+"/api/v1/perms",
		`{"path":"/globex","principal_type":"tenant","principal":"globex","read":true}`, 200)

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
	// loses their home entirely
	hg := must(t, e.admin, "GET", u+"/api/v1/perms?path=/globex", "", 200)
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
	must(t, carol, "GET", u+"/api/v1/fs?path=/globex", "", 403)
	// wendy keeps it only through her explicit personal grant
	must(t, wendy, "GET", u+"/api/v1/fs?path=/globex", "", 200)
}

// Tenant names must be able to back a directory: creation validates, and
// the reserved documentation path stays out of reach.
func TestTenantNameValidation(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	for _, bad := range []string{"Bad Name", "UPPER", "help", "-x", "a/b", "..", "ünïcøde", "a_b"} {
		must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"`+bad+`"}`, 422)
	}
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"good-2"}`, 201)
}

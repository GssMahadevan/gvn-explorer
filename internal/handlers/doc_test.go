package handlers

// Entity and ACL documents: export any entity as one JSON statement of
// names (never ids), and apply that same shape back as a reusable
// template — validate-first, merge or replace, homes respected.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// grantAt counts explicit grants visible at a path via the normal API.
func grantAt(t *testing.T, c *http.Client, url, path string) []map[string]any {
	t.Helper()
	out := must(t, c, "GET", url+"/api/v1/perms?path="+path, "", 200)
	var gs []map[string]any
	for _, g := range out["grants"].([]any) {
		gs = append(gs, g.(map[string]any))
	}
	return gs
}

func TestDocAclRoundTrip(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/groups", `{"name":"readers"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"alice","password":"pw"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"group","principal":"readers","read":true}`, 200)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"user","principal":"alice","read":true,"write":true}`, 200)

	// --- export: the document is names-only and complete.
	doc := must(t, e.admin, "GET", u+"/api/v1/export?path=/docs", "", 200)
	if doc["kind"] != "acl" || doc["path"] != "/docs" {
		t.Fatalf("export shape: %v", doc)
	}
	if gs, _ := doc["grants"].([]any); len(gs) != 2 {
		t.Fatalf("want 2 grant lines, got %v", doc["grants"])
	}
	raw, _ := json.Marshal(doc)
	if strings.Contains(string(raw), `"id"`) {
		t.Fatalf("document leaked ids: %s", raw)
	}

	// --- merge onto a fresh path: exactly the document's rows land.
	doc["path"] = "/other"
	rep := must(t, e.admin, "POST", u+"/api/v1/import", `{"doc":`+string(mustMarshal(t, doc))+`}`, 200)
	if rep["applied"].(float64) != 2 || rep["kind"] != "acl" {
		t.Fatalf("merge report: %v", rep)
	}
	if got := grantAt(t, e.admin, u, "/other"); len(got) != 2 {
		t.Fatalf("merge landed %d rows", len(got))
	}

	// merge is idempotent in effect: re-applying changes nothing visible.
	must(t, e.admin, "POST", u+"/api/v1/import", `{"doc":`+string(mustMarshal(t, doc))+`, "mode":"merge"}`, 200)
	if got := grantAt(t, e.admin, u, "/other"); len(got) != 2 {
		t.Fatalf("re-merge duplicated rows: %d", len(got))
	}

	// --- replace removes exactly what the document omits.
	// Inheritance is untouched: "/" grants never appear in current.
	replaceDoc := `{"kind":"acl","version":1,"path":"/other","grants":[
		{"principal_type":"group","tenant":"acme","principal":"readers","read":true}]}`
	rep = must(t, e.admin, "POST", u+"/api/v1/import",
		`{"mode":"replace","doc":`+replaceDoc+`}`, 200)
	if rep["applied"].(float64) != 1 || len(rep["removed"].([]any)) != 1 {
		t.Fatalf("replace report: %v", rep)
	}
	if !strings.Contains(rep["removed"].([]any)[0].(string), "user acme/alice") {
		t.Fatalf("removed list: %v", rep["removed"])
	}
	got := grantAt(t, e.admin, u, "/other")
	if len(got) != 1 || got[0]["principal_name"] != "readers" {
		t.Fatalf("replace left %v", got)
	}

	// --- a document may predate the path (grants precede files), and
	// dir kind creates the directory first.
	must(t, e.admin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"dir","version":1,"path":"/shared/templates","grants":[
		{"principal_type":"group","tenant":"acme","principal":"readers","read":true}]}}`, 200)
	list := must(t, e.admin, "GET", u+"/api/v1/fs?path=/shared", "", 200)
	if len(list["entries"].([]any)) != 1 {
		t.Fatalf("dir doc created no directory: %v", list)
	}

	// --- the built-in /help tree takes no documents either — same
	// read-only refusal every /help write gets.
	must(t, e.admin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"acl","version":1,"path":"/help","grants":[]}}`, 403)

	// --- malformed requests are refused before anything is read.
	must(t, e.admin, "POST", u+"/api/v1/import",
		`{"mode":"wipe","doc":{"kind":"acl","path":"/x","grants":[]}}`, 400)
	must(t, e.admin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"widget","path":"/x"}}`, 422)
	must(t, e.admin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"acl","version":99,"path":"/x","grants":[]}}`, 422)
}

func TestDocValidateFirst(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"globex"}`, 201)

	// Two unknown principals: one request lists both, nothing lands.
	code, out, _ := reqJSON(t, e.admin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"acl","version":1,"path":"/new","grants":[
			{"principal_type":"user","principal":"ghost","read":true},
			{"principal_type":"group","principal":"nosuch","read":true}]}}`)
	if code != 422 {
		t.Fatalf("want 422, got %d %v", code, out)
	}
	msg := errMsg(out)
	if !strings.Contains(msg, "acme/ghost") || !strings.Contains(msg, "acme/nosuch") {
		t.Fatalf("problems not listed: %q", msg)
	}
	if got := grantAt(t, e.admin, u, "/new"); len(got) != 0 {
		t.Fatalf("half-applied import: %v", got)
	}

	// The home partition applies to imported rows exactly as to
	// hand-entered ones — a template cannot smuggle a world grant into
	// someone's home.
	code, out, _ = reqJSON(t, e.admin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"acl","version":1,"path":"/tenants/globex","grants":[
			{"principal_type":"all","read":true}]}}`)
	if code != 422 || !strings.Contains(errMsg(out), "tenant home") {
		t.Fatalf("home rule not enforced: %d %v", code, out)
	}

	// Tenant admins cannot import foreign-tenant principals.
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"globex","username":"gladmin","password":"pw","is_admin":true}`, 201)
	gladmin := login(t, e.srv, "globex", "gladmin", "pw")
	code, _, _ = reqJSON(t, gladmin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"acl","version":1,"path":"/shared","grants":[
			{"principal_type":"user","tenant":"acme","principal":"admin","read":true}]}}`)
	if code != 422 {
		t.Fatalf("foreign principal accepted for tenant admin")
	}
}

func TestDocUser(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/groups", `{"name":"readers"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"alice","password":"pw"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"group","principal":"readers","read":true}`, 200)

	// Put alice in readers and grant her something, then export her:
	// the document carries fields, memberships, and named grants.
	must(t, e.admin, "POST", u+"/api/v1/groups/"+idOf(t, mustList(t, e.admin, "GET", u+"/api/v1/groups?q=readers", 200)[0])+"/members",
		`{"username":"alice"}`, 204)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"user","principal":"alice","read":true}`, 200)

	doc := must(t, e.admin, "GET", u+"/api/v1/export?user=acme/alice", "", 200)
	if doc["kind"] != "user" || doc["username"] != "alice" {
		t.Fatalf("user doc: %v", doc)
	}
	if mo, _ := doc["member_of"].([]any); len(mo) != 1 || mo[0] != "readers" {
		t.Fatalf("member_of: %v", doc["member_of"])
	}
	if gs, _ := doc["grants"].([]any); len(gs) != 1 {
		t.Fatalf("grants: %v", doc["grants"])
	}

	// Re-applying alice's own document is a no-op-shaped success: the
	// subject already exists and is never modified by a document.
	rep := must(t, e.admin, "POST", u+"/api/v1/import", `{"doc":`+string(mustMarshal(t, doc))+`}`, 200)
	if rep["created"] == true {
		t.Fatalf("existing user re-created")
	}
	if got := grantAt(t, e.admin, u, "/docs"); len(got) != 2 {
		t.Fatalf("re-apply changed grants: %d", len(got))
	}

	// The same document with the subject renamed creates bob as a
	// device user (documents carry no secrets), joins him to readers,
	// and applies his grant line.
	doc["username"] = "bob"
	doc["grants"].([]any)[0].(map[string]any)["principal"] = "bob"
	rep = must(t, e.admin, "POST", u+"/api/v1/import", `{"doc":`+string(mustMarshal(t, doc))+`}`, 200)
	if rep["created"] != true || rep["device_user"] != true || rep["memberships"].(float64) != 1 {
		t.Fatalf("import report: %v", rep)
	}
	bobs := mustList(t, e.admin, "GET", u+"/api/v1/users?q=bob", 200)
	if len(bobs) != 1 {
		t.Fatalf("bob not created")
	}
	if got := grantAt(t, e.admin, u, "/docs"); len(got) != 3 {
		t.Fatalf("bob grant not applied: %d rows", len(got))
	}
	// No interactive login until an admin sets one — the device-user
	// convention, not a new concept.
	if c := loginOpt(t, u, "acme", "bob", ""); c != http.StatusUnauthorized {
		t.Fatalf("device user login status %d", c)
	}
	// Memberships landed: readers now has two members.
	rg := mustList(t, e.admin, "GET", u+"/api/v1/groups?q=readers", 200)
	if ms := rg[0].(map[string]any)["members"].([]any); len(ms) != 2 {
		t.Fatalf("members: %v", ms)
	}

	// Unknown membership group: whole import refused, bob untouched.
	code, out, _ := reqJSON(t, e.admin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"user","version":1,"tenant":"acme","username":"carol","member_of":["nosuch"],"grants":[]}}`)
	if code != 422 || !strings.Contains(errMsg(out), "nosuch") {
		t.Fatalf("member_of validation: %d %v", code, out)
	}
	if users := mustList(t, e.admin, "GET", u+"/api/v1/users?q=carol", 200); len(users) != 0 {
		t.Fatalf("carol created despite invalid doc")
	}
}

func TestDocGroupAndTenant(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"alice","password":"pw"}`, 201)

	// A group document creates the group, resolves members by name, and
	// places its grants.
	rep := must(t, e.admin, "POST", u+"/api/v1/import", `{"doc":{
		"kind":"group","version":1,"tenant":"acme","name":"team",
		"members":["alice"],
		"grants":[{"path":"/team","principal_type":"group","tenant":"acme","principal":"team","read":true,"write":true}]}}`, 200)
	if rep["created"] != true || rep["memberships"].(float64) != 1 || rep["applied"].(float64) != 1 {
		t.Fatalf("group import: %v", rep)
	}
	if got := grantAt(t, e.admin, u, "/team"); len(got) != 1 {
		t.Fatalf("team grants: %v", got)
	}
	groups := mustList(t, e.admin, "GET", u+"/api/v1/groups?q=team", 200)
	if len(groups) != 1 || len(groups[0].(map[string]any)["members"].([]any)) != 1 {
		t.Fatalf("group not as documented: %v", groups)
	}

	// A missing member fails the whole document.
	code, out, _ := reqJSON(t, e.admin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"group","version":1,"tenant":"acme","name":"team2","members":["ghost"],"grants":[]}}`)
	if code != 422 || !strings.Contains(errMsg(out), "acme/ghost") {
		t.Fatalf("member validation: %d %v", code, out)
	}

	// Export the group: round-trips through import without creating.
	doc := must(t, e.admin, "GET", u+"/api/v1/export?group=acme/team", "", 200)
	if doc["kind"] != "group" || len(doc["members"].([]any)) != 1 {
		t.Fatalf("group doc: %v", doc)
	}
	rep = must(t, e.admin, "POST", u+"/api/v1/import", `{"doc":`+string(mustMarshal(t, doc))+`}`, 200)
	if rep["created"] == true {
		t.Fatalf("existing group re-created")
	}

	// Tenant documents: super only, home provisioned like POST /tenants.
	must(t, e.admin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"tenant","version":1,"name":"yoo","grants":[]}}`, 200)
	if t2 := mustList(t, e.admin, "GET", u+"/api/v1/tenants?q=yoo", 200); len(t2) != 1 {
		t.Fatalf("tenant not created")
	}
	if code, _, _ := reqJSON(t, e.admin, "GET", u+"/api/v1/fs?path=/tenants/yoo", ""); code != 200 {
		t.Fatalf("home not provisioned by tenant doc")
	}
	// Tenant home partition still holds against the imported rows:
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/shared","principal_type":"tenant","principal":"yoo","read":true}`, 200)
	doc = must(t, e.admin, "GET", u+"/api/v1/export?tenant=yoo", "", 200)
	// Two rows: the home's default browse grant (provisioned with the
	// tenant, exactly like POST /tenants) plus the explicit /shared one.
	// A tenant document is the complete statement of what names tenant:yoo.
	if doc["kind"] != "tenant" || len(doc["grants"].([]any)) != 2 {
		t.Fatalf("tenant doc: %v", doc)
	}
	if doc["active"] != true {
		t.Fatalf("tenant doc active flag wrong: %v", doc["active"])
	}

	// Unsafe tenant names are rejected by the same HomeSafe rule.
	code, _, _ = reqJSON(t, e.admin, "POST", u+"/api/v1/import",
		`{"doc":{"kind":"tenant","version":1,"name":"Bad Name","grants":[]}}`)
	if code != 422 {
		t.Fatalf("unsafe tenant name accepted")
	}

	// Tenant admins do not get tenant documents, and cannot export or
	// import outside their tenant.
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"yoo","username":"yt","password":"pw","is_admin":true}`, 201)
	yt := login(t, e.srv, "yoo", "yt", "pw")
	if code, _, _ := reqJSON(t, yt, "GET", u+"/api/v1/export?tenant=yoo", ""); code != 403 {
		t.Fatalf("tenant admin exported a tenant doc")
	}
	if code, _, _ := reqJSON(t, yt, "GET", u+"/api/v1/export?user=acme/admin", ""); code != 403 {
		t.Fatalf("tenant admin exported a foreign user")
	}
	// A plain logged-in user can still export a path document — it is
	// exactly what GET /perms already shows them.
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"viewer","password":"pw"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/shared","principal_type":"user","principal":"viewer","read":true}`, 200)
	viewer := login(t, e.srv, "acme", "viewer", "pw")
	if doc, _ := mustOK(t, viewer, u+"/api/v1/export?path=/shared"); doc["kind"] != "acl" {
		t.Fatalf("viewer path doc: %v", doc)
	}
	if code, _, _ := reqJSON(t, viewer, "POST", u+"/api/v1/import", `{"doc":{"kind":"acl","path":"/x","grants":[]}}`); code != 403 {
		t.Fatalf("non-admin imported")
	}
}

// errMsg pulls the message out of the API's {"error":{…}} envelope.
func errMsg(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	m, _ := e["message"].(string)
	return m
}

// idOf picks the numeric id out of a decoded list row, as a URL-ready
// string.
func idOf(t *testing.T, row any) string {
	t.Helper()
	m, ok := row.(map[string]any)
	if !ok {
		t.Fatalf("row is not an object: %v", row)
	}
	return strconv.FormatInt(int64(m["id"].(float64)), 10)
}

// mustOK GETs a URL and decodes the JSON body, failing on non-200.
func mustOK(t *testing.T, c *http.Client, url string) (map[string]any, error) {
	t.Helper()
	code, out, raw := reqJSON(t, c, "GET", url, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", url, code, raw)
	}
	return out, nil
}

// loginOpt reports the status code a login attempt gets (no fatal).
func loginOpt(t *testing.T, base, tenant, user, pass string) int {
	t.Helper()
	code, _, _ := reqJSON(t, http.DefaultClient, "POST", base+"/api/v1/auth/login",
		`{"tenant":"`+tenant+`","username":"`+user+`","password":"`+pass+`"}`)
	return code
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestDocApplyManyLines: a single large document applies in one request —
// the transaction path, exercised at scale (200 grant lines). An entity doc
// is used because its grant lines carry their own paths (an ACL doc states
// one path). Replace then deletes exactly the rows the doc owns.
func TestDocApplyManyLines(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL

	const n = 200
	lines := make([]map[string]any, n)
	for i := range lines {
		lines[i] = map[string]any{
			"path":           "/bulk/dir" + strconv.Itoa(i),
			"principal_type": "group",
			"principal":      "bulk",
			"read":           true,
		}
	}
	doc := map[string]any{"kind": "group", "version": 1, "name": "bulk", "grants": lines}
	rep := must(t, e.admin, "POST", u+"/api/v1/import",
		`{"mode":"merge","doc":`+string(mustMarshal(t, doc))+`}`, 200)
	if rep["applied"].(float64) != n || rep["created"] != true {
		t.Fatalf("want %d applied + created subject, got %v", n, rep)
	}
	// A deep line landed: count grants on the last path.
	if got := grantAt(t, e.admin, u, "/bulk/dir199"); len(got) != 1 {
		t.Fatalf("deep grant missing: %d rows at /bulk/dir199", len(got))
	}

	// Re-apply in replace mode stating nothing: every row this subject
	// carries is removed; inheritance is never part of it.
	empty := `{"kind":"group","version":1,"name":"bulk","grants":[]}`
	rep = must(t, e.admin, "POST", u+"/api/v1/import",
		`{"mode":"replace","doc":`+empty+`}`, 200)
	if rep["applied"].(float64) != 0 || len(rep["removed"].([]any)) != n {
		t.Fatalf("replace-all report: %v", rep)
	}
	for _, p := range []string{"/bulk/dir0", "/bulk/dir99", "/bulk/dir199"} {
		if got := grantAt(t, e.admin, u, p); len(got) != 0 {
			t.Fatalf("replace left %d rows at %s", len(got), p)
		}
	}
}

// TestDocApplyAtomicOnUnknownPrincipal: one unresolvable name in an
// otherwise-large document fails the whole request and applies nothing —
// validate-first holds regardless of document size.
func TestDocApplyAtomicOnUnknownPrincipal(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL

	lines := make([]map[string]any, 150)
	for i := range lines {
		lines[i] = map[string]any{
			"path":           "/atomic/dir" + strconv.Itoa(i),
			"principal_type": "user",
			"principal":      "ghost",
			"read":           true,
		}
	}
	// A subject named in the doc, plus many grant lines that all resolve
	// to a real user, plus one line naming a user that does not exist.
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"real","password":"pw"}`, 201)
	for _, l := range lines {
		l["principal"] = "real"
	}
	lines = append(lines, map[string]any{
		"path": "/atomic/ghost", "principal_type": "user", "principal": "nobody", "read": true,
	})
	doc := map[string]any{"kind": "group", "version": 1, "name": "mixed", "grants": lines}
	code, _, raw := reqJSON(t, e.admin, "POST", u+"/api/v1/import",
		`{"mode":"merge","doc":`+string(mustMarshal(t, doc))+`}`)
	if code != 422 {
		t.Fatalf("want 422, got %d (%s)", code, raw)
	}
	if !strings.Contains(string(raw), "nobody") {
		t.Fatalf("422 should name the missing principal: %s", raw)
	}
	// Nothing applied — not the first valid line, not the last one.
	for _, p := range []string{"/atomic/dir0", "/atomic/dir149"} {
		if got := grantAt(t, e.admin, u, p); len(got) != 0 {
			t.Fatalf("failed import applied rows: %d at %s", len(got), p)
		}
	}
	// And the group subject was not created either.
	if gs := mustList(t, e.admin, "GET", u+"/api/v1/groups", 200); len(gs) != 0 {
		t.Fatalf("failed import created a subject group: %v", gs)
	}
}

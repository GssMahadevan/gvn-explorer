package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GssMahadevan/gvn-explorer/internal/acl"
	"github.com/GssMahadevan/gvn-explorer/internal/audit"
	"github.com/GssMahadevan/gvn-explorer/internal/auth"
	"github.com/GssMahadevan/gvn-explorer/internal/db"
	"github.com/GssMahadevan/gvn-explorer/internal/drafts"
	"github.com/GssMahadevan/gvn-explorer/internal/fsx"
)

type env struct {
	srv   *httptest.Server
	admin *http.Client // super admin in tenant "acme"
}

func newEnv(t *testing.T) *env { return newEnvLimits(t, 0, 0, 0, 0) }

// newEnvLimits builds a server with rate limiting enabled: rps/rburst for
// authenticated endpoints, authRPS/authBurst for login-per-IP. Zero = off.
func newEnvLimits(t *testing.T, rps float64, rburst int, authRPS float64, authBurst int) *env {
	t.Helper()
	conn, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	tree, err := fsx.New(t.TempDir())
	if err != nil {
		t.Fatalf("fs root: %v", err)
	}
	as := auth.NewStore(conn)
	ctx := context.Background()
	if _, err := as.CreateTenant(ctx, "acme", ""); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	if _, err := as.CreateUser(ctx, "acme", "admin", "pw", "", true, true, nil); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	api := API{Auth: as, ACL: acl.NewStore(conn), Events: audit.NewStore(conn),
		Drafts: drafts.NewStore(conn),
		FS:     tree, SessionTTL: time.Hour, MaxFileMB: 1,
		RPS: rps, RBurst: rburst, AuthRPS: authRPS, AuthBurst: authBurst}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &env{srv: srv, admin: login(t, srv, "acme", "admin", "pw")}
}

func login(t *testing.T, srv *httptest.Server, tenant, user, pass string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.Post(srv.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"tenant":"`+tenant+`","username":"`+user+`","password":"`+pass+`"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s/%s: status %d", tenant, user, resp.StatusCode)
	}
	return c
}

// must performs a JSON request and fails the test unless status == wantCode.
// Returns the decoded JSON body.
func must(t *testing.T, c *http.Client, method, url, body string, wantCode int) map[string]any {
	t.Helper()
	code, out, raw := reqJSON(t, c, method, url, body)
	if code != wantCode {
		t.Fatalf("%s %s: status %d want %d (%s)", method, url, code, wantCode, raw)
	}
	return out
}

func reqJSON(t *testing.T, c *http.Client, method, url, body string) (int, map[string]any, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(r)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, raw
}

// mustList like must, for endpoints returning a top-level JSON array.
func mustList(t *testing.T, c *http.Client, method, url string, wantCode int) []any {
	t.Helper()
	code, _, raw := reqJSON(t, c, method, url, "")
	if code != wantCode {
		t.Fatalf("%s %s: status %d want %d (%s)", method, url, code, wantCode, raw)
	}
	var arr []any
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("%s %s: not an array: %s", method, url, raw)
	}
	return arr
}

func jsonInt(v any) int64 { return int64(v.(float64)) }

func TestAdminFSFlow(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/d"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/d"}`, 409)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/d/n.txt","content":"hello"}`, 200)
	body := must(t, e.admin, "GET", u+"/api/v1/fs/content?path=/d/n.txt", "", 200)
	if body["content"] != "hello" {
		t.Fatalf("content round-trip: %v", body)
	}
	body = must(t, e.admin, "GET", u+"/api/v1/fs?path=/d", "", 200)
	if n := len(body["entries"].([]any)); n != 1 {
		t.Fatalf("entries: %d want 1", n)
	}
	must(t, e.admin, "POST", u+"/api/v1/fs/rename", `{"from":"/d/n.txt","to":"/d/r.txt"}`, 200)
	must(t, e.admin, "DELETE", u+"/api/v1/fs?path=/d", "", 409) // non-empty dir
	must(t, e.admin, "DELETE", u+"/api/v1/fs?path=/d/r.txt", "", 204)
	must(t, e.admin, "GET", u+"/api/v1/fs?path=/d/../etc", "", 400)
}

func TestUploadAndRaw(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/up"}`, 201)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "f.bin")
	fw.Write([]byte{0, 1, 2, 255})
	mw.WriteField("path", "/up")
	mw.Close()
	r, _ := http.NewRequest("POST", u+"/api/v1/fs/upload", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := e.admin.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}
	rawResp, err := e.admin.Get(u + "/api/v1/fs/raw?path=/up/f.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer rawResp.Body.Close()
	raw, _ := io.ReadAll(rawResp.Body)
	if !bytes.Equal(raw, []byte{0, 1, 2, 255}) {
		t.Fatalf("raw mismatch: %v", raw)
	}
	body := must(t, e.admin, "GET", u+"/api/v1/fs/content?path=/up/f.bin", "", 200)
	if body["is_binary"] != true {
		t.Fatalf("binary detect: %v", body)
	}
}

func TestACLMatrix(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	// seed fs: /docs/n.txt, /hidden
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/docs"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/docs/n.txt","content":"secret-doc"}`, 200)
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/hidden"}`, 201)

	// bob + readers group in acme, read-only on /docs
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"bob","password":"pw"}`, 201)
	g := must(t, e.admin, "POST", u+"/api/v1/groups", `{"name":"readers"}`, 201)
	gpath := u + "/api/v1/groups/" + strconv.FormatInt(jsonInt(g["id"]), 10) + "/members"
	must(t, e.admin, "POST", gpath, `{"username":"bob"}`, 204)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"group","principal":"readers","read":true}`, 200)

	bob := login(t, e.srv, "acme", "bob", "pw")
	must(t, e.admin, "GET", u+"/api/v1/fs/content?path=/docs/n.txt", "", 200)
	must(t, bob, "GET", u+"/api/v1/fs?path=/docs", "", 200)
	must(t, bob, "POST", u+"/api/v1/fs/write", `{"path":"/docs/x","content":"no"}`, 403)
	must(t, bob, "DELETE", u+"/api/v1/fs?path=/docs/n.txt", "", 403)
	must(t, bob, "GET", u+"/api/v1/users", "", 403)
	must(t, bob, "GET", u+"/api/v1/fs?path=/hidden", "", 403) // no grant anywhere

	// r on / exposes root listing; zero-grant excludes /hidden
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/","principal_type":"group","principal":"readers","read":true}`, 200)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/hidden","principal_type":"group","principal":"readers","read":false}`, 200)
	body := must(t, bob, "GET", u+"/api/v1/fs?path=/", "", 200)
	ents := body["entries"].([]any)
	if len(ents) != 1 {
		t.Fatalf("root visible entries: %d want 1 (/docs)", len(ents))
	}
	if p := ents[0].(map[string]any)["entry"].(map[string]any)["path"]; p != "/docs" {
		t.Fatalf("visible entry %v want /docs", p)
	}

	// direct user grant overrides group at nearer path
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs/in","principal_type":"user","principal":"bob","read":true,"write":true}`, 200)
	must(t, bob, "POST", u+"/api/v1/fs/write", `{"path":"/docs/in/ok.txt","content":"yes"}`, 200)
	eff := must(t, bob, "GET", u+"/api/v1/perms?path=/docs/in", "", 200)["effective"].(map[string]any)
	if eff["source"] != "/docs/in" {
		t.Fatalf("effective source %v want /docs/in", eff["source"])
	}
	must(t, bob, "DELETE", u+"/api/v1/fs?path=/docs/in/ok.txt", "", 403) // no d bit
}

// Same usernames and group names in two tenants are distinct principals:
// a grant for acme/bob must never apply to globex/bob.
func TestTenantNameIsolation(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/docs"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/docs/n.txt","content":"acme-only"}`, 200)

	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"bob","password":"pw"}`, 201) // acme/bob
	must(t, e.admin, "POST", u+"/api/v1/groups", `{"name":"readers"}`, 201)                // acme/readers
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"group","principal":"readers","read":true}`, 200)

	// second tenant with the SAME names
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"globex"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"globex","username":"bob","password":"pw2"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/groups", `{"tenant":"globex","name":"readers"}`, 201)

	gbob := login(t, e.srv, "globex", "bob", "pw2")
	// acme's grants do not leak: bob (globex) sees nothing
	must(t, gbob, "GET", u+"/api/v1/fs?path=/docs", "", 403)
	must(t, gbob, "GET", u+"/api/v1/fs/content?path=/docs/n.txt", "", 403)

	// membership lookup is group-tenant-scoped: adding "bob" to
	// globex's group by name resolves to globex/bob (a different row)
	allGroups := mustList(t, e.admin, "GET", u+"/api/v1/groups", 200)
	var globexReadersID int64
	for _, gv := range allGroups {
		gm := gv.(map[string]any)
		if gm["tenant"] == "globex" {
			globexReadersID = jsonInt(gm["id"])
		}
	}
	if globexReadersID == 0 {
		t.Fatal("globex/readers group not found in list")
	}
	must(t, e.admin, "POST", u+"/api/v1/groups/"+strconv.FormatInt(globexReadersID, 10)+"/members", `{"username":"bob"}`, 204)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"group","principal":"readers","tenant":"globex","read":true}`, 200)
	must(t, gbob, "GET", u+"/api/v1/fs?path=/docs", "", 200)

	// login resolves the tenant: same name, different principal
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	must(t, c, "POST", u+"/api/v1/auth/login", `{"tenant":"acme","username":"bob","password":"pw"}`, 200)
	must(t, c, "POST", u+"/api/v1/auth/login", `{"tenant":"globex","username":"bob","password":"pw"}`, 401) // globex/bob uses pw2
	must(t, c, "POST", u+"/api/v1/auth/login", `{"username":"bob","password":"pw"}`, 422)                   // tenant required
}

// Tenant admins manage their own namespace only and do NOT bypass file ACLs.
// TestTenantFilter: users/groups lists accept tenant=<name> as an exact
// scope (for the comboboxes in grant/member forms); lists without the
// capability reject the parameter with 400.
func TestTenantFilter(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"globex"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"globex","username":"g-user","password":"pw"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/groups", `{"tenant":"globex","name":"g-grp"}`, 201)

	for _, tc := range []struct{ path, want string }{
		{"/api/v1/users?tenant=globex", "globex"},
		{"/api/v1/groups?tenant=globex", "globex"},
	} {
		for _, row := range mustList(t, e.admin, "GET", u+tc.path, 200) {
			if m := row.(map[string]any); m["tenant"] != tc.want {
				t.Fatalf("tenant filter leaked %v under %s", m, tc.path)
			}
		}
	}
	// an unknown tenant is a valid filter that matches nothing
	if n := len(mustList(t, e.admin, "GET", u+"/api/v1/users?tenant=nope", 200)); n != 0 {
		t.Fatalf("unknown tenant filter returned %d rows, want 0", n)
	}
	// capability-gated: other list endpoints reject the parameter
	must(t, e.admin, "GET", u+"/api/v1/perms?path=%2F&tenant=acme", "", 400)
	must(t, e.admin, "GET", u+"/api/v1/fs?path=%2F&tenant=acme", "", 400)
}

func TestTenantAdminScope(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/docs"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"globex"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"globex","username":"tadmin","password":"pw","is_admin":true}`, 201)
	ta := login(t, e.srv, "globex", "tadmin", "pw")

	// sees only its own tenant's users and groups
	for _, uv := range mustList(t, ta, "GET", u+"/api/v1/users", 200) {
		if um := uv.(map[string]any); um["tenant"] != "globex" {
			t.Fatalf("tenant admin saw foreign user: %v", um)
		}
	}
	// pinned to own tenant on create (requested tenant ignored)
	created := must(t, ta, "POST", u+"/api/v1/users", `{"tenant":"acme","username":"x1","password":"pw"}`, 201)
	if created["tenant"] != "globex" {
		t.Fatalf("created user landed in %v, want globex", created["tenant"])
	}
	// cannot administer principals of another tenant
	acmeUsers := mustList(t, e.admin, "GET", u+"/api/v1/users", 200)
	var acmeAdminID int64
	for _, uv := range acmeUsers {
		um := uv.(map[string]any)
		if um["tenant"] == "acme" && um["username"] == "admin" {
			acmeAdminID = jsonInt(um["id"])
		}
	}
	if acmeAdminID == 0 {
		t.Fatal("acme/admin not found")
	}
	must(t, ta, "POST", u+"/api/v1/users/"+strconv.FormatInt(acmeAdminID, 10)+"/password", `{"password":"pwn"}`, 403)
	must(t, ta, "DELETE", u+"/api/v1/users/"+strconv.FormatInt(acmeAdminID, 10), "", 403)

	// cannot grant to foreign tenants, cannot revoke foreign grants
	must(t, ta, "PUT", u+"/api/v1/perms", `{"path":"/","principal_type":"user","principal":"admin","tenant":"acme","read":true}`, 403)
	grants := must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"user","principal":"admin","read":true}`, 200)
	must(t, ta, "DELETE", u+"/api/v1/perms/"+strconv.FormatInt(jsonInt(grants["id"]), 10), "", 403)

	// tenant admin does NOT bypass file ACLs
	must(t, ta, "GET", u+"/api/v1/fs?path=/docs", "", 403)
	// but its own grants work for its own members
	must(t, ta, "POST", u+"/api/v1/groups", `{"name":"staff"}`, 201)
	must(t, ta, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"user","principal":"tadmin","read":true}`, 200)
	must(t, ta, "GET", u+"/api/v1/fs?path=/docs", "", 200)

	// tenant CRUD is super-only
	must(t, ta, "GET", u+"/api/v1/tenants", "", 403)
	must(t, ta, "POST", u+"/api/v1/tenants", `{"name":"intruder"}`, 403)
}

// Poor-man's-Dropbox semantics: grants and whole accounts can carry an
// expiry; expiry is evaluated per request, so access dies mid-session
// with no cron or extra infra.
func TestExpiringAccess(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/share"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/share/f.txt","content":"dropbox-ish"}`, 200)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"cust","password":"pw"}`, 201)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)

	// grant that already expired: never visible, but still listed (flagged)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/share","principal_type":"user","principal":"cust","read":true,"expires_at":"`+past+`"}`, 200)
	cust := login(t, e.srv, "acme", "cust", "pw")
	must(t, cust, "GET", u+"/api/v1/fs?path=/share", "", 403)
	g0 := must(t, cust, "GET", u+"/api/v1/perms?path=/share", "", 200)["grants"].([]any)[0].(map[string]any)
	if g0["expired"] != true {
		t.Fatalf("expired grant should be flagged: %v", g0)
	}

	// 2-second window: works now, gone after — live session included
	soon := time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/share","principal_type":"user","principal":"cust","read":true,"expires_at":"`+soon+`"}`, 200)
	must(t, cust, "GET", u+"/api/v1/fs?path=/share", "", 200)
	time.Sleep(3 * time.Second)
	must(t, cust, "GET", u+"/api/v1/fs?path=/share", "", 403)

	// expired grants do not block inheritance from further up
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/","principal_type":"user","principal":"cust","read":true}`, 200)
	must(t, cust, "GET", u+"/api/v1/fs?path=/share", "", 200)
	must(t, cust, "POST", u+"/api/v1/fs/write", `{"path":"/share/x","content":"no"}`, 403) // bits come from / (read only)

	// expires_days lands an expires_at ~3 days out
	created := must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/share","principal_type":"user","principal":"cust","read":true,"write":true,"expires_days":3}`, 200)
	if created["expires_at"] == nil || created["expired"] == true {
		t.Fatalf("3-day grant: %v", created)
	}
	must(t, cust, "POST", u+"/api/v1/fs/write", `{"path":"/share/ok","content":"yes"}`, 200)

	// account expiry: an already-lapsed account cannot even log in
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"old","password":"pw","expires_at":"`+past+`"}`, 201)
	must(t, &http.Client{}, "POST", u+"/api/v1/auth/login", `{"tenant":"acme","username":"old","password":"pw"}`, 401)

	// short-lived customer account: live session is killed when it lapses
	soon2 := time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"temp","password":"pw","expires_at":"`+soon2+`"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/share","principal_type":"user","principal":"temp","read":true}`, 200)
	tmp := login(t, e.srv, "acme", "temp", "pw")
	must(t, tmp, "GET", u+"/api/v1/fs?path=/share", "", 200)
	time.Sleep(3 * time.Second)
	must(t, tmp, "GET", u+"/api/v1/fs?path=/share", "", 401) // session gone, not just perms
	must(t, &http.Client{}, "POST", u+"/api/v1/auth/login", `{"tenant":"acme","username":"temp","password":"pw"}`, 401)
}

// IoT device flow: user without password + provisioned API key + bearer
// requests. Revocation must bite on the very next request.
func TestDeviceAPIKeys(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/firmware"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/firmware/fw.bin","content":"binary-ish"}`, 200)

	// device user: empty password means interactive login is impossible
	dev := must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"node7"}`, 201)
	devID := int64(dev["id"].(float64))
	factorsURL := fmt.Sprintf("%s/api/v1/users/%d/factors", u, devID)
	must(t, &http.Client{}, "POST", u+"/api/v1/auth/login", `{"tenant":"acme","username":"node7","password":"guess"}`, 401)

	// reserved methods are rejected cleanly
	must(t, e.admin, "POST", factorsURL, `{"method":"ed25519"}`, 422)
	must(t, e.admin, "POST", factorsURL, `{"method":"oauth2","alg":"google"}`, 422)

	// provision key (plaintext shown once)
	kr := must(t, e.admin, "POST", factorsURL, `{"method":"apikey","label":"v1"}`, 201)
	key, _ := kr["key"].(string)
	if len(key) < 40 {
		t.Fatalf("api key too short: %q", key)
	}
	fid := int64(kr["factor"].(map[string]any)["id"].(float64))
	must(t, e.admin, "POST", factorsURL, `{"method":"apikey","label":"v1"}`, 409) // same label

	bearer := func(k, url string) int {
		t.Helper()
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if k != "" {
			req.Header.Set("Authorization", "Bearer "+k)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	bearerIfNoneMatch := func(k, url, etag string) int {
		t.Helper()
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+k)
		req.Header.Set("If-None-Match", etag)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}

	// grant the firmware dir, then bearer downloads must work
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/firmware","principal_type":"user","principal":"node7","read":true}`, 200)
	if c := bearer(key, u+"/api/v1/fs/raw?path=/firmware/fw.bin"); c != 200 {
		t.Fatalf("bearer download: %d", c)
	}
	if c := bearer("deadbeef", u+"/api/v1/fs/raw?path=/firmware/fw.bin"); c != 401 {
		t.Fatalf("bogus key: %d", c)
	}
	if c := bearer("", u+"/api/v1/fs/raw?path=/firmware/fw.bin"); c != 401 {
		t.Fatalf("no header: %d", c)
	}

	// ACLs still apply: same key, path the device has no grant on
	if c := bearer(key, u+"/api/v1/fs?path=/firmware-locked"); c != 403 {
		t.Fatalf("ungranted path with valid key: %d", c)
	}

	// FW polling: raw GET exposes an ETag, If-None-Match yields a bodiless 304
	rawURL := u + "/api/v1/fs/raw?path=/firmware/fw.bin"
	req, _ := http.NewRequest("GET", rawURL, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	etag := resp.Header.Get("ETag")
	resp.Body.Close()
	if etag == "" {
		t.Fatal("raw download should carry an ETag for device polling")
	}
	if c := bearerIfNoneMatch(key, rawURL, etag); c != http.StatusNotModified {
		t.Fatalf("conditional GET: %d", c)
	}

	// factors are listed without secrets, last use recorded
	fs := mustList(t, e.admin, "GET", factorsURL, 200)
	f0 := fs[0].(map[string]any)
	if _, leaked := f0["secret"]; leaked {
		t.Fatal("factor listing must not expose the secret")
	}
	if f0["last_used_at"] == nil {
		t.Fatal("last_used_at should be recorded after a bearer hit")
	}

	// an already-lapsed key is useless
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	kr2 := must(t, e.admin, "POST", factorsURL, `{"method":"apikey","label":"dead","expires_at":"`+past+`"}`, 201)
	if c := bearer(kr2["key"].(string), u+"/api/v1/fs/raw?path=/firmware/fw.bin"); c != 401 {
		t.Fatalf("lapsed key: %d", c)
	}

	// account expiry outranks a live key
	ghost := must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"ghost","expires_at":"`+past+`"}`, 201)
	kr3 := must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/factors", u, int64(ghost["id"].(float64))),
		`{"method":"apikey"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/firmware","principal_type":"user","principal":"ghost","read":true}`, 200)
	if c := bearer(kr3["key"].(string), u+"/api/v1/fs/raw?path=/firmware/fw.bin"); c != 401 {
		t.Fatalf("key on lapsed account: %d", c)
	}

	// revocation bites immediately
	must(t, e.admin, "DELETE", fmt.Sprintf("%s/%d", factorsURL, fid), "", 204)
	if c := bearer(key, u+"/api/v1/fs/raw?path=/firmware/fw.bin"); c != 401 {
		t.Fatalf("after revoke: %d", c)
	}

	// tenant scoping: tenant admin may not touch another tenant's user
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"globex"}`, 201)
	gUser := must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"globex","username":"gnode"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"tacme","password":"pw","is_admin":true}`, 201)
	ta := login(t, e.srv, "acme", "tacme", "pw")
	must(t, ta, "GET", fmt.Sprintf("%s/api/v1/users/%d/factors", u, int64(gUser["id"].(float64))), "", 403)
	must(t, ta, "POST", fmt.Sprintf("%s/api/v1/users/%d/factors", u, devID), `{"method":"apikey"}`, 201) // own tenant: fine
}

// DoS control: per-principal token bucket on every authenticated endpoint.
func TestRateLimitPerPrincipal(t *testing.T) {
	e := newEnvLimits(t, 5, 3, 0, 0) // 5 req/s, burst 3
	u := e.srv.URL
	// setup first, then let the admin bucket refill so the burst math is exact
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"other","password":"pw"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/","principal_type":"user","principal":"other","read":true}`, 200)
	other := login(t, e.srv, "acme", "other", "pw") // login route: no principal bucket
	time.Sleep(1200 * time.Millisecond)             // refill to burst (5/s)

	var codes []int
	for i := 0; i < 5; i++ {
		code, _, _ := reqJSON(t, e.admin, "GET", u+"/api/v1/fs?path=/", "")
		codes = append(codes, code)
	}
	for i := 0; i < 3; i++ {
		if codes[i] != http.StatusOK {
			t.Fatalf("burst should allow 3: %v", codes)
		}
	}
	if codes[4] != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst: %v", codes)
	}
	// a different principal has its own bucket
	if code, _, _ := reqJSON(t, other, "GET", u+"/api/v1/fs?path=/", ""); code != http.StatusOK {
		t.Fatalf("second principal must not share the bucket: %d", code)
	}
}

// Brute-force control: login is bucketed by source IP.
func TestRateLimitLoginPerIP(t *testing.T) {
	e := newEnvLimits(t, 0, 0, 2, 3) // 2 logins/s, burst 3
	u := e.srv.URL
	c := &http.Client{}
	var codes []int
	for i := 0; i < 6; i++ {
		code, _, _ := reqJSON(t, c, "POST", u+"/api/v1/auth/login", `{"tenant":"acme","username":"admin","password":"wrong"}`)
		codes = append(codes, code)
	}
	if codes[0] != http.StatusUnauthorized {
		t.Fatalf("first attempt should be a plain 401: %v", codes)
	}
	if codes[len(codes)-1] != http.StatusTooManyRequests {
		t.Fatalf("eventually rate limited: %v", codes)
	}
}

func TestAuthLifecycle(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	must(t, c, "POST", u+"/api/v1/auth/login", `{"tenant":"acme","username":"admin","password":"WRONG"}`, 401)
	must(t, c, "POST", u+"/api/v1/auth/login", `{"tenant":"NOPE","username":"admin","password":"pw"}`, 401)
	must(t, c, "GET", u+"/api/v1/fs?path=/", "", 401)
	must(t, e.admin, "POST", u+"/api/v1/auth/logout", "", 204)
	must(t, e.admin, "GET", u+"/api/v1/fs?path=/", "", 401)
}

// Deleting a tenant removes its principals and every grant targeting them.
func TestTenantDeleteCascade(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/docs"}`, 201)
	tt := must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"temp"}`, 201)
	tid := strconv.FormatInt(jsonInt(tt["id"]), 10)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"temp","username":"u","password":"pw"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"user","principal":"u","tenant":"temp","read":true}`, 200)
	before := must(t, e.admin, "GET", u+"/api/v1/perms?path=/docs", "", 200)["grants"].([]any)
	if len(before) != 1 {
		t.Fatalf("grants before delete: %d", len(before))
	}
	must(t, e.admin, "DELETE", u+"/api/v1/tenants/"+tid, "", 204)
	after := must(t, e.admin, "GET", u+"/api/v1/perms?path=/docs", "", 200)["grants"].([]any)
	if len(after) != 0 {
		t.Fatalf("grants after tenant delete: %d want 0", len(after))
	}
	if n := len(mustList(t, e.admin, "GET", u+"/api/v1/users", 200)); n != 1 {
		t.Fatalf("users after cascade: %d want 1 (acme/admin)", n)
	}
}

// Suspension (active=false) is a kill switch, not a deletion: the user or
// group row, its grants, factors and history survive, but every action in
// the system is blocked — login, live sessions, bearer keys and, for groups,
// the grants flowing through them.
func TestSuspendUsersAndGroups(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/pub"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/grp"}`, 201)

	// interactive user
	nu := must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"alice","password":"pw"}`, 201)
	uid := jsonInt(nu["id"])
	if nu["active"] != true {
		t.Fatalf("created user should report active: %v", nu)
	}
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/pub","principal_type":"user","principal":"alice","read":true}`, 200)
	alice := login(t, e.srv, "acme", "alice", "pw")
	must(t, alice, "GET", u+"/api/v1/fs?path=/pub", "", 200)

	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/active", u, uid), `{"active":false}`, 200)
	must(t, alice, "GET", u+"/api/v1/auth/me", "", 401) // live session dies on the next request
	must(t, alice, "GET", u+"/api/v1/fs?path=/pub", "", 401)
	must(t, e.admin, "POST", u+"/api/v1/auth/login", `{"tenant":"acme","username":"alice","password":"pw"}`, 401) // indistinguishable from a wrong password

	// the grant survives but is flagged inert, so reinstatement restores it
	gr := must(t, e.admin, "GET", u+"/api/v1/perms?path=/pub", "", 200)["grants"].([]any)
	if g0 := gr[0].(map[string]any); g0["principal_active"] != false {
		t.Fatalf("grant should flag suspended principal: %v", g0)
	}

	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/active", u, uid), `{"active":true}`, 200)
	alice2 := login(t, e.srv, "acme", "alice", "pw")
	must(t, alice2, "GET", u+"/api/v1/fs?path=/pub", "", 200)

	// footgun guard: nobody suspends their own account
	adm := must(t, e.admin, "GET", u+"/api/v1/auth/me", "", 200)
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/active", u, jsonInt(adm["id"])), `{"active":false}`, 422)

	// device user over bearer key
	dev := must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"node9","password":""}`, 201)
	did := jsonInt(dev["id"])
	f := must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/factors", u, did), `{"method":"apikey","label":"k"}`, 201)
	key := f["key"].(string)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/fw.bin","content":"zz"}`, 200)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/fw.bin","principal_type":"user","principal":"node9","read":true}`, 200)
	bearer := func(k, url string) int {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+k)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			t.Fatalf("bearer: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if c := bearer(key, u+"/api/v1/fs/raw?path=/fw.bin"); c != 200 {
		t.Fatalf("device download before suspend: %d", c)
	}
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/active", u, did), `{"active":false}`, 200)
	if c := bearer(key, u+"/api/v1/fs/raw?path=/fw.bin"); c != 401 {
		t.Fatalf("bearer must die with the account: %d", c)
	}
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/active", u, did), `{"active":true}`, 200)
	if c := bearer(key, u+"/api/v1/fs/raw?path=/fw.bin"); c != 200 {
		t.Fatalf("reinstated device: %d", c)
	}

	// group suspension: grants through the group go inert, direct grants do not
	g := must(t, e.admin, "POST", u+"/api/v1/groups", `{"name":"staff"}`, 201)
	gid := jsonInt(g["id"])
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/groups/%d/members", u, gid), `{"username":"alice"}`, 204)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/grp","principal_type":"group","principal":"staff","read":true}`, 200)
	must(t, alice2, "GET", u+"/api/v1/fs?path=/grp", "", 200)
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/groups/%d/active", u, gid), `{"active":false}`, 200)
	must(t, alice2, "GET", u+"/api/v1/fs?path=/grp", "", 403) // session alive, group grant inert
	must(t, alice2, "GET", u+"/api/v1/fs?path=/pub", "", 200) // direct grant untouched
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/groups/%d/active", u, gid), `{"active":true}`, 200)
	must(t, alice2, "GET", u+"/api/v1/fs?path=/grp", "", 200)

	// a suspended tenant admin cannot administer anything anymore
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"globex"}`, 201)
	ta := must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"globex","username":"tacarey","password":"pw","is_admin":true}`, 201)
	taj := login(t, e.srv, "globex", "tacarey", "pw")
	must(t, taj, "GET", u+"/api/v1/users", "", 200)
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/active", u, jsonInt(ta["id"])), `{"active":false}`, 200)
	must(t, taj, "GET", u+"/api/v1/users", "", 401)

	// tenant scoping: an admin cannot suspend across tenants
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/active", u, jsonInt(ta["id"])), `{"active":true}`, 200)
	taj2 := login(t, e.srv, "globex", "tacarey", "pw")
	must(t, taj2, "POST", fmt.Sprintf("%s/api/v1/users/%d/active", u, uid), `{"active":false}`, 403)
	must(t, taj2, "POST", fmt.Sprintf("%s/api/v1/groups/%d/active", u, gid), `{"active":false}`, 403)
}

// Every authz/authn DML lands in the append-only events table with actor,
// source IP and target; admins read it back scoped to their tenant.
func TestAuditTrail(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/docs"}`, 201)
	tt := must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"watch"}`, 201)
	tid := jsonInt(tt["id"])
	ta := must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"watch","username":"wat","password":"pw","is_admin":true}`, 201)
	taID := jsonInt(ta["id"])
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/docs","principal_type":"user","principal":"wat","tenant":"watch","read":true,"expires_days":7}`, 200)
	gid := jsonInt(must(t, e.admin, "GET", u+"/api/v1/perms?path=/docs", "", 200)["grants"].([]any)[0].(map[string]any)["id"])
	must(t, e.admin, "DELETE", fmt.Sprintf("%s/api/v1/perms/%d", u, gid), "", 204)
	g2 := must(t, e.admin, "POST", u+"/api/v1/groups", `{"tenant":"watch","name":"crew"}`, 201)
	g2ID := jsonInt(g2["id"])
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/groups/%d/members", u, g2ID), `{"username":"wat"}`, 204)
	must(t, e.admin, "DELETE", fmt.Sprintf("%s/api/v1/groups/%d/members/%d", u, g2ID, taID), "", 204)
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/active", u, taID), `{"active":false}`, 200)
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/active", u, taID), `{"active":true}`, 200)
	must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/password", u, taID), `{"password":"pw2"}`, 204)
	f := must(t, e.admin, "POST", fmt.Sprintf("%s/api/v1/users/%d/factors", u, taID), `{"method":"apikey","label":"devk"}`, 201)
	fkey := f["key"].(string)
	fid := jsonInt(f["factor"].(map[string]any)["id"])
	must(t, e.admin, "DELETE", fmt.Sprintf("%s/api/v1/users/%d/factors/%d", u, taID, fid), "", 204)
	doomed := must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"gone"}`, 201)
	must(t, e.admin, "DELETE", fmt.Sprintf("%s/api/v1/tenants/%d", u, jsonInt(doomed["id"])), "", 204) // snapshot before cascade
	must(t, e.admin, "POST", u+"/api/v1/auth/login", `{"tenant":"watch","username":"wat","password":"BAD"}`, 401)

	events := mustList(t, e.admin, "GET", u+"/api/v1/events?limit=500", 200)
	seen := map[string]int{}
	for _, ev := range events {
		seen[ev.(map[string]any)["action"].(string)]++
	}
	for _, action := range []string{"tenant_create", "user_create", "perm_set", "perm_delete",
		"group_create", "member_add", "member_remove", "user_active", "user_password",
		"factor_create", "factor_delete", "login_failed", "tenant_delete"} {
		if seen[action] == 0 {
			t.Fatalf("event %q missing from %d events", action, len(events))
		}
	}
	if seen["user_active"] < 2 {
		t.Fatalf("both suspensions should be recorded: %v", seen)
	}
	first := events[0].(map[string]any) // newest first: the failed login
	if first["action"] != "login_failed" || first["target_name"] != "wat" || first["actor"] != "watch/wat" {
		t.Fatalf("newest event wrong: %v", first)
	}

	// the plaintext API key must never reach the audit trail
	_, _, raw := reqJSON(t, e.admin, "GET", u+"/api/v1/events?limit=500", "")
	if strings.Contains(string(raw), fkey) {
		t.Fatal("audit trail leaked the API key")
	}
	for _, ev := range events {
		m := ev.(map[string]any)
		if m["action"] == "factor_create" {
			d := m["details"].(map[string]any)
			if d["method"] != "apikey" || d["label"] != "devk" {
				t.Fatalf("factor_create details: %v", d)
			}
		}
	}
	if lf, ok := func() (map[string]any, bool) {
		for _, ev := range events {
			m := ev.(map[string]any)
			if m["action"] == "login_failed" {
				return m, true
			}
		}
		return nil, false
	}(); ok {
		if lf["actor"] != "watch/wat" || lf["ip"] == "" || lf["ip"] == nil {
			t.Fatalf("login_failed record: %v", lf)
		}
	} else {
		t.Fatal("login_failed missing")
	}

	// scoping + filters + permissions
	wat := login(t, e.srv, "watch", "wat", "pw2")
	own := mustList(t, wat, "GET", u+"/api/v1/events?limit=500", 200)
	if len(own) == 0 {
		t.Fatal("tenant admin should see their tenant's events")
	}
	for _, ev := range own {
		m := ev.(map[string]any)
		if jsonInt(m["tenant_id"]) != tid {
			t.Fatalf("cross-tenant leak: %v", m)
		}
	}
	only := mustList(t, e.admin, "GET", u+"/api/v1/events?action=perm_set", 200)
	for _, ev := range only {
		if ev.(map[string]any)["action"] != "perm_set" {
			t.Fatalf("action filter: %v", ev)
		}
	}
	must(t, e.admin, "GET", u+"/api/v1/events?limit=abc", "", 400)
	must(t, e.admin, "GET", u+"/api/v1/events?since=zzz", "", 400)
	peon := must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"peon","password":"pw"}`, 201)
	_ = peon
	peonc := login(t, e.srv, "acme", "peon", "pw")
	must(t, peonc, "GET", u+"/api/v1/events", "", 403)
}

// Every list endpoint shares one contract: limit/offset paging with an
// X-Total-Count header, q/col column search and sort/dir ordering.
func TestListPagingSearchSort(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL

	for i := 1; i <= 12; i++ {
		must(t, e.admin, "POST", u+"/api/v1/users", fmt.Sprintf(`{"username":"u%02d","password":"pw"}`, i), 201)
	}

	// reqGet returns status, raw body and the X-Total-Count header.
	reqGet := func(url string) (int, []byte, int) {
		t.Helper()
		resp, err := e.admin.Get(url)
		if err != nil {
			t.Fatalf("get %s: %v", url, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		total, _ := strconv.Atoi(resp.Header.Get("X-Total-Count"))
		return resp.StatusCode, raw, total
	}
	arrList := func(url string) ([]any, int) {
		t.Helper()
		code, raw, total := reqGet(url)
		if code != 200 {
			t.Fatalf("GET %s: status %d (%s)", url, code, raw)
		}
		var arr []any
		if err := json.Unmarshal(raw, &arr); err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		return arr, total
	}
	objList := func(url string) (map[string]any, int) {
		t.Helper()
		code, raw, total := reqGet(url)
		if code != 200 {
			t.Fatalf("GET %s: status %d (%s)", url, code, raw)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		return body, total
	}

	// ---- users ----
	users := func(qs string) ([]any, int) { return arrList(u + "/api/v1/users?" + qs) }
	if arr, total := users("limit=5"); len(arr) != 5 || total != 13 {
		t.Fatalf("users limit=5: %d rows, total %d; want 5/13", len(arr), total)
	}
	if arr, total := users("limit=5&offset=10"); len(arr) != 3 || total != 13 {
		t.Fatalf("users offset tail: %d rows, total %d; want 3/13", len(arr), total)
	}
	if arr, total := users(""); len(arr) != 13 || total != 13 {
		t.Fatalf("users unparameterised must stay full list: %d/%d; want 13/13", len(arr), total)
	}
	if p1, _ := users("limit=4"); p1[0].(map[string]any)["username"] != "admin" {
		t.Fatalf("users default order: first %v, want admin", p1[0])
	}
	if _, total := users("q=u1"); total != 3 {
		t.Fatalf("users q=u1 total %d, want 3 (u10..u12)", total)
	}
	if _, total := users("q=acme&col=username"); total != 0 {
		t.Fatalf("users q=acme col=username total %d, want 0", total)
	}
	if _, total := users("q=acme&col=tenant"); total != 13 {
		t.Fatalf("users q=acme col=tenant total %d, want 13", total)
	}
	if arr, _ := users("sort=username&dir=desc&limit=1"); arr[0].(map[string]any)["username"] != "u12" {
		t.Fatalf("users desc head: %v, want u12", arr[0])
	}
	for _, qs := range []string{"sort=pass", "col=id", "dir=up", "limit=0", "limit=1001", "offset=-1", "q=" + strings.Repeat("x", 101)} {
		if code, _, _ := reqGet(u + "/api/v1/users?" + qs); code != 400 {
			t.Fatalf("users?%s: status %d, want 400", qs, code)
		}
	}

	// ---- fs listing ----
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/pag"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/pag/sub"}`, 201)
	for name, size := range map[string]int{"a1": 3, "b2": 5, "c3": 7} {
		must(t, e.admin, "POST", u+"/api/v1/fs/write",
			fmt.Sprintf(`{"path":"/pag/%s.txt","content":"%s"}`, name, strings.Repeat("x", size)), 200)
	}
	fslist := func(qs string) (map[string]any, int) {
		return objList(u + "/api/v1/fs?path=/pag&" + qs)
	}
	if body, total := fslist(""); total != 4 || len(body["entries"].([]any)) != 4 {
		t.Fatalf("fs list: %d entries total %d; want 4/4", len(body["entries"].([]any)), total)
	}
	if body, _ := fslist("limit=1"); body["entries"].([]any)[0].(map[string]any)["entry"].(map[string]any)["name"] != "sub" {
		t.Fatalf("fs dirs must sort first: %v", body["entries"].([]any)[0])
	}
	if body, total := fslist("q=.txt"); total != 3 || len(body["entries"].([]any)) != 3 {
		t.Fatalf("fs q=.txt: %d/%d, want 3/3", len(body["entries"].([]any)), total)
	}
	if body, _ := fslist("sort=size&dir=desc"); body["entries"].([]any)[1].(map[string]any)["entry"].(map[string]any)["name"] != "c3.txt" {
		t.Fatalf("fs size desc after dirs must start with c3.txt: %v", body["entries"].([]any)[1])
	}
	if body, total := fslist("limit=2&offset=3"); len(body["entries"].([]any)) != 1 || total != 4 {
		t.Fatalf("fs offset tail: %d rows total %d; want 1/4", len(body["entries"].([]any)), total)
	}

	// ---- perms (grants on one path) ----
	for i := 1; i <= 4; i++ {
		must(t, e.admin, "PUT", u+"/api/v1/perms",
			fmt.Sprintf(`{"path":"/pag","principal_type":"user","principal":"u%02d","read":true}`, i), 200)
	}
	must(t, e.admin, "POST", u+"/api/v1/groups", `{"name":"readers"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/pag","principal_type":"group","principal":"readers","read":true}`, 200)
	pshow := func(qs string) (map[string]any, int) {
		return objList(u + "/api/v1/perms?path=/pag&" + qs)
	}
	if body, total := pshow("limit=2"); total != 5 || len(body["grants"].([]any)) != 2 {
		t.Fatalf("perms limit=2: %d rows total %d; want 2/5", len(body["grants"].([]any)), total)
	}
	if _, total := pshow("q=reader"); total != 1 {
		t.Fatalf("perms q=reader total %d, want 1", total)
	}
	if _, total := pshow("q=u0&col=principal"); total != 4 {
		t.Fatalf("perms q=u0 col=principal total %d, want 4", total)
	}
	if _, total := pshow("q=acme&col=tenant"); total != 5 {
		t.Fatalf("perms q=acme col=tenant total %d, want 5", total)
	}

	// ---- groups & tenants ----
	must(t, e.admin, "POST", u+"/api/v1/groups", `{"name":"ops"}`, 201)
	if arr, total := arrList(u + "/api/v1/groups?limit=1"); len(arr) != 1 || total != 2 {
		t.Fatalf("groups limit=1: %d rows total %d; want 1/2", len(arr), total)
	}
	if _, total := arrList(u + "/api/v1/groups?q=ops"); total != 1 {
		t.Fatalf("groups q=ops total %d, want 1", total)
	}
	if _, total := arrList(u + "/api/v1/tenants?q=zzz"); total != 0 {
		t.Fatalf("tenants q=zzz total %d, want 0", total)
	}
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"beta"}`, 201)
	if _, total := arrList(u + "/api/v1/tenants"); total != 2 {
		t.Fatalf("tenants total %d, want 2", total)
	}

	// ---- events ----
	evs := func(qs string) ([]any, int) { return arrList(u + "/api/v1/events?" + qs) }
	if arr, total := evs("limit=5"); len(arr) != 5 || total <= 5 {
		t.Fatalf("events limit=5: %d rows total %d; want 5/>5", len(arr), total)
	}
	arr, total := evs("q=user_create")
	if total == 0 {
		t.Fatalf("events q=user_create total %d, want >0", total)
	}
	for _, ev := range arr2slice(arr) {
		if ev["action"] != "user_create" {
			t.Fatalf("events q=user_create leak: %v", ev)
		}
	}
	if arr, _ := evs("sort=ts&dir=asc&limit=5"); jsonInt(arr[0].(map[string]any)["ts"]) > jsonInt(arr[4].(map[string]any)["ts"]) {
		t.Fatal("events asc order broken")
	}
	if arr, _ := evs("limit=5"); jsonInt(arr[0].(map[string]any)["ts"]) < jsonInt(arr[4].(map[string]any)["ts"]) {
		t.Fatal("events must stay newest-first by default")
	}
	if _, total := evs("action=user_create&q=u0&col=target"); total < 1 {
		t.Fatalf("events structured filter + q total %d, want >=1", total)
	}
}

// arr2slice decodes a []any of objects into []map[string]any.
func arr2slice(arr []any) []map[string]any {
	out := make([]map[string]any, 0, len(arr))
	for _, v := range arr {
		out = append(out, v.(map[string]any))
	}
	return out
}

// TestEntityMetaActive covers description + active across all three entity
// kinds: create bodies accept a description, lists expose/search/filter/sort
// by it, PUT /{id} updates it, and the active filter narrows lists quickly.
func TestEntityMetaActive(t *testing.T) {
	e := newEnv(t)
	u := e.admin
	base := e.srv.URL + "/api/v1"

	tn := must(t, u, "POST", base+"/tenants", `{"name":"acme2","description":"customer acme 2"}`, http.StatusCreated)
	if tn["description"] != "customer acme 2" || tn["active"] != true {
		t.Fatalf("tenant create echo: %v", tn)
	}
	usr := must(t, u, "POST", base+"/users", `{"username":"meta1","password":"pw123456","description":"q3 contractor"}`, http.StatusCreated)
	if usr["description"] != "q3 contractor" || usr["active"] != true {
		t.Fatalf("user create echo: %v", usr)
	}
	grp := must(t, u, "POST", base+"/groups", `{"name":"gmeta","description":"build agents"}`, http.StatusCreated)
	if grp["description"] != "build agents" || grp["active"] != true {
		t.Fatalf("group create echo: %v", grp)
	}

	// description is searchable per column and via the default all-columns search
	if arr := mustList(t, u, "GET", base+"/users?q=contractor&col=description", http.StatusOK); len(arr) != 1 {
		t.Fatalf("users col=description hit: %v", arr)
	}
	if arr := mustList(t, u, "GET", base+"/users?q=contractor", http.StatusOK); len(arr) != 1 {
		t.Fatalf("users all-columns description hit: %v", arr)
	}
	if arr := mustList(t, u, "GET", base+"/groups?q=build&col=description", http.StatusOK); len(arr) != 1 {
		t.Fatalf("groups col=description hit: %v", arr)
	}
	if arr := mustList(t, u, "GET", base+"/tenants?q=acme%202&col=description", http.StatusOK); len(arr) != 1 {
		t.Fatalf("tenants col=description hit: %v", arr)
	}
	// and sortable
	if arr := mustList(t, u, "GET", base+"/users?sort=description", http.StatusOK); len(arr) == 0 {
		t.Fatal("users sort=description empty")
	}

	// PUT updates descriptions
	uid := jsonInt(usr["id"])
	gid := jsonInt(grp["id"])
	tid := jsonInt(tn["id"])
	must(t, u, "PUT", base+"/users/"+strconv.FormatInt(uid, 10), `{"description":"now permanent"}`, http.StatusNoContent)
	must(t, u, "PUT", base+"/groups/"+strconv.FormatInt(gid, 10), `{"description":"build + release agents"}`, http.StatusNoContent)
	must(t, u, "PUT", base+"/tenants/"+strconv.FormatInt(tid, 10), `{"description":"renamed customer"}`, http.StatusNoContent)
	if arr := mustList(t, u, "GET", base+"/users?q=permanent&col=description", http.StatusOK); len(arr) != 1 {
		t.Fatalf("user description update not visible: %v", arr)
	}

	// active filter: exact-match on each entity list, 400 on non-entity lists
	must(t, u, "POST", base+"/users/"+strconv.FormatInt(uid, 10)+"/active", `{"active":false}`, http.StatusOK)
	ids := func(url string) []int64 {
		var out []int64
		for _, row := range mustList(t, u, "GET", url, http.StatusOK) {
			out = append(out, jsonInt(row.(map[string]any)["id"]))
		}
		return out
	}
	has := func(ids []int64, id int64) bool {
		for _, v := range ids {
			if v == id {
				return true
			}
		}
		return false
	}
	if has(ids(base+"/users?active=true"), uid) || !has(ids(base+"/users?active=false"), uid) {
		t.Fatal("users active filter broken")
	}
	must(t, u, "POST", base+"/groups/"+strconv.FormatInt(gid, 10)+"/active", `{"active":false}`, http.StatusOK)
	if has(ids(base+"/groups?active=true"), gid) || !has(ids(base+"/groups?active=false"), gid) {
		t.Fatal("groups active filter broken")
	}
	for _, bad := range []string{base + "/users?active=maybe", base + "/fs?path=/&active=true", base + "/events?active=true"} {
		if code, _, _ := reqJSON(t, u, "GET", bad, ""); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 for invalid/unsupported active", bad, code)
		}
	}
}

// TestTenantSuspend exercises the tenant-wide kill switch: a suspended
// tenant's users can neither log in nor keep live sessions, and the tenant
// is filterable via active=false. Super admins may not suspend their own
// tenant (footgun guard), but may suspend and reinstate another.
func TestTenantSuspend(t *testing.T) {
	e := newEnv(t)
	u := e.admin
	base := e.srv.URL + "/api/v1"

	tn := must(t, u, "POST", base+"/tenants", `{"name":"offgrid","description":"paused account"}`, http.StatusCreated)
	tid := jsonInt(tn["id"])
	must(t, u, "POST", base+"/users", `{"username":"cust","password":"pw123456","tenant":"offgrid"}`, http.StatusCreated)

	c := login(t, e.srv, "offgrid", "cust", "pw123456") // live session before the switch
	must(t, c, "GET", base+"/auth/me", "", http.StatusOK)

	must(t, u, "POST", base+"/tenants/"+strconv.FormatInt(tid, 10)+"/active", `{"active":false}`, http.StatusNoContent)

	// live session dies on the next request
	if code, _, _ := reqJSON(t, c, "GET", base+"/auth/me", ""); code != http.StatusUnauthorized {
		t.Fatalf("auth/me after tenant suspend: %d, want 401", code)
	}
	// and new logins fail like unknown credentials
	jar, _ := cookiejar.New(nil)
	fresh := &http.Client{Jar: jar}
	if code, _, _ := reqJSON(t, fresh, "POST", base+"/auth/login",
		`{"tenant":"offgrid","username":"cust","password":"pw123456"}`); code != http.StatusUnauthorized {
		t.Fatalf("login after tenant suspend: %d, want 401", code)
	}

	// visible through the active filter, and its users/grants are untouched
	if arr := mustList(t, u, "GET", base+"/tenants?active=false", http.StatusOK); len(arr) != 1 {
		t.Fatalf("tenants?active=false: %v", arr)
	}
	if arr := mustList(t, u, "GET", base+"/users?q=cust", http.StatusOK); len(arr) != 1 {
		t.Fatalf("suspended tenant's users must remain listed: %v", arr)
	}

	// footgun guard: the bootstrap super admin sits in acme; acme cannot self-suspend
	acmeID := jsonInt(mustList(t, u, "GET", base+"/tenants?q=acme&col=name", http.StatusOK)[0].(map[string]any)["id"])
	if code, _, _ := reqJSON(t, u, "POST", base+"/tenants/"+strconv.FormatInt(acmeID, 10)+"/active", `{"active":false}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("self-tenant suspend: %d, want 422", code)
	}

	// reinstatement brings the tenant back: the old session row was deleted
	// by the kill switch, but a fresh login succeeds again
	must(t, u, "POST", base+"/tenants/"+strconv.FormatInt(tid, 10)+"/active", `{"active":true}`, http.StatusNoContent)
	c2 := login(t, e.srv, "offgrid", "cust", "pw123456")
	must(t, c2, "GET", base+"/auth/me", "", http.StatusOK)
}

// TestDrafts: the draft/publish lifecycle. Drafting takes the w bit (an
// r-only caller can view but not save); last writer wins; publish refuses
// invalid YAML without touching file or draft, and on success writes the
// file, drops the draft and leaves an fs_commit event behind.
func TestDrafts(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/cfg"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/cfg/app.yml","content":"a: 1\n"}`, 200)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"ro","password":"pw"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/cfg","principal_type":"user","principal":"ro","read":true}`, 200)
	ro := login(t, e.srv, "acme", "ro", "pw")

	// no draft yet
	if got := must(t, e.admin, "GET", u+"/api/v1/fs/draft?path=/cfg/app.yml", "", 200); got["draft"] != nil {
		t.Fatalf("expected draft null, got %v", got["draft"])
	}
	// r-only: may view a draft, may not create, commit or discard one
	must(t, ro, "GET", u+"/api/v1/fs/draft?path=/cfg/app.yml", "", 200)
	must(t, ro, "PUT", u+"/api/v1/fs/draft", `{"path":"/cfg/app.yml","content":"a: 2\n"}`, 403)
	must(t, ro, "POST", u+"/api/v1/fs/draft/commit", `{"path":"/cfg/app.yml"}`, 403)
	must(t, ro, "DELETE", u+"/api/v1/fs/draft?path=/cfg/app.yml", "", 403)

	// save, then last writer wins
	must(t, e.admin, "PUT", u+"/api/v1/fs/draft", `{"path":"/cfg/app.yml","content":"a: 2\n"}`, 200)
	must(t, e.admin, "PUT", u+"/api/v1/fs/draft", `{"path":"/cfg/app.yml","content":"a: [1,\n"}`, 200)
	got := must(t, e.admin, "GET", u+"/api/v1/fs/draft?path=/cfg/app.yml", "", 200)
	d, ok := got["draft"].(map[string]any)
	if !ok || d["content"] != "a: [1,\n" || d["updated_by"] != "acme/admin" {
		t.Fatalf("last writer did not win: %v", got["draft"])
	}

	// invalid YAML: commit is refused, file and draft untouched
	must(t, e.admin, "POST", u+"/api/v1/fs/draft/commit", `{"path":"/cfg/app.yml"}`, 422)
	if body := must(t, e.admin, "GET", u+"/api/v1/fs/content?path=/cfg/app.yml", "", 200); body["content"] != "a: 1\n" {
		t.Fatalf("commit changed the file despite invalid YAML: %q", body["content"])
	}

	// publish a valid multi-document draft
	const published = "a: 2 # keep comments\n---\nb: [1, 2]\n"
	must(t, e.admin, "PUT", u+"/api/v1/fs/draft", `{"path":"/cfg/app.yml","content":`+`"`+escapeJSON(published)+`"}`, 200)
	must(t, e.admin, "POST", u+"/api/v1/fs/draft/commit", `{"path":"/cfg/app.yml"}`, 200)
	if body := must(t, e.admin, "GET", u+"/api/v1/fs/content?path=/cfg/app.yml", "", 200); body["content"] != published {
		t.Fatalf("file not updated: %q", body["content"])
	}
	// the draft is gone once published
	if got := must(t, e.admin, "GET", u+"/api/v1/fs/draft?path=/cfg/app.yml", "", 200); got["draft"] != nil {
		t.Fatalf("draft survived commit: %v", got["draft"])
	}

	// committing nothing is a conflict; discarding is idempotent
	must(t, e.admin, "POST", u+"/api/v1/fs/draft/commit", `{"path":"/cfg/app.yml"}`, 409)
	must(t, e.admin, "PUT", u+"/api/v1/fs/draft", `{"path":"/cfg/app.yml","content":"a: 9\n"}`, 200)
	must(t, e.admin, "DELETE", u+"/api/v1/fs/draft?path=/cfg/app.yml", "", 204)
	must(t, e.admin, "DELETE", u+"/api/v1/fs/draft?path=/cfg/app.yml", "", 204)

	// the publish left a forensic trail
	var found bool
	for _, ev := range mustList(t, e.admin, "GET", u+"/api/v1/events?action=fs_commit", 200) {
		if m := ev.(map[string]any); m["path"] == "/cfg/app.yml" {
			found = true
		}
	}
	if !found {
		t.Fatal("no fs_commit event recorded")
	}
}

// TestBaseMtimeGuard: /fs/write may be conditional on the mtime the caller
// read; a stale base_mtime yields 409 and leaves the file alone.
func TestBaseMtimeGuard(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/g.txt","content":"v1"}`, 200)
	content := must(t, e.admin, "GET", u+"/api/v1/fs/content?path=/g.txt", "", 200)
	mt, ok := content["mtime"].(float64)
	if !ok || mt == 0 {
		t.Fatalf("content response missing mtime: %v", content)
	}
	// matching base_mtime goes through
	must(t, e.admin, "POST", u+"/api/v1/fs/write",
		`{"path":"/g.txt","content":"v2","base_mtime":`+strconv.FormatInt(int64(mt), 10)+`}`, 200)
	// a stale value conflicts instead of clobbering (mtime granularity is
	// one second, so compare against mt-1 rather than timing a re-write)
	must(t, e.admin, "POST", u+"/api/v1/fs/write",
		`{"path":"/g.txt","content":"v3","base_mtime":`+strconv.FormatInt(int64(mt)-1, 10)+`}`, 409)
	if body := must(t, e.admin, "GET", u+"/api/v1/fs/content?path=/g.txt", "", 200); body["content"] != "v2" {
		t.Fatalf("conflict did not preserve the file: %v", body["content"])
	}
	// base_mtime against a missing path is a conflict, not a create
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/gone.txt","content":"x","base_mtime":1}`, 409)
	// without the field the endpoint behaves exactly as before
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/g.txt","content":"v4"}`, 200)
}

func escapeJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b[1 : len(b)-1])
}

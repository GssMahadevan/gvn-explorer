package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/user/llm-crud1/internal/acl"
	"github.com/user/llm-crud1/internal/auth"
	"github.com/user/llm-crud1/internal/db"
	"github.com/user/llm-crud1/internal/fsx"
)

type env struct {
	srv   *httptest.Server
	admin *http.Client // super admin in tenant "acme"
}

func newEnv(t *testing.T) *env {
	t.Helper()
	conn, err := db.Open(":memory:")
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
	if _, err := as.CreateTenant(ctx, "acme"); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	if _, err := as.CreateUser(ctx, "acme", "admin", "pw", true, true); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	api := API{Auth: as, ACL: acl.NewStore(conn), FS: tree, SessionTTL: time.Hour, MaxFileMB: 1}
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

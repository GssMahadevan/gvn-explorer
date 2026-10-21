package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GssMahadevan/gvn-explorer/internal/acl"
	"github.com/GssMahadevan/gvn-explorer/internal/audit"
	"github.com/GssMahadevan/gvn-explorer/internal/auth"
	"github.com/GssMahadevan/gvn-explorer/internal/db"
	"github.com/GssMahadevan/gvn-explorer/internal/drafts"
	"github.com/GssMahadevan/gvn-explorer/internal/fsx"
	"github.com/GssMahadevan/gvn-explorer/internal/query"
)

// newEnvSlots is newEnvLimits plus the global bcrypt cap (-auth-concurrency
// equivalent); 0 leaves the cap off.
func newEnvSlots(t *testing.T, authConcurrency int) *env {
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
	api := API{DB: conn, Auth: as, ACL: acl.NewStore(conn), Events: audit.NewStore(conn),
		Drafts: drafts.NewStore(conn),
		FS:     tree, SessionTTL: time.Hour, MaxFileMB: 1,
		AuthConcurrency: authConcurrency}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &env{srv: srv, admin: login(t, srv, "acme", "admin", "pw")}
}

// TestPairingJoinFlow walks the whole invite: admin opens it, the device
// redeems the QR token, gets a working session and a loginable password,
// and the invite is spent afterwards.
func TestPairingJoinFlow(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL

	p := must(t, e.admin, "POST", u+"/api/v1/pairings",
		`{"description":"Mia's phone"}`, 201)
	token, _ := p["token"].(string)
	code, _ := p["code"].(string)
	if len(token) < 40 || len(code) != 7 {
		t.Fatalf("claim secrets missing/odd: token=%q code=%q", token, code)
	}
	pm := p["pairing"].(map[string]any)
	if pm["status"] != "pending" {
		t.Fatalf("pairing body: %v", pm)
	}
	pid := jsonInt(pm["id"])

	// the list shows metadata only — no redeemable secret anywhere
	_, _, raw := reqJSON(t, e.admin, "GET", u+"/api/v1/pairings", "")
	for _, s := range []string{token, code} {
		if strings.Contains(string(raw), s) {
			t.Fatalf("list leaked a claim secret: %s", raw)
		}
	}

	// redeem with the QR token: 200 + session cookie + one-time password
	jar, _ := cookiejar.New(nil)
	dev := &http.Client{Jar: jar}
	r := must(t, dev, "POST", u+"/api/v1/join", `{"token":"`+token+`"}`, 200)
	user := r["user"].(map[string]any)
	name, _ := user["username"].(string)
	pass, _ := r["password"].(string)
	if name == "" || pass == "" {
		t.Fatalf("join response missing credentials: %v", r)
	}
	if user["is_admin"] != false || user["is_super"] != false {
		t.Fatalf("joined user must be plain: %v", user)
	}
	// the session cookie is live immediately
	if me := must(t, dev, "GET", u+"/api/v1/auth/me", "", 200); jsonInt(me["id"]) != jsonInt(user["id"]) {
		t.Fatalf("join session belongs to someone else: %v", me)
	}
	// and the generated password really logs in
	login(t, e.srv, "acme", name, pass)

	// spent: same token again conflicts, and the list now says used
	must(t, dev, "POST", u+"/api/v1/join", `{"token":"`+token+`"}`, 409)
	after := mustList(t, e.admin, "GET", u+"/api/v1/pairings", 200)
	if len(after) != 1 {
		t.Fatalf("pairings list: %v", after)
	}
	got := after[0].(map[string]any)
	if got["status"] != "used" || got["used_by"] != name || jsonInt(got["id"]) != pid {
		t.Fatalf("used pairing: %v", got)
	}

	// audit trail covers both sides of the join
	evs := mustList(t, e.admin, "GET", u+"/api/v1/events?action=pair_join", 200)
	if len(evs) != 1 {
		t.Fatalf("pair_join audit: %v", evs)
	}
	if ev := evs[0].(map[string]any); ev["target_type"] != "pairing" || jsonInt(ev["target_id"]) != pid {
		t.Fatalf("pair_join event: %v", ev)
	}
	if len(mustList(t, e.admin, "GET", u+"/api/v1/events?action=user_create", 200)) != 1 {
		t.Fatal("join must leave a user_create event (pairing-born)")
	}
}

// TestPairingCodeAndKey covers the typed short code path and the
// "also issue an API key" flag end to end.
func TestPairingCodeAndKey(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL

	p := must(t, e.admin, "POST", u+"/api/v1/pairings",
		`{"description":"backup cron", "username": "syncbot", "apikey": true}`, 201)
	code := p["code"].(string)
	must(t, &http.Client{}, "POST", u+"/api/v1/join", `{"code":"NOSUCHC"}`, 404)

	r := must(t, &http.Client{}, "POST", u+"/api/v1/join", `{"code":"`+code+`"}`, 200)
	user := r["user"].(map[string]any)
	if user["username"] != "syncbot" {
		t.Fatalf("preset username ignored: %v", user)
	}
	key, _ := r["apikey"].(string)
	if len(key) < 40 {
		t.Fatalf("apikey flag produced no key: %v", r)
	}
	// the key works as a bearer credential for the new account
	req, err := http.NewRequest("GET", u+"/api/v1/auth/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("paired api key rejected: %d", resp.StatusCode)
	}

	// a preset username that already exists conflicts at create time
	must(t, e.admin, "POST", u+"/api/v1/pairings",
		`{"description":"dupe", "username": "admin"}`, 409)
	// and both secrets at once is a validation error, not a guess
	must(t, &http.Client{}, "POST", u+"/api/v1/join", `{"token":"x","code":"Y"}`, 422)
}

// TestPairingsAccess: plain users cannot touch the endpoint; tenant admins
// see and revoke only their own tenant's invites; the list honours the
// standard paging contract.
func TestPairingsAccess(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL

	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"pleb","password":"pw3"}`, 201)
	plain := login(t, e.srv, "acme", "pleb", "pw3")
	must(t, plain, "POST", u+"/api/v1/pairings", `{"description":"nope"}`, 403)
	must(t, plain, "GET", u+"/api/v1/pairings", "", 403)
	must(t, plain, "DELETE", u+"/api/v1/pairings/1", "", 403)

	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"watch"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"wat","password":"pw2","is_admin":true,"tenant":"watch"}`, 201)
	wat := login(t, e.srv, "watch", "wat", "pw2")
	own := must(t, wat, "POST", u+"/api/v1/pairings", `{"description":"wat tv"}`, 201)
	watPID := jsonInt(own["pairing"].(map[string]any)["id"])
	for _, x := range mustList(t, wat, "GET", u+"/api/v1/pairings", 200) {
		if x.(map[string]any)["tenant"] != "watch" {
			t.Fatalf("tenant admin saw a foreign pairing: %v", x)
		}
	}
	// wat cannot burn an invite from acme...
	acme := must(t, e.admin, "POST", u+"/api/v1/pairings", `{"description":"phone"}`, 201)
	acmePID := jsonInt(acme["pairing"].(map[string]any)["id"])
	must(t, wat, "DELETE", u+"/api/v1/pairings/"+strconv.FormatInt(acmePID, 10), "", 404)
	// ...but may revoke their own, and joining a revoked invite conflicts
	must(t, wat, "DELETE", u+"/api/v1/pairings/"+strconv.FormatInt(watPID, 10), "", 204)
	code := own["code"].(string)
	must(t, &http.Client{}, "POST", u+"/api/v1/join", `{"code":"`+code+`"}`, 409)
	// revoking twice is a clean 404
	must(t, wat, "DELETE", u+"/api/v1/pairings/"+strconv.FormatInt(watPID, 10), "", 404)

	// paging contract on the new list endpoint
	for i := 0; i < 3; i++ {
		must(t, e.admin, "POST", u+"/api/v1/pairings",
			`{"description":"bulk-`+strconv.Itoa(i)+`"}`, 201)
	}
	if n := len(mustList(t, e.admin, "GET", u+"/api/v1/pairings?limit=2", 200)); n != 2 {
		t.Fatalf("limit ignored: %d rows", n)
	}
	if q := mustList(t, e.admin, "GET", u+"/api/v1/pairings?q="+url.QueryEscape("bulk-1"), 200); len(q) != 1 {
		t.Fatalf("q filter: %v", q)
	}
	for _, bad := range []string{"?limit=abc", "?sort=nope", "?dir=sideways", "?col=nope"} {
		must(t, e.admin, "GET", u+"/api/v1/pairings"+bad, "", 400)
	}
}

// TestPairingExpiry exercises the store directly: an invite past its
// expiry is unredeemable and reports as expired in the list.
func TestPairingExpiry(t *testing.T) {
	conn, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	as := auth.NewStore(conn)
	ctx := context.Background()
	if _, err := as.CreateTenant(ctx, "acme", ""); err != nil {
		t.Fatal(err)
	}
	au, err := as.CreateUser(ctx, "acme", "admin", "pw", "", true, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	tid, err := as.TenantID(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}

	p, token, code, err := as.CreatePairing(ctx, tid, "stale", "", false, nil, 500*time.Millisecond, au.ID)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(p.ExpiresAt) > time.Second {
		t.Fatalf("ttl not applied: %v", p.ExpiresAt)
	}
	time.Sleep(700 * time.Millisecond)
	if _, _, _, _, err := as.RedeemPairing(ctx, token); err == nil {
		t.Fatal("expired invite redeemed")
	}
	if _, _, _, _, err := as.RedeemPairing(ctx, code); err == nil {
		t.Fatal("expired invite redeemed via code")
	}
	// the list view derives the expired status
	ps, total, err := as.ListPairings(ctx, 0, query.List{Sort: "created", Dir: "desc"})
	if err != nil || total != 1 || ps[0].Status != "expired" {
		t.Fatalf("expired status: err=%v total=%d rows=%+v", err, total, ps)
	}
}

// TestAuthSlots proves the global bcrypt cap: with one slot, concurrent
// logins cannot all pay for bcrypt at once — the excess gets 503 with a
// Retry-After, and the service is healthy again once the burst passes.
func TestAuthSlots(t *testing.T) {
	e := newEnvSlots(t, 1)
	u := e.srv.URL

	var wg sync.WaitGroup
	var mu sync.Mutex
	var got503, passed int
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &http.Client{}
			resp, err := c.Post(u+"/api/v1/auth/login", "application/json",
				strings.NewReader(`{"tenant":"acme","username":"admin","password":"wrong-password"}`))
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusServiceUnavailable && resp.Header.Get("Retry-After") != "" {
				mu.Lock()
				got503++
				mu.Unlock()
				return
			}
			if resp.StatusCode == http.StatusUnauthorized {
				mu.Lock()
				passed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	// bcrypt at default cost takes ~100 ms: four simultaneous requests
	// against one slot cannot all get through.
	if got503 == 0 {
		t.Fatalf("nothing was capped (%d passed, 0 refused)", passed)
	}
	// slots are released after each request: serial use still works
	must(t, &http.Client{}, "POST", u+"/api/v1/auth/login",
		`{"tenant":"acme","username":"admin","password":"wrong-password"}`, 401)
}

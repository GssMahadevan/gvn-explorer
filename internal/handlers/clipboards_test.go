package handlers

// Clipboards (Batch E): tenant link feeds with a REST face and a
// virtual read-only subtree in the home. The invariant under test is
// that the link is only ever as strong as the target file's ACL —
// post requires read, open re-checks read, a removed target reads as
// dead, expiry purges lazily, and the fs write surface refuses to
// touch the virtual tree.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// cid renders a clipboard id parsed from a JSON response for URL use.
func cid(f float64) string { return strconv.Itoa(int(f)) }

// clipList decodes a top-level-array clipboard/item list (entity-list
// convention) from a must()-style call.
func clipList(t *testing.T, c *http.Client, method, url, body string) []map[string]any {
	t.Helper()
	code, _, raw := reqJSON(t, c, method, url, body)
	if code != 200 {
		t.Fatalf("%s %s: status %d want 200 (%s)", method, url, code, raw)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode array: %v (%s)", err, raw)
	}
	return out
}

func TestClipboards(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL
	if err := e.api.BootstrapHomes(context.Background()); err != nil {
		t.Fatalf("bootstrap homes: %v", err)
	}
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"acme","username":"alice","password":"pw"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"acme","username":"mallory","password":"pw"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/tenants", `{"name":"globex"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/users", `{"tenant":"globex","username":"wendy","password":"pw"}`, 201)
	alice := login(t, e.srv, "acme", "alice", "pw")
	mallory := login(t, e.srv, "acme", "mallory", "pw")
	wendy := login(t, e.srv, "globex", "wendy", "pw")

	// a target file alice can read: super writes it, grants alice r.
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/shared"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/shared/a.txt","content":"clip target"}`, 200)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/shared","principal_type":"user","principal":"alice","read":true}`, 200)

	// --- create: any member may; validation is strict ---
	c := must(t, alice, "POST", u+"/api/v1/clipboards", `{"name":"work","mime":"image/*"}`, 201)
	clipID := c["id"].(float64)
	if c["link_path"] != "/tenants/acme/clipboards/work" {
		t.Errorf("link_path: %v", c["link_path"])
	}
	must(t, alice, "POST", u+"/api/v1/clipboards", `{"name":"work"}`, 409)
	must(t, alice, "POST", u+"/api/v1/clipboards", `{"name":"Bad Name"}`, 422)
	must(t, alice, "POST", u+"/api/v1/clipboards", `{"name":"x","mime":"application/zip"}`, 422)
	// ""mime defaults to */* (mixed feed)
	must(t, alice, "POST", u+"/api/v1/clipboards", `{"name":"misc"}`, 201)

	// --- posting requires read on the target ---
	must(t, mallory, "POST", u+"/api/v1/clipboards", `{"name":"no"}`, 201)
	nc := clipList(t, mallory, "GET", u+"/api/v1/clipboards", "")
	var noID float64
	for _, cc := range nc {
		if cc["name"] == "no" {
			noID = cc["id"].(float64)
		}
	}
	// mallory cannot read /shared/a.txt: posting would hand out a share
	// she cannot exercise — refused.
	must(t, mallory, "PUT", u+"/api/v1/clipboards/"+cid(noID)+"/items", `{"path":"/shared/a.txt"}`, 403)
	// a directory and a virtual path are not valid targets
	must(t, alice, "PUT", u+"/api/v1/clipboards/"+cid(clipID)+"/items", `{"path":"/shared"}`, 400)
	must(t, alice, "PUT", u+"/api/v1/clipboards/"+cid(clipID)+"/items", `{"path":"/tenants/acme/clipboards/work"}`, 400)

	it := must(t, alice, "PUT", u+"/api/v1/clipboards/"+cid(clipID)+"/items", `{"path":"/shared/a.txt"}`, 201)
	link := it["link_path"].(string)
	if link == "" || it["content_type"] != "text/plain; charset=utf-8" {
		t.Errorf("posted item: %v", it)
	}

	// --- the virtual tree: home listing shows the feed folder, the
	// feed shows the link, and opening the link serves the target ---
	home := listedPaths(t, alice, u+"/api/v1/fs?path=/tenants/acme")
	if !contains(home, "/tenants/acme/clipboards") {
		t.Errorf("virtual clipboards folder missing from the home listing: %v", home)
	}
	idx := listedPaths(t, alice, u+"/api/v1/fs?path=/tenants/acme/clipboards")
	if !contains(idx, "/tenants/acme/clipboards/work") || !contains(idx, "/tenants/acme/clipboards/misc") {
		t.Errorf("feed index wrong: %v", idx)
	}
	feed := listedPaths(t, alice, u+"/api/v1/fs?path=/tenants/acme/clipboards/work")
	if !contains(feed, link) {
		t.Errorf("item missing from feed listing: %v want %v", feed, link)
	}
	got := must(t, alice, "GET", u+"/api/v1/fs/content?path="+link, "", 200)
	if got["content"] != "clip target" {
		t.Errorf("reading through the link: %v", got)
	}
	// raw works through the link too (status + content-type come from
	// the normal raw pipeline)
	if code, _, _ := reqJSON(t, alice, "GET", u+"/api/v1/fs/raw?path="+link, ""); code != 200 {
		t.Errorf("raw through the link: %d", code)
	}

	// the default tenant grant exists on the feed path (Perms panel
	// truth), and a tenant admin can see it.
	eff := must(t, alice, "GET", u+"/api/v1/perms?path=/tenants/acme/clipboards/work", "", 200)
	if ef := eff["effective"].(map[string]any); ef["read"] != true {
		t.Errorf("member effective on feed path: %v", ef)
	}
	if gr := eff["grants"].([]any); len(gr) != 1 {
		t.Errorf("expected exactly the default tenant grant, got %v", gr)
	}

	// --- other tenants never see acme's feeds ---
	if w := clipList(t, wendy, "GET", u+"/api/v1/clipboards", ""); len(w) != 0 {
		t.Errorf("foreign tenant sees acme feeds: %v", w)
	}
	must(t, wendy, "GET", u+"/api/v1/fs?path=/tenants/acme/clipboards", "", 403)
	must(t, wendy, "GET", u+"/api/v1/fs/content?path="+link, "", 403)

	// a same-tenant member without any grant on /shared cannot open
	// the link (target ACL gates every read) and cannot see it listed.
	mall := listedPaths(t, mallory, u+"/api/v1/fs?path=/tenants/acme/clipboards/work")
	if contains(mall, link) {
		t.Errorf("unreadable live link listed for a member: %v", mall)
	}
	if items := clipList(t, mallory, "GET", u+"/api/v1/clipboards/"+cid(clipID)+"/items", ""); len(items) != 0 {
		t.Errorf("unreadable live link in items list: %v", items)
	}
	must(t, mallory, "GET", u+"/api/v1/fs/content?path="+link, "", 403)

	// --- the virtual tree rejects fs writes ---
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/tenants/acme/clipboards/work","content":"x"}`, 403)
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/tenants/acme/clipboards/sub"}`, 403)
	must(t, e.admin, "DELETE", u+"/api/v1/fs?path=/tenants/acme/clipboards/work", "", 403)
	must(t, e.admin, "POST", u+"/api/v1/fs/rename", `{"from":"/shared/a.txt","to":"/tenants/acme/clipboards/a.txt"}`, 403)

	// --- dead links stay listed, flagged ---
	must(t, e.admin, "DELETE", u+"/api/v1/fs?path=/shared/a.txt", "", 204)
	items := clipList(t, alice, "GET", u+"/api/v1/clipboards/"+cid(clipID)+"/items", "")
	if len(items) != 1 || items[0]["dead"] != true {
		t.Fatalf("dead link must stay listed with dead=true: %v", items)
	}
	if code, _, _ := reqJSON(t, alice, "GET", u+"/api/v1/fs/content?path="+link, ""); code != 404 {
		t.Errorf("dead link content: %d want 404", code)
	}

	// --- delete: creator or tenant admin only; grants go with it ---
	must(t, mallory, "DELETE", u+"/api/v1/clipboards/"+cid(clipID), "", 403)
	must(t, alice, "DELETE", u+"/api/v1/clipboards/"+cid(clipID), "", 204)
	if gr := must(t, alice, "GET", u+"/api/v1/perms?path=/tenants/acme/clipboards/work", "", 200)["grants"]; len(gr.([]any)) != 0 {
		t.Errorf("grants must be removed with the feed: %v", gr)
	}

	// --- the item cap (-clip-max-items) is enforced ---
	e.api.Clips.MaxItems = 1
	must(t, alice, "POST", u+"/api/v1/fs/write", `{"path":"/shared/b.txt","content":"b"}`, 403) // alice has no write on /shared
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/shared/b.txt","content":"b"}`, 200)
	must(t, e.admin, "PUT", u+"/api/v1/perms", `{"path":"/shared","principal_type":"user","principal":"alice","read":true}`, 200)
	// misc feed (id from list): post once, then expect the cap
	var miscID float64
	for _, cc := range clipList(t, alice, "GET", u+"/api/v1/clipboards", "") {
		if cc["name"] == "misc" {
			miscID = cc["id"].(float64)
		}
	}
	// post once with a short TTL, hit the cap, then watch the lazy
	// purge free both the feed and the cap.
	e.api.ClipTTL = 100 * time.Millisecond
	must(t, alice, "PUT", u+"/api/v1/clipboards/"+cid(miscID)+"/items", `{"path":"/shared/b.txt"}`, 201)
	must(t, alice, "PUT", u+"/api/v1/clipboards/"+cid(miscID)+"/items", `{"path":"/shared/b.txt"}`, 409)
	time.Sleep(150 * time.Millisecond)
	if items := clipList(t, alice, "GET", u+"/api/v1/clipboards/"+cid(miscID)+"/items", ""); len(items) != 0 {
		t.Errorf("expired item must be purged on read: %v", items)
	}
	// the cap freed with the expiry
	e.api.ClipTTL = 24 * time.Hour
	must(t, alice, "PUT", u+"/api/v1/clipboards/"+cid(miscID)+"/items", `{"path":"/shared/b.txt"}`, 201)
}

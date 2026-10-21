package handlers

// TestTimestampPrecision: since migration 0006 the audit trail, the draft
// auto-save line and file mtimes all travel at millisecond precision —
// added next to (never replacing) the long-standing seconds fields. What
// the GUI now renders from those *_ms fields is asserted here at the API
// level: exact instants, consistent seconds/milliseconds pairs, and a
// since= filter that can cut *inside* a second.

import (
	"encoding/json"
	"io"
	"strconv"
	"testing"
	"time"
)

// consistentPair asserts the seconds/ms pair of one JSON object: both
// present, and the seconds value is the floor of the ms one.
func consistentPair(t *testing.T, obj map[string]any, secKey, msKey string) int64 {
	t.Helper()
	ms, ok := obj[msKey].(float64)
	if !ok || ms <= 0 {
		t.Fatalf("%s: missing %s: %v", obj["path"], msKey, obj)
	}
	sec, ok := obj[secKey].(float64)
	if !ok {
		t.Fatalf("%s: missing %s: %v", obj["path"], secKey, obj)
	}
	if int64(ms)/1000 != int64(sec) {
		t.Fatalf("%s: %s=%d and %s=%d disagree", obj["path"], secKey, int64(sec), msKey, int64(ms))
	}
	return int64(ms)
}

func TestTimestampPrecision(t *testing.T) {
	e := newEnv(t)
	u := e.srv.URL

	// events: two perm_sets milliseconds apart must be strictly ordered by
	// ts_ms — the pair of seconds columns cannot even see them apart —
	// and since= must be able to cut between them.
	must(t, e.admin, "POST", u+"/api/v1/users", `{"username":"tsp","password":"pw"}`, 201)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/ts-a","principal_type":"user","principal":"tsp","read":true}`, 200)
	time.Sleep(5 * time.Millisecond)
	cutoff := time.Now()
	time.Sleep(5 * time.Millisecond)
	must(t, e.admin, "PUT", u+"/api/v1/perms",
		`{"path":"/ts-b","principal_type":"user","principal":"tsp","read":true}`, 200)

	evs := func(qs string) []map[string]any {
		t.Helper()
		resp, err := e.admin.Get(u + "/api/v1/events?" + qs)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("GET events?%s: %d (%s)", qs, resp.StatusCode, raw)
		}
		var arr []map[string]any
		if err := json.Unmarshal(raw, &arr); err != nil {
			t.Fatalf("events?%s decode: %v", qs, err)
		}
		return arr
	}
	arr := evs("action=perm_set&limit=10")
	if len(arr) < 2 {
		t.Fatalf("expected 2 perm_set events, got %d", len(arr))
	}
	// default order is newest-first; the top is /ts-b.
	if arr[0]["path"] != "/ts-b" || arr[1]["path"] != "/ts-a" {
		t.Fatalf("same-second events not strictly ordered by ms: %v %v",
			arr[0]["path"], arr[1]["path"])
	}
	msNew, msOld := jsonInt(arr[0]["ts_ms"]), jsonInt(arr[1]["ts_ms"])
	if msNew <= msOld {
		t.Fatalf("ts_ms not strictly newer: %d vs %d", msNew, msOld)
	}
	if msNew-time.Now().UnixMilli() > 2000 {
		t.Fatalf("ts_ms is not a recent millisecond instant: %d", msNew)
	}
	for _, ev := range arr {
		consistentPair(t, ev, "ts", "ts_ms")
		// "time" gained fractional seconds too (same instant, RFC3339).
		tv, ok := ev["time"].(string)
		if !ok {
			t.Fatalf("event missing time: %v", ev)
		}
		parsed, err := time.Parse(time.RFC3339Nano, tv)
		if err != nil || parsed.UnixMilli() != jsonInt(ev["ts_ms"]) {
			t.Fatalf("event time %q does not match ts_ms %d", tv, jsonInt(ev["ts_ms"]))
		}
	}
	// since= with sub-second granularity cuts between the two events.
	// (UTC so the offset serialises as "Z": a "+" would URL-decode to a
	// space and break the parse client-side.)
	just := evs("action=perm_set&since=" + cutoff.UTC().Format(time.RFC3339Nano))
	if len(just) != 1 || just[0]["path"] != "/ts-b" {
		t.Fatalf("sub-second since= filter wrong: %d rows", len(just))
	}

	// drafts: the auto-save instant is exact; updated_at keeps its seconds
	// contract as the floored view.
	must(t, e.admin, "POST", u+"/api/v1/fs/mkdir", `{"path":"/tsd"}`, 201)
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/tsd/cfg.yml","content":"a: 1\n"}`, 200)
	must(t, e.admin, "PUT", u+"/api/v1/fs/draft", `{"path":"/tsd/cfg.yml","content":"a: 2\n"}`, 200)
	got := must(t, e.admin, "GET", u+"/api/v1/fs/draft?path=/tsd/cfg.yml", "", 200)
	draft, ok := got["draft"].(map[string]any)
	if !ok {
		t.Fatalf("draft missing after PUT: %v", got)
	}
	if consistentPair(t, draft, "updated_at", "updated_at_ms") < time.Now().UnixMilli()-2000 {
		t.Fatalf("draft updated_at_ms is not a recent millisecond instant: %v", draft["updated_at_ms"])
	}

	// file mtimes: the content response and the listing entry carry the
	// same exact instant as numbers, and base_mtime_ms guards at that
	// granularity.
	must(t, e.admin, "POST", u+"/api/v1/fs/write", `{"path":"/tsd/cfg.yml","content":"a: 3\n"}`, 200)
	content := must(t, e.admin, "GET", u+"/api/v1/fs/content?path=/tsd/cfg.yml", "", 200)
	msFile := consistentPair(t, content, "mtime", "mtime_ms")

	resp, err := e.admin.Get(u + "/api/v1/fs?path=/tsd")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /fs?path=/tsd: %d (%s)", resp.StatusCode, raw)
	}
	var listing struct {
		Entries []struct {
			Entry struct {
				Name      string `json:"name"`
				ModTimeMs int64  `json:"mod_time_ms"`
			} `json:"entry"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatalf("fs listing decode: %v", err)
	}
	var found bool
	for _, ent := range listing.Entries {
		if ent.Entry.Name == "cfg.yml" {
			if ent.Entry.ModTimeMs != msFile {
				t.Fatalf("listing mod_time_ms %d disagrees with content mtime_ms %d",
					ent.Entry.ModTimeMs, msFile)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("cfg.yml not in listing")
	}

	// the ms guard: exact echo passes, one millisecond stale conflicts —
	// a difference the seconds guard cannot express.
	must(t, e.admin, "POST", u+"/api/v1/fs/write",
		`{"path":"/tsd/cfg.yml","content":"a: 4\n","base_mtime_ms":`+strconv.FormatInt(msFile, 10)+`}`, 200)
	must(t, e.admin, "POST", u+"/api/v1/fs/write",
		`{"path":"/tsd/cfg.yml","content":"a: 5\n","base_mtime_ms":`+strconv.FormatInt(msFile, 10)+`}`, 409)
}

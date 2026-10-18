package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/llm-crud1/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	api := API{Store: store.NewMemoryStore(), Log: slog.Default()}
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// do performs a request and returns status plus decoded JSON body (nil if empty).
func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			// non-JSON body (e.g. mux's plain-text 405): expose as _raw
			out = map[string]any{"_raw": string(raw)}
		}
	}
	return resp.StatusCode, out
}

func errCode(t *testing.T, body map[string]any) string {
	t.Helper()
	e, _ := body["error"].(map[string]any)
	if e == nil {
		t.Fatalf("missing error envelope: %v", body)
	}
	code, _ := e["code"].(string)
	msg, _ := e["message"].(string)
	if code == "" || msg == "" {
		t.Fatalf("error envelope missing code/message: %v", e)
	}
	return code
}

func TestCRUDFlow(t *testing.T) {
	srv := newTestServer(t)

	st, body := do(t, srv, "POST", "/api/v1/llms", `{"name":"alpha","provider":"openai","model_id":"gpt-4o","context_length":128,"enabled":true,"tags":["x"]}`)
	if st != http.StatusCreated {
		t.Fatalf("create: got %d %v", st, body)
	}
	id, _ := body["id"].(string)
	if id == "" || body["name"] != "alpha" {
		t.Fatalf("create response: %v", body)
	}

	if st, body = do(t, srv, "GET", "/api/v1/llms/"+id, ""); st != http.StatusOK || body["id"] != id {
		t.Fatalf("get: %d %v", st, body)
	}
	if st, body = do(t, srv, "PATCH", "/api/v1/llms/"+id, `{"enabled":false}`); st != http.StatusOK || body["enabled"] != false {
		t.Fatalf("patch: %d %v", st, body)
	}
	if st, body = do(t, srv, "PUT", "/api/v1/llms/"+id, `{"name":"alpha2","provider":"ollama","model_id":"llama3"}`); st != http.StatusOK || body["name"] != "alpha2" || body["provider"] != "ollama" {
		t.Fatalf("put: %d %v", st, body)
	}
	if st, _ = do(t, srv, "DELETE", "/api/v1/llms/"+id, ""); st != http.StatusNoContent {
		t.Fatalf("delete: %d", st)
	}
	if st, body = do(t, srv, "GET", "/api/v1/llms/"+id, ""); st != http.StatusNotFound || errCode(t, body) != "not_found" {
		t.Fatalf("get deleted: %d %v", st, body)
	}
}

func TestErrorPaths(t *testing.T) {
	srv := newTestServer(t)
	do(t, srv, "POST", "/api/v1/llms", `{"name":"dup","provider":"p","model_id":"m"}`)

	cases := []struct {
		method, path, body string
		want               int
		code               string
	}{
		{"POST", "/api/v1/llms", `{"provider":"p","model_id":"m"}`, 422, "validation"},
		{"POST", "/api/v1/llms", `{"name":"dup","provider":"p","model_id":"m"}`, 409, "duplicate"},
		{"POST", "/api/v1/llms", ``, 400, "bad_request"},
		{"POST", "/api/v1/llms", `{bad`, 400, "bad_request"},
		{"POST", "/api/v1/llms", `{"name":"x","provider":"p","model_id":"m","nope":1}`, 400, "bad_request"},
		{"GET", "/api/v1/llms/missing", "", 404, "not_found"},
		{"PUT", "/api/v1/llms/missing", `{"name":"a","provider":"p","model_id":"m"}`, 404, "not_found"},
		{"PATCH", "/api/v1/llms/missing", `{"name":"a"}`, 404, "not_found"},
		{"DELETE", "/api/v1/llms/missing", "", 404, "not_found"},
		{"POST", "/api/v1/llms/any", `{}`, 405, ""},
	}
	for _, c := range cases {
		st, body := do(t, srv, c.method, c.path, c.body)
		if st != c.want {
			t.Errorf("%s %s %q: got %d want %d (%v)", c.method, c.path, c.body, st, c.want, body)
			continue
		}
		if c.code != "" && errCode(t, body) != c.code {
			t.Errorf("%s %s: wrong error code: %v", c.method, c.path, body)
		}
	}
}

func TestListFiltersAndHealth(t *testing.T) {
	srv := newTestServer(t)
	seeds := []string{
		`{"name":"one","provider":"openai","model_id":"gpt-4o","enabled":true}`,
		`{"name":"two","provider":"ollama","model_id":"llama3:8b","enabled":false}`,
		`{"name":"three","provider":"anthropic","model_id":"claude","enabled":true}`,
	}
	for _, s := range seeds {
		if st, body := do(t, srv, "POST", "/api/v1/llms", s); st != http.StatusCreated {
			t.Fatalf("seed %s: %d %v", s, st, body)
		}
	}

	checks := []struct {
		path string
		want float64
	}{
		{"/api/v1/llms", 3},
		{"/api/v1/llms?provider=OLLAMA", 1},
		{"/api/v1/llms?enabled=true", 2},
		{"/api/v1/llms?q=claude", 1},
		{"/api/v1/llms?q=nothing", 0},
	}
	for _, c := range checks {
		st, body := do(t, srv, "GET", c.path, "")
		if st != http.StatusOK || body["total"] != c.want {
			t.Errorf("GET %s: got %d total=%v want %v", c.path, st, body["total"], c.want)
		}
	}

	st, body := do(t, srv, "GET", "/healthz", "")
	if st != http.StatusOK || body["status"] != "ok" || body["count"] != float64(3) {
		t.Errorf("healthz: %d %v", st, body)
	}
}

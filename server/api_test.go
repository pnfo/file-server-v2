package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPITokenAuth(t *testing.T) {
	good := strings.Repeat("a", 40)
	old := getSecrets
	getSecrets = func() *Secrets {
		return &Secrets{APITokens: map[string]string{"claude": good, "short": "tooshort"}}
	}
	t.Cleanup(func() { getSecrets = old })
	s := testStore(t)

	call := func(method, path, auth, ip, payload string) (int, map[string]any) {
		r := httptest.NewRequest(method, "/library/admin/"+path, strings.NewReader(payload))
		r.Header.Set("X-Real-IP", ip)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		p, _, _ := strings.Cut(path, "?")
		segs := strings.Split(p, "/")
		serveAdmin(w, r, &SiteCtx{ID: "library", Base: "/library", Store: s}, segs)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	if code, out := call("GET", "api/list?folder=0", "Bearer "+good, "1.1.1.1", ""); code != 200 || out["children"] == nil {
		t.Errorf("list with token: %d %v", code, out)
	}
	// unpublished entries are visible to the API (4 is an unpublished folder in testStore)
	if code, out := call("GET", "api/entry?id=4", "Bearer "+good, "1.1.1.1", ""); code != 200 || out["entry"] == nil {
		t.Errorf("entry 4: %d %v", code, out)
	}
	// an unknown id is a 400
	if code, _ := call("POST", "api/replace-commit", "Bearer "+good, "1.1.1.1", `{"id":999}`); code != 400 {
		t.Errorf("token POST: %d", code)
	}
	// short tokens never match, even when configured; wrong or malformed tokens are 401
	for _, auth := range []string{"Bearer tooshort", "Bearer " + strings.Repeat("b", 40), "Basic " + good, "Bearer"} {
		if code, _ := call("GET", "api/list", auth, "2.2.2.2", ""); code != 401 {
			t.Errorf("%q: %d, want 401", auth, code)
		}
	}
	// ...and count toward the rate limit, which then blocks even a good token from that IP
	call("GET", "api/list", "Bearer nope", "2.2.2.2", "")
	if code, _ := call("GET", "api/list", "Bearer "+good, "2.2.2.2", ""); code != 429 {
		t.Errorf("rate limit: %d", code)
	}
	// the browser admin is gone: only /admin/api/* exists
	if code, _ := call("GET", "upload", "Bearer "+good, "1.1.1.1", ""); code != 404 {
		t.Errorf("old admin page: %d", code)
	}
	// without a token the API stays closed
	if code, _ := call("GET", "api/list", "", "3.3.3.3", ""); code != 401 {
		t.Errorf("no auth: %d", code)
	}
}

func TestAPISearchIncludesUnpublished(t *testing.T) {
	s := testStore(t)
	found := 0
	for _, e := range s.APISearch(s.GetAny(5).Name, 100) {
		if e.ID == 5 {
			found++
		}
	}
	if found != 1 {
		t.Errorf("entry 5 (inside an unpublished folder) not found by APISearch")
	}
}

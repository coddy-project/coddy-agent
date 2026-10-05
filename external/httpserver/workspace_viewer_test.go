//go:build http

package httpserver

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func workspaceRequest(e *changesEnv, method, route string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/coddy/sessions/"+e.id+"/workspace/"+route, bytes.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(w, r)
	return w
}

func TestWorkspaceViewerCORSAndOpenAPI(t *testing.T) {
	e := newChangesEnv(t)
	e.srv.activeCfg().HTTPServer.CORS = config.HTTPCORSConfig{Enabled: true, AllowedOrigins: []string{"https://ui.example"}}
	w := workspaceRequest(e, "OPTIONS", "raw?path_rel=a.txt", nil, map[string]string{"Origin": "https://ui.example", "Access-Control-Request-Method": "HEAD", "Access-Control-Request-Headers": "Range, If-None-Match"})
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "https://ui.example" {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
	for header, want := range map[string]string{"Access-Control-Allow-Methods": "HEAD", "Access-Control-Allow-Headers": "Range", "Access-Control-Expose-Headers": "ETag"} {
		if !strings.Contains(w.Header().Get(header), want) {
			t.Fatalf("%s: %v", header, w.Header())
		}
	}
	paths := openAPISpec()["paths"].(map[string]interface{})
	for _, route := range []string{"tree", "raw", "text", "media-token"} {
		if _, ok := paths["/coddy/sessions/{id}/workspace/"+route]; !ok {
			t.Fatalf("missing OpenAPI route %s", route)
		}
	}
}

func TestWorkspaceViewerETagChangesWithWorkspace(t *testing.T) {
	e := newChangesEnv(t)
	other := t.TempDir()
	writeInWorkspace(t, e.cwd, "a.txt", "old")
	writeInWorkspace(t, other, "a.txt", "new")
	stamp := time.Now().Add(-time.Minute)
	for _, dir := range []string{e.cwd, other} {
		if err := os.Chtimes(filepath.Join(dir, "a.txt"), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	w := workspaceRequest(e, "GET", "raw?path_rel=a.txt", nil, nil)
	etag := w.Header().Get("ETag")
	e.srv.mgr.SessionByID(e.id).SetCWD(other)
	w = workspaceRequest(e, "GET", "raw?path_rel=a.txt", nil, map[string]string{"If-None-Match": etag})
	if w.Code != 200 || w.Body.String() != "new" || w.Header().Get("ETag") == etag {
		t.Fatalf("old workspace cache survived: %d %s %v", w.Code, w.Body.String(), w.Header())
	}
}

func TestWorkspaceViewerCapabilityExpiryAndWorkspaceBinding(t *testing.T) {
	e := newChangesEnv(t)
	writeInWorkspace(t, e.cwd, "a.txt", "original")
	e.srv.SetExtraAuthTokens([]string{"test-secret"})
	claim := workspaceCapability{Version: 1, Session: e.id, Path: "a.txt", Workspace: workspaceIdentity(e.cwd)}
	sign := func(c workspaceCapability) string {
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		payload := base64.RawURLEncoding.EncodeToString(data)
		mac := hmac.New(sha256.New, e.srv.workspaceSigningKey())
		_, _ = mac.Write([]byte(payload))
		return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	for _, exp := range []int64{time.Now().Add(-time.Second).Unix(), time.Now().Add(2 * time.Hour).Unix()} {
		claim.Expires = exp
		w := workspaceRequest(e, "GET", "raw?path_rel=a.txt&access_token="+sign(claim), nil, nil)
		if w.Code != 401 {
			t.Fatalf("expiry %d accepted: %d", exp, w.Code)
		}
	}
	claim.Expires = time.Now().Add(time.Minute).Unix()
	claim.Session = "another-session"
	w := workspaceRequest(e, "GET", "raw?path_rel=a.txt&access_token="+sign(claim), nil, nil)
	if w.Code != 401 {
		t.Fatalf("another session accepted: %d", w.Code)
	}
	claim.Session = e.id
	token := sign(claim)
	e.srv.mgr.SessionByID(e.id).SetCWD(t.TempDir())
	w = workspaceRequest(e, "GET", "raw?path_rel=a.txt&access_token="+token, nil, nil)
	if w.Code != 401 {
		t.Fatalf("changed workspace accepted: %d", w.Code)
	}
}

func TestWorkspaceViewerRawHeadersSniffAndRanges(t *testing.T) {
	e := newChangesEnv(t)
	body := strings.Repeat("text contents ", 200)
	writeInWorkspace(t, e.cwd, "fake.png", body)
	w := workspaceRequest(e, "GET", "raw?path_rel=fake.png", nil, nil)
	if w.Code != 200 || w.Body.String() != body || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("sniff/reset: %d %v, bytes=%d", w.Code, w.Header(), w.Body.Len())
	}
	for name, want := range map[string]string{"Cache-Control": "private, no-cache", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "Accept-Ranges": "bytes"} {
		if w.Header().Get(name) != want {
			t.Fatalf("%s = %q", name, w.Header().Get(name))
		}
	}
	etag := w.Header().Get("ETag")
	w = workspaceRequest(e, "HEAD", "raw?path_rel=fake.png", nil, nil)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("ETag") != etag {
		t.Fatalf("HEAD: %d %v", w.Code, w.Header())
	}
	w = workspaceRequest(e, "GET", "raw?path_rel=fake.png", nil, map[string]string{"If-None-Match": etag})
	if w.Code != 304 {
		t.Fatalf("raw revalidation: %d", w.Code)
	}
	w = workspaceRequest(e, "GET", "raw?path_rel=fake.png", nil, map[string]string{"Range": "bytes=999999-"})
	if w.Code != 416 {
		t.Fatalf("invalid range: %d", w.Code)
	}
	w = workspaceRequest(e, "GET", "tree?include_hidden=1", nil, nil)
	if w.Code != 200 {
		t.Fatalf("explicit hidden navigation: %d", w.Code)
	}
}

func TestWorkspaceViewerTreeAndLongText(t *testing.T) {
	e := newChangesEnv(t)
	writeInWorkspace(t, e.cwd, "a.go", strings.Repeat("hello world\n", 100000))
	writeInWorkspace(t, e.cwd, "b.md", "# title\n")
	writeInWorkspace(t, e.cwd, ".hidden", "secret")
	w := workspaceRequest(e, "GET", "tree?limit=1", nil, nil)
	if w.Code != 200 {
		t.Fatalf("tree: %d %s", w.Code, w.Body.String())
	}
	var tree struct {
		Entries []struct {
			Name string `json:"name"`
		}
		Cursor  string `json:"next_cursor"`
		HasMore bool   `json:"has_more"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tree); err != nil {
		t.Fatal(err)
	}
	if len(tree.Entries) != 1 || tree.Entries[0].Name != "a.go" || !tree.HasMore || tree.Cursor != "a.go" {
		t.Fatalf("page: %+v", tree)
	}
	w = workspaceRequest(e, "GET", "text?path_rel=a.go&offset=99998&max_lines=1", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"next_offset":99999`) || !strings.Contains(w.Body.String(), `"has_more":true`) {
		t.Fatalf("long text: %d %s", w.Code, w.Body.String())
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	w = workspaceRequest(e, "GET", "text?path_rel=a.go", nil, map[string]string{"If-None-Match": etag})
	if w.Code != 304 {
		t.Fatalf("revalidation = %d", w.Code)
	}
	writeInWorkspace(t, e.cwd, "a.go", "changed\n")
	w = workspaceRequest(e, "GET", "text?path_rel=a.go&etag="+url.QueryEscape(etag), nil, nil)
	if w.Code != 409 {
		t.Fatalf("changed paging = %d", w.Code)
	}
}

func TestWorkspaceViewerCapabilitiesAndRawBoundary(t *testing.T) {
	e := newChangesEnv(t)
	writeInWorkspace(t, e.cwd, "a.txt", "hello world")
	writeInWorkspace(t, e.cwd, "evil.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	e.srv.SetExtraAuthTokens([]string{"test-secret"})
	w := workspaceRequest(e, "POST", "media-token", []byte(`{"path_rel":"a.txt"}`), map[string]string{"Authorization": "Bearer test-secret"})
	if w.Code != 200 {
		t.Fatalf("mint: %d %s", w.Code, w.Body.String())
	}
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	raw := "raw?path_rel=a.txt&access_token=" + url.QueryEscape(token.Token)
	w = workspaceRequest(e, "GET", raw, nil, map[string]string{"Range": "bytes=0-4"})
	if w.Code != 206 || w.Body.String() != "hello" {
		t.Fatalf("capability Range: %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []string{raw + "&download=1", strings.Replace(raw, "a.txt", "evil.svg", 1), raw + "x"} {
		w = workspaceRequest(e, "GET", bad, nil, nil)
		if w.Code != 401 {
			t.Fatalf("capability accepted mismatch: %s => %d", bad, w.Code)
		}
	}
	w = workspaceRequest(e, "GET", "raw?path_rel=evil.svg", nil, map[string]string{"Authorization": "Bearer test-secret"})
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("active content inline: %d %v", w.Code, w.Header())
	}
	w = workspaceRequest(e, "GET", "raw?path_rel=../outside", nil, map[string]string{"Authorization": "Bearer test-secret"})
	if w.Code != 400 {
		t.Fatalf("traversal: %d", w.Code)
	}
}

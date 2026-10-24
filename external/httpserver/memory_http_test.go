//go:build http && memory

package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestMemoryTreeRejectsTraversal(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	ctx := context.Background()
	nr, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := nr.SessionID
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	u := ts.URL + "/coddy/sessions/" + sid + "/memory/tree?root=global&path=" + url.QueryEscape("../etc/passwd")
	r, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 got %d: %s", r.StatusCode, b)
	}
}

func TestMemoryFileDeleteRejectsMemoryRoot(t *testing.T) {
	mgr, srv, _ := testHTTPServerPersist(t)
	ctx := context.Background()
	nr, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := srv.memoryStore(mgr.SessionByID(nr.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store.GlobalRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodDelete,
		ts.URL+"/coddy/sessions/"+nr.SessionID+"/memory/file?root=global&path=", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 got %d: %s", res.StatusCode, b)
	}
	if _, err := os.Stat(store.GlobalRoot()); err != nil {
		t.Fatalf("memory root was removed: %v", err)
	}
}

// memoryLinkStand is a session whose folder holds .coddy/memory as a clone
// could bring it, and a home with a secret and another project beside it.
type memoryLinkStand struct {
	ts                      *httptest.Server
	sid, project            string
	secret, other, otherDoc string
}

func newMemoryLinkStand(t *testing.T) memoryLinkStand {
	t.Helper()
	mgr, srv, _ := testHTTPServerPersist(t)
	workspace, home := t.TempDir(), t.TempDir()
	s := memoryLinkStand{project: filepath.Join(workspace, ".coddy", "memory")}
	if err := os.MkdirAll(s.project, 0o755); err != nil {
		t.Fatal(err)
	}
	s.secret = filepath.Join(home, "credentials")
	if err := os.WriteFile(s.secret, []byte("OUTSIDE-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.other = filepath.Join(home, "other-project")
	s.otherDoc = filepath.Join(s.other, "docs", "keep.md")
	if err := os.MkdirAll(filepath.Dir(s.otherDoc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.otherDoc, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	nr, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: workspace})
	if err != nil {
		t.Fatal(err)
	}
	s.sid = nr.SessionID
	s.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(s.ts.Close)
	return s
}

func (s memoryLinkStand) call(t *testing.T, method, route string, q url.Values, body string) (int, string) {
	t.Helper()
	u := s.ts.URL + "/coddy/sessions/" + s.sid + "/memory/" + route
	if q != nil {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(method, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(b)
}

func symlinkOrSkipT(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// The notes routes reach the workspace notes the way the memory tools do: a
// note or a folder that is a link out of .coddy/memory leads nowhere, and
// deleting such a link removes the link only.
func TestMemoryRoutesKeepToTheWorkspaceNotes(t *testing.T) {
	s := newMemoryLinkStand(t)
	symlinkOrSkipT(t, s.secret, filepath.Join(s.project, "setup.md"))
	symlinkOrSkipT(t, s.other, filepath.Join(s.project, "shared"))
	ws := func(p string) url.Values { return url.Values{"root": {"workspace"}, "path": {p}} }

	refused := func(what string, code int, body string) {
		t.Helper()
		if code != http.StatusBadRequest || !strings.Contains(body, "leads out of") || strings.Contains(body, "OUTSIDE-SECRET") {
			t.Fatalf("%s = %d %s; want 400 saying the path leads out through a link", what, code, body)
		}
	}
	code, body := s.call(t, http.MethodGet, "file", ws("setup.md"), "")
	refused("GET a note linked out of the workspace notes", code, body)
	code, body = s.call(t, http.MethodGet, "file", ws("shared/docs/keep.md"), "")
	refused("GET a note behind a linked folder", code, body)
	code, body = s.call(t, http.MethodGet, "tree", ws("shared/docs"), "")
	refused("GET the tree of a linked folder", code, body)
	code, body = s.call(t, http.MethodPut, "file", nil, `{"root":"workspace","path":"shared/AGENTS.md","content":"planted"}`)
	refused("PUT a note behind a linked folder", code, body)
	code, body = s.call(t, http.MethodPost, "dir", nil, `{"root":"workspace","path":"shared/new"}`)
	refused("POST a folder behind a linked folder", code, body)
	if _, err := os.Stat(filepath.Join(s.other, "AGENTS.md")); err == nil {
		t.Fatal("PUT wrote a file outside the workspace notes")
	}
	code, body = s.call(t, http.MethodDelete, "file", ws("shared/docs"), "")
	refused("DELETE a folder behind a linked folder", code, body)
	if _, err := os.Stat(s.otherDoc); err != nil {
		t.Fatalf("DELETE removed a file outside the workspace notes: %v", err)
	}
	if code, body = s.call(t, http.MethodDelete, "file", ws("shared"), ""); code != http.StatusOK {
		t.Fatalf("deleting the link itself: %d %s", code, body)
	}
	if _, err := os.Lstat(filepath.Join(s.project, "shared")); !os.IsNotExist(err) {
		t.Fatalf("the link is still there: %v", err)
	}
	if _, err := os.Stat(s.otherDoc); err != nil {
		t.Fatalf("deleting the link removed what it pointed at: %v", err)
	}
}

// A workspace whose .coddy/memory is itself a link has no notes the routes
// will read or write: the answer says why instead of serving the folder
// behind it.
func TestMemoryRoutesRefuseALinkedWorkspaceRoot(t *testing.T) {
	s := newMemoryLinkStand(t)
	if err := os.RemoveAll(s.project); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkipT(t, filepath.Dir(s.otherDoc), s.project)
	ws := func(p string) url.Values { return url.Values{"root": {"workspace"}, "path": {p}} }

	for _, c := range []struct {
		method, route string
		q             url.Values
		body          string
	}{
		{http.MethodGet, "tree", ws(""), ""},
		{http.MethodGet, "file", ws("keep.md"), ""},
		{http.MethodPut, "file", nil, `{"root":"workspace","path":"planted.md","content":"x"}`},
		{http.MethodDelete, "file", ws("keep.md"), ""},
	} {
		code, body := s.call(t, c.method, c.route, c.q, c.body)
		if code != http.StatusBadRequest || !strings.Contains(body, "must be real folders") {
			t.Fatalf("%s %s on a linked workspace root = %d %s; want 400 naming the link", c.method, c.route, code, body)
		}
	}
	if _, err := os.Stat(s.otherDoc); err != nil {
		t.Fatalf("something behind the linked root was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.otherDoc), "planted.md")); err == nil {
		t.Fatal("PUT wrote behind the linked root")
	}
}

// A workspace whose .coddy/memory is a file holds no notes: the routes say it
// is not a folder, as a request the client got wrong, not as a failure.
func TestMemoryRoutesRefuseAWorkspaceRootThatIsAFile(t *testing.T) {
	s := newMemoryLinkStand(t)
	if err := os.RemoveAll(s.project); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.project, []byte("not a folder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := func(p string) url.Values { return url.Values{"root": {"workspace"}, "path": {p}} }
	for _, c := range []struct {
		method, route string
		q             url.Values
		body          string
	}{
		{http.MethodGet, "tree", ws(""), ""},
		{http.MethodPut, "file", nil, `{"root":"workspace","path":"planted.md","content":"x"}`},
	} {
		code, body := s.call(t, c.method, c.route, c.q, c.body)
		if code != http.StatusBadRequest || !strings.Contains(body, "is not a folder") {
			t.Fatalf("%s %s on a workspace root that is a file = %d %s; want 400 saying so", c.method, c.route, code, body)
		}
	}
}

//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type workspaceFileBody struct {
	Object     string   `json:"object"`
	PathRel    string   `json:"path_rel"`
	Lines      []string `json:"lines"`
	TotalLines int      `json:"total_lines"`
	Truncated  bool     `json:"truncated"`
}

// newWorkspaceFileTestServer boots a server whose cwd is a temp workspace holding
// the given files, and returns it plus the workspace root.
func newWorkspaceFileTestServer(t *testing.T, files map[string]string) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	wd := filepath.Join(root, "wd")
	for _, d := range []string{home, wd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		p := filepath.Join(wd, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: wd},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), wd, nil)
	srv := New(cfg, mgr, slog.Default(), wd)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, wd
}

func getWorkspaceFile(t *testing.T, ts *httptest.Server, query string) (int, workspaceFileBody) {
	t.Helper()
	rsp, err := http.Get(ts.URL + "/coddy/workspace/file?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rsp.Body.Close() }()
	var body workspaceFileBody
	if rsp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(rsp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
	}
	return rsp.StatusCode, body
}

func TestCoddyWorkspaceFileGetSplitsLines(t *testing.T) {
	ts, _ := newWorkspaceFileTestServer(t, map[string]string{
		"pkg/app.go": "package main\n\nfunc main() {}\n",
	})
	status, body := getWorkspaceFile(t, ts, "path_rel="+url.QueryEscape("pkg/app.go"))
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if body.Object != "coddy.workspace_file" || body.PathRel != "pkg/app.go" {
		t.Fatalf("body %+v", body)
	}
	// A trailing newline does not add an empty last line.
	if body.TotalLines != 3 || body.Truncated {
		t.Fatalf("body %+v", body)
	}
	if len(body.Lines) != 3 || body.Lines[0] != "package main" || body.Lines[1] != "" || body.Lines[2] != "func main() {}" {
		t.Fatalf("lines %q", body.Lines)
	}
}

// CRLF files lose the stray "\r" so the picker shows what the editor shows.
func TestCoddyWorkspaceFileGetStripsCR(t *testing.T) {
	ts, _ := newWorkspaceFileTestServer(t, map[string]string{"win.txt": "a\r\nb\r\n"})
	status, body := getWorkspaceFile(t, ts, "path_rel=win.txt")
	if status != http.StatusOK || len(body.Lines) != 2 || body.Lines[0] != "a" || body.Lines[1] != "b" {
		t.Fatalf("status=%d lines=%q", status, body.Lines)
	}
}

func TestCoddyWorkspaceFileGetMaxLines(t *testing.T) {
	ts, _ := newWorkspaceFileTestServer(t, map[string]string{"long.txt": "1\n2\n3\n4\n5\n"})
	status, body := getWorkspaceFile(t, ts, "path_rel=long.txt&max_lines=2")
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if len(body.Lines) != 2 || body.TotalLines != 5 || !body.Truncated {
		t.Fatalf("body %+v", body)
	}
}

func TestCoddyWorkspaceFileGetRejects(t *testing.T) {
	ts, _ := newWorkspaceFileTestServer(t, map[string]string{
		"ok.txt":     "x\n",
		"dir/in.txt": "y\n",
	})
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"missing path_rel", "", http.StatusBadRequest},
		{"max_lines below range", "path_rel=ok.txt&max_lines=0", http.StatusBadRequest},
		{"max_lines above range", "path_rel=ok.txt&max_lines=5001", http.StatusBadRequest},
		{"max_lines not a number", "path_rel=ok.txt&max_lines=abc", http.StatusBadRequest},
		{"directory", "path_rel=dir", http.StatusBadRequest},
		{"traversal", "path_rel=" + url.QueryEscape("../outside.txt"), http.StatusBadRequest},
		{"missing file", "path_rel=nope.txt", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, _ := getWorkspaceFile(t, ts, c.query)
			if status != c.want {
				t.Fatalf("status %d, want %d", status, c.want)
			}
		})
	}
}

// An I/O failure that is not the caller's doing (here: a file the server
// cannot open) is a 500, matching the served OpenAPI, not a 400.
func TestCoddyWorkspaceFileGetUnreadableIs500(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("relies on POSIX permission bits blocking the read")
	}
	ts, wd := newWorkspaceFileTestServer(t, map[string]string{"locked.txt": "x\n"})
	if err := os.Chmod(filepath.Join(wd, "locked.txt"), 0o000); err != nil {
		t.Fatal(err)
	}
	status, _ := getWorkspaceFile(t, ts, "path_rel=locked.txt")
	if status != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", status)
	}
}

type mentionsBody struct {
	Object string `json:"object"`
	Items  []struct {
		Kind     string `json:"kind"`
		Insert   string `json:"insert"`
		Label    string `json:"label"`
		Continue bool   `json:"continue"`
	} `json:"items"`
	Total int `json:"total"`
}

func getMentions(t *testing.T, ts *httptest.Server, query url.Values) (int, mentionsBody) {
	t.Helper()
	rsp, err := http.Get(ts.URL + "/coddy/mentions?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rsp.Body.Close() }()
	var body mentionsBody
	if rsp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(rsp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
	}
	return rsp.StatusCode, body
}

// GET /coddy/mentions ranks the workspace against the text after "@",
// browses a folder typed by its absolute path, and offers the scheme hints
// for an empty query.
func TestCoddyMentionsGet(t *testing.T) {
	ts, _ := newWorkspaceFileTestServer(t, map[string]string{
		"internal/agent/react.go":      "x",
		"internal/agent/react_test.go": "x",
		"docs/react-notes.md":          "x",
		"my notes.md":                  "x",
	})

	code, body := getMentions(t, ts, url.Values{"q": {"react.go"}})
	if code != http.StatusOK || body.Object != "coddy.mentions" || len(body.Items) == 0 || body.Items[0].Insert != "@internal/agent/react.go" {
		t.Fatalf("ranked search: %d %+v", code, body)
	}
	code, body = getMentions(t, ts, url.Values{"q": {"my no"}})
	if code != http.StatusOK || len(body.Items) == 0 || body.Items[0].Insert != `@"my notes.md"` {
		t.Fatalf("a path with a space is inserted quoted: %d %+v", code, body)
	}
	code, body = getMentions(t, ts, url.Values{"q": {""}})
	if code != http.StatusOK || len(body.Items) < 3 || body.Items[0].Kind != "scheme" || !body.Items[0].Continue {
		t.Fatalf("empty query offers the scheme hints: %d %+v", code, body)
	}
	if runtime.GOOS != "windows" {
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "far.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, body = getMentions(t, ts, url.Values{"q": {outside + "/f"}})
		if code != http.StatusOK || len(body.Items) != 1 || body.Items[0].Insert != "@"+outside+"/far.txt" {
			t.Fatalf("absolute browse: %d %+v", code, body)
		}
	}
	if code, _ := getMentions(t, ts, url.Values{"q": {"x"}, "limit": {"0"}}); code != http.StatusBadRequest {
		t.Fatalf("limit 0: status %d, want 400", code)
	}
}

// POST /coddy/mentions/check tells the composer which mentions of a draft
// sending would attach: a package name stays unmarked, a file is marked.
func TestCoddyMentionsCheckPost(t *testing.T) {
	ts, _ := newWorkspaceFileTestServer(t, map[string]string{"README.md": "x"})
	post := func(body string) (int, string) {
		t.Helper()
		rsp, err := http.Post(ts.URL+"/coddy/mentions/check", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rsp.Body.Close() }()
		b, _ := io.ReadAll(rsp.Body)
		return rsp.StatusCode, string(b)
	}
	code, body := post(`{"text":"npm install @google/genai and read @README.md"}`)
	var got struct {
		Object   string `json:"object"`
		Mentions []struct {
			Token string `json:"token"`
			Typed string `json:"typed"`
			Kind  string `json:"kind"`
		} `json:"mentions"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &got) != nil {
		t.Fatalf("check: %d %s", code, body)
	}
	if got.Object != "coddy.mention_check" || len(got.Mentions) != 2 ||
		got.Mentions[0].Token != "@google/genai" || got.Mentions[0].Typed != "" ||
		got.Mentions[1].Typed != "@README.md" || got.Mentions[1].Kind != "file" {
		t.Fatalf("check: %s", body)
	}
	if code, body := post(`{"text":"no mentions here"}`); code != http.StatusOK || !strings.Contains(body, `"mentions":[]`) {
		t.Fatalf("a draft without mentions: %d %s", code, body)
	}
	if code, _ := post(`{"text":`); code != http.StatusBadRequest {
		t.Fatalf("broken JSON: status %d, want 400", code)
	}
	huge, _ := json.Marshal(map[string]string{"text": strings.Repeat("a", session.MaxMentionCheckBytes+1)})
	if code, _ := post(string(huge)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized draft: status %d, want 413", code)
	}
}

//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

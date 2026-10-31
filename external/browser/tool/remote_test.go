//go:build browser

package browsertool

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/browser"
	"github.com/EvilFreelancer/coddy-agent/external/browser/browsertest"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// home starts a home server (the endpoint and the API on one mux, as the
// HTTP server mounts them) with one browser sharing one tab, and answers the
// configuration of a process that names it in browser.remotes.
func home(t *testing.T, id string, serving bool) (*browsertest.Extension, *config.Config, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/browser/", browser.NewServer(browser.Default(), func() string { return testKey }, nil))
	(&browser.API{Hub: browser.Default(), Allow: func() bool { return serving }}).Register(mux)
	srv := httptest.NewServer(mux)
	serverURL = srv.URL
	ext, err := browsertest.Connect(srv.URL, browsertest.Options{Key: testKey, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ext.Close()
		srv.Close()
		_ = waitFor(func() bool { return !browser.Default().Connected(id) })
	})
	if err := ext.Share(browsertest.Tab{ID: 1, URL: "https://example.com/", Title: "Example"}); err != nil {
		t.Fatal(err)
	}
	// The tab and the grant it was shared under reach the hub in one message.
	if err := waitFor(func() bool {
		for _, b := range browser.Default().List("") {
			if b.ID == id && len(b.Tabs) == 1 {
				return true
			}
		}
		return false
	}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Browser: config.BrowserConfig{Enable: true, Remotes: []string{srv.URL}},
		Swarm:   config.SwarmConfig{Join: []config.SwarmJoin{{Name: "laptop"}}},
	}
	return ext, cfg, id + "@" + strings.TrimPrefix(srv.URL, "http://")
}

func runWith(t *testing.T, cfg *config.Config, env *tooling.Env, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return execute(t.Context(), cfg, string(raw), env)
}

func fetchTransfer(ticket string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, serverURL+"/browser/v1/files/"+ticket, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+testKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	return io.ReadAll(res.Body)
}

// A file goes up from the workspace of a chat of another process and comes
// back down into it, its bytes riding the call to the home server.
func TestFilesMoveBetweenAnotherProcessAndTheHomeServersBrowser(t *testing.T) {
	ext, cfg, name := home(t, "browser-remote-file1", true)
	env := chat(t)
	if err := os.WriteFile(filepath.Join(env.CWD, "cv.pdf"), []byte("%PDF-1.7 cv"), 0o600); err != nil {
		t.Fatal(err)
	}
	var uploaded []byte
	ext.Handle = func(c browsertest.Command) browsertest.Answer {
		var p struct {
			TransferID string `json:"transfer_id"`
			Filename   string `json:"filename"`
		}
		_ = json.Unmarshal(c.Params, &p)
		switch c.Action {
		case "upload_file":
			got, err := fetchTransfer(p.TransferID)
			if err != nil {
				return browsertest.Answer{Error: err.Error()}
			}
			uploaded = got
			return browsertest.Answer{Result: map[string]string{"file": p.Filename}}
		case "download_file":
			if err := postTransfer(p.TransferID, []byte("report bytes")); err != nil {
				return browsertest.Answer{Error: err.Error()}
			}
			return browsertest.Answer{Result: map[string]int{"bytes": 12}}
		}
		return browsertest.Answer{Result: map[string]bool{"ok": true}}
	}
	if _, err := runWith(t, cfg, env, map[string]any{"action": "upload_file", "browser_id": name, "tab_id": 1, "path": "cv.pdf", "params": map[string]any{"ref": "r1:2"}}); err != nil {
		t.Fatal(err)
	}
	if string(uploaded) != "%PDF-1.7 cv" {
		t.Fatalf("the page got %q", uploaded)
	}
	out, err := runWith(t, cfg, env, map[string]any{"action": "download_file", "browser_id": name, "tab_id": 1, "path": "report.txt", "params": map[string]any{"url": "https://example.com/r"}})
	if err != nil || !strings.Contains(out, "12 bytes") {
		t.Fatalf("%s %v", out, err)
	}
	if got, _ := os.ReadFile(filepath.Join(env.CWD, "report.txt")); string(got) != "report bytes" {
		t.Fatalf("saved %q", got)
	}
}

// A server that does not serve its browsers is reported by name, and the
// chat is told what it needs.
func TestAServerThatKeepsItsBrowsersIsReported(t *testing.T) {
	_, cfg, _ := home(t, "browser-remote-off01", false)
	out, err := runWith(t, cfg, chat(t), map[string]any{"action": "list"})
	if err != nil || !strings.Contains(out, "unreachable") || !strings.Contains(out, "browser.serve_remote") {
		t.Fatalf("%s %v", out, err)
	}
	if _, err := runWith(t, cfg, chat(t), map[string]any{"action": "snapshot", "tab_id": 1}); err == nil || !strings.Contains(err.Error(), "serve_remote") {
		t.Fatalf("an action with no reachable browser: %v", err)
	}
}

// A browser of another server is selected by its full name and remembered.
func TestSelectingABrowserOfAnotherServer(t *testing.T) {
	_, cfg, name := home(t, "browser-remote-sel01", true)
	env := chat(t)
	if _, err := runWith(t, cfg, env, map[string]any{"action": "select", "browser_id": "browser-remote-sel01"}); err == nil {
		t.Fatal("selected the bare id of a browser of another server")
	}
	if _, err := runWith(t, cfg, env, map[string]any{"action": "select", "browser_id": name}); err != nil {
		t.Fatal(err)
	}
	out, err := runWith(t, cfg, env, map[string]any{"action": "list"})
	if err != nil || !strings.Contains(out, `"selected":"`+name+`"`) {
		t.Fatalf("%s %v", out, err)
	}
}

// A console or an editor finds the server of its machine in the
// configuration it shares with that server.
func TestTheServerOfThisMachineComesFromTheSharedConfiguration(t *testing.T) {
	cfg := &config.Config{
		Browser:    config.BrowserConfig{Enable: true, Token: strings.Repeat("k", 32), ServeRemote: true},
		HTTPServer: config.HTTPServerConfig{Host: "0.0.0.0", Port: 4567, AuthToken: "api-token"},
	}
	bs := backends(cfg)
	if len(bs) != 1 || bs[0].remote == nil || bs[0].label != "" {
		t.Fatalf("backends %+v", bs)
	}
	if bs[0].remote.Base != "http://127.0.0.1:4567" || bs[0].remote.Token != "api-token" {
		t.Fatalf("the local server %+v", bs[0].remote)
	}
	cfg.Browser.ServeRemote = false
	if bs := backends(cfg); len(bs) != 0 {
		t.Fatalf("a server that keeps its browsers was asked: %+v", bs)
	}
}

func TestRemoteLabels(t *testing.T) {
	for entry, want := range map[string]string{
		"home": "home",
		"https://relay:8443/swarm/nodes/a/swarm/nodes/b": "b",
		"http://10.0.0.5:12345":                          "10.0.0.5:12345",
	} {
		base := entry
		if !strings.Contains(base, "://") {
			base = "http://box:12345"
		}
		if got := remoteLabel(entry, base); got != want {
			t.Errorf("%s: %q", entry, got)
		}
	}
}

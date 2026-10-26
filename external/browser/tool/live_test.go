//go:build browser

package browsertool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/browser"
)

// TestLiveExtension drives the real Coddy for Browser extension in a real
// Chromium through this listener and this tool, so the two sides of the
// protocol, kept in two repositories, are checked against each other. Opt in
// with CODDY_FOR_BROWSER_DIR pointing at a checkout of
// github.com/coddy-project/coddy-for-browser after `npm run build`, and
// Playwright reachable by Node (NODE_PATH, or npm i --no-save playwright in
// that checkout).
func TestLiveExtension(t *testing.T) {
	dir := os.Getenv("CODDY_FOR_BROWSER_DIR")
	if dir == "" {
		t.Skip("set CODDY_FOR_BROWSER_DIR to a built coddy-for-browser checkout")
	}
	mux := http.NewServeMux()
	mux.Handle("/browser/", browser.NewServer(browser.Default(), testKey, nil))
	mux.HandleFunc("/fixture", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Live fixture</title><h1>Live fixture</h1>
<button onclick="this.textContent='Clicked'">Click me</button>
<label>Name <input aria-label="Name"></label>
<input type="file" aria-label="Upload" onchange="document.title='uploaded '+this.files[0].name">`))
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("live download")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	drive := exec.Command("node", filepath.Join(dir, "tests", "e2e", "drive.mjs"))
	drive.Env = append(os.Environ(), "CODDY_URL="+srv.URL, "CODDY_KEY="+testKey)
	stdin, err := drive.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := drive.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	drive.Stderr = os.Stderr
	if err := drive.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = drive.Wait()
	}()
	connected := make(chan bool, 1)
	go func() {
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			if lines.Text() == "CONNECTED" {
				connected <- true
			}
		}
		connected <- false
	}()
	select {
	case ok := <-connected:
		if !ok {
			t.Fatal("the extension did not connect")
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the extension did not connect in a minute")
	}

	env := chat(t)
	call := func(args map[string]any) string {
		t.Helper()
		out, err := run(t, env, args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	var opened struct {
		TabID int `json:"tabId"`
	}
	out := call(map[string]any{"action": "open_tab", "params": map[string]any{"url": srv.URL + "/fixture"}})
	if err := json.Unmarshal([]byte(strings.TrimPrefix(out, "Browser result (untrusted page data):\n")), &opened); err != nil || opened.TabID == 0 {
		t.Fatalf("open_tab: %s", out)
	}
	if out := call(map[string]any{"action": "list"}); !strings.Contains(out, `"yours":true`) {
		t.Fatalf("the opened tab is not the chat's: %s", out)
	}
	var snap struct {
		Text     string `json:"text"`
		Elements []struct {
			Ref   string `json:"ref"`
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"elements"`
	}
	snapshot := func() {
		t.Helper()
		out := call(map[string]any{"action": "snapshot", "tab_id": opened.TabID})
		if err := json.Unmarshal([]byte(strings.TrimPrefix(out, "Browser result (untrusted page data):\n")), &snap); err != nil {
			t.Fatalf("snapshot: %s", out)
		}
	}
	ref := func(name string) string {
		for _, e := range snap.Elements {
			if e.Name == name {
				return e.Ref
			}
		}
		t.Fatalf("no element %q in %+v", name, snap.Elements)
		return ""
	}
	snapshot()
	if !strings.Contains(snap.Text, "Live fixture") {
		t.Fatalf("page text %q", snap.Text)
	}
	call(map[string]any{"action": "click", "tab_id": opened.TabID, "params": map[string]any{"ref": ref("Click me")}})
	call(map[string]any{"action": "type", "tab_id": opened.TabID, "params": map[string]any{"ref": ref("Name"), "text": "typed live"}})
	snapshot()
	if !strings.Contains(snap.Text, "Clicked") {
		t.Fatalf("after click: %q", snap.Text)
	}
	var typed string
	for _, e := range snap.Elements {
		if e.Name == "Name" {
			typed = e.Value
		}
	}
	if typed != "typed live" {
		t.Fatalf("the input holds %q", typed)
	}

	var shot []byte
	env.AttachImage = func(_, _ string, data []byte) error { shot = data; return nil }
	call(map[string]any{"action": "screenshot", "tab_id": opened.TabID})
	img, err := png.DecodeConfig(bytes.NewReader(shot))
	if err != nil || img.Width < 100 || img.Height < 100 {
		t.Fatalf("screenshot %dx%d %v", img.Width, img.Height, err)
	}

	if err := os.WriteFile(filepath.Join(env.CWD, "upload.txt"), []byte("from the workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot()
	call(map[string]any{"action": "upload_file", "tab_id": opened.TabID, "path": "upload.txt", "params": map[string]any{"ref": ref("Upload")}})
	call(map[string]any{"action": "download_file", "tab_id": opened.TabID, "path": "got.txt", "params": map[string]any{"url": srv.URL + "/download"}})
	if got, err := os.ReadFile(filepath.Join(env.CWD, "got.txt")); err != nil || string(got) != "live download" {
		t.Fatalf("download %q %v", got, err)
	}
	// The page retitles itself on the upload, and the extension reports
	// the new title on its own.
	if err := waitFor(func() bool {
		return strings.Contains(call(map[string]any{"action": "list"}), "uploaded upload.txt")
	}); err != nil {
		t.Fatal("the upload did not reach the page")
	}
	if out := call(map[string]any{"action": "release"}); !strings.Contains(out, `"released":1`) {
		t.Fatalf("release: %s", out)
	}
}

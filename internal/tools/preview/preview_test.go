package preview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// serveDir runs the handler over dir the way the tool does, through os.Root.
func serveDir(t *testing.T, dir string, opts handlerOptions) *httptest.Server {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(newHandler(root.FS(), opts))
	t.Cleanup(func() {
		ts.Close()
		_ = root.Close()
	})
	return ts
}

func fetch(t *testing.T, method, address, host string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestHandlerServesScriptsWithAModuleSafeType(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app.js"), "export const x = 1;")
	writeFile(t, filepath.Join(dir, "lib", "util.mjs"), "export const y = 2;")
	writeFile(t, filepath.Join(dir, "style.css"), "body{}")
	ts := serveDir(t, dir, handlerOptions{})

	for path, want := range map[string]string{
		"/app.js":       "text/javascript",
		"/lib/util.mjs": "text/javascript",
		"/style.css":    "text/css",
	} {
		resp, _ := fetch(t, http.MethodGet, ts.URL+path, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %s", path, resp.Status)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, want) {
			t.Errorf("%s: Content-Type %q, want %q", path, got, want)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control %q, want no-store", path, got)
		}
	}
}

func TestHandlerHidesDotFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"), "SECRET=1")
	writeFile(t, filepath.Join(dir, ".git", "config"), "[core]")
	writeFile(t, filepath.Join(dir, "sub", ".hidden.html"), "x")
	writeFile(t, filepath.Join(dir, "index.html"), "ok")
	ts := serveDir(t, dir, handlerOptions{})

	for _, path := range []string{"/.env", "/.git/config", "/sub/.hidden.html", "/sub/../.env"} {
		resp, body := fetch(t, http.MethodGet, ts.URL+path, "")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %s, want 404 (body %q)", path, resp.Status, body)
		}
	}
}

func TestHandlerRefusesAForeignHost(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "ok")
	ts := serveDir(t, dir, handlerOptions{AllowedHosts: []string{"localhost", "127.0.0.1", "::1"}})

	if resp, _ := fetch(t, http.MethodGet, ts.URL+"/", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback host: %s", resp.Status)
	}
	if resp, _ := fetch(t, http.MethodGet, ts.URL+"/", "LOCALHOST:1234"); resp.StatusCode != http.StatusOK {
		t.Fatalf("localhost in another case: %s", resp.Status)
	}
	if resp, _ := fetch(t, http.MethodGet, ts.URL+"/", "[::1]:1234"); resp.StatusCode != http.StatusOK {
		t.Fatalf("IPv6 loopback: %s", resp.Status)
	}
	if resp, _ := fetch(t, http.MethodGet, ts.URL+"/", "evil.example:1234"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rebinding host: %s, want 403", resp.Status)
	}
}

func TestHandlerIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "ok")
	ts := serveDir(t, dir, handlerOptions{})

	resp, _ := fetch(t, http.MethodPost, ts.URL+"/index.html", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %s, want 405", resp.Status)
	}
}

func TestHandlerLogsEveryRequestWithItsStatus(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "ok")
	log := &syncBuffer{}
	ts := serveDir(t, dir, handlerOptions{Log: log})

	fetch(t, http.MethodGet, ts.URL+"/", "")
	fetch(t, http.MethodGet, ts.URL+"/missing.js", "")
	// The line is written once the handler returns, which is after the client
	// has its answer.
	eventually(t, func() bool {
		got := log.String()
		return strings.Contains(got, "GET / 200\n") && strings.Contains(got, "GET /missing.js 404\n")
	}, func() string { return "request log = " + log.String() })
}

// syncBuffer is a log sink the test can read while the server still writes.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func eventually(t *testing.T, ok func() bool, describe func() string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(describe())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHandlerDoesNotFollowASymlinkOutOfTheRoot(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "outside")
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "leak.txt")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	ts := serveDir(t, dir, handlerOptions{})

	resp, body := fetch(t, http.MethodGet, ts.URL+"/leak.txt", "")
	if resp.StatusCode == http.StatusOK || strings.Contains(body, "outside") {
		t.Fatalf("symlink out of the root was served: %s %q", resp.Status, body)
	}
}

func TestServerURLAndAllowedHosts(t *testing.T) {
	cases := []struct {
		name, host, public string
		wantHost           string
		wantAnyHost        bool
	}{
		{name: "loopback", host: "127.0.0.1", wantHost: "127.0.0.1"},
		{name: "wildcard has no address of its own", host: "0.0.0.0", wantHost: "localhost", wantAnyHost: true},
		{name: "public host wins", host: "127.0.0.1", public: "dev.example", wantHost: "dev.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := listen(t.TempDir(), tc.host, tc.public)
			if err != nil {
				t.Fatal(err)
			}
			defer srv.close()
			if got := srv.url(); !strings.HasPrefix(got, "http://"+tc.wantHost+":") || !strings.HasSuffix(got, "/") {
				t.Errorf("url = %q, want host %q", got, tc.wantHost)
			}
			hosts := srv.allowedHosts()
			if tc.wantAnyHost != (hosts == nil) {
				t.Errorf("allowedHosts = %v, any host wanted: %v", hosts, tc.wantAnyHost)
			}
			if tc.public != "" && !strings.Contains(strings.Join(hosts, " "), tc.public) {
				t.Errorf("allowedHosts %v misses the public host", hosts)
			}
		})
	}
}

func newEnv(t *testing.T, cfg bgtask.Config) *tooling.Env {
	t.Helper()
	pool := bgtask.New(cfg)
	env := &tooling.Env{CWD: t.TempDir(), SessionID: "unit", BackgroundEnabled: true, Background: pool}
	t.Cleanup(func() { pool.StopSession("unit") })
	return env
}

func run(env *tooling.Env, args map[string]interface{}) (string, error) {
	raw, _ := json.Marshal(args)
	return PreviewServerTool().Execute(context.Background(), string(raw), env)
}

func TestToolRefusesADirectoryOutsideTheWorkingDirectory(t *testing.T) {
	env := newEnv(t, bgtask.Config{})
	for _, path := range []string{t.TempDir(), ".."} {
		if _, err := run(env, map[string]interface{}{"path": path}); err == nil || !strings.Contains(err.Error(), "outside the working directory") {
			t.Errorf("path %q: err = %v, want an outside-the-project refusal", path, err)
		}
	}
	if got := env.Background.List("unit"); len(got) != 0 {
		t.Fatalf("a refused call left %d tasks", len(got))
	}
}

func TestToolRefusesAMissingDirectory(t *testing.T) {
	env := newEnv(t, bgtask.Config{})
	if _, err := run(env, map[string]interface{}{"path": "nope"}); err == nil {
		t.Fatal("a missing directory was served")
	}
}

func TestToolRefusesANegativeTimeout(t *testing.T) {
	env := newEnv(t, bgtask.Config{})
	if _, err := run(env, map[string]interface{}{"timeout_seconds": -1}); err == nil {
		t.Fatal("a negative timeout was accepted")
	}
}

func TestToolServesTheDirectoryOfAFileAndLinksToIt(t *testing.T) {
	env := newEnv(t, bgtask.Config{})
	writeFile(t, filepath.Join(env.CWD, "demo", "my page.html"), "the demo page")

	out, err := run(env, map[string]interface{}{"path": "demo/my page.html"})
	if err != nil {
		t.Fatal(err)
	}
	address := urlInResult.FindString(out)
	if !strings.HasSuffix(address, "/my%20page.html") {
		t.Fatalf("address = %q, want it to open the file", address)
	}
	body, err := get(address)
	if err != nil || !strings.Contains(body, "the demo page") {
		t.Fatalf("GET %s: %q, %v", address, body, err)
	}
	tasks := env.Background.List("unit")
	if len(tasks) != 1 || tasks[0].Label != "preview demo" {
		t.Fatalf("tasks = %+v", tasks)
	}
}

func TestToolKeepsSeparateServersForSeparateDirectories(t *testing.T) {
	env := newEnv(t, bgtask.Config{})
	writeFile(t, filepath.Join(env.CWD, "a", "index.html"), "a")
	writeFile(t, filepath.Join(env.CWD, "b", "index.html"), "b")

	first, err := run(env, map[string]interface{}{"path": "a"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := run(env, map[string]interface{}{"path": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if urlInResult.FindString(first) == urlInResult.FindString(second) {
		t.Fatalf("two directories share one address: %q", first)
	}
}

func TestToolReleasesThePortWhenThePoolRefusesTheTask(t *testing.T) {
	env := newEnv(t, bgtask.Config{MaxConcurrent: 1})
	writeFile(t, filepath.Join(env.CWD, "a", "index.html"), "a")
	writeFile(t, filepath.Join(env.CWD, "b", "index.html"), "b")

	if _, err := run(env, map[string]interface{}{"path": "a"}); err != nil {
		t.Fatal(err)
	}
	_, err := run(env, map[string]interface{}{"path": "b"})
	if !errors.Is(err, bgtask.ErrPoolFull) {
		t.Fatalf("err = %v, want ErrPoolFull", err)
	}
	if got := env.Background.List("unit"); len(got) != 1 {
		t.Fatalf("pool holds %d tasks, want the first server only", len(got))
	}
}

func TestToolHonoursTheSwitches(t *testing.T) {
	env := newEnv(t, bgtask.Config{})
	env.PreviewServer = &tooling.PreviewServerSettings{Enabled: false, Host: "127.0.0.1"}
	if _, err := run(env, nil); err == nil || !strings.Contains(err.Error(), "tools.preview_server.enable") {
		t.Fatalf("disabled tool: err = %v", err)
	}

	env.PreviewServer = nil
	env.BackgroundEnabled = false
	if _, err := run(env, nil); err == nil || !strings.Contains(err.Error(), "tools.background.enable") {
		t.Fatalf("background off: err = %v", err)
	}
}

func TestToolWritesTheRequestLogToTheTaskOutput(t *testing.T) {
	env := newEnv(t, bgtask.Config{})
	writeFile(t, filepath.Join(env.CWD, "index.html"), "ok")

	out, err := run(env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := get(urlInResult.FindString(out)); err != nil {
		t.Fatal(err)
	}
	tasks := env.Background.List("unit")
	output := func() string {
		text, _, err := env.Background.Output("unit", tasks[0].ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	eventually(t, func() bool { return strings.Contains(output(), "GET / 200") },
		func() string { return "task output = " + output() })
}

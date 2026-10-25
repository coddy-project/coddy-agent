package configapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// workspaceBackend serves the configuration of a process that runs on the
// workspace's config.yaml (the home has none) or, with fromHome, on the
// home's own file.
func workspaceBackend(t *testing.T, fromHome bool) (*http.ServeMux, string) {
	return workspaceBackendWith(t, fromHome, "mcp:\n  project_trust: ask\n")
}

func workspaceBackendWith(t *testing.T, fromHome bool, body string) (*http.ServeMux, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	project := t.TempDir()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := project
	if fromHome {
		dir = home
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	live, err := config.LoadFromCLI(config.CLIPaths{Home: home, CWD: project})
	if err != nil {
		t.Fatal(err)
	}
	if live.Paths.ConfigFromWorkspace == fromHome {
		t.Fatalf("paths = %+v, want ConfigFromWorkspace %v", live.Paths, !fromHome)
	}
	b := &Backend{
		Live:    func() *config.Config { return live },
		Install: func(c *config.Config) error { live = c; return nil },
		Schema:  func() ([]byte, error) { return []byte("{}"), nil },
	}
	mux := http.NewServeMux()
	b.Register(mux)
	return mux, path
}

func configDoc(t *testing.T, mux *http.ServeMux) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/coddy/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /coddy/config = %d %s", rec.Code, rec.Body)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func send(t *testing.T, mux *http.ServeMux, method, target string, doc map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(string(body))))
	return rec
}

// A process that runs on the workspace's config.yaml does not use an allow
// trust policy or a command allowlist from it, so a save that sets one is
// refused with the key named, rather than written and then silently not used.
func TestAWorkspaceConfigRefusesASaveThatWidensTrust(t *testing.T) {
	for name, edit := range map[string]func(doc map[string]any){
		"mcp.project_trust": func(doc map[string]any) {
			doc["mcp"] = map[string]any{"project_trust": "allow"}
		},
		"tools.command_allowlist": func(doc map[string]any) {
			tools, _ := doc["tools"].(map[string]any)
			if tools == nil {
				tools = map[string]any{}
			}
			tools["command_allowlist"] = []string{"git status"}
			doc["tools"] = tools
		},
	} {
		t.Run(name, func(t *testing.T) {
			mux, path := workspaceBackend(t, false)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc := configDoc(t, mux)
			edit(doc)
			for _, target := range []string{"/coddy/config/validate", "/coddy/config"} {
				method := http.MethodPost
				if target == "/coddy/config" {
					method = http.MethodPut
				}
				rec := send(t, mux, method, target, doc)
				if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), name) {
					t.Fatalf("%s %s = %d %s, want 400 naming %s", method, target, rec.Code, rec.Body, name)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatalf("the refused save changed the file:\n%s", after)
			}
		})
	}
}

// Anything else saves as before on the workspace's file, and the home's own
// file takes an allow policy.
func TestAWorkspaceConfigSavesWhatItMaySet(t *testing.T) {
	mux, _ := workspaceBackend(t, false)
	doc := configDoc(t, mux)
	doc["mcp"] = map[string]any{"project_trust": "deny"}
	if rec := send(t, mux, http.MethodPut, "/coddy/config", doc); rec.Code != http.StatusOK {
		t.Fatalf("PUT deny on the workspace's file = %d %s", rec.Code, rec.Body)
	}

	mux, path := workspaceBackend(t, true)
	doc = configDoc(t, mux)
	doc["mcp"] = map[string]any{"project_trust": "allow"}
	if rec := send(t, mux, http.MethodPut, "/coddy/config", doc); rec.Code != http.StatusOK {
		t.Fatalf("PUT allow on the home's file = %d %s", rec.Code, rec.Body)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "project_trust: allow") {
		t.Fatalf("the home's file did not take the allow policy:\n%s", saved)
	}
}

// A workspace file that already says allow is served as ask, the value in
// use; a save that leaves that field as served goes through, so an unrelated
// edit is never blocked by a key the operator did not touch.
func TestAWorkspaceConfigWithAnUnusedAllowStillSaves(t *testing.T) {
	mux, _ := workspaceBackendWith(t, false, "mcp:\n  project_trust: allow\n")
	doc := configDoc(t, mux)
	if mcp, _ := doc["mcp"].(map[string]any); mcp["project_trust"] == "allow" {
		t.Fatalf("served mcp = %v, want the value in use", mcp)
	}
	if rec := send(t, mux, http.MethodPut, "/coddy/config", doc); rec.Code != http.StatusOK {
		t.Fatalf("PUT as served = %d %s", rec.Code, rec.Body)
	}
}

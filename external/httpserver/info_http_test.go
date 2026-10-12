//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// newInfoTestServer serves the API over a configuration built in code whose
// fallback permission mode (Tools.PermissionMode, what new sessions start in
// while nothing was chosen) is mode, with the manager every reload path goes
// through, so a test can swap the configuration the way a settings save does.
func newInfoTestServer(t *testing.T, mode string) (*httptest.Server, *session.Manager) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = root
	cfg.Tools.PermissionMode = mode
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, nil)
	srv := New(cfg, mgr, slog.Default(), root)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})
	return ts, mgr
}

func readInfo(t *testing.T, ts *httptest.Server) map[string]interface{} {
	t.Helper()
	resp, err := http.Get(ts.URL + "/coddy/info")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /coddy/info: status %d", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The web UI's start screen has no session to read a settings snapshot from,
// yet its permission chip has to name the mode the first turn will run under
// and its pick has to be compared with that mode. GET /coddy/info, which the
// page already reads through any environment, names it: the mode a new
// session starts in (here the fallback of a configuration built in code, ask
// when it names none), and the new value as soon as a reload installs one.
func TestCoddyInfoNamesTheConfiguredPermissionMode(t *testing.T) {
	cases := []struct {
		configured string
		want       string
	}{
		{config.PermModeBypass, config.PermModeBypass},
		{config.PermModeAcceptEdits, config.PermModeAcceptEdits},
		{config.PermModeAsk, config.PermModeAsk},
		{"", config.PermModeAsk},
		{"sometimes", config.PermModeAsk},
	}
	for _, tc := range cases {
		t.Run("configured="+tc.configured, func(t *testing.T) {
			ts, _ := newInfoTestServer(t, tc.configured)
			if got, _ := readInfo(t, ts)["permissionMode"].(string); got != tc.want {
				t.Fatalf("permissionMode = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCoddyInfoFollowsAReloadedPermissionMode(t *testing.T) {
	ts, mgr := newInfoTestServer(t, config.PermModeBypass)
	if got, _ := readInfo(t, ts)["permissionMode"].(string); got != config.PermModeBypass {
		t.Fatalf("before the reload: permissionMode = %q, want bypass", got)
	}
	next := *mgr.Cfg()
	next.Tools.PermissionMode = config.PermModeAsk
	mgr.ReplaceConfig(&next)
	if got, _ := readInfo(t, ts)["permissionMode"].(string); got != config.PermModeAsk {
		t.Fatalf("after the reload: permissionMode = %q, want ask", got)
	}
}

// The served spec names the field, with the values it takes.
func TestOpenAPIDescribesTheInfoPermissionMode(t *testing.T) {
	paths, _ := openAPISpec()["paths"].(map[string]interface{})
	entry, _ := paths["/coddy/info"].(map[string]interface{})
	get, _ := entry["get"].(map[string]interface{})
	responses, _ := get["responses"].(map[string]interface{})
	ok, _ := responses["200"].(map[string]interface{})
	content, _ := ok["content"].(map[string]interface{})
	media, _ := content["application/json"].(map[string]interface{})
	schema, _ := media["schema"].(map[string]interface{})
	props, _ := schema["properties"].(map[string]interface{})
	field, _ := props["permissionMode"].(map[string]interface{})
	if field == nil {
		t.Fatalf("GET /coddy/info schema has no permissionMode: %#v", schema)
	}
	enum, _ := field["enum"].([]string)
	want := []string{config.PermModeAsk, config.PermModeAcceptEdits, config.PermModeBypass}
	if len(enum) != len(want) {
		t.Fatalf("permissionMode enum = %v, want %v", enum, want)
	}
	for i := range want {
		if enum[i] != want[i] {
			t.Fatalf("permissionMode enum = %v, want %v", enum, want)
		}
	}
	required, _ := schema["required"].([]string)
	found := false
	for _, r := range required {
		found = found || r == "permissionMode"
	}
	if !found {
		t.Fatalf("permissionMode is not required: %v", required)
	}
}

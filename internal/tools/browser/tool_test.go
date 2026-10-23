package browser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bridge "github.com/EvilFreelancer/coddy-agent/internal/browser"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestSelectionSurvivesToolCalls(t *testing.T) {
	hub := bridge.Default()
	c, err := hub.Connect("browser-tool-test", "Chrome", []string{"snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Disconnect(c)
	if err := hub.UpdateTabs(c, []bridge.Tab{{ID: 1, URL: "https://example.com"}}); err != nil {
		t.Fatal(err)
	}
	env := &tooling.Env{SessionID: "sess_test", SessionDir: t.TempDir(), CWD: t.TempDir()}
	if _, err := execute(context.Background(), `{"action":"select","browser_id":"browser-tool-test"}`, env); err != nil {
		t.Fatal(err)
	}
	go func() {
		cmd := <-c.Commands
		hub.Reply(c, bridge.Reply{ID: cmd.ID, Result: json.RawMessage(`{"text":"selected page"}`)})
	}()
	result, err := execute(context.Background(), `{"action":"snapshot","tab_id":1}`, env)
	if err != nil || !strings.Contains(result, "selected page") {
		t.Fatalf("remembered browser: %s %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(env.SessionDir, "browser-selection.json")); err != nil {
		t.Fatal(err)
	}
}

func TestFilePathsCannotEscapeWorkspace(t *testing.T) {
	env := &tooling.Env{SessionID: "sess_test", CWD: t.TempDir()}
	for _, path := range []string{"../outside", "/absolute", "C:\\outside"} {
		input, _ := json.Marshal(Args{Action: "upload_file", BrowserID: "browser-tool-test", Path: path})
		if _, err := execute(context.Background(), string(input), env); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
}

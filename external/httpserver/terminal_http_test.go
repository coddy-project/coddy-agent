//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestTerminalKeepsShellStateForTheChat(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, nil)
	created, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, mgr, slog.Default(), root)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})
	endpoint := ts.URL + "/coddy/sessions/" + created.SessionID + "/terminal"

	postTerminalInput(t, endpoint, `cd "sub"`)
	showCWD := "pwd"
	if platform.CurrentShell().Kind == platform.ShellCmd {
		showCWD = "cd"
	}
	postTerminalInput(t, endpoint, showCWD)

	deadline := time.Now().Add(3 * time.Second)
	for {
		res, err := http.Get(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		var snap terminalSnapshot
		err = json.NewDecoder(res.Body).Decode(&snap)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(snap.Output, sub) {
			if !snap.Running {
				t.Fatalf("terminal exited early: %+v", snap)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal output never reported cwd %q: %q", sub, snap.Output)
		}
		time.Sleep(20 * time.Millisecond)
	}

	req, _ := http.NewRequest(http.MethodDelete, endpoint, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE terminal status = %d", res.StatusCode)
	}
}

func postTerminalInput(t *testing.T, endpoint, input string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"input": input})
	res, err := http.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST terminal status = %d", res.StatusCode)
	}
}

//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	historyMarker := "coddy-terminal-history-marker"
	if platform.CurrentShell().Kind == platform.ShellBash {
		postTerminalInput(t, endpoint, "echo "+historyMarker)
		postTerminalInput(t, endpoint, "history")
	}

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
		historyReady :=
			platform.CurrentShell().Kind != platform.ShellBash ||
				strings.Count(snap.Output, historyMarker) >= 2
		if strings.Contains(snap.Output, sub) && historyReady {
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
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST terminal status = %d", res.StatusCode)
	}
}

// Escape sequences carry no meaning in the browser's <pre>, and a cut UTF-8
// sequence must survive the read that split it: both are what a shell writing
// colour and non-ASCII text produces on every platform.
func TestTerminalOutputIsDecodedAndStripped(t *testing.T) {
	term := &terminalSession{running: true, seen: time.Now()}
	w := &terminalWriter{term: term}
	text := []byte("\x1b[32mпривет\x1b[0m\r\nвторая строка\n")
	for i := 0; i < len(text); i += 3 {
		end := i + 3
		if end > len(text) {
			end = len(text)
		}
		if _, err := w.Write(text[i:end]); err != nil {
			t.Fatal(err)
		}
	}
	got := term.snapshot().Output
	if got != "привет\nвторая строка\n" {
		t.Fatalf("output = %q", got)
	}
}

// The retained tail is handed to a browser as JSON, so a cut that beheaded a
// character must not leave its continuation bytes at the top of the screen.
func TestTerminalOutputTailStaysValidUTF8(t *testing.T) {
	term := &terminalSession{running: true, seen: time.Now()}
	term.append(strings.Repeat("я", terminalOutputLimit))
	term.append("tail")
	out := term.snapshot().Output
	if !utf8.ValidString(out) {
		t.Fatalf("retained tail is not valid UTF-8: %q", out[:16])
	}
	if len(out) > terminalOutputLimit {
		t.Fatalf("retained %d bytes, cap is %d", len(out), terminalOutputLimit)
	}
	if !strings.HasSuffix(out, "tail") {
		t.Fatalf("the newest output was dropped: %q", out[len(out)-8:])
	}
}

// A command is one line: a second line would run unseen behind the first.
func TestTerminalRefusesACommandOfSeveralLines(t *testing.T) {
	term := &terminalSession{running: true, stdin: nopWriteCloser{io.Discard}, seen: time.Now()}
	if err := term.write("echo one\nrm -rf /"); err == nil {
		t.Fatal("a two-line command was accepted")
	}
	if err := term.write("echo one\n"); err != nil {
		t.Fatalf("a trailing newline was refused: %v", err)
	}
}

// Nothing else ever ends a shell: the window may be closed and opened again
// without losing it, so a chat left open months ago must not keep a process.
func TestTerminalsIdleTooLongAreStoppedWhenTheNextOneStarts(t *testing.T) {
	srv := &Server{terminals: map[string]*terminalSession{}}
	idle := &terminalSession{seen: time.Now().Add(-terminalIdleLimit - time.Minute)}
	fresh := &terminalSession{seen: time.Now()}
	srv.terminals["old"] = idle
	srv.terminals["new"] = fresh

	srv.terminalMu.Lock()
	reaped := srv.reapIdleTerminalsLocked("asking")
	srv.terminalMu.Unlock()

	if len(reaped) != 1 || reaped[0] != idle {
		t.Fatalf("reaped %d terminals, want the idle one", len(reaped))
	}
	if _, still := srv.terminals["old"]; still {
		t.Fatal("the idle terminal is still in the map")
	}
	if srv.terminals["new"] != fresh {
		t.Fatal("a terminal in use was reaped")
	}
}

// The chat being asked for is in use by that very request, however long its
// window was closed.
func TestTheTerminalBeingAskedForIsNeverReaped(t *testing.T) {
	srv := &Server{terminals: map[string]*terminalSession{}}
	srv.terminals["asking"] = &terminalSession{
		seen: time.Now().Add(-terminalIdleLimit - time.Hour),
	}
	srv.terminalMu.Lock()
	reaped := srv.reapIdleTerminalsLocked("asking")
	srv.terminalMu.Unlock()
	if len(reaped) != 0 || srv.terminals["asking"] == nil {
		t.Fatal("the terminal of the request was reaped")
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

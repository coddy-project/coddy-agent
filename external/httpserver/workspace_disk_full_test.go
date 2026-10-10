//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// newEnsureSessionServer is a server whose session creation answers what
// ensure says, so a test makes it fail the way a full volume fails it.
func newEnsureSessionServer(t *testing.T, ensure func(ctx context.Context, id, cwd, origin string) (*session.State, error)) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: filepath.Join(root, "home"), CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: "http://127.0.0.1:1", APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "local/qwen3-1.7b"}},
		Agent:     config.Agent{Model: "local/qwen3-1.7b"},
	}
	log := slog.New(slog.DiscardHandler)
	mgr := session.NewManager(cfg, noopSender{}, nil, log, cwd, nil)
	srv := New(cfg, mgr, log, cwd)
	srv.ensureSession = ensure
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})
	return ts, cwd
}

func postWorkspace(t *testing.T, ts *httptest.Server, id, path string) (int, string) {
	t.Helper()
	body := fmt.Sprintf(`{"path":%q}`, path)
	res, err := http.Post(ts.URL+"/coddy/sessions/"+id+"/workspace", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	return res.StatusCode, env.Error.Message
}

// The web UI picks the folder of a new chat first: this route is what creates
// the session of a chat that has a pending folder, so a full volume has to be
// named here too, or the person never reaches the send that would say it.
func TestWorkspacePostOnAFullDiskIsInsufficientStorage(t *testing.T) {
	layout := fmt.Errorf("session/new: layout: %w", &os.PathError{Op: "mkdir", Path: "/home/coddy/sessions/sess_x", Err: diskFullErrno})
	ts, _ := newEnsureSessionServer(t, func(context.Context, string, string, string) (*session.State, error) {
		return nil, layout
	})
	folder := t.TempDir()

	status, message := postWorkspace(t, ts, session.NewSessionID(), folder)

	if status != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want 507; message %q", status, message)
	}
	if message != session.DiskFullMessage {
		t.Fatalf("message = %q, want %q", message, session.DiskFullMessage)
	}
}

// Every other failure to open the session keeps the answer this route always
// gave: 400 with the error's own text.
func TestWorkspacePostKeepsItsOtherAnswersForOtherFailures(t *testing.T) {
	ts, _ := newEnsureSessionServer(t, func(context.Context, string, string, string) (*session.State, error) {
		return nil, errors.New("session/new: layout: permission denied")
	})

	status, message := postWorkspace(t, ts, session.NewSessionID(), t.TempDir())

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; message %q", status, message)
	}
	if message != "session/new: layout: permission denied" {
		t.Fatalf("message = %q, want the error's own text", message)
	}
}

// The folder the picker named reaches session creation, so the bundle is made
// in the workspace the person chose and not in the server default.
func TestWorkspacePostCreatesTheSessionInThePickedFolder(t *testing.T) {
	var gotCWD string
	ts, defaultCWD := newEnsureSessionServer(t, func(_ context.Context, _ string, cwd, _ string) (*session.State, error) {
		gotCWD = cwd
		return nil, errors.New("stop here")
	})
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	postWorkspace(t, ts, session.NewSessionID(), folder)

	if gotCWD != folder {
		t.Fatalf("session created in %q, want the picked folder %q (default %q)", gotCWD, folder, defaultCWD)
	}
}

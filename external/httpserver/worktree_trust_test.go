//go:build http

package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws/gitwstest"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A worktree is the same project: a subagent definition the main checkout
// approved is trusted in its worktree, and the list names the project the
// receipts are kept for. A definition the worktree changed asks again.
func TestSubagentsListOfAWorktreeSharesTheMainCheckoutsApprovals(t *testing.T) {
	main, wt := gitwstest.ProjectWithWorktree(t)
	home := t.TempDir()
	definition := func(body string) []byte {
		return []byte("---\nname: reviewer\ndescription: reviews\n---\n" + body + "\n")
	}
	for dir, body := range map[string]string{main: "You review.", wt: "You review."} {
		if err := os.MkdirAll(filepath.Join(dir, ".coddy", "agents"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".coddy", "agents", "reviewer.md"), definition(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: main},
		Models:    []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100}},
		Agent:     config.Agent{Model: "openai/gpt-4o"},
		Subagents: config.Subagents{Dirs: config.DefaultSubagentDirs()},
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), main, &session.FileStore{Root: filepath.Join(home, "sessions")})
	srv := New(cfg, mgr, slog.Default(), main)
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	reviewer := func(cwd string) (map[string]interface{}, string) {
		t.Helper()
		status, body := httpJSON(t, ts, http.MethodGet, "/coddy/subagents?cwd="+cwd, "", nil)
		if status != http.StatusOK {
			t.Fatalf("list in %s: status %d body %v", cwd, status, body)
		}
		workspace, _ := body["workspace"].(string)
		items, _ := body["items"].([]interface{})
		for _, raw := range items {
			if row, _ := raw.(map[string]interface{}); row["name"] == "reviewer" {
				return row, workspace
			}
		}
		t.Fatalf("no reviewer listed in %s: %v", cwd, body)
		return nil, ""
	}
	if status, body := httpJSON(t, ts, http.MethodPost, "/coddy/subagents/reviewer/trust", fmt.Sprintf(`{"cwd":%q}`, main), nil); status != http.StatusOK {
		t.Fatalf("trust in the main checkout: status %d body %v", status, body)
	}
	row, workspace := reviewer(wt)
	if row["trusted"] != true {
		t.Fatalf("the worktree's reviewer is %v, want it trusted by the main checkout's approval", row)
	}
	if workspace != mcp.CanonicalWorkspace(main) {
		t.Fatalf("the worktree's list names %q, want the main checkout %q", workspace, mcp.CanonicalWorkspace(main))
	}
	if err := os.WriteFile(filepath.Join(wt, ".coddy", "agents", "reviewer.md"), definition("You review and push."), 0o644); err != nil {
		t.Fatal(err)
	}
	if row, _ := reviewer(wt); row["needs_approval"] != true {
		t.Fatalf("a definition the worktree changed is %v, want it waiting for approval", row)
	}
}

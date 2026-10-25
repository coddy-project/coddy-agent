package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// runOneWrite runs a turn whose model makes one filesystem call and reports
// how many permission prompts the client saw.
func runOneWrite(t *testing.T, mode, cwd, home string, additional []string, tool string, args map[string]string) int {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 4},
	}
	cfg.Tools.PermissionMode = mode
	st := &session.State{ID: "sess_accept_edits_scope", CWD: cwd, Mode: session.ModeAgent}
	st.SetAdditionalDirectoriesWithoutPersist(additional)
	sender := &recordingPermissionSender{}
	provider := &scriptedProvider{steps: []scriptStep{
		toolStep(llm.ToolCall{ID: "call_1", Name: tool, InputJSON: string(raw)}),
		answerStep("done"),
	}}
	ag := NewAgent(cfg, st, sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "go"}}); err != nil {
		t.Fatal(err)
	}
	return len(sender.requests)
}

// accept_edits approves the edits of the project, not every file the
// process can write: the agent's home (hooks.json, mcp.json, config.yaml,
// the trust receipts) and anything outside the workspace still ask, the way
// config_commit does. bypass keeps skipping every prompt.
func TestAcceptEditsAutoApprovesOnlyWorkspaceWrites(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	home := filepath.Join(root, "coddy-home")
	extra := filepath.Join(root, "extra")
	outside := filepath.Join(root, "elsewhere")
	for _, d := range []string{cwd, home, extra, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p string) map[string]string { return map[string]string{"path": p, "content": "x"} }

	cases := []struct {
		name, mode, tool string
		args             map[string]string
		prompts          int
	}{
		{"relative path in the project", config.PermModeAcceptEdits, "write", write("notes.txt"), 0},
		{"absolute path in the project", config.PermModeAcceptEdits, "write", write(filepath.Join(cwd, "a", "b.txt")), 0},
		{"additional directory", config.PermModeAcceptEdits, "write", write(filepath.Join(extra, "c.txt")), 0},
		{"the agent's home", config.PermModeAcceptEdits, "write", write(filepath.Join(home, "hooks.json")), 1},
		{"outside the workspace", config.PermModeAcceptEdits, "write", write(filepath.Join(outside, "d.txt")), 1},
		{"climbing out with ..", config.PermModeAcceptEdits, "write", write(filepath.Join("..", "elsewhere", "e.txt")), 1},
		{"move out of the project", config.PermModeAcceptEdits, "mv", map[string]string{"src": "notes.txt", "dst": filepath.Join(outside, "f.txt")}, 1},
		{"bypass writes the home", config.PermModeBypass, "write", write(filepath.Join(home, "g.json")), 0},
		{"ask still asks in the project", config.PermModeAsk, "write", write("h.txt"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runOneWrite(t, tc.mode, cwd, home, []string{extra}, tc.tool, tc.args); got != tc.prompts {
				t.Fatalf("permission prompts = %d, want %d", got, tc.prompts)
			}
		})
	}
}

// An extra root an ACP client names (session/new, session/resume) widens the
// workspace, never into the agent's home, even when the root contains it.
func TestAcceptEditsAdditionalRootDoesNotCoverTheHome(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	home := filepath.Join(root, "coddy-home")
	for _, d := range []string{cwd, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := runOneWrite(t, config.PermModeAcceptEdits, cwd, home, []string{root}, "write",
		map[string]string{"path": filepath.Join(home, "mcp.json"), "content": "x"})
	if got != 1 {
		t.Fatalf("permission prompts = %d, want 1 for the home under an extra root", got)
	}
}

// A link inside the project that leads into the agent's home is judged by
// where it leads.
func TestAcceptEditsFollowsLinksOutOfTheWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs a privilege the Windows runner may not have")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	home := filepath.Join(root, "coddy-home")
	for _, d := range []string{cwd, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(home, filepath.Join(cwd, "settings")); err != nil {
		t.Fatal(err)
	}
	got := runOneWrite(t, config.PermModeAcceptEdits, cwd, home, nil, "write",
		map[string]string{"path": filepath.Join("settings", "hooks.json"), "content": "x"})
	if got != 1 {
		t.Fatalf("permission prompts = %d, want 1 for a write through a link into the home", got)
	}
}

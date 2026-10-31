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
func runOneWrite(t *testing.T, mode, cwd, home string, additional []string, tool string, args any) int {
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
		{"git's own directory", config.PermModeAcceptEdits, "write", write(filepath.Join(".git", "hooks", "pre-commit")), 1},
		{"git's own directory of an additional root", config.PermModeAcceptEdits, "write", write(filepath.Join(extra, ".git", "config")), 1},
		{"a file named like git's folder", config.PermModeAcceptEdits, "write", write(filepath.Join(".github", "workflows", "ci.yaml")), 0},
		{"a move into git's own directory", config.PermModeAcceptEdits, "mv", map[string]string{"src": "notes.txt", "dst": filepath.Join(".git", "hooks", "post-merge")}, 1},
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

// A link whose target does not exist yet leads where a write through it creates
// the file: into the agent's home or a git hook, which ask.
func TestAcceptEditsFollowsADanglingLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs a privilege the Windows runner may not have")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	home := filepath.Join(root, "coddy-home")
	for _, d := range []string{filepath.Join(cwd, ".git", "hooks"), home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		"to-home.txt":   filepath.Join(home, "hooks.json"),
		"to-hook.txt":   filepath.Join(".git", "hooks", "post-commit"),
		"to-link.txt":   "to-home.txt",
		"in-folder.txt": filepath.Join("..", "coddy-home", "mcp.json"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(cwd, name)); err != nil {
			t.Fatal(err)
		}
	}
	for name := range links {
		t.Run(name, func(t *testing.T) {
			got := runOneWrite(t, config.PermModeAcceptEdits, cwd, home, nil, "write",
				map[string]string{"path": name, "content": "x"})
			if got != 1 {
				t.Fatalf("permission prompts = %d, want 1 for a write through a dangling link", got)
			}
		})
	}
	// A dangling link that stays in the project is an edit of the project.
	if err := os.Symlink(filepath.Join("docs", "new.md"), filepath.Join(cwd, "stays.md")); err != nil {
		t.Fatal(err)
	}
	if got := runOneWrite(t, config.PermModeAcceptEdits, cwd, home, nil, "write",
		map[string]string{"path": "stays.md", "content": "x"}); got != 0 {
		t.Fatalf("permission prompts = %d, want 0 for a link that stays in the project", got)
	}
}

// A .git that is a link is git's own directory all the same. Git's tree format
// cannot ship one, an unpacked archive can.
func TestAcceptEditsKnowsAGitDirThatIsALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs a privilege the Windows runner may not have")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	home := filepath.Join(root, "coddy-home")
	for _, d := range []string{filepath.Join(cwd, ".gitdir", "hooks"), home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(".gitdir", filepath.Join(cwd, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := runOneWrite(t, config.PermModeAcceptEdits, cwd, home, nil, "write",
		map[string]string{"path": filepath.Join(".git", "hooks", "post-commit"), "content": "x"}); got != 1 {
		t.Fatalf("permission prompts = %d, want 1", got)
	}
}

// A removal or a move takes its whole tree: one that holds the home or a .git
// entry asks, an ordinary folder of the project does not.
func TestAcceptEditsLooksInsideARemovedTree(t *testing.T) {
	rm := func(p string) map[string]any { return map[string]any{"path": p, "recursive": true} }
	cases := []struct {
		name, tool string
		args       any
		prompts    int
	}{
		{"the project itself", "rm", rm("."), 1},
		{"a folder holding a nested checkout", "rm", rm("vendor"), 1},
		{"a folder holding the home", "rm", rm("tools"), 1},
		{"a move of a folder holding a nested checkout", "mv", map[string]string{"src": "vendor", "dst": "third_party"}, 1},
		{"an ordinary folder", "rm", rm("build"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The scripted client approves, so a case removes what it names.
			cwd := filepath.Join(t.TempDir(), "project")
			home := filepath.Join(cwd, "tools", "coddy-home")
			for _, d := range []string{filepath.Join(cwd, "vendor", "lib", ".git"), filepath.Join(cwd, "build", "out"), home} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if got := runOneWrite(t, config.PermModeAcceptEdits, cwd, home, nil, tc.tool, tc.args); got != tc.prompts {
				t.Fatalf("permission prompts = %d, want %d", got, tc.prompts)
			}
		})
	}
}

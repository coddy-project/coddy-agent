package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestParseWorktreeCommand(t *testing.T) {
	tests := []struct {
		in      string
		want    worktreeCommand
		problem bool
		wantOK  bool
	}{
		{in: "/worktree", want: worktreeCommand{}, wantOK: true},
		{in: "  /worktree  ", want: worktreeCommand{}, wantOK: true},
		{in: "/worktree feature/login", want: worktreeCommand{Branch: "feature/login"}, wantOK: true},
		{in: "/worktree\tfeature/next --from-head", want: worktreeCommand{Branch: "feature/next", FromHead: true}, wantOK: true},
		{in: "/worktree --from-head feature/next", want: worktreeCommand{Branch: "feature/next", FromHead: true}, wantOK: true},
		{in: "/worktree exit", want: worktreeCommand{Exit: true}, wantOK: true},
		{in: "/worktree exit --remove", want: worktreeCommand{Exit: true, Remove: true}, wantOK: true},
		{in: "/worktree exit --remove --force", want: worktreeCommand{Exit: true, Remove: true, Force: true}, wantOK: true},
		// Words that do not make a command still answer as /worktree, with
		// the reason and the usage, rather than going to the model.
		{in: "/worktree a b", problem: true, wantOK: true},
		{in: "/worktree --from-head", problem: true, wantOK: true},
		{in: "/worktree feature/x --remove", problem: true, wantOK: true},
		{in: "/worktree exit --force", problem: true, wantOK: true},
		{in: "/worktree exit --from-head", problem: true, wantOK: true},
		{in: "/worktree --detach x", problem: true, wantOK: true},
		{in: "/worktrees", wantOK: false},      // a longer word is not the command
		{in: "say /worktree x", wantOK: false}, // only when it leads the message
		{in: "hello", wantOK: false},
	}
	for _, tc := range tests {
		got, ok := parseWorktreeCommand(tc.in)
		if ok != tc.wantOK {
			t.Errorf("parseWorktreeCommand(%q) ok=%v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if tc.problem {
			if got.Problem == "" {
				t.Errorf("parseWorktreeCommand(%q) = %+v, want a problem", tc.in, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("parseWorktreeCommand(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

// A surface that took the worktree tools away from the turn - a messenger
// user who is not the bot's admin - takes the command away as well: it would
// otherwise move the chat with no tool call to refuse.
func TestWorktreeCommandHonoursATurnRestriction(t *testing.T) {
	cwd := t.TempDir()
	st := &session.State{ID: "sess_restricted", CWD: cwd, Mode: session.ModeAgent}
	st.SetTurnRestriction(&session.TurnRestriction{AllowedTools: []string{"read"}, Note: "only the bot's admins can do this"})
	a := NewAgent(&config.Config{}, st, nil, nil)
	moved := false
	a.SetWorkspaceSwitcher(func(context.Context, string) error { moved = true; return nil })

	if _, err := a.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/worktree exit"}}); err != nil {
		t.Fatal(err)
	}
	if moved || st.GetCWD() != cwd {
		t.Fatalf("a restricted turn moved the chat to %q", st.GetCWD())
	}
	msgs := st.GetMessages()
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != llm.RoleAssistant || !strings.Contains(msgs[len(msgs)-1].Content, "only the bot's admins") {
		t.Fatalf("reply = %+v, want the restriction's note", msgs)
	}
}

// Outside a repository /worktree says so and moves nothing.
func TestWorktreeCommandOutsideARepository(t *testing.T) {
	cwd := t.TempDir()
	st := &session.State{ID: "sess_plain", CWD: cwd, Mode: session.ModeAgent}
	a := NewAgent(&config.Config{}, st, nil, nil)
	a.SetWorkspaceSwitcher(func(context.Context, string) error { return nil })
	for _, prompt := range []string{"/worktree", "/worktree exit"} {
		if _, err := a.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: prompt}}); err != nil {
			t.Fatal(err)
		}
		msgs := st.GetMessages()
		reply := msgs[len(msgs)-1].Content
		if !strings.Contains(reply, "not") || st.GetCWD() != cwd {
			t.Fatalf("%s outside git answered %q in %q", prompt, reply, st.GetCWD())
		}
	}
}

// Without the git binary /worktree says so rather than that the folder is
// not in a repository.
func TestWorktreeCommandWithoutTheGitBinary(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	st := &session.State{ID: "sess_nogit", CWD: cwd, Mode: session.ModeAgent}
	a := NewAgent(&config.Config{}, st, nil, nil)
	a.SetWorkspaceSwitcher(func(context.Context, string) error { return nil })
	if _, err := a.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/worktree"}}); err != nil {
		t.Fatal(err)
	}
	msgs := st.GetMessages()
	if reply := msgs[len(msgs)-1].Content; !strings.Contains(reply, "git binary") || strings.Contains(reply, "not in a git repository") {
		t.Fatalf("/worktree without git answered %q", reply)
	}
}

func TestWorktreeCommandTellsAPathFromABranch(t *testing.T) {
	for word, path := range map[string]bool{
		"/srv/repo/.coddy/worktrees/x": true, "./review": true, "../other": true, "~": true, "~/wt": true,
		"feature/login": false, "fix/a/b": false, "main": false, "~branch": false,
	} {
		if got := looksLikePath(word); got != path {
			t.Errorf("looksLikePath(%q) = %t, want %t", word, got, path)
		}
	}
}

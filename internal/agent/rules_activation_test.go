package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// scopedRulesProject writes a root AGENTS.md plus one nested AGENTS.md per dir
// (slash-separated, relative to the project root) and returns a wired agent.
func scopedRulesProject(t *testing.T, dirs ...string) (*Agent, string) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("ROOT_AGENTS_TOKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		full := filepath.Join(tmp, filepath.FromSlash(d))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		token := "NESTED_TOKEN_" + strings.ToUpper(strings.NewReplacer("/", "_", "-", "_").Replace(d))
		if err := os.WriteFile(filepath.Join(full, "AGENTS.md"), []byte(token), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st := &session.State{ID: "t", CWD: tmp, Mode: session.ModeAgent}
	st.ReplaceRulesCatalog(session.DiscoverRules(&config.Config{}, tmp))
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	return NewAgent(cfg, st, nil, nil), tmp
}

func nestedToken(dir string) string {
	return "NESTED_TOKEN_" + strings.ToUpper(strings.NewReplacer("/", "_", "-", "_").Replace(dir))
}

func TestScopedAgentsRuleHiddenUntilToolTouchesItsDirectory(t *testing.T) {
	a, tmp := scopedRulesProject(t, "internal/agent", "external/httpserver")

	before := a.buildSystemPrompt("agent", nil, nil, "", nil)
	if !strings.Contains(before, "ROOT_AGENTS_TOKEN") {
		t.Fatal("root AGENTS.md must always be in the prompt")
	}
	for _, d := range []string{"internal/agent", "external/httpserver"} {
		if strings.Contains(before, nestedToken(d)) {
			t.Fatalf("nested AGENTS.md for %s leaked before any tool touched it", d)
		}
	}

	a.activateScopedRulesForToolCall("read", `{"path":"internal/agent/react.go"}`, tmp)

	after := a.buildSystemPrompt("agent", nil, nil, "", nil)
	if !strings.Contains(after, nestedToken("internal/agent")) {
		t.Fatal("nested AGENTS.md missing after a read inside its directory")
	}
	if strings.Contains(after, nestedToken("external/httpserver")) {
		t.Fatal("an untouched sibling directory's AGENTS.md must stay out of the prompt")
	}
}

func TestScopedAgentsRuleStaysActiveOnLaterTurns(t *testing.T) {
	a, tmp := scopedRulesProject(t, "internal/agent")

	a.activateScopedRulesForToolCall("edit", `{"path":"internal/agent/react.go","oldString":"a","newString":"b"}`, tmp)
	if !strings.Contains(a.buildSystemPrompt("agent", nil, nil, "", nil), nestedToken("internal/agent")) {
		t.Fatal("rule missing right after activation")
	}
	// A later turn working elsewhere must not drop it.
	a.activateScopedRulesForToolCall("read", `{"path":"docs/rules.md"}`, tmp)
	later := a.buildSystemPrompt("agent", nil, nil, "", []string{filepath.Join(tmp, "docs", "rules.md")})
	if !strings.Contains(later, nestedToken("internal/agent")) {
		t.Fatal("activation must stick for the rest of the session")
	}
}

func TestScopedAgentsRuleActivatesFromAttachedContextFile(t *testing.T) {
	a, tmp := scopedRulesProject(t, "internal/agent")

	prompt := a.buildSystemPrompt("agent", nil, nil, "", []string{filepath.Join(tmp, "internal", "agent", "react.go")})
	if !strings.Contains(prompt, nestedToken("internal/agent")) {
		t.Fatal("an attached file:// path inside the directory must activate its AGENTS.md")
	}
}

func TestScopedAgentsRuleNotActivatedByRunCommand(t *testing.T) {
	a, tmp := scopedRulesProject(t, "internal/agent")

	a.activateScopedRulesForToolCall("run_command", `{"command":"go test ./internal/agent"}`, tmp)
	if strings.Contains(a.buildSystemPrompt("agent", nil, nil, "", nil), nestedToken("internal/agent")) {
		t.Fatal("run_command must not activate scoped rules")
	}
}

func TestScopedAgentsRuleAncestorChain(t *testing.T) {
	a, tmp := scopedRulesProject(t, "a", "a/b", "a/b/c", "a/other")

	a.activateScopedRulesForToolCall("read", `{"path":"a/b/c/f.go"}`, tmp)
	prompt := a.buildSystemPrompt("agent", nil, nil, "", nil)
	for _, d := range []string{"a", "a/b", "a/b/c"} {
		if !strings.Contains(prompt, nestedToken(d)) {
			t.Fatalf("ancestor AGENTS.md for %q must activate too", d)
		}
	}
	if strings.Contains(prompt, nestedToken("a/other")) {
		t.Fatal("a sibling of the touched directory must not activate")
	}
}

func TestScopedAgentsRuleMoveActivatesBothEnds(t *testing.T) {
	a, tmp := scopedRulesProject(t, "src", "dst")

	a.activateScopedRulesForToolCall("mv", `{"src":"src/x.go","dst":"dst/x.go"}`, tmp)
	prompt := a.buildSystemPrompt("agent", nil, nil, "", nil)
	for _, d := range []string{"src", "dst"} {
		if !strings.Contains(prompt, nestedToken(d)) {
			t.Fatalf("mv must activate the AGENTS.md of %q", d)
		}
	}
}

func TestExtractContextFilesStripsWindowsDriveSlash(t *testing.T) {
	blocks := []acp.ContentBlock{
		{Type: "resource", Resource: &acp.Resource{URI: "file:///C:/proj/x.go"}},
		{Type: "resource", Resource: &acp.Resource{URI: "file:///home/u/y.go"}},
		// A POSIX path that merely contains a colon is not a drive path.
		{Type: "resource", Resource: &acp.Resource{URI: "file:///a:b/z.go"}},
	}
	got := extractContextFiles(blocks)
	want := []string{"C:/proj/x.go", "/home/u/y.go", "/a:b/z.go"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

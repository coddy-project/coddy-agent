//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// scriptedTitleModel answers every describe call with the same reply and
// remembers what it was asked, and how many times.
type scriptedTitleModel struct {
	reply  string
	calls  int
	system string
	user   string
}

func (p *scriptedTitleModel) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.calls++
	p.system, p.user = "", ""
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			p.system += m.Content
		case llm.RoleUser:
			p.user = m.Content
		}
	}
	return &llm.Response{Content: p.reply, StopReason: "end_turn"}, nil
}

func (p *scriptedTitleModel) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
	return p.Complete(ctx, msgs, tools)
}

// describeServer is a server whose default workspace is a fresh folder, so a
// test can put skills in it without touching a shared directory.
func describeServer(t *testing.T, model llm.Provider) (*httptest.Server, *session.Manager, string) {
	t.Helper()
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	home := filepath.Join(root, "home")
	for _, d := range []string{ws, home, filepath.Join(root, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: ws},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), ws, &session.FileStore{Root: filepath.Join(root, "sessions")})
	srv := New(cfg, mgr, slog.Default(), ws)
	srv.providerFactory = func(*config.Config) (llm.Provider, error) { return model, nil }
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})
	return ts, mgr, ws
}

func postDescribe(t *testing.T, ts *httptest.Server, text string, header map[string]string, query url.Values) (int, string, []string) {
	t.Helper()
	target := ts.URL + "/coddy/describe"
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	body, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		Short string   `json:"short"`
		Tags  []string `json:"tags"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out.Short, out.Tags
}

// A model that only repeats the command has named nothing. The words the user
// wrote around the command are the title then, not the command token.
func TestCoddyDescribeCommandEchoFallsBackToTheUsersWords(t *testing.T) {
	model := &scriptedTitleModel{reply: "/warm-up"}
	ts, _, ws := describeServer(t, model)
	if err := writeQuotedSkill(filepath.Join(ws, ".agents", "skills"), "warm-up", "Onboard onto a repository"); err != nil {
		t.Fatal(err)
	}
	status, short, _ := postDescribe(t, ts, "/warm-up focus on the session titles", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if short != "focus on the session titles" {
		t.Fatalf("short = %q, want the user's words without the command", short)
	}
}

// A command typed alone, with a model that gives nothing usable back, has no
// other words to fall back to: the command as typed is all that is left.
func TestCoddyDescribeBareCommandWithAnUnusableAnswerKeepsTheCommand(t *testing.T) {
	model := &scriptedTitleModel{reply: "/warm-up"}
	ts, _, ws := describeServer(t, model)
	if err := writeQuotedSkill(filepath.Join(ws, ".agents", "skills"), "warm-up", "Onboard onto a repository"); err != nil {
		t.Fatal(err)
	}
	_, short, _ := postDescribe(t, ts, "/warm-up", nil, nil)
	if short != "/warm-up" {
		t.Fatalf("short = %q, want the command as typed", short)
	}
}

// Settings commands configure the session and say nothing about the work: a
// first message made only of them names nothing, and the model is not asked.
func TestCoddyDescribeSettingsCommandsAloneNameNothing(t *testing.T) {
	model := &scriptedTitleModel{reply: "Plan mode switch\ntags: plan"}
	ts, _, _ := describeServer(t, model)
	status, short, tags := postDescribe(t, ts, "/plan\n/model openai/gpt-4o", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if short != "" || len(tags) != 0 {
		t.Fatalf("short = %q tags = %v, want nothing", short, tags)
	}
	if model.calls != 0 {
		t.Fatalf("the model was asked %d times about settings commands", model.calls)
	}
}

// Settings commands in front of a prompt are taken off it: the model is asked
// about the prompt alone.
func TestCoddyDescribeAsksAboutThePromptAfterTheSettingsCommands(t *testing.T) {
	model := &scriptedTitleModel{reply: "Refactor the memory API"}
	ts, _, _ := describeServer(t, model)
	_, short, _ := postDescribe(t, ts, "/plan\nRefactor the memory API please", nil, nil)
	if model.user != "Refactor the memory API please" {
		t.Fatalf("the model was asked about %q", model.user)
	}
	if short != "Refactor the memory API" {
		t.Fatalf("short = %q", short)
	}
}

// A slash word that names no command of the workspace is prose: a path, a
// fraction, an unknown name. The model is not told about any command.
func TestCoddyDescribeUnknownSlashWordIsNotACommand(t *testing.T) {
	model := &scriptedTitleModel{reply: "Missing binaries on the path"}
	ts, _, _ := describeServer(t, model)
	postDescribe(t, ts, "/usr/bin is missing from PATH", nil, nil)
	if strings.Contains(model.system, describeCommandsHeading) {
		t.Fatalf("an unknown slash word was described as a command:\n%s", model.system)
	}
}

// The commands come from the workspace of the session the header names when
// the server has it, whatever the cwd query says.
func TestCoddyDescribeReadsTheCommandsOfTheSessionWorkspace(t *testing.T) {
	model := &scriptedTitleModel{reply: "Issue triage by severity"}
	ts, mgr, ws := describeServer(t, model)
	sessionDir := filepath.Join(filepath.Dir(ws), "session-folder")
	other := filepath.Join(filepath.Dir(ws), "other")
	for _, d := range []string{sessionDir, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeQuotedSkill(filepath.Join(sessionDir, ".agents", "skills"), "triage", "Sort the open issues by severity"); err != nil {
		t.Fatal(err)
	}
	if err := writeQuotedSkill(filepath.Join(other, ".agents", "skills"), "triage", "Something else entirely"); err != nil {
		t.Fatal(err)
	}
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: sessionDir})
	if err != nil {
		t.Fatal(err)
	}
	postDescribe(t, ts, "/triage", map[string]string{"X-Coddy-Session-ID": res.SessionID}, url.Values{"cwd": {other}})
	if !strings.Contains(model.system, "- /triage: Sort the open issues by severity") {
		t.Fatalf("the session's own skill was not described:\n%s", model.system)
	}
}

// A folder the server cannot use never costs the chat its title: the call
// falls back to the default workspace instead of failing.
func TestCoddyDescribeUnusableFolderFallsBackToTheDefaultWorkspace(t *testing.T) {
	model := &scriptedTitleModel{reply: "Repository onboarding"}
	ts, _, ws := describeServer(t, model)
	if err := writeQuotedSkill(filepath.Join(ws, ".agents", "skills"), "warm-up", "Onboard onto a repository"); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{"relative/folder", filepath.Join(ws, "missing")} {
		model.system = ""
		status, short, _ := postDescribe(t, ts, "/warm-up", map[string]string{"X-Coddy-Session-ID": session.NewSessionID()}, url.Values{"cwd": {cwd}})
		if status != http.StatusOK || short != "Repository onboarding" {
			t.Fatalf("cwd %q: status %d short %q", cwd, status, short)
		}
		if !strings.Contains(model.system, "- /warm-up: Onboard onto a repository") {
			t.Fatalf("cwd %q: the default workspace's skill was not described:\n%s", cwd, model.system)
		}
	}
}

// A skill description is written for the agent that picks the skill and can
// run long; the title prompt carries a bounded slice of it, and of the
// commands a message names.
func TestCoddyDescribeBoundsTheCommandContext(t *testing.T) {
	model := &scriptedTitleModel{reply: "Several commands at once"}
	ts, _, ws := describeServer(t, model)
	dir := filepath.Join(ws, ".agents", "skills")
	long := strings.Repeat("word ", 400)
	names := []string{"one", "two", "three", "four", "five", "six", "seven"}
	for _, n := range names {
		if err := writeQuotedSkill(dir, n, long); err != nil {
			t.Fatal(err)
		}
	}
	postDescribe(t, ts, "/"+strings.Join(names, " /"), nil, nil)
	listed := 0
	for _, line := range strings.Split(model.system, "\n") {
		if strings.HasPrefix(line, "- /") {
			listed++
			if n := utf8.RuneCountInString(line); n > describeCommandDescriptionRunes+40 {
				t.Fatalf("a command line is %d runes long", n)
			}
		}
	}
	if listed != describeMaxCommands {
		t.Fatalf("%d commands described, want %d", listed, describeMaxCommands)
	}
}

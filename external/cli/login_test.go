//go:build cli

package cli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/providerlogin"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// /login refuses what it cannot sign in before anything starts: the refusal
// says why and what would work instead, and no sign-in is left running.
func TestLoginRefusals(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		prepare func(a *App)
		want    string
	}{
		{name: "a console attached to a server", text: "/login",
			prepare: func(a *App) { a.remoteURL = "https://coddy.example.test" },
			want:    "sign in on that server"},
		{name: "a second sign-in", text: "/login codex",
			prepare: func(a *App) { a.login = &loginRun{title: "NeuralDeep", cancel: func() {}} },
			want:    "A sign-in to NeuralDeep is already running"},
		{name: "two arguments", text: "/login codex devin", want: "Usage: /login [provider]"},
		{name: "a name config.yaml does not list", text: "/login work",
			want: `no provider "work" in config.yaml`},
		{name: "a row that takes a key", text: "/login mine",
			prepare: func(a *App) {
				a.config().Providers = []config.ProviderConfig{{Name: "mine", Type: "openai"}}
			},
			want: "MINE_API_KEY"},
		{name: "an acp row", text: "/login claude",
			prepare: func(a *App) {
				a.config().Providers = []config.ProviderConfig{{Name: "claude", Type: "acp", ACP: &config.ACPProviderConfig{Command: "claude-agent-acp"}}}
			},
			want: "signs in on its own"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			if tc.prepare != nil {
				tc.prepare(a)
			}
			before := a.login
			if !a.dispatchSlash(tc.text) {
				t.Fatalf("%q is the console's own command", tc.text)
			}
			if got := transcriptText(a); !strings.Contains(strings.Join(strings.Fields(got), " "), tc.want) {
				t.Fatalf("transcript %q does not say %q", got, tc.want)
			}
			if a.login != before {
				t.Fatal("a refused /login started a sign-in")
			}
		})
	}
}

// A bare /login is a sign-in to NeuralDeep; a name picks another provider.
func TestLoginTarget(t *testing.T) {
	for args, want := range map[string]string{"": "neuraldeep", "codex": "codex", "devin": "devin", "opencode": "opencode"} {
		var fields []string
		if args != "" {
			fields = []string{args}
		}
		if got := loginTarget(fields); got != want {
			t.Fatalf("/login %s signs in to %q, want %q", args, got, want)
		}
	}
}

// newManagedTestApp is newTestApp over a real manager, for the paths that
// read the session (the header, the slash catalog). The configuration has
// the paths a sign-in writes config.yaml to.
func newManagedTestApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	cfg := a.config()
	cfg.Paths.ConfigPath = filepath.Join(cfg.Paths.Home, "config.yaml")
	a.mgr = session.NewManager(cfg, nil, nil, slog.New(slog.DiscardHandler), cfg.Paths.CWD, &session.FileStore{Root: t.TempDir()})
	t.Cleanup(func() {
		a.Close()
		a.JoinWorkers(3 * time.Second)
	})
	return a
}

func TestSlashCatalogOffersLoginAndExit(t *testing.T) {
	a := newManagedTestApp(t)
	var names []string
	for _, it := range a.slashCatalog() {
		names = append(names, it.Value)
	}
	for _, want := range []string{"login", "exit", "quit"} {
		if !slices.Contains(names, want) {
			t.Fatalf("the slash menu lacks /%s: %v", want, names)
		}
	}
}

// The header has no line about ACP and HTTP any more; the one line under the
// hints is the /login pointer, and only while no provider can answer.
func TestHeaderShowsTheLoginHintOnlyWhenAsked(t *testing.T) {
	h := newHeader(newTheme("dark"))
	text := strings.Join(keysOf(renderedRows(h, 200)), "\n")
	if strings.Contains(text, "ACP and HTTP") || strings.Contains(text, "/login") {
		t.Fatalf("a set-up console carries no extra line:\n%s", text)
	}
	h.SetLoginHint(true)
	text = strings.Join(keysOf(renderedRows(h, 200)), "\n")
	if !strings.Contains(text, "Type /login to sign in to NeuralDeep") {
		t.Fatalf("a fresh install points at /login:\n%s", text)
	}
	h.SetLoginHint(false)
	if text := strings.Join(keysOf(renderedRows(h, 200)), "\n"); strings.Contains(text, "/login") {
		t.Fatalf("the hint goes once a provider is set up:\n%s", text)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// needsLogin is what decides the header hint and the line after a failed
// turn: a local console whose configuration has no provider that can answer.
func TestNeedsLogin(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	a := newTestApp(t)
	if !a.needsLogin() {
		t.Fatal("a console with no config.yaml needs a sign-in")
	}
	a.config().Providers = []config.ProviderConfig{{Name: "openai", Type: "openai"}}
	if !a.needsLogin() {
		t.Fatal("the example config with its key unset still needs a sign-in")
	}
	a.config().Providers[0].APIKey = "sk-set"
	if a.needsLogin() {
		t.Fatal("a provider with a key answers")
	}
	a.config().Providers = nil
	a.remoteURL = "https://coddy.example.test"
	if a.needsLogin() {
		t.Fatal("over --remote the server's configuration decides, not this one")
	}
}

// Escape stops a sign-in that waits for the browser, before it would
// interrupt anything else.
func TestEscapeStopsTheSignIn(t *testing.T) {
	a := newTestApp(t)
	stopped := false
	a.login = &loginRun{title: "Codex", cancel: func() { stopped = true }}
	if !a.handleGlobalKey([]byte("\x1b")) {
		t.Fatal("escape is taken while a sign-in runs")
	}
	if !stopped {
		t.Fatal("escape did not stop the sign-in")
	}
}

// A Devin sign-in completes from the address its browser was sent back to:
// pasted into the editor and sent, it goes to the sign-in, not to the model.
func TestPastedCallbackGoesToTheSignIn(t *testing.T) {
	a := newTestApp(t)
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	a.login = &loginRun{title: "Devin", cancel: func() {}, paste: w}
	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(r)
		if sc.Scan() {
			got <- sc.Text()
		}
	}()
	callback := "http://127.0.0.1:43123/callback?code=abc&state=xyz"
	a.onSubmit(`"` + callback + `"`)
	select {
	case line := <-got:
		if line != `"`+callback+`"` {
			t.Fatalf("the sign-in read %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the pasted address never reached the sign-in")
	}
	if strings.Contains(transcriptText(a), "Turn failed") {
		t.Fatal("the address was sent to the model")
	}
	// Every form the sign-in itself takes reaches it; a sentence does not.
	for _, pasted := range []string{callback, `"` + callback + `"`, "127.0.0.1:43123/callback?code=abc&state=xyz", "code=abc&state=xyz"} {
		if !looksLikeCallback(pasted) {
			t.Fatalf("%q is a callback the sign-in takes", pasted)
		}
	}
	for _, typed := range []string{"what does code=1 mean here?", "fix the login page", ""} {
		if looksLikeCallback(typed) {
			t.Fatalf("%q is a prompt, not a callback", typed)
		}
	}
}

// What a finished sign-in prints: who signed in, what config.yaml gained or
// why it could not, and a stop or a failure in words.
func TestFinishLoginReports(t *testing.T) {
	a := newManagedTestApp(t)
	run := &loginRun{title: "NeuralDeep", cancel: func() {}}
	a.login = run
	a.finishLogin(run, providerlogin.Result{
		Provider: "neuraldeep", Type: "neuraldeep", Account: "dev (tier pro)",
		Added: []string{"provider neuraldeep", "model neuraldeep/a", "agent.model neuraldeep/a"},
	}, nil, nil)
	text := strings.Join(strings.Fields(transcriptText(a)), " ")
	for _, want := range []string{"Signed in to NeuralDeep as dev (tier pro)", "Added to config.yaml: provider neuraldeep, agent.model neuraldeep/a, model neuraldeep/a"} {
		if !strings.Contains(text, want) {
			t.Fatalf("transcript %q lacks %q", text, want)
		}
	}
	if a.login != nil {
		t.Fatal("a finished sign-in is not running any more")
	}

	b := newManagedTestApp(t)
	run = &loginRun{title: "Codex", cancel: func() {}}
	b.login = run
	b.finishLogin(run, providerlogin.Result{Provider: "codex", Type: "codex"}, nil, nil)
	if !strings.Contains(transcriptText(b), "config.yaml already lists") {
		t.Fatalf("a sign-in that added nothing says so: %q", transcriptText(b))
	}

	c := newManagedTestApp(t)
	run = &loginRun{title: "Codex", cancel: func() {}}
	c.login = run
	c.finishLogin(run, providerlogin.Result{Provider: "codex", Type: "codex", ConfigErr: errors.New("disk full")}, nil, nil)
	if !strings.Contains(transcriptText(c), "disk full") {
		t.Fatalf("a config write that failed is named: %q", transcriptText(c))
	}

	d := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run = &loginRun{title: "Devin", cancel: cancel, ctx: ctx}
	d.login = run
	d.finishLogin(run, providerlogin.Result{}, context.Canceled, nil)
	if !strings.Contains(transcriptText(d), "Sign-in to Devin stopped") {
		t.Fatalf("a stopped sign-in says so: %q", transcriptText(d))
	}

	e := newManagedTestApp(t)
	run = &loginRun{title: "OpenCode", cancel: func() {}}
	e.login = run
	e.finishLogin(run, providerlogin.Result{}, errors.New("device code expired"), nil)
	if !strings.Contains(transcriptText(e), "Sign-in to OpenCode failed: device code expired") {
		t.Fatalf("a failed sign-in names the reason: %q", transcriptText(e))
	}
}

// A sign-in that added nothing - config.yaml listed the provider already, the
// login was all it lacked - still takes the /login line out of the header.
func TestFinishLoginRefreshesTheHeaderWhenNothingWasAdded(t *testing.T) {
	t.Setenv("NEURALDEEP_API_KEY", "")
	a := newManagedTestApp(t)
	cfg := a.config()
	cfg.Providers = []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep"}}
	cfg.Models = []config.ModelEntry{{Model: "neuraldeep/qwen3.8-27b"}}
	a.populateHeader()
	if !a.header.loginHint {
		t.Fatal("a row nobody signed in leaves the console without a provider")
	}
	if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(cfg.Paths.Home, "neuraldeep"), "sk-stored-0123456789abcdef", "https://hub.invalid", llm.NeuralDeepClientID, "coddy"); err != nil {
		t.Fatal(err)
	}
	run := &loginRun{title: "NeuralDeep", cancel: func() {}}
	a.login = run
	a.finishLogin(run, providerlogin.Result{Provider: "neuraldeep", Type: "neuraldeep"}, nil, nil)
	if a.header.loginHint {
		t.Fatal("the header still points at /login after the sign-in")
	}
}

// The example config install.sh writes keeps agent.model on its openai row;
// after a sign-in the session must not stay on a model no request reaches.
func TestFinishLoginMovesTheSessionOffAProviderThatCannotAnswer(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("NEURALDEEP_API_KEY", "")
	a := newManagedTestApp(t)
	cfg := a.config()
	cfg.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai"}, {Name: "neuraldeep", Type: "neuraldeep"}}
	cfg.Models = []config.ModelEntry{{Model: "openai/gpt-5.6-terra"}, {Model: "neuraldeep/qwen3.8-27b"}, {Model: "neuraldeep/gpt-oss-120b"}}
	cfg.Agent.Model = "openai/gpt-5.6-terra"
	if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(cfg.Paths.Home, "neuraldeep"), "sk-stored-0123456789abcdef", "https://hub.invalid", llm.NeuralDeepClientID, "coddy"); err != nil {
		t.Fatal(err)
	}
	a.modelID = "openai/gpt-5.6-terra"
	run := &loginRun{title: "NeuralDeep", cancel: func() {}, modelBefore: a.modelID}
	a.login = run
	a.finishLogin(run, providerlogin.Result{Provider: "neuraldeep", Type: "neuraldeep", Added: []string{"model neuraldeep/qwen3.8-27b"}}, nil, nil)
	if a.modelID != "neuraldeep/qwen3.8-27b" {
		t.Fatalf("the session stayed on %q", a.modelID)
	}
	if text := transcriptText(a); !strings.Contains(text, "This session runs on neuraldeep/qwen3.8-27b now") {
		t.Fatalf("the switch is not said: %q", text)
	}

	// A session on a provider that answers keeps its model.
	b := newManagedTestApp(t)
	cfg = b.config()
	cfg.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "sk-set"}, {Name: "neuraldeep", Type: "neuraldeep"}}
	cfg.Models = []config.ModelEntry{{Model: "openai/gpt-5.6-terra"}, {Model: "neuraldeep/qwen3.8-27b"}}
	b.modelID = "openai/gpt-5.6-terra"
	run = &loginRun{title: "NeuralDeep", cancel: func() {}}
	b.login = run
	b.finishLogin(run, providerlogin.Result{Provider: "neuraldeep", Type: "neuraldeep", Added: []string{"model neuraldeep/qwen3.8-27b"}}, nil, nil)
	if b.modelID != "openai/gpt-5.6-terra" || !strings.Contains(transcriptText(b), "ctrl+l or /model picks one of its models") {
		t.Fatalf("a working session was moved (%q) or not told how to switch: %q", b.modelID, transcriptText(b))
	}
}

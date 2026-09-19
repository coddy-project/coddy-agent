//go:build cli

package cli

// Godog harness for features/cli_prompt_input.feature: cli.Run runs in this
// process the way `coddy -p ...` would, with a pipe or a file as its stdin,
// against a scripted OpenAI-compatible model (internal/tgfake/llmstub) that
// records every request it answers. The error paths are unit tests below the
// suite: each asserts that the model was never asked.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake/llmstub"
)

const promptInputAnswer = "stub answer for the one-shot run"

// recordingModel is the scripted model plus the bodies of the completion
// requests it was sent.
type recordingModel struct {
	ts       *httptest.Server
	mu       sync.Mutex
	requests [][]byte
}

func newRecordingModel() *recordingModel {
	m := &recordingModel{}
	stub := &llmstub.Server{Model: "coddy-demo", Answers: []string{promptInputAnswer}}
	inner := stub.Handler()
	m.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions") {
			body, _ := io.ReadAll(r.Body)
			m.mu.Lock()
			m.requests = append(m.requests, body)
			m.mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		inner.ServeHTTP(w, r)
	}))
	return m
}

func (m *recordingModel) calls() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]byte(nil), m.requests...)
}

// userMessages is the content of every user message the model was sent,
// flattened from a string or an array of text parts.
func (m *recordingModel) userMessages() []string {
	var out []string
	for _, body := range m.calls() {
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(body, &req) != nil {
			continue
		}
		for _, msg := range req.Messages {
			if msg.Role != "user" {
				continue
			}
			var s string
			if json.Unmarshal(msg.Content, &s) == nil {
				out = append(out, s)
				continue
			}
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(msg.Content, &parts) == nil {
				var b strings.Builder
				for _, p := range parts {
					b.WriteString(p.Text)
				}
				out = append(out, b.String())
			}
		}
	}
	return out
}

// promptInputRun is one `coddy ...` run inside the test process.
type promptInputRun struct {
	home, work string
	model      *recordingModel
	stdin      *os.File
	stdout     syncBuffer
	stderr     syncBuffer
	err        error
	// sent is what the scenario expects the model to receive.
	piped, fileBody string
}

// looseTempDir is a temporary directory removed on a best-effort basis: a run
// in this process may still hold a file of its home open when the test ends,
// which Windows refuses to delete and t.TempDir reports as a failure.
func looseTempDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "coddy-prompt-input-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newPromptInputRun(t testing.TB) *promptInputRun {
	t.Helper()
	r := &promptInputRun{home: looseTempDir(t), work: looseTempDir(t), model: newRecordingModel()}
	t.Cleanup(r.model.ts.Close)
	cfg := fmt.Sprintf(`providers:
  - name: stub
    type: openai
    api_base: "%s/v1"
    api_key: "sk-stub"
models:
  - model: stub/coddy-demo
    max_context_tokens: 131072
agent:
  model: stub/coddy-demo
`, r.model.ts.URL)
	if err := os.WriteFile(filepath.Join(r.home, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devNull.Close() })
	r.stdin = devNull
	return r
}

// pipeStdin makes stdin a pipe that carries data and closes.
func (r *promptInputRun) pipeStdin(t testing.TB, data string) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = io.WriteString(pw, data)
		_ = pw.Close()
	}()
	t.Cleanup(func() { _ = pr.Close() })
	r.stdin = pr
	r.piped = data
}

func (r *promptInputRun) run(args ...string) error {
	full := append([]string{"--home", r.home, "--cwd", r.work, "--log-file", filepath.Join(r.home, "cli.log")}, args...)
	r.err = Run(full, CommandDeps{
		OpenStore: serve.OpenSessionStore,
		Stdin:     r.stdin,
		Stdout:    &r.stdout,
		Stderr:    &r.stderr,
	})
	return r.err
}

// splitCommandLine splits `coddy -p 'two words'` the way a shell would for
// the quoting the scenarios use, and drops the program name.
func splitCommandLine(line string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var quote rune
	inArg := false
	for _, c := range line {
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(c)
		case c == '\'' || c == '"':
			quote, inArg = c, true
		case c == ' ':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(c)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote in %q", line)
	}
	if inArg {
		args = append(args, cur.String())
	}
	if len(args) == 0 || args[0] != "coddy" {
		return nil, fmt.Errorf("the command line must start with coddy: %q", line)
	}
	return args[1:], nil
}

// scenarioT adapts godog's scenario to the helpers above, which only need
// Fatal and Cleanup.
type scenarioT struct {
	testing.TB
	cleanups []func()
}

func (s *scenarioT) Helper()                   {}
func (s *scenarioT) Cleanup(f func())          { s.cleanups = append(s.cleanups, f) }
func (s *scenarioT) Fatal(args ...any)         { panic(fmt.Sprint(args...)) }
func (s *scenarioT) Fatalf(f string, a ...any) { panic(fmt.Sprintf(f, a...)) }

func (s *scenarioT) close() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
}

type promptInputScenario struct {
	t   *scenarioT
	run *promptInputRun
}

func unescapeStep(s string) string {
	return strings.NewReplacer(`\r`, "\r", `\n`, "\n", `\t`, "\t").Replace(s)
}

func (s *promptInputScenario) aHomeWithARecordingModel() error {
	s.run = newPromptInputRun(s.t)
	return nil
}

func (s *promptInputScenario) stdinIsAPipeCarrying(data string) error {
	s.run.pipeStdin(s.t, unescapeStep(data))
	return nil
}

func (s *promptInputScenario) aLargePromptFile(name string, kib int) error {
	var b strings.Builder
	for b.Len() < kib<<10 {
		b.WriteString("Строка брифа with «Unicode», \"double\" and 'single' quotes, $HOME and `ticks`\r\n")
	}
	b.WriteString("\n\n")
	s.run.fileBody = b.String()
	return os.WriteFile(filepath.Join(s.run.work, name), []byte(s.run.fileBody), 0o644)
}

func (s *promptInputScenario) workspaceHoldsFile(name, body string) error {
	return os.WriteFile(filepath.Join(s.run.work, name), []byte(body), 0o644)
}

func (s *promptInputScenario) operatorRuns(line string) error {
	args, err := splitCommandLine(line)
	if err != nil {
		return err
	}
	// A relative -i resolves against the process directory, as in a shell.
	prev, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(s.run.work); err != nil {
		return err
	}
	defer func() { _ = os.Chdir(prev) }()
	_ = s.run.run(args...)
	return nil
}

func (s *promptInputScenario) runEndsCleanly() error {
	if s.run.err != nil {
		return fmt.Errorf("run failed: %v\nstderr:\n%s", s.run.err, s.run.stderr.String())
	}
	if got := s.run.stdout.String(); !strings.Contains(got, promptInputAnswer) {
		return fmt.Errorf("stdout lacks the answer: %q", got)
	}
	return nil
}

func (s *promptInputScenario) modelReceivedPrompt(want string) error {
	for _, msg := range s.run.model.userMessages() {
		if msg == want {
			return nil
		}
	}
	return fmt.Errorf("no user message equals the %d-byte prompt; got %d user messages, first 200 bytes of each: %q",
		len(want), len(s.run.model.userMessages()), heads(s.run.model.userMessages(), 200))
}

func (s *promptInputScenario) modelReceivedPipedPrompt() error {
	return s.modelReceivedPrompt(s.run.piped)
}

func (s *promptInputScenario) modelReceivedFilePrompt() error {
	return s.modelReceivedPrompt(s.run.fileBody)
}

func (s *promptInputScenario) modelReceivedTextAndAttachment(text string) error {
	want := text + "\n\n" + mention.Attachment{Kind: mention.KindStdin, Path: session.StdinAttachmentPath, Body: s.run.piped}.XML()
	return s.modelReceivedPrompt(want)
}

func (s *promptInputScenario) noRequestCarries(secret string) error {
	for _, body := range s.run.model.calls() {
		if bytes.Contains(body, []byte(secret)) {
			return fmt.Errorf("a request carried %q", secret)
		}
	}
	if len(s.run.model.calls()) == 0 {
		return fmt.Errorf("the model was never asked")
	}
	return nil
}

func (s *promptInputScenario) transcriptShows(text string) error {
	store := &session.FileStore{Root: filepath.Join(s.run.home, "sessions")}
	entries, err := os.ReadDir(store.Root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "sess_") {
			continue
		}
		snap, err := store.ReadSnapshot(e.Name())
		if err != nil {
			return err
		}
		for _, msg := range snap.Messages {
			if msg.Role == "user" && mention.ForDisplay(msg.Content) == text+"\n\n"+mention.StdinLabel {
				return nil
			}
		}
	}
	return fmt.Errorf("no persisted user message displays as %q + %q", text, mention.StdinLabel)
}

func heads(msgs []string, n int) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		if len(m) > n {
			m = m[:n]
		}
		out[i] = m
	}
	return out
}

func initializePromptInputScenario(sc *godog.ScenarioContext) {
	s := &promptInputScenario{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.t = &scenarioT{}
		s.run = nil
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.t.close()
		return ctx, nil
	})
	sc.Step(`^a coddy home whose model records every request it answers$`, s.aHomeWithARecordingModel)
	sc.Step(`^stdin is a pipe carrying "([^"]*)"$`, s.stdinIsAPipeCarrying)
	sc.Step(`^a prompt file "([^"]*)" of (\d+) KiB with Unicode, quotes, CRLF line ends and trailing newlines$`, s.aLargePromptFile)
	sc.Step(`^the workspace holds a file "([^"]*)" reading "([^"]*)"$`, s.workspaceHoldsFile)
	sc.Step(`^the operator runs "(.*)"$`, s.operatorRuns)
	sc.Step(`^the run ends cleanly and prints the model's answer$`, s.runEndsCleanly)
	sc.Step(`^the model received the piped text as the prompt, byte for byte$`, s.modelReceivedPipedPrompt)
	sc.Step(`^the model received the prompt file as the prompt, byte for byte$`, s.modelReceivedFilePrompt)
	sc.Step(`^the model received "([^"]*)" followed by the piped data as a stdin attachment$`, s.modelReceivedTextAndAttachment)
	sc.Step(`^no request to the model carries "([^"]*)"$`, s.noRequestCarries)
	sc.Step(`^the session transcript shows "([^"]*)" and the stdin label$`, s.transcriptShows)
}

func TestCLIPromptInputFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cli-prompt-input",
		ScenarioInitializer: initializePromptInputScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/cli_prompt_input.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cli prompt input feature suite failed")
	}
}

// Input that cannot be sent stops the run before the model is asked, before
// a session exists, and before the home directory is even created.
func TestPromptInputRefusalsAskNoModel(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"not UTF-8", []string{"-p", "-"}, "ok\xff", "not UTF-8"},
		{"a NUL byte", []string{"-p", "review"}, "a\x00b", "NUL"},
		{"two prompts", []string{"-p", "text", "-i", "brief.md"}, "", "pass one of them"},
		{"a missing file", []string{"-i", "missing.md"}, "", "missing.md"},
		{"an empty -p", []string{"-p", ""}, "", "empty prompt"},
		{"an unquoted prompt", []string{"-p", "fix", "the", "bug"}, "", `unexpected argument "the"`},
		{"over the limit", []string{"-p", "-"}, strings.Repeat("x", MaxPromptInputBytes+1), "limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newPromptInputRun(t)
			// A home that does not exist yet stays that way.
			r.home = filepath.Join(t.TempDir(), "never-created")
			if tc.stdin != "" {
				r.pipeStdin(t, tc.stdin)
			}
			t.Chdir(r.work)
			err := r.run(tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
			if n := len(r.model.calls()); n != 0 {
				t.Fatalf("the model was asked %d times", n)
			}
			if _, statErr := os.Stat(r.home); !os.IsNotExist(statErr) {
				t.Fatalf("the home was created for a run that could not start: %v", statErr)
			}
		})
	}
}

// A relative -i names a file from where the command runs, as any shell
// argument does; --cwd moves the session, not the path.
func TestPromptFileResolvesAgainstTheProcessDirectory(t *testing.T) {
	r := newPromptInputRun(t)
	here := looseTempDir(t)
	if err := os.WriteFile(filepath.Join(here, "brief.md"), []byte("from the process directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.work, "brief.md"), []byte("from the session workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)
	if err := r.run("-i", "brief.md"); err != nil {
		t.Fatalf("run: %v\n%s", err, r.stderr.String())
	}
	found := false
	for _, msg := range r.model.userMessages() {
		if msg == "from the process directory\n" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the prompt did not come from the process directory: %q", heads(r.model.userMessages(), 80))
	}
}

// Blank piped data is no data: the prompt goes alone, and nothing named
// "stdin" is looked up on disk.
func TestBlankStdinRunsThePromptAlone(t *testing.T) {
	r := newPromptInputRun(t)
	r.pipeStdin(t, "\n  \n")
	if err := r.run("-p", "hello"); err != nil {
		t.Fatalf("run: %v\n%s", err, r.stderr.String())
	}
	for _, msg := range r.model.userMessages() {
		if strings.Contains(msg, "coddy_attachment") {
			t.Fatalf("blank stdin was attached: %q", msg)
		}
	}
	if strings.Contains(r.stderr.String(), "attached") {
		t.Fatalf("stderr claims an attachment: %q", r.stderr.String())
	}
}

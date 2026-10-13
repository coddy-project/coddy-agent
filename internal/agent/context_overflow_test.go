package agent

// A request the provider refused as larger than the model's context window
// (context_overflow.go): the explanation a turn ends with, the limit the next
// request has to fit under, and the recovery that compacts the turn and asks the
// same step again, once. The happy paths are in
// features/context_compaction.feature (the turn that ends with the explanation)
// and features/context_in_turn_compaction.feature (the recovery).

import (
	"bytes"
	"context"
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

	"github.com/openai/openai-go"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestContextOverflowMessage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		estimated int
		window    int
		source    string
		detail    llm.OverflowDetail
		compacted bool
		want      []string
		notWant   []string
	}{
		{
			name: "the turn was compacted and the smaller request was refused too", estimated: 31000, window: 49152, source: session.ContextWindowFromConfig,
			detail: llm.OverflowDetail{Prompt: 51402, Limit: 49152}, compacted: true,
			want: []string{
				"context window exceeded", "the provider counted 51402 tokens against a limit of 49152",
				"Coddy compacted this turn and asked again, but the provider refused the smaller request too", "run /compact",
			},
		},
		{
			name: "everything known and the provider agrees with the window", estimated: 38000, window: 49152, source: session.ContextWindowFromConfig,
			detail: llm.OverflowDetail{Prompt: 51402, Limit: 49152},
			want: []string{
				"context window exceeded", "Coddy estimated about 38000 tokens", "the window is 49152 tokens",
				"the provider counted 51402 tokens against a limit of 49152", "run /compact", "start a new session", "split the task",
			},
			notWant: []string{"max_context_tokens"},
		},
		{
			name: "the provider's limit is lower than the window", estimated: 30000, window: 128000, source: session.ContextWindowFromConfig,
			detail: llm.OverflowDetail{Prompt: 36000, Limit: 32768},
			want:   []string{"the window is 128000 tokens", "against a limit of 32768", "set models[].max_context_tokens to 32768"},
		},
		{
			name: "the window was never set", estimated: 40000, window: 128000, source: session.ContextWindowDefault,
			want:    []string{"the window is assumed to be 128000 tokens", "set models[].max_context_tokens to the window the provider serves"},
			notWant: []string{"the provider counted"},
		},
		{
			name: "a limit without the prompt size", estimated: 1000, window: 4096, source: session.ContextWindowFromProvider,
			detail:  llm.OverflowDetail{Limit: 4096},
			want:    []string{"the provider's limit is 4096 tokens"},
			notWant: []string{"counted", "set models[].max_context_tokens"},
		},
		{
			name: "nothing is known", want: []string{"context window exceeded", "run /compact"},
			notWant: []string{"()", "estimated", "the window is", "max_context_tokens", "compacted"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := contextOverflowMessage(tc.estimated, tc.window, tc.source, tc.detail, tc.compacted)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("message lacks %q: %s", w, got)
				}
			}
			for _, n := range tc.notWant {
				if strings.Contains(got, n) {
					t.Errorf("message holds %q: %s", n, got)
				}
			}
		})
	}
}

// The refusal of an MLX swap proxy, as coddy logged it: the prompt was 30 tokens
// past a window the config named correctly, so the message is the one that
// shows the figures side by side and does not tell anyone to change the window.
func TestContextOverflowMessageOfAnMLXSwapProxyReadsAsAnExplanation(t *testing.T) {
	got := contextOverflowMessage(47500, 49152, session.ContextWindowFromConfig, llm.OverflowDetail{Prompt: 49182, Limit: 49152}, false)
	want := "context window exceeded: the provider refused the request because it does not fit the model's context window " +
		"(Coddy estimated about 47500 tokens; the window is 49152 tokens; the provider counted 49182 tokens against a limit of 49152); " +
		"run /compact, start a new session or split the task into smaller steps"
	if got != want {
		t.Fatalf("message =\n%s\nwant\n%s", got, want)
	}
}

func TestRunExplainsTheContextLimitRefusalOfAnMLXSwapProxy(t *testing.T) {
	cause := errors.New(`provider "mlx" (http://192.0.2.10:8080/v1): openai stream: POST "http://192.0.2.10:8080/v1/chat/completions": 404 Not Found "Context limit is 49152 tokens; prompt=49182 leaves 0 output tokens, below the minimum 16"`)
	provider := &failingStreamProvider{err: cause}
	ag := overflowRunAgent(t, provider, 49152)

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "resume"}})
	if err == nil || stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop=%q err=%v, want the turn refused with an explanation", stop, err)
	}
	for _, want := range []string{
		"context window exceeded", "the window is 49152 tokens",
		"the provider counted 49182 tokens against a limit of 49152", "run /compact",
		"Context limit is 49152 tokens", // the provider's own words stay
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "max_context_tokens") {
		t.Errorf("the window already matches the provider's limit, yet the error advises changing it: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider asked %d times, want once", provider.calls)
	}
}

// failingStreamProvider refuses every request with err.
type failingStreamProvider struct {
	err   error
	calls int
}

func (p *failingStreamProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, errors.New("not used")
}

func (p *failingStreamProvider) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	return nil, p.err
}

func overflowRunAgent(t *testing.T, provider llm.Provider, window int) *Agent {
	t.Helper()
	st := &session.State{ID: "sess_overflow", CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: t.TempDir()}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: window}},
		Agent:     config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	return ag
}

func TestRunExplainsAContextOverflowAndKeepsTheProvidersAnswer(t *testing.T) {
	cause := errors.New(`provider "local" (http://127.0.0.1:8080/v1): openai stream: POST "http://127.0.0.1:8080/v1/chat/completions": 404 Not Found "Context limit is 49152 tokens; prompt=51402 leaves 0 output tokens, below the minimum 16"`)
	provider := &failingStreamProvider{err: cause}
	ag := overflowRunAgent(t, provider, 49152)

	stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "resume"}})
	if err == nil {
		t.Fatal("expected the refusal to end the turn with an error")
	}
	if stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop = %q, want refused", stop)
	}
	for _, want := range []string{
		"context window exceeded", "the window is 49152 tokens", "against a limit of 49152", "/compact",
		"Context limit is 49152 tokens", // the provider's own words stay
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.HasPrefix(err.Error(), "LLM error:") {
		t.Errorf("the bare provider error is back: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Error("the cause is no longer reachable, so a surface cannot read the status or classify the error")
	}
	if !llm.IsContextOverflow(err) {
		t.Error("a surface cannot tell the turn ended on an overflow")
	}
	if provider.calls != 1 {
		t.Fatalf("provider asked %d times, want once: a request that does not fit is not retried", provider.calls)
	}
}

func TestRunKeepsTheBareLLMErrorForOtherRefusals(t *testing.T) {
	for _, cause := range []error{
		errors.New("401 Unauthorized: Incorrect API key provided"),
		errors.New("400 Bad Request: Invalid schema for function 'read'"),
	} {
		provider := &failingStreamProvider{err: cause}
		ag := overflowRunAgent(t, provider, 49152)
		stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "resume"}})
		if err == nil || stop != string(acp.StopReasonRefused) {
			t.Fatalf("%v: stop=%q err=%v", cause, stop, err)
		}
		if got, want := err.Error(), "LLM error: "+cause.Error(); got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
	}
}

// A client of the HTTP API reads the provider's status off the error the turn
// ended with (external/httpserver/upstream_error.go), so the explanation keeps
// the typed error in its chain.
func TestRunKeepsTheProvidersStatusOnAnOverflow(t *testing.T) {
	var apiErr openai.Error
	body := `"Context limit is 49152 tokens; prompt=51402 leaves 0 output tokens, below the minimum 16"`
	if err := apiErr.UnmarshalJSON([]byte(body)); err != nil {
		t.Fatal(err)
	}
	apiErr.StatusCode = http.StatusNotFound
	apiErr.Request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/v1/chat/completions", nil)
	apiErr.Response = &http.Response{StatusCode: http.StatusNotFound}
	provider := &failingStreamProvider{err: fmt.Errorf("provider %q: openai stream: %w", "local", &apiErr)}
	ag := overflowRunAgent(t, provider, 49152)

	_, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "resume"}})
	if err == nil || !strings.HasPrefix(err.Error(), "context window exceeded") {
		t.Fatalf("error = %v", err)
	}
	if got := llm.UpstreamStatus(err); got != http.StatusNotFound {
		t.Fatalf("UpstreamStatus = %d, want the 404 the provider answered", got)
	}
	if !llm.IsContextOverflow(err) {
		t.Fatal("the typed error no longer classifies as an overflow")
	}
}

// apiRefusal is the error the OpenAI client builds for an answer of the given
// status: a typed error whose body, when it has an "error" object, is parsed.
// Without one it carries nothing, as it does for an nginx page.
func apiRefusal(t *testing.T, status int, body string) error {
	t.Helper()
	var apiErr openai.Error
	if body != "" {
		if err := apiErr.UnmarshalJSON([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	apiErr.StatusCode = status
	apiErr.Request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/v1/chat/completions", nil)
	apiErr.Response = &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
	return fmt.Errorf("openai complete: %w", &apiErr)
}

// --- the limit a refused request has to fit under ---------------------------

func TestRecoveryLimitTokens(t *testing.T) {
	for _, tc := range []struct {
		name     string
		window   int
		detail   llm.OverflowDetail
		estimate int
		want     int
	}{
		{name: "no figures: the estimate of the refused request is the ceiling", window: 128000, estimate: 40000, want: 40000},
		{name: "no figures and no estimate: the window", window: 49152, want: 49152},
		{name: "the provider's limit below the window, estimate agrees", window: 128000,
			detail: llm.OverflowDetail{Prompt: 51402, Limit: 49152}, estimate: 51402, want: 49152},
		{name: "the provider counts denser than Coddy: the limit is scaled to Coddy's units", window: 128000,
			detail: llm.OverflowDetail{Prompt: 51402, Limit: 49152}, estimate: 40000, want: 38249},
		{name: "a provider that counts fewer tokens than Coddy is not trusted to", window: 128000,
			detail: llm.OverflowDetail{Prompt: 30000, Limit: 49152}, estimate: 40000, want: 40000},
		{name: "the limit without the prompt size", window: 128000,
			detail: llm.OverflowDetail{Limit: 32768}, estimate: 40000, want: 32768},
		{name: "a limit above the window never raises it", window: 20000,
			detail: llm.OverflowDetail{Prompt: 25000, Limit: 49152}, estimate: 0, want: 20000},
		{name: "nothing known", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := recoveryLimitTokens(tc.window, tc.detail, tc.estimate); got != tc.want {
				t.Fatalf("limit = %d, want %d", got, tc.want)
			}
		})
	}
}

// --- the recovery --------------------------------------------------------------

// errRecoveryRefusal is what a local backend answers a request that does not fit.
var errRecoveryRefusal = errors.New(`provider "local" (http://127.0.0.1:8080/v1): openai stream: POST "http://127.0.0.1:8080/v1/chat/completions": 404 Not Found "Context limit is 49152 tokens; prompt=51402 leaves 0 output tokens, below the minimum 16"`)

// recoveryProvider serves a scripted turn of read steps and refuses the requests
// refuse names as too large. Complete answers a summarization request.
type recoveryProvider struct {
	steps []evStep
	next  int // the next scripted step to serve
	calls int // Stream calls, the refused ones included
	// refuse reports the error call number n (0-based) is refused with, or nil.
	refuse func(n int) error
	// textBeforeRefusal makes a refused call emit a chunk of text first, the way
	// a stream that broke after it began.
	textBeforeRefusal bool

	seen     [][]llm.Message // requests that were answered
	refused  [][]llm.Message // requests that were refused
	summary  int
	onFold   func()
	foldFail error
}

func (p *recoveryProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	if p.onFold != nil {
		p.onFold()
	}
	if p.foldFail != nil {
		return nil, p.foldFail
	}
	p.summary++
	return &llm.Response{Content: fmt.Sprintf("FOLD-%d: the work so far", p.summary), StopReason: "end_turn"}, nil
}

func (p *recoveryProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	n := p.calls
	p.calls++
	if p.refuse != nil {
		if err := p.refuse(n); err != nil {
			p.refused = append(p.refused, append([]llm.Message(nil), messages...))
			if p.textBeforeRefusal {
				onChunk(llm.StreamChunk{TextDelta: "Let me look at "})
			}
			return nil, err
		}
	}
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	var step evStep
	if p.next < len(p.steps) {
		step = p.steps[p.next]
	} else {
		step = evStep{text: "done"}
	}
	p.next++
	if len(step.calls) > 0 {
		return &llm.Response{ToolCalls: step.calls, StopReason: "tool_use"}, nil
	}
	if step.text == "" {
		step.text = "done"
	}
	onChunk(llm.StreamChunk{TextDelta: step.text})
	return &llm.Response{Content: step.text, StopReason: "end_turn"}, nil
}

// readSteps is n steps of one read each, every one of a file of its own (the
// loop guard stops a model that repeats a call), then the model's answer.
func readSteps(n int) []evStep {
	var steps []evStep
	for i := 0; i < n; i++ {
		steps = append(steps, evStep{calls: []llm.ToolCall{tcRead(fmt.Sprintf("r%d", i+1), fmt.Sprintf("notes_%02d.txt", i+1), 1, 200)}})
	}
	return append(steps, evStep{text: "all read"})
}

// recoveryRefusalOf is the refusal of a local backend whose window is limit
// tokens for a prompt of prompt tokens.
func recoveryRefusalOf(limit, prompt int) error {
	return fmt.Errorf(`provider "local" (http://127.0.0.1:8080/v1): openai stream: POST "http://127.0.0.1:8080/v1/chat/completions": 404 Not Found "Context limit is %d tokens; prompt=%d leaves 0 output tokens, below the minimum 16"`, limit, prompt)
}

type recoveryFixture struct {
	st   *session.State
	ag   *Agent
	p    *recoveryProvider
	logs *bytes.Buffer
	cwd  string
}

func newRecoveryFixture(t *testing.T, p *recoveryProvider, comp config.Compaction) *recoveryFixture {
	t.Helper()
	cwd := t.TempDir()
	for n := 1; n <= 12; n++ {
		var b strings.Builder
		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&b, "line %03d of notes %02d: a few words that make the file worth reading\n", i, n)
		}
		if err := os.WriteFile(filepath.Join(cwd, fmt.Sprintf("notes_%02d.txt", n)), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := t.TempDir()
	sessionDir := filepath.Join(store, "bundle")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Result eviction would shrink the requests by itself; these tests are about
	// the compaction, and the one that tests forced eviction turns it on.
	if comp.ResultEviction.Enabled == nil {
		off := false
		comp.ResultEviction.Enabled = &off
	}
	logs := &bytes.Buffer{}
	st := &session.State{ID: "sess_overflow_recovery", CWD: cwd, Mode: session.ModeAgent, SessionDir: sessionDir}
	ag := NewAgent(&config.Config{
		Providers:  []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:     []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 49152}},
		Agent:      config.Agent{Model: "fake/model"},
		Compaction: comp,
		Tools:      config.Tools{PermissionMode: config.PermModeBypass},
	}, st, resumePermissionSender{}, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return p, nil }
	return &recoveryFixture{st: st, ag: ag, p: p, logs: logs, cwd: cwd}
}

func (f *recoveryFixture) run(t *testing.T, prompt string) (string, error) {
	t.Helper()
	return f.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: prompt}})
}

func (f *recoveryFixture) summaryRows() []llm.Message {
	var rows []llm.Message
	for _, m := range f.st.GetMessages() {
		if m.CompactionSummary {
			rows = append(rows, m)
		}
	}
	return rows
}

func refuseCalls(calls ...int) func(int) error {
	return func(n int) error {
		for _, c := range calls {
			if n == c {
				return errRecoveryRefusal
			}
		}
		return nil
	}
}

const recoveryPrompt = "RECOVERY-PROMPT: read the notes again and again, then tell me what they say."

func TestOverflowRecoveryCompactsTheTurnAndAsksTheStepAgain(t *testing.T) {
	p := &recoveryProvider{steps: readSteps(5), refuse: refuseCalls(4)}
	f := newRecoveryFixture(t, p, config.Compaction{})

	stop, err := f.run(t, recoveryPrompt)
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop=%q err=%v, want the turn to end with the model's answer", stop, err)
	}
	if len(p.refused) != 1 {
		t.Fatalf("%d request(s) refused, want one", len(p.refused))
	}
	rows := f.summaryRows()
	if len(rows) != 1 || !strings.HasPrefix(rows[0].Content, recoveryPrompt) {
		t.Fatalf("summary rows = %d (%q), want one that begins with the prompt", len(rows), firstChars(rows[0].Content, 80))
	}
	// The request asked again is the refused one made smaller, and it carries the
	// prompt, once, at the start of the row.
	again := p.seen[4]
	if before, after := estimateMessagesTokens(p.refused[0]), estimateMessagesTokens(again); after >= before {
		t.Fatalf("the request asked again is %d tokens, the refused one was %d", after, before)
	}
	if err := carriesPrompt2(again, recoveryPrompt); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(transcriptText(again), recoveryPrompt); got != 1 {
		t.Fatalf("the request asked again holds the prompt %d times, want once", got)
	}
	if issues := session.ValidateToolPairing(again); len(issues) > 0 {
		t.Fatalf("the request asked again has a malformed tool-call history: %v", issues)
	}
	// The transcript keeps every message the turn produced.
	results := 0
	for _, m := range f.st.GetMessages() {
		if m.Role == llm.RoleTool {
			results++
		}
	}
	if results != 5 {
		t.Fatalf("the transcript holds %d tool results, want all 5", results)
	}
	// The log says what happened, with the limit the refusal revealed.
	for _, want := range []string{"compacted the turn and asking again", "limitTokens=", "providerLimit=49152", "providerTokens=51402"} {
		if !strings.Contains(f.logs.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, f.logs.String())
		}
	}
	if !f.ag.evictionForced {
		t.Error("result eviction was not forced after the refusal")
	}
}

// carriesPrompt2 is carriesPrompt for a prompt of the test's own.
func carriesPrompt2(req []llm.Message, prompt string) error {
	for _, m := range req {
		if m.Role == llm.RoleSystem {
			continue
		}
		if m.CompactionSummary {
			if !strings.HasPrefix(m.Content, prompt) {
				return fmt.Errorf("the summary row does not begin with the prompt: %q", firstChars(m.Content, 100))
			}
			return nil
		}
		if m.Role == llm.RoleUser && m.Content == prompt {
			return nil
		}
		return fmt.Errorf("the history does not begin with the prompt: %+v", m)
	}
	return fmt.Errorf("the request holds no history")
}

func TestOverflowRecoveryIsOncePerStep(t *testing.T) {
	// The smaller request is refused as well: the turn ends with the explanation,
	// which now says the compaction was tried.
	p := &recoveryProvider{steps: readSteps(5), refuse: refuseCalls(4, 5)}
	f := newRecoveryFixture(t, p, config.Compaction{})

	stop, err := f.run(t, recoveryPrompt)
	if err == nil || stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop=%q err=%v, want the turn refused", stop, err)
	}
	for _, want := range []string{"context window exceeded", "Coddy compacted this turn and asked again", "run /compact", "Context limit is 49152 tokens"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if !llm.IsContextOverflow(err) || !errors.Is(err, errRecoveryRefusal) {
		t.Error("the provider's refusal is no longer reachable from the error")
	}
	if p.calls != 6 {
		t.Fatalf("provider asked %d times, want 6: four steps, the refusal, the retry and no third try", p.calls)
	}
	if len(f.summaryRows()) != 1 {
		t.Fatalf("%d summary rows, want one compaction", len(f.summaryRows()))
	}
}

func TestOverflowRecoveryResetsAfterASuccessfulCall(t *testing.T) {
	// Two different steps are each refused once; both are recovered.
	p := &recoveryProvider{steps: readSteps(9), refuse: refuseCalls(3, 8)}
	f := newRecoveryFixture(t, p, config.Compaction{})

	stop, err := f.run(t, recoveryPrompt)
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop=%q err=%v, want both refusals recovered and the turn answered", stop, err)
	}
	if len(p.refused) != 2 {
		t.Fatalf("%d request(s) refused, want 2", len(p.refused))
	}
	rows := f.summaryRows()
	if len(rows) != 2 {
		t.Fatalf("%d summary rows, want 2", len(rows))
	}
	last := p.seen[len(p.seen)-1]
	if err := carriesPrompt2(last, recoveryPrompt); err != nil {
		t.Fatalf("the last request: %v", err)
	}
	if got := strings.Count(transcriptText(last), recoveryPrompt); got != 1 {
		t.Fatalf("the last request holds the prompt %d times after two folds, want once", got)
	}
}

func TestOverflowAfterStreamedOutputIsNotRecovered(t *testing.T) {
	// llm-streaming rule: nothing is retried once a delta reached the client.
	p := &recoveryProvider{steps: readSteps(3), refuse: refuseCalls(2), textBeforeRefusal: true}
	f := newRecoveryFixture(t, p, config.Compaction{})

	stop, err := f.run(t, recoveryPrompt)
	if err == nil || stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop=%q err=%v, want the turn refused", stop, err)
	}
	if strings.Contains(err.Error(), "Coddy compacted") {
		t.Errorf("a compaction was reported for a request that had streamed: %v", err)
	}
	if p.calls != 3 || p.summary != 0 || len(f.summaryRows()) != 0 {
		t.Fatalf("calls=%d summaries=%d rows=%d: a request that streamed must not be compacted and asked again", p.calls, p.summary, len(f.summaryRows()))
	}
}

func TestOverflowRecoveryIsOffWithTheSection(t *testing.T) {
	off := false
	for name, comp := range map[string]config.Compaction{
		"auto_enable false":     {AutoEnabled: &off},
		"compaction.enable off": {Enabled: &off},
	} {
		t.Run(name, func(t *testing.T) {
			p := &recoveryProvider{steps: readSteps(4), refuse: refuseCalls(3)}
			f := newRecoveryFixture(t, p, comp)

			stop, err := f.run(t, recoveryPrompt)
			if err == nil || stop != string(acp.StopReasonRefused) {
				t.Fatalf("stop=%q err=%v, want the turn refused at once", stop, err)
			}
			// Exactly what the turn ended with before the recovery existed.
			if !strings.HasPrefix(err.Error(), "context window exceeded") || strings.Contains(err.Error(), "Coddy compacted") {
				t.Errorf("error = %v", err)
			}
			if p.calls != 4 || p.summary != 0 || len(f.summaryRows()) != 0 {
				t.Fatalf("calls=%d summaries=%d rows=%d: nothing may be compacted", p.calls, p.summary, len(f.summaryRows()))
			}
			if f.ag.evictionForced {
				t.Error("eviction was forced although the recovery is off")
			}
		})
	}
}

func TestOverflowOfTheFirstRequestWithNothingToFoldEndsTheTurn(t *testing.T) {
	p := &recoveryProvider{steps: readSteps(0), refuse: refuseCalls(0, 1, 2)}
	f := newRecoveryFixture(t, p, config.Compaction{})

	stop, err := f.run(t, recoveryPrompt)
	if err == nil || stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop=%q err=%v", stop, err)
	}
	if strings.Contains(err.Error(), "Coddy compacted") {
		t.Errorf("nothing was compacted, yet the error says so: %v", err)
	}
	if p.calls != 1 || p.summary != 0 {
		t.Fatalf("calls=%d summaries=%d, want one request and no compaction", p.calls, p.summary)
	}
}

// The user's Stop during the compaction after a refusal is a stop, not a
// refusal: the summarizer call fails because the context is gone, and the turn
// ends the way it does at the top of every step.
func TestOverflowRecoveryInterruptedByTheUsersStopEndsTheTurnCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &recoveryProvider{steps: readSteps(4), refuse: refuseCalls(3), foldFail: context.Canceled}
	f := newRecoveryFixture(t, p, config.Compaction{})
	p.onFold = func() {
		f.st.SetUserCancelledTurn()
		cancel()
	}

	stop, err := f.ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: recoveryPrompt}})
	if err != nil || stop != string(acp.StopReasonCancelled) {
		t.Fatalf("stop=%q err=%v, want the turn cancelled", stop, err)
	}
	if len(f.summaryRows()) != 0 {
		t.Fatal("a summary row was written by a compaction that never finished")
	}
}

// A shutdown or a deadline is not the user: the turn ends refused, with the
// explanation of the refusal it was recovering from and what cut the compaction
// short, as an interrupted provider recovery does.
func TestOverflowRecoveryInterruptedByAShutdownEndsTheTurnRefused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &recoveryProvider{steps: readSteps(4), refuse: refuseCalls(3), onFold: cancel, foldFail: context.Canceled}
	f := newRecoveryFixture(t, p, config.Compaction{})

	stop, err := f.ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: recoveryPrompt}})
	if err == nil || stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop=%q err=%v, want the turn refused", stop, err)
	}
	for _, want := range []string{
		"context window exceeded", "the compaction after the refusal was interrupted: context canceled", "Context limit is 49152 tokens",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "Coddy compacted") {
		t.Errorf("a compaction that never finished is reported as tried: %v", err)
	}
	if !llm.IsContextOverflow(err) || !errors.Is(err, errRecoveryRefusal) {
		t.Error("the provider's refusal is no longer reachable from the error")
	}
	if len(f.summaryRows()) != 0 {
		t.Fatal("a summary row was written by a compaction that never finished")
	}
}

func TestOverflowRecoveryThatFailsEndsTheTurnWithTheExplanation(t *testing.T) {
	p := &recoveryProvider{steps: readSteps(4), refuse: refuseCalls(3), foldFail: errors.New("500 summarizer down")}
	f := newRecoveryFixture(t, p, config.Compaction{})

	stop, err := f.run(t, recoveryPrompt)
	if err == nil || stop != string(acp.StopReasonRefused) {
		t.Fatalf("stop=%q err=%v", stop, err)
	}
	if !strings.HasPrefix(err.Error(), "context window exceeded") || strings.Contains(err.Error(), "Coddy compacted") {
		t.Errorf("a compaction that failed must not be reported as tried: %v", err)
	}
	if !strings.Contains(f.logs.String(), "compacting the turn did not work") {
		t.Errorf("the failure is not logged:\n%s", f.logs.String())
	}
}

// With earlier turns in the window and a current turn that fits what is left,
// the recovery folds the earlier turns the way a regular compaction does: the
// prompt stays a message of its own and the row has nothing in front of it.
func TestOverflowRecoveryPrefersFoldingEarlierTurns(t *testing.T) {
	// The backend serves 38000 tokens; the earlier turns are what made the
	// request too large, and the turn in progress is small enough to stay. The
	// threshold is the whole window so the aim leaves room for it next to the
	// request's fixed overhead (the system prompt and the tool definitions).
	hundred := 100
	p := &recoveryProvider{steps: readSteps(2), refuse: func(n int) error {
		if n == 2 {
			return recoveryRefusalOf(38000, 40500)
		}
		return nil
	}}
	f := newRecoveryFixture(t, p, config.Compaction{ThresholdPercent: hundred})
	for i := 1; i <= 2; i++ {
		f.st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("earlier question %d", i)})
		f.st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("earlier answer %d %s", i, strings.Repeat("x", 36000))})
	}

	stop, err := f.run(t, recoveryPrompt)
	if err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop=%q err=%v", stop, err)
	}
	rows := f.summaryRows()
	if len(rows) != 1 || !strings.HasPrefix(rows[0].Content, "The earlier conversation was compacted.") {
		t.Fatalf("summary rows = %d, want one regular row (first: %q)", len(rows), firstChars(rows[0].Content, 80))
	}
	again := p.seen[2]
	text := transcriptText(again)
	if strings.Count(text, recoveryPrompt) != 1 || strings.Contains(text, "earlier question 1") {
		t.Fatalf("the request asked again must carry the prompt once and none of the folded turns:\n%s", firstChars(text, 600))
	}
	// The prompt is a message of its own, after the row, and the steps the turn
	// already made stay as they were.
	var promptMsg *llm.Message
	for i := range again {
		if again[i].Role == llm.RoleUser && !again[i].CompactionSummary && strings.HasPrefix(again[i].Content, recoveryPrompt) {
			promptMsg = &again[i]
		}
	}
	if promptMsg == nil {
		t.Fatalf("the prompt is no message of its own in the request asked again:\n%s", firstChars(text, 600))
	}
	if !strings.Contains(text, "line 001 of notes 01") || !strings.Contains(text, "line 001 of notes 02") {
		t.Fatalf("the turn's own results were folded although the earlier turns were enough:\n%s", firstChars(text, 600))
	}
}

// The gate that holds result eviction off below start_percent is a cache
// measure. Once the provider has refused a request of the turn the gate is
// open, for that turn only.
func TestForcedEvictionOpensTheGateOnlyAfterARefusal(t *testing.T) {
	hundred, bytesFloor := 100, 1
	comp := config.Compaction{ResultEviction: config.ResultEviction{StartPercent: &hundred, MinResultBytes: &bytesFloor}}
	on := true
	comp.ResultEviction.Enabled = &on

	msgs := fixtureTurn("p", 6, 1, 3000)
	t.Run("the gate holds a short history", func(t *testing.T) {
		f := newRecoveryFixture(t, &recoveryProvider{}, comp)
		if f.ag.evictionDue(msgs) || f.ag.listingEvictionDue(msgs) {
			t.Fatal("eviction is due below its start_percent")
		}
		if f.ag.evictionForced {
			t.Fatal("a new agent starts with eviction forced")
		}
	})

	t.Run("a refusal forces it, for read and grep and for the listings", func(t *testing.T) {
		f := newRecoveryFixture(t, &recoveryProvider{}, comp)
		f.ag.evictionForced = true
		if !f.ag.evictionDue(msgs) {
			t.Fatal("eviction is not due after a refusal")
		}
		if !f.ag.listingEvictionDue(msgs) {
			t.Fatal("the listing gate is not open after a refusal")
		}
	})

	t.Run("not when eviction is switched off", func(t *testing.T) {
		off := comp
		disabled := false
		off.ResultEviction.Enabled = &disabled
		f := newRecoveryFixture(t, &recoveryProvider{}, off)
		f.ag.evictionForced = true
		if f.ag.evictionDue(msgs) {
			t.Fatal("a forced projection ran although result_eviction.enable is false")
		}
	})

	t.Run("the next turn starts unforced", func(t *testing.T) {
		p := &recoveryProvider{steps: readSteps(4), refuse: refuseCalls(3)}
		f := newRecoveryFixture(t, p, comp)
		if _, err := f.run(t, recoveryPrompt); err != nil {
			t.Fatal(err)
		}
		if !f.ag.evictionForced {
			t.Fatal("the turn that was refused did not force eviction")
		}
		next := NewAgent(f.ag.cfg, f.st, resumePermissionSender{}, nil)
		if next.evictionForced {
			t.Fatal("a new turn inherited the forced eviction")
		}
	})
}

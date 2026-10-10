package agent

// Edge cases of how a turn ends when the provider refuses a request as larger
// than the model's context window. The happy path is in
// features/context_compaction.feature.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
		want      []string
		notWant   []string
	}{
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
			notWant: []string{"()", "estimated", "the window is", "max_context_tokens"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := contextOverflowMessage(tc.estimated, tc.window, tc.source, tc.detail)
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
	got := contextOverflowMessage(47500, 49152, session.ContextWindowFromConfig, llm.OverflowDetail{Prompt: 49182, Limit: 49152})
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

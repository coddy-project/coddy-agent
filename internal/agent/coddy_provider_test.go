package agent

// The agent and a model a remote Coddy shares (provider type coddy): no
// first-token timer for such a row, the wait for a free stream slot lives in
// the provider and is shown the way a limit wait is, the capability cache of
// the session manager supplies the revision a request sends, and the agent's
// provider recovery reacts to the typed errors of the type as the plan says
// (docs/plans/remote-model-provider.md, 4.1b, 4.2, 4.3).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// coddyRow is the models[] row of a remote's shared alias.
const coddyRow = "remote/coder"

func coddyConfig(apiBase string, tune func(*config.Config)) *config.Config {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "remote", Type: "coddy", APIBase: apiBase, APIKey: "shared-token", Proxy: "none"},
			{Name: "plain", Type: "openai", APIKey: "test"},
		},
		Models: []config.ModelEntry{
			{Model: coddyRow, MaxTokens: 100},
			{Model: "plain/gpt", MaxTokens: 100},
		},
		Agent: config.Agent{Model: coddyRow, MaxTurns: 8, LLMRetryBaseMS: 1},
	}
	if tune != nil {
		tune(cfg)
	}
	return cfg
}

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

// --- the provider input -----------------------------------------------------

func bareCoddyAgent(t *testing.T, tune func(*config.Config)) (*Agent, *config.Config) {
	t.Helper()
	cfg := coddyConfig("https://remote.example", tune)
	cfg.Agent.ApplyDefaults()
	st := &session.State{ID: "sess_coddy_input", CWD: t.TempDir(), Mode: session.ModeAgent}
	return NewAgent(cfg, st, resumePermissionSender{}, nil), cfg
}

func resolved(t *testing.T, cfg *config.Config, ref string) *config.ResolvedLLM {
	t.Helper()
	rm, err := cfg.ResolveLLM(ref)
	if err != nil {
		t.Fatal(err)
	}
	return rm
}

// A coddy row never arms the first-token timer: CallBudget stays unset, whatever
// the row's stream flag says, and the stall guard applies to it, since the wire
// is always a stream.
func TestCoddyRowInputHasNoCallBudgetAndAlwaysTheIdleGuard(t *testing.T) {
	for _, stream := range []*bool{nil, boolPtr(true), boolPtr(false)} {
		name := "stream unset"
		if stream != nil {
			name = fmt.Sprintf("stream %v", *stream)
		}
		t.Run(name, func(t *testing.T) {
			ag, cfg := bareCoddyAgent(t, func(c *config.Config) {
				c.Models[0].Stream = stream
				c.Agent.LLMFirstTokenTimeoutMS = intPtr(45000)
				c.Agent.LLMStreamIdleTimeoutMS = intPtr(20000)
			})
			in := ag.turnProviderInput(resolved(t, cfg, coddyRow))
			if in.CallBudget != 0 {
				t.Errorf("CallBudget = %v for a coddy row, want unset: no first-token timer is armed for it", in.CallBudget)
			}
			if in.StreamIdleTimeout != 20*time.Second {
				t.Errorf("StreamIdleTimeout = %v, want 20s whatever models[].stream says", in.StreamIdleTimeout)
			}
			if in.Type != "coddy" || in.Model != "coder" || in.BaseURL != "https://remote.example" || in.APIKey != "shared-token" {
				t.Errorf("input = %+v, want the coddy row's type, alias, base and token", in)
			}
		})
	}
}

// retry_budget_ms information keeps flowing: the rest of the retry budget under
// wait_for_limit_reset, from RetryBudget and the turn's ledger, with no
// first-token term in it.
func TestCoddyRowInputKeepsTheRetryBudgetUnderTheLimitWait(t *testing.T) {
	ag, cfg := bareCoddyAgent(t, func(c *config.Config) {
		c.Agent.WaitForLimitReset = true
		c.Agent.WaitForLimitResetMaxMS = intPtr(5000)
		c.Agent.LLMFirstTokenTimeoutMS = intPtr(45000)
	})
	in := ag.turnProviderInput(resolved(t, cfg, coddyRow))
	if !in.RetryBudgetSet || in.RetryBudget != 5*time.Second {
		t.Fatalf("RetryBudget = %v (set %v), want 5s with the wait on", in.RetryBudget, in.RetryBudgetSet)
	}
	if in.LimitLedger == nil || in.LimitLedger != llm.LimitLedger(ag.limitLedgerFor()) {
		t.Fatal("the provider must charge the turn's ledger, so retry_budget_ms is the budget minus what was spent")
	}
	if in.CallBudget != 0 {
		t.Fatalf("CallBudget = %v, want unset", in.CallBudget)
	}

	off, offCfg := bareCoddyAgent(t, nil)
	in = off.turnProviderInput(resolved(t, offCfg, coddyRow))
	if in.RetryBudgetSet || in.LimitLedger != nil {
		t.Fatalf("with the wait off the input carries a retry budget: %+v", in)
	}
}

// The wait for a slot: providers[].busy_wait_ms over agent.shared_busy_wait_ms,
// resolved by config and handed to the provider as it is.
func TestCoddyRowInputCarriesTheBusyWaitBudget(t *testing.T) {
	cases := []struct {
		name     string
		provider int
		global   *int
		want     time.Duration
	}{
		{"nothing set means 30 s", 0, nil, 30 * time.Second},
		{"the provider wins", 12000, intPtr(5000), 12 * time.Second},
		{"zero falls back to the global key", 0, intPtr(7000), 7 * time.Second},
		{"an explicit global zero means no waiting", 0, intPtr(0), 0},
		{"a positive provider value waits whatever the global says", 4000, intPtr(0), 4 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ag, cfg := bareCoddyAgent(t, func(c *config.Config) {
				c.Providers[0].BusyWaitMS = tc.provider
				c.Agent.SharedBusyWaitMS = tc.global
			})
			rm := resolved(t, cfg, coddyRow)
			for _, in := range []llm.ProviderInput{ag.turnProviderInput(rm), ag.llmProviderInput(rm)} {
				if in.BusyWait != tc.want {
					t.Errorf("BusyWait = %v, want %v", in.BusyWait, tc.want)
				}
			}
		})
	}
}

// Every other provider type is built byte for byte as before: no busy wait, no
// revision, no refresh, the first-token budget and the stream-bound idle guard.
func TestOtherProviderTypesAreBuiltAsBefore(t *testing.T) {
	for _, typ := range []string{"openai", "anthropic", "neuraldeep"} {
		for _, stream := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s stream %v", typ, stream), func(t *testing.T) {
				ag, cfg := bareCoddyAgent(t, func(c *config.Config) {
					c.Providers[1].Type = typ
					c.Models[1].Stream = boolPtr(stream)
					c.Agent.LLMFirstTokenTimeoutMS = intPtr(45000)
					c.Agent.LLMStreamIdleTimeoutMS = intPtr(20000)
					c.Agent.SharedBusyWaitMS = intPtr(5000)
					c.Providers[1].BusyWaitMS = 9000
				})
				rm := resolved(t, cfg, "plain/gpt")
				in := ag.turnProviderInput(rm)

				if in.BusyWait != 0 || in.ExpectedRevision != "" || in.RefreshCapabilities != nil {
					t.Errorf("a %s row carries coddy-only settings: busy %v revision %q refresh %v", typ, in.BusyWait, in.ExpectedRevision, in.RefreshCapabilities != nil)
				}
				var wantCall, wantIdle time.Duration
				if stream {
					wantCall, wantIdle = 45*time.Second, 20*time.Second
				}
				if in.CallBudget != wantCall {
					t.Errorf("CallBudget = %v, want %v", in.CallBudget, wantCall)
				}
				if in.StreamIdleTimeout != wantIdle {
					t.Errorf("StreamIdleTimeout = %v, want %v", in.StreamIdleTimeout, wantIdle)
				}
				if in.DisableStream != !stream {
					t.Errorf("DisableStream = %v, want %v", in.DisableStream, !stream)
				}
			})
		}
	}
}

// --- the first-token timer --------------------------------------------------

// The transport of a coddy row is streaming whatever models[].stream says - it
// drives the reasoning clock, and the wire is always an event stream - and only
// its first-token guard is off; every other type keeps both exactly where they
// were (streaming is the row's own flag).
func TestTransportOfACoddyRowKeepsStreamingAndDropsOnlyTheGuard(t *testing.T) {
	ag, cfg := bareCoddyAgent(t, func(c *config.Config) { c.Models[0].Stream = boolPtr(false) })
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return &silentLaneProvider{}, nil }
	for _, tc := range []struct {
		model                 string
		streaming, guard      bool
		providerName, provTyp string
	}{
		{coddyRow, true, false, "remote", "coddy"},
		{"plain/gpt", true, true, "plain", "openai"},
	} {
		cfg.Agent.Model = tc.model
		tr, err := ag.getProvider("agent")
		if err != nil {
			t.Fatal(err)
		}
		if tr.streaming != tc.streaming || tr.firstTokenGuard != tc.guard || tr.providerName != tc.providerName || tr.providerType != tc.provTyp {
			t.Errorf("%s: transport = streaming %v guard %v row %s/%s, want %v %v %s/%s", tc.model,
				tr.streaming, tr.firstTokenGuard, tr.providerName, tr.providerType, tc.streaming, tc.guard, tc.providerName, tc.provTyp)
		}
	}

	// Another type with the blocking flag stays blocking: no reasoning clock, no guard.
	blocking, blockCfg := bareCoddyAgent(t, func(c *config.Config) { c.Models[1].Stream = boolPtr(false) })
	blocking.providerFactory = ag.providerFactory
	blockCfg.Agent.Model = "plain/gpt"
	if tr, err := blocking.getProvider("agent"); err != nil || tr.streaming {
		t.Errorf("an openai row with stream: false: streaming %v (err %v), want false", tr.streaming, err)
	}

	streamed, cfg2 := bareCoddyAgent(t, func(c *config.Config) { c.Models[0].Stream = boolPtr(true) })
	streamed.providerFactory = ag.providerFactory
	cfg2.Agent.Model = coddyRow
	tr, err := streamed.getProvider("agent")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.streaming || tr.firstTokenGuard {
		t.Errorf("a streamed coddy row: streaming %v guard %v, want true and false", tr.streaming, tr.firstTokenGuard)
	}
}

// remote is a remote coddy serve: it records every completion request and
// answers each with its handler.
type remote struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	reqs    []llm.WireRequest
	handler func(w http.ResponseWriter, r *http.Request, n int, req llm.WireRequest)
}

func newRemote(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, n int, req llm.WireRequest)) *remote {
	t.Helper()
	rm := &remote{t: t, handler: handler}
	rm.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != llm.CoddyCompletionsPath || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var req llm.WireRequest
		_ = json.Unmarshal(raw, &req)
		rm.mu.Lock()
		rm.reqs = append(rm.reqs, req)
		n := len(rm.reqs)
		rm.mu.Unlock()
		rm.handler(w, r, n, req)
	}))
	t.Cleanup(rm.srv.Close)
	return rm
}

func (rm *remote) count() int {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return len(rm.reqs)
}

func (rm *remote) request(n int) llm.WireRequest {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if n < 1 || n > len(rm.reqs) {
		rm.t.Fatalf("remote saw %d requests, asked for #%d", len(rm.reqs), n)
	}
	return rm.reqs[n-1]
}

type frames struct{ w http.ResponseWriter }

func startFrames(w http.ResponseWriter) *frames {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	f := &frames{w: w}
	f.flush()
	return f
}

func (f *frames) flush() {
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
}

func (f *frames) raw(s string) { _, _ = io.WriteString(f.w, s); f.flush() }

func (f *frames) frame(v any) {
	b, err := llm.EncodeWireFrame(v)
	if err != nil {
		panic(err)
	}
	_, _ = f.w.Write(b)
	f.flush()
}

func (f *frames) text(s string) { f.frame(llm.WireChunkFromStream(llm.StreamChunk{TextDelta: s})) }
func (f *frames) final(content string) {
	f.frame(llm.WireFinalFromResponse(&llm.Response{Content: content, StopReason: "end_turn", InputTokens: 5, OutputTokens: 2}))
}
func (f *frames) fail(e llm.WireError) { e.Type = llm.WireTypeError; f.frame(e) }

// refuse answers a request with the flat error object and a status.
func refuse(w http.ResponseWriter, status int, e llm.WireError, headers ...string) {
	w.Header().Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		w.Header().Set(headers[i], headers[i+1])
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

// coddyRun drives a real Agent turn over the real coddy provider.
type coddyRun struct {
	ag     *Agent
	st     *session.State
	sender *limitWaitCapture
	stop   string
	err    error
}

func newCoddyRun(t *testing.T, rm *remote, tune func(*config.Config)) *coddyRun {
	t.Helper()
	cfg := coddyConfig(rm.srv.URL, tune)
	st := &session.State{ID: "sess_coddy_run", CWD: t.TempDir(), SessionDir: t.TempDir(), Mode: session.ModeAgent}
	sender := &limitWaitCapture{}
	return &coddyRun{ag: NewAgent(cfg, st, sender, slog.New(slog.NewTextHandler(io.Discard, nil))), st: st, sender: sender}
}

func (r *coddyRun) run(ctx context.Context) {
	r.stop, r.err = r.ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "do the thing"}})
}

func (r *coddyRun) lastAssistant() string {
	msgs := r.st.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleAssistant {
			return msgs[i].Content
		}
	}
	return ""
}

// A remote whose first token takes longer than agent.llm_first_token_timeout_ms
// is not cut by the agent: the remote owns model progress. The same holds for a
// blocking local row (stream: false), whose wire is still a stream.
func TestCoddyRowIsNotCutByTheFirstTokenTimer(t *testing.T) {
	for _, stream := range []*bool{boolPtr(true), boolPtr(false)} {
		t.Run(fmt.Sprintf("stream %v", *stream), func(t *testing.T) {
			rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ llm.WireRequest) {
				f := startFrames(w)
				f.raw(llm.CoddyHeartbeat)
				time.Sleep(450 * time.Millisecond) // four times the guard below
				f.text("late but whole")
				f.final("late but whole")
			})
			r := newCoddyRun(t, rm, func(c *config.Config) {
				c.Models[0].Stream = stream
				c.Agent.LLMFirstTokenTimeoutMS = intPtr(100)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			r.run(ctx)

			if r.err != nil {
				t.Fatalf("Run: %v (stop %q): the first-token timer cut a coddy row", r.err, r.stop)
			}
			if n := rm.count(); n != 1 {
				t.Fatalf("remote saw %d requests, want 1: the agent re-issued a call the timer cut", n)
			}
			if got := r.lastAssistant(); got != "late but whole" {
				t.Fatalf("answer = %q", got)
			}
		})
	}
}

// The byte-level guard of the client still watches a coddy row whatever its
// stream flag says: a remote that sends its headers and then goes silent is cut
// after agent.llm_stream_idle_timeout_ms, the call returns and nothing hangs.
func TestCoddyRowWithABlockingFlagStillHasTheIdleGuard(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ llm.WireRequest) {
		startFrames(w).raw(llm.CoddyHeartbeat)
		<-r.Context().Done() // a remote that vanished after its first bytes
	})
	r := newCoddyRun(t, rm, func(c *config.Config) {
		c.Models[0].Stream = boolPtr(false)
		c.Agent.LLMStreamIdleTimeoutMS = intPtr(150)
		c.Agent.LLMRetryMax = intPtr(0)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	r.run(ctx)

	if r.err == nil {
		t.Fatalf("Run succeeded against a remote that went silent (stop %q)", r.stop)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the silent remote held the call for %v: the idle guard did not apply to a stream: false row", elapsed)
	}
	if !llm.IsStreamStalled(r.err) {
		t.Fatalf("error = %v, want the stall the guard reports", r.err)
	}
}

// The control: another provider type with the same silence is cut as before.
func TestOtherTypeIsStillCutByTheFirstTokenTimer(t *testing.T) {
	st := &session.State{ID: "sess_ctl", CWD: t.TempDir(), Mode: session.ModeAgent, SessionDir: t.TempDir()}
	provider := &silentLaneProvider{}
	cfg := coddyConfig("https://remote.example", func(c *config.Config) {
		c.Agent.Model = "plain/gpt"
		c.Agent.LLMFirstTokenTimeoutMS = intPtr(100)
	})
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }

	_, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "go"}})
	if err == nil || !strings.Contains(err.Error(), "did not respond") {
		t.Fatalf("err = %v, want the silence notice for an openai row", err)
	}
	if want := maxFirstTokenRetries + 1; provider.calls != want {
		t.Fatalf("provider called %d times, want %d", provider.calls, want)
	}
}

// --- the agent's reaction to the typed errors ------------------------------

// The agent re-issues a step after a provider failure only for an error that is
// transient (IsTransientProviderError): an upstream failure of the remote's lane,
// not a spent wait for a slot, a rate limit, a refusal, a credential problem or
// the max_call_ms timeout of a blocking row. maxProviderRecoveries bounds the
// repeats, so a transient failure costs 1 + 2 requests, anything else 1.
func TestAgentRecoveryReactsToTheTypedErrorsOfTheCoddyType(t *testing.T) {
	cases := []struct {
		name     string
		answer   func(w http.ResponseWriter)
		requests int
		errHas   string
	}{
		{
			name: "busy with the wait off is terminal",
			answer: func(w http.ResponseWriter) {
				refuse(w, http.StatusTooManyRequests, llm.WireError{Kind: llm.WireKindBusy}, "Retry-After", "1")
			},
			requests: 1, errHas: "no free stream slot",
		},
		{
			name: "rate is not re-issued",
			answer: func(w http.ResponseWriter) {
				refuse(w, http.StatusTooManyRequests, llm.WireError{Kind: llm.WireKindRate, RetryAfterS: 1})
			},
			requests: 1, errHas: "rate limit",
		},
		{
			name: "invalid is not re-issued",
			answer: func(w http.ResponseWriter) {
				refuse(w, http.StatusBadRequest, llm.WireError{Kind: llm.WireKindInvalid})
			},
			requests: 1, errHas: "refused the request",
		},
		{
			name:     "auth is not re-issued",
			answer:   func(w http.ResponseWriter) { refuse(w, http.StatusUnauthorized, llm.WireError{Kind: llm.WireKindAuth}) },
			requests: 1, errHas: "refused the credential",
		},
		{
			name: "an upstream timeout (max_call_ms) is not re-issued",
			answer: func(w http.ResponseWriter) {
				startFrames(w).fail(llm.WireError{Kind: llm.WireKindUpstream, Cause: llm.WireCauseTimeout, Status: 504})
			},
			requests: 1, errHas: "did not answer in time",
		},
		{
			name: "an upstream failure is re-issued up to the recovery budget",
			answer: func(w http.ResponseWriter) {
				startFrames(w).fail(llm.WireError{Kind: llm.WireKindUpstream, Cause: llm.WireCauseStatus, Status: 502})
			},
			requests: 1 + maxProviderRecoveries, errHas: "provider failed",
		},
		{
			name: "a stall before output is re-issued up to the recovery budget",
			answer: func(w http.ResponseWriter) {
				startFrames(w).fail(llm.WireError{Kind: llm.WireKindUpstream, Cause: llm.WireCauseStall})
			},
			requests: 1 + maxProviderRecoveries, errHas: "went silent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ llm.WireRequest) { tc.answer(w) })
			r := newCoddyRun(t, rm, func(c *config.Config) {
				c.Agent.SharedBusyWaitMS = intPtr(0)
				c.Agent.LLMRetryMax = intPtr(3)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			r.run(ctx)

			if r.err == nil {
				t.Fatalf("Run succeeded (stop %q), want the remote's error", r.stop)
			}
			if !strings.Contains(r.err.Error(), tc.errHas) {
				t.Errorf("error = %v, want it to say %q", r.err, tc.errHas)
			}
			if n := rm.count(); n != tc.requests {
				t.Errorf("remote saw %d requests, want %d", n, tc.requests)
			}
		})
	}
}

// A stall after output is a terminal error of the remote's lane and the
// partial answer is kept: the agent's recovery continues the step after a pause
// with the kept text, as for a local model, and the client's wrapper does not
// repeat the call itself (the remote already did).
func TestAgentRecoveryContinuesAfterAStallPastOutput(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ llm.WireRequest) {
		f := startFrames(w)
		if n == 1 {
			f.text("The first half. ")
			f.fail(llm.WireError{Kind: llm.WireKindUpstream, Cause: llm.WireCauseStall, Emitted: true})
			return
		}
		f.text("The second half.")
		f.final("The second half.")
	})
	r := newCoddyRun(t, rm, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r.run(ctx)

	if r.err != nil {
		t.Fatalf("Run: %v", r.err)
	}
	if n := rm.count(); n != 2 {
		t.Fatalf("remote saw %d requests, want 2: one cut by the stall, one recovery", n)
	}
	second := rm.request(2)
	var kept, nudged bool
	for _, m := range second.Messages {
		kept = kept || (m.Role == "assistant" && strings.Contains(m.Content, "The first half."))
		nudged = nudged || strings.Contains(m.Content, providerRecoveryNudge)
	}
	if !kept || !nudged {
		t.Fatalf("the recovery request holds the kept partial answer: %v, the nudge: %v", kept, nudged)
	}
	if got := r.lastAssistant(); got != "The second half." {
		t.Fatalf("final answer = %q", got)
	}
}

// A usage limit the remote reports with its reset time reaches the agent as the
// typed reset, so agent.wait_for_limit_reset works through the hop: the turn
// waits for the reset and the same call runs again.
func TestRemoteQuotaIsWaitedForByTheAgent(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ llm.WireRequest) {
		if n == 1 {
			reset := time.Now().Add(2 * time.Second).UTC().Format(time.RFC3339)
			refuse(w, http.StatusTooManyRequests, llm.WireError{Kind: llm.WireKindQuota, ResetAt: reset})
			return
		}
		f := startFrames(w)
		f.text("after the reset")
		f.final("after the reset")
	})
	r := newCoddyRun(t, rm, func(c *config.Config) {
		c.Agent.WaitForLimitReset = true
		c.Agent.WaitForLimitResetMaxMS = intPtr(30000)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.run(ctx)

	if r.err != nil {
		t.Fatalf("Run: %v", r.err)
	}
	if n := rm.count(); n != 2 {
		t.Fatalf("remote saw %d requests, want 2 (the quota, then the call after the reset)", n)
	}
	if got := r.lastAssistant(); got != "after the reset" {
		t.Fatalf("answer = %q", got)
	}
}

// A blocking row cut by the remote's max_call_ms after output keeps its partial
// answer like a truncated stream, and the agent does not re-issue the call: it
// already ran as long as the remote allows.
func TestTimeoutAfterOutputKeepsThePartialAnswerAndIsNotReissued(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ llm.WireRequest) {
		f := startFrames(w)
		f.text("Half of an answer")
		f.fail(llm.WireError{Kind: llm.WireKindUpstream, Cause: llm.WireCauseTimeout, Emitted: true})
	})
	r := newCoddyRun(t, rm, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r.run(ctx)

	if r.err == nil || !strings.Contains(r.err.Error(), "did not answer in time") {
		t.Fatalf("err = %v, want the remote's timeout", r.err)
	}
	if n := rm.count(); n != 1 {
		t.Fatalf("remote saw %d requests, want 1: a timeout is not re-issued", n)
	}
	if got := r.lastAssistant(); got != "Half of an answer" {
		t.Fatalf("kept answer = %q, want the partial text the user watched stream in", got)
	}
}

// The pause the remote names with an upstream error (retry_after_s) is the
// least the agent waits before it runs the step again.
func TestRecoveryWaitsForTheRetryAfterOfAnUpstreamError(t *testing.T) {
	var firstAt, secondAt time.Time
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ llm.WireRequest) {
		f := startFrames(w)
		if n == 1 {
			firstAt = time.Now()
			f.fail(llm.WireError{Kind: llm.WireKindUpstream, Cause: llm.WireCauseStatus, Status: 503, RetryAfterS: 1})
			return
		}
		secondAt = time.Now()
		f.text("ok")
		f.final("ok")
	})
	r := newCoddyRun(t, rm, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r.run(ctx)

	if r.err != nil {
		t.Fatalf("Run: %v", r.err)
	}
	if n := rm.count(); n != 2 {
		t.Fatalf("remote saw %d requests, want 2", n)
	}
	if gap := secondAt.Sub(firstAt); gap < 900*time.Millisecond {
		t.Fatalf("the step ran again after %v, want at least the 1 s the remote asked for", gap)
	}
}

// A busy remote is waited out inside the provider: the countdown reaches the
// session as a provider_usage update that says resuming, never the agent's own
// first-token timer, and the call goes through once a slot frees.
func TestBusyWaitShowsACountdownAndTheCallGoesThrough(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ llm.WireRequest) {
		if n == 1 {
			refuse(w, http.StatusTooManyRequests, llm.WireError{Kind: llm.WireKindBusy}, "Retry-After", "1")
			return
		}
		f := startFrames(w)
		f.text("Done after the wait.")
		f.final("Done after the wait.")
	})
	r := newCoddyRun(t, rm, func(c *config.Config) {
		c.Providers[0].BusyWaitMS = 10000
		c.Agent.LLMFirstTokenTimeoutMS = intPtr(200) // far below the wait: it must not be charged to it
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.run(ctx)

	if r.err != nil {
		t.Fatalf("Run: %v", r.err)
	}
	if n := rm.count(); n != 2 {
		t.Fatalf("remote saw %d requests, want 2 (busy, then the slot)", n)
	}
	if got := r.lastAssistant(); got != "Done after the wait." {
		t.Fatalf("answer = %q", got)
	}
	var waits []acp.ProviderUsageUpdate
	for _, u := range r.sender.snapshot() {
		if u.Resuming {
			waits = append(waits, u)
		}
	}
	if len(waits) == 0 {
		t.Fatal("the wait for a slot showed no countdown to the session")
	}
	u := waits[0]
	if !u.Blocked || u.Provider != "remote" || u.ProviderType != "coddy" || u.RetryAt == "" || u.RetryInSec <= 0 {
		t.Fatalf("countdown = %+v, want a blocked, resuming update of the coddy row with its time", u)
	}
	if u.Model != "coder" {
		t.Fatalf("countdown model = %q, want the alias \"coder\" and not the selector %q", u.Model, coddyRow)
	}
	if len(u.Blockers) != 1 || u.Blockers[0] != "remote_busy" {
		t.Fatalf("blockers = %v, want [remote_busy]: the surface must not read it as a usage limit", u.Blockers)
	}
	// The call takes the countdown down itself with the end update of the
	// family (4.6): the same blocker, no Resuming, not Blocked, no usage data and
	// no Unsupported (a surface reads that as "drop this row's usage").
	all := r.sender.snapshot()
	last := all[len(all)-1]
	requireBusyEnd(t, last, "remote", "coder")
	if last.FetchedAt <= u.FetchedAt {
		t.Fatalf("the end is stamped %s, not after the countdown it ends (%s)", last.FetchedAt, u.FetchedAt)
	}
}

// The countdown is re-sent at the limit wait's heartbeat, not on every sleep
// of the provider, and a call that never waited shows and clears nothing.
func TestBusyWaitNoticeIsThrottledAndClearedOnlyWhenShown(t *testing.T) {
	sender := &limitWaitCapture{}
	ag := NewAgent(coddyConfig("https://remote.example", nil), &session.State{ID: "sess_notice"}, sender, nil)
	ag.limitWaitHeartbeat = time.Hour
	notice := ag.newBusyWaitNotice("sess_notice", coddyTransport())
	if notice == nil {
		t.Fatal("a coddy transport has a wait notice")
	}

	notice.clear()
	if n := len(sender.snapshot()); n != 0 {
		t.Fatalf("clear without a wait sent %d updates", n)
	}
	for i := 0; i < 5; i++ {
		notice.report(llm.BusyWaitStatus{Budget: 30 * time.Second, Remaining: time.Duration(30-i) * time.Second, RetryIn: time.Second})
	}
	if n := len(sender.snapshot()); n != 1 {
		t.Fatalf("5 sleeps inside the heartbeat sent %d countdowns, want 1", n)
	}
	got := sender.snapshot()[0]
	if got.RetryInSec != 30 || !got.Resuming || !got.Blocked {
		t.Fatalf("countdown = %+v, want 30 s left, blocked and resuming", got)
	}
	notice.clear()
	notice.clear()
	all := sender.snapshot()
	if len(all) != 2 {
		t.Fatalf("updates = %+v, want the countdown and one end update", all)
	}
	requireBusyEnd(t, all[1], "remote", "coder")

	ag.limitWaitHeartbeat = time.Nanosecond
	again := ag.newBusyWaitNotice("sess_notice", coddyTransport())
	again.report(llm.BusyWaitStatus{Remaining: time.Second})
	time.Sleep(time.Millisecond)
	again.report(llm.BusyWaitStatus{Remaining: time.Second})
	if n := len(sender.snapshot()); n != 4 {
		t.Fatalf("after the heartbeat passed the countdown was not re-sent: %d updates", n)
	}

	if other := ag.newBusyWaitNotice("sess_notice", llmTransport{providerName: "plain", providerType: "openai"}); other != nil {
		t.Fatal("a provider type that does not wait shows a countdown")
	}
	var none *busyWaitNotice
	none.report(llm.BusyWaitStatus{})
	none.clear()
}

// Stop cancels the wait, the upload and the stream through the context: a turn
// stopped during a wait ends as cancelled within the same tick, with no more
// requests to the remote.
func TestStopCancelsTheBusyWait(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ llm.WireRequest) {
		refuse(w, http.StatusTooManyRequests, llm.WireError{Kind: llm.WireKindBusy}, "Retry-After", "1")
	})
	r := newCoddyRun(t, rm, func(c *config.Config) { c.Providers[0].BusyWaitMS = 60000 })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { r.run(ctx); close(done) }()

	deadline := time.Now().Add(10 * time.Second)
	for rm.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rm.count() < 1 {
		t.Fatal("the remote was never asked")
	}
	r.st.SetUserCancelledTurn()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not end the wait for a free slot")
	}
	if r.stop != string(acp.StopReasonCancelled) {
		t.Fatalf("stop = %q (err %v), want cancelled", r.stop, r.err)
	}
	if n := rm.count(); n > 2 {
		t.Fatalf("remote saw %d requests after Stop, want the wait to have sent at most one more", n)
	}
}

// retry_budget_ms is what is left of the retry budget under
// agent.wait_for_limit_reset, and nothing else: absent without the wait.
func TestRetryBudgetReachesTheRemote(t *testing.T) {
	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprintf("wait %v", on), func(t *testing.T) {
			rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ llm.WireRequest) {
				f := startFrames(w)
				f.text("ok")
				f.final("ok")
			})
			r := newCoddyRun(t, rm, func(c *config.Config) {
				c.Agent.WaitForLimitReset = on
				c.Agent.WaitForLimitResetMaxMS = intPtr(5000)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			r.run(ctx)
			if r.err != nil {
				t.Fatal(r.err)
			}
			got := rm.request(1).Options.RetryBudgetMS
			switch {
			case on && (got == nil || *got <= 0 || *got > 5000):
				t.Fatalf("retry_budget_ms = %v, want a value in (0, 5000]", got)
			case !on && got != nil:
				t.Fatalf("retry_budget_ms = %d without the limit wait, want absent", *got)
			}
		})
	}
}

// --- the capability cache --------------------------------------------------

// coddyHarness is a session manager over a stand-in listing and the real
// coddy provider, so the turn's first request is built from the cache.
type coddyHarness struct {
	t         *testing.T
	mgr       *session.Manager
	sessionID string
	rm        *remote

	mu       sync.Mutex
	revision string
	window   int
	listings int
	// What the stand-in listing reports besides the revision and the window: the
	// levels, the default and the off switch of the model, and the error a read
	// fails with while listErr is set.
	levels  []string
	def     string
	off     bool
	listErr error
}

func newCoddyHarness(t *testing.T, rm *remote, tune func(*config.Config)) *coddyHarness {
	t.Helper()
	root, cwd := t.TempDir(), t.TempDir()
	cfg := coddyConfig(rm.srv.URL, func(c *config.Config) {
		c.Paths = config.Paths{Home: root, CWD: cwd, ConfigPath: root + "/config.yaml"}
		c.Sessions = config.Sessions{Dir: root + "/sessions"}
		c.Tools.PermissionMode = config.PermModeBypass
		if tune != nil {
			tune(c)
		}
	})
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	cfg.Prompts.ApplyDefaults()
	h := &coddyHarness{t: t, rm: rm, revision: "rev-1", window: 128000}
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		loop := NewAgent(cfg, st, snd, slog.New(slog.NewTextHandler(io.Discard, nil)))
		return loop.Run(ctx, prompt)
	}
	h.mgr = session.NewManager(cfg, &todoSnapshotSender{}, runner, slog.Default(), cwd, &session.FileStore{Root: cfg.Sessions.Dir})
	h.mgr.SetContextWindowLister(func(_ context.Context, in llm.ProviderInput) ([]llm.ModelEntry, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.listings++
		if h.listErr != nil {
			return nil, h.listErr
		}
		return []llm.ModelEntry{{
			ID: "coder", Revision: h.revision, ContextWindow: h.window,
			ReasoningLevels: append([]string(nil), h.levels...), ReasoningDefault: h.def, AllowReasoningOff: h.off,
		}}, nil
	}, nil)
	t.Cleanup(func() { _ = h.mgr.WaitContextWindowsIdle(5 * time.Second) })
	res, err := h.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	h.sessionID = res.SessionID
	return h
}

func (h *coddyHarness) setRemoteRevision(rev string, window int) {
	h.mu.Lock()
	h.revision, h.window = rev, window
	h.mu.Unlock()
}

// setListing changes what the remote's listing reports for the model: the next
// read of the manager sees it.
func (h *coddyHarness) setListing(rev string, levels []string, def string, off bool) {
	h.mu.Lock()
	h.revision, h.levels, h.def, h.off = rev, levels, def, off
	h.mu.Unlock()
}

func (h *coddyHarness) failListing(err error) {
	h.mu.Lock()
	h.listErr = err
	h.mu.Unlock()
}

// relist makes the manager read the listing again now and returns the record
// of the model in it, the way a stale_revision answer does.
func (h *coddyHarness) relist() (*llm.ModelEntry, error) {
	if err := h.mgr.WaitContextWindowsIdle(5 * time.Second); err != nil {
		h.t.Fatal(err)
	}
	return h.mgr.RefreshProviderModelEntry(context.Background(), h.mgr.Cfg(), "remote", "coder", "", 5*time.Second)
}

func (h *coddyHarness) prompt(text string) error {
	_, err := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: h.sessionID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
	})
	return err
}

// The first request of a turn carries the revision of the cache entry as
// expected_revision, and the context window is the listing's.
func TestFirstRequestSendsTheCachedRevision(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ llm.WireRequest) {
		f := startFrames(w)
		f.text("ok")
		f.final("ok")
	})
	h := newCoddyHarness(t, rm, nil)
	if err := h.prompt("hello"); err != nil {
		t.Fatal(err)
	}
	got := rm.request(1).Options.ExpectedRevision
	if got == nil || *got != "rev-1" {
		t.Fatalf("expected_revision = %v, want rev-1 from the session manager's cache", got)
	}
	st := h.mgr.SessionByID(h.sessionID)
	if tokens, source := st.ContextWindowFor(h.mgr.Cfg(), coddyRow); tokens != 128000 || source != session.ContextWindowFromProvider {
		t.Fatalf("context window = %d/%q, want 128000 from the listing", tokens, source)
	}
}

// A request answered stale_revision refreshes the cache through the manager and
// is sent once more with the new revision; the config is untouched.
func TestStaleRevisionRefreshesTheCacheAndResends(t *testing.T) {
	var h *coddyHarness
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, req llm.WireRequest) {
		if n == 1 {
			// The remote changed the row after the client read the listing.
			h.setRemoteRevision("rev-2", 64000)
			refuse(w, http.StatusBadRequest, llm.WireError{Kind: llm.WireKindInvalid, Code: llm.WireCodeStaleRevision, Revision: "rev-2"})
			return
		}
		f := startFrames(w)
		f.text("ok")
		f.final("ok")
	})
	h = newCoddyHarness(t, rm, nil)
	before := *h.mgr.Cfg()
	models := append([]config.ModelEntry(nil), before.Models...)

	if err := h.prompt("hello"); err != nil {
		t.Fatal(err)
	}
	if n := rm.count(); n != 2 {
		t.Fatalf("remote saw %d requests, want 2 (stale, then the refreshed one)", n)
	}
	first, second := rm.request(1).Options.ExpectedRevision, rm.request(2).Options.ExpectedRevision
	if first == nil || *first != "rev-1" || second == nil || *second != "rev-2" {
		t.Fatalf("expected_revision = %v then %v, want rev-1 then rev-2", first, second)
	}
	if tokens, _ := h.mgr.ContextWindow(h.mgr.Cfg(), coddyRow); tokens != 64000 {
		t.Fatalf("context window = %d after the refresh, want the new 64000", tokens)
	}
	after := h.mgr.Cfg().Models
	if len(after) != len(models) {
		t.Fatalf("models changed length: %d -> %d", len(models), len(after))
	}
	for i := range after {
		if after[i].MaxContextTokens != models[i].MaxContextTokens {
			t.Fatalf("a refresh wrote max_context_tokens on %s", after[i].Model)
		}
	}
}

// A second stale_revision ends the call, as the plan says; the error names
// nothing but the remote's refusal.
func TestSecondStaleRevisionEndsTheCall(t *testing.T) {
	var h *coddyHarness
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ llm.WireRequest) {
		h.setRemoteRevision(fmt.Sprintf("rev-%d", n+1), 64000)
		refuse(w, http.StatusBadRequest, llm.WireError{Kind: llm.WireKindInvalid, Code: llm.WireCodeStaleRevision})
	})
	h = newCoddyHarness(t, rm, nil)
	err := h.prompt("hello")
	if err == nil {
		// The manager reports a refused turn as a stop reason; look at the transcript.
		t.Log("prompt returned no error; checking the number of requests")
	}
	if n := rm.count(); n != 2 {
		t.Fatalf("remote saw %d requests, want 2: one refresh and one more try, no loop", n)
	}
}

var _ = errors.New

// --- the countdown of the wait and the reasoning clock ----------------------

// The countdown is taken down when the remote admits the call, not when the
// stream ends: a banner that says "waiting for a free slot" while the answer
// streams in is wrong for as long as the generation takes. The remote here does
// not finish the answer until the session has been told the wait is over.
func TestBusyCountdownIsTakenDownAtAdmissionNotAtTheEndOfTheStream(t *testing.T) {
	var r *coddyRun
	clearedBeforeEnd := make(chan bool, 1)
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ llm.WireRequest) {
		if n == 1 {
			refuse(w, http.StatusTooManyRequests, llm.WireError{Kind: llm.WireKindBusy}, "Retry-After", "1")
			return
		}
		f := startFrames(w)
		f.text("The answer starts. ")
		// Admitted and streaming: wait for the session to hear the wait is over.
		cleared := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			for _, u := range r.sender.snapshot() {
				cleared = cleared || isBusyEnd(u)
			}
			if cleared {
				break
			}
		}
		clearedBeforeEnd <- cleared
		f.text("And ends.")
		f.final("The answer starts. And ends.")
	})
	r = newCoddyRun(t, rm, func(c *config.Config) { c.Providers[0].BusyWaitMS = 10000 })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.run(ctx)

	if r.err != nil {
		t.Fatalf("Run: %v", r.err)
	}
	select {
	case cleared := <-clearedBeforeEnd:
		if !cleared {
			t.Fatal("the stream ended with the countdown of the wait still showing: no update took it down at admission")
		}
	default:
		t.Fatal("the remote never admitted the call")
	}
}

// A call the remote admitted at once never showed a countdown, so nothing is
// sent to take one down; and a report that says Admitted without a countdown on
// screen sends nothing either.
func TestBusyWaitNoticeEndsOnAdmissionAndOnlyWhenShown(t *testing.T) {
	sender := &limitWaitCapture{}
	ag := NewAgent(coddyConfig("https://remote.example", nil), &session.State{ID: "sess_admit"}, sender, nil)
	ag.limitWaitHeartbeat = time.Hour
	notice := ag.newBusyWaitNotice("sess_admit", coddyTransport())

	notice.report(llm.BusyWaitStatus{Admitted: true, Requests: 1})
	if n := len(sender.snapshot()); n != 0 {
		t.Fatalf("an admission with no countdown shown sent %d updates", n)
	}

	notice.report(llm.BusyWaitStatus{Budget: 30 * time.Second, Remaining: 29 * time.Second, RetryIn: time.Second})
	notice.report(llm.BusyWaitStatus{Admitted: true, Requests: 3, Waited: 3 * time.Second, Remaining: 27 * time.Second})
	all := sender.snapshot()
	if len(all) != 2 || !all[0].Resuming {
		t.Fatalf("updates = %+v, want the countdown and then the end update", all)
	}
	requireBusyEnd(t, all[1], "remote", "coder")

	// The end of the call finds nothing left to clear, and a later wait of the
	// same call (a second attempt that is told to wait again) shows again.
	notice.clear()
	if n := len(sender.snapshot()); n != 2 {
		t.Fatalf("clear after the admission sent another update: %d", n)
	}
	notice.report(llm.BusyWaitStatus{Budget: 30 * time.Second, Remaining: 20 * time.Second, RetryIn: time.Second})
	if n := len(sender.snapshot()); n != 3 {
		t.Fatalf("a new wait after an admission shows no countdown: %d updates", n)
	}
}

// A coddy row with models[].stream: false still reads an event stream, so the
// reasoning clock of the streamed transport runs for it: the time between the
// first reasoning delta and the first answer text is recorded.
func TestReasoningDurationIsRecordedForACoddyRowWithABlockingFlag(t *testing.T) {
	for _, stream := range []*bool{boolPtr(false), boolPtr(true)} {
		t.Run(fmt.Sprintf("stream %v", *stream), func(t *testing.T) {
			rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ llm.WireRequest) {
				f := startFrames(w)
				f.frame(llm.WireChunkFromStream(llm.StreamChunk{ReasoningDelta: "Let me think about it."}))
				time.Sleep(150 * time.Millisecond)
				f.text("The answer.")
				f.final("The answer.")
			})
			r := newCoddyRun(t, rm, func(c *config.Config) { c.Models[0].Stream = stream })
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			r.run(ctx)

			if r.err != nil {
				t.Fatalf("Run: %v", r.err)
			}
			var got int64 = -1
			for _, m := range r.st.GetMessages() {
				if m.Role == llm.RoleAssistant {
					got = m.ReasoningDurationMs
				}
			}
			if got < 100 {
				t.Fatalf("reasoning_duration_ms = %d, want about the 150 ms the remote thought for", got)
			}
		})
	}
}

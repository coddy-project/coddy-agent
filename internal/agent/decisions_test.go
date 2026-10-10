package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// decisionResponse is one canned answer of the decisions stand.
type decisionResponse struct {
	status  int
	body    string
	headers map[string]string
}

// decisionsStand is a scriptable decisions endpoint: it answers the queued
// responses in order (the last one repeats) and records every request.
type decisionsStand struct {
	srv   *httptest.Server
	calls atomic.Int32
	mu    sync.Mutex
	queue []decisionResponse
}

func newDecisionsStand(t *testing.T, responses ...decisionResponse) *decisionsStand {
	t.Helper()
	d := &decisionsStand{queue: append([]decisionResponse(nil), responses...)}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.calls.Add(1)
		if r.URL.Path != "/decisions" {
			http.NotFound(w, r)
			return
		}
		d.mu.Lock()
		resp := decisionResponse{status: http.StatusOK, body: `{"answers":{"safety":{"choice":"safe"}}}`}
		if len(d.queue) > 0 {
			resp = d.queue[0]
			if len(d.queue) > 1 {
				d.queue = d.queue[1:]
			}
		}
		d.mu.Unlock()
		for k, v := range resp.headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(d.srv.Close)
	t.Setenv(llm.EnvNeuralDeepBaseURL, d.srv.URL)
	t.Setenv("NEURALDEEP_API_KEY", "")
	return d
}

func decisionUnsafeBody() string {
	return decisionBody("unsafe", 0.01, 0.99)
}

// decisionBody answers one choice with the given option and probabilities.
func decisionBody(option string, safe, unsafe float64) string {
	return fmt.Sprintf(`{"answers":{"safety":{"choice":%q,"probabilities":{"safe":%v,"unsafe":%v}}}}`, option, safe, unsafe)
}

// newDecisionsAgent builds an agent whose run_command calls run in bypass
// mode against the stand, the way an unattended turn does.
func newDecisionsAgent(t *testing.T, enabled bool) (*Agent, *session.State, string) {
	t.Helper()
	dir := t.TempDir()
	st := &session.State{
		ID:         "sess_decisions",
		CWD:        dir,
		Mode:       session.ModeAgent,
		SessionDir: dir,
	}
	cfg := &config.Config{
		Paths: config.Paths{Home: t.TempDir()},
		Tools: config.Tools{PermissionMode: config.PermModeBypass},
		Decisions: config.DecisionsConfig{
			Enabled: enabled,
		},
		Providers: []config.ProviderConfig{{Name: "neuraldeep", Type: "neuraldeep", APIKey: "sk-stand-key-0123456789abcd"}},
	}
	ag := NewAgent(cfg, st, &todoSnapshotSender{}, nil)
	return ag, st, dir
}

func runCommandToolCall(t *testing.T, ag *Agent, st *session.State, dir, command string, skipPermission bool) string {
	t.Helper()
	args := `{"command":` + quoteJSON(command) + `}`
	res, err := ag.executeToolCall(
		context.Background(),
		llm.ToolCall{ID: "call_decisions_1", Name: "run_command", InputJSON: args},
		ag.buildToolEnv(string(session.ModeAgent), dir),
		string(session.ModeAgent),
		st.ID,
		skipPermission,
	)
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	return res
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestDecisionsGateBlocksUnsafeCommandWithoutAPrompt(t *testing.T) {
	stand := newDecisionsStand(t, decisionResponse{status: http.StatusOK, body: decisionUnsafeBody()})
	ag, st, dir := newDecisionsAgent(t, true)
	res := runCommandToolCall(t, ag, st, dir, "rm -rf /", false)
	if !strings.HasPrefix(res, commandRejectedAsUnsafePrefix) {
		t.Fatalf("result = %q, want the unsafe rejection", res)
	}
	if strings.Contains(res, "coddy-shell-ok") {
		t.Fatalf("the command ran anyway: %q", res)
	}
	if !strings.Contains(res, "unsafe option at 0.99") || !strings.Contains(res, "threshold 0.50") {
		t.Fatalf("result = %q, want the probability and the threshold", res)
	}
	if stand.calls.Load() != 1 {
		t.Fatalf("calls = %d, want one decision request", stand.calls.Load())
	}
	meta, err := session.ReadToolCallMeta(dir, "call_decisions_1")
	if err != nil || meta.Status != "cancelled" {
		t.Fatalf("meta = %+v err = %v, want status cancelled", meta, err)
	}
	stored, err := session.ReadToolCallResult(dir, "call_decisions_1")
	if err != nil || !strings.HasPrefix(stored, commandRejectedAsUnsafePrefix) {
		t.Fatalf("stored result = %q err = %v, want the rejection in the session", stored, err)
	}
}

func TestDecisionsGateAllowsSafeCommand(t *testing.T) {
	stand := newDecisionsStand(t)
	ag, st, dir := newDecisionsAgent(t, true)
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if !strings.Contains(res, "coddy-shell-ok") {
		t.Fatalf("result = %q, want the command output", res)
	}
	if stand.calls.Load() != 1 {
		t.Fatalf("calls = %d, want one decision request", stand.calls.Load())
	}
	meta, err := session.ReadToolCallMeta(dir, "call_decisions_1")
	if err != nil || meta.Status != "completed" {
		t.Fatalf("meta = %+v err = %v, want status completed", meta, err)
	}
}

func TestDecisionsGateSkippedWhenDisabled(t *testing.T) {
	stand := newDecisionsStand(t, decisionResponse{status: http.StatusOK, body: decisionUnsafeBody()})
	ag, st, dir := newDecisionsAgent(t, false)
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if !strings.Contains(res, "coddy-shell-ok") {
		t.Fatalf("result = %q, want the command output", res)
	}
	if stand.calls.Load() != 0 {
		t.Fatalf("calls = %d, want no decision request", stand.calls.Load())
	}
}

func TestDecisionsGateSkippedWhenTheOperatorApprovedThePrompt(t *testing.T) {
	stand := newDecisionsStand(t, decisionResponse{status: http.StatusOK, body: decisionUnsafeBody()})
	ag, st, dir := newDecisionsAgent(t, true)
	// Ask mode with no allowlist: the call carries an approval a prompt
	// already collected (skipPermission), so the human verdict wins.
	ag.cfg.Tools.PermissionMode = config.PermModeAsk
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", true)
	if !strings.Contains(res, "coddy-shell-ok") {
		t.Fatalf("result = %q, want the command output", res)
	}
	if stand.calls.Load() != 0 {
		t.Fatalf("calls = %d, want no decision request after a human approval", stand.calls.Load())
	}
}

func TestDecisionsGateChecksAllowlistedCommandInAskMode(t *testing.T) {
	stand := newDecisionsStand(t, decisionResponse{status: http.StatusOK, body: decisionUnsafeBody()})
	ag, st, dir := newDecisionsAgent(t, true)
	ag.cfg.Tools.PermissionMode = config.PermModeAsk
	ag.cfg.Tools.CommandAllowlist = []string{"echo"}
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if !strings.HasPrefix(res, commandRejectedAsUnsafePrefix) {
		t.Fatalf("result = %q, want the unsafe rejection", res)
	}
	if stand.calls.Load() != 1 {
		t.Fatalf("calls = %d, want one decision request", stand.calls.Load())
	}
}

func TestDecisionsGateThresholdRejectsBorderlineCommands(t *testing.T) {
	// p(unsafe)=0.6 sits above the default 0.5 but below a strict 0.9.
	newDecisionsStand(t, decisionResponse{status: http.StatusOK, body: decisionBody("unsafe", 0.4, 0.6)})
	ag, st, dir := newDecisionsAgent(t, true)
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if !strings.HasPrefix(res, commandRejectedAsUnsafePrefix) {
		t.Fatalf("result = %q, want the borderline rejection at the default threshold", res)
	}
	if !strings.Contains(res, "threshold 0.50") {
		t.Fatalf("result = %q, want the default threshold in the refusal", res)
	}

	stand2 := newDecisionsStand(t, decisionResponse{status: http.StatusOK, body: decisionBody("unsafe", 0.4, 0.6)})
	ag2, st2, dir2 := newDecisionsAgent(t, true)
	ag2.cfg.Decisions.Threshold = 0.9
	res2 := runCommandToolCall(t, ag2, st2, dir2, "echo coddy-shell-ok", false)
	if !strings.Contains(res2, "coddy-shell-ok") {
		t.Fatalf("result = %q, want the command to run below a high threshold", res2)
	}
	if stand2.calls.Load() != 1 {
		t.Fatalf("calls = %d, want one decision request", stand2.calls.Load())
	}
}

func TestDecisionsGateRunsAChosenUnsafeBelowTheThreshold(t *testing.T) {
	// The endpoint picked unsafe, but at p=0.7 a threshold of 0.9 lets it run.
	stand := newDecisionsStand(t, decisionResponse{status: http.StatusOK, body: decisionBody("unsafe", 0.3, 0.7)})
	ag, st, dir := newDecisionsAgent(t, true)
	ag.cfg.Decisions.Threshold = 0.9
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if strings.HasPrefix(res, commandRejectedAsUnsafePrefix) {
		t.Fatalf("result = %q, want the command to run below the threshold", res)
	}
	if !strings.Contains(res, "coddy-shell-ok") {
		t.Fatalf("result = %q, want the command output", res)
	}
	if stand.calls.Load() != 1 {
		t.Fatalf("calls = %d, want one decision request", stand.calls.Load())
	}
}

func TestDecisionsGateBlocksWhenNoCredential(t *testing.T) {
	stand := newDecisionsStand(t, decisionResponse{status: http.StatusOK, body: decisionUnsafeBody()})
	ag, st, dir := newDecisionsAgent(t, true)
	// No provider row, no env key, no stored login: the check is on, so the
	// command waits for a credential instead of running unchecked.
	ag.cfg.Providers = nil
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if !strings.HasPrefix(res, commandNotExecutedPrefix) {
		t.Fatalf("result = %q, want the not-executed refusal", res)
	}
	if !strings.Contains(res, "decisions safety check is not available") {
		t.Fatalf("result = %q, want the credential hint", res)
	}
	if strings.Contains(res, "coddy-shell-ok") {
		t.Fatalf("the command ran anyway: %q", res)
	}
	if stand.calls.Load() != 0 {
		t.Fatalf("calls = %d, want no request without a credential", stand.calls.Load())
	}
}

func TestDecisionsGateRetriesThenBlocksWhenTheWindowRunsOut(t *testing.T) {
	stand := newDecisionsStand(t, decisionResponse{status: http.StatusTooManyRequests, body: `{"detail":"quota exhausted"}`})
	ag, st, dir := newDecisionsAgent(t, true)
	ag.decisionsRetryWindow = 150 * time.Millisecond
	ag.decisionsRetryBackoff = 20 * time.Millisecond
	start := time.Now()
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if !strings.HasPrefix(res, commandNotExecutedPrefix) {
		t.Fatalf("result = %q, want the not-executed refusal", res)
	}
	if !strings.Contains(res, "retry window") || !strings.Contains(res, "rate_limited") {
		t.Fatalf("result = %q, want the window and the last error", res)
	}
	if strings.Contains(res, "coddy-shell-ok") {
		t.Fatalf("the command ran anyway: %q", res)
	}
	if stand.calls.Load() < 2 {
		t.Fatalf("calls = %d, want retries inside the window", stand.calls.Load())
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("gate took %s, want it bounded by the retry window", elapsed)
	}
}

func TestDecisionsGateRunsTheCommandAfterARateLimitClears(t *testing.T) {
	stand := newDecisionsStand(t,
		decisionResponse{status: http.StatusTooManyRequests, body: `{"detail":"slow down"}`},
		decisionResponse{status: http.StatusOK, body: `{"answers":{"safety":{"choice":"safe"}}}`},
	)
	ag, st, dir := newDecisionsAgent(t, true)
	ag.decisionsRetryBackoff = 10 * time.Millisecond
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if !strings.Contains(res, "coddy-shell-ok") {
		t.Fatalf("result = %q, want the command output after the retry", res)
	}
	if stand.calls.Load() != 2 {
		t.Fatalf("calls = %d, want one refused attempt and one answered", stand.calls.Load())
	}
}

func TestDecisionsProviderPrefersTheConfiguredRow(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{
		{Name: "main", Type: "openai"},
		{Name: "nd-eu", Type: "neuraldeep", APIBase: "https://api.neuraldeep.tech/v1", Proxy: "socks5://127.0.0.1:1080"},
	}}
	got := decisionsProvider(cfg)
	if got.Name != "nd-eu" || got.Proxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("provider = %+v, want the configured neuraldeep row", got)
	}
	fallback := decisionsProvider(&config.Config{})
	if fallback.Name != "neuraldeep" || fallback.Type != "neuraldeep" {
		t.Fatalf("fallback = %+v, want the synthetic neuraldeep row", fallback)
	}
}

func TestRunCommandAndCWD(t *testing.T) {
	cmd, cwd := runCommandAndCWD(`{"command":" rm -rf / ","cwd":"/tmp"}`, nil)
	if cmd != "rm -rf /" || cwd != "/tmp" {
		t.Fatalf("cmd=%q cwd=%q", cmd, cwd)
	}
	cmd, cwd = runCommandAndCWD(`{"command":"ls"}`, &tooling.Env{CWD: "/work"})
	if cmd != "ls" || cwd != "/work" {
		t.Fatalf("cmd=%q cwd=%q, want the env cwd to stand in", cmd, cwd)
	}
	if cmd, _ := runCommandAndCWD(`not json`, nil); cmd != "" {
		t.Fatalf("cmd=%q, want empty on undecodable args", cmd)
	}
}

func TestDecisionsGateStopsAtOnceWhenTheHubRefuses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		detail string
	}{
		{"empty wallet", http.StatusPaymentRequired, "wallet is empty, top up the balance"},
		{"unknown model", http.StatusNotFound, "model 'nosuch' not found, use frida-decisions or clef-flash"},
		{"bad request", http.StatusBadRequest, "questions: field required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stand := newDecisionsStand(t, decisionResponse{status: tc.status, body: `{"detail":` + quoteJSON(tc.detail) + `}`})
			ag, st, dir := newDecisionsAgent(t, true)
			// The default window (two minutes) stays: a refusal no retry can
			// change must not spend it.
			start := time.Now()
			res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
			if !strings.HasPrefix(res, commandNotExecutedPrefix) {
				t.Fatalf("result = %q, want the not-executed refusal", res)
			}
			if !strings.Contains(res, tc.detail) {
				t.Fatalf("result = %q, want the hub's reason", res)
			}
			if strings.Contains(res, "Provide a NeuralDeep credential") {
				t.Fatalf("result = %q, want no credential hint for a key the hub accepted", res)
			}
			if strings.Contains(res, "coddy-shell-ok") {
				t.Fatalf("the command ran anyway: %q", res)
			}
			if n := stand.calls.Load(); n != 1 {
				t.Fatalf("calls = %d, want one request and no retries", n)
			}
			if elapsed := time.Since(start); elapsed > 10*time.Second {
				t.Fatalf("gate took %s, want an immediate refusal", elapsed)
			}
		})
	}
}

func TestDecisionsGateStopsACommandTheModelReadOnlyInPart(t *testing.T) {
	// The hub cut the state: the safe verdict covers only the head of the
	// command, and what the model never read may be the dangerous part.
	stand := newDecisionsStand(t, decisionResponse{status: http.StatusOK,
		body: `{"answers":{"safety":{"choice":"safe","probabilities":{"safe":0.97,"unsafe":0.03}}},"usage":{"state_tokens":512,"state_truncated":true}}`})
	ag, st, dir := newDecisionsAgent(t, true)
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if !strings.HasPrefix(res, commandNotExecutedPrefix) {
		t.Fatalf("result = %q, want the not-executed refusal", res)
	}
	if !strings.Contains(res, "too long") || !strings.Contains(res, "clef-flash") {
		t.Fatalf("result = %q, want the length reason and the model that reads more", res)
	}
	if strings.Contains(res, "coddy-shell-ok") {
		t.Fatalf("the command ran anyway: %q", res)
	}
	if stand.calls.Load() != 1 {
		t.Fatalf("calls = %d, want one decision request", stand.calls.Load())
	}
	meta, err := session.ReadToolCallMeta(dir, "call_decisions_1")
	if err != nil || meta.Status != "cancelled" {
		t.Fatalf("meta = %+v err = %v, want status cancelled", meta, err)
	}
}

func TestDecisionsGateRejectsAnUnsafeHeadOfATruncatedCommand(t *testing.T) {
	// What the model did read is already unsafe: that verdict stands.
	newDecisionsStand(t, decisionResponse{status: http.StatusOK,
		body: `{"answers":{"safety":{"choice":"unsafe","probabilities":{"safe":0.02,"unsafe":0.98}}},"usage":{"state_truncated":true}}`})
	ag, st, dir := newDecisionsAgent(t, true)
	res := runCommandToolCall(t, ag, st, dir, "rm -rf /", false)
	if !strings.HasPrefix(res, commandRejectedAsUnsafePrefix) {
		t.Fatalf("result = %q, want the unsafe rejection", res)
	}
}

func TestDecisionsGateNamesTheCredentialOfItsRow(t *testing.T) {
	// A neuraldeep row named hub reads HUB_API_KEY, so the refusal must not
	// send the operator to NEURALDEEP_API_KEY.
	stand := newDecisionsStand(t)
	ag, st, dir := newDecisionsAgent(t, true)
	ag.cfg.Providers = []config.ProviderConfig{{Name: "hub", Type: "neuraldeep"}}
	t.Setenv("HUB_API_KEY", "")
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if !strings.HasPrefix(res, commandNotExecutedPrefix) {
		t.Fatalf("result = %q, want the not-executed refusal", res)
	}
	if !strings.Contains(res, "HUB_API_KEY") || !strings.Contains(res, "coddy providers login hub") {
		t.Fatalf("result = %q, want the row's variable and sign-in command", res)
	}
	if strings.Contains(res, "NEURALDEEP_API_KEY") {
		t.Fatalf("result = %q, want no variable the row does not read", res)
	}
	if stand.calls.Load() != 0 {
		t.Fatalf("calls = %d, want no request without a credential", stand.calls.Load())
	}
}

func TestDecisionsGateStopsOnAnAnswerOutsideTheQuestion(t *testing.T) {
	stand := newDecisionsStand(t, decisionResponse{status: http.StatusOK, body: `{"answers":{"safety":{"choice":"dangerous"}}}`})
	ag, st, dir := newDecisionsAgent(t, true)
	res := runCommandToolCall(t, ag, st, dir, "echo coddy-shell-ok", false)
	if !strings.HasPrefix(res, commandNotExecutedPrefix) {
		t.Fatalf("result = %q, want the not-executed refusal", res)
	}
	if strings.Contains(res, "coddy-shell-ok") {
		t.Fatalf("the command ran anyway: %q", res)
	}
	if stand.calls.Load() != 1 {
		t.Fatalf("calls = %d, want one request and no retries", stand.calls.Load())
	}
}

package agent

// Godog harness for features/context_in_turn_compaction.feature: drives the real
// Agent through one long turn of a scripted model that reads files in parallel,
// against a provider that refuses a request larger than its window the way a
// local backend does, and asserts what every request carried and what the
// transcript kept.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// inTurnPrompt is the one prompt of the scenarios; its marker lets a request be
// checked for carrying it without caring where.
const inTurnPrompt = "AUDIT-PROMPT: read every module of the project, one file after another, and report what each does."

// inTurnProvider is a model that makes a fixed number of steps of parallel reads
// and then answers, and a backend that refuses a request larger than refuseAt
// tokens (session.EstimateContextTokens over what the request carries, the tool
// definitions included) with the text of a local server's context-limit refusal.
// It answers a summarization request (Complete) with a fixed summary.
type inTurnProvider struct {
	steps    int
	fanOut   int
	files    func(step, k int) string // path of the k-th file read at a step
	refuseAt int

	answered  int // steps the model made, refused requests excluded
	accepted  [][]llm.Message
	refused   []inTurnRefusal
	issued    []string // the ids of every tool call the model issued
	summaries int
	// afterRefusal is the first accepted request that followed a refusal.
	afterRefusal []llm.Message
}

type inTurnRefusal struct {
	messages []llm.Message
	tokens   int
}

func (p *inTurnProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.summaries++
	return &llm.Response{Content: fmt.Sprintf("SUMMARY-%d: the files read so far were summarized.", p.summaries), StopReason: "end_turn"}, nil
}

// requestTokens is the provider's own count of a request. It is built on the
// estimate Coddy's trigger uses - the system message, the conversation with its
// reasoning and tool calls, and the schemas of the tool definitions - so a
// scenario can size a window against it without guessing at a tokenizer.
func requestTokens(messages []llm.Message, defs []llm.ToolDefinition) int {
	total := 0
	var history []llm.Message
	for _, m := range messages {
		if m.Role == llm.RoleSystem {
			total += session.EstimateContextTokens(m.Content)
			continue
		}
		history = append(history, m)
	}
	total += conversationTokens(history, false)
	for _, d := range defs {
		if raw, err := json.Marshal(d.InputSchema); err == nil {
			total += session.EstimateContextTokens(string(raw))
		}
	}
	return total
}

func (p *inTurnProvider) Stream(_ context.Context, messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	if p.refuseAt > 0 {
		if n := requestTokens(messages, defs); n > p.refuseAt {
			p.refused = append(p.refused, inTurnRefusal{messages: append([]llm.Message(nil), messages...), tokens: n})
			return nil, fmt.Errorf("provider %q (http://127.0.0.1:8080/v1): openai stream: POST %q: 404 Not Found %q",
				"local", "http://127.0.0.1:8080/v1/chat/completions",
				fmt.Sprintf("Context limit is %d tokens; prompt=%d leaves 0 output tokens, below the minimum 16", p.refuseAt, n))
		}
	}
	req := append([]llm.Message(nil), messages...)
	if len(p.refused) > 0 && p.afterRefusal == nil {
		p.afterRefusal = req
	}
	p.accepted = append(p.accepted, req)
	if p.answered >= p.steps {
		const answer = "all modules audited"
		onChunk(llm.StreamChunk{TextDelta: answer})
		return &llm.Response{Content: answer, StopReason: "end_turn"}, nil
	}
	calls := make([]llm.ToolCall, 0, p.fanOut)
	for k := 0; k < p.fanOut; k++ {
		raw, _ := json.Marshal(map[string]interface{}{"path": p.files(p.answered, k)})
		id := fmt.Sprintf("call_%02d_%d", p.answered, k)
		calls = append(calls, llm.ToolCall{ID: id, Name: "read", InputJSON: string(raw)})
		p.issued = append(p.issued, id)
	}
	p.answered++
	return &llm.Response{ToolCalls: calls, StopReason: "tool_use"}, nil
}

type inTurnFeatureState struct {
	tmpDirs  []string
	window   int
	served   int
	evictOff bool
	steps    int
	fanOut   int
	refuseAt int

	provider *inTurnProvider
	st       *session.State
	ag       *Agent
	stop     string
	runErr   error
}

func (s *inTurnFeatureState) reset() error {
	s.close()
	s.window, s.served, s.evictOff, s.steps, s.fanOut, s.refuseAt = 0, 0, false, 0, 0, 0
	s.provider, s.st, s.ag, s.stop, s.runErr = nil, nil, nil, "", nil
	return nil
}

func (s *inTurnFeatureState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
}

func (s *inTurnFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-inturn-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

func (s *inTurnFeatureState) modelWindow(tokens int) error {
	s.window = tokens
	return nil
}

func (s *inTurnFeatureState) modelWindowServedUpTo(tokens, served int) error {
	s.window, s.served = tokens, served
	return nil
}

func (s *inTurnFeatureState) evictionOff() error {
	s.evictOff = true
	return nil
}

func (s *inTurnFeatureState) modelReads(fanOut, steps int) error {
	s.fanOut, s.steps = fanOut, steps
	return nil
}

func (s *inTurnFeatureState) providerRefusesAboveWindow() error {
	s.refuseAt = s.window
	return nil
}

func (s *inTurnFeatureState) providerRefusesAboveWhatItServes() error {
	s.refuseAt = s.served
	return nil
}

// inTurnLongTurn describes one long turn: a model that reads fanOut files in
// parallel at each of steps steps and then answers, on a window of window tokens
// that a provider refusing above refuseAt tokens (0: never) serves.
type inTurnLongTurn struct {
	window   int
	steps    int
	fanOut   int
	refuseAt int
	evictOff bool
}

// inTurnWorld is a long turn that has been set up and run.
type inTurnWorld struct {
	provider *inTurnProvider
	st       *session.State
	ag       *Agent
	stop     string
	runErr   error
}

// writeModules makes the workspace the model reads: one file per read, each a
// few thousand characters, so a step of parallel reads costs a few thousand
// tokens of context.
func (d inTurnLongTurn) writeModules(cwd string) (func(step, k int) string, error) {
	for step := 0; step < d.steps; step++ {
		for k := 0; k < d.fanOut; k++ {
			var b strings.Builder
			for line := 1; line <= 40; line++ {
				fmt.Fprintf(&b, "module %02d-%d line %02d: this part of the project parses its input and hands the result on\n", step, k, line)
			}
			name := fmt.Sprintf("module_%02d_%d.txt", step, k)
			if err := os.WriteFile(filepath.Join(cwd, name), []byte(b.String()), 0o644); err != nil {
				return nil, err
			}
		}
	}
	return func(step, k int) string { return fmt.Sprintf("module_%02d_%d.txt", step, k) }, nil
}

// run sets the turn up in directories from tempDir and sends the one prompt.
func (d inTurnLongTurn) run(tempDir func() (string, error)) (*inTurnWorld, error) {
	cwd, err := tempDir()
	if err != nil {
		return nil, err
	}
	store, err := tempDir()
	if err != nil {
		return nil, err
	}
	sessionDir := filepath.Join(store, "bundle")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, err
	}
	files, err := d.writeModules(cwd)
	if err != nil {
		return nil, err
	}
	w := &inTurnWorld{provider: &inTurnProvider{steps: d.steps, fanOut: d.fanOut, files: files, refuseAt: d.refuseAt}}

	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: d.window}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	if d.evictOff {
		off := false
		cfg.Compaction.ResultEviction.Enabled = &off
	}
	w.st = &session.State{ID: "sess_bdd_in_turn", CWD: cwd, Mode: session.ModeAgent, SessionDir: sessionDir}
	w.ag = NewAgent(cfg, w.st, resumePermissionSender{}, nil)
	w.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return w.provider, nil }
	w.stop, w.runErr = w.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: inTurnPrompt}})
	return w, nil
}

func (s *inTurnFeatureState) userSendsOnePrompt() error {
	w, err := inTurnLongTurn{window: s.window, steps: s.steps, fanOut: s.fanOut, refuseAt: s.refuseAt, evictOff: s.evictOff}.run(s.tempDir)
	if err != nil {
		return err
	}
	s.provider, s.st, s.ag, s.stop, s.runErr = w.provider, w.st, w.ag, w.stop, w.runErr
	return nil
}

func (s *inTurnFeatureState) noRequestWasRefused() error {
	if n := len(s.provider.refused); n != 0 {
		return fmt.Errorf("the provider refused %d request(s), the first at %d tokens against a window of %d", n, s.provider.refused[0].tokens, s.provider.refuseAt)
	}
	return nil
}

func (s *inTurnFeatureState) turnEndedWithAnswer() error {
	if s.runErr != nil {
		return fmt.Errorf("the turn failed (stop %q): %v", s.stop, s.runErr)
	}
	msgs := s.st.GetMessages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(last.Content, "all modules audited") {
		return fmt.Errorf("the turn did not end with the model's answer: %+v", last)
	}
	return nil
}

func (s *inTurnFeatureState) summaryRows() []llm.Message {
	var rows []llm.Message
	for _, m := range s.st.GetMessages() {
		if m.CompactionSummary {
			rows = append(rows, m)
		}
	}
	return rows
}

func (s *inTurnFeatureState) turnFoldedMoreThanOnce() error {
	if n := len(s.summaryRows()); n < 2 {
		return fmt.Errorf("the transcript holds %d summary row(s), want the turn folded more than once", n)
	}
	return nil
}

// carriesPrompt reports whether a request begins its history with the prompt:
// as the first user message, or at the start of the summary row of a fold.
func carriesPrompt(req []llm.Message) error {
	for _, m := range req {
		if m.Role == llm.RoleSystem {
			continue
		}
		if m.Role != llm.RoleUser {
			return fmt.Errorf("the history begins with a %s message, not the prompt", m.Role)
		}
		if m.CompactionSummary {
			if !strings.HasPrefix(m.Content, inTurnPrompt) {
				return fmt.Errorf("the summary row does not begin with the prompt: %q", firstChars(m.Content, 120))
			}
			return nil
		}
		if m.Content != inTurnPrompt {
			return fmt.Errorf("the first user message is not the prompt verbatim: %q", firstChars(m.Content, 120))
		}
		return nil
	}
	return fmt.Errorf("the request holds no history")
}

func firstChars(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func (s *inTurnFeatureState) everyRequestCarriedThePrompt() error {
	if len(s.provider.accepted) == 0 {
		return fmt.Errorf("the provider received no request")
	}
	for i, req := range s.provider.accepted {
		if err := carriesPrompt(req); err != nil {
			return fmt.Errorf("request %d: %w", i+1, err)
		}
	}
	return nil
}

func (s *inTurnFeatureState) everyToolResultAnswersACall() error {
	for i, req := range s.provider.accepted {
		if issues := session.ValidateToolPairing(req); len(issues) > 0 {
			return fmt.Errorf("request %d has a malformed tool-call history: %s", i+1, formatToolPairingIssues(issues))
		}
	}
	return nil
}

func (s *inTurnFeatureState) transcriptHoldsEveryOriginalMessage() error {
	msgs := s.st.GetMessages()
	if len(msgs) == 0 || msgs[0].Role != llm.RoleUser || msgs[0].Content != inTurnPrompt {
		return fmt.Errorf("the transcript does not begin with the prompt: %+v", msgs)
	}
	results := map[string]bool{}
	calls := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
		}
		if m.Role == llm.RoleTool && strings.TrimSpace(m.Content) != "" {
			results[m.ToolCallID] = true
		}
	}
	for _, id := range s.provider.issued {
		if !calls[id] || !results[id] {
			return fmt.Errorf("the transcript lost the call or the result of %s", id)
		}
		// A fold writes a summary row; it never replaces a message.
	}
	return nil
}

func (s *inTurnFeatureState) providerRefusedOneRequest() error {
	if n := len(s.provider.refused); n != 1 {
		return fmt.Errorf("the provider refused %d request(s), want exactly one", n)
	}
	return nil
}

func (s *inTurnFeatureState) turnCompactedOnce() error {
	rows := s.summaryRows()
	if len(rows) != 1 {
		return fmt.Errorf("the transcript holds %d summary row(s), want one compaction", len(rows))
	}
	return nil
}

func (s *inTurnFeatureState) requestSentAgainIsSmaller() error {
	if len(s.provider.refused) == 0 || s.provider.afterRefusal == nil {
		return fmt.Errorf("no request was sent again after a refusal")
	}
	refused := s.provider.refused[0]
	again := requestTokens(s.provider.afterRefusal, nil)
	before := requestTokens(refused.messages, nil)
	if again >= before {
		return fmt.Errorf("the request sent again (%d tokens) is not smaller than the refused one (%d)", again, before)
	}
	if err := carriesPrompt(s.provider.afterRefusal); err != nil {
		return fmt.Errorf("the request sent again: %w", err)
	}
	return nil
}

func (s *inTurnFeatureState) transcriptHoldsSummaryStartingWithPrompt() error {
	for _, m := range s.summaryRows() {
		if strings.HasPrefix(m.Content, inTurnPrompt) {
			return nil
		}
	}
	return fmt.Errorf("no summary row begins with the prompt: %v", s.summaryRows())
}

func initializeInTurnScenario(sc *godog.ScenarioContext) {
	s := &inTurnFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a model window of (\d+) tokens$`, s.modelWindow)
	sc.Step(`^a model window of (\d+) tokens that the provider serves only up to (\d+)$`, s.modelWindowServedUpTo)
	sc.Step(`^result eviction is off, so only compaction can keep the turn inside the window$`, s.evictionOff)
	sc.Step(`^a model that reads (\d+) files in parallel at each of (\d+) steps and then answers$`, s.modelReads)
	sc.Step(`^the provider refuses any request larger than the window$`, s.providerRefusesAboveWindow)
	sc.Step(`^the provider refuses any request larger than what it serves$`, s.providerRefusesAboveWhatItServes)
	sc.Step(`^the user sends one prompt$`, s.userSendsOnePrompt)
	sc.Step(`^no request was refused$`, s.noRequestWasRefused)
	sc.Step(`^the turn ended with the model's answer$`, s.turnEndedWithAnswer)
	sc.Step(`^the turn was folded more than once$`, s.turnFoldedMoreThanOnce)
	sc.Step(`^every request carried the prompt verbatim$`, s.everyRequestCarriedThePrompt)
	sc.Step(`^every tool result in each request answers a call that request carries$`, s.everyToolResultAnswersACall)
	sc.Step(`^the transcript still holds every original message$`, s.transcriptHoldsEveryOriginalMessage)
	sc.Step(`^the provider refused one request as larger than its window$`, s.providerRefusedOneRequest)
	sc.Step(`^the turn was compacted inside the turn once$`, s.turnCompactedOnce)
	sc.Step(`^the request sent again is smaller than the refused one and carries the prompt$`, s.requestSentAgainIsSmaller)
	sc.Step(`^the transcript holds a summary row that starts with the prompt$`, s.transcriptHoldsSummaryStartingWithPrompt)
}

func TestContextInTurnCompactionFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "context-in-turn-compaction",
		ScenarioInitializer: initializeInTurnScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/context_in_turn_compaction.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("context in-turn compaction feature suite failed")
	}
}

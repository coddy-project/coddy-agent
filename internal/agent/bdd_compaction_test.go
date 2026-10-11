package agent

// Godog harness for features/context_compaction.feature: drives the real
// Agent (CompactSession + Run) with a fake LLM provider, asserting what the
// next LLM request contains after the session history was compacted.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// bddCompactionProvider returns a canned summary for Complete (the compaction
// call) and a canned answer for Stream (agent turns), recording every request.
type bddCompactionProvider struct {
	completeSeen [][]llm.Message
	streamSeen   [][]llm.Message
	// toolCall, when set, is what the first Stream answers with; the second
	// answers with plain text, so a turn that calls a tool still ends.
	toolCall *llm.ToolCall
	// streamErr, when set, is what every Stream refuses with.
	streamErr error
	// completeLimit, when positive, is the length of a summarization request
	// (its transcript message) the provider still accepts; a longer one is
	// refused as too large for the model's window, and counted.
	completeLimit   int
	completeRefused int
}

func (p *bddCompactionProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.completeSeen = append(p.completeSeen, append([]llm.Message(nil), messages...))
	if p.completeLimit > 0 && len(messages[len(messages)-1].Content) > p.completeLimit {
		p.completeRefused++
		return nil, fmt.Errorf("400 Bad Request: This model's maximum context length is 8192 tokens. However, your messages resulted in 12000 tokens. Please reduce the length of the messages.")
	}
	return &llm.Response{Content: "CANNED-SUMMARY of the earlier exchanges", StopReason: "end_turn"}, nil
}

func (p *bddCompactionProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.streamSeen = append(p.streamSeen, append([]llm.Message(nil), messages...))
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	if p.toolCall != nil {
		tc := *p.toolCall
		p.toolCall = nil
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_calls"}, nil
	}
	onChunk(llm.StreamChunk{TextDelta: "post-compaction answer"})
	return &llm.Response{Content: "post-compaction answer", StopReason: "end_turn"}, nil
}

type compactionUsageSender struct {
	resumePermissionSender
	updates []interface{}
}

func (s *compactionUsageSender) SendSessionUpdate(_ string, update interface{}) error {
	s.updates = append(s.updates, update)
	return nil
}

type compactionFeatureState struct {
	tmpDirs    []string
	st         *session.State
	ag         *Agent
	provider   *bddCompactionProvider
	sender     *compactionUsageSender
	exchanges  int
	beforeUsed int
	// filler pads every answer, so a scenario can build a history no single
	// summarization request can hold.
	filler string
	// stop and runErr are how the turn of a scenario meant to fail ended.
	stop   string
	runErr error
}

func (s *compactionFeatureState) reset() error {
	s.close()
	s.provider = &bddCompactionProvider{}
	s.sender = &compactionUsageSender{}
	s.exchanges = 0
	s.beforeUsed = 0
	s.filler = ""
	s.stop, s.runErr = "", nil
	return nil
}

func (s *compactionFeatureState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.st = nil
	s.ag = nil
}

func (s *compactionFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-compact-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

func (s *compactionFeatureState) sessionWithExchanges(n int) error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.st = &session.State{
		ID:         "sess_bdd_compaction",
		CWD:        cwd,
		Mode:       session.ModeAgent,
		SessionDir: sessionDir,
	}
	for i := 1; i <= n; i++ {
		s.st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("question %d", i)})
		s.st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d%s", i, s.filler)})
	}
	s.exchanges = n

	keep := 2
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
		Compaction: config.Compaction{
			KeepRecentTurns: &keep,
		},
	}
	s.ag = NewAgent(cfg, s.st, s.sender, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return s.provider, nil
	}
	return nil
}

func (s *compactionFeatureState) compactSession() error {
	if s.ag == nil {
		return fmt.Errorf("no session prepared")
	}
	res, err := s.ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.Summary) == "" {
		return fmt.Errorf("compaction returned an empty summary")
	}
	return nil
}

func (s *compactionFeatureState) summaryInsertedIntoTranscript() error {
	for _, m := range s.st.GetMessages() {
		if m.CompactionSummary {
			return nil
		}
	}
	return fmt.Errorf("no compaction summary row in transcript")
}

// summaryRowBeginsWithThePrompt checks the row a compact_context call wrote in
// the first turn of a session: the head it folded held the prompt being answered,
// so the row starts with it, verbatim, and the model keeps knowing what it was
// asked (issue #490).
func (s *compactionFeatureState) summaryRowBeginsWithThePrompt() error {
	for _, m := range s.st.GetMessages() {
		if !m.CompactionSummary {
			continue
		}
		if !strings.HasPrefix(m.Content, "probe prompt\n\n") {
			return fmt.Errorf("the summary row does not begin with the prompt: %q", firstChars(m.Content, 120))
		}
		if n := strings.Count(m.Content, "probe prompt"); n != 1 {
			return fmt.Errorf("the summary row holds the prompt %d times, want once", n)
		}
		return nil
	}
	return fmt.Errorf("no compaction summary row in transcript")
}

func (s *compactionFeatureState) transcriptContainsAllExchanges() error {
	joined := transcriptText(s.st.GetMessages())
	for i := 1; i <= s.exchanges; i++ {
		if !strings.Contains(joined, fmt.Sprintf("question %d", i)) ||
			!strings.Contains(joined, fmt.Sprintf("answer %d", i)) {
			return fmt.Errorf("transcript lost exchange %d", i)
		}
	}
	return nil
}

func transcriptText(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// nextLLMRequest sends a probe prompt through the agent and returns the
// message slice the provider received for it.
func (s *compactionFeatureState) nextLLMRequest() ([]llm.Message, error) {
	if len(s.provider.streamSeen) == 0 {
		if _, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "probe prompt"}}); err != nil {
			return nil, err
		}
	}
	if len(s.provider.streamSeen) == 0 {
		return nil, fmt.Errorf("provider received no stream request")
	}
	return s.provider.streamSeen[len(s.provider.streamSeen)-1], nil
}

func (s *compactionFeatureState) nextRequestStartsFromSummary() error {
	req, err := s.nextLLMRequest()
	if err != nil {
		return err
	}
	for _, m := range req {
		if m.Role == llm.RoleSystem {
			continue
		}
		if !m.CompactionSummary || !strings.Contains(m.Content, "CANNED-SUMMARY") {
			return fmt.Errorf("first history message is not the compaction summary: %+v", m)
		}
		return nil
	}
	return fmt.Errorf("request had no history messages")
}

func (s *compactionFeatureState) nextRequestContainsLastExchanges(keep int) error {
	req, err := s.nextLLMRequest()
	if err != nil {
		return err
	}
	joined := transcriptText(req)
	for i := s.exchanges - keep + 1; i <= s.exchanges; i++ {
		if !strings.Contains(joined, fmt.Sprintf("question %d", i)) ||
			!strings.Contains(joined, fmt.Sprintf("answer %d", i)) {
			return fmt.Errorf("kept exchange %d missing from LLM request", i)
		}
	}
	return nil
}

func (s *compactionFeatureState) nextRequestOmitsOlderExchanges() error {
	req, err := s.nextLLMRequest()
	if err != nil {
		return err
	}
	// Exchanges before the kept tail must not be replayed verbatim.
	for _, m := range req {
		if m.CompactionSummary || m.Role == llm.RoleSystem {
			continue
		}
		for i := 1; i <= s.exchanges-2; i++ {
			if strings.Contains(m.Content, fmt.Sprintf("question %d", i)) ||
				strings.Contains(m.Content, fmt.Sprintf("answer %d", i)) {
				return fmt.Errorf("older exchange %d leaked into LLM request: %q", i, m.Content)
			}
		}
	}
	return nil
}

func (s *compactionFeatureState) userSendsNewPrompt() error {
	_, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "probe prompt"}})
	return err
}

// contextWindowIs sets the window the model entry declares, which is what the
// trigger measures against and what an overflow error reports.
func (s *compactionFeatureState) contextWindowIs(tokens int) error {
	if s.ag == nil {
		return fmt.Errorf("no session prepared")
	}
	s.ag.cfg.Models[0].MaxContextTokens = tokens
	return nil
}

// providerRefusesEveryTurnRequest makes every model call of the turn fail the
// way a backend answers a request that does not fit: a 404 whose body names the
// limit. The summarizer's calls (Complete) are not the turn's and are answered.
func (s *compactionFeatureState) providerRefusesEveryTurnRequest(body string) error {
	s.provider.streamErr = fmt.Errorf("provider %q (http://127.0.0.1:8080/v1): openai stream: POST %q: 404 Not Found %q",
		"local", "http://127.0.0.1:8080/v1/chat/completions", body)
	return nil
}

func (s *compactionFeatureState) userSendsPromptAndTheTurnFails() error {
	s.stop, s.runErr = s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "resume"}})
	if s.runErr == nil {
		return fmt.Errorf("the turn ended with stop reason %q and no error", s.stop)
	}
	return nil
}

func (s *compactionFeatureState) turnWasRefusedWithAnExplanation() error {
	if s.stop != string(acp.StopReasonRefused) {
		return fmt.Errorf("stop reason = %q, want %q", s.stop, acp.StopReasonRefused)
	}
	if !strings.HasPrefix(s.runErr.Error(), "context window exceeded") {
		return fmt.Errorf("the error does not start by saying the context window was exceeded: %v", s.runErr)
	}
	return nil
}

func (s *compactionFeatureState) errorSays(want string) error {
	if !strings.Contains(s.runErr.Error(), want) {
		return fmt.Errorf("the error does not say %q: %v", want, s.runErr)
	}
	return nil
}

// turnRequestSentTwice holds the story of a turn whose request was refused as
// too large: the loop compacted the turn once (a summary row folds the earlier
// exchanges, the prompt stays), asked the same step again, was refused again
// and stopped - it neither gave up at the first refusal nor went on asking.
func (s *compactionFeatureState) turnRequestSentTwice() error {
	if got := len(s.provider.streamSeen); got != 2 {
		return fmt.Errorf("the turn's request was sent %d times, want twice", got)
	}
	if len(s.provider.completeSeen) != 1 {
		return fmt.Errorf("the summarizer was called %d times, want once", len(s.provider.completeSeen))
	}
	// The exchanges here are a few words each, so the summary row is no smaller
	// than what it replaced; that the second request is smaller is held by
	// features/context_in_turn_compaction.feature, over a history with some bulk.
	second := s.provider.streamSeen[1]
	if !strings.Contains(transcriptText(second), "resume") {
		return fmt.Errorf("the request sent after the compaction lost the prompt")
	}
	folded := false
	for _, m := range second {
		folded = folded || (m.CompactionSummary && strings.Contains(m.Content, "CANNED-SUMMARY"))
	}
	if !folded {
		return fmt.Errorf("the request sent after the compaction does not start from the summary: %s", transcriptText(second))
	}
	return nil
}

// summarizerRefusesLargeRequests gives the summarizer a window of its own that
// is smaller than the pass the fold sizes for it: the situation of a backend
// whose real window is below the one it was configured with.
func (s *compactionFeatureState) summarizerRefusesLargeRequests() error {
	s.provider.completeLimit = 1500
	return nil
}

func (s *compactionFeatureState) summarizerRefusedARequestAsTooLarge() error {
	if s.provider.completeRefused == 0 {
		return fmt.Errorf("the summarizer never refused a request, so nothing was asked again with less")
	}
	return nil
}

func (s *compactionFeatureState) agentRepliesSuccessfully() error {
	msgs := s.st.GetMessages()
	if len(msgs) == 0 {
		return fmt.Errorf("no messages in session")
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(last.Content, "post-compaction answer") {
		return fmt.Errorf("agent did not reply: %+v", last)
	}
	return nil
}

func (s *compactionFeatureState) clientObservedContextUsageBeforeCompaction() error {
	b := &session.ContextBreakdown{
		SystemPrompt: 100,
		Conversation: 10000,
	}
	b.Sum()
	s.beforeUsed = b.EstimatedTotal
	s.st.SetLastContextBreakdown(b)
	s.sender.updates = nil
	return nil
}

func (s *compactionFeatureState) lastACPUsageUpdate() (used, size int, err error) {
	for i := len(s.sender.updates) - 1; i >= 0; i-- {
		raw, marshalErr := json.Marshal(s.sender.updates[i])
		if marshalErr != nil {
			return 0, 0, marshalErr
		}
		var update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Used          int    `json:"used"`
			Size          int    `json:"size"`
		}
		if unmarshalErr := json.Unmarshal(raw, &update); unmarshalErr != nil {
			return 0, 0, unmarshalErr
		}
		if update.SessionUpdate == "usage_update" {
			return update.Used, update.Size, nil
		}
	}
	return 0, 0, fmt.Errorf("ACP client received no usage_update: %#v", s.sender.updates)
}

func (s *compactionFeatureState) clientReceivesSmallerContextUsage() error {
	used, size, err := s.lastACPUsageUpdate()
	if err != nil {
		return err
	}
	if used >= s.beforeUsed {
		return fmt.Errorf("compacted usage = %d, want less than %d", used, s.beforeUsed)
	}
	if size != 128000 {
		return fmt.Errorf("context size = %d, want 128000", size)
	}
	return nil
}

func (s *compactionFeatureState) reportedACPUsageMatchesContext() error {
	used, _, err := s.lastACPUsageUpdate()
	if err != nil {
		return err
	}
	b := s.st.GetLastContextBreakdown()
	if b == nil {
		return fmt.Errorf("session has no context breakdown after compaction")
	}
	wantConversation := conversationTokens(session.MessagesForLLM(s.st.GetMessages()), s.ag.modelReadsImages())
	if b.Conversation != wantConversation {
		return fmt.Errorf("conversation tokens = %d, want %d", b.Conversation, wantConversation)
	}
	if used != b.EstimatedTotal {
		return fmt.Errorf("ACP used = %d, context breakdown total = %d", used, b.EstimatedTotal)
	}
	return nil
}

// summarizerFitsPartOfTheHistory shrinks the summarizer's window so the fold
// cannot send the whole head in one request, the situation a session that ran
// far past its window arrives at /compact in.
func (s *compactionFeatureState) summarizerFitsPartOfTheHistory() error {
	if s.ag == nil {
		return fmt.Errorf("no session prepared")
	}
	// The same exchanges, each long enough that a pass bounded by the floor
	// (compactionMinChunkTokens) holds at most one of them.
	s.filler = "\n" + strings.Repeat("a line of the log this turn pasted into the session. ", 120)
	if err := s.sessionWithExchanges(s.exchanges); err != nil {
		return err
	}
	// A window this small resolves the pass budget down to the floor.
	s.ag.cfg.Models[0].MaxContextTokens = 1
	return nil
}

func (s *compactionFeatureState) summarizerCalledMoreThanOnce() error {
	if len(s.provider.completeSeen) < 2 {
		return fmt.Errorf("summarizer called %d time(s), want more than one pass", len(s.provider.completeSeen))
	}
	return nil
}

func (s *compactionFeatureState) laterRequestsCarryTheSummarySoFar() error {
	if len(s.provider.completeSeen) < 2 {
		return fmt.Errorf("only %d summarization request(s)", len(s.provider.completeSeen))
	}
	for i, req := range s.provider.completeSeen[1:] {
		text := transcriptText(req)
		if !strings.Contains(text, "<summary-so-far>") || !strings.Contains(text, "CANNED-SUMMARY") {
			return fmt.Errorf("pass %d did not carry the summary so far: %q", i+2, text)
		}
	}
	return nil
}

// toolCallRowStatuses collects the tool-call rows the client was sent, keyed by
// the id the row carries.
func (s *compactionFeatureState) toolCallRows() ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	for _, u := range s.sender.updates {
		raw, err := json.Marshal(u)
		if err != nil {
			return nil, err
		}
		var row map[string]interface{}
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		switch row["sessionUpdate"] {
		case "tool_call", "tool_call_update":
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *compactionFeatureState) clientToldCompactionWasRunning() error {
	rows, err := s.toolCallRows()
	if err != nil {
		return err
	}
	announced := false
	progressed := false
	for _, row := range rows {
		if row["title"] == tools.ToolCompactContext {
			announced = true
		}
		if content, ok := row["content"].([]interface{}); ok {
			for _, item := range content {
				m, _ := item.(map[string]interface{})
				block, _ := m["content"].(map[string]interface{})
				if text, _ := block["text"].(string); strings.Contains(text, "compacting context: pass ") {
					progressed = true
				}
			}
		}
	}
	if !announced {
		return fmt.Errorf("no %s row announced to the client: %#v", tools.ToolCompactContext, rows)
	}
	if !progressed {
		return fmt.Errorf("client never saw a pass of the fold: %#v", rows)
	}
	return nil
}

// modelCallsCompactContext runs a turn whose first step is the model asking for
// a compaction, so the fold goes through the tool the way the model reaches it.
func (s *compactionFeatureState) modelCallsCompactContext() error {
	return s.modelCallsCompactContextWith(`{"instructions":"keep the file paths"}`)
}

func (s *compactionFeatureState) modelCallsCompactContextWithModel(model string) error {
	return s.modelCallsCompactContextWith(fmt.Sprintf(`{"model":%q}`, model))
}

func (s *compactionFeatureState) modelCallsCompactContextWith(argsJSON string) error {
	if s.ag == nil {
		return fmt.Errorf("no session prepared")
	}
	s.provider.toolCall = &llm.ToolCall{
		ID:        "call_compact_1",
		Name:      tools.ToolCompactContext,
		InputJSON: argsJSON,
	}
	_, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "probe prompt"}})
	return err
}

func (s *compactionFeatureState) toolResultSaysWhatWasCompacted() error {
	for _, m := range s.st.GetMessages() {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "Context compacted:") {
			return nil
		}
	}
	return fmt.Errorf("no compact_context tool result in the transcript: %s", transcriptText(s.st.GetMessages()))
}

// requestAfterToolCallStartsFromSummary reads the request the loop sent after
// the tool folded the history: it must replay the summary, not the exchanges
// the fold replaced.
func (s *compactionFeatureState) requestAfterToolCallStartsFromSummary() error {
	if len(s.provider.streamSeen) < 2 {
		return fmt.Errorf("the loop made %d model call(s); expected one after the tool", len(s.provider.streamSeen))
	}
	req := s.provider.streamSeen[len(s.provider.streamSeen)-1]
	for _, m := range req {
		if m.Role == llm.RoleSystem {
			continue
		}
		if !m.CompactionSummary || !strings.Contains(m.Content, "CANNED-SUMMARY") {
			return fmt.Errorf("first history message after the tool call is not the summary: %+v", m)
		}
		return nil
	}
	return fmt.Errorf("request had no history messages")
}

// toolResultsAnswerCallsInTheRequest reads the same request as a provider
// does: a tool result whose call is not in the request is refused.
func (s *compactionFeatureState) toolResultsAnswerCallsInTheRequest() error {
	if len(s.provider.streamSeen) < 2 {
		return fmt.Errorf("the loop made %d model call(s); expected one after the tool", len(s.provider.streamSeen))
	}
	req := s.provider.streamSeen[len(s.provider.streamSeen)-1]
	called := map[string]bool{}
	results := 0
	for _, m := range req {
		for _, tc := range m.ToolCalls {
			called[tc.ID] = true
		}
		if m.Role == llm.RoleTool {
			results++
			if !called[m.ToolCallID] {
				return fmt.Errorf("tool result %q answers no call in the request: %s", m.ToolCallID, transcriptText(req))
			}
		}
	}
	if results == 0 {
		return fmt.Errorf("the request carries no tool result: %s", transcriptText(req))
	}
	return nil
}

// secondModelConfigured adds a model the session does not run on, so a summary
// it wrote can only come from a compaction that asked for it by name.
func (s *compactionFeatureState) secondModelConfigured(model string) error {
	if s.ag == nil {
		return fmt.Errorf("no session prepared")
	}
	s.ag.cfg.Models = append(s.ag.cfg.Models, config.ModelEntry{Model: model, MaxTokens: 100, MaxContextTokens: 128000})
	return nil
}

func (s *compactionFeatureState) summaryWrittenBy(model string) error {
	for _, m := range s.st.GetMessages() {
		if m.CompactionSummary {
			if m.Model != model {
				return fmt.Errorf("summary written by %q, want %q", m.Model, model)
			}
			return nil
		}
	}
	return fmt.Errorf("no compaction summary row in transcript")
}

func (s *compactionFeatureState) toolResultNamesSummarizer(model string) error {
	for _, m := range s.st.GetMessages() {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "Summarizer: "+model+".") {
			return nil
		}
	}
	return fmt.Errorf("no tool result naming %q: %s", model, transcriptText(s.st.GetMessages()))
}

func initializeCompactionScenario(sc *godog.ScenarioContext) {
	s := &compactionFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a session with (\d+) completed exchanges$`, s.sessionWithExchanges)
	sc.Step(`^the session is compacted keeping the last 2 user turns$`, s.compactSession)
	sc.Step(`^the compaction summary is inserted into the transcript$`, s.summaryInsertedIntoTranscript)
	sc.Step(`^the summary row begins with the prompt being answered$`, s.summaryRowBeginsWithThePrompt)
	sc.Step(`^the transcript still contains all (\d+) original exchanges$`, func(int) error { return s.transcriptContainsAllExchanges() })
	sc.Step(`^the next LLM request starts from the summary$`, s.nextRequestStartsFromSummary)
	sc.Step(`^the next LLM request contains the last (\d+) exchanges verbatim$`, s.nextRequestContainsLastExchanges)
	sc.Step(`^the next LLM request does not contain the older exchanges$`, s.nextRequestOmitsOlderExchanges)
	sc.Step(`^the user sends a new prompt$`, s.userSendsNewPrompt)
	sc.Step(`^the model's context window is (\d+) tokens$`, s.contextWindowIs)
	sc.Step(`^the provider refuses every request of the turn: "([^"]+)"$`, s.providerRefusesEveryTurnRequest)
	sc.Step(`^the user sends a new prompt and the turn fails$`, s.userSendsPromptAndTheTurnFails)
	sc.Step(`^the turn is refused with an explanation that the context window was exceeded$`, s.turnWasRefusedWithAnExplanation)
	sc.Step(`^the error says "([^"]+)"$`, s.errorSays)
	sc.Step(`^the turn's request was sent twice, before and after the compaction$`, s.turnRequestSentTwice)
	sc.Step(`^the summarizer refuses requests larger than its window$`, s.summarizerRefusesLargeRequests)
	sc.Step(`^the summarizer refused a request as too large$`, s.summarizerRefusedARequestAsTooLarge)
	sc.Step(`^the agent replies successfully$`, s.agentRepliesSuccessfully)
	sc.Step(`^the LLM request for that reply starts from the summary$`, s.nextRequestStartsFromSummary)
	sc.Step(`^the ACP client has observed the context usage before compaction$`, s.clientObservedContextUsageBeforeCompaction)
	sc.Step(`^the ACP client receives a smaller context usage update$`, s.clientReceivesSmallerContextUsage)
	sc.Step(`^the reported ACP usage matches the compacted LLM context$`, s.reportedACPUsageMatchesContext)
	sc.Step(`^the summarizer model has room for only part of the history per request$`, s.summarizerFitsPartOfTheHistory)
	sc.Step(`^the summarizer was called more than once$`, s.summarizerCalledMoreThanOnce)
	sc.Step(`^every summarization request after the first carries the summary so far$`, s.laterRequestsCarryTheSummarySoFar)
	sc.Step(`^the client was told the compaction was running$`, s.clientToldCompactionWasRunning)
	sc.Step(`^the model calls the compact_context tool$`, s.modelCallsCompactContext)
	sc.Step(`^the tool result says what was compacted$`, s.toolResultSaysWhatWasCompacted)
	sc.Step(`^the model calls the compact_context tool with the model "([^"]+)"$`, s.modelCallsCompactContextWithModel)
	sc.Step(`^a second model "([^"]+)" is configured$`, s.secondModelConfigured)
	sc.Step(`^the summary was written by "([^"]+)"$`, s.summaryWrittenBy)
	sc.Step(`^the tool result names the summarizer "([^"]+)"$`, s.toolResultNamesSummarizer)
	sc.Step(`^every tool result in that request answers a call the request carries$`, s.toolResultsAnswerCallsInTheRequest)
	sc.Step(`^the LLM request after the tool call starts from the summary$`, s.requestAfterToolCallStartsFromSummary)
}

func TestContextCompactionFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "context-compaction",
		ScenarioInitializer: initializeCompactionScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/context_compaction.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("context compaction feature suite failed")
	}
}

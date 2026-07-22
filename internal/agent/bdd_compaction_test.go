package agent

// Godog harness for features/context_compaction.feature: drives the real
// Agent (CompactSession + Run) with a fake LLM provider, asserting what the
// next LLM request contains after the session history was compacted.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// bddCompactionProvider returns a canned summary for Complete (the compaction
// call) and a canned answer for Stream (agent turns), recording every request.
type bddCompactionProvider struct {
	completeSeen [][]llm.Message
	streamSeen   [][]llm.Message
}

func (p *bddCompactionProvider) Complete(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	p.completeSeen = append(p.completeSeen, append([]llm.Message(nil), messages...))
	return &llm.Response{Content: "CANNED-SUMMARY of the earlier exchanges", StopReason: "end_turn"}, nil
}

func (p *bddCompactionProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.streamSeen = append(p.streamSeen, append([]llm.Message(nil), messages...))
	onChunk(llm.StreamChunk{TextDelta: "post-compaction answer"})
	return &llm.Response{Content: "post-compaction answer", StopReason: "end_turn"}, nil
}

type compactionFeatureState struct {
	tmpDirs  []string
	st       *session.State
	ag       *Agent
	provider *bddCompactionProvider
	exchanges int
}

func (s *compactionFeatureState) reset() error {
	s.close()
	s.provider = &bddCompactionProvider{}
	s.exchanges = 0
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
		s.st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)})
	}
	s.exchanges = n

	keep := 2
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
		Compaction: config.Compaction{
			KeepRecentTurns: &keep,
		},
	}
	s.ag = NewAgent(cfg, s.st, resumePermissionSender{}, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return s.provider, nil
	}
	return nil
}

func (s *compactionFeatureState) compactSession() error {
	if s.ag == nil {
		return fmt.Errorf("no session prepared")
	}
	res, err := s.ag.CompactSession(context.Background(), "", false)
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
	sc.Step(`^the transcript still contains all (\d+) original exchanges$`, func(int) error { return s.transcriptContainsAllExchanges() })
	sc.Step(`^the next LLM request starts from the summary$`, s.nextRequestStartsFromSummary)
	sc.Step(`^the next LLM request contains the last (\d+) exchanges verbatim$`, s.nextRequestContainsLastExchanges)
	sc.Step(`^the next LLM request does not contain the older exchanges$`, s.nextRequestOmitsOlderExchanges)
	sc.Step(`^the user sends a new prompt$`, s.userSendsNewPrompt)
	sc.Step(`^the agent replies successfully$`, s.agentRepliesSuccessfully)
	sc.Step(`^the LLM request for that reply starts from the summary$`, s.nextRequestStartsFromSummary)
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

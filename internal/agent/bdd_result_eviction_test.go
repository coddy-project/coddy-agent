package agent

// Godog harness for features/context_result_eviction.feature: drives the real
// Agent through a scripted provider that issues read/grep/keep_result tool calls
// over a real temp workspace, then asserts what the final LLM request contains
// after read/grep result eviction, and that the persisted transcript stays whole.

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

// evStep is one scripted assistant turn: either tool calls to execute, or a final
// text answer when calls is empty.
type evStep struct {
	calls []llm.ToolCall
	text  string
}

type evScriptProvider struct {
	steps      []evStep
	i          int
	streamSeen [][]llm.Message
}

func (p *evScriptProvider) Complete(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: "summary", StopReason: "end_turn"}, nil
}

func (p *evScriptProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.streamSeen = append(p.streamSeen, append([]llm.Message(nil), messages...))
	var step evStep
	if p.i < len(p.steps) {
		step = p.steps[p.i]
	} else {
		step = evStep{text: "done"}
	}
	p.i++
	if len(step.calls) > 0 {
		return &llm.Response{ToolCalls: step.calls, StopReason: "tool_use"}, nil
	}
	if step.text == "" {
		step.text = "done"
	}
	onChunk(llm.StreamChunk{TextDelta: step.text})
	return &llm.Response{Content: step.text, StopReason: "end_turn"}, nil
}

func tcRead(id, path string, offset, limit int) llm.ToolCall {
	b, _ := json.Marshal(map[string]interface{}{"path": path, "offset": offset, "limit": limit})
	return llm.ToolCall{ID: id, Name: "read", InputJSON: string(b)}
}

func tcGrep(id, pattern string) llm.ToolCall {
	b, _ := json.Marshal(map[string]interface{}{"pattern": pattern})
	return llm.ToolCall{ID: id, Name: "grep", InputJSON: string(b)}
}

func tcKeepRead(id, path string, offset, limit int) llm.ToolCall {
	b, _ := json.Marshal(map[string]interface{}{"path": path, "offset": offset, "limit": limit})
	return llm.ToolCall{ID: id, Name: "keep_result", InputJSON: string(b)}
}

func tcKeepGrep(id, pattern string) llm.ToolCall {
	b, _ := json.Marshal(map[string]interface{}{"pattern": pattern})
	return llm.ToolCall{ID: id, Name: "keep_result", InputJSON: string(b)}
}

type evFeatureState struct {
	tmpDirs      []string
	cwd          string
	sessionDir   string
	outputLimits config.ToolOutputLimits
	st           *session.State
	ag           *Agent
	provider     *evScriptProvider
}

func (s *evFeatureState) reset() error {
	s.close()
	s.provider = &evScriptProvider{}
	s.outputLimits = config.ToolOutputLimits{}
	var err error
	if s.cwd, err = s.tempDir(); err != nil {
		return err
	}
	// The session bundle must live under its own store root, not a sibling of the
	// workspace: grep hides the session store root (parent of the bundle), and if
	// that root also contained the workspace, every match would be filtered out.
	store, err := s.tempDir()
	if err != nil {
		return err
	}
	s.sessionDir = filepath.Join(store, "bundle")
	return os.MkdirAll(s.sessionDir, 0o755)
}

func (s *evFeatureState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.st = nil
	s.ag = nil
}

func (s *evFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-evict-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

// buildAgent wires a real Agent with eviction enabled (keep_recent 0 so only
// explicitly marked results survive) and the scripted provider.
func (s *evFeatureState) buildAgent() {
	keepRecent := 0
	minBytes := 20
	enabled := true
	cfg := &config.Config{
		Providers:  []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:     []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:      config.Agent{Model: "fake/model"},
		Compaction: config.Compaction{ResultEviction: config.ResultEviction{Enabled: &enabled, KeepRecent: &keepRecent, MinResultBytes: &minBytes}},
		Tools:      config.Tools{PermissionMode: config.PermModeBypass, OutputLimits: s.outputLimits},
	}
	s.st = &session.State{ID: "sess_bdd_evict", CWD: s.cwd, Mode: session.ModeAgent, SessionDir: s.sessionDir}
	s.ag = NewAgent(cfg, s.st, resumePermissionSender{}, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
}

func (s *evFeatureState) run() error {
	_, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "go"}})
	return err
}

func (s *evFeatureState) lastRequest() []llm.Message {
	if len(s.provider.streamSeen) == 0 {
		return nil
	}
	return s.provider.streamSeen[len(s.provider.streamSeen)-1]
}

func requestToolContent(req []llm.Message, id string) string {
	for _, m := range req {
		if m.Role == llm.RoleTool && m.ToolCallID == id {
			return m.Content
		}
	}
	return ""
}

func joinMessages(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// --- Scenario 1: paged read -------------------------------------------------

func (s *evFeatureState) fileWithNumberedLines(name string, n int) error {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "LINE-%04d the quick brown fox jumps over the lazy dog %d\n", i, i)
	}
	return os.WriteFile(filepath.Join(s.cwd, name), []byte(b.String()), 0o644)
}

func (s *evFeatureState) pageThroughMarkingPage2() error {
	s.buildAgent()
	s.provider.steps = []evStep{
		{calls: []llm.ToolCall{tcRead("r1", "big.go", 1, 10)}},
		{calls: []llm.ToolCall{tcRead("r2", "big.go", 11, 10)}},
		{calls: []llm.ToolCall{tcKeepRead("k1", "big.go", 11, 10)}},
		{calls: []llm.ToolCall{tcRead("r3", "big.go", 21, 10)}},
		{text: "answer"},
	}
	return s.run()
}

func (s *evFeatureState) requestKeepsPage2() error {
	if c := requestToolContent(s.lastRequest(), "r2"); !strings.Contains(c, "LINE-0011") {
		return fmt.Errorf("marked page 2 not kept verbatim: %q", c)
	}
	return nil
}

func (s *evFeatureState) requestEvictsPage1And3() error {
	req := s.lastRequest()
	for _, id := range []string{"r1", "r3"} {
		c := requestToolContent(req, id)
		if !strings.HasPrefix(c, "[evicted:") {
			return fmt.Errorf("page %s not replaced by a placeholder: %q", id, c)
		}
	}
	joined := joinMessages(req)
	if strings.Contains(joined, "LINE-0001") || strings.Contains(joined, "LINE-0021") {
		return fmt.Errorf("evicted page content leaked into the request")
	}
	return nil
}

func (s *evFeatureState) requestHasOneResultPerCall() error {
	req := s.lastRequest()
	seen := map[string]int{}
	for _, m := range req {
		if m.Role == llm.RoleTool {
			seen[m.ToolCallID]++
			if strings.TrimSpace(m.Content) == "" {
				return fmt.Errorf("tool result for %s is empty", m.ToolCallID)
			}
		}
	}
	for _, id := range []string{"r1", "r2", "k1", "r3"} {
		if seen[id] != 1 {
			return fmt.Errorf("tool call %s has %d results, want 1", id, seen[id])
		}
	}
	return nil
}

func (s *evFeatureState) transcriptHasAllThreePages() error {
	joined := joinMessages(s.st.GetMessages())
	for _, marker := range []string{"LINE-0001", "LINE-0011", "LINE-0021"} {
		if !strings.Contains(joined, marker) {
			return fmt.Errorf("persisted transcript lost %s", marker)
		}
	}
	return nil
}

// --- Scenario 2: grep -------------------------------------------------------

func (s *evFeatureState) grepWorkspace() error {
	alpha := "func handlerA() { alphaMATCH ALPHA_PAYLOAD_42 }\nfunc a2() { alphaMATCH ALPHA_PAYLOAD_42 more }\n"
	beta := "func handlerB() { betaMATCH BETA_PAYLOAD_99 }\nfunc b2() { betaMATCH BETA_PAYLOAD_99 more }\n"
	if err := os.WriteFile(filepath.Join(s.cwd, "alpha.go"), []byte(alpha), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.cwd, "beta.go"), []byte(beta), 0o644)
}

func (s *evFeatureState) grepMarkingAlpha() error {
	s.buildAgent()
	s.provider.steps = []evStep{
		{calls: []llm.ToolCall{tcGrep("g1", "alphaMATCH")}},
		{calls: []llm.ToolCall{tcGrep("g2", "betaMATCH")}},
		{calls: []llm.ToolCall{tcKeepGrep("k1", "alphaMATCH")}},
		{text: "answer"},
	}
	return s.run()
}

func (s *evFeatureState) requestKeepsAlphaGrep() error {
	if c := requestToolContent(s.lastRequest(), "g1"); !strings.Contains(c, "ALPHA_PAYLOAD_42") {
		return fmt.Errorf("marked grep not kept verbatim: %q", c)
	}
	return nil
}

func (s *evFeatureState) requestEvictsBetaGrep() error {
	req := s.lastRequest()
	c := requestToolContent(req, "g2")
	if !strings.HasPrefix(c, "[evicted:") {
		return fmt.Errorf("unmarked grep not replaced by a placeholder: %q", c)
	}
	if strings.Contains(joinMessages(req), "BETA_PAYLOAD_99") {
		return fmt.Errorf("evicted grep content leaked into the request")
	}
	return nil
}

func (s *evFeatureState) transcriptHasBothGreps() error {
	joined := joinMessages(s.st.GetMessages())
	if !strings.Contains(joined, "ALPHA_PAYLOAD_42") || !strings.Contains(joined, "BETA_PAYLOAD_99") {
		return fmt.Errorf("persisted transcript lost a grep result")
	}
	return nil
}

// --- Scenario 3: output limit ----------------------------------------------

func (s *evFeatureState) fileWithNMatchingLines(name string, n int) error {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "row %d contains dup here\n", i)
	}
	return os.WriteFile(filepath.Join(s.cwd, name), []byte(b.String()), 0o644)
}

func (s *evFeatureState) grepOutputLimit(n int) error {
	limit := n
	s.outputLimits = config.ToolOutputLimits{Grep: &limit}
	return nil
}

func (s *evFeatureState) grepFor(pattern string) error {
	s.buildAgent()
	s.provider.steps = []evStep{
		{calls: []llm.ToolCall{tcGrep("g1", pattern)}},
		{text: "answer"},
	}
	return s.run()
}

func (s *evFeatureState) grepResultCapped(maxLines int) error {
	body := requestGrepPersisted(s.st.GetMessages(), "g1")
	if body == "" {
		return fmt.Errorf("grep result not found in transcript")
	}
	dupLines := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "dup") {
			dupLines++
		}
	}
	if dupLines > maxLines {
		return fmt.Errorf("grep returned %d matching lines, want at most %d", dupLines, maxLines)
	}
	return nil
}

func (s *evFeatureState) grepResultHasTruncationMarker() error {
	body := requestGrepPersisted(s.st.GetMessages(), "g1")
	if !strings.Contains(body, "[output truncated:") {
		return fmt.Errorf("grep result missing truncation marker: %q", body)
	}
	return nil
}

func requestGrepPersisted(msgs []llm.Message, id string) string {
	for _, m := range msgs {
		if m.Role == llm.RoleTool && m.ToolCallID == id {
			return m.Content
		}
	}
	return ""
}

func initializeResultEvictionScenario(sc *godog.ScenarioContext) {
	s := &evFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a workspace file "([^"]*)" with (\d+) numbered lines$`, func(name string, n int) error {
		return s.fileWithNumberedLines(name, n)
	})
	sc.Step(`^the model reads page 1, reads page 2, marks page 2 as useful, reads page 3, then answers$`, s.pageThroughMarkingPage2)
	sc.Step(`^the next LLM request keeps page 2 verbatim$`, s.requestKeepsPage2)
	sc.Step(`^the next LLM request replaces page 1 and page 3 with placeholders$`, s.requestEvictsPage1And3)
	sc.Step(`^the next LLM request has one tool result per tool call$`, s.requestHasOneResultPerCall)
	sc.Step(`^the persisted transcript still contains all three pages in full$`, s.transcriptHasAllThreePages)

	sc.Step(`^a workspace with files matching "alphaMATCH" and "betaMATCH"$`, s.grepWorkspace)
	sc.Step(`^the model greps for "alphaMATCH", greps for "betaMATCH", marks the "alphaMATCH" search as useful, then answers$`, s.grepMarkingAlpha)
	sc.Step(`^the next LLM request keeps the "alphaMATCH" results verbatim$`, s.requestKeepsAlphaGrep)
	sc.Step(`^the next LLM request replaces the "betaMATCH" results with a placeholder$`, s.requestEvictsBetaGrep)
	sc.Step(`^the persisted transcript still contains both grep results in full$`, s.transcriptHasBothGreps)

	sc.Step(`^a workspace file "([^"]*)" with (\d+) lines matching "dup"$`, func(name string, n int) error {
		return s.fileWithNMatchingLines(name, n)
	})
	sc.Step(`^the grep output limit is (\d+) lines$`, s.grepOutputLimit)
	sc.Step(`^the model greps for "([^"]*)"$`, s.grepFor)
	sc.Step(`^the grep result shows at most (\d+) matching lines$`, s.grepResultCapped)
	sc.Step(`^the grep result ends with a truncation marker$`, s.grepResultHasTruncationMarker)
}

func TestContextResultEvictionFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "context-result-eviction",
		ScenarioInitializer: initializeResultEvictionScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/context_result_eviction.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("context result eviction feature suite failed")
	}
}

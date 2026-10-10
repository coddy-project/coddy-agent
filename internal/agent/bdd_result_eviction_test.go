package agent

// Godog harness for features/context_result_eviction.feature: drives the real
// Agent through a scripted provider that issues read, grep, keep_result, glob and
// print_tree tool calls over a real temp workspace, then asserts what the final
// LLM request contains after result eviction, and that the persisted transcript
// stays whole.

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
	startPercent int
	// keepSteps is compaction.result_eviction.keep_recent_steps; nil leaves the
	// default.
	keepSteps *int
	// folders are the workspace folders of the listing scenario, in the order
	// the model lists them, and filesPerFolder the files each holds.
	folders        []string
	filesPerFolder int
	st             *session.State
	ag             *Agent
	provider       *evScriptProvider
}

func (s *evFeatureState) reset() error {
	s.close()
	s.provider = &evScriptProvider{}
	s.outputLimits = config.ToolOutputLimits{}
	s.startPercent = 0
	s.keepSteps = nil
	s.folders = nil
	s.filesPerFolder = 0
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
	// start_percent 0: these scenarios are about what eviction collapses, not
	// about when it starts. The threshold that holds it off on a short
	// conversation has a scenario of its own below.
	startPercent := s.startPercent
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
		Compaction: config.Compaction{ResultEviction: config.ResultEviction{
			Enabled: &enabled, KeepRecent: &keepRecent, MinResultBytes: &minBytes, StartPercent: &startPercent,
			KeepRecentSteps: s.keepSteps,
		}},
		Tools: config.Tools{PermissionMode: config.PermModeBypass, OutputLimits: s.outputLimits},
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

// Below the start threshold nothing is collapsed: the replayed history has to
// stay byte for byte what the provider already cached.
func (s *evFeatureState) pageThroughWithoutMarking() error {
	s.buildAgent()
	s.provider.steps = []evStep{
		{calls: []llm.ToolCall{tcRead("r1", "big.go", 1, 10)}},
		{calls: []llm.ToolCall{tcRead("r2", "big.go", 11, 10)}},
		{calls: []llm.ToolCall{tcRead("r3", "big.go", 21, 10)}},
		{text: "answer"},
	}
	return s.run()
}

func (s *evFeatureState) requestKeepsAllThreePages() error {
	req := s.lastRequest()
	for id, line := range map[string]string{"r1": "LINE-0001", "r2": "LINE-0011", "r3": "LINE-0021"} {
		if c := requestToolContent(req, id); !strings.Contains(c, line) {
			return fmt.Errorf("page %s was collapsed below the eviction threshold: %q", id, c)
		}
	}
	return nil
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

// requestHasOneResultPerCall checks the pairing the provider enforces on the last
// request: every tool call the request carries has exactly one non-empty result,
// and no result stands without its call.
func (s *evFeatureState) requestHasOneResultPerCall() error {
	req := s.lastRequest()
	results := map[string]int{}
	for _, m := range req {
		if m.Role == llm.RoleTool {
			results[m.ToolCallID]++
			if strings.TrimSpace(m.Content) == "" {
				return fmt.Errorf("tool result for %s is empty", m.ToolCallID)
			}
		}
	}
	calls := map[string]bool{}
	for _, m := range req {
		for _, tc := range m.ToolCalls {
			calls[tc.ID] = true
			if results[tc.ID] != 1 {
				return fmt.Errorf("tool call %s has %d results, want 1", tc.ID, results[tc.ID])
			}
		}
	}
	if len(calls) == 0 {
		return fmt.Errorf("the request carries no tool calls")
	}
	for id := range results {
		if !calls[id] {
			return fmt.Errorf("tool result %s answers no call of the request", id)
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

// --- Scenario 3: directory listings -----------------------------------------

// listingFolderNames are the folders a listing scenario can create, in the order
// the model lists them.
var listingFolderNames = []string{"alpha", "beta", "gamma", "delta"}

// listingFileName is the name of the i-th file (1-based) of a folder: it carries
// the folder, so a listing that leaked into the request says whose it was.
func listingFileName(folder string, i int) string {
	return fmt.Sprintf("%s_file_%02d.go", folder, i)
}

func (s *evFeatureState) foldersWithFiles(folders, files int) error {
	if folders < 1 || folders > len(listingFolderNames) {
		return fmt.Errorf("the harness knows %d folder names, not %d", len(listingFolderNames), folders)
	}
	s.folders = listingFolderNames[:folders]
	s.filesPerFolder = files
	for _, folder := range s.folders {
		dir := filepath.Join(s.cwd, folder)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for i := 1; i <= files; i++ {
			if err := os.WriteFile(filepath.Join(dir, listingFileName(folder, i)), []byte("package x\n"), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *evFeatureState) listingStepsKept(n int) error {
	s.keepSteps = &n
	return nil
}

func tcGlobIn(id, pattern, path string) llm.ToolCall {
	b, _ := json.Marshal(map[string]interface{}{"pattern": pattern, "path": path})
	return llm.ToolCall{ID: id, Name: "glob", InputJSON: string(b)}
}

func tcTreeOf(id, path string) llm.ToolCall {
	b, _ := json.Marshal(map[string]interface{}{"path": path, "depth": 2})
	return llm.ToolCall{ID: id, Name: "print_tree", InputJSON: string(b)}
}

// listingIDs are the two call ids of the i-th folder's step (0-based).
func listingIDs(i int) (glob, tree string) {
	return fmt.Sprintf("g%d", i+1), fmt.Sprintf("t%d", i+1)
}

func (s *evFeatureState) modelListsEachFolder() error {
	s.buildAgent()
	for i, folder := range s.folders {
		g, tr := listingIDs(i)
		s.provider.steps = append(s.provider.steps, evStep{calls: []llm.ToolCall{
			tcGlobIn(g, "*.go", folder), tcTreeOf(tr, folder),
		}})
	}
	s.provider.steps = append(s.provider.steps, evStep{text: "answer"})
	return s.run()
}

func (s *evFeatureState) requestEvictsFirstStepListings() error {
	req := s.lastRequest()
	g, tr := listingIDs(0)
	for _, id := range []string{g, tr} {
		c := requestToolContent(req, id)
		if !strings.HasPrefix(c, "[evicted:") {
			return fmt.Errorf("listing %s of the first step not replaced by a placeholder: %q", id, truncateForError(c))
		}
	}
	if strings.Contains(joinMessages(req), s.folders[0]+"_file_") {
		return fmt.Errorf("an evicted listing leaked into the request")
	}
	return nil
}

func (s *evFeatureState) requestKeepsLastStepListings(n int) error {
	req := s.lastRequest()
	if n > len(s.folders) {
		return fmt.Errorf("the scenario lists %d folders, not %d", len(s.folders), n)
	}
	for i := len(s.folders) - n; i < len(s.folders); i++ {
		folder := s.folders[i]
		first, last := listingFileName(folder, 1), listingFileName(folder, s.filesPerFolder)
		g, tr := listingIDs(i)
		for _, id := range []string{g, tr} {
			c := requestToolContent(req, id)
			if strings.HasPrefix(c, "[evicted:") {
				return fmt.Errorf("listing %s of %s was collapsed although its step is among the last %d", id, folder, n)
			}
			if !strings.Contains(c, first) || !strings.Contains(c, last) {
				return fmt.Errorf("listing %s of %s is not verbatim (want %s .. %s): %q", id, folder, first, last, truncateForError(c))
			}
		}
	}
	return nil
}

func (s *evFeatureState) transcriptHasEveryListing() error {
	persisted := s.st.GetMessages()
	for i, folder := range s.folders {
		first, last := listingFileName(folder, 1), listingFileName(folder, s.filesPerFolder)
		g, tr := listingIDs(i)
		for _, id := range []string{g, tr} {
			c := requestGrepPersisted(persisted, id)
			if !strings.Contains(c, first) || !strings.Contains(c, last) {
				return fmt.Errorf("persisted listing %s of %s lost files: %q", id, folder, truncateForError(c))
			}
		}
	}
	return nil
}

// --- Scenario 4: output limit ----------------------------------------------

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
	sc.Step(`^result eviction starts at (\d+) percent of the context window$`, func(p int) error {
		s.startPercent = p
		return nil
	})
	sc.Step(`^the model reads page 1, reads page 2, reads page 3, then answers$`, s.pageThroughWithoutMarking)
	sc.Step(`^the next LLM request keeps all three pages verbatim$`, s.requestKeepsAllThreePages)
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

	sc.Step(`^a workspace with (\d+) folders of (\d+) files each$`, s.foldersWithFiles)
	sc.Step(`^listing results are evicted outside the last (\d+) steps$`, s.listingStepsKept)
	sc.Step(`^the model lists each folder in its own step, with a glob and a tree each, then answers$`, s.modelListsEachFolder)
	sc.Step(`^the next LLM request replaces the listings of the first step with placeholders$`, s.requestEvictsFirstStepListings)
	sc.Step(`^the next LLM request keeps the listings of the last (\d+) steps verbatim$`, s.requestKeepsLastStepListings)
	sc.Step(`^the persisted transcript still contains every listing in full$`, s.transcriptHasEveryListing)

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

package agent

// Harness for features/prompt_cache_compaction.feature: what a fold asks the
// summarizer, and what a batch of collapsed results does to the history the
// provider has already cached. The suite is named TestPromptCache*, so
// `make test-cache` runs it.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// pccProvider records both halves of what a session sends: the turn's streamed
// requests and the summarizer's blocking ones, each with the tool definitions it
// was given.
type pccProvider struct {
	steps     []pcStep
	i         int
	turns     [][]llm.Message
	turnTools [][]llm.ToolDefinition
	folds     [][]llm.Message
	foldTools [][]llm.ToolDefinition
}

func (p *pccProvider) Complete(_ context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	p.folds = append(p.folds, append([]llm.Message(nil), messages...))
	p.foldTools = append(p.foldTools, append([]llm.ToolDefinition(nil), tools...))
	return &llm.Response{Content: fmt.Sprintf("summary %d", len(p.folds)), StopReason: "end_turn"}, nil
}

func (p *pccProvider) Stream(_ context.Context, messages []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.turns = append(p.turns, append([]llm.Message(nil), messages...))
	p.turnTools = append(p.turnTools, append([]llm.ToolDefinition(nil), tools...))
	var step pcStep
	if p.i < len(p.steps) {
		step = p.steps[p.i]
	} else {
		step = pcStep{text: "done"}
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

type pccState struct {
	tmpDirs  []string
	cwd      string
	st       *session.State
	ag       *Agent
	provider *pccProvider
	// foldsBefore is how many summarization calls had been made before the
	// latest `/compact`, so a scenario can name the first and the second.
	foldsBefore []int
	// turnsBefore is how many requests the turns had streamed before each of
	// those compactions: the last of them is the one the fold repeats.
	turnsBefore []int
}

func (s *pccState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.st = nil
	s.ag = nil
}

func (s *pccState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-pccache-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

// build opens a session whose model is fake/model. summarizer, when set, is the
// compaction.model of that session.
func (s *pccState) build(summarizer string, tune func(*config.Config)) error {
	s.close()
	s.provider = &pccProvider{}
	s.foldsBefore, s.turnsBefore = nil, nil
	var err error
	if s.cwd, err = s.tempDir(); err != nil {
		return err
	}
	store, err := s.tempDir()
	if err != nil {
		return err
	}
	sessionDir := filepath.Join(store, "bundle")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.cwd, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		return err
	}
	off := false
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models: []config.ModelEntry{
			{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000},
			{Model: "fake/other", MaxTokens: 100, MaxContextTokens: 128000},
		},
		Agent:      config.Agent{Model: "fake/model"},
		Tools:      config.Tools{PermissionMode: config.PermModeBypass},
		Compaction: config.Compaction{AutoEnabled: &off, Model: summarizer},
	}
	if tune != nil {
		tune(cfg)
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	s.st = &session.State{ID: "sess_bdd_pccache", CWD: s.cwd, Mode: session.ModeAgent, SessionDir: sessionDir}
	s.st.ReplaceRulesCatalog(session.DiscoverRules(cfg, s.cwd))
	s.ag = NewAgent(cfg, s.st, resumePermissionSender{}, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
	return nil
}

func (s *pccState) session() error                { return s.build("", nil) }
func (s *pccState) sessionOtherSummarizer() error { return s.build("fake/other", nil) }

// pccFirstPromptContext is what the FirstPrompt hook hands over: a one-time
// enrichment that rides the turn-scoped block of the first turn's requests and
// is gone from the next turn's.
const pccFirstPromptContext = "the working tree has unsaved buffers"

// pccTurnContext is what the UserPromptSubmit hook hands over on every turn,
// which the system prompt carries.
const pccTurnContext = "the operator is reviewing a pull request"

// sessionWithHooks opens a session whose operator runs lifecycle hooks: the two
// that hand context to the model, and the observational ones around a tool call
// and the end of a turn. None of them may move what a fold reads out of the
// cache, so this is the scenario that holds the two features to each other.
func (s *pccState) sessionWithHooks() error {
	if err := s.build("", nil); err != nil {
		return err
	}
	home, err := s.tempDir()
	if err != nil {
		return err
	}
	if err := hooktest.Write(filepath.Join(home, "hooks.json"),
		hooktest.Entry{Event: hooks.EventFirstPrompt, Handlers: []hooks.Handler{hooktest.Handler("context", pccFirstPromptContext)}},
		hooktest.Entry{Event: hooks.EventUserPromptSubmit, Handlers: []hooks.Handler{hooktest.Handler("context", pccTurnContext)}},
		hooktest.Entry{Event: hooks.EventPreToolUse, Matcher: "*", Handlers: []hooks.Handler{hooktest.Handler("silent")}},
		hooktest.Entry{Event: hooks.EventTurnEnd, Handlers: []hooks.Handler{hooktest.Handler("silent")}},
	); err != nil {
		return err
	}
	s.ag.cfg.Paths.Home = home
	s.ag.cfg.Paths.CWD = s.cwd
	s.ag.cfg.Hooks.ApplyDefaults(s.ag.cfg.Paths)
	return nil
}

// turnsCarriedTheHookContext keeps the scenario honest: unless the hooks did
// reach the requests, the assertions after it hold nothing. The one-time
// enrichment travels in the trailing turn-context message and the per-turn one
// in the system message, which is where each is supposed to be.
func (s *pccState) turnsCarriedTheHookContext() error {
	var first, every bool
	for _, req := range s.provider.turns {
		if len(req) == 0 {
			continue
		}
		if strings.Contains(req[0].Content, pccTurnContext) {
			every = true
		}
		last := req[len(req)-1]
		if last.Ephemeral && strings.Contains(last.Content, pccFirstPromptContext) {
			first = true
		}
	}
	switch {
	case !first:
		return fmt.Errorf("no request carried the FirstPrompt enrichment %q: the hook never ran", pccFirstPromptContext)
	case !every:
		return fmt.Errorf("no system message carried the UserPromptSubmit context %q: the hook never ran", pccTurnContext)
	}
	return nil
}

// noFoldCarriesTheFirstPromptContext holds the fold to the conversation itself.
// The one-time enrichment is turn-scoped and trails the request the turn sends,
// so it falls past the compaction boundary; a fold that carried it would be
// asking with something no cached prefix holds.
func (s *pccState) noFoldCarriesTheFirstPromptContext() error {
	for n := 1; n <= len(s.provider.folds); n++ {
		req, _, err := s.fold(n)
		if err != nil {
			return err
		}
		for i, m := range req {
			if strings.Contains(m.Content, pccFirstPromptContext) {
				return fmt.Errorf("message %d of summarization request %d carries the one-time enrichment: %s %q",
					i, n, m.Role, truncateForError(m.Content))
			}
		}
	}
	return nil
}

// manyFiles opens a session whose workspace holds files large enough to be
// eviction candidates, with the eviction gate open from the first result.
func (s *pccState) manyFiles() error {
	if err := s.build("", func(cfg *config.Config) {
		on := true
		keep := 2
		minBytes := 200
		start := 0
		cfg.Compaction.ResultEviction = config.ResultEviction{
			Enabled: &on, KeepRecent: &keep, MinResultBytes: &minBytes, StartPercent: &start,
		}
	}); err != nil {
		return err
	}
	body := strings.Repeat("// a line of a file the model reads and moves past\n", 60)
	for i := 0; i < pccReadSteps; i++ {
		name := filepath.Join(s.cwd, fmt.Sprintf("file_%02d.go", i))
		if err := os.WriteFile(name, []byte("package x\n"+body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// worksThroughAFewTurns runs three turns, each a read and an answer, so a forced
// compaction has several messages to fold.
func (s *pccState) worksThroughAFewTurns() error {
	for t := 0; t < 3; t++ {
		s.provider.i = 0
		s.provider.steps = []pcStep{
			{calls: []llm.ToolCall{pcReadCall(fmt.Sprintf("r%d", t), "main.go")}},
			{text: fmt.Sprintf("answer %d", t)},
		}
		prompt := fmt.Sprintf("look at main.go, round %d", t)
		if _, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: prompt}}); err != nil {
			return err
		}
	}
	return nil
}

func (s *pccState) operatorCompacts() error {
	s.foldsBefore = append(s.foldsBefore, len(s.provider.folds))
	s.turnsBefore = append(s.turnsBefore, len(s.provider.turns))
	if _, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "/compact"}}); err != nil {
		return err
	}
	if len(s.provider.folds) == s.foldsBefore[len(s.foldsBefore)-1] {
		return fmt.Errorf("the compaction asked the summarizer nothing")
	}
	for _, m := range s.st.GetMessages() {
		if m.CompactionSummary {
			return nil
		}
	}
	return fmt.Errorf("the compaction left no summary row in the transcript")
}

// pccReadSteps is how many single-read steps the eviction scenario runs: enough
// for the batch to fill twice over the working window of two.
const pccReadSteps = 14

func (s *pccState) readsUntilCollapsedTwice() error {
	s.provider.i = 0
	s.provider.steps = nil
	for i := 0; i < pccReadSteps; i++ {
		s.provider.steps = append(s.provider.steps, pcStep{
			calls: []llm.ToolCall{pcReadCall(fmt.Sprintf("read%02d", i), fmt.Sprintf("file_%02d.go", i))},
		})
	}
	s.provider.steps = append(s.provider.steps, pcStep{text: "done"})
	_, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "read them all"}})
	return err
}

// --- assertions ------------------------------------------------------------

// fold is the summarization request number n (1-based) of the run.
func (s *pccState) fold(n int) ([]llm.Message, []llm.ToolDefinition, error) {
	if len(s.provider.folds) < n {
		return nil, nil, fmt.Errorf("the run made %d summarization request(s), wanted at least %d", len(s.provider.folds), n)
	}
	return s.provider.folds[n-1], s.provider.foldTools[n-1], nil
}

// turnBefore is the last streamed request made before summarization request n,
// without its turn context block: the bytes the provider cached last.
func (s *pccState) turnBefore(n int) ([]llm.Message, error) {
	if len(s.turnsBefore) < n {
		return nil, fmt.Errorf("the run compacted %d time(s), wanted at least %d", len(s.turnsBefore), n)
	}
	streamed := s.turnsBefore[n-1]
	if streamed == 0 {
		return nil, fmt.Errorf("compaction %d followed no streamed request", n)
	}
	return withoutTurnContext(s.provider.turns[streamed-1]), nil
}

// foldRepeatsTheTurn holds summarization request n to the bytes the provider
// has already seen: every message in front of its instruction is, at its own
// position, the message the turn's last request carried there. The fold stops
// at the compaction boundary and the turn's request runs on to the kept tail,
// so the shared part ends with the shorter of the two.
func (s *pccState) foldRepeatsTheTurn(n int) error {
	req, _, err := s.fold(n)
	if err != nil {
		return err
	}
	prev, err := s.turnBefore(n)
	if err != nil {
		return err
	}
	if len(req)-1 > len(prev) {
		return fmt.Errorf("summarization request %d carries %d message(s) in front of its instruction, more than the %d the turn's last request held: the extra ones were never sent",
			n, len(req)-1, len(prev))
	}
	shared := len(req) - 1
	if shared < 3 {
		return fmt.Errorf("summarization request %d shares %d message(s) with the turn's last request: too few to have repeated anything", n, shared)
	}
	for i := 0; i < shared; i++ {
		if reflect.DeepEqual(prev[i], req[i]) {
			continue
		}
		return fmt.Errorf("summarization request %d differs from the turn's last request at message %d (%s):\nturn: %q\nfold: %q",
			n, i, prev[i].Role, truncateForError(prev[i].Content), truncateForError(req[i].Content))
	}
	return nil
}

func (s *pccState) instructionIsLast(n int) error {
	req, _, err := s.fold(n)
	if err != nil {
		return err
	}
	last := req[len(req)-1]
	if last.Role != llm.RoleUser {
		return fmt.Errorf("the last message of summarization request %d is %s, not a user message", n, last.Role)
	}
	if !strings.Contains(last.Content, cachedCompactionMarker) {
		return fmt.Errorf("the last message of summarization request %d is not the summarization instruction: %q", n, truncateForError(last.Content))
	}
	if !last.Ephemeral {
		return fmt.Errorf("the summarization instruction of request %d is not marked request-only", n)
	}
	for i, m := range req[:len(req)-1] {
		if strings.Contains(m.Content, cachedCompactionMarker) {
			return fmt.Errorf("message %d of summarization request %d also carries the instruction", i, n)
		}
	}
	return nil
}

func (s *pccState) everyOtherMessageWasSent(n int) error {
	req, _, err := s.fold(n)
	if err != nil {
		return err
	}
	stored := map[string]bool{}
	for _, m := range s.st.GetMessages() {
		stored[string(m.Role)+"\x00"+m.Content] = true
	}
	for i, m := range req[:len(req)-1] {
		if i == 0 && m.Role == llm.RoleSystem {
			continue
		}
		if stored[string(m.Role)+"\x00"+m.Content] {
			continue
		}
		return fmt.Errorf("message %d of summarization request %d is in no transcript row: %s %q", i, n, m.Role, truncateForError(m.Content))
	}
	return nil
}

func (s *pccState) foldOpensWithTheTurnsSystemMessage(n int) error {
	req, _, err := s.fold(n)
	if err != nil {
		return err
	}
	prev, err := s.turnBefore(n)
	if err != nil {
		return err
	}
	if len(req) == 0 || req[0].Role != llm.RoleSystem {
		return fmt.Errorf("summarization request %d does not open with a system message", n)
	}
	if req[0].Content != prev[0].Content {
		return fmt.Errorf("summarization request %d opens with a system message of its own:\nturn: %q\nfold: %q",
			n, truncateForError(prev[0].Content), truncateForError(req[0].Content))
	}
	return nil
}

func (s *pccState) foldCarriesTheTurnsTools(n int) error {
	_, tools, err := s.fold(n)
	if err != nil {
		return err
	}
	if len(s.turnsBefore) < n || s.turnsBefore[n-1] == 0 {
		return fmt.Errorf("compaction %d followed no streamed request", n)
	}
	want := s.provider.turnTools[s.turnsBefore[n-1]-1]
	if len(want) == 0 {
		return fmt.Errorf("the turn carried no tool definitions: nothing to compare")
	}
	if !reflect.DeepEqual(want, tools) {
		return fmt.Errorf("summarization request %d carries %d tool definition(s), the turn carried %d", n, len(tools), len(want))
	}
	return nil
}

func (s *pccState) foldIsTheFlattenedTranscript(n int) error {
	req, _, err := s.fold(n)
	if err != nil {
		return err
	}
	if len(req) != 2 {
		return fmt.Errorf("the flattened request holds %d message(s), want 2", len(req))
	}
	if req[0].Role != llm.RoleSystem {
		return fmt.Errorf("the flattened request does not open with a system message")
	}
	prev, err := s.turnBefore(n)
	if err != nil {
		return err
	}
	if req[0].Content == prev[0].Content {
		return fmt.Errorf("the flattened request opens with the turn's system message, not the summarizer's own")
	}
	if !strings.Contains(req[1].Content, "<transcript>") {
		return fmt.Errorf("the flattened request carries no transcript: %q", truncateForError(req[1].Content))
	}
	return nil
}

func (s *pccState) foldCarriesNoTools(n int) error {
	_, tools, err := s.fold(n)
	if err != nil {
		return err
	}
	if len(tools) != 0 {
		return fmt.Errorf("summarization request %d carries %d tool definition(s), want none", n, len(tools))
	}
	return nil
}

func (s *pccState) foldCarriesTheFirstSummaryRow(n int) error {
	req, _, err := s.fold(n)
	if err != nil {
		return err
	}
	for _, m := range req {
		if m.CompactionSummary && strings.Contains(m.Content, "summary 1") {
			return nil
		}
	}
	return fmt.Errorf("summarization request %d carries no summary row of its own", n)
}

func (s *pccState) noFoldCarriesAnEarlierSummariesBlock() error {
	for i, req := range s.provider.folds {
		for _, m := range req {
			if strings.Contains(m.Content, "<earlier-summaries>") {
				return fmt.Errorf("summarization request %d rebuilt the earlier summaries as a block of text", i+1)
			}
		}
	}
	return nil
}

// histories is every streamed request of the run without its turn context block.
func (s *pccState) histories() [][]llm.Message {
	out := make([][]llm.Message, len(s.provider.turns))
	for i, r := range s.provider.turns {
		out[i] = withoutTurnContext(r)
	}
	return out
}

// collapsePoints is, for every request that collapsed a result the request
// before it carried whole, the index of that request.
func (s *pccState) collapsePoints() []int {
	hist := s.histories()
	var out []int
	for i := 1; i < len(hist); i++ {
		prev, cur := hist[i-1], hist[i]
		for j := range prev {
			if j < len(cur) && isPlaceholder(cur[j]) && !isPlaceholder(prev[j]) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

func (s *pccState) everyRequestRepeatsTheOneBeforeIt() error {
	hist := s.histories()
	if len(hist) < 3 {
		return fmt.Errorf("the run made %d requests: too few to compare", len(hist))
	}
	for i := 1; i < len(hist); i++ {
		prev, cur := hist[i-1], hist[i]
		if len(cur) < len(prev) {
			return fmt.Errorf("request %d is shorter than request %d: a history only grows", i, i-1)
		}
		for j := range prev {
			if reflect.DeepEqual(prev[j], cur[j]) {
				continue
			}
			if isPlaceholder(cur[j]) && !isPlaceholder(prev[j]) {
				break
			}
			return fmt.Errorf("request %d changed message %d without collapsing it:\nbefore: %q\nafter:  %q",
				i, j, truncateForError(prev[j].Content), truncateForError(cur[j].Content))
		}
	}
	return nil
}

func (s *pccState) collapsesAreFollowedByPlainGrowth() error {
	points := s.collapsePoints()
	if len(points) < 2 {
		return fmt.Errorf("the run collapsed a result %d time(s), wanted at least twice: nothing was tested", len(points))
	}
	for i := 1; i < len(points); i++ {
		if gap := points[i] - points[i-1]; gap < resultEvictionBatch {
			return fmt.Errorf("requests %d and %d both collapsed a result, %d request(s) apart: the batch is %d",
				points[i-1], points[i], gap, resultEvictionBatch)
		}
	}
	hist := s.histories()
	for _, p := range points {
		if p+1 >= len(hist) {
			continue
		}
		prev, cur := hist[p], hist[p+1]
		if len(cur) < len(prev) || !reflect.DeepEqual(prev, cur[:len(prev)]) {
			return fmt.Errorf("request %d, right after the collapse of request %d, did not simply grow", p+1, p)
		}
	}
	return nil
}

func initializePromptCacheCompactionScenario(sc *godog.ScenarioContext) {
	s := &pccState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, err
	})

	sc.Step(`^an agent session in a workspace$`, s.session)
	sc.Step(`^an agent session whose summarizer is another model$`, s.sessionOtherSummarizer)
	sc.Step(`^an agent session in a workspace of many files$`, s.manyFiles)
	sc.Step(`^an agent session whose hooks enrich the first message and every turn$`, s.sessionWithHooks)
	sc.Step(`^the model works through a few turns$`, s.worksThroughAFewTurns)
	sc.Step(`^the operator compacts the conversation$`, s.operatorCompacts)
	sc.Step(`^the model reads one file per step until results are collapsed twice$`, s.readsUntilCollapsedTwice)

	sc.Step(`^the summarization request repeats, message for message, what the turn had already sent$`, func() error {
		return s.foldRepeatsTheTurn(1)
	})
	sc.Step(`^the second summarization request repeats, message for message, what the turn had already sent$`, func() error {
		return s.foldRepeatsTheTurn(2)
	})
	sc.Step(`^the summarization instruction is the last message of the summarization request$`, func() error {
		return s.instructionIsLast(1)
	})
	sc.Step(`^every other message of the summarization request is one the session had sent$`, func() error {
		return s.everyOtherMessageWasSent(1)
	})
	sc.Step(`^the summarization request opens with the system message of the turn$`, func() error {
		return s.foldOpensWithTheTurnsSystemMessage(1)
	})
	sc.Step(`^the summarization request carries the tool definitions of the turn$`, func() error {
		return s.foldCarriesTheTurnsTools(1)
	})
	sc.Step(`^the second summarization request carries the first summary row as a message of its own$`, func() error {
		return s.foldCarriesTheFirstSummaryRow(2)
	})
	sc.Step(`^no summarization request carries the earlier summaries as a block of text$`, s.noFoldCarriesAnEarlierSummariesBlock)
	sc.Step(`^the turns carried the context the hooks handed over$`, s.turnsCarriedTheHookContext)
	sc.Step(`^no summarization request carries the one-time enrichment of the first message$`, s.noFoldCarriesTheFirstPromptContext)
	sc.Step(`^the summarization request is the flattened transcript under its own system message$`, func() error {
		return s.foldIsTheFlattenedTranscript(1)
	})
	sc.Step(`^the summarization request carries no tool definitions$`, func() error {
		return s.foldCarriesNoTools(1)
	})
	sc.Step(`^every request repeats the previous one up to the first result it collapsed$`, s.everyRequestRepeatsTheOneBeforeIt)
	sc.Step(`^a request that collapses results is followed by requests that only grow$`, s.collapsesAreFollowedByPlainGrowth)
}

func TestPromptCacheCompactionFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "prompt-cache-compaction",
		ScenarioInitializer: initializePromptCacheCompactionScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/prompt_cache_compaction.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("prompt-cache-compaction feature failed")
	}
}

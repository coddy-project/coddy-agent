package agent

// Godog harness for features/rules_agents_dir.feature: rules kept in the
// tool-neutral .agents/rules folder, parsed in the dialect their extension
// names (.mdc is a Cursor rule, .md a Claude Code rule). The catalog scenario
// renders the same table `coddy rules list` prints; the prompt scenarios drive
// the real Agent.Run against a fake provider and inspect the system message of
// every request it received, which is the only honest view of what the model
// was told.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// bddAnswerProvider answers every request at once and records what it saw.
type bddAnswerProvider struct {
	seen [][]llm.Message
}

func (p *bddAnswerProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("Complete must not be used by the .agents/rules suite")
}

func (p *bddAnswerProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	const answer = "Done."
	onChunk(llm.StreamChunk{TextDelta: answer})
	return &llm.Response{Content: answer, StopReason: "end_turn"}, nil
}

type agentsDirRulesFeatureState struct {
	tmpDirs []string
	cwd     string
	st      *session.State
	ag      *Agent
	catalog string
	seen    [][]llm.Message
}

func (s *agentsDirRulesFeatureState) reset() error {
	s.close()
	return nil
}

func (s *agentsDirRulesFeatureState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.cwd = ""
	s.st = nil
	s.ag = nil
	s.catalog = ""
	s.seen = nil
}

func (s *agentsDirRulesFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-agents-dir-rules-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

// projectWithAgentsDirRules writes the table rows as rule files. The
// frontmatter column carries YAML lines separated by ";" because a Gherkin
// cell cannot hold a newline; an empty cell means no frontmatter at all.
func (s *agentsDirRulesFeatureState) projectWithAgentsDirRules(folder string, table *godog.Table) error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	s.cwd = cwd
	root := filepath.Join(cwd, filepath.FromSlash(folder))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if len(table.Rows) < 2 {
		return fmt.Errorf("the rule files table needs a header and at least one row")
	}
	for _, row := range table.Rows[1:] {
		if len(row.Cells) != 3 {
			return fmt.Errorf("rule file row needs 3 cells, got %d", len(row.Cells))
		}
		file, frontmatter, body := row.Cells[0].Value, row.Cells[1].Value, row.Cells[2].Value
		var b strings.Builder
		if strings.TrimSpace(frontmatter) != "" {
			b.WriteString("---\n")
			for _, line := range strings.Split(frontmatter, ";") {
				b.WriteString(strings.TrimSpace(line))
				b.WriteString("\n")
			}
			b.WriteString("---\n\n")
		}
		b.WriteString(body)
		b.WriteString("\n")
		if err := os.WriteFile(filepath.Join(root, file), []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	// The file the model will read in the path-scoped scenario.
	apiDir := filepath.Join(cwd, "internal", "api")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(apiDir, "handler.go"), []byte("package api\n"), 0o644)
}

func (s *agentsDirRulesFeatureState) operatorListsCatalog() error {
	if s.cwd == "" {
		return fmt.Errorf("no project prepared")
	}
	var buf bytes.Buffer
	if err := rules.RenderCatalog(&buf, s.cwd, rules.DefaultFactory(""), nil); err != nil {
		return err
	}
	s.catalog = buf.String()
	return nil
}

// catalogListsRule finds a table row whose SOURCE, FORMAT and NAME cells hold
// the expected values. Cells are compared exactly so "go" cannot pass on the
// strength of "go-conventions".
func (s *agentsDirRulesFeatureState) catalogListsRule(name, source, format string) error {
	for _, line := range strings.Split(s.catalog, "\n") {
		if !strings.Contains(line, "│") {
			continue
		}
		var cells []string
		for _, c := range strings.Split(line, "│") {
			if t := strings.TrimSpace(c); t != "" {
				cells = append(cells, t)
			}
		}
		if len(cells) < 3 {
			continue
		}
		if cells[0] == source && cells[1] == format && cells[2] == name {
			return nil
		}
	}
	return fmt.Errorf("no catalog row with source %q, format %q and name %q in:\n%s", source, format, name, s.catalog)
}

func (s *agentsDirRulesFeatureState) agentSessionInThatProject() error {
	if s.cwd == "" {
		return fmt.Errorf("no project prepared")
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.st = &session.State{
		ID:         "sess_bdd_agents_dir_rules",
		CWD:        s.cwd,
		Mode:       session.ModeAgent,
		SessionDir: sessionDir,
	}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 6},
	}
	cfg.Prompts.ApplyDefaults()
	s.st.ReplaceRulesCatalog(session.DiscoverRules(cfg, s.cwd))
	s.ag = NewAgent(cfg, s.st, resumePermissionSender{}, nil)
	return nil
}

func (s *agentsDirRulesFeatureState) run(prompt string, provider llm.Provider, seen func() [][]llm.Message, minRequests int) error {
	if s.ag == nil {
		return fmt.Errorf("no agent prepared")
	}
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	stop, err := s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: prompt}})
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		return fmt.Errorf("stop reason = %q, want end_turn", stop)
	}
	s.seen = seen()
	if len(s.seen) < minRequests {
		return fmt.Errorf("expected at least %d request(s), got %d", minRequests, len(s.seen))
	}
	return nil
}

func (s *agentsDirRulesFeatureState) modelAnswersWithoutTouchingFiles() error {
	p := &bddAnswerProvider{}
	return s.run("summarize the project", p, func() [][]llm.Message { return p.seen }, 1)
}

func (s *agentsDirRulesFeatureState) modelReadsFileThenAnswers(path string) error {
	p := &bddReadThenAnswerProvider{readPath: path}
	return s.run("summarize the api package", p, func() [][]llm.Message { return p.seen }, 2)
}

func (s *agentsDirRulesFeatureState) userAsksAndModelAnswers(prompt string) error {
	p := &bddAnswerProvider{}
	return s.run(prompt, p, func() [][]llm.Message { return p.seen }, 1)
}

// systemPrompt returns the system message of the nth request (0-based).
func (s *agentsDirRulesFeatureState) systemPrompt(n int) (string, error) {
	if n >= len(s.seen) {
		return "", fmt.Errorf("request %d was never made (%d total)", n, len(s.seen))
	}
	msgs := s.seen[n]
	if len(msgs) == 0 || msgs[0].Role != llm.RoleSystem {
		return "", fmt.Errorf("request %d does not start with a system message", n)
	}
	return msgs[0].Content, nil
}

var bddQuotedTokenRE = regexp.MustCompile(`"([^"]+)"`)

// quotedTokens extracts every "TOKEN" from a step tail such as
// `"A", "B" nor "C"`, so one step text covers any number of tokens.
func quotedTokens(tail string) []string {
	var out []string
	for _, m := range bddQuotedTokenRE.FindAllStringSubmatch(tail, -1) {
		out = append(out, m[1])
	}
	return out
}

func (s *agentsDirRulesFeatureState) lastRequestCarries(tail string) error {
	sp, err := s.systemPrompt(len(s.seen) - 1)
	if err != nil {
		return err
	}
	for _, tok := range quotedTokens(tail) {
		if !strings.Contains(sp, tok) {
			return fmt.Errorf("the request is missing %s", tok)
		}
	}
	return nil
}

func (s *agentsDirRulesFeatureState) lastRequestCarriesNone(tail string) error {
	sp, err := s.systemPrompt(len(s.seen) - 1)
	if err != nil {
		return err
	}
	for _, tok := range quotedTokens(tail) {
		if strings.Contains(sp, tok) {
			return fmt.Errorf("the request carries %s although nothing activated that rule", tok)
		}
	}
	return nil
}

func (s *agentsDirRulesFeatureState) firstRequestCarriesNone(tail string) error {
	sp, err := s.systemPrompt(0)
	if err != nil {
		return err
	}
	for _, tok := range quotedTokens(tail) {
		if strings.Contains(sp, tok) {
			return fmt.Errorf("the first request carries %s before any file was read", tok)
		}
	}
	return nil
}

func (s *agentsDirRulesFeatureState) requestsAfterReadCarry(tail string) error {
	if len(s.seen) < 2 {
		return fmt.Errorf("expected a request after the read, got %d request(s)", len(s.seen))
	}
	for n := 1; n < len(s.seen); n++ {
		sp, err := s.systemPrompt(n)
		if err != nil {
			return err
		}
		for _, tok := range quotedTokens(tail) {
			if !strings.Contains(sp, tok) {
				return fmt.Errorf("request %d is missing %s after the read", n, tok)
			}
		}
	}
	return nil
}

func initializeAgentsDirRulesScenario(sc *godog.ScenarioContext) {
	s := &agentsDirRulesFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a project whose "([^"]*)" folder holds these rule files:$`, s.projectWithAgentsDirRules)
	sc.Step(`^the operator lists the rules catalog$`, s.operatorListsCatalog)
	sc.Step(`^the catalog lists "([^"]*)" from source "([^"]*)" in the "([^"]*)" format$`, s.catalogListsRule)
	sc.Step(`^a coddy agent session in that project$`, s.agentSessionInThatProject)
	sc.Step(`^the model answers without touching any file$`, s.modelAnswersWithoutTouchingFiles)
	sc.Step(`^the model reads "([^"]*)" and then answers$`, s.modelReadsFileThenAnswers)
	sc.Step(`^the user asks "([^"]*)" and the model answers$`, s.userAsksAndModelAnswers)
	// The positive forms only accept a quoted token list, so "carries neither"
	// cannot match them and every step has exactly one definition.
	sc.Step(`^the request carries ("[^"]+"(?:(?:,| and) "[^"]+")*)$`, s.lastRequestCarries)
	sc.Step(`^the request carries neither (.+)$`, s.lastRequestCarriesNone)
	sc.Step(`^the first request carries neither (.+)$`, s.firstRequestCarriesNone)
	sc.Step(`^every request after the read carries ("[^"]+"(?:(?:,| and) "[^"]+")*)$`, s.requestsAfterReadCarry)
}

func TestAgentsDirRulesFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "rules-agents-dir",
		ScenarioInitializer: initializeAgentsDirRulesScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/rules_agents_dir.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal(".agents/rules feature suite failed")
	}
}

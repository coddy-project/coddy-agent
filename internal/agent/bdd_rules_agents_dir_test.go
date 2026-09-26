package agent

// Godog harness for features/rules_agents_dir.feature and
// features/rules_one_folder.feature: rules kept in the tool-neutral
// .agents/rules folder, parsed in the dialect their extension names (.mdc is a
// Cursor rule, .md a Claude Code rule), and the one project folder of the chain
// .coddy/rules, .agents/rules, .cursor/rules, .claude/rules that is read. The
// catalog scenarios render the same table `coddy rules list` prints; the prompt
// scenarios drive the real Agent.Run against a fake provider and inspect every
// request it received, which is the only honest view of what the model was
// told.

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

// projectWithAgentsDirRules starts a project and writes the table rows as rule
// files of folder.
func (s *agentsDirRulesFeatureState) projectWithAgentsDirRules(folder string, table *godog.Table) error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	s.cwd = cwd
	if err := s.writeRuleFiles(folder, table); err != nil {
		return err
	}
	// The file the model will read in the path-scoped scenario.
	apiDir := filepath.Join(cwd, "internal", "api")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(apiDir, "handler.go"), []byte("package api\n"), 0o644)
}

// projectAlsoHoldsRules writes rule files into another folder of the project
// the scenario already started.
func (s *agentsDirRulesFeatureState) projectAlsoHoldsRules(folder string, table *godog.Table) error {
	if s.cwd == "" {
		return fmt.Errorf("no project prepared")
	}
	return s.writeRuleFiles(folder, table)
}

// projectFolderHoldsNoRuleFile creates folder with a file that is not a rule,
// so the folder exists and still holds nothing to read.
func (s *agentsDirRulesFeatureState) projectFolderHoldsNoRuleFile(folder string) error {
	if s.cwd == "" {
		return fmt.Errorf("no project prepared")
	}
	root := filepath.Join(s.cwd, filepath.FromSlash(folder))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "notes.txt"), []byte("not a rule\n"), 0o644)
}

// writeRuleFiles writes the table rows as rule files of folder. The
// frontmatter column carries YAML lines separated by ";" because a Gherkin
// cell cannot hold a newline; an empty cell means no frontmatter at all.
func (s *agentsDirRulesFeatureState) writeRuleFiles(folder string, table *godog.Table) error {
	root := filepath.Join(s.cwd, filepath.FromSlash(folder))
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
	return nil
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

// catalogRows returns the cells of every table row of the catalog.
func (s *agentsDirRulesFeatureState) catalogRows() [][]string {
	var rows [][]string
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
		if len(cells) >= 3 {
			rows = append(rows, cells)
		}
	}
	return rows
}

func (s *agentsDirRulesFeatureState) catalogListsNoRuleFrom(source string) error {
	for _, cells := range s.catalogRows() {
		if cells[0] == source {
			return fmt.Errorf("the catalog lists %q from source %q:\n%s", cells[2], source, s.catalog)
		}
	}
	return nil
}

// catalogNamesSkippedFolder finds the line under the table that names the
// project folders holding rules that were not read.
func (s *agentsDirRulesFeatureState) catalogNamesSkippedFolder(folder string) error {
	for _, line := range strings.Split(s.catalog, "\n") {
		if strings.HasPrefix(line, "Not read: ") && strings.Contains(line, folder) {
			return nil
		}
	}
	return fmt.Errorf("the catalog does not name %s as a folder it did not read:\n%s", folder, s.catalog)
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
	// The manager resolves a prompt's mentions before the agent runs it
	// (session.Manager.ResolvePromptMentions); the suite does the same.
	blocks := (*session.Manager)(nil).ResolvePromptMentions(context.Background(), s.st, []acp.ContentBlock{{Type: "text", Text: prompt}}, session.MentionScope{})
	stop, err := s.ag.Run(context.Background(), blocks)
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

// wholeRequest returns everything the nth request says to the model. A rule or
// a nested AGENTS.md a tool call pulled in mid-turn arrives in the turn context
// block after the history, not in the frozen system message, so what matters
// here is that the model was told, not where (turn_context.go).
func (s *agentsDirRulesFeatureState) wholeRequest(n int) (string, error) {
	if n >= len(s.seen) {
		return "", fmt.Errorf("request %d was never made (%d total)", n, len(s.seen))
	}
	var b strings.Builder
	for _, m := range s.seen[n] {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String(), nil
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

// userMessageCarries checks the last user message of the last request: a
// rule the user named rides there, in the message that named it.
func (s *agentsDirRulesFeatureState) userMessageCarries(tail string) error {
	if len(s.seen) == 0 {
		return fmt.Errorf("no request was made")
	}
	msgs := s.seen[len(s.seen)-1]
	var user string
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser && !strings.HasPrefix(msgs[i].Content, turnContextOpenTag) {
			user = msgs[i].Content
			break
		}
	}
	for _, tok := range quotedTokens(tail) {
		if !strings.Contains(user, tok) {
			return fmt.Errorf("the user's message is missing %s:\n%s", tok, user)
		}
	}
	return nil
}

// requestCarriesOnce counts tok over everything the last request says.
func (s *agentsDirRulesFeatureState) requestCarriesOnce(tok string) error {
	all, err := s.wholeRequest(len(s.seen) - 1)
	if err != nil {
		return err
	}
	if n := strings.Count(all, tok); n != 1 {
		return fmt.Errorf("the request carries %s %d times, want once", tok, n)
	}
	return nil
}

// resultOfReadCarries checks the tool result of the read in the last request:
// a rule the read activated rides there, not in the system message.
func (s *agentsDirRulesFeatureState) resultOfReadCarries(tail string) error {
	if len(s.seen) == 0 {
		return fmt.Errorf("no request was made")
	}
	var result string
	found := false
	for _, m := range s.seen[len(s.seen)-1] {
		if m.Role == llm.RoleTool {
			result, found = m.Content, true
		}
	}
	if !found {
		return fmt.Errorf("the last request carries no tool result")
	}
	for _, tok := range quotedTokens(tail) {
		if !strings.Contains(result, tok) {
			return fmt.Errorf("the result of the read is missing %s:\n%s", tok, result)
		}
	}
	return nil
}

func (s *agentsDirRulesFeatureState) everyRequestOpensWithSameSystemMessage() error {
	first, err := s.systemPrompt(0)
	if err != nil {
		return err
	}
	for n := 1; n < len(s.seen); n++ {
		sp, err := s.systemPrompt(n)
		if err != nil {
			return err
		}
		if sp != first {
			return fmt.Errorf("the system message of request %d differs from the first one", n)
		}
	}
	return nil
}

func (s *agentsDirRulesFeatureState) requestsAfterReadCarry(tail string) error {
	if len(s.seen) < 2 {
		return fmt.Errorf("expected a request after the read, got %d request(s)", len(s.seen))
	}
	for n := 1; n < len(s.seen); n++ {
		sp, err := s.wholeRequest(n)
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
	sc.Step(`^its "([^"]*)" folder holds these rule files:$`, s.projectAlsoHoldsRules)
	sc.Step(`^its "([^"]*)" folder holds no rule file$`, s.projectFolderHoldsNoRuleFile)
	sc.Step(`^the operator lists the rules catalog$`, s.operatorListsCatalog)
	sc.Step(`^the catalog lists "([^"]*)" from source "([^"]*)" in the "([^"]*)" format$`, s.catalogListsRule)
	sc.Step(`^the catalog lists no rule from source "([^"]*)"$`, s.catalogListsNoRuleFrom)
	sc.Step(`^the catalog names "([^"]*)" as a folder it did not read$`, s.catalogNamesSkippedFolder)
	sc.Step(`^a coddy agent session in that project$`, s.agentSessionInThatProject)
	sc.Step(`^the model answers without touching any file$`, s.modelAnswersWithoutTouchingFiles)
	sc.Step(`^the model reads "([^"]*)" and then answers$`, s.modelReadsFileThenAnswers)
	sc.Step(`^the user asks "([^"]*)" and the model answers$`, s.userAsksAndModelAnswers)
	// The positive forms only accept a quoted token list, so "carries neither"
	// cannot match them and every step has exactly one definition.
	sc.Step(`^the request carries ("[^"]+"(?:(?:,| and) "[^"]+")*)$`, s.lastRequestCarries)
	sc.Step(`^the request carries neither (.+)$`, s.lastRequestCarriesNone)
	sc.Step(`^the system prompt carries neither (.+)$`, s.lastRequestCarriesNone)
	sc.Step(`^the user's message carries ("[^"]+"(?:(?:,| and) "[^"]+")*)$`, s.userMessageCarries)
	sc.Step(`^the first request carries neither (.+)$`, s.firstRequestCarriesNone)
	sc.Step(`^every request after the read carries ("[^"]+"(?:(?:,| and) "[^"]+")*)$`, s.requestsAfterReadCarry)
	sc.Step(`^the request carries "([^"]*)" exactly once$`, s.requestCarriesOnce)
	sc.Step(`^the result of the read carries ("[^"]+"(?:(?:,| and) "[^"]+")*)$`, s.resultOfReadCarries)
	sc.Step(`^every request opens with the same system message$`, s.everyRequestOpensWithSameSystemMessage)
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

func TestRulesOneFolderFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "rules-one-folder",
		ScenarioInitializer: initializeAgentsDirRulesScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/rules_one_folder.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("one project rules folder feature suite failed")
	}
}

package agent

// Godog harness for features/global_instructions.feature: the instructions and
// rules that belong to the operator rather than to a checkout, read from
// CODDY_HOME for every session. The scenarios drive the real Agent.Run against
// a fake provider and inspect the system message of every request it received,
// which is the only honest view of what the model was told; the catalog
// scenario renders the same table `coddy rules list` prints.

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type globalInstructionsFeatureState struct {
	tmpDirs []string
	home    string
	cwd     string
	st      *session.State
	ag      *Agent
	catalog string
	seen    [][]llm.Message
	// What the scenario configures before the session starts.
	promptsDir       string
	instructionFiles []string
	tweak            func(*config.Config)
	// sharedFile is a file of instructions.files that lives outside the
	// agent home and the project, the way several agents share one.
	sharedFile string
	// missingFile is an absolute entry of instructions.files with nothing
	// behind it.
	missingFile string
	// logs is what the agent logged during the scenario.
	logs bytes.Buffer
}

func (s *globalInstructionsFeatureState) reset() error {
	s.close()
	return nil
}

func (s *globalInstructionsFeatureState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.home = ""
	s.cwd = ""
	s.st = nil
	s.ag = nil
	s.catalog = ""
	s.seen = nil
	s.promptsDir = ""
	s.instructionFiles = nil
	s.tweak = nil
	s.sharedFile = ""
	s.missingFile = ""
	s.logs.Reset()
}

func (s *globalInstructionsFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-global-instructions-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

func (s *globalInstructionsFeatureState) agentHomeWithFile(name, body string) error {
	home, err := s.tempDir()
	if err != nil {
		return err
	}
	s.home = home
	return os.WriteFile(filepath.Join(home, filepath.FromSlash(name)), []byte(body+"\n"), 0o644)
}

// agentHomeRules writes the table rows as rule files under <home>/<folder>.
// The frontmatter column carries YAML lines separated by ";" because a Gherkin
// cell cannot hold a newline; an empty cell means no frontmatter at all.
// agentHomeAlsoHas writes a second document into the home prepared above.
func (s *globalInstructionsFeatureState) agentHomeAlsoHas(name, body string) error {
	if s.home == "" {
		return fmt.Errorf("no agent home prepared")
	}
	return os.WriteFile(filepath.Join(s.home, filepath.FromSlash(name)), []byte(body+"\n"), 0o644)
}

// nestedDocs writes the pair a folder can describe itself with, which a tool
// call into that folder reads on the spot.
func (s *globalInstructionsFeatureState) nestedDocs(folder, agentsFile, agentsBody, designFile, designBody string) error {
	if s.cwd == "" {
		return fmt.Errorf("no project prepared")
	}
	dir := filepath.Join(s.cwd, filepath.FromSlash(folder))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, agentsFile), []byte(agentsBody+"\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, designFile), []byte(designBody+"\n"), 0o644)
}

func (s *globalInstructionsFeatureState) agentHomeRules(folder string, table *godog.Table) error {
	if s.home == "" {
		return fmt.Errorf("no agent home prepared")
	}
	root := filepath.Join(s.home, filepath.FromSlash(folder))
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

// projectWithoutAgentsMD is the plain case: a checkout that says nothing, so
// everything in the prompt came from the operator's own home.
func (s *globalInstructionsFeatureState) projectWithoutAgentsMD() error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	s.cwd = cwd
	apiDir := filepath.Join(cwd, "internal", "api")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(apiDir, "handler.go"), []byte("package api\n"), 0o644)
}

func (s *globalInstructionsFeatureState) projectWithFile(name, body string) error {
	if err := s.projectWithoutAgentsMD(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.cwd, filepath.FromSlash(name)), []byte(body+"\n"), 0o644)
}

func (s *globalInstructionsFeatureState) agentSessionInThatProject() error {
	if s.cwd == "" {
		return fmt.Errorf("no project prepared")
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.st = &session.State{
		ID:         "sess_bdd_global_instructions",
		CWD:        s.cwd,
		Mode:       session.ModeAgent,
		SessionDir: sessionDir,
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 6},
	}
	cfg.Prompts.ApplyDefaults()
	cfg.Prompts.Dir = s.promptsDir
	cfg.Instructions.Files = s.instructionFiles
	cfg.Instructions.ApplyDefaults()
	if s.tweak != nil {
		s.tweak(cfg)
	}
	s.st.ReplaceRulesCatalog(session.DiscoverRules(cfg, s.cwd))
	s.ag = NewAgent(cfg, s.st, resumePermissionSender{}, slog.New(slog.NewTextHandler(&s.logs, nil)))
	return nil
}

// agentSessionInAgentHome opens the session in CODDY_HOME itself, where the
// operator's pair and the session folder's pair are the same two files.
func (s *globalInstructionsFeatureState) agentSessionInAgentHome() error {
	if s.home == "" {
		return fmt.Errorf("no agent home prepared")
	}
	s.cwd = s.home
	return s.agentSessionInThatProject()
}

// projectLinkingHomeAgentsMD is a checkout whose AGENTS.md is a symlink to
// the operator's own.
func (s *globalInstructionsFeatureState) projectLinkingHomeAgentsMD() error {
	if err := s.projectWithoutAgentsMD(); err != nil {
		return err
	}
	if err := os.Symlink(filepath.Join(s.home, "AGENTS.md"), filepath.Join(s.cwd, "AGENTS.md")); err != nil {
		// Windows without the symlink privilege: nothing to check there.
		return godog.ErrSkip
	}
	return nil
}

func (s *globalInstructionsFeatureState) projectAlsoHas(nameA, bodyA, nameB, bodyB string) error {
	if s.cwd == "" {
		return fmt.Errorf("no project prepared")
	}
	for _, f := range [][2]string{{nameA, bodyA}, {nameB, bodyB}} {
		path := filepath.Join(s.cwd, filepath.FromSlash(f[0]))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(f[1]+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *globalInstructionsFeatureState) instructionFilesList(tail string) error {
	s.instructionFiles = quotedTokens(tail)
	return nil
}

// sharedFolderHolds writes a file into a folder that is neither the agent
// home nor the project, the way several agents with configurations of their
// own share one set of instructions. The text is long enough that the context
// estimate cannot count it under rules by accident.
func (s *globalInstructionsFeatureState) sharedFolderHolds(name, token string) error {
	dir, err := s.tempDir()
	if err != nil {
		return err
	}
	body := token + "\n\n" + strings.Repeat("Every agent of this fleet follows the house style. ", 80)
	s.sharedFile = filepath.Join(dir, filepath.FromSlash(name))
	return os.WriteFile(s.sharedFile, []byte(body+"\n"), 0o644)
}

func (s *globalInstructionsFeatureState) instructionFilesListsShared() error {
	if s.sharedFile == "" {
		return fmt.Errorf("no shared file prepared")
	}
	s.instructionFiles = []string{s.sharedFile}
	return nil
}

func (s *globalInstructionsFeatureState) instructionFilesListsMissing() error {
	dir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.missingFile = filepath.Join(dir, "shared", "infrastructure.md")
	s.instructionFiles = []string{s.missingFile}
	return nil
}

// requestNamesSharedFile is what lets the model connect the text it was
// given with the file the operator talks about: without the name, "follow
// infrastructure.md" sends it to read a file it already holds.
func (s *globalInstructionsFeatureState) requestNamesSharedFile() error {
	sp, err := s.systemPrompt(len(s.seen) - 1)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(s.sharedFile)
	if err != nil {
		return err
	}
	want := "### " + rules.UserDocLabel(s.cwd, s.sharedFile) + "\n\n" + strings.TrimSpace(string(data))
	if !strings.Contains(sp, want) {
		return fmt.Errorf("the request does not carry the shared file under %q", "### "+rules.UserDocLabel(s.cwd, s.sharedFile))
	}
	return nil
}

// contextCountsSharedUnderRules reads the estimate the context ring and the
// stats endpoint show: an instruction file is a rule the operator wrote, so
// its tokens belong to the rules share, not to the system prompt.
func (s *globalInstructionsFeatureState) contextCountsSharedUnderRules() error {
	b := s.st.GetLastContextBreakdown()
	if b == nil {
		return fmt.Errorf("no context estimate was published")
	}
	data, err := os.ReadFile(s.sharedFile)
	if err != nil {
		return err
	}
	want := session.EstimateContextTokens(strings.TrimSpace(string(data)))
	if b.Rules < want {
		return fmt.Errorf("the rules share is %d tokens, want at least the %d of the shared file (system prompt %d)", b.Rules, want, b.SystemPrompt)
	}
	return nil
}

func (s *globalInstructionsFeatureState) logWarnsUnread() error {
	logs := s.logs.String()
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, "instructions file not read") && strings.Contains(line, s.missingFile) {
			return nil
		}
	}
	return fmt.Errorf("no warning names %s; the log was:\n%s", s.missingFile, logs)
}

// templateWithoutBlocks is an operator's prompts.dir whose templates print
// neither {{.Rules}} nor {{.Instructions}}: the documents must reach the
// model anyway.
func (s *globalInstructionsFeatureState) templateWithoutBlocks() error {
	dir, err := s.tempDir()
	if err != nil {
		return err
	}
	const body = "You are a test agent working in {{.CWD}}.\n\n{{if .Tools}}## Available tools\n\n{{.Tools}}\n{{end}}"
	for _, file := range []string{"agent.md", "plan.md", "ask.md"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			return err
		}
	}
	s.promptsDir = dir
	return nil
}

// configurationSets applies one of the rules settings that used to switch the
// nested documents off.
func (s *globalInstructionsFeatureState) configurationSets(setting string) error {
	switch strings.TrimSpace(setting) {
	case "rules.auto_discover: false":
		s.tweak = func(c *config.Config) {
			off := false
			c.Rules.AutoDiscover = &off
		}
	case "rules.systems: [coddy]":
		s.tweak = func(c *config.Config) { c.Rules.Systems = []string{"coddy"} }
	default:
		return fmt.Errorf("unknown setting %q", setting)
	}
	return nil
}

// everyRequestCarriesOnce counts in everything each request says, the system
// message and the tool results alike: a document reaches the model once.
func (s *globalInstructionsFeatureState) everyRequestCarriesOnce(token string) error {
	if len(s.seen) == 0 {
		return fmt.Errorf("no request was made")
	}
	for n := range s.seen {
		whole, err := s.wholeRequest(n)
		if err != nil {
			return err
		}
		if c := strings.Count(whole, token); c != 1 {
			return fmt.Errorf("request %d carries %q %d time(s), want exactly 1", n, token, c)
		}
	}
	return nil
}

func (s *globalInstructionsFeatureState) run(prompt string, provider llm.Provider, seen func() [][]llm.Message, minRequests int) error {
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

func (s *globalInstructionsFeatureState) modelAnswers() error {
	p := &bddAnswerProvider{}
	return s.run("summarize the project", p, func() [][]llm.Message { return p.seen }, 1)
}

func (s *globalInstructionsFeatureState) modelReadsFileThenAnswers(path string) error {
	p := &bddReadThenAnswerProvider{readPath: path}
	return s.run("summarize the api package", p, func() [][]llm.Message { return p.seen }, 2)
}

func (s *globalInstructionsFeatureState) systemPrompt(n int) (string, error) {
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
func (s *globalInstructionsFeatureState) wholeRequest(n int) (string, error) {
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

func (s *globalInstructionsFeatureState) requestCarries(tail string) error {
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

// requestCarriesOnce is the dedupe guard: the project AGENTS.md is both the
// project docs preamble of the rules block and an entry of instructions.files,
// and sending it twice is paying twice for the same text.
func (s *globalInstructionsFeatureState) requestCarriesOnce(token string) error {
	sp, err := s.systemPrompt(len(s.seen) - 1)
	if err != nil {
		return err
	}
	if n := strings.Count(sp, token); n != 1 {
		return fmt.Errorf("the request carries %q %d time(s), want exactly 1", token, n)
	}
	return nil
}

// requestCarriesBefore is the ordering the operator asked for: what they want
// from every session is read before what the checkout says about itself.
func (s *globalInstructionsFeatureState) requestCarriesBefore(first, second string) error {
	sp, err := s.systemPrompt(len(s.seen) - 1)
	if err != nil {
		return err
	}
	i, j := strings.Index(sp, first), strings.Index(sp, second)
	if i < 0 || j < 0 {
		return fmt.Errorf("the request carries %q at %d and %q at %d, want both", first, i, second, j)
	}
	if i > j {
		return fmt.Errorf("the request carries %q after %q", first, second)
	}
	return nil
}

func (s *globalInstructionsFeatureState) firstRequestCarriesNone(tail string) error {
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

func (s *globalInstructionsFeatureState) requestsAfterReadCarry(tail string) error {
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

func (s *globalInstructionsFeatureState) operatorListsCatalog() error {
	if s.cwd == "" {
		return fmt.Errorf("no project prepared")
	}
	var buf bytes.Buffer
	if err := rules.RenderCatalog(&buf, s.cwd, rules.DefaultFactory(s.home), nil); err != nil {
		return err
	}
	s.catalog = buf.String()
	return nil
}

func (s *globalInstructionsFeatureState) catalogListsRule(name, source, format string) error {
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

func initializeGlobalInstructionsScenario(sc *godog.ScenarioContext) {
	s := &globalInstructionsFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^an agent home whose "([^"]*)" holds "([^"]*)"$`, s.agentHomeWithFile)
	sc.Step(`^that agent home also has a "([^"]*)" holding "([^"]*)"$`, s.agentHomeAlsoHas)
	sc.Step(`^that agent home holds these rule files under "([^"]*)":$`, s.agentHomeRules)
	sc.Step(`^"([^"]*)" holds an "([^"]*)" with "([^"]*)" and a "([^"]*)" with "([^"]*)"$`, s.nestedDocs)
	sc.Step(`^a project without an AGENTS\.md of its own$`, s.projectWithoutAgentsMD)
	sc.Step(`^a project whose "([^"]*)" holds "([^"]*)"$`, s.projectWithFile)
	sc.Step(`^a coddy agent session in that project$`, s.agentSessionInThatProject)
	sc.Step(`^a coddy agent session in the agent home itself$`, s.agentSessionInAgentHome)
	sc.Step(`^a project whose AGENTS\.md is a link to the agent home's$`, s.projectLinkingHomeAgentsMD)
	sc.Step(`^the project also has "([^"]*)" holding "([^"]*)" and "([^"]*)" holding "([^"]*)"$`, s.projectAlsoHas)
	sc.Step(`^instructions\.files lists ("[^"]+"(?:(?:,| and) "[^"]+")*)$`, s.instructionFilesList)
	sc.Step(`^a folder outside the agent home and the project holds "([^"]*)" with "([^"]*)"$`, s.sharedFolderHolds)
	sc.Step(`^instructions\.files lists that shared file by its absolute path$`, s.instructionFilesListsShared)
	sc.Step(`^instructions\.files lists an absolute path that does not exist$`, s.instructionFilesListsMissing)
	sc.Step(`^the request names the shared file in a heading above its text$`, s.requestNamesSharedFile)
	sc.Step(`^the context estimate counts the shared file under rules$`, s.contextCountsSharedUnderRules)
	sc.Step(`^the log warns that the instructions file was not read and names its path$`, s.logWarnsUnread)
	sc.Step(`^the operator's prompts\.dir template prints neither \{\{\.Rules\}\} nor \{\{\.Instructions\}\}$`, s.templateWithoutBlocks)
	sc.Step(`^the configuration sets (.+)$`, s.configurationSets)
	sc.Step(`^every request carries "([^"]+)" exactly once$`, s.everyRequestCarriesOnce)
	sc.Step(`^the model answers without touching any file$`, s.modelAnswers)
	sc.Step(`^the model reads "([^"]*)" and then answers$`, s.modelReadsFileThenAnswers)
	sc.Step(`^the operator lists the rules catalog$`, s.operatorListsCatalog)
	sc.Step(`^the catalog lists "([^"]*)" from source "([^"]*)" in the "([^"]*)" format$`, s.catalogListsRule)
	// The "exactly once" form is matched before the plain token list so both
	// steps stay unambiguous under Strict.
	sc.Step(`^the request carries "([^"]+)" exactly once$`, s.requestCarriesOnce)
	sc.Step(`^the request carries "([^"]+)" before "([^"]+)"$`, s.requestCarriesBefore)
	sc.Step(`^the request carries ("[^"]+"(?:(?:,| and) "[^"]+")*)$`, s.requestCarries)
	sc.Step(`^the first request carries neither (.+)$`, s.firstRequestCarriesNone)
	sc.Step(`^every request after the read carries ("[^"]+"(?:(?:,| and) "[^"]+")*)$`, s.requestsAfterReadCarry)
}

func TestGlobalInstructionsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "global-instructions",
		ScenarioInitializer: initializeGlobalInstructionsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/global_instructions.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("global instructions feature suite failed")
	}
}

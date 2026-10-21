package agent

// Godog harness for features/prompt_cache_rules.feature: a project that keeps
// its rules for Cursor and a copy of each for Claude Code, a folder with an
// AGENTS.md of its own, and a scripted session of several turns over the real
// Agent. Every request the provider received is checked as a whole - what it
// carries (each rule at most once, nothing unmatched, no copy kept for another
// agent) and how it relates to the request before it (the same system message,
// the history growing only at its end), which is what the provider's prompt
// cache keys on.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// claudeCopyPrefix marks the body of a rule's copy kept for Claude Code, so a
// request that carries the copy can be told apart from one that carries the
// rule.
const claudeCopyPrefix = "CLAUDE_COPY_"

type pcRulesFeatureState struct {
	*pcFeatureState
	// tokens are the bodies of every rule document of the project: the
	// Cursor rules, their Claude Code copies and the nested AGENTS.md.
	tokens []string
	// copies are the bodies of the Claude Code copies alone.
	copies []string
	// firstRead maps a path to the id of the call that read it first.
	firstRead map[string]string
	calls     int
	// namingPrompt is the prompt that named rules by their address.
	namingPrompt string
}

// projectWithMirroredRules writes each row as a Cursor rule and as the Claude
// Code copy a project keeps next to it: globs become paths, an alwaysApply
// rule loses its header (Claude Code loads a rule without paths
// unconditionally), and the body is marked as the copy.
func (s *pcRulesFeatureState) projectWithMirroredRules(table *godog.Table) error {
	if err := s.reset(); err != nil {
		return err
	}
	s.tokens, s.copies, s.firstRead, s.calls = nil, nil, map[string]string{}, 0
	if len(table.Rows) < 2 {
		return fmt.Errorf("the rules table needs a header and at least one row")
	}
	for _, row := range table.Rows[1:] {
		if len(row.Cells) != 3 {
			return fmt.Errorf("rule row needs 3 cells, got %d", len(row.Cells))
		}
		name, frontmatter, body := row.Cells[0].Value, row.Cells[1].Value, row.Cells[2].Value
		var cursor, claude []string
		for _, line := range strings.Split(frontmatter, ";") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			cursor = append(cursor, line)
			if pattern, ok := strings.CutPrefix(line, "globs:"); ok {
				claude = append(claude, "paths:", fmt.Sprintf("  - %q", strings.TrimSpace(pattern)))
			}
		}
		if err := writeRuleDoc(filepath.Join(s.cwd, ".cursor", "rules", name+".mdc"), cursor, body); err != nil {
			return err
		}
		if err := writeRuleDoc(filepath.Join(s.cwd, ".claude", "rules", name+".md"), claude, claudeCopyPrefix+body); err != nil {
			return err
		}
		s.tokens = append(s.tokens, body, claudeCopyPrefix+body)
		s.copies = append(s.copies, claudeCopyPrefix+body)
	}
	for _, f := range []string{"main.go", "internal/api/handler.go", "internal/api/routes.go", "web/app.ts"} {
		path := filepath.Join(s.cwd, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("// "+f+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// writeRuleDoc writes a rule file with the header lines given, none meaning a
// file without frontmatter.
func writeRuleDoc(path string, header []string, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	if len(header) > 0 {
		b.WriteString("---\n" + strings.Join(header, "\n") + "\n---\n\n")
	}
	b.WriteString(body + "\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func (s *pcRulesFeatureState) folderDescribesItself(dir, token string) error {
	path := filepath.Join(s.cwd, filepath.FromSlash(dir), "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	s.tokens = append(s.tokens, token)
	return os.WriteFile(path, []byte(token+"\n"), 0o644)
}

// rewriteFolderAgents changes a nested AGENTS.md on disk between two turns, the
// way the agent's own edit or the person watching it would.
func (s *pcRulesFeatureState) rewriteFolderAgents(dir, token string) error {
	s.tokens = append(s.tokens, token)
	return os.WriteFile(filepath.Join(s.cwd, filepath.FromSlash(dir), "AGENTS.md"), []byte(token+"\n"), 0o644)
}

// subprojectKeepsGlobRule writes a Cursor rule into the rule folder of a
// subfolder after the session started, with the files its pattern names: a
// nested rule folder is read when a tool first reaches it, never at the start.
func (s *pcRulesFeatureState) subprojectKeepsGlobRule(dir, glob, token string) error {
	if err := writeRuleDoc(filepath.Join(s.cwd, filepath.FromSlash(dir), ".cursor", "rules", "nested.mdc"), []string{"globs: " + glob}, token); err != nil {
		return err
	}
	s.tokens = append(s.tokens, token)
	for _, f := range []string{"migrations/0001.sql", "migrations/0002.sql"} {
		path := filepath.Join(s.cwd, filepath.FromSlash(dir), filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("-- "+f+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// manualRuleInFolder writes a manual Cursor rule into a folder of the rule
// folder the session reads, so its name is its path there. It is written
// before the first turn and the session opened again, as if it had been there
// when the session started: the catalog is read at the start.
func (s *pcRulesFeatureState) manualRuleInFolder(name, token string) error {
	path := filepath.Join(s.cwd, ".cursor", "rules", filepath.FromSlash(name)+".mdc")
	if err := writeRuleDoc(path, []string{"description: " + name, "alwaysApply: false"}, token); err != nil {
		return err
	}
	s.tokens = append(s.tokens, token)
	s.buildAgent()
	return nil
}

// subprojectKeepsManualRule writes a manual rule into the Coddy rule folder of
// a subfolder, the one "@rule:<folder>/<name>" reads when a message names it.
func (s *pcRulesFeatureState) subprojectKeepsManualRule(dir, name, token string) error {
	path := filepath.Join(s.cwd, filepath.FromSlash(dir), ".coddy", "rules", filepath.FromSlash(name)+".mdc")
	if err := writeRuleDoc(path, []string{"description: " + name, "alwaysApply: false"}, token); err != nil {
		return err
	}
	s.tokens = append(s.tokens, token)
	return nil
}

// userNamesRules runs one turn whose prompt goes through the session
// manager's mention resolver first, as every surface's prompt does, and the
// model answers straight away.
func (s *pcRulesFeatureState) userNamesRules(prompt string) error {
	s.namingPrompt = prompt
	mgr := session.NewManager(s.cfg, nil, nil, slog.Default(), s.cwd, nil)
	blocks := mgr.ResolvePromptMentions(context.Background(), s.st, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: prompt}}, session.MentionScope{})
	s.provider.steps = []pcStep{{text: "answer"}}
	s.provider.i = 0
	_, err := s.ag.Run(context.Background(), blocks)
	return err
}

func (s *pcRulesFeatureState) session() error {
	s.buildAgent()
	return nil
}

// readsThenAnswers scripts one turn: a read of every quoted path in order,
// then an answer.
func (s *pcRulesFeatureState) readsThenAnswers(prompt, list string) error {
	var steps []pcStep
	for _, path := range quotedTokens(list) {
		s.calls++
		id := fmt.Sprintf("r%d", s.calls)
		if _, ok := s.firstRead[path]; !ok {
			s.firstRead[path] = id
		}
		steps = append(steps, pcStep{calls: []llm.ToolCall{pcReadCall(id, path)}})
	}
	s.provider.steps = append(steps, pcStep{text: "answer"})
	return s.runPrompt(prompt)
}

func (s *pcRulesFeatureState) modelReadsAndAnswers(list string) error {
	return s.readsThenAnswers("go through the code", list)
}

func (s *pcRulesFeatureState) userAsksAgainModelReads(list string) error {
	return s.readsThenAnswers("once more, please", list)
}

// wholeRequest is everything one request says to the model.
func wholeRequest(req []llm.Message) string {
	var b strings.Builder
	for _, m := range req {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func (s *pcRulesFeatureState) eachRuleAtMostOnce() error {
	reqs := s.requests()
	if len(reqs) == 0 {
		return fmt.Errorf("no requests recorded")
	}
	for i, r := range reqs {
		all := wholeRequest(r)
		for _, tok := range s.tokens {
			if n := strings.Count(all, tok); n > 1 {
				return fmt.Errorf("request %d carries %s %d times", i, tok, n)
			}
		}
	}
	return nil
}

func (s *pcRulesFeatureState) systemMessageOfEveryRequestCarries(tok string) error {
	for i, r := range s.requests() {
		if !strings.Contains(r[0].Content, tok) {
			return fmt.Errorf("the system message of request %d does not carry %s", i, tok)
		}
	}
	return nil
}

// reachWithFirstRead checks the result of the first read of path, as the last
// request replays it: the rules that read brought in ride there.
func (s *pcRulesFeatureState) reachWithFirstRead(list, path string) error {
	id, ok := s.firstRead[path]
	if !ok {
		return fmt.Errorf("the model never read %s", path)
	}
	reqs := s.requests()
	if len(reqs) == 0 {
		return fmt.Errorf("no requests recorded")
	}
	res, ok := toolResultOf(reqs[len(reqs)-1], id)
	if !ok {
		return fmt.Errorf("the last request does not replay the read %s of %s", id, path)
	}
	for _, tok := range quotedTokens(list) {
		if !strings.Contains(res, tok) {
			return fmt.Errorf("the first read of %s does not carry %s: %q", path, tok, truncateForError(res))
		}
	}
	return nil
}

// reachInTheMessageThatNamesThem checks the user message of the prompt that
// named the rules, as the last request replays it: their bodies ride there,
// and nowhere before that message.
func (s *pcRulesFeatureState) reachInTheMessageThatNamesThem(list string) error {
	reqs := s.requests()
	if len(reqs) == 0 {
		return fmt.Errorf("no requests recorded")
	}
	var named string
	found := false
	for _, m := range reqs[len(reqs)-1] {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, s.namingPrompt) {
			named, found = m.Content, true
			break
		}
	}
	if !found {
		return fmt.Errorf("the last request replays no user message %q", s.namingPrompt)
	}
	for _, tok := range quotedTokens(list) {
		if !strings.Contains(named, tok) {
			return fmt.Errorf("the message naming the rules does not carry %s: %q", tok, truncateForError(named))
		}
		for i, r := range reqs {
			if strings.Contains(r[0].Content, tok) {
				return fmt.Errorf("the system message of request %d carries %s", i, tok)
			}
		}
	}
	return nil
}

func (s *pcRulesFeatureState) noRequestCarries(tok string) error {
	for i, r := range s.requests() {
		if strings.Contains(wholeRequest(r), tok) {
			return fmt.Errorf("request %d carries %s, which nothing brought in", i, tok)
		}
	}
	return nil
}

func (s *pcRulesFeatureState) noRequestCarriesACopy() error {
	for i, r := range s.requests() {
		all := wholeRequest(r)
		for _, tok := range s.copies {
			if strings.Contains(all, tok) {
				return fmt.Errorf("request %d carries %s, the copy kept for Claude Code", i, tok)
			}
		}
	}
	return nil
}

func initializePromptCacheRulesScenario(sc *godog.ScenarioContext) {
	s := &pcRulesFeatureState{pcFeatureState: &pcFeatureState{}}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, err
	})

	sc.Step(`^a project that keeps these rules for Cursor and a copy of each for Claude Code:$`, s.projectWithMirroredRules)
	sc.Step(`^its folder "([^"]*)" describes itself in an AGENTS\.md reading "([^"]*)"$`, s.folderDescribesItself)
	sc.Step(`^a coddy agent session in that project$`, s.session)
	sc.Step(`^the model reads ("[^"]+"(?:, then "[^"]+")*), and answers$`, s.modelReadsAndAnswers)
	sc.Step(`^the user asks again, and the model reads ("[^"]+"(?:, then "[^"]+")*), and answers$`, s.userAsksAgainModelReads)
	sc.Step(`^someone rewrites the AGENTS\.md of "([^"]*)" to read "([^"]*)"$`, s.rewriteFolderAgents)
	sc.Step(`^its subproject "([^"]*)" keeps a Cursor rule for "([^"]*)" reading "([^"]*)"$`, s.subprojectKeepsGlobRule)
	sc.Step(`^its rule folder keeps a manual rule "([^"]*)" reading "([^"]*)"$`, s.manualRuleInFolder)
	sc.Step(`^its subproject "([^"]*)" keeps a manual Coddy rule "([^"]*)" reading "([^"]*)"$`, s.subprojectKeepsManualRule)
	sc.Step(`^the user asks "([^"]*)", and the model answers$`, s.userNamesRules)

	sc.Step(`^every request carries each rule at most once$`, s.eachRuleAtMostOnce)
	sc.Step(`^every request carries "([^"]*)" in its system message$`, s.systemMessageOfEveryRequestCarries)
	sc.Step(`^("[^"]+"(?: and "[^"]+")*) reach(?:es)? the model with the first read of "([^"]*)"$`, s.reachWithFirstRead)
	sc.Step(`^("[^"]+"(?: and "[^"]+")*) reach(?:es)? the model in the message that names (?:it|them)$`, s.reachInTheMessageThatNamesThem)
	sc.Step(`^no request carries "([^"]*)"$`, s.noRequestCarries)
	sc.Step(`^no request carries the copy of any rule kept for Claude Code$`, s.noRequestCarriesACopy)
	sc.Step(`^every request opens with the system message of the first request$`, s.sameSystemMessageEveryRequest)
	sc.Step(`^every request repeats the one before it up to its turn context block$`, s.requestsGrowByAppendOnly)
}

func TestPromptCacheRulesFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "prompt-cache-rules",
		ScenarioInitializer: initializePromptCacheRulesScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/prompt_cache_rules.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("prompt cache rules feature suite failed")
	}
}

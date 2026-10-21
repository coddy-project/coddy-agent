package agent

// Godog harness for features/rules_nested_folders.feature: a group folder with
// a rule folder of its own and subprojects that keep theirs, and a scripted
// session over the real Agent that reads files across them. Every request the
// provider received is checked for where each rule rode: the group's rule in
// the system message, a subproject's in the result of the read that first
// reached it, and nothing of a subproject anywhere else.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type nestedRulesFeatureState struct {
	*pcRulesFeatureState
}

// groupFolder starts a workspace whose own .cursor/rules holds one rule that
// always applies, with the files the scenarios read across the subprojects.
func (s *nestedRulesFeatureState) groupFolder(name, body string) error {
	if err := s.reset(); err != nil {
		return err
	}
	s.tokens, s.copies, s.firstRead, s.calls = nil, nil, map[string]string{}, 0
	for _, f := range []string{"db/schema.sql", "db/migrations/0001.sql", "mobile/app.ts"} {
		path := filepath.Join(s.cwd, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("-- "+f+"\n"), 0o644); err != nil {
			return err
		}
	}
	return s.writeRule(".", ".cursor", name+".mdc", []string{"alwaysApply: true"}, body)
}

// writeRule writes one rule file into the rule folder dot (".cursor") of the
// workspace folder dir and records its body as a rule token.
func (s *nestedRulesFeatureState) writeRule(dir, dot, file string, header []string, body string) error {
	path := filepath.Join(s.cwd, filepath.FromSlash(dir), dot, "rules", file)
	s.tokens = append(s.tokens, body)
	return writeRuleDoc(path, header, body)
}

func (s *nestedRulesFeatureState) subprojectAlwaysRuleWithClaudeCopy(dir, name, body string) error {
	if err := s.writeRule(dir, ".cursor", name+".mdc", []string{"alwaysApply: true"}, body); err != nil {
		return err
	}
	// Claude Code loads a rule without paths unconditionally: the copy has no
	// header, as in a project that mirrors its rules for both agents.
	copyBody := claudeCopyPrefix + body
	s.copies = append(s.copies, copyBody)
	return s.writeRule(dir, ".claude", name+".md", nil, copyBody)
}

func (s *nestedRulesFeatureState) subprojectCoddyGlobRule(dir, name, glob, body string) error {
	return s.writeRule(dir, ".coddy", name+".mdc", []string{"globs: " + glob}, body)
}

func (s *nestedRulesFeatureState) groupAndSubprojectShareARuleName(dir, name, groupBody, subBody string) error {
	if err := s.writeRule(".", ".cursor", name+".mdc", []string{"alwaysApply: true"}, groupBody); err != nil {
		return err
	}
	return s.writeRule(dir, ".cursor", name+".mdc", []string{"alwaysApply: true"}, subBody)
}

func (s *nestedRulesFeatureState) noSystemMessageCarries(a, b string) error {
	for i, r := range s.requests() {
		for _, tok := range []string{a, b} {
			if strings.Contains(r[0].Content, tok) {
				return fmt.Errorf("the system message of request %d carries %s", i, tok)
			}
		}
	}
	return nil
}

// readBringsInNoRule checks the result of the first read of path, as the last
// request replays it: no rule rides with it.
func (s *nestedRulesFeatureState) readBringsInNoRule(path string) error {
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
	if strings.Contains(res, toolCallRulesLead) {
		return fmt.Errorf("the read of %s brought in rules: %q", path, truncateForError(res))
	}
	return nil
}

func initializeRulesNestedScenario(sc *godog.ScenarioContext) {
	s := &nestedRulesFeatureState{pcRulesFeatureState: &pcRulesFeatureState{pcFeatureState: &pcFeatureState{}}}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, err
	})

	sc.Step(`^a group folder whose own Cursor rule "([^"]*)" always applies and reads "([^"]*)"$`, s.groupFolder)
	sc.Step(`^its subproject "([^"]*)" keeps a Cursor rule "([^"]*)" that always applies and reads "([^"]*)", with a copy for Claude Code$`, s.subprojectAlwaysRuleWithClaudeCopy)
	sc.Step(`^its subproject "([^"]*)" keeps a Coddy rule "([^"]*)" for "([^"]*)" that reads "([^"]*)"$`, s.subprojectCoddyGlobRule)
	sc.Step(`^the group folder and its subproject "([^"]*)" each keep a Cursor rule "([^"]*)" that always applies, reading "([^"]*)" and "([^"]*)"$`, s.groupAndSubprojectShareARuleName)
	sc.Step(`^a coddy agent session in the group folder$`, s.session)
	sc.Step(`^the model reads ("[^"]+"(?:, then "[^"]+")*), and answers$`, s.modelReadsAndAnswers)

	sc.Step(`^every request carries "([^"]*)" in its system message$`, s.systemMessageOfEveryRequestCarries)
	sc.Step(`^no system message carries "([^"]*)" or "([^"]*)"$`, s.noSystemMessageCarries)
	sc.Step(`^the read of "([^"]*)" brings in no rule$`, s.readBringsInNoRule)
	sc.Step(`^("[^"]+"(?: and "[^"]+")*) reach(?:es)? the model with the first read of "([^"]*)"$`, s.reachWithFirstRead)
	sc.Step(`^no request carries the copy of any rule kept for Claude Code$`, s.noRequestCarriesACopy)
	sc.Step(`^every request carries each rule at most once$`, s.eachRuleAtMostOnce)
}

func TestRulesNestedFoldersFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "rules-nested-folders",
		ScenarioInitializer: initializeRulesNestedScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/rules_nested_folders.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("nested rule folders feature suite failed")
	}
}

package subagents

// Godog harness for features/subagents_catalog.feature: real files under a
// temporary coddy home and workspace, the real loader and trust store, and the
// same listing the CLI prints, so the scenarios describe what an operator sees.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type catalogFeatureState struct {
	home    string
	cwd     string
	other   string
	listing string
	entries []CatalogEntry
}

func (s *catalogFeatureState) reset() error {
	s.home = ""
	s.cwd = ""
	s.other = ""
	s.listing = ""
	s.entries = nil
	return nil
}

func (s *catalogFeatureState) ensureHome() error {
	if s.home != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "coddy-bdd-subagents-home-*")
	if err != nil {
		return err
	}
	s.home = dir
	return nil
}

func (s *catalogFeatureState) ensureWorkspace() error {
	if s.cwd != "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "coddy-bdd-subagents-ws-*")
	if err != nil {
		return err
	}
	s.cwd = dir
	return nil
}

func (s *catalogFeatureState) close() {
	for _, d := range []string{s.home, s.cwd, s.other} {
		if d != "" {
			_ = os.RemoveAll(d)
		}
	}
}

func definitionFile(name, description string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\nYou are %s.\n", name, description, name)
}

func (s *catalogFeatureState) homeDefinition(name string) error {
	return s.homeDefinitionDescribed(name, "a user-scope helper")
}

func (s *catalogFeatureState) homeDefinitionDescribed(name, description string) error {
	if err := s.ensureHome(); err != nil {
		return err
	}
	path := filepath.Join(s.home, "agents", name+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(definitionFile(name, description)), 0o644)
}

func (s *catalogFeatureState) workspaceDefinition(name string) error {
	return s.workspaceDefinitionDescribed(name, "a project-scope helper")
}

func (s *catalogFeatureState) workspaceDefinitionDescribed(name, description string) error {
	if err := s.ensureWorkspace(); err != nil {
		return err
	}
	path := filepath.Join(s.cwd, ".coddy", "agents", name+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(definitionFile(name, description)), 0o644)
}

func (s *catalogFeatureState) listFor(cwd string) error {
	if err := s.ensureHome(); err != nil {
		return err
	}
	loader := NewLoader([]string{"${CODDY_HOME}/agents", "${CWD}/.claude/agents", "${CWD}/.coddy/agents"}, "ask")
	defs := loader.Load(cwd, s.home)
	s.entries = BuildCatalog(defs, "ask", CanonicalWorkspace(cwd), NewTrustStore(s.home))
	var b strings.Builder
	WriteListing(&b, s.entries)
	s.listing = b.String()
	return nil
}

func (s *catalogFeatureState) listWorkspace() error {
	if err := s.ensureWorkspace(); err != nil {
		return err
	}
	return s.listFor(s.cwd)
}

// listOtherWorkspace copies the project file into a second workspace so the
// same definition (same digest) is seen from a workspace that never approved it.
func (s *catalogFeatureState) listOtherWorkspace() error {
	if s.other == "" {
		dir, err := os.MkdirTemp("", "coddy-bdd-subagents-ws2-*")
		if err != nil {
			return err
		}
		s.other = dir
	}
	src := filepath.Join(s.cwd, ".coddy", "agents")
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	dst := filepath.Join(s.other, ".coddy", "agents")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			return err
		}
	}
	return s.listFor(s.other)
}

func (s *catalogFeatureState) entry(name string) (*CatalogEntry, error) {
	for i := range s.entries {
		if s.entries[i].Name == name {
			return &s.entries[i], nil
		}
	}
	return nil, fmt.Errorf("listing does not name %q:\n%s", name, s.listing)
}

func (s *catalogFeatureState) namesBuiltin(name string) error {
	e, err := s.entry(name)
	if err != nil {
		return err
	}
	if !e.Builtin || e.Scope != ScopeBuiltin {
		return fmt.Errorf("%s is not listed as a built-in: %+v", name, *e)
	}
	return nil
}

func (s *catalogFeatureState) namesWithScope(name, scope string) error {
	e, err := s.entry(name)
	if err != nil {
		return err
	}
	if string(e.Scope) != scope {
		return fmt.Errorf("%s has scope %q, want %q", name, e.Scope, scope)
	}
	if !strings.Contains(s.listing, name) || !strings.Contains(s.listing, scope) {
		return fmt.Errorf("listing does not show %s with scope %s:\n%s", name, scope, s.listing)
	}
	return nil
}

func (s *catalogFeatureState) namesWithScopeAndTrust(name, scope, trust string) error {
	if err := s.namesWithScope(name, scope); err != nil {
		return err
	}
	e, _ := s.entry(name)
	if string(e.Trust) != trust {
		return fmt.Errorf("%s has trust %q, want %q", name, e.Trust, trust)
	}
	if !strings.Contains(s.listing, trust) {
		return fmt.Errorf("listing does not show trust %s:\n%s", trust, s.listing)
	}
	return nil
}

func (s *catalogFeatureState) describes(name, description string) error {
	e, err := s.entry(name)
	if err != nil {
		return err
	}
	if e.Description != description {
		return fmt.Errorf("%s is described as %q, want %q", name, e.Description, description)
	}
	return nil
}

func (s *catalogFeatureState) trust(name string) error {
	if err := s.ensureHome(); err != nil {
		return err
	}
	loader := NewLoader([]string{"${CODDY_HOME}/agents", "${CWD}/.claude/agents", "${CWD}/.coddy/agents"}, "ask")
	def := FindByName(loader.Load(s.cwd, s.home), name)
	if def == nil {
		return fmt.Errorf("no definition %q to trust", name)
	}
	return NewTrustStore(s.home).Approve(CanonicalWorkspace(s.cwd), def)
}

func initializeCatalogScenario(sc *godog.ScenarioContext) {
	s := &catalogFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy home with a subagent definition "([^"]*)"$`, s.homeDefinition)
	sc.Step(`^a coddy home with a subagent definition "([^"]*)" described as "([^"]*)"$`, s.homeDefinitionDescribed)
	sc.Step(`^a workspace with a subagent definition "([^"]*)" under \.coddy/agents$`, s.workspaceDefinition)
	sc.Step(`^a workspace with a subagent definition "([^"]*)" under \.coddy/agents described as "([^"]*)"$`, s.workspaceDefinitionDescribed)
	sc.Step(`^I list the subagent catalog for that workspace$`, s.listWorkspace)
	sc.Step(`^I list the subagent catalog for a different workspace holding the same file$`, s.listOtherWorkspace)
	sc.Step(`^I trust the subagent "([^"]*)" for that workspace$`, s.trust)
	sc.Step(`^the listing names "([^"]*)" as a built-in$`, s.namesBuiltin)
	sc.Step(`^the listing names "([^"]*)" with scope "([^"]*)"$`, s.namesWithScope)
	sc.Step(`^the listing names "([^"]*)" with scope "([^"]*)" and trust "([^"]*)"$`, s.namesWithScopeAndTrust)
	sc.Step(`^the listing describes "([^"]*)" as "([^"]*)"$`, s.describes)
}

func TestSubagentsCatalogFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "subagents-catalog",
		ScenarioInitializer: initializeCatalogScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/subagents_catalog.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("subagents catalog feature suite failed")
	}
}

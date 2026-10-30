package main

// Godog harness for features/commit_gate_models_catalog.feature, over the
// scratch checkout of commit_gate_test.go.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type commitGateState struct {
	checkout *gateCheckout
	output   string
	err      error
}

func (s *commitGateState) aCheckoutLackingTheModel(t *testing.T) func(string) error {
	return func(model string) error {
		if model != gateNewModel {
			return fmt.Errorf("the scratch catalog adds %q, not %q", gateNewModel, model)
		}
		s.checkout = newGateCheckout(t)
		return nil
	}
}

func (s *commitGateState) theCatalogIsRefreshedAndStaged(t *testing.T) func() error {
	return func() error {
		s.checkout.refreshCatalog(t)
		s.checkout.git(t, "add", gateCatalogPath)
		return nil
	}
}

func (s *commitGateState) aCodeChangeIsCommitted(t *testing.T) func() error {
	return func() error {
		s.output, s.err = s.checkout.commitCodeChange(t)
		return nil
	}
}

func (s *commitGateState) theCommitIsRefused() error {
	if s.err == nil {
		return fmt.Errorf("the commit landed:\n%s", s.output)
	}
	return nil
}

func (s *commitGateState) theCommitLands() error {
	if s.err != nil {
		return fmt.Errorf("the commit was refused: %v\n%s", s.err, s.output)
	}
	return nil
}

func (s *commitGateState) theGateSays(text string) error {
	if !strings.Contains(s.output, text) {
		return fmt.Errorf("the gate does not say %q:\n%s", text, s.output)
	}
	return nil
}

func (s *commitGateState) theCommitCarriesTheModel(t *testing.T) func(string) error {
	return func(model string) error {
		if got := s.checkout.git(t, "show", "HEAD:"+gateCatalogPath); !strings.Contains(got, `"`+model+`"`) {
			return fmt.Errorf("the committed catalog does not list %q:\n%s", model, got)
		}
		return nil
	}
}

func TestCommitGateModelsCatalogFeature(t *testing.T) {
	skipWithoutBash(t)
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &commitGateState{}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				*s = commitGateState{}
				return ctx, nil
			})
			sc.Step(`^a checkout whose models catalog lacks the model "([^"]*)" of models\.dev$`, s.aCheckoutLackingTheModel(t))
			sc.Step(`^the catalog has been refreshed and staged$`, s.theCatalogIsRefreshedAndStaged(t))
			sc.Step(`^a code change is committed$`, s.aCodeChangeIsCommitted(t))
			sc.Step(`^the commit is refused$`, s.theCommitIsRefused)
			sc.Step(`^the commit lands$`, s.theCommitLands)
			sc.Step(`^the gate names the model "([^"]*)"$`, s.theGateSays)
			sc.Step(`^the gate says to run "([^"]*)"$`, s.theGateSays)
			sc.Step(`^the commit carries the model "([^"]*)" in the catalog$`, s.theCommitCarriesTheModel(t))
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/commit_gate_models_catalog.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("commit gate models catalog feature failed")
	}
}

package main

// Godog harness for features/provider_diagnostics.feature: builds a real
// provider through llm.NewProvider against a stand-in endpoint that rejects
// everything, and renders `coddy providers list` for a config that points at
// the official OpenAI endpoint with nothing to authenticate with. Both are the
// paths a user meets when a model is wired to the wrong provider.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// diagUpstreamMessage is what the stand-in endpoint says, so the harness can
// assert the upstream text is not swallowed by the label around it.
const diagUpstreamMessage = "You didn't provide an API key"

type providerDiagState struct {
	upstream *httptest.Server
	provider llm.Provider
	callErr  error
	lines    []string
	cfg      *config.Config
	// restoreEnv puts back a provider key env var the scenario had to clear.
	restoreEnv func()
}

func (s *providerDiagState) reset() error {
	s.close()
	s.provider = nil
	s.callErr = nil
	s.lines = nil
	s.cfg = nil
	return nil
}

func (s *providerDiagState) close() {
	if s.upstream != nil {
		s.upstream.Close()
		s.upstream = nil
	}
	if s.restoreEnv != nil {
		s.restoreEnv()
		s.restoreEnv = nil
	}
}

func (s *providerDiagState) rejectingProvider(name string) error {
	s.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"error":{"message":%q,"type":"invalid_request_error"}}`, diagUpstreamMessage)
	}))
	p, err := llm.NewProvider(llm.ProviderInput{
		Name:    name,
		Type:    "openai",
		Model:   "some-model",
		APIKey:  "",
		BaseURL: s.upstream.URL,
	})
	if err != nil {
		return err
	}
	s.provider = p
	return nil
}

func (s *providerDiagState) streamThroughProvider() error {
	if s.provider == nil {
		return fmt.Errorf("no provider prepared")
	}
	_, s.callErr = s.provider.Stream(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "hello"}}, nil, func(llm.StreamChunk) {})
	if s.callErr == nil {
		return fmt.Errorf("the rejecting endpoint produced no error")
	}
	return nil
}

func (s *providerDiagState) noticeNamesProvider(name string) error {
	if !strings.Contains(s.callErr.Error(), `"`+name+`"`) {
		return fmt.Errorf("the notice does not name provider %q: %v", name, s.callErr)
	}
	return nil
}

func (s *providerDiagState) noticeNamesEndpointAddress() error {
	if !strings.Contains(s.callErr.Error(), s.upstream.URL) {
		return fmt.Errorf("the notice does not name %s: %v", s.upstream.URL, s.callErr)
	}
	return nil
}

func (s *providerDiagState) upstreamMessageSurvives() error {
	if !strings.Contains(s.callErr.Error(), diagUpstreamMessage) {
		return fmt.Errorf("the upstream message is gone from the notice: %v", s.callErr)
	}
	return nil
}

func (s *providerDiagState) configWithBareOpenAIProvider(name string) error {
	s.cfg = &config.Config{
		Providers: []config.ProviderConfig{{Name: name, Type: "openai"}},
	}
	// The env fallback would otherwise count as a credential on a developer
	// machine that exports one.
	env := config.ProviderAPIKeyEnvVarName(name)
	if prev, ok := os.LookupEnv(env); ok {
		s.restoreEnv = func() { _ = os.Setenv(env, prev) }
		if err := os.Unsetenv(env); err != nil {
			return err
		}
	}
	return nil
}

func (s *providerDiagState) runProviderList() error {
	if s.cfg == nil {
		return fmt.Errorf("no config prepared")
	}
	s.lines = providersListLines(s.cfg)
	return nil
}

func (s *providerDiagState) listText() string {
	return strings.Join(s.lines, "\n")
}

func (s *providerDiagState) warnsAboutOfficialEndpoint() error {
	if !strings.Contains(s.listText(), llm.OpenAIDefaultAPIBase()) {
		return fmt.Errorf("the list never names %s:\n%s", llm.OpenAIDefaultAPIBase(), s.listText())
	}
	return nil
}

func (s *providerDiagState) warningSaysNoCredential() error {
	txt := strings.ToLower(s.listText())
	if !strings.Contains(txt, "no credential") {
		return fmt.Errorf("the list does not say a credential is missing:\n%s", s.listText())
	}
	return nil
}

func (s *providerDiagState) warningNamesProvider(name string) error {
	for _, l := range s.lines {
		if strings.Contains(l, llm.OpenAIDefaultAPIBase()) && strings.Contains(l, name) {
			return nil
		}
	}
	return fmt.Errorf("no warning line names provider %q:\n%s", name, s.listText())
}

func initializeProviderDiagScenario(sc *godog.ScenarioContext) {
	s := &providerDiagState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a provider named "([^"]*)" of type openai whose endpoint rejects every request$`, s.rejectingProvider)
	sc.Step(`^a completion is streamed through that provider$`, s.streamThroughProvider)
	sc.Step(`^the notice names the provider "([^"]*)"$`, s.noticeNamesProvider)
	sc.Step(`^the notice names the address of that endpoint$`, s.noticeNamesEndpointAddress)
	sc.Step(`^the message from the endpoint survives in the notice$`, s.upstreamMessageSurvives)
	sc.Step(`^a config whose only provider is "([^"]*)" of type openai with no api_base and no key$`, s.configWithBareOpenAIProvider)
	sc.Step(`^I run the provider list$`, s.runProviderList)
	sc.Step(`^the list warns that the provider talks to the official OpenAI endpoint$`, s.warnsAboutOfficialEndpoint)
	sc.Step(`^the warning says no credential is configured$`, s.warningSaysNoCredential)
	sc.Step(`^the warning names the provider "([^"]*)"$`, s.warningNamesProvider)
}

func TestProviderDiagnosticsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "provider-diagnostics",
		ScenarioInitializer: initializeProviderDiagScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/provider_diagnostics.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("provider diagnostics feature failed")
	}
}

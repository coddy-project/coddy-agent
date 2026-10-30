//go:build http

package httpserver

import (
	"slices"
	"testing"

	"github.com/cucumber/godog"
)

func TestOnboardingStatusE2E(t *testing.T) {
	var status onboardingStatusDTO
	suite := godog.TestSuite{
		Name: "onboarding_status",
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/onboarding_status.feature"},
			TestingT: t,
			Strict:   true,
		},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a Coddy config with a ready local agent provider and a keyless spare provider$`, func() {
				status = onboardingStatusFor(t, `
providers:
  - name: local
    type: openai
    api_base: "http://127.0.0.1:8080/v1"
  - name: spare
    type: openai
models:
  - model: "local/qwen"
agent:
  model: "local/qwen"
`)
			})
			sc.Step(`^a Coddy config whose OpenAI agent provider has no credentials$`, func() {
				t.Setenv("OPENAI_API_KEY", "")
				status = onboardingStatusFor(t, `
providers:
  - name: openai
    type: openai
models:
  - model: "openai/gpt-5.6-terra"
agent:
  model: "openai/gpt-5.6-terra"
`)
			})
			sc.Step(`^the onboarding status is requested$`, func() {})
			sc.Step(`^the onboarding status keeps the provider picker closed$`, func() {
				if !status.HasProviders || !status.HasModels || !status.HasAgentModel || !status.HasAgentCredentials {
					t.Fatalf("status would open the picker: %+v", status)
				}
			})
			sc.Step(`^the onboarding status asks for the provider picker$`, func() {
				if status.HasAgentCredentials {
					t.Fatalf("status has unexpected agent credentials: %+v", status)
				}
			})
			sc.Step(`^the onboarding status lists "([^"]+)" among the providers without credentials$`, func(name string) {
				if !slices.Contains(status.MissingAPIKeys, name) {
					t.Fatalf("missing_api_keys = %v, want it to list %q", status.MissingAPIKeys, name)
				}
			})
		},
	}
	if suite.Run() != 0 {
		t.Fatal("onboarding_status feature failed")
	}
}

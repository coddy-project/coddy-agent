//go:build http && ui

package ui

import (
	"regexp"
	"testing"

	"github.com/cucumber/godog"
)

// Godog harness for features/provider_models_web_ui.feature: the provider
// form's model list, the logical-model form's id and context window fields,
// each step a vitest scenario over the components.
func TestProviderModelsWebUIFeature(t *testing.T) {
	const list = "src/ui/settings/ProviderModelList.test.tsx"
	const field = "src/ui/settings/ModelField.test.tsx"
	const window = "src/ui/settings/ContextWindowField.test.tsx"
	const section = "src/ui/settings/SettingsSection.test.tsx"
	const array = "src/ui/settings/SettingsArraySection.test.tsx"
	steps := map[string][2]string{
		"opening a provider row fetches the list with the row as the form holds it":            {list, "opening a provider row fetches the list with the row as the form holds it"},
		"a model is added to Logical models from its row":                                      {list, "a model is added to Logical models from its row"},
		"a model added from the provider list carries the context window the provider reports": {section, "a model added from the provider list carries the context window the provider reports"},
		"a model already in Logical models is checked and unchecking removes it":               {list, "a model already in Logical models is checked and unchecking removes it"},
		"a listed model the provider does not advertise is flagged and can be removed":         {list, "a listed model the provider does not advertise is flagged and can be removed"},
		"picking a provider and typing the id composes provider/id":                            {field, "picking a provider and typing the id composes provider/id"},
		"an unset window shows the one the provider reports for the model":                     {window, "an unset window shows the one the provider reports for the model"},
		"fetching the context window writes the provider's number into the model":              {window, "fetching the context window writes the provider's number into the model"},
		"opening a row puts its name in the address and going back takes it out":               {array, "opening a row puts its name in the address and going back takes it out"},
		"the address opens the row it names":                                                   {array, "the address opens the row it names"},
		"an address naming no row goes back to the list and clears the name":                   {array, "an address naming no row goes back to the list and clears the name"},
		"renaming the open row renames it in the address":                                      {array, "renaming the open row renames it in the address"},
	}
	suite := godog.TestSuite{
		Name: "provider_models_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for step, target := range steps {
				file, name := target[0], target[1]
				sc.Step("^"+regexp.QuoteMeta(step)+"$", func() error {
					return runVitestScenario(file, name)
				})
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/provider_models_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("provider models web UI feature failed")
	}
}

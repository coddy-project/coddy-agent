package main

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// schemaNode follows a dotted config key through the properties of a settings
// form schema; nil when the form does not show the key.
func schemaNode(doc map[string]interface{}, key string) map[string]interface{} {
	node := doc
	for _, part := range strings.Split(key, ".") {
		props, _ := node["properties"].(map[string]interface{})
		next, ok := props[part].(map[string]interface{})
		if !ok {
			return nil
		}
		node = next
	}
	return node
}

// TestSubsystemSwitchesWaitForSaveInTheSettingsForm: the settings form saves a
// change on its own a moment after the last edit, except where the schema says
// it waits for Save. Turning a surface of coddy serve on or off starts or stops
// something that reaches outside the process - a bot, a relay, the scheduler's
// runs - so every switch of a subsystem that a settings form shows carries the
// mark. A new subsystem whose switch the form offers fails here until it does.
func TestSubsystemSwitchesWaitForSaveInTheSettingsForm(t *testing.T) {
	forms := map[string]map[string]interface{}{
		"agent": config.UISchemaMap(),
		"relay": config.RelayUISchemaMap(),
	}
	shown := 0
	for _, sub := range subsystems(nil, subsystemDeps{}) {
		for form, doc := range forms {
			node := schemaNode(doc, sub.ConfigKey)
			if node == nil {
				continue
			}
			shown++
			if node[config.UISchemaSaveKey] != config.SaveConfirm {
				t.Errorf("%s form: %s does not wait for Save (%s=%v)", form, sub.ConfigKey, config.UISchemaSaveKey, node[config.UISchemaSaveKey])
			}
		}
	}
	// The agent's form shows the Telegram, Pachca and scheduler switches; a
	// walk that finds none has stopped looking at the forms.
	if shown < 3 {
		t.Fatalf("found %d subsystem switches in the settings forms, want at least 3", shown)
	}
}

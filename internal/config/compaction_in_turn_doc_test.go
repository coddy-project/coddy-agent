package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// descendant walks the properties of a JSON-schema-shaped document by name.
func descendant(t *testing.T, node map[string]interface{}, path ...string) map[string]interface{} {
	t.Helper()
	for _, name := range path {
		props, ok := node["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("no properties before %q", name)
		}
		next, ok := props[name].(map[string]interface{})
		if !ok {
			t.Fatalf("no property %q", name)
		}
		node = next
	}
	return node
}

// compaction.in_turn.enable does nothing while compaction.auto_enable is false:
// the fold and the recovery from a refused request both need the automatic
// trigger. Every description a person reads the key by says so (the web UI
// dictionaries, the Russian descriptions and the bundled skill carry the same
// sentence and are held by docs-check and by review).
func TestInTurnDescriptionsSayTheyNeedTheAutomaticTrigger(t *testing.T) {
	var schema map[string]interface{}
	if err := json.Unmarshal(ConfigSchemaJSON(), &schema); err != nil {
		t.Fatal(err)
	}
	ui := UISchemaMap()

	for name, root := range map[string]map[string]interface{}{"config.schema.json": schema, "UISchemaMap": ui} {
		section := descendant(t, root, "compaction", "in_turn")
		enable := descendant(t, section, "enable")
		for label, node := range map[string]map[string]interface{}{"compaction.in_turn": section, "compaction.in_turn.enable": enable} {
			desc, _ := node["description"].(string)
			if !strings.Contains(desc, "auto_enable") {
				t.Errorf("%s: the description of %s does not say it needs compaction.auto_enable: %q", name, label, desc)
			}
		}
	}
}

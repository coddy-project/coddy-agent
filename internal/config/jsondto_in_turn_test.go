package config_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The Settings form is drawn from UISchemaMap: compaction.in_turn is a section
// of its own with the switch and the step count, both plain controls (a bool and
// an integer with no list to confuse an absent key with an empty one).
func TestUISchemaCompactionOffersTheInTurnSection(t *testing.T) {
	doc := config.UISchemaMap()
	compaction := doc["properties"].(map[string]interface{})["compaction"].(map[string]interface{})
	props := compaction["properties"].(map[string]interface{})

	inTurn, ok := props["in_turn"].(map[string]interface{})
	if !ok || inTurn["type"] != "object" {
		t.Fatalf("compaction.in_turn = %#v, want an object section", props["in_turn"])
	}
	fields := inTurn["properties"].(map[string]interface{})
	if f, ok := fields["enable"].(map[string]interface{}); !ok || f["type"] != "boolean" {
		t.Fatalf("in_turn.enable = %#v, want a boolean", fields["enable"])
	}
	if f, ok := fields["keep_recent_steps"].(map[string]interface{}); !ok || f["type"] != "integer" {
		t.Fatalf("in_turn.keep_recent_steps = %#v, want an integer", fields["keep_recent_steps"])
	}

	order := func(node map[string]interface{}) []string {
		var got []string
		raw, _ := node["x-coddy-property-order"].([]interface{})
		for _, v := range raw {
			got = append(got, v.(string))
		}
		return got
	}
	if got, want := order(inTurn), []string{"enable", "keep_recent_steps"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("in_turn field order %v, want %v", got, want)
	}
	if got := order(compaction); !reflect.DeepEqual(got[len(got)-2:], []string{"in_turn", "result_eviction"}) {
		t.Fatalf("compaction field order %v: in_turn must sit before result_eviction", got)
	}
}

// compaction.in_turn rides the Settings round trip like every other key:
// pointer fields keep the difference between an absent key (the default) and an
// explicit value.
func TestInTurnKeysSurviveTheJSONDTO(t *testing.T) {
	roundTrip := func(t *testing.T, in config.InTurn) (string, config.InTurn) {
		t.Helper()
		cfg := &config.Config{}
		cfg.Compaction.InTurn = in
		raw, err := json.Marshal(config.ConfigToJSONDTO(cfg))
		if err != nil {
			t.Fatal(err)
		}
		var j config.ConfigJSON
		if err := json.Unmarshal(raw, &j); err != nil {
			t.Fatal(err)
		}
		return string(raw), config.JSONDTOToConfig(&j, config.Paths{}).Compaction.InTurn
	}

	t.Run("unset keys stay unset", func(t *testing.T) {
		raw, back := roundTrip(t, config.InTurn{})
		var doc struct {
			Compaction struct {
				InTurn map[string]json.RawMessage `json:"in_turn"`
			} `json:"compaction"`
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"enable", "keep_recent_steps"} {
			if _, ok := doc.Compaction.InTurn[key]; ok {
				t.Fatalf("an unset %s was written: %s", key, raw)
			}
		}
		if back.Enabled != nil || back.KeepRecentSteps != nil {
			t.Fatalf("unset keys came back set: %+v", back)
		}
	})

	t.Run("an explicit false and a count are kept", func(t *testing.T) {
		off := false
		steps := 6
		raw, back := roundTrip(t, config.InTurn{Enabled: &off, KeepRecentSteps: &steps})
		if !strings.Contains(raw, `"in_turn":{"enable":false,"keep_recent_steps":6}`) {
			t.Fatalf("GET DTO dropped the explicit values: %s", raw)
		}
		if back.Enabled == nil || *back.Enabled || back.KeepRecentSteps == nil || *back.KeepRecentSteps != 6 {
			t.Fatalf("PUT path lost the values: %+v", back)
		}
		// The copy must not alias the source.
		*back.KeepRecentSteps = 99
		if steps != 6 {
			t.Fatalf("the DTO conversion shares the count with its source: %d", steps)
		}
	})
}

// The same through the whole Settings save - GET document, PUT body, YAML on
// disk, reload.
func TestInTurnKeysSurviveASettingsSave(t *testing.T) {
	const head = `providers:
  - name: valera
    type: openai
    api_base: "http://127.0.0.1:9/v1"
    api_key: "k"
models:
  - model: "valera/m"
    max_tokens: 4096
agent:
  model: "valera/m"
compaction:
  threshold_percent: 70
`
	save := func(t *testing.T, extra string) (*config.Config, string) {
		t.Helper()
		p := writeConfig(t, head+extra)
		cur, err := config.Load(p)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(config.ConfigToJSONDTO(cur))
		if err != nil {
			t.Fatal(err)
		}
		reloaded := saveThroughSettings(t, p, body)
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return reloaded, string(raw)
	}

	t.Run("absent keys stay absent", func(t *testing.T) {
		cfg, raw := save(t, "")
		in := cfg.Compaction.InTurn
		if in.Enabled != nil || in.KeepRecentSteps != nil {
			t.Fatalf("a save invented the keys: %+v", in)
		}
		if strings.Contains(raw, "in_turn") || strings.Contains(raw, "keep_recent_steps") {
			t.Fatalf("saved YAML must omit the unset section:\n%s", raw)
		}
		if !in.IsEnabled() || in.EffectiveKeepRecentSteps() != 4 {
			t.Fatalf("the defaults were lost: %+v", in)
		}
	})

	t.Run("an explicit false and a count survive", func(t *testing.T) {
		cfg, raw := save(t, "  in_turn:\n    enable: false\n    keep_recent_steps: 2\n")
		in := cfg.Compaction.InTurn
		if in.IsEnabled() || in.EffectiveKeepRecentSteps() != 2 {
			t.Fatalf("the configured values were lost: %+v\n%s", in, raw)
		}
		if !strings.Contains(raw, "enable: false") || !strings.Contains(raw, "keep_recent_steps: 2") {
			t.Fatalf("saved YAML dropped the explicit values:\n%s", raw)
		}
	})
}

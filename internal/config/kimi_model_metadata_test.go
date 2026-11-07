package config

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const kimiModelYAML = "model: kimi/model\nkimi_metadata:\n  model: kimi/model\n  protocol: kimi\n  mode: subscription\n  region: global\n"

func TestKimiModelMetadataRoundTrip(t *testing.T) {
	var m ModelEntry
	if err := yaml.Unmarshal([]byte(kimiModelYAML), &m); err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "kimi_metadata:") {
		t.Fatalf("metadata lost: %s", out)
	}
	data, err := json.Marshal(ModelJSON(m))
	if err != nil {
		t.Fatal(err)
	}
	var j ModelJSON
	if err := json.Unmarshal(data, &j); err != nil {
		t.Fatal(err)
	}
	out, err = yaml.Marshal(ModelEntry(j))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "protocol: kimi") {
		t.Fatalf("JSON metadata lost: %s", out)
	}
}

func TestKimiModelMetadataResolution(t *testing.T) {
	for _, tc := range []struct {
		name, metadata, mode, region string
		wantErr                      bool
	}{
		{"valid", kimiModelYAML, "subscription", "global", false},
		{"missing", "model: kimi/model\n", "subscription", "global", false},
		{"model changed", strings.Replace(kimiModelYAML, "model: kimi/model\n", "model: kimi/other\n", 1), "subscription", "global", true},
		{"mode changed", kimiModelYAML, "platform", "global", true},
		{"region changed", kimiModelYAML, "subscription", "china", true},
		{"unknown protocol", strings.Replace(kimiModelYAML, "protocol: kimi", "protocol: unknown", 1), "subscription", "global", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m ModelEntry
			if err := yaml.Unmarshal([]byte(tc.metadata), &m); err != nil {
				t.Fatal(err)
			}
			c := &Config{Providers: []ProviderConfig{{Name: "kimi", Type: "kimi", Mode: tc.mode, Region: tc.region}}, Models: []ModelEntry{m}}
			_, err := c.ResolveLLM(m.Model)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ResolveLLM error=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

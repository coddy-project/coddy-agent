package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestKimiProviderContract(t *testing.T) {
	t.Setenv("KIMI_API_KEY", "")
	for _, tc := range []struct {
		name, fields string
		valid        bool
		reason       string
	}{
		{"default subscription", "", true, ""},
		{"mainland subscription", "region: china\n", true, ""},
		{"global platform", "mode: platform\nregion: global\napi_key: key\n", true, ""},
		{"china platform helper", "mode: platform\nregion: china\napi_key_command: echo-key\n", true, ""},
		{"invalid mode", "mode: other\n", false, "mode"},
		{"invalid region", "region: other\n", false, "region"},
		{"subscription key", "api_key: key\n", false, "api_key"},
		{"subscription command", "api_key_command: echo-key\n", false, "api_key_command"},
		{"subscription base", "api_base: https://example.com\n", false, "api_base"},
		{"platform missing key", "mode: platform\n", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p ProviderConfig
			if err := yaml.Unmarshal([]byte("name: kimi\ntype: kimi\n"+tc.fields), &p); err != nil {
				t.Fatal(err)
			}
			p.Normalize()
			err := p.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("Validate() = %v; valid = %v", err, tc.valid)
			}
			if !tc.valid && err != nil && !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("error %q does not name %q", err, tc.reason)
			}
		})
	}
}

func TestKimiSubscriptionDoesNotResolveEnvironmentKey(t *testing.T) {
	t.Setenv("KIMI_API_KEY", "secret")
	p := ProviderConfig{Name: "kimi", Type: "kimi"}
	if got := p.EffectiveAPIKey(); got != "" {
		t.Fatalf("subscription resolved key %q", got)
	}
}

func TestKimiFieldsRoundTripJSON(t *testing.T) {
	var p ProviderConfig
	if err := yaml.Unmarshal([]byte("name: kimi\ntype: kimi\nmode: platform\nregion: china\n"), &p); err != nil {
		t.Fatal(err)
	}
	b, err := yaml.Marshal(JSONDTOToConfig(ConfigToJSONDTO(&Config{Providers: []ProviderConfig{p}}), Paths{}).Providers[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"mode: platform", "region: china"} {
		if !strings.Contains(string(b), s) {
			t.Errorf("round trip missing %q: %s", s, b)
		}
	}
}

func TestKimiSchemaRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct{ field, value string }{{"mode", "invalid"}, {"region", "invalid"}, {"mode", "123"}} {
		t.Run(tc.field+tc.value, func(t *testing.T) {
			rep := checkYAML(t, withModeline("providers:\n  - name: kimi\n    type: kimi\n    "+tc.field+": "+tc.value+"\n"))
			errors := errorsOf(rep)
			if len(errors) == 0 || !strings.Contains(errors[0].Path, tc.field) {
				t.Fatalf("schema errors = %+v", errors)
			}
		})
	}
}

func TestKimiPlatformKeyResolution(t *testing.T) {
	t.Setenv("KIMI_API_KEY", "env-key")
	p := ProviderConfig{Name: "kimi", Type: "kimi", Mode: "platform", APIKey: "explicit-key"}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := p.EffectiveAPIKey(); got != "explicit-key" {
		t.Fatalf("key = %q", got)
	}
	p.APIKey = ""
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := p.EffectiveAPIKey(); got != "env-key" {
		t.Fatalf("key = %q", got)
	}
}

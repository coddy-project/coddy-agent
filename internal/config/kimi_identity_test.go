package config

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestKimiIdentityConfig(t *testing.T) {
	for _, tc := range []struct {
		identity string
		valid    bool
	}{
		{"", true}, {"0123456789abcdef0123456789abcdef", true}, {strings.Repeat("a", 64), true},
		{"kimi", false}, {strings.Repeat("A", 32), false}, {strings.Repeat("g", 32), false},
	} {
		var p ProviderConfig
		if err := yaml.Unmarshal([]byte("name: kimi\ntype: kimi\nkimi_identity: '"+tc.identity+"'\n"), &p); err != nil {
			t.Fatal(err)
		}
		if err := p.Validate(); (err == nil) != tc.valid {
			t.Errorf("identity %q: %v", tc.identity, err)
		}
		if tc.valid && tc.identity != "" {
			b, err := yaml.Marshal(JSONDTOToConfig(ConfigToJSONDTO(&Config{Providers: []ProviderConfig{p}}), Paths{}).Providers[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "kimi_identity: "+tc.identity) {
				t.Errorf("roundtrip lost identity: %s", b)
			}
		}
	}
}

func TestKimiIdentityDuplicate(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	var c Config
	if err := yaml.Unmarshal([]byte("providers:\n  - {name: one, type: kimi, kimi_identity: "+id+"}\n  - {name: two, type: kimi, kimi_identity: "+id+"}\n"), &c); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateModelsProvidersAndAgent(); err == nil {
		t.Fatal("duplicate identity accepted")
	}
}

func TestKimiIdentityLifecycle(t *testing.T) {
	home := t.TempDir()
	first, err := NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !ValidKimiIdentity(first) || !ValidKimiIdentity(second) {
		t.Fatal("identities must be distinct and valid")
	}
	old := ProviderConfig{Name: "kimi", Type: "kimi", KimiIdentity: first}
	renamed := old
	renamed.Name = "work"
	recreated := ProviderConfig{Name: "kimi", Type: "kimi", KimiIdentity: second}
	oldPath := ProviderConfigAuthPath(home, old)
	if oldPath == "" || ProviderConfigAuthPath(home, renamed) != oldPath {
		t.Fatal("rename changed identity")
	}
	if ProviderConfigAuthPath(home, recreated) == oldPath {
		t.Fatal("recreated row reused identity")
	}
	if ProviderConfigAuthPath(home, ProviderConfig{Name: "kimi", Type: "kimi"}) != "" {
		t.Fatal("row without id uses managed store")
	}
	cfg := &Config{Paths: Paths{Home: home}, Providers: []ProviderConfig{old}, Models: []ModelEntry{{Model: "kimi/model"}}}
	resolved, err := cfg.ResolveLLM("kimi/model")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.KimiIdentity != first || resolved.AuthPath != oldPath || resolved.NoCLILogin {
		t.Fatalf("resolved %#v", resolved)
	}
}

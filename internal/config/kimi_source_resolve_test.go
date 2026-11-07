package config

import "testing"

func TestKimiCLILoginRowIsolation(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{[]string{"work"}, "work"},
		{[]string{"work", "kimi"}, "kimi"},
		{[]string{"work", "other"}, ""},
	} {
		c := &Config{}
		for _, name := range tc.names {
			c.Providers = append(c.Providers, ProviderConfig{Name: name, Type: "kimi"})
			ref := name + "/model"
			c.Models = append(c.Models, ModelEntry{Model: ref, KimiMetadata: &KimiModelMetadata{Model: ref, Protocol: "kimi", Mode: "subscription", Region: "global"}})
		}
		if got := c.CLILoginRow("kimi", ""); got != tc.want {
			t.Errorf("%v: row %q want %q", tc.names, got, tc.want)
		}
		for _, name := range tc.names {
			r, err := c.ResolveLLM(name + "/model")
			if err != nil {
				t.Fatal(err)
			}
			if r.NoCLILogin != (name != tc.want) {
				t.Errorf("%s: NoCLILogin=%v", name, r.NoCLILogin)
			}
		}
	}
}

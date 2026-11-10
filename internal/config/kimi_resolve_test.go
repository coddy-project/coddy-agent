package config

import (
	"reflect"
	"testing"
)

func TestKimiResolveCarriesModeRegion(t *testing.T) {
	for _, tc := range []struct{ typ, mode, region string }{
		{"kimi", "", ""}, {"kimi", "subscription", "china"},
		{"kimi", "platform", "global"}, {"kimi", "platform", "china"},
		{"openai", "", ""},
	} {
		t.Run(tc.typ+"/"+tc.mode+"/"+tc.region, func(t *testing.T) {
			entry := ModelEntry{Model: "test/model"}
			if tc.typ == "kimi" {
				mode, region := tc.mode, tc.region
				if mode == "" {
					mode = "subscription"
				}
				if region == "" {
					region = "global"
				}
				entry.KimiMetadata = &KimiModelMetadata{Model: entry.Model, Protocol: "kimi", Mode: mode, Region: region}
			}
			cfg := &Config{Providers: []ProviderConfig{{Name: "test", Type: tc.typ, Mode: tc.mode, Region: tc.region}}, Models: []ModelEntry{entry}}
			got, err := cfg.ResolveLLM("test/model")
			if err != nil {
				t.Fatal(err)
			}
			v := reflect.ValueOf(got).Elem()
			for name, want := range map[string]string{"Mode": tc.mode, "Region": tc.region} {
				field := v.FieldByName(name)
				if !field.IsValid() {
					t.Errorf("ResolvedLLM lacks %s", name)
					continue
				}
				if field.String() != want {
					t.Errorf("%s = %q, want %q", name, field.String(), want)
				}
			}
		})
	}
}

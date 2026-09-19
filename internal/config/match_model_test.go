package config

import (
	"strings"
	"testing"
)

func TestMatchModelID(t *testing.T) {
	cfg := &Config{Models: []ModelEntry{
		{Model: "openai/gpt-4o"},
		{Model: "openai/gpt-4o-mini"},
		{Model: "hub/Qwen3-Coder"},
	}}
	cases := []struct {
		name    string
		want    string
		id      string
		errPart string
	}{
		{name: "exact", want: "openai/gpt-4o", id: "openai/gpt-4o"},
		{name: "exact wins over a longer id containing it", want: " openai/gpt-4o ", id: "openai/gpt-4o"},
		{name: "unique substring", want: "mini", id: "openai/gpt-4o-mini"},
		{name: "substring ignores case", want: "qwen", id: "hub/Qwen3-Coder"},
		{name: "ambiguous", want: "gpt", errPart: "ambiguous (matches: openai/gpt-4o, openai/gpt-4o-mini)"},
		{name: "unknown lists the configured models", want: "claude", errPart: "unknown model \"claude\" (configured: openai/gpt-4o, openai/gpt-4o-mini, hub/Qwen3-Coder)"},
		{name: "empty", want: "  ", errPart: "model is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cfg.MatchModelID(tc.want)
			if tc.errPart != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errPart) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.errPart)
				}
				return
			}
			if err != nil || got != tc.id {
				t.Fatalf("MatchModelID(%q) = %q, %v; want %q", tc.want, got, err, tc.id)
			}
		})
	}
}

// A name that is a whole id, or the whole model part of one, in another letter
// case or without its provider, names that model even when a longer id holds
// it: "qwen3.8-27b" next to "qwen3.8-27b-noreason" is not ambiguous.
func TestMatchModelIDPrefersAWholeName(t *testing.T) {
	cfg := &Config{Models: []ModelEntry{
		{Model: "openai/gpt-4o"},
		{Model: "openai/gpt-4o-mini"},
		{Model: "neuraldeep/qwen3.8-27b"},
		{Model: "neuraldeep/qwen3.8-27b-noreason"},
		{Model: "azure/gpt-4o-mini"},
	}}
	for _, tc := range []struct{ want, id string }{
		{want: "OPENAI/GPT-4O", id: "openai/gpt-4o"},
		{want: "qwen3.8-27b", id: "neuraldeep/qwen3.8-27b"},
		{want: "QWEN3.8-27B-NOREASON", id: "neuraldeep/qwen3.8-27b-noreason"},
		{want: "gpt-4o", id: "openai/gpt-4o"},
	} {
		got, err := cfg.MatchModelID(tc.want)
		if err != nil || got != tc.id {
			t.Errorf("MatchModelID(%q) = %q, %v; want %q", tc.want, got, err, tc.id)
		}
	}
	// The same model name under two providers still needs the provider.
	if _, err := cfg.MatchModelID("gpt-4o-mini"); err == nil || !strings.Contains(err.Error(), "ambiguous (matches: openai/gpt-4o-mini, azure/gpt-4o-mini)") {
		t.Errorf("gpt-4o-mini under two providers: err = %v", err)
	}
}

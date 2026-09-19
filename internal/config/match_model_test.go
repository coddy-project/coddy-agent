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

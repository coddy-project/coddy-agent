package config

import "testing"

func TestFirstModelID(t *testing.T) {
	cases := []struct {
		name   string
		models []ModelEntry
		want   string
	}{
		{name: "empty", models: nil, want: ""},
		{name: "single", models: []ModelEntry{{Model: "zed/m"}}, want: "zed/m"},
		{
			name: "alphabetical first of unordered ids",
			models: []ModelEntry{
				{Model: "zed/m"},
				{Model: "aaa/m"},
				{Model: "mid/m"},
			},
			want: "aaa/m",
		},
		{
			name: "config order does not decide",
			models: []ModelEntry{
				{Model: "neuraldeep/gpt-oss-120b"},
				{Model: "codex/gpt-5.5"},
			},
			want: "codex/gpt-5.5",
		},
		{
			name: "blank ids are skipped",
			models: []ModelEntry{
				{Model: "  "},
				{Model: "zed/m"},
			},
			want: "zed/m",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Models: tc.models}
			if got := cfg.FirstModelID(); got != tc.want {
				t.Fatalf("FirstModelID() = %q, want %q", got, tc.want)
			}
		})
	}
}

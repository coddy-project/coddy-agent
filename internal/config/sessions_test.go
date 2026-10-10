package config

import (
	"strings"
	"testing"
)

// sessions.min_free_mb is the free space below which Coddy warns that the
// volume holding the sessions is running out. Left out it is 512 MB; an
// explicit 0 is a choice, not a missing value, and turns the warning off.
func TestSessionsMinFreeBytes(t *testing.T) {
	two := 2
	zero := 0
	cases := []struct {
		name string
		in   *int
		want uint64
	}{
		{"unset takes the default", nil, uint64(SessionsDefaultMinFreeMB) << 20},
		{"explicit value", &two, 2 << 20},
		{"explicit zero turns the warning off", &zero, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Sessions{MinFreeMB: tc.in}
			if got := s.MinFreeBytes(); got != tc.want {
				t.Fatalf("MinFreeBytes() = %d, want %d", got, tc.want)
			}
		})
	}
	if SessionsDefaultMinFreeMB != 512 {
		t.Fatalf("the documented default is 512 MB, the code says %d", SessionsDefaultMinFreeMB)
	}
}

func TestSessionsMinFreeMustNotBeNegative(t *testing.T) {
	neg := -1
	s := Sessions{MinFreeMB: &neg}
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "sessions.min_free_mb") {
		t.Fatalf("Validate() = %v, want an error naming sessions.min_free_mb", err)
	}
}

// parseSessionsYAML reads a config document the way the loader does, minus
// the file system.
func parseSessionsYAML(t *testing.T, doc string) *Config {
	t.Helper()
	cfg, err := parseValidateYAMLBytes(doc, Paths{Home: t.TempDir()})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cfg
}

// The key survives a read of config.yaml and the JSON document the settings
// form sends back, with "unset" and "0" kept apart.
func TestSessionsMinFreeRoundTripsThroughYAMLAndJSON(t *testing.T) {
	cfg := parseSessionsYAML(t, "sessions:\n  min_free_mb: 0\n")
	if cfg.Sessions.MinFreeMB == nil || *cfg.Sessions.MinFreeMB != 0 {
		t.Fatalf("min_free_mb: 0 read as %v", cfg.Sessions.MinFreeMB)
	}
	if got := cfg.Sessions.MinFreeBytes(); got != 0 {
		t.Fatalf("MinFreeBytes() = %d, want 0 (warning off)", got)
	}
	back := JSONDTOToConfig(ConfigToJSONDTO(cfg), cfg.Paths)
	if back.Sessions.MinFreeMB == nil || *back.Sessions.MinFreeMB != 0 {
		t.Fatalf("JSON round trip lost the explicit 0: %v", back.Sessions.MinFreeMB)
	}

	unset := parseSessionsYAML(t, "sessions:\n  dir: \"\"\n")
	if unset.Sessions.MinFreeMB != nil {
		t.Fatalf("an unset min_free_mb read as %d", *unset.Sessions.MinFreeMB)
	}
	if got, want := unset.Sessions.MinFreeBytes(), uint64(SessionsDefaultMinFreeMB)<<20; got != want {
		t.Fatalf("MinFreeBytes() = %d, want the default %d", got, want)
	}
}

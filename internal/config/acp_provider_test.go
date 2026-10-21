package config

import (
	"path/filepath"
	"testing"
)

// The settings form seeds a new row from the schema defaults, acp block
// included; only a row of type acp keeps it in the configuration.
func TestJSONDTOKeepsTheACPBlockOnACPRowsOnly(t *testing.T) {
	block := &ACPProviderConfig{Command: "", Permissions: ACPPermissionsAsk, Process: ACPProcessShared}
	cfg := JSONDTOToConfig(&ConfigJSON{Providers: []ProviderJSON{
		{Name: "openai", Type: "openai", ACP: block},
		{Name: "claude", Type: "acp", ACP: &ACPProviderConfig{Command: "claude-agent-acp"}},
	}}, Paths{})
	if cfg.Providers[0].ACP != nil {
		t.Fatalf("an openai row kept an acp block: %+v", cfg.Providers[0].ACP)
	}
	if cfg.Providers[1].ACP == nil || cfg.Providers[1].ACP.Command != "claude-agent-acp" {
		t.Fatalf("the acp row lost its block: %+v", cfg.Providers[1].ACP)
	}
}

func TestACPProviderValidation(t *testing.T) {
	// Absolute on the host: "/srv/work" has no drive letter on Windows.
	abs := filepath.Join(t.TempDir(), "work")
	cases := map[string]struct {
		block   *ACPProviderConfig
		wantErr string
	}{
		"no block":            {nil, "acp.command"},
		"no command":          {&ACPProviderConfig{}, "acp.command"},
		"bad permissions":     {&ACPProviderConfig{Command: "x", Permissions: "maybe"}, "acp.permissions"},
		"bad process":         {&ACPProviderConfig{Command: "x", Process: "pooled"}, "acp.process"},
		"relative cwd":        {&ACPProviderConfig{Command: "x", CWD: "work"}, "acp.cwd"},
		"negative idle":       {&ACPProviderConfig{Command: "x", IdleTimeoutMS: -1}, "idle_timeout_ms"},
		"absolute cwd":        {&ACPProviderConfig{Command: "x", CWD: abs}, ""},
		"home cwd":            {&ACPProviderConfig{Command: "x", CWD: "~/work"}, ""},
		"workspace cwd":       {&ACPProviderConfig{Command: "x", CWD: "${CWD}/sub"}, ""},
		"per session process": {&ACPProviderConfig{Command: "x", Process: "per_session", Permissions: "deny"}, ""},
	}
	for name, c := range cases {
		p := ProviderConfig{Name: "agent", Type: "acp", ACP: c.block}
		p.Normalize()
		err := p.Validate()
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.wantErr != "" && (err == nil || !containsFold(err.Error(), c.wantErr)):
			t.Errorf("%s: err = %v, want one naming %s", name, err, c.wantErr)
		}
	}
	if (&ProviderConfig{Type: "acp"}).TakesAPIKey() {
		t.Error("an acp row reads no api_key")
	}
}

func containsFold(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (stringIndexFold(s, sub) >= 0))
}

func stringIndexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

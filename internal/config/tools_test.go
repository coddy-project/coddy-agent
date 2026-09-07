package config

import "testing"

func TestToolOutputLimitsDefaults(t *testing.T) {
	var l ToolOutputLimits
	cases := map[string]int{
		"read":            OutputLimitDefaultRead,
		"grep":            OutputLimitDefaultGrep,
		"glob":            OutputLimitDefaultGlob,
		"print_tree":      OutputLimitDefaultPrintTree,
		"run_command":     OutputLimitDefaultRunCommand,
		"ssh_run_command": OutputLimitDefaultSSHRunCommand,
		"webfetch":        OutputLimitDefaultWebFetch,
		"websearch":       OutputLimitDefaultWebSearch,
		"anything_else":   OutputLimitDefaultDefault,
		"":                OutputLimitDefaultDefault,
	}
	for tool, want := range cases {
		if got := l.MaxLines(tool); got != want {
			t.Errorf("MaxLines(%q) = %d, want %d", tool, got, want)
		}
	}
}

func TestToolOutputLimitsExplicitOverrides(t *testing.T) {
	zero := 0
	fifty := 50
	l := ToolOutputLimits{Read: &fifty, Grep: &zero}
	if got := l.MaxLines("read"); got != 50 {
		t.Fatalf("read = %d, want 50", got)
	}
	if got := l.MaxLines("grep"); got != 0 {
		t.Fatalf("grep = %d, want 0 (explicit unlimited)", got)
	}
	// Unset field still falls back to its default.
	if got := l.MaxLines("glob"); got != OutputLimitDefaultGlob {
		t.Fatalf("glob = %d, want default %d", got, OutputLimitDefaultGlob)
	}
}

func TestToolOutputLimitsAsMapHasDefaultKey(t *testing.T) {
	l := ToolOutputLimits{}
	m := l.AsMap()
	if _, ok := m[""]; !ok {
		t.Fatal("AsMap missing default key")
	}
	if m["read"] != OutputLimitDefaultRead {
		t.Fatalf("AsMap read = %d, want %d", m["read"], OutputLimitDefaultRead)
	}
	if m[""] != OutputLimitDefaultDefault {
		t.Fatalf("AsMap default = %d, want %d", m[""], OutputLimitDefaultDefault)
	}
}

func TestToolsValidateRejectsNegativeOutputLimit(t *testing.T) {
	neg := -1
	c := Tools{OutputLimits: ToolOutputLimits{Read: &neg}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for negative read limit")
	}
}

func TestToolsValidateAcceptsZeroOutputLimit(t *testing.T) {
	zero := 0
	c := Tools{OutputLimits: ToolOutputLimits{Grep: &zero}}
	if err := c.Validate(); err != nil {
		t.Fatalf("zero limit should be valid: %v", err)
	}
}

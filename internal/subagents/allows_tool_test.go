package subagents

import "testing"

// AllowsTool is the rule a subagent definition and a models[] row share: the
// denylist wins, an empty allowlist admits everything, a non-empty one only
// what it matches, and patterns are exact names, a bare * or a prefix*.
func TestAllowsToolSharedRule(t *testing.T) {
	cases := []struct {
		name        string
		allow, deny []string
		tool        string
		want        bool
	}{
		{"no lists admits everything", nil, nil, "write", true},
		{"empty lists admit everything", []string{}, []string{}, "write", true},
		{"allowlist admits a listed name", []string{"read", "grep"}, nil, "grep", true},
		{"allowlist refuses an unlisted name", []string{"read", "grep"}, nil, "write", false},
		{"denylist alone removes a name", nil, []string{"write"}, "write", false},
		{"denylist alone keeps the rest", nil, []string{"write"}, "read", true},
		{"denylist wins over the allowlist", []string{"read", "write"}, []string{"write"}, "write", false},
		{"allowlist prefix pattern", []string{"context7__*"}, nil, "context7__search", true},
		{"allowlist prefix pattern leaves other servers", []string{"context7__*"}, nil, "other__search", false},
		{"bare star allows all, deny still removes", []string{"*"}, []string{"run_command"}, "run_command", false},
		{"deny star removes everything", nil, []string{"*"}, "read", false},
		{"blank pattern matches nothing", []string{" "}, nil, "read", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AllowsTool(tc.allow, tc.deny, tc.tool); got != tc.want {
				t.Fatalf("AllowsTool(%v, %v, %q) = %v, want %v", tc.allow, tc.deny, tc.tool, got, tc.want)
			}
		})
	}
}

// A definition reads its lists through the same rule.
func TestDefinitionAllowsUsesTheSharedRule(t *testing.T) {
	var none *Definition
	if !none.Allows("anything") {
		t.Fatal("a nil definition must admit every tool")
	}
	d := &Definition{Tools: []string{"read", "write"}, DisallowedTools: []string{"write"}}
	if !d.Allows("read") || d.Allows("write") || d.Allows("grep") {
		t.Fatalf("definition lists read as %v/%v/%v, want true/false/false", d.Allows("read"), d.Allows("write"), d.Allows("grep"))
	}
}

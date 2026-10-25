package rules

import (
	"strings"
	"testing"
	"unsafe"
)

// The same rule file read again, as every session of a workspace reads it,
// gives an equal rule of its own whose text shares the first parse's memory;
// changed bytes are parsed again.
func TestParseRuleFileSharesTheParseOfUnchangedBytes(t *testing.T) {
	data := []byte("---\ndescription: Go style\nglobs: \"**/*.go\"\n---\n" + strings.Repeat("Use gofmt.\n", 2000))
	a, err := ParseRuleFile("/w/.coddy/rules/go.md", Source("coddy"), data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseRuleFile("/w/.coddy/rules/go.md", Source("coddy"), append([]byte(nil), data...))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("both reads returned the same *Rule: a caller's edits would reach every session")
	}
	if a.Content != b.Content || a.Description != b.Description || len(a.Globs) != 1 || b.Globs[0] != a.Globs[0] {
		t.Fatalf("the second read differs: %+v vs %+v", a, b)
	}
	if unsafe.StringData(a.Content) != unsafe.StringData(b.Content) {
		t.Fatal("the second read holds a copy of the rule text")
	}

	b.Root = "/elsewhere"
	b.Globs[0] = "changed"
	c, _ := ParseRuleFile("/w/.coddy/rules/go.md", Source("coddy"), data)
	if c.Root != "" || c.Globs[0] != "**/*.go" {
		t.Fatalf("a caller's edit reached the cache: root %q, globs %v", c.Root, c.Globs)
	}

	d, _ := ParseRuleFile("/w/.coddy/rules/go.md", Source("coddy"), []byte("Always run tests.\n"))
	if d.Content != "Always run tests." {
		t.Fatalf("changed bytes were not parsed again: %q", d.Content)
	}
	e, _ := ParseRuleFile("/w/.cursor/rules/go.md", Source("cursor"), data)
	if e.ID == a.ID || e.Source == a.Source {
		t.Fatalf("another path or source shared the first parse: %q", e.ID)
	}
}

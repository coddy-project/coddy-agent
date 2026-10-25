package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"
)

// Every session loads the skills of its search roots; an unchanged SKILL.md
// read again gives a skill of its own whose text shares the first parse, and
// an edited one is parsed again.
func TestLoadFileSharesTheParseOfUnchangedBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: review\ndescription: Review a change\n---\n" + strings.Repeat("Read the diff first.\n", 2000)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("both loads returned the same *Skill")
	}
	if *a != *b {
		t.Fatalf("the second load differs: %+v vs %+v", a, b)
	}
	if unsafe.StringData(a.Content) != unsafe.StringData(b.Content) {
		t.Fatal("the second load holds a copy of the skill text")
	}
	b.Description = "edited by a caller"
	if c, _ := loadFile(path); c.Description != "Review a change" {
		t.Fatalf("a caller's edit reached the cache: %q", c.Description)
	}
	if err := os.WriteFile(path, []byte("---\nname: review\ndescription: New\n---\nShort.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d, _ := loadFile(path); d.Description != "New" || d.Content != "Short." {
		t.Fatalf("an edited file was not parsed again: %+v", d)
	}
}

package rules

import (
	"fmt"
	"path/filepath"
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

func TestParseRuleFileConcurrentColdLoadsShareText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cold.mdc")
	data := []byte("---\nglobs: '**/*.go'\n---\n" + strings.Repeat("Use gofmt.\n", 20000))
	const readers = 16
	start := make(chan struct{})
	out := make(chan *Rule, readers)
	for range readers {
		go func() {
			<-start
			r, err := ParseRuleFile(path, SourceCoddy, data)
			if err != nil {
				t.Error(err)
			}
			out <- r
		}()
	}
	close(start)
	first := <-out
	for range readers - 1 {
		r := <-out
		if first == nil || r == nil {
			t.Fatal("parse failed")
		}
		if unsafe.StringData(r.Content) != unsafe.StringData(first.Content) {
			t.Error("concurrent loads retained separate text allocations")
		}
	}
}

func TestParseRuleFileSeparatesSourcesAtSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "style.mdc")
	for _, source := range []Source{SourceCoddy, SourceCursor, SourceCoddy} {
		r, err := ParseRuleFile(path, source, []byte("Use gofmt."))
		if err != nil {
			t.Fatal(err)
		}
		if r.Source != source || r.ID != string(source)+":"+path {
			t.Fatalf("wrong source or identity: %+v", r)
		}
	}
}

func TestParseRuleFileCacheIsBounded(t *testing.T) {
	root := t.TempDir()
	data := []byte(strings.Repeat("x", 1<<20))
	for i := range 20 {
		if _, err := ParseRuleFile(filepath.Join(root, fmt.Sprintf("%d.md", i)), SourceCoddy, data); err != nil {
			t.Fatal(err)
		}
	}
	parseCache.mu.Lock()
	defer parseCache.mu.Unlock()
	var bytes int
	for _, entry := range parseCache.byFile {
		bytes += len(entry.rule.Content)
	}
	if bytes > 16<<20 {
		t.Fatalf("cache retains %d bytes of rule bodies, want at most 16 MiB", bytes)
	}
}

func TestParseRuleFileCacheBoundsEmptyEntries(t *testing.T) {
	root := t.TempDir()
	for i := range 1100 {
		if _, err := ParseRuleFile(filepath.Join(root, fmt.Sprintf("%d.md", i)), SourceCoddy, nil); err != nil {
			t.Fatal(err)
		}
	}
	parseCache.mu.Lock()
	defer parseCache.mu.Unlock()
	if len(parseCache.byFile) > 1024 {
		t.Fatalf("cache holds %d entries, want at most 1024", len(parseCache.byFile))
	}
	var bytes int
	for _, entry := range parseCache.byFile {
		bytes += entry.size
	}
	if parseCache.bytes != bytes {
		t.Fatalf("cache accounts for %d input bytes, actually holds %d", parseCache.bytes, bytes)
	}
}

func TestParseRuleFileDoesNotCacheOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.md")
	data := []byte(strings.Repeat("x", (16<<20)+1))
	r, err := ParseRuleFile(path, SourceCoddy, data)
	if err != nil {
		t.Fatal(err)
	}
	if r.Content != string(data) {
		t.Fatal("oversized input was not parsed in full")
	}
	parseCache.mu.Lock()
	defer parseCache.mu.Unlock()
	if _, ok := parseCache.byFile[parseKey{path: path, src: SourceCoddy}]; ok {
		t.Fatal("oversized input was retained in cache")
	}
}

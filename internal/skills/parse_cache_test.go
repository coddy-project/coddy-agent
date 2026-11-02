package skills

import (
	"fmt"
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

func TestLoadFileConcurrentColdLoadsShareText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	data := []byte("---\nname: cold\n---\n" + strings.Repeat("Read the diff.\n", 20000))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	const readers = 16
	start := make(chan struct{})
	out := make(chan *Skill, readers)
	for range readers {
		go func() {
			<-start
			s, err := loadFile(path)
			if err != nil {
				t.Error(err)
			}
			out <- s
		}()
	}
	close(start)
	first := <-out
	for range readers - 1 {
		s := <-out
		if first == nil || s == nil {
			t.Fatal("load failed")
		}
		if unsafe.StringData(s.Content) != unsafe.StringData(first.Content) {
			t.Error("concurrent loads retained separate text allocations")
		}
	}
}

func TestLoadFileCacheIsBounded(t *testing.T) {
	root := t.TempDir()
	data := []byte(strings.Repeat("x", 1<<20))
	for i := range 20 {
		path := filepath.Join(root, fmt.Sprintf("%d.md", i))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	parseCache.mu.Lock()
	defer parseCache.mu.Unlock()
	var bytes int
	for _, entry := range parseCache.byPath {
		bytes += len(entry.skill.Content)
	}
	if bytes > 16<<20 {
		t.Fatalf("cache retains %d bytes of skill bodies, want at most 16 MiB", bytes)
	}
}

func TestLoadFileDoesNotServeDeletedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte("Read the diff."), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadFile(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadFile(path); !os.IsNotExist(err) {
		t.Fatalf("deleted file answered from cache: %v", err)
	}
}

func TestLoadFileCacheBoundsEmptyEntries(t *testing.T) {
	root := t.TempDir()
	for i := range 1100 {
		path := filepath.Join(root, fmt.Sprintf("%d.md", i))
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	parseCache.mu.Lock()
	defer parseCache.mu.Unlock()
	if len(parseCache.byPath) > 1024 {
		t.Fatalf("cache holds %d entries, want at most 1024", len(parseCache.byPath))
	}
	var bytes int
	for _, entry := range parseCache.byPath {
		bytes += entry.size
	}
	if parseCache.bytes != bytes {
		t.Fatalf("cache accounts for %d input bytes, actually holds %d", parseCache.bytes, bytes)
	}
}

func TestLoadFileDoesNotCacheOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	data := []byte(strings.Repeat("x", (16<<20)+1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Content != string(data) {
		t.Fatal("oversized input was not loaded in full")
	}
	parseCache.mu.Lock()
	defer parseCache.mu.Unlock()
	if _, ok := parseCache.byPath[path]; ok {
		t.Fatal("oversized input was retained in cache")
	}
}

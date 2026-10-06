//go:build memory

package memtools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func saveNote(t *testing.T, mem *config.MemoryConfig, body string) (string, string) {
	t.Helper()
	st, defaults := testMemoryStore(t)
	if mem == nil {
		mem = defaults
	}
	args, err := json.Marshal(map[string]string{
		"title":         "note",
		"body":          body,
		"scope":         "project",
		"relative_path": "notes/note.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := memorySaveTool(st, mem).Execute(context.Background(), string(args), nil)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(st.ProjectRoot(), "notes", "note.md"))
	if err != nil {
		t.Fatalf("read saved note: %v", err)
	}
	return out, string(data)
}

func TestMemorySaveKeepsBodyWithinLimit(t *testing.T) {
	// 900 two-byte Cyrillic characters: 1800 bytes, but within the character limit.
	body := strings.Repeat("ж", config.MemoryDefaultMaxNoteChars)
	out, got := saveNote(t, nil, body)
	if strings.Contains(out, "truncated") {
		t.Fatalf("unexpected truncation warning: %q", out)
	}
	if got != body+"\n" {
		t.Fatalf("body changed: got %d runes, want %d", utf8.RuneCountInString(got), config.MemoryDefaultMaxNoteChars+1)
	}
}

func TestMemorySaveTruncatesByRunesAndWarns(t *testing.T) {
	// The cut falls right where a byte-based slice would split a character.
	body := "a" + strings.Repeat("ж", config.MemoryDefaultMaxNoteChars+50)
	out, got := saveNote(t, nil, body)
	if !strings.Contains(out, "truncated to 900 characters") {
		t.Fatalf("expected truncation warning, got %q", out)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("saved note is not valid UTF-8")
	}
	if !strings.HasSuffix(got, "\n...\n") {
		t.Fatalf("missing truncation marker: %q", got[len(got)-10:])
	}
	kept := strings.TrimSuffix(got, "\n...\n")
	if n := utf8.RuneCountInString(kept); n != config.MemoryDefaultMaxNoteChars {
		t.Fatalf("kept %d runes, want %d", n, config.MemoryDefaultMaxNoteChars)
	}
}

// memory.max_note_chars moves the cap, and an explicit 0 removes it; the
// schema of the body argument says which.
func TestMemorySaveFollowsConfiguredCap(t *testing.T) {
	small := 10
	mem := &config.MemoryConfig{MaxNoteChars: &small}
	out, got := saveNote(t, mem, strings.Repeat("ж", 25))
	if !strings.Contains(out, "truncated to 10 characters") || strings.TrimSuffix(got, "\n...\n") != strings.Repeat("ж", 10) {
		t.Fatalf("cap of 10: out=%q note=%q", out, got)
	}
	st, _ := testMemoryStore(t)
	if d := bodyDescription(memorySaveTool(st, mem)); !strings.Contains(d, "at most 10 characters") {
		t.Fatalf("schema does not state the cap: %q", d)
	}

	zero := 0
	mem = &config.MemoryConfig{MaxNoteChars: &zero}
	long := strings.Repeat("ж", 5000)
	out, got = saveNote(t, mem, long)
	if strings.Contains(out, "truncated") || got != long+"\n" {
		t.Fatalf("no cap: out=%q, kept %d runes", out, utf8.RuneCountInString(got))
	}
	if d := bodyDescription(memorySaveTool(st, mem)); strings.Contains(d, "at most") {
		t.Fatalf("schema states a cap that is off: %q", d)
	}
}

// bodyDescription reads the description of the save tool's body argument.
func bodyDescription(tool *tooling.Tool) string {
	schema := tool.Definition.InputSchema.(map[string]interface{})
	body := schema["properties"].(map[string]interface{})["body"].(map[string]interface{})
	return body["description"].(string)
}

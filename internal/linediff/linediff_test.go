package linediff

import (
	"strings"
	"testing"
)

func TestStat(t *testing.T) {
	tests := []struct {
		name           string
		before, after  string
		added, deleted int
	}{
		{"identical", "a\nb\nc\n", "a\nb\nc\n", 0, 0},
		{"empty both", "", "", 0, 0},
		{"created", "", "a\nb\n", 2, 0},
		{"deleted", "a\nb\n", "", 0, 2},
		{"one line changed", "a\nb\nc\n", "a\nB\nc\n", 1, 1},
		{"appended", "a\n", "a\nb\nc\n", 2, 0},
		{"removed middle", "a\nb\nc\n", "a\nc\n", 0, 1},
		{"no trailing newline", "a\nb", "a\nB", 1, 1},
		{"crlf untouched line", "a\r\nb\r\n", "a\r\nB\r\n", 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			added, deleted := Stat(tc.before, tc.after)
			if added != tc.added || deleted != tc.deleted {
				t.Fatalf("Stat = (+%d -%d), want (+%d -%d)", added, deleted, tc.added, tc.deleted)
			}
		})
	}
}

// A CRLF file edited on one line must not report every line as changed: the
// carriage return belongs to the line content, not to the separator.
func TestStatCRLFDoesNotDirtyEveryLine(t *testing.T) {
	before := "one\r\ntwo\r\nthree\r\nfour\r\n"
	after := "one\r\nTWO\r\nthree\r\nfour\r\n"
	added, deleted := Stat(before, after)
	if added != 1 || deleted != 1 {
		t.Fatalf("Stat = (+%d -%d), want (+1 -1)", added, deleted)
	}
}

func TestUnifiedHeaderAndHunk(t *testing.T) {
	before := "a\nb\nc\n"
	after := "a\nB\nc\n"
	patch, truncated := Unified("src/x.go", before, after, DefaultContext)
	if truncated {
		t.Fatal("small patch reported as truncated")
	}
	if !strings.HasPrefix(patch, "--- a/src/x.go\n+++ b/src/x.go\n") {
		t.Fatalf("missing file headers:\n%s", patch)
	}
	if !strings.Contains(patch, "@@ -1,3 +1,3 @@\n") {
		t.Fatalf("missing hunk header:\n%s", patch)
	}
	for _, want := range []string{" a\n", "-b\n", "+B\n", " c\n"} {
		if !strings.Contains(patch, want) {
			t.Fatalf("patch missing %q:\n%s", want, patch)
		}
	}
}

// Context lines that are empty must still carry the leading space, otherwise
// the SPA's unified parser drops them.
func TestUnifiedEmptyContextLineKeepsPrefix(t *testing.T) {
	patch, _ := Unified("f.txt", "a\n\nb\n", "a\n\nB\n", DefaultContext)
	if !strings.Contains(patch, "\n \n") {
		t.Fatalf("empty context line lost its space prefix:\n%q", patch)
	}
}

func TestUnifiedAddedFile(t *testing.T) {
	patch, _ := Unified("new.txt", "", "x\ny\n", DefaultContext)
	if !strings.Contains(patch, "@@ -0,0 +1,2 @@\n") {
		t.Fatalf("added-file hunk header wrong:\n%s", patch)
	}
	if strings.Contains(patch, "-") && !strings.Contains(patch, "--- a/new.txt") {
		t.Fatalf("unexpected deletions:\n%s", patch)
	}
}

func TestUnifiedDeletedFile(t *testing.T) {
	patch, _ := Unified("gone.txt", "x\ny\n", "", DefaultContext)
	if !strings.Contains(patch, "@@ -1,2 +0,0 @@\n") {
		t.Fatalf("deleted-file hunk header wrong:\n%s", patch)
	}
}

func TestUnifiedIdenticalIsEmpty(t *testing.T) {
	patch, _ := Unified("same.txt", "a\n", "a\n", DefaultContext)
	if patch != "" {
		t.Fatalf("identical content produced a patch:\n%s", patch)
	}
}

// Two far-apart edits belong to separate hunks, not one hunk swallowing the
// whole file.
func TestUnifiedSplitsDistantHunks(t *testing.T) {
	var b, a []string
	for i := 0; i < 40; i++ {
		b = append(b, "line")
		a = append(a, "line")
	}
	b[2], a[2] = "old-top", "new-top"
	b[35], a[35] = "old-bottom", "new-bottom"
	patch, _ := Unified("f.txt", strings.Join(b, "\n")+"\n", strings.Join(a, "\n")+"\n", DefaultContext)
	if got := strings.Count(patch, "@@ -"); got != 2 {
		t.Fatalf("want 2 hunks, got %d:\n%s", got, patch)
	}
}

// Beyond the size guard the patch is cut short but the stat still counts the
// whole file, so the card never lies about the totals.
func TestUnifiedTruncatesHugePatch(t *testing.T) {
	before := strings.Repeat("old line\n", 60000)
	after := strings.Repeat("new line\n", 60000)
	patch, truncated := Unified("big.txt", before, after, DefaultContext)
	if !truncated {
		t.Fatal("huge patch not reported as truncated")
	}
	if len(patch) > maxPatchBytes+4096 {
		t.Fatalf("truncated patch still %d bytes", len(patch))
	}
	added, deleted := Stat(before, after)
	if added != 60000 || deleted != 60000 {
		t.Fatalf("Stat = (+%d -%d), want (+60000 -60000)", added, deleted)
	}
}

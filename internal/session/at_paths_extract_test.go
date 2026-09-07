package session_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestExtractAtFilePathsFromTextSkipsProseAndFolders(t *testing.T) {
	got := session.ExtractAtFilePathsFromText("see @a/b.txt and @a/ and @a/b.txt")
	if len(got) != 1 || got[0] != "a/b.txt" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractAtFilePathsFromTextSpaceInName(t *testing.T) {
	got := session.ExtractAtFilePathsFromText("open @readme copy.md now")
	if len(got) != 1 || got[0] != "readme copy.md" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractAtFilePathsFromTextSkipsCodeFence(t *testing.T) {
	s := "```\n@x.go\n```"
	got := session.ExtractAtFilePathsFromText(s)
	if len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}

// --- ":N-M" line-range suffix ---
// The literals below are shared with external/ui/src/ui/skills/draftAt.test.ts so
// both twins of the grammar stay in step.

func refsEqual(t *testing.T, got []session.AtFileRef, want []session.AtFileRef) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}

func TestExtractAtFileRefsAbsorbsLineRange(t *testing.T) {
	refsEqual(t, session.ExtractAtFileRefsFromText("see @Dockerfile:21-31 ok"),
		[]session.AtFileRef{{Path: "Dockerfile", StartLine: 21, EndLine: 31}})
	refsEqual(t, session.ExtractAtFileRefsFromText("@a/b.go:5-5"),
		[]session.AtFileRef{{Path: "a/b.go", StartLine: 5, EndLine: 5}})
}

func TestExtractAtFileRefsSingleNumberIsNotARange(t *testing.T) {
	refsEqual(t, session.ExtractAtFileRefsFromText("open @x.go:21 now"),
		[]session.AtFileRef{{Path: "x.go"}})
}

func TestExtractAtFileRefsTrailingGarbageIsNotARange(t *testing.T) {
	refsEqual(t, session.ExtractAtFileRefsFromText("see @file.go:21-31x here"),
		[]session.AtFileRef{{Path: "file.go"}})
}

func TestExtractAtFileRefsRejectsInvalidRanges(t *testing.T) {
	refsEqual(t, session.ExtractAtFileRefsFromText("@f.go:31-21 x"),
		[]session.AtFileRef{{Path: "f.go"}})
	refsEqual(t, session.ExtractAtFileRefsFromText("@f.go:0-5 x"),
		[]session.AtFileRef{{Path: "f.go"}})
	refsEqual(t, session.ExtractAtFileRefsFromText("@f.go:1234567890-1234567891 x"),
		[]session.AtFileRef{{Path: "f.go"}})
}

func TestExtractAtFileRefsAtBoundaries(t *testing.T) {
	refsEqual(t, session.ExtractAtFileRefsFromText("take @f.go:2-4\r\nplease"),
		[]session.AtFileRef{{Path: "f.go", StartLine: 2, EndLine: 4}})
	refsEqual(t, session.ExtractAtFileRefsFromText("check @f.go:2-4, then run"),
		[]session.AtFileRef{{Path: "f.go", StartLine: 2, EndLine: 4}})
	refsEqual(t, session.ExtractAtFileRefsFromText("@f.go:2-4"),
		[]session.AtFileRef{{Path: "f.go", StartLine: 2, EndLine: 4}})
}

// A padded token had its trailing space trimmed, so the suffix that follows
// belongs to the prose, not to the path.
func TestExtractAtFileRefsIgnoresRangeAfterSpace(t *testing.T) {
	refsEqual(t, session.ExtractAtFileRefsFromText("look @notes.md :2-4 here"),
		[]session.AtFileRef{{Path: "notes.md"}})
}

func TestExtractAtFileRefsDedupesByPathAndRange(t *testing.T) {
	refsEqual(t, session.ExtractAtFileRefsFromText("@f.go:1-2 @f.go:1-2 @f.go:3-4 @f.go"),
		[]session.AtFileRef{
			{Path: "f.go", StartLine: 1, EndLine: 2},
			{Path: "f.go", StartLine: 3, EndLine: 4},
			{Path: "f.go"},
		})
}

// ExtractAtFilePathsFromText keeps its old contract for callers that do not care
// about ranges (internal/session/hydrate_plans.go): one entry per path.
func TestExtractAtFilePathsCollapsesRanges(t *testing.T) {
	got := session.ExtractAtFilePathsFromText("@f.go:1-2 and @f.go:3-4 and @g.go")
	if len(got) != 2 || got[0] != "f.go" || got[1] != "g.go" {
		t.Fatalf("got %q", got)
	}
}

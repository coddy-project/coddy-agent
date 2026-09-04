package session_test

// Boundary cases for the ":N-M" line-range suffix on @mentions. The literals here
// are shared with external/ui/src/ui/skills/draftAt.test.ts so both twins of the
// grammar stay in step.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

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

// --- hydration with line ranges ---

func writeRangeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	body := "one\ntwo\nthree\nfour\nfive\n"
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func soleResource(t *testing.T, blocks []acp.ContentBlock) *acp.Resource {
	t.Helper()
	var found []*acp.Resource
	for _, b := range blocks {
		if b.Type == "resource" && b.Resource != nil {
			found = append(found, b.Resource)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected one resource block, got %d in %+v", len(found), blocks)
	}
	return found[0]
}

func TestBuildHydratedComposerPromptLineRange(t *testing.T) {
	root := writeRangeFixture(t)
	blocks, err := session.BuildHydratedComposerPrompt(root, "see @f.txt:2-3", []session.PromptFileAttachment{
		{Path: "f.txt", Source: &session.PromptFileAttachmentSourceField{StartLine: 2, EndLine: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := soleResource(t, blocks)
	if res.URI != "f.txt#L2-3" {
		t.Fatalf("uri %q", res.URI)
	}
	if res.Text != "two\nthree" {
		t.Fatalf("text %q", res.Text)
	}
}

// A literal body is what the client already holds; the range only labels the URI.
func TestBuildHydratedComposerPromptLiteralWinsOverLineRange(t *testing.T) {
	root := writeRangeFixture(t)
	blocks, err := session.BuildHydratedComposerPrompt(root, "x", []session.PromptFileAttachment{
		{Path: "f.txt", Source: &session.PromptFileAttachmentSourceField{
			Literal: "edited\nfragment", StartLine: 2, EndLine: 3,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := soleResource(t, blocks)
	if res.URI != "f.txt#L2-3" || res.Text != "edited\nfragment" {
		t.Fatalf("got %q / %q", res.URI, res.Text)
	}
}

func TestHydratePromptContentBlocksRangedMention(t *testing.T) {
	root := writeRangeFixture(t)
	blocks, err := session.HydratePromptContentBlocks(root, []acp.ContentBlock{
		{Type: acp.ContentTypeText, Text: "look at @f.txt:4-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := soleResource(t, blocks)
	if res.URI != "f.txt#L4-5" || res.Text != "four\nfive" {
		t.Fatalf("got %q / %q", res.URI, res.Text)
	}
}

// A plain mention and a ranged one of the same path are different attachments,
// so neither suppresses the other.
func TestHydratePromptContentBlocksRangedAndPlainCoexist(t *testing.T) {
	root := writeRangeFixture(t)
	blocks, err := session.HydratePromptContentBlocks(root, []acp.ContentBlock{
		{Type: acp.ContentTypeText, Text: "@f.txt:2-2 versus @f.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var uris []string
	for _, b := range blocks {
		if b.Type == "resource" && b.Resource != nil {
			uris = append(uris, b.Resource.URI)
		}
	}
	if len(uris) != 2 || uris[0] != "f.txt#L2-2" || uris[1] != "f.txt" {
		t.Fatalf("got %q", uris)
	}
}

// An empty ranged resource sent by a client is filled from disk and sliced.
func TestHydratePromptContentBlocksFillsEmptyRangedResource(t *testing.T) {
	root := writeRangeFixture(t)
	blocks, err := session.HydratePromptContentBlocks(root, []acp.ContentBlock{
		{Type: "resource", Resource: &acp.Resource{URI: "f.txt#L1-2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := soleResource(t, blocks)
	if res.Text != "one\ntwo" {
		t.Fatalf("text %q", res.Text)
	}
}

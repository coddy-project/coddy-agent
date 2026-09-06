package session_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestBuildHydratedComposerPromptAttachment(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "hello world.txt")
	if err := os.WriteFile(p, []byte("hi there"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocks, err := session.BuildHydratedComposerPrompt(root, "see @", []session.PromptFileAttachment{
		{Path: "hello world.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 || blocks[0].Type != "text" || blocks[0].Text != "see @" {
		t.Fatalf("unexpected first blocks: %+v", blocks)
	}
	if blocks[1].Type != "resource" || blocks[1].Resource == nil || blocks[1].Resource.Text != "hi there" {
		t.Fatalf("unexpected resource: %+v", blocks[1])
	}
}

func TestHydratePromptContentBlocksExpandsAtInText(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(p, []byte("z9"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := []acp.ContentBlock{{Type: "text", Text: `please read @secret.txt`}}
	out, err := session.HydratePromptContentBlocks(root, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d blocks", len(out))
	}
	if out[1].Type != "resource" || out[1].Resource == nil || out[1].Resource.Text != "z9" {
		t.Fatalf("resource %+v", out[1])
	}
}

func TestHydratePromptContentBlocksSkipsMissingAtMention(t *testing.T) {
	root := t.TempDir()
	// "@mention_demo" is a rules @mention trigger (no such file). A heuristic @token that
	// does not resolve to a workspace file must be left as text, not fail the whole prompt.
	in := []acp.ContentBlock{{Type: "text", Text: "@mention_demo apply the mention-only rule"}}
	out, err := session.HydratePromptContentBlocks(root, in)
	if err != nil {
		t.Fatalf("missing @file mention must not error: %v", err)
	}
	if len(out) != 1 || out[0].Type != "text" {
		t.Fatalf("expected unchanged single text block, got %+v", out)
	}
}

// binaryBlob is a PNG header followed by NUL padding: valid as bytes, never text.
var binaryBlob = append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 32)...)

func TestBuildHydratedComposerPromptRejectsBinaryAttachment(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "logo.png"), binaryBlob, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := session.BuildHydratedComposerPrompt(root, "look @logo.png", []session.PromptFileAttachment{
		{Path: "logo.png"},
	})
	if !errors.Is(err, session.ErrNotDecodableText) {
		t.Fatalf("err = %v, want ErrNotDecodableText", err)
	}
}

func TestHydratePromptContentBlocksSkipsBinaryAtMention(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "logo.png"), binaryBlob, 0o644); err != nil {
		t.Fatal(err)
	}
	// An @token scraped out of prose that lands on a binary file must be left as
	// text, exactly like a token that resolves to nothing, instead of failing the turn.
	in := []acp.ContentBlock{{Type: "text", Text: "compare @logo.png with the mockup"}}
	out, err := session.HydratePromptContentBlocks(root, in)
	if err != nil {
		t.Fatalf("binary @mention must not error: %v", err)
	}
	if len(out) != 1 || out[0].Type != "text" {
		t.Fatalf("expected unchanged single text block, got %+v", out)
	}
}

// TestReadWorkspaceUTF8KeepsFileWithEmbeddedNUL guards the attachment path
// against over-eager binary detection: a UTF-8 source file that holds a NUL
// literal (this repository ships one in external/ui/src/ui/settings/
// SkillsSection.tsx) must still hydrate, and its bytes must arrive unchanged.
func TestReadWorkspaceUTF8KeepsFileWithEmbeddedNUL(t *testing.T) {
	root := t.TempDir()
	const source = "// Flash key for the \"Sync all\" action.\n" +
		"const SYNC_ALL_KEY = \"\x00all\";\n" +
		"export function useSkills() {\n" +
		"  return SYNC_ALL_KEY;\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(root, "SkillsSection.tsx"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	got, mime, err := session.ReadWorkspaceUTF8(root, "SkillsSection.tsx")
	if err != nil {
		t.Fatalf("ReadWorkspaceUTF8: %v", err)
	}
	if got != source {
		t.Fatalf("content %q, want %q", got, source)
	}
	if mime != "text/plain; charset=utf-8" {
		t.Fatalf("mime %q", mime)
	}
}

func TestReadWorkspaceUTF8DecodesLegacyEncodings(t *testing.T) {
	root := t.TempDir()
	const russian = "Первая строка файла в устаревшей кодировке.\n" +
		"Вторая строка нужна, чтобы определение кодировки было уверенным.\n" +
		"Третья строка завершает пример текста на русском языке.\n"

	cases := []struct {
		name string
		enc  encoding.Encoding
	}{
		{name: "cp1251.txt", enc: charmap.Windows1251},
		{name: "koi8.txt", enc: charmap.KOI8R},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.enc.NewEncoder().Bytes([]byte(russian))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, tc.name), data, 0o644); err != nil {
				t.Fatal(err)
			}
			got, mime, err := session.ReadWorkspaceUTF8(root, tc.name)
			if err != nil {
				t.Fatalf("ReadWorkspaceUTF8: %v", err)
			}
			if got != russian {
				t.Fatalf("content %q, want %q", got, russian)
			}
			if mime != "text/plain; charset=utf-8" {
				t.Fatalf("mime %q", mime)
			}
		})
	}
}

func TestHydratePromptContentBlocksReadsResourceURI(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "a.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := []acp.ContentBlock{
		{Type: "text", Text: "x"},
		{Type: "resource", Resource: &acp.Resource{URI: "a.txt"}},
	}
	out, err := session.HydratePromptContentBlocks(root, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[1].Resource == nil || out[1].Resource.Text != "x" {
		t.Fatalf("got %+v", out)
	}
}

// --- line ranges ("@f.txt:2-3") ---

const rangeFixtureBody = "one\ntwo\nthree\nfour\nfive\n"

// writeRangeFixture writes body to f.txt in a fresh workspace and returns its root.
func writeRangeFixture(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
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
	root := writeRangeFixture(t, rangeFixtureBody)
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

// Boundaries of the slice. An end past the last line clamps; no range at all
// attaches the whole file under the plain path; a range the file cannot honour
// is refused, so the lines label never claims lines the body lacks.
func TestBuildHydratedComposerPromptLineRangeBounds(t *testing.T) {
	cases := []struct {
		name       string
		start, end int
		wantURI    string
		wantText   string
		wantErr    bool
	}{
		{"single line", 1, 1, "f.txt#L1-1", "one", false},
		{"end past last line clamps", 4, 99, "f.txt#L4-99", "four\nfive", false},
		{"no range", 0, 0, "f.txt", rangeFixtureBody, false},
		{"start past last line is refused", 99, 100, "", "", true},
		{"inverted range is refused", 4, 2, "", "", true},
		{"zero start is refused", 0, 5, "", "", true},
		{"missing end is refused", 3, 0, "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := writeRangeFixture(t, rangeFixtureBody)
			blocks, err := session.BuildHydratedComposerPrompt(root, "x", []session.PromptFileAttachment{
				{Path: "f.txt", Source: &session.PromptFileAttachmentSourceField{StartLine: c.start, EndLine: c.end}},
			})
			if c.wantErr {
				if !errors.Is(err, session.ErrLineRange) {
					t.Fatalf("err = %v, want ErrLineRange", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			res := soleResource(t, blocks)
			if res.URI != c.wantURI || res.Text != c.wantText {
				t.Fatalf("got %q / %q, want %q / %q", res.URI, res.Text, c.wantURI, c.wantText)
			}
		})
	}
}

// CRLF content keeps its "\r" between lines and drops it only at the tail, so a
// pasted fragment matches what the editor showed.
func TestBuildHydratedComposerPromptLineRangeCRLF(t *testing.T) {
	root := writeRangeFixture(t, "a\r\nb\r\nc\r\n")
	blocks, err := session.BuildHydratedComposerPrompt(root, "x", []session.PromptFileAttachment{
		{Path: "f.txt", Source: &session.PromptFileAttachmentSourceField{StartLine: 1, EndLine: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := soleResource(t, blocks).Text; got != "a\r\nb" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildHydratedComposerPromptLineRangeNoTrailingNewline(t *testing.T) {
	root := writeRangeFixture(t, "a\nb\nc")
	blocks, err := session.BuildHydratedComposerPrompt(root, "x", []session.PromptFileAttachment{
		{Path: "f.txt", Source: &session.PromptFileAttachmentSourceField{StartLine: 2, EndLine: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := soleResource(t, blocks).Text; got != "b\nc" {
		t.Fatalf("got %q", got)
	}
}

// A literal body is whatever the client sent, so it carries no line label even
// when a range rides along.
func TestBuildHydratedComposerPromptLiteralWinsOverLineRange(t *testing.T) {
	root := writeRangeFixture(t, rangeFixtureBody)
	blocks, err := session.BuildHydratedComposerPrompt(root, "x", []session.PromptFileAttachment{
		{Path: "f.txt", Source: &session.PromptFileAttachmentSourceField{
			Literal: "edited\nfragment", StartLine: 2, EndLine: 3,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := soleResource(t, blocks)
	if res.URI != "f.txt" || res.Text != "edited\nfragment" {
		t.Fatalf("got %q / %q", res.URI, res.Text)
	}
}

// Byte offsets win over a line range, and the body is then no longer those
// lines, so the label goes with them.
func TestBuildHydratedComposerPromptByteOffsetsDropLineLabel(t *testing.T) {
	root := writeRangeFixture(t, rangeFixtureBody)
	blocks, err := session.BuildHydratedComposerPrompt(root, "x", []session.PromptFileAttachment{
		{Path: "f.txt", Source: &session.PromptFileAttachmentSourceField{Start: 0, End: 3, StartLine: 2, EndLine: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := soleResource(t, blocks)
	if res.URI != "f.txt" || res.Text != "one" {
		t.Fatalf("got %q / %q", res.URI, res.Text)
	}
}

func TestHydratePromptContentBlocksRangedMention(t *testing.T) {
	root := writeRangeFixture(t, rangeFixtureBody)
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
	root := writeRangeFixture(t, rangeFixtureBody)
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
	root := writeRangeFixture(t, rangeFixtureBody)
	blocks, err := session.HydratePromptContentBlocks(root, []acp.ContentBlock{
		{Type: "resource", Resource: &acp.Resource{URI: "f.txt#L1-2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := soleResource(t, blocks)
	if res.URI != "f.txt#L1-2" || res.Text != "one\ntwo" {
		t.Fatalf("got %q / %q", res.URI, res.Text)
	}
}

// A typed range that starts past the last line stays prose: it attaches nothing,
// while a plain mention of the same file next to it still hydrates whole. A
// client resource asking for such lines is an error, like a missing file.
func TestHydratePromptContentBlocksRangePastEndIsSkipped(t *testing.T) {
	root := writeRangeFixture(t, rangeFixtureBody)
	blocks, err := session.HydratePromptContentBlocks(root, []acp.ContentBlock{
		{Type: acp.ContentTypeText, Text: "@f.txt:9-12 and @f.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := soleResource(t, blocks)
	if res.URI != "f.txt" || res.Text != rangeFixtureBody {
		t.Fatalf("got %q / %q", res.URI, res.Text)
	}

	blocks, err = session.HydratePromptContentBlocks(root, []acp.ContentBlock{
		{Type: acp.ContentTypeText, Text: "only @f.txt:9-12 here"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Type != acp.ContentTypeText {
		t.Fatalf("expected the text block alone, got %+v", blocks)
	}

	if _, err := session.HydratePromptContentBlocks(root, []acp.ContentBlock{
		{Type: "resource", Resource: &acp.Resource{URI: "f.txt#L9-12"}},
	}); !errors.Is(err, session.ErrLineRange) {
		t.Fatalf("client resource: err = %v, want ErrLineRange", err)
	}
}

// A path that merely contains "#L" without a well-formed range is a file name,
// so a file named that way still resolves whole.
func TestHydratePromptContentBlocksKeepsMalformedFragmentInName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"f.go#L5", "f.go#Labc-def", "f.go#L0-3", "f.go#L9-2", "notes#L1-2.go", "f.go#L1-2x"} {
		body := "body of " + name + "\n"
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		blocks, err := session.HydratePromptContentBlocks(root, []acp.ContentBlock{
			{Type: "resource", Resource: &acp.Resource{URI: name}},
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		res := soleResource(t, blocks)
		if res.URI != name || res.Text != body {
			t.Fatalf("%s: got %q / %q", name, res.URI, res.Text)
		}
	}
}

package session_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
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

func TestHydratePromptContentBlocksNotesBinaryAtMention(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "logo.png"), binaryBlob, 0o644); err != nil {
		t.Fatal(err)
	}
	// A mention that lands on a binary file does not fail the turn, and it is
	// not dropped either: the model is told the file is there and why its
	// bytes are not inlined, so it does not guess.
	in := []acp.ContentBlock{{Type: "text", Text: "compare @logo.png with the mockup"}}
	out, err := session.HydratePromptContentBlocks(root, in)
	if err != nil {
		t.Fatalf("binary @mention must not error: %v", err)
	}
	res := soleResource(t, out)
	if res.URI != "logo.png" || !strings.Contains(res.Text, "Not inlined") || !strings.Contains(res.Text, "binary") {
		t.Fatalf("binary note: %q / %q", res.URI, res.Text)
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

// A typed range that starts past the last line never widens into the whole
// file: it attaches a note naming the file's length, while a plain mention of
// the same file next to it still hydrates whole. A client resource asking for
// such lines is an error, like a missing file.
func TestHydratePromptContentBlocksRangePastEndIsNoted(t *testing.T) {
	root := writeRangeFixture(t, rangeFixtureBody)
	blocks, err := session.HydratePromptContentBlocks(root, []acp.ContentBlock{
		{Type: acp.ContentTypeText, Text: "@f.txt:9-12 and @f.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []*acp.Resource
	for _, b := range blocks {
		if b.Resource != nil {
			got = append(got, b.Resource)
		}
	}
	if len(got) != 2 || got[0].URI != "f.txt#L9-12" || !strings.Contains(got[0].Text, "which has 5 lines") ||
		got[1].URI != "f.txt" || got[1].Text != rangeFixtureBody {
		t.Fatalf("got %+v", got)
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

// --- "@" completion (SearchMentions) ---

func mentionTestManager(t *testing.T, cwd string) (*session.Manager, string) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	store := &session.FileStore{Root: t.TempDir()}
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.New(slog.DiscardHandler), cwd, store)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	return m, res.SessionID
}

func candidateInserts(res session.MentionSearchResult) []string {
	var out []string
	for _, c := range res.Items {
		out = append(out, c.Insert)
	}
	return out
}

// Issue #291: the list is ranked against what was typed, not the first fifty
// files of the tree, and the count of what was cut travels with it.
func TestSearchMentionsRanksTheWholeWorkspace(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 120; i++ {
		p := filepath.Join(root, "aaa", fmt.Sprintf("file%03d.go", i))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}
	_ = os.MkdirAll(filepath.Join(root, "zzz", "deep"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "zzz", "deep", "target_handler.go"), []byte("x"), 0o644)
	m, sid := mentionTestManager(t, root)

	res, _ := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: "targhand"})
	if len(res.Items) == 0 || res.Items[0].Insert != "@zzz/deep/target_handler.go" {
		t.Fatalf("a file deep in the tree must be found by a fragment of its name: %+v", res.Items)
	}
	res, _ = m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: "file", Limit: 10})
	if len(res.Items) != 10 || res.Total < 120 {
		t.Fatalf("the cut must be reported: %d items of %d", len(res.Items), res.Total)
	}
}

// Issue #290: a file written after the first search is offered once the
// picker opens again (the surface asks with Refresh), and a deleted one goes.
func TestSearchMentionsRefreshSeesNewAndDeletedFiles(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "old.txt"), []byte("x"), 0o644)
	m, sid := mentionTestManager(t, root)
	if res, _ := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: "new"}); len(res.Items) != 0 {
		t.Fatalf("nothing new yet: %+v", res.Items)
	}
	_ = os.WriteFile(filepath.Join(root, "brand_new.txt"), []byte("x"), 0o644)
	_ = os.Remove(filepath.Join(root, "old.txt"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, _ := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: "new", Refresh: true})
		gone, _ := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: "old"})
		if len(res.Items) > 0 && res.Items[0].Insert == "@brand_new.txt" && len(gone.Items) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the index never caught up: new=%v old=%v", candidateInserts(res), candidateInserts(gone))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Issue #289: an absolute path, a path under home and a path above the
// workspace are completed by browsing the folder typed so far.
func TestSearchMentionsBrowsesAbsoluteAndHomePaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	_ = os.MkdirAll(filepath.Join(outside, "sub dir"), 0o755)
	_ = os.WriteFile(filepath.Join(outside, "notes.md"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(outside, ".hidden"), []byte("x"), 0o644)
	m, sid := mentionTestManager(t, root)

	res, _ := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: filepath.ToSlash(outside) + "/"})
	got := candidateInserts(res)
	want := []string{`@"` + filepath.ToSlash(outside) + `/sub dir/`, "@" + filepath.ToSlash(outside) + "/notes.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("browse %s = %q, want %q", outside, got, want)
	}
	if !res.Items[0].Continue || res.Items[1].Continue {
		t.Fatalf("a folder keeps the picker open, a file closes it: %+v", res.Items)
	}
	res, _ = m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: filepath.ToSlash(outside) + "/no"})
	if got := candidateInserts(res); len(got) != 1 || got[0] != "@"+filepath.ToSlash(outside)+"/notes.md" {
		t.Fatalf("a name prefix filters the folder: %q", got)
	}
	res, _ = m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: filepath.ToSlash(outside) + "/."})
	if got := candidateInserts(res); len(got) != 1 || got[0] != "@"+filepath.ToSlash(outside)+"/.hidden" {
		t.Fatalf("a dot prefix shows hidden entries: %q", got)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.WriteFile(filepath.Join(home, "todo.txt"), []byte("x"), 0o644)
	res, _ = m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: "~/to"})
	if got := candidateInserts(res); len(got) != 1 || got[0] != "@~/todo.txt" {
		t.Fatalf("~/ completes under home and keeps the typed form: %q", got)
	}
}

// An empty query offers the meta schemes first; "session:" lists the other
// sessions, never the one the draft belongs to.
func TestSearchMentionsSchemes(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("x"), 0o644)
	m, sid := mentionTestManager(t, root)
	other, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	st := m.SessionByID(other.SessionID)
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "refactor the auth layer"})
	if err := m.FileStore().Save(st); err != nil {
		t.Fatal(err)
	}

	res, _ := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid})
	if got := candidateInserts(res); len(got) < 4 || got[0] != "@session:" || got[1] != "@rule:" || got[2] != "@agent:" || got[3] != "@a.go" {
		t.Fatalf("empty query: %q", got)
	}
	res, _ = m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: "session:auth"})
	if got := candidateInserts(res); len(got) != 1 || got[0] != "@session:"+other.SessionID {
		t.Fatalf("session search by title: %q", got)
	}
	res, _ = m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: "agent:expl"})
	if got := candidateInserts(res); len(got) != 1 || got[0] != "@agent:explore" {
		t.Fatalf("agent search: %q", got)
	}
}

// A folder written a moment before the prompt was sent is listed as it is
// on disk: the completion index, built earlier, has not seen it yet, and the
// prompt must not read that snapshot as "the folder is empty". Inside a git
// checkout git still decides what is left out.
func TestFolderMentionListsWhatIsOnDiskNow(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(root, ".gitignore"), []byte("fresh/*.log\n"), 0o644)
	m, sid := mentionTestManager(t, root)
	// The completion index of this workspace is built before the folder exists.
	if res, _ := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: "a.go", Wait: 5 * time.Second}); len(res.Items) == 0 {
		t.Fatal("the index was not built")
	}
	if err := os.MkdirAll(filepath.Join(root, "fresh", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"fresh/inner.txt", "fresh/deep/leaf.txt", "fresh/build.log"} {
		_ = os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644)
	}
	out, err := session.HydratePromptContentBlocks(root, []acp.ContentBlock{{Type: "text", Text: "what is in @fresh/ ?"}})
	if err != nil {
		t.Fatal(err)
	}
	var listing string
	for _, b := range out {
		if b.Resource != nil {
			listing = b.Resource.Text
		}
	}
	for _, want := range []string{"inner.txt", "deep/leaf.txt"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("the listing lacks %q:\n%s", want, listing)
		}
	}
	if strings.Contains(listing, "build.log") {
		t.Fatalf("the listing carries a file git ignores:\n%s", listing)
	}
}

// The composer's check reads a draft the way sending it would, and reads
// nothing it names: every mention that would attach is marked over the
// reading that wins, a mention repeated in the draft is marked each time, and
// one that names nothing - a package, a handle - is left unmarked.
func TestCheckMentionsMarksWhatSendingWouldAttach(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"src/a.go", "notes/my draft.md"} {
		p := filepath.Join(root, f)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, sid := mentionTestManager(t, root)
	other, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	st := m.SessionByID(other.SessionID)
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "earlier work"})
	if err := m.FileStore().Save(st); err != nil {
		t.Fatal(err)
	}
	text := strings.Join([]string{
		"npm i @types/node",
		"compare @src/a.go b.go",
		"lines @src/a.go:1-1",
		`quoted @"notes/my draft.md"`,
		"folder @src/",
		"session @session:" + other.SessionID,
		"handle @user",
		"code `@src/a.go`",
		"again @src/a.go",
	}, "\n")
	got := m.CheckMentions(context.Background(), session.MentionCheck{SessionID: sid, Text: text})
	want := []session.CheckedMention{
		{Token: "@types/node"},
		{Token: "@src/a.go b.go", Typed: "@src/a.go", Kind: "file"},
		{Token: "@src/a.go:1-1", Typed: "@src/a.go:1-1", Kind: "file"},
		{Token: `@"notes/my draft.md"`, Typed: `@"notes/my draft.md"`, Kind: "file"},
		{Token: "@src/", Typed: "@src/", Kind: "directory"},
		{Token: "@session:" + other.SessionID, Typed: "@session:" + other.SessionID, Kind: "session"},
		{Token: "@user"},
		{Token: "@src/a.go", Typed: "@src/a.go", Kind: "file"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("check:\n got %+v\nwant %+v", got, want)
	}
}

// A rule kept in two trees (.cursor/rules/x.mdc and .claude/rules/x.md) is one
// name to "@rule:", so the picker offers it once, and a rule without a
// description adds nothing to the kind its row already names.
func TestSearchMentionsOffersARuleOnce(t *testing.T) {
	root := t.TempDir()
	for _, f := range []struct{ path, body string }{
		{".cursor/rules/release-order.mdc", "---\nalwaysApply: false\n---\nShip in order."},
		{".claude/rules/release-order.md", "Ship in order."},
	} {
		p := filepath.Join(root, f.path)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, sid := mentionTestManager(t, root)
	for _, q := range []string{"rule:release", "release-ord"} {
		res, _ := m.SearchMentions(context.Background(), session.MentionSearch{SessionID: sid, Query: q})
		n := 0
		for _, c := range res.Items {
			if c.Insert == "@rule:release-order" {
				n++
				if c.Detail != "" {
					t.Fatalf("%s: detail %q repeats the kind", q, c.Detail)
				}
			}
		}
		if n != 1 {
			t.Fatalf("%s: @rule:release-order offered %d times in %q", q, n, candidateInserts(res))
		}
	}
}

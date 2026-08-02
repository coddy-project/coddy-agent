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

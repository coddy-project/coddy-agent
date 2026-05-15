package agent

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestContentBlocksToText_textAndResource(t *testing.T) {
	blocks := []acp.ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "resource", Resource: &acp.Resource{URI: "file:///a/b.go", Text: "pkg main"}},
	}
	got := contentBlocksToText(blocks)
	if !strings.Contains(got, `<coddy_attachment path="`) ||
		!strings.Contains(got, `name="b.go"`) ||
		!strings.Contains(got, "<![CDATA[") ||
		!strings.Contains(got, "pkg main") ||
		!strings.Contains(got, "]]>") {
		t.Fatalf("unexpected XML bundle: %s", got)
	}
}

func TestExtractContextFiles_fileURI(t *testing.T) {
	blocks := []acp.ContentBlock{
		{Type: "resource", Resource: &acp.Resource{URI: "file:///tmp/x.txt", Text: "x"}},
		{Type: "resource", Resource: &acp.Resource{URI: "https://example.com/z", Text: ""}},
	}
	got := extractContextFiles(blocks)
	if len(got) != 1 || got[0] != "/tmp/x.txt" {
		t.Fatalf("got %#v", got)
	}
}

func TestToolKind(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"read", "read"},
		{"glob", "read"},
		{"grep", "read"},
		{"write", "write"},
		{"apply_patch", "write"},
		{"run_command", "run_command"},
		{"mkdir", "write"},
		{"mcp_server__tool", "other"},
	}
	for _, tc := range cases {
		if g := toolKind(tc.name); g != tc.want {
			t.Errorf("toolKind(%q) = %q, want %q", tc.name, g, tc.want)
		}
	}
}

func TestExtractCommand(t *testing.T) {
	if g := extractCommand(`{"command":"ls -la"}`); g != "ls -la" {
		t.Fatalf("got %q", g)
	}
	if g := extractCommand(`{`); g != "" {
		t.Fatalf("invalid json: got %q", g)
	}
}

func TestFormatMergedMemory(t *testing.T) {
	if g := formatMergedMemory("", "facts"); g != "facts" {
		t.Fatalf("got %q", g)
	}
	if g := formatMergedMemory("note", ""); g != "Session notes:\nnote" {
		t.Fatalf("got %q", g)
	}
	want := "facts\n\nSession notes:\nnote"
	if g := formatMergedMemory("note", "facts"); g != want {
		t.Fatalf("got %q want %q", g, want)
	}
}

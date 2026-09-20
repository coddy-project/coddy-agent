package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// docsToolDescriptions is what the two documentation tools put in front of the
// model: their descriptions are inlined verbatim into the system prompt.
func docsToolDescriptions() map[string]string {
	read := tools.DocsReadTool().Definition
	search := tools.DocsSearchTool().Definition
	return map[string]string{
		read.Name:   read.Description,
		search.Name: search.Description,
	}
}

var docsCommandRE = regexp.MustCompile(`coddy docs ([a-z][a-z-]*)`)

// TestDocsToolsNameOnlyCommandsTheBinaryAccepts holds the model-facing text to
// the verbs runDocs really has. A description that names `coddy docs read`
// teaches the model a command the binary answers with a usage error.
func TestDocsToolsNameOnlyCommandsTheBinaryAccepts(t *testing.T) {
	accepted := map[string]bool{}
	for _, v := range docsVerbs {
		accepted[v] = true
	}
	for name, desc := range docsToolDescriptions() {
		for _, m := range docsCommandRE.FindAllStringSubmatch(desc, -1) {
			if !accepted[m[1]] {
				t.Errorf("%s names %q, but coddy docs accepts only %v", name, m[0], docsVerbs)
			}
		}
	}
}

// TestDocsVerbsMatchTheUsageLine keeps the mirrored list honest: the usage line
// is built from docsVerbs, so a verb added to the switch alone shows up here.
func TestDocsVerbsMatchTheUsageLine(t *testing.T) {
	for _, v := range docsVerbs {
		if !strings.Contains(docsUsage(), v) {
			t.Errorf("usage line does not name the verb %q: %s", v, docsUsage())
		}
	}
}

// TestDocsReadToolTeachesTheMentionFirst keeps the built-in reader the default
// way to point a user at a page: the mention resolves inside every surface,
// the public address only helps outside Coddy.
func TestDocsReadToolTeachesTheMentionFirst(t *testing.T) {
	desc := tools.DocsReadTool().Definition.Description
	mention := strings.Index(desc, "@coddy:")
	site := strings.Index(desc, "https://coddy.dev/docs/")
	if mention < 0 {
		t.Fatalf("the reader's description never names the @coddy: form:\n%s", desc)
	}
	if site >= 0 && site < mention {
		t.Errorf("the public address comes before the @coddy: form:\n%s", desc)
	}
	if !strings.Contains(desc, "coddy docs show") {
		t.Errorf("the reader's description never names the command line spelling:\n%s", desc)
	}
}

// docsReadToolName is the key of the reader in docsToolDescriptions.
func docsReadToolName() string { return tools.ToolDocsRead }

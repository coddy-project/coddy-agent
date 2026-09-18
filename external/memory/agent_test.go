//go:build memory

package memory

import (
	"path/filepath"
	"strings"
	"testing"

	memtools "github.com/EvilFreelancer/coddy-agent/external/memory/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

func TestPromptTemplateRendersToolsAndReadOnlyAddendum(t *testing.T) {
	full, err := prompts.RenderSource("memory", PromptTemplate(false), prompts.TemplateData{CWD: "/w", Tools: "- `coddy_memory_search`"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You are Coddy's memory subagent", "Working directory: /w", "## Available tools", "coddy_memory_search", "(no memory hits)"} {
		if !strings.Contains(full, want) {
			t.Errorf("rendered template lacks %q", want)
		}
	}
	if strings.Contains(full, "read-only ask mode") {
		t.Error("the full template must not carry the read-only addendum")
	}
	if !strings.Contains(strings.ToLower(full[:220]), prompts.IdentityMarker) {
		t.Error("the template must identify Coddy inside the gateway window")
	}
	ro, err := prompts.RenderSource("memory", PromptTemplate(true), prompts.TemplateData{CWD: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ro, "read-only ask mode") {
		t.Error("the read-only template must carry the addendum")
	}
	if strings.Contains(ro, "## Available tools") {
		t.Error("an empty tool list must render no tools section")
	}
}

func TestTaskMessageIsBounded(t *testing.T) {
	if got := TaskMessage("  remember that I prefer pytest  "); got != "User message for this turn:\nremember that I prefer pytest" {
		t.Fatalf("TaskMessage = %q", got)
	}
	long := strings.Repeat("ж", subagents.MaxPromptBytes) // two bytes per rune: the cut lands inside a rune
	got := TaskMessage(long)
	if len(got) > subagents.MaxPromptBytes+len(taskPreamble)+len(cutMarker) {
		t.Fatalf("task is %d bytes, over the bound", len(got))
	}
	if !strings.HasSuffix(got, cutMarker) {
		t.Fatal("a cut task must end with the marker")
	}
	body := strings.TrimSuffix(strings.TrimPrefix(got, taskPreamble), cutMarker)
	if !strings.HasPrefix(long, body) || strings.ContainsRune(body, '�') {
		t.Fatal("the cut must fall on a rune boundary and keep the head of the message")
	}
}

func TestToolNamesAndTools(t *testing.T) {
	if got := ToolNames(true); len(got) != 3 || got[0] != memtools.NameSearch || got[2] != memtools.NameRead {
		t.Fatalf("recall tool names = %v", got)
	}
	if got := ToolNames(false); len(got) != 6 || got[5] != memtools.NameDelete {
		t.Fatalf("full tool names = %v", got)
	}
	tmp := t.TempDir()
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.ApplyDefaults()
	cfg.Paths.Home = tmp
	tools, err := Tools(cfg, filepath.Join(tmp, "w"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Definition.Name] = true
	}
	for _, want := range ToolNames(false) {
		if !names[want] {
			t.Errorf("Tools() lacks %s", want)
		}
	}
}

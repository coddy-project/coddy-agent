package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func TestBuildSkillsPromptMarkdown_orderAndDedupeInvoke(t *testing.T) {
	dirSK := filepath.Join("p", "my-skill", "SKILL.md")
	all := []*skills.Skill{
		{Name: "SKILL", FilePath: dirSK, Content: "Body skill", AlwaysApply: true},
	}
	active := []*skills.Skill{
		{Name: "SKILL", FilePath: dirSK, Content: "Body skill", AlwaysApply: true},
	}

	md := buildSkillsPromptMarkdown(all, active, "/my-skill hello")
	if !strings.Contains(md, "## Slash commands") {
		t.Fatalf("missing catalog: %s", md)
	}
	if strings.Contains(md, "## Active Rules and Skills") {
		t.Fatalf("active skills already listed in catalog; full bodies must be omitted:\n%s", md)
	}
	if strings.Contains(md, "## User-invoked slash command instructions") {
		t.Fatalf("/my-skill matches glob-active; ephemeral block should be omitted:\n%s", md)
	}

	md2 := buildSkillsPromptMarkdown(all, active, "")
	if strings.Contains(md2, "User-invoked") {
		t.Fatalf("unexpected invoke block: %s", md2)
	}

	other := []*skills.Skill{
		{Name: "SKILL", FilePath: filepath.Join("p", "other", "SKILL.md"), Content: "Ephemeral-only body", AlwaysApply: false, Globs: []string{"**/*.zzz"}},
	}
	activeNone := []*skills.Skill{}
	md3 := buildSkillsPromptMarkdown(other, activeNone, "/other")
	if !strings.Contains(md3, "## User-invoked slash command instructions") || !strings.Contains(md3, "Ephemeral-only body") {
		t.Fatalf("expected ephemeral slash body:\n%s", md3)
	}
}

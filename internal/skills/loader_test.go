package skills_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func withoutBundled(loaded []*skills.Skill) []*skills.Skill {
	var out []*skills.Skill
	for _, s := range loaded {
		if skills.CanonicalCommandName(s) == "generate-rules" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func TestLoadSkillWithFrontmatter(t *testing.T) {
	tmp := t.TempDir()

	content := `---
name: "go-standards"
description: "Go coding standards"
version: "1.4.2"
---

# Go Standards

Write comments in English.
Use fmt.Errorf for error wrapping.
`
	path := filepath.Join(tmp, "go-standards.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader([]string{tmp})
	loaded, err := loader.LoadAll(tmp, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}

	s := loaded[0]
	if s.Name != "go-standards" {
		t.Errorf("expected name %q from frontmatter, got %q", "go-standards", s.Name)
	}
	if s.Description != "Go coding standards" {
		t.Errorf("expected description %q, got %q", "Go coding standards", s.Description)
	}
	if s.Version != "1.4.2" {
		t.Errorf("expected version %q from frontmatter, got %q", "1.4.2", s.Version)
	}
	if !strings.Contains(s.Content, "Write comments") {
		t.Errorf("expected content in skill body, got: %q", s.Content)
	}
}

func TestLoadSkillNoFrontmatter(t *testing.T) {
	tmp := t.TempDir()

	content := "# Simple Rule\n\nAlways write tests."
	path := filepath.Join(tmp, "simple.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader([]string{tmp})
	loaded, err := loader.LoadAll(tmp, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if !strings.Contains(loaded[0].Content, "Always write tests") {
		t.Errorf("unexpected content: %q", loaded[0].Content)
	}
}

func TestLoadSKILLFile(t *testing.T) {
	tmp := t.TempDir()

	// Create skill in subdirectory.
	skillDir := filepath.Join(tmp, "my-skill")
	if err := os.Mkdir(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "# My Skill\n\nDo something useful."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader([]string{tmp})
	loaded, err := loader.LoadAll(tmp, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Name != "SKILL" {
		t.Errorf("expected name %q, got %q", "SKILL", loaded[0].Name)
	}
}

func TestLoadSymlinkDirWithSKILLMd(t *testing.T) {
	tmp := t.TempDir()
	realDir := filepath.Join(tmp, "real", "linked-skill")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ndescription: via symlink dir\n---\n\nBody."
	if err := os.WriteFile(filepath.Join(realDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "skills-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-skill")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skip("unsupported symlink:", err)
	}

	loader := skills.NewLoader([]string{root})
	loaded, err := loader.LoadAll(tmp, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if got := skills.CanonicalCommandName(loaded[0]); got != "linked-skill" {
		t.Fatalf("canonical name: got %q want linked-skill", got)
	}
	if loaded[0].Description != "via symlink dir" {
		t.Errorf("description %q", loaded[0].Description)
	}
}

func TestFilterForContext(t *testing.T) {
	a := &skills.Skill{Name: "a", Content: "content a"}
	b := &skills.Skill{Name: "b", Content: "content b"}
	c := &skills.Skill{Name: "c", Content: "content c"}

	all := []*skills.Skill{a, b, c}

	// All skills are always returned regardless of context files.
	for _, contextFiles := range [][]string{nil, {"/project/main.go"}, {"/project/app.ts"}} {
		filtered := skills.FilterForContext(all, contextFiles)
		if len(filtered) != 3 {
			t.Errorf("expected 3 skills for context %v, got %d", contextFiles, len(filtered))
		}
	}
}

func TestBuildSystemPromptSection(t *testing.T) {
	loaded := []*skills.Skill{
		{Name: "rule1", FilePath: filepath.Join("/tmp", "rules", "rule1.md"), Description: "First rule", Content: "Content of rule 1"},
		{Name: "rule2", FilePath: filepath.Join("/tmp", "rules", "rule2.md"), Content: "Content of rule 2"},
	}

	section := skills.BuildSystemPromptSection(loaded)
	if !strings.Contains(section, "## Active Skills") {
		t.Error("missing section header")
	}
	if !strings.Contains(section, "rule1") {
		t.Error("missing rule1")
	}
	if !strings.Contains(section, "First rule") {
		t.Error("missing description")
	}
	if !strings.Contains(section, "Content of rule 1") {
		t.Error("missing content")
	}
}

func TestLoadAllExpandsCODDYHome(t *testing.T) {
	home := t.TempDir()
	skillRoot := filepath.Join(home, "skills")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# In Home\n\nBody."
	if err := os.WriteFile(filepath.Join(skillRoot, "rule.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := skills.NewLoader([]string{"${CODDY_HOME}/skills"})
	loaded, err := loader.LoadAll("/tmp", home)
	if err != nil {
		t.Fatal(err)
	}
	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("got %d skills", len(loaded))
	}
}

func TestLoadFromNonexistentDir(t *testing.T) {
	loader := skills.NewLoader([]string{"/nonexistent/path"})
	loaded, err := loader.LoadAll("/tmp", "")
	// Should not error, just return empty.
	if err != nil {
		t.Fatalf("expected no error for nonexistent dir, got: %v", err)
	}
	loaded = withoutBundled(loaded)
	if len(loaded) != 0 {
		t.Errorf("expected no user skills for nonexistent dir, got %d", len(loaded))
	}
}

func TestLaterDirOverridesSameName(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// Same skill name in two directories.
	skill1 := filepath.Join(dir1, "my-skill")
	if err := os.MkdirAll(skill1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill1, "SKILL.md"), []byte("---\ndescription: from dir1\n---\n\nBody1"), 0o644); err != nil {
		t.Fatal(err)
	}

	skill2 := filepath.Join(dir2, "my-skill")
	if err := os.MkdirAll(skill2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill2, "SKILL.md"), []byte("---\ndescription: from dir2\n---\n\nBody2"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader([]string{dir1, dir2})
	loaded, err := loader.LoadAll("/tmp", "")
	if err != nil {
		t.Fatal(err)
	}
	loaded = withoutBundled(loaded)

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill after dedup, got %d", len(loaded))
	}
	if loaded[0].Description != "from dir2" {
		t.Errorf("expected dir2 to win, got description %q", loaded[0].Description)
	}
	if !strings.Contains(loaded[0].Content, "Body2") {
		t.Errorf("expected dir2 content, got %q", loaded[0].Content)
	}
}

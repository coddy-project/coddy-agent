package skills_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func TestLoadSkillWithFrontmatter(t *testing.T) {
	tmp := t.TempDir()

	content := `---
description: "Go coding standards"
globs: ["**/*.go"]
alwaysApply: false
---

# Go Standards

Write comments in English.
Use fmt.Errorf for error wrapping.
`
	path := filepath.Join(tmp, "go-standards.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := skills.NewLoader([]string{tmp}, nil)
	loaded, err := loader.LoadAll(tmp)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}

	s := loaded[0]
	if s.Description != "Go coding standards" {
		t.Errorf("expected description %q, got %q", "Go coding standards", s.Description)
	}
	if len(s.Globs) != 1 || s.Globs[0] != "**/*.go" {
		t.Errorf("unexpected globs: %v", s.Globs)
	}
	if s.AlwaysApply {
		t.Error("expected alwaysApply to be false")
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

	loader := skills.NewLoader([]string{tmp}, nil)
	loaded, err := loader.LoadAll(tmp)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

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

	loader := skills.NewLoader([]string{tmp}, nil)
	loaded, err := loader.LoadAll(tmp)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Name != "SKILL" {
		t.Errorf("expected name %q, got %q", "SKILL", loaded[0].Name)
	}
}

func TestFilterForContext(t *testing.T) {
	goRule := &skills.Skill{
		Name:        "go-rule",
		Globs:       []string{"**/*.go"},
		AlwaysApply: false,
		Content:     "Go rule content",
	}
	alwaysRule := &skills.Skill{
		Name:        "always-rule",
		AlwaysApply: true,
		Content:     "Always rule content",
	}
	tsRule := &skills.Skill{
		Name:        "ts-rule",
		Globs:       []string{"**/*.ts"},
		AlwaysApply: false,
		Content:     "TypeScript rule content",
	}

	allSkills := []*skills.Skill{goRule, alwaysRule, tsRule}

	// With Go files in context.
	goFiles := []string{"/project/main.go", "/project/server.go"}
	filtered := skills.FilterForContext(allSkills, goFiles)
	if len(filtered) != 2 {
		t.Errorf("expected 2 skills for Go context, got %d", len(filtered))
	}
	for _, s := range filtered {
		if s.Name == "ts-rule" {
			t.Error("ts-rule should not be included for Go files")
		}
	}

	// With TypeScript files in context.
	tsFiles := []string{"/project/app.ts"}
	filtered = skills.FilterForContext(allSkills, tsFiles)
	if len(filtered) != 2 {
		t.Errorf("expected 2 skills for TS context, got %d", len(filtered))
	}

	// With no context files - only alwaysApply and no-glob rules.
	filtered = skills.FilterForContext(allSkills, nil)
	if len(filtered) != 1 {
		t.Errorf("expected 1 skill for empty context, got %d", len(filtered))
	}
	if filtered[0].Name != "always-rule" {
		t.Errorf("expected always-rule, got %q", filtered[0].Name)
	}
}

func TestBuildSystemPromptSection(t *testing.T) {
	loaded := []*skills.Skill{
		{Name: "rule1", Description: "First rule", Content: "Content of rule 1"},
		{Name: "rule2", Content: "Content of rule 2"},
	}

	section := skills.BuildSystemPromptSection(loaded)
	if !strings.Contains(section, "## Active Rules and Skills") {
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

func TestLoadFromNonexistentDir(t *testing.T) {
	loader := skills.NewLoader([]string{"/nonexistent/path"}, nil)
	loaded, err := loader.LoadAll("/tmp")
	// Should not error, just return empty.
	if err != nil {
		t.Fatalf("expected no error for nonexistent dir, got: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected empty result for nonexistent dir, got %d", len(loaded))
	}
}

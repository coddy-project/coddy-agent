package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestRepositoryRuleMirrors(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	cursor := repositoryRuleFiles(t, filepath.Join(root, ".cursor", "rules"), ".mdc")
	claude := repositoryRuleFiles(t, filepath.Join(root, ".claude", "rules"), ".md")

	if !reflect.DeepEqual(sortedKeys(cursor), sortedKeys(claude)) {
		t.Fatalf("rule IDs differ: cursor=%v claude=%v", sortedKeys(cursor), sortedKeys(claude))
	}
	for _, name := range sortedKeys(cursor) {
		cursorData := mustReadRule(t, cursor[name])
		claudeData := mustReadRule(t, claude[name])
		cursorRule, err := ParseRuleFile(cursor[name], SourceCursor, cursorData)
		if err != nil {
			t.Fatal(err)
		}
		claudeRule, err := ParseRuleFile(claude[name], SourceClaude, claudeData)
		if err != nil {
			t.Fatal(err)
		}

		if cursorRule.Description != claudeRule.Description {
			t.Errorf("%s description differs: %q != %q", name, cursorRule.Description, claudeRule.Description)
		}
		if !reflect.DeepEqual(cursorRule.Globs, claudeRule.Globs) {
			t.Errorf("%s globs differ: %v != %v", name, cursorRule.Globs, claudeRule.Globs)
		}
		if cursorRule.Content != claudeRule.Content {
			t.Errorf("%s body differs", name)
		}
		if cursorRule.AlwaysOn() != claudeRule.AlwaysOn() {
			t.Errorf(
				"%s activation differs: cursor always=%v claude always=%v",
				name,
				cursorRule.AlwaysOn(),
				claudeRule.AlwaysOn(),
			)
		}
		if len(claudeRule.Globs) > 0 && !strings.Contains(string(cursorData), "alwaysApply: false") {
			t.Errorf("%s is path-scoped in Claude but not in Cursor", name)
		}
	}
}

func TestRepositoryWorkflowRuleCoversGovernedArtifacts(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(root, ".cursor", "rules", "workflow.mdc")
	rule, err := ParseRuleFile(path, SourceCursor, mustReadRule(t, path))
	if err != nil {
		t.Fatal(err)
	}
	rule.Root = root
	for _, governed := range []string{
		"config.example.yaml",
		"examples/httpserver/config.example.yaml",
		"go.mod",
		"go.sum",
		".golangci.yml",
		"Dockerfile",
	} {
		if !matchesRuleGlobs(rule, []string{governed}) {
			t.Errorf("workflow rule does not cover %s", governed)
		}
	}
}

func TestRepositoryInstructionCompatibilityFiles(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	target, err := instructionCompatibilityTarget(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "AGENTS.md" {
		t.Fatalf("CLAUDE.md -> %q, want AGENTS.md", target)
	}
	if _, err := os.Stat(filepath.Join(root, ".codex", "rules.md")); !os.IsNotExist(err) {
		t.Fatalf(".codex/rules.md must not exist, err=%v", err)
	}
}

func instructionCompatibilityTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Readlink(path)
	}
	if info.Mode().IsRegular() {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return "", fmt.Errorf("%s is neither a symlink nor a regular compatibility file", path)
}

func TestInstructionCompatibilityTargetAllowsMaterializedSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target, err := instructionCompatibilityTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	if target != "AGENTS.md" {
		t.Fatalf("materialized CLAUDE.md -> %q, want AGENTS.md", target)
	}
}

func repositoryRuleFiles(t *testing.T, dir, extension string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != extension {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), extension)
		out[name] = filepath.Join(dir, entry.Name())
	}
	return out
}

func sortedKeys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func mustReadRule(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

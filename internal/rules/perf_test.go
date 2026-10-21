package rules_test

// The performance group: benchmarks only, run by `make test-perf` and never by
// `make test`. Discovery runs once per session and after every compaction, so
// it has to stay cheap on a project that keeps its rules for several agents.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

// mirroredProject keeps n rules for Cursor, a copy of each for Claude Code and
// Codex's command policy, the way a project set up for several agents does.
func mirroredProject(b *testing.B, n int) string {
	b.Helper()
	cwd := b.TempDir()
	var files []string
	for i := 0; i < n; i++ {
		files = append(files, fmt.Sprintf(".cursor/rules/rule-%03d.mdc", i), fmt.Sprintf(".claude/rules/rule-%03d.md", i))
	}
	writeRuleTree(b, cwd, files...)
	if err := os.MkdirAll(filepath.Join(cwd, ".codex", "rules"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".codex", "rules", "default.rules"), []byte("prefix_rule()"), 0o644); err != nil {
		b.Fatal(err)
	}
	return cwd
}

// BenchmarkDiscoverMirroredProject is what a session start and a compaction
// pay: the chain stops at the first folder that holds rules.
func BenchmarkDiscoverMirroredProject(b *testing.B) {
	cwd := mirroredProject(b, 50)
	f := rules.DefaultFactory("")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := f.Discover(cwd, nil)
		if err != nil || len(got) != 50 {
			b.Fatalf("discovered %d rules (%v), want 50", len(got), err)
		}
	}
}

// BenchmarkInspectMirroredProject is what `coddy rules list` pays on top: it
// also looks into the folders the chain skipped.
func BenchmarkInspectMirroredProject(b *testing.B) {
	cwd := mirroredProject(b, 50)
	f := rules.DefaultFactory("")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, err := f.Inspect(cwd, nil)
		if err != nil || len(d.Skipped) != 1 {
			b.Fatalf("inspect = %+v (%v), want one skipped folder", d, err)
		}
	}
}

// BenchmarkNestedOnADeepPath is what every filesystem tool call pays for the
// rule folders of subfolders (issue #557) in a workspace whose subfolders keep
// none: the chain of project folders probed under each folder on the way down
// to the file.
func BenchmarkNestedOnADeepPath(b *testing.B) {
	root := b.TempDir()
	file := filepath.Join(root, "a", "b", "c", "d", "e", "f", "x.go")
	writeBenchFile(b, file)
	f := rules.DefaultFactory("")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := f.Nested(root, []string{file}, nil); len(got) != 0 {
			b.Fatalf("read %d nested rules, want none", len(got))
		}
	}
}

// BenchmarkNestedSubprojectOfThisRepository is what a tool call pays when it
// reaches a subproject that keeps a copy of this repository's own rules and
// their Claude Code mirror: Nested reads and parses the subproject's rule
// folder on every call, nothing is cached between calls.
func BenchmarkNestedSubprojectOfThisRepository(b *testing.B) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		b.Fatal(err)
	}
	root := b.TempDir()
	for _, folder := range []string{".cursor/rules", ".claude/rules"} {
		if err := copyRuleFolder(filepath.Join(repo, filepath.FromSlash(folder)), filepath.Join(root, "svc", filepath.FromSlash(folder))); err != nil {
			b.Skipf("this checkout has no %s to measure: %v", folder, err)
		}
	}
	file := filepath.Join(root, "svc", "internal", "rules", "factory.go")
	writeBenchFile(b, file)
	f := rules.DefaultFactory("")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got := f.Nested(root, []string{file}, nil)
		if len(got) == 0 {
			b.Fatal("the subproject's rules were not read")
		}
		_ = rules.MatchScoped(got, []string{file})
	}
}

// writeBenchFile creates file and the folders above it: a tool call reaches
// a file that exists, and probing an existing chain costs more than one that
// ends at its first missing folder.
func writeBenchFile(b *testing.B, file string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package x\n"), 0o644); err != nil {
		b.Fatal(err)
	}
}

// copyRuleFolder copies the regular files under src to dst.
func copyRuleFolder(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

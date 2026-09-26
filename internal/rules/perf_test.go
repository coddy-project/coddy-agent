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

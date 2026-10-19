package gitws

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitCmd is a git command of the benchmark's fixture, the user's
// configuration kept out of it.
func gitCmd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command(gitBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	return cmd
}

// MainCheckoutFolder runs on every trust decision of a turn (each project
// hook file, each project subagent definition, each project MCP server's pool
// key): it reads a few small files and never runs git.
func BenchmarkMainCheckoutFolder(b *testing.B) {
	if gitBin == "" {
		b.Skip("git binary not available to build the fixture")
	}
	main := b.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := gitCmd(main, args...).CombinedOutput(); err != nil {
			b.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	wt := filepath.Join(WorktreesRoot(main), "feature-login")
	if out, err := gitCmd(main, "worktree", "add", "-q", "-b", "feature/login", wt).CombinedOutput(); err != nil {
		b.Fatalf("git worktree add: %v\n%s", err, out)
	}
	deep := filepath.Join(wt, "pkg", "api")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		b.Fatal(err)
	}
	for name, dir := range map[string]string{"worktree": deep, "main": filepath.Join(main, "pkg"), "plain": b.TempDir()} {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				MainCheckoutFolder(dir)
			}
		})
	}
}

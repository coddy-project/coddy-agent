//go:build !windows

package rules_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

// A subfolder that is a link out of the workspace is not a subfolder of it:
// "@rule:<folder>/<name>" reads nothing there and the completion does not
// offer it, while a link that stays inside the workspace is followed.
func TestFolderRefusesALinkOutOfTheWorkspace(t *testing.T) {
	outside := t.TempDir()
	writeRuleTree(t, outside, ".coddy/rules/secret.mdc")
	root := t.TempDir()
	writeRuleTree(t, root, "real/.coddy/rules/inside.mdc")
	if err := os.Symlink(outside, filepath.Join(root, "evil")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	f := rules.DefaultFactory("")
	if got := f.Folder(root, filepath.Join(root, "evil"), nil); len(got) != 0 {
		t.Fatalf("a link out of the workspace read %v", ruleFiles(root, got))
	}
	if got := f.Folder(root, filepath.Join(root, "alias"), nil); len(got) != 1 {
		t.Fatalf("a link inside the workspace read %v, want its one rule", ruleFiles(root, got))
	}
	got := f.FoldersWithRules(root, root, nil, nil, 10)
	if len(got) != 2 || got[0] != "alias" || got[1] != "real" {
		t.Fatalf("subfolders with rules = %v, want [alias real] and never evil", got)
	}
}

// A rule folder holding a FIFO named like a rule is read without waiting on
// it: only regular files are rules.
func TestRuleFolderSkipsAFifo(t *testing.T) {
	root := t.TempDir()
	writeRuleTree(t, root, ".coddy/rules/real.mdc")
	if err := syscall.Mkfifo(filepath.Join(root, ".coddy", "rules", "pipe.mdc"), 0o644); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	done := make(chan []*rules.Rule, 1)
	go func() {
		got, _ := rules.DefaultFactory("").Discover(root, nil)
		done <- got
	}()
	select {
	case got := <-done:
		if len(got) != 1 {
			t.Fatalf("discovered %v, want the regular rule only", ruleFiles(root, got))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("discovery blocked on a FIFO in the rule folder")
	}
}

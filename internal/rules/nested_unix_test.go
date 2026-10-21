//go:build !windows

package rules_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

// Links are followed in a subfolder as they are in the workspace's own rule
// folder: a subfolder that is a link, one out of the workspace included, is
// read by "@rule:<folder>/<name>" and offered by the completion.
func TestFolderFollowsLinksLikeTheWorkspace(t *testing.T) {
	outside := t.TempDir()
	writeRuleTree(t, outside, ".coddy/rules/shared.mdc")
	root := t.TempDir()
	writeRuleTree(t, root, "real/.coddy/rules/inside.mdc")
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	f := rules.DefaultFactory("")
	for _, dir := range []string{"linked", "alias"} {
		if got := f.Folder(root, filepath.Join(root, dir), nil); len(got) != 1 {
			t.Fatalf("the link %s read %v, want its one rule", dir, ruleFiles(root, got))
		}
	}
	got := f.FoldersWithRules(root, root, nil, nil, 10)
	if len(got) != 3 || got[0] != "alias" || got[1] != "linked" || got[2] != "real" {
		t.Fatalf("subfolders with rules = %v, want [alias linked real]", got)
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

// A nested AGENTS.md, and a document of the workspace, that is a FIFO is
// passed over instead of blocking the call that reaches its folder.
func TestDocumentsSkipAFifo(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(root, "sub", "AGENTS.md"), filepath.Join(root, "AGENTS.md")} {
		if err := syscall.Mkfifo(p, 0o644); err != nil {
			t.Skipf("no FIFO here: %v", err)
		}
	}
	done := make(chan error, 1)
	go func() {
		if got := rules.AgentsForPaths(root, []string{filepath.Join(root, "sub", "x.go")}, nil); len(got) != 0 {
			done <- fmt.Errorf("a FIFO was read as a document: %v", got)
			return
		}
		done <- rules.CheckDoc(filepath.Join(root, "AGENTS.md"))
	}()
	select {
	case err := <-done:
		if !errors.Is(err, rules.ErrDocNotAFile) {
			t.Fatalf("a FIFO document: %v, want ErrDocNotAFile", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading the documents blocked on a FIFO")
	}
}

// A rule file of a subfolder that is a link is read like one of the
// workspace's own folder, wherever the link leads.
func TestSubfolderRuleLinkIsFollowed(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "shared.mdc"), []byte("SHARED"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeRuleTree(t, root, "db/.coddy/rules/own.mdc")
	if err := os.Symlink(filepath.Join(outside, "shared.mdc"), filepath.Join(root, "db", ".coddy", "rules", "shared.mdc")); err != nil {
		t.Fatal(err)
	}
	f := rules.DefaultFactory("")
	for name, got := range map[string][]*rules.Rule{
		"Folder": f.Folder(root, filepath.Join(root, "db"), nil),
		"Nested": f.Nested(root, []string{filepath.Join(root, "db", "x.go")}, nil),
	} {
		if files := ruleFiles(root, got); len(files) != 2 {
			t.Fatalf("%s read %v, want the rule and the linked one", name, files)
		}
	}
}

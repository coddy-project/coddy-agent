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
		linked := false
		for _, r := range got {
			linked = linked || (r.CanonicalName() == "shared" && r.Content == "SHARED")
		}
		if len(got) != 2 || !linked {
			t.Fatalf("%s read %v, want the rule and the linked one with its body", name, ruleFiles(root, got))
		}
	}
}

// A folder inside a rule folder that is a link is read like any other folder
// of it, its rules named by the path through the link; a link that loops
// back is read once.
func TestRuleFolderFollowsALinkedFolderInside(t *testing.T) {
	shared := t.TempDir()
	writeRuleTree(t, shared, "lint.mdc")
	root := t.TempDir()
	writeRuleTree(t, root, ".coddy/rules/own.mdc")
	rulesDir := filepath.Join(root, ".coddy", "rules")
	if err := os.Symlink(shared, filepath.Join(rulesDir, "shared")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rulesDir, filepath.Join(rulesDir, "loop")); err != nil {
		t.Fatal(err)
	}
	done := make(chan []*rules.Rule, 1)
	go func() {
		got, _ := rules.DefaultFactory("").Discover(root, nil)
		done <- got
	}()
	select {
	case got := <-done:
		names := ruleNames(got)
		if len(names) != 2 || names[0] != "own" || names[1] != "shared/lint" {
			t.Fatalf("names = %v, want [own shared/lint]", names)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a link that loops back kept the read going")
	}
}

// A link back to the workspace, or to a folder above it on the chain, is not
// a subfolder: the workspace's rules are not read a second time under it, and
// the completion does not offer it.
func TestALinkBackUpTheChainIsNoSubfolder(t *testing.T) {
	root := t.TempDir()
	writeRuleTree(t, root, ".coddy/rules/top.mdc", "db/.coddy/rules/deploy.mdc")
	if err := os.Symlink(root, filepath.Join(root, "self")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "db"), filepath.Join(root, "db", "again")); err != nil {
		t.Fatal(err)
	}
	f := rules.DefaultFactory("")
	for _, dir := range []string{"self", "db/again"} {
		if got := f.Folder(root, filepath.Join(root, filepath.FromSlash(dir)), nil); len(got) != 0 {
			t.Fatalf("Folder(%s) read %v, a folder the chain already holds", dir, ruleFiles(root, got))
		}
	}
	if got := f.Nested(root, []string{filepath.Join(root, "db", "again", "x.go")}, nil); len(got) != 1 {
		t.Fatalf("Nested through db/again read %v, want db's rule once", ruleFiles(root, got))
	}
	if got := f.FoldersWithRules(root, root, nil, nil, 10); len(got) != 1 || got[0] != "db" {
		t.Fatalf("subfolders with rules = %v, want [db]", got)
	}
	if got := f.FoldersWithRules(root, filepath.Join(root, "db"), nil, nil, 10); len(got) != 0 {
		t.Fatalf("subfolders of db with rules = %v, want none", got)
	}
}

// A rule file that is a link to a FIFO is passed over like the FIFO itself.
func TestRuleLinkToAFifoIsSkipped(t *testing.T) {
	fifoDir := t.TempDir()
	fifo := filepath.Join(fifoDir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	root := t.TempDir()
	writeRuleTree(t, root, ".coddy/rules/real.mdc")
	if err := os.Symlink(fifo, filepath.Join(root, ".coddy", "rules", "pipe.mdc")); err != nil {
		t.Fatal(err)
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
		t.Fatal("discovery blocked on a link to a FIFO")
	}
}

// A link back up the chain is passed over, not the folders below it: a file
// reached through the link still gets the rules of the real subproject on
// its way.
func TestALinkBackUpTheChainKeepsTheFoldersBelowIt(t *testing.T) {
	root := t.TempDir()
	writeRuleTree(t, root, "pkg/.coddy/rules/pkg.mdc")
	if err := os.Symlink(root, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	got := rules.DefaultFactory("").Nested(root, []string{filepath.Join(root, "alias", "pkg", "x.go")}, nil)
	if len(got) != 1 || got[0].CanonicalName() != "pkg" {
		t.Fatalf("Nested through alias/pkg read %v, want pkg's rule", ruleFiles(root, got))
	}
}

// A link inside a rule folder to another folder of the same rule folder is
// passed over: the rules keep the names of their real place.
func TestALinkWithinTheRuleFolderKeepsTheRealNames(t *testing.T) {
	root := t.TempDir()
	writeRuleTree(t, root, ".coddy/rules/frontend/style.mdc")
	if err := os.Symlink(filepath.Join(root, ".coddy", "rules", "frontend"), filepath.Join(root, ".coddy", "rules", "alias")); err != nil {
		t.Fatal(err)
	}
	got, err := rules.DefaultFactory("").Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := ruleNames(got); len(names) != 1 || names[0] != "frontend/style" {
		t.Fatalf("names = %v, want [frontend/style]", names)
	}
}

// A link from a rule folder to a large tree is read only up to a bound, so a
// link to the home folder or to / cannot hold a session start.
func TestALinkedFolderIsReadUpToABound(t *testing.T) {
	wide := t.TempDir()
	for i := 0; i < 40; i++ {
		dir := wide
		for d := 0; d < 40; d++ {
			dir = filepath.Join(dir, fmt.Sprintf("d%02d", i))
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "deep.md"), []byte("deep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	writeRuleTree(t, root, ".coddy/rules/own.mdc")
	if err := os.Symlink(wide, filepath.Join(root, ".coddy", "rules", "wide")); err != nil {
		t.Fatal(err)
	}
	got, err := rules.DefaultFactory("").Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.CanonicalName() != "own" {
			t.Fatalf("read %s, deeper than the bound", r.CanonicalName())
		}
	}
	if len(got) != 1 {
		t.Fatalf("names = %v, want [own]", ruleNames(got))
	}
}

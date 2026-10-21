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

// A rule of a subfolder whose file is a link out of the workspace is not the
// subfolder's: neither the chain of a tool call nor "@rule:<folder>/<name>"
// hands it over, while a link inside the workspace is followed.
func TestSubfolderRuleLinkedOutOfTheWorkspaceIsDropped(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeRuleTree(t, root, "db/.coddy/rules/own.mdc", "shared/common.mdc")
	for link, target := range map[string]string{
		"db/.coddy/rules/leak.mdc":   filepath.Join(outside, "secret.txt"),
		"db/.coddy/rules/common.mdc": filepath.Join(root, "shared", "common.mdc"),
	} {
		if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	f := rules.DefaultFactory("")
	for name, got := range map[string][]*rules.Rule{
		"Folder": f.Folder(root, filepath.Join(root, "db"), nil),
		"Nested": f.Nested(root, []string{filepath.Join(root, "db", "x.go")}, nil),
	} {
		files := ruleFiles(root, got)
		if len(files) != 2 || files[0] != "db/.coddy/rules/common.mdc" || files[1] != "db/.coddy/rules/own.mdc" {
			t.Fatalf("%s read %v, want the rule and the link inside the workspace only", name, files)
		}
	}
}

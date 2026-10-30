package hooks_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func loadProject(t *testing.T, store *hooks.TrustStore, cwd, home string) *hooks.Source {
	t.Helper()
	sources := hooks.NewLoader([]string{"${CWD}/.coddy/hooks.json"}, "ask").WithStore(store).Load(cwd, home)
	if len(sources) != 1 || sources[0].Scope != hooks.ScopeProject {
		t.Fatalf("sources = %+v", sources)
	}
	return sources[0]
}

// An approval of a project hooks file covers the scripts of the workspace its
// commands run: editing one asks again, as editing the file does.
func TestApprovalCoversTheScriptsAHookRuns(t *testing.T) {
	for name, command := range map[string]string{
		"relative path":        ".coddy/hooks/check.sh",
		"interpreter and path": "python3 .coddy/hooks/check.sh --strict",
		"project dir variable": `"$CLAUDE_PROJECT_DIR"/.coddy/hooks/check.sh`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			cwd := filepath.Join(root, "work")
			writeFile(t, filepath.Join(cwd, ".coddy", "hooks", "check.sh"), "#!/bin/sh\nexit 0\n")
			body := `{"hooks":{"PreToolUse":[{"matcher":"run_command","hooks":[{"type":"command","command":` + quoteJSON(command) + `}]}]}}`
			writeFile(t, filepath.Join(cwd, ".coddy", "hooks.json"), body)
			store := hooks.NewTrustStore(home)
			workspace := hooks.CanonicalWorkspace(cwd)

			src := loadProject(t, store, cwd, home)
			if err := store.Approve(workspace, src); err != nil {
				t.Fatal(err)
			}
			if !loadProject(t, store, cwd, home).Runnable() {
				t.Fatal("the approved file does not run")
			}
			writeFile(t, filepath.Join(cwd, ".coddy", "hooks", "check.sh"), "#!/bin/sh\necho changed\nexit 0\n")
			if loadProject(t, store, cwd, home).Runnable() {
				t.Fatal("an edited script still runs under the old approval")
			}
		})
	}
}

// A hooks file whose commands name no script of the workspace keeps the
// digest of the file alone, so receipts written before stay valid.
func TestADigestWithoutScriptsIsTheFilesOwn(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "work")
	body := `{"hooks":{"PreToolUse":[{"matcher":"run_command","hooks":[{"type":"command","command":"echo ok"}]}]}}`
	writeFile(t, filepath.Join(cwd, ".coddy", "hooks.json"), body)
	src := loadProject(t, hooks.NewTrustStore(home), cwd, home)
	sum := sha256.Sum256([]byte(body))
	if want := "sha256:" + hex.EncodeToString(sum[:]); src.Digest != want {
		t.Fatalf("digest = %s, want the file's own %s", src.Digest, want)
	}
}

// A script that is not there when the file is approved and appears later
// asks again too.
func TestAScriptThatAppearsLaterAsksAgain(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "work")
	body := `{"hooks":{"PreToolUse":[{"matcher":"run_command","hooks":[{"type":"command","command":"./scripts/check.sh"}]}]}}`
	writeFile(t, filepath.Join(cwd, ".coddy", "hooks.json"), body)
	store := hooks.NewTrustStore(home)
	src := loadProject(t, store, cwd, home)
	if err := store.Approve(hooks.CanonicalWorkspace(cwd), src); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cwd, "scripts", "check.sh"), "#!/bin/sh\nexit 0\n")
	if loadProject(t, store, cwd, home).Runnable() {
		t.Fatal("a script that appeared after the approval runs under it")
	}
}

// Each spelling of a command that runs a file of the checkout binds that file.
func TestApprovalCoversEverySpellingOfACommand(t *testing.T) {
	for name, command := range map[string]string{
		"subshell":          "(./tool/check.sh)",
		"backticks":         "echo `./tool/check.sh`",
		"input redirection": "sh <./tool/check.sh",
		"no space":          "sh<./tool/check.sh",
		"option value":      "runner --script=./tool/check.sh",
		"assignment":        "SCRIPT=./tool/check.sh runner",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			cwd := filepath.Join(root, "work")
			writeFile(t, filepath.Join(cwd, "tool", "check.sh"), "#!/bin/sh\nexit 0\n")
			body := `{"hooks":{"PreToolUse":[{"matcher":"run_command","hooks":[{"type":"command","command":` + quoteJSON(command) + `}]}]}}`
			writeFile(t, filepath.Join(cwd, ".coddy", "hooks.json"), body)
			store := hooks.NewTrustStore(home)
			if err := store.Approve(hooks.CanonicalWorkspace(cwd), loadProject(t, store, cwd, home)); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(cwd, "tool", "check.sh"), "#!/bin/sh\necho changed\n")
			if loadProject(t, store, cwd, home).Runnable() {
				t.Fatal("an edited script still runs under the old approval")
			}
		})
	}
}

// A file a hook writes to is not a script it runs: a log that grows with every
// run must not withdraw the approval after every run.
func TestAFileAHookWritesToKeepsTheApproval(t *testing.T) {
	for name, command := range map[string]string{
		"append":       "./check.sh >> .coddy/hook.log",
		"truncate":     "./check.sh > .coddy/hook.log",
		"no space":     "./check.sh>.coddy/hook.log",
		"stderr":       "./check.sh 2> .coddy/hook.log",
		"both streams": "./check.sh &> .coddy/hook.log",
		"after a pipe": "./check.sh | cat >> .coddy/hook.log",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			cwd := filepath.Join(root, "work")
			writeFile(t, filepath.Join(cwd, "check.sh"), "#!/bin/sh\nexit 0\n")
			writeFile(t, filepath.Join(cwd, ".coddy", "hook.log"), "one\n")
			body := `{"hooks":{"PreToolUse":[{"matcher":"run_command","hooks":[{"type":"command","command":` + quoteJSON(command) + `}]}]}}`
			writeFile(t, filepath.Join(cwd, ".coddy", "hooks.json"), body)
			store := hooks.NewTrustStore(home)
			if err := store.Approve(hooks.CanonicalWorkspace(cwd), loadProject(t, store, cwd, home)); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(cwd, ".coddy", "hook.log"), "one\ntwo\n")
			if !loadProject(t, store, cwd, home).Runnable() {
				t.Fatal("a log the hook writes withdrew its approval")
			}
			writeFile(t, filepath.Join(cwd, "check.sh"), "#!/bin/sh\necho changed\n")
			if loadProject(t, store, cwd, home).Runnable() {
				t.Fatal("an edited script still runs under the old approval")
			}
		})
	}
}

// However many files the commands name, the digest covers each of them and
// does not depend on the order the events are read in: a script past some
// count would otherwise keep its approval through any edit, and the approval
// would come and go from one load to the next.
func TestADigestCoversEveryScriptInAStableOrder(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "work")
	var events []string
	for _, event := range []string{"PreToolUse", "PostToolUse", "Stop", "UserPromptSubmit", "SessionStart"} {
		var names []string
		for i := range 20 {
			rel := fmt.Sprintf("tool/%s-%d.sh", event, i)
			writeFile(t, filepath.Join(cwd, rel), "#!/bin/sh\nexit 0\n")
			names = append(names, "./"+rel)
		}
		events = append(events, quoteJSON(event)+`:[{"hooks":[{"type":"command","command":`+quoteJSON(strings.Join(names, " && "))+`}]}]`)
	}
	writeFile(t, filepath.Join(cwd, ".coddy", "hooks.json"), `{"hooks":{`+strings.Join(events, ",")+`}}`)
	store := hooks.NewTrustStore(home)
	first := loadProject(t, store, cwd, home)
	for range 20 {
		if got := loadProject(t, store, cwd, home).Digest; got != first.Digest {
			t.Fatalf("the digest changed between loads: %s, %s", first.Digest, got)
		}
	}
	if err := store.Approve(hooks.CanonicalWorkspace(cwd), first); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreToolUse", "SessionStart"} {
		writeFile(t, filepath.Join(cwd, "tool", event+"-19.sh"), "#!/bin/sh\necho changed\n")
		if loadProject(t, store, cwd, home).Runnable() {
			t.Fatalf("%s-19.sh was edited and still runs under the old approval", event)
		}
		writeFile(t, filepath.Join(cwd, "tool", event+"-19.sh"), "#!/bin/sh\nexit 0\n")
	}
}

func quoteJSON(s string) string {
	out := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\', byte(r))
		default:
			out = append(out, string(r)...)
		}
	}
	return string(append(out, '"'))
}

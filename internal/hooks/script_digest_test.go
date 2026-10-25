package hooks_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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

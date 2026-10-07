package skills_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The crossreview detection script is the first-run entry point on a machine
// without Python: it must name exactly the CLIs that are installed and
// verified, never the full candidate list, and take its templates from the
// agents.tsv table it ships with.
func TestDetectAgentsScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("detect-agents.sh is the POSIX half; Windows uses detect-agents.ps1")
	}
	if _, err := exec.LookPath("tr"); err != nil {
		t.Skip("detect-agents.sh needs tr(1), which this host lacks")
	}
	bin := t.TempDir()
	stub := func(name, banner string, rc int) {
		body := "#!/bin/sh\ncase \"$1\" in --version|--help) echo '" + banner + "' ;; esac\nexit " +
			string(rune('0'+rc)) + "\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("claude", "2.1.285 (Claude Code)", 0) // no models command
	stub("codex", "codex-cli 0.157.1", 0)      // every probe exits 0, so its models_cmd is reported
	stub("devin", "devin 3000.11.3", 1)        // a name on PATH that is not the agent: --version fails
	stub("agent", "some other tool", 0)        // an unrelated `agent`: no Cursor marker in its banner
	// opencode, cursor-agent, koda deliberately absent.

	// The script needs tr, and timeout or sleep; nothing else may leak in from
	// the host PATH, where the real agents live.
	tools := t.TempDir()
	for _, name := range []string{"tr", "timeout", "sleep"} {
		if path, err := exec.LookPath(name); err == nil {
			if err := os.Symlink(path, filepath.Join(tools, name)); err != nil {
				t.Fatal(err)
			}
		}
	}

	cmd := exec.Command("sh", "bundled/crossreview/scripts/detect-agents.sh")
	cmd.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + tools}
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("detect-agents.sh: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	got := map[string][]string{}
	for _, ln := range lines {
		f := strings.Split(ln, "\t")
		if len(f) != 4 {
			t.Fatalf("line %q is not agent<TAB>path<TAB>models_cmd<TAB>template", ln)
		}
		got[f[0]] = f
	}
	if len(got) != 2 {
		t.Fatalf("detected agents = %v, want claude and codex only", lines)
	}
	if got["claude"][1] != filepath.Join(bin, "claude") || got["claude"][2] != "" {
		t.Fatalf("claude row = %v", got["claude"])
	}
	if got["codex"][2] != "codex debug models" {
		t.Fatalf("codex models_cmd = %q", got["codex"][2])
	}
	for _, row := range got {
		tpl := row[3]
		for _, placeholder := range []string{"{model}", "{brief}", "{out}"} {
			if !strings.Contains(tpl, placeholder) {
				t.Fatalf("template keeps the %s placeholder: %q", placeholder, tpl)
			}
		}
		// A brief over 128 KB does not fit in one argument: it goes through
		// stdin or a file flag, never "$(cat {brief})" or backticks.
		if strings.ContainsAny(tpl, "`") || strings.Contains(tpl, "$(") || strings.Contains(tpl, "{bin}") {
			t.Fatalf("template %q passes the brief as an argument or kept {bin}", tpl)
		}
		if !strings.Contains(tpl, "< {brief}") && !strings.Contains(tpl, "-i {brief}") &&
			!strings.Contains(tpl, "--prompt-file {brief}") {
			t.Fatalf("template %q neither redirects the brief into stdin nor names it with a file flag", tpl)
		}
	}
}

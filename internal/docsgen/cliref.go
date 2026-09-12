package docsgen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cliSection is one help screen of the binary: the arguments that print it
// and the heading it gets in the reference.
type cliSection struct {
	Title string
	Args  []string
}

// cliSections lists the help screens that make up the CLI reference. Only
// commands that answer --help with their own text are here; the verbs that
// print a one-line usage are already in the synopsis.
var cliSections = []cliSection{
	{"coddy", []string{"--help"}},
	{"coddy cli", []string{"cli", "--help"}},
	{"coddy acp", []string{"acp", "--help"}},
	{"coddy serve", []string{"serve", "--help"}},
	{"coddy serve status | stop | restart", []string{"serve", "status", "--help"}},
	{"coddy sessions list", []string{"sessions", "list", "--help"}},
	{"coddy sessions export", []string{"sessions", "export", "--help"}},
	{"coddy providers", []string{"providers", "list", "--help"}},
	{"coddy plugin", []string{"plugin", "--help"}},
	{"coddy update", []string{"update", "--help"}},
}

// BuildCoddy compiles the binary the reference is generated from into dir.
func BuildCoddy(root, dir, tags string) (string, error) {
	out := filepath.Join(dir, "coddy")
	args := []string{"build"}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	args = append(args, "-o", out, "./cmd/coddy")
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, b)
	}
	return out, nil
}

// CLIReference renders the generated block of docs/reference/cli.md by
// running every help screen of binary. Exit codes are ignored: the flag
// package exits 2 after printing its usage, and that text is the point. The
// block opens with the build tags the binary was made with, because the
// synopsis and the console flags differ between builds.
func CLIReference(binary, tags string) (string, error) {
	var b strings.Builder
	if tags == "" {
		b.WriteString("Help screens of the default build (no build tags).\n\n")
	} else {
		fmt.Fprintf(&b, "Help screens of a binary built with `-tags=%s`, the set the release binaries carry.\n\n", tags)
	}
	for i, s := range cliSections {
		cmd := exec.Command(binary, s.Args...)
		cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb", "CODDY_HOME="+os.TempDir())
		out, _ := cmd.CombinedOutput()
		text := strings.TrimRight(string(out), "\n")
		text = strings.ReplaceAll(text, binary, "coddy")
		if text == "" {
			return "", fmt.Errorf("%s printed nothing", strings.Join(append([]string{"coddy"}, s.Args...), " "))
		}
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "### %s\n\n```text\n%s\n```\n", s.Title, text)
	}
	return b.String(), nil
}

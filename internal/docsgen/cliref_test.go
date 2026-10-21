package docsgen

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIReferenceIsolatesHelpEnvironment(t *testing.T) {
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "cli-reference-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("CODDY_DOCSGEN_HELPER", "1")
	previous := cliSections
	t.Cleanup(func() { cliSections = previous })
	cliSections = []cliSection{{Title: "coddy", Args: []string{"-test.run=^TestCLIReferenceHelper$"}}}
	got, err := CLIReference("./"+name, "http,ui")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Usage: coddy\n") || strings.Contains(got, dir) || strings.Contains(got, name) {
		t.Fatalf("the CLI reference depends on the host environment:\n%s", got)
	}
}

func TestCLIReferenceHelper(t *testing.T) {
	if os.Getenv("CODDY_DOCSGEN_HELPER") != "1" {
		t.Skip("helper process")
	}
	home := os.Getenv("CODDY_HOME")
	if home == "" || home == os.TempDir() {
		fmt.Println("help would write into the shared system temporary directory")
		os.Exit(0)
	}
	if err := os.WriteFile(filepath.Join(home, "help-state"), []byte("isolated"), 0o600); err != nil {
		fmt.Printf("help home is not writable: %v\n", err)
		os.Exit(0)
	}
	fmt.Printf("Usage: %s\n", os.Args[0])
	os.Exit(0)
}

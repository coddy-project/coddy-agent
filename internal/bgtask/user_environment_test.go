package bgtask

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func TestCommandRunnerCancellationPreventsCommandStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash is not available on Windows")
	}
	home := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	runner := &CommandRunner{Shell: platform.Shell{Kind: platform.ShellBash, Path: "/bin/bash"}}
	handle, err := runner.Start(Spec{Context: ctx, Command: `printf started`, CWD: home}, &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want context.Canceled", err)
	}
	if handle != nil {
		t.Fatal("Start returned a handle for a cancelled command")
	}
}

func TestCommandRunnerReadsCurrentBashEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash is not available on Windows")
	}
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("/bin/bash is not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("export PATH=/background/bin:$PATH\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	runner := &CommandRunner{Shell: platform.Shell{Kind: platform.ShellBash, Path: "/bin/bash"}}
	handle, err := runner.Start(Spec{Command: `printf '%s' "$PATH"`, CWD: home}, &out)
	if err != nil {
		t.Fatal(err)
	}
	code, err := handle.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("background command exit code = %d, output %q", code, out.String())
	}
	if got := out.String(); !strings.HasPrefix(got, "/background/bin:") {
		t.Fatalf("background PATH = %q, want the path exported by .bashrc", got)
	}
}

package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

type shellUserEnvironmentState struct {
	shellSpecialCharsState
	home string
}

func (s *shellUserEnvironmentState) temporaryBash(t *testing.T) error {
	s.shell = platform.Shell{Kind: platform.ShellBash, Path: "/bin/bash"}
	s.home = t.TempDir()
	t.Setenv("HOME", s.home)
	return nil
}

func (s *shellUserEnvironmentState) bashrcAddsToPATH(path string) error {
	rcfile := filepath.Join(s.home, ".bashrc")
	return os.WriteFile(rcfile, []byte("export PATH="+path+":$PATH\n"), 0o600)
}

func (s *shellUserEnvironmentState) printPATH() error {
	return s.run(`printf '%s' "$PATH"`)
}

func (s *shellUserEnvironmentState) outputStartsWith(want string) error {
	if !strings.HasPrefix(s.out, want) {
		return fmt.Errorf("output %q does not start with %q", s.out, want)
	}
	return nil
}

func TestShellUserEnvironmentFeature(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash is not available on Windows")
	}
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("/bin/bash is not available")
	}
	s := &shellUserEnvironmentState{}
	suite := godog.TestSuite{
		Name: "shell-user-environment",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				s.reset()
				return ctx, nil
			})
			sc.Step(`^Bash with a temporary home directory$`, func() error { return s.temporaryBash(t) })
			sc.Step(`^the Bash startup file adds "([^"]*)" to PATH$`, s.bashrcAddsToPATH)
			sc.Step(`^I run a command that prints PATH$`, s.printPATH)
			sc.Step(`^the output starts with "([^"]*)"$`, s.outputStartsWith)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/shell_user_environment.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("shell user environment feature suite failed")
	}
}

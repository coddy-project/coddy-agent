package serve

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// SetupUserService enables the packaged systemd user unit and reports its status.
func SetupUserService(ctx context.Context, out io.Writer) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("coddy serve setup requires Linux and systemd")
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("run coddy serve setup as the user who will own the service, without sudo")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find user home: %w", err)
	}
	if err := config.RunCheck(out, config.CLIPaths{Home: filepath.Join(home, ".coddy")}); err != nil {
		return fmt.Errorf("check ~/.coddy/config.yaml before enabling the service: %w", err)
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl is required for coddy serve setup: %w", err)
	}
	run := func(args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	}
	return setupUserService(run, out)
}

func setupUserService(run func(...string) ([]byte, error), out io.Writer) error {
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", "coddy.service"},
		{"--user", "status", "--no-pager", "coddy.service"},
	} {
		result, err := run(args...)
		if err != nil {
			return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(result)))
		}
		if args[1] == "status" {
			_, _ = out.Write(result)
		}
	}
	return nil
}

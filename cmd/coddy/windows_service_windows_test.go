//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func TestPrepareWindowsServiceEnvironment(t *testing.T) {
	home := filepath.Join(t.TempDir(), "service-home")
	t.Setenv(config.EnvCODDYCWD, "")
	t.Setenv(serve.EnvRole, "")
	args := []string{"--home", home, "--config", filepath.Join(home, "config.yaml")}
	if err := prepareWindowsServiceEnvironment(args); err != nil {
		t.Fatal(err)
	}
	if got := serve.Role(); got != serve.RoleService || !serve.Supervised() {
		t.Fatalf("service role = %q, supervised = %v", got, serve.Supervised())
	}
	if got, want := os.Getenv(config.EnvCODDYCWD), serve.WindowsServiceWorkspace(home); got != want {
		t.Fatalf("workspace = %q, want %q", got, want)
	}
}

func TestPrepareWindowsServiceEnvironmentKeepsExplicitWorkspace(t *testing.T) {
	t.Setenv(config.EnvCODDYCWD, `C:\work`)
	if err := prepareWindowsServiceEnvironment([]string{"--home", `C:\service-home`}); err != nil {
		t.Fatal(err)
	}
}

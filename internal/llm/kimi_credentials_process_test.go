package llm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func TestKimiCredentialCrossProcessLogoutInvalidatesAttempt(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("durable locking unavailable")
	}
	dir := privateKimiTestDir(t)
	s := NewKimiCredentialStore(dir)
	attempt, err := s.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestKimiCredentialLogoutSubprocess$")
	platform.AdaptCommand(cmd)
	cmd.Env = append(os.Environ(), "KIMI_LOGOUT_HELPER="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("logout helper: %v %s", err, out)
	}
	if err := s.CompleteLogin(context.Background(), "global", attempt, kimiTestCredential("stale", "refresh", 0)); !errors.Is(err, ErrKimiLoginAttemptStale) {
		t.Fatalf("completion after subprocess logout: %v", err)
	}
	if _, state, err := s.Load("global"); err != nil || state != KimiCredentialMissing {
		t.Fatalf("state=%v err=%v", state, err)
	}
}

func TestKimiCredentialLogoutSubprocess(t *testing.T) {
	dir := os.Getenv("KIMI_LOGOUT_HELPER")
	if dir == "" {
		return
	}
	if runtime.GOOS == "windows" {
		t.Fatal("unsupported")
	}
	if err := NewKimiCredentialStore(dir).Remove("global"); err != nil {
		t.Fatal(err)
	}
}

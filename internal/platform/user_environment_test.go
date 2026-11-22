package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUserEnvironmentReadsChangedBashrc(t *testing.T) {
	requireBash(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	rcfile := filepath.Join(home, ".bashrc")
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(rcfile, []byte("export PATH="+path+":$PATH\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("/first/bin")
	first := environmentValue(UserEnvironment(context.Background(), home), "PATH")
	if !strings.HasPrefix(first, "/first/bin:") {
		t.Fatalf("first PATH = %q, want the path exported by .bashrc", first)
	}

	write("/second/bin")
	second := environmentValue(UserEnvironment(context.Background(), home), "PATH")
	if !strings.HasPrefix(second, "/second/bin:") {
		t.Fatalf("second PATH = %q, want the updated path exported by .bashrc", second)
	}
}

func TestUserEnvironmentDoesNotNeedExternalEnv(t *testing.T) {
	requireBash(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("name", "original")
	rcfile := "export PATH=/definitely-no-env\nexport CODDY_USER_ENV_PROBE=bash-builtins\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(rcfile), 0o600); err != nil {
		t.Fatal(err)
	}

	env := UserEnvironment(context.Background(), home)
	if got := environmentValue(env, "PATH"); got != "/definitely-no-env" {
		t.Fatalf("PATH = %q, want the value exported by .bashrc", got)
	}
	if got := environmentValue(env, "CODDY_USER_ENV_PROBE"); got != "bash-builtins" {
		t.Fatalf("CODDY_USER_ENV_PROBE = %q, want bash-builtins", got)
	}
	if got := environmentValue(env, "name"); got != "original" {
		t.Fatalf("name = %q, want the exported value to survive enumeration", got)
	}
}

func TestUserEnvironmentAcceptsCompleteOutputAfterWaitDelay(t *testing.T) {
	requireBash(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("sleep 2 &\nexport CODDY_USER_ENV_PROBE=pipes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	env := UserEnvironment(context.Background(), home)
	if got := environmentValue(env, "CODDY_USER_ENV_PROBE"); got != "pipes" {
		t.Fatalf("CODDY_USER_ENV_PROBE = %q, want pipes despite an inherited output pipe", got)
	}
}

func TestUserEnvironmentIgnoresStartupOutputBeforeBoundary(t *testing.T) {
	requireBash(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	rcfile := "printf 'noise\\0FAKE=injected\\0'\nexport CODDY_USER_ENV_PROBE=real\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(rcfile), 0o600); err != nil {
		t.Fatal(err)
	}

	env := UserEnvironment(context.Background(), home)
	if got := environmentValue(env, "FAKE"); got != "" {
		t.Fatalf("FAKE = %q, want startup output to be ignored", got)
	}
	if got := environmentValue(env, "CODDY_USER_ENV_PROBE"); got != "real" {
		t.Fatalf("CODDY_USER_ENV_PROBE = %q, want real", got)
	}
}

func TestUserEnvironmentPreservesInheritedNonIdentifierEntries(t *testing.T) {
	requireBash(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("foo.bar", "kept")

	env := UserEnvironment(context.Background(), home)
	if got := environmentValue(env, "foo.bar"); got != "kept" {
		t.Fatalf("foo.bar = %q, want the inherited value", got)
	}
}

func TestUserEnvironmentTimeoutReapsStartupChildren(t *testing.T) {
	requireBash(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	rcfile := "sleep 60 &\nprintf '%s' \"$!\" > child.pid\nwait\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(rcfile), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan []string, 1)
	go func() {
		result <- UserEnvironment(ctx, home)
	}()

	var pidBytes []byte
	deadline := time.Now().Add(5 * time.Second)
	for {
		var err error
		pidBytes, err = os.ReadFile(filepath.Join(home, "child.pid"))
		if err == nil && len(pidBytes) > 0 {
			break
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("startup child did not write its PID")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case env := <-result:
		if got := environmentValue(env, "PATH"); got != "/usr/bin:/bin" {
			t.Fatalf("PATH = %q, want the fallback environment", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not return after cancellation")
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("kill startup child: %v", err)
		}
	}()
	deadline = time.Now().Add(2 * time.Second)
	for processAlive(process, pid) {
		if time.Now().After(deadline) {
			t.Fatalf("startup child %d survived the probe timeout", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processAlive(process *os.Process, pid int) bool {
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	if runtime.GOOS != "linux" {
		return true
	}
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	closingParen := strings.LastIndexByte(string(stat), ')')
	return closingParen < 0 || len(strings.Fields(string(stat)[closingParen+1:])) == 0 || strings.Fields(string(stat)[closingParen+1:])[0] != "Z"
}

func TestUserEnvironmentFallsBackWhenStartupFileExits(t *testing.T) {
	requireBash(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("CODDY_USER_ENV_FALLBACK", "process")
	rcfile := "export CODDY_USER_ENV_FALLBACK=bashrc\nexit 1\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(rcfile), 0o600); err != nil {
		t.Fatal(err)
	}

	env := UserEnvironment(context.Background(), home)
	if got := environmentValue(env, "CODDY_USER_ENV_FALLBACK"); got != "process" {
		t.Fatalf("CODDY_USER_ENV_FALLBACK = %q, want the process environment", got)
	}
}

func requireBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Bash is not available on Windows")
	}
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("/bin/bash is not available")
	}
}

func environmentValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

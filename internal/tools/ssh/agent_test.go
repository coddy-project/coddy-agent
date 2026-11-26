package ssh

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAgentAuth_NoSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Ensure SSH_AUTH_SOCK is unset for this test.
	orig := os.Getenv("SSH_AUTH_SOCK")
	if err := os.Unsetenv("SSH_AUTH_SOCK"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if orig != "" {
			_ = os.Setenv("SSH_AUTH_SOCK", orig)
		}
	})

	method, closer, err := agentAuth()
	if err != nil {
		t.Errorf("agentAuth() error = %v, want nil", err)
	}
	if method != nil {
		t.Error("agentAuth() method should be nil when SSH_AUTH_SOCK is not set")
	}
	if closer != nil {
		t.Error("agentAuth() closer should be nil when SSH_AUTH_SOCK is not set")
	}
}

func TestAgentAuth_BadSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Point SSH_AUTH_SOCK at a path that does not exist.
	orig := os.Getenv("SSH_AUTH_SOCK")
	if err := os.Setenv("SSH_AUTH_SOCK", "/tmp/coddy-test-nonexistent.sock"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if orig != "" {
			_ = os.Setenv("SSH_AUTH_SOCK", orig)
		} else {
			_ = os.Unsetenv("SSH_AUTH_SOCK")
		}
	})

	// Should not return an error — unreachable agent is a soft failure.
	method, closer, err := agentAuth()
	if err != nil {
		t.Errorf("agentAuth() error = %v, want nil (soft failure)", err)
	}
	if method != nil {
		t.Error("agentAuth() should return nil method for bad socket")
	}
	if closer != nil {
		t.Error("agentAuth() should return nil closer for bad socket")
	}
}

func TestAgentAuthReadsCurrentBashEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Bash and Unix sockets are not available on Windows")
	}
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("/bin/bash is not available")
	}
	home := t.TempDir()
	socket := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(home, "stale.sock"))
	rcfile := "export SSH_AUTH_SOCK=" + socket + "\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(rcfile), 0o600); err != nil {
		t.Fatal(err)
	}

	method, closer, err := agentAuth()
	if err != nil {
		t.Fatal(err)
	}
	if method == nil || closer == nil {
		t.Fatal("agentAuth did not use the socket exported by the current .bashrc")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildAuthMethods_NoAgentNoKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := os.Getenv("SSH_AUTH_SOCK")
	if err := os.Unsetenv("SSH_AUTH_SOCK"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if orig != "" {
			_ = os.Setenv("SSH_AUTH_SOCK", orig)
		}
	})

	methods, closer := buildAuthMethods(t.TempDir()) // empty dir, no keys
	if closer != nil {
		t.Error("closer should be nil when no agent")
		_ = closer.Close()
	}
	if len(methods) != 0 {
		t.Errorf("buildAuthMethods() = %d methods, want 0", len(methods))
	}
}

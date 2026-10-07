//go:build !windows

package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const (
	treePIDFileEnv = "CODDY_TEST_TREE_PIDFILE"
	treeSleeperEnv = "CODDY_TEST_TREE_SLEEPER"
)

// TestHelperPoolTreeServer stands in for a server a package runner starts:
// it spawns a child of its own that shares its stdout and does not read
// stdin - the node under `npx -y <package>` - records both pids, and then
// serves MCP until its stdin closes, when it exits and leaves the child.
func TestHelperPoolTreeServer(t *testing.T) {
	pidFile := os.Getenv(treePIDFileEnv)
	if pidFile == "" {
		t.Skip("helper process")
	}
	child := exec.Command(os.Args[0], "-test.run=^TestHelperPoolTreeSleeper$")
	child.Env = append(os.Environ(), treeSleeperEnv+"=1", treePIDFileEnv+"=")
	child.Stdout = os.Stdout
	if err := child.Start(); err != nil {
		os.Exit(5)
	}
	_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d %d", os.Getpid(), child.Process.Pid)), 0o644)
	runFakeMCPServer()
	os.Exit(0)
}

// TestHelperPoolTreeSleeper is the child of TestHelperPoolTreeServer.
func TestHelperPoolTreeSleeper(t *testing.T) {
	if os.Getenv(treeSleeperEnv) != "1" {
		t.Skip("helper process")
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

// processRuns reports whether pid names a process that has not exited; a
// zombie waiting for its parent to reap it has exited.
func processRuns(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	if runtime.GOOS == "linux" {
		if status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil &&
			strings.Contains(string(status), "State:\tZ") {
			return false
		}
	}
	return true
}

// Closing a stdio server stops what it started as well: the server runs in
// a process group of its own, and the group is terminated once the server
// had its chance to exit on stdin EOF (issue #379).
func TestClosedStdioServerLeavesNoChildBehind(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	client, err := NewStdioClient(testCtx(t), "tree", os.Args[0], []string{"-test.run=^TestHelperPoolTreeServer$"},
		[]string{treePIDFileEnv + "=" + pidFile}, slog.Default())
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	tr := client.conn.tr.(*stdioTransport)
	tr.exitGrace, tr.termGrace = 200*time.Millisecond, time.Second
	var leader, child int
	if !eventually(t, 5*time.Second, func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		_, err = fmt.Sscanf(string(data), "%d %d", &leader, &child)
		return err == nil
	}) {
		_ = client.Close()
		t.Fatal("the server did not record its pids")
	}
	if !processRuns(child) {
		t.Fatal("the child is not running before Close")
	}
	_ = client.Close()
	if !eventually(t, 10*time.Second, func() bool { return !processRuns(leader) && !processRuns(child) }) {
		t.Fatalf("after Close: server running = %v, its child running = %v", processRuns(leader), processRuns(child))
	}
}

// Close cancels a start still in flight and returns only once that server is
// gone, so a process on its way out leaves no server it was starting behind.
func TestPoolCloseStopsAStartInFlight(t *testing.T) {
	dir := t.TempDir()
	record, pidFile := filepath.Join(dir, "record"), filepath.Join(dir, "pid")
	pool := NewPool(slog.Default())
	srv := globalServer(poolStub("starting", record,
		config.EnvVarConfig{Name: poolHoldEnv, Value: filepath.Join(dir, "never")},
		config.EnvVarConfig{Name: poolPIDEnv, Value: pidFile}))
	go func() { _, _ = pool.Acquire(context.Background(), srv, "") }()
	var pid int
	if !eventually(t, 10*time.Second, func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		_, err = fmt.Sscanf(string(data), "%d", &pid)
		return err == nil
	}) {
		t.Fatal("the server did not start")
	}
	pool.Close()
	if processRuns(pid) {
		t.Fatal("Close returned while the server it was starting still ran")
	}
}

// Closing a connection whose server stopped reading stdin returns at once,
// and a write stuck on the full pipe fails instead of holding the close up.
func TestCloseDoesNotWaitForAWriteStuckOnAServerThatStoppedReading(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	client, err := NewStdioClient(testCtx(t), "stuck", os.Args[0], []string{"-test.run=^TestHelperPoolServer$"},
		[]string{poolRecordEnv + "=" + record}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	tr := client.conn.tr.(*stdioTransport)
	tr.exitGrace, tr.termGrace = 200*time.Millisecond, time.Second
	if _, err := client.CallTool(testCtx(t), "stall", `{}`); err != nil {
		t.Fatal(err)
	}
	big := `{"text":"` + strings.Repeat("x", 1<<20) + `"}`
	stuck := make(chan error, 1)
	go func() {
		_, err := client.CallTool(context.Background(), "echo", big)
		stuck <- err
	}()
	time.Sleep(300 * time.Millisecond)
	closed := make(chan struct{})
	go func() {
		_ = client.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close waited for a write stuck on a server that stopped reading")
	}
	select {
	case err := <-stuck:
		if err == nil {
			t.Fatal("the stuck call succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stuck call never failed")
	}
	// Close only starts the shutdown. The stub sleeps for an hour without
	// reading stdin, so it is stopped by its group being terminated, which a
	// test binary that exits first never gets to: wait for it here.
	select {
	case <-tr.Stopped():
	case <-time.After(10 * time.Second):
		t.Fatal("the server that stopped reading was never stopped")
	}
}

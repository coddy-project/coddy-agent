//go:build desktop

package shell

import (
	"os/exec"
	"testing"
)

// The shell is plain JavaScript. These tests execute its real event handlers
// with controllable frames, clocks and HTTP answers rather than checking that
// the source contains a particular implementation.
func TestShellBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required to execute the desktop shell behavior tests")
	}
	cmd := exec.Command(node, "--test", "testdata/shell_behavior.test.mjs")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("desktop shell behavior failed: %v\n%s", err, output)
	}
	t.Logf("%s", output)
}

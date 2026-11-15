//go:build desktop && freebsd

package desktop

import (
	"os"
	"testing"
)

// parentOf is the one piece of this platform that reads the kernel by hand:
// a kern.proc.pid sysctl whose reply is decoded at offsets written down here
// rather than produced by a generator. Cross-compiling proves the file builds
// and nothing more, so this test runs in the FreeBSD virtual machine of the
// pull-request checks, where the process it asks about is the test itself.
func TestParentOfAnswersTheRealParent(t *testing.T) {
	got, ok := parentOf(os.Getpid())
	if !ok {
		t.Fatalf("parentOf(%d) failed for this very process: the kinfo_proc offsets no longer match the kernel", os.Getpid())
	}
	if want := os.Getppid(); got != want {
		t.Fatalf("parentOf(%d) = %d, want %d", os.Getpid(), got, want)
	}
}

// A pid nothing owns has no kinfo_proc to decode, and the walk up the tree
// must end there rather than answer with whatever the buffer held.
func TestParentOfRefusesAProcessThatIsNotThere(t *testing.T) {
	// pid_t is a signed 32-bit integer and the kernel never hands out the
	// maximum, so this asks about a process that cannot exist.
	if _, ok := parentOf(1<<31 - 1); ok {
		t.Fatal("parentOf answered for a pid that cannot exist")
	}
}

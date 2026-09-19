//go:build cli && !windows

package cli

import (
	"io"
	"os"
	"syscall"
	"testing"
)

// A socket on stdin is what a program that spawned coddy with a pipe of its
// own (Node's child_process, for one) leaves there: never written, often never
// closed. It is not taken for piped data, so a text prompt runs without
// waiting on it; "-p -" still reads it.
func TestReadPrintInputDoesNotAttachASocket(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Skipf("socketpair: %v", err)
	}
	ours := os.NewFile(uintptr(fds[0]), "stdin-socket")
	theirs := os.NewFile(uintptr(fds[1]), "writer-socket")
	defer func() { _ = ours.Close(); _ = theirs.Close() }()

	in, err := readPrintInput(parsedRequest(t, "-p", "review"), printInputEnv{
		Stdin: ours, Notices: io.Discard, IsTerminal: func(*os.File) bool { return false }, Limit: MaxPromptInputBytes,
	})
	if err != nil || in.Stdin != "" {
		t.Fatalf("a socket stdin was attached: %q, %v", in.Stdin, err)
	}

	go func() {
		_, _ = io.WriteString(theirs, "asked for explicitly\n")
		_ = theirs.Close()
	}()
	in, err = readPrintInput(parsedRequest(t, "-p", "-"), printInputEnv{
		Stdin: ours, Notices: io.Discard, IsTerminal: func(*os.File) bool { return false }, Limit: MaxPromptInputBytes,
	})
	if err != nil || in.Prompt != "asked for explicitly\n" {
		t.Fatalf("-p - over a socket = %q, %v", in.Prompt, err)
	}
}

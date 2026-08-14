//go:build cli && !windows

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

// watchResize refreshes the terminal size on SIGWINCH.
func watchResize(t *ProcessTerminal) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-t.stopCh:
				signal.Stop(ch)
				return
			case <-ch:
				t.refreshSize()
			}
		}
	}()
}

// enableVT is a no-op on POSIX terminals.
func enableVT() error { return nil }

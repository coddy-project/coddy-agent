//go:build cli && windows

package tui

import (
	"time"

	"golang.org/x/sys/windows"
)

// watchResize polls the console size (Windows has no SIGWINCH).
func watchResize(t *ProcessTerminal) {
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-t.stopCh:
				return
			case <-ticker.C:
				t.refreshSize()
			}
		}
	}()
}

// enableVT turns on VT input and output processing so ANSI sequences work in
// conhost and Windows Terminal.
func enableVT() error {
	out := windows.Handle(windows.Stdout)
	var mode uint32
	if err := windows.GetConsoleMode(out, &mode); err == nil {
		_ = windows.SetConsoleMode(out, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
	in := windows.Handle(windows.Stdin)
	if err := windows.GetConsoleMode(in, &mode); err == nil {
		_ = windows.SetConsoleMode(in, mode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	}
	return nil
}

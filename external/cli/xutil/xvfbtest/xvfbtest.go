//go:build cli

// Package xvfbtest starts a private X server (Xvfb) for a test and drives it
// with XTEST the way a person would: pointer moves, clicks and key presses.
// A test on a machine without Xvfb is skipped, never failed.
package xvfbtest

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"

	"github.com/EvilFreelancer/coddy-agent/external/cli/xutil"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// Start runs Xvfb on a free display for the life of the test and returns
// its name (":N").
func Start(t testing.TB) string {
	t.Helper()
	bin, err := exec.LookPath("Xvfb")
	if err != nil {
		t.Skip("Xvfb is not installed")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Xvfb writes the display number it picked to fd 3 once it listens.
	// -noreset: the server must outlive a test client that disconnects first.
	cmd := exec.Command(bin, "-displayfd", "3", "-screen", "0", "1600x1000x24", "-nolisten", "tcp", "-noreset")
	cmd.ExtraFiles = []*os.File{w}
	platform.AdaptCommand(cmd)
	if err := cmd.Start(); err != nil {
		t.Skipf("Xvfb did not start: %v", err)
	}
	_ = w.Close()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = r.Close()
	})
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(r).ReadString('\n')
		got <- strings.TrimSpace(line)
	}()
	select {
	case n := <-got:
		if n == "" {
			t.Skip("Xvfb exited before it named its display")
		}
		return ":" + n
	case <-time.After(15 * time.Second):
		t.Skip("Xvfb did not name its display within 15s")
	}
	return ""
}

// Driver is a second client of the display that plays the person.
type Driver struct {
	t testing.TB
	C *xutil.Conn
}

// Drive connects to the display with XTEST.
func Drive(t testing.TB, display string) *Driver {
	t.Helper()
	c, err := xutil.Open(display)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	if err := xtest.Init(c.Conn); err != nil {
		t.Skipf("the X server has no XTEST: %v", err)
	}
	return &Driver{t: t, C: c}
}

// sync waits until the server has handled everything sent so far.
func (d *Driver) sync() {
	_, _ = xproto.GetInputFocus(d.C.Conn).Reply()
}

// Move puts the pointer at a root position.
func (d *Driver) Move(x, y int) {
	xtest.FakeInput(d.C.Conn, xproto.MotionNotify, 0, 0, d.C.Screen.Root, int16(x), int16(y), 0)
	d.sync()
}

// Press and Release work the first button.
func (d *Driver) Press() {
	xtest.FakeInput(d.C.Conn, xproto.ButtonPress, 1, 0, d.C.Screen.Root, 0, 0, 0)
	d.sync()
}

func (d *Driver) Release() {
	xtest.FakeInput(d.C.Conn, xproto.ButtonRelease, 1, 0, d.C.Screen.Root, 0, 0, 0)
	d.sync()
}

// Click moves to a root position and clicks there.
func (d *Driver) Click(x, y int) {
	d.Move(x, y)
	d.Press()
	d.Release()
}

// Key presses and releases a keycode.
func (d *Driver) Key(code xproto.Keycode) {
	xtest.FakeInput(d.C.Conn, xproto.KeyPress, byte(code), 0, d.C.Screen.Root, 0, 0, 0)
	xtest.FakeInput(d.C.Conn, xproto.KeyRelease, byte(code), 0, d.C.Screen.Root, 0, 0, 0)
	d.sync()
}

// Keycode finds the key whose first symbol is sym, or binds a spare key to
// it when none is, so a test can type a character the server's keymap does
// not have (Cyrillic on a bare Xvfb). Bind keys before the client under test
// reads the keymap.
func (d *Driver) Keycode(sym xproto.Keysym) xproto.Keycode {
	d.t.Helper()
	minK, maxK := d.C.Setup.MinKeycode, d.C.Setup.MaxKeycode
	r, err := xproto.GetKeyboardMapping(d.C.Conn, minK, byte(maxK-minK+1)).Reply()
	if err != nil {
		d.t.Fatal(err)
	}
	per := int(r.KeysymsPerKeycode)
	spare := xproto.Keycode(0)
	for i := 0; i <= int(maxK-minK); i++ {
		row := r.Keysyms[i*per : (i+1)*per]
		if row[0] == sym {
			return minK + xproto.Keycode(i)
		}
		empty := true
		for _, s := range row {
			if s != 0 {
				empty = false
			}
		}
		if empty && spare == 0 {
			spare = minK + xproto.Keycode(i)
		}
	}
	if spare == 0 {
		d.t.Fatalf("no key types keysym %#x and none is free", sym)
	}
	row := make([]xproto.Keysym, per)
	row[0] = sym
	if err := xproto.ChangeKeyboardMappingChecked(d.C.Conn, 1, spare, byte(per), row).Check(); err != nil {
		d.t.Fatal(err)
	}
	return spare
}

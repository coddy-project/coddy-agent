//go:build desktop

package desktop

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

// The desktop remembers the loopback port it serves on, and this is not a
// convenience: a browser scopes localStorage to the origin, and the origin
// includes the port. A fresh port every launch would throw away the tab list
// the shell is there to restore, and with it everything else the SPA keeps per
// origin - the flag that says the first-run tour has been seen, the selected
// environment, the drafts. Cookies are scoped to the host instead, which is
// why the theme survives a moving port and nothing else does.
//
// Remembering it buys two more things. The launcher knows the address before
// it starts anything, so readiness is a dial rather than a callback out of the
// server. And a second launch can tell that a desktop is already serving here
// and hand it the window, instead of racing for the port - which is also how
// Chromium's one-instance-per-profile rule is kept out of the way, since that
// hand-off would otherwise look to the launcher like a window that closed at
// once.

// PortFile is where one agent home remembers its desktop port.
func PortFile(home string) string {
	return filepath.Join(home, "desktop", "port")
}

// ReadPort answers the remembered port, or zero when this home has none. A
// file that cannot be parsed is treated as no file at all: the next launch
// writes a good one over it.
func ReadPort(home string) (int, error) {
	b, err := os.ReadFile(PortFile(home))
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("desktop: %s does not name a port", PortFile(home))
	}
	return port, nil
}

// WritePort records the port for the next launch.
func WritePort(home string, port int) error {
	path := PortFile(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(port)+"\n"), 0o600)
}

// loopbackAddr is the only address a desktop binds. The window is on this
// machine, so there is nothing to gain by listening anywhere else and a whole
// class of accidents to avoid.
func loopbackAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// portFree reports whether this machine will let us bind the port right now.
// Something can still take it between the answer and the pipeline's own bind;
// that race is narrow and loses nothing, because the pipeline then fails to
// start and says why.
func portFree(port int) bool {
	ln, err := net.Listen("tcp", loopbackAddr(port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// portFreeWithin waits a little for a port to come free.
//
// It is for the one case that would otherwise cost an operator their tabs:
// closing the window and starting the application again straight away. The
// desktop that is ending still holds the port for as long as its surface takes
// to stop, and a launch that gave up at the first refusal would take a fresh
// port - a new origin, and therefore no tabs, no seen tour and no drafts.
func portFreeWithin(port int, bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for {
		if portFree(port) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// portHandover is how long the remembered port is waited for.
const portHandover = 3 * time.Second

// freeLoopbackPort asks the operating system for one.
func freeLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// desktopServingAt reports whether a Coddy desktop is answering on the port.
// An open port is not enough to decide that - handing our window to a stranger
// is worse than picking another port - so it asks the shell, which answers
// with a header no other server sets.
func desktopServingAt(ctx context.Context, addr string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+shell.Prefix, nil)
	if err != nil {
		return false
	}
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode == http.StatusOK && res.Header.Get(shell.MarkerHeader) != ""
}

// waitForListener returns once the address accepts a connection, or when the
// pipeline behind it gives up first.
func waitForListener(ctx context.Context, addr string, serveErr <-chan error, bound time.Duration) error {
	deadline := time.Now().Add(bound)
	for {
		select {
		case err := <-serveErr:
			if err == nil {
				err = fmt.Errorf("it stopped before it was serving")
			}
			return fmt.Errorf("the server behind the window did not come up: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the server behind the window was not listening on %s within %s", addr, bound)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

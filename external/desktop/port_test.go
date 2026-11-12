//go:build desktop

package desktop

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestThePortSurvivesARelaunch(t *testing.T) {
	home := t.TempDir()
	if _, err := ReadPort(home); err == nil {
		t.Fatal("a home that never opened a window remembers a port")
	}
	first, err := choosePort(home, discardLog())
	if err != nil {
		t.Fatalf("choosePort: %v", err)
	}
	again, err := choosePort(home, discardLog())
	if err != nil {
		t.Fatalf("choosePort: %v", err)
	}
	if again != first {
		t.Fatalf("the second launch took port %d, not the remembered %d, so it would open with no tabs", again, first)
	}
}

// A port file left by an older version, a half-written file, a folder: none of
// them may stop a launch. The next launch writes a good one over it.
func TestAnUnreadablePortFileIsNoPortAtAll(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(PortFile(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"", "not a port", "0", "70000", "-1"} {
		if err := os.WriteFile(PortFile(home), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if port, err := ReadPort(home); err == nil {
			t.Fatalf("%q was read as port %d", content, port)
		}
		port, err := choosePort(home, discardLog())
		if err != nil {
			t.Fatalf("choosePort over %q: %v", content, err)
		}
		if port <= 0 {
			t.Fatalf("choosePort over %q answered %d", content, port)
		}
	}
}

// Something else on the remembered port costs this launch its tab list and
// nothing more: it takes another port and records it.
func TestAStrangerOnTheRememberedPortMovesTheDesktop(t *testing.T) {
	home := t.TempDir()
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	busy := taken.Addr().(*net.TCPAddr).Port
	if err := WritePort(home, busy); err != nil {
		t.Fatal(err)
	}

	port, err := choosePort(home, discardLog())
	if err != nil {
		t.Fatalf("choosePort: %v", err)
	}
	if port == busy {
		t.Fatalf("the desktop took port %d, which something else is holding", busy)
	}
	remembered, err := ReadPort(home)
	if err != nil || remembered != port {
		t.Fatalf("the home remembers %d (%v), not the port the launch took, %d", remembered, err, port)
	}
}

// An open port is not a Coddy desktop. Handing the window to whatever answers
// there would be worse than taking another port, so the probe asks the shell
// and reads the header no other server sets.
func TestOnlyTheShellIsTakenForADesktop(t *testing.T) {
	ctx := context.Background()

	stranger := httpStand(t, http.NotFoundHandler())
	if desktopServingAt(ctx, stranger) {
		t.Fatal("a server that is not Coddy was taken for a desktop")
	}

	mux := http.NewServeMux()
	mux.Handle(shell.Prefix, shell.Handler(nil))
	if addr := httpStand(t, mux); !desktopServingAt(ctx, addr) {
		t.Fatal("a Coddy desktop was not recognised as one")
	}
}

// httpStand serves h on loopback until the test ends, and answers its address.
func httpStand(t *testing.T, h http.Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
}

// A desktop that is still stopping holds the port its tabs are scoped to, and
// the launch that follows is usually the operator starting the application
// again - so the port is waited for rather than given away.
func TestTheRememberedPortIsWaitedForWhileItIsStillHeld(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	if portFreeWithin(port, 100*time.Millisecond) {
		t.Fatal("a port somebody is listening on was reported free")
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = ln.Close()
	}()
	if !portFreeWithin(port, 3*time.Second) {
		t.Fatal("the port was not taken back once the desktop before it let go")
	}
}

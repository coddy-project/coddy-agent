//go:build desktop

package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAWindowBackendThatDoesNotExistIsNamed(t *testing.T) {
	err := openWindow(context.Background(), Options{Window: "webview2"}, "http://127.0.0.1:1/desktop/")
	if err == nil {
		t.Fatal("an unknown --window value was accepted")
	}
	for _, want := range []string{"webview2", WindowAuto, WindowBrowser, WindowNative} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the message %q does not name %q", err, want)
		}
	}
}

// Asking for the native window in a build that has none is an error that says
// how to get one, not a run that opens nothing and waits.
func TestTheNativeWindowSaysWhichTagCarriesIt(t *testing.T) {
	if nativeAvailable {
		t.Skip("this build has the native window")
	}
	err := openWindow(context.Background(), Options{Window: WindowNative}, "http://127.0.0.1:1/desktop/")
	if err == nil || !strings.Contains(err.Error(), "desktop.native") {
		t.Fatalf("--window native answered %v, which does not name the tag that carries it", err)
	}
}

func TestRunRefusesAHomelessOrServerlessLaunch(t *testing.T) {
	if err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("a run with no agent home was accepted")
	}
	err := Run(context.Background(), Options{Home: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "serve") {
		t.Fatalf("a run with nothing to serve answered %v", err)
	}
}

// A machine with no window this run can wait on is not a failure: Coddy
// serves, says where, and lasts until the context ends.
func TestWithNoWindowTheRunServesAndSaysWhere(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := &strings.Builder{}
	served := make(chan error, 1)
	go func() {
		served <- finishWindow(Options{Out: out}, ErrNoWaitableWindow, "http://127.0.0.1:4321/desktop/", nil, ctx)
	}()
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("the run ended with %v", err)
	}
	if !strings.Contains(out.String(), "http://127.0.0.1:4321/desktop/") {
		t.Fatalf("the operator was not told where to look: %q", out)
	}
}

func TestAWindowThatFailedForAnyOtherReasonIsTheRunsError(t *testing.T) {
	boom := errors.New("the browser died")
	if err := finishWindow(Options{}, boom, "http://x/", nil, context.Background()); !errors.Is(err, boom) {
		t.Fatalf("finishWindow swallowed %v into %v", boom, err)
	}
}
